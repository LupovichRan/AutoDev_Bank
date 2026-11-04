package main

import (
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"errors"
	"fmt"
	"io/fs"
	"log/slog"
	"net"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"time"

	"github.com/LupovichRan/AutoDev_Bank/internal/approvals"
	"github.com/LupovichRan/AutoDev_Bank/internal/audit"
	"github.com/LupovichRan/AutoDev_Bank/internal/auth"
	"github.com/LupovichRan/AutoDev_Bank/internal/db"
	"github.com/LupovichRan/AutoDev_Bank/internal/httpapi"
	"github.com/LupovichRan/AutoDev_Bank/internal/ledger"
	"github.com/LupovichRan/AutoDev_Bank/internal/opendata"
	"github.com/LupovichRan/AutoDev_Bank/internal/opendata/providers"
	"github.com/LupovichRan/AutoDev_Bank/migrations"
	"github.com/LupovichRan/AutoDev_Bank/web"
)

// runServe migrates the database, starts the open-data refresher, the
// daily audit-root signer and the hourly expiry sweeps, and serves the API and web app on cfg.Addr until
// ctx is cancelled (SIGINT/SIGTERM), then shuts down gracefully.
func runServe(ctx context.Context, cfg Config, log *slog.Logger) error {
	signer, err := auditSignerFor(cfg)
	if err != nil {
		return err
	}
	if cfg.AuditSigningKey == "" {
		pub := signer.Public().(ed25519.PublicKey)
		fp := sha256.Sum256(pub)
		log.Warn("BANK_AUDIT_SIGNING_KEY is not set; using an ephemeral audit signing key (dev origin only). "+
			"Roots signed with it cannot be verified after a restart.",
			"public_key", base64.StdEncoding.EncodeToString(pub),
			"fingerprint", "sha256:"+hex.EncodeToString(fp[:8]))
	}

	pool, err := db.Connect(ctx, cfg.DatabaseURL)
	if err != nil {
		return err
	}
	defer pool.Close()
	applied, err := db.Migrate(ctx, pool, migrations.FS)
	if err != nil {
		return err
	}
	log.Info("migrations applied", "count", len(applied))

	origin, err := url.Parse(cfg.Origin)
	if err != nil || origin.Hostname() == "" {
		return fmt.Errorf("BANK_ORIGIN %q is not a valid origin", cfg.Origin)
	}

	rec := audit.New(pool, signer)
	led := ledger.New(pool)
	ap := approvals.New(pool, led, rec, approvals.DefaultPolicy())
	rateLimit, publicRate := 10, 60
	if cfg.DevTestLogin {
		rateLimit, publicRate = 600, 600
		log.Warn("BANK_DEV_TEST_LOGIN=1: POST /api/auth/dev-login signs anyone in; never enable this in production")
	}
	authSvc, err := auth.New(pool, auth.Config{
		RPID:               origin.Hostname(),
		RPDisplayName:      "AutoDev Bank",
		Origins:            []string{cfg.Origin},
		AllowTestLogin:     cfg.DevTestLogin,
		RateLimitPerMinute: rateLimit,
		Auditor:            httpapi.NewAuditor(rec),
		Logger:             log,
	})
	if err != nil {
		return err
	}

	client := &http.Client{Timeout: 2 * time.Minute} // honours HTTPS_PROXY
	ps := providers.NewHTTPProviders(client)
	if eb, ok := providers.EnableBankingFromEnv(client); ok {
		ps = append(ps, eb)
		log.Info("enable banking provider enabled")
	}
	refresher := opendata.NewRefresher(pool, ps, log)
	refresher.Fallback = providers.FixtureProviders(providers.Fixtures)
	refresher.UseFixtures = cfg.OpenDataFixture
	od := opendata.NewHandlers(opendata.NewStore(pool), refresher)
	od.Log = log

	static, err := fs.Sub(web.Dist, "dist")
	if err != nil {
		return err
	}

	handler := httpapi.New(httpapi.Deps{
		Pool: pool, Ledger: led, Audit: rec, Approvals: ap, Auth: authSvc,
		OpenData: od, Static: static, Log: log,
		Origin: cfg.Origin, DevTestLogin: cfg.DevTestLogin, PublicRateLimitPerMinute: publicRate,
	})

	// The background goroutines use pool; stop and wait for them before the
	// deferred pool.Close runs.
	ctx, cancel := context.WithCancel(ctx)
	var wg sync.WaitGroup
	defer func() {
		cancel()
		wg.Wait()
	}()
	wg.Add(3)
	go func() {
		defer wg.Done()
		refresher.Run(ctx, 6*time.Hour) // refreshes once now, then every 6 hours
	}()
	go func() {
		defer wg.Done()
		signDailyRoots(ctx, rec, log)
	}()
	go func() {
		defer wg.Done()
		runSweeps(ctx, time.Hour, log, sweep{"approvals", ap.ExpireApprovals}, sweep{"ledger holds", led.ExpireHolds})
	}()

	srv := &http.Server{
		Addr:              cfg.Addr,
		Handler:           handler,
		ReadHeaderTimeout: 10 * time.Second,
		ReadTimeout:       30 * time.Second,
		WriteTimeout:      2 * time.Minute, // a staff-triggered refresh can take a while
		IdleTimeout:       2 * time.Minute,
	}
	errc := make(chan error, 1)
	go func() {
		log.Info("serving", "addr", cfg.Addr, "origin", cfg.Origin)
		errc <- srv.ListenAndServe()
	}()

	select {
	case err := <-errc:
		return err
	case <-ctx.Done():
	}
	log.Info("shutting down")
	shutdownCtx, cancelShutdown := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancelShutdown()
	if err := srv.Shutdown(shutdownCtx); err != nil {
		return fmt.Errorf("shutdown: %w", err)
	}
	if err := <-errc; err != nil && !errors.Is(err, http.ErrServerClosed) {
		return err
	}
	return nil
}

// auditSignerFor returns the Ed25519 audit signing key: the 32-byte seed in
// BANK_AUDIT_SIGNING_KEY (64 hex characters) when set; otherwise a freshly
// generated ephemeral key, but only for a development origin (localhost,
// *.localhost or a loopback IP). Any other origin without a key is an error,
// so a public deployment never signs roots with a key nobody can keep.
func auditSignerFor(cfg Config) (ed25519.PrivateKey, error) {
	if cfg.AuditSigningKey != "" {
		seed, err := hex.DecodeString(cfg.AuditSigningKey)
		if err != nil || len(seed) != ed25519.SeedSize {
			return nil, errors.New("BANK_AUDIT_SIGNING_KEY must be 64 hex characters (a 32-byte Ed25519 seed)")
		}
		return ed25519.NewKeyFromSeed(seed), nil
	}
	if !isDevOrigin(cfg.Origin) {
		return nil, fmt.Errorf("BANK_AUDIT_SIGNING_KEY is required when BANK_ORIGIN (%q) is not localhost or a loopback address; "+
			"set it to 64 hex characters (a 32-byte Ed25519 seed), e.g. from `openssl rand -hex 32`", cfg.Origin)
	}
	_, priv, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		return nil, err
	}
	return priv, nil
}

// isDevOrigin reports whether origin's host is localhost, *.localhost or a
// loopback IP.
func isDevOrigin(origin string) bool {
	u, err := url.Parse(origin)
	if err != nil {
		return false
	}
	host := strings.ToLower(u.Hostname())
	if host == "localhost" || strings.HasSuffix(host, ".localhost") {
		return true
	}
	ip := net.ParseIP(host)
	return ip != nil && ip.IsLoopback()
}

// sweep is one periodic expiry job: run returns how many items it expired.
type sweep struct {
	name string
	run  func(context.Context) (int, error)
}

// runSweeps runs every sweep once now and then every interval until ctx is
// done: pending approvals past expires_at become expired (R3) and ledger
// holds past expires_at are voided (M9). A failing sweep is logged and
// retried at the next tick.
func runSweeps(ctx context.Context, interval time.Duration, log *slog.Logger, sweeps ...sweep) {
	t := time.NewTicker(interval)
	defer t.Stop()
	for {
		for _, s := range sweeps {
			n, err := s.run(ctx)
			if err != nil && ctx.Err() == nil {
				log.Error("expiry sweep failed", "sweep", s.name, "expired", n, "err", err)
			} else if n > 0 {
				log.Info("expiry sweep", "sweep", s.name, "expired", n)
			}
		}
		select {
		case <-ctx.Done():
			return
		case <-t.C:
		}
	}
}

// signDailyRoots stores yesterday's (UTC) audit root at startup if it is
// missing, then checks again every hour, until ctx is done. It never
// overwrites a stored root.
func signDailyRoots(ctx context.Context, rec *audit.Recorder, log *slog.Logger) {
	t := time.NewTicker(time.Hour)
	defer t.Stop()
	for {
		day := time.Now().UTC().AddDate(0, 0, -1)
		root, created, err := rec.SignDailyRootIfMissing(ctx, day)
		switch {
		case err != nil:
			if ctx.Err() == nil {
				log.Error("audit root signing failed", "day", day.Format("2006-01-02"), "err", err)
			}
		case created:
			log.Info("audit root signed", "day", root.Day, "head_seq", root.HeadSeq, "head_hash", root.HeadHash)
		}
		select {
		case <-ctx.Done():
			return
		case <-t.C:
		}
	}
}

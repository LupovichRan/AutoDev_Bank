package main

import (
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"io/fs"
	"log/slog"
	"net/http"
	"net/url"
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

// runServe migrates the database, starts the open-data refresher and the
// daily audit-root signer, and serves the API and web app on cfg.Addr until
// ctx is cancelled (SIGINT/SIGTERM), then shuts down gracefully.
func runServe(ctx context.Context, cfg Config, log *slog.Logger) error {
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

	signer, err := auditSigner(cfg.AuditSigningKey, log)
	if err != nil {
		return err
	}
	origin, err := url.Parse(cfg.Origin)
	if err != nil || origin.Hostname() == "" {
		return fmt.Errorf("BANK_ORIGIN %q is not a valid origin", cfg.Origin)
	}

	rec := audit.New(pool, signer)
	led := ledger.New(pool)
	ap := approvals.New(pool, led, rec, approvals.DefaultPolicy())
	rateLimit := 10
	if cfg.DevTestLogin {
		rateLimit = 600
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
	})

	ctx, cancel := context.WithCancel(ctx)
	defer cancel()
	go refresher.Run(ctx, 6*time.Hour) // refreshes once now, then every 6 hours
	go signDailyRoots(ctx, rec, log)

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

// auditSigner decodes BANK_AUDIT_SIGNING_KEY (hex 32-byte Ed25519 seed) or,
// when empty, generates a key and logs its seed so a developer can persist it.
func auditSigner(hexSeed string, log *slog.Logger) (ed25519.PrivateKey, error) {
	if hexSeed != "" {
		seed, err := hex.DecodeString(hexSeed)
		if err != nil || len(seed) != ed25519.SeedSize {
			return nil, errors.New("BANK_AUDIT_SIGNING_KEY must be 64 hex characters (a 32-byte Ed25519 seed)")
		}
		return ed25519.NewKeyFromSeed(seed), nil
	}
	seed := make([]byte, ed25519.SeedSize)
	if _, err := rand.Read(seed); err != nil {
		return nil, err
	}
	log.Warn("WARNING: BANK_AUDIT_SIGNING_KEY is not set; generated an ephemeral audit signing key. "+
		"Roots signed with it cannot be verified after a restart unless you persist it.",
		"BANK_AUDIT_SIGNING_KEY", hex.EncodeToString(seed))
	return ed25519.NewKeyFromSeed(seed), nil
}

// signDailyRoots signs yesterday's (UTC) audit root at startup and then
// every 24 hours until ctx is done.
func signDailyRoots(ctx context.Context, rec *audit.Recorder, log *slog.Logger) {
	t := time.NewTicker(24 * time.Hour)
	defer t.Stop()
	for {
		day := time.Now().UTC().AddDate(0, 0, -1)
		if root, err := rec.SignDailyRoot(ctx, day); err != nil {
			log.Error("audit root signing failed", "day", day.Format("2006-01-02"), "err", err)
		} else {
			log.Info("audit root signed", "day", root.Day, "head_seq", root.HeadSeq, "head_hash", root.HeadHash)
		}
		select {
		case <-ctx.Done():
			return
		case <-t.C:
		}
	}
}

package opendata

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

const fixtureSuffix = " (fixture)"

// Refresher fetches every provider/kind and stores good results as the new
// snapshot. On error or an empty result the previous snapshot is kept and
// the attempt is logged with ok=false.
type Refresher struct {
	pool      *pgxpool.Pool
	providers []Provider
	log       *slog.Logger

	// Fallback providers (matched by ID) answer from recorded fixtures when
	// UseFixtures is set (BANK_OPENDATA_FIXTURES=1) and a live fetch fails.
	// A fixture is stored only when no live snapshot exists yet, so it never
	// replaces real data; its source_url gets the suffix " (fixture)".
	Fallback    []Provider
	UseFixtures bool

	Timeout     time.Duration // per fetch; default 90s
	Concurrency int           // providers refreshed in parallel; default 4

	running sync.Mutex // held for the duration of RefreshAll
}

// ErrRefreshInProgress is returned by RefreshAll when another run is active.
var ErrRefreshInProgress = errors.New("opendata: a refresh is already in progress")

const maxErrText = 2000

func truncateErr(s string) string {
	if len(s) > maxErrText {
		s = s[:maxErrText]
	}
	return strings.ToValidUTF8(s, "?")
}

func NewRefresher(pool *pgxpool.Pool, providers []Provider, log *slog.Logger) *Refresher {
	if log == nil {
		log = slog.Default()
	}
	return &Refresher{pool: pool, providers: providers, log: log, Timeout: 90 * time.Second, Concurrency: 4}
}

// RefreshOutcome reports one provider/kind attempt. OK means the live fetch
// succeeded and its snapshot was stored; Fallback means a fixture was stored.
type RefreshOutcome struct {
	Provider   string    `json:"provider"`
	Kind       Kind      `json:"kind"`
	OK         bool      `json:"ok"`
	Fallback   bool      `json:"fallback,omitempty"`
	Error      string    `json:"error,omitempty"`
	Records    int       `json:"records"`
	FetchedAt  time.Time `json:"fetched_at"`
	DurationMS int64     `json:"duration_ms"`
}

// RefreshAll refreshes every provider/kind once and returns outcomes sorted
// by provider then kind. Runs never overlap: while one is in progress,
// RefreshAll returns ErrRefreshInProgress.
func (r *Refresher) RefreshAll(ctx context.Context) ([]RefreshOutcome, error) {
	if !r.running.TryLock() {
		return nil, ErrRefreshInProgress
	}
	defer r.running.Unlock()
	conc := max(r.Concurrency, 1)
	sem := make(chan struct{}, conc)
	var mu sync.Mutex
	var out []RefreshOutcome
	var wg sync.WaitGroup
	for _, p := range r.providers {
		wg.Add(1)
		go func() {
			defer wg.Done()
			sem <- struct{}{}
			defer func() { <-sem }()
			for _, k := range p.Kinds() {
				o := r.refreshOne(ctx, p, k)
				mu.Lock()
				out = append(out, o)
				mu.Unlock()
			}
		}()
	}
	wg.Wait()
	sort.Slice(out, func(i, j int) bool {
		if out[i].Provider != out[j].Provider {
			return out[i].Provider < out[j].Provider
		}
		return out[i].Kind < out[j].Kind
	})
	return out, nil
}

// Run refreshes immediately and then every interval until ctx is done.
func (r *Refresher) Run(ctx context.Context, every time.Duration) {
	if every <= 0 {
		every = time.Hour
	}
	t := time.NewTicker(every)
	defer t.Stop()
	for {
		if outs, err := r.RefreshAll(ctx); err != nil {
			r.log.Warn("opendata refresh skipped", "err", err)
		} else {
			for _, o := range outs {
				r.logOutcome(o)
			}
		}
		select {
		case <-ctx.Done():
			return
		case <-t.C:
		}
	}
}

func (r *Refresher) logOutcome(o RefreshOutcome) {
	attrs := []any{"provider", o.Provider, "kind", o.Kind, "records", o.Records, "duration_ms", o.DurationMS}
	switch {
	case o.OK:
		r.log.Info("opendata refreshed", attrs...)
	case o.Fallback:
		r.log.Warn("opendata fixture fallback", append(attrs, "err", o.Error)...)
	default:
		r.log.Warn("opendata refresh failed; previous snapshot kept", append(attrs, "err", o.Error)...)
	}
}

func (r *Refresher) fetch(ctx context.Context, p Provider, k Kind) (Result, error) {
	timeout := r.Timeout
	if timeout <= 0 {
		timeout = 90 * time.Second
	}
	fctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	res, err := p.Fetch(fctx, k)
	if err == nil && len(res.Records) == 0 {
		err = errors.New("no records in response")
	}
	return res, err
}

func (r *Refresher) refreshOne(ctx context.Context, p Provider, k Kind) RefreshOutcome {
	start := time.Now()
	o := RefreshOutcome{Provider: p.ID(), Kind: k, FetchedAt: start.UTC()}
	res, err := r.fetch(ctx, p, k)
	if err == nil {
		err = r.store(ctx, o, res, "", time.Since(start))
		if err == nil {
			o.OK, o.Records = true, len(res.Records)
			o.DurationMS = time.Since(start).Milliseconds()
			return o
		}
	}
	o.Error = err.Error()

	if fb := r.fallbackFor(p.ID()); fb != nil && ctx.Err() == nil {
		if live, lerr := r.hasLiveSnapshot(ctx, p.ID(), k); lerr == nil && !live {
			if fres, ferr := r.fetch(ctx, fb, k); ferr == nil {
				fres.SourceURL += fixtureSuffix
				msg := truncateErr("fixture fallback; live fetch failed: " + o.Error)
				if serr := r.store(ctx, o, fres, msg, time.Since(start)); serr == nil {
					o.Fallback, o.Records, o.Error = true, len(fres.Records), msg
					o.DurationMS = time.Since(start).Milliseconds()
					return o
				}
			}
		}
	}
	o.DurationMS = time.Since(start).Milliseconds()
	if ctx.Err() != nil {
		// Shutdown or cancelled request: not a provider failure, so leave no
		// ok=false row behind.
		o.Error = "cancelled: " + o.Error
		return o
	}
	r.logFailure(o)
	return o
}

func (r *Refresher) fallbackFor(id string) Provider {
	if !r.UseFixtures {
		return nil
	}
	for _, p := range r.Fallback {
		if p.ID() == id {
			return p
		}
	}
	return nil
}

func (r *Refresher) hasLiveSnapshot(ctx context.Context, provider string, k Kind) (bool, error) {
	var live bool
	err := r.pool.QueryRow(ctx, `select exists(select 1 from opendata.snapshots
		where provider=$1 and kind=$2 and source_url not like $3)`, provider, string(k), "%"+fixtureSuffix).Scan(&live)
	return live, err
}

// store upserts the snapshot and appends the log row in one transaction.
// A non-empty fallbackMsg marks a fixture snapshot (logged ok=false).
func (r *Refresher) store(ctx context.Context, o RefreshOutcome, res Result, fallbackMsg string, d time.Duration) error {
	for i := range res.Records {
		if res.Records[i].Kind == "" {
			res.Records[i].Kind = o.Kind
		}
	}
	payload, err := json.Marshal(res.Records)
	if err != nil {
		return fmt.Errorf("encode records: %w", err)
	}
	return pgx.BeginFunc(ctx, r.pool, func(tx pgx.Tx) error {
		if _, err := tx.Exec(ctx, `insert into opendata.snapshots (provider, kind, fetched_at, source_url, license, payload)
			values ($1, $2, $3, $4, $5, $6)
			on conflict (provider, kind) do update set fetched_at = excluded.fetched_at,
			  source_url = excluded.source_url, license = excluded.license, payload = excluded.payload`,
			o.Provider, string(o.Kind), o.FetchedAt, res.SourceURL, res.License, payload); err != nil {
			return fmt.Errorf("store snapshot: %w", err)
		}
		_, err := tx.Exec(ctx, `insert into opendata.refresh_log (provider, kind, at, ok, error, duration_ms, records)
			values ($1, $2, $3, $4, $5, $6, $7)`,
			o.Provider, string(o.Kind), o.FetchedAt, fallbackMsg == "", fallbackMsg, int(d.Milliseconds()), len(res.Records))
		return err
	})
}

func (r *Refresher) logFailure(o RefreshOutcome) {
	// Use a fresh context so a timed-out fetch still records why it failed (cancelled runs skip logging).
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	msg := truncateErr(o.Error)
	if _, err := r.pool.Exec(ctx, `insert into opendata.refresh_log (provider, kind, at, ok, error, duration_ms)
		values ($1, $2, $3, false, $4, $5)`, o.Provider, string(o.Kind), o.FetchedAt, msg, int(o.DurationMS)); err != nil {
		r.log.Error("opendata: write refresh_log", "provider", o.Provider, "kind", o.Kind, "err", err)
	}
}

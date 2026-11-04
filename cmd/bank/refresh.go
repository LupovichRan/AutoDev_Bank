package main

import (
	"context"
	"fmt"
	"log/slog"
	"net/http"
	"time"

	"github.com/LupovichRan/AutoDev_Bank/internal/db"
	"github.com/LupovichRan/AutoDev_Bank/internal/opendata"
	"github.com/LupovichRan/AutoDev_Bank/internal/opendata/providers"
)

// runRefresh fetches every open-data provider once, stores good snapshots
// and logs one line per provider/kind. It fails only when nothing at all
// could be stored (every provider failed, fixtures included).
func runRefresh(ctx context.Context, cfg Config, log *slog.Logger) error {
	pool, err := db.Connect(ctx, cfg.DatabaseURL)
	if err != nil {
		return err
	}
	defer pool.Close()

	// The default transport honours HTTPS_PROXY via http.ProxyFromEnvironment.
	client := &http.Client{Timeout: 2 * time.Minute}
	ps := providers.NewHTTPProviders(client)
	if eb, ok := providers.EnableBankingFromEnv(client); ok {
		ps = append(ps, eb)
		log.Info("enable banking provider enabled")
	}
	r := opendata.NewRefresher(pool, ps, log)
	r.Fallback = providers.FixtureProviders(providers.Fixtures)
	r.UseFixtures = cfg.OpenDataFixture

	outcomes, err := r.RefreshAll(ctx)
	if err != nil {
		return err
	}
	stored := 0
	for _, o := range outcomes {
		attrs := []any{"provider", o.Provider, "kind", o.Kind, "records", o.Records, "duration_ms", o.DurationMS}
		switch {
		case o.OK:
			stored++
			log.Info("opendata refreshed", attrs...)
		case o.Fallback:
			stored++
			log.Warn("opendata fixture fallback", append(attrs, "err", o.Error)...)
		default:
			log.Warn("opendata refresh failed; previous snapshot kept", append(attrs, "err", o.Error)...)
		}
	}
	log.Info("opendata refresh done", "stored", stored, "attempted", len(outcomes))
	if stored == 0 {
		return fmt.Errorf("opendata: all %d provider fetches failed", len(outcomes))
	}
	return nil
}

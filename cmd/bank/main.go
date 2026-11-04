// Command bank is the single AutoDev Bank binary: HTTP API, migrations,
// seed data and open-data refresh.
package main

import (
	"context"
	"fmt"
	"log/slog"
	"os"
	"os/signal"
	"syscall"

	"github.com/LupovichRan/AutoDev_Bank/internal/db"
	"github.com/LupovichRan/AutoDev_Bank/migrations"
)

// Config is read from the environment once at startup.
type Config struct {
	DatabaseURL     string // DATABASE_URL
	Addr            string // BANK_ADDR, default :8080
	Origin          string // BANK_ORIGIN, default http://localhost:8080
	AuditSigningKey string // BANK_AUDIT_SIGNING_KEY, hex Ed25519 seed (generated if empty)
	DevTestLogin    bool   // BANK_DEV_TEST_LOGIN=1 enables POST /api/auth/dev-login (tests only)
	OpenDataFixture bool   // BANK_OPENDATA_FIXTURES=1 serves recorded fixtures when upstreams fail
}

func loadConfig() Config {
	get := func(k, def string) string {
		if v := os.Getenv(k); v != "" {
			return v
		}
		return def
	}
	return Config{
		DatabaseURL:     get("DATABASE_URL", "postgres://postgres@localhost:55432/bank?sslmode=disable"),
		Addr:            get("BANK_ADDR", ":8080"),
		Origin:          get("BANK_ORIGIN", "http://localhost:8080"),
		AuditSigningKey: os.Getenv("BANK_AUDIT_SIGNING_KEY"),
		DevTestLogin:    os.Getenv("BANK_DEV_TEST_LOGIN") == "1",
		OpenDataFixture: os.Getenv("BANK_OPENDATA_FIXTURES") == "1",
	}
}

func main() {
	log := slog.New(slog.NewJSONHandler(os.Stderr, nil))
	slog.SetDefault(log)
	if len(os.Args) < 2 {
		usage()
		os.Exit(2)
	}
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	cfg := loadConfig()

	var err error
	switch os.Args[1] {
	case "migrate":
		err = runMigrate(ctx, cfg, log)
	case "serve":
		err = runServe(ctx, cfg, log)
	case "seed":
		err = runSeed(ctx, cfg, log)
	case "opendata-refresh":
		err = runRefresh(ctx, cfg, log)
	case "help", "-h", "--help":
		usage()
	default:
		usage()
		os.Exit(2)
	}
	if err != nil {
		log.Error("command failed", "cmd", os.Args[1], "err", err)
		os.Exit(1)
	}
}

func usage() {
	fmt.Fprintln(os.Stderr, `usage: bank <serve|migrate|seed|opendata-refresh>
env: DATABASE_URL BANK_ADDR BANK_ORIGIN BANK_AUDIT_SIGNING_KEY BANK_DEV_TEST_LOGIN BANK_OPENDATA_FIXTURES`)
}

func runMigrate(ctx context.Context, cfg Config, log *slog.Logger) error {
	pool, err := db.Connect(ctx, cfg.DatabaseURL)
	if err != nil {
		return err
	}
	defer pool.Close()
	applied, err := db.Migrate(ctx, pool, migrations.FS)
	if err != nil {
		return err
	}
	log.Info("migrations applied", "count", len(applied), "versions", applied)
	return nil
}

package db

import (
	"context"
	"os"
	"testing"
	"time"

	"github.com/LupovichRan/AutoDev_Bank/migrations"
	"github.com/jackc/pgx/v5/pgxpool"
)

// TestPool returns a migrated pool for integration tests, or skips the test
// when DATABASE_URL is unset. All tables in the ledger, control and opendata
// schemas are truncated when the test ends.
func TestPool(t testing.TB) *pgxpool.Pool {
	t.Helper()
	url := os.Getenv("DATABASE_URL")
	if url == "" {
		t.Skip("DATABASE_URL not set; skipping integration test")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	pool, err := Connect(ctx, url)
	if err != nil {
		t.Fatalf("connect: %v", err)
	}
	if _, err := Migrate(ctx, pool, migrations.FS); err != nil {
		pool.Close()
		t.Fatalf("migrate: %v", err)
	}
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cancel()
		TruncateAll(ctx, pool)
		pool.Close()
	})
	return pool
}

// TruncateAll empties every table in the application schemas (not
// schema_migrations). Used by tests and by the e2e harness reset.
func TruncateAll(ctx context.Context, pool *pgxpool.Pool) {
	_, _ = pool.Exec(ctx, `do $$
	declare r record;
	begin
	  for r in select n.nspname, c.relname from pg_class c join pg_namespace n on n.oid = c.relnamespace
	           where c.relkind = 'r' and n.nspname in ('ledger','control','opendata') loop
	    execute format('truncate table %I.%I restart identity cascade', r.nspname, r.relname);
	  end loop;
	end $$`)
}

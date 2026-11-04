package db

import (
	"context"
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
	"time"

	"github.com/LupovichRan/AutoDev_Bank/migrations"
	"github.com/jackc/pgx/v5/pgxpool"
)

// TestPool returns a migrated pool for integration tests, or skips the test
// when DATABASE_URL is unset. Because `go test ./...` runs packages in
// parallel, each test binary gets its own database (DATABASE_URL's database
// name suffixed with the package name, e.g. bank_t_ledger), created on
// demand. All tables in the ledger, control and opendata schemas are
// truncated when the test ends.
func TestPool(t testing.TB) *pgxpool.Pool {
	t.Helper()
	base := os.Getenv("DATABASE_URL")
	if base == "" {
		t.Skip("DATABASE_URL not set; skipping integration test")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	testURL, err := ensureTestDatabase(ctx, base)
	if err != nil {
		t.Fatalf("test database: %v", err)
	}
	pool, err := Connect(ctx, testURL)
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

var unsafeDBChars = regexp.MustCompile(`[^a-z0-9_]`)

// ensureTestDatabase creates (if needed) a per-test-binary database next to
// the one in baseURL and returns the URL pointing at it.
func ensureTestDatabase(ctx context.Context, baseURL string) (string, error) {
	u, err := url.Parse(baseURL)
	if err != nil {
		return "", err
	}
	pkg := strings.TrimSuffix(filepath.Base(os.Args[0]), ".test")
	pkg = unsafeDBChars.ReplaceAllString(strings.ToLower(pkg), "_")
	baseName := strings.Trim(u.Path, "/")
	if baseName == "" {
		baseName = "postgres"
	}
	name := fmt.Sprintf("%s_t_%s", baseName, pkg)
	if len(name) > 63 {
		name = name[:63]
	}
	admin, err := Connect(ctx, baseURL)
	if err != nil {
		return "", err
	}
	defer admin.Close()
	var exists bool
	if err := admin.QueryRow(ctx, `select exists(select 1 from pg_database where datname = $1)`, name).Scan(&exists); err != nil {
		return "", err
	}
	if !exists {
		if _, err := admin.Exec(ctx, fmt.Sprintf(`create database %q`, name)); err != nil && !strings.Contains(err.Error(), "already exists") {
			return "", err
		}
	}
	u.Path = "/" + name
	return u.String(), nil
}

package db

import (
	"context"
	"testing"

	"github.com/LupovichRan/AutoDev_Bank/internal/testutil"
	"github.com/LupovichRan/AutoDev_Bank/migrations"
)

func TestMigrate_AppliesOnceAndIsIdempotent(t *testing.T) {
	pool := TestPool(t) // already migrated
	ctx := context.Background()

	applied, err := Migrate(ctx, pool, migrations.FS)
	testutil.NoError(t, err)
	testutil.Len(t, applied, 0, "second run applies nothing")

	var n int
	testutil.NoError(t, pool.QueryRow(ctx, `select count(*) from public.schema_migrations`).Scan(&n))
	testutil.True(t, n >= 1, "at least the init migration is recorded")

	for _, schema := range []string{"ledger", "control", "opendata"} {
		var exists bool
		testutil.NoError(t, pool.QueryRow(ctx, `select exists(select 1 from pg_namespace where nspname=$1)`, schema).Scan(&exists))
		testutil.True(t, exists, "schema "+schema)
	}
}

package db

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"io/fs"
	"testing"
	"testing/fstest"

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

	// Every applied migration records the sha256 of its file.
	initSQL, err := fs.ReadFile(migrations.FS, "0001_init.sql")
	testutil.NoError(t, err)
	sum := sha256.Sum256(initSQL)
	var stored string
	testutil.NoError(t, pool.QueryRow(ctx, `select checksum from public.schema_migrations where version = '0001_init'`).Scan(&stored))
	testutil.Equal(t, hex.EncodeToString(sum[:]), stored)

	for _, schema := range []string{"ledger", "control", "opendata"} {
		var exists bool
		testutil.NoError(t, pool.QueryRow(ctx, `select exists(select 1 from pg_namespace where nspname=$1)`, schema).Scan(&exists))
		testutil.True(t, exists, "schema "+schema)
	}
}

// M8: an applied migration whose file changed afterwards stops the run.
func TestMigrate_FailsWhenAppliedFileChanged(t *testing.T) {
	pool := TestPool(t)
	ctx := context.Background()
	changed := fstest.MapFS{}
	entries, err := fs.ReadDir(migrations.FS, ".")
	testutil.NoError(t, err)
	for _, e := range entries {
		b, err := fs.ReadFile(migrations.FS, e.Name())
		testutil.NoError(t, err)
		changed[e.Name()] = &fstest.MapFile{Data: b}
	}
	changed["0001_init.sql"] = &fstest.MapFile{Data: append([]byte("-- edited\n"), changed["0001_init.sql"].Data...)}

	_, err = Migrate(ctx, pool, changed)
	testutil.ErrorContains(t, err, "migration 0001_init changed after being applied; recreate the database or restore the file")

	// The real files still pass.
	_, err = Migrate(ctx, pool, migrations.FS)
	testutil.NoError(t, err)
}

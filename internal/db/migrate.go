package db

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io/fs"
	"sort"
	"strings"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

const migrationsLock = 7311

// Migrate applies every *.sql file in src (lexical order) that is not yet
// recorded in public.schema_migrations, recording each file's sha256. The
// whole run is one transaction holding an advisory lock, so concurrent
// starters do not race. A recorded migration whose file now has a different
// checksum fails the run before anything is applied. Rows recorded before
// checksums existed are stamped with the current file's checksum once. It
// returns the versions applied in this call.
func Migrate(ctx context.Context, pool *pgxpool.Pool, src fs.FS) ([]string, error) {
	entries, err := fs.ReadDir(src, ".")
	if err != nil {
		return nil, fmt.Errorf("db: read migrations: %w", err)
	}
	var names []string
	for _, e := range entries {
		if !e.IsDir() && strings.HasSuffix(e.Name(), ".sql") {
			names = append(names, e.Name())
		}
	}
	sort.Strings(names)

	conn, err := pool.Acquire(ctx)
	if err != nil {
		return nil, err
	}
	defer conn.Release()

	if _, err := conn.Exec(ctx, `create table if not exists public.schema_migrations (
		version text primary key, applied_at timestamptz not null default now())`); err != nil {
		return nil, fmt.Errorf("db: ensure schema_migrations: %w", err)
	}

	var applied []string
	err = pgx.BeginFunc(ctx, conn, func(tx pgx.Tx) error {
		if _, err := tx.Exec(ctx, `select pg_advisory_xact_lock($1)`, migrationsLock); err != nil {
			return err
		}
		if _, err := tx.Exec(ctx, `alter table public.schema_migrations add column if not exists checksum text`); err != nil {
			return fmt.Errorf("db: ensure schema_migrations.checksum: %w", err)
		}
		done := map[string]*string{} // version -> recorded checksum (nil: recorded before checksums)
		rows, err := tx.Query(ctx, `select version, checksum from public.schema_migrations`)
		if err != nil {
			return err
		}
		for rows.Next() {
			var v string
			var sum *string
			if err := rows.Scan(&v, &sum); err != nil {
				rows.Close()
				return err
			}
			done[v] = sum
		}
		rows.Close()
		if err := rows.Err(); err != nil {
			return err
		}
		texts := make(map[string][]byte, len(names))
		sums := make(map[string]string, len(names))
		for _, name := range names {
			version := strings.TrimSuffix(name, ".sql")
			sqlText, err := fs.ReadFile(src, name)
			if err != nil {
				return err
			}
			h := sha256.Sum256(sqlText)
			texts[version], sums[version] = sqlText, hex.EncodeToString(h[:])
			if stored, ok := done[version]; ok && stored != nil && *stored != sums[version] {
				return fmt.Errorf("db: migration %s changed after being applied; recreate the database or restore the file "+
					"(recorded sha256 %s, file sha256 %s)", version, *stored, sums[version])
			}
		}
		for _, name := range names {
			version := strings.TrimSuffix(name, ".sql")
			if stored, ok := done[version]; ok {
				if stored == nil {
					if _, err := tx.Exec(ctx, `update public.schema_migrations set checksum = $2 where version = $1`,
						version, sums[version]); err != nil {
						return err
					}
				}
				continue
			}
			if _, err := tx.Exec(ctx, string(texts[version])); err != nil {
				return fmt.Errorf("db: migration %s: %w", name, err)
			}
			if _, err := tx.Exec(ctx, `insert into public.schema_migrations (version, checksum) values ($1, $2)`,
				version, sums[version]); err != nil {
				return err
			}
			applied = append(applied, version)
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	return applied, nil
}

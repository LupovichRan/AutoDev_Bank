package db

import (
	"context"
	"fmt"
	"io/fs"
	"sort"
	"strings"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

const migrationsLock = 7311

// Migrate applies every *.sql file in src (lexical order) that is not yet
// recorded in public.schema_migrations. Each file runs in its own
// transaction; the whole run holds an advisory lock so concurrent starters
// do not race. It returns the versions applied in this call.
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
		done := map[string]bool{}
		rows, err := tx.Query(ctx, `select version from public.schema_migrations`)
		if err != nil {
			return err
		}
		for rows.Next() {
			var v string
			if err := rows.Scan(&v); err != nil {
				rows.Close()
				return err
			}
			done[v] = true
		}
		rows.Close()
		for _, name := range names {
			version := strings.TrimSuffix(name, ".sql")
			if done[version] {
				continue
			}
			sqlText, err := fs.ReadFile(src, name)
			if err != nil {
				return err
			}
			if _, err := tx.Exec(ctx, string(sqlText)); err != nil {
				return fmt.Errorf("db: migration %s: %w", name, err)
			}
			if _, err := tx.Exec(ctx, `insert into public.schema_migrations (version) values ($1)`, version); err != nil {
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

package main

import (
	"context"
	"fmt"
	"log/slog"
	"os"
	"slices"

	"github.com/jackc/pgx/v5"

	"github.com/LupovichRan/AutoDev_Bank/internal/approvals"
	"github.com/LupovichRan/AutoDev_Bank/internal/audit"
	"github.com/LupovichRan/AutoDev_Bank/internal/db"
	"github.com/LupovichRan/AutoDev_Bank/internal/ledger"
	"github.com/LupovichRan/AutoDev_Bank/internal/seed"
)

// checkSeedAllowed refuses to seed demo principals (with well-known handles
// and funded accounts) unless BANK_ORIGIN is a development origin
// (localhost, *.localhost or a loopback IP) or --force is given.
func checkSeedAllowed(cfg Config, args []string) error {
	if isDevOrigin(cfg.Origin) || slices.Contains(args, "--force") {
		return nil
	}
	return fmt.Errorf("refusing to seed demo data: BANK_ORIGIN %q is not localhost or a loopback address; "+
		"pass --force to seed anyway", cfg.Origin)
}

// runSeed loads the demo principals and accounts (idempotent), audits
// seed.run and prints a summary of handles, account ids and balances. Run
// `bank migrate` first.
func runSeed(ctx context.Context, cfg Config, args []string, log *slog.Logger) error {
	if err := checkSeedAllowed(cfg, args); err != nil {
		return err
	}
	pool, err := db.Connect(ctx, cfg.DatabaseURL)
	if err != nil {
		return err
	}
	defer pool.Close()
	led := ledger.New(pool)
	// Seeding posts opening balances directly; the run itself is audited
	// below (an append-only recorder needs no signing key).
	ap := approvals.New(pool, led, nil, approvals.DefaultPolicy())
	res, err := seed.Run(ctx, pool, led, ap)
	if err != nil {
		return err
	}
	rec := audit.New(pool, nil)
	err = db.WithTx(ctx, pool, func(ctx context.Context, tx pgx.Tx) error {
		_, err := rec.Append(ctx, tx, audit.Event{Action: "seed.run", Subject: "seed", Payload: map[string]any{
			"principals": len(res.Principals), "accounts": len(res.Accounts), "origin": cfg.Origin,
			"forced": !isDevOrigin(cfg.Origin),
		}})
		return err
	})
	if err != nil {
		return fmt.Errorf("seed: audit: %w", err)
	}
	log.Info("seed complete", "principals", len(res.Principals), "accounts", len(res.Accounts))
	return res.WriteSummary(os.Stdout)
}

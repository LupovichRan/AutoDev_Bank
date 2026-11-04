package main

import (
	"context"
	"log/slog"
	"os"

	"github.com/LupovichRan/AutoDev_Bank/internal/approvals"
	"github.com/LupovichRan/AutoDev_Bank/internal/db"
	"github.com/LupovichRan/AutoDev_Bank/internal/ledger"
	"github.com/LupovichRan/AutoDev_Bank/internal/seed"
)

// runSeed loads the demo principals and accounts (idempotent) and prints a
// summary of handles, account ids and balances. Run `bank migrate` first.
func runSeed(ctx context.Context, cfg Config, log *slog.Logger) error {
	pool, err := db.Connect(ctx, cfg.DatabaseURL)
	if err != nil {
		return err
	}
	defer pool.Close()
	led := ledger.New(pool)
	// No audit recorder: seeding is a developer operation, not a customer action.
	ap := approvals.New(pool, led, nil, approvals.DefaultPolicy())
	res, err := seed.Run(ctx, pool, led, ap)
	if err != nil {
		return err
	}
	log.Info("seed complete", "principals", len(res.Principals), "accounts", len(res.Accounts))
	return res.WriteSummary(os.Stdout)
}

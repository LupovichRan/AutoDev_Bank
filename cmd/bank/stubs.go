package main

import (
	"context"
	"errors"
	"log/slog"
)

// These are filled in by later tasks (serve: Task 8, seed: Task 8, refresh: Task 5).

var errNotImplemented = errors.New("not implemented yet")

func runServe(ctx context.Context, cfg Config, log *slog.Logger) error   { return errNotImplemented }
func runSeed(ctx context.Context, cfg Config, log *slog.Logger) error    { return errNotImplemented }
func runRefresh(ctx context.Context, cfg Config, log *slog.Logger) error { return errNotImplemented }

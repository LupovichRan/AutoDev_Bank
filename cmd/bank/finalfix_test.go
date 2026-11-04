package main

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"sync/atomic"
	"testing"
	"time"

	"github.com/LupovichRan/AutoDev_Bank/internal/testutil"
)

// M4: demo data only goes into a development origin unless forced.
func TestCheckSeedAllowed(t *testing.T) {
	for _, origin := range []string{"http://localhost:8080", "http://127.0.0.1:18082", "http://bank.localhost"} {
		testutil.NoError(t, checkSeedAllowed(Config{Origin: origin}, nil), origin)
	}
	err := checkSeedAllowed(Config{Origin: "https://bank.example"}, nil)
	testutil.ErrorContains(t, err, "--force")
	testutil.NoError(t, checkSeedAllowed(Config{Origin: "https://bank.example"}, []string{"--force"}))
}

// M9 / R3: sweeps run at once, then on every tick, and a failing sweep does
// not stop the others.
func TestRunSweeps(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	var ok, bad atomic.Int32
	done := make(chan struct{})
	go func() {
		defer close(done)
		runSweeps(ctx, 10*time.Millisecond, slog.New(slog.NewTextHandler(io.Discard, nil)),
			sweep{"failing", func(context.Context) (int, error) { bad.Add(1); return 0, errors.New("boom") }},
			sweep{"ok", func(context.Context) (int, error) { ok.Add(1); return 1, nil }})
	}()
	deadline := time.Now().Add(5 * time.Second)
	for ok.Load() < 3 && time.Now().Before(deadline) {
		time.Sleep(5 * time.Millisecond)
	}
	cancel()
	<-done
	testutil.True(t, ok.Load() >= 3, "ok sweep ran repeatedly")
	testutil.True(t, bad.Load() >= 3, "failing sweep retried")
}

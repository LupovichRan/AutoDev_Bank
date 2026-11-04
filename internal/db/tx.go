package db

import (
	"context"
	"errors"
	"fmt"
	"math/rand/v2"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"
)

const (
	maxAttempts   = 5
	sqlSerialFail = "40001"
	sqlDeadlock   = "40P01"
)

// WithTx runs fn inside a SERIALIZABLE transaction and retries the whole
// function on serialization failures (SQLSTATE 40001) or deadlocks (40P01),
// up to five attempts with jittered backoff between 10 and 200 ms.
// fn must be idempotent with respect to side effects outside the database.
func WithTx(ctx context.Context, pool *pgxpool.Pool, fn func(ctx context.Context, tx pgx.Tx) error) error {
	var last error
	for attempt := 1; attempt <= maxAttempts; attempt++ {
		err := runOnce(ctx, pool, fn)
		if err == nil {
			return nil
		}
		if !IsRetryable(err) {
			return err
		}
		last = err
		if attempt < maxAttempts {
			sleep := time.Duration(10+rand.IntN(40*attempt)) * time.Millisecond
			if sleep > 200*time.Millisecond {
				sleep = 200 * time.Millisecond
			}
			select {
			case <-time.After(sleep):
			case <-ctx.Done():
				return ctx.Err()
			}
		}
	}
	return fmt.Errorf("db: transaction failed after %d attempts: %w", maxAttempts, last)
}

func runOnce(ctx context.Context, pool *pgxpool.Pool, fn func(context.Context, pgx.Tx) error) (err error) {
	tx, err := pool.BeginTx(ctx, pgx.TxOptions{IsoLevel: pgx.Serializable})
	if err != nil {
		return fmt.Errorf("db: begin: %w", err)
	}
	defer func() {
		if err != nil {
			_ = tx.Rollback(ctx)
		}
	}()
	if err = fn(ctx, tx); err != nil {
		return err
	}
	if err = tx.Commit(ctx); err != nil {
		return err
	}
	return nil
}

// IsRetryable reports whether err is a serialization failure or deadlock.
func IsRetryable(err error) bool {
	var pgErr *pgconn.PgError
	if errors.As(err, &pgErr) {
		return pgErr.Code == sqlSerialFail || pgErr.Code == sqlDeadlock
	}
	return false
}

// PgCode returns the SQLSTATE of err, or "" when err is not a PgError.
func PgCode(err error) string {
	var pgErr *pgconn.PgError
	if errors.As(err, &pgErr) {
		return pgErr.Code
	}
	return ""
}

// PgMessage returns the server message of err, or "" when err is not a PgError.
func PgMessage(err error) string {
	var pgErr *pgconn.PgError
	if errors.As(err, &pgErr) {
		return pgErr.Message
	}
	return ""
}

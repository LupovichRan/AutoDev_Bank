package db

import (
	"context"
	"sync"
	"testing"

	"github.com/LupovichRan/AutoDev_Bank/internal/testutil"
	"github.com/jackc/pgx/v5"
)

func TestWithTx_RetriesOnSerializationFailure(t *testing.T) {
	pool := TestPool(t)
	ctx := context.Background()
	_, err := pool.Exec(ctx, `create table if not exists control.tx_test (id int primary key, n int not null)`)
	testutil.NoError(t, err)
	_, err = pool.Exec(ctx, `insert into control.tx_test values (1, 0) on conflict (id) do update set n = 0`)
	testutil.NoError(t, err)

	const workers = 8
	var wg sync.WaitGroup
	errs := make(chan error, workers)
	for i := 0; i < workers; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			errs <- WithTx(ctx, pool, func(ctx context.Context, tx pgx.Tx) error {
				var n int
				if err := tx.QueryRow(ctx, `select n from control.tx_test where id = 1`).Scan(&n); err != nil {
					return err
				}
				_, err := tx.Exec(ctx, `update control.tx_test set n = $1 where id = 1`, n+1)
				return err
			})
		}()
	}
	wg.Wait()
	close(errs)
	for err := range errs {
		testutil.NoError(t, err)
	}
	var n int
	testutil.NoError(t, pool.QueryRow(ctx, `select n from control.tx_test where id = 1`).Scan(&n))
	testutil.Equal(t, workers, n, "every increment applied exactly once")
}

func TestIsRetryable(t *testing.T) {
	testutil.False(t, IsRetryable(context.Canceled))
	testutil.False(t, IsRetryable(nil))
}

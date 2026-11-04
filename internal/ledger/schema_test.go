package ledger

import (
	"context"
	"testing"
	"time"

	"github.com/LupovichRan/AutoDev_Bank/internal/db"
	"github.com/LupovichRan/AutoDev_Bank/internal/ids"
	"github.com/LupovichRan/AutoDev_Bank/internal/testutil"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

// Schema-level tests: the invariants must hold even when raw SQL bypasses the Go service.

func mkAccount(t *testing.T, pool *pgxpool.Pool, code, currency string, allowNegative bool) uuid.UUID {
	t.Helper()
	id := ids.New()
	_, err := pool.Exec(context.Background(),
		`insert into ledger.accounts (id, code, name, currency, kind, allow_negative) values ($1,$2,$2,$3,'asset',$4)`,
		id, code, currency, allowNegative)
	testutil.NoError(t, err)
	return id
}

func rawTx(ctx context.Context, tx pgx.Tx, key, status string, effective time.Time) (uuid.UUID, error) {
	id := ids.New()
	_, err := tx.Exec(ctx, `insert into ledger.transactions (id, idempotency_key, payload_hash, description, status, effective_date)
		values ($1,$2,'\x00'::bytea,$2,$3,$4)`, id, key, status, effective)
	return id, err
}

func rawEntry(ctx context.Context, tx pgx.Tx, txID, acct uuid.UUID, amount int64, currency string) error {
	_, err := tx.Exec(ctx, `insert into ledger.entries (id, transaction_id, account_id, amount, currency)
		values ($1,$2,$3,$4,$5)`, ids.New(), txID, acct, amount, currency)
	return err
}

func TestUnbalancedTransactionRejectedAtCommit(t *testing.T) {
	pool := db.TestPool(t)
	ctx := context.Background()
	a := mkAccount(t, pool, "a", "ILS", true)
	err := db.WithTx(ctx, pool, func(ctx context.Context, tx pgx.Tx) error {
		id, err := rawTx(ctx, tx, "k1", "posted", time.Now())
		if err != nil {
			return err
		}
		return rawEntry(ctx, tx, id, a, 100, "ILS") // single-sided: must fail at commit
	})
	testutil.ErrorContains(t, err, "ledger_unbalanced")
}

func TestBalancedTransactionCommitsAndUpdatesBalances(t *testing.T) {
	pool := db.TestPool(t)
	ctx := context.Background()
	a := mkAccount(t, pool, "a", "ILS", true)
	b := mkAccount(t, pool, "b", "ILS", false)
	err := db.WithTx(ctx, pool, func(ctx context.Context, tx pgx.Tx) error {
		id, err := rawTx(ctx, tx, "k1", "posted", time.Now())
		if err != nil {
			return err
		}
		if err := rawEntry(ctx, tx, id, a, -100, "ILS"); err != nil {
			return err
		}
		return rawEntry(ctx, tx, id, b, 100, "ILS")
	})
	testutil.NoError(t, err)
	var ba, bb int64
	var va int64
	testutil.NoError(t, pool.QueryRow(ctx, `select balance, version from ledger.accounts where id=$1`, a).Scan(&ba, &va))
	testutil.NoError(t, pool.QueryRow(ctx, `select balance from ledger.accounts where id=$1`, b).Scan(&bb))
	testutil.Equal(t, int64(-100), ba)
	testutil.Equal(t, int64(100), bb)
	testutil.Equal(t, int64(1), va)
	var after int64
	testutil.NoError(t, pool.QueryRow(ctx, `select balance_after from ledger.entries where account_id=$1`, b).Scan(&after))
	testutil.Equal(t, int64(100), after)
}

func TestEntriesAreImmutable(t *testing.T) {
	pool := db.TestPool(t)
	ctx := context.Background()
	a := mkAccount(t, pool, "a", "ILS", true)
	b := mkAccount(t, pool, "b", "ILS", true)
	testutil.NoError(t, db.WithTx(ctx, pool, func(ctx context.Context, tx pgx.Tx) error {
		id, _ := rawTx(ctx, tx, "k1", "posted", time.Now())
		_ = rawEntry(ctx, tx, id, a, -5, "ILS")
		return rawEntry(ctx, tx, id, b, 5, "ILS")
	}))
	_, err := pool.Exec(ctx, `update ledger.entries set amount = 6 where account_id=$1`, b)
	testutil.ErrorContains(t, err, "ledger_immutable")
	_, err = pool.Exec(ctx, `delete from ledger.entries where account_id=$1`, b)
	testutil.ErrorContains(t, err, "ledger_immutable")
}

func mkTx(t *testing.T, pool *pgxpool.Pool, key, status string, from, to uuid.UUID, amount int64) uuid.UUID {
	t.Helper()
	var id uuid.UUID
	testutil.NoError(t, db.WithTx(context.Background(), pool, func(ctx context.Context, tx pgx.Tx) (err error) {
		id, err = rawTx(ctx, tx, key, status, time.Now())
		if err != nil {
			return err
		}
		if err := rawEntry(ctx, tx, id, from, -amount, "ILS"); err != nil {
			return err
		}
		return rawEntry(ctx, tx, id, to, amount, "ILS")
	}))
	return id
}

func TestTransactionWithoutEntriesRejectedAtCommit(t *testing.T) {
	pool := db.TestPool(t)
	err := db.WithTx(context.Background(), pool, func(ctx context.Context, tx pgx.Tx) (err error) {
		_, err = rawTx(ctx, tx, "k1", "posted", time.Now())
		return err
	})
	testutil.ErrorContains(t, err, "ledger_unbalanced")
}

func TestPostedTransactionCannotChange(t *testing.T) {
	pool := db.TestPool(t)
	ctx := context.Background()
	a := mkAccount(t, pool, "a", "ILS", true)
	b := mkAccount(t, pool, "b", "ILS", true)
	id := mkTx(t, pool, "k1", "posted", a, b, 5)
	_, err := pool.Exec(ctx, `update ledger.transactions set description='x' where id=$1`, id)
	testutil.ErrorContains(t, err, "ledger_immutable")
	_, err = pool.Exec(ctx, `update ledger.transactions set status='voided' where id=$1`, id)
	testutil.ErrorContains(t, err, "ledger_immutable")
	_, err = pool.Exec(ctx, `delete from ledger.transactions where id=$1`, id)
	testutil.ErrorContains(t, err, "ledger_immutable")
}

func balances(t *testing.T, pool *pgxpool.Pool, id uuid.UUID) (balance, pendingDebits, pendingCredits, available int64) {
	t.Helper()
	testutil.NoError(t, pool.QueryRow(context.Background(),
		`select balance, pending_debits, pending_credits, available from ledger.available_balances where account_id=$1`, id).
		Scan(&balance, &pendingDebits, &pendingCredits, &available))
	return
}

func TestPendingTransactionResolvesExactlyOnce(t *testing.T) {
	pool := db.TestPool(t)
	ctx := context.Background()
	a := mkAccount(t, pool, "a", "ILS", true)
	b := mkAccount(t, pool, "b", "ILS", true)
	id := mkTx(t, pool, "k1", "pending", a, b, 5)
	_, err := pool.Exec(ctx, `update ledger.transactions set status='posted' where id=$1`, id)
	testutil.NoError(t, err)
	var resolved *time.Time
	testutil.NoError(t, pool.QueryRow(ctx, `select resolved_at from ledger.transactions where id=$1`, id).Scan(&resolved))
	testutil.True(t, resolved != nil, "resolved_at stamped")
	_, err = pool.Exec(ctx, `update ledger.transactions set status='voided' where id=$1`, id)
	testutil.ErrorContains(t, err, "ledger_immutable")
}

func TestPostingPendingMovesReservedFundsIntoBalance(t *testing.T) {
	pool := db.TestPool(t)
	ctx := context.Background()
	a := mkAccount(t, pool, "a", "ILS", true)
	b := mkAccount(t, pool, "b", "ILS", true)
	id := mkTx(t, pool, "hold", "pending", a, b, 300)
	bal, pd, _, avail := balances(t, pool, a)
	testutil.Equal(t, []int64{0, 300, -300}, []int64{bal, pd, avail})
	_, pdb, pcb, availb := balances(t, pool, b)
	testutil.Equal(t, []int64{0, 300, 0}, []int64{pdb, pcb, availb}, "pending credit is not spendable")

	_, err := pool.Exec(ctx, `update ledger.transactions set status='posted' where id=$1`, id)
	testutil.NoError(t, err)
	bal, pd, _, avail = balances(t, pool, a)
	testutil.Equal(t, []int64{-300, 0, -300}, []int64{bal, pd, avail})
	balb, _, pcb, availb := balances(t, pool, b)
	testutil.Equal(t, []int64{300, 0, 300}, []int64{balb, pcb, availb})
}

func TestVoidingPendingReleasesReservedFunds(t *testing.T) {
	pool := db.TestPool(t)
	ctx := context.Background()
	a := mkAccount(t, pool, "a", "ILS", true)
	b := mkAccount(t, pool, "b", "ILS", true)
	id := mkTx(t, pool, "hold", "pending", a, b, 300)
	_, err := pool.Exec(ctx, `update ledger.transactions set status='voided' where id=$1`, id)
	testutil.NoError(t, err)
	bal, pd, _, avail := balances(t, pool, a)
	testutil.Equal(t, []int64{0, 0, 0}, []int64{bal, pd, avail})
	balb, _, pcb, _ := balances(t, pool, b)
	testutil.Equal(t, []int64{0, 0}, []int64{balb, pcb})
}

func TestBalancesCannotBeEditedDirectly(t *testing.T) {
	pool := db.TestPool(t)
	a := mkAccount(t, pool, "a", "ILS", true)
	_, err := pool.Exec(context.Background(), `update ledger.accounts set balance = 999 where id=$1`, a)
	testutil.ErrorContains(t, err, "ledger_immutable")
}

func TestNegativeBalanceRejectedUnlessAllowed(t *testing.T) {
	pool := db.TestPool(t)
	ctx := context.Background()
	strict := mkAccount(t, pool, "strict", "ILS", false)
	sink := mkAccount(t, pool, "sink", "ILS", true)
	err := db.WithTx(ctx, pool, func(ctx context.Context, tx pgx.Tx) error {
		id, _ := rawTx(ctx, tx, "k1", "posted", time.Now())
		if err := rawEntry(ctx, tx, id, strict, -1, "ILS"); err != nil {
			return err
		}
		return rawEntry(ctx, tx, id, sink, 1, "ILS")
	})
	testutil.ErrorContains(t, err, "ledger_insufficient_funds")
}

func TestCurrencyMismatchRejected(t *testing.T) {
	pool := db.TestPool(t)
	ctx := context.Background()
	ils := mkAccount(t, pool, "ils", "ILS", true)
	eur := mkAccount(t, pool, "eur", "EUR", true)
	err := db.WithTx(ctx, pool, func(ctx context.Context, tx pgx.Tx) error {
		id, _ := rawTx(ctx, tx, "k1", "posted", time.Now())
		if err := rawEntry(ctx, tx, id, ils, -1, "ILS"); err != nil {
			return err
		}
		return rawEntry(ctx, tx, id, eur, 1, "ILS") // entry currency must equal account currency
	})
	testutil.ErrorContains(t, err, "ledger_currency_mismatch")
}

func TestPeriodCloseRejectsBackdatedEntry(t *testing.T) {
	pool := db.TestPool(t)
	ctx := context.Background()
	a := mkAccount(t, pool, "a", "ILS", true)
	b := mkAccount(t, pool, "b", "ILS", true)
	_, err := pool.Exec(ctx, `insert into ledger.period_closes (currency, closed_through) values ('ILS', current_date - 1)`)
	testutil.NoError(t, err)
	err = db.WithTx(ctx, pool, func(ctx context.Context, tx pgx.Tx) error {
		id, _ := rawTx(ctx, tx, "k1", "posted", time.Now().AddDate(0, 0, -3))
		if err := rawEntry(ctx, tx, id, a, -1, "ILS"); err != nil {
			return err
		}
		return rawEntry(ctx, tx, id, b, 1, "ILS")
	})
	testutil.ErrorContains(t, err, "ledger_period_closed")
	// today is after the close: allowed
	err = db.WithTx(ctx, pool, func(ctx context.Context, tx pgx.Tx) error {
		id, _ := rawTx(ctx, tx, "k2", "posted", time.Now())
		if err := rawEntry(ctx, tx, id, a, -1, "ILS"); err != nil {
			return err
		}
		return rawEntry(ctx, tx, id, b, 1, "ILS")
	})
	testutil.NoError(t, err)
}

func TestAvailableBalanceSubtractsPendingDebits(t *testing.T) {
	pool := db.TestPool(t)
	ctx := context.Background()
	a := mkAccount(t, pool, "a", "ILS", true)
	b := mkAccount(t, pool, "b", "ILS", true)
	testutil.NoError(t, db.WithTx(ctx, pool, func(ctx context.Context, tx pgx.Tx) error {
		id, _ := rawTx(ctx, tx, "fund", "posted", time.Now())
		_ = rawEntry(ctx, tx, id, b, -1000, "ILS")
		return rawEntry(ctx, tx, id, a, 1000, "ILS")
	}))
	testutil.NoError(t, db.WithTx(ctx, pool, func(ctx context.Context, tx pgx.Tx) error {
		id, _ := rawTx(ctx, tx, "hold", "pending", time.Now())
		_ = rawEntry(ctx, tx, id, a, -300, "ILS")
		return rawEntry(ctx, tx, id, b, 300, "ILS")
	}))
	var balance, pending, available int64
	testutil.NoError(t, pool.QueryRow(ctx,
		`select balance, pending_debits, available from ledger.available_balances where account_id=$1`, a).
		Scan(&balance, &pending, &available))
	// a hold reserves funds without moving the posted balance
	testutil.Equal(t, int64(1000), balance)
	testutil.Equal(t, int64(300), pending)
	testutil.Equal(t, int64(700), available)
}

// I1(a): a committed transaction (posted or pending) never takes more
// entries, even balanced ones inserted with raw SQL.
func TestCommittedTransactionRejectsLaterEntries(t *testing.T) {
	pool := db.TestPool(t)
	ctx := context.Background()
	a := mkAccount(t, pool, "a", "ILS", true)
	b := mkAccount(t, pool, "b", "ILS", true)
	for _, status := range []string{"posted", "pending"} {
		id := mkTx(t, pool, "k-"+status, status, a, b, 5)
		err := db.WithTx(ctx, pool, func(ctx context.Context, tx pgx.Tx) error {
			if err := rawEntry(ctx, tx, id, a, -1000, "ILS"); err != nil {
				return err
			}
			return rawEntry(ctx, tx, id, b, 1000, "ILS")
		})
		testutil.ErrorContains(t, err, "ledger_immutable", status)
	}
	bal, _, _, _ := balances(t, pool, b)
	testutil.Equal(t, int64(5), bal, "only the original posting moved money")

	// A posted transaction is stamped resolved when it is created.
	var created, resolved *time.Time
	testutil.NoError(t, pool.QueryRow(ctx, `select created_at, resolved_at from ledger.transactions
		where idempotency_key = 'k-posted'`).Scan(&created, &resolved))
	testutil.True(t, resolved != nil && resolved.Equal(*created), "posted: resolved_at = created_at")
}

// I1(b): posting a hold into a closed period is refused; voiding it (which
// moves no posted balance) stays possible so holds can still expire.
func TestResolvingHoldIntoClosedPeriodRejected(t *testing.T) {
	pool := db.TestPool(t)
	ctx := context.Background()
	a := mkAccount(t, pool, "a", "ILS", true)
	b := mkAccount(t, pool, "b", "ILS", true)
	mkHold := func(key string) uuid.UUID {
		var id uuid.UUID
		testutil.NoError(t, db.WithTx(ctx, pool, func(ctx context.Context, tx pgx.Tx) (err error) {
			id, err = rawTx(ctx, tx, key, "pending", time.Now().AddDate(0, 0, -3))
			if err != nil {
				return err
			}
			if err := rawEntry(ctx, tx, id, a, -7, "ILS"); err != nil {
				return err
			}
			return rawEntry(ctx, tx, id, b, 7, "ILS")
		}))
		return id
	}
	post, void := mkHold("hold-post"), mkHold("hold-void")
	_, err := pool.Exec(ctx, `insert into ledger.period_closes (currency, closed_through) values ('ILS', current_date - 1)`)
	testutil.NoError(t, err)

	_, err = pool.Exec(ctx, `update ledger.transactions set status='posted' where id=$1`, post)
	testutil.ErrorContains(t, err, "ledger_period_closed")
	_, err = pool.Exec(ctx, `update ledger.transactions set status='voided' where id=$1`, void)
	testutil.NoError(t, err)
	bal, pd, _, _ := balances(t, pool, a)
	testutil.Equal(t, []int64{0, 7}, []int64{bal, pd}, "the refused hold still reserves its funds")
}

// I1(c): only an account's name and owner may change after creation.
func TestAccountColumnsImmutableExceptNameAndOwner(t *testing.T) {
	pool := db.TestPool(t)
	ctx := context.Background()
	a := mkAccount(t, pool, "a", "ILS", false)
	for _, set := range []string{
		`allow_negative = true`,
		`code = 'renamed'`,
		`version = version + 1`,
	} {
		_, err := pool.Exec(ctx, `update ledger.accounts set `+set+` where id=$1`, a)
		testutil.ErrorContains(t, err, "ledger_immutable", set)
	}
	_, err := pool.Exec(ctx, `update ledger.accounts set name = 'New name', owner_id = $2 where id=$1`, a, ids.New())
	testutil.NoError(t, err)
}

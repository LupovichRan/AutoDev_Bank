package ledger

import (
	"context"
	"errors"
	"fmt"
	"math"
	"sync"
	"testing"
	"time"

	"github.com/LupovichRan/AutoDev_Bank/internal/db"
	"github.com/LupovichRan/AutoDev_Bank/internal/ids"
	"github.com/LupovichRan/AutoDev_Bank/internal/testutil"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
)

// Service-level tests: behaviour of the Go API on top of the SQL invariants.

func newService(t *testing.T) *Service {
	t.Helper()
	return New(db.TestPool(t))
}

// newAccount creates an account with a unique code.
func newAccount(t *testing.T, s *Service, kind, currency string, allowNegative bool) Account {
	t.Helper()
	code := kind + "-" + ids.New().String()
	a, err := s.CreateAccount(context.Background(), AccountSpec{
		Code: code, Name: code, Currency: currency, Kind: kind, AllowNegative: allowNegative,
	})
	testutil.NoError(t, err)
	return a
}

// fund moves amount from a fresh allow-negative equity account into to.
func fund(t *testing.T, s *Service, to Account, amount int64) {
	t.Helper()
	src := newAccount(t, s, "equity", to.Currency, true)
	_, err := s.Post(context.Background(), PostRequest{
		IdempotencyKey: "fund-" + ids.New().String(),
		Description:    "funding",
		EffectiveDate:  time.Now(),
		Currency:       to.Currency,
		Lines:          []Line{{src.ID, -amount}, {to.ID, amount}},
	})
	testutil.NoError(t, err)
}

func transfer(key string, from, to uuid.UUID, amount int64) PostRequest {
	return PostRequest{
		IdempotencyKey: key,
		Description:    "transfer",
		EffectiveDate:  time.Now(),
		Currency:       "ILS",
		Lines:          []Line{{from, -amount}, {to, amount}},
	}
}

func mustAccount(t *testing.T, s *Service, id uuid.UUID) Account {
	t.Helper()
	a, err := s.Account(context.Background(), id)
	testutil.NoError(t, err)
	return a
}

func TestPost_TransfersBetweenAccounts(t *testing.T) {
	s := newService(t)
	ctx := context.Background()
	a := newAccount(t, s, "liability", "ILS", false)
	b := newAccount(t, s, "liability", "ILS", false)
	fund(t, s, a, 1000)

	txn, err := s.Post(ctx, transfer("t1", a.ID, b.ID, 500))
	testutil.NoError(t, err)
	testutil.Equal(t, StatusPosted, txn.Status)
	testutil.Equal(t, "t1", txn.IdempotencyKey)
	testutil.Equal(t, "ILS", txn.Currency)
	testutil.Len(t, txn.Entries, 2)
	byAcct := map[uuid.UUID]Entry{}
	for _, e := range txn.Entries {
		testutil.Equal(t, txn.ID, e.TransactionID)
		testutil.Equal(t, "ILS", e.Currency)
		byAcct[e.AccountID] = e
	}
	testutil.Equal(t, int64(-500), byAcct[a.ID].Amount)
	testutil.Equal(t, int64(500), byAcct[a.ID].BalanceAfter)
	testutil.Equal(t, int64(500), byAcct[b.ID].Amount)
	testutil.Equal(t, int64(500), byAcct[b.ID].BalanceAfter)

	ga, gb := mustAccount(t, s, a.ID), mustAccount(t, s, b.ID)
	testutil.Equal(t, int64(500), ga.Balance)
	testutil.Equal(t, int64(500), ga.Available)
	testutil.Equal(t, int64(500), gb.Balance)

	es, err := s.Entries(ctx, b.ID, 10)
	testutil.NoError(t, err)
	testutil.Len(t, es, 1)
	testutil.Equal(t, int64(500), es[0].BalanceAfter)
	es, err = s.Entries(ctx, a.ID, 10)
	testutil.NoError(t, err)
	testutil.Len(t, es, 2)
	testutil.True(t, es[0].Seq > es[1].Seq, "entries newest first")
}

func TestPost_IdempotentReplayReturnsSameTransaction(t *testing.T) {
	s := newService(t)
	ctx := context.Background()
	a := newAccount(t, s, "liability", "ILS", false)
	b := newAccount(t, s, "liability", "ILS", false)
	fund(t, s, a, 1000)

	first, err := s.Post(ctx, transfer("dup", a.ID, b.ID, 300))
	testutil.NoError(t, err)
	// Line order does not change the payload hash.
	replay := transfer("dup", a.ID, b.ID, 300)
	replay.Lines[0], replay.Lines[1] = replay.Lines[1], replay.Lines[0]
	second, err := s.Post(ctx, replay)
	testutil.NoError(t, err)
	testutil.Equal(t, first.ID, second.ID)
	testutil.Len(t, second.Entries, 2)
	testutil.Equal(t, int64(700), mustAccount(t, s, a.ID).Balance)
	testutil.Equal(t, int64(300), mustAccount(t, s, b.ID).Balance)
	es, err := s.Entries(ctx, b.ID, 10)
	testutil.NoError(t, err)
	testutil.Len(t, es, 1)
}

func TestPost_IdempotencyKeyPayloadMismatch(t *testing.T) {
	s := newService(t)
	ctx := context.Background()
	a := newAccount(t, s, "liability", "ILS", false)
	b := newAccount(t, s, "liability", "ILS", false)
	fund(t, s, a, 1000)

	_, err := s.Post(ctx, transfer("dup", a.ID, b.ID, 300))
	testutil.NoError(t, err)
	_, err = s.Post(ctx, transfer("dup", a.ID, b.ID, 301))
	testutil.ErrorIs(t, err, ErrIdempotencyConflict)
	testutil.Equal(t, int64(700), mustAccount(t, s, a.ID).Balance)
}

func TestPost_RejectsCurrencyMismatch(t *testing.T) {
	s := newService(t)
	ctx := context.Background()
	a := newAccount(t, s, "liability", "USD", true)
	b := newAccount(t, s, "liability", "USD", true)
	_, err := s.Post(ctx, transfer("fx", a.ID, b.ID, 100)) // request is ILS
	testutil.ErrorIs(t, err, ErrCurrencyMismatch)
}

func TestPost_RejectsUnbalanced(t *testing.T) {
	s := newService(t)
	ctx := context.Background()
	a := newAccount(t, s, "liability", "ILS", true)
	b := newAccount(t, s, "liability", "ILS", true)
	req := transfer("ub", a.ID, b.ID, 100)
	req.Lines[1].Amount = 99
	_, err := s.Post(ctx, req)
	testutil.ErrorIs(t, err, ErrUnbalanced)
	req.Lines = req.Lines[:1]
	_, err = s.Post(ctx, req)
	testutil.ErrorIs(t, err, ErrUnbalanced)
}

func TestPost_UnknownAccount(t *testing.T) {
	s := newService(t)
	a := newAccount(t, s, "liability", "ILS", true)
	_, err := s.Post(context.Background(), transfer("nf", a.ID, ids.New(), 100))
	testutil.ErrorIs(t, err, ErrNotFound)
}

func TestPost_InsufficientFunds(t *testing.T) {
	s := newService(t)
	ctx := context.Background()
	a := newAccount(t, s, "liability", "ILS", false)
	b := newAccount(t, s, "liability", "ILS", false)
	fund(t, s, a, 100)
	_, err := s.Post(ctx, transfer("big", a.ID, b.ID, 101))
	testutil.ErrorIs(t, err, ErrInsufficientFunds)
	testutil.Equal(t, int64(100), mustAccount(t, s, a.ID).Balance)
	// The key was not consumed by the failed attempt.
	_, err = s.Post(ctx, transfer("big", a.ID, b.ID, 100))
	testutil.NoError(t, err)
}

func TestHold_ReducesAvailableThenPostOrVoid(t *testing.T) {
	s := newService(t)
	ctx := context.Background()
	a := newAccount(t, s, "liability", "ILS", false)
	b := newAccount(t, s, "liability", "ILS", false)
	fund(t, s, a, 1000)

	hold := transfer("h1", a.ID, b.ID, 300)
	hold.Pending = true
	h1, err := s.Post(ctx, hold)
	testutil.NoError(t, err)
	testutil.Equal(t, StatusPending, h1.Status)
	ga := mustAccount(t, s, a.ID)
	testutil.Equal(t, int64(1000), ga.Balance)
	testutil.Equal(t, int64(300), ga.PendingDebits)
	testutil.Equal(t, int64(700), ga.Available)
	gb := mustAccount(t, s, b.ID)
	testutil.Equal(t, int64(0), gb.Balance)
	testutil.Equal(t, int64(300), gb.PendingCredits)

	// Held funds cannot be spent twice.
	_, err = s.Post(ctx, transfer("over", a.ID, b.ID, 701))
	testutil.ErrorIs(t, err, ErrInsufficientFunds)

	posted, err := s.Resolve(ctx, h1.ID, true)
	testutil.NoError(t, err)
	testutil.Equal(t, StatusPosted, posted.Status)
	testutil.True(t, posted.ResolvedAt != nil, "resolved_at set")
	ga, gb = mustAccount(t, s, a.ID), mustAccount(t, s, b.ID)
	testutil.Equal(t, int64(700), ga.Balance)
	testutil.Equal(t, int64(0), ga.PendingDebits)
	testutil.Equal(t, int64(300), gb.Balance)
	testutil.Equal(t, int64(0), gb.PendingCredits)

	hold2 := transfer("h2", a.ID, b.ID, 200)
	hold2.Pending = true
	h2, err := s.Post(ctx, hold2)
	testutil.NoError(t, err)
	testutil.Equal(t, int64(500), mustAccount(t, s, a.ID).Available)
	voided, err := s.Resolve(ctx, h2.ID, false)
	testutil.NoError(t, err)
	testutil.Equal(t, StatusVoided, voided.Status)
	ga, gb = mustAccount(t, s, a.ID), mustAccount(t, s, b.ID)
	testutil.Equal(t, int64(700), ga.Balance)
	testutil.Equal(t, int64(700), ga.Available)
	testutil.Equal(t, int64(300), gb.Balance)
	testutil.Equal(t, int64(0), gb.PendingCredits)
}

func TestResolve_Twice_ErrAlreadyResolved(t *testing.T) {
	s := newService(t)
	ctx := context.Background()
	a := newAccount(t, s, "liability", "ILS", false)
	b := newAccount(t, s, "liability", "ILS", false)
	fund(t, s, a, 1000)
	hold := transfer("h1", a.ID, b.ID, 300)
	hold.Pending = true
	h, err := s.Post(ctx, hold)
	testutil.NoError(t, err)
	_, err = s.Resolve(ctx, h.ID, true)
	testutil.NoError(t, err)
	_, err = s.Resolve(ctx, h.ID, true)
	testutil.ErrorIs(t, err, ErrAlreadyResolved)
	_, err = s.Resolve(ctx, h.ID, false)
	testutil.ErrorIs(t, err, ErrAlreadyResolved)
	testutil.Equal(t, int64(300), mustAccount(t, s, b.ID).Balance)

	_, err = s.Resolve(ctx, ids.New(), true)
	testutil.ErrorIs(t, err, ErrNotFound)
}

func TestResolve_PostExpiredHold_ErrExpired(t *testing.T) {
	s := newService(t)
	ctx := context.Background()
	a := newAccount(t, s, "liability", "ILS", false)
	b := newAccount(t, s, "liability", "ILS", false)
	fund(t, s, a, 1000)
	past := time.Now().Add(-time.Minute)
	hold := transfer("h1", a.ID, b.ID, 300)
	hold.Pending, hold.ExpiresAt = true, &past
	h, err := s.Post(ctx, hold)
	testutil.NoError(t, err)
	_, err = s.Resolve(ctx, h.ID, true)
	testutil.ErrorIs(t, err, ErrExpired)
	v, err := s.Resolve(ctx, h.ID, false)
	testutil.NoError(t, err)
	testutil.Equal(t, StatusVoided, v.Status)
	testutil.Equal(t, int64(1000), mustAccount(t, s, a.ID).Available)
}

func TestExpireHolds_VoidsOnlyExpired(t *testing.T) {
	s := newService(t)
	ctx := context.Background()
	a := newAccount(t, s, "liability", "ILS", false)
	b := newAccount(t, s, "liability", "ILS", false)
	fund(t, s, a, 1000)
	past, future := time.Now().Add(-time.Minute), time.Now().Add(time.Hour)

	mk := func(key string, amount int64, exp *time.Time) Transaction {
		r := transfer(key, a.ID, b.ID, amount)
		r.Pending, r.ExpiresAt = true, exp
		txn, err := s.Post(ctx, r)
		testutil.NoError(t, err)
		return txn
	}
	expired := mk("expired", 100, &past)
	live := mk("live", 200, &future)
	open := mk("open", 300, nil)

	n, err := s.ExpireHolds(ctx)
	testutil.NoError(t, err)
	testutil.Equal(t, 1, n)
	n, err = s.ExpireHolds(ctx)
	testutil.NoError(t, err)
	testutil.Equal(t, 0, n)

	for id, want := range map[uuid.UUID]string{expired.ID: StatusVoided, live.ID: StatusPending, open.ID: StatusPending} {
		txn, err := s.Transaction(ctx, id)
		testutil.NoError(t, err)
		testutil.Equal(t, want, txn.Status)
	}
	ga := mustAccount(t, s, a.ID)
	testutil.Equal(t, int64(500), ga.PendingDebits)
	testutil.Equal(t, int64(500), ga.Available)
}

func TestClosePeriod_RejectsBackdated(t *testing.T) {
	s := newService(t)
	ctx := context.Background()
	a := newAccount(t, s, "liability", "ILS", false)
	b := newAccount(t, s, "liability", "ILS", false)
	fund(t, s, a, 1000)
	yesterday := time.Now().AddDate(0, 0, -1)
	testutil.NoError(t, s.ClosePeriod(ctx, "ILS", yesterday))

	back := transfer("back", a.ID, b.ID, 100)
	back.EffectiveDate = yesterday
	_, err := s.Post(ctx, back)
	testutil.ErrorIs(t, err, ErrPeriodClosed)

	// Closing an earlier date never reopens the period.
	testutil.NoError(t, s.ClosePeriod(ctx, "ILS", yesterday.AddDate(0, 0, -5)))
	_, err = s.Post(ctx, back)
	testutil.ErrorIs(t, err, ErrPeriodClosed)

	_, err = s.Post(ctx, transfer("today", a.ID, b.ID, 100))
	testutil.NoError(t, err)
	// Other currencies are unaffected.
	u1 := newAccount(t, s, "liability", "USD", true)
	u2 := newAccount(t, s, "liability", "USD", true)
	usd := transfer("usd", u1.ID, u2.ID, 5)
	usd.Currency, usd.EffectiveDate = "USD", yesterday
	_, err = s.Post(ctx, usd)
	testutil.NoError(t, err)
}

func TestAccountsByOwner(t *testing.T) {
	s := newService(t)
	ctx := context.Background()
	owner := ids.New()
	for i := range 2 {
		code := fmt.Sprintf("own-%d-%s", i, ids.New())
		_, err := s.CreateAccount(ctx, AccountSpec{Code: code, Name: code, Currency: "ILS", Kind: "liability", OwnerID: &owner})
		testutil.NoError(t, err)
	}
	newAccount(t, s, "liability", "ILS", false) // not owned
	got, err := s.AccountsByOwner(ctx, owner)
	testutil.NoError(t, err)
	testutil.Len(t, got, 2)
	for _, a := range got {
		testutil.True(t, a.OwnerID != nil && *a.OwnerID == owner)
	}
	_, err = s.Account(ctx, ids.New())
	testutil.ErrorIs(t, err, ErrNotFound)
}

// TestPost_ConcurrentHotAccount drains one under-funded account from 48
// goroutines (480 transfers, funded for 470) through four independent Service
// instances (no shared in-process state, like four bank processes) and through
// PostTx inside caller-owned READ COMMITTED transactions (the approvals path).
// The database alone must serialise the debits: exactly 10 transfers fail with
// ErrInsufficientFunds, nothing else fails, the hot account ends at exactly 0
// and is never negative, and money is conserved.
func TestPost_ConcurrentHotAccount(t *testing.T) {
	pool := db.TestPool(t)
	ctx := context.Background()
	const workers, perWorker, amount, short = 48, 10, 3, 10
	services := []*Service{New(pool), New(pool), New(pool), New(pool)}
	s := services[0]
	hot := newAccount(t, s, "liability", "ILS", false)
	fund(t, s, hot, (workers*perWorker-short)*amount)
	var dests []Account
	for range 5 {
		dests = append(dests, newAccount(t, s, "liability", "ILS", false))
	}
	all := append([]Account{hot}, dests...)
	before := totalBalance(t, s, all)

	var wg sync.WaitGroup
	errs := make(chan error, workers*perWorker)
	for w := range workers {
		wg.Add(1)
		go func() {
			defer wg.Done()
			svc := services[w%len(services)]
			for i := range perWorker {
				req := transfer(fmt.Sprintf("hot-%d-%d", w, i), hot.ID, dests[(w+i)%len(dests)].ID, amount)
				var err error
				if (w+i)%2 == 0 {
					_, err = svc.Post(ctx, req)
				} else {
					err = db.WithTxLevel(ctx, pool, pgx.ReadCommitted, func(ctx context.Context, tx pgx.Tx) error {
						_, err := svc.PostTx(ctx, tx, req)
						return err
					})
					err = MapError(err)
				}
				errs <- err
			}
		}()
	}
	wg.Wait()
	close(errs)
	insufficient := 0
	for err := range errs {
		switch {
		case err == nil:
		case errors.Is(err, ErrInsufficientFunds):
			insufficient++
		default:
			t.Errorf("transfer failed: %v", err)
		}
	}
	testutil.Equal(t, short, insufficient, "ErrInsufficientFunds count")

	testutil.Equal(t, before, totalBalance(t, s, all), "sum of balances unchanged")
	testutil.Equal(t, int64(0), mustAccount(t, s, hot.ID).Balance)
	var negative int
	testutil.NoError(t, pool.QueryRow(ctx, `select count(*) from ledger.available_balances
		where available < 0 and account_id = any($1)`, []uuid.UUID{hot.ID}).Scan(&negative))
	testutil.Equal(t, 0, negative, "available never negative now")
	var minAfter int64
	testutil.NoError(t, pool.QueryRow(ctx, `select coalesce(min(balance_after), 0) from ledger.entries
		where account_id = $1`, hot.ID).Scan(&minAfter))
	testutil.True(t, minAfter >= 0, "available never negative after any entry")
	var n int
	testutil.NoError(t, pool.QueryRow(ctx, `select count(*) from ledger.entries
		where account_id = $1 and amount < 0`, hot.ID).Scan(&n))
	testutil.Equal(t, workers*perWorker-short, n, "successful debits")
}

func totalBalance(t *testing.T, s *Service, accts []Account) int64 {
	t.Helper()
	var sum int64
	for _, a := range accts {
		sum += mustAccount(t, s, a.ID).Balance
	}
	return sum
}

func TestPost_CreditsAppliedBeforeDebits(t *testing.T) {
	s := newService(t)
	a := newAccount(t, s, "liability", "ILS", false)
	b := newAccount(t, s, "liability", "ILS", false)
	fund(t, s, a, 100)
	// In request order A would dip to -500 before its credit arrives.
	txn, err := s.Post(context.Background(), PostRequest{
		IdempotencyKey: "net", Currency: "ILS",
		Lines: []Line{{a.ID, -600}, {b.ID, 50}, {a.ID, 550}},
	})
	testutil.NoError(t, err)
	testutil.Len(t, txn.Entries, 3)
	testutil.Equal(t, int64(50), mustAccount(t, s, a.ID).Balance)
	testutil.Equal(t, int64(50), mustAccount(t, s, b.ID).Balance)
}

func TestPost_RejectsHugeAmountsAndMapsOverflow(t *testing.T) {
	s := newService(t)
	ctx := context.Background()
	src := newAccount(t, s, "equity", "ILS", true)
	dst := newAccount(t, s, "liability", "ILS", true)
	for _, amt := range []int64{math.MinInt64, -(math.MaxInt64/2 + 1), math.MaxInt64/2 + 1} {
		_, err := s.Post(ctx, PostRequest{IdempotencyKey: fmt.Sprint("huge", amt), Currency: "ILS",
			Lines: []Line{{src.ID, amt}, {dst.ID, -amt}}})
		testutil.ErrorIs(t, err, ErrInvalidRequest)
	}
	// Each posting is within bounds, but the third overflows dst's bigint balance.
	var err error
	for i := range 3 {
		_, err = s.Post(ctx, PostRequest{IdempotencyKey: fmt.Sprint("big", i), Currency: "ILS",
			Lines: []Line{{src.ID, -math.MaxInt64 / 2}, {dst.ID, math.MaxInt64 / 2}}})
		if i < 2 {
			testutil.NoError(t, err)
		}
	}
	testutil.ErrorIs(t, err, ErrInvalidRequest)
	testutil.Equal(t, "22003", db.PgCode(err))
}

func TestDates_AreUTCCalendarDates(t *testing.T) {
	s := newService(t)
	ctx := context.Background()
	a := newAccount(t, s, "liability", "ILS", true)
	b := newAccount(t, s, "liability", "ILS", true)
	plus5 := time.FixedZone("UTC+5", 5*3600)
	// 2026-01-05 01:30 at +05:00 is 2026-01-04 20:30 UTC.
	local := time.Date(2026, 1, 5, 1, 30, 0, 0, plus5)
	req := transfer("tz", a.ID, b.ID, 10)
	req.EffectiveDate = local
	txn, err := s.Post(ctx, req)
	testutil.NoError(t, err)
	testutil.Equal(t, "2026-01-04", txn.EffectiveDate.UTC().Format("2006-01-02"))
	// The same instant written in UTC is the same payload.
	req.EffectiveDate = local.UTC()
	again, err := s.Post(ctx, req)
	testutil.NoError(t, err)
	testutil.Equal(t, txn.ID, again.ID)

	// ClosePeriod uses the same convention: closing through `local` closes 2026-01-04 only.
	testutil.NoError(t, s.ClosePeriod(ctx, "ILS", local))
	back := transfer("tz-back", a.ID, b.ID, 10)
	back.EffectiveDate = time.Date(2026, 1, 4, 12, 0, 0, 0, time.UTC)
	_, err = s.Post(ctx, back)
	testutil.ErrorIs(t, err, ErrPeriodClosed)
	next := transfer("tz-next", a.ID, b.ID, 10)
	next.EffectiveDate = time.Date(2026, 1, 5, 0, 0, 0, 0, time.UTC)
	_, err = s.Post(ctx, next)
	testutil.NoError(t, err)
}

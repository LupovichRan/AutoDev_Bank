package ledger

import (
	"context"
	"fmt"
	"sync"
	"testing"
	"time"

	"github.com/LupovichRan/AutoDev_Bank/internal/db"
	"github.com/LupovichRan/AutoDev_Bank/internal/ids"
	"github.com/LupovichRan/AutoDev_Bank/internal/testutil"
	"github.com/google/uuid"
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

func TestPost_ConcurrentHotAccount(t *testing.T) {
	s := newService(t)
	ctx := context.Background()
	const workers, perWorker, amount = 50, 10, 3
	hot := newAccount(t, s, "liability", "ILS", false)
	fund(t, s, hot, workers*perWorker*amount)
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
			for i := range perWorker {
				to := dests[(w+i)%len(dests)]
				_, err := s.Post(ctx, transfer(fmt.Sprintf("hot-%d-%d", w, i), hot.ID, to.ID, amount))
				if err != nil {
					errs <- err
				}
			}
		}()
	}
	wg.Wait()
	close(errs)
	for err := range errs {
		t.Errorf("transfer failed: %v", err)
	}

	testutil.Equal(t, before, totalBalance(t, s, all))
	for _, a := range all {
		testutil.True(t, mustAccount(t, s, a.ID).Balance >= 0, "no negative balance")
	}
	testutil.Equal(t, int64(0), mustAccount(t, s, hot.ID).Balance)
}

func totalBalance(t *testing.T, s *Service, accts []Account) int64 {
	t.Helper()
	var sum int64
	for _, a := range accts {
		sum += mustAccount(t, s, a.ID).Balance
	}
	return sum
}

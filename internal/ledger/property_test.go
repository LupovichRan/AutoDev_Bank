package ledger

import (
	"context"
	"fmt"
	"math/rand/v2"
	"testing"
	"time"

	"github.com/LupovichRan/AutoDev_Bank/internal/db"
	"github.com/LupovichRan/AutoDev_Bank/internal/testutil"
)

// TestProperty_SumPerCurrencyIsZero posts 200 random balanced multi-line
// transactions across 8 accounts in 2 currencies (a mix of posted ones and
// pending holds that are later posted or voided) and checks after every step
// that the journal and the account balances each sum to zero per currency.
func TestProperty_SumPerCurrencyIsZero(t *testing.T) {
	pool := db.TestPool(t)
	s := New(pool)
	ctx := context.Background()
	seed := uint64(time.Now().UnixNano())
	t.Logf("seed %d", seed)
	rng := rand.New(rand.NewPCG(seed, 2026))

	currencies := []string{"ILS", "USD"}
	accts := map[string][]Account{}
	for i := range 8 {
		cur := currencies[i%2]
		accts[cur] = append(accts[cur], newAccount(t, s, "asset", cur, true))
	}

	check := func(step int) {
		t.Helper()
		rows, err := pool.Query(ctx, `select currency, sum(amount)::bigint from ledger.entries group by currency`)
		testutil.NoError(t, err)
		for rows.Next() {
			var cur string
			var sum int64
			testutil.NoError(t, rows.Scan(&cur, &sum))
			testutil.Equal(t, int64(0), sum, fmt.Sprintf("step %d: entries sum for %s", step, cur))
		}
		testutil.NoError(t, rows.Err())
		rows, err = pool.Query(ctx, `select currency, sum(balance)::bigint, sum(pending_debits)::bigint,
			sum(pending_credits)::bigint from ledger.accounts group by currency`)
		testutil.NoError(t, err)
		for rows.Next() {
			var cur string
			var bal, pd, pc int64
			testutil.NoError(t, rows.Scan(&cur, &bal, &pd, &pc))
			testutil.Equal(t, int64(0), bal, fmt.Sprintf("step %d: balance sum for %s", step, cur))
			testutil.Equal(t, pd, pc, fmt.Sprintf("step %d: pending debits = credits for %s", step, cur))
		}
		testutil.NoError(t, rows.Err())
	}

	var pending []Transaction
	for step := range 200 {
		cur := currencies[rng.IntN(2)]
		set := accts[cur]
		n := 2 + rng.IntN(3) // 2..4 lines
		lines := make([]Line, n)
		var sum int64
		for i := range n - 1 {
			amt := int64(rng.IntN(2000) - 1000)
			if amt == 0 {
				amt = 1
			}
			lines[i] = Line{set[rng.IntN(len(set))].ID, amt}
			sum += amt
		}
		if sum == 0 { // the closing line must be non-zero
			lines[0].Amount++
			if lines[0].Amount == 0 {
				lines[0].Amount++
			}
			sum = 0
			for _, l := range lines[:n-1] {
				sum += l.Amount
			}
		}
		lines[n-1] = Line{set[rng.IntN(len(set))].ID, -sum}

		txn, err := s.Post(ctx, PostRequest{
			IdempotencyKey: fmt.Sprintf("prop-%d", step),
			Description:    "property",
			EffectiveDate:  time.Now(),
			Currency:       cur,
			Lines:          lines,
			Pending:        rng.IntN(4) == 0,
		})
		testutil.NoError(t, err, fmt.Sprintf("step %d", step))
		if txn.Status == StatusPending {
			pending = append(pending, txn)
		}
		if len(pending) > 0 && rng.IntN(3) == 0 {
			i := rng.IntN(len(pending))
			_, err := s.Resolve(ctx, pending[i].ID, rng.IntN(2) == 0)
			testutil.NoError(t, err, fmt.Sprintf("step %d resolve", step))
			pending = append(pending[:i], pending[i+1:]...)
		}
		check(step)
	}
}

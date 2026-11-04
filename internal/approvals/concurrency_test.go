package approvals

import (
	"context"
	"errors"
	"sync"
	"testing"

	"github.com/LupovichRan/AutoDev_Bank/internal/testutil"
)

// Eight eligible approvers race to be the deciding approval of a tier-1
// transfer: exactly one executes it, the others see it settled, and the
// ledger posts once.
func TestDecide_ConcurrentLastApprovers(t *testing.T) {
	e := newEnv(t)
	ctx := context.Background()
	alice := e.principal(t, "customer")
	from := e.account(t, 1_000_000, alice)
	to := e.account(t, 0)
	const n = 8
	checkers := make([]Principal, n)
	for i := range checkers {
		checkers[i] = e.principal(t, "staff")
		testutil.NoError(t, e.svc.GrantRole(ctx, checkers[i].ID, RoleApprover))
	}
	out, err := e.svc.SubmitTransfer(ctx, e.transfer(alice, from, to, 200000))
	testutil.NoError(t, err)
	testutil.Equal(t, 1, out.Approval.RequiredApprovals)

	results := make([]Approval, n)
	errs := make([]error, n)
	start := make(chan struct{})
	var wg sync.WaitGroup
	for i := range checkers {
		wg.Add(1)
		go func() {
			defer wg.Done()
			<-start
			results[i], errs[i] = e.svc.Decide(ctx, out.Approval.ID, checkers[i].ID, true, "")
		}()
	}
	close(start)
	wg.Wait()

	executed := 0
	for i, err := range errs {
		switch {
		case err == nil:
			testutil.Equal(t, StatusExecuted, results[i].Status)
			executed++
		case errors.Is(err, ErrNotPending), errors.Is(err, ErrAlreadyDecided):
		default:
			t.Fatalf("approver %d: unexpected error %v", i, err)
		}
	}
	testutil.Equal(t, 1, executed)

	ap, err := e.svc.Get(ctx, out.Approval.ID)
	testutil.NoError(t, err)
	testutil.Equal(t, StatusExecuted, ap.Status)
	testutil.Len(t, ap.Decisions, 1)
	testutil.Equal(t, 1, e.ledgerTxCount(t, out.Approval.Subject.IdempotencyKey))
	testutil.Equal(t, int64(800000), e.balance(t, from.ID))
	testutil.Equal(t, int64(200000), e.balance(t, to.ID))
	testutil.Equal(t, 1, countAction(e.auditActions(t), "approval.executed"))
}

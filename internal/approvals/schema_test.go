package approvals

import (
	"context"
	"testing"

	"github.com/LupovichRan/AutoDev_Bank/internal/ids"
	"github.com/LupovichRan/AutoDev_Bank/internal/testutil"
	"github.com/google/uuid"
)

// pendingApproval creates a pending tier-1 approval through the service and
// returns its id plus an eligible staff approver.
func (e *env) pendingApproval(t *testing.T) (uuid.UUID, Principal) {
	t.Helper()
	ctx := context.Background()
	alice := e.principal(t, "customer")
	checker := e.principal(t, "staff")
	testutil.NoError(t, e.svc.GrantRole(ctx, checker.ID, RoleApprover))
	from := e.account(t, 1_000_000, alice)
	to := e.account(t, 0)
	out, err := e.svc.SubmitTransfer(ctx, e.transfer(alice, from, to, 200000))
	testutil.NoError(t, err)
	testutil.True(t, out.Approval != nil, "expected an approval")
	return out.Approval.ID, checker
}

func TestApprovalsStateMachine_ImmutableColumns(t *testing.T) {
	e := newEnv(t)
	ctx := context.Background()
	id, _ := e.pendingApproval(t)
	other := e.principal(t, "customer")

	for _, set := range []string{
		`id = '` + ids.New().String() + `'`,
		`kind = 'other'`,
		`subject = '{"idempotency_key":"x"}'::jsonb`,
		`amount = amount + 1`,
		`currency = 'USD'`,
		`initiator_id = '` + other.ID.String() + `'`,
		`required_approvals = required_approvals + 1`,
		`requires_holder_approval = not requires_holder_approval`,
		`created_at = created_at - interval '1 day'`,
	} {
		_, err := e.pool.Exec(ctx, `update control.approvals set `+set+` where id = $1`, id)
		testutil.ErrorContains(t, err, "approval_immutable")
	}
	// not_before is still adjustable while pending.
	_, err := e.pool.Exec(ctx, `update control.approvals set not_before = now() where id = $1`, id)
	testutil.NoError(t, err)
}

func TestApprovalsStateMachine_Transitions(t *testing.T) {
	e := newEnv(t)
	ctx := context.Background()
	setStatus := func(id uuid.UUID, status string) error {
		_, err := e.pool.Exec(ctx, `update control.approvals set status = $2 where id = $1`, id, status)
		return err
	}

	// pending -> executed directly is not a transition (must pass approved).
	id, _ := e.pendingApproval(t)
	testutil.ErrorContains(t, setStatus(id, StatusExecuted), "approval_immutable")
	// pending -> expired is allowed, and expired is then final.
	testutil.NoError(t, setStatus(id, StatusExpired))
	testutil.ErrorContains(t, setStatus(id, StatusPending), "approval_immutable")

	// pending -> rejected; rejected is terminal.
	id, checker := e.pendingApproval(t)
	_, err := e.svc.Decide(ctx, id, checker.ID, false, "no")
	testutil.NoError(t, err)
	for _, s := range []string{StatusPending, StatusApproved, StatusExpired} {
		testutil.ErrorContains(t, setStatus(id, s), "approval_immutable")
	}
	_, err = e.pool.Exec(ctx, `update control.approvals set not_before = now() where id = $1`, id)
	testutil.ErrorContains(t, err, "approval_immutable")

	// executed is terminal.
	id, checker = e.pendingApproval(t)
	ap, err := e.svc.Decide(ctx, id, checker.ID, true, "ok")
	testutil.NoError(t, err)
	testutil.Equal(t, StatusExecuted, ap.Status)
	for _, s := range []string{StatusPending, StatusApproved, StatusRejected} {
		testutil.ErrorContains(t, setStatus(id, s), "approval_immutable")
	}
	_, err = e.pool.Exec(ctx, `update control.approvals set ledger_transaction_id = null where id = $1`, id)
	testutil.ErrorContains(t, err, "approval_immutable")

	// Decisions are append-only.
	_, err = e.pool.Exec(ctx, `delete from control.approval_decisions where approval_id = $1`, id)
	testutil.ErrorContains(t, err, "approval_immutable")
	_, err = e.pool.Exec(ctx, `update control.approval_decisions set reason = 'edited' where approval_id = $1`, id)
	testutil.ErrorContains(t, err, "approval_immutable")
	// Approvals cannot be deleted either.
	_, err = e.pool.Exec(ctx, `delete from control.approvals where id = $1`, id)
	testutil.ErrorContains(t, err, "approval_immutable")
}

package approvals

import (
	"context"
	"testing"

	"github.com/LupovichRan/AutoDev_Bank/internal/testutil"
)

func TestGrantRole_InitiatorAndApproverConflict(t *testing.T) {
	e := newEnv(t)
	ctx := context.Background()

	// initiator first, then approver
	a := e.principal(t, "staff")
	testutil.NoError(t, e.svc.GrantRole(ctx, a.ID, RoleInitiator))
	testutil.NoError(t, e.svc.GrantRole(ctx, a.ID, RoleInitiator)) // idempotent
	testutil.ErrorIs(t, e.svc.GrantRole(ctx, a.ID, RoleApprover), ErrSoDConflict)
	roles, err := e.svc.Roles(ctx, a.ID)
	testutil.NoError(t, err)
	testutil.Equal(t, []string{RoleInitiator}, roles)

	// approver first, then initiator
	b := e.principal(t, "staff")
	testutil.NoError(t, e.svc.GrantRole(ctx, b.ID, RoleApprover))
	testutil.ErrorIs(t, e.svc.GrantRole(ctx, b.ID, RoleInitiator), ErrSoDConflict)

	// The trigger holds for raw SQL too, not only through the service.
	_, err = e.pool.Exec(ctx, `insert into control.roles (principal_id, role) values ($1, $2)`, b.ID, RoleInitiator)
	testutil.ErrorContains(t, err, "sod_conflict")

	// Unrelated roles are fine.
	testutil.NoError(t, e.svc.GrantRole(ctx, b.ID, "support.read"))
}

func TestDecide_InitiatorCannotApprove_DBLevel(t *testing.T) {
	e := newEnv(t)
	ctx := context.Background()
	alice := e.principal(t, "customer")
	from := e.account(t, 1_000_000, alice)
	to := e.account(t, 0)

	out, err := e.svc.SubmitTransfer(ctx, e.transfer(alice, from, to, 200000))
	testutil.NoError(t, err)
	testutil.True(t, out.Approval != nil, "expected a pending approval")

	_, err = e.pool.Exec(ctx, `insert into control.approval_decisions (approval_id, approver_id, decision, reason)
		values ($1, $2, 'approve', 'self')`, out.Approval.ID, alice.ID)
	testutil.ErrorContains(t, err, "sod_self_approval")
}

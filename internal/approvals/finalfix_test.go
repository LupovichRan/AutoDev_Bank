package approvals

import (
	"context"
	"testing"
	"time"

	"github.com/LupovichRan/AutoDev_Bank/internal/ledger"
	"github.com/LupovichRan/AutoDev_Bank/internal/testutil"
	"github.com/google/uuid"
)

// ---------------------------------------------------------------------------
// C1 / R2: who may debit what, and when an approval is unavoidable.

// equity creates a holder-less, allow-negative ILS account (like bank.equity.ILS).
func (e *env) equity(t *testing.T) ledger.Account {
	t.Helper()
	code := "equity-" + uuid.NewString()
	a, err := e.led.CreateAccount(context.Background(), ledger.AccountSpec{
		Code: code, Name: code, Currency: "ILS", Kind: "equity", AllowNegative: true})
	testutil.NoError(t, err)
	return a
}

func TestSubmitTransfer_StaffFromEquityAlwaysNeedsApproval(t *testing.T) {
	e := newEnv(t)
	ctx := context.Background()
	dave := e.principal(t, "staff")
	testutil.NoError(t, e.svc.GrantRole(ctx, dave.ID, RoleInitiator))
	carol := e.principal(t, "staff")
	testutil.NoError(t, e.svc.GrantRole(ctx, carol.ID, RoleApprover))
	eq := e.equity(t)
	bob := e.principal(t, "customer")
	to := e.account(t, 0, bob)

	for i := 0; i < 3; i++ { // the reviewer's repro: several sub-tier transfers
		out, err := e.svc.SubmitTransfer(ctx, e.transfer(dave, eq, to, 99999))
		testutil.NoError(t, err)
		testutil.True(t, out.Approval != nil, "staff debit from equity needs a checker")
		testutil.Equal(t, 1, out.Approval.RequiredApprovals)
	}
	testutil.Equal(t, int64(0), e.balance(t, to.ID))

	// Only a payments.approver can clear it (there is no holder).
	inbox, err := e.svc.Inbox(ctx, carol.ID)
	testutil.NoError(t, err)
	testutil.Len(t, inbox, 3)
	ap, err := e.svc.Decide(ctx, inbox[0].ID, carol.ID, true, "ok")
	testutil.NoError(t, err)
	testutil.Equal(t, StatusExecuted, ap.Status)
	testutil.Equal(t, int64(99999), e.balance(t, to.ID))
}

func TestSubmitTransfer_HolderOfAllowNegativeAccountNeedsApproval(t *testing.T) {
	e := newEnv(t)
	ctx := context.Background()
	alice := e.principal(t, "customer")
	code := "overdraft-" + uuid.NewString()
	od, err := e.led.CreateAccount(ctx, ledger.AccountSpec{Code: code, Name: code, Currency: "ILS", Kind: "asset", AllowNegative: true})
	testutil.NoError(t, err)
	testutil.NoError(t, e.svc.AddHolder(ctx, od.ID, alice.ID))
	out, err := e.svc.SubmitTransfer(ctx, e.transfer(alice, od, e.account(t, 0, e.principal(t, "customer")), 100))
	testutil.NoError(t, err)
	testutil.True(t, out.Approval != nil, "allow_negative source needs an approval")
}

func TestSubmitTransfer_DestinationMustBeUsable(t *testing.T) {
	e := newEnv(t)
	ctx := context.Background()
	alice, bob := e.principal(t, "customer"), e.principal(t, "customer")
	from := e.account(t, 50000, alice)

	// Unknown, internal (holder-less) and other-currency destinations all
	// fail the same way for a customer: no existence oracle.
	eur, err := e.led.CreateAccount(ctx, ledger.AccountSpec{Code: "eur-" + uuid.NewString(), Name: "eur", Currency: "EUR", Kind: "asset"})
	testutil.NoError(t, err)
	testutil.NoError(t, e.svc.AddHolder(ctx, eur.ID, bob.ID))
	for name, to := range map[string]uuid.UUID{"unknown": uuid.New(), "internal": e.equity(t).ID, "currency": eur.ID} {
		in := e.transfer(alice, from, ledger.Account{ID: to}, 100)
		_, err := e.svc.SubmitTransfer(ctx, in)
		testutil.ErrorIs(t, err, ErrInvalidDestination, name)
	}
	// An unknown source account looks like someone else's.
	in := e.transfer(alice, ledger.Account{ID: uuid.New()}, e.account(t, 0, bob), 100)
	_, err = e.svc.SubmitTransfer(ctx, in)
	testutil.ErrorIs(t, err, ErrNotAuthorized)
}

// ---------------------------------------------------------------------------
// I3: the quorum, the co-holder rule, approver eligibility, payment roles
// for customers and the executed ledger link hold in the schema too.

func TestApprovalsSchema_ApprovedNeedsQuorum(t *testing.T) {
	e := newEnv(t)
	ctx := context.Background()
	id, _ := e.pendingApproval(t)
	_, err := e.pool.Exec(ctx, `update control.approvals set status = 'approved' where id = $1`, id)
	testutil.ErrorContains(t, err, "approval_quorum")
}

func TestApprovalsSchema_JointNeedsCoHolderDecision(t *testing.T) {
	e := newEnv(t)
	ctx := context.Background()
	alice, bob := e.principal(t, "customer"), e.principal(t, "customer")
	checker := e.principal(t, "staff")
	testutil.NoError(t, e.svc.GrantRole(ctx, checker.ID, RoleApprover))
	joint := e.account(t, 100000, alice, bob)
	out, err := e.svc.SubmitTransfer(ctx, e.transfer(alice, joint, e.account(t, 0, bob), 500))
	testutil.NoError(t, err)
	testutil.True(t, out.Approval != nil && out.Approval.RequiresHolderApproval, "joint approval")
	id := out.Approval.ID

	_, err = e.pool.Exec(ctx, `insert into control.approval_decisions (approval_id, approver_id, decision)
		values ($1, $2, 'approve')`, id, checker.ID)
	testutil.NoError(t, err)
	_, err = e.pool.Exec(ctx, `update control.approvals set status = 'approved' where id = $1`, id)
	testutil.ErrorContains(t, err, "approval_holder_required")

	_, err = e.pool.Exec(ctx, `insert into control.approval_decisions (approval_id, approver_id, decision)
		values ($1, $2, 'approve')`, id, bob.ID)
	testutil.NoError(t, err)
	_, err = e.pool.Exec(ctx, `update control.approvals set status = 'approved' where id = $1`, id)
	testutil.NoError(t, err)
}

func TestApprovalsSchema_DeciderMustBeHolderOrApprover(t *testing.T) {
	e := newEnv(t)
	ctx := context.Background()
	id, _ := e.pendingApproval(t)
	for _, kind := range []string{"customer", "staff"} {
		stranger := e.principal(t, kind)
		_, err := e.pool.Exec(ctx, `insert into control.approval_decisions (approval_id, approver_id, decision)
			values ($1, $2, 'approve')`, id, stranger.ID)
		testutil.ErrorContains(t, err, "sod_not_eligible", kind)
	}
}

func TestApprovalsSchema_CustomersCannotHoldPaymentRoles(t *testing.T) {
	e := newEnv(t)
	ctx := context.Background()
	alice := e.principal(t, "customer")
	for _, role := range []string{RoleApprover, RoleInitiator} {
		_, err := e.pool.Exec(ctx, `insert into control.roles (principal_id, role) values ($1, $2)`, alice.ID, role)
		testutil.ErrorContains(t, err, "sod_role_not_allowed", role)
	}
	// Other roles are fine.
	_, err := e.pool.Exec(ctx, `insert into control.roles (principal_id, role) values ($1, 'support.read')`, alice.ID)
	testutil.NoError(t, err)
}

func TestApprovalsSchema_ExecutedMustLinkItsOwnPosting(t *testing.T) {
	e := newEnv(t)
	ctx := context.Background()
	id, checker := e.pendingApproval(t)
	_, err := e.pool.Exec(ctx, `insert into control.approval_decisions (approval_id, approver_id, decision)
		values ($1, $2, 'approve')`, id, checker.ID)
	testutil.NoError(t, err)
	_, err = e.pool.Exec(ctx, `update control.approvals set status = 'approved' where id = $1`, id)
	testutil.NoError(t, err)

	var other uuid.UUID // some unrelated posting (an account funding)
	testutil.NoError(t, e.pool.QueryRow(ctx, `select id from ledger.transactions order by created_at limit 1`).Scan(&other))
	_, err = e.pool.Exec(ctx, `update control.approvals set status = 'executed', ledger_transaction_id = $2 where id = $1`, id, other)
	testutil.ErrorContains(t, err, "approval_ledger_mismatch")
}

// ---------------------------------------------------------------------------
// I7 / R3: expiry and cancellation.

func TestApprovals_ExpireAfterSevenDays(t *testing.T) {
	e := newEnv(t)
	ctx := context.Background()
	id, checker := e.pendingApproval(t)
	ap, err := e.svc.Get(ctx, id)
	testutil.NoError(t, err)
	testutil.Equal(t, ap.CreatedAt.Add(7*24*time.Hour).UTC(), ap.ExpiresAt.UTC())

	// Nothing is due yet.
	n, err := e.svc.ExpireApprovals(ctx)
	testutil.NoError(t, err)
	testutil.Equal(t, 0, n)

	// Bring the deadline forward (allowed while pending; it can never be extended).
	_, err = e.pool.Exec(ctx, `update control.approvals set expires_at = expires_at + interval '1 day' where id = $1`, id)
	testutil.ErrorContains(t, err, "approval_immutable")
	_, err = e.pool.Exec(ctx, `update control.approvals set expires_at = now() - interval '1 minute' where id = $1`, id)
	testutil.NoError(t, err)

	// Past its deadline it cannot be decided, even before the sweep runs.
	_, err = e.svc.Decide(ctx, id, checker.ID, true, "late")
	testutil.ErrorIs(t, err, ErrNotPending)

	n, err = e.svc.ExpireApprovals(ctx)
	testutil.NoError(t, err)
	testutil.Equal(t, 1, n)
	ap, err = e.svc.Get(ctx, id)
	testutil.NoError(t, err)
	testutil.Equal(t, StatusExpired, ap.Status)
	testutil.True(t, contains(e.auditActions(t), "approval.expired"), "audit approval.expired")
	n, err = e.svc.ExpireApprovals(ctx)
	testutil.NoError(t, err)
	testutil.Equal(t, 0, n)
}

func TestApprovals_InitiatorCanCancel(t *testing.T) {
	e := newEnv(t)
	ctx := context.Background()
	id, checker := e.pendingApproval(t)
	ap, err := e.svc.Get(ctx, id)
	testutil.NoError(t, err)

	// Nobody but the initiator may cancel.
	_, err = e.svc.Cancel(ctx, id, checker.ID)
	testutil.ErrorIs(t, err, ErrNotAuthorized)
	_, err = e.svc.Cancel(ctx, uuid.New(), ap.InitiatorID)
	testutil.ErrorIs(t, err, ErrNotFound)

	got, err := e.svc.Cancel(ctx, id, ap.InitiatorID)
	testutil.NoError(t, err)
	testutil.Equal(t, StatusRejected, got.Status)
	testutil.True(t, contains(e.auditActions(t), "approval.cancelled"), "audit approval.cancelled")
	var reason string
	testutil.NoError(t, e.pool.QueryRow(ctx, `select reason from control.decision_log
		where action = 'approval.cancel' and principal_id = $1 order by id desc limit 1`, ap.InitiatorID).Scan(&reason))
	testutil.Equal(t, "cancelled by initiator", reason)

	_, err = e.svc.Cancel(ctx, id, ap.InitiatorID)
	testutil.ErrorIs(t, err, ErrNotPending)
	_, err = e.svc.Decide(ctx, id, checker.ID, true, "")
	testutil.ErrorIs(t, err, ErrNotPending)
}

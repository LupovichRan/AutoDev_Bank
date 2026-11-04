package approvals

import (
	"context"
	"crypto/ed25519"
	"testing"
	"time"

	"github.com/LupovichRan/AutoDev_Bank/internal/audit"
	"github.com/LupovichRan/AutoDev_Bank/internal/db"
	"github.com/LupovichRan/AutoDev_Bank/internal/ids"
	"github.com/LupovichRan/AutoDev_Bank/internal/ledger"
	"github.com/LupovichRan/AutoDev_Bank/internal/testutil"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
)

type env struct {
	pool *pgxpool.Pool
	led  *ledger.Service
	aud  *audit.Recorder
	svc  *Service
}

func newEnv(t *testing.T) *env {
	t.Helper()
	pool := db.TestPool(t)
	_, key, err := ed25519.GenerateKey(nil)
	testutil.NoError(t, err)
	led := ledger.New(pool)
	aud := audit.New(pool, key)
	return &env{pool: pool, led: led, aud: aud, svc: New(pool, led, aud, DefaultPolicy())}
}

func (e *env) principal(t *testing.T, kind string) Principal {
	t.Helper()
	h := kind + "-" + ids.New().String()
	p, err := e.svc.CreatePrincipal(context.Background(), kind, h, h)
	testutil.NoError(t, err)
	return p
}

// account creates an ILS asset account held by holders and funds it with
// balance from a fresh allow-negative equity account.
func (e *env) account(t *testing.T, balance int64, holders ...Principal) ledger.Account {
	t.Helper()
	ctx := context.Background()
	newAcct := func(kind string, neg bool) ledger.Account {
		code := kind + "-" + ids.New().String()
		a, err := e.led.CreateAccount(ctx, ledger.AccountSpec{Code: code, Name: code, Currency: "ILS", Kind: kind, AllowNegative: neg})
		testutil.NoError(t, err)
		return a
	}
	a := newAcct("asset", false)
	for _, h := range holders {
		testutil.NoError(t, e.svc.AddHolder(ctx, a.ID, h.ID))
	}
	if balance > 0 {
		eq := newAcct("equity", true)
		_, err := e.led.Post(ctx, ledger.PostRequest{
			IdempotencyKey: "fund-" + ids.New().String(), Description: "funding", Currency: "ILS",
			Lines: []ledger.Line{{AccountID: eq.ID, Amount: -balance}, {AccountID: a.ID, Amount: balance}},
		})
		testutil.NoError(t, err)
	}
	return a
}

func (e *env) transfer(initiator Principal, from, to ledger.Account, amount int64) TransferInput {
	return TransferInput{
		IdempotencyKey: "tr-" + ids.New().String(), InitiatorID: initiator.ID,
		FromAccountID: from.ID, ToAccountID: to.ID, Amount: amount, Currency: "ILS", Description: "rent",
	}
}

func (e *env) balance(t *testing.T, id uuid.UUID) int64 {
	t.Helper()
	a, err := e.led.Account(context.Background(), id)
	testutil.NoError(t, err)
	return a.Balance
}

func (e *env) auditActions(t *testing.T) []string {
	t.Helper()
	evs, err := e.aud.Recent(context.Background(), 100)
	testutil.NoError(t, err)
	var out []string
	for _, ev := range evs {
		out = append(out, ev.Action)
	}
	return out
}

func (e *env) logOutcomes(t *testing.T, principalID uuid.UUID) []string {
	t.Helper()
	rows, err := e.pool.Query(context.Background(),
		`select outcome from control.decision_log where principal_id = $1 order by id`, principalID)
	testutil.NoError(t, err)
	defer rows.Close()
	var out []string
	for rows.Next() {
		var s string
		testutil.NoError(t, rows.Scan(&s))
		out = append(out, s)
	}
	testutil.NoError(t, rows.Err())
	return out
}

func contains(xs []string, x string) bool {
	for _, v := range xs {
		if v == x {
			return true
		}
	}
	return false
}

func TestSubmitTransfer_SmallAmountPostsImmediately(t *testing.T) {
	e := newEnv(t)
	ctx := context.Background()
	alice := e.principal(t, "customer")
	from := e.account(t, 50000, alice)
	to := e.account(t, 0)

	out, err := e.svc.SubmitTransfer(ctx, e.transfer(alice, from, to, 12345))
	testutil.NoError(t, err)
	testutil.True(t, out.Posted != nil, "expected an immediate posting")
	testutil.True(t, out.Approval == nil, "expected no approval")
	testutil.Equal(t, ledger.StatusPosted, out.Posted.Status)
	testutil.Equal(t, int64(50000-12345), e.balance(t, from.ID))
	testutil.Equal(t, int64(12345), e.balance(t, to.ID))
	testutil.Equal(t, []string{"posted"}, e.logOutcomes(t, alice.ID))
	testutil.True(t, contains(e.auditActions(t), "transfer.posted"), "audit transfer.posted")

	// A stranger who neither holds the account nor has payments.initiator is refused.
	mallory := e.principal(t, "customer")
	_, err = e.svc.SubmitTransfer(ctx, e.transfer(mallory, from, to, 100))
	testutil.ErrorIs(t, err, ErrNotAuthorized)
	testutil.Equal(t, []string{"denied"}, e.logOutcomes(t, mallory.ID))
	testutil.Equal(t, int64(50000-12345), e.balance(t, from.ID))
}

func TestSubmitTransfer_LargeAmountCreatesApproval(t *testing.T) {
	e := newEnv(t)
	ctx := context.Background()
	alice := e.principal(t, "customer")
	from := e.account(t, 1_000_000, alice)
	to := e.account(t, 0)

	in := e.transfer(alice, from, to, 200000)
	out, err := e.svc.SubmitTransfer(ctx, in)
	testutil.NoError(t, err)
	testutil.True(t, out.Posted == nil, "expected no posting")
	testutil.True(t, out.Approval != nil, "expected an approval")
	ap := *out.Approval
	testutil.Equal(t, StatusPending, ap.Status)
	testutil.Equal(t, 1, ap.RequiredApprovals)
	testutil.Equal(t, int64(200000), ap.Amount)
	testutil.Equal(t, "ILS", ap.Currency)
	testutil.Equal(t, alice.ID, ap.InitiatorID)
	testutil.Equal(t, in, ap.Subject)
	testutil.True(t, ap.LedgerTransactionID == nil, "not executed yet")

	// ledger unchanged
	testutil.Equal(t, int64(1_000_000), e.balance(t, from.ID))
	testutil.Equal(t, int64(0), e.balance(t, to.ID))
	// decision_log row
	testutil.Equal(t, []string{"approval_created"}, e.logOutcomes(t, alice.ID))
	testutil.True(t, contains(e.auditActions(t), "approval.created"), "audit approval.created")

	mine, err := e.svc.ListForInitiator(ctx, alice.ID)
	testutil.NoError(t, err)
	testutil.Len(t, mine, 1)
	testutil.Equal(t, ap.ID, mine[0].ID)

	// Replaying the same submission returns the same approval.
	again, err := e.svc.SubmitTransfer(ctx, in)
	testutil.NoError(t, err)
	testutil.True(t, again.Approval != nil && again.Approval.ID == ap.ID, "idempotent replay")
	mine, err = e.svc.ListForInitiator(ctx, alice.ID)
	testutil.NoError(t, err)
	testutil.Len(t, mine, 1)
}

func TestDecide_ApproveExecutesLedgerPost(t *testing.T) {
	e := newEnv(t)
	ctx := context.Background()
	alice := e.principal(t, "customer")
	checker := e.principal(t, "staff")
	testutil.NoError(t, e.svc.GrantRole(ctx, checker.ID, RoleApprover))
	from := e.account(t, 1_000_000, alice)
	to := e.account(t, 0)

	out, err := e.svc.SubmitTransfer(ctx, e.transfer(alice, from, to, 200000))
	testutil.NoError(t, err)

	inbox, err := e.svc.Inbox(ctx, checker.ID)
	testutil.NoError(t, err)
	testutil.Len(t, inbox, 1)
	own, err := e.svc.Inbox(ctx, alice.ID)
	testutil.NoError(t, err)
	testutil.Len(t, own, 0)

	// A principal who is neither holder nor approver cannot decide.
	mallory := e.principal(t, "staff")
	_, err = e.svc.Decide(ctx, out.Approval.ID, mallory.ID, true, "")
	testutil.ErrorIs(t, err, ErrNotAuthorized)

	ap, err := e.svc.Decide(ctx, out.Approval.ID, checker.ID, true, "looks fine")
	testutil.NoError(t, err)
	testutil.Equal(t, StatusExecuted, ap.Status)
	testutil.True(t, ap.LedgerTransactionID != nil, "ledger transaction recorded")
	testutil.Len(t, ap.Decisions, 1)
	testutil.Equal(t, checker.ID, ap.Decisions[0].ApproverID)
	testutil.Equal(t, "approve", ap.Decisions[0].Decision)
	testutil.Equal(t, "looks fine", ap.Decisions[0].Reason)

	testutil.Equal(t, int64(800000), e.balance(t, from.ID))
	testutil.Equal(t, int64(200000), e.balance(t, to.ID))
	tx, err := e.led.Transaction(ctx, *ap.LedgerTransactionID)
	testutil.NoError(t, err)
	testutil.Equal(t, out.Approval.Subject.IdempotencyKey, tx.IdempotencyKey)
	testutil.True(t, contains(e.auditActions(t), "approval.executed"), "audit approval.executed")
	testutil.Equal(t, []string{"executed"}, e.logOutcomes(t, checker.ID))

	inbox, err = e.svc.Inbox(ctx, checker.ID)
	testutil.NoError(t, err)
	testutil.Len(t, inbox, 0)

	// Deciding again after execution is refused.
	_, err = e.svc.Decide(ctx, out.Approval.ID, checker.ID, true, "")
	testutil.ErrorIs(t, err, ErrNotPending)
}

func TestDecide_TwoApproversForTier2(t *testing.T) {
	e := newEnv(t)
	ctx := context.Background()
	alice := e.principal(t, "customer")
	c1, c2 := e.principal(t, "staff"), e.principal(t, "staff")
	testutil.NoError(t, e.svc.GrantRole(ctx, c1.ID, RoleApprover))
	testutil.NoError(t, e.svc.GrantRole(ctx, c2.ID, RoleApprover))
	from := e.account(t, 2_000_000, alice)
	to := e.account(t, 0)

	out, err := e.svc.SubmitTransfer(ctx, e.transfer(alice, from, to, 1_500_000))
	testutil.NoError(t, err)
	testutil.Equal(t, 2, out.Approval.RequiredApprovals)

	ap, err := e.svc.Decide(ctx, out.Approval.ID, c1.ID, true, "")
	testutil.NoError(t, err)
	testutil.Equal(t, StatusPending, ap.Status)
	testutil.Equal(t, int64(2_000_000), e.balance(t, from.ID))

	_, err = e.svc.Decide(ctx, out.Approval.ID, c1.ID, true, "")
	testutil.ErrorIs(t, err, ErrAlreadyDecided)

	ap, err = e.svc.Decide(ctx, out.Approval.ID, c2.ID, true, "")
	testutil.NoError(t, err)
	testutil.Equal(t, StatusExecuted, ap.Status)
	testutil.Len(t, ap.Decisions, 2)
	testutil.Equal(t, int64(500_000), e.balance(t, from.ID))
}

func TestDecide_RejectLeavesLedgerUnchanged(t *testing.T) {
	e := newEnv(t)
	ctx := context.Background()
	alice := e.principal(t, "customer")
	checker := e.principal(t, "staff")
	testutil.NoError(t, e.svc.GrantRole(ctx, checker.ID, RoleApprover))
	from := e.account(t, 1_000_000, alice)
	to := e.account(t, 0)

	out, err := e.svc.SubmitTransfer(ctx, e.transfer(alice, from, to, 200000))
	testutil.NoError(t, err)

	ap, err := e.svc.Decide(ctx, out.Approval.ID, checker.ID, false, "unusual payee")
	testutil.NoError(t, err)
	testutil.Equal(t, StatusRejected, ap.Status)
	testutil.True(t, ap.LedgerTransactionID == nil, "no ledger transaction")
	testutil.Len(t, ap.Decisions, 1)
	testutil.Equal(t, "reject", ap.Decisions[0].Decision)
	testutil.Equal(t, int64(1_000_000), e.balance(t, from.ID))
	testutil.Equal(t, int64(0), e.balance(t, to.ID))
	testutil.True(t, contains(e.auditActions(t), "approval.rejected"), "audit approval.rejected")
	testutil.Equal(t, []string{"rejected"}, e.logOutcomes(t, checker.ID))

	other := e.principal(t, "staff")
	testutil.NoError(t, e.svc.GrantRole(ctx, other.ID, RoleApprover))
	_, err = e.svc.Decide(ctx, out.Approval.ID, other.ID, true, "")
	testutil.ErrorIs(t, err, ErrNotPending)

	mine, err := e.svc.ListForInitiator(ctx, alice.ID)
	testutil.NoError(t, err)
	testutil.Len(t, mine, 1)
	testutil.Equal(t, StatusRejected, mine[0].Status)
}

func TestDecide_SelfApprovalRejected(t *testing.T) {
	e := newEnv(t)
	ctx := context.Background()
	alice, bob := e.principal(t, "customer"), e.principal(t, "customer")
	from := e.account(t, 100000, alice, bob) // joint account
	to := e.account(t, 0)

	// Below the tier, but the joint account needs the other holder.
	out, err := e.svc.SubmitTransfer(ctx, e.transfer(alice, from, to, 5000))
	testutil.NoError(t, err)
	testutil.True(t, out.Approval != nil, "joint account needs approval")
	testutil.Equal(t, 1, out.Approval.RequiredApprovals)

	_, err = e.svc.Decide(ctx, out.Approval.ID, alice.ID, true, "")
	testutil.ErrorIs(t, err, ErrSelfApproval)
	testutil.Equal(t, []string{"approval_created", "denied"}, e.logOutcomes(t, alice.ID))
	testutil.Equal(t, int64(100000), e.balance(t, from.ID))

	inbox, err := e.svc.Inbox(ctx, bob.ID)
	testutil.NoError(t, err)
	testutil.Len(t, inbox, 1)
	ap, err := e.svc.Decide(ctx, out.Approval.ID, bob.ID, true, "ok")
	testutil.NoError(t, err)
	testutil.Equal(t, StatusExecuted, ap.Status)
	testutil.Equal(t, int64(95000), e.balance(t, from.ID))
}

func TestDecide_NotBeforeBlocksEarlyExecution(t *testing.T) {
	e := newEnv(t)
	ctx := context.Background()
	alice := e.principal(t, "customer")
	checker := e.principal(t, "staff")
	testutil.NoError(t, e.svc.GrantRole(ctx, checker.ID, RoleApprover))
	from := e.account(t, 1_000_000, alice)
	to := e.account(t, 0)

	out, err := e.svc.SubmitTransfer(ctx, e.transfer(alice, from, to, 200000))
	testutil.NoError(t, err)
	_, err = e.pool.Exec(ctx, `update control.approvals set not_before = now() + interval '1 hour' where id = $1`, out.Approval.ID)
	testutil.NoError(t, err)

	_, err = e.svc.Decide(ctx, out.Approval.ID, checker.ID, true, "")
	testutil.ErrorIs(t, err, ErrNotBefore)
	testutil.Equal(t, int64(1_000_000), e.balance(t, from.ID))

	_, err = e.pool.Exec(ctx, `update control.approvals set not_before = now() - interval '1 second' where id = $1`, out.Approval.ID)
	testutil.NoError(t, err)
	ap, err := e.svc.Decide(ctx, out.Approval.ID, checker.ID, true, "")
	testutil.NoError(t, err)
	testutil.Equal(t, StatusExecuted, ap.Status)
	testutil.True(t, ap.NotBefore != nil && ap.NotBefore.Before(time.Now()), "not_before loaded")
	testutil.Equal(t, int64(800000), e.balance(t, from.ID))
}

func TestPrincipals_HoldersAndAccounts(t *testing.T) {
	e := newEnv(t)
	ctx := context.Background()
	alice, bob := e.principal(t, "customer"), e.principal(t, "customer")
	joint := e.account(t, 0, alice, bob)
	solo := e.account(t, 0, alice)

	got, err := e.svc.GetPrincipalByHandle(ctx, alice.Handle)
	testutil.NoError(t, err)
	testutil.Equal(t, alice.ID, got.ID)
	_, err = e.svc.GetPrincipalByHandle(ctx, "nobody")
	testutil.ErrorIs(t, err, ErrNotFound)

	hs, err := e.svc.Holders(ctx, joint.ID)
	testutil.NoError(t, err)
	testutil.Len(t, hs, 2)
	accts, err := e.svc.AccountsForHolder(ctx, alice.ID)
	testutil.NoError(t, err)
	testutil.Len(t, accts, 2)
	testutil.Equal(t, joint.ID, accts[0].ID)
	testutil.Equal(t, solo.ID, accts[1].ID)
	testutil.ErrorIs(t, e.svc.AddHolder(ctx, joint.ID, ids.New()), ErrNotFound)
}

func TestDecide_RejectAllowedBeforeNotBefore(t *testing.T) {
	e := newEnv(t)
	ctx := context.Background()
	alice := e.principal(t, "customer")
	checker := e.principal(t, "staff")
	testutil.NoError(t, e.svc.GrantRole(ctx, checker.ID, RoleApprover))
	from := e.account(t, 1_000_000, alice)
	to := e.account(t, 0)

	out, err := e.svc.SubmitTransfer(ctx, e.transfer(alice, from, to, 200000))
	testutil.NoError(t, err)
	_, err = e.pool.Exec(ctx, `update control.approvals set not_before = now() + interval '1 hour' where id = $1`, out.Approval.ID)
	testutil.NoError(t, err)

	ap, err := e.svc.Decide(ctx, out.Approval.ID, checker.ID, false, "cancel")
	testutil.NoError(t, err)
	testutil.Equal(t, StatusRejected, ap.Status)
}

func TestDecide_JointAccountStaffApprovalDoesNotReplaceCoHolder(t *testing.T) {
	e := newEnv(t)
	ctx := context.Background()
	alice, bob := e.principal(t, "customer"), e.principal(t, "customer")
	checker := e.principal(t, "staff")
	testutil.NoError(t, e.svc.GrantRole(ctx, checker.ID, RoleApprover))
	from := e.account(t, 100000, alice, bob) // joint account
	to := e.account(t, 0)

	out, err := e.svc.SubmitTransfer(ctx, e.transfer(alice, from, to, 5000))
	testutil.NoError(t, err)
	testutil.True(t, out.Approval != nil, "joint account needs approval")
	testutil.Equal(t, 1, out.Approval.RequiredApprovals)
	testutil.True(t, out.Approval.RequiresHolderApproval, "co-holder approval flagged")

	// Staff approval reaches the count but not the co-holder rule.
	ap, err := e.svc.Decide(ctx, out.Approval.ID, checker.ID, true, "staff ok")
	testutil.NoError(t, err)
	testutil.Equal(t, StatusPending, ap.Status)
	testutil.Len(t, ap.Decisions, 1)
	testutil.Equal(t, checker.ID, ap.Decisions[0].ApproverID)
	testutil.True(t, ap.LedgerTransactionID == nil, "not executed")
	testutil.Equal(t, int64(100000), e.balance(t, from.ID))
	testutil.Equal(t, []string{"approved"}, e.logOutcomes(t, checker.ID))

	ap, err = e.svc.Decide(ctx, out.Approval.ID, bob.ID, true, "co-holder ok")
	testutil.NoError(t, err)
	testutil.Equal(t, StatusExecuted, ap.Status)
	testutil.Len(t, ap.Decisions, 2)
	testutil.Equal(t, int64(95000), e.balance(t, from.ID))
	testutil.Equal(t, int64(5000), e.balance(t, to.ID))
}

func TestDecide_JointAccountTier2NeedsCoHolderAmongApprovers(t *testing.T) {
	e := newEnv(t)
	ctx := context.Background()
	alice, bob := e.principal(t, "customer"), e.principal(t, "customer")
	c1, c2 := e.principal(t, "staff"), e.principal(t, "staff")
	testutil.NoError(t, e.svc.GrantRole(ctx, c1.ID, RoleApprover))
	testutil.NoError(t, e.svc.GrantRole(ctx, c2.ID, RoleApprover))
	from := e.account(t, 2_000_000, alice, bob)
	to := e.account(t, 0)

	out, err := e.svc.SubmitTransfer(ctx, e.transfer(alice, from, to, 1_500_000))
	testutil.NoError(t, err)
	testutil.Equal(t, 2, out.Approval.RequiredApprovals)

	_, err = e.svc.Decide(ctx, out.Approval.ID, c1.ID, true, "")
	testutil.NoError(t, err)
	ap, err := e.svc.Decide(ctx, out.Approval.ID, c2.ID, true, "")
	testutil.NoError(t, err)
	testutil.Equal(t, StatusPending, ap.Status) // two approvals, no co-holder
	testutil.Equal(t, int64(2_000_000), e.balance(t, from.ID))

	ap, err = e.svc.Decide(ctx, out.Approval.ID, bob.ID, true, "")
	testutil.NoError(t, err)
	testutil.Equal(t, StatusExecuted, ap.Status)
	testutil.Equal(t, int64(500_000), e.balance(t, from.ID))
}

func TestSubmitTransfer_StaffInitiatedCustomerDebitNeedsApproval(t *testing.T) {
	e := newEnv(t)
	ctx := context.Background()
	alice := e.principal(t, "customer")
	clerk := e.principal(t, "staff")
	testutil.NoError(t, e.svc.GrantRole(ctx, clerk.ID, RoleInitiator))
	from := e.account(t, 100000, alice)
	to := e.account(t, 0)

	out, err := e.svc.SubmitTransfer(ctx, e.transfer(clerk, from, to, 100)) // far below tier 1
	testutil.NoError(t, err)
	testutil.True(t, out.Approval != nil, "staff-initiated customer debit needs approval")
	testutil.Equal(t, 1, out.Approval.RequiredApprovals)
	testutil.Equal(t, int64(100000), e.balance(t, from.ID))

	// The account holder may approve it.
	ap, err := e.svc.Decide(ctx, out.Approval.ID, alice.ID, true, "")
	testutil.NoError(t, err)
	testutil.Equal(t, StatusExecuted, ap.Status)
	testutil.Equal(t, int64(99900), e.balance(t, from.ID))
}

package approvals

import (
	"context"
	"testing"

	"github.com/LupovichRan/AutoDev_Bank/internal/ledger"
	"github.com/LupovichRan/AutoDev_Bank/internal/testutil"
)

func (e *env) ledgerTxCount(t *testing.T, key string) int {
	t.Helper()
	var n int
	testutil.NoError(t, e.pool.QueryRow(context.Background(),
		`select count(*) from ledger.transactions where idempotency_key = $1`, key).Scan(&n))
	return n
}

func (e *env) approvalCount(t *testing.T, key string) int {
	t.Helper()
	var n int
	testutil.NoError(t, e.pool.QueryRow(context.Background(),
		`select count(*) from control.approvals where subject->>'idempotency_key' = $1`, key).Scan(&n))
	return n
}

// Posted first, then the same key arrives on the approval path.
func TestIdempotency_PostedKeyThenApprovalPath(t *testing.T) {
	e := newEnv(t)
	ctx := context.Background()
	alice := e.principal(t, "customer")
	from := e.account(t, 2_000_000, alice)
	to := e.payee(t)

	small := e.transfer(alice, from, to, 5000)
	out, err := e.svc.SubmitTransfer(ctx, small)
	testutil.NoError(t, err)
	testutil.True(t, out.Posted != nil, "posted immediately")

	// Same key, different (approval-sized) payload: refused, nothing parked.
	big := small
	big.Amount = 200000
	_, err = e.svc.SubmitTransfer(ctx, big)
	testutil.ErrorIs(t, err, ledger.ErrIdempotencyConflict)
	testutil.Equal(t, 0, e.approvalCount(t, small.IdempotencyKey))

	// Same transfer, but the policy now demands an approval (stricter
	// policy): the replay returns the original posting.
	strict := New(e.pool, e.led, e.aud, Policy{Tiers: []Tier{{Threshold: 1, Required: 1}}})
	again, err := strict.SubmitTransfer(ctx, small)
	testutil.NoError(t, err)
	testutil.True(t, again.Posted != nil && again.Approval == nil, "replay returns the posting")
	testutil.Equal(t, out.Posted.ID, again.Posted.ID)
	testutil.Equal(t, 0, e.approvalCount(t, small.IdempotencyKey))
	testutil.Equal(t, int64(2_000_000-5000), e.balance(t, from.ID))
	testutil.Equal(t, 1, countAction(e.auditActions(t), "transfer.posted"))
}

// Parked as an approval first, then the same key arrives on the posting path.
func TestIdempotency_ApprovalKeyThenPostPath(t *testing.T) {
	e := newEnv(t)
	ctx := context.Background()
	alice := e.principal(t, "customer")
	from := e.account(t, 2_000_000, alice)
	to := e.payee(t)

	big := e.transfer(alice, from, to, 200000)
	out, err := e.svc.SubmitTransfer(ctx, big)
	testutil.NoError(t, err)
	testutil.True(t, out.Approval != nil, "parked")

	// Same key, different (small) payload: refused, nothing posted.
	small := big
	small.Amount = 5000
	_, err = e.svc.SubmitTransfer(ctx, small)
	testutil.ErrorIs(t, err, ledger.ErrIdempotencyConflict)
	testutil.Equal(t, 0, e.ledgerTxCount(t, big.IdempotencyKey))

	// Same transfer under a policy that would post it at once: the replay
	// returns the pending approval and does not bypass maker-checker.
	lax := New(e.pool, e.led, e.aud, Policy{})
	again, err := lax.SubmitTransfer(ctx, big)
	testutil.NoError(t, err)
	testutil.True(t, again.Approval != nil && again.Posted == nil, "replay returns the approval")
	testutil.Equal(t, out.Approval.ID, again.Approval.ID)
	testutil.Equal(t, StatusPending, again.Approval.Status)
	testutil.Equal(t, 0, e.ledgerTxCount(t, big.IdempotencyKey))
	testutil.Equal(t, int64(2_000_000), e.balance(t, from.ID))
}

// A replayed immediate posting is not audited twice.
func TestIdempotency_PostReplayAuditedOnce(t *testing.T) {
	e := newEnv(t)
	ctx := context.Background()
	alice := e.principal(t, "customer")
	from := e.account(t, 100000, alice)
	to := e.payee(t)
	in := e.transfer(alice, from, to, 5000)
	a, err := e.svc.SubmitTransfer(ctx, in)
	testutil.NoError(t, err)
	b, err := e.svc.SubmitTransfer(ctx, in)
	testutil.NoError(t, err)
	testutil.Equal(t, a.Posted.ID, b.Posted.ID)
	testutil.Equal(t, 1, countAction(e.auditActions(t), "transfer.posted"))
	testutil.Equal(t, int64(95000), e.balance(t, from.ID))
}

func countAction(xs []string, x string) int {
	n := 0
	for _, v := range xs {
		if v == x {
			n++
		}
	}
	return n
}

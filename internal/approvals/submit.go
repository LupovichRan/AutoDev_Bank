package approvals

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/LupovichRan/AutoDev_Bank/internal/db"
	"github.com/LupovichRan/AutoDev_Bank/internal/ids"
	"github.com/LupovichRan/AutoDev_Bank/internal/ledger"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
)

// submitResult is what submitTx decided; SubmitTransfer turns it into an
// Outcome after commit.
type submitResult struct {
	posted     *ledger.Transaction // fresh or replayed posting read in the tx
	postedID   uuid.UUID           // replayed posting found on the approval path
	approvalID uuid.UUID
}

// SubmitTransfer authorizes the initiator (a holder of the from-account, or
// staff with payments.initiator), evaluates the policy and records the
// evaluation in control.decision_log. When no approval is required it posts
// the transfer at once; otherwise it creates a pending approval holding in.
//
// Authorization, the holder set and the policy decision are all read inside
// the transaction that acts on them, under locks (see Service).
//
// The idempotency key is one namespace across both paths: resubmitting a key
// with the same input returns the original posting or approval whichever
// path the policy would now pick, and reusing it with a different input
// fails with ledger.ErrIdempotencyConflict.
func (s *Service) SubmitTransfer(ctx context.Context, in TransferInput) (Outcome, error) {
	if err := validateTransfer(in); err != nil {
		return Outcome{}, err
	}
	var logIn map[string]any
	var res submitResult
	err := db.WithTxLevel(ctx, s.pool, pgx.ReadCommitted, func(ctx context.Context, tx pgx.Tx) error {
		logIn = map[string]any{"transfer": in}
		res = submitResult{}
		return s.submitTx(ctx, tx, in, logIn, &res)
	})
	if err != nil {
		err = ledger.MapError(err)
		s.logDenied(ctx, in.InitiatorID, "transfer.submit", logIn, err)
		return Outcome{}, err
	}
	switch {
	case res.approvalID != uuid.Nil:
		ap, err := s.Get(ctx, res.approvalID)
		if err != nil {
			return Outcome{}, err
		}
		return Outcome{Approval: &ap}, nil
	case res.posted != nil:
		return Outcome{Posted: res.posted}, nil
	default:
		t, err := s.ledger.Transaction(ctx, res.postedID)
		if err != nil {
			return Outcome{}, err
		}
		return Outcome{Posted: &t}, nil
	}
}

func (s *Service) submitTx(ctx context.Context, tx pgx.Tx, in TransferInput, logIn map[string]any, res *submitResult) error {
	// 1. accounts, 2. holders and the initiator's roles, all locked.
	accts, err := lockAccountRows(ctx, tx, in)
	if err != nil {
		return err
	}
	holders, err := lockHolders(ctx, tx, in.FromAccountID)
	if err != nil {
		return err
	}
	var initiatorKind string
	err = tx.QueryRow(ctx, `select kind from control.principals where id = $1`, in.InitiatorID).Scan(&initiatorKind)
	if err != nil && !errors.Is(err, pgx.ErrNoRows) {
		return fmt.Errorf("approvals: load initiator: %w", err)
	}
	hasRole, err := lockRole(ctx, tx, in.InitiatorID, RoleInitiator)
	if err != nil {
		return err
	}
	// R2: a customer debits only accounts they hold; staff need
	// payments.initiator. An unknown from-account is refused the same way
	// as someone else's, so the answer reveals nothing about it.
	from, fromExists := accts[in.FromAccountID]
	isHolder := isHolderOf(holders, in.InitiatorID)
	allowed := isHolder || (hasRole && initiatorKind != "customer")
	if initiatorKind == "" || !fromExists || !allowed {
		return fmt.Errorf("%w: principal %s may not initiate transfers from account %s",
			ErrNotAuthorized, in.InitiatorID, in.FromAccountID)
	}
	if from.currency != in.Currency {
		return fmt.Errorf("%w: account %s is %s, transfer is %s", ledger.ErrCurrencyMismatch, in.FromAccountID, from.currency, in.Currency)
	}
	// The destination must exist, be in the transfer's currency and, for a
	// customer initiator, be a payee (an account with a holder, not one of
	// the bank's internal accounts). Every failure is the same error.
	to, toExists := accts[in.ToAccountID]
	if !toExists || to.currency != in.Currency {
		return fmt.Errorf("%w: %s", ErrInvalidDestination, in.ToAccountID)
	}
	if initiatorKind == "customer" {
		n, err := holderCount(ctx, tx, in.ToAccountID)
		if err != nil {
			return err
		}
		if n == 0 {
			return fmt.Errorf("%w: %s", ErrInvalidDestination, in.ToAccountID)
		}
	}

	customers := 0
	for _, h := range holders {
		if h.kind == "customer" {
			customers++
		}
	}
	d := s.policy.Evaluate(Input{
		Amount: in.Amount, Currency: in.Currency, InitiatorID: in.InitiatorID,
		HolderIDs: holderIDs(holders), AccountKind: from.kind,
		InitiatorKind: initiatorKind, CustomerHolders: customers,
		InitiatorIsHolder: isHolder, FromAllowNegative: from.allowNegative, FromHolderCount: len(holders),
	})
	logIn["required"] = d.Required
	logIn["requires_holder"] = d.RequiresHolder
	logIn["holders"] = len(holders)

	// 3. The idempotency key spans ledger.transactions and control.approvals;
	// serialise submissions of one key so the cross-table check is exact.
	if _, err := tx.Exec(ctx, `select pg_advisory_xact_lock(hashtextextended('approvals.transfer:' || $1, 0))`,
		in.IdempotencyKey); err != nil {
		return fmt.Errorf("approvals: lock idempotency key: %w", err)
	}
	if d.Required == 0 {
		return s.postNow(ctx, tx, in, d, logIn, res)
	}
	return s.createApproval(ctx, tx, in, d, logIn, res)
}

// replayApproval handles a key that already names an approval: the same
// input returns it, a different input is a conflict. ok is false when no
// approval holds the key.
func replayApproval(ctx context.Context, tx pgx.Tx, in TransferInput, d Decision, logIn map[string]any, res *submitResult) (ok bool, err error) {
	existing, err := getApproval(ctx, tx, `a.subject->>'idempotency_key' = $1`, in.IdempotencyKey)
	if errors.Is(err, ErrNotFound) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	if existing.Subject != in {
		return true, fmt.Errorf("%w: key %q is approval %s", ledger.ErrIdempotencyConflict, in.IdempotencyKey, existing.ID)
	}
	res.approvalID = existing.ID
	logIn["approval_id"] = existing.ID.String()
	return true, writeLog(ctx, tx, in.InitiatorID, "transfer.submit", logIn, outcomeApprovalCreated,
		"idempotent replay of approval; "+d.Reason)
}

// postNow posts an approval-free transfer; the audit append is the last
// statement (see the ledger package's isolation rule).
func (s *Service) postNow(ctx context.Context, tx pgx.Tx, in TransferInput, d Decision, logIn map[string]any, res *submitResult) error {
	// A key already parked as an approval stays on the approval path.
	if ok, err := replayApproval(ctx, tx, in, d, logIn, res); ok || err != nil {
		return err
	}
	var started time.Time
	if err := tx.QueryRow(ctx, `select transaction_timestamp()`).Scan(&started); err != nil {
		return err
	}
	t, err := s.ledger.PostTx(ctx, tx, postRequest(in, map[string]any{"initiator_id": in.InitiatorID.String()}))
	if err != nil {
		return err
	}
	res.posted = &t
	// ledger.transactions.created_at defaults to now(), the transaction start:
	// an older row is a replay returned by PostTx, and is not re-audited.
	replayed := t.CreatedAt.Before(started)
	reason := d.Reason
	if replayed {
		reason = "idempotent replay; " + reason
	}
	if err := writeLog(ctx, tx, in.InitiatorID, "transfer.submit", logIn, outcomePosted, reason); err != nil {
		return err
	}
	if replayed {
		return nil
	}
	return s.appendAudit(ctx, tx, in.InitiatorID, "transfer.posted", "ledger_transaction:"+t.ID.String(), map[string]any{
		"amount": in.Amount, "currency": in.Currency,
		"from_account_id": in.FromAccountID.String(), "to_account_id": in.ToAccountID.String(),
		"idempotency_key": in.IdempotencyKey, "policy": d.Reason,
	})
}

// createApproval stores a pending approval.
func (s *Service) createApproval(ctx context.Context, tx pgx.Tx, in TransferInput, d Decision, logIn map[string]any, res *submitResult) error {
	if ok, err := replayApproval(ctx, tx, in, d, logIn, res); ok || err != nil {
		return err
	}
	// A key already posted directly: the same transfer returns that posting.
	postedID, same, err := postedTransfer(ctx, tx, in)
	if err != nil {
		return err
	}
	if postedID != uuid.Nil {
		if !same {
			return fmt.Errorf("%w: key %q is ledger transaction %s", ledger.ErrIdempotencyConflict, in.IdempotencyKey, postedID)
		}
		res.postedID = postedID
		logIn["ledger_transaction_id"] = postedID.String()
		return writeLog(ctx, tx, in.InitiatorID, "transfer.submit", logIn, outcomePosted,
			"idempotent replay of posting; "+d.Reason)
	}

	subject, err := json.Marshal(in)
	if err != nil {
		return err
	}
	id := ids.New()
	if _, err := tx.Exec(ctx, `insert into control.approvals
		  (id, kind, subject, amount, currency, initiator_id, required_approvals, requires_holder_approval)
		values ($1, $2, $3::jsonb, $4, $5, $6, $7, $8)`,
		id, KindTransfer, string(subject), in.Amount, in.Currency, in.InitiatorID, d.Required, d.RequiresHolder); err != nil {
		return mapError(err)
	}
	res.approvalID = id
	logIn["approval_id"] = id.String()
	if err := writeLog(ctx, tx, in.InitiatorID, "transfer.submit", logIn, outcomeApprovalCreated, d.Reason); err != nil {
		return err
	}
	return s.appendAudit(ctx, tx, in.InitiatorID, "approval.created", "approval:"+id.String(), map[string]any{
		"amount": in.Amount, "currency": in.Currency,
		"from_account_id": in.FromAccountID.String(), "to_account_id": in.ToAccountID.String(),
		"required_approvals": d.Required, "requires_holder_approval": d.RequiresHolder, "policy": d.Reason,
	})
}

// postedTransfer looks up a ledger transaction under in's idempotency key and
// reports whether it is exactly this transfer: same description, initiator,
// and the two lines from -> to for the amount in the currency.
func postedTransfer(ctx context.Context, tx pgx.Tx, in TransferInput) (id uuid.UUID, same bool, err error) {
	err = tx.QueryRow(ctx, `select t.id,
			t.description = $2
			and coalesce(t.metadata->>'initiator_id', '') = $3
			and (select count(*) from ledger.entries e where e.transaction_id = t.id) = 2
			and exists (select 1 from ledger.entries e where e.transaction_id = t.id
			            and e.account_id = $4 and e.amount = -$6::bigint and e.currency = $7)
			and exists (select 1 from ledger.entries e where e.transaction_id = t.id
			            and e.account_id = $5 and e.amount = $6::bigint and e.currency = $7)
		from ledger.transactions t where t.idempotency_key = $1`,
		in.IdempotencyKey, in.Description, in.InitiatorID.String(), in.FromAccountID, in.ToAccountID,
		in.Amount, in.Currency).Scan(&id, &same)
	if errors.Is(err, pgx.ErrNoRows) {
		return uuid.Nil, false, nil
	}
	if err != nil {
		return uuid.Nil, false, fmt.Errorf("approvals: idempotency lookup: %w", err)
	}
	return id, same, nil
}

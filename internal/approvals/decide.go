package approvals

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"

	"github.com/LupovichRan/AutoDev_Bank/internal/db"
	"github.com/LupovichRan/AutoDev_Bank/internal/ledger"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
)

// Decide records approverID's decision on the approval. The approver must be
// a holder of the from-account or hold payments.approver, and must not be the
// initiator. A rejection settles the approval as rejected. An approval
// executes once it has required_approvals approvals and, when
// requires_holder_approval is set (joint accounts), at least one of them is
// from a holder of the from-account other than the initiator; staff
// approvals count toward the quorum but never replace that holder.
// Execution happens in the same READ COMMITTED transaction: approved, then
// posted to the ledger with the stored idempotency key, then executed. Every
// call is recorded in decision_log, refusals as "denied".
func (s *Service) Decide(ctx context.Context, approvalID, approverID uuid.UUID, approve bool, reason string) (Approval, error) {
	decision := "reject"
	if approve {
		decision = "approve"
	}
	logIn := map[string]any{"approval_id": approvalID.String(), "decision": decision, "reason": reason}
	err := db.WithTxLevel(ctx, s.pool, pgx.ReadCommitted, func(ctx context.Context, tx pgx.Tx) error {
		return s.decideTx(ctx, tx, approvalID, approverID, approve, decision, reason, logIn)
	})
	if err != nil {
		err = ledger.MapError(mapError(err))
		s.logDenied(ctx, approverID, "approval.decide", logIn, err)
		return Approval{}, err
	}
	return s.Get(ctx, approvalID)
}

func (s *Service) decideTx(ctx context.Context, tx pgx.Tx, approvalID, approverID uuid.UUID,
	approve bool, decision, reason string, logIn map[string]any) error {
	// The request columns are immutable (trigger approvals_state_machine), so
	// reading them before taking any lock is safe; they name the accounts to
	// lock first.
	var ap Approval
	var subject []byte
	err := tx.QueryRow(ctx, `select initiator_id, required_approvals, requires_holder_approval, subject
		from control.approvals where id = $1`, approvalID).
		Scan(&ap.InitiatorID, &ap.RequiredApprovals, &ap.RequiresHolderApproval, &subject)
	if errors.Is(err, pgx.ErrNoRows) {
		return fmt.Errorf("%w: approval %s", ErrNotFound, approvalID)
	}
	if err != nil {
		return fmt.Errorf("approvals: load approval: %w", err)
	}
	if err := json.Unmarshal(subject, &ap.Subject); err != nil {
		return fmt.Errorf("approvals: decode subject: %w", err)
	}

	// Lock order: accounts -> holders/roles -> approval row -> ledger -> audit.
	if _, err := lockTransferAccounts(ctx, tx, ap.Subject); err != nil {
		return err
	}
	holders, err := lockHolders(ctx, tx, ap.Subject.FromAccountID)
	if err != nil {
		return err
	}
	isApprover, err := lockRole(ctx, tx, approverID, RoleApprover)
	if err != nil {
		return err
	}
	var early bool
	if err := tx.QueryRow(ctx, `select status, coalesce(not_before > now(), false)
		from control.approvals where id = $1 for update`, approvalID).Scan(&ap.Status, &early); err != nil {
		return fmt.Errorf("approvals: lock approval: %w", err)
	}

	if approverID == ap.InitiatorID {
		return ErrSelfApproval
	}
	if !isHolderOf(holders, approverID) && !isApprover {
		return fmt.Errorf("%w: principal %s may not decide approval %s", ErrNotAuthorized, approverID, approvalID)
	}
	if ap.Status != StatusPending {
		return fmt.Errorf("%w: status is %s", ErrNotPending, ap.Status)
	}
	if early && approve { // not_before delays execution; a rejection may come any time
		return ErrNotBefore
	}
	var decided bool
	if err := tx.QueryRow(ctx, `select exists(select 1 from control.approval_decisions
		where approval_id = $1 and approver_id = $2)`, approvalID, approverID).Scan(&decided); err != nil {
		return err
	}
	if decided {
		return ErrAlreadyDecided
	}

	// The decisions_dynamic_sod trigger re-checks self-approval and status.
	if _, err := tx.Exec(ctx, `insert into control.approval_decisions (approval_id, approver_id, decision, reason)
		values ($1, $2, $3, $4)`, approvalID, approverID, decision, reason); err != nil {
		return mapError(err)
	}
	subjectRef := "approval:" + approvalID.String()
	payload := map[string]any{"reason": reason, "amount": ap.Subject.Amount, "currency": ap.Subject.Currency}

	if !approve {
		if _, err := tx.Exec(ctx, `update control.approvals set status = 'rejected' where id = $1`, approvalID); err != nil {
			return err
		}
		if err := writeLog(ctx, tx, approverID, "approval.decide", logIn, outcomeRejected, reason); err != nil {
			return err
		}
		return s.appendAudit(ctx, tx, approverID, "approval.rejected", subjectRef, payload)
	}

	// Count approvals, and those from a co-holder (a holder other than the
	// initiator) against the holder set locked above.
	var approvals, holderApprovals int
	if err := tx.QueryRow(ctx, `select count(*),
			count(*) filter (where approver_id = any($2) and approver_id <> $3)
		from control.approval_decisions where approval_id = $1 and decision = 'approve'`,
		approvalID, holderIDs(holders), ap.InitiatorID).Scan(&approvals, &holderApprovals); err != nil {
		return err
	}
	payload["approvals"] = approvals
	payload["required_approvals"] = ap.RequiredApprovals
	payload["requires_holder_approval"] = ap.RequiresHolderApproval
	needHolder := ap.RequiresHolderApproval && holderApprovals == 0
	if approvals < ap.RequiredApprovals || needHolder {
		why := fmt.Sprintf("%d of %d approvals", approvals, ap.RequiredApprovals)
		if needHolder {
			why += "; awaiting another account holder"
		}
		if err := writeLog(ctx, tx, approverID, "approval.decide", logIn, outcomeApproved, why); err != nil {
			return err
		}
		return s.appendAudit(ctx, tx, approverID, "approval.approved", subjectRef, payload)
	}

	// Quorum reached: execute in this transaction.
	if _, err := tx.Exec(ctx, `update control.approvals set status = 'approved' where id = $1`, approvalID); err != nil {
		return err
	}
	t, err := s.ledger.PostTx(ctx, tx, postRequest(ap.Subject, map[string]any{
		"initiator_id": ap.Subject.InitiatorID.String(), "approval_id": approvalID.String(),
	}))
	if err != nil {
		return err
	}
	if _, err := tx.Exec(ctx, `update control.approvals set status = 'executed', ledger_transaction_id = $2
		where id = $1`, approvalID, t.ID); err != nil {
		return err
	}
	why := fmt.Sprintf("%d of %d approvals; posted %s", approvals, ap.RequiredApprovals, t.ID)
	if err := writeLog(ctx, tx, approverID, "approval.decide", logIn, outcomeExecuted, why); err != nil {
		return err
	}
	payload["ledger_transaction_id"] = t.ID.String()
	return s.appendAudit(ctx, tx, approverID, "approval.executed", subjectRef, payload)
}

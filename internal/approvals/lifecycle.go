package approvals

import (
	"context"
	"errors"
	"fmt"

	"github.com/LupovichRan/AutoDev_Bank/internal/audit"
	"github.com/LupovichRan/AutoDev_Bank/internal/db"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
)

// CancelReason is the decision-log reason recorded when an initiator
// cancels their own pending approval.
const CancelReason = "cancelled by initiator"

// Cancel lets the initiator withdraw a pending approval: it becomes
// rejected (no decision row is written, since the initiator may never
// decide their own approval), the decision log records CancelReason and
// approval.cancelled is audited. Anyone else gets ErrNotAuthorized, an
// unknown id ErrNotFound and a settled approval ErrNotPending.
//
// Cancel locks only the approval row (then the audit head), a suffix of the
// global lock order, so it cannot deadlock with Decide.
func (s *Service) Cancel(ctx context.Context, approvalID, initiatorID uuid.UUID) (Approval, error) {
	logIn := map[string]any{"approval_id": approvalID.String()}
	err := db.WithTxLevel(ctx, s.pool, pgx.ReadCommitted, func(ctx context.Context, tx pgx.Tx) error {
		var initiator uuid.UUID
		var status string
		var amount int64
		var currency string
		err := tx.QueryRow(ctx, `select initiator_id, status, amount, currency from control.approvals
			where id = $1 for update`, approvalID).Scan(&initiator, &status, &amount, &currency)
		if errors.Is(err, pgx.ErrNoRows) {
			return fmt.Errorf("%w: approval %s", ErrNotFound, approvalID)
		}
		if err != nil {
			return fmt.Errorf("approvals: lock approval: %w", err)
		}
		if initiator != initiatorID {
			return fmt.Errorf("%w: only the initiator may cancel approval %s", ErrNotAuthorized, approvalID)
		}
		if status != StatusPending {
			return fmt.Errorf("%w: status is %s", ErrNotPending, status)
		}
		if _, err := tx.Exec(ctx, `update control.approvals set status = 'rejected' where id = $1`, approvalID); err != nil {
			return mapError(err)
		}
		if err := writeLog(ctx, tx, initiatorID, "approval.cancel", logIn, outcomeRejected, CancelReason); err != nil {
			return err
		}
		return s.appendAudit(ctx, tx, initiatorID, "approval.cancelled", "approval:"+approvalID.String(), map[string]any{
			"reason": CancelReason, "amount": amount, "currency": currency,
		})
	})
	if err != nil {
		err = mapError(err)
		s.logDenied(ctx, initiatorID, "approval.cancel", logIn, err)
		return Approval{}, err
	}
	return s.Get(ctx, approvalID)
}

// ExpireApprovals marks every pending approval past its expires_at as
// expired, each in its own transaction with a decision-log row and an
// approval.expired audit event (no actor: the system did it). It returns
// how many it expired; failures are joined and do not stop the others.
func (s *Service) ExpireApprovals(ctx context.Context) (int, error) {
	rows, err := s.pool.Query(ctx, `select id from control.approvals
		where status = 'pending' and expires_at <= now() order by expires_at`)
	if err != nil {
		return 0, fmt.Errorf("approvals: list expired: %w", err)
	}
	due, err := pgx.CollectRows(rows, pgx.RowTo[uuid.UUID])
	if err != nil {
		return 0, fmt.Errorf("approvals: list expired: %w", err)
	}
	var (
		n    int
		errs []error
	)
	for _, id := range due {
		if err := ctx.Err(); err != nil {
			errs = append(errs, err)
			break
		}
		var done bool
		err := db.WithTxLevel(ctx, s.pool, pgx.ReadCommitted, func(ctx context.Context, tx pgx.Tx) error {
			done = false
			tag, err := tx.Exec(ctx, `update control.approvals set status = 'expired'
				where id = $1 and status = 'pending' and expires_at <= now()`, id)
			if err != nil || tag.RowsAffected() == 0 {
				return err // decided or cancelled meanwhile
			}
			done = true
			if _, err := tx.Exec(ctx, `insert into control.decision_log (principal_id, action, input, outcome, reason)
				values (null, 'approval.expire', jsonb_build_object('approval_id', $1::text), 'expired', 'expired after 7 days')`,
				id.String()); err != nil {
				return fmt.Errorf("approvals: decision log: %w", err)
			}
			if s.audit == nil {
				return nil
			}
			_, err = s.audit.Append(ctx, tx, audit.Event{Action: "approval.expired", Subject: "approval:" + id.String(),
				Payload: map[string]any{"reason": "expired"}})
			return err
		})
		if err != nil {
			errs = append(errs, fmt.Errorf("approvals: expire %s: %w", id, err))
			continue
		}
		if done {
			n++
		}
	}
	return n, errors.Join(errs...)
}

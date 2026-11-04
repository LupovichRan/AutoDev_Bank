package approvals

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
)

const approvalCols = `a.id, a.kind, a.subject, a.amount, a.currency, a.initiator_id, a.required_approvals,
	a.requires_holder_approval, a.not_before, a.status, a.created_at, a.ledger_transaction_id`

func scanApproval(row pgx.Row) (Approval, error) {
	var ap Approval
	var subject []byte
	err := row.Scan(&ap.ID, &ap.Kind, &subject, &ap.Amount, &ap.Currency, &ap.InitiatorID, &ap.RequiredApprovals,
		&ap.RequiresHolderApproval, &ap.NotBefore, &ap.Status, &ap.CreatedAt, &ap.LedgerTransactionID)
	if err != nil {
		return Approval{}, err
	}
	if err := json.Unmarshal(subject, &ap.Subject); err != nil {
		return Approval{}, fmt.Errorf("approvals: decode subject: %w", err)
	}
	return ap, nil
}

type rowQuerier interface {
	Query(ctx context.Context, sql string, args ...any) (pgx.Rows, error)
}

func getApproval(ctx context.Context, q rowQuerier, where string, args ...any) (Approval, error) {
	aps, err := listApprovals(ctx, q, where+` order by a.created_at, a.id limit 1`, args...)
	if err != nil {
		return Approval{}, err
	}
	if len(aps) == 0 {
		return Approval{}, fmt.Errorf("%w: approval", ErrNotFound)
	}
	return aps[0], nil
}

// listApprovals returns the approvals matching where (with its order by),
// each with its decisions.
func listApprovals(ctx context.Context, q rowQuerier, where string, args ...any) ([]Approval, error) {
	rows, err := q.Query(ctx, `select `+approvalCols+` from control.approvals a where `+where, args...)
	if err != nil {
		return nil, fmt.Errorf("approvals: list: %w", err)
	}
	aps, err := pgx.CollectRows(rows, func(r pgx.CollectableRow) (Approval, error) { return scanApproval(r) })
	if err != nil {
		return nil, fmt.Errorf("approvals: list: %w", err)
	}
	if len(aps) == 0 {
		return aps, nil
	}
	idx := make(map[uuid.UUID]int, len(aps))
	idList := make([]uuid.UUID, len(aps))
	for i, ap := range aps {
		idx[ap.ID] = i
		idList[i] = ap.ID
	}
	rows, err = q.Query(ctx, `select approval_id, approver_id, decision, reason, at
		from control.approval_decisions where approval_id = any($1) order by at, approver_id`, idList)
	if err != nil {
		return nil, fmt.Errorf("approvals: decisions: %w", err)
	}
	defer rows.Close()
	for rows.Next() {
		var aid uuid.UUID
		var d ApprovalDecision
		if err := rows.Scan(&aid, &d.ApproverID, &d.Decision, &d.Reason, &d.At); err != nil {
			return nil, fmt.Errorf("approvals: decisions: %w", err)
		}
		i := idx[aid]
		aps[i].Decisions = append(aps[i].Decisions, d)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("approvals: decisions: %w", err)
	}
	return aps, nil
}

// Get returns one approval with its decisions, or ErrNotFound.
func (s *Service) Get(ctx context.Context, id uuid.UUID) (Approval, error) {
	return getApproval(ctx, s.pool, `a.id = $1`, id)
}

// Inbox returns the pending approvals principalID may decide: not initiated
// by them, not yet decided by them, and on an account they hold or (with
// payments.approver) any account. Oldest first.
func (s *Service) Inbox(ctx context.Context, principalID uuid.UUID) ([]Approval, error) {
	return listApprovals(ctx, s.pool, `a.status = 'pending' and a.initiator_id <> $1
		and not exists (select 1 from control.approval_decisions d where d.approval_id = a.id and d.approver_id = $1)
		and (exists (select 1 from control.account_holders h
		             where h.account_id = (a.subject->>'from_account_id')::uuid and h.principal_id = $1)
		     or exists (select 1 from control.roles r where r.principal_id = $1 and r.role = $2))
		order by a.created_at, a.id`, principalID, RoleApprover)
}

// ListForInitiator returns every approval principalID initiated, newest first.
func (s *Service) ListForInitiator(ctx context.Context, principalID uuid.UUID) ([]Approval, error) {
	return listApprovals(ctx, s.pool, `a.initiator_id = $1 order by a.created_at desc, a.id desc`, principalID)
}

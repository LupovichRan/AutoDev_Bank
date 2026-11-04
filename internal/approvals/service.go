package approvals

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/LupovichRan/AutoDev_Bank/internal/audit"
	"github.com/LupovichRan/AutoDev_Bank/internal/db"
	"github.com/LupovichRan/AutoDev_Bank/internal/ids"
	"github.com/LupovichRan/AutoDev_Bank/internal/ledger"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"
)

// Approval statuses, as stored in control.approvals.status.
const (
	StatusPending  = "pending"
	StatusApproved = "approved"
	StatusRejected = "rejected"
	StatusExecuted = "executed"
	StatusExpired  = "expired"
)

// KindTransfer is the only approval kind so far.
const KindTransfer = "transfer"

// Outcomes recorded in control.decision_log.outcome.
const (
	outcomePosted          = "posted"
	outcomeApprovalCreated = "approval_created"
	outcomeApproved        = "approved"
	outcomeRejected        = "rejected"
	outcomeExecuted        = "executed"
	outcomeDenied          = "denied"
)

// TransferInput is a requested transfer; it is stored verbatim as an
// approval's subject.
type TransferInput struct {
	IdempotencyKey string    `json:"idempotency_key"`
	InitiatorID    uuid.UUID `json:"initiator_id"`
	FromAccountID  uuid.UUID `json:"from_account_id"`
	ToAccountID    uuid.UUID `json:"to_account_id"`
	Amount         int64     `json:"amount"` // minor units, positive
	Currency       string    `json:"currency"`
	Description    string    `json:"description"`
}

// Outcome of SubmitTransfer: exactly one of Posted and Approval is set.
type Outcome struct {
	Posted   *ledger.Transaction
	Approval *Approval
}

// ApprovalDecision is one approver's recorded decision. (It is not named
// Decision because that is the policy's result type.)
type ApprovalDecision struct {
	ApproverID uuid.UUID
	Decision   string // approve, reject
	Reason     string
	At         time.Time
}

// Approval is a pending or settled maker-checker request.
type Approval struct {
	ID                  uuid.UUID
	Kind                string
	Subject             TransferInput
	Amount              int64
	Currency            string
	InitiatorID         uuid.UUID
	RequiredApprovals   int
	NotBefore           *time.Time
	Status              string
	CreatedAt           time.Time
	LedgerTransactionID *uuid.UUID
	Decisions           []ApprovalDecision
}

// Service runs the maker-checker workflow and the principal registry.
type Service struct {
	pool   *pgxpool.Pool
	ledger *ledger.Service
	audit  *audit.Recorder // nil disables audit appends
	policy Policy
}

// New returns a Service. audit may be nil (no audit events are appended).
func New(pool *pgxpool.Pool, ledger *ledger.Service, audit *audit.Recorder, policy Policy) *Service {
	return &Service{pool: pool, ledger: ledger, audit: audit, policy: policy}
}

// ---------------------------------------------------------------------------
// Submitting

// SubmitTransfer authorizes the initiator (a holder of the from-account, or
// staff with payments.initiator), evaluates the policy and records the
// evaluation in control.decision_log. When no approval is required it posts
// the transfer at once; otherwise it creates a pending approval holding in.
// Resubmitting the same IdempotencyKey with the same input returns the
// original posting or approval.
func (s *Service) SubmitTransfer(ctx context.Context, in TransferInput) (Outcome, error) {
	if err := validateTransfer(in); err != nil {
		return Outcome{}, err
	}
	logIn := map[string]any{"transfer": in}
	from, err := s.ledger.Account(ctx, in.FromAccountID)
	if err != nil {
		if errors.Is(err, ledger.ErrNotFound) {
			err = fmt.Errorf("%w: %w", ErrNotFound, err)
		}
		return Outcome{}, err
	}
	ok, err := eligible(ctx, s.pool, from.ID, in.InitiatorID, RoleInitiator)
	if err != nil {
		return Outcome{}, fmt.Errorf("approvals: authorize: %w", err)
	}
	if !ok {
		s.logDenied(ctx, in.InitiatorID, "transfer.submit", logIn, ErrNotAuthorized)
		return Outcome{}, fmt.Errorf("%w: principal %s may not initiate transfers from account %s", ErrNotAuthorized, in.InitiatorID, from.ID)
	}
	holders, err := s.holderIDs(ctx, from.ID)
	if err != nil {
		return Outcome{}, err
	}
	d := s.policy.Evaluate(Input{
		Amount: in.Amount, Currency: in.Currency, InitiatorID: in.InitiatorID,
		HolderIDs: holders, AccountKind: from.Kind,
	})
	logIn["required"] = d.Required
	logIn["holders"] = len(holders)

	var out Outcome
	if d.Required == 0 {
		var t ledger.Transaction
		t, err = s.postNow(ctx, in, d, logIn)
		out.Posted = &t
	} else {
		var ap Approval
		ap, err = s.createApproval(ctx, in, d, logIn)
		out.Approval = &ap
	}
	if err != nil {
		s.logDenied(ctx, in.InitiatorID, "transfer.submit", logIn, err)
		return Outcome{}, err
	}
	return out, nil
}

func validateTransfer(in TransferInput) error {
	switch {
	case in.IdempotencyKey == "":
		return fmt.Errorf("%w: idempotency key is required", ledger.ErrInvalidRequest)
	case in.InitiatorID == uuid.Nil:
		return fmt.Errorf("%w: initiator is required", ledger.ErrInvalidRequest)
	case in.Amount <= 0:
		return fmt.Errorf("%w: amount must be positive", ledger.ErrInvalidRequest)
	case in.FromAccountID == in.ToAccountID:
		return fmt.Errorf("%w: from and to accounts must differ", ledger.ErrInvalidRequest)
	}
	return nil
}

func postRequest(in TransferInput, meta map[string]any) ledger.PostRequest {
	return ledger.PostRequest{
		IdempotencyKey: in.IdempotencyKey,
		Description:    in.Description,
		Currency:       in.Currency,
		Lines: []ledger.Line{
			{AccountID: in.FromAccountID, Amount: -in.Amount},
			{AccountID: in.ToAccountID, Amount: in.Amount},
		},
		Metadata: meta,
	}
}

// postNow posts an approval-free transfer under READ COMMITTED; the audit
// append is the last statement (see the ledger package's isolation rule).
func (s *Service) postNow(ctx context.Context, in TransferInput, d Decision, logIn map[string]any) (ledger.Transaction, error) {
	var t ledger.Transaction
	err := db.WithTxLevel(ctx, s.pool, pgx.ReadCommitted, func(ctx context.Context, tx pgx.Tx) error {
		// A replayed key returns the original posting and is not re-audited.
		var replay bool
		if err := tx.QueryRow(ctx, `select exists(select 1 from ledger.transactions where idempotency_key = $1)`,
			in.IdempotencyKey).Scan(&replay); err != nil {
			return err
		}
		var err error
		t, err = s.ledger.PostTx(ctx, tx, postRequest(in, map[string]any{"initiator_id": in.InitiatorID.String()}))
		if err != nil {
			return err
		}
		reason := d.Reason
		if replay {
			reason = "idempotent replay; " + reason
		}
		if err := writeLog(ctx, tx, in.InitiatorID, "transfer.submit", logIn, outcomePosted, reason); err != nil {
			return err
		}
		if replay {
			return nil
		}
		return s.appendAudit(ctx, tx, in.InitiatorID, "transfer.posted", "ledger_transaction:"+t.ID.String(), map[string]any{
			"amount": in.Amount, "currency": in.Currency,
			"from_account_id": in.FromAccountID.String(), "to_account_id": in.ToAccountID.String(),
			"idempotency_key": in.IdempotencyKey, "policy": d.Reason,
		})
	})
	if err != nil {
		return ledger.Transaction{}, ledger.MapError(err)
	}
	return t, nil
}

// createApproval stores a pending approval (SERIALIZABLE control-plane write).
func (s *Service) createApproval(ctx context.Context, in TransferInput, d Decision, logIn map[string]any) (Approval, error) {
	subject, err := json.Marshal(in)
	if err != nil {
		return Approval{}, err
	}
	var id uuid.UUID
	err = db.WithTx(ctx, s.pool, func(ctx context.Context, tx pgx.Tx) error {
		id = ids.New()
		err := tx.QueryRow(ctx, `insert into control.approvals
			  (id, kind, subject, amount, currency, initiator_id, required_approvals)
			values ($1, $2, $3::jsonb, $4, $5, $6, $7)
			on conflict ((subject->>'idempotency_key')) do nothing
			returning id`,
			id, KindTransfer, string(subject), in.Amount, in.Currency, in.InitiatorID, d.Required).Scan(&id)
		if errors.Is(err, pgx.ErrNoRows) {
			existing, err := getApproval(ctx, tx, `a.subject->>'idempotency_key' = $1`, in.IdempotencyKey)
			if err != nil {
				return err
			}
			if existing.Subject != in {
				return fmt.Errorf("%w: approval %s", ledger.ErrIdempotencyConflict, existing.ID)
			}
			id = existing.ID
			return writeLog(ctx, tx, in.InitiatorID, "transfer.submit", logIn, outcomeApprovalCreated, "idempotent replay; "+d.Reason)
		}
		if err != nil {
			return mapError(err)
		}
		logIn["approval_id"] = id.String()
		if err := writeLog(ctx, tx, in.InitiatorID, "transfer.submit", logIn, outcomeApprovalCreated, d.Reason); err != nil {
			return err
		}
		return s.appendAudit(ctx, tx, in.InitiatorID, "approval.created", "approval:"+id.String(), map[string]any{
			"amount": in.Amount, "currency": in.Currency,
			"from_account_id": in.FromAccountID.String(), "to_account_id": in.ToAccountID.String(),
			"required_approvals": d.Required, "policy": d.Reason,
		})
	})
	if err != nil {
		return Approval{}, err
	}
	return s.Get(ctx, id)
}

// ---------------------------------------------------------------------------
// Deciding

// Decide records approverID's decision on the approval. The approver must be
// a holder of the from-account or hold payments.approver, and must not be the
// initiator. A rejection settles the approval as rejected. An approval that
// reaches required_approvals is executed in the same READ COMMITTED
// transaction: approved, then posted to the ledger with the stored
// idempotency key, then executed. Every call is recorded in decision_log,
// refusals as "denied".
func (s *Service) Decide(ctx context.Context, approvalID, approverID uuid.UUID, approve bool, reason string) (Approval, error) {
	// Execution moves money, so the approve path runs READ COMMITTED with
	// explicit locks; a rejection is a plain control-plane write.
	level := pgx.Serializable
	if approve {
		level = pgx.ReadCommitted
	}
	decision := "reject"
	if approve {
		decision = "approve"
	}
	logIn := map[string]any{"approval_id": approvalID.String(), "decision": decision, "reason": reason}
	err := db.WithTxLevel(ctx, s.pool, level, func(ctx context.Context, tx pgx.Tx) error {
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
	// Lock the approval row first: concurrent deciders queue here.
	var ap Approval
	var subject []byte
	var early bool
	err := tx.QueryRow(ctx, `select initiator_id, required_approvals, status, subject,
			coalesce(not_before > now(), false)
		from control.approvals where id = $1 for update`, approvalID).
		Scan(&ap.InitiatorID, &ap.RequiredApprovals, &ap.Status, &subject, &early)
	if errors.Is(err, pgx.ErrNoRows) {
		return fmt.Errorf("%w: approval %s", ErrNotFound, approvalID)
	}
	if err != nil {
		return fmt.Errorf("approvals: lock approval: %w", err)
	}
	if err := json.Unmarshal(subject, &ap.Subject); err != nil {
		return fmt.Errorf("approvals: decode subject: %w", err)
	}

	if approverID == ap.InitiatorID {
		return ErrSelfApproval
	}
	ok, err := eligible(ctx, tx, ap.Subject.FromAccountID, approverID, RoleApprover)
	if err != nil {
		return fmt.Errorf("approvals: authorize: %w", err)
	}
	if !ok {
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

	var approvals int
	if err := tx.QueryRow(ctx, `select count(*) from control.approval_decisions
		where approval_id = $1 and decision = 'approve'`, approvalID).Scan(&approvals); err != nil {
		return err
	}
	payload["approvals"] = approvals
	payload["required_approvals"] = ap.RequiredApprovals
	if approvals < ap.RequiredApprovals {
		why := fmt.Sprintf("%d of %d approvals", approvals, ap.RequiredApprovals)
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

// ---------------------------------------------------------------------------
// Reading

const approvalCols = `a.id, a.kind, a.subject, a.amount, a.currency, a.initiator_id, a.required_approvals,
	a.not_before, a.status, a.created_at, a.ledger_transaction_id`

func scanApproval(row pgx.Row) (Approval, error) {
	var ap Approval
	var subject []byte
	err := row.Scan(&ap.ID, &ap.Kind, &subject, &ap.Amount, &ap.Currency, &ap.InitiatorID, &ap.RequiredApprovals,
		&ap.NotBefore, &ap.Status, &ap.CreatedAt, &ap.LedgerTransactionID)
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

// ---------------------------------------------------------------------------
// Helpers

func (s *Service) holderIDs(ctx context.Context, accountID uuid.UUID) ([]uuid.UUID, error) {
	rows, err := s.pool.Query(ctx, `select principal_id from control.account_holders
		where account_id = $1 order by principal_id`, accountID)
	if err != nil {
		return nil, fmt.Errorf("approvals: holders: %w", err)
	}
	out, err := pgx.CollectRows(rows, pgx.RowTo[uuid.UUID])
	if err != nil {
		return nil, fmt.Errorf("approvals: holders: %w", err)
	}
	return out, nil
}

func (s *Service) appendAudit(ctx context.Context, tx pgx.Tx, actor uuid.UUID, action, subject string, payload map[string]any) error {
	if s.audit == nil {
		return nil
	}
	_, err := s.audit.Append(ctx, tx, audit.Event{ActorID: &actor, Action: action, Subject: subject, Payload: payload})
	return err
}

type execer interface {
	Exec(ctx context.Context, sql string, args ...any) (pgconn.CommandTag, error)
}

func writeLog(ctx context.Context, q execer, principalID uuid.UUID, action string, input map[string]any, outcome, reason string) error {
	in, err := json.Marshal(input)
	if err != nil {
		return err
	}
	if _, err := q.Exec(ctx, `insert into control.decision_log (principal_id, action, input, outcome, reason)
		values ($1, $2, $3::jsonb, $4, $5)`, principalID, action, string(in), outcome, reason); err != nil {
		return fmt.Errorf("approvals: decision log: %w", err)
	}
	return nil
}

// logDenied records a refused request in its own statement, since the
// request's transaction (if any) was rolled back. It is best effort.
func (s *Service) logDenied(ctx context.Context, principalID uuid.UUID, action string, input map[string]any, cause error) {
	_ = writeLog(ctx, s.pool, principalID, action, input, outcomeDenied, cause.Error())
}

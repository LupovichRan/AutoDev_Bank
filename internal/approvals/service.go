package approvals

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"time"

	"github.com/LupovichRan/AutoDev_Bank/internal/audit"
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
	ID                uuid.UUID
	Kind              string
	Subject           TransferInput
	Amount            int64
	Currency          string
	InitiatorID       uuid.UUID
	RequiredApprovals int
	// RequiresHolderApproval: one approval must come from a holder of the
	// from-account other than the initiator (joint-account rule).
	RequiresHolderApproval bool
	NotBefore              *time.Time
	Status                 string
	CreatedAt              time.Time
	// ExpiresAt: a pending approval not executed by then expires (R3: 7 days).
	ExpiresAt           time.Time
	LedgerTransactionID *uuid.UUID
	Decisions           []ApprovalDecision
}

// Service runs the maker-checker workflow and the principal registry.
//
// Every money-moving or policy-deciding transaction runs READ COMMITTED with
// explicit locks taken in one global order:
//
//	ledger.accounts (from/to, ascending id, FOR UPDATE)
//	-> control.account_holders / control.roles (FOR SHARE)
//	-> the transfer's idempotency key (advisory) or the approval row (FOR UPDATE)
//	-> ledger.PostTx
//	-> audit.Append, always the last statement.
//
// Locking the from-account FOR UPDATE also blocks a concurrent AddHolder for
// that account (its foreign-key check needs KEY SHARE on the account row),
// so the holder set read under it cannot change before commit.
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
// Helpers

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

// lockedAccount is one transfer account as read under its row lock.
type lockedAccount struct {
	kind, currency string
	allowNegative  bool
}

// lockAccountRows locks both accounts of a transfer FOR UPDATE in ascending
// id order (the order ledger.PostTx uses, so a later PostTx in the same
// transaction takes no new account lock and opposite transfers cannot
// deadlock) and returns those that exist.
func lockAccountRows(ctx context.Context, tx pgx.Tx, in TransferInput) (map[uuid.UUID]lockedAccount, error) {
	rows, err := tx.Query(ctx, `select id, kind, currency, allow_negative from ledger.accounts
		where id = any($1) order by id for update`, []uuid.UUID{in.FromAccountID, in.ToAccountID})
	if err != nil {
		return nil, fmt.Errorf("approvals: lock accounts: %w", err)
	}
	found := map[uuid.UUID]lockedAccount{}
	for rows.Next() {
		var id uuid.UUID
		var a lockedAccount
		if err := rows.Scan(&id, &a.kind, &a.currency, &a.allowNegative); err != nil {
			rows.Close()
			return nil, fmt.Errorf("approvals: lock accounts: %w", err)
		}
		found[id] = a
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("approvals: lock accounts: %w", err)
	}
	return found, nil
}

// lockTransferAccounts is lockAccountRows plus the check that both accounts
// exist and are in the transfer's currency (used when deciding a stored
// approval, whose accounts were validated at submission).
func lockTransferAccounts(ctx context.Context, tx pgx.Tx, in TransferInput) error {
	found, err := lockAccountRows(ctx, tx, in)
	if err != nil {
		return err
	}
	for _, id := range []uuid.UUID{in.FromAccountID, in.ToAccountID} {
		a, ok := found[id]
		if !ok {
			return fmt.Errorf("%w: %w: account %s", ErrNotFound, ledger.ErrNotFound, id)
		}
		if a.currency != in.Currency {
			return fmt.Errorf("%w: account %s is %s, transfer is %s", ledger.ErrCurrencyMismatch, id, a.currency, in.Currency)
		}
	}
	return nil
}

// holderCount counts an account's holders (no lock: used for the to-account,
// whose holder set only decides whether it is a payee).
func holderCount(ctx context.Context, tx pgx.Tx, accountID uuid.UUID) (int, error) {
	var n int
	if err := tx.QueryRow(ctx, `select count(*) from control.account_holders where account_id = $1`, accountID).Scan(&n); err != nil {
		return 0, fmt.Errorf("approvals: holders: %w", err)
	}
	return n, nil
}

// holder is one holder of an account, as read under lock.
type holder struct {
	id   uuid.UUID
	kind string
}

// lockHolders returns the account's holders, locking their holder rows FOR
// SHARE. Call it after lockTransferAccounts.
func lockHolders(ctx context.Context, tx pgx.Tx, accountID uuid.UUID) ([]holder, error) {
	rows, err := tx.Query(ctx, `select h.principal_id, p.kind
		from control.account_holders h join control.principals p on p.id = h.principal_id
		where h.account_id = $1 order by h.principal_id for share of h`, accountID)
	if err != nil {
		return nil, fmt.Errorf("approvals: holders: %w", err)
	}
	hs, err := pgx.CollectRows(rows, func(r pgx.CollectableRow) (holder, error) {
		var h holder
		err := r.Scan(&h.id, &h.kind)
		return h, err
	})
	if err != nil {
		return nil, fmt.Errorf("approvals: holders: %w", err)
	}
	return hs, nil
}

func holderIDs(hs []holder) []uuid.UUID {
	out := make([]uuid.UUID, len(hs))
	for i, h := range hs {
		out[i] = h.id
	}
	return out
}

func isHolderOf(hs []holder, id uuid.UUID) bool {
	for _, h := range hs {
		if h.id == id {
			return true
		}
	}
	return false
}

// lockRole reports whether the principal holds role, locking its role rows
// FOR SHARE so they cannot be revoked before commit.
func lockRole(ctx context.Context, tx pgx.Tx, principalID uuid.UUID, role string) (bool, error) {
	rows, err := tx.Query(ctx, `select role from control.roles where principal_id = $1 for share`, principalID)
	if err != nil {
		return false, fmt.Errorf("approvals: roles: %w", err)
	}
	roles, err := pgx.CollectRows(rows, pgx.RowTo[string])
	if err != nil {
		return false, fmt.Errorf("approvals: roles: %w", err)
	}
	for _, r := range roles {
		if r == role {
			return true, nil
		}
	}
	return false, nil
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

// denyLogTimeout bounds the out-of-transaction refusal log write.
const denyLogTimeout = 5 * time.Second

// logDenied records a refused request in its own statement, since the
// request's transaction (if any) was rolled back. It runs even when the
// caller's context is already cancelled (a refusal caused by cancellation
// is still worth recording) and reports its own failure through slog rather
// than to the caller, whose error is the refusal.
func (s *Service) logDenied(ctx context.Context, principalID uuid.UUID, action string, input map[string]any, cause error) {
	ctx, cancel := context.WithTimeout(context.WithoutCancel(ctx), denyLogTimeout)
	defer cancel()
	if err := writeLog(ctx, s.pool, principalID, action, input, outcomeDenied, cause.Error()); err != nil {
		slog.ErrorContext(ctx, "approvals: could not record denied decision",
			"action", action, "principal_id", principalID, "cause", cause, "err", err)
	}
}

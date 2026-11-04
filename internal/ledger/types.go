package ledger

import (
	"time"

	"github.com/google/uuid"
)

// Transaction statuses, as stored in ledger.transactions.status.
const (
	StatusPending = "pending"
	StatusPosted  = "posted"
	StatusVoided  = "voided"
)

// Line is one leg of a posting. Amount is added to the account's balance:
// a transfer of 500 from A to B is []Line{{A, -500}, {B, +500}}.
type Line struct {
	AccountID uuid.UUID
	Amount    int64
}

// PostRequest describes a balanced journal transaction to record.
type PostRequest struct {
	// IdempotencyKey identifies the request; replaying the same key with the
	// same payload returns the original transaction.
	IdempotencyKey string
	Description    string
	// EffectiveDate is the accounting date (only its calendar date in its own
	// location is used). The zero value means today in UTC.
	EffectiveDate time.Time
	Lines         []Line
	Currency      string
	// Pending creates a hold: funds are reserved until Resolve posts or voids it.
	Pending   bool
	ExpiresAt *time.Time
	Metadata  map[string]any
}

// Transaction is a journal transaction and its entries.
type Transaction struct {
	ID             uuid.UUID
	IdempotencyKey string
	Description    string
	Status         string
	EffectiveDate  time.Time
	CreatedAt      time.Time
	ResolvedAt     *time.Time
	ExpiresAt      *time.Time
	Metadata       map[string]any
	Entries        []Entry
}

// Entry is one immutable journal line. BalanceAfter is the account's
// available balance (balance - pending_debits) right after this entry.
type Entry struct {
	ID            uuid.UUID
	TransactionID uuid.UUID
	AccountID     uuid.UUID
	Amount        int64
	Currency      string
	BalanceAfter  int64
	Seq           int64
	CreatedAt     time.Time
}

// Account is a ledger account with its current balances.
// Available = Balance - PendingDebits.
type Account struct {
	ID             uuid.UUID
	Code           string
	Name           string
	Currency       string
	Kind           string
	OwnerID        *uuid.UUID
	AllowNegative  bool
	Balance        int64
	PendingDebits  int64
	PendingCredits int64
	Available      int64
	Version        int64
	CreatedAt      time.Time
}

// AccountSpec describes an account to create. Kind is one of asset,
// liability, equity, income, expense.
type AccountSpec struct {
	Code          string
	Name          string
	Currency      string
	Kind          string
	OwnerID       *uuid.UUID
	AllowNegative bool
}

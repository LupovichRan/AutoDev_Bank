package ledger

import (
	"errors"
	"fmt"
	"strings"

	"github.com/LupovichRan/AutoDev_Bank/internal/db"
)

var (
	ErrUnbalanced          = errors.New("ledger: transaction is unbalanced")
	ErrInsufficientFunds   = errors.New("ledger: insufficient funds")
	ErrPeriodClosed        = errors.New("ledger: accounting period is closed")
	ErrIdempotencyConflict = errors.New("ledger: idempotency key reused with a different payload")
	ErrAlreadyResolved     = errors.New("ledger: transaction is already resolved")
	ErrExpired             = errors.New("ledger: hold has expired")
	ErrNotFound            = errors.New("ledger: not found")
	ErrCurrencyMismatch    = errors.New("ledger: currency mismatch")
	ErrInvalidRequest      = errors.New("ledger: invalid request")
)

// sentinels are the typed errors; an error already wrapping one is not re-mapped.
var sentinels = []error{
	ErrUnbalanced, ErrInsufficientFunds, ErrPeriodClosed, ErrIdempotencyConflict,
	ErrAlreadyResolved, ErrExpired, ErrNotFound, ErrCurrencyMismatch, ErrInvalidRequest,
}

// pgPrefixes maps the schema's raise-message prefixes (SQLSTATE P0001) to typed errors.
var pgPrefixes = []struct {
	prefix string
	err    error
}{
	{"ledger_unbalanced", ErrUnbalanced},
	{"ledger_insufficient_funds", ErrInsufficientFunds},
	{"ledger_currency_mismatch", ErrCurrencyMismatch},
	{"ledger_period_closed", ErrPeriodClosed},
	{"ledger_account_missing", ErrNotFound},
	{"ledger_transaction_missing", ErrNotFound},
}

// sqlNumericOutOfRange is SQLSTATE 22003 (e.g. a bigint balance overflow).
const sqlNumericOutOfRange = "22003"

// MapError translates schema errors into typed errors, keeping the original
// PgError in the chain (both satisfy errors.Is / errors.As). Other errors,
// including retryable serialization failures, are returned unchanged.
// Callers of PostTx must pass their commit error through it: the zero-sum
// check is a deferred trigger that fires at commit. Numeric overflow
// (SQLSTATE 22003) maps to ErrInvalidRequest.
func MapError(err error) error {
	if err == nil {
		return nil
	}
	for _, s := range sentinels {
		if errors.Is(err, s) {
			return err
		}
	}
	if db.PgCode(err) == sqlNumericOutOfRange {
		return fmt.Errorf("%w: %w", ErrInvalidRequest, err)
	}
	msg := db.PgMessage(err)
	for _, p := range pgPrefixes {
		if strings.HasPrefix(msg, p.prefix) {
			return fmt.Errorf("%w: %w", p.err, err)
		}
	}
	return err
}

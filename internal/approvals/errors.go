package approvals

import (
	"errors"
	"fmt"
	"strings"

	"github.com/LupovichRan/AutoDev_Bank/internal/db"
)

var (
	ErrNotAuthorized  = errors.New("approvals: not authorized")
	ErrSelfApproval   = errors.New("approvals: initiator cannot decide their own approval")
	ErrNotPending     = errors.New("approvals: approval is not pending")
	ErrNotBefore      = errors.New("approvals: approval cannot be decided before not_before")
	ErrSoDConflict    = errors.New("approvals: role conflicts with separation of duties")
	ErrNotFound       = errors.New("approvals: not found")
	ErrAlreadyDecided = errors.New("approvals: principal has already decided this approval")
)

// pgPrefixes maps migration 0005's raise-message prefixes to typed errors.
var pgPrefixes = []struct {
	prefix string
	err    error
}{
	{"sod_conflict", ErrSoDConflict},
	{"sod_self_approval", ErrSelfApproval},
	{"approval_not_pending", ErrNotPending},
}

const (
	sqlForeignKeyViolation = "23503"
	sqlUniqueViolation     = "23505"
)

// mapError translates schema errors into typed errors, keeping the PgError
// in the chain. A foreign-key violation means a referenced principal or
// account does not exist.
func mapError(err error) error {
	if err == nil {
		return nil
	}
	msg := db.PgMessage(err)
	for _, p := range pgPrefixes {
		if strings.HasPrefix(msg, p.prefix) {
			return fmt.Errorf("%w: %w", p.err, err)
		}
	}
	if db.PgCode(err) == sqlForeignKeyViolation {
		return fmt.Errorf("%w: %w", ErrNotFound, err)
	}
	return err
}

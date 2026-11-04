package approvals

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/LupovichRan/AutoDev_Bank/internal/db"
	"github.com/LupovichRan/AutoDev_Bank/internal/ids"
	"github.com/LupovichRan/AutoDev_Bank/internal/ledger"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
)

// Roles that the static separation-of-duties rule keeps apart.
const (
	RoleInitiator = "payments.initiator"
	RoleApprover  = "payments.approver"
)

// Principal is a customer, staff member or service identity.
type Principal struct {
	ID          uuid.UUID
	Kind        string // customer, staff, service
	DisplayName string
	Handle      string
	CreatedAt   time.Time
}

const principalCols = `p.id, p.kind, p.display_name, p.handle, p.created_at`

func scanPrincipal(row pgx.Row) (Principal, error) {
	var p Principal
	err := row.Scan(&p.ID, &p.Kind, &p.DisplayName, &p.Handle, &p.CreatedAt)
	return p, err
}

// CreatePrincipal creates a principal; handle must be unique.
func (s *Service) CreatePrincipal(ctx context.Context, kind, displayName, handle string) (Principal, error) {
	var p Principal
	err := db.WithTx(ctx, s.pool, func(ctx context.Context, tx pgx.Tx) error {
		var err error
		p, err = scanPrincipal(tx.QueryRow(ctx, `insert into control.principals as p (id, kind, display_name, handle)
			values ($1, $2, $3, $4) returning `+principalCols, ids.New(), kind, displayName, handle))
		return err
	})
	if err != nil {
		return Principal{}, fmt.Errorf("approvals: create principal %q: %w", handle, err)
	}
	return p, nil
}

// GetPrincipalByHandle returns the principal with handle, or ErrNotFound.
func (s *Service) GetPrincipalByHandle(ctx context.Context, handle string) (Principal, error) {
	p, err := scanPrincipal(s.pool.QueryRow(ctx, `select `+principalCols+` from control.principals p where p.handle = $1`, handle))
	if errors.Is(err, pgx.ErrNoRows) {
		return Principal{}, fmt.Errorf("%w: principal %q", ErrNotFound, handle)
	}
	if err != nil {
		return Principal{}, fmt.Errorf("approvals: load principal: %w", err)
	}
	return p, nil
}

// GrantRole grants role to the principal (idempotent). Granting
// payments.initiator and payments.approver to the same principal fails with
// ErrSoDConflict (trigger roles_static_sod).
func (s *Service) GrantRole(ctx context.Context, principalID uuid.UUID, role string) error {
	err := db.WithTx(ctx, s.pool, func(ctx context.Context, tx pgx.Tx) error {
		_, err := tx.Exec(ctx, `insert into control.roles (principal_id, role) values ($1, $2)
			on conflict do nothing`, principalID, role)
		return err
	})
	if err != nil {
		return fmt.Errorf("approvals: grant %s: %w", role, mapError(err))
	}
	return nil
}

// Roles returns the principal's roles, sorted.
func (s *Service) Roles(ctx context.Context, principalID uuid.UUID) ([]string, error) {
	rows, err := s.pool.Query(ctx, `select role from control.roles where principal_id = $1 order by role`, principalID)
	if err != nil {
		return nil, fmt.Errorf("approvals: roles: %w", err)
	}
	roles, err := pgx.CollectRows(rows, pgx.RowTo[string])
	if err != nil {
		return nil, fmt.Errorf("approvals: roles: %w", err)
	}
	return roles, nil
}

// AddHolder makes the principal a holder of the account (idempotent).
// An unknown account or principal is ErrNotFound.
func (s *Service) AddHolder(ctx context.Context, accountID, principalID uuid.UUID) error {
	err := db.WithTx(ctx, s.pool, func(ctx context.Context, tx pgx.Tx) error {
		_, err := tx.Exec(ctx, `insert into control.account_holders (account_id, principal_id) values ($1, $2)
			on conflict do nothing`, accountID, principalID)
		return err
	})
	if err != nil {
		return fmt.Errorf("approvals: add holder: %w", mapError(err))
	}
	return nil
}

// Holders returns the account's holders, oldest principal first.
func (s *Service) Holders(ctx context.Context, accountID uuid.UUID) ([]Principal, error) {
	rows, err := s.pool.Query(ctx, `select `+principalCols+` from control.account_holders h
		join control.principals p on p.id = h.principal_id
		where h.account_id = $1 order by p.created_at, p.id`, accountID)
	if err != nil {
		return nil, fmt.Errorf("approvals: holders: %w", err)
	}
	ps, err := pgx.CollectRows(rows, func(r pgx.CollectableRow) (Principal, error) { return scanPrincipal(r) })
	if err != nil {
		return nil, fmt.Errorf("approvals: holders: %w", err)
	}
	return ps, nil
}

// AccountsForHolder returns the accounts the principal holds, oldest first.
func (s *Service) AccountsForHolder(ctx context.Context, principalID uuid.UUID) ([]ledger.Account, error) {
	rows, err := s.pool.Query(ctx, `select a.id, a.code, a.name, a.currency, a.kind, a.owner_id, a.allow_negative,
			a.balance, a.pending_debits, a.pending_credits, a.balance - a.pending_debits, a.version, a.created_at
		from control.account_holders h join ledger.accounts a on a.id = h.account_id
		where h.principal_id = $1 order by a.created_at, a.id`, principalID)
	if err != nil {
		return nil, fmt.Errorf("approvals: accounts for holder: %w", err)
	}
	accts, err := pgx.CollectRows(rows, func(r pgx.CollectableRow) (ledger.Account, error) {
		var a ledger.Account
		err := r.Scan(&a.ID, &a.Code, &a.Name, &a.Currency, &a.Kind, &a.OwnerID, &a.AllowNegative,
			&a.Balance, &a.PendingDebits, &a.PendingCredits, &a.Available, &a.Version, &a.CreatedAt)
		return a, err
	})
	if err != nil {
		return nil, fmt.Errorf("approvals: accounts for holder: %w", err)
	}
	return accts, nil
}

// querier is satisfied by *pgxpool.Pool and pgx.Tx.
type querier interface {
	QueryRow(ctx context.Context, sql string, args ...any) pgx.Row
}

func isHolder(ctx context.Context, q querier, accountID, principalID uuid.UUID) (bool, error) {
	var ok bool
	err := q.QueryRow(ctx, `select exists(select 1 from control.account_holders
		where account_id = $1 and principal_id = $2)`, accountID, principalID).Scan(&ok)
	return ok, err
}

func hasRole(ctx context.Context, q querier, principalID uuid.UUID, role string) (bool, error) {
	var ok bool
	err := q.QueryRow(ctx, `select exists(select 1 from control.roles
		where principal_id = $1 and role = $2)`, principalID, role).Scan(&ok)
	return ok, err
}

// eligible reports whether principalID may act on accountID: as one of its
// holders, or as a staff member holding role.
func eligible(ctx context.Context, q querier, accountID, principalID uuid.UUID, role string) (bool, error) {
	ok, err := isHolder(ctx, q, accountID, principalID)
	if err != nil || ok {
		return ok, err
	}
	return hasRole(ctx, q, principalID, role)
}

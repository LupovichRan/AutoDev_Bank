package httpapi

import (
	"context"
	"net/http"
	"strconv"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/LupovichRan/AutoDev_Bank/internal/auth"
)

type holderJSON struct {
	PrincipalID string `json:"principal_id"`
	DisplayName string `json:"display_name"`
}

type accountJSON struct {
	ID             string       `json:"id"`
	Code           string       `json:"code"`
	Name           string       `json:"name"`
	Currency       string       `json:"currency"`
	Kind           string       `json:"kind"`
	Balance        int64        `json:"balance"`
	Available      int64        `json:"available"`
	PendingDebits  int64        `json:"pending_debits"`
	PendingCredits int64        `json:"pending_credits"`
	Holders        []holderJSON `json:"holders"`
}

type entryJSON struct {
	ID            string    `json:"id"`
	TransactionID string    `json:"transaction_id"`
	Amount        int64     `json:"amount"`
	Currency      string    `json:"currency"`
	BalanceAfter  int64     `json:"balance_after"`
	Description   string    `json:"description"`
	Status        string    `json:"status"`
	EffectiveDate string    `json:"effective_date"`
	CreatedAt     time.Time `json:"created_at"`
}

// GET /api/accounts: the signed-in principal's accounts with their holders.
func (a *api) listAccounts(w http.ResponseWriter, r *http.Request) {
	sess, _ := auth.FromContext(r.Context())
	accts, err := a.Approvals.AccountsForHolder(r.Context(), sess.PrincipalID)
	if err != nil {
		WriteErr(w, r, err)
		return
	}
	acctIDs := make([]uuid.UUID, len(accts))
	for i, ac := range accts {
		acctIDs[i] = ac.ID
	}
	holders, err := a.holdersOf(r.Context(), acctIDs)
	if err != nil {
		WriteErr(w, r, err)
		return
	}
	out := make([]accountJSON, len(accts))
	for i, ac := range accts {
		hs := holders[ac.ID]
		if hs == nil {
			hs = []holderJSON{}
		}
		out[i] = accountJSON{
			ID: ac.ID.String(), Code: ac.Code, Name: ac.Name, Currency: ac.Currency, Kind: ac.Kind,
			Balance: ac.Balance, Available: ac.Available,
			PendingDebits: ac.PendingDebits, PendingCredits: ac.PendingCredits, Holders: hs,
		}
	}
	JSON(w, http.StatusOK, map[string]any{"accounts": out})
}

// holdersOf returns the holders of each account in one query.
func (a *api) holdersOf(ctx context.Context, accountIDs []uuid.UUID) (map[uuid.UUID][]holderJSON, error) {
	out := map[uuid.UUID][]holderJSON{}
	if len(accountIDs) == 0 {
		return out, nil
	}
	rows, err := a.Pool.Query(ctx, `select h.account_id, p.id, p.display_name
		from control.account_holders h join control.principals p on p.id = h.principal_id
		where h.account_id = any($1) order by h.account_id, p.created_at, p.id`, accountIDs)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	for rows.Next() {
		var acct, pid uuid.UUID
		var name string
		if err := rows.Scan(&acct, &pid, &name); err != nil {
			return nil, err
		}
		out[acct] = append(out[acct], holderJSON{PrincipalID: pid.String(), DisplayName: name})
	}
	return out, rows.Err()
}

// GET /api/accounts/{id}/entries?limit=50: newest first, holders only. An
// account the caller does not hold is reported as not found.
func (a *api) listEntries(w http.ResponseWriter, r *http.Request) {
	sess, _ := auth.FromContext(r.Context())
	id, err := uuid.Parse(r.PathValue("id"))
	if err != nil {
		Write(w, r, http.StatusNotFound, "", "not_found", nil)
		return
	}
	limit := 50
	if s := r.URL.Query().Get("limit"); s != "" {
		n, err := strconv.Atoi(s)
		if err != nil || n < 1 || n > 500 {
			Write(w, r, http.StatusBadRequest, "", "invalid_limit", nil)
			return
		}
		limit = n
	}
	var holder bool
	if err := a.Pool.QueryRow(r.Context(), `select exists(select 1 from control.account_holders
		where account_id = $1 and principal_id = $2)`, id, sess.PrincipalID).Scan(&holder); err != nil {
		WriteErr(w, r, err)
		return
	}
	if !holder {
		Write(w, r, http.StatusNotFound, "", "not_found", nil)
		return
	}
	rows, err := a.Pool.Query(r.Context(), `select e.id, e.transaction_id, e.amount, e.currency, e.balance_after,
			t.description, t.status, to_char(t.effective_date, 'YYYY-MM-DD'), e.created_at
		from ledger.entries e join ledger.transactions t on t.id = e.transaction_id
		where e.account_id = $1 order by e.seq desc limit $2`, id, limit)
	if err != nil {
		WriteErr(w, r, err)
		return
	}
	entries, err := pgx.CollectRows(rows, func(row pgx.CollectableRow) (entryJSON, error) {
		var e entryJSON
		var eid, tid uuid.UUID
		err := row.Scan(&eid, &tid, &e.Amount, &e.Currency, &e.BalanceAfter,
			&e.Description, &e.Status, &e.EffectiveDate, &e.CreatedAt)
		e.ID, e.TransactionID, e.CreatedAt = eid.String(), tid.String(), e.CreatedAt.UTC()
		return e, err
	})
	if err != nil {
		WriteErr(w, r, err)
		return
	}
	if entries == nil {
		entries = []entryJSON{}
	}
	JSON(w, http.StatusOK, map[string]any{"entries": entries})
}

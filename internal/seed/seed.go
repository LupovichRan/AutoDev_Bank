// Package seed loads the demo principals, roles and accounts used by
// `bank seed`, the HTTP API tests and the end-to-end suite. Run is
// idempotent: principals are looked up by handle, accounts by code, roles
// and holders are added only if missing, and opening balances are posted
// with fixed `seed:*` idempotency keys, so running it again changes nothing.
//
// Demo names are Hebrew (the app is Hebrew-first); handles and account codes
// stay ASCII. A database seeded before the names were translated is renamed
// in place: a principal or account whose name is still the old English seed
// name gets the Hebrew one, and any other name (edited by hand) is left
// alone. Opening-balance transactions are immutable in the ledger, so their
// description stays "Opening balance" on such databases.
package seed

import (
	"context"
	"errors"
	"fmt"
	"io"
	"text/tabwriter"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/LupovichRan/AutoDev_Bank/internal/approvals"
	"github.com/LupovichRan/AutoDev_Bank/internal/ledger"
)

// Account codes created by Run.
const (
	EquityILS = "bank.equity.ILS"
	EquityEUR = "bank.equity.EUR"
	AliceILS  = "cust.alice.ILS.current"
	AliceEUR  = "cust.alice.EUR.savings"
	Joint     = "cust.alice-bob.ILS.joint"
	BobILS    = "cust.bob.ILS.current"
)

// RoleStaffOps guards staff-only operations such as POST /api/opendata/refresh.
const RoleStaffOps = "staff.ops"

// OpeningDescription is the description of the opening-balance transactions.
const OpeningDescription = "יתרת פתיחה"

type principalSpec struct {
	handle, kind, displayName string
	roles                     []string
	legacyName                string // English display name used by earlier seeds
}

var principals = []principalSpec{
	{"alice", "customer", "אליס", nil, "Alice"},
	{"bob", "customer", "בוב", nil, "Bob"},
	{"carol", "staff", "קרול", []string{approvals.RoleApprover, RoleStaffOps}, "Carol"},
	{"dave", "staff", "דייב", []string{approvals.RoleInitiator}, "Dave"},
}

type accountSpec struct {
	code, name, legacyName, currency, kind string
	owner                                  string   // handle of owner_id, "" for none
	holders                                []string // handles
	allowNegative                          bool
	opening                                int64 // minor units, funded from the currency's equity account
}

// Customer accounts use kind 'asset' in this slice, i.e. they are modelled
// from the customer's perspective (money the customer has). In the bank's
// own books customer deposits are liabilities; the chart of accounts will be
// flipped to 'liability' when real general-ledger reporting lands. Equity
// accounts may go negative: they are the source of the opening balances.
var accounts = []accountSpec{
	{EquityILS, "הון הבנק (ILS)", "Bank equity (ILS)", "ILS", "equity", "", nil, true, 0},
	{EquityEUR, "הון הבנק (EUR)", "Bank equity (EUR)", "EUR", "equity", "", nil, true, 0},
	{AliceILS, "עו״ש אליס", "Alice current account", "ILS", "asset", "alice", []string{"alice"}, false, 2_500_000},
	{AliceEUR, "חיסכון אליס (אירו)", "Alice savings", "EUR", "asset", "alice", []string{"alice"}, false, 120_000},
	{Joint, "חשבון משותף אליס ובוב", "Alice & Bob joint account", "ILS", "asset", "", []string{"alice", "bob"}, false, 800_000},
	{BobILS, "עו״ש בוב", "Bob current account", "ILS", "asset", "bob", []string{"bob"}, false, 350_000},
}

var equityFor = map[string]string{"ILS": EquityILS, "EUR": EquityEUR}

// Result is what Run created or found.
type Result struct {
	Principals map[string]uuid.UUID      // by handle
	Accounts   map[string]ledger.Account // by code, with balances after seeding
	Holders    map[string][]string       // account code -> holder handles
}

// Run creates (or finds) the demo data. led and ap must use pool.
func Run(ctx context.Context, pool *pgxpool.Pool, led *ledger.Service, ap *approvals.Service) (Result, error) {
	res := Result{Principals: map[string]uuid.UUID{}, Accounts: map[string]ledger.Account{}, Holders: map[string][]string{}}
	for _, p := range principals {
		pr, err := ap.GetPrincipalByHandle(ctx, p.handle)
		if errors.Is(err, approvals.ErrNotFound) {
			pr, err = ap.CreatePrincipal(ctx, p.kind, p.displayName, p.handle)
		} else if err == nil && pr.DisplayName == p.legacyName {
			_, err = pool.Exec(ctx,
				`update control.principals set display_name = $2 where id = $1 and display_name = $3`,
				pr.ID, p.displayName, p.legacyName)
		}
		if err != nil {
			return Result{}, fmt.Errorf("seed: principal %s: %w", p.handle, err)
		}
		for _, role := range p.roles {
			if err := ap.GrantRole(ctx, pr.ID, role); err != nil {
				return Result{}, fmt.Errorf("seed: %s role %s: %w", p.handle, role, err)
			}
		}
		res.Principals[p.handle] = pr.ID
	}

	for _, a := range accounts {
		acct, err := ensureAccount(ctx, pool, led, a, res.Principals)
		if err != nil {
			return Result{}, err
		}
		for _, h := range a.holders {
			if err := ap.AddHolder(ctx, acct.ID, res.Principals[h]); err != nil {
				return Result{}, fmt.Errorf("seed: holder %s of %s: %w", h, a.code, err)
			}
		}
		res.Accounts[a.code] = acct
		res.Holders[a.code] = a.holders
	}

	for _, a := range accounts {
		if a.opening == 0 {
			continue
		}
		_, err := led.Post(ctx, ledger.PostRequest{
			IdempotencyKey: "seed:opening:" + a.code,
			Description:    OpeningDescription,
			Currency:       a.currency,
			Lines: []ledger.Line{
				{AccountID: res.Accounts[equityFor[a.currency]].ID, Amount: -a.opening},
				{AccountID: res.Accounts[a.code].ID, Amount: a.opening},
			},
			Metadata: map[string]any{"seed": true},
		})
		if err != nil {
			return Result{}, fmt.Errorf("seed: opening balance %s: %w", a.code, err)
		}
	}

	for code, acct := range res.Accounts {
		fresh, err := led.Account(ctx, acct.ID)
		if err != nil {
			return Result{}, fmt.Errorf("seed: reload %s: %w", code, err)
		}
		res.Accounts[code] = fresh
	}
	return res, nil
}

func ensureAccount(ctx context.Context, pool *pgxpool.Pool, led *ledger.Service, a accountSpec, ids map[string]uuid.UUID) (ledger.Account, error) {
	var id uuid.UUID
	err := pool.QueryRow(ctx, `select id from ledger.accounts where code = $1`, a.code).Scan(&id)
	if err == nil {
		// Rename a pre-Hebrew seed account; a hand-edited name is kept.
		if _, err := pool.Exec(ctx, `update ledger.accounts set name = $2 where id = $1 and name = $3`,
			id, a.name, a.legacyName); err != nil {
			return ledger.Account{}, fmt.Errorf("seed: rename %s: %w", a.code, err)
		}
		return led.Account(ctx, id)
	}
	if !errors.Is(err, pgx.ErrNoRows) {
		return ledger.Account{}, fmt.Errorf("seed: look up %s: %w", a.code, err)
	}
	spec := ledger.AccountSpec{Code: a.code, Name: a.name, Currency: a.currency, Kind: a.kind, AllowNegative: a.allowNegative}
	if a.owner != "" {
		owner := ids[a.owner]
		spec.OwnerID = &owner
	}
	acct, err := led.CreateAccount(ctx, spec)
	if err != nil {
		return ledger.Account{}, fmt.Errorf("seed: %w", err)
	}
	return acct, nil
}

// WriteSummary prints the principals and accounts as two aligned tables.
func (r Result) WriteSummary(w io.Writer) error {
	tw := tabwriter.NewWriter(w, 0, 4, 2, ' ', 0)
	fmt.Fprintln(tw, "HANDLE\tPRINCIPAL ID")
	for _, p := range principals {
		fmt.Fprintf(tw, "%s\t%s\n", p.handle, r.Principals[p.handle])
	}
	fmt.Fprintln(tw)
	fmt.Fprintln(tw, "ACCOUNT\tID\tCURRENCY\tBALANCE\tHOLDERS")
	for _, a := range accounts {
		acct := r.Accounts[a.code]
		holders := "-"
		if hs := r.Holders[a.code]; len(hs) > 0 {
			holders = fmt.Sprint(hs)
		}
		fmt.Fprintf(tw, "%s\t%s\t%s\t%s\t%s\n", a.code, acct.ID, acct.Currency, formatMinor(acct.Balance), holders)
	}
	return tw.Flush()
}

func formatMinor(v int64) string {
	sign := ""
	if v < 0 {
		sign, v = "-", -v
	}
	return fmt.Sprintf("%s%d.%02d", sign, v/100, v%100)
}

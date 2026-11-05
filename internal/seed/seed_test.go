package seed

import (
	"bytes"
	"context"
	"strings"
	"testing"

	"github.com/LupovichRan/AutoDev_Bank/internal/approvals"
	"github.com/LupovichRan/AutoDev_Bank/internal/db"
	"github.com/LupovichRan/AutoDev_Bank/internal/ledger"
	"github.com/LupovichRan/AutoDev_Bank/internal/testutil"
)

func TestRun_IdempotentBalancesAndRoles(t *testing.T) {
	ctx := context.Background()
	pool := db.TestPool(t)
	led := ledger.New(pool)
	ap := approvals.New(pool, led, nil, approvals.DefaultPolicy())

	first, err := Run(ctx, pool, led, ap)
	testutil.NoError(t, err)
	second, err := Run(ctx, pool, led, ap)
	testutil.NoError(t, err)

	want := map[string]int64{AliceILS: 2_500_000, AliceEUR: 120_000, Joint: 800_000, BobILS: 350_000,
		EquityILS: -(2_500_000 + 800_000 + 350_000), EquityEUR: -120_000}
	for code, bal := range want {
		testutil.Equal(t, bal, second.Accounts[code].Balance, code)
		testutil.Equal(t, first.Accounts[code].ID, second.Accounts[code].ID, code)
	}
	for h, id := range first.Principals {
		testutil.Equal(t, id, second.Principals[h], h)
	}
	roles, err := ap.Roles(ctx, second.Principals["carol"])
	testutil.NoError(t, err)
	testutil.Equal(t, "payments.approver,staff.ops", strings.Join(roles, ","))
	roles, err = ap.Roles(ctx, second.Principals["dave"])
	testutil.NoError(t, err)
	testutil.Equal(t, "payments.initiator", strings.Join(roles, ","))
	holders, err := ap.Holders(ctx, second.Accounts[Joint].ID)
	testutil.NoError(t, err)
	testutil.Len(t, holders, 2)

	var buf bytes.Buffer
	testutil.NoError(t, second.WriteSummary(&buf))
	testutil.True(t, strings.Contains(buf.String(), "25000.00"), buf.String())
}

func TestRun_HebrewNames(t *testing.T) {
	ctx := context.Background()
	pool := db.TestPool(t)
	led := ledger.New(pool)
	ap := approvals.New(pool, led, nil, approvals.DefaultPolicy())

	res, err := Run(ctx, pool, led, ap)
	testutil.NoError(t, err)
	wantAcct := map[string]string{
		EquityILS: "הון הבנק (ILS)", EquityEUR: "הון הבנק (EUR)",
		AliceILS: "עו״ש אליס", AliceEUR: "חיסכון אליס (אירו)",
		Joint: "חשבון משותף אליס ובוב", BobILS: "עו״ש בוב",
	}
	for code, name := range wantAcct {
		testutil.Equal(t, name, res.Accounts[code].Name, code)
	}
	wantPrincipal := map[string]string{"alice": "אליס", "bob": "בוב", "carol": "קרול", "dave": "דייב"}
	for h, name := range wantPrincipal {
		p, err := ap.GetPrincipalByHandle(ctx, h)
		testutil.NoError(t, err)
		testutil.Equal(t, name, p.DisplayName, h)
	}
	var desc string
	testutil.NoError(t, pool.QueryRow(ctx,
		`select description from ledger.transactions where idempotency_key = $1`, "seed:opening:"+AliceILS).Scan(&desc))
	testutil.Equal(t, OpeningDescription, desc)
}

// A database seeded with the earlier English names is renamed in place,
// without duplicating anything; names edited by hand are kept.
func TestRun_RenamesLegacyEnglishSeed(t *testing.T) {
	ctx := context.Background()
	pool := db.TestPool(t)
	led := ledger.New(pool)
	ap := approvals.New(pool, led, nil, approvals.DefaultPolicy())

	first, err := Run(ctx, pool, led, ap)
	testutil.NoError(t, err)
	for _, a := range accounts {
		_, err := pool.Exec(ctx, `update ledger.accounts set name = $2 where code = $1`, a.code, a.legacyName)
		testutil.NoError(t, err)
	}
	for _, p := range principals {
		_, err := pool.Exec(ctx, `update control.principals set display_name = $2 where handle = $1`, p.handle, p.legacyName)
		testutil.NoError(t, err)
	}
	_, err = pool.Exec(ctx, `update ledger.accounts set name = 'My own name' where code = $1`, BobILS)
	testutil.NoError(t, err)
	_, err = pool.Exec(ctx, `update control.principals set display_name = 'Robert' where handle = 'bob'`)
	testutil.NoError(t, err)

	second, err := Run(ctx, pool, led, ap)
	testutil.NoError(t, err)
	testutil.Equal(t, "עו״ש אליס", second.Accounts[AliceILS].Name)
	testutil.Equal(t, "חשבון משותף אליס ובוב", second.Accounts[Joint].Name)
	testutil.Equal(t, "My own name", second.Accounts[BobILS].Name)
	testutil.Equal(t, first.Accounts[Joint].ID, second.Accounts[Joint].ID)
	testutil.Equal(t, int64(800_000), second.Accounts[Joint].Balance)
	alice, err := ap.GetPrincipalByHandle(ctx, "alice")
	testutil.NoError(t, err)
	testutil.Equal(t, "אליס", alice.DisplayName)
	bob, err := ap.GetPrincipalByHandle(ctx, "bob")
	testutil.NoError(t, err)
	testutil.Equal(t, "Robert", bob.DisplayName)

	var n int
	testutil.NoError(t, pool.QueryRow(ctx, `select count(*) from ledger.accounts`).Scan(&n))
	testutil.Equal(t, len(accounts), n)
	testutil.NoError(t, pool.QueryRow(ctx, `select count(*) from control.principals`).Scan(&n))
	testutil.Equal(t, len(principals), n)
}

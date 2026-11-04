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

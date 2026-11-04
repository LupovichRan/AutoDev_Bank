package httpapi

import (
	"encoding/hex"
	"strings"
	"testing"

	"github.com/LupovichRan/AutoDev_Bank/internal/testutil"
)

// The expected digests were computed independently with
// `printf '%s' '<canonical json>' | sha256sum` and with Node's
// JSON.stringify + crypto.createHash (what web/src/lib/payload.ts does).
func TestTransferPayloadHash_Vectors(t *testing.T) {
	cases := []struct {
		name string
		in   transferBody
		want string
	}{
		{
			// The exact body from web/src/lib/payload.test.ts.
			name: "web client vector",
			in:   transferBody{ToAccountID: "acc_b", FromAccountID: "acc_a", Description: `Rent "Nov"`, Currency: "EUR", Amount: 125000},
			want: "0d2b9883101894d0275f4e21f2236660980b9a9e1b8add720cd9f57d68ea7a2f",
		},
		{
			name: "html characters are not escaped",
			in: transferBody{Amount: 5000, Currency: "ILS", Description: "café & <tea>",
				FromAccountID: "0192f000-0000-7000-8000-000000000001", ToAccountID: "0192f000-0000-7000-8000-000000000002"},
			want: "6da13fd8ac4d25db60db9dfb3be61f912e24410a4c2069bec8d7e9c81dfe9e3a",
		},
		{
			name: "utf-8 description",
			in:   transferBody{Amount: 42, Currency: "ILS", Description: "שכר דירה €", FromAccountID: "a", ToAccountID: "b"},
			want: "34a687a833f255f0a1f2823bde20b02bde453a23ad9d2bfb409f7443900d1efb",
		},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			testutil.Equal(t, c.want, hex.EncodeToString(transferPayloadHash(c.in)))
		})
	}
}

func TestCanonicalTransferJSON(t *testing.T) {
	got := string(canonicalTransferJSON(transferBody{ToAccountID: "acc_b", FromAccountID: "acc_a", Description: `Rent "Nov"`, Currency: "EUR", Amount: 125000}))
	testutil.Equal(t, `{"amount":125000,"currency":"EUR","description":"Rent \"Nov\"","from_account_id":"acc_a","to_account_id":"acc_b"}`, got)
}

func TestValidateTransferBody(t *testing.T) {
	ok := transferBody{Amount: 1, Currency: "ILS", Description: "x",
		FromAccountID: "0192f000-0000-7000-8000-000000000001", ToAccountID: "0192f000-0000-7000-8000-000000000002"}
	_, _, detail := validateTransferBody(ok)
	testutil.Equal(t, "", detail)
	bad := map[string]func(b *transferBody){
		"invalid_amount":      func(b *transferBody) { b.Amount = 0 },
		"invalid_currency":    func(b *transferBody) { b.Currency = "ils" },
		"invalid_description": func(b *transferBody) { b.Description = "line sep" },
		"invalid_account":     func(b *transferBody) { b.FromAccountID = "nope" },
	}
	for want, mut := range bad {
		b := ok
		mut(&b)
		_, _, detail := validateTransferBody(b)
		testutil.Equal(t, want, detail)
	}
	long := ok
	long.Description = strings.Repeat("a", 141)
	_, _, detail = validateTransferBody(long)
	testutil.Equal(t, "invalid_description", detail)
	long.Description = strings.Repeat("ש", 140) // 140 characters, 280 bytes
	_, _, detail = validateTransferBody(long)
	testutil.Equal(t, "", detail)
}

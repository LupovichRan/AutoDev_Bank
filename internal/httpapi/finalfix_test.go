package httpapi

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/LupovichRan/AutoDev_Bank/internal/seed"
	"github.com/LupovichRan/AutoDev_Bank/internal/testutil"
)

// approveStepUp records a step-up bound to sha256("approve:" + id) (R1).
func (c *apiClient) approveStepUp(id string) {
	c.e.t.Helper()
	h := sha256.Sum256([]byte("approve:" + id))
	testutil.NoError(c.e.t, c.e.auth.TestMarkSteppedUp(context.Background(), c.sess, h[:]))
}

func (c *apiClient) submit(key string, body transferBody) (*http.Response, []byte) {
	c.e.t.Helper()
	c.stepUp(body)
	return c.transfer(key, body)
}

// C1: a payments.initiator debiting bank equity always needs a checker; a
// customer below the tier on their own account posts at once and cannot
// debit someone else's.
func TestTransfers_StaffFromEquityAlwaysPending(t *testing.T) {
	e := newEnv(t)
	dave := e.login("dave")
	body := transferBody{FromAccountID: e.account(seed.EquityILS), ToAccountID: e.account(seed.BobILS),
		Amount: 99999, Currency: "ILS", Description: "gift"}
	for i, key := range []string{"dave-eq-0001", "dave-eq-0002", "dave-eq-0003", "dave-eq-0004", "dave-eq-0005"} {
		body.Description = "gift " + string(rune('a'+i))
		resp, raw := dave.submit(key, body)
		testutil.Equal(t, http.StatusAccepted, resp.StatusCode, string(raw))
		var out struct {
			Approval tApproval `json:"approval"`
		}
		testutil.NoError(t, json.Unmarshal(raw, &out))
		testutil.Equal(t, "pending", out.Approval.Status)
	}
	testutil.Equal(t, int64(350000), e.login("bob").balances()[seed.BobILS].Balance)

	alice := e.login("alice")
	resp, raw := alice.submit("alice-own-001", transferBody{FromAccountID: e.account(seed.AliceILS),
		ToAccountID: e.account(seed.BobILS), Amount: 500, Currency: "ILS", Description: "own"})
	testutil.Equal(t, http.StatusCreated, resp.StatusCode, string(raw))
	resp, raw = alice.submit("alice-bob-001", transferBody{FromAccountID: e.account(seed.BobILS),
		ToAccountID: e.account(seed.AliceILS), Amount: 500, Currency: "ILS", Description: "bob's"})
	testutil.Equal(t, http.StatusForbidden, resp.StatusCode, string(raw))
	testutil.Equal(t, "not_authorized", decodeProblem(t, resp, raw).Detail)
}

// C2: the 201 body carries only the caller's own entries; it never shows
// the payee's balance.
func TestTransfers_ResponseHidesPayee(t *testing.T) {
	e := newEnv(t)
	alice := e.login("alice")
	bobAcct := e.account(seed.BobILS)
	resp, raw := alice.submit("alice-pays-bob1", transferBody{FromAccountID: e.account(seed.AliceILS),
		ToAccountID: bobAcct, Amount: 5000, Currency: "ILS", Description: "lunch"})
	testutil.Equal(t, http.StatusCreated, resp.StatusCode, string(raw))
	testutil.False(t, strings.Contains(string(raw), bobAcct), "no payee account in the response: "+string(raw))
	testutil.False(t, strings.Contains(string(raw), "355000"), "no payee balance in the response")
	var out struct {
		Transaction struct {
			Entries []struct {
				AccountID    string `json:"account_id"`
				Amount       int64  `json:"amount"`
				BalanceAfter int64  `json:"balance_after"`
			} `json:"entries"`
		} `json:"transaction"`
	}
	testutil.NoError(t, json.Unmarshal(raw, &out))
	testutil.Len(t, out.Transaction.Entries, 1)
	testutil.Equal(t, e.account(seed.AliceILS), out.Transaction.Entries[0].AccountID)
	testutil.Equal(t, int64(-5000), out.Transaction.Entries[0].Amount)
	testutil.Equal(t, int64(2500000-5000), out.Transaction.Entries[0].BalanceAfter)
}

// C2: unknown, other-currency and internal destinations answer the same
// 422 invalid_destination; there is no 404.
func TestTransfers_InvalidDestinationIsNoOracle(t *testing.T) {
	e := newEnv(t)
	alice := e.login("alice")
	type shape struct {
		Status int
		Detail string
		Title  string
		Type   string
	}
	var bodies []shape
	for i, to := range []string{
		"0192f000-0000-7000-8000-00000000dead", // unknown
		e.account(seed.EquityILS),              // the bank's own account
		e.account(seed.AliceEUR),               // other currency
	} {
		resp, raw := alice.submit("alice-dest-00"+string(rune('1'+i)), transferBody{FromAccountID: e.account(seed.AliceILS),
			ToAccountID: to, Amount: 100, Currency: "ILS", Description: "x"})
		p := decodeProblem(t, resp, raw)
		bodies = append(bodies, shape{resp.StatusCode, p.Detail, p.Title, p.Type})
	}
	for _, b := range bodies {
		testutil.Equal(t, shape{http.StatusUnprocessableEntity, "invalid_destination", "Unprocessable Entity", "about:blank"}, b)
	}
	// An unknown source looks like a foreign one: 403, never 404.
	resp, raw := alice.submit("alice-src-0001", transferBody{FromAccountID: "0192f000-0000-7000-8000-00000000beef",
		ToAccountID: e.account(seed.BobILS), Amount: 100, Currency: "ILS", Description: "x"})
	testutil.Equal(t, http.StatusForbidden, resp.StatusCode, string(raw))
}

// I4 / R1: approving needs a fresh step-up bound to sha256("approve:"+id);
// rejecting does not.
func TestApprovals_ApproveNeedsStepUp(t *testing.T) {
	e := newEnv(t)
	alice, bob := e.login("alice"), e.login("bob")
	big := transferBody{FromAccountID: e.account(seed.Joint), ToAccountID: e.account(seed.BobILS),
		Amount: 200000, Currency: "ILS", Description: "big"}
	resp, raw := alice.submit("alice-big-0001", big)
	testutil.Equal(t, http.StatusAccepted, resp.StatusCode, string(raw))
	var out struct {
		Approval tApproval `json:"approval"`
	}
	testutil.NoError(t, json.Unmarshal(raw, &out))
	id := out.Approval.ID

	decide := map[string]any{"approve": true, "reason": "ok"}
	resp, raw = bob.do("POST", "/api/approvals/"+id+"/decide", decide, nil)
	testutil.Equal(t, http.StatusForbidden, resp.StatusCode, string(raw))
	testutil.Equal(t, "step_up_required", decodeProblem(t, resp, raw).Detail)

	// A step-up for another approval does not count.
	bob.approveStepUp("0192f000-0000-7000-8000-000000000001")
	resp, raw = bob.do("POST", "/api/approvals/"+id+"/decide", decide, nil)
	testutil.Equal(t, http.StatusForbidden, resp.StatusCode, string(raw))

	bob.approveStepUp(id)
	resp, raw = bob.do("POST", "/api/approvals/"+id+"/decide", decide, nil)
	testutil.Equal(t, http.StatusOK, resp.StatusCode, string(raw))

	// Rejecting needs no step-up.
	resp, raw = alice.submit("alice-big-0002", transferBody{FromAccountID: big.FromAccountID, ToAccountID: big.ToAccountID,
		Amount: 200000, Currency: "ILS", Description: "big 2"})
	testutil.Equal(t, http.StatusAccepted, resp.StatusCode, string(raw))
	testutil.NoError(t, json.Unmarshal(raw, &out))
	resp, raw = bob.do("POST", "/api/approvals/"+out.Approval.ID+"/decide", map[string]any{"approve": false, "reason": "no"}, nil)
	testutil.Equal(t, http.StatusOK, resp.StatusCode, string(raw))
}

// I7 / R3: the initiator can cancel a pending approval; nobody else can.
func TestApprovals_CancelByInitiator(t *testing.T) {
	e := newEnv(t)
	alice, bob := e.login("alice"), e.login("bob")
	resp, raw := alice.submit("alice-cancel-01", transferBody{FromAccountID: e.account(seed.Joint), ToAccountID: e.account(seed.BobILS),
		Amount: 200000, Currency: "ILS", Description: "oops"})
	testutil.Equal(t, http.StatusAccepted, resp.StatusCode, string(raw))
	var out struct {
		Approval struct {
			ID        string    `json:"id"`
			Status    string    `json:"status"`
			CreatedAt time.Time `json:"created_at"`
			ExpiresAt time.Time `json:"expires_at"`
		} `json:"approval"`
	}
	testutil.NoError(t, json.Unmarshal(raw, &out))
	testutil.Equal(t, out.Approval.CreatedAt.Add(7*24*time.Hour), out.Approval.ExpiresAt)
	path := "/api/approvals/" + out.Approval.ID + "/cancel"

	resp, raw = bob.do("POST", path, map[string]any{}, nil)
	testutil.Equal(t, http.StatusForbidden, resp.StatusCode, string(raw))
	resp, raw = alice.do("POST", path, map[string]any{}, nil)
	testutil.Equal(t, http.StatusOK, resp.StatusCode, string(raw))
	testutil.NoError(t, json.Unmarshal(raw, &out))
	testutil.Equal(t, "rejected", out.Approval.Status)
	resp, raw = alice.do("POST", path, map[string]any{}, nil)
	testutil.Equal(t, http.StatusConflict, resp.StatusCode, string(raw))
}

// I5: the public recent-events feed carries no actor ids or subjects.
func TestTransparency_RecentIsAnonymous(t *testing.T) {
	e := newEnv(t)
	alice := e.login("alice")
	resp, raw := alice.submit("alice-recent-01", transferBody{FromAccountID: e.account(seed.AliceILS),
		ToAccountID: e.account(seed.BobILS), Amount: 100, Currency: "ILS", Description: "x"})
	testutil.Equal(t, http.StatusCreated, resp.StatusCode, string(raw))

	resp, raw = e.anon().do("GET", "/api/transparency/audit/recent?limit=5", nil, nil)
	testutil.Equal(t, http.StatusOK, resp.StatusCode, string(raw))
	testutil.False(t, strings.Contains(string(raw), alice.sess.PrincipalID.String()), "no actor id")
	testutil.False(t, strings.Contains(string(raw), "actor_id"), "no actor_id field")
	testutil.False(t, strings.Contains(string(raw), "subject"), "no subject field")
	var r struct {
		Events []map[string]any `json:"events"`
	}
	testutil.NoError(t, json.Unmarshal(raw, &r))
	testutil.True(t, len(r.Events) >= 2, "events")
	for _, ev := range r.Events {
		testutil.Len(t, keys(ev), 4, "seq, at, action, hash_prefix")
		at, err := time.Parse(time.RFC3339, ev["at"].(string))
		testutil.NoError(t, err)
		testutil.True(t, at.Equal(at.Truncate(time.Hour)), "at truncated to the hour")
		testutil.False(t, strings.Contains(ev["action"].(string), "."), "action class only")
		testutil.Len(t, []byte(ev["hash_prefix"].(string)), 16)
	}
	testutil.Equal[any](t, "transfer", r.Events[0]["action"])
}

func keys(m map[string]any) []string {
	var out []string
	for k := range m {
		out = append(out, k)
	}
	return out
}

// I2 / R4: today's root is provisional and unsigned.
func TestTransparency_TodayRootUnsigned(t *testing.T) {
	e := newEnv(t)
	e.login("alice")
	today := time.Now().UTC().Format("2006-01-02")
	resp, raw := e.anon().do("GET", "/api/transparency/audit/root?day="+today, nil, nil)
	testutil.Equal(t, http.StatusOK, resp.StatusCode, string(raw))
	var r map[string]any
	testutil.NoError(t, json.Unmarshal(raw, &r))
	testutil.Equal[any](t, true, r["provisional"])
	testutil.Equal[any](t, today, r["day"])
	_, hasSig := r["signature"]
	testutil.False(t, hasSig, "no signature on a provisional root: "+string(raw))
	testutil.True(t, r["head_seq"].(float64) >= 1, "head includes the login")
	testutil.Len(t, []byte(r["head_hash"].(string)), 64)
}

// R5: GET /api/config tells the web app whether demo sign-in exists.
func TestConfig_DevTestLoginFlag(t *testing.T) {
	for _, on := range []bool{false, true} {
		e := newEnvWith(t, func(d *Deps) { d.DevTestLogin = on; d.Origin = "http://localhost:8080" })
		var cfg struct {
			DevTestLogin bool   `json:"dev_test_login"`
			Origin       string `json:"origin"`
		}
		e.anon().getJSON("/api/config", &cfg)
		testutil.Equal(t, on, cfg.DevTestLogin)
		testutil.Equal(t, "http://localhost:8080", cfg.Origin)
	}
}

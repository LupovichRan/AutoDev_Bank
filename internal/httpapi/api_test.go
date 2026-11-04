package httpapi

import (
	"bytes"
	"context"
	"crypto/ed25519"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/cookiejar"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"testing/fstest"
	"time"

	"github.com/LupovichRan/AutoDev_Bank/internal/approvals"
	"github.com/LupovichRan/AutoDev_Bank/internal/audit"
	"github.com/LupovichRan/AutoDev_Bank/internal/auth"
	"github.com/LupovichRan/AutoDev_Bank/internal/db"
	"github.com/LupovichRan/AutoDev_Bank/internal/ledger"
	"github.com/LupovichRan/AutoDev_Bank/internal/opendata"
	"github.com/LupovichRan/AutoDev_Bank/internal/opendata/providers"
	"github.com/LupovichRan/AutoDev_Bank/internal/seed"
	"github.com/LupovichRan/AutoDev_Bank/internal/testutil"
)

type testEnv struct {
	t    *testing.T
	srv  *httptest.Server
	auth *auth.Service
	rec  *audit.Recorder
	seed seed.Result
}

var testStatic = fstest.MapFS{
	"index.html":    {Data: []byte("<!doctype html><title>AutoDev Bank</title><div id=app></div>")},
	"sw.js":         {Data: []byte("self.addEventListener('fetch', () => {});")},
	"assets/app.js": {Data: []byte("console.log('app');")},
}

func newEnv(t *testing.T) *testEnv {
	t.Helper()
	ctx := context.Background()
	pool := db.TestPool(t)
	log := slog.New(slog.NewTextHandler(io.Discard, nil))

	_, signer, err := ed25519.GenerateKey(nil)
	testutil.NoError(t, err)
	rec := audit.New(pool, signer)
	led := ledger.New(pool)
	ap := approvals.New(pool, led, rec, approvals.DefaultPolicy())
	authSvc, err := auth.New(pool, auth.Config{
		RPID: "localhost", RPDisplayName: "AutoDev Bank", Origins: []string{"http://localhost"},
		AllowTestLogin: true, RateLimitPerMinute: 600, Auditor: NewAuditor(rec), Logger: log,
	})
	testutil.NoError(t, err)
	refresher := opendata.NewRefresher(pool, providers.FixtureProviders(providers.Fixtures), log)
	od := opendata.NewHandlers(opendata.NewStore(pool), refresher)
	od.Log = log

	res, err := seed.Run(ctx, pool, led, ap)
	testutil.NoError(t, err)

	srv := httptest.NewServer(New(Deps{
		Pool: pool, Ledger: led, Audit: rec, Approvals: ap, Auth: authSvc,
		OpenData: od, Static: testStatic, Log: log,
	}))
	t.Cleanup(srv.Close)
	return &testEnv{t: t, srv: srv, auth: authSvc, rec: rec, seed: res}
}

func (e *testEnv) account(code string) string {
	e.t.Helper()
	a, ok := e.seed.Accounts[code]
	if !ok {
		e.t.Fatalf("seed account %q missing", code)
	}
	return a.ID.String()
}

type apiClient struct {
	e    *testEnv
	hc   *http.Client
	sess auth.Session
}

// anon returns a client without a session.
func (e *testEnv) anon() *apiClient {
	jar, _ := cookiejar.New(nil)
	return &apiClient{e: e, hc: &http.Client{Jar: jar}}
}

// login signs handle in through POST /api/auth/dev-login.
func (e *testEnv) login(handle string) *apiClient {
	e.t.Helper()
	c := e.anon()
	resp, body := c.do("POST", "/api/auth/dev-login", map[string]string{"handle": handle}, nil)
	testutil.Equal(e.t, http.StatusOK, resp.StatusCode, string(body))
	u, _ := url.Parse(e.srv.URL)
	for _, ck := range c.hc.Jar.Cookies(u) {
		if ck.Name == auth.CookieName {
			sess, err := e.auth.LoadSession(context.Background(), ck.Value)
			testutil.NoError(e.t, err)
			c.sess = sess
		}
	}
	testutil.True(e.t, c.sess.ID != "", "session cookie set")
	return c
}

func (c *apiClient) do(method, path string, body any, hdr map[string]string) (*http.Response, []byte) {
	c.e.t.Helper()
	var rd io.Reader
	if body != nil {
		b, err := json.Marshal(body)
		testutil.NoError(c.e.t, err)
		rd = bytes.NewReader(b)
	}
	req, err := http.NewRequest(method, c.e.srv.URL+path, rd)
	testutil.NoError(c.e.t, err)
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	for k, v := range hdr {
		req.Header.Set(k, v)
	}
	resp, err := c.hc.Do(req)
	testutil.NoError(c.e.t, err)
	defer resp.Body.Close()
	out, err := io.ReadAll(resp.Body)
	testutil.NoError(c.e.t, err)
	return resp, out
}

func (c *apiClient) getJSON(path string, v any) {
	c.e.t.Helper()
	resp, body := c.do("GET", path, nil, nil)
	testutil.Equal(c.e.t, http.StatusOK, resp.StatusCode, path+": "+string(body))
	testutil.NoError(c.e.t, json.Unmarshal(body, v))
}

// stepUp records a verified step-up for exactly body (the server-side
// equivalent of a passkey assertion over its payload hash).
func (c *apiClient) stepUp(body transferBody) {
	c.e.t.Helper()
	testutil.NoError(c.e.t, c.e.auth.TestMarkSteppedUp(context.Background(), c.sess, transferPayloadHash(body)))
}

func (c *apiClient) transfer(key string, body transferBody) (*http.Response, []byte) {
	c.e.t.Helper()
	hdr := map[string]string{}
	if key != "" {
		hdr["Idempotency-Key"] = key
	}
	return c.do("POST", "/api/transfers", body, hdr)
}

type problemBody struct {
	Type      string `json:"type"`
	Title     string `json:"title"`
	Status    int    `json:"status"`
	Detail    string `json:"detail"`
	Instance  string `json:"instance"`
	RequestID string `json:"request_id"`
}

func decodeProblem(t *testing.T, resp *http.Response, body []byte) problemBody {
	t.Helper()
	testutil.Equal(t, "application/problem+json", resp.Header.Get("Content-Type"), string(body))
	var p problemBody
	testutil.NoError(t, json.Unmarshal(body, &p))
	return p
}

type tAccount struct {
	ID        string `json:"id"`
	Code      string `json:"code"`
	Currency  string `json:"currency"`
	Kind      string `json:"kind"`
	Balance   int64  `json:"balance"`
	Available int64  `json:"available"`
	Holders   []struct {
		PrincipalID string `json:"principal_id"`
		DisplayName string `json:"display_name"`
	} `json:"holders"`
}

func (c *apiClient) balances() map[string]tAccount {
	var r struct {
		Accounts []tAccount `json:"accounts"`
	}
	c.getJSON("/api/accounts", &r)
	out := map[string]tAccount{}
	for _, a := range r.Accounts {
		out[a.Code] = a
	}
	return out
}

type tApproval struct {
	ID        string `json:"id"`
	Status    string `json:"status"`
	Amount    int64  `json:"amount"`
	Initiator struct {
		PrincipalID string `json:"principal_id"`
		DisplayName string `json:"display_name"`
	} `json:"initiator"`
	RequiredApprovals int `json:"required_approvals"`
	Decisions         []struct {
		ApproverID string `json:"approver_id"`
		Decision   string `json:"decision"`
	} `json:"decisions"`
	Subject struct {
		FromAccountID string `json:"from_account_id"`
		ToAccountID   string `json:"to_account_id"`
		Description   string `json:"description"`
	} `json:"subject"`
}

type tApprovals struct {
	Inbox []tApproval `json:"inbox"`
	Mine  []tApproval `json:"mine"`
}

// ---------------------------------------------------------------------------

func TestTransfers_RequiresIdempotencyKey(t *testing.T) {
	e := newEnv(t)
	alice := e.login("alice")
	body := transferBody{FromAccountID: e.account(seed.AliceILS), ToAccountID: e.account(seed.BobILS),
		Amount: 5000, Currency: "ILS", Description: "lunch"}
	alice.stepUp(body)
	resp, raw := alice.transfer("", body)
	testutil.Equal(t, http.StatusBadRequest, resp.StatusCode, string(raw))
	testutil.Equal(t, "idempotency_key_required", decodeProblem(t, resp, raw).Detail)

	resp, raw = alice.transfer("short", body)
	testutil.Equal(t, http.StatusBadRequest, resp.StatusCode, string(raw))
	testutil.Equal(t, "idempotency_key_required", decodeProblem(t, resp, raw).Detail)

	// Without a session the request is refused before anything else.
	resp, raw = e.anon().transfer("key-0000001", body)
	testutil.Equal(t, http.StatusUnauthorized, resp.StatusCode, string(raw))
}

func TestTransfers_RequiresStepUp(t *testing.T) {
	e := newEnv(t)
	alice := e.login("alice")
	body := transferBody{FromAccountID: e.account(seed.AliceILS), ToAccountID: e.account(seed.BobILS),
		Amount: 5000, Currency: "ILS", Description: "lunch"}
	resp, raw := alice.transfer("key-stepup-1", body)
	testutil.Equal(t, http.StatusForbidden, resp.StatusCode, string(raw))
	testutil.Equal(t, "step_up_required", decodeProblem(t, resp, raw).Detail)
	testutil.Equal(t, int64(2500000), alice.balances()[seed.AliceILS].Balance)
}

func TestTransfers_SmallPosts201(t *testing.T) {
	e := newEnv(t)
	alice := e.login("alice")
	body := transferBody{FromAccountID: e.account(seed.AliceILS), ToAccountID: e.account(seed.BobILS),
		Amount: 5000, Currency: "ILS", Description: "café & <tea>"}
	alice.stepUp(body)
	resp, raw := alice.transfer("key-small-0001", body)
	testutil.Equal(t, http.StatusCreated, resp.StatusCode, string(raw))
	var out struct {
		Transaction struct {
			ID     string `json:"id"`
			Status string `json:"status"`
		} `json:"transaction"`
		Approval *json.RawMessage `json:"approval"`
	}
	testutil.NoError(t, json.Unmarshal(raw, &out))
	testutil.Equal(t, "posted", out.Transaction.Status)
	testutil.True(t, out.Approval == nil, "no approval for a small transfer")

	bal := alice.balances()
	testutil.Equal(t, int64(2500000-5000), bal[seed.AliceILS].Balance)
	testutil.Equal(t, int64(2500000-5000), bal[seed.AliceILS].Available)
	testutil.Equal(t, "ILS", bal[seed.AliceILS].Currency)
	testutil.Len(t, bal[seed.AliceILS].Holders, 1)
	testutil.Equal(t, "alice", strings.ToLower(bal[seed.AliceILS].Holders[0].DisplayName))
	testutil.Len(t, bal[seed.Joint].Holders, 2)
	_, hasBobs := bal[seed.BobILS]
	testutil.False(t, hasBobs, "alice does not see bob's account")

	var bobBal = e.login("bob").balances()
	testutil.Equal(t, int64(350000+5000), bobBal[seed.BobILS].Balance)

	var entries struct {
		Entries []struct {
			TransactionID string `json:"transaction_id"`
			Amount        int64  `json:"amount"`
			Currency      string `json:"currency"`
			BalanceAfter  int64  `json:"balance_after"`
			Description   string `json:"description"`
			Status        string `json:"status"`
			EffectiveDate string `json:"effective_date"`
			CreatedAt     string `json:"created_at"`
		} `json:"entries"`
	}
	alice.getJSON("/api/accounts/"+e.account(seed.AliceILS)+"/entries?limit=50", &entries)
	testutil.True(t, len(entries.Entries) >= 2, "funding and transfer entries")
	top := entries.Entries[0]
	testutil.Equal(t, out.Transaction.ID, top.TransactionID)
	testutil.Equal(t, int64(-5000), top.Amount)
	testutil.Equal(t, int64(2500000-5000), top.BalanceAfter)
	testutil.Equal(t, "café & <tea>", top.Description)
	testutil.Equal(t, "posted", top.Status)
	testutil.Equal(t, time.Now().UTC().Format("2006-01-02"), top.EffectiveDate)

	// Replay with the same key needs a fresh step-up and returns the same transaction.
	alice.stepUp(body)
	resp, raw = alice.transfer("key-small-0001", body)
	testutil.Equal(t, http.StatusCreated, resp.StatusCode, string(raw))
	var again struct {
		Transaction struct {
			ID string `json:"id"`
		} `json:"transaction"`
	}
	testutil.NoError(t, json.Unmarshal(raw, &again))
	testutil.Equal(t, out.Transaction.ID, again.Transaction.ID)
	testutil.Equal(t, int64(2500000-5000), alice.balances()[seed.AliceILS].Balance)

	// Same key, different body: conflict.
	other := body
	other.Amount = 5001
	alice.stepUp(other)
	resp, raw = alice.transfer("key-small-0001", other)
	testutil.Equal(t, http.StatusConflict, resp.StatusCode, string(raw))

	// Entries of an account alice does not hold are not found.
	resp, raw = alice.do("GET", "/api/accounts/"+e.account(seed.BobILS)+"/entries", nil, nil)
	testutil.Equal(t, http.StatusNotFound, resp.StatusCode, string(raw))
}

func TestTransfers_InsufficientFunds422(t *testing.T) {
	e := newEnv(t)
	bob := e.login("bob")
	body := transferBody{FromAccountID: e.account(seed.BobILS), ToAccountID: e.account(seed.AliceILS),
		Amount: 99999, Currency: "ILS", Description: "too much"}
	// 999.99 is below the approval threshold; bob only has 3,500.00, so post a
	// first transfer to drain most of it, then one more that cannot fit.
	drain := transferBody{FromAccountID: body.FromAccountID, ToAccountID: body.ToAccountID, Amount: 99999, Currency: "ILS", Description: "d"}
	for i, key := range []string{"drain-000001", "drain-000002", "drain-000003"} {
		drain.Description = "d" + string(rune('a'+i))
		bob.stepUp(drain)
		resp, raw := bob.transfer(key, drain)
		testutil.Equal(t, http.StatusCreated, resp.StatusCode, string(raw))
	}
	bob.stepUp(body)
	resp, raw := bob.transfer("drain-000004", body)
	testutil.Equal(t, http.StatusUnprocessableEntity, resp.StatusCode, string(raw))
	testutil.Equal(t, "insufficient_funds", decodeProblem(t, resp, raw).Detail)
}

func TestTransfers_LargeReturns202AndApproverCanDecide(t *testing.T) {
	e := newEnv(t)
	alice := e.login("alice")
	bob := e.login("bob")
	body := transferBody{FromAccountID: e.account(seed.Joint), ToAccountID: e.account(seed.AliceILS),
		Amount: 200000, Currency: "ILS", Description: "move savings"}
	alice.stepUp(body)
	resp, raw := alice.transfer("key-large-0001", body)
	testutil.Equal(t, http.StatusAccepted, resp.StatusCode, string(raw))
	var out struct {
		Approval tApproval `json:"approval"`
	}
	testutil.NoError(t, json.Unmarshal(raw, &out))
	testutil.Equal(t, "pending", out.Approval.Status)
	testutil.Equal(t, 1, out.Approval.RequiredApprovals)
	testutil.Equal(t, int64(200000), out.Approval.Amount)
	testutil.Equal(t, e.seed.Principals["alice"].String(), out.Approval.Initiator.PrincipalID)
	testutil.Equal(t, "move savings", out.Approval.Subject.Description)
	// Funds have not moved.
	testutil.Equal(t, int64(800000), alice.balances()[seed.Joint].Balance)

	var bl tApprovals
	bob.getJSON("/api/approvals", &bl)
	testutil.Len(t, bl.Inbox, 1)
	testutil.Equal(t, out.Approval.ID, bl.Inbox[0].ID)
	testutil.Equal(t, "Alice", bl.Inbox[0].Initiator.DisplayName)

	resp, raw = bob.do("POST", "/api/approvals/"+out.Approval.ID+"/decide", map[string]any{"approve": true, "reason": "ok"}, nil)
	testutil.Equal(t, http.StatusOK, resp.StatusCode, string(raw))
	var dec struct {
		Approval tApproval `json:"approval"`
	}
	testutil.NoError(t, json.Unmarshal(raw, &dec))
	testutil.Equal(t, "executed", dec.Approval.Status)
	testutil.Len(t, dec.Approval.Decisions, 1)
	testutil.Equal(t, e.seed.Principals["bob"].String(), dec.Approval.Decisions[0].ApproverID)

	var al tApprovals
	alice.getJSON("/api/approvals", &al)
	testutil.Len(t, al.Mine, 1)
	testutil.Equal(t, "executed", al.Mine[0].Status)
	testutil.Len(t, al.Inbox, 0)
	bal := alice.balances()
	testutil.Equal(t, int64(600000), bal[seed.Joint].Balance)
	testutil.Equal(t, int64(2700000), bal[seed.AliceILS].Balance)

	// Deciding again is a conflict; an unknown approval is not found.
	resp, raw = bob.do("POST", "/api/approvals/"+out.Approval.ID+"/decide", map[string]any{"approve": true, "reason": "again"}, nil)
	testutil.Equal(t, http.StatusConflict, resp.StatusCode, string(raw))
	resp, raw = bob.do("POST", "/api/approvals/0192f000-0000-7000-8000-000000000009/decide", map[string]any{"approve": true}, nil)
	testutil.Equal(t, http.StatusNotFound, resp.StatusCode, string(raw))
}

func TestTransfers_InitiatorCannotDecide403(t *testing.T) {
	e := newEnv(t)
	alice := e.login("alice")
	body := transferBody{FromAccountID: e.account(seed.Joint), ToAccountID: e.account(seed.AliceILS),
		Amount: 200000, Currency: "ILS", Description: "self approve?"}
	alice.stepUp(body)
	resp, raw := alice.transfer("key-self-00001", body)
	testutil.Equal(t, http.StatusAccepted, resp.StatusCode, string(raw))
	var out struct {
		Approval tApproval `json:"approval"`
	}
	testutil.NoError(t, json.Unmarshal(raw, &out))

	resp, raw = alice.do("POST", "/api/approvals/"+out.Approval.ID+"/decide", map[string]any{"approve": true, "reason": "me"}, nil)
	testutil.Equal(t, http.StatusForbidden, resp.StatusCode, string(raw))
	testutil.Equal(t, "self_approval", decodeProblem(t, resp, raw).Detail)

	// dave (payments.initiator, no approver role, not a holder) may not decide either.
	dave := e.login("dave")
	resp, raw = dave.do("POST", "/api/approvals/"+out.Approval.ID+"/decide", map[string]any{"approve": true, "reason": "x"}, nil)
	testutil.Equal(t, http.StatusForbidden, resp.StatusCode, string(raw))
	testutil.Equal(t, "not_authorized", decodeProblem(t, resp, raw).Detail)

	// bob cannot spend from alice's own account.
	bob := e.login("bob")
	steal := transferBody{FromAccountID: e.account(seed.AliceILS), ToAccountID: e.account(seed.BobILS),
		Amount: 100, Currency: "ILS", Description: "mine now"}
	bob.stepUp(steal)
	resp, raw = bob.transfer("key-steal-0001", steal)
	testutil.Equal(t, http.StatusForbidden, resp.StatusCode, string(raw))
	testutil.Equal(t, "not_authorized", decodeProblem(t, resp, raw).Detail)
}

func TestTransfers_StepUpHashMustMatchBody(t *testing.T) {
	e := newEnv(t)
	alice := e.login("alice")
	body := transferBody{FromAccountID: e.account(seed.AliceILS), ToAccountID: e.account(seed.BobILS),
		Amount: 5000, Currency: "ILS", Description: "lunch"}
	alice.stepUp(body)
	tampered := body
	tampered.Amount = 6000
	resp, raw := alice.transfer("key-tamper-001", tampered)
	testutil.Equal(t, http.StatusForbidden, resp.StatusCode, string(raw))
	testutil.Equal(t, "step_up_required", decodeProblem(t, resp, raw).Detail)
	testutil.Equal(t, int64(2500000), alice.balances()[seed.AliceILS].Balance)

	// The step-up still authorises exactly the body it was bound to, once.
	resp, raw = alice.transfer("key-tamper-002", body)
	testutil.Equal(t, http.StatusCreated, resp.StatusCode, string(raw))
	resp, raw = alice.transfer("key-tamper-003", body)
	testutil.Equal(t, http.StatusForbidden, resp.StatusCode, string(raw))
	testutil.Equal(t, "step_up_required", decodeProblem(t, resp, raw).Detail)
}

func TestTransparency_AuditVerifyOK(t *testing.T) {
	e := newEnv(t)
	alice := e.login("alice")
	body := transferBody{FromAccountID: e.account(seed.AliceILS), ToAccountID: e.account(seed.BobILS),
		Amount: 1234, Currency: "ILS", Description: "audited"}
	alice.stepUp(body)
	resp, raw := alice.transfer("key-audit-0001", body)
	testutil.Equal(t, http.StatusCreated, resp.StatusCode, string(raw))

	anon := e.anon()
	var v struct {
		OK          bool   `json:"ok"`
		Length      int64  `json:"length"`
		HeadHash    string `json:"head_hash"`
		FirstBadSeq *int64 `json:"first_bad_seq"`
	}
	anon.getJSON("/api/transparency/audit/verify", &v)
	testutil.True(t, v.OK, "chain verifies")
	testutil.True(t, v.Length >= 3, "dev_login, transfer.posted, transfer.submitted")
	testutil.Len(t, []byte(v.HeadHash), 64)
	testutil.True(t, v.FirstBadSeq == nil, "first_bad_seq is null when ok")

	var recent struct {
		Events []struct {
			Seq     int64   `json:"seq"`
			At      string  `json:"at"`
			Action  string  `json:"action"`
			Subject string  `json:"subject"`
			ActorID *string `json:"actor_id"`
		} `json:"events"`
	}
	anon.getJSON("/api/transparency/audit/recent?limit=5", &recent)
	testutil.True(t, len(recent.Events) >= 2 && len(recent.Events) <= 5, "bounded by limit")
	testutil.Equal(t, "transfer.submitted", recent.Events[0].Action)
	testutil.Equal(t, alice.sess.PrincipalID.String(), *recent.Events[0].ActorID)
	testutil.Equal(t, "transfer.posted", recent.Events[1].Action)
	testutil.Equal(t, v.Length, recent.Events[0].Seq)
}

func TestTransparency_FeesAndRoot(t *testing.T) {
	e := newEnv(t)
	anon := e.anon()
	var fees struct {
		Currency           string `json:"currency"`
		FXMarkupBP         int    `json:"currency_fx_markup_bp"`
		MonthlyFee         int64  `json:"monthly_fee"`
		IncomeSharePercent int    `json:"income_share_percent"`
		Rules              []struct {
			Name        string `json:"name"`
			Description string `json:"description"`
		} `json:"rules"`
	}
	anon.getJSON("/api/transparency/fees", &fees)
	testutil.Equal(t, "ILS", fees.Currency)
	testutil.Equal(t, 0, fees.FXMarkupBP)
	testutil.Equal(t, int64(0), fees.MonthlyFee)
	testutil.Equal(t, 50, fees.IncomeSharePercent)
	testutil.True(t, len(fees.Rules) >= 4 && len(fees.Rules) <= 6, "4-6 rules")

	e.login("alice") // at least one event today
	day := time.Now().UTC().Format("2006-01-02")
	var root struct {
		Day       string `json:"day"`
		HeadSeq   int64  `json:"head_seq"`
		HeadHash  string `json:"head_hash"`
		Signature string `json:"signature"`
		PublicKey string `json:"public_key"`
	}
	anon.getJSON("/api/transparency/audit/root?day="+day, &root)
	testutil.Equal(t, day, root.Day)
	testutil.True(t, root.HeadSeq >= 1, "today's head includes the login")
	sig, err := base64.StdEncoding.DecodeString(root.Signature)
	testutil.NoError(t, err)
	pub, err := base64.StdEncoding.DecodeString(root.PublicKey)
	testutil.NoError(t, err)
	testutil.Equal(t, hex.EncodeToString(e.rec.PublicKey()), hex.EncodeToString(pub))
	testutil.True(t, audit.VerifyRoot(ed25519.PublicKey(pub), audit.Root{
		Day: root.Day, HeadSeq: root.HeadSeq, HeadHash: root.HeadHash, Signature: sig,
	}), "signature verifies")

	for _, bad := range []string{"2026-13-01", "yesterday", time.Now().UTC().AddDate(0, 0, 2).Format("2006-01-02")} {
		resp, raw := anon.do("GET", "/api/transparency/audit/root?day="+bad, nil, nil)
		testutil.Equal(t, http.StatusBadRequest, resp.StatusCode, bad+": "+string(raw))
	}
}

func TestOpenData_FXPublicAndRefreshStaffOnly(t *testing.T) {
	e := newEnv(t)
	anon := e.anon()
	resp, raw := anon.do("GET", "/api/opendata/fx", nil, nil)
	testutil.Equal(t, http.StatusOK, resp.StatusCode, string(raw))

	resp, raw = anon.do("POST", "/api/opendata/refresh", map[string]any{}, nil)
	testutil.Equal(t, http.StatusUnauthorized, resp.StatusCode, string(raw))
	resp, raw = e.login("alice").do("POST", "/api/opendata/refresh", map[string]any{}, nil)
	testutil.Equal(t, http.StatusForbidden, resp.StatusCode, string(raw))

	resp, raw = e.login("carol").do("POST", "/api/opendata/refresh", map[string]any{}, nil)
	testutil.True(t, resp.StatusCode == http.StatusOK || resp.StatusCode == http.StatusConflict, string(raw))

	var fx struct {
		Count int `json:"count"`
	}
	anon.getJSON("/api/opendata/fx", &fx)
	testutil.True(t, fx.Count > 0, "fixture fx rates stored")
}

func TestStatic_SPAFallback(t *testing.T) {
	e := newEnv(t)
	c := e.anon()
	for _, p := range []string{"/", "/pay", "/some/deep/route"} {
		resp, raw := c.do("GET", p, nil, nil)
		testutil.Equal(t, http.StatusOK, resp.StatusCode, p)
		testutil.True(t, strings.Contains(string(raw), "<div id=app>"), p+" serves index.html")
		testutil.True(t, strings.HasPrefix(resp.Header.Get("Content-Type"), "text/html"), p)
	}
	resp, raw := c.do("GET", "/assets/app.js", nil, nil)
	testutil.Equal(t, http.StatusOK, resp.StatusCode)
	testutil.Equal(t, "console.log('app');", string(raw))

	resp, _ = c.do("GET", "/sw.js", nil, nil)
	testutil.Equal(t, http.StatusOK, resp.StatusCode)
	testutil.Equal(t, "no-cache", resp.Header.Get("Cache-Control"))

	resp, _ = c.do("GET", "/assets/missing.js", nil, nil)
	testutil.Equal(t, http.StatusNotFound, resp.StatusCode)

	resp, raw = c.do("GET", "/api/does-not-exist", nil, nil)
	testutil.Equal(t, http.StatusNotFound, resp.StatusCode)
	decodeProblem(t, resp, raw)

	var h struct {
		OK bool `json:"ok"`
	}
	c.getJSON("/healthz", &h)
	testutil.True(t, h.OK, "healthz ok")
}

func TestSecurityHeadersAndProblemShape(t *testing.T) {
	e := newEnv(t)
	c := e.anon()
	resp, raw := c.do("GET", "/api/accounts", nil, map[string]string{"X-Request-Id": "req-abc-123"})
	testutil.Equal(t, http.StatusUnauthorized, resp.StatusCode)
	p := decodeProblem(t, resp, raw)
	testutil.Equal(t, "about:blank", p.Type)
	testutil.Equal(t, 401, p.Status)
	testutil.Equal(t, "/api/accounts", p.Instance)
	testutil.Equal(t, "req-abc-123", p.RequestID)
	testutil.True(t, p.Title != "" && p.Detail != "", "title and detail")
	testutil.Equal(t, "req-abc-123", resp.Header.Get("X-Request-Id"))
	testutil.Equal(t, "no-store", resp.Header.Get("Cache-Control"))

	for _, path := range []string{"/", "/api/transparency/fees"} {
		resp, _ := c.do("GET", path, nil, nil)
		h := resp.Header
		testutil.Equal(t, "nosniff", h.Get("X-Content-Type-Options"), path)
		testutil.Equal(t, "same-origin", h.Get("Referrer-Policy"), path)
		testutil.Equal(t, "default-src 'self'; img-src 'self' data:; style-src 'self' 'unsafe-inline'; connect-src 'self'",
			h.Get("Content-Security-Policy"), path)
		testutil.Equal(t, "geolocation=(self)", h.Get("Permissions-Policy"), path)
		testutil.True(t, h.Get("X-Request-Id") != "", "generated request id")
	}

	// Bodies: unknown fields and oversized bodies are rejected.
	alice := e.login("alice")
	resp, raw = alice.do("POST", "/api/transfers", map[string]any{"amount": 1, "bogus": true},
		map[string]string{"Idempotency-Key": "key-bogus-0001"})
	testutil.Equal(t, http.StatusBadRequest, resp.StatusCode, string(raw))
	decodeProblem(t, resp, raw)
	big := map[string]any{"approve": true, "reason": strings.Repeat("x", 70<<10)}
	resp, raw = alice.do("POST", "/api/approvals/0192f000-0000-7000-8000-000000000009/decide", big, nil)
	testutil.True(t, resp.StatusCode == http.StatusRequestEntityTooLarge || resp.StatusCode == http.StatusBadRequest, string(raw))
}

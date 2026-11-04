package httpapi

import (
	"bytes"
	"context"
	"crypto/ed25519"
	"encoding/base64"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/LupovichRan/AutoDev_Bank/internal/testutil"
)

type tRoot struct {
	Day         string `json:"day"`
	HeadSeq     int64  `json:"head_seq"`
	HeadHash    string `json:"head_hash"`
	Signature   string `json:"signature"`
	PublicKey   string `json:"public_key"`
	Verified    bool   `json:"verified"`
	Provisional bool   `json:"provisional"`
}

func (e *testEnv) storedRootCount(t *testing.T) int {
	t.Helper()
	var n int
	testutil.NoError(t, e.pool.QueryRow(context.Background(), `select count(*) from control.audit_roots`).Scan(&n))
	return n
}

func TestTransparency_StoredRootIsNeverRewritten(t *testing.T) {
	e := newEnv(t)
	ctx := context.Background()
	anon := e.anon()
	yesterday := time.Now().UTC().AddDate(0, 0, -1)
	day := yesterday.Format("2006-01-02")

	_, err := e.rec.SignDailyRoot(ctx, yesterday)
	testutil.NoError(t, err)
	bad := bytes.Repeat([]byte{9}, ed25519.SignatureSize)
	_, err = e.pool.Exec(ctx, `update control.audit_roots set signature = $1 where day = $2::date`, bad, day)
	testutil.NoError(t, err)

	var root tRoot
	anon.getJSON("/api/transparency/audit/root?day="+day, &root)
	testutil.Equal(t, day, root.Day)
	testutil.False(t, root.Verified, "corrupted root reported as unverified")
	testutil.False(t, root.Provisional, "finished day is not provisional")
	testutil.Equal(t, base64.StdEncoding.EncodeToString(bad), root.Signature)

	// The default (yesterday) also returns the stored row as is.
	anon.getJSON("/api/transparency/audit/root", &root)
	testutil.False(t, root.Verified, "default day returns stored root")

	var sig []byte
	testutil.NoError(t, e.pool.QueryRow(ctx, `select signature from control.audit_roots where day = $1::date`, day).Scan(&sig))
	testutil.True(t, bytes.Equal(bad, sig), "stored root not rewritten by GET")
	testutil.Equal(t, 1, e.storedRootCount(t))
}

func TestTransparency_RootFillsMissingDayAndTodayIsProvisional(t *testing.T) {
	e := newEnv(t)
	anon := e.anon()
	e.login("alice")

	// Today: the current head, never signed, never stored.
	today := time.Now().UTC().Format("2006-01-02")
	var root tRoot
	anon.getJSON("/api/transparency/audit/root?day="+today, &root)
	testutil.True(t, root.Provisional, "today is provisional")
	testutil.Equal(t, "", root.Signature, "today is not signed")
	testutil.False(t, root.Verified, "nothing to verify")
	testutil.True(t, root.HeadSeq >= 1, "head includes the login")
	testutil.Equal(t, 0, e.storedRootCount(t))

	// A missing finished day is filled once.
	past := time.Now().UTC().AddDate(0, 0, -3).Format("2006-01-02")
	anon.getJSON("/api/transparency/audit/root?day="+past, &root)
	testutil.False(t, root.Provisional, "finished day")
	testutil.True(t, root.Verified, "fresh root verifies")
	testutil.Equal(t, int64(0), root.HeadSeq)
	testutil.Equal(t, 1, e.storedRootCount(t))
}

// fakeClock is a settable clock for the cache and rate limiter.
type fakeClock struct {
	mu sync.Mutex
	t  time.Time
}

func (c *fakeClock) now() time.Time {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.t
}

func (c *fakeClock) advance(d time.Duration) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.t = c.t.Add(d)
}

func TestTransparency_VerifyIsCached(t *testing.T) {
	clock := &fakeClock{t: time.Now()}
	e := newEnvWith(t, func(d *Deps) { d.now = clock.now })
	anon := e.anon()
	var v struct {
		Length int64 `json:"length"`
	}
	anon.getJSON("/api/transparency/audit/verify", &v)
	before := v.Length

	e.login("alice") // appends an audit event
	anon.getJSON("/api/transparency/audit/verify", &v)
	testutil.Equal(t, before, v.Length, "cached report within TTL")

	clock.advance(verifyCacheTTL + time.Second)
	anon.getJSON("/api/transparency/audit/verify", &v)
	testutil.Equal(t, before+1, v.Length, "fresh report after TTL")
}

func TestPublicEndpoints_RateLimited(t *testing.T) {
	clock := &fakeClock{t: time.Now()}
	e := newEnvWith(t, func(d *Deps) {
		d.PublicRateLimitPerMinute = 3
		d.now = clock.now
	})
	anon := e.anon()
	for _, p := range []string{"/api/opendata/fx", "/api/transparency/fees", "/api/opendata/status"} {
		resp, raw := anon.do("GET", p, nil, nil)
		testutil.Equal(t, http.StatusOK, resp.StatusCode, p+": "+string(raw))
	}
	resp, raw := anon.do("GET", "/api/transparency/audit/verify", nil, nil)
	testutil.Equal(t, http.StatusTooManyRequests, resp.StatusCode, string(raw))
	p := decodeProblem(t, resp, raw)
	testutil.Equal(t, "rate_limited", p.Detail)
	testutil.True(t, resp.Header.Get("Retry-After") != "", "Retry-After set")

	// Other routes are not throttled by this limiter.
	resp, _ = anon.do("GET", "/healthz", nil, nil)
	testutil.Equal(t, http.StatusOK, resp.StatusCode)

	clock.advance(time.Minute)
	resp, raw = anon.do("GET", "/api/transparency/fees", nil, nil)
	testutil.Equal(t, http.StatusOK, resp.StatusCode, string(raw))
}

func TestJSONEndpoints_RequireJSONContentType(t *testing.T) {
	e := newEnv(t)
	alice := e.login("alice")
	for _, ct := range []string{"", "text/plain", "application/x-www-form-urlencoded"} {
		hdr := map[string]string{"Idempotency-Key": "key-ctype-0001"}
		req, err := http.NewRequest("POST", e.srv.URL+"/api/transfers", strings.NewReader(`{"amount":1}`))
		testutil.NoError(t, err)
		for k, v := range hdr {
			req.Header.Set(k, v)
		}
		if ct != "" {
			req.Header.Set("Content-Type", ct)
		}
		resp, err := alice.hc.Do(req)
		testutil.NoError(t, err)
		resp.Body.Close()
		testutil.Equal(t, http.StatusUnsupportedMediaType, resp.StatusCode, ct)
	}
	// Parameters are fine.
	resp, raw := alice.do("POST", "/api/approvals/0192f000-0000-7000-8000-000000000009/decide",
		map[string]any{"approve": false}, map[string]string{"Content-Type": "application/json; charset=utf-8"})
	testutil.Equal(t, http.StatusNotFound, resp.StatusCode, string(raw))
}

func TestBodyCap_AppliesToEveryRequest(t *testing.T) {
	big := strings.Repeat("x", 70<<10)
	// Any handler, even one that reads without its own limit, sees the cap.
	var readErr error
	h := limitBody(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, readErr = io.ReadAll(r.Body)
	}))
	h.ServeHTTP(httptest.NewRecorder(), httptest.NewRequest("POST", "/api/auth/x", strings.NewReader(big)))
	var tooBig *http.MaxBytesError
	testutil.True(t, errors.As(readErr, &tooBig), "global cap applied")

	e := newEnv(t)
	alice := e.login("alice")
	resp, raw := alice.do("POST", "/api/approvals/0192f000-0000-7000-8000-000000000009/decide",
		map[string]any{"approve": true, "reason": big}, nil)
	testutil.Equal(t, http.StatusRequestEntityTooLarge, resp.StatusCode, string(raw))
}

func TestSecurityHeaders_CSPAndHSTS(t *testing.T) {
	e := newEnv(t)
	resp, _ := e.anon().do("GET", "/", nil, nil)
	testutil.Equal(t, "default-src 'self'; img-src 'self' data:; style-src 'self'; connect-src 'self'; "+
		"base-uri 'none'; form-action 'self'; frame-ancestors 'none'; object-src 'none'",
		resp.Header.Get("Content-Security-Policy"))
	testutil.Equal(t, "", resp.Header.Get("Strict-Transport-Security"), "no HSTS on http origin")

	s := newEnvWith(t, func(d *Deps) { d.Origin = "https://bank.example" })
	resp, _ = s.anon().do("GET", "/", nil, nil)
	testutil.Equal(t, "max-age=63072000; includeSubDomains", resp.Header.Get("Strict-Transport-Security"))
}

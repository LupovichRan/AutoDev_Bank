package auth

import (
	"bytes"
	"context"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/LupovichRan/AutoDev_Bank/internal/testutil"
)

type testClient struct {
	t      *testing.T
	h      http.Handler
	cookie *http.Cookie
	ip     string
}

func newTestClient(t *testing.T, s *Service) *testClient {
	mux := http.NewServeMux()
	s.Mount(mux)
	return &testClient{t: t, h: mux, ip: "192.0.2.10:4711"}
}

func (c *testClient) do(method, path string, body any) *httptest.ResponseRecorder {
	c.t.Helper()
	var buf bytes.Buffer
	if body != nil {
		testutil.NoError(c.t, json.NewEncoder(&buf).Encode(body))
	}
	r := httptest.NewRequest(method, path, &buf)
	r.RemoteAddr = c.ip
	r.Header.Set("Content-Type", "application/json")
	r.Header.Set("User-Agent", "handlers-test")
	if c.cookie != nil {
		r.AddCookie(c.cookie)
	}
	w := httptest.NewRecorder()
	c.h.ServeHTTP(w, r)
	for _, ck := range w.Result().Cookies() {
		if ck.Name == CookieName {
			if ck.MaxAge < 0 || ck.Value == "" {
				c.cookie = nil
			} else {
				c.cookie = ck
			}
		}
	}
	return w
}

type beginResp struct {
	ChallengeID string          `json:"challenge_id"`
	Options     json.RawMessage `json:"options"`
}

func decode[T any](t *testing.T, w *httptest.ResponseRecorder) T {
	t.Helper()
	var v T
	testutil.NoError(t, json.Unmarshal(w.Body.Bytes(), &v), w.Body.String())
	return v
}

func TestHandlers_MeRequiresSession(t *testing.T) {
	s, _ := newTestService(t)
	c := newTestClient(t, s)
	w := c.do("GET", "/api/me", nil)
	testutil.Equal(t, http.StatusUnauthorized, w.Code)
	testutil.Equal(t, "application/problem+json", w.Header().Get("Content-Type"))
	p := decode[map[string]any](t, w)
	testutil.Equal(t, map[string]any{"type": "about:blank", "title": "Unauthorized", "status": float64(401), "detail": "sign in required"}, p)

	// A bogus cookie is ignored, not an error.
	c.cookie = &http.Cookie{Name: CookieName, Value: "nope"}
	testutil.Equal(t, http.StatusUnauthorized, c.do("GET", "/api/me", nil).Code)
}

func TestHandlers_CookieFlags(t *testing.T) {
	s, aud := newTestService(t)
	c := newTestClient(t, s)
	w := c.do("POST", "/api/auth/dev-login", map[string]string{"handle": "kim"})
	testutil.Equal(t, http.StatusOK, w.Code, w.Body.String())
	testutil.True(t, aud.has("auth.dev_login"), "dev login audited")

	var ck *http.Cookie
	for _, x := range w.Result().Cookies() {
		if x.Name == "bank_session" {
			ck = x
		}
	}
	testutil.True(t, ck != nil, "bank_session cookie set")
	testutil.True(t, ck.HttpOnly, "HttpOnly")
	testutil.Equal(t, http.SameSiteStrictMode, ck.SameSite)
	testutil.Equal(t, "/", ck.Path)
	testutil.False(t, ck.Secure, "not Secure for an http origin")

	me := decode[map[string]any](t, c.do("GET", "/api/me", nil))
	testutil.Equal(t, "kim", me["handle"])
	testutil.Equal(t, "customer", me["kind"])
	testutil.Equal[any](t, []any{}, me["roles"])
	testutil.True(t, me["principal_id"] != "", "principal id")

	w = c.do("POST", "/api/auth/logout", nil)
	testutil.Equal(t, http.StatusNoContent, w.Code)
	testutil.True(t, aud.has("auth.logout"), "logout audited")
	testutil.True(t, c.cookie == nil, "cookie cleared")
	c.cookie = ck // the old cookie no longer works
	testutil.Equal(t, http.StatusUnauthorized, c.do("GET", "/api/me", nil).Code)
}

func TestHandlers_CookieSecureForHTTPSOrigin(t *testing.T) {
	s, _ := newTestService(t, func(c *Config) { c.Origins = []string{"https://localhost:8443"} })
	c := newTestClient(t, s)
	w := c.do("POST", "/api/auth/dev-login", map[string]string{"handle": "lee"})
	testutil.Equal(t, http.StatusOK, w.Code)
	testutil.True(t, c.cookie != nil && c.cookie.Secure, "Secure for an https origin")
}

func TestHandlers_DevLoginDisabled(t *testing.T) {
	s, _ := newTestService(t, func(c *Config) { c.AllowTestLogin = false })
	c := newTestClient(t, s)
	testutil.Equal(t, http.StatusNotFound, c.do("POST", "/api/auth/dev-login", map[string]string{"handle": "x"}).Code)
}

func TestHandlers_RegisterLoginStepUpFlow(t *testing.T) {
	s, _ := newTestService(t)
	c := newTestClient(t, s)
	authr := newSoftAuthenticator(t)

	w := c.do("POST", "/api/auth/register/begin", map[string]string{"handle": "mona", "display_name": "Mona"})
	testutil.Equal(t, http.StatusOK, w.Code, w.Body.String())
	b := decode[beginResp](t, w)
	w = c.do("POST", "/api/auth/register/finish", map[string]any{"challenge_id": b.ChallengeID, "credential": authr.create(t, b.Options)})
	testutil.Equal(t, http.StatusOK, w.Code, w.Body.String())
	pid := decode[map[string]string](t, w)["principal_id"]
	testutil.True(t, c.cookie != nil, "register logs you in")
	testutil.Equal(t, http.StatusOK, c.do("GET", "/api/me", nil).Code)

	// Someone else cannot start a registration for an existing handle.
	other := newTestClient(t, s)
	other.ip = "192.0.2.99:1"
	testutil.Equal(t, http.StatusConflict, other.do("POST", "/api/auth/register/begin", map[string]string{"handle": "mona", "display_name": "x"}).Code)

	c.do("POST", "/api/auth/logout", nil)
	w = c.do("POST", "/api/auth/login/begin", map[string]string{})
	testutil.Equal(t, http.StatusOK, w.Code, w.Body.String())
	b = decode[beginResp](t, w)
	w = c.do("POST", "/api/auth/login/finish", map[string]any{"challenge_id": b.ChallengeID, "credential": authr.get(t, b.Options)})
	testutil.Equal(t, http.StatusOK, w.Code, w.Body.String())
	testutil.Equal(t, pid, decode[map[string]string](t, w)["principal_id"])

	hash := bytes.Repeat([]byte{0xab}, 32)
	w = c.do("POST", "/api/auth/stepup/begin", map[string]string{"payload_hash": hex.EncodeToString(hash)})
	testutil.Equal(t, http.StatusOK, w.Code, w.Body.String())
	b = decode[beginResp](t, w)
	w = c.do("POST", "/api/auth/stepup/finish", map[string]any{"challenge_id": b.ChallengeID, "credential": authr.get(t, b.Options)})
	testutil.Equal(t, http.StatusOK, w.Code, w.Body.String())
	testutil.Equal(t, map[string]any{"stepped_up": true}, decode[map[string]any](t, w))

	sess, err := s.LoadSession(context.Background(), c.cookie.Value)
	testutil.NoError(t, err)
	testutil.NoError(t, s.RequireStepUp(context.Background(), sess, hash))

	// Bad finish payload is a 400 problem, not a 500.
	w = c.do("POST", "/api/auth/login/finish", map[string]any{"challenge_id": "nope", "credential": map[string]any{}})
	testutil.Equal(t, http.StatusBadRequest, w.Code, w.Body.String())
	testutil.Equal(t, "application/problem+json", w.Header().Get("Content-Type"))
}

func TestHandlers_StepUpBeginRequiresSession(t *testing.T) {
	s, _ := newTestService(t)
	c := newTestClient(t, s)
	w := c.do("POST", "/api/auth/stepup/begin", map[string]string{"payload_hash": "00"})
	testutil.Equal(t, http.StatusUnauthorized, w.Code)
}

func TestHandlers_RateLimit(t *testing.T) {
	s, _ := newTestService(t)
	c := newTestClient(t, s)
	for i := 0; i < 10; i++ {
		testutil.Equal(t, http.StatusOK, c.do("POST", "/api/auth/login/begin", map[string]string{}).Code)
	}
	w := c.do("POST", "/api/auth/login/begin", map[string]string{})
	testutil.Equal(t, http.StatusTooManyRequests, w.Code)
	testutil.Equal(t, "application/problem+json", w.Header().Get("Content-Type"))

	c.ip = "198.51.100.7:1" // other clients are unaffected
	testutil.Equal(t, http.StatusOK, c.do("POST", "/api/auth/login/begin", map[string]string{}).Code)
}

func TestMiddleware_RequireRole(t *testing.T) {
	s, _ := newTestService(t)
	ctx := context.Background()
	pid := mustPrincipal(t, s, "nina")
	sess, err := s.TestLogin(ctx, pid)
	testutil.NoError(t, err)
	h := s.Authenticate(s.RequireRole("payments.approver")(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusTeapot)
	})))
	call := func(withCookie bool) int {
		r := httptest.NewRequest("GET", "/x", nil)
		if withCookie {
			r.AddCookie(&http.Cookie{Name: CookieName, Value: sess.ID})
		}
		w := httptest.NewRecorder()
		h.ServeHTTP(w, r)
		return w.Code
	}
	testutil.Equal(t, http.StatusUnauthorized, call(false))
	testutil.Equal(t, http.StatusForbidden, call(true))
	_, err = s.pool.Exec(ctx, `insert into control.roles (principal_id, role) values ($1, 'payments.approver')`, pid)
	testutil.NoError(t, err)
	testutil.Equal(t, http.StatusTeapot, call(true))
}

func TestHandlers_RegisterCancelledThenRetried(t *testing.T) {
	s, _ := newTestService(t)
	c := newTestClient(t, s)
	w := c.do("POST", "/api/auth/register/begin", map[string]string{"handle": "nora"})
	testutil.Equal(t, http.StatusOK, w.Code, w.Body.String())
	testutil.Equal(t, 0, principalCount(t, s, "nora"))

	// The user cancelled; retrying (even from another client) is allowed.
	retry := newTestClient(t, s)
	retry.ip = "192.0.2.50:1"
	w = retry.do("POST", "/api/auth/register/begin", map[string]string{"handle": "nora"})
	testutil.Equal(t, http.StatusOK, w.Code, w.Body.String())
	b := decode[beginResp](t, w)
	w = retry.do("POST", "/api/auth/register/finish", map[string]any{"challenge_id": b.ChallengeID, "credential": newSoftAuthenticator(t).create(t, b.Options)})
	testutil.Equal(t, http.StatusOK, w.Code, w.Body.String())
	testutil.Equal(t, 1, principalCount(t, s, "nora"))

	// Now the handle is taken: begin is a 409 for anyone not signed in as nora.
	testutil.Equal(t, http.StatusConflict, c.do("POST", "/api/auth/register/begin", map[string]string{"handle": "nora"}).Code)
}

func TestHandlers_RegisterFinishHandleTakenIs409(t *testing.T) {
	s, _ := newTestService(t)
	a, b := newTestClient(t, s), newTestClient(t, s)
	b.ip = "192.0.2.51:1"
	ba := decode[beginResp](t, a.do("POST", "/api/auth/register/begin", map[string]string{"handle": "omar"}))
	bb := decode[beginResp](t, b.do("POST", "/api/auth/register/begin", map[string]string{"handle": "omar"}))
	w := a.do("POST", "/api/auth/register/finish", map[string]any{"challenge_id": ba.ChallengeID, "credential": newSoftAuthenticator(t).create(t, ba.Options)})
	testutil.Equal(t, http.StatusOK, w.Code, w.Body.String())
	w = b.do("POST", "/api/auth/register/finish", map[string]any{"challenge_id": bb.ChallengeID, "credential": newSoftAuthenticator(t).create(t, bb.Options)})
	testutil.Equal(t, http.StatusConflict, w.Code, w.Body.String())
	testutil.True(t, b.cookie == nil, "the loser is not signed in")
}

func TestHandlers_AddCredentialNeedsStepUp(t *testing.T) {
	s, aud := newTestService(t)
	c := newTestClient(t, s)
	authr := newSoftAuthenticator(t)
	b := decode[beginResp](t, c.do("POST", "/api/auth/register/begin", map[string]string{"handle": "pete"}))
	w := c.do("POST", "/api/auth/register/finish", map[string]any{"challenge_id": b.ChallengeID, "credential": authr.create(t, b.Options)})
	testutil.Equal(t, http.StatusOK, w.Code, w.Body.String())
	actingSession := c.cookie.Value

	// Signed in, same handle, but no step-up: refused.
	w = c.do("POST", "/api/auth/register/begin", map[string]string{"handle": "pete"})
	testutil.Equal(t, http.StatusForbidden, w.Code, w.Body.String())
	testutil.Equal(t, "application/problem+json", w.Header().Get("Content-Type"))

	// Step up for "add-credential:pete", then begin succeeds.
	hash := AddCredentialPayloadHash("pete")
	b = decode[beginResp](t, c.do("POST", "/api/auth/stepup/begin", map[string]string{"payload_hash": hex.EncodeToString(hash)}))
	w = c.do("POST", "/api/auth/stepup/finish", map[string]any{"challenge_id": b.ChallengeID, "credential": authr.get(t, b.Options)})
	testutil.Equal(t, http.StatusOK, w.Code, w.Body.String())
	w = c.do("POST", "/api/auth/register/begin", map[string]string{"handle": "pete"})
	testutil.Equal(t, http.StatusOK, w.Code, w.Body.String())
	b = decode[beginResp](t, w)
	second := newSoftAuthenticator(t)
	w = c.do("POST", "/api/auth/register/finish", map[string]any{"challenge_id": b.ChallengeID, "credential": second.create(t, b.Options)})
	testutil.Equal(t, http.StatusOK, w.Code, w.Body.String())
	testutil.True(t, aud.has("auth.credential_added"), "credential_added audited")
	testutil.Equal[any](t, actingSession, aud.last("auth.credential_added")["session_id"])
	testutil.True(t, c.cookie.Value != actingSession, "session replaced")
	_, err := s.LoadSession(context.Background(), actingSession)
	testutil.ErrorIs(t, err, ErrNoSession)
	var n int
	testutil.NoError(t, s.pool.QueryRow(context.Background(), `select count(*) from control.credentials`).Scan(&n))
	testutil.Equal(t, 2, n)
}

func TestHandlers_RegisterFinishReplacesSession(t *testing.T) {
	s, _ := newTestService(t)
	c := newTestClient(t, s)
	testutil.Equal(t, http.StatusOK, c.do("POST", "/api/auth/dev-login", map[string]string{"handle": "quin"}).Code)
	old := c.cookie.Value
	b := decode[beginResp](t, c.do("POST", "/api/auth/register/begin", map[string]string{"handle": "rhea"}))
	w := c.do("POST", "/api/auth/register/finish", map[string]any{"challenge_id": b.ChallengeID, "credential": newSoftAuthenticator(t).create(t, b.Options)})
	testutil.Equal(t, http.StatusOK, w.Code, w.Body.String())
	testutil.True(t, c.cookie.Value != old, "new cookie")
	_, err := s.LoadSession(context.Background(), old)
	testutil.ErrorIs(t, err, ErrNoSession, "previous session deleted")
	me := decode[map[string]any](t, c.do("GET", "/api/me", nil))
	testutil.Equal(t, "rhea", me["handle"])
}

func TestRateLimiter_CapsBuckets(t *testing.T) {
	l := newRateLimiter(10, time.Minute)
	l.maxBuckets = 100
	now := time.Unix(1_000_000, 0)
	l.now = func() time.Time { return now }
	for i := 0; i < 1000; i++ {
		now = now.Add(time.Millisecond)
		testutil.True(t, l.allow(fmt.Sprintf("10.0.%d.%d", i/256, i%256)))
		testutil.True(t, len(l.buckets) <= 100, "bucket map is capped")
	}
	_, newest := l.buckets["10.0.3.231"]
	testutil.True(t, newest, "the most recent key is kept")
	_, oldest := l.buckets["10.0.0.0"]
	testutil.False(t, oldest, "the oldest key was evicted")
}

// syncBuffer is a goroutine-safe log sink.
type syncBuffer struct {
	mu sync.Mutex
	b  bytes.Buffer
}

func (s *syncBuffer) Write(p []byte) (int, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.b.Write(p)
}

func (s *syncBuffer) String() string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.b.String()
}

func TestHandlers_RateLimitRunsBeforeSessionLookup(t *testing.T) {
	logs := &syncBuffer{}
	s, _ := newTestService(t, func(c *Config) { c.Logger = slog.New(slog.NewTextHandler(logs, nil)) })
	mux := http.NewServeMux()
	s.Mount(mux)

	// A request whose context is already cancelled makes any session lookup
	// fail loudly ("auth: load session"), which tells us whether it ran.
	probe := func(ip string) int {
		ctx, cancel := context.WithCancel(context.Background())
		cancel()
		r := httptest.NewRequest("POST", "/api/auth/login/begin", bytes.NewReader([]byte(`{}`))).WithContext(ctx)
		r.RemoteAddr = ip
		r.AddCookie(&http.Cookie{Name: CookieName, Value: "some-session"})
		w := httptest.NewRecorder()
		mux.ServeHTTP(w, r)
		return w.Code
	}
	c := newTestClient(t, s)
	for i := 0; i < 10; i++ {
		testutil.Equal(t, http.StatusOK, c.do("POST", "/api/auth/login/begin", map[string]string{}).Code)
	}
	testutil.Equal(t, http.StatusTooManyRequests, probe(c.ip))
	testutil.False(t, strings.Contains(logs.String(), "auth: load session"), "throttled request never looked up its session")

	probe("198.51.100.200:1") // an unthrottled client does reach the lookup
	testutil.True(t, strings.Contains(logs.String(), "auth: load session"), "probe is sensitive")
}

func TestHandlers_DevStepUp(t *testing.T) {
	s, aud := newTestService(t)
	c := newTestClient(t, s)
	hash := hex.EncodeToString(AddCredentialPayloadHash("uma"))
	// Needs a session.
	testutil.Equal(t, http.StatusUnauthorized, c.do("POST", "/api/auth/dev-stepup", map[string]string{"payload_hash": hash}).Code)
	testutil.Equal(t, http.StatusOK, c.do("POST", "/api/auth/dev-login", map[string]string{"handle": "uma"}).Code)
	testutil.Equal(t, http.StatusBadRequest, c.do("POST", "/api/auth/dev-stepup", map[string]string{"payload_hash": "zz"}).Code)
	testutil.Equal(t, http.StatusBadRequest, c.do("POST", "/api/auth/dev-stepup", map[string]string{"payload_hash": "abcd"}).Code)

	// A dev-login principal has no passkey; adding one needs a step-up.
	testutil.Equal(t, http.StatusForbidden, c.do("POST", "/api/auth/register/begin", map[string]string{"handle": "uma"}).Code)
	w := c.do("POST", "/api/auth/dev-stepup", map[string]string{"payload_hash": hash})
	testutil.Equal(t, http.StatusOK, w.Code, w.Body.String())
	testutil.True(t, aud.has("auth.dev_stepup"), "dev step-up audited")
	b := decode[beginResp](t, c.do("POST", "/api/auth/register/begin", map[string]string{"handle": "uma"}))
	w = c.do("POST", "/api/auth/register/finish", map[string]any{"challenge_id": b.ChallengeID, "credential": newSoftAuthenticator(t).create(t, b.Options)})
	testutil.Equal(t, http.StatusOK, w.Code, w.Body.String())
	testutil.True(t, aud.has("auth.credential_added"), "credential added")
}

func TestHandlers_DevStepUpDisabled(t *testing.T) {
	s, _ := newTestService(t, func(c *Config) { c.AllowTestLogin = false })
	c := newTestClient(t, s)
	testutil.Equal(t, http.StatusNotFound, c.do("POST", "/api/auth/dev-stepup", map[string]string{"payload_hash": "00"}).Code)
}

// The route is not mounted without AllowTestLogin, but the handler itself
// must also refuse when called directly.
func TestHandlers_DevStepUpHandlerRefusesWhenDisabled(t *testing.T) {
	s, aud := newTestService(t, func(c *Config) { c.AllowTestLogin = false })
	req := httptest.NewRequest("POST", "/api/auth/dev-stepup", strings.NewReader(`{"payload_hash":"`+strings.Repeat("00", 32)+`"}`))
	w := httptest.NewRecorder()
	s.handleDevStepUp(w, req)
	testutil.Equal(t, http.StatusNotFound, w.Code)
	testutil.False(t, aud.has("auth.dev_stepup"), "nothing audited")
}

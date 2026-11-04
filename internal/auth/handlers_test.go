package auth

import (
	"bytes"
	"context"
	"encoding/hex"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

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
	s, _ := newTestService(t, func(c *Config) { c.Origins = []string{"https://bank.example"}; c.RPID = "bank.example" })
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

	type beginResp struct {
		ChallengeID string          `json:"challenge_id"`
		Options     json.RawMessage `json:"options"`
	}
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

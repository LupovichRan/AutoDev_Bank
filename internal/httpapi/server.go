// Package httpapi wires every service into the bank's single HTTP handler:
// the JSON API under /api, the open-data and auth routes, the transparency
// endpoints, /healthz and the embedded web app.
package httpapi

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"io/fs"
	"log/slog"
	"net/http"
	"regexp"
	"runtime/debug"
	"strings"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/LupovichRan/AutoDev_Bank/internal/approvals"
	"github.com/LupovichRan/AutoDev_Bank/internal/audit"
	"github.com/LupovichRan/AutoDev_Bank/internal/auth"
	"github.com/LupovichRan/AutoDev_Bank/internal/ledger"
	"github.com/LupovichRan/AutoDev_Bank/internal/opendata"
)

// Deps are the services the API is built from.
type Deps struct {
	Pool      *pgxpool.Pool
	Ledger    *ledger.Service
	Audit     *audit.Recorder
	Approvals *approvals.Service
	Auth      *auth.Service
	OpenData  *opendata.Handlers // its RequireStaff is set by New
	Static    fs.FS              // the built web app (index.html at the root); nil serves nothing
	Log       *slog.Logger
}

// RoleStaffOps may trigger an open-data refresh.
const RoleStaffOps = "staff.ops"

// ContentSecurityPolicy is sent on every response.
const ContentSecurityPolicy = "default-src 'self'; img-src 'self' data:; style-src 'self' 'unsafe-inline'; connect-src 'self'"

type api struct {
	Deps
}

// New returns the bank's HTTP handler.
func New(d Deps) http.Handler {
	if d.Log == nil {
		d.Log = slog.Default()
	}
	a := &api{Deps: d}
	mux := http.NewServeMux()

	d.Auth.Mount(mux)
	if d.OpenData != nil {
		d.OpenData.RequireStaff = d.Auth.RequireRole(RoleStaffOps)
		d.OpenData.Mount(mux)
	}

	mux.HandleFunc("GET /healthz", a.healthz)
	mux.Handle("GET /api/accounts", a.session(a.listAccounts))
	mux.Handle("GET /api/accounts/{id}/entries", a.session(a.listEntries))
	mux.Handle("POST /api/transfers", a.session(a.createTransfer))
	mux.Handle("GET /api/approvals", a.session(a.listApprovals))
	mux.Handle("POST /api/approvals/{id}/decide", a.session(a.decideApproval))
	mux.HandleFunc("GET /api/transparency/audit/verify", a.auditVerify)
	mux.HandleFunc("GET /api/transparency/audit/recent", a.auditRecent)
	mux.HandleFunc("GET /api/transparency/audit/root", a.auditRoot)
	mux.HandleFunc("GET /api/transparency/fees", a.fees)
	mux.HandleFunc("/api/", func(w http.ResponseWriter, r *http.Request) {
		Write(w, r, http.StatusNotFound, "", "not_found", nil)
	})
	mux.Handle("/", staticHandler(d.Static))

	// Outermost first: request id, access log, recover, headers, session.
	var h http.Handler = authenticate(d.Auth, mux)
	h = securityHeaders(h)
	h = recoverer(h)
	h = accessLog(h)
	h = withRequestID(d.Log, h)
	return h
}

func (a *api) healthz(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Cache-Control", "no-store")
	ctx, cancel := context.WithTimeout(r.Context(), 2*time.Second)
	defer cancel()
	if err := a.Pool.Ping(ctx); err != nil {
		logger(r.Context()).WarnContext(r.Context(), "healthz: database ping failed", "err", err)
		JSON(w, http.StatusServiceUnavailable, map[string]bool{"ok": false})
		return
	}
	JSON(w, http.StatusOK, map[string]bool{"ok": true})
}

// session rejects requests without a signed-in session (401).
func (a *api) session(h http.HandlerFunc) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if _, ok := auth.FromContext(r.Context()); !ok {
			Write(w, r, http.StatusUnauthorized, "", "sign_in_required", nil)
			return
		}
		h(w, r)
	})
}

// ---------------------------------------------------------------------------
// Middleware

type ctxKey int

const (
	requestIDKey ctxKey = iota
	loggerKey
)

// RequestID returns the request's id ("" outside a request).
func RequestID(ctx context.Context) string {
	id, _ := ctx.Value(requestIDKey).(string)
	return id
}

func logger(ctx context.Context) *slog.Logger {
	if l, ok := ctx.Value(loggerKey).(*slog.Logger); ok {
		return l
	}
	return slog.Default()
}

var requestIDRE = regexp.MustCompile(`^[A-Za-z0-9._-]{1,64}$`)

// withRequestID keeps a well-formed incoming X-Request-Id or generates one,
// echoes it in the response and stores it (and the logger) in the context.
func withRequestID(log *slog.Logger, next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		id := r.Header.Get("X-Request-Id")
		if !requestIDRE.MatchString(id) {
			var b [12]byte
			_, _ = rand.Read(b[:])
			id = hex.EncodeToString(b[:])
		}
		w.Header().Set("X-Request-Id", id)
		ctx := context.WithValue(r.Context(), requestIDKey, id)
		ctx = context.WithValue(ctx, loggerKey, log)
		next.ServeHTTP(w, r.WithContext(ctx))
	})
}

// statusWriter records the status code for the access log and recoverer.
type statusWriter struct {
	http.ResponseWriter
	status int
}

func (s *statusWriter) WriteHeader(code int) {
	if s.status == 0 {
		s.status = code
	}
	s.ResponseWriter.WriteHeader(code)
}

func (s *statusWriter) Write(b []byte) (int, error) {
	if s.status == 0 {
		s.status = http.StatusOK
	}
	return s.ResponseWriter.Write(b)
}

func (s *statusWriter) Unwrap() http.ResponseWriter { return s.ResponseWriter }

func accessLog(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		start := time.Now()
		sw := &statusWriter{ResponseWriter: w}
		defer func() {
			status := sw.status
			if status == 0 {
				status = http.StatusOK
			}
			logger(r.Context()).InfoContext(r.Context(), "http request",
				"method", r.Method, "path", r.URL.Path, "status", status,
				"duration_ms", time.Since(start).Milliseconds(), "request_id", RequestID(r.Context()))
		}()
		next.ServeHTTP(sw, r)
	})
}

// recoverer turns a handler panic into a logged 500 problem response.
func recoverer(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		sw, ok := w.(*statusWriter)
		if !ok {
			sw = &statusWriter{ResponseWriter: w}
		}
		defer func() {
			v := recover()
			if v == nil {
				return
			}
			if v == http.ErrAbortHandler {
				panic(v)
			}
			logger(r.Context()).ErrorContext(r.Context(), "httpapi: panic",
				"path", r.URL.Path, "request_id", RequestID(r.Context()),
				"panic", fmt.Sprint(v), "stack", string(debug.Stack()))
			if sw.status == 0 {
				Write(sw, r, http.StatusInternalServerError, "", "internal_error", nil)
			}
		}()
		next.ServeHTTP(sw, r)
	})
}

// authenticate attaches the session for every route except /api/auth/*:
// those routes authenticate themselves, after their per-IP rate limiter, so
// throttled ceremony requests never reach the session store.
func authenticate(a *auth.Service, next http.Handler) http.Handler {
	withSession := a.Authenticate(next)
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.HasPrefix(r.URL.Path, "/api/auth/") {
			next.ServeHTTP(w, r)
			return
		}
		withSession.ServeHTTP(w, r)
	})
}

func securityHeaders(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		h := w.Header()
		h.Set("X-Content-Type-Options", "nosniff")
		h.Set("Referrer-Policy", "same-origin")
		h.Set("Content-Security-Policy", ContentSecurityPolicy)
		h.Set("Permissions-Policy", "geolocation=(self)")
		h.Set("X-Frame-Options", "DENY")
		if r.URL.Path == "/api" || strings.HasPrefix(r.URL.Path, "/api/") {
			h.Set("Cache-Control", "no-store")
		}
		next.ServeHTTP(w, r)
	})
}

package auth

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"net/http"
	"regexp"
	"strings"
	"unicode/utf8"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/LupovichRan/AutoDev_Bank/internal/db"
)

var handleRE = regexp.MustCompile(`^[a-z0-9][a-z0-9._-]{0,62}$`)

const maxBody = 128 << 10

// Mount registers the auth endpoints (spec §3.7) on mux. Every route is
// wrapped in Authenticate, so it works with or without a global session
// middleware; the ceremony endpoints are rate limited per client IP, and
// the limiter runs first so throttled requests never reach the database.
//
// POST /api/auth/dev-login and POST /api/auth/dev-stepup are mounted only
// when Config.AllowTestLogin is set. dev-login signs anyone in by handle;
// dev-stepup marks the caller's session as stepped up for a payload hash
// without a passkey. Both exist for the Playwright harness and must never
// be enabled in production.
func (s *Service) Mount(mux *http.ServeMux) {
	limited := func(h http.HandlerFunc) http.Handler { return s.rateLimit(s.Authenticate(h)) }
	authed := func(h http.HandlerFunc) http.Handler {
		return s.Authenticate(s.RequireSession(h))
	}
	limitedAuthed := func(h http.HandlerFunc) http.Handler {
		return s.rateLimit(s.Authenticate(s.RequireSession(h)))
	}
	mux.Handle("POST /api/auth/register/begin", limited(s.handleRegisterBegin))
	mux.Handle("POST /api/auth/register/finish", limited(s.handleRegisterFinish))
	mux.Handle("POST /api/auth/login/begin", limited(s.handleLoginBegin))
	mux.Handle("POST /api/auth/login/finish", limited(s.handleLoginFinish))
	mux.Handle("POST /api/auth/stepup/begin", limitedAuthed(s.handleStepUpBegin))
	mux.Handle("POST /api/auth/stepup/finish", limitedAuthed(s.handleStepUpFinish))
	mux.Handle("POST /api/auth/logout", s.Authenticate(http.HandlerFunc(s.handleLogout)))
	mux.Handle("GET /api/me", authed(s.handleMe))
	if s.cfg.AllowTestLogin {
		mux.Handle("POST /api/auth/dev-login", s.Authenticate(http.HandlerFunc(s.handleDevLogin)))
		mux.Handle("POST /api/auth/dev-stepup", authed(s.handleDevStepUp))
	}
}

type beginResponse struct {
	ChallengeID string          `json:"challenge_id"`
	Options     json.RawMessage `json:"options"`
}

type finishRequest struct {
	ChallengeID string          `json:"challenge_id"`
	Credential  json.RawMessage `json:"credential"`
}

func (s *Service) handleRegisterBegin(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Handle      string `json:"handle"`
		DisplayName string `json:"display_name"`
	}
	if !decodeJSON(w, r, &req) {
		return
	}
	handle := strings.ToLower(strings.TrimSpace(req.Handle))
	display := strings.TrimSpace(req.DisplayName)
	if !handleRE.MatchString(handle) {
		problem(w, http.StatusBadRequest, "handle must be 1-63 characters of a-z, 0-9, '.', '_' or '-'")
		return
	}
	if display == "" {
		display = handle
	}
	if utf8.RuneCountInString(display) > 100 {
		problem(w, http.StatusBadRequest, "display_name is too long")
		return
	}
	// A new handle starts a registration; the principal is only created
	// when it finishes. An existing handle is a 409 unless the caller is
	// signed in as that principal, in which case it adds a passkey, which
	// requires a fresh step-up for AddCredentialPayloadHash(handle).
	var (
		pid  uuid.UUID
		opts json.RawMessage
		cid  string
	)
	err := s.pool.QueryRow(r.Context(), `select id from control.principals where handle=$1`, handle).Scan(&pid)
	switch {
	case errors.Is(err, pgx.ErrNoRows):
		opts, cid, err = s.BeginRegistration(r.Context(), handle, display)
	case err != nil:
	default:
		if sess, ok := FromContext(r.Context()); ok && sess.PrincipalID == pid {
			opts, cid, err = s.BeginAddCredential(r.Context(), sess)
		} else {
			err = ErrHandleTaken
		}
	}
	if err != nil {
		s.writeErr(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, beginResponse{ChallengeID: cid, Options: opts})
}

func (s *Service) handleRegisterFinish(w http.ResponseWriter, r *http.Request) {
	var req finishRequest
	if !decodeJSON(w, r, &req) {
		return
	}
	// The browser's previous session, if any, is ended in the same transaction.
	sess, err := s.finishRegistration(r.Context(), req.ChallengeID, req.Credential, r.UserAgent(), currentSession(r.Context(), r))
	if err != nil {
		s.writeErr(w, r, err)
		return
	}
	s.setSessionCookie(w, sess)
	writeJSON(w, http.StatusOK, map[string]string{"principal_id": sess.PrincipalID.String()})
}

func (s *Service) handleLoginBegin(w http.ResponseWriter, r *http.Request) {
	opts, cid, err := s.BeginLogin(r.Context())
	if err != nil {
		s.writeErr(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, beginResponse{ChallengeID: cid, Options: opts})
}

func (s *Service) handleLoginFinish(w http.ResponseWriter, r *http.Request) {
	var req finishRequest
	if !decodeJSON(w, r, &req) {
		return
	}
	// The browser's previous session, if any, is ended in the same transaction.
	sess, err := s.finishLogin(r.Context(), req.ChallengeID, req.Credential, r.UserAgent(), currentSession(r.Context(), r))
	if err != nil {
		s.writeErr(w, r, err)
		return
	}
	s.setSessionCookie(w, sess)
	writeJSON(w, http.StatusOK, map[string]string{"principal_id": sess.PrincipalID.String()})
}

func (s *Service) handleStepUpBegin(w http.ResponseWriter, r *http.Request) {
	sess, _ := FromContext(r.Context())
	var req struct {
		PayloadHash string `json:"payload_hash"`
	}
	if !decodeJSON(w, r, &req) {
		return
	}
	hash, err := hex.DecodeString(req.PayloadHash)
	if err != nil {
		problem(w, http.StatusBadRequest, "payload_hash must be hex")
		return
	}
	opts, cid, err := s.BeginStepUp(r.Context(), sess, hash)
	if err != nil {
		s.writeErr(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, beginResponse{ChallengeID: cid, Options: opts})
}

func (s *Service) handleStepUpFinish(w http.ResponseWriter, r *http.Request) {
	sess, _ := FromContext(r.Context())
	var req finishRequest
	if !decodeJSON(w, r, &req) {
		return
	}
	if err := s.finishStepUp(r.Context(), sess, req.ChallengeID, req.Credential); err != nil {
		s.writeErr(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]bool{"stepped_up": true})
}

func (s *Service) handleLogout(w http.ResponseWriter, r *http.Request) {
	if sess, ok := FromContext(r.Context()); ok {
		if err := s.Logout(r.Context(), sess); err != nil {
			s.writeErr(w, r, err)
			return
		}
	}
	s.clearSessionCookie(w)
	w.WriteHeader(http.StatusNoContent)
}

func (s *Service) handleMe(w http.ResponseWriter, r *http.Request) {
	sess, _ := FromContext(r.Context())
	var resp struct {
		PrincipalID string   `json:"principal_id"`
		DisplayName string   `json:"display_name"`
		Handle      string   `json:"handle"`
		Kind        string   `json:"kind"`
		Roles       []string `json:"roles"`
	}
	err := s.pool.QueryRow(r.Context(), `select p.display_name, p.handle, p.kind,
		coalesce((select array_agg(role order by role) from control.roles where principal_id = p.id), '{}')
		from control.principals p where p.id=$1`, sess.PrincipalID).
		Scan(&resp.DisplayName, &resp.Handle, &resp.Kind, &resp.Roles)
	if errors.Is(err, pgx.ErrNoRows) {
		problem(w, http.StatusUnauthorized, "sign in required")
		return
	}
	if err != nil {
		s.writeErr(w, r, err)
		return
	}
	if resp.Roles == nil {
		resp.Roles = []string{}
	}
	resp.PrincipalID = sess.PrincipalID.String()
	writeJSON(w, http.StatusOK, resp)
}

// handleDevLogin is mounted only when AllowTestLogin is set (tests and the
// Playwright harness). NEVER enable it in production.
func (s *Service) handleDevLogin(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Handle string `json:"handle"`
	}
	if !decodeJSON(w, r, &req) {
		return
	}
	handle := strings.ToLower(strings.TrimSpace(req.Handle))
	if !handleRE.MatchString(handle) {
		problem(w, http.StatusBadRequest, "invalid handle")
		return
	}
	var pid uuid.UUID
	err := db.WithTx(r.Context(), s.pool, func(ctx context.Context, tx pgx.Tx) error {
		var err error
		pid, _, err = ensurePrincipal(ctx, tx, handle, handle)
		return err
	})
	if err != nil {
		s.writeErr(w, r, err)
		return
	}
	sess, err := s.TestLogin(r.Context(), pid)
	if err != nil {
		s.writeErr(w, r, err)
		return
	}
	s.setSessionCookie(w, sess)
	writeJSON(w, http.StatusOK, map[string]string{"principal_id": pid.String()})
}

// handleDevStepUp is mounted only when AllowTestLogin is set. It records a
// step-up for {payload_hash} on the caller's session without a passkey
// assertion, e.g. so a dev-login principal (who has no credential) can add
// its first passkey. NEVER enable it in production.
func (s *Service) handleDevStepUp(w http.ResponseWriter, r *http.Request) {
	sess, _ := FromContext(r.Context())
	var req struct {
		PayloadHash string `json:"payload_hash"`
	}
	if !decodeJSON(w, r, &req) {
		return
	}
	hash, err := hex.DecodeString(req.PayloadHash)
	if err != nil {
		problem(w, http.StatusBadRequest, "payload_hash must be hex")
		return
	}
	if len(hash) != sha256.Size {
		s.writeErr(w, r, ErrBadPayloadHash)
		return
	}
	err = db.WithTx(r.Context(), s.pool, func(ctx context.Context, tx pgx.Tx) error {
		if err := markSteppedUpTx(ctx, tx, sess, hash); err != nil {
			return err
		}
		return s.audit(ctx, tx, "auth.dev_stepup", "principal:"+sess.PrincipalID.String(), &sess.PrincipalID,
			map[string]any{"payload_hash": req.PayloadHash})
	})
	if err != nil {
		s.writeErr(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]bool{"stepped_up": true})
}

// secureCookie is true when ANY configured origin is https (see Config.AllowMixedOrigins).
func (s *Service) secureCookie() bool { return s.secure }

func (s *Service) setSessionCookie(w http.ResponseWriter, sess Session) {
	http.SetCookie(w, &http.Cookie{
		Name:     CookieName,
		Value:    sess.ID,
		Path:     "/",
		Expires:  sess.ExpiresAt,
		MaxAge:   int(s.cfg.SessionTTL.Seconds()),
		HttpOnly: true,
		Secure:   s.secureCookie(),
		SameSite: http.SameSiteStrictMode,
	})
}

func (s *Service) clearSessionCookie(w http.ResponseWriter) {
	http.SetCookie(w, &http.Cookie{
		Name:     CookieName,
		Value:    "",
		Path:     "/",
		MaxAge:   -1,
		HttpOnly: true,
		Secure:   s.secureCookie(),
		SameSite: http.SameSiteStrictMode,
	})
}

// writeErr maps service errors to problem responses without leaking internals.
func (s *Service) writeErr(w http.ResponseWriter, r *http.Request, err error) {
	switch {
	case errors.Is(err, ErrChallengeNotFound):
		problem(w, http.StatusBadRequest, "challenge not found or expired; start again")
	case errors.Is(err, ErrVerification):
		s.log.InfoContext(r.Context(), "auth: verification failed", "err", err)
		problem(w, http.StatusBadRequest, "passkey verification failed")
	case errors.Is(err, ErrBadPayloadHash):
		problem(w, http.StatusBadRequest, "payload_hash must be a hex sha256 digest")
	case errors.Is(err, ErrCredentialExists):
		problem(w, http.StatusConflict, "this passkey is already registered")
	case errors.Is(err, ErrHandleTaken):
		problem(w, http.StatusConflict, "handle already registered; sign in to add a passkey")
	case errors.Is(err, ErrInvalidHandle):
		problem(w, http.StatusBadRequest, "invalid handle")
	case errors.Is(err, ErrStepUpRequired):
		problem(w, http.StatusForbidden, "step-up required: confirm with your passkey first")
	case errors.Is(err, ErrNoSession):
		problem(w, http.StatusUnauthorized, "sign in required")
	case errors.Is(err, ErrPrincipalNotFound):
		problem(w, http.StatusBadRequest, "unknown principal")
	case errors.Is(err, ErrTestLoginDisabled):
		problem(w, http.StatusNotFound, "not found")
	default:
		s.log.ErrorContext(r.Context(), "auth: internal error", "path", r.URL.Path, "err", err)
		problem(w, http.StatusInternalServerError, "internal error")
	}
}

func decodeJSON(w http.ResponseWriter, r *http.Request, v any) bool {
	r.Body = http.MaxBytesReader(w, r.Body, maxBody)
	dec := json.NewDecoder(r.Body)
	if err := dec.Decode(v); err != nil {
		problem(w, http.StatusBadRequest, "invalid JSON body")
		return false
	}
	return true
}

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}

// problem writes an RFC 9457 problem details response. The shared helper
// lives in the httpapi package; this one keeps auth self-contained.
func problem(w http.ResponseWriter, status int, detail string) {
	w.Header().Set("Content-Type", "application/problem+json")
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(map[string]any{
		"type":   "about:blank",
		"title":  http.StatusText(status),
		"status": status,
		"detail": detail,
	})
}

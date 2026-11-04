// Package auth implements passkey (WebAuthn) registration and login, opaque
// server-side sessions, and step-up authentication bound to a payment
// payload hash (dynamic linking).
package auth

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/go-webauthn/webauthn/protocol"
	"github.com/go-webauthn/webauthn/webauthn"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/LupovichRan/AutoDev_Bank/internal/db"
	"github.com/LupovichRan/AutoDev_Bank/internal/ids"
)

// ChallengeTTL bounds the time between a ceremony's /begin and /finish.
const ChallengeTTL = 5 * time.Minute

// maxCredentialBytes caps the size of a credential JSON document we parse.
const maxCredentialBytes = 64 << 10

var (
	// ErrChallengeNotFound means the challenge id is unknown, expired, already
	// used, or belongs to another principal, session or ceremony kind.
	ErrChallengeNotFound = errors.New("auth: challenge not found or expired")
	// ErrVerification means the authenticator response failed WebAuthn verification.
	ErrVerification = errors.New("auth: webauthn verification failed")
	// ErrPrincipalNotFound means no principal matches.
	ErrPrincipalNotFound = errors.New("auth: principal not found")
	// ErrCredentialExists means the authenticator is already registered.
	ErrCredentialExists = errors.New("auth: credential already registered")
	// ErrHandleTaken means a principal with this handle already exists (at
	// register/begin, or because another registration finished first).
	ErrHandleTaken = errors.New("auth: handle already registered")
	// ErrInvalidHandle means the handle is not 1-63 characters of a-z, 0-9, '.', '_' or '-'.
	ErrInvalidHandle = errors.New("auth: invalid handle")
)

// errCloneDetected is internal: a step-up assertion whose signature counter
// did not advance.
var errCloneDetected = errors.New("auth: signature counter regression")

// Auditor appends an audit event inside the caller's transaction. The real
// recorder (internal/audit) is adapted to this interface at wiring time.
type Auditor interface {
	Append(ctx context.Context, tx pgx.Tx, action, subject string, actorID *uuid.UUID, payload map[string]any) error
}

// Config configures the relying party, sessions and test hooks.
type Config struct {
	RPID          string
	RPDisplayName string
	Origins       []string
	// AllowMixedOrigins permits Origins to mix http:// and https://. Without
	// it New refuses such a list. The session cookie is Secure whenever ANY
	// origin is https, so an http origin in a mixed list cannot keep a session.
	AllowMixedOrigins bool
	SessionTTL        time.Duration // default 12h
	StepUpTTL         time.Duration // default 2m
	// AllowTestLogin enables TestLogin and POST /api/auth/dev-login, which
	// sign a principal in without any authenticator. Set only in tests and
	// when BANK_DEV_TEST_LOGIN=1; it must NEVER be enabled in production.
	// New refuses it when any origin is https on a non-localhost host.
	AllowTestLogin bool
	// Auditor receives auth.* events; nil disables auditing (tests only).
	Auditor Auditor
	// RateLimitPerMinute is the per-client-IP budget of the ceremony
	// endpoints (default 10).
	RateLimitPerMinute int
	Logger             *slog.Logger
}

// Service owns the WebAuthn relying party and the session store.
type Service struct {
	pool    *pgxpool.Pool
	cfg     Config
	wa      *webauthn.WebAuthn
	limiter *rateLimiter
	log     *slog.Logger
	secure  bool // Secure cookie: some origin is https
}

// New validates cfg and builds the relying party.
func New(pool *pgxpool.Pool, cfg Config) (*Service, error) {
	if cfg.SessionTTL <= 0 {
		cfg.SessionTTL = 12 * time.Hour
	}
	if cfg.StepUpTTL <= 0 {
		cfg.StepUpTTL = 2 * time.Minute
	}
	if cfg.RateLimitPerMinute <= 0 {
		cfg.RateLimitPerMinute = 10
	}
	if cfg.Logger == nil {
		cfg.Logger = slog.Default()
	}
	secure, err := checkOrigins(cfg)
	if err != nil {
		return nil, err
	}
	wa, err := webauthn.New(&webauthn.Config{
		RPID:          cfg.RPID,
		RPDisplayName: cfg.RPDisplayName,
		RPOrigins:     cfg.Origins,
		AuthenticatorSelection: protocol.AuthenticatorSelection{
			ResidentKey:      protocol.ResidentKeyRequirementRequired,
			UserVerification: protocol.VerificationRequired,
		},
	})
	if err != nil {
		return nil, fmt.Errorf("auth: webauthn config: %w", err)
	}
	return &Service{
		pool:    pool,
		cfg:     cfg,
		wa:      wa,
		limiter: newRateLimiter(cfg.RateLimitPerMinute, time.Minute),
		log:     cfg.Logger,
		secure:  secure,
	}, nil
}

// checkOrigins enforces the origin policy and reports whether the session
// cookie must be Secure.
func checkOrigins(cfg Config) (secure bool, err error) {
	if len(cfg.Origins) == 0 {
		return false, errors.New("auth: at least one origin is required")
	}
	var nHTTPS, nHTTP int
	for _, o := range cfg.Origins {
		u, err := url.Parse(o)
		if err != nil || u.Host == "" {
			return false, fmt.Errorf("auth: invalid origin %q", o)
		}
		switch strings.ToLower(u.Scheme) {
		case "https":
			nHTTPS++
			if cfg.AllowTestLogin && !isLocalHost(u.Hostname()) {
				return false, fmt.Errorf("auth: AllowTestLogin is refused with the public https origin %q", o)
			}
		case "http":
			nHTTP++
		default:
			return false, fmt.Errorf("auth: origin %q must be http or https", o)
		}
	}
	if nHTTPS > 0 && nHTTP > 0 && !cfg.AllowMixedOrigins {
		return false, errors.New("auth: origins mix http and https; set AllowMixedOrigins to permit it")
	}
	return nHTTPS > 0, nil
}

func isLocalHost(host string) bool {
	host = strings.ToLower(strings.TrimSuffix(host, "."))
	if host == "localhost" || strings.HasSuffix(host, ".localhost") {
		return true
	}
	ip := net.ParseIP(host)
	return ip != nil && ip.IsLoopback()
}

// principalUser adapts a control.principals row to webauthn.User.
type principalUser struct {
	id          uuid.UUID
	kind        string
	handle      string
	displayName string
	creds       []webauthn.Credential
}

func (u *principalUser) WebAuthnID() []byte                         { return u.id[:] }
func (u *principalUser) WebAuthnName() string                       { return u.handle }
func (u *principalUser) WebAuthnDisplayName() string                { return u.displayName }
func (u *principalUser) WebAuthnCredentials() []webauthn.Credential { return u.creds }

// loadUser loads a principal with its credentials.
func loadUser(ctx context.Context, q pgx.Tx, id uuid.UUID) (*principalUser, error) {
	u := &principalUser{id: id}
	err := q.QueryRow(ctx, `select kind, handle, display_name from control.principals where id=$1`, id).
		Scan(&u.kind, &u.handle, &u.displayName)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, ErrPrincipalNotFound
	}
	if err != nil {
		return nil, err
	}
	rows, err := q.Query(ctx, `select id, public_key, attestation_type, transport, flags, authenticator
		from control.credentials where principal_id=$1 order by created_at`, id)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	for rows.Next() {
		var (
			c          webauthn.Credential
			transports []string
			flags      storedFlags
			authr      []byte
		)
		if err := rows.Scan(&c.ID, &c.PublicKey, &c.AttestationType, &transports, &flags, &authr); err != nil {
			return nil, err
		}
		for _, t := range transports {
			c.Transport = append(c.Transport, protocol.AuthenticatorTransport(t))
		}
		c.Flags = webauthn.CredentialFlagsFromMsgpByte(flags.Raw)
		if err := json.Unmarshal(authr, &c.Authenticator); err != nil {
			return nil, err
		}
		// The library sets CloneWarning when THIS assertion's counter does
		// not advance; start clear so the flag describes one ceremony.
		c.Authenticator.CloneWarning = false
		u.creds = append(u.creds, c)
	}
	return u, rows.Err()
}

// storedFlags is the jsonb form of webauthn.CredentialFlags. The raw byte
// is authoritative (the library's struct hides it from JSON); the booleans
// are kept for humans and SQL queries.
type storedFlags struct {
	UserPresent    bool `json:"userPresent"`
	UserVerified   bool `json:"userVerified"`
	BackupEligible bool `json:"backupEligible"`
	BackupState    bool `json:"backupState"`
	Raw            byte `json:"raw"`
}

func toStoredFlags(f webauthn.CredentialFlags) storedFlags {
	return storedFlags{f.UserPresent, f.UserVerified, f.BackupEligible, f.BackupState, f.MsgpByte()}
}

func insertCredential(ctx context.Context, tx pgx.Tx, principalID uuid.UUID, c *webauthn.Credential) error {
	transports := make([]string, 0, len(c.Transport))
	for _, t := range c.Transport {
		transports = append(transports, string(t))
	}
	authr, err := json.Marshal(c.Authenticator)
	if err != nil {
		return err
	}
	tag, err := tx.Exec(ctx, `insert into control.credentials
		(id, principal_id, public_key, attestation_type, transport, flags, authenticator)
		values ($1,$2,$3,$4,$5,$6,$7) on conflict (id) do nothing`,
		c.ID, principalID, c.PublicKey, c.AttestationType, transports, toStoredFlags(c.Flags), authr)
	if err != nil {
		return err
	}
	if tag.RowsAffected() != 1 {
		return ErrCredentialExists
	}
	return nil
}

// updateCredentialAfterUse stores the new sign count and flags of a
// credential. On a counter regression the library leaves the stored count
// unchanged and sets cloneWarning, which is stored as "the last assertion
// was suspicious".
func updateCredentialAfterUse(ctx context.Context, tx pgx.Tx, principalID uuid.UUID, c *webauthn.Credential) error {
	authr, err := json.Marshal(c.Authenticator)
	if err != nil {
		return err
	}
	_, err = tx.Exec(ctx, `update control.credentials set authenticator=$3, flags=$4, last_used_at=now()
		where id=$1 and principal_id=$2`, c.ID, principalID, authr, toStoredFlags(c.Flags))
	return err
}

// randomID returns 32 random bytes, base64url without padding.
func randomID() (string, error) {
	b := make([]byte, 32)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	return base64.RawURLEncoding.EncodeToString(b), nil
}

// execer is satisfied by *pgxpool.Pool and pgx.Tx.
type execer interface {
	Exec(ctx context.Context, sql string, args ...any) (pgconn.CommandTag, error)
}

// challengeRow is the ceremony context stored next to the library's SessionData.
type challengeRow struct {
	kind        string
	principalID *uuid.UUID
	sessionID   *string
	handle      *string // new registrations only
	displayName *string // new registrations only
	payloadHash []byte  // step-up only
}

// saveChallenge persists the ceremony state and returns its id.
func saveChallenge(ctx context.Context, q execer, row challengeRow, sd *webauthn.SessionData) (string, error) {
	id, err := randomID()
	if err != nil {
		return "", err
	}
	data, err := json.Marshal(sd)
	if err != nil {
		return "", err
	}
	_, err = q.Exec(ctx, `insert into control.webauthn_challenges
		(id, kind, principal_id, session_id, handle, display_name, session_data, payload_hash, expires_at)
		values ($1,$2,$3,$4,$5,$6,$7,$8, now() + make_interval(secs => $9))`,
		id, row.kind, row.principalID, row.sessionID, row.handle, row.displayName, data, row.payloadHash,
		ChallengeTTL.Seconds())
	if db.PgCode(err) == "23503" { // the session vanished (logout) meanwhile
		return "", ErrNoSession
	}
	if err != nil {
		return "", fmt.Errorf("auth: save challenge: %w", err)
	}
	return id, nil
}

type challenge struct {
	challengeRow
	data webauthn.SessionData
}

// consumeChallenge deletes and returns an unexpired challenge of the given
// kind. It commits on its own so a challenge is spent even when the
// verification that follows fails: one /begin allows one /finish attempt.
// When principalID (sessionID) is non-nil the challenge must belong to that
// principal (session).
func (s *Service) consumeChallenge(ctx context.Context, id, kind string, principalID *uuid.UUID, sessionID *string) (*challenge, error) {
	var (
		c    challenge
		data []byte
	)
	err := s.pool.QueryRow(ctx, `delete from control.webauthn_challenges
		where id=$1 and kind=$2 and expires_at > now()
		  and ($3::uuid is null or principal_id = $3)
		  and ($4::text is null or session_id = $4)
		returning principal_id, session_id, handle, display_name, session_data, payload_hash`,
		id, kind, principalID, sessionID).
		Scan(&c.principalID, &c.sessionID, &c.handle, &c.displayName, &data, &c.payloadHash)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, ErrChallengeNotFound
	}
	if err != nil {
		return nil, err
	}
	c.kind = kind
	if err := json.Unmarshal(data, &c.data); err != nil {
		return nil, err
	}
	// Opportunistic cleanup of abandoned ceremonies.
	_, _ = s.pool.Exec(ctx, `delete from control.webauthn_challenges where expires_at < now() - interval '1 hour'`)
	return &c, nil
}

func (s *Service) audit(ctx context.Context, tx pgx.Tx, action, subject string, actor *uuid.UUID, payload map[string]any) error {
	if s.cfg.Auditor == nil {
		return nil
	}
	return s.cfg.Auditor.Append(ctx, tx, action, subject, actor, payload)
}

// verificationError wraps a library error so callers can test for ErrVerification.
func verificationError(err error) error {
	var perr *protocol.Error
	if errors.As(err, &perr) {
		return fmt.Errorf("%w: %s (%s)", ErrVerification, perr.Details, perr.DevInfo)
	}
	return fmt.Errorf("%w: %v", ErrVerification, err)
}

func parseAssertion(credential []byte) (*protocol.ParsedCredentialAssertionData, error) {
	parsed, err := protocol.ParseCredentialRequestResponseBody(bytes.NewReader(credential))
	if err != nil {
		return nil, verificationError(err)
	}
	return parsed, nil
}

// readCredential reads the PublicKeyCredential JSON from r's body.
func readCredential(r *http.Request) ([]byte, error) {
	if r == nil || r.Body == nil {
		return nil, fmt.Errorf("%w: no credential", ErrVerification)
	}
	defer r.Body.Close()
	return io.ReadAll(io.LimitReader(r.Body, maxCredentialBytes))
}

// currentSession is the browser's existing session (from ctx, else from
// r's context), which a successful login or registration replaces.
func currentSession(ctx context.Context, r *http.Request) *Session {
	if sess, ok := FromContext(ctx); ok {
		return &sess
	}
	if r != nil {
		if sess, ok := FromContext(r.Context()); ok {
			return &sess
		}
	}
	return nil
}

// cloneWarningPayload describes a signature counter regression for the audit log.
func cloneWarningPayload(ceremony string, cred *webauthn.Credential, parsed *protocol.ParsedCredentialAssertionData) map[string]any {
	return map[string]any{
		"ceremony":          ceremony,
		"credential_id":     base64.RawURLEncoding.EncodeToString(cred.ID),
		"stored_sign_count": cred.Authenticator.SignCount,
		"sign_count":        parsed.Response.AuthenticatorData.Counter,
	}
}

// BeginRegistration starts a passkey registration for a NEW principal with
// handle. Nothing is created yet: the handle, display name and the new
// principal id (the WebAuthn user handle) travel with the challenge, and
// FinishRegistration creates the principal. An existing handle is
// ErrHandleTaken; adding a passkey to an existing principal is
// BeginAddCredential.
func (s *Service) BeginRegistration(ctx context.Context, handle, displayName string) (json.RawMessage, string, error) {
	if !handleRE.MatchString(handle) {
		return nil, "", ErrInvalidHandle
	}
	if displayName == "" {
		displayName = handle
	}
	var taken bool
	if err := s.pool.QueryRow(ctx, `select exists(select 1 from control.principals where handle=$1)`, handle).
		Scan(&taken); err != nil {
		return nil, "", err
	}
	if taken {
		return nil, "", ErrHandleTaken
	}
	user := &principalUser{id: ids.New(), kind: "customer", handle: handle, displayName: displayName}
	creation, sd, err := s.wa.BeginRegistration(user)
	if err != nil {
		return nil, "", fmt.Errorf("auth: begin registration: %w", err)
	}
	id, err := saveChallenge(ctx, s.pool, challengeRow{kind: "register", handle: &handle, displayName: &displayName}, sd)
	if err != nil {
		return nil, "", err
	}
	opts, err := json.Marshal(creation)
	return opts, id, err
}

// AddCredentialPayloadHash is the step-up payload hash that authorises
// adding a passkey to the principal with handle: sha256("add-credential:" + handle).
func AddCredentialPayloadHash(handle string) []byte {
	return payloadHash("add-credential:" + handle)
}

// BeginAddCredential starts registering another passkey for sess's
// principal. It requires (and consumes) a fresh step-up for
// AddCredentialPayloadHash(handle), so a stolen session cookie alone cannot
// enrol an attacker's authenticator. The challenge is bound to sess.
func (s *Service) BeginAddCredential(ctx context.Context, sess Session) (json.RawMessage, string, error) {
	var (
		opts json.RawMessage
		id   string
	)
	err := db.WithTx(ctx, s.pool, func(ctx context.Context, tx pgx.Tx) error {
		user, err := loadUser(ctx, tx, sess.PrincipalID)
		if err != nil {
			return err
		}
		if err := s.RequireStepUpTx(ctx, tx, sess, AddCredentialPayloadHash(user.handle)); err != nil {
			return err
		}
		creation, sd, err := s.wa.BeginRegistration(user,
			webauthn.WithExclusions(webauthn.Credentials(user.creds).CredentialDescriptors()))
		if err != nil {
			return fmt.Errorf("auth: begin registration: %w", err)
		}
		id, err = saveChallenge(ctx, tx, challengeRow{kind: "register", principalID: &user.id, sessionID: &sess.ID}, sd)
		if err != nil {
			return err
		}
		opts, err = json.Marshal(creation)
		return err
	})
	if err != nil {
		return nil, "", err
	}
	return opts, id, nil
}

// FinishRegistration verifies the attestation in r's body (the browser's
// PublicKeyCredential.toJSON()), stores the credential and signs the
// principal in: registering is also a login. For a new registration the
// principal is created here, in the same transaction; if its handle was
// taken meanwhile the result is ErrHandleTaken. For an added passkey the
// caller must carry (in ctx or r's context) the session that began it. The
// browser's previous session, if any, is ended.
func (s *Service) FinishRegistration(ctx context.Context, challengeID string, r *http.Request) (Session, error) {
	body, err := readCredential(r)
	if err != nil {
		return Session{}, err
	}
	return s.finishRegistration(ctx, challengeID, body, r.UserAgent(), currentSession(ctx, r))
}

func (s *Service) finishRegistration(ctx context.Context, challengeID string, credential []byte, userAgent string, current *Session) (Session, error) {
	ch, err := s.consumeChallenge(ctx, challengeID, "register", nil, nil)
	if err != nil {
		return Session{}, err
	}
	adding := ch.principalID != nil
	if adding && (current == nil || ch.sessionID == nil || current.ID != *ch.sessionID || current.PrincipalID != *ch.principalID) {
		return Session{}, ErrChallengeNotFound
	}
	parsed, err := protocol.ParseCredentialCreationResponseBody(bytes.NewReader(credential))
	if err != nil {
		return Session{}, verificationError(err)
	}
	var sess Session
	err = db.WithTx(ctx, s.pool, func(ctx context.Context, tx pgx.Tx) error {
		var user *principalUser
		if adding {
			if user, err = loadUser(ctx, tx, *ch.principalID); err != nil {
				return err
			}
		} else {
			id, err := uuid.FromBytes(ch.data.UserID)
			if err != nil || ch.handle == nil || ch.displayName == nil {
				return fmt.Errorf("auth: corrupt registration challenge")
			}
			user = &principalUser{id: id, kind: "customer", handle: *ch.handle, displayName: *ch.displayName}
		}
		cred, err := s.wa.CreateCredential(user, ch.data, parsed)
		if err != nil {
			return verificationError(err)
		}
		if !adding {
			tag, err := tx.Exec(ctx, `insert into control.principals (id, kind, display_name, handle)
				values ($1, 'customer', $2, $3) on conflict do nothing`, user.id, user.displayName, user.handle)
			if err != nil {
				return err
			}
			if tag.RowsAffected() != 1 {
				return ErrHandleTaken
			}
		}
		if err := insertCredential(ctx, tx, user.id, cred); err != nil {
			return err
		}
		if sess, err = s.createSession(ctx, tx, user.id, userAgent); err != nil {
			return err
		}
		if current != nil {
			ended, err := s.endSessionTx(ctx, tx, *current, "replaced")
			if err != nil {
				return err
			}
			if adding && !ended {
				return ErrNoSession // the acting session was logged out meanwhile
			}
		}
		action, payload := "auth.register", map[string]any{
			"credential_id": base64.RawURLEncoding.EncodeToString(cred.ID),
			"transports":    cred.Transport,
			"user_agent":    userAgent,
		}
		if adding {
			action = "auth.credential_added"
			payload["session_id"] = current.ID // ended above, so no longer a bearer secret
		}
		return s.audit(ctx, tx, action, "principal:"+user.id.String(), &user.id, payload)
	})
	if err != nil {
		return Session{}, err
	}
	return sess, nil
}

// BeginLogin starts a discoverable (usernameless) passkey login.
func (s *Service) BeginLogin(ctx context.Context) (json.RawMessage, string, error) {
	assertion, sd, err := s.wa.BeginDiscoverableLogin()
	if err != nil {
		return nil, "", fmt.Errorf("auth: begin login: %w", err)
	}
	id, err := saveChallenge(ctx, s.pool, challengeRow{kind: "login"}, sd)
	if err != nil {
		return nil, "", err
	}
	opts, err := json.Marshal(assertion)
	return opts, id, err
}

// FinishLogin verifies the assertion in r's body and creates a session,
// ending the browser's previous session (from ctx or r's context) if any.
// A signature counter regression is allowed here but audited as
// auth.clone_warning; step-up refuses it.
func (s *Service) FinishLogin(ctx context.Context, challengeID string, r *http.Request) (Session, error) {
	body, err := readCredential(r)
	if err != nil {
		return Session{}, err
	}
	return s.finishLogin(ctx, challengeID, body, r.UserAgent(), currentSession(ctx, r))
}

func (s *Service) finishLogin(ctx context.Context, challengeID string, credential []byte, userAgent string, current *Session) (Session, error) {
	ch, err := s.consumeChallenge(ctx, challengeID, "login", nil, nil)
	if err != nil {
		return Session{}, err
	}
	parsed, err := parseAssertion(credential)
	if err != nil {
		return Session{}, err
	}
	var sess Session
	err = db.WithTx(ctx, s.pool, func(ctx context.Context, tx pgx.Tx) error {
		var user *principalUser
		handler := func(_, userHandle []byte) (webauthn.User, error) {
			id, err := uuid.FromBytes(userHandle)
			if err != nil {
				return nil, ErrPrincipalNotFound
			}
			user, err = loadUser(ctx, tx, id)
			return user, err
		}
		cred, err := s.wa.ValidateDiscoverableLogin(handler, ch.data, parsed)
		if err != nil {
			// A database failure inside the handler surfaces wrapped in a
			// protocol.Error; keep it retryable rather than a 400.
			if db.IsRetryable(err) || errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
				return err
			}
			return verificationError(err)
		}
		if err := updateCredentialAfterUse(ctx, tx, user.id, cred); err != nil {
			return err
		}
		if sess, err = s.createSession(ctx, tx, user.id, userAgent); err != nil {
			return err
		}
		if current != nil {
			if _, err := s.endSessionTx(ctx, tx, *current, "replaced"); err != nil {
				return err
			}
		}
		subject := "principal:" + user.id.String()
		if cred.Authenticator.CloneWarning {
			if err := s.audit(ctx, tx, "auth.clone_warning", subject, &user.id, cloneWarningPayload("login", cred, parsed)); err != nil {
				return err
			}
		}
		return s.audit(ctx, tx, "auth.login", subject, &user.id, map[string]any{
			"credential_id": base64.RawURLEncoding.EncodeToString(cred.ID),
			"clone_warning": cred.Authenticator.CloneWarning,
			"user_agent":    userAgent,
		})
	})
	if err != nil {
		return Session{}, err
	}
	return sess, nil
}

// clientIP is the transport-level peer address (proxy headers are not
// trusted; the deployment's reverse proxy is expected to set RemoteAddr).
func clientIP(r *http.Request) string {
	addr := r.RemoteAddr
	if i := strings.LastIndexByte(addr, ':'); i > 0 {
		addr = addr[:i]
	}
	return strings.Trim(addr, "[]")
}

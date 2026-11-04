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
	"net/http"
	"strings"
	"time"

	"github.com/go-webauthn/webauthn/protocol"
	"github.com/go-webauthn/webauthn/webauthn"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/LupovichRan/AutoDev_Bank/internal/db"
)

// ChallengeTTL bounds the time between a ceremony's /begin and /finish.
const ChallengeTTL = 5 * time.Minute

// maxCredentialBytes caps the size of a credential JSON document we parse.
const maxCredentialBytes = 64 << 10

var (
	// ErrChallengeNotFound means the challenge id is unknown, expired, already
	// used, or belongs to another principal or ceremony kind.
	ErrChallengeNotFound = errors.New("auth: challenge not found or expired")
	// ErrVerification means the authenticator response failed WebAuthn verification.
	ErrVerification = errors.New("auth: webauthn verification failed")
	// ErrPrincipalNotFound means no principal matches.
	ErrPrincipalNotFound = errors.New("auth: principal not found")
	// ErrCredentialExists means the authenticator is already registered.
	ErrCredentialExists = errors.New("auth: credential already registered")
)

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
	SessionTTL    time.Duration // default 12h
	StepUpTTL     time.Duration // default 2m
	// AllowTestLogin enables TestLogin and POST /api/auth/dev-login, which
	// sign a principal in without any authenticator. Set only in tests and
	// when BANK_DEV_TEST_LOGIN=1; it must NEVER be enabled in production.
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
	if len(cfg.Origins) == 0 {
		return nil, errors.New("auth: at least one origin is required")
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
	}, nil
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

// updateCredentialAfterUse stores the new sign count and flags of a credential.
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

// saveChallenge persists the ceremony state and returns its id.
func (s *Service) saveChallenge(ctx context.Context, kind string, principalID *uuid.UUID, sd *webauthn.SessionData, payloadHash []byte) (string, error) {
	id, err := randomID()
	if err != nil {
		return "", err
	}
	data, err := json.Marshal(sd)
	if err != nil {
		return "", err
	}
	_, err = s.pool.Exec(ctx, `insert into control.webauthn_challenges
		(id, kind, principal_id, session_data, payload_hash, expires_at)
		values ($1,$2,$3,$4,$5, now() + make_interval(secs => $6))`,
		id, kind, principalID, data, payloadHash, ChallengeTTL.Seconds())
	if err != nil {
		return "", fmt.Errorf("auth: save challenge: %w", err)
	}
	return id, nil
}

type challenge struct {
	principalID *uuid.UUID
	data        webauthn.SessionData
	payloadHash []byte
}

// consumeChallenge deletes and returns an unexpired challenge of the given
// kind. It commits on its own so a challenge is spent even when the
// verification that follows fails: one /begin allows one /finish attempt.
// When principalID is non-nil the challenge must belong to that principal.
func (s *Service) consumeChallenge(ctx context.Context, id, kind string, principalID *uuid.UUID) (*challenge, error) {
	var (
		c    challenge
		data []byte
	)
	err := s.pool.QueryRow(ctx, `delete from control.webauthn_challenges
		where id=$1 and kind=$2 and expires_at > now() and ($3::uuid is null or principal_id = $3)
		returning principal_id, session_data, payload_hash`, id, kind, principalID).
		Scan(&c.principalID, &data, &c.payloadHash)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, ErrChallengeNotFound
	}
	if err != nil {
		return nil, err
	}
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

// BeginRegistration starts a passkey registration for an existing principal
// and returns the CredentialCreation options for navigator.credentials.create().
func (s *Service) BeginRegistration(ctx context.Context, principalID uuid.UUID, displayName, handle string) (json.RawMessage, string, error) {
	var user *principalUser
	err := pgx.BeginTxFunc(ctx, s.pool, pgx.TxOptions{AccessMode: pgx.ReadOnly}, func(tx pgx.Tx) error {
		var err error
		user, err = loadUser(ctx, tx, principalID)
		return err
	})
	if err != nil {
		return nil, "", err
	}
	if displayName != "" {
		user.displayName = displayName
	}
	if handle != "" {
		user.handle = handle
	}
	creation, sd, err := s.wa.BeginRegistration(user,
		webauthn.WithExclusions(webauthn.Credentials(user.creds).CredentialDescriptors()))
	if err != nil {
		return nil, "", fmt.Errorf("auth: begin registration: %w", err)
	}
	id, err := s.saveChallenge(ctx, "register", &principalID, sd, nil)
	if err != nil {
		return nil, "", err
	}
	opts, err := json.Marshal(creation)
	return opts, id, err
}

// FinishRegistration verifies the attestation in r's body (the browser's
// PublicKeyCredential.toJSON()), stores the credential and signs the
// principal in: registering is also a login.
func (s *Service) FinishRegistration(ctx context.Context, challengeID string, r *http.Request) (Session, error) {
	body, err := readCredential(r)
	if err != nil {
		return Session{}, err
	}
	return s.finishRegistration(ctx, challengeID, body, r.UserAgent())
}

func (s *Service) finishRegistration(ctx context.Context, challengeID string, credential []byte, userAgent string) (Session, error) {
	ch, err := s.consumeChallenge(ctx, challengeID, "register", nil)
	if err != nil {
		return Session{}, err
	}
	parsed, err := protocol.ParseCredentialCreationResponseBody(bytes.NewReader(credential))
	if err != nil {
		return Session{}, verificationError(err)
	}
	var sess Session
	err = db.WithTx(ctx, s.pool, func(ctx context.Context, tx pgx.Tx) error {
		user, err := loadUser(ctx, tx, *ch.principalID)
		if err != nil {
			return err
		}
		cred, err := s.wa.CreateCredential(user, ch.data, parsed)
		if err != nil {
			return verificationError(err)
		}
		if err := insertCredential(ctx, tx, user.id, cred); err != nil {
			return err
		}
		sess, err = s.createSession(ctx, tx, user.id, userAgent)
		if err != nil {
			return err
		}
		return s.audit(ctx, tx, "auth.register", "principal:"+user.id.String(), &user.id, map[string]any{
			"credential_id": base64.RawURLEncoding.EncodeToString(cred.ID),
			"transports":    cred.Transport,
			"user_agent":    userAgent,
		})
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
	id, err := s.saveChallenge(ctx, "login", nil, sd, nil)
	if err != nil {
		return nil, "", err
	}
	opts, err := json.Marshal(assertion)
	return opts, id, err
}

// FinishLogin verifies the assertion in r's body and creates a session.
func (s *Service) FinishLogin(ctx context.Context, challengeID string, r *http.Request) (Session, error) {
	body, err := readCredential(r)
	if err != nil {
		return Session{}, err
	}
	return s.finishLogin(ctx, challengeID, body, r.UserAgent())
}

func (s *Service) finishLogin(ctx context.Context, challengeID string, credential []byte, userAgent string) (Session, error) {
	ch, err := s.consumeChallenge(ctx, challengeID, "login", nil)
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
		sess, err = s.createSession(ctx, tx, user.id, userAgent)
		if err != nil {
			return err
		}
		return s.audit(ctx, tx, "auth.login", "principal:"+user.id.String(), &user.id, map[string]any{
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

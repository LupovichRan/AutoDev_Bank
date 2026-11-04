package auth

import (
	"context"
	"errors"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/LupovichRan/AutoDev_Bank/internal/db"
	"github.com/LupovichRan/AutoDev_Bank/internal/ids"
)

var (
	// ErrNoSession means the session id is unknown or expired.
	ErrNoSession = errors.New("auth: no valid session")
	// ErrTestLoginDisabled is returned by TestLogin unless Config.AllowTestLogin is set.
	ErrTestLoginDisabled = errors.New("auth: test login is disabled")
)

// Session is a signed-in browser session. ID is the opaque 256-bit value
// carried in the bank_session cookie.
type Session struct {
	ID          string
	PrincipalID uuid.UUID
	CreatedAt   time.Time
	ExpiresAt   time.Time
	UserAgent   string
}

const maxUserAgent = 512

// createSession inserts a new session row inside tx.
func (s *Service) createSession(ctx context.Context, tx pgx.Tx, principalID uuid.UUID, userAgent string) (Session, error) {
	id, err := randomID()
	if err != nil {
		return Session{}, err
	}
	if len(userAgent) > maxUserAgent {
		userAgent = userAgent[:maxUserAgent]
	}
	sess := Session{ID: id, PrincipalID: principalID, UserAgent: userAgent}
	err = tx.QueryRow(ctx, `insert into control.sessions (id, principal_id, expires_at, user_agent)
		values ($1, $2, now() + make_interval(secs => $3), $4)
		returning created_at, expires_at`, id, principalID, s.cfg.SessionTTL.Seconds(), userAgent).
		Scan(&sess.CreatedAt, &sess.ExpiresAt)
	return sess, err
}

// LoadSession returns the unexpired session with the given id.
func (s *Service) LoadSession(ctx context.Context, id string) (Session, error) {
	if id == "" {
		return Session{}, ErrNoSession
	}
	sess := Session{ID: id}
	err := s.pool.QueryRow(ctx, `select principal_id, created_at, expires_at, user_agent
		from control.sessions where id=$1 and expires_at > now()`, id).
		Scan(&sess.PrincipalID, &sess.CreatedAt, &sess.ExpiresAt, &sess.UserAgent)
	if errors.Is(err, pgx.ErrNoRows) {
		return Session{}, ErrNoSession
	}
	if err != nil {
		return Session{}, err
	}
	return sess, nil
}

// Logout deletes the session (and opportunistically purges expired ones).
func (s *Service) Logout(ctx context.Context, sess Session) error {
	return db.WithTx(ctx, s.pool, func(ctx context.Context, tx pgx.Tx) error {
		tag, err := tx.Exec(ctx, `delete from control.sessions where id=$1`, sess.ID)
		if err != nil {
			return err
		}
		if tag.RowsAffected() == 0 {
			return nil
		}
		if _, err := tx.Exec(ctx, `delete from control.sessions where expires_at < now() - interval '1 day'`); err != nil {
			return err
		}
		return s.audit(ctx, tx, "auth.logout", "principal:"+sess.PrincipalID.String(), &sess.PrincipalID, nil)
	})
}

// TestLogin signs principalID in without an authenticator. It works only
// when Config.AllowTestLogin is set (tests, and BANK_DEV_TEST_LOGIN=1 for
// the Playwright harness) and must never be enabled in production.
func (s *Service) TestLogin(ctx context.Context, principalID uuid.UUID) (Session, error) {
	if !s.cfg.AllowTestLogin {
		return Session{}, ErrTestLoginDisabled
	}
	var sess Session
	err := db.WithTx(ctx, s.pool, func(ctx context.Context, tx pgx.Tx) error {
		var err error
		sess, err = s.createSession(ctx, tx, principalID, "test-login")
		if err != nil {
			return err
		}
		return s.audit(ctx, tx, "auth.dev_login", "principal:"+principalID.String(), &principalID, nil)
	})
	return sess, err
}

// ensurePrincipal returns the principal with handle, creating a customer
// principal when the handle is new. created reports which happened.
func ensurePrincipal(ctx context.Context, tx pgx.Tx, handle, displayName string) (id uuid.UUID, created bool, err error) {
	err = tx.QueryRow(ctx, `select id from control.principals where handle=$1`, handle).Scan(&id)
	if err == nil {
		return id, false, nil
	}
	if !errors.Is(err, pgx.ErrNoRows) {
		return uuid.Nil, false, err
	}
	err = tx.QueryRow(ctx, `insert into control.principals (id, kind, display_name, handle)
		values ($1, 'customer', $2, $3) on conflict (handle) do nothing returning id`,
		ids.New(), displayName, handle).Scan(&id)
	if errors.Is(err, pgx.ErrNoRows) { // lost a race with a concurrent insert
		err = tx.QueryRow(ctx, `select id from control.principals where handle=$1`, handle).Scan(&id)
		return id, false, err
	}
	return id, err == nil, err
}

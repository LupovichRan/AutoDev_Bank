package auth

import (
	"context"
	"testing"
	"time"

	"github.com/LupovichRan/AutoDev_Bank/internal/testutil"
)

func TestSessions_CreateLoadExpire(t *testing.T) {
	s, _ := newTestService(t)
	ctx := context.Background()
	pid := mustPrincipal(t, s, "erin")

	sess, err := s.TestLogin(ctx, pid)
	testutil.NoError(t, err)
	testutil.Equal(t, pid, sess.PrincipalID)
	raw, err := b64.DecodeString(sess.ID)
	testutil.NoError(t, err)
	testutil.Len(t, raw, 32, "256-bit opaque session id")
	ttl := sess.ExpiresAt.Sub(sess.CreatedAt)
	testutil.True(t, ttl > 12*time.Hour-time.Minute && ttl <= 12*time.Hour+time.Minute, "default 12h ttl")

	loaded, err := s.LoadSession(ctx, sess.ID)
	testutil.NoError(t, err)
	testutil.Equal(t, sess.ID, loaded.ID)
	testutil.Equal(t, pid, loaded.PrincipalID)

	_, err = s.LoadSession(ctx, "does-not-exist")
	testutil.ErrorIs(t, err, ErrNoSession)

	_, err = s.pool.Exec(ctx, `update control.sessions set expires_at = now() - interval '1 second' where id=$1`, sess.ID)
	testutil.NoError(t, err)
	_, err = s.LoadSession(ctx, sess.ID)
	testutil.ErrorIs(t, err, ErrNoSession, "expired session")

	other, err := s.TestLogin(ctx, pid)
	testutil.NoError(t, err)
	testutil.True(t, other.ID != sess.ID, "fresh id per session")
	testutil.NoError(t, s.Logout(ctx, other))
	_, err = s.LoadSession(ctx, other.ID)
	testutil.ErrorIs(t, err, ErrNoSession, "deleted on logout")
}

func TestSessions_CustomTTL(t *testing.T) {
	s, _ := newTestService(t, func(c *Config) { c.SessionTTL = time.Hour })
	sess, err := s.TestLogin(context.Background(), mustPrincipal(t, s, "frank"))
	testutil.NoError(t, err)
	testutil.True(t, sess.ExpiresAt.Sub(sess.CreatedAt) <= time.Hour+time.Second)
}

func TestTestLogin_DisabledByDefault(t *testing.T) {
	s, _ := newTestService(t, func(c *Config) { c.AllowTestLogin = false })
	_, err := s.TestLogin(context.Background(), mustPrincipal(t, s, "grace"))
	testutil.ErrorIs(t, err, ErrTestLoginDisabled)
}

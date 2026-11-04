package auth

import (
	"context"
	"crypto/sha256"
	"testing"

	"github.com/LupovichRan/AutoDev_Bank/internal/testutil"
)

// M6: adding a passkey ends every other session of the principal; the
// session it signs in stays.
func TestAddCredential_RevokesOtherSessions(t *testing.T) {
	s, aud := newTestService(t)
	ctx := context.Background()
	sess, authr := register(t, s, "rhea")
	other, err := login(t, s, authr) // e.g. another browser
	testutil.NoError(t, err)

	testutil.NoError(t, stepUp(t, s, sess, authr, AddCredentialPayloadHash("rhea")))
	options, cid, err := s.BeginAddCredential(ctx, sess)
	testutil.NoError(t, err)
	r := credentialRequest(newSoftAuthenticator(t).create(t, options))
	r = r.WithContext(WithSession(r.Context(), sess))
	fresh, err := s.FinishRegistration(ctx, cid, r)
	testutil.NoError(t, err)

	_, err = s.LoadSession(ctx, other.ID)
	testutil.ErrorIs(t, err, ErrNoSession, "other session revoked")
	_, err = s.LoadSession(ctx, fresh.ID)
	testutil.NoError(t, err, "the new session stays")
	testutil.True(t, aud.has("auth.sessions_revoked"), "revocation audited")
}

// M6: a clone warning at step-up ends the principal's other sessions but
// keeps the one that saw it.
func TestCloneWarning_RevokesOtherSessions(t *testing.T) {
	s, _ := newTestService(t)
	ctx := context.Background()
	sess, authr := register(t, s, "sven")
	other, err := login(t, s, authr) // stored sign count is now 1
	testutil.NoError(t, err)

	authr.counter = 0 // the next assertion replays counter 1
	hash := sha256.Sum256([]byte("transfer"))
	testutil.ErrorIs(t, stepUp(t, s, sess, authr, hash[:]), ErrVerification)

	_, err = s.LoadSession(ctx, other.ID)
	testutil.ErrorIs(t, err, ErrNoSession, "other session revoked")
	_, err = s.LoadSession(ctx, sess.ID)
	testutil.NoError(t, err, "current session kept")
}

// M6: a clone warning at login ends the other sessions but keeps the new one.
func TestCloneWarningAtLogin_RevokesOtherSessions(t *testing.T) {
	s, _ := newTestService(t)
	ctx := context.Background()
	sess, authr := register(t, s, "tess")
	_, err := login(t, s, authr)
	testutil.NoError(t, err)

	authr.counter = 0
	fresh, err := login(t, s, authr)
	testutil.NoError(t, err)
	_, err = s.LoadSession(ctx, sess.ID)
	testutil.ErrorIs(t, err, ErrNoSession, "older session revoked")
	_, err = s.LoadSession(ctx, fresh.ID)
	testutil.NoError(t, err, "the new session stays")
}

package auth

import (
	"context"
	"crypto/sha256"
	"testing"

	"github.com/LupovichRan/AutoDev_Bank/internal/testutil"
)

func TestStepUp_BoundToPayloadHash(t *testing.T) {
	s, _ := newTestService(t)
	ctx := context.Background()
	sess, err := s.TestLogin(ctx, mustPrincipal(t, s, "heidi"))
	testutil.NoError(t, err)
	hashA := sha256.Sum256([]byte("transfer A"))
	hashB := sha256.Sum256([]byte("transfer B"))

	testutil.NoError(t, s.markSteppedUp(ctx, sess, hashA[:]))
	testutil.ErrorIs(t, s.RequireStepUp(ctx, sess, hashB[:]), ErrStepUpRequired, "different payload")
	testutil.NoError(t, s.RequireStepUp(ctx, sess, hashA[:]), "matching payload")
	testutil.ErrorIs(t, s.RequireStepUp(ctx, sess, hashA[:]), ErrStepUpRequired, "consumed: one assertion = one transfer")
}

func TestStepUp_ExpiresAfterTTL(t *testing.T) {
	s, _ := newTestService(t)
	ctx := context.Background()
	sess, err := s.TestLogin(ctx, mustPrincipal(t, s, "ivan"))
	testutil.NoError(t, err)
	hash := sha256.Sum256([]byte("transfer"))
	testutil.NoError(t, s.markSteppedUp(ctx, sess, hash[:]))
	_, err = s.pool.Exec(ctx, `update control.sessions set stepped_up_at = now() - interval '121 seconds' where id=$1`, sess.ID)
	testutil.NoError(t, err)
	testutil.ErrorIs(t, s.RequireStepUp(ctx, sess, hash[:]), ErrStepUpRequired, "older than the 2 minute default")
}

func TestStepUp_RejectsBadHashLength(t *testing.T) {
	s, _ := newTestService(t)
	ctx := context.Background()
	sess, err := s.TestLogin(ctx, mustPrincipal(t, s, "judy"))
	testutil.NoError(t, err)
	_, _, err = s.BeginStepUp(ctx, sess, []byte("short"))
	testutil.ErrorIs(t, err, ErrBadPayloadHash)
	testutil.ErrorIs(t, s.RequireStepUp(ctx, sess, nil), ErrStepUpRequired)
}

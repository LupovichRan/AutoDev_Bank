package auth

import (
	"context"
	"crypto/sha256"
	"errors"
	"sync"
	"sync/atomic"
	"testing"

	"github.com/jackc/pgx/v5"

	"github.com/LupovichRan/AutoDev_Bank/internal/db"
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

func TestTestMarkSteppedUp_OnlyWithTestLogin(t *testing.T) {
	ctx := context.Background()
	hash := sha256.Sum256([]byte("transfer"))

	s, _ := newTestService(t)
	sess, err := s.TestLogin(ctx, mustPrincipal(t, s, "trent"))
	testutil.NoError(t, err)
	testutil.ErrorIs(t, s.TestMarkSteppedUp(ctx, sess, hash[:4]), ErrBadPayloadHash)
	testutil.NoError(t, s.TestMarkSteppedUp(ctx, sess, hash[:]))
	testutil.NoError(t, s.RequireStepUp(ctx, sess, hash[:]))

	off, _ := newTestService(t, func(c *Config) { c.AllowTestLogin = false })
	testutil.ErrorIs(t, off.TestMarkSteppedUp(ctx, sess, hash[:]), ErrTestLoginDisabled)
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

func TestStepUp_ConcurrentRequireExactlyOneWins(t *testing.T) {
	s, _ := newTestService(t)
	ctx := context.Background()
	sess, err := s.TestLogin(ctx, mustPrincipal(t, s, "kate"))
	testutil.NoError(t, err)
	hash := sha256.Sum256([]byte("transfer"))
	testutil.NoError(t, s.markSteppedUp(ctx, sess, hash[:]))

	const n = 16
	var (
		wg           sync.WaitGroup
		start        = make(chan struct{})
		ok, required atomic.Int32
		mu           sync.Mutex
		unexpected   []error
	)
	for i := 0; i < n; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			<-start
			switch err := s.RequireStepUp(ctx, sess, hash[:]); {
			case err == nil:
				ok.Add(1)
			case errors.Is(err, ErrStepUpRequired):
				required.Add(1)
			default:
				mu.Lock()
				unexpected = append(unexpected, err)
				mu.Unlock()
			}
		}()
	}
	close(start)
	wg.Wait()
	testutil.Len(t, unexpected, 0)
	testutil.Equal(t, int32(1), ok.Load(), "exactly one caller consumes the step-up")
	testutil.Equal(t, int32(n-1), required.Load())
}

func TestRequireStepUpTx_RollbackKeepsStepUp(t *testing.T) {
	s, _ := newTestService(t)
	ctx := context.Background()
	sess, err := s.TestLogin(ctx, mustPrincipal(t, s, "liam"))
	testutil.NoError(t, err)
	hash := sha256.Sum256([]byte("transfer"))
	testutil.NoError(t, s.markSteppedUp(ctx, sess, hash[:]))

	boom := errors.New("transfer failed")
	err = db.WithTx(ctx, s.pool, func(ctx context.Context, tx pgx.Tx) error {
		if err := s.RequireStepUpTx(ctx, tx, sess, hash[:]); err != nil {
			return err
		}
		return boom
	})
	testutil.ErrorIs(t, err, boom)

	// The failed transfer rolled the consumption back; a committed one spends it.
	err = db.WithTx(ctx, s.pool, func(ctx context.Context, tx pgx.Tx) error {
		return s.RequireStepUpTx(ctx, tx, sess, hash[:])
	})
	testutil.NoError(t, err)
	testutil.ErrorIs(t, s.RequireStepUp(ctx, sess, hash[:]), ErrStepUpRequired)
}

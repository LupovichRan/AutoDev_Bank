package audit

import (
	"bytes"
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"fmt"
	"sync"
	"testing"
	"time"

	"github.com/LupovichRan/AutoDev_Bank/internal/db"
	"github.com/LupovichRan/AutoDev_Bank/internal/ids"
	"github.com/LupovichRan/AutoDev_Bank/internal/testutil"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

func newRecorder(t *testing.T) (*Recorder, *pgxpool.Pool) {
	t.Helper()
	pool := db.TestPool(t)
	_, priv, err := ed25519.GenerateKey(rand.Reader)
	testutil.NoError(t, err)
	return New(pool, priv), pool
}

func appendOne(t *testing.T, r *Recorder, pool *pgxpool.Pool, ev Event) int64 {
	t.Helper()
	var seq int64
	err := db.WithTx(context.Background(), pool, func(ctx context.Context, tx pgx.Tx) error {
		var err error
		seq, err = r.Append(ctx, tx, ev)
		return err
	})
	testutil.NoError(t, err)
	return seq
}

func TestCanonicalJSON_SortsKeysNoWhitespace(t *testing.T) {
	got, err := CanonicalJSON(map[string]any{"b": 1, "a": map[string]any{"z": "<x>", "y": []any{true, nil}}})
	testutil.NoError(t, err)
	testutil.Equal(t, `{"a":{"y":[true,null],"z":"<x>"},"b":1}`, string(got))
}

func TestAppend_ChainsHashes(t *testing.T) {
	r, pool := newRecorder(t)
	actor := ids.New()
	s1 := appendOne(t, r, pool, Event{ActorID: &actor, Action: "a.one", Subject: "s1", Payload: map[string]any{"k": 1}})
	s2 := appendOne(t, r, pool, Event{Action: "a.two", Subject: "s2"})
	testutil.True(t, s2 > s1)

	evs, err := r.Recent(context.Background(), 10)
	testutil.NoError(t, err)
	testutil.Len(t, evs, 2)
	testutil.Equal(t, s2, evs[0].Seq)
	testutil.Equal(t, s1, evs[1].Seq)

	var prev2, hash1, prev1 []byte
	testutil.NoError(t, pool.QueryRow(context.Background(), `select prev_hash from control.audit_events where seq=$1`, s2).Scan(&prev2))
	testutil.NoError(t, pool.QueryRow(context.Background(), `select hash from control.audit_events where seq=$1`, s1).Scan(&hash1))
	testutil.True(t, bytes.Equal(prev2, hash1), "second prev_hash must equal first hash")
	testutil.NoError(t, pool.QueryRow(context.Background(), `select prev_hash from control.audit_events where seq=$1`, s1).Scan(&prev1))
	testutil.True(t, bytes.Equal(prev1, make([]byte, 32)), "genesis prev_hash is 32 zero bytes")

	rep, err := r.Verify(context.Background())
	testutil.NoError(t, err)
	testutil.True(t, rep.OK)
	testutil.Equal(t, int64(2), rep.Length)
	testutil.Equal(t, int64(0), rep.FirstBadSeq)
	testutil.Equal(t, 64, len(rep.HeadHash))
}

func TestAppend_ImmutableRows(t *testing.T) {
	r, pool := newRecorder(t)
	seq := appendOne(t, r, pool, Event{Action: "a", Subject: "s"})
	_, err := pool.Exec(context.Background(), `update control.audit_events set action='x' where seq=$1`, seq)
	testutil.ErrorContains(t, err, "audit_immutable")
	_, err = pool.Exec(context.Background(), `delete from control.audit_events where seq=$1`, seq)
	testutil.ErrorContains(t, err, "audit_immutable")
}

func TestVerify_EmptyChain(t *testing.T) {
	r, _ := newRecorder(t)
	rep, err := r.Verify(context.Background())
	testutil.NoError(t, err)
	testutil.True(t, rep.OK)
	testutil.Equal(t, int64(0), rep.Length)
	testutil.Equal(t, "", rep.HeadHash)
}

func TestVerify_DetectsTamperedPayload(t *testing.T) {
	r, pool := newRecorder(t)
	appendOne(t, r, pool, Event{Action: "a", Subject: "s", Payload: map[string]any{"amount": 100}})
	bad := appendOne(t, r, pool, Event{Action: "b", Subject: "s", Payload: map[string]any{"amount": 200}})
	appendOne(t, r, pool, Event{Action: "c", Subject: "s"})

	ctx := context.Background()
	_, err := pool.Exec(ctx, `alter table control.audit_events disable trigger audit_events_immutable`)
	testutil.NoError(t, err)
	defer pool.Exec(ctx, `alter table control.audit_events enable trigger audit_events_immutable`)
	_, err = pool.Exec(ctx, `update control.audit_events set payload='{"amount":1}' where seq=$1`, bad)
	testutil.NoError(t, err)

	rep, err := r.Verify(ctx)
	testutil.NoError(t, err)
	testutil.False(t, rep.OK)
	testutil.Equal(t, bad, rep.FirstBadSeq)
}

func TestSignDailyRoot_VerifiesWithPublicKey(t *testing.T) {
	r, pool := newRecorder(t)
	appendOne(t, r, pool, Event{Action: "a", Subject: "s"})
	seq := appendOne(t, r, pool, Event{Action: "b", Subject: "s"})

	root, err := r.SignDailyRoot(context.Background(), time.Now().UTC())
	testutil.NoError(t, err)
	testutil.Equal(t, seq, root.HeadSeq)
	testutil.True(t, VerifyRoot(r.PublicKey(), root))

	root.HeadSeq++
	testutil.False(t, VerifyRoot(r.PublicKey(), root), "tampered root must not verify")

	var n int
	testutil.NoError(t, pool.QueryRow(context.Background(), `select count(*) from control.audit_roots`).Scan(&n))
	testutil.Equal(t, 1, n)

	// Re-signing the same day upserts.
	_, err = r.SignDailyRoot(context.Background(), time.Now().UTC())
	testutil.NoError(t, err)
	testutil.NoError(t, pool.QueryRow(context.Background(), `select count(*) from control.audit_roots`).Scan(&n))
	testutil.Equal(t, 1, n)
}

func TestAppend_ConcurrentStaysLinear(t *testing.T) {
	r, pool := newRecorder(t)
	const workers, each = 16, 5
	errs := make(chan error, workers*each)
	var wg sync.WaitGroup
	for w := 0; w < workers; w++ {
		wg.Add(1)
		go func(w int) {
			defer wg.Done()
			for i := 0; i < each; i++ {
				err := db.WithTx(context.Background(), pool, func(ctx context.Context, tx pgx.Tx) error {
					_, err := r.Append(ctx, tx, Event{Action: "c.append", Subject: fmt.Sprintf("w%d-%d", w, i)})
					return err
				})
				if err != nil {
					errs <- err
				}
			}
		}(w)
	}
	wg.Wait()
	close(errs)
	for err := range errs {
		t.Errorf("append failed: %v", err)
	}
	rep, err := r.Verify(context.Background())
	testutil.NoError(t, err)
	testutil.True(t, rep.OK, "chain must verify")
	testutil.Equal(t, int64(workers*each), rep.Length)
}

func TestAppend_LargeIntegerPayloadSurvivesVerify(t *testing.T) {
	r, pool := newRecorder(t)
	appendOne(t, r, pool, Event{Action: "big", Subject: "s", Payload: map[string]any{"n": int64(9007199254740993), "m": int64(9223372036854775807)}})
	rep, err := r.Verify(context.Background())
	testutil.NoError(t, err)
	testutil.True(t, rep.OK, "large integers must hash exactly")
	testutil.Equal(t, int64(1), rep.Length)

	// Integers above 2^53 must hash exactly, not via a rounded float64.
	zero := make([]byte, 32)
	at := time.Now().UTC().Truncate(time.Microsecond)
	h1, err := computeHash(zero, at, nil, "a", "s", []byte(`{"n":9007199254740992}`))
	testutil.NoError(t, err)
	h2, err := computeHash(zero, at, nil, "a", "s", []byte(`{"n":9007199254740993}`))
	testutil.NoError(t, err)
	testutil.False(t, bytes.Equal(h1, h2), "2^53 and 2^53+1 must hash differently")
}

func TestRoot_ReadOnlyAndMissing(t *testing.T) {
	r, pool := newRecorder(t)
	ctx := context.Background()
	yesterday := time.Now().UTC().AddDate(0, 0, -1)

	_, ok, err := r.Root(ctx, yesterday)
	testutil.NoError(t, err)
	testutil.False(t, ok, "no stored root yet")

	appendOne(t, r, pool, Event{Action: "a", Subject: "s"})
	signed, err := r.SignDailyRoot(ctx, yesterday)
	testutil.NoError(t, err)
	got, ok, err := r.Root(ctx, yesterday)
	testutil.NoError(t, err)
	testutil.True(t, ok, "stored root found")
	testutil.Equal(t, signed.Day, got.Day)
	testutil.Equal(t, signed.HeadSeq, got.HeadSeq)
	testutil.Equal(t, signed.HeadHash, got.HeadHash)
	testutil.True(t, bytes.Equal(signed.Signature, got.Signature), "same signature")
	testutil.True(t, VerifyRoot(r.PublicKey(), got), "stored root verifies")

	var n int
	testutil.NoError(t, pool.QueryRow(ctx, `select count(*) from control.audit_roots`).Scan(&n))
	testutil.Equal(t, 1, n)
}

func TestSignDailyRootIfMissing_NeverOverwrites(t *testing.T) {
	r, pool := newRecorder(t)
	ctx := context.Background()
	yesterday := time.Now().UTC().AddDate(0, 0, -1)

	first, created, err := r.SignDailyRootIfMissing(ctx, yesterday)
	testutil.NoError(t, err)
	testutil.True(t, created, "first call stores the root")
	testutil.True(t, VerifyRoot(r.PublicKey(), first), "verifies")

	// Corrupt the stored signature; a second call must return it unchanged.
	bad := bytes.Repeat([]byte{7}, ed25519.SignatureSize)
	_, err = pool.Exec(ctx, `update control.audit_roots set signature = $1`, bad)
	testutil.NoError(t, err)
	again, created, err := r.SignDailyRootIfMissing(ctx, yesterday)
	testutil.NoError(t, err)
	testutil.False(t, created, "existing root is kept")
	testutil.True(t, bytes.Equal(bad, again.Signature), "stored signature returned as is")
	testutil.False(t, VerifyRoot(r.PublicKey(), again), "corrupted root does not verify")

	var sig []byte
	testutil.NoError(t, pool.QueryRow(ctx, `select signature from control.audit_roots`).Scan(&sig))
	testutil.True(t, bytes.Equal(bad, sig), "row not rewritten")

	// An unfinished day is never persisted.
	_, _, err = r.SignDailyRootIfMissing(ctx, time.Now().UTC())
	testutil.Error(t, err)
}

func TestComputeRoot_DoesNotPersist(t *testing.T) {
	r, pool := newRecorder(t)
	ctx := context.Background()
	seq := appendOne(t, r, pool, Event{Action: "a", Subject: "s"})
	root, err := r.ComputeRoot(ctx, time.Now().UTC())
	testutil.NoError(t, err)
	testutil.Equal(t, seq, root.HeadSeq)
	testutil.True(t, VerifyRoot(r.PublicKey(), root), "verifies")
	var n int
	testutil.NoError(t, pool.QueryRow(ctx, `select count(*) from control.audit_roots`).Scan(&n))
	testutil.Equal(t, 0, n)
}

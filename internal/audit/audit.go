// Package audit implements the hash-chained, append-only audit log.
//
// Every sensitive action appends an Event inside the caller's transaction.
// Each row stores hash = sha256(prev_hash || canonicalJSON({at, actor_id,
// action, subject, payload})); the genesis prev_hash is 32 zero bytes. Daily
// chain heads are signed with Ed25519 and stored in control.audit_roots.
package audit

import (
	"bytes"
	"context"
	"crypto/ed25519"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

// Event is one audit record. Seq, At and Hash are populated on read.
type Event struct {
	ActorID *uuid.UUID
	Action  string
	Subject string
	Payload map[string]any

	Seq  int64
	At   time.Time
	Hash []byte
}

// Report is the result of verifying the whole chain.
type Report struct {
	OK          bool
	Length      int64
	HeadHash    string // hex of the last hash; empty when the chain is empty
	FirstBadSeq int64  // 0 when OK
}

// Root is a signed daily chain head.
type Root struct {
	Day       string // YYYY-MM-DD
	HeadSeq   int64
	HeadHash  string // hex
	Signature []byte
}

// Recorder appends to and verifies the audit chain.
type Recorder struct {
	pool   *pgxpool.Pool
	signer ed25519.PrivateKey
	now    func() time.Time // event timestamps and "today"; tests may override
}

// New returns a Recorder. signer is used only to sign daily roots; it may
// be nil for a recorder that only appends (e.g. `bank seed`).
func New(pool *pgxpool.Pool, signer ed25519.PrivateKey) *Recorder {
	return &Recorder{pool: pool, signer: signer, now: time.Now}
}

// PublicKey returns the key that verifies signed roots.
func (r *Recorder) PublicKey() ed25519.PublicKey {
	return r.signer.Public().(ed25519.PublicKey)
}

func formatAt(t time.Time) string { return t.UTC().Format(time.RFC3339Nano) }

func computeHash(prev []byte, at time.Time, actor *uuid.UUID, action, subject string, payload []byte) ([]byte, error) {
	var actorVal any
	if actor != nil {
		actorVal = actor.String()
	}
	var pl any
	// UseNumber keeps integers beyond 2^53 exact instead of rounding via float64.
	dec := json.NewDecoder(bytes.NewReader(payload))
	dec.UseNumber()
	if err := dec.Decode(&pl); err != nil {
		return nil, err
	}
	body, err := CanonicalJSON(map[string]any{
		"at":       formatAt(at),
		"actor_id": actorVal,
		"action":   action,
		"subject":  subject,
		"payload":  pl,
	})
	if err != nil {
		return nil, err
	}
	h := sha256.New()
	h.Write(prev)
	h.Write(body)
	return h.Sum(nil), nil
}

// Append adds ev to the chain inside tx and returns its seq.
//
// Chain growth is serialised through the single control.audit_head row, read
// with SELECT ... FOR UPDATE as the first statement. We deliberately do not
// use an advisory lock: db.WithTx runs at SERIALIZABLE, whose snapshot is
// taken at the first statement, i.e. before any lock wait finishes, so after
// waiting the transaction would read the chain head from a stale snapshot. A
// row lock has the right semantics here: if the head row was updated by a
// transaction that committed after our snapshot, FOR UPDATE raises 40001
// (serialization_failure), which db.WithTx retries on a fresh snapshot. The
// chain is therefore always linear; contention costs retries, not forks.
//
// As a cheap pre-serialiser, Append first takes LOCK TABLE audit_head IN
// EXCLUSIVE MODE. LOCK is a utility statement that does not take a snapshot,
// so when Append is the first statement of the transaction the wait happens
// before the snapshot exists, the snapshot is fresh once the lock is granted,
// and appenders queue instead of stampeding into 40001 retries. If earlier
// statements already fixed the snapshot, the row lock below still detects the
// concurrent append and forces a retry.
func (r *Recorder) Append(ctx context.Context, tx pgx.Tx, ev Event) (int64, error) {
	if _, err := tx.Exec(ctx, `lock table control.audit_head in exclusive mode`); err != nil {
		return 0, fmt.Errorf("audit: lock table: %w", err)
	}
	prev, err := lockHead(ctx, tx)
	if err != nil {
		return 0, err
	}

	payload := ev.Payload
	if payload == nil {
		payload = map[string]any{}
	}
	// Canonical bytes are what we store, so jsonb round-trips to the same value.
	payloadJSON, err := CanonicalJSON(payload)
	if err != nil {
		return 0, fmt.Errorf("audit: payload: %w", err)
	}
	at := r.now().UTC().Truncate(time.Microsecond) // timestamptz precision
	hash, err := computeHash(prev, at, ev.ActorID, ev.Action, ev.Subject, payloadJSON)
	if err != nil {
		return 0, err
	}
	var seq int64
	err = tx.QueryRow(ctx, `
		insert into control.audit_events (at, actor_id, action, subject, payload, prev_hash, hash)
		values ($1, $2, $3, $4, $5::jsonb, $6, $7) returning seq`,
		at, ev.ActorID, ev.Action, ev.Subject, string(payloadJSON), prev, hash).Scan(&seq)
	if err != nil {
		return 0, fmt.Errorf("audit: insert: %w", err)
	}
	if _, err := tx.Exec(ctx, `update control.audit_head set seq = $1, hash = $2 where id = 1`, seq, hash); err != nil {
		return 0, fmt.Errorf("audit: advance head: %w", err)
	}
	return seq, nil
}

// lockHead row-locks the chain head and returns its hash. The migration seeds
// the row; it is re-created lazily if absent (e.g. after a test truncate).
func lockHead(ctx context.Context, tx pgx.Tx) ([]byte, error) {
	var prev []byte
	q := `select hash from control.audit_head where id = 1 for update`
	err := tx.QueryRow(ctx, q).Scan(&prev)
	if errors.Is(err, pgx.ErrNoRows) {
		if _, err := tx.Exec(ctx, `insert into control.audit_head (id, seq, hash) values (1, 0, $1) on conflict (id) do nothing`, make([]byte, 32)); err != nil {
			return nil, fmt.Errorf("audit: seed head: %w", err)
		}
		err = tx.QueryRow(ctx, q).Scan(&prev)
	}
	if err != nil {
		return nil, fmt.Errorf("audit: lock head: %w", err)
	}
	return prev, nil
}

// Verify recomputes the whole chain in seq order and reports the first row
// whose stored hash or prev_hash link does not match.
func (r *Recorder) Verify(ctx context.Context) (Report, error) {
	rows, err := r.pool.Query(ctx, `
		select seq, at, actor_id, action, subject, payload::text, prev_hash, hash
		from control.audit_events order by seq`)
	if err != nil {
		return Report{}, err
	}
	defer rows.Close()

	rep := Report{OK: true}
	prev := make([]byte, 32)
	var head []byte
	for rows.Next() {
		var (
			seq                      int64
			at                       time.Time
			actor                    *uuid.UUID
			action, subject, payload string
			storedPrev, storedHash   []byte
		)
		if err := rows.Scan(&seq, &at, &actor, &action, &subject, &payload, &storedPrev, &storedHash); err != nil {
			return Report{}, err
		}
		rep.Length++
		head = storedHash
		if !rep.OK {
			continue // keep counting length and head, but the first bad seq is fixed
		}
		want, err := computeHash(prev, at, actor, action, subject, []byte(payload))
		if err != nil {
			return Report{}, err
		}
		if !bytes.Equal(storedPrev, prev) || !bytes.Equal(storedHash, want) {
			rep.OK = false
			rep.FirstBadSeq = seq
		}
		prev = storedHash
	}
	if err := rows.Err(); err != nil {
		return Report{}, err
	}
	if head != nil {
		rep.HeadHash = hex.EncodeToString(head)
	}
	return rep, nil
}

// Recent returns the latest events, newest first.
func (r *Recorder) Recent(ctx context.Context, limit int) ([]Event, error) {
	rows, err := r.pool.Query(ctx, `
		select seq, at, actor_id, action, subject, payload::text, hash
		from control.audit_events order by seq desc limit $1`, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Event
	for rows.Next() {
		var ev Event
		var payload string
		if err := rows.Scan(&ev.Seq, &ev.At, &ev.ActorID, &ev.Action, &ev.Subject, &payload, &ev.Hash); err != nil {
			return nil, err
		}
		if err := json.Unmarshal([]byte(payload), &ev.Payload); err != nil {
			return nil, err
		}
		ev.At = ev.At.UTC()
		out = append(out, ev)
	}
	return out, rows.Err()
}

func rootMessage(day string, headSeq int64, headHash string) ([]byte, error) {
	return CanonicalJSON(map[string]any{"day": day, "head_seq": headSeq, "head_hash": headHash})
}

func dayStart(day time.Time) time.Time {
	d := day.UTC()
	return time.Date(d.Year(), d.Month(), d.Day(), 0, 0, 0, 0, time.UTC)
}

// finished reports whether the UTC day has ended.
func (r *Recorder) finished(day time.Time) bool {
	return dayStart(day).Before(dayStart(r.now()))
}

// HeadAsOf returns the chain head as of the end of day (UTC), unsigned
// (Signature is nil). For today it is a provisional head that will still
// move. The day's head is chosen by each event's application-assigned
// timestamp (the app clock at Append), so the result is only exact when all
// app servers have synchronised clocks.
func (r *Recorder) HeadAsOf(ctx context.Context, day time.Time) (Root, error) {
	start := dayStart(day)
	end := start.AddDate(0, 0, 1)

	var seq int64
	head := make([]byte, 32) // empty chain: the genesis hash at seq 0
	err := r.pool.QueryRow(ctx, `
		select seq, hash from control.audit_events where at < $1 order by seq desc limit 1`, end).Scan(&seq, &head)
	if err != nil && !errors.Is(err, pgx.ErrNoRows) {
		return Root{}, err
	}
	if errors.Is(err, pgx.ErrNoRows) {
		seq, head = 0, make([]byte, 32)
	}
	return Root{Day: start.Format("2006-01-02"), HeadSeq: seq, HeadHash: hex.EncodeToString(head)}, nil
}

// ComputeRoot signs the chain head as of the end of a finished UTC day in
// memory, without storing it. Today (or a future day) is never signed: it
// fails with ErrDayNotFinished; use HeadAsOf for a provisional head.
func (r *Recorder) ComputeRoot(ctx context.Context, day time.Time) (Root, error) {
	if !r.finished(day) {
		return Root{}, ErrDayNotFinished
	}
	if r.signer == nil {
		return Root{}, errors.New("audit: no signing key")
	}
	root, err := r.HeadAsOf(ctx, day)
	if err != nil {
		return Root{}, err
	}
	msg, err := rootMessage(root.Day, root.HeadSeq, root.HeadHash)
	if err != nil {
		return Root{}, err
	}
	root.Signature = ed25519.Sign(r.signer, msg)
	return root, nil
}

// SignDailyRoot signs the chain head as of the end of a finished day (UTC)
// and upserts it into control.audit_roots, replacing any stored root for
// that day. It is for explicit operator use; automatic signing uses
// SignDailyRootIfMissing. An unfinished day is ErrDayNotFinished.
func (r *Recorder) SignDailyRoot(ctx context.Context, day time.Time) (Root, error) {
	root, err := r.ComputeRoot(ctx, day)
	if err != nil {
		return Root{}, err
	}
	head, _ := hex.DecodeString(root.HeadHash)
	_, err = r.pool.Exec(ctx, `
		insert into control.audit_roots (day, head_seq, head_hash, signature, signed_at)
		values ($1::date, $2, $3, $4, now())
		on conflict (day) do update
		  set head_seq = excluded.head_seq, head_hash = excluded.head_hash,
		      signature = excluded.signature, signed_at = excluded.signed_at`,
		root.Day, root.HeadSeq, head, root.Signature)
	if err != nil {
		return Root{}, err
	}
	return root, nil
}

// ErrDayNotFinished is returned when a root would be stored for a UTC day
// that has not ended yet.
var ErrDayNotFinished = errors.New("audit: day is not finished")

// SignDailyRootIfMissing stores a signed root for a finished UTC day unless
// one is already stored, in which case the stored root is returned
// unchanged (even if its signature no longer verifies). created reports
// whether this call stored it.
func (r *Recorder) SignDailyRootIfMissing(ctx context.Context, day time.Time) (root Root, created bool, err error) {
	if !r.finished(day) {
		return Root{}, false, ErrDayNotFinished
	}
	if stored, ok, err := r.Root(ctx, day); err != nil || ok {
		return stored, false, err
	}
	root, err = r.ComputeRoot(ctx, day)
	if err != nil {
		return Root{}, false, err
	}
	head, _ := hex.DecodeString(root.HeadHash)
	tag, err := r.pool.Exec(ctx, `
		insert into control.audit_roots (day, head_seq, head_hash, signature, signed_at)
		values ($1::date, $2, $3, $4, now())
		on conflict (day) do nothing`,
		root.Day, root.HeadSeq, head, root.Signature)
	if err != nil {
		return Root{}, false, err
	}
	if tag.RowsAffected() == 0 { // lost a race with another signer
		stored, _, err := r.Root(ctx, day)
		return stored, false, err
	}
	return root, true, nil
}

// Root returns the stored root for day (UTC), if any. It never writes.
func (r *Recorder) Root(ctx context.Context, day time.Time) (Root, bool, error) {
	var root Root
	var head []byte
	err := r.pool.QueryRow(ctx, `
		select to_char(day, 'YYYY-MM-DD'), head_seq, head_hash, signature
		from control.audit_roots where day = $1::date`, dayStart(day).Format("2006-01-02")).
		Scan(&root.Day, &root.HeadSeq, &head, &root.Signature)
	if errors.Is(err, pgx.ErrNoRows) {
		return Root{}, false, nil
	}
	if err != nil {
		return Root{}, false, err
	}
	root.HeadHash = hex.EncodeToString(head)
	return root, true, nil
}

// VerifyRoot reports whether r carries a valid signature under pub.
func VerifyRoot(pub ed25519.PublicKey, r Root) bool {
	msg, err := rootMessage(r.Day, r.HeadSeq, r.HeadHash)
	if err != nil {
		return false
	}
	return ed25519.Verify(pub, msg, r.Signature)
}

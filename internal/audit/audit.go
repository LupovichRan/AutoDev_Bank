// Package audit implements the hash-chained, append-only audit log.
//
// Every sensitive action appends an Event inside the caller's transaction.
// Each row stores hash = sha256(prev_hash || canonicalJSON({at, actor_id,
// action, subject, payload})); the genesis prev_hash is 32 zero bytes. Daily
// chain heads are signed with Ed25519 and stored in control.audit_roots.
package audit

import (
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

// chainLockKey serialises chain growth across transactions.
const chainLockKey = 7312

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
}

// New returns a Recorder. signer is used only by SignDailyRoot.
func New(pool *pgxpool.Pool, signer ed25519.PrivateKey) *Recorder {
	return &Recorder{pool: pool, signer: signer}
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
	if err := json.Unmarshal(payload, &pl); err != nil {
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

// Append adds ev to the chain inside tx and returns its seq. The chain is
// serialised with a transaction-scoped advisory lock, so concurrent appends
// queue up until the earlier transaction commits.
func (r *Recorder) Append(ctx context.Context, tx pgx.Tx, ev Event) (int64, error) {
	if _, err := tx.Exec(ctx, `select pg_advisory_xact_lock($1)`, int64(chainLockKey)); err != nil {
		return 0, fmt.Errorf("audit: lock: %w", err)
	}
	prev := make([]byte, 32)
	err := tx.QueryRow(ctx, `select hash from control.audit_events order by seq desc limit 1`).Scan(&prev)
	if err != nil && !errors.Is(err, pgx.ErrNoRows) {
		return 0, fmt.Errorf("audit: read head: %w", err)
	}
	if errors.Is(err, pgx.ErrNoRows) {
		prev = make([]byte, 32)
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
	at := time.Now().UTC().Truncate(time.Microsecond) // timestamptz precision
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
	return seq, nil
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
		if !bytesEqual(storedPrev, prev) || !bytesEqual(storedHash, want) {
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

func bytesEqual(a, b []byte) bool {
	if len(a) != len(b) {
		return false
	}
	var d byte
	for i := range a {
		d |= a[i] ^ b[i]
	}
	return d == 0
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

// SignDailyRoot signs the chain head as of the end of day (UTC) and upserts
// it into control.audit_roots.
func (r *Recorder) SignDailyRoot(ctx context.Context, day time.Time) (Root, error) {
	d := day.UTC()
	start := time.Date(d.Year(), d.Month(), d.Day(), 0, 0, 0, 0, time.UTC)
	end := start.AddDate(0, 0, 1)

	var seq int64
	head := make([]byte, 32) // empty chain signs the genesis hash at seq 0
	err := r.pool.QueryRow(ctx, `
		select seq, hash from control.audit_events where at < $1 order by seq desc limit 1`, end).Scan(&seq, &head)
	if err != nil && !errors.Is(err, pgx.ErrNoRows) {
		return Root{}, err
	}
	if errors.Is(err, pgx.ErrNoRows) {
		seq, head = 0, make([]byte, 32)
	}
	root := Root{Day: start.Format("2006-01-02"), HeadSeq: seq, HeadHash: hex.EncodeToString(head)}
	msg, err := rootMessage(root.Day, root.HeadSeq, root.HeadHash)
	if err != nil {
		return Root{}, err
	}
	root.Signature = ed25519.Sign(r.signer, msg)
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

// VerifyRoot reports whether r carries a valid signature under pub.
func VerifyRoot(pub ed25519.PublicKey, r Root) bool {
	msg, err := rootMessage(r.Day, r.HeadSeq, r.HeadHash)
	if err != nil {
		return false
	}
	return ed25519.Verify(pub, msg, r.Signature)
}

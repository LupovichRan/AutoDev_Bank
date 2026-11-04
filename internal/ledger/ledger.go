// Package ledger is the Go service over the double-entry journal in schema
// ledger. The SQL triggers (migration 0002) enforce the invariants; this
// package validates requests early, makes posting idempotent, takes account
// locks in a deterministic order, and maps schema errors to typed errors.
package ledger

import (
	"bytes"
	"cmp"
	"context"
	"crypto/sha256"
	"encoding/binary"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"slices"
	"strings"
	"time"

	"github.com/LupovichRan/AutoDev_Bank/internal/db"
	"github.com/LupovichRan/AutoDev_Bank/internal/ids"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"
)

const dateLayout = "2006-01-02"

// lockStripes is the number of in-process account lock stripes (see lockAccountsLocal).
const lockStripes = 256

// Service is the ledger API. It is safe for concurrent use; construct it with New.
type Service struct {
	pool *pgxpool.Pool
	// stripes are 1-slot semaphores, one per account-id stripe.
	stripes [lockStripes]chan struct{}
}

// New returns a Service backed by pool.
func New(pool *pgxpool.Pool) *Service {
	s := &Service{pool: pool}
	for i := range s.stripes {
		s.stripes[i] = make(chan struct{}, 1)
	}
	return s
}

// querier is satisfied by both *pgxpool.Pool and pgx.Tx.
type querier interface {
	Exec(ctx context.Context, sql string, args ...any) (pgconn.CommandTag, error)
	Query(ctx context.Context, sql string, args ...any) (pgx.Rows, error)
	QueryRow(ctx context.Context, sql string, args ...any) pgx.Row
}

// ---------------------------------------------------------------------------
// Posting

// Post records req in its own serializable transaction (retried on
// serialization failures). See PostTx.
func (s *Service) Post(ctx context.Context, req PostRequest) (Transaction, error) {
	unlock, err := s.lockAccountsLocal(ctx, req.Lines)
	if err != nil {
		return Transaction{}, err
	}
	defer unlock()
	var out Transaction
	err = db.WithTx(ctx, s.pool, func(ctx context.Context, tx pgx.Tx) (err error) {
		out, err = s.PostTx(ctx, tx, req)
		return err
	})
	if err != nil {
		return Transaction{}, mapErr(err) // commit-time (deferred) trigger errors land here
	}
	return out, nil
}

// PostTx records req inside the caller's transaction. Replaying an
// IdempotencyKey with the same payload returns the existing transaction and
// writes nothing; with a different payload it fails with
// ErrIdempotencyConflict. On any error the caller must roll back.
func (s *Service) PostTx(ctx context.Context, tx pgx.Tx, req PostRequest) (Transaction, error) {
	out, err := postTx(ctx, tx, req)
	return out, mapErr(err)
}

func postTx(ctx context.Context, tx pgx.Tx, req PostRequest) (Transaction, error) {
	if err := validatePost(req); err != nil {
		return Transaction{}, err
	}
	date := effectiveDate(req.EffectiveDate)
	hash := payloadHash(req, date)
	status := StatusPosted
	if req.Pending {
		status = StatusPending
	}
	meta := req.Metadata
	if meta == nil {
		meta = map[string]any{}
	}

	id := ids.New()
	err := tx.QueryRow(ctx, `insert into ledger.transactions
		  (id, idempotency_key, payload_hash, description, status, effective_date, expires_at, metadata)
		values ($1, $2, $3, $4, $5, $6::date, $7, $8)
		on conflict (idempotency_key) do nothing
		returning id`,
		id, req.IdempotencyKey, hash, req.Description, status, date, req.ExpiresAt, meta).Scan(&id)
	if errors.Is(err, pgx.ErrNoRows) {
		return replay(ctx, tx, req, hash)
	}
	if err != nil {
		return Transaction{}, fmt.Errorf("ledger: insert transaction: %w", err)
	}

	if err := lockAccounts(ctx, tx, req); err != nil {
		return Transaction{}, err
	}
	for _, l := range req.Lines {
		if _, err := tx.Exec(ctx, `insert into ledger.entries (id, transaction_id, account_id, amount, currency)
			values ($1, $2, $3, $4, $5)`, ids.New(), id, l.AccountID, l.Amount, req.Currency); err != nil {
			return Transaction{}, fmt.Errorf("ledger: insert entry: %w", err)
		}
	}
	return getTransaction(ctx, tx, id)
}

// replay handles an idempotency-key hit: same payload returns the stored
// transaction, a different payload is a conflict.
func replay(ctx context.Context, tx pgx.Tx, req PostRequest, hash []byte) (Transaction, error) {
	var id uuid.UUID
	var stored []byte
	var storedDate time.Time
	if err := tx.QueryRow(ctx, `select id, payload_hash, effective_date from ledger.transactions
		where idempotency_key = $1`, req.IdempotencyKey).Scan(&id, &stored, &storedDate); err != nil {
		return Transaction{}, fmt.Errorf("ledger: load idempotent transaction: %w", err)
	}
	if req.EffectiveDate.IsZero() {
		// "Today" was defaulted; a retry after midnight must still match.
		hash = payloadHash(req, storedDate.Format(dateLayout))
	}
	if !bytes.Equal(stored, hash) {
		return Transaction{}, fmt.Errorf("%w: key %q", ErrIdempotencyConflict, req.IdempotencyKey)
	}
	return getTransaction(ctx, tx, id)
}

// lockAccountsLocal serialises, within this process, Posts that touch the
// same account stripe, taking stripes in ascending order (deadlock-free).
// Under SERIALIZABLE a transaction that waits on another's FOR UPDATE row
// lock fails with 40001 once the holder commits, so many concurrent postings
// on one hot account would exhaust db.WithTx's retries; queueing them here,
// before the snapshot is taken, avoids that retry storm. Database locks
// (lockAccounts) and retries still guarantee correctness across processes.
// PostTx does not use it: the caller's snapshot already exists.
func (s *Service) lockAccountsLocal(ctx context.Context, lines []Line) (unlock func(), err error) {
	idx := make([]int, 0, len(lines))
	for _, l := range lines {
		idx = append(idx, int(binary.BigEndian.Uint64(l.AccountID[8:])%lockStripes))
	}
	slices.Sort(idx)
	idx = slices.Compact(idx)
	release := func(held []int) {
		for _, i := range held {
			<-s.stripes[i]
		}
	}
	for n, i := range idx {
		select {
		case s.stripes[i] <- struct{}{}:
		case <-ctx.Done():
			release(idx[:n])
			return nil, ctx.Err()
		}
	}
	return func() { release(idx) }, nil
}

// lockAccounts locks every account in req in ascending id order (so
// concurrent postings cannot deadlock) and checks existence and currency.
func lockAccounts(ctx context.Context, tx pgx.Tx, req PostRequest) error {
	want := make([]uuid.UUID, 0, len(req.Lines))
	for _, l := range req.Lines {
		want = append(want, l.AccountID)
	}
	slices.SortFunc(want, func(a, b uuid.UUID) int { return bytes.Compare(a[:], b[:]) })
	want = slices.Compact(want)

	rows, err := tx.Query(ctx, `select id, currency from ledger.accounts
		where id = any($1) order by id for update`, want)
	if err != nil {
		return fmt.Errorf("ledger: lock accounts: %w", err)
	}
	found := map[uuid.UUID]string{}
	for rows.Next() {
		var id uuid.UUID
		var cur string
		if err := rows.Scan(&id, &cur); err != nil {
			rows.Close()
			return fmt.Errorf("ledger: lock accounts: %w", err)
		}
		found[id] = cur
	}
	if err := rows.Err(); err != nil {
		return fmt.Errorf("ledger: lock accounts: %w", err)
	}
	for _, id := range want {
		cur, ok := found[id]
		if !ok {
			return fmt.Errorf("%w: account %s", ErrNotFound, id)
		}
		if cur != req.Currency {
			return fmt.Errorf("%w: account %s is %s, request is %s", ErrCurrencyMismatch, id, cur, req.Currency)
		}
	}
	return nil
}

func validatePost(req PostRequest) error {
	if strings.TrimSpace(req.IdempotencyKey) == "" {
		return fmt.Errorf("%w: idempotency key is required", ErrInvalidRequest)
	}
	if len(req.Currency) != 3 {
		return fmt.Errorf("%w: currency %q is not a 3-letter code", ErrInvalidRequest, req.Currency)
	}
	if req.ExpiresAt != nil && !req.Pending {
		return fmt.Errorf("%w: expires_at is only valid for pending transactions", ErrInvalidRequest)
	}
	if len(req.Lines) < 2 {
		return fmt.Errorf("%w: need at least 2 lines, got %d", ErrUnbalanced, len(req.Lines))
	}
	var sum int64
	for i, l := range req.Lines {
		if l.AccountID == uuid.Nil {
			return fmt.Errorf("%w: line %d has no account", ErrInvalidRequest, i)
		}
		if l.Amount == 0 {
			return fmt.Errorf("%w: line %d has a zero amount", ErrInvalidRequest, i)
		}
		if (l.Amount > 0 && sum > math.MaxInt64-l.Amount) || (l.Amount < 0 && sum < math.MinInt64-l.Amount) {
			return fmt.Errorf("%w: line amounts overflow", ErrInvalidRequest)
		}
		sum += l.Amount
	}
	if sum != 0 {
		return fmt.Errorf("%w: lines sum to %d", ErrUnbalanced, sum)
	}
	return nil
}

// effectiveDate renders t as YYYY-MM-DD in t's own location; zero means today (UTC).
func effectiveDate(t time.Time) string {
	if t.IsZero() {
		t = time.Now().UTC()
	}
	return t.Format(dateLayout)
}

// payloadHash is sha256 over canonical JSON (keys sorted, lines sorted by
// account id then amount) of the fields that define a posting's meaning.
func payloadHash(req PostRequest, date string) []byte {
	type line struct {
		AccountID string `json:"account_id"`
		Amount    int64  `json:"amount"`
	}
	type payload struct { // fields in key order
		Currency      string `json:"currency"`
		Description   string `json:"description"`
		EffectiveDate string `json:"effective_date"`
		Lines         []line `json:"lines"`
		Pending       bool   `json:"pending"`
	}
	lines := make([]line, len(req.Lines))
	for i, l := range req.Lines {
		lines[i] = line{l.AccountID.String(), l.Amount}
	}
	slices.SortFunc(lines, func(a, b line) int {
		if c := strings.Compare(a.AccountID, b.AccountID); c != 0 {
			return c
		}
		return cmp.Compare(a.Amount, b.Amount)
	})
	b, err := json.Marshal(payload{req.Currency, req.Description, date, lines, req.Pending})
	if err != nil {
		panic(fmt.Sprintf("ledger: marshal payload: %v", err)) // only strings and ints: cannot fail
	}
	sum := sha256.Sum256(b)
	return sum[:]
}

// ---------------------------------------------------------------------------
// Two-phase resolution

// Resolve posts (post=true) or voids (post=false) a pending transaction.
// It fails with ErrAlreadyResolved if the transaction is no longer pending,
// and with ErrExpired when posting a hold whose expires_at has passed
// (voiding an expired hold is allowed).
func (s *Service) Resolve(ctx context.Context, txID uuid.UUID, post bool) (Transaction, error) {
	var out Transaction
	err := db.WithTx(ctx, s.pool, func(ctx context.Context, tx pgx.Tx) (err error) {
		out, err = resolveTx(ctx, tx, txID, post)
		return err
	})
	if err != nil {
		return Transaction{}, mapErr(err)
	}
	return out, nil
}

func resolveTx(ctx context.Context, tx pgx.Tx, id uuid.UUID, post bool) (Transaction, error) {
	var status string
	var expired bool
	err := tx.QueryRow(ctx, `select status, coalesce(expires_at < now(), false)
		from ledger.transactions where id = $1 for update`, id).Scan(&status, &expired)
	if errors.Is(err, pgx.ErrNoRows) {
		return Transaction{}, fmt.Errorf("%w: transaction %s", ErrNotFound, id)
	}
	if err != nil {
		return Transaction{}, fmt.Errorf("ledger: load transaction: %w", err)
	}
	if status != StatusPending {
		return Transaction{}, fmt.Errorf("%w: transaction %s is %s", ErrAlreadyResolved, id, status)
	}
	if post && expired {
		return Transaction{}, fmt.Errorf("%w: transaction %s", ErrExpired, id)
	}
	// Same ascending lock order as posting; the trigger then updates them.
	if _, err := tx.Exec(ctx, `select id from ledger.accounts
		where id in (select account_id from ledger.entries where transaction_id = $1)
		order by id for update`, id); err != nil {
		return Transaction{}, fmt.Errorf("ledger: lock accounts: %w", err)
	}
	next := StatusVoided
	if post {
		next = StatusPosted
	}
	if _, err := tx.Exec(ctx, `update ledger.transactions set status = $2 where id = $1`, id, next); err != nil {
		return Transaction{}, fmt.Errorf("ledger: resolve transaction: %w", err)
	}
	return getTransaction(ctx, tx, id)
}

// ExpireHolds voids every pending transaction whose expires_at has passed,
// each in its own transaction, and returns how many it voided.
func (s *Service) ExpireHolds(ctx context.Context) (voided int, err error) {
	rows, err := s.pool.Query(ctx, `select id from ledger.transactions
		where status = 'pending' and expires_at < now() order by expires_at`)
	if err != nil {
		return 0, fmt.Errorf("ledger: list expired holds: %w", err)
	}
	expired, err := pgx.CollectRows(rows, pgx.RowTo[uuid.UUID])
	if err != nil {
		return 0, fmt.Errorf("ledger: list expired holds: %w", err)
	}
	for _, id := range expired {
		_, err := s.Resolve(ctx, id, false)
		if errors.Is(err, ErrAlreadyResolved) {
			continue // resolved concurrently
		}
		if err != nil {
			return voided, err
		}
		voided++
	}
	return voided, nil
}

// ---------------------------------------------------------------------------
// Period close

// ClosePeriod closes currency through the calendar date of through: no
// entries with an effective date on or before it can be added afterwards.
// Closing never moves backwards; an earlier date is a no-op.
func (s *Service) ClosePeriod(ctx context.Context, currency string, through time.Time) error {
	if len(currency) != 3 {
		return fmt.Errorf("%w: currency %q is not a 3-letter code", ErrInvalidRequest, currency)
	}
	_, err := s.pool.Exec(ctx, `insert into ledger.period_closes (currency, closed_through)
		values ($1, $2::date)
		on conflict (currency) do update
		  set closed_through = excluded.closed_through, closed_at = now()
		  where ledger.period_closes.closed_through < excluded.closed_through`,
		currency, through.Format(dateLayout))
	if err != nil {
		return fmt.Errorf("ledger: close period: %w", err)
	}
	return nil
}

// ---------------------------------------------------------------------------
// Accounts and reads

const accountCols = `id, code, name, currency, kind, owner_id, allow_negative,
	balance, pending_debits, pending_credits, balance - pending_debits, version, created_at`

func scanAccount(row pgx.Row) (Account, error) {
	var a Account
	err := row.Scan(&a.ID, &a.Code, &a.Name, &a.Currency, &a.Kind, &a.OwnerID, &a.AllowNegative,
		&a.Balance, &a.PendingDebits, &a.PendingCredits, &a.Available, &a.Version, &a.CreatedAt)
	return a, err
}

// CreateAccount creates an account with zero balances.
func (s *Service) CreateAccount(ctx context.Context, spec AccountSpec) (Account, error) {
	if spec.Code == "" || spec.Name == "" {
		return Account{}, fmt.Errorf("%w: account code and name are required", ErrInvalidRequest)
	}
	if len(spec.Currency) != 3 {
		return Account{}, fmt.Errorf("%w: currency %q is not a 3-letter code", ErrInvalidRequest, spec.Currency)
	}
	a, err := scanAccount(s.pool.QueryRow(ctx, `insert into ledger.accounts
		  (id, code, name, currency, kind, owner_id, allow_negative)
		values ($1, $2, $3, $4, $5, $6, $7)
		returning `+accountCols,
		ids.New(), spec.Code, spec.Name, spec.Currency, spec.Kind, spec.OwnerID, spec.AllowNegative))
	if err != nil {
		return Account{}, fmt.Errorf("ledger: create account %q: %w", spec.Code, err)
	}
	return a, nil
}

// Account returns one account, or ErrNotFound.
func (s *Service) Account(ctx context.Context, id uuid.UUID) (Account, error) {
	a, err := scanAccount(s.pool.QueryRow(ctx, `select `+accountCols+` from ledger.accounts where id = $1`, id))
	if errors.Is(err, pgx.ErrNoRows) {
		return Account{}, fmt.Errorf("%w: account %s", ErrNotFound, id)
	}
	if err != nil {
		return Account{}, fmt.Errorf("ledger: load account: %w", err)
	}
	return a, nil
}

// AccountsByOwner returns the owner's accounts, oldest first.
func (s *Service) AccountsByOwner(ctx context.Context, ownerID uuid.UUID) ([]Account, error) {
	rows, err := s.pool.Query(ctx, `select `+accountCols+` from ledger.accounts
		where owner_id = $1 order by created_at, id`, ownerID)
	if err != nil {
		return nil, fmt.Errorf("ledger: list accounts: %w", err)
	}
	accts, err := pgx.CollectRows(rows, func(r pgx.CollectableRow) (Account, error) { return scanAccount(r) })
	if err != nil {
		return nil, fmt.Errorf("ledger: list accounts: %w", err)
	}
	return accts, nil
}

const entryCols = `id, transaction_id, account_id, amount, currency, balance_after, seq, created_at`

func scanEntry(r pgx.CollectableRow) (Entry, error) {
	var e Entry
	err := r.Scan(&e.ID, &e.TransactionID, &e.AccountID, &e.Amount, &e.Currency, &e.BalanceAfter, &e.Seq, &e.CreatedAt)
	return e, err
}

// Entries returns an account's most recent entries, newest first. limit is
// clamped to 1..1000; zero or negative means 50.
func (s *Service) Entries(ctx context.Context, accountID uuid.UUID, limit int) ([]Entry, error) {
	if limit <= 0 {
		limit = 50
	}
	limit = min(limit, 1000)
	rows, err := s.pool.Query(ctx, `select `+entryCols+` from ledger.entries
		where account_id = $1 order by seq desc limit $2`, accountID, limit)
	if err != nil {
		return nil, fmt.Errorf("ledger: list entries: %w", err)
	}
	es, err := pgx.CollectRows(rows, scanEntry)
	if err != nil {
		return nil, fmt.Errorf("ledger: list entries: %w", err)
	}
	return es, nil
}

// Transaction returns one transaction with its entries, or ErrNotFound.
func (s *Service) Transaction(ctx context.Context, id uuid.UUID) (Transaction, error) {
	return getTransaction(ctx, s.pool, id)
}

func getTransaction(ctx context.Context, q querier, id uuid.UUID) (Transaction, error) {
	var t Transaction
	err := q.QueryRow(ctx, `select id, idempotency_key, description, status, effective_date,
		created_at, resolved_at, expires_at, metadata from ledger.transactions where id = $1`, id).
		Scan(&t.ID, &t.IdempotencyKey, &t.Description, &t.Status, &t.EffectiveDate,
			&t.CreatedAt, &t.ResolvedAt, &t.ExpiresAt, &t.Metadata)
	if errors.Is(err, pgx.ErrNoRows) {
		return Transaction{}, fmt.Errorf("%w: transaction %s", ErrNotFound, id)
	}
	if err != nil {
		return Transaction{}, fmt.Errorf("ledger: load transaction: %w", err)
	}
	rows, err := q.Query(ctx, `select `+entryCols+` from ledger.entries
		where transaction_id = $1 order by seq`, id)
	if err != nil {
		return Transaction{}, fmt.Errorf("ledger: load entries: %w", err)
	}
	if t.Entries, err = pgx.CollectRows(rows, scanEntry); err != nil {
		return Transaction{}, fmt.Errorf("ledger: load entries: %w", err)
	}
	return t, nil
}

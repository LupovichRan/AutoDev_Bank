# AutoDev Bank Core Vertical Slice Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** A single Go binary + PostgreSQL 16 that runs an append-only double-entry ledger with hash-chained audit, maker-checker separation of duties, passkey authentication, live zero-credential open-banking data, and a Svelte 5 PWA front end, all testable without Docker.

**Architecture:** One Go module (`cmd/bank`) with focused internal packages (`db`, `ledger`, `audit`, `approvals`, `auth`, `opendata`, `httpapi`) over three Postgres schemas (`ledger`, `control`, `opendata`). Invariants live in SQL (deferrable triggers, unique keys); Go wraps them with a `40001`-retrying transaction helper. The web client is a static Svelte 5 build served by the same binary.

**Tech Stack:** Go 1.26 (auto-downloaded toolchain; `go.mod` says `go 1.26.0`), `github.com/jackc/pgx/v5 v5.12.0`, `github.com/google/uuid v1.6.0` (UUIDv7), `github.com/go-webauthn/webauthn v0.18.2`, PostgreSQL 16, Node 22, Svelte 5.57, Vite 7, `@playwright/test 1.63.0` (matches the preinstalled browsers), GitHub Actions.

**Spec:** `docs/superpowers/specs/2026-11-04-autodev-bank-design.md`

## Global Constraints

- Free open-source dependencies only (Apache/MIT/BSD/MPL/PostgreSQL licences); no BSL/ELv2/SSPL.
- Money is always `BIGINT` minor units with a `char(3)` ISO currency; never float.
- Every ledger write goes through `db.WithTx` (SERIALIZABLE, retry SQLSTATE `40001` up to 5 times).
- IDs are UUIDv7 generated in Go via `uuid.NewV7()`; PG16 has no `uuidv7()`.
- No passwords anywhere. Customer auth is WebAuthn only.
- All HTTP errors are RFC 9457 problem details (`application/problem+json`).
- Integration tests use `DATABASE_URL`; they call `t.Skip` when it is unset so `go test ./...` passes without Postgres.
- The in-house `./dev` script is the only developer entry point; no `just`, `mise`, or Docker required in the sandbox.
- Commit after every task with a conventional-commit message.

## Review Focus

1. Concurrent transfers that both read a balance then post: must serialize (ordered `FOR UPDATE`) and never produce a negative balance on a non-overdraft account. Test added in Task 3 (`TestPost_ConcurrentHotAccount`).
2. Idempotency replay with a *different* payload under the same key: must return a conflict, not silently return the first transaction. Test added in Task 3 (`TestPost_IdempotencyKeyPayloadMismatch`).
3. An approver who holds a session but is also the initiator of the approval: DB trigger must reject even if the HTTP layer is bypassed. Test added in Task 6 (`TestDecide_InitiatorCannotApprove_DBLevel`).
4. Open data upstream returns 200 with an empty or malformed body: refresh must keep the previous snapshot and report the error, never blank the screen. Test added in Task 5 (`TestRefresh_KeepsPreviousSnapshotOnBadBody`).
5. Step-up assertion reused for a second transfer or for a transfer with a different amount: must be rejected (dynamic linking). Test added in Task 7 (`TestStepUp_BoundToPayloadHash`).

---

### Task 1: Repository foundation, dev tooling, migrator

**Files:**
- Create: `go.mod`, `cmd/bank/main.go`, `internal/db/pool.go`, `internal/db/tx.go`, `internal/db/migrate.go`, `migrations/0001_init.sql`, `tools/dev`, `tools/pg.sh`, `.github/workflows/ci.yml`, `README.md`, `.env.example`
- Test: `internal/db/migrate_test.go`, `internal/db/tx_test.go`

**Interfaces:**
- Produces: `db.Connect(ctx, url string) (*pgxpool.Pool, error)`; `db.WithTx(ctx, pool, func(ctx, pgx.Tx) error) error` (SERIALIZABLE, retries `40001` up to 5× with jittered backoff 10–200 ms); `db.Migrate(ctx, pool, fs.FS) (applied []string, err error)` using embedded `migrations/*.sql` and table `public.schema_migrations(version text pk, applied_at)`; `db.TestPool(t) *pgxpool.Pool` which skips when `DATABASE_URL` is unset, runs `Migrate`, and truncates all tables in `ledger`, `control`, `opendata` in `t.Cleanup`.
- `./dev` subcommands: `pg start|stop|reset|status`, `migrate`, `test`, `run`, `web`, `e2e`, `ci`.

- [ ] **Step 1: Write `tools/pg.sh`** — starts a PG16 cluster under `.pgdata/` on port `55432` with `-k /tmp` using `/usr/lib/postgresql/16/bin/{initdb,pg_ctl}` run as the `postgres` user when invoked by root (use `su postgres -c` if `id -u` is 0, else run directly); `reset` stops, deletes, re-inits; prints `DATABASE_URL=postgres://postgres@localhost:55432/bank?sslmode=disable` (create db `bank` on start if missing).

- [ ] **Step 2: Write `tools/dev`** (bash, `set -euo pipefail`) delegating `pg *` to `tools/pg.sh`; `migrate` → `go run ./cmd/bank migrate`; `test` → `go test ./...`; `run` → `go run ./cmd/bank serve`; `web` → `npm --prefix web run dev`; `e2e` → `npm --prefix web run e2e`; `ci` → `go vet ./... && go test -race ./... && npm --prefix web ci && npm --prefix web run build && npm --prefix web run check`. Export `DATABASE_URL` default if unset. `chmod +x tools/dev` and symlink `./dev -> tools/dev`.

- [ ] **Step 3: Write the failing migrator test** in `internal/db/migrate_test.go`:
```go
func TestMigrate_AppliesOnceAndIsIdempotent(t *testing.T) {
    pool := TestPool(t) // skips without DATABASE_URL
    applied, err := Migrate(ctx, pool, migrations.FS)
    require.NoError(t, err); require.Empty(t, applied) // TestPool already migrated
    var n int
    require.NoError(t, pool.QueryRow(ctx, `select count(*) from schema_migrations`).Scan(&n))
    require.GreaterOrEqual(t, n, 1)
}
```
Use `testing` + a tiny `internal/testutil/require.go` (no testify; in-house `require.NoError`, `require.Equal`, `require.True`).

- [ ] **Step 4: Run** `go test ./internal/db/ -run TestMigrate -v` → FAIL (package missing).

- [ ] **Step 5: Implement** `go.mod` (`module github.com/LupovichRan/AutoDev_Bank`, `go 1.26.0`), `migrations/embed.go` (`package migrations; //go:embed *.sql; var FS embed.FS`), `0001_init.sql` (`create schema if not exists ledger; create schema if not exists control; create schema if not exists opendata;`), `pool.go`, `tx.go`, `migrate.go` (advisory lock `pg_advisory_xact_lock(7311)`, sorted filenames, each file in one tx, insert version).

- [ ] **Step 6: Write `tx_test.go`**: `TestWithTx_RetriesOnSerializationFailure` — two goroutines both read then update the same row of a temp table under `WithTx`; assert final value equals both increments applied and no error returned.

- [ ] **Step 7: Run** `./dev pg start && ./dev migrate && go test ./internal/db/ -v` → PASS. Then `DATABASE_URL= go test ./internal/db/` → SKIP (not fail).

- [ ] **Step 8: Write `cmd/bank/main.go`** with subcommands `serve`, `migrate`, `seed`, `opendata-refresh` (later tasks fill `serve`/`seed`/`opendata-refresh`; `migrate` works now). Config from env: `DATABASE_URL`, `BANK_ADDR` (default `:8080`), `BANK_ORIGIN` (default `http://localhost:8080`), `BANK_AUDIT_SIGNING_KEY` (hex Ed25519 seed; generated and logged if empty).

- [ ] **Step 9: Write `.github/workflows/ci.yml`**: ubuntu-latest, `services: postgres:16` with `DATABASE_URL`, setup-go (from go.mod), setup-node 22, run `./dev ci` (make `ci` skip `e2e`; e2e is a separate job that installs `npx playwright install --with-deps chromium` and runs `./dev e2e` against `go run ./cmd/bank serve` in background).

- [ ] **Step 10: README.md** — one-screen quickstart: `./dev pg start`, `./dev migrate`, `./dev run`, `./dev test`; architecture pointer to spec; licence note (Apache-2.0, add `LICENSE`).

- [ ] **Step 11: Commit** `feat: repository foundation, dev tooling, migrator`.

---

### Task 2: Ledger schema and SQL invariants

**Files:**
- Create: `migrations/0002_ledger.sql`
- Test: `internal/ledger/schema_test.go`

**Interfaces:**
- Produces tables in schema `ledger`: `accounts(id uuid pk, code text unique not null, name text not null, currency char(3) not null, kind text not null check (kind in ('asset','liability','equity','income','expense')), owner_id uuid null, allow_negative bool not null default false, balance bigint not null default 0, version bigint not null default 0, created_at timestamptz default now())`; `transactions(id uuid pk, idempotency_key text unique not null, payload_hash bytea not null, description text not null, status text not null check (status in ('pending','posted','voided')), effective_date date not null, created_at timestamptz default now(), resolved_at timestamptz null, expires_at timestamptz null, metadata jsonb not null default '{}')`; `entries(id uuid pk, transaction_id uuid references transactions, account_id uuid references accounts, amount bigint not null check (amount <> 0), currency char(3) not null, balance_after bigint not null, seq bigserial)`; `period_closes(currency char(3) pk, closed_through date not null)`.
- Triggers: `entries_balanced` deferrable initially deferred constraint trigger (per transaction per currency sum = 0, raise `ledger_unbalanced`); `entries_immutable` (raise on UPDATE/DELETE); `transactions_immutable` (only `pending→posted|voided` allowed, `resolved_at` set once; raise `ledger_immutable`); `accounts_balance_check` (raise `ledger_insufficient_funds` when `balance < 0 and not allow_negative`); `period_close_check` on insert into entries (raise `ledger_period_closed` when transaction effective_date <= closed_through).
- View `ledger.available_balances(account_id, balance, pending_debits, available)` where pending_debits = sum of negative entries whose transaction is `pending` and (`expires_at` is null or > now()).

- [ ] **Step 1: Write failing schema tests** (`schema_test.go`, using `db.TestPool`): `TestUnbalancedTransactionRejectedAtCommit` (insert a tx with a single +100 entry; expect commit error containing `ledger_unbalanced`), `TestEntriesAreImmutable`, `TestPostedTransactionCannotChange`, `TestNegativeBalanceRejectedUnlessAllowed`, `TestPeriodCloseRejectsBackdatedEntry`, `TestAvailableBalanceSubtractsPendingDebits`.

- [ ] **Step 2: Run** `go test ./internal/ledger/ -run 'Test(Unbalanced|EntriesAre|Posted|Negative|PeriodClose|Available)' -v` → FAIL (relations missing).

- [ ] **Step 3: Write `migrations/0002_ledger.sql`** implementing the tables, triggers and view above. Balance maintenance: a `BEFORE INSERT` trigger on `entries` updates `accounts.balance += NEW.amount`, `version += 1`, and sets `NEW.balance_after`, then the balance check trigger fires `AFTER` on `accounts`.

- [ ] **Step 4: Run** the tests → PASS.

- [ ] **Step 5: Commit** `feat(ledger): schema with balanced, immutable, period-close invariants`.

---

### Task 3: Ledger Go package

**Files:**
- Create: `internal/ledger/ledger.go`, `internal/ledger/types.go`, `internal/ledger/errors.go`
- Test: `internal/ledger/ledger_test.go`, `internal/ledger/property_test.go`

**Interfaces:**
- Consumes: `db.WithTx`, schema from Task 2.
- Produces:
  - `type Service struct{ pool *pgxpool.Pool }`; `func New(pool) *Service`.
  - `type Line struct{ AccountID uuid.UUID; Amount int64 }` (positive credit to account balance sign convention: amount is added to `balance`).
  - `type PostRequest struct{ IdempotencyKey string; Description string; EffectiveDate time.Time; Lines []Line; Currency string; Pending bool; ExpiresAt *time.Time; Metadata map[string]any }`.
  - `func (s *Service) Post(ctx, PostRequest) (Transaction, error)`; `func (s *Service) PostTx(ctx, pgx.Tx, PostRequest) (Transaction, error)` (same, inside caller's tx; used by approvals).
  - `func (s *Service) Resolve(ctx, txID uuid.UUID, post bool) (Transaction, error)`.
  - `func (s *Service) CreateAccount(ctx, AccountSpec) (Account, error)`; `func (s *Service) Account(ctx, id) (Account, error)`; `func (s *Service) AccountsByOwner(ctx, ownerID) ([]Account, error)`; `func (s *Service) Entries(ctx, accountID, limit int) ([]Entry, error)`; `func (s *Service) ClosePeriod(ctx, currency string, through time.Time) error`.
  - Errors: `ErrUnbalanced`, `ErrInsufficientFunds`, `ErrPeriodClosed`, `ErrIdempotencyConflict`, `ErrAlreadyResolved`, `ErrNotFound`, `ErrCurrencyMismatch` mapped from SQLSTATE/raise messages.
  - `payload_hash` = sha256 of canonical JSON of (Currency, EffectiveDate, Lines sorted by AccountID, Description).

- [ ] **Step 1: Write failing tests**: `TestPost_TransfersBetweenAccounts` (A −500, B +500; balances; two entries with `balance_after`), `TestPost_IdempotentReplayReturnsSameTransaction`, `TestPost_IdempotencyKeyPayloadMismatch` (same key, different amount → `ErrIdempotencyConflict`), `TestPost_RejectsCurrencyMismatch` (account currency ≠ request currency), `TestPost_InsufficientFunds`, `TestHold_ReducesAvailableThenPostOrVoid`, `TestResolve_Twice_ErrAlreadyResolved`, `TestClosePeriod_RejectsBackdated`, `TestPost_ConcurrentHotAccount` (50 goroutines × 10 transfers from one funded account to 5 others under `-race`; final balances sum unchanged; no negative balance).

- [ ] **Step 2: Run** `go test ./internal/ledger/ -run TestPost -v` → FAIL (undefined).

- [ ] **Step 3: Implement** `ledger.go`: `PostTx` does: validate (≥2 lines, sum 0, all accounts same currency), compute hash, `insert into transactions ... on conflict (idempotency_key) do nothing returning ...`; if no row returned, fetch existing and compare `payload_hash` (mismatch → `ErrIdempotencyConflict`; match → return existing); lock accounts `select ... for update` in sorted id order; insert entries; map SQLSTATE `P0001` messages (`ledger_unbalanced` etc.) to typed errors. `Post` wraps `PostTx` in `db.WithTx`.

- [ ] **Step 4: Write `property_test.go`**: `TestProperty_SumPerCurrencyIsZero` — 200 random multi-line transactions across 8 accounts (2 currencies); after each, `select currency, sum(amount) from ledger.entries group by currency` is all zero and `sum(balance)` over accounts equals zero per currency.

- [ ] **Step 5: Run** `go test -race ./internal/ledger/ -v` → PASS.

- [ ] **Step 6: Commit** `feat(ledger): Go service with idempotent posting, holds, period close`.

---

### Task 4: Audit hash chain

**Files:**
- Create: `migrations/0003_audit.sql`, `internal/audit/audit.go`, `internal/audit/canonical.go`
- Test: `internal/audit/audit_test.go`

**Interfaces:**
- Tables: `control.audit_events(seq bigserial pk, at timestamptz default now(), actor_id uuid null, action text not null, subject text not null, payload jsonb not null default '{}', prev_hash bytea not null, hash bytea not null)` append-only (trigger raises `audit_immutable` on UPDATE/DELETE); `control.audit_roots(day date pk, head_seq bigint, head_hash bytea, signature bytea, signed_at timestamptz)`.
- `type Recorder struct{...}`; `func New(pool, signer ed25519.PrivateKey) *Recorder`.
- `func (r *Recorder) Append(ctx, tx pgx.Tx, ev Event) (seq int64, err error)` where `Event{ActorID *uuid.UUID; Action, Subject string; Payload map[string]any}`; computes `hash = sha256(prev_hash || canonicalJSON(seq? no: at, actor, action, subject, payload))`, taking `prev_hash` from the last row under `pg_advisory_xact_lock(7312)` (genesis prev_hash = 32 zero bytes).
- `func (r *Recorder) Verify(ctx) (Report, error)` with `Report{OK bool; Length int64; HeadHash string; FirstBadSeq int64}`.
- `func (r *Recorder) SignDailyRoot(ctx, day time.Time) (Root, error)`; `func (r *Recorder) PublicKey() ed25519.PublicKey`.
- `canonical.JSON(v any) ([]byte, error)`: sorted keys, no whitespace, RFC 8785-style for the subset we produce.

- [ ] **Step 1: Failing tests**: `TestAppend_ChainsHashes`, `TestVerify_DetectsTamperedPayload` (disable trigger via `alter table ... disable trigger` as superuser in test, update a payload, expect `OK=false`, `FirstBadSeq` set), `TestSignDailyRoot_VerifiesWithPublicKey`.
- [ ] **Step 2: Run** → FAIL.
- [ ] **Step 3: Implement** migration + package.
- [ ] **Step 4: Run** `go test ./internal/audit/ -v` → PASS.
- [ ] **Step 5: Commit** `feat(audit): hash-chained append-only audit log with signed daily roots`.

---

### Task 5: Open data providers, cache, refresh, API

**Files:**
- Create: `migrations/0004_opendata.sql`, `internal/opendata/provider.go`, `internal/opendata/cache.go`, `internal/opendata/refresh.go`, `internal/opendata/providers/obuk.go`, `internal/opendata/providers/frankfurter.go`, `internal/opendata/providers/ecb.go`, `internal/opendata/providers/boi.go`, `internal/opendata/providers/boe.go`, `internal/opendata/providers/enablebanking.go`, `internal/opendata/handlers.go`, `internal/opendata/testdata/*.json|xml|csv`
- Test: `internal/opendata/providers/*_test.go`, `internal/opendata/refresh_test.go`, `internal/opendata/handlers_test.go`

**Interfaces:**
- Table `opendata.snapshots(provider text, kind text, fetched_at timestamptz, source_url text, license text, payload jsonb, primary key (provider, kind))`; `opendata.refresh_log(id bigserial, provider, kind, at, ok bool, error text, duration_ms int)`.
- `type Kind string` consts `KindFXRate="fx_rate"`, `KindPolicyRate="policy_rate"`, `KindATM="atm"`, `KindBranch="branch"`, `KindProduct="product"`.
- `type Provider interface{ ID() string; Kinds() []Kind; Fetch(ctx, kind Kind) (Result, error) }` with `Result{Records []Record; SourceURL, License string}`; `type Record struct{ Kind Kind; Key string; Data map[string]any }`.
- Typed Data shapes (documented in `provider.go` comments): fx_rate `{base, quote, rate(float64 string), date, source}`; policy_rate `{bank, name, rate, effective}`; atm/branch `{bank, name, lat, lon, address, postcode, access24h bool}`; product `{bank, brand, name, type, overdraft_ear, monthly_fee, url}`.
- `func NewHTTPProviders(client *http.Client) []Provider` — OBUK providers for NatWest (`https://openapi.natwest.com`), Lloyds (`https://api.lloydsbank.com`), Barclays (`https://atlas.api.barclays`, atms+branches only), HSBC (`https://api.hsbc.com`, header `Accept: application/prs.openbanking.opendata.v2.2+json`), Nationwide (`https://openapi.nationwide.co.uk`, atms+products), paths `/open-banking/v2.2/{atms,branches,personal-current-accounts}`; Frankfurter `https://api.frankfurter.dev/v1/latest?base=EUR`; ECB `https://data-api.ecb.europa.eu/service/data/EXR/D.USD+GBP+ILS.EUR.SP00.A?lastNObservations=1&format=jsondata`; BoI `https://boi.org.il/PublicApi/GetExchangeRates` and `/PublicApi/GetInterest`; BoE `https://www.bankofengland.co.uk/boeapps/database/_iadb-fromshowcolumns.asp?csv.x=yes&Datefrom=01/Jan/2026&Dateto=now&SeriesCodes=IUDBEDR&CSVF=TN&UsingCodes=Y&VPD=Y&VFD=N`.
- `enablebanking.New(appID string, privateKeyPEM []byte, client) (Provider, bool)` returns `false` when either is empty; implements RS256 JWT (`iss=enablebanking.com`, `aud=api.enablebanking.com`, `kid=appID`) and `GET /application` + `GET /sessions`… **only** `KindFXRate`-free kinds: expose `Kind "own_account"` records from `GET /accounts/{uid}/balances` for account uids listed in `ENABLE_BANKING_ACCOUNT_UIDS`; unit-test the JWT signing and parsing against fixtures; no live test.
- `type Refresher struct{...}`; `func NewRefresher(pool, providers []Provider, log *slog.Logger) *Refresher`; `func (r *Refresher) RefreshAll(ctx) []RefreshOutcome`; `func (r *Refresher) Run(ctx, every time.Duration)`; on error or empty `Records` the previous snapshot is kept and `refresh_log.ok=false`.
- `type Store struct{...}` `func (s *Store) Records(ctx, kind Kind, filter Filter) ([]RecordWithMeta, error)` where `RecordWithMeta{Record; Provider string; FetchedAt time.Time; SourceURL, License string}`; `Filter{Bank string; Near *LatLon; Limit int}` (ATM/branch sorted by haversine distance when `Near` set).
- HTTP: `func (h *Handlers) Mount(mux *http.ServeMux)` routes `GET /api/opendata/fx`, `/api/opendata/rates`, `/api/opendata/atms?near=lat,lon&limit=20`, `/api/opendata/branches`, `/api/opendata/products`, `GET /api/opendata/status` (last refresh per provider), `POST /api/opendata/refresh` (staff only; auth middleware injected later via `Handlers.RequireStaff func(http.Handler) http.Handler`, default pass-through).

- [ ] **Step 1: Record fixtures** with curl into `testdata/` (one small trimmed response per provider; HSBC with header; BoE CSV; ECB jsondata; BoI json).
- [ ] **Step 2: Failing parser tests** per provider: e.g. `TestOBUK_ParsesATMs` (first record has `bank`, `lat`, `lon`, `postcode`), `TestFrankfurter_ParsesRates` (GBP, ILS, USD present, `base=EUR`), `TestECB_ParsesSDMX`, `TestBoI_ParsesRatesAndInterest`, `TestBoE_ParsesCSVLastObservation`; use `httptest.Server` serving fixtures.
- [ ] **Step 3: Run** → FAIL. **Step 4: Implement** providers. **Step 5: Run** → PASS.
- [ ] **Step 6: Failing refresh tests**: `TestRefresh_StoresSnapshotAndLog`, `TestRefresh_KeepsPreviousSnapshotOnBadBody` (second run server returns `200` with `{}` → snapshot unchanged, log ok=false), `TestRefresh_KeepsPreviousSnapshotOnHTTP500`.
- [ ] **Step 7: Implement** migration, cache, refresher. Run → PASS.
- [ ] **Step 8: Failing handler tests**: `TestHandlers_FXReturnsRecordsWithMeta` (JSON has `fetched_at`, `source_url`, `license`), `TestHandlers_ATMsNearSortsByDistance`.
- [ ] **Step 9: Implement** handlers; wire `opendata-refresh` subcommand in `cmd/bank`. Run → PASS.
- [ ] **Step 10: Live smoke (opt-in)**: `LIVE=1 go test ./internal/opendata/... -run Live -v` test `TestLive_AllProvidersReturnRecords` skipped unless `LIVE=1`; run it once in the sandbox and record the outcome in the commit message.
- [ ] **Step 11: Commit** `feat(opendata): zero-credential open banking and central-bank providers with cached refresh`.

---

### Task 6: Control plane and maker-checker (separation of duties)

**Files:**
- Create: `migrations/0005_control.sql`, `internal/approvals/policy.go`, `internal/approvals/service.go`, `internal/approvals/principals.go`
- Test: `internal/approvals/policy_test.go`, `internal/approvals/service_test.go`, `internal/approvals/sod_test.go`

**Interfaces:**
- Tables: `control.principals(id uuid pk, kind text check in ('customer','staff','service'), display_name text, handle text unique, created_at)`; `control.roles(principal_id uuid, role text, primary key (principal_id, role))` with trigger `roles_static_sod` raising `sod_conflict` when inserting `payments.approver` for a principal holding `payments.initiator` or vice versa; `control.account_holders(account_id uuid, principal_id uuid, primary key(account_id, principal_id))`; `control.approvals(id uuid pk, kind text, subject jsonb, amount bigint, currency char(3), initiator_id uuid, required_approvals int, not_before timestamptz null, status text check in ('pending','approved','rejected','executed','expired'), created_at, ledger_transaction_id uuid null)`; `control.approval_decisions(approval_id uuid, approver_id uuid, decision text check in ('approve','reject'), reason text, at timestamptz, primary key (approval_id, approver_id))` with trigger `decisions_dynamic_sod` raising `sod_self_approval` when `approver_id = initiator_id` of the approval; `control.decision_log(id bigserial, at, principal_id, action, input jsonb, outcome text, reason text)`.
- `type Tier struct{ Threshold int64; Required int }`; `type Policy struct{ Tiers []Tier; JointRequiresOtherHolder bool }`; `DefaultPolicy()` = tiers `[{100000,1},{1000000,2}]` (i.e. ≥ 1,000.00 → 1 approver, ≥ 10,000.00 → 2) and joint rule on.
- `type Decision struct{ Required int; Reason string }`; `func (p Policy) Evaluate(in Input) Decision` where `Input{Amount int64; Currency string; InitiatorID uuid.UUID; HolderIDs []uuid.UUID; AccountKind string}`.
- `type Service struct{...}` `func New(pool, ledger *ledger.Service, audit *audit.Recorder, policy Policy) *Service`.
- `func (s *Service) SubmitTransfer(ctx, in TransferInput) (Outcome, error)` with `TransferInput{IdempotencyKey string; InitiatorID uuid.UUID; FromAccountID, ToAccountID uuid.UUID; Amount int64; Currency, Description string}`; `Outcome{Posted *ledger.Transaction; Approval *Approval}` — evaluates policy, logs to `decision_log`, either posts via `ledger.PostTx` immediately or creates a pending approval (storing the `TransferInput` in `subject`).
- `func (s *Service) Decide(ctx, approvalID, approverID uuid.UUID, approve bool, reason string) (Approval, error)` — requires approver to be a holder of the from-account (customer) or hold role `payments.approver` (staff); on reaching `required_approvals` executes in the same tx (`approved`→`executed`, ledger post with the stored idempotency key).
- `func (s *Service) Inbox(ctx, principalID) ([]Approval, error)`; `func (s *Service) ListForInitiator(ctx, principalID) ([]Approval, error)`.
- Principals: `CreatePrincipal(ctx, kind, displayName, handle) (Principal, error)`, `GrantRole(ctx, id, role) error` (surfaces `ErrSoDConflict`), `AddHolder(ctx, accountID, principalID) error`.

- [ ] **Step 1: Failing policy tests**: `TestPolicy_BelowTierNoApproval`, `TestPolicy_Tier1OneApprover`, `TestPolicy_Tier2TwoApprovers`, `TestPolicy_JointAccountRequiresOtherHolder` (amount below tier but 2 holders → Required 1).
- [ ] **Step 2: Run** → FAIL. **Step 3: Implement** `policy.go`. **Step 4: Run** → PASS.
- [ ] **Step 5: Failing SoD tests** (`sod_test.go`): `TestGrantRole_InitiatorAndApproverConflict` (expects `ErrSoDConflict`), `TestDecide_InitiatorCannotApprove_DBLevel` (insert decision row directly via SQL with approver=initiator → error contains `sod_self_approval`).
- [ ] **Step 6: Failing service tests**: `TestSubmitTransfer_SmallAmountPostsImmediately`, `TestSubmitTransfer_LargeAmountCreatesApproval` (ledger unchanged; approval pending; decision_log row), `TestDecide_ApproveExecutesLedgerPost` (balance moves; status executed; audit event `approval.executed`), `TestDecide_RejectLeavesLedgerUnchanged`, `TestDecide_SelfApprovalRejected` (service-level `ErrSelfApproval`), `TestDecide_NotBeforeBlocksEarlyExecution`.
- [ ] **Step 7: Implement** migration + `service.go` + `principals.go`. **Step 8: Run** `go test ./internal/approvals/ -v` → PASS.
- [ ] **Step 9: Commit** `feat(approvals): maker-checker workflow with static and dynamic separation of duties`.

---

### Task 7: Passkey authentication and sessions

**Files:**
- Create: `migrations/0006_auth.sql`, `internal/auth/webauthn.go`, `internal/auth/sessions.go`, `internal/auth/stepup.go`, `internal/auth/middleware.go`, `internal/auth/handlers.go`
- Test: `internal/auth/webauthn_test.go`, `internal/auth/sessions_test.go`, `internal/auth/stepup_test.go`, `internal/auth/handlers_test.go`

**Interfaces:**
- Tables: `control.credentials(id bytea pk, principal_id uuid, public_key bytea, attestation_type text, transport text[], flags jsonb, authenticator jsonb, created_at, last_used_at)`; `control.sessions(id text pk, principal_id uuid, created_at, expires_at, user_agent text, stepped_up_at timestamptz null, stepup_payload_hash bytea null)`; `control.webauthn_challenges(id text pk, kind text, principal_id uuid null, session_data jsonb, payload_hash bytea null, expires_at)`.
- `func New(pool, cfg Config) (*Service, error)` with `Config{RPID, RPDisplayName string; Origins []string; SessionTTL time.Duration (default 12h); StepUpTTL time.Duration (default 2m)}`; wraps `webauthn.New(&webauthn.Config{RPID, RPDisplayName, RPOrigins, AuthenticatorSelection: {ResidentKey: required, UserVerification: required}})`.
- `BeginRegistration(ctx, principalID, displayName, handle) (options json.RawMessage, challengeID string, err)`, `FinishRegistration(ctx, challengeID string, r *http.Request) error`; `BeginLogin(ctx) (options, challengeID, err)` (discoverable login), `FinishLogin(ctx, challengeID, r) (Session, error)`; `BeginStepUp(ctx, sess Session, payloadHash []byte)`, `FinishStepUp(ctx, sess, challengeID, r) error` (sets `stepped_up_at`, `stepup_payload_hash`).
- `func (s *Service) RequireStepUp(ctx, sess Session, payloadHash []byte) error` → `ErrStepUpRequired` unless `stepped_up_at` within `StepUpTTL` and hash equal; consumes the step-up (clears it) so one assertion = one transfer.
- Middleware: `func (s *Service) Authenticate(next http.Handler) http.Handler` (reads cookie `bank_session`, loads session into context via `auth.FromContext(ctx) (Session, bool)`); `RequireSession`, `RequireRole(role string)`.
- Handlers mount under `/api/auth/...` as in spec §3.7, plus `GET /api/me` → `{principal_id, display_name, handle, roles, kind}`.
- Cookie: name `bank_session`, HttpOnly, SameSite=Strict, Secure when `BANK_ORIGIN` is https, Path=/.
- Dev helper for tests only: `func (s *Service) TestLogin(ctx, principalID) (Session, error)` guarded by `cfg.AllowTestLogin` (set only in tests and when `BANK_DEV_TEST_LOGIN=1`), exposed as `POST /api/auth/dev-login` only when enabled — used by Playwright setup to skip the ceremony in some tests.

- [ ] **Step 1: Failing tests**: `TestSessions_CreateLoadExpire`, `TestStepUp_BoundToPayloadHash` (step-up with hash A, `RequireStepUp(hash B)` → `ErrStepUpRequired`; `RequireStepUp(hash A)` ok once, second call → `ErrStepUpRequired`), `TestRegistration_RoundTrip` using the library's `protocol` test vectors is impractical — instead test `BeginRegistration` returns options with `residentKey: "required"` and `userVerification: "required"` and persists a challenge; `TestHandlers_MeRequiresSession` (401 problem+json), `TestHandlers_CookieFlags`.
- [ ] **Step 2: Run** → FAIL. **Step 3: Implement**. **Step 4: Run** `go test ./internal/auth/ -v` → PASS.
- [ ] **Step 5: Commit** `feat(auth): WebAuthn passkeys, sessions, step-up bound to payment payload`.

---

### Task 8: HTTP API, server wiring, seed data

**Files:**
- Create: `internal/httpapi/server.go`, `internal/httpapi/problem.go`, `internal/httpapi/accounts.go`, `internal/httpapi/transfers.go`, `internal/httpapi/approvals.go`, `internal/httpapi/transparency.go`, `internal/httpapi/static.go`, `cmd/bank/serve.go`, `cmd/bank/seed.go`
- Test: `internal/httpapi/api_test.go` (uses `httptest` with full wiring and `TestLogin`)

**Interfaces:**
- `httpapi.New(Deps{Pool, Ledger, Audit, Approvals, Auth, OpenData, Static fs.FS, Log}) http.Handler`.
- `problem.Write(w, status int, title, detail string, ext map[string]any)` with `Content-Type: application/problem+json`.
- Routes (all JSON): `GET /api/accounts` (holder's accounts with `balance`, `available`, currency), `GET /api/accounts/{id}/entries?limit=50`, `POST /api/transfers` (header `Idempotency-Key` required → 400 otherwise; body `{from_account_id,to_account_id,amount,currency,description}`; requires step-up with `payloadHash = sha256(canonical body)`; returns `201 {transaction}` or `202 {approval}`; `409` on idempotency conflict; `402`-style `422 insufficient_funds`), `GET /api/approvals` (inbox + mine), `POST /api/approvals/{id}/decide {approve:bool, reason}`, `GET /api/transparency/audit/verify`, `GET /api/transparency/audit/root?day=YYYY-MM-DD`, `GET /api/transparency/fees` (static JSON from `internal/httpapi/fees.json`: zero fees, FX = mid-rate + 0 bp, income-share 50 % — the published fee logic), `GET /healthz`.
- Static: serve `web/dist` (embedded via `web/embed.go` with `//go:embed dist/*` and a build tag fallback that serves a placeholder when dist is absent); SPA fallback to `index.html` for non-`/api` paths.
- `seed` subcommand: creates principals `alice` (customer), `bob` (customer), `carol` (staff, role `payments.approver`), `dave` (staff, role `payments.initiator`); accounts: `alice` ILS current (balance 25,000.00), `alice` EUR savings (1,200.00), joint ILS account held by `alice`+`bob` (8,000.00), `bob` ILS current (3,500.00), bank liability/equity accounts; funded via balanced ledger postings from an equity account `bank.equity.ILS` etc. (`allow_negative=true` on equity). Idempotent (keys `seed:*`).

- [ ] **Step 1: Failing API tests**: `TestTransfers_RequiresIdempotencyKey`, `TestTransfers_RequiresStepUp` (403 `step_up_required`), `TestTransfers_SmallPosts201`, `TestTransfers_LargeReturns202AndApproverCanDecide`, `TestTransfers_InitiatorCannotDecide403`, `TestTransparency_AuditVerifyOK`, `TestStatic_SPAFallback`.
- [ ] **Step 2: Run** → FAIL. **Step 3: Implement** server + seed + `serve.go` (starts refresher ticker every 6h, serves on `BANK_ADDR`, graceful shutdown). **Step 4: Run** `go test ./... -race` → PASS.
- [ ] **Step 5: Manual smoke**: `./dev pg reset && ./dev migrate && go run ./cmd/bank seed && go run ./cmd/bank opendata-refresh && (go run ./cmd/bank serve &) && curl -s localhost:8080/api/opendata/fx | head -c 300` shows live rates with `fetched_at`.
- [ ] **Step 6: Commit** `feat(api): HTTP API, static serving, seed data`.

---

### Task 9: Svelte 5 PWA web client

**Files:**
- Create: `web/package.json`, `web/vite.config.ts`, `web/tsconfig.json`, `web/index.html`, `web/public/manifest.webmanifest`, `web/public/icons/*.svg`, `web/src/main.ts`, `web/src/App.svelte`, `web/src/lib/api.ts`, `web/src/lib/webauthn.ts`, `web/src/lib/money.ts`, `web/src/lib/router.ts` (hash router, in-house), `web/src/styles/tokens.css`, `web/src/styles/base.css`, `web/src/routes/SignIn.svelte`, `Home.svelte`, `Pay.svelte`, `Approvals.svelte`, `OpenFinance.svelte`, `Transparency.svelte`, `web/src/components/{Nav,Card,Amount,Problem,LiveBadge}.svelte`, `web/src/sw.ts` (service worker: cache-first for static, network-first with cache fallback for `GET /api/accounts*` and `/api/opendata/*`, never caches POST), `web/embed.go`
- Test: `web/src/lib/money.test.ts`, `web/src/lib/router.test.ts` (vitest)

**Interfaces:**
- Consumes the API from Task 8 and auth endpoints from Task 7.
- `api.ts`: `apiGet<T>(path)`, `apiPost<T>(path, body, headers?)`; throws `ProblemError{status,title,detail}` parsed from problem+json; sends `credentials: 'include'`.
- `webauthn.ts`: `register(handle, displayName)`, `login()`, `stepUp(payloadHash)` using `navigator.credentials` with base64url helpers (`PublicKeyCredential.parseCreationOptionsFromJSON` when available, manual fallback).
- `money.ts`: `formatMinor(amount: bigint|number, currency: string, locale?)` using `Intl.NumberFormat`; `parseToMinor(input: string, currency): number` (2 decimals for ILS/EUR/GBP/USD, throws on >2 decimals).
- `router.ts`: `route` store from `location.hash`, `navigate(path)`.
- Screens per spec §3.8; Open Finance shows: FX table (EUR base + BoI ILS), policy rates (ECB/BoI/BoE), UK current-account comparison table (bank, product, overdraft EAR, fee) with `LiveBadge` showing `fetched_at` and source link + licence; ATM/branch list near a user-entered or geolocated point. Transparency shows audit verify result (length, head hash) and fee logic.
- Accessibility: all interactive elements are native buttons/inputs/links, visible focus, `aria-live` for status, min 24×24 targets, colour contrast ≥ 4.5:1 in tokens; dark mode via `prefers-color-scheme`.
- Dev: `vite` proxy `/api` → `http://localhost:8080`. Scripts: `dev`, `build`, `check` (`svelte-check`), `test` (vitest), `e2e` (playwright).

- [ ] **Step 1: Scaffold** `npm create vite@latest` equivalent by hand (no interactive): package.json deps `svelte@5.57.3`, `vite@7.3.1`, `@sveltejs/vite-plugin-svelte@7.0.2`, `typescript`, `svelte-check`, `vitest`, `@playwright/test@1.63.0`. `npm install`.
- [ ] **Step 2: Failing unit tests** `money.test.ts` (`formatMinor(123456,'ILS')` contains `1,234.56`; `parseToMinor('12.5','EUR')===1250`; `parseToMinor('1.234','EUR')` throws), `router.test.ts`.
- [ ] **Step 3: Implement** libs. Run `npm test` → PASS.
- [ ] **Step 4: Implement** screens and styles; `npm run build && npm run check` → clean.
- [ ] **Step 5: `web/embed.go`** (`package web; //go:embed all:dist; var Dist embed.FS`) plus `web/dist/.gitkeep`-free approach: commit a `web/dist/index.html` placeholder? No — add build step to `./dev run` (`npm --prefix web run build` before `go run`), and in CI build web before `go test`. Document.
- [ ] **Step 6: Commit** `feat(web): Svelte 5 PWA with passkey sign-in, payments, approvals, open finance, transparency`.

---

### Task 10: Playwright end-to-end

**Files:**
- Create: `web/playwright.config.ts`, `web/e2e/helpers.ts`, `web/e2e/passkey.spec.ts`, `web/e2e/payments.spec.ts`, `web/e2e/openfinance.spec.ts`, `web/e2e/transparency.spec.ts`, `tools/e2e.sh`

**Interfaces:**
- `tools/e2e.sh`: `./dev pg start`, reset db, migrate, seed, `opendata-refresh` (falls back to fixtures via `BANK_OPENDATA_FIXTURES=1` which makes providers read `internal/opendata/testdata` when upstream is unreachable), start `bank serve` with `BANK_DEV_TEST_LOGIN=1` on `:8080`, run `npx playwright test`, stop server.
- `helpers.ts`: `enableVirtualAuthenticator(page)` via CDP `WebAuthn.enable` + `WebAuthn.addVirtualAuthenticator({protocol:'ctap2', transport:'internal', hasResidentKey:true, hasUserVerification:true, isUserVerified:true})`; `devLogin(page, handle)`.

- [ ] **Step 1: Write specs**: `passkey.spec.ts` (register new user `erin` with virtual authenticator → lands on Home; sign out; sign in with passkey → Home), `payments.spec.ts` (dev-login alice; pay 50.00 ILS to bob → step-up prompt handled by virtual authenticator → entry appears; pay 2,000.00 from joint account → "Awaiting approval"; dev-login bob → Approvals shows it, approve → alice's Home shows posted; bob cannot see own-initiated approval as approvable), `openfinance.spec.ts` (table rows > 0 for FX, products, ATMs; LiveBadge shows a date), `transparency.spec.ts` (audit verify shows OK).
- [ ] **Step 2: Run** `./dev e2e` → PASS (record run output in commit message).
- [ ] **Step 3: Commit** `test(e2e): Playwright flows for passkeys, payments with maker-checker, open finance`.

---

### Task 11: CI green, docs, ADRs

**Files:**
- Modify: `.github/workflows/ci.yml`, `README.md`
- Create: `docs/adr/0001-postgres-ledger-over-tigerbeetle.md`, `0002-in-house-passkey-auth.md`, `0003-web-core-capacitor-shell.md`, `0004-open-data-tier0-providers.md`, `docs/OPERATIONS.md` (env vars, refresh, audit root verification, Enable Banking setup)

- [ ] **Step 1:** Ensure `./dev ci` passes locally end to end (`go vet`, `go test -race ./...`, web build/check/test, e2e).
- [ ] **Step 2:** Write ADRs (≤ 1 page each: context, decision, consequences, swap path).
- [ ] **Step 3:** Commit `docs: ADRs, operations guide, CI` and push.

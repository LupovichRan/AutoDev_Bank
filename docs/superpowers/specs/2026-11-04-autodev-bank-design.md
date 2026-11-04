# AutoDev Bank — Design Spec

Date: 2026-11-04
Status: Approved by directive (goal mode); assumptions listed in §1.3
Research: `docs/research/reports/Open source bank platform research.md` (7 notes, ~150 citations)

## 1. Intent

### 1.1 What we are building
An open-source, web + mobile digital bank platform ("bank OS") whose chief
virtues are a provably correct ledger, controls-as-code (separation of duties,
audit chain, compliance gates), and radical transparency, built only on free
open-source infrastructure with small in-house tooling, following KISS.

The directive adds a second outcome: connect to open banking APIs and show
**real production data** in the app.

### 1.2 Success criteria
1. A single Go binary + PostgreSQL runs the bank locally with one command
   (no Docker needed), with tests that prove ledger invariants.
2. Every monetary action is double-entry, idempotent, append-only and
   hash-chained; corrections are reversals.
3. Separation of duties is enforced in the database and the policy layer:
   the approver is never the initiator; initiator and approver staff roles
   cannot coexist on one identity; four-eyes above an amount tier.
4. Passkey-first login (WebAuthn), no passwords, session cookies, step-up
   for payments.
5. Live production data from zero-credential open banking / open finance
   endpoints is visible in the web app (UK bank products, ATMs/branches, FX
   and policy rates from ECB, Bank of Israel, Bank of England).
6. The web client is a PWA that works on phones, is accessible (WCAG 2.2 AA
   targets), and is wrappable by Capacitor later without change.

### 1.3 Assumptions made in place of user confirmation
- Jurisdiction-neutral core; EU/UK/Israel examples. Currency-agnostic ledger
  with ILS, EUR, GBP, USD seeded.
- "Real production data" means live public production endpoints (Tier 0 in
  the research) plus an optional Enable Banking adapter for the developer's
  own accounts when credentials are supplied. No licensed TPP access is
  assumed.
- Backend language Go 1.24 (sandbox floor), client Svelte 5 + Vite, Node 22.
- Capacitor mobile shell, Keycloak staff SSO, OpenBao, Compose/k3s are
  later phases (need Docker or native toolchains not present in the sandbox).

## 2. Approaches considered

| Option | Verdict | Why |
|---|---|---|
| A. Adopt Apache Fineract | Rejected | Java monolith, microfinance design centre, no production deployment guide; not KISS. |
| B. TigerBeetle data plane + Postgres control plane | Deferred (documented swap) | Best correctness, but 6-replica ops bill, no SQL/auth; unjustified at new-bank volumes. |
| C. In-house Postgres ledger (pgledger pattern) + Go monolith | **Chosen** | Invariants enforceable in SQL; ~10k tx/s on a laptop; fits sandbox (PG16, Go 1.24, no Docker); all in-house. |
| Auth: Keycloak for customers | Rejected for v1 | 1–2 GiB JVM, container-first; customer channel needs only WebAuthn + sessions. Keep for staff SSO / FAPI AS later. |
| Client: PWA only | Rejected | No attestation / device-bound keys / store presence. Web core + Capacitor shell instead. |
| Client: React Native / Flutter / KMP | Rejected | Heavy or no web reuse; single-codebase web core is the only option a small team sustains. |
| Queue: NATS/Kafka | Deferred | Postgres-as-queue (outbox + jobs table) gives exactly-once with the ledger write. |

## 3. Architecture

```
                 ┌──────────────── web (Svelte 5 PWA) ────────────────┐
                 │ Home · Pay · Approvals · Open Finance · Transparency │
                 └──────────────┬───────────────────────────────────────┘
                                │ HTTPS JSON, session cookie
┌───────────────────────────────▼──────────────────────────────────────┐
│ cmd/bank (one Go binary)                                             │
│  httpapi ─ auth(webauthn, sessions, step-up)                         │
│         ─ ledger(accounts, transfers, holds)  ─ audit(hash chain)    │
│         ─ approvals(maker-checker, policy, decision log)             │
│         ─ opendata(providers: OBUK v2.2, ECB, BoI, BoE, Frankfurter, │
│                    Enable Banking[optional]) ─ cache + scheduler     │
│  migrate ─ embedded SQL migrations (in-house)                        │
└───────────────────────────────┬──────────────────────────────────────┘
                                │ pgx
                 ┌──────────────▼───────────────┐
                 │ PostgreSQL 16: schemas        │
                 │ ledger · control · opendata   │
                 └──────────────────────────────┘
```

### 3.1 Repository layout (monorepo)
```
cmd/bank/            main: serve | migrate | opendata-refresh | seed
internal/ledger/     accounts, transfers, holds, balances, period close
internal/audit/      hash-chained audit events, verify, daily root
internal/auth/       webauthn, sessions, principals, step-up
internal/approvals/  maker-checker workflow, policy, decision log
internal/opendata/   provider interface + providers/ + cache + refresh
internal/httpapi/    routes, middleware, JSON helpers, problem details
internal/db/         pool, tx helper with 40001 retry, migrator
migrations/          NNNN_name.sql (embedded)
web/                 Svelte 5 + Vite PWA (TypeScript)
tools/               in-house dev tooling: ./dev script, pg helpers
docs/                research, specs, plans, ADRs
.github/workflows/   CI (go test, vet, web build, playwright)
```

### 3.2 Ledger (schema `ledger`)
- `accounts(id uuid, code text unique, currency char(3), kind enum
  {asset,liability,equity,income,expense}, owner_id uuid null,
  allow_negative bool default false, version bigint, balance bigint,
  created_at)`; balance in **minor units (BIGINT)**.
- `transactions(id uuid, idempotency_key text unique, description,
  status enum {posted,pending,voided}, effective_date date, posted_at,
  expires_at timestamptz null, metadata jsonb)`.
- `entries(id, transaction_id, account_id, amount bigint signed,
  currency, balance_after bigint, created_at)`.
- Invariants enforced in SQL: deferrable constraint trigger
  `entries_balanced` (sum per transaction per currency = 0 at commit);
  `accounts.balance >= 0` unless `allow_negative`; entries immutable
  (UPDATE/DELETE blocked by trigger); transactions immutable once posted
  except `pending -> posted|voided` exactly once.
- Concurrency: Go `ledger.Post()` locks accounts `FOR UPDATE` in sorted id
  order inside one tx; SERIALIZABLE with retry on SQLSTATE 40001; duplicate
  idempotency key returns the original transaction.
- Holds: `pending` transaction with `expires_at`; `Resolve(post|void)`;
  pending debits reduce *available* balance (computed view).
- Period close: `ledger.period_closes(currency, closed_through date)`;
  posting with effective_date <= closed_through is rejected.
- IDs are UUIDv7 generated in Go (PG16 has no uuidv7()).

### 3.3 Audit chain (schema `control`)
- `audit_events(seq bigserial, at, actor_id, action, subject, payload jsonb,
  prev_hash bytea, hash bytea)`; `hash = sha256(prev_hash || canonical json)`.
- Append-only via triggers; `audit.Verify()` walks the chain; `GET
  /api/transparency/audit/verify` returns {ok, length, head}.
- Daily root: `audit_roots(day, head_hash, signature)` signed with an
  Ed25519 key from env (OpenBao Transit later).

### 3.4 Separation of duties (maker-checker)
- `control.principals(id, kind {customer,staff,service}, display_name)`,
  `control.roles(principal_id, role)` with trigger enforcing **static SoD**:
  `payments.initiator` and `payments.approver` cannot both be held.
- `control.approvals(id, kind, subject jsonb, amount bigint, currency,
  initiator_id, required_approvals int, not_before timestamptz, status
  {pending,approved,rejected,executed,expired}, created_at)`;
  `control.approval_decisions(approval_id, approver_id, decision, reason,
  at)` with `CHECK`-equivalent trigger: approver_id <> initiator_id and
  one decision per approver.
- Policy (in code, `approvals.Policy`): tiers by amount
  (e.g. < 1,000 minor-units×100 none; ≥ tier1 → 1 approver; ≥ tier2 → 2),
  joint accounts → another holder must approve, business accounts → role
  approver. Every evaluation is written to `control.decision_log`.
- Execution: `approvals.Execute()` posts the ledger transaction in the same
  DB tx as the status change (`approved -> executed`).

### 3.5 Authentication
- WebAuthn via `github.com/go-webauthn/webauthn` (BSD-3). Registration
  (resident key preferred, user verification required), login, and
  **step-up** (fresh assertion ≤ 2 minutes old) required for `POST
  /api/transfers`. Challenge binds the transfer payload hash (dynamic
  linking, EBA Opinion 2019 pattern).
- Sessions: opaque 256-bit id, HttpOnly Secure SameSite=Strict cookie,
  stored in `control.sessions` with expiry and device info.
- No passwords. Recovery: second authenticator; staff recovery via
  approver-mediated reset (maker-checker).
- Rate limits and audit events on every auth action.

### 3.6 Open data / open banking connectors (schema `opendata`)
- `opendata.Provider` interface: `ID()`, `Fetch(ctx) ([]Record, error)`;
  records are typed (`fx_rate`, `policy_rate`, `atm`, `branch`,
  `product`).
- Providers (Tier 0, zero credentials, live-verified 2026-11-04):
  - OBUK Open Data v2.2: NatWest, Lloyds, Barclays (atms, branches), HSBC
    (requires `Accept: application/prs.openbanking.opendata.v2.2+json`),
    Nationwide; endpoints `/open-banking/v2.2/{atms,branches,
    personal-current-accounts}`.
  - FX: Frankfurter `api.frankfurter.dev/v1/latest`, ECB SDMX
    `data-api.ecb.europa.eu/service/data/EXR/...`.
  - Bank of Israel `boi.org.il/PublicApi/GetExchangeRates`, `/GetInterest`.
  - Bank of England IADB CSV (Bank Rate series IUDBEDR).
- Optional (free signup, env-configured): Enable Banking restricted
  production (own accounts) — adapter behind `ENABLE_BANKING_APP_ID` and
  `ENABLE_BANKING_PRIVATE_KEY`; disabled when unset.
- Cache: `opendata.snapshots(provider, kind, fetched_at, payload jsonb)`;
  refresh via `bank opendata-refresh` and an in-process ticker (default
  6h, Barclays `max-age=28800`). API serves from cache with `fetched_at`
  so screens never block on upstreams; a `source` and `license` field is
  returned per record (OBL Open Licence, ECB, BoI terms).

### 3.7 HTTP API (JSON, `/api/...`)
- `POST /api/auth/register/{begin,finish}`, `/api/auth/login/{begin,finish}`,
  `POST /api/auth/stepup/{begin,finish}`, `POST /api/auth/logout`, `GET /api/me`.
- `GET /api/accounts`, `GET /api/accounts/{id}/entries`.
- `POST /api/transfers` (idempotency-key header required) → either posts
  immediately or returns `202 {approval_id}`.
- `GET /api/approvals`, `POST /api/approvals/{id}/decide`.
- `GET /api/opendata/fx`, `/rates`, `/atms?near=lat,lon`, `/branches`,
  `/products`; `POST /api/opendata/refresh` (staff).
- `GET /api/transparency/audit/verify`, `GET /api/transparency/fees`.
- Errors: RFC 9457 problem details. All handlers emit audit events.

### 3.8 Web client (`web/`)
- Svelte 5 + Vite + TypeScript, PWA manifest + service worker (offline
  readable cache of last known balances and open data; online for money
  movement). No UI framework; in-house tokens (`tokens.css`), native
  semantics, keyboard and screen-reader tested.
- Screens: Sign in (passkey), Home (balances, entries), Pay (transfer with
  step-up; shows approval status), Approvals (inbox; cannot approve own),
  Open Finance (live FX & policy rates, UK current-account comparison,
  nearest ATMs/branches), Transparency (audit verify, fee logic).
- E2E: Playwright with CDP virtual authenticator for passkeys.

### 3.9 In-house tooling (`tools/`)
- `./dev` bash script: `pg start|stop|reset` (local PG16 cluster under
  `.pgdata/`, no Docker), `migrate`, `test`, `run`, `web`, `e2e`, `ci`.
- Go migrator in `internal/db` with `migrations/*.sql` embedded; applied
  in a tx with `schema_migrations` table.
- CI: GitHub Actions (free for public repos) runs `./dev ci`.

### 3.10 Error handling and observability
- All DB writes in `db.WithTx` (retry 40001 up to 5×, jittered backoff).
- Problem-details errors; never leak internals. Structured logs (slog JSON)
  with request id; OpenTelemetry wiring deferred to staging phase.

### 3.11 Testing strategy
- Ledger: table-driven + property tests (random transfers keep
  per-currency sum zero, idempotency replay, concurrent transfers on hot
  accounts under `-race`, hold expiry, period close).
- SoD: DB-level tests that the triggers reject approver==initiator and
  mixed roles; policy unit tests; decision log asserted.
- Audit: chain verify passes; tampering a row makes verify fail.
- Open data: provider parsers tested against recorded fixtures; one
  opt-in live test (`LIVE=1`) hitting the real endpoints.
- Auth: WebAuthn handshake tests using the library's test helpers;
  Playwright e2e with virtual authenticator.

## 4. Sub-project decomposition and order

| # | Sub-project | Outcome |
|---|---|---|
| 1 | Foundation + ledger + audit | Repo, tooling, migrations, CI; ledger invariants proven |
| 2 | Open data connectors + Open Finance screens | Real production data on screen (goal 2) |
| 3 | Control plane + maker-checker (SoD) | Approvals workflow, static and dynamic SoD |
| 4 | Passkey auth + web client + e2e | Full customer flow: sign in, see balances, pay, approve |
| 5 | Payments orchestration (jobs, outbox, VoP hook, Enable Banking) | Exactly-once effects; own-account aggregation |
| 6 | Compliance-as-code, staging platform, Capacitor shell | Later phases (need Docker / native toolchains) |

This session implements 1–4 end to end and lays stubs for 5.

## 5. Out of scope (for now)
Card issuing, lending products, KYC document verification, real payment
rails, Keycloak, OpenBao, Kubernetes, native mobile builds.

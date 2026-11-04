# Operations guide

AutoDev Bank is one Go binary (`cmd/bank`) and one PostgreSQL 16 database. There is no Docker in the development loop.

## Local run

All commands go through `./dev`.

| Command | What it does |
|---|---|
| `./dev demo` | See the product: `pg start`, `migrate`, `seed`, `refresh`, build the web app, then `serve` on http://localhost:8080 with `BANK_DEV_TEST_LOGIN=1` and `BANK_ORIGIN=http://localhost:8080`. The Sign-in screen then offers "Demo sign-in (dev only)" buttons for alice, bob, carol and dave. Local use only. |
| `./dev pg start` | Start a local PostgreSQL 16 under `.pgdata` on port 55432. Also `stop`, `reset`, `status`. |
| `./dev migrate` | Apply `migrations/*.sql` in order. |
| `./dev seed` | Load demo principals and accounts. Refuses unless the host of `BANK_ORIGIN` is localhost, `*.localhost` or a loopback IP; `./dev seed --force` overrides. Each run is audited as `seed.run`. |
| `./dev refresh` | Fetch live open data into the cache once. |
| `./dev test` | `go test ./...`. Integration tests need `DATABASE_URL`. |
| `./dev run` | Build the web app (if npm is present) and serve on `$BANK_ADDR`. |
| `./dev web` | Vite dev server with an `/api` proxy. |
| `./dev e2e` | Playwright suite (`tools/e2e.sh`). It recreates database `bank_e2e` (from `DATABASE_URL`, or `E2E_DATABASE_URL`), builds web and server, seeds, and serves on `E2E_PORT` (default 8080). |
| `./dev ci` | `go vet`, `go test -race`, web build, check and test. |

First run, to look around: `./dev demo`, then open http://localhost:8080 and press a Demo sign-in button. Without demo sign-in (passkeys only):

```
./dev pg start
./dev migrate
./dev seed
./dev run
```

The binary has four subcommands: `serve`, `migrate`, `seed [--force]`, `opendata-refresh`. `serve` applies pending migrations at startup, refreshes open data once and then every six hours, stores yesterday's audit root at startup if it is missing (checking again hourly; a stored root is never overwritten), runs the expiry sweeps at startup and then hourly (pending approvals past `expires_at` become `expired`, ledger holds past `expires_at` are voided), and shuts down gracefully on SIGINT/SIGTERM. `seed` is idempotent: it creates (or finds) the demo principals `alice`, `bob` (customers), `carol` (staff: `payments.approver`, `staff.ops`) and `dave` (staff: `payments.initiator`), their ILS/EUR and joint accounts with opening balances funded from `bank.equity.*`, and prints a table of ids and balances.

## Environment variables

Read once at startup in `cmd/bank/main.go`, plus the Enable Banking variables read by the provider. Nothing loads `.env` automatically: export the variables in your shell, for example `set -a; . ./.env; set +a`.

| Variable | Meaning | Default |
|---|---|---|
| `DATABASE_URL` | PostgreSQL connection string. | `postgres://postgres@localhost:55432/bank?sslmode=disable` |
| `BANK_ADDR` | HTTP listen address. | `:8080` |
| `BANK_ORIGIN` | Public origin. Used as the WebAuthn relying-party origin, so it must match the browser URL. | `http://localhost:8080` |
| `BANK_AUDIT_SIGNING_KEY` | Hex-encoded 32-byte Ed25519 seed that signs daily audit roots. Required unless the host of `BANK_ORIGIN` is `localhost`, `*.localhost` or a loopback IP; then an ephemeral key is generated (only its public key and fingerprint are logged) and is lost on restart. | empty |
| `BANK_DEV_TEST_LOGIN` | `1` enables `POST /api/auth/dev-login` and `POST /api/auth/dev-stepup`, and makes `GET /api/config` report `dev_test_login: true` so the Sign-in screen shows demo buttons. Tests and `./dev demo` only. See Security notes. | off |
| `BANK_OPENDATA_FIXTURES` | `1` serves recorded fixtures instead of live upstreams. | off |
| `ENABLE_BANKING_APP_ID` | Enable Banking application id. | empty (provider off) |
| `ENABLE_BANKING_PRIVATE_KEY` | RS256 private key in PEM form. | empty |
| `ENABLE_BANKING_PRIVATE_KEY_FILE` | Path to the PEM file. Used only when the variable above is empty. | empty |
| `ENABLE_BANKING_ACCOUNT_UIDS` | Comma-separated account uids whitelisted in the Enable Banking portal. | empty |
| `HTTPS_PROXY` | Honoured by the open-data HTTP client. | unset |

`.env.example` lists `DATABASE_URL`, `BANK_ADDR`, `BANK_ORIGIN`, `BANK_AUDIT_SIGNING_KEY` and the Enable Banking entries (`ENABLE_BANKING_APP_ID`, `ENABLE_BANKING_PRIVATE_KEY`, `ENABLE_BANKING_PRIVATE_KEY_FILE`, `ENABLE_BANKING_ACCOUNT_UIDS`), with the same names as the table above.

`tools/pg.sh` (behind `./dev pg`) finds the PostgreSQL 16 server binaries in `PGBIN` (default `/usr/lib/postgresql/16/bin`) and keeps the cluster in `PGDATA` (default `.pgdata`). On macOS with Homebrew: `PGBIN="$(brew --prefix postgresql@16)/bin" ./dev pg start`. `PGPORT` (55432), `PGSOCK` (`/tmp`) and `PGDATABASE` (`bank`) can be overridden the same way. Only when run as root does the script run the server as the `postgres` user and make the parents of `PGDATA` traversable (`chmod o+x`); otherwise it changes no permissions.

## Databases per test package

`go test ./...` runs packages in parallel. `db.TestPool` takes the database name from `DATABASE_URL` and appends the package name, for example `bank_t_ledger`. It creates that database on demand, applies all migrations, and truncates every table in the `ledger`, `control` and `opendata` schemas when the test ends. If `DATABASE_URL` is unset, integration tests are skipped. Drop the `bank_t_*` databases to start clean.

## Migrations policy

- Files in `migrations/` are numbered and append-only: `NNNN_name.sql`, applied in order and recorded in `schema_migrations` with the sha256 of the file.
- Every run checks the recorded checksums first. If an applied file changed, the run fails with "migration X changed after being applied; recreate the database or restore the file" and applies nothing. Rows recorded before checksums existed are stamped with the current file's checksum once.
- Before the first release, a migration may be edited in place; then recreate the databases that applied it (`./dev pg reset`, and drop the `<db>_t_*` test databases). After the first release, never edit an applied file. Add a new numbered file.
- Constraints and triggers belong in migrations, not in application code.

## Controls enforced in the schema

The Go services check these too; the triggers are the backstop for raw SQL.

- Ledger (0002): entries are immutable and balance per currency; entries can only be added in the database transaction that created their ledger transaction (`created_at` is forced to `now()`; a posted transaction is resolved at creation), so a committed transaction never takes more entries. A hold may become posted only if its effective date is after the currency's `closed_through` (voiding stays allowed). Accounts: only `name` and `owner_id` may change directly; `code`, `currency`, `kind`, `allow_negative` are fixed, and balances and `version` change only through entries.
- Control (0005): nobody holds both `payments.initiator` and `payments.approver`, and customers hold no `payments.*` role. Decisions come only from a holder of the from-account or a `payments.approver`, never the initiator, and only on a pending, unexpired approval. `pending -> approved` needs `required_approvals` approve decisions and, for joint accounts, one by another holder; `approved -> executed` needs the approval's own posting (its idempotency key, posted, from/to and amount). `expires_at` (created + 7 days) may be brought forward, never extended.

## Payments policy

- A customer debits only accounts they hold. Staff with `payments.initiator` may debit any account but always need at least one approval. Any debit from an `allow_negative` or holder-less account (e.g. `bank.equity.*`) always needs at least one approval. The amount tiers (>= 1,000.00: one approver, >= 10,000.00: two) and the joint-account rule add approvers on top.
- `POST /api/transfers` answers 422 `invalid_destination` for an unknown, other-currency or internal (holder-less) destination alike, and 403 for an unknown or foreign source. A 201 shows only the caller's own entries.
- Approving (`POST /api/approvals/{id}/decide` with `approve: true`) needs a fresh passkey step-up bound to `sha256("approve:" + id)`; rejecting does not. The initiator may withdraw a pending approval with `POST /api/approvals/{id}/cancel` (status `rejected`, decision-log reason "cancelled by initiator", audit `approval.cancelled`). Pending approvals expire after 7 days (audit `approval.expired`).

## Open-data refresh

- One-off: `./dev refresh` (same as `go run ./cmd/bank opendata-refresh`). It fetches every provider and stores the last good snapshot per provider and kind. A failed provider keeps its previous snapshot. The command exits non-zero only if nothing at all was stored.
- Ticker: the refresher runs once at start and then every six hours while `serve` is up.
- Fixture fallback: recorded fixtures are used only when a live fetch fails and nothing is cached yet (cold start). Set `BANK_OPENDATA_FIXTURES=1` to force fixtures, for offline work and tests.
- Provenance: every record carries `fetched_at`, `source_url`, `license` and `license_url`. `license` is our short factual attribution; `license_url` is the publisher's own terms page, which is what governs reuse. The web app shows the source, fetch time, licence and a Terms link next to the data. Keep that attribution when you add a provider.
- Sources and their terms pages:

| Provider | Terms (`license_url`) |
|---|---|
| OBIE Open Data v2.2 (UK banks) | https://www.openbanking.org.uk/open-licence |
| Frankfurter | https://frankfurter.dev/ |
| ECB Data Portal | https://www.ecb.europa.eu/services/using-our-site/disclaimer/html/index.en.html |
| Bank of Israel PublicApi | https://www.boi.org.il/en/ (site root; link the exact terms-of-use page when confirmed) |
| Bank of England IADB | https://www.bankofengland.co.uk/legal |
| Enable Banking (own accounts) | https://enablebanking.com/ |

## Audit root verification

The chain is in `control.audit_events`. Each row's `hash` is `sha256(prev_hash || canonicalJSON({at, actor_id, action, subject, payload}))`, and the genesis `prev_hash` is 32 zero bytes. Daily roots are in `control.audit_roots` (`day`, `head_seq`, `head_hash`, `signature`). Only finished UTC days are signed; `audit.Recorder.ComputeRoot` and `SignDailyRoot` refuse today with `ErrDayNotFinished`, and `HeadAsOf` gives the unsigned head.

Verify the chain: call `audit.Recorder.Verify(ctx)`. It re-walks every row and returns a report with `OK`, `Length`, `HeadHash` and `FirstBadSeq` (0 when the chain is intact).

Verify a daily root with the public key:

1. Derive the public key from the signing seed (once, keep it with your records):
   ```
   (printf '\x30\x2e\x02\x01\x00\x30\x05\x06\x03\x2b\x65\x70\x04\x22\x04\x20'; echo -n "$BANK_AUDIT_SIGNING_KEY" | xxd -r -p) \
     | openssl pkey -inform DER -pubout -outform PEM > audit-root.pub
   ```
2. Read the root: `select day, head_seq, encode(head_hash,'hex'), encode(signature,'hex') from control.audit_roots where day = '2026-11-04';`
3. The signed message is the canonical JSON `{"day":"<day>","head_hash":"<hex>","head_seq":<n>}` (keys sorted, no spaces). Check the signature with `audit.VerifyRoot(pub, root)` or any Ed25519 verifier.
4. Check that the root's `head_hash` equals the `hash` of row `head_seq` in `audit_events`.

Publish each root outside the database (object-locked storage or a public page). A root kept only in the same database does not protect against someone who controls the database. If `BANK_AUDIT_SIGNING_KEY` was left empty (dev origins only), roots are signed with a throwaway key that cannot be recovered.

`GET /api/transparency/audit/root?day=YYYY-MM-DD` (public, rate limited) returns a finished day's stored root exactly as stored, with `verified` (does it check out under the current public key) and `provisional: false`; a finished day without a stored root is signed and stored once (insert if missing). Today is never signed: the answer is `{day, head_seq, head_hash, provisional: true}` with no signature, because the day's head still moves. To replace a stored root deliberately, an operator calls `audit.Recorder.SignDailyRoot`.

`GET /api/transparency/audit/recent` publishes only `seq`, `at` truncated to the hour, the action class (the part before the first dot, e.g. `transfer`) and the first 16 hex characters of each event's hash. Actor ids, subjects and payloads stay private, so the feed cannot be read as anyone's activity timeline.

## Enable Banking setup

Restricted production gives read access to your own accounts only.

1. Register an application at Enable Banking and choose restricted production.
2. Generate an RSA key pair. Upload the public key to the portal. Keep the private key as a PEM file.
3. Note the application id.
4. In the portal, link your own bank accounts and note each account uid.
5. Set:
   ```
   ENABLE_BANKING_APP_ID=<application id>
   ENABLE_BANKING_PRIVATE_KEY_FILE=/path/to/private.pem
   ENABLE_BANKING_ACCOUNT_UIDS=<uid1>,<uid2>
   ```
6. Run `./dev refresh`. The log shows "enable banking provider enabled" when the variables are complete.

Requests are signed with RS256 JWTs. Other people's accounts cannot be read in this mode. Never commit the PEM file.

## Security notes

- `BANK_DEV_TEST_LOGIN` must never be set in production. It opens a login endpoint and a step-up endpoint (`dev-stepup`, marks the session stepped up for a payload hash) that skip passkeys, and the Sign-in screen then offers demo buttons. Use it only in tests, the e2e harness and `./dev demo` on your own machine.
- Adding a passkey, or a signature-counter regression (clone warning) at login or step-up, revokes every other session of that principal (audit `auth.sessions_revoked`); only the session of the ceremony itself survives.
- Login and registration rate limiting is keyed on the client IP, taken from the transport peer address (`RemoteAddr`). Proxy headers are not trusted. Behind a reverse proxy every request then appears to come from the proxy, so all users share one limit. Before deploying behind a proxy, add trusted-proxy handling (accept `X-Forwarded-For` only from configured proxy addresses) or have the proxy preserve the client address.
- Set `BANK_ORIGIN` to the exact public HTTPS origin. WebAuthn will not work on a mismatch.
- Always set `BANK_AUDIT_SIGNING_KEY` and keep it out of the repository.
- Keep `.pgdata` and `.env` out of version control.

## Not yet

These are planned and not built:

- A non-owner application database role. Today the app connects as the owner of its tables, so its own credentials could disable the triggers above (`alter table ... disable trigger`); the next slice runs the app under a role that owns nothing.
- Per-request snapshot decode cost: open-data reads decode each provider's whole JSON snapshot on every request; cache the decoded records in process.
- Docker and Compose (no Docker in the sandbox; first needed for staging).
- Keycloak for staff SSO and the FAPI authorisation server.
- OpenBao for secrets.
- k3s or any orchestrator.
- The Capacitor shell, attestation and device-bound key (see ADR 0003).
- Payment rails (SEPA, FPS) and payment orchestration with River.
- KYC, sanctions screening and transaction monitoring.
- Backups with a restore drill, observability stack, TLS termination.

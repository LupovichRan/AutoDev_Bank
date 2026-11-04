# Operations guide

AutoDev Bank is one Go binary (`cmd/bank`) and one PostgreSQL 16 database. There is no Docker in the development loop.

## Local run

All commands go through `./dev`.

| Command | What it does |
|---|---|
| `./dev pg start` | Start a local PostgreSQL 16 under `.pgdata` on port 55432. Also `stop`, `reset`, `status`. |
| `./dev migrate` | Apply `migrations/*.sql` in order. |
| `./dev seed` | Load demo principals and accounts. |
| `./dev refresh` | Fetch live open data into the cache once. |
| `./dev test` | `go test ./...`. Integration tests need `DATABASE_URL`. |
| `./dev run` | Build the web app (if npm is present) and serve on `$BANK_ADDR`. |
| `./dev web` | Vite dev server with an `/api` proxy. |
| `./dev e2e` | Playwright suite. It starts its own server. |
| `./dev ci` | `go vet`, `go test -race`, web build, check and test. |

First run:

```
./dev pg start
./dev migrate
./dev seed
./dev run
```

The binary has four subcommands: `serve`, `migrate`, `seed`, `opendata-refresh`. At the time of writing `serve` and `seed` are stubs in the code base and return "not implemented yet"; they land with the HTTP wiring task.

## Environment variables

Read once at startup in `cmd/bank/main.go`, plus the Enable Banking variables read by the provider.

| Variable | Meaning | Default |
|---|---|---|
| `DATABASE_URL` | PostgreSQL connection string. | `postgres://postgres@localhost:55432/bank?sslmode=disable` |
| `BANK_ADDR` | HTTP listen address. | `:8080` |
| `BANK_ORIGIN` | Public origin. Used as the WebAuthn relying-party origin, so it must match the browser URL. | `http://localhost:8080` |
| `BANK_AUDIT_SIGNING_KEY` | Hex-encoded 32-byte Ed25519 seed that signs daily audit roots. If empty, a key is generated and logged, and is lost on restart. Set it in any real deployment. | empty |
| `BANK_DEV_TEST_LOGIN` | `1` enables `POST /api/auth/dev-login`. Tests only. See Security notes. | off |
| `BANK_OPENDATA_FIXTURES` | `1` serves recorded fixtures instead of live upstreams. | off |
| `ENABLE_BANKING_APP_ID` | Enable Banking application id. | empty (provider off) |
| `ENABLE_BANKING_PRIVATE_KEY` | RS256 private key in PEM form. | empty |
| `ENABLE_BANKING_PRIVATE_KEY_FILE` | Path to the PEM file. Used only when the variable above is empty. | empty |
| `ENABLE_BANKING_ACCOUNT_UIDS` | Comma-separated account uids whitelisted in the Enable Banking portal. | empty |
| `HTTPS_PROXY` | Honoured by the open-data HTTP client. | unset |

`.env.example` lists `DATABASE_URL`, `BANK_ADDR`, `BANK_ORIGIN`, `BANK_AUDIT_SIGNING_KEY` and the Enable Banking entries. It names the key variable `ENABLE_BANKING_PRIVATE_KEY_PEM`; the provider code reads `ENABLE_BANKING_PRIVATE_KEY` or `ENABLE_BANKING_PRIVATE_KEY_FILE`. Use the names in the table above.

## Databases per test package

`go test ./...` runs packages in parallel. `db.TestPool` takes the database name from `DATABASE_URL` and appends the package name, for example `bank_t_ledger`. It creates that database on demand, applies all migrations, and truncates every table in the `ledger`, `control` and `opendata` schemas when the test ends. If `DATABASE_URL` is unset, integration tests are skipped. Drop the `bank_t_*` databases to start clean.

## Migrations policy

- Files in `migrations/` are numbered and append-only: `NNNN_name.sql`, applied in order and recorded in `schema_migrations`.
- Before the first release, a migration may be edited in place (then run `./dev pg reset`). After the first release, never edit an applied file. Add a new numbered file.
- Note: there is currently no `0005` file; numbering is not required to be gapless.
- Constraints and triggers belong in migrations, not in application code.

## Open-data refresh

- One-off: `./dev refresh` (same as `go run ./cmd/bank opendata-refresh`). It fetches every provider and stores the last good snapshot per provider and kind. A failed provider keeps its previous snapshot. The command exits non-zero only if nothing at all was stored.
- Ticker: the refresher runs once at start and then every interval (default one hour) while the server is up.
- Fixture fallback: recorded fixtures are used only when a live fetch fails and nothing is cached yet (cold start). Set `BANK_OPENDATA_FIXTURES=1` to force fixtures, for offline work and tests.
- Provenance: every record carries `fetched_at`, `source_url` and `license`. The web app shows the source, fetch time and licence next to the data. Keep that attribution when you add a provider.
- Sources: OBIE Open Data v2.2 at UK banks, Frankfurter, ECB, Bank of Israel PublicApi, Bank of England IADB.

## Audit root verification

The chain is in `control.audit_events`. Each row's `hash` is `sha256(prev_hash || canonicalJSON({at, actor_id, action, subject, payload}))`, and the genesis `prev_hash` is 32 zero bytes. Daily roots are in `control.audit_roots` (`day`, `head_seq`, `head_hash`, `signature`).

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

Publish each root outside the database (object-locked storage or a public page). A root kept only in the same database does not protect against someone who controls the database. If `BANK_AUDIT_SIGNING_KEY` was left empty, roots are signed with a throwaway key that cannot be recovered.

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

- `BANK_DEV_TEST_LOGIN` must never be set in production. It opens a login endpoint that skips passkeys. Use it only in tests and the e2e harness.
- Login and registration rate limiting is keyed on the client IP, taken from the transport peer address (`RemoteAddr`). Proxy headers are not trusted. Behind a reverse proxy every request then appears to come from the proxy, so all users share one limit. Before deploying behind a proxy, add trusted-proxy handling (accept `X-Forwarded-For` only from configured proxy addresses) or have the proxy preserve the client address.
- Set `BANK_ORIGIN` to the exact public HTTPS origin. WebAuthn will not work on a mismatch.
- Always set `BANK_AUDIT_SIGNING_KEY` and keep it out of the repository.
- Keep `.pgdata` and `.env` out of version control.

## Not yet

These are planned and not built:

- Docker and Compose (no Docker in the sandbox; first needed for staging).
- Keycloak for staff SSO and the FAPI authorisation server.
- OpenBao for secrets.
- k3s or any orchestrator.
- The Capacitor shell, attestation and device-bound key (see ADR 0003).
- Payment rails (SEPA, FPS) and payment orchestration with River.
- KYC, sanctions screening and transaction monitoring.
- Backups with a restore drill, observability stack, TLS termination.

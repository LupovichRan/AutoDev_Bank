# AutoDev Bank

An open-source, web + mobile digital bank platform built only on free
open-source infrastructure, with a provably correct ledger, controls as code
(separation of duties, hash-chained audit), passkey authentication, live open
banking data, and a KISS philosophy: one Go binary, one PostgreSQL.

## See the product (no Docker needed)

```bash
./dev demo
```

Then open http://localhost:8080 and press one of the "Demo sign-in (dev
only)" buttons: Alice and Bob are customers with a joint account, Carol is a
staff approver, Dave a staff initiator. `./dev demo` starts PostgreSQL 16
under `.pgdata` (port 55432), migrates, seeds, refreshes open data, builds
the web app and serves it with `BANK_DEV_TEST_LOGIN=1`, which skips
passkeys for the seeded users. It is for your own machine only; never set
`BANK_DEV_TEST_LOGIN` anywhere else.

Try: pay Bob 50.00 from Alice's account (posts at once, after a passkey
step-up), pay 2,000.00 from the joint account (waits for Bob), approve it as
Bob, then check the audit chain on the Transparency screen.

## Developer commands

```bash
./dev pg start      # local PostgreSQL 16 under .pgdata on port 55432
./dev migrate       # apply migrations/*.sql
./dev seed          # demo principals and accounts (localhost origins only)
./dev refresh       # pull live open-banking and central-bank data into the cache
./dev run           # build the web app and serve on http://localhost:8080 (passkeys only)
./dev test          # go test ./...   (integration tests use DATABASE_URL)
./dev e2e           # Playwright end-to-end suite
./dev ci            # everything CI runs
```

Nothing loads `.env` automatically; export variables yourself
(`set -a; . ./.env; set +a`). See `docs/OPERATIONS.md`.

Go 1.26 is auto-downloaded by the Go toolchain if your local `go` is older.

## Layout

```
cmd/bank/            the single binary: serve | migrate | seed | opendata-refresh
internal/ledger/     append-only double-entry ledger (invariants enforced in SQL)
internal/audit/      hash-chained audit log with signed daily roots
internal/approvals/  maker-checker workflow, separation-of-duties policy
internal/auth/       WebAuthn passkeys, sessions, step-up bound to payment payload
internal/opendata/   zero-credential open banking / central-bank providers + cache
internal/httpapi/    JSON API (RFC 9457 problem details) + static web serving
internal/db/         pool, serializable tx with retry, in-house migrator
migrations/          NNNN_name.sql, embedded
web/                 Svelte 5 PWA
tools/               in-house dev tooling (./dev)
docs/                research, design spec, implementation plan, ADRs
```

## Design

- Spec: `docs/superpowers/specs/2026-11-04-autodev-bank-design.md`
- Plan: `docs/superpowers/plans/2026-11-04-core-vertical-slice.md`
- Research report: `docs/research/reports/Open source bank platform research.md`

## Licence

Apache-2.0. Open data served by the app carries its source, a short
attribution and a link to the publisher's own terms (Open Banking Limited
Open Licence, ECB, Bank of Israel, Bank of England, Frankfurter); the
publisher's terms govern reuse.

## Android test app

`android/` is a deliberately tiny in-house shell: one Java `WebView` activity
with no dependencies. It loads the PWA from a running bank server (same origin
as the API, so cookies and CSP work exactly as in a browser) and asks for the
server address on first start.

```bash
./dev demo                                   # on your laptop: server on :8080 with demo users
cd android && ./gradlew assembleDebug        # or download the APK artifact from the "android" CI workflow
adb install app/build/outputs/apk/debug/app-debug.apk
```

On the phone, enter `http://<your-laptop-LAN-IP>:8080` (emulator: `http://10.0.2.2:8080`).
This test build allows plain http and relies on the server's dev mode for the
passkey step-up (WebAuthn needs an https origin); a production build must use
https, certificate pinning and the Capacitor plugins described in ADR 0003.

# AutoDev Bank

An open-source, web + mobile digital bank platform built only on free
open-source infrastructure, with a provably correct ledger, controls as code
(separation of duties, hash-chained audit), passkey authentication, live open
banking data, and a KISS philosophy: one Go binary, one PostgreSQL.

## Quickstart (no Docker needed)

```bash
./dev pg start      # local PostgreSQL 16 under .pgdata on port 55432
./dev migrate       # apply migrations/*.sql
./dev seed          # demo principals and accounts
./dev refresh       # pull live open-banking and central-bank data into the cache
./dev run           # build the web app and serve on http://localhost:8080
./dev test          # go test ./...   (integration tests use DATABASE_URL)
./dev ci            # everything CI runs
```

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

Apache-2.0. Open data served by the app carries its source and licence
(Open Banking Limited Open Licence, ECB, Bank of Israel, Bank of England terms).

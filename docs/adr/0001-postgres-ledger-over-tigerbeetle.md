# 0001. Postgres ledger over TigerBeetle

- Status: Accepted
- Date: 2026-11-04

## Context
A bank ledger needs an immutable journal, integer minor units, idempotency keys at the storage layer, two-phase transfers and corrections by reversal. TigerBeetle has the strongest engineering (Apache-2.0, strict serializability, Jepsen-audited in 2025), but its recommended deployment is six replicas across three providers. That is too much operations for a small team. The pgledger pattern (accounts, transfers, entries in plain SQL) benchmarked at 10,637 transfers/s on a laptop; its cost is hot-account contention. Formance v3 needs Go 1.27, Midaz is ELv2 and Blnk needs Redis, so each fails a licence or simplicity test.

## Decision
Build an in-house append-only ledger in PostgreSQL (schema `ledger`, migration 0002). Invariants live in SQL triggers: entries are immutable, each transaction balances to zero per currency, and balances change only through the triggers. Amounts are BIGINT minor units. Transfers are two-phase (pending, posted, voided) with reserved `pending_debits` and `pending_credits`, so available balance excludes held funds. Posting is idempotent through a UNIQUE key.

Isolation ruling:
- Ledger money movement runs under READ COMMITTED with `FOR UPDATE` row locks taken in sorted account order. Every row that is read and then written is explicitly locked. SERIALIZABLE added only retry storms and no extra safety.
- Control-plane writes default to SERIALIZABLE through `db.WithTx`, which retries on 40001 and 40P01 (five attempts, jittered backoff).

## Consequences
Good:
- One datastore, one backup, one transaction for ledger, audit and approvals.
- Invariants hold even if the Go code has a bug, because the database refuses bad writes.
- Property tests run against real PostgreSQL.

Bad:
- Hot accounts serialise on their row lock. Throughput is bounded by the busiest account.
- We own the ledger code and its correctness.
- No built-in multi-region replication.

## Swap path
If measured hot-account contention appears, move the balances and transfers data plane to TigerBeetle. Keep the Go `ledger` package interface, the control plane and the audit chain in PostgreSQL.

## References
- docs/research/reports/Open source bank platform research.md (section "An append-only Postgres ledger beats TigerBeetle for a small team"; licence table, "Ledger")
- docs/superpowers/specs/2026-11-04-autodev-bank-design.md

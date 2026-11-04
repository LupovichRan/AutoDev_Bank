# 0006. Audit hash chain

- Status: Accepted
- Date: 2026-11-04

## Context
Auditors and customers need evidence that history was not rewritten, including by the operator. immudb does this but is BSL-licensed. A hash chain in PostgreSQL is small, and a signed daily root can be published outside the database.

## Decision
`control.audit_events` is append-only (triggers refuse UPDATE and DELETE). Each row stores `hash = sha256(prev_hash || canonicalJSON({at, actor_id, action, subject, payload}))`. The genesis `prev_hash` is 32 zero bytes. Appends run in the caller's transaction and lock the single `control.audit_head` row, so the chain is serialised. Once a day the chain head (`day`, `head_seq`, `head_hash`) is signed with Ed25519 and stored in `control.audit_roots`, insert-if-missing, never overwritten automatically. Only finished UTC days are signed: today's head is published unsigned and marked provisional, so no signature ever covers a head that later moves. The public recent-events feed shows only seq, the hour, the action class and a hash prefix, never actors or subjects, so transparency does not become a per-person timeline. immudb is rejected (BSL).

## Consequences
Good:
- The audit event and the business change commit atomically.
- Anyone with the public key can check a published root.

Bad:
- The head-row lock serialises all audited writes. This is a throughput ceiling.
- A root stored only in the same database proves nothing against an attacker who controls the whole database. Roots must be copied elsewhere.
- Losing the signing key stops new roots being signed (old ones still verify).

## Swap path
Anchor roots to WORM storage (object lock) or a public log. If the head lock hurts, shard chains. A different tamper-evident store can replace the table behind `audit.Recorder`.

## References
- docs/research/reports/Open source bank platform research.md (licence table, "Audit integrity"; build order, phase 1)

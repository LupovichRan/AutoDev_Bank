# Architecture decision records

One file per decision. Each is one page or less. Accepted records are not edited
except to change Status; a new record supersedes an old one.

| No. | Title | Status |
|---|---|---|
| [0001](0001-postgres-ledger-over-tigerbeetle.md) | Postgres ledger over TigerBeetle | Accepted |
| [0002](0002-in-house-passkey-auth.md) | In-house passkey authentication | Accepted |
| [0003](0003-web-core-capacitor-shell.md) | Web core in a Capacitor shell | Accepted |
| [0004](0004-open-data-tier0-providers.md) | Open data: tier-0 providers | Accepted |
| [0005](0005-separation-of-duties.md) | Separation of duties | Accepted |
| [0006](0006-audit-hash-chain.md) | Audit hash chain | Accepted |

## Template

```markdown
# NNNN. Title

- Status: Proposed | Accepted | Superseded by NNNN
- Date: YYYY-MM-DD

## Context
2 to 5 sentences with the key evidence.

## Decision
What we do.

## Consequences
Good:
- ...
Bad:
- ...

## Swap path
How to replace this later, and the trigger for doing so.

## References
- Paths under docs/research.
```

Research source: `docs/research/reports/Open source bank platform research.md`.

# 0005. Separation of duties

- Status: Accepted
- Date: 2026-11-04

## Context
DORA Art. 21 and ordinary four-eyes practice require that the person who starts a payment is not the person who approves it. Role assignment mistakes are as dangerous as workflow mistakes. An external policy engine adds a service before the policy is understood, and Casbin has no SoD constraints or audit.

## Decision
- Maker-checker is a transactional `control.approvals` table with `control.approval_decisions`, plus an in-code policy (amount tiers, joint accounts, business roles). Every policy evaluation is written to a decision log.
- Static SoD: `payments.initiator` and `payments.approver` cannot be held by the same principal. A DB trigger on role assignment enforces it.
- Dynamic SoD: the approver must differ from the initiator, and each approver decides once. A DB trigger enforces it.
- Execution posts the ledger transaction in the same database transaction as the status change.

## Consequences
Good:
- The rules hold even if application code is wrong or bypassed.
- Approval, ledger write and audit event commit together.

Bad:
- Policy changes need Go changes and a release.
- Trigger errors must be mapped to clear API errors.
- A small team may find the two-role split awkward.

## Swap path
When policy outgrows code, move evaluation to Cerbos or OpenFGA (both Apache-2.0). Keep the DB triggers as the backstop and keep the decision log.

## References
- docs/research/reports/Open source bank platform research.md (build order, phase 3; licence table, "Authorization")
- docs/superpowers/specs/2026-11-04-autodev-bank-design.md

# 0005. Separation of duties

- Status: Accepted
- Date: 2026-11-04

## Context
DORA Art. 21 and ordinary four-eyes practice require that the person who starts a payment is not the person who approves it. Role assignment mistakes are as dangerous as workflow mistakes. An external policy engine adds a service before the policy is understood, and Casbin has no SoD constraints or audit.

## Decision
- Maker-checker is a transactional `control.approvals` table with `control.approval_decisions`, plus an in-code policy (amount tiers, joint accounts, business roles). Every policy evaluation is written to a decision log.
- Static SoD: `payments.initiator` and `payments.approver` cannot be held by the same principal, and customer principals hold no `payments.*` role. A DB trigger on role assignment enforces both.
- Dynamic SoD: the approver must differ from the initiator, must hold the from-account or `payments.approver`, and decides once, only while the approval is pending and unexpired. A DB trigger enforces it.
- Quorum in the schema: `pending -> approved` requires `required_approvals` approve decisions and, for joint accounts, one from another holder of the from-account; `approved -> executed` requires the approval's own posting (idempotency key, posted, from/to, amount). So raw SQL cannot approve or execute with zero decisions.
- Who must be checked (policy, amended 2026-11-04 after the final review): a customer debits only accounts they hold; a staff initiator may debit any account but always needs at least one approval; any debit from an `allow_negative` or holder-less account (bank equity) always needs at least one approval; amount tiers add approvers on top.
- Approving needs a fresh passkey step-up bound to `sha256("approve:" + approval_id)`, for co-holders and staff alike; rejecting and cancelling do not.
- Pending approvals expire after 7 days (hourly sweep); the initiator may cancel a pending approval (it becomes `rejected`, reason "cancelled by initiator"; no decision row, since the initiator may never decide).
- Execution posts the ledger transaction in the same database transaction as the status change.

## Consequences
Good:
- The rules hold even if application code is wrong or bypassed.
- Approval, ledger write and audit event commit together.

Bad:
- Policy changes need Go changes and a release.
- The triggers bind the app only while it cannot disable them: today the app connects as the table owner. A non-owner application role is planned (OPERATIONS "Not yet").
- Trigger errors must be mapped to clear API errors.
- A small team may find the two-role split awkward.

## Swap path
When policy outgrows code, move evaluation to Cerbos or OpenFGA (both Apache-2.0). Keep the DB triggers as the backstop and keep the decision log.

## References
- docs/research/reports/Open source bank platform research.md (build order, phase 3; licence table, "Authorization")
- docs/superpowers/specs/2026-11-04-autodev-bank-design.md

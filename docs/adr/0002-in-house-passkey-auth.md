# 0002. In-house passkey authentication

- Status: Accepted
- Date: 2026-11-04

## Context
Strong customer authentication needs two factors. A passkey with user verification gives possession plus inherence or knowledge in one gesture and resists phishing. PSD2 dynamic linking requires the authentication to be bound to the amount and payee. Keycloak adds a second deployable and does not natively bind an assertion to a payment payload. The research report recommends an in-house WebAuthn service for customers and Keycloak only for staff and open-banking authorisation.

## Decision
Customer authentication is a package in the Go monolith (`internal/auth`) using go-webauthn (BSD-3).
- Registration requires a resident key and user verification.
- Login creates an opaque server-side session. The cookie holds a random id only.
- Step-up is a fresh WebAuthn assertion whose challenge embeds the hash of the payment payload (dynamic linking). Changing amount or payee invalidates it.
- Adding a passkey to an existing account requires a fresh step-up.
- No passwords exist anywhere.

## Consequences
Good:
- No password database and no reset-by-email attack surface.
- Step-up is tied to the exact payment.
- Sessions can be revoked server-side at once.

Bad:
- We own account recovery, which is the hard part. Recovery is approver-mediated (maker-checker).
- We own WebAuthn edge cases (clone warnings, counters).
- Device attestation is not available from a PWA (see 0003).

## Swap path
Keycloak (Apache-2.0) is reserved for staff SSO and, later, as the FAPI authorisation server for open banking. Customer auth can move behind it by replacing the session issuer. The step-up payload-hash contract stays.

## References
- docs/research/reports/Open source bank platform research.md (section "Passkeys plus a device-bound signing key satisfy SCA; Keycloak only for staff and open banking"; licence table, "Customer auth")
- docs/superpowers/specs/2026-11-04-autodev-bank-design.md

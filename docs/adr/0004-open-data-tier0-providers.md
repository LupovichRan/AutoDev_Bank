# 0004. Open data: tier-0 providers

- Status: Accepted
- Date: 2026-11-04

## Context
The product should show real production data without licences or secrets. On 2026-11-04 the research verified zero-credential sources: UK Open Banking Open Data v2.2 at 12 UK banks, ECB SDMX, Frankfurter, Bank of Israel PublicApi and Bank of England IADB. GoCardless Bank Account Data has closed signups. Enable Banking restricted production is the remaining free route to real PSD2 data for one's own accounts. Anything touching a third party's accounts needs a licence (FCA AISP/PISP, eIDAS certificates).

## Decision
Tier-0 providers are the default source: OBIE Open Data v2.2 (ATMs, branches, products), Frankfurter, ECB SDMX, Bank of Israel PublicApi and Bank of England IADB. Each snapshot is cached in PostgreSQL per provider and kind, with `fetched_at`, `source_url` and `license` on every record. A failed fetch keeps the previous snapshot. Recorded fixtures are only a cold-start fallback. Enable Banking restricted production is an optional provider for the operator's own accounts. Licensed AIS/PIS is out of scope.

## Consequences
Good:
- Real data, no credentials, demonstrable in the sandbox.
- Provenance and licence are visible in the UI.

Bad:
- Upstream formats change without notice, so parsers need maintenance.
- The OBIE participant list dates from 2019 and some banks are gone.
- Enable Banking shows only the operator's own accounts, not customers'.

## Swap path
Add a provider by implementing the `opendata` provider interface. Move to licensed AIS/PIS (Tink, TrueLayer, Yapily, or own FCA agent status) behind the same cache and provenance fields.

## References
- docs/research/reports/Open source bank platform research.md (section "Real production data today: zero-credential, free-signup and licensed tiers"; tiers table)

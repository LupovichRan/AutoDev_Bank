# Backend and platform infrastructure for a small-team digital bank (late-2026 state)

Research date: 2026-11-04. Scope: free/OSS backend language, database, messaging/workflow, platform, CI/CD, compliance tooling, and hosting cost realities. License status was verified against primary LICENSE files or vendor licensing pages wherever possible (fetched 2026-11-04); where I could not reach the primary file, the item is listed under Gaps rather than asserted.

License legend used below: **OSI-free** = Apache-2.0 / MIT / BSD / MPL-2.0 / PostgreSQL / GPL/AGPL (copyleft but free). **Source-available (not OSI)** = BSL 1.1, ELv2, SSPL, Kurrent License, GrowthBook/SigNoz enterprise dirs. Flagged inline.

---

## Key Question 1: Backend language/runtime for a bank in 2026

### Takeaway
There is no single "bank language"; the production evidence splits between Go (Monzo), JVM Java/Kotlin (Wise, Revolut), Clojure-on-JVM (Nubank), Haskell (Mercury), Ruby+Sorbet (Stripe) and Zig (TigerBeetle). For a small KISS-minded team in 2026, Go (simplest ops, single static binary, Go 1.26 compat guarantee, shopspring/decimal MIT) or Kotlin/JVM (BigDecimal built in, largest fintech hiring pool) are the evidenced defaults; TypeScript is viable but still lacks a native decimal type (TC39 Decimal is Stage 1).

### Cited Findings

**What real fintechs chose**
- Monzo: "Building a Bank with Go" (QCon London 2017, Matt Heath) — Go's concurrency features suit high-volume, low-latency distributed systems and microservice architectures — [InfoQ](https://www.infoq.com/news/2017/03/monzo-bank-golang/); Go team case study says Go's simplicity makes it easy to build small single-responsibility networked services — [go.dev Monzo case study](https://go.googlesource.com/website/+/HEAD/_content/solutions/monzo.md); ~1,600 microservices by 2020 — [The Register](https://www.theregister.com/2020/03/09/monzo_microservices/); a Monzo platform engineer cites the Go compatibility guarantee as the reason upgrades are painless, and that the bank is "built largely in Go" with some Python — [Monzo community AMA](https://community.monzo.com/t/monzonaut-ama-bee-platform-engineer/120012?page=2). (Flag: the primary talk is 2017; still-current per the 2020 Register piece and AMA.)
- Wise: "Most of our over 600 backend services are running on JVM, use Spring Boot and written in Java or, sometimes, Kotlin" (2022) — [Wise Engineering](https://medium.com/wise-engineering/wise-tech-stack-2022-edition-a6ac089a382f); 2025 update: "In the backend we are primarily using Java and Kotlin," plus a Kubernetes-based Compute Runtime Platform — [Wise Tech Stack 2025 update](https://medium.com/wise-engineering/wise-tech-stack-2025-update-d0e63fe718c7); job ads cite 700+ microservices on Java/Spring Boot/Kafka/PostgreSQL — [Wise job listing](https://builtinlondon.uk/job/senior-software-engineer-i-cash-management/11307886).
- Revolut: core platform engineering roles are Java — [Index Ventures / Revolut job listing](https://www.indexventures.com/startup-jobs/revolut/software-engineer-java-core-platform/). (Weak evidence: job ads only; no Revolut engineering-blog statement found.)
- Nubank: Clojure main language, Datomic DB, Kafka messaging; "600 Clojure developers … 2.5 million lines of Clojure code in 500 microservices on 2000+ Datomic servers"; Nubank acquired Cognitect (Clojure/Datomic stewards) — [Finovate](https://finovate.com/turning-ideas-into-business-agility-brazils-nubank-acquires-cognitect/); [InfoQ podcast with Lucas Cavalcanti](https://www.infoq.com/podcasts/lucas-cavalcanti-nubank-fintech-clojure/).
- Mercury: Haskell backend; CTO Max Tagher: "we wanted something statically typed because customers have higher correctness expectations for their bank"; claims backend Haskell engineer is the "easiest role to hire for" in the company; acknowledges parts of Haskell are "slower to get stuff ramped up" — [Serokell interview](https://serokell.io/blog/haskell-in-production-mercury). Note Mercury is a fintech working through partner banks, not a chartered bank.
- Stripe: "most of its code is written in Ruby" (also Java, TypeScript, Go); >15M lines of Ruby across ~150k files (2022); built Sorbet because Ruby was "becoming a source of friction more than a source of productivity"; Sorbet required in CI from May 2018, open-sourced June 2019 — [Stripe blog](https://stripe.dev/blog/sorbet-stripes-type-checker-for-ruby).
- Chime: all backend listings found cite Ruby on Rails, none mention Elixir — [Chime job listing](https://themuse.com/jobs/chime/backend-engineer). I found **no** evidence Chime uses Elixir; treat the "Chime Elixir?" premise as unsupported.
- TigerBeetle: written in Zig (~78% of repo), Apache 2.0, production-ready on Linux since March 2024, clients for Python/Java/Node/.NET/Go/Ruby/Rust; vendor claims 100M txns/month in production customers (Jan 2025) and a "trillion-transaction" scale test (Mar 2026); latest release 0.17.9 (2026-07-06) — [tigerbeetle.com](https://tigerbeetle.com); [company page](https://www.tigerbeetle.com/company). Production topology: "Six replicas are used rather than three," each on a dedicated machine with a supervisor — [TigerBeetle deploying docs](https://docs.tigerbeetle.com/operating/deploying/).

**Runtime status, late 2026**
- Go 1.26 (Feb 2026): Green Tea GC on by default, `go fix` modernizers, goroutine-leak profile (experimental, default planned for 1.27), heap base randomization, cgo overhead −30%, requires Go 1.24.6+ to bootstrap — [Go 1.26 release notes](https://go.dev/doc/go1.26).
- Node.js 24 "Krypton" is the active LTS line with maintenance through April 2028; TS support is type-stripping only (`--experimental-strip-types`, no enums/decorators); one source says Node 26 moves to LTS in Oct 2026 (single-source, unverified) — [dev.to runtime comparison](https://dev.to/mr_manushukla/deno-vs-bun-vs-nodejs-in-2026-the-production-backend-runtime-decision-30pc). Deno 2.9 is an LTS channel backported to 31 Jan 2027; Bun 1.3.14 (12 May 2026) — same source. Benchmarks across sources conflict badly (Bun 180k rps vs "all converge to ~12k rps with DB round-trips") — [Medium comparison](https://medium.com/@moksh45/node-js-vs-bun-vs-deno-2-in-2026-which-javascript-runtime-should-you-actually-use-0ecea201b54f). All are blog sources; treat as indicative.

**Decimal / money handling**
- JavaScript: TC39 Decimal proposal is **Stage 1** (champions Jesse Alama/Igalia, Jirka Maršík/Oracle, Andrew Paprocki/Bloomberg); targets IEEE 754 Decimal128 — [tc39/proposal-decimal](https://github.com/tc39/proposal-decimal). July 2026 plenary: "Decimal stage 1 update"; implementers strongly objected to making it a primitive — [TC39 2026-07 meeting notes (secondary, Japanese)](https://scrapbox.io/petamoriken/2026-07_%E3%81%AE_TC39_meeting). One blog claims Stage 2; contradicted by the repo README.
- Go: shopspring/decimal is MIT — [LICENSE](https://raw.githubusercontent.com/shopspring/decimal/HEAD/LICENSE).
- Rust: rust_decimal is a "Pure-Rust fixed-precision Decimal type for financial and monetary calculations," v1.42.1, MIT — [openapps listing](https://openapps.pro/packages/rust-decimal).
- Elixir: `decimal` (~> 2.0) is the standard library for exact decimal arithmetic — [ssojet guide](https://binary-encoding-decoding.ssojet.com/decimal-in-elixir).

**Hiring / popularity (Stack Overflow 2025, professional developers)**
- JavaScript 68.8%, Python 54.8%, TypeScript 48.8%, Java 29.6%, Go 17.4%, Rust 14.5%, Kotlin 11.5%, Elixir 2.8%; Rust is the most admired language (72%), followed by Gleam 70%, Elixir 66%, Zig 64%; PostgreSQL is the #1 database at 58.2% of professionals — [Stack Overflow Developer Survey 2025](https://survey.stackoverflow.co/2025/technology/).

**Thoughtworks Radar (Vol 35, Nov 2026)**
- Restate and Temporal are both in the **Trial** ring; Restate moved Assess → Trial because it "has become a core part of a large insurance system that automates an entire business process" — [Thoughtworks Radar: Restate](https://www.thoughtworks.com/radar/platforms/restate).

### Inferences
- For a 2–6 person team that wants in-house tooling and KISS, Go is the lowest-operational-surface choice: one static binary, no runtime to manage, strong stdlib for HTTP/TLS, Postgres-native queue/workflow libraries (River, DBOS-style patterns) and the Monzo precedent. Kotlin/JVM is the strongest "hire from the fintech pool" choice (Wise, Revolut, Nubank-JVM) with `BigDecimal` built in, at the cost of JVM memory and startup.
- TypeScript is acceptable for a bank backend only if money is modelled as integer minor units (bigint) or via a library; the absence of a native decimal type (Stage 1 in late 2026) is a real correctness hazard for a small team.
- Rust gives the best correctness story but the smallest pool (14.5% usage) and slowest iteration; it is more defensible for a ledger core (cf. TigerBeetle in Zig) than for the CRUD/API surface.
- Elixir/Haskell/Clojure each have a bank precedent, but hiring data (Elixir 2.8%) and the Mercury CTO's "slower ramp-up" admission argue against them for a small generalist team unless the founders already know them.

### Gaps
- No primary Revolut or Chime engineering-blog statements on language choice were found; evidence is job ads only.
- No independent 2026 benchmark of Node 24 vs Bun vs Deno under bank-like workloads; sources conflict by >10x.
- Could not retrieve 2025/2026 Stack Overflow "admired" percentages for Go, TypeScript, Kotlin, Java.
- Go 1.27 (expected Aug 2026) release notes were not checked; Go 1.26 is the latest release confirmed.

---

## Key Question 2: Database — PostgreSQL 17/18, HA on free tooling, SQLite/libSQL, ClickHouse, event sourcing

### Takeaway
PostgreSQL 18 (released 2025-09-25) plus Postgres 17's logical-replication failover and incremental backup cover every ledger need a small bank has (SERIALIZABLE/SSI, temporal constraints, uuidv7, partitioning); HA and backup are fully free with Patroni (MIT) or CloudNativePG (Apache-2.0) plus pgBackRest (MIT). Dedicated event stores are now source-available (KurrentDB under Kurrent License v1), so event sourcing, if wanted, should live in Postgres (Marten MIT, or a hand-rolled append-only table + outbox).

### Cited Findings

**PostgreSQL 18 (GA 2025-09-25)**
- Asynchronous I/O subsystem: "up to 3×" faster reads from storage in certain scenarios; `io_method` = worker | io_uring | sync; covers seq scans, bitmap heap scans, vacuum — [PostgreSQL 18 press kit](https://www.postgresql.org/about/press/presskit18/).
- `uuidv7()` timestamp-ordered UUIDs (better index locality); `uuidv4()` alias — same source.
- Temporal constraints: `PRIMARY KEY/UNIQUE … WITHOUT OVERLAPS` and `FOREIGN KEY … PERIOD` — same source.
- Logical replication: conflicts reported in `pg_stat_subscription_stats`; parallel streaming default; `pg_createsubscriber --all`; `idle_replication_slot_timeout`; stored generated columns replicated — same source.
- OAuth 2.0 auth method; md5 passwords deprecated (use SCRAM); data checksums on by default for new `initdb` clusters; B-tree skip scan; virtual generated columns default — same source.

**PostgreSQL 17 (relevant to ledgers/HA)**
- Logical replication failover control (`failover = true` on subscriptions); slots survive major upgrades so subscribers need not resync; `pg_createsubscriber` converts a physical replica into a logical one; `pg_basebackup --incremental` + `pg_combinebackup`; vacuum uses up to 20× less memory; "up to 2x better write throughput" from WAL improvements; `JSON_TABLE`; `MERGE … RETURNING`; `pg_maintain` role — [PostgreSQL 17 press kit](https://www.postgresql.org/about/press/presskit17/).

**SERIALIZABLE isolation (SSI) for ledgers**
- Serializable "emulates serial transaction execution for all committed transactions"; monitoring "does not introduce any blocking beyond that present in repeatable read, but there is some overhead"; failures always return SQLSTATE `40001` and "applications using this level must be prepared to retry transactions"; predicate locks (`SIReadLock`) never block or deadlock; tuning: use `READ ONLY`, pool connections, keep transactions short, raise `max_pred_locks_per_transaction`; unique-constraint violations can still surface under overlapping Serializable transactions; `SERIALIZABLE READ ONLY DEFERRABLE` is the only blocking case — [PostgreSQL docs: transaction isolation](https://www.postgresql.org/docs/current/transaction-iso.html).

**HA / backup tooling (licenses verified)**
- Patroni: MIT — [LICENSE](https://raw.githubusercontent.com/patroni/patroni/HEAD/LICENSE). pgBackRest: MIT — [LICENSE](https://raw.githubusercontent.com/pgbackrest/pgbackrest/HEAD/LICENSE). CloudNativePG: Apache-2.0 — [LICENSE](https://raw.githubusercontent.com/cloudnative-pg/cloudnative-pg/HEAD/LICENSE).
- CNPG "deliberately does not use Patroni; failover is coordinated through the Kubernetes API and a per-cluster lease"; it is a CNCF Sandbox project with declarative major-version upgrades in releases up to 1.30; Patroni "has been through every failure mode in production, and gives you a REST API" but is an extra layer; Crunchy PGO had a ~8-month release gap in 2025 after the Snowflake acquisition — [dev.to: Postgres on Kubernetes in 2026](https://dev.to/mr_manushukla/postgres-on-kubernetes-in-2026-cloudnativepg-vs-kubeblocks-vs-crunchy-pgo-and-when-managed-dbaas-240h); [Gabriele Bartolini CNPG vs PGO comparison (HN mirror)](https://hackernews-dynamic.seasondane.workers.dev/news/48248201). One ithy.com article wrongly claims CNPG uses Patroni; disregard.
- pgvector: PostgreSQL License — [LICENSE](https://raw.githubusercontent.com/pgvector/pgvector/HEAD/LICENSE).

**SQLite / libSQL / Turso**
- libSQL (C fork of SQLite): MIT — [LICENSE.md](https://raw.githubusercontent.com/tursodatabase/libsql/HEAD/LICENSE.md). Turso Database (Rust rewrite, ex-"Limbo"): MIT, **beta**, "libSQL is production ready, Turso Database is not"; CEO AMA (June 2026) still calls it beta; async/io_uring design, deterministic simulation testing, MVCC concurrent writes — [Turso announcement](https://turso.tech/blog/we-will-rewrite-sqlite-and-we-are-going-all-in); [Reddit AMA snapshot 2026-06-12](https://reddit.sentinel-team.org/posts/1tvz2dm/snapshots/2026-06-12T03%3A41%3A07.2239Z).

**ClickHouse (analytics)**
- Apache-2.0 — [LICENSE](https://raw.githubusercontent.com/ClickHouse/ClickHouse/HEAD/LICENSE).

**Event sourcing options**
- EventStoreDB → ESLv2 (Elastic-License-v2 variant) from 24.10 LTS (2024) — [Kurrent blog](https://www.kurrent.io/blog/introducing-event-store-license-v2-eslv2); renamed KurrentDB 25.0 and relicensed to **Kurrent License v1** (2025) — [KurrentDB 25.0 release notes](https://next.kurrent.io/releases/kurrentdb/25-0/). KLv1 text: broad royalty-free use/copy/modify grant; "You may not provide the software to third parties as a hosted or managed service"; may not circumvent license mechanisms; no node/size limits stated — [KurrentDB LICENSE.md](https://raw.githubusercontent.com/kurrent-io/KurrentDB/master/LICENSE.md). License-key-gated features: Auto-scavenge, Kafka and most connectors (HTTP sink free), Stream Policies, encryption-at-rest, LDAPS/OAuth auth, OTLP/logs endpoints, archiving — [Kurrent what's new](https://docs.kurrent.io/server/v25.0/quick-start/whatsnew.html). **Source-available, not OSI.**
- Marten (Postgres document DB + event store for .NET): MIT — [LICENSE](https://raw.githubusercontent.com/JasperFx/marten/HEAD/LICENSE).
- Formance Ledger (Go, Postgres, Numscript double-entry): core "fully functional in OSS", enterprise Wallets/Flows/Reconciliation sold separately; "production usage … is (only) supported through the official k8s operator deployment mode" — [checkthat.ai summary](https://checkthat.ai/ai-answers/f938d0df-d124-43aa-b426-51d02de44326); [project-awesome listing](https://project-awesome.org/r/formancehq-ledger-1). License conflicting (MIT vs Apache-2.0) across secondary sources; **could not fetch LICENSE (404)** — see Gaps.
- TigerBeetle: Apache-2.0, purpose-built double-entry ledger, but recommends six dedicated replicas — [TigerBeetle deploying docs](https://docs.tigerbeetle.com/operating/deploying/).

### Inferences
- Smallest thing that works: one PostgreSQL 18 primary + one streaming standby, `SERIALIZABLE` for ledger postings with a generic `40001` retry wrapper, `uuidv7()` keys, temporal constraints for effective-dated balances/rates, pgBackRest to object storage with WAL archiving, Patroni only when you need automated failover (or CNPG if you are already on k3s). Partition `postings`/`journal` tables by month once they exceed tens of millions of rows.
- Scale later: logical replication (PG17 failover slots) to a ClickHouse or read-replica analytics tier; TigerBeetle only if posting throughput exceeds what a single Postgres primary can commit (and you can afford six machines).
- Event sourcing: an append-only `events` table + transactional outbox in Postgres gives the audit benefits without KurrentDB's license risk or an extra stateful system. A dedicated event store is now source-available and a separate HA problem; avoid for a small team.
- libSQL/Turso are edge/embedded tools, not a bank system-of-record; ignore until there is an offline/edge requirement.

### Gaps
- Could not fetch formancehq/ledger LICENSE (404 on both `LICENSE` and `LICENSE.md`); license is MIT or Apache-2.0 per secondary sources but unverified.
- No independent benchmark of PG18 AIO on ledger-style (write-heavy, small-transaction) workloads; the "up to 3×" figure is read-path only.
- Did not verify pgvector's current version/features (only license) — vector search is low priority for a ledger.

---

## Key Question 3: Messaging and workflow — NATS vs Kafka vs Redpanda vs Postgres-as-queue; Temporal/Restate/DBOS/Inngest/Windmill

### Takeaway
Postgres-as-queue (River MPL-2.0 for Go, pgmq PostgreSQL-license, Graphile Worker MIT) is the smallest thing that works and keeps job state transactional with the ledger; NATS JetStream (Apache-2.0, confirmed after the May 2025 CNCF/Synadia settlement) is the free "scale later" broker; Kafka 4.x is free but heavy; Redpanda Community is **BSL** with many enterprise-gated features. For durable payment orchestration, Temporal (MIT, heavy to run) and DBOS Transact (MIT, Postgres-only, library not a server) are the free options; Restate's server is **BSL 1.1** and Inngest's server is **SSPL + delayed Apache-2.0**.

### Cited Findings

**Brokers**
- NATS server: Apache-2.0 — [nats-server LICENSE](https://github.com/nats-io/nats-server/blob/main/LICENSE). Synadia attempted in April 2025 to withdraw NATS from CNCF and move to BSL; CNCF filed a trademark petition — [The Register](https://www.theregister.com/2025/04/28/cncf_synadia_nats_dispute/). Settlement 2025-05-01: NATS stays in CNCF under Apache-2.0; Synadia assigns its two NATS trademark registrations to the Linux Foundation; CNCF keeps nats.io domain and GitHub repos; any future proprietary fork must use a new name — [CNCF announcement](https://www.cncf.io/announcements/2025/05/01/cncf-and-synadia-align-on-securing-the-future-of-the-nats-io-project/).
- NATS 2.12: atomic batch publish, counter streams (`Nats-Incr`), scheduled/delayed messages (`Nats-Schedule-TTL`), prioritized consumer mode, mirror promotion, strict-mode JetStream API; later line is v2.14.x — [nats-server releases](https://github.com/nats-io/nats-server/releases); [v2.12.0-preview.1 notes](https://newreleases.io/project/github/nats-io/nats-server/release/v2.12.0-preview.1).
- Apache Kafka 4.0 (2025-03-18): ZooKeeper removed, KRaft only; ZK clusters must migrate on 3.x first; 4.1 ships a preview of "Queues for Kafka" (KIP-932) — [Confluent: Introducing Kafka 4.0](https://www.confluent.io/en-gb/blog/apache-kafka-goes-1-0); [Kafka upgrade docs](https://apache.googlesource.com/kafka/+/be816b82d25370ceac697ccf7c88cea873e9b4e3/docs/upgrade.html).
- Redpanda Community Edition: **Business Source License**; "Users cannot provide Redpanda as a commercial streaming or queuing service to others"; converts to Apache-2.0 four years after each merge; enterprise-only list includes Audit Logging, Tiered Storage, RBAC, Continuous Data Balancing, OAUTHBEARER/OIDC, Kerberos, FIPS, Remote Read Replicas, Whole Cluster Restore, Schema Registry Authorization; new clusters get a 30-day trial (extendable to 60) — [Redpanda licensing overview](https://docs.redpanda.com/current/get-started/licensing/overview/). The RCL/BSL split has existed since at least 23.3; no 2025 relicensing event found — [Redpanda 23.3 docs](https://docs.redpanda.com/streaming/23.3/get-started/licenses.md).

**Postgres-as-queue**
- River (Go): MPL-2.0 — [LICENSE](https://raw.githubusercontent.com/riverqueue/river/HEAD/LICENSE); transactional job completion, COPY batch inserts, unique/scheduled jobs, LISTEN/NOTIFY wake-up for sub-millisecond dispatch — [riverqueue/river](https://github.com/riverqueue/river); [HN launch thread](https://news.ycombinator.com/item?id=38349716).
- pgmq: PostgreSQL License — [LICENSE](https://raw.githubusercontent.com/pgmq/pgmq/HEAD/LICENSE). Graphile Worker (Node): MIT — [LICENSE.md](https://raw.githubusercontent.com/graphile/worker/HEAD/LICENSE.md); uses LISTEN/NOTIFY — [tooling roundup](https://tooling-2ry.pages.dev/docs/categories/queues/).

**Durable workflow engines**
- Temporal server: MIT — [LICENSE](https://raw.githubusercontent.com/temporalio/temporal/HEAD/LICENSE); Postgres 12+ works for persistence and advanced visibility (no Elasticsearch needed); `NUM_HISTORY_SHARDS` is fixed at first boot — [Railway Temporal template](https://railway.com/deploy/temporal-server.md); one team's retrospective: "persistence is almost always the constraint," they moved from Postgres to Cassandra at scale — [Quo blog](https://www.quo.com/blog/postgres-to-cassandra/); vendor-blog cost estimate for a small self-hosted cluster $480–790/mo on AWS (excl. staff) — [Activepieces guide](https://www.activepieces.com/blog/temporal-self-hosted-setup-and-costs-2026-guide.md). Thoughtworks Radar Vol 35 (Nov 2026): Trial — [Radar: Restate entry](https://www.thoughtworks.com/radar/platforms/restate).
- Restate server: **BSL 1.1**, Licensor "Restate Software, Inc., Restate GmbH", Additional Use Grant: "you may not use the Licensed Work for a Public Restate Platform Service", Change Date "4 years after release", Change License Apache-2.0 — [restate LICENSE](https://github.com/restatedev/restate/blob/main/LICENSE); SDKs MIT — [Restate docs](https://docs.restate.dev/get-restate). Radar Vol 35: Trial — [Thoughtworks Radar](https://www.thoughtworks.com/radar/platforms/restate).
- DBOS Transact (Python/TS libraries): MIT; "DBOS Transact Enterprise" (Conductor, auto-upgrade) is proprietary under the DBOS Conductor Software License — [DBOS docs](https://docs.dbos.dev/explanations/transact-enterprise); argument: "replacing a central orchestrator with Postgres … makes durable workflows fundamentally simpler" — [DBOS blog](https://dbos.dev/blog/postgres-is-all-you-need-for-durable-execution); caveat: every step checkpoints to Postgres, so write throughput is the limit — [benchmark article](https://feedbagel.com/post/benchmarking-postgres-write-scalability-for-durable-workflow-execution).
- Inngest server/CLI: "Server Side Public License and delayed open source publication (DOSP) under Apache 2.0" (3-year delay); SDKs Apache-2.0 — [inngest README mirror](https://github.com/Percepta-Core/inngest); [Inngest 1.0 announcement](https://inngest.com/blog/inngest-1-0-announcing-self-hosting-support). Self-host: single binary with embedded Redis+SQLite; external Redis recommended; Postgres optional; no automatic pruning of old runs; no support guarantee for self-hosted — [Inngest self-hosting docs](https://www.inngest.com/docs/self-hosting). **Source-available (SSPL).**
- Windmill: AGPLv3 primary (plus some Apache-2.0 components); backend snippets under compile flag "enterprise" and frontend snippets requiring a license check are proprietary; binaries built without the flag are AGPLv3 — [windmill LICENSE](https://raw.githubusercontent.com/windmill-labs/windmill/HEAD/LICENSE); internal use of CE requires no commercial license — [Windmill fork README](https://github.com/apify/windmill).

### Inferences
- Smallest thing that works for payments orchestration: Postgres outbox + idempotency-key table + River (Go) or Graphile Worker (TS) with retries/backoff and unique jobs; model sagas as explicit state machines in a `payment_attempts` table. This gives exactly-once *effects* via the same transaction as the ledger write, which no external broker can.
- Scale later: NATS JetStream (single Go binary, Apache-2.0, 2.12 atomic batches) for fan-out between services; Kafka only if you need its ecosystem (Connect, Debezium) or >100k msg/s. Avoid Redpanda for a bank: audit logging, RBAC and OIDC — all compliance basics — are enterprise-gated.
- Durable execution: DBOS Transact is the KISS fit (library + your existing Postgres, MIT) if you are on Python/TS; Temporal is MIT but is a four-service cluster with sharding decided at first boot — defer until workflow complexity justifies a dedicated ops owner. Restate is technically compelling and Radar-Trial, but BSL on the server is a policy decision the team must make explicitly; it is fine for internal use (the grant only forbids offering a "Public Restate Platform Service").

### Gaps
- No head-to-head benchmark of River vs pgmq vs Graphile Worker; only vendor roundups.
- No independent small-team operational study of self-hosted Temporal; cost figures are from a competitor's blog.
- Kafka 4.1 release date and full notes not retrieved.
- DBOS Go SDK status not checked (only Python/TS confirmed MIT).

---

## Key Question 4: Platform — Compose vs k3s/k0s vs Nomad vs systemd; proxies; mTLS; mesh VPN; secrets; observability; log immutability

### Takeaway
Docker Compose on one or two VMs with Caddy (Apache-2.0) in front is the smallest thing that works; k3s (Apache-2.0) is the scale-later path (k0s is also Apache-2.0 but its `--single` mode cannot be expanded; Nomad is **BSL 1.1** since Aug 2023). Secrets: OpenBao (MPL-2.0 fork of Vault, active 2.6.x/2.7-beta in 2026). Observability: OpenTelemetry Collector (Apache-2.0) feeding Grafana LGTM (AGPLv3 — fine to self-host) or HyperDX (MIT); SigNoz is MIT with an `ee/` directory, OpenObserve is AGPLv3. Immutable audit logs: immudb is now **BSL 1.1** (1.9DOM.2+), so prefer an in-Postgres hash chain plus WORM object storage.

### Cited Findings

**Orchestration**
- k3s: Apache-2.0 — [LICENSE](https://raw.githubusercontent.com/k3s-io/k3s/HEAD/LICENSE). k0s: Apache-2.0 (docs CC-BY-SA-4.0) — [LICENSE](https://raw.githubusercontent.com/k0sproject/k0s/HEAD/LICENSE). k0s `--single` "gives you a single-node cluster that cannot be expanded later"; k3s bundles Traefik and runs at ~512 MB RAM/1 vCPU for a server node — [k3s vs k0s](https://selfhosting-sh.pages.dev/compare/k3s-vs-k0s/); one DEV post argues single-node k3s + Velero backups is lower-effort than Compose at the same scale, another says Compose is "simpler and sufficient" until 10+ services or multi-node — [DEV: k3s vs microk8s vs k0s 2026](https://dev.to/pickuma/k3s-vs-microk8s-vs-k0s-lightweight-kubernetes-for-small-teams-in-2026-4bl9); [Docker Swarm comparison](https://dev.to/selfhostingsh/k3s-vs-docker-swarm-4kog). (All blog sources.)
- Nomad: **BSL 1.1**, Change License MPL-2.0, four years after publication; grant forbids offering the work "on a hosted or embedded basis to compete with IBM Corp's paid versions" — [nomad LICENSE](https://raw.githubusercontent.com/hashicorp/nomad/HEAD/LICENSE). HashiCorp announced the MPL-2.0 → BSL 1.1 move on 2023-08-10 for "all future releases of HashiCorp products" — [HashiCorp blog](https://www.hashicorp.com/blog/hashicorp-adopts-business-source-license). **Source-available.**

**Reverse proxies / TLS / mTLS**
- Caddy: Apache-2.0 — [LICENSE](https://raw.githubusercontent.com/caddyserver/caddy/HEAD/LICENSE). Traefik: MIT — [LICENSE.md](https://raw.githubusercontent.com/traefik/traefik/HEAD/LICENSE.md). step-ca (smallstep/certificates): Apache-2.0 — [LICENSE](https://raw.githubusercontent.com/smallstep/certificates/HEAD/LICENSE).

**Mesh VPN**
- Headscale: BSD-3-Clause — [LICENSE](https://raw.githubusercontent.com/juanfont/headscale/HEAD/LICENSE). NetBird: BSD-3-Clause except `management/`, `signal/`, `relay/`, `combined/` which are AGPLv3 — [LICENSE](https://raw.githubusercontent.com/netbirdio/netbird/HEAD/LICENSE).

**Secrets**
- OpenBao (Vault fork under Linux Foundation): 2.7.0-beta (2026-09-09) adds external KMS-backed keys for PKI/Transit, moves Kerberos/LDAP/RADIUS engines to external plugins, discontinues the HSM distribution, moves Go module to `/v2`; 2.6.2–2.6.4 shipped Jun–Oct 2026 with GHSA fixes; a Kubernetes OpenBao Operator 0.1.0 released 2026-03-30 — [OpenBao 2.7.0 release notes](https://openbao.org/community/release-notes/2-7-0/); [Fedora advisory 2.6.4](https://linuxsecurity.com/advisories/fedora/fedora-44-openbao-2026-73f5dc988f); [openbao-operator 0.1.0](https://dc-tec.github.io/openbao-operator/releases/0.1.0). (OpenBao's own MPL-2.0 LICENSE was not fetched; see Gaps.)

**Observability**
- Grafana, Loki, Tempo relicensed Apache-2.0 → AGPLv3 on 2021-04-20 — [Grafana licensing](https://grafana.com/licensing/); Loki AGPLv3 — [LICENSE](https://raw.githubusercontent.com/grafana/loki/HEAD/LICENSE); Mimir AGPLv3 — [LICENSE](https://raw.githubusercontent.com/grafana/mimir/HEAD/LICENSE); Alloy (collector) Apache-2.0 — [LICENSE](https://raw.githubusercontent.com/grafana/alloy/HEAD/LICENSE).
- SigNoz: "MIT Expat" outside `ee/` and `cmd/enterprise/` (those under `ee/LICENSE`) — [SigNoz LICENSE](https://github.com/SigNoz/signoz/blob/main/LICENSE). HyperDX: MIT — [LICENSE](https://raw.githubusercontent.com/hyperdxio/hyperdx/HEAD/LICENSE). OpenObserve: AGPLv3 — [LICENSE](https://raw.githubusercontent.com/openobserve/openobserve/HEAD/LICENSE).

**Immutable / tamper-evident logs**
- immudb 1.9DOM.2 and later: **BSL 1.1**, Licensor Codenotary; production use allowed unless "offering the Licensed Work to third parties on a hosted or embedded basis"; Change License Apache-2.0 four years after publication — [immudb LICENSE](https://github.com/codenotary/immudb/blob/master/LICENSE). Older releases were Apache-2.0; built-in `--audit-log` stores JSON audit events in the tamper-proof KV store — [immudb audit log docs](https://docs.immudb.io/master/develop/sql/auditlog). **Source-available for current versions.**

### Inferences
- Smallest thing that works: two Hetzner/Oracle VMs, Docker Compose (app, Postgres, Caddy, OTel Collector, Grafana+Loki+Tempo+Mimir or HyperDX), systemd units for Compose stacks, Headscale for operator access, OpenBao in dev mode → single-node Raft. Caddy's automatic TLS and simple config beat nginx/Traefik for a small team; Traefik wins only once you adopt k3s (it ships bundled).
- Scale later: k3s single-node → three-node, CNPG for Postgres, cert-manager + step-ca for internal mTLS, Cilium only if you need network policy/eBPF observability. Nomad is out on license grounds for a team that wants to stay OSI-free.
- Log immutability: keep audit events in an append-only Postgres table with a per-row SHA-256 hash chain (prev_hash, row_hash) and ship daily Merkle roots/segments to WORM/object-lock storage; immudb's BSL and the added stateful service are not worth it for a small team.
- AGPL is acceptable for internal observability (no distribution/SaaS obligation triggered by running Grafana for your own ops); document that as a conscious policy.

### Gaps
- OpenBao's own LICENSE file, cert-manager, Cilium, OTel Collector, nginx licenses were not fetched (GitHub API was blocked in this session); they are widely documented as MPL-2.0 / Apache-2.0 / Apache-2.0 / Apache-2.0 / BSD-2 but this is unverified here.
- No 2026 primary comparison of Caddy vs Traefik vs nginx; search budget exhausted before that query ran.
- No evidence collected on SigNoz `ee/LICENSE` contents or which features it gates.

---

## Key Question 5: CI/CD and dev tooling that can be self-hosted free

### Takeaway
Forgejo (GPLv3) with Forgejo Actions (GitHub-Actions-compatible YAML) or Woodpecker CI (Apache-2.0) is the standard free self-hosted forge+CI in 2026; Gitea remains MIT. Earthly is end-of-life (maintenance ended April 2025; community fork EarthBuild, MPL-2.0); Dagger (Apache-2.0) is the maintained container-native build tool. mise (MIT), just (CC0), Renovate (AGPLv3), cosign/Syft (Apache-2.0), and Flagsmith (BSD-3) / GrowthBook (MIT + enterprise dirs) are all free; Unleash is now AGPLv3.

### Cited Findings
- Forgejo: GPLv3 — [LICENSE](https://codeberg.org/forgejo/forgejo/raw/branch/forgejo/LICENSE). Gitea: MIT — [LICENSE](https://raw.githubusercontent.com/go-gitea/gitea/HEAD/LICENSE). Woodpecker CI: Apache-2.0 — [LICENSE](https://raw.githubusercontent.com/woodpecker-ci/woodpecker/HEAD/LICENSE).
- Forgejo Actions uses GitHub-Actions-compatible YAML but "the actions/* marketplace isn't mirrored 1:1"; Woodpecker (Drone fork after Drone's license change) uses its own `.woodpecker.yml`, so pipelines must be rewritten; Forgejo + Woodpecker + runner fit on a 4 GB VPS — [nodatools review](https://nodatools.pages.dev/blog/self-hosted-ci-alternative-to-github-actions); [Forgejo Actions on Proxmox 2026](https://www.gilricardo.com/blog/forgejo-actions-proxmox-self-hosted-git-ci-2026); [bitdoze Forgejo+Woodpecker](https://www.bitdoze.com/forgejo-woodpecker-ci-cicd/).
- Earthly: 2025-04-16 post — "Earthly Cloud, including Satellites, will stop working on July 16th, 2025"; "ending active maintenance of the open-source project," only critical fixes, no PRs; supports a community fork that must rename; Dagger offered a migration path (both use BuildKit) — [Earthly shutdown post](https://earthly.dev/blog/shutting-down-earthfiles-cloud/). Earthly LICENSE is MPL-2.0 — [LICENSE](https://raw.githubusercontent.com/earthly/earthly/HEAD/LICENSE); EarthBuild fork is MPL-2.0 and renames CLI `earthly` → `earth`, env vars `EARTHLY_*` → `EARTHBUILD_*` — [EarthBuild LICENSE](https://raw.githubusercontent.com/EarthBuild/earthbuild/HEAD/LICENSE); [EarthBuild migration PR](https://gh.r-mj.com/EarthBuild/earthbuild/pull/14).
- Dagger: Apache-2.0 — [LICENSE](https://raw.githubusercontent.com/dagger/dagger/HEAD/LICENSE). mise: MIT — [LICENSE](https://raw.githubusercontent.com/jdx/mise/HEAD/LICENSE). just: CC0-1.0 — [LICENSE](https://raw.githubusercontent.com/casey/just/HEAD/LICENSE). Renovate: AGPLv3 — [license](https://raw.githubusercontent.com/renovatebot/renovate/HEAD/license).
- Feature flags: Unleash main repo AGPLv3 — [LICENSE](https://github.com/Unleash/unleash/blob/main/LICENSE) (older roundups still say Apache-2.0; the LICENSE file is authoritative). Flagsmith: BSD-3-Clause (Bullet Train Ltd) — [LICENSE.md](https://raw.githubusercontent.com/Flagsmith/flagsmith/HEAD/LICENSE.md). GrowthBook: "MIT Expat" except `packages/back-end/src/enterprise`, `packages/front-end/enterprise`, `packages/shared/src/enterprise` under the GrowthBook Enterprise License — [LICENSE](https://raw.githubusercontent.com/growthbook/growthbook/HEAD/LICENSE).
- GitHub Codespaces free allowance: Free personal accounts 120 compute hours + 15 GB-month storage; Pro 180 hours + 20 GB; $0.09 per core-hour thereafter (a 2-core machine consumes 2 units/hour) — [GitHub Docs: Codespaces billing](https://docs.github.com/en/billing/concepts/product-billing/github-codespaces).

### Inferences
- Smallest thing that works: a single Forgejo instance (GPLv3 is irrelevant for internal use) with Forgejo Actions runners, `just` + `mise` for local/CI parity, Renovate self-hosted on a cron, Dagger only if build logic outgrows a Makefile/justfile. Sign images with cosign keyless or a self-hosted key in OpenBao, generate SBOMs with Syft in CI, and store attestations in the forge's OCI registry.
- Feature flags: Flagsmith (BSD-3, no enterprise directory ambiguity) or OpenFeature SDK over a flags table in Postgres (zero extra services) for a bank's typical dozen flags. GrowthBook is fine if experimentation matters; Unleash's AGPL is acceptable for internal use but note the change.
- Do not adopt Earthly; if an Earthfile exists, move to EarthBuild or Dagger.

### Gaps
- cosign/Syft/SLSA licenses and 2026 status were not fetched (search budget exhausted); they are widely documented as Apache-2.0 but unverified here.
- Nix/devenv status and license not verified.
- Woodpecker's current version/maturity in 2026 and Forgejo 16's runner changes were only seen in tutorials, not release notes.

---

## Key Question 6: Compliance tooling — audit chains, immudb, retention, backups, encryption, KYC/AML, notifications, email, documents

### Takeaway
Free OSS covers backups (restic BSD-2, Kopia Apache-2.0), notifications (ntfy Apache-2.0, Listmonk AGPLv3), and document generation (Typst Apache-2.0, Gotenberg MIT). KYC/AML is the weakest free layer: Marble is **ELv2** (source-available), Hummingbird is paid-only SaaS, and **OpenSanctions bulk data is CC-BY-NC — commercial compliance screening requires a paid data license**. immudb is BSL for current versions; a Postgres hash chain is the free path.

### Cited Findings
- Backups: restic BSD-2-Clause — [LICENSE](https://raw.githubusercontent.com/restic/restic/HEAD/LICENSE); Kopia Apache-2.0 — [LICENSE](https://raw.githubusercontent.com/kopia/kopia/HEAD/LICENSE). Postgres-native: pgBackRest MIT (see Q2); PG17 incremental `pg_basebackup`/`pg_combinebackup` — [PostgreSQL 17 press kit](https://www.postgresql.org/about/press/presskit17/).
- Encryption at rest: PG18 enables data checksums by default (integrity, not confidentiality) — [PostgreSQL 18 press kit](https://www.postgresql.org/about/press/presskit18/); OpenBao 2.7 adds KMS-backed external keys for Transit (envelope encryption) — [OpenBao 2.7.0 notes](https://openbao.org/community/release-notes/2-7-0/).
- Audit log immutability: immudb ≥1.9DOM.2 BSL 1.1 (see Q4) — [immudb LICENSE](https://github.com/codenotary/immudb/blob/master/LICENSE).
- KYC/AML: Marble (Checkmarble) — Elastic License 2.0: "You may not provide the software to third parties as a hosted or managed service," may not bypass license keys — [marble LICENSE](https://github.com/checkmarble/marble/blob/main/LICENSE); OSS core = transaction monitoring, sanctions screening, case management, API ingestion; enterprise = SSO, IP allowlisting, advanced audit logs, AI case review, analytics — [awesome.ecosyste.ms listing](https://awesome.ecosyste.ms/projects/github.com%2Fcheckmarble%2Fmarble); raised €6.5M Series A (2026-09-30) — [Renascence](https://www.renascence.io/news/81199/marble-raises-6-5m-series-a-for-open-source-aml-infrastructure). **Source-available.**
- Hummingbird: paid-only, quote-based enterprise pricing; no OSS — [GetApp](https://www.getapp.sg/software/2052017/hummingbird-1); [Neuronfeed](https://neuronfeed.com/compare/hummingbird-vs-quantexa).
- OpenSanctions: database is CC-BY-NC 4.0; "compliance screening is a commercial use even though it generates no revenue"; any use inside a for-profit business requires a data license; free API keys only for journalism/nonprofit/academic public-interest work; "commercial options scale down to low-volume use" — [OpenSanctions: free and non-commercial use](https://www.opensanctions.org/docs/commercial/exemption/); [FAQ: paying for use](https://www.opensanctions.org/faq/commercial/paying-for-use/).
- Notifications: ntfy LICENSE file is Apache-2.0 — [LICENSE](https://raw.githubusercontent.com/binwiederhier/ntfy/HEAD/LICENSE). Novu: maintainer says Community edition is MIT with EE modules excluded; a user hit a 20-workflow limit in the Docker community build — [Novu Discord/answeroverflow thread](https://www.answeroverflow.com/m/1400454976802787501) (informal; LICENSE not fetched — see Gaps).
- Email: Listmonk AGPLv3 — [LICENSE](https://raw.githubusercontent.com/knadh/listmonk/HEAD/LICENSE). Postal: LICENSE not fetched (404) — see Gaps.
- Documents: Typst Apache-2.0 — [LICENSE](https://raw.githubusercontent.com/typst/typst/HEAD/LICENSE); Gotenberg MIT — [LICENSE](https://raw.githubusercontent.com/gotenberg/gotenberg/HEAD/LICENSE).

### Inferences
- Smallest thing that works: pgBackRest (full nightly + WAL archive) to S3-compatible object storage with object lock, plus restic for file-level backups of config/OpenBao snapshots; a quarterly restore drill. Hash-chained `audit_events` table in Postgres, daily anchor to WORM storage. Typst for statements/letters (deterministic, fast, no browser), Gotenberg when HTML→PDF fidelity is needed. ntfy for ops paging; Listmonk or a plain SMTP relay for customer email.
- KYC/AML: budget for a paid sanctions data license (OpenSanctions commercial, low-volume tier) from day one — it is not free for a bank. Run screening logic in-house (simple name matching + review queue) before adopting Marble; if adopting Marble, record the ELv2 decision (fine for internal use).
- Retention: use PG partitioning by month on events/audit tables to implement retention windows by dropping/archiving partitions rather than deleting rows.

### Gaps
- Novu, Postal, Formance LICENSE files could not be fetched (404 on raw URLs); Novu's MIT-with-EE claim is from a maintainer chat message only.
- No source found for OSS KYC document-verification (ID/OCR/liveness) tools; the free layer appears to be absent and this remains a vendor purchase.
- ntfy README reportedly dual-licenses Apache-2.0/GPL-2.0, but only the Apache-2.0 LICENSE file was verified.
- Data-retention regulatory specifics (e.g., 5/7-year rules) were out of scope and not researched.

---

## Key Question 7: Free hosting realities — demo/dev on free tiers, honest staging cost, fully local via Compose

### Takeaway
Fly.io has no free tier for new organizations (a $1/7-day trial only); Oracle Cloud's Always Free Ampere A1 (4 OCPU/24 GB, capacity permitting) is the only genuinely free always-on VM large enough to run a full demo stack; Hetzner raised prices three times in 2026 and its cost-optimized CX/CAX lines have been intermittently unorderable, so the honest minimum for a staging environment is roughly €15–35/month (two small VMs + IPv4 + object storage), and everything can run locally with Docker Compose.

### Cited Findings
- Fly.io: "New organizations don't have a free tier or a monthly free usage allowance"; trial is $1 credit for 7 days, no card needed; cheapest Machine `shared-cpu-1x` 256 MB = $2.19/month; volumes $0.15/GB/month; Managed Postgres from $38/month + $0.28/GB; unmanaged Fly Postgres ≈$2/month single-node dev, $82–164/month for 3-node; no platform fee, support tiers $29/$199/$2,500 — [Fly.io pricing docs](https://docs.fly.io/about/pricing).
- Oracle Cloud Always Free: Ampere A1 total of 4 OCPUs and 24 GB RAM, usable as 1–4 VMs; plus 2 AMD micro instances, 200 GB block storage, 10 GB object storage; capacity frequently unavailable; account-level cap prevents a second 4-OCPU instance — [Medium setup guide 2026](https://medium.com/@imvinojanv/setup-always-free-vps-with-4-ocpu-24gb-ram-and-200gb-storage-the-ultimate-oracle-cloud-guide-bed5cbf73d34); [fosshost summary](https://fosshost.com/hosts/oracle-free-tier/). (No official Oracle page retrieved; third-party sources only.)
- Hetzner 2026: price rises of ~30–37% on 2026-04-01 — [Tom's Hardware](https://www.tomshardware.com/tech-industry/hetzner-to-raise-prices-by-up-to-37-percent-from-april-1); a further adjustment effective 2026-06-15 08:00 CEST for new orders/rescales (existing instances keep price); CX23 €3.99→€5.49, CAX11 €4.49→€5.99 — [privatedevops](https://privatedevops.com/news/hetzner-june-2026-cloud-price-increase-what-to-do); [wz-it](https://wz-it.com/en/blog/hetzner-price-increase-june-2026-cpx-ccx-alternatives/); IPv4 €0.50/month extra — [betterstack review](https://betterstack.com/community/guides/web-servers/hetzner-cloud-review/). Hetzner's cloud page today: Cost-Optimized "from €5.99/month" marked "Currently not available"; Regular Performance from €6.49/month; General Purpose from €43.49/month — [hetzner.com/cloud](https://www.hetzner.com/cloud/). Reports that CX/CAX "Create" was disabled on several dates Sep–Oct 2026 — [bitdoze](https://www.bitdoze.com/es/hetzner-cloud-precios-2026/) (unverified).
- GitHub Codespaces: 120 core-hours + 15 GB free per month on Free personal accounts; $0.09/core-hour — [GitHub Docs](https://docs.github.com/en/billing/concepts/product-billing/github-codespaces).
- Inngest self-host demonstrates the local pattern: single binary with embedded Redis + SQLite, Compose example with Postgres 17 + Redis 7 — [Inngest self-hosting docs](https://www.inngest.com/docs/self-hosting). Forgejo + Woodpecker + runner on a 4 GB VPS — [nodatools](https://nodatools.pages.dev/blog/self-hosted-ci-alternative-to-github-actions).

### Inferences
- Fully local: a `docker compose` stack of Postgres 18, the app, Caddy, OTel Collector + HyperDX (or Grafana LGTM), ntfy, Forgejo (optional), and a Gotenberg/Typst container runs on a 16 GB laptop; this is the recommended day-one dev environment and the demo environment.
- Free demo in the cloud: Oracle A1 (if capacity is obtainable) runs the same Compose stack 24/7 at $0; treat it as disposable (Oracle's terms and capacity are not contractual). Fly.io is not free; Hetzner has no free tier.
- Honest staging minimum (late 2026, excluding domains/email): Hetzner Regular Performance 2 vCPU/4 GB ≈ €6.49 + IPv4 €0.50, a second node for Postgres standby ≈ €6.49 + €0.50, object storage for backups ≈ €5 → ~€19–20/month; add a 4-core node for CI runners and you are at ~€35/month. A three-node k3s staging with CNPG lands at €40–60/month. Fly.io equivalent: ~$2.19×3 machines + $82 unmanaged 3-node Postgres ≈ $90/month, or $38 managed single-node Postgres.
- Codespaces free hours (120 core-hours) are enough for a solo developer's part-time use but not for a team or CI.

### Gaps
- No official Oracle Always Free page retrieved; allocation details are from 2026 blog guides.
- Hetzner's official price-adjustment page returned 404; figures are from third-party reporting of that page and should be re-checked before budgeting.
- Searches for Railway/Render/Koyeb 2026 free tiers and for Oracle A1 availability statistics were not run (search budget exhausted).

---

## Cross-cutting license register (verified 2026-11-04 unless noted)

| Layer | Tool | License | Status |
|---|---|---|---|
| Broker | NATS server | Apache-2.0 | OSI-free; BSL attempt reversed May 2025 |
| Broker | Apache Kafka | Apache-2.0 (not fetched; ASF) | OSI-free |
| Broker | Redpanda CE | **BSL 1.1** (+RCL enterprise) | Source-available; audit log/RBAC/OIDC gated |
| Queue | River | MPL-2.0 | OSI-free |
| Queue | pgmq | PostgreSQL | OSI-free |
| Queue | Graphile Worker | MIT | OSI-free |
| Workflow | Temporal | MIT | OSI-free |
| Workflow | DBOS Transact | MIT (Enterprise/Conductor proprietary) | OSI-free core |
| Workflow | Restate server | **BSL 1.1** → Apache-2.0 after 4y; SDKs MIT | Source-available |
| Workflow | Inngest server | **SSPL + DOSP Apache-2.0 (3y)**; SDKs Apache-2.0 | Source-available |
| Workflow | Windmill | AGPLv3 (+proprietary enterprise snippets) | OSI-free core |
| Event store | KurrentDB (ex-EventStoreDB) | **Kurrent License v1** (ESLv2 in 24.10, KLv1 in 25.0) | Source-available |
| Event store | Marten | MIT | OSI-free |
| Ledger | TigerBeetle | Apache-2.0 | OSI-free |
| Ledger | Formance Ledger | MIT or Apache-2.0 (unverified) | Gap |
| DB | PostgreSQL / pgvector / pgmq | PostgreSQL | OSI-free |
| DB HA | Patroni / pgBackRest | MIT | OSI-free |
| DB HA | CloudNativePG | Apache-2.0 | OSI-free |
| DB | libSQL / Turso DB | MIT | OSI-free (Turso DB beta) |
| Analytics | ClickHouse | Apache-2.0 | OSI-free |
| Orchestration | k3s / k0s | Apache-2.0 | OSI-free |
| Orchestration | Nomad | **BSL 1.1** → MPL-2.0 after 4y | Source-available (Aug 2023) |
| Proxy | Caddy / Traefik | Apache-2.0 / MIT | OSI-free |
| PKI | step-ca | Apache-2.0 | OSI-free |
| VPN | Headscale | BSD-3 | OSI-free |
| VPN | NetBird | BSD-3 + AGPLv3 (management/signal/relay) | OSI-free |
| Secrets | OpenBao | MPL-2.0 (not fetched) | OSI-free |
| Obs | Grafana/Loki/Tempo/Mimir | AGPLv3 | OSI-free (copyleft) |
| Obs | Grafana Alloy | Apache-2.0 | OSI-free |
| Obs | SigNoz | MIT Expat + `ee/` proprietary | OSI-free core |
| Obs | HyperDX | MIT | OSI-free |
| Obs | OpenObserve | AGPLv3 | OSI-free (copyleft) |
| Audit | immudb ≥1.9DOM.2 | **BSL 1.1** → Apache-2.0 after 4y | Source-available |
| Forge | Forgejo / Gitea | GPLv3 / MIT | OSI-free |
| CI | Woodpecker | Apache-2.0 | OSI-free |
| Build | Dagger | Apache-2.0 | OSI-free |
| Build | Earthly / EarthBuild | MPL-2.0 | **EOL Apr 2025** / community fork |
| Dev | mise / just / Renovate | MIT / CC0 / AGPLv3 | OSI-free |
| Flags | Flagsmith / GrowthBook / Unleash | BSD-3 / MIT+enterprise dirs / AGPLv3 | OSI-free |
| AML | Marble | **ELv2** | Source-available |
| AML | Hummingbird | Proprietary SaaS | Paid only |
| Sanctions data | OpenSanctions | **CC-BY-NC 4.0** (commercial license required) | Paid for banks |
| Notify | ntfy / Novu | Apache-2.0 / MIT (unverified) | OSI-free / gap |
| Email | Listmonk / Postal | AGPLv3 / unverified | OSI-free / gap |
| Docs | Typst / Gotenberg | Apache-2.0 / MIT | OSI-free |
| Backup | restic / Kopia | BSD-2 / Apache-2.0 | OSI-free |

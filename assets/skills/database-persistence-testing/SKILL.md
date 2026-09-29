---
name: database-persistence-testing
description: "Trigger: database testing, migration testing, schema migration, up down migration, zero-downtime, expand contract, database concurrency, deadlock test, isolation level, N+1 query budget, foreign key index audit. Test data persistence, migrations, and transactional invariants."
license: Apache-2.0
metadata:
  author: "alesierraalta"
  version: "1.2"
---

## Activation Contract

Load when testing migrations, persistence, transaction boundaries, concurrency anomalies, or query performance: "test migration up/down", "zero-downtime migration", "deadlock detection", "database race condition", "N+1 detection", "missing FK index", "isolation level test".

NOT for: purely in-memory data structures, mock-only repository unit tests with no real SQL, or static architecture layer checks (`clean-architecture-audit`).

## Preflight Safety Gate

| Target | Requirement |
|---|---|
| Local isolated: throwaway worktree or ephemeral database container, synthetic data, no shared network, credentials, or tenants | Proceed. The sandbox is the authorization; record it in the plan. |
| Shared, remote, staging with real data, multi-tenant, or production database | All items in `~/.claude/skills/exploit-testing/references/safety-gate.md` (persistence subsection) are required. Never run destructive probes against a production database; an ephemeral container alone is not authorization here. If any item is missing, stop at planning and report the gap. |

## Plan Contribution

When invoked by `test-strategy` in PLAN mode: do not execute. Return target rows for the plan:
target (migration file, repository, query, or table) · check (this skill's domain check) ·
target rung or depth · consequence class · why. Cover every check this skill would run on this
codebase, including the cheap static ones: migration file naming and ordering, up/down presence,
idempotency, index/FK audit ([references/invariants.md](references/invariants.md)).

## Hard Rules

Full text and rationale: [references/invariants.md](references/invariants.md).

1. **Migration invariants are contract-dependent**: reversible → verify the rollback cycle and schema equivalence; irreversible expand/contract → verify compatibility and the documented rollback/restore path. Never pretend `DOWN` is valid when it is not. Missing required invariant fails closed.
2. **Zero-downtime expand/contract**: never drop, rename, or add a non-nullable column without default in one migration; split into expand, backfill, contract.
3. **Lock and latency budget** per migration, with provenance; missing budget or telemetry fails closed.
4. **No BDD / Cucumber on SQL tables**: domain builders, table-driven tests, real ephemeral databases.
5. **Concurrency and isolation**: real parallel connections at the declared workload; assert the applicable race and locking invariant.
6. **Query budget** declared with provenance and enforced with native counters; a missing applicable budget is a blocker.
7. **Foreign key index audit** under the active dialect and workload; unverified required index fails closed.
8. **Teardown and isolation**: isolated transactions with rollback, or clean containers per suite.
9. **Evidence**: every finding carries an executed evidence record per `~/.claude/skills/test-strategy/references/evidence.md`; no finding from reading alone.

## Decision Gates

| Change Scope | Required Testing Altitude | Enforcement / Metric |
| :--- | :--- | :--- |
| **New / modified migration** | Static checks (naming, order, up/down, idempotency) plus contract-selected reversibility or compatibility and lock audit | Declared invariant passes; `squawk` findings evaluated against the target's budget |
| **Breaking schema change** | Expand / contract compatibility verification | Compatibility and rollback or backup/restore evidence meet the declared contract |
| **Repository / query changes** | Query budget and N+1 assertion | Observed behavior meets the configured budget for the sampled workload |
| **Concurrent mutations / locks** | Declared parallelism and isolation probe | Observed anomalies and recovery meet the documented contract |
| **Schema integrity audit** | Dialect- and workload-aware FK index verification | Every applicable index requirement evidenced or explicitly excepted |

## Execution Steps

1. **Ephemeral test database**: the production dialect and major version in an isolated container (`docker-test-containers`). An in-process SQLite stand-in is not valid for PostgreSQL/MySQL semantics (locking, isolation, DDL, types); use it only when SQLite is the production engine.
2. **Static migration checks**: naming, ordering, up/down presence, idempotency (`assets/migration-idempotency-check.sh --mode reversible`, explicit `--target` and argv arrays).
3. **Contract-selected migration verification**: reversible → baseline/rollback/UP schema dumps compared; irreversible → old and new application compatibility plus the documented restore path. Record dialect, operation class, invariant selection, exceptions.
4. **Static DDL lock inspection**: `squawk` (default) on migration files, or the manual lock checklist in `references/patterns.md` section 5 when it is unavailable.
5. **Query budget and N+1 assertions** with query logging interceptors or framework counters.
6. **Concurrency and isolation suite**: parallel workers on shared state; assert the expected isolation behavior (anomaly table and retry contract: `references/patterns.md` section 4).
7. **Foreign key index audit** via catalog query (`references/patterns.md`).
8. **Backup restore**, when backups are in scope: restore into a scratch instance and query it; a dump exit code is not evidence.
9. **Clean teardown** of containers and volumes.

## Output Contract

Report: migration verification (invariant, evidence, rollback path, budget with provenance) · static checks (naming, order, up/down, idempotency) · DDL lock safety · query budgets (measured vs bound) · concurrency and locks (workload, anomalies, retry behavior) · FK index audit (constraints, evidence, exceptions) · verdict `PASSED` | `FAILED` with reproduction, SQL logs, and the evidence record per finding. RDD receipt when required: [references/rdd-receipt.md](references/rdd-receipt.md).

## References

- [references/invariants.md](references/invariants.md) — full rule text and static migration checks.
- [references/patterns.md](references/patterns.md) — SQL patterns for expand/contract, concurrency locks, FK audits, isolation anomalies, lint and restore.
- [references/rdd-receipt.md](references/rdd-receipt.md) — receipt contract for `lens:persistence`.
- `assets/migration-idempotency-check.sh` — reversible-only up/down/up canonical schema-dump check; explicit commands and target, no ambient defaults.
- `assets/unindexed-foreign-keys.sql` — PostgreSQL query for FKs without a valid leading-column index.

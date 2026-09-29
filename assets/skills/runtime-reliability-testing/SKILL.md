---
name: runtime-reliability-testing
description: "Trigger: runtime testing, load testing, chaos testing, stress test, fault injection, p95 latency, circuit breaker, k6, toxiproxy, schemathesis, smoke test. Prove running code under operational hostility."
license: Apache-2.0
metadata:
  author: "alesierraalta"
  version: "1.1"
---

## Activation Contract

Load to PROVE running code under operational hostility: saturation, network degradation, dependency outages, contract fuzzing, post-deploy smoke. "Does this hold under load", "verify circuit breaker/retries", "test network failure", "measure p95/p99", "detect memory leaks", "run smoke test".

NOT for: static unit tests (`test-strategy`), pruning unit test suites (`no-excess-tests`), single manual execution of a happy-path function (`real-run-validation`).

## Preflight Safety Gate

| Target | Requirement |
|---|---|
| Local isolated: throwaway worktree or ephemeral containers on an isolated bridge network, synthetic data, no shared network, credentials, or tenants | Proceed. The sandbox is the authorization; record it in the plan. Load and chaos still carry bounded duration, concurrency, and resource limits. |
| Shared, remote, staging with real data, multi-tenant, or production | All items in `~/.claude/skills/exploit-testing/references/safety-gate.md` (runtime subsection) are required. Never load-test or inject faults into shared environments without owner approval and egress limits; an ephemeral container alone is not authorization here. If any item is missing, stop at planning and report the gap. |

## Plan Contribution

When invoked by `test-strategy` in PLAN mode: do not execute. Return target rows for the plan:
target (endpoint, dependency seam, journey) · check (latency SLO, soak, saturation, breaker
transition, network fault, contract fuzz, smoke) · target rung or depth · consequence class ·
why. Cover every check this skill would run on this codebase, including cheap ones (smoke
journey, Schemathesis against an existing OpenAPI spec).

## Hard Rules

1. **No probe without a FALSIFIABLE CLAIM**: "If <invariant/latency/threshold> violates <bound>, this probe goes RED". A probe that cannot go red is decorative.
2. **Execute against the REAL RUNNING ARTIFACT**: real services in ephemeral containers (`docker-test-containers`); never mock the seam being validated. If isolation cannot be provided, record the surface as unreachable.
3. **No coordinated omission**: load tests use arrival-rate / open workload models (`references/patterns.md`); closed loops must state the omission bias. Closed-model tools (autocannon, `wrk`, fixed-VU loops) are allowed only for an explicitly closed-model question (a connection-pool or fixed-client ceiling), labelled as such and never used for latency SLOs or capacity numbers. Offered arrival rate and completed throughput are separate metrics, and timed-out requests stay in the percentile denominator, censored at the timeout (`references/patterns.md`).
4. **Assert on OBSERVED TELEMETRY, not exit codes**: database state, wire payloads, Toxiproxy logs, RSS/heap trends, breaker counters.
5. **Blast radius containment**: chaos injection is scoped to test-owned ephemeral containers on isolated networks; approved exceptions keep the same bounded limits and stop contract.
6. **Teardown is mandatory**: every injected fault is removed in a `finally` hook, even on assertion failure; only test-owned faults, never pre-existing toxics.
7. **Evidence**: every conclusion is `observado` (backed by metrics/logs) or `razonado`; every finding carries an executed evidence record per `~/.claude/skills/test-strategy/references/evidence.md`; no finding from reading alone.

## Decision Gates

| What needs to be proven | Primary Tool | Technique / Pattern | Stop Gate / Success Metric |
| :--- | :--- | :--- | :--- |
| Latency compliance under concurrency | **k6** | Open workload arrival-rate (`assets/k6-arrival-rate-template.js`) | Target-declared latency/error SLO with provenance, sample context, tolerance |
| Memory leaks and endurance | **k6 + pprof** | Target-configured soak duration and load profile | Target-declared RSS/heap trend criterion |
| Breaking throughput limit | **k6** | Step-up `ramping-arrival-rate` stages, open workload | Target-declared saturation criterion; `dropped_iterations` aborts the run |
| Circuit breaker transitions | **Toxiproxy** | `latency` and drop toxics (`assets/toxiproxy-breaker-test.py`) | Declared state-transition, timeout, fail-fast contract |
| Network drops and corruption | **Toxiproxy** | `reset_peer`, `packet_loss`, `slicer` | Target-declared idempotency and retry contract |
| API contract robustness | **Schemathesis** | Property fuzzing against OpenAPI | Target-declared error and schema conformance |
| Post-deploy golden path | **Playwright** | Headless API and browser journey | Target-declared duration and critical-flow criterion |

## Execution Steps

1. **Falsifiable claim**: target-owned SLOs/bounds with provenance, sample size, tolerance, measurement conditions, semantic oracle. Missing evidence is UNVERIFIED, never a pass.
2. **Isolated environment**: SUT and dependencies via `docker-test-containers` on an isolated bridge network; Toxiproxy on the seams.
3. **Test driver**: k6 script, Toxiproxy runner, or Schemathesis invocation.
4. **Baseline run** under zero-fault conditions: p50, p95, RPS, error rate.
5. **Inject load or hostility**; capture RPS, latency percentiles, memory growth, error rates continuously.
6. **Verify invariant and recovery**: fail-fast, fallback, retries; remove hostility and confirm return to baseline.
7. **Teardown**: remove toxics; terminate only owned, labelled containers and volumes; report skipped ambiguous items.
8. **Synthesize**: telemetry table, root causes, minimal reproduction commands, evidence record per finding.

## Output Contract

Report: falsifiable invariant table (claim | bound and provenance | observed | conditions | PASS/FAIL/BLOCKED) · telemetry summary (percentiles, peak RPS, error rate, memory trend) · fault analysis (injection timestamps, breaker transitions, fallback) · defects with minimal reproduction, classification (`[Memory Leak]`, `[Coordinated Omission]`, `[Cascading Timeout]`, `[Contract Violation]`, `[Unbounded Retry Storm]`), and evidence record. RDD receipt when required: [references/rdd-receipt.md](references/rdd-receipt.md).

## References

- [references/patterns.md](references/patterns.md) — coordinated omission, tail latency amplification, jitter math.
- [references/rdd-receipt.md](references/rdd-receipt.md) — receipt contract for `lens:reliability`.
- [assets/k6-arrival-rate-template.js](assets/k6-arrival-rate-template.js) — k6 open workload script.
- [assets/toxiproxy-breaker-test.py](assets/toxiproxy-breaker-test.py) — Toxiproxy circuit breaker test.
- Sibling skills: `test-strategy` · `docker-test-containers` · `exploit-testing` · `real-run-validation`.

---
name: runtime-reliability-testing
description: "Trigger: runtime testing, load testing, stress test, soak test, capacity, p95 latency, SLO, k6, schemathesis, smoke test. Prove running code under load and post-deploy checks."
license: Apache-2.0
metadata:
  author: "alesierraalta"
  version: "1.3"
---

## Activation Contract

Load to PROVE running code under operational load: saturation, capacity limits, spikes, endurance, contract fuzzing, post-deploy smoke. "Does this hold under load", "measure p95/p99", "find the breaking point", "detect memory leaks", "run smoke test".

Fault injection (retries, timeouts, deadlines, circuit breakers, network faults, shutdown) lives in `resilience-fault-injection`.

NOT for: fault injection (`resilience-fault-injection`), static unit tests (`tsp`), pruning unit test suites (`no-excess-tests`), single manual execution of a happy-path function (`real-run-validation`).

## Preflight Safety Gate

| Target | Requirement |
|---|---|
| Local isolated: throwaway worktree or ephemeral containers on an isolated bridge network, synthetic data, no shared network, credentials, or tenants | Proceed. The sandbox is the authorization; record it in the plan. Load still carries bounded duration, concurrency, and resource limits. |
| Shared, remote, staging with real data, multi-tenant, or production | All items in `~/.claude/skills/exploit-testing/references/safety-gate.md` (runtime subsection) are required. Never load-test shared environments without owner approval and egress limits; an ephemeral container alone is not authorization here. If any item is missing, stop at planning and report the gap. |

## Plan Contribution

When invoked by `tsp` in PLAN mode: do not execute. Return target rows for the plan:
target (endpoint, dependency seam, journey) · check (latency SLO, soak, saturation, spike,
contract fuzz, smoke) · target rung or depth · consequence class ·
why. Cover every check this skill would run on this codebase, including cheap ones (smoke
journey, Schemathesis against an existing OpenAPI spec).

## Hard Rules

1. **No probe without a FALSIFIABLE CLAIM**: "If <invariant/latency/threshold> violates <bound>, this probe goes RED". A probe that cannot go red is decorative.
2. **Execute against the REAL RUNNING ARTIFACT**: real services in ephemeral containers (`docker-test-containers`); never mock the seam being validated. If isolation cannot be provided, record the surface as unreachable.
3. **No coordinated omission**: load tests use arrival-rate / open workload models (`references/patterns.md`); closed loops must state the omission bias. Closed-model tools (autocannon, `wrk`, fixed-VU loops) are allowed only for an explicitly closed-model question (a connection-pool or fixed-client ceiling), labelled as such and never used for latency SLOs or capacity numbers. Attempted arrival rate (completed plus dropped iterations) and completed throughput are separate metrics, and timed-out requests (status 0) are counted separately and reported next to the percentiles, since percentiles over successful responses alone are optimistic (`references/patterns.md`).
4. **Assert on OBSERVED TELEMETRY, not exit codes**: database state, wire payloads, RSS/heap trends, saturation counters.
5. **Blast radius containment**: load is scoped to test-owned ephemeral containers on isolated networks; approved exceptions keep the same bounded limits and stop contract.
6. **Teardown is mandatory**: test-owned containers and volumes are removed even on assertion failure; never touch pre-existing resources.
7. **Evidence**: every conclusion is `observado` (backed by metrics/logs) or `razonado`; every finding carries an executed evidence record per `~/.claude/skills/tsp/references/evidence.md`; no finding from reading alone.

## Decision Gates

| What needs to be proven | Primary Tool | Technique / Pattern | Stop Gate / Success Metric |
| :--- | :--- | :--- | :--- |
| Latency compliance under concurrency | **k6** | Open workload arrival-rate (`assets/k6-arrival-rate-template.js`) | Target-declared latency/error SLO with provenance, sample context, tolerance |
| Memory leaks and endurance | **k6 + pprof** | Target-configured soak duration and load profile | Target-declared RSS/heap trend criterion |
| Breaking throughput limit | **k6** | Step-up `ramping-arrival-rate` stages, open workload | Target-declared saturation criterion; `dropped_iterations` aborts the run |
| Spike tolerance and recovery | **k6** | Sudden `ramping-arrival-rate` jump, then drop; measure return to baseline | Target-declared recovery time |
| API contract robustness | **Schemathesis** | Property fuzzing against OpenAPI | Target-declared error and schema conformance |
| Post-deploy golden path | **Playwright** | Headless API and browser journey | Target-declared duration and critical-flow criterion |

## Execution Steps

1. **Falsifiable claim**: target-owned SLOs/bounds with provenance, sample size, tolerance, measurement conditions, semantic oracle. Missing evidence is UNVERIFIED, never a pass.
2. **Isolated environment**: SUT and dependencies via `docker-test-containers` on an isolated bridge network.
3. **Test driver**: k6 script, Schemathesis invocation, or smoke journey.
4. **Baseline run** under zero-fault conditions: p50, p95, RPS, error rate.
5. **Apply load**; capture RPS, latency percentiles, memory growth, error rates continuously.
6. **Verify invariant and recovery**: remove the load and confirm return to baseline.
7. **Teardown**: terminate only owned, labelled containers and volumes; report skipped ambiguous items.
8. **Synthesize**: telemetry table, root causes, minimal reproduction commands, evidence record per finding.

## Output Contract

Report: falsifiable invariant table (claim | bound and provenance | observed | conditions | PASS/FAIL/BLOCKED) · telemetry summary (percentiles, peak RPS, error rate, memory trend) · load phases (start, peak, recovery timestamps) · defects with minimal reproduction, classification (`[Memory Leak]`, `[Coordinated Omission]`, `[Saturation]`, `[Contract Violation]`), and evidence record. RDD receipt when required: [references/rdd-receipt.md](references/rdd-receipt.md).

## References

- [references/patterns.md](references/patterns.md) — coordinated omission, tail latency amplification.
- [references/rdd-receipt.md](references/rdd-receipt.md) — receipt contract for `lens:reliability`.
- [assets/k6-arrival-rate-template.js](assets/k6-arrival-rate-template.js) — k6 open workload script.
- Sibling skills: `resilience-fault-injection` (fault injection, retries, breakers) · `tsp` · `docker-test-containers` · `exploit-testing` · `real-run-validation`.

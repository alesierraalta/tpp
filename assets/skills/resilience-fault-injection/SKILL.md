---
name: resilience-fault-injection
description: "Trigger: fault injection, retry, timeout, deadline, circuit breaker, backoff, jitter, backpressure, graceful shutdown, readiness, toxiproxy, idempotency key, metastable. Prove clients and services survive failing dependencies."
license: Apache-2.0
metadata:
  author: "alesierraalta"
  version: "0.1.0"
  requires_tpp: "0.4.1"
  scope: [resilience, faults, retries, timeouts, circuit-breaker, shutdown]
  auto_invoke: "Code that calls another service or resource over a network: HTTP/RPC clients, retries, timeouts, circuit breakers, connection pools, bounded queues between services, startup and shutdown of a service"
---

## Activation Contract

Load when the target calls a dependency over a network (HTTP, RPC, database, cache, queue client), retries, sets timeouts, breaks circuits, buffers work between services, pools connections, or starts and stops as a service.

NOT for single-process pure logic. Boundaries, do not duplicate: load, capacity, soak and SLO thresholds stay in `runtime-reliability-testing`; message redelivery, ordering and DLQ stay in `messaging-eventdriven-testing`; torn writes and process kills of a CLI stay in `crash-and-process-testing`; lock and isolation faults stay in `database-persistence-testing`.

## Hard Rules

1. **Test recovery, not only behavior during the fault.** A metastable failure is a trigger that pushes the system into a state (retry storm, full queue, cold cache) that persists after the trigger is gone (Bronson et al., HotOS 2021). Inject the fault, remove it, and assert the system returns to baseline within a stated time; a system that stays degraded is a defect even if every request during the fault behaved.
2. **The deadline is a budget passed down.** The caller's remaining time bounds every attempt, backoff sleep and downstream call. Downstream timeouts must be shorter than the upstream's. Assert total elapsed time against the budget with an always-failing dependency; a retry that starts after the deadline is a defect.
3. **Retries multiply across layers.** Count calls at the dependency while it fails; layers of A attempts give up to A^L calls (`references/patterns.md`). Require a retry budget (token bucket) or retries at one layer only. Retry only idempotent operations, or a write that carries one idempotency key, generated once per logical call and reused on every attempt.
4. **Timeout-after-success is the write-retry case.** The dependency applied the write and the response was lost. Assert the effect count is one after the retry; a per-attempt key, or no key, duplicates it.
5. **Backoff is exponential, jittered and capped.** Assert delays grow, stay under the cap, and differ across clients (seeded random). Synchronized retries are a defect.
6. **A breaker opens on failure rate, probes in half-open, closes only on success.** Assert fail-fast while open with no call to the dependency, at most K probes in half-open, back to open on a failed probe, and no traffic surge at close. A breaker that never leaves open, or lets all traffic through in half-open, is a defect.
7. **Overload sheds, it does not buffer without bound.** Flood a slow dependency: queues, in-flight counts and memory stay bounded, and excess work is rejected fast with a distinct error.
8. **Shutdown drains.** On termination, readiness turns false first, new work is refused, in-flight work finishes or is handed off within a grace period, then the process exits. Assert no in-flight request is lost and none starts after the stop.
9. **Prove with a seam and a re-run.** Use a deterministic fault seam (injectable transport, fake clock) or Toxiproxy on a test-owned proxy (`assets/toxiproxy-breaker-test.py`, faults removed in `finally`). Record measured attempt counts, elapsed time against the budget and recovery time, fix, and re-run green. A test that never fails on the defective code is not evidence.

## Decision Gates

| Signal | Fault injected | Failing evidence |
| --- | --- | --- |
| Client with a deadline or timeout option | Dependency never answers | Elapsed time above the budget; retry after the deadline |
| Retry on a write | Apply, then lose the response | Effect count above one |
| Retries at more than one layer | Dependency always fails | Calls at the dependency above the single-layer maximum |
| Retry delay code | Repeated failures, seeded jitter | Fixed or synchronized delays; no cap |
| Circuit breaker | Failure burst, then recovery | Never closes; probe count above K; no fail-fast |
| Queue or pool between services | Slow dependency plus flood | Unbounded growth; no rejection |
| Service with startup and shutdown | SIGTERM under load | Lost in-flight request; readiness true while draining |
| Any of the above | Fault then removal | Degraded state persists after the fault is gone |
| Pure logic, no dependency | Stop; record why the gate does not apply | none |

## Execution Steps

1. List every outbound call, its timeout, retry policy, idempotency, breaker, queue and shutdown path.
2. Find or add the seam: injectable transport and clock, or a Toxiproxy proxy on an isolated network (`docker-test-containers`). Toxiproxy toxic names and client API vary by version: `verify` before relying on them. Fault types worth covering: latency, connection reset, blackhole, partial partition, DNS failure, clock skew.
3. Run the matrix per call site; record attempt counts, elapsed time against the budget, and time to recover after the fault is removed.
4. Fix, re-run the same fault, and show the numbers now inside the bound. Keep each failing fault as a regression test.
5. Record probes you could not run as `not run` with the reason.

## Output Contract

Return per call site: the fault injected, the seam, measured attempt count, elapsed time against the budget, recovery time, the defect class, and the re-run result after the fix. List gates that did not apply with the reason.

## References

- [references/patterns.md](references/patterns.md): breaker state dynamics, jitter math, layered retry amplification.
- [assets/toxiproxy-breaker-test.py](assets/toxiproxy-breaker-test.py): Toxiproxy circuit breaker test.
- Sibling skills: `runtime-reliability-testing` · `messaging-eventdriven-testing` · `docker-test-containers`.

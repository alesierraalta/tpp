# Operational Runtime Testing Patterns

## 0. Evidence and Scope Requirements

- Declare target-owned SLOs with provenance (baseline, capacity plan, or protocol contract), sample context, tolerance/variance, measurement conditions, and rationale.
- Scope every injected fault to test-owned resources; mandatory teardown removes only owned faults, including on assertion failure.
- Use semantic oracles for payload/schema and recovery behavior, not status-only checks. Missing evidence is `UNVERIFIED`/`INCONCLUSIVE`, never a pass.

## 1. Coordinated Omission (Gil Tene)

Traditional closed-loop load generators (e.g. standard JMeter, fixed-concurrency loops) skew latency metrics during server stalls:
1. When the server slows down (GC pauses, I/O locks), worker threads block.
2. While blocked, the generator stops issuing new requests.
3. The queue of requests that *would* have arrived in reality is never sent or measured.
4. Reported p95/p99 latency severely underestimates real user wait time.

### Mitigation: Open Workload Model
Decouple request generation from response time. The arrival rate must remain fixed regardless of server latency:
- In `k6`: Use `executor: 'ramping-arrival-rate'` or `constant-arrival-rate` when the
  declared target requires an open workload.
- Pre-allocate enough Virtual Users (`maxVUs`) to sustain the declared target rate during
  latency spikes when that capability is available; otherwise record the limitation.

### Reporting rules for open-workload runs

- **Fail closed**: SLO thresholds come only from target-owned values; a missing bound aborts the run (the template throws), it is never defaulted.
- **`dropped_iterations` is an abort condition**: the generator could not offer the declared rate (VUs exhausted), so the run is INCONCLUSIVE. Use `abortOnFail: true`, not only a threshold that fails after the fact.
- **Offered vs completed**: offered = iterations started + dropped, completed = iterations finished. Report `offered_rate` and `completed_throughput` as separate numbers; a gap is the saturation signal.
- **Timeouts are censored samples**: a request that times out has a latency of at least the timeout, not exactly the timeout. Keep it in the percentile denominator (k6 records it in `http_req_duration` at the timeout value) and count it in `http_req_failed` and a timeout counter. Dropping timeouts from percentiles hides the tail; report percentiles as lower bounds when any timeout occurred.
- **Summary**: never return `{}` from `handleSummary`; that suppresses the default summary. Return the text summary on stdout plus a JSON file.

---

## 2. Tail Latency Amplification

In a distributed microservice topology where a user request fans out to $N$ independent downstream dependencies:
$$P(\text{Slow Request}) = 1 - (1 - p)^N$$

If a downstream service has a $p99 = 1\%$ (meaning 1% of calls exceed 1 second):
- For $N = 10$ services: $1 - (1 - 0.01)^{10} \approx 9.6\%$ of requests are slow.
- For $N = 50$ services: $1 - (1 - 0.01)^{50} \approx 39.5\%$ of requests are slow.
- For $N = 100$ services: $1 - (1 - 0.01)^{100} \approx 63.4\%$ of requests are slow.

**Scoped default**: When the declared SLO or test target includes tail latency, measure p99/p99.9 on each available leaf service, not only the edge gateway. If a leaf metric is unavailable, record that blind spot rather than infer it.

---

## 3. Circuit Breaker State Dynamics

```
       [Failures < Threshold]
       +--------------+
+----->|    CLOSED    |<--------------------------+
|      | Normal state |                           |
|      +--------------+                           |
|             | [Failures >= Threshold]           | [Probe Success]
|             v                                   |
|      +--------------+                           |
|      |     OPEN     |                    +---------------+
|      |  Fail-fast   |                    |   HALF-OPEN   |
|      +--------------+                    |  Probe state  |
|             |                            +---------------+
|             | [Sleep Window Expires]            ^
|             +-----------------------------------+
|               (Allow limited canary traffic)    |
|                                                 |
+-------------------------------------------------+
       [Probe Failure -> Back to OPEN]
```

### Verification Requirements:
1. If the declared circuit-breaker contract specifies fail-fast behavior, calls in `OPEN` should fail within its target-owned bound (for example, $< 20\text{ ms}$) without acquiring sockets or thread pool slots.
2. Where a fallback is part of the contract, verify the fallback payload/schema and observed breaker state, not only an HTTP status.
3. When recovery behavior is in scope, verify the declared transition to `HALF-OPEN` and its configured probe count $K$ before closing.
4. Faults must be scoped to test-owned names and removed in mandatory teardown; preserve pre-existing toxics. Declare the Toxiproxy API/client version assumption and proxy-routing precondition.

---

## 4. Retries and Jitter Math

As a default, do not retry immediately; avoid deterministic exponential backoff that can cause synchronized request waves ("Thundering Herd"). A protocol-specific retry schedule or owner-approved environment may define another policy; test that declared policy and its bounds:

* **Full Jitter Formula (AWS Architecture)**:
  $$t_i = \text{random}(0, \min(t_{\max}, t_{\text{base}} \cdot 2^i))$$
* **Decorrelated Jitter Formula (Database Contention)**:
  $$t_i = \min(t_{\max}, \text{random}(t_{\text{base}}, t_{i-1} \cdot 3))$$

# Operational Runtime Testing Patterns

## 0. Evidence and Scope Requirements

- Declare target-owned SLOs with provenance (baseline, capacity plan, or protocol contract), sample context, tolerance/variance, measurement conditions, and rationale.
- Scope every run to test-owned resources; teardown removes only what the run created, including on assertion failure.
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
- **Attempted vs completed**: attempted = completed iterations + `dropped_iterations` (arrivals the executor tried to schedule), completed = iterations finished. Report `attempted_rate_per_s` and `completed_throughput` as separate numbers; a gap is the saturation signal.
- **Timeouts are censored samples**: a request that times out has a latency of at least the timeout, not a known value, and k6 reports it with status 0 (its `http_req_duration` value is not guaranteed to equal the timeout). Procedure: count timeouts separately (status 0 or the error code) in a counter, report that count next to the percentiles, and count them in `http_req_failed`. Percentiles computed only over successful responses are optimistic. Analysis rule the operator applies: when any timeout occurred, treat each timeout as a sample at or above the timeout and report the percentiles as lower bounds.
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

# Adaptive Resource-Efficiency Investigation

Use this optional guide when a changed or live path has plausible resource, scaling, latency, or operating-cost risk. It complements reliability probes; it is not a requirement to profile every change, run a soak by default, or exhaustively optimize. Preserve behavior and prefer an improvement only when its practical benefit is worth its complexity.

## Choose the smallest useful probe

Start from the affected path and the risk, then state what observation would change the decision:

- **Micro or localized change without evidence of a hot path:** source/call-path analysis may be enough to spot repeated work, an avoidable conversion, or a cheaper equivalent algorithm or data structure. Record this as `razonado` with the concrete path/evidence; it is not a measured speedup or resource saving.
- **Known hot path, critical API, or concrete resource concern:** measure representative input size(s) and concurrency with the narrowest existing benchmark or profiler that can test the claim. Include enough of the real call path to expose relevant blocking and I/O.
- **Architectural change or scaling concern:** broaden the workload only when the changed dependency/path or expected traffic makes a broader effect plausible. State why that workload is representative. Do not expand automatically to every endpoint, profile, or soak duration.

Prove that the measured path is actually reached by the selected workload. A static caller or possible route establishes reachability, not that the path is hot. If the path cannot be reached or observed, say so and limit the claim. For load studies, choose the workload model to match arrivals: a closed loop reduces new arrivals as responses slow and can hide overload; use an open arrival model when the real workload continues to arrive independently of response time ([k6 workload-model guidance](https://grafana.com/docs/k6/latest/using-k6/scenarios/concepts/open-vs-closed/)).

## Measure the cost that matches the claim

Pick only the few signals needed for the claim; these are options, not a checklist:

- CPU time or CPU per successful operation; distinguish active work from time waiting on I/O, locks, or dependencies.
- Allocations/bytes per operation and GC activity for allocation churn; separately inspect retained/live heap for retention or leak claims. RSS alone neither proves nor rules out a live-heap leak.
- Blocking and lock contention when wait time or tail latency is at issue; CPU profiles alone do not explain blocked time.
- Calls, queries, bytes, and I/O per successful operation when redundant work or transport cost is plausible. Route database query plans, query-count budgets, lock behavior, and database-specific costs to `database-persistence-testing`; this guide does not reproduce its procedures.
- Throughput and latency across representative input sizes or concurrency when scale behavior is the claim. Include successful-operation rate and errors so dropping work cannot appear as an efficiency win.

Consider whether a simpler equivalent algorithm or structure removes repeated computation, allocations, copies, serialization, blocking, or unnecessary calls/bytes. Keep clarity and behavior as constraints; do not trade them for a statistically detectable but practically irrelevant result.

## Make comparisons credible and bounded

For a measured improvement/regression claim, identify the baseline and candidate revisions. Keep inputs/data, runtime and configuration, hardware/resource limits, workload/concurrency, cache state, and JIT/warm-up treatment comparable. Set a small, fixed repeat/time budget before running; use repeated, preferably interleaved baseline/candidate samples where practical. Report spread/noise and the practical threshold's provenance (target SLO, agreed resource/cost budget, or other owner-backed limit), or state that no such threshold is declared. Statistical significance is not itself practical significance; do not rerun until a desired result appears. These comparison and noise cautions are also reflected in [`benchstat`](https://pkg.go.dev/golang.org/x/perf/cmd/benchstat).

Apply comparable instrumentation to both sides and account for profiler/trace overhead. Use profiling to locate a cause, then corroborate a claimed user-visible or per-operation improvement with a minimally instrumented comparison when feasible. Stack-specific tools are optional and must already be available or otherwise authorized: for Go, `go test -bench ... -benchmem`, `benchstat`, and `go tool pprof` CPU/heap/block/mutex profiles are examples; the [Go diagnostics guide](https://go.dev/doc/diagnostics) explains what these profiles observe and their tradeoffs. Use the existing benchmark/profiler idiomatic to another stack rather than installing tools for this check.

Database measurement has extra execution hazards: PostgreSQL `EXPLAIN ANALYZE` executes the statement, adds measurement overhead, and excludes network transmission; toy data may not predict production behavior ([PostgreSQL EXPLAIN documentation](https://www.postgresql.org/docs/current/using-explain.html)). Run it only on an authorized, safe representative environment and account for effects. Never create production load or run effectful analysis in production without explicit owner authority.

## Optional comparative opportunity route

When the affected path shows a plausible hot path, repeated work, or material per-operation cost **and** a concrete simpler equivalent is credible, a bounded scratch comparison can say whether that alternative is worth proposing:

- Put both implementations in scratch you already own and drive them with the **same** workload, inputs, environment, and instrumentation; nothing here requires editing the shipped source, adding a benchmark the repository does not already have, or installing a tool.
- Before running, confirm the candidate preserves observable behavior: same outputs, same API/error surface, same ordering and side effects. A candidate that only measures well but changes behavior is not an equivalent.
- Record whatever the stack makes available for each side — ns/op, B/op, allocs/op, or the equivalent — together with repeat count, observed spread/noise, environment, and profiler/trace overhead. Availability varies by stack and by what the project already uses; a missing metric is a stated limitation, not a reason to install something.
- Prefer interleaved samples and a small, fixed repeat budget chosen before the run, as above. One run showing a number is a data point, not a finding.

Skip this route when the cost is immaterial, when no simpler equivalent is credible, when it would cost clarity or behavior fidelity, or when no authorized environment supports it. Not running it is a valid outcome. This route never rewrites the artifact under test, never becomes a mandatory profiling step, and never blocks delivery on its own.

## Report limits honestly

### Classify the result honestly

- **Regression is comparative, not bound-based.** It requires a named prior revision, comparable measured evidence on both sides, and stated noise/uncertainty under the conditions above; an owner-declared bound is not a precondition for this label. Report a practical bound's provenance when one exists, or state explicitly that none is declared — do not invent a threshold either way. Severity/practical impact is judged separately from the label and must be evidenced (observed cost, latency, or resource effect in context), not assumed.
- **Defect** still requires a violated owner contract or budget, with provenance for the bound and for the violation. Practical impact alone does not create that contract, and a missing bound does not dismiss a measured prior-version regression.
- **No named prior revision and no declared bound:** a measured current cost supports an *optional improvement* only — an *optional measured opportunity*, or a *proposed opportunity* (`razonado`) when the comparison was not run. It is neither a regression nor a confirmed defect.
- Volume is not a finding by itself. Allocation counts, formatting or conversion calls, and constant literals are not automatically defects: a defect needs a bound or contract to violate, a regression needs a named prior revision with comparable evidence, and "an alternative exists" is neither.
- *No opportunity found* and *insufficient evidence* are both valid results. Record whether further exploration is worthwhile and name the specific missing evidence (a named prior revision, a bound and its provenance, a tool the project already has, a representative workload, a credible candidate) rather than leaving the gap implicit.

Report the falsifiable claim, baseline/candidate identities when measured, workload and environment conditions, bounded repeat method, observed values and uncertainty, practical threshold and provenance or an explicit statement that none is declared, and behavior/error-rate check. Separate observed measurements from source-level reasoning. If the required baseline, profiler, representative workload, or authority is unavailable, state the specific limitation and mark the performance conclusion `limited` or `unverified`—never fabricate a pass or claim a measured improvement from static inspection alone.

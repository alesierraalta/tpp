# TPP Resource-Efficiency Evaluation Rubric

## Purpose and boundary

This is a **manual, offline aid** for comparing TPP plans. It is not part of the runtime harness, adds no fixed checks to `bench`, and is not a merge gate. Evaluate only risks made relevant by the case/change; do not reward exhaustive profiling or lists of every possible resource dimension.

Keep four signals separate: plan delivery, defect reporting, behavioral test discrimination (`caught`), and quality of resource evidence. A defect report or passing functional test alone does not prove a performance measurement occurred.

## Unit and artifact handling

Grade one case-run at a time. Use the runner's `result.json.plan_found` and `result.json.plan_path` as the authoritative record of what the scorer resolved; the runner already applies the workspace declaration, falling back to `docs/testing/test-plan.md`. Do not assume the default path when `plan_path` records a declared alternative. A useful plan in a sidecar the scorer did not read is a **delivery-path failure**; note its content separately for diagnosis, but do not count it as delivered.

Do not read `agent.log`, credentials, telemetry, or unrelated workspace files. Use the saved plan, case `KEY.json`, fixture README, and public aggregate fields only. Blind arm labels before grading when practical; reveal them only after individual grades are recorded.

A missing, unreadable, or untouched-template plan receives **delivery 0**; score its resource reasoning as `N/A`, not as evidence that it chose a good or bad probe. A wrong-path substantive plan is still a delivery failure; record its content as supplemental diagnostic evidence only.

## Per-plan dimensions

Score applicable dimensions `0`, `1`, or `2`, citing a plan row or evidence-ledger entry. Use `N/A` only when the artifact/content is unavailable.

| Dimension | 0 | 1 | 2 |
|---|---|---|---|
| **Plan delivery** | Missing, unreadable, wrong path, or untouched template | Canonical plan exists but is materially incomplete | Canonical plan contains the run's findings/decisions and a usable evidence ledger; clean cases state the scoped conclusion |
| **Risk correspondence** | Misses the planted/resource risk or substitutes unrelated correctness work | Names a relevant symptom/resource, but does not connect it to the affected path | Connects the changed path and risk mechanism (for example retained growth, operation growth, query amplification, or serialized independent I/O) |
| **Probe fit and proportionality** | No falsifiable probe, or irrelevant/exhaustive profiling for a low-risk change | Plausible source-level reasoning or proposed probe, but workload/signal/bound is underspecified | Chooses a change-relevant probe with representative input/volume/concurrency and a practical bound; uses the narrowest evidence that answers the risk |
| **Evidence integrity** | Claims measurements/results without observed evidence, or invents a baseline/value | Clearly distinguishes proposed/not-run work from observations and explains limitations/tool gaps | Records executed method, workload, observed result, comparison/baseline and threshold provenance; instrumentation limitations are acknowledged |
| **Design trade-off** | Recommends complexity/micro-optimization without benefit or harms clarity | Mentions clarity/cost but does not weigh practical benefit | Preserves behavior and maintainability; justifies extra complexity with a material resource/cost benefit |

For **risk correspondence**, select only the applicable signal: retained/live memory versus allocation churn/GC; active CPU versus blocked/lock wait; algorithmic work versus input size; query/call/byte/I/O count; concurrency and overlap; latency/throughput under representative load; or cost per useful operation. These are examples, not mandatory rows.

## Evidence-state labels

For each resource probe, label it one of:

- `reasoned`: source/call-path analysis only; no saving or speedup claimed.
- `proposed`: a suitable measurement is specified but was not run.
- `observed`: command/tool, representative workload, and result are in the evidence ledger.
- `limited` / `unverified`: required baseline, bound, tooling, workload, or authority is unavailable; no pass is inferred.

A profile, benchmark, query plan, or metric named in prose is not `observed` without recorded execution and output. A deterministic counter in a fixture is a **proxy** for resource work, not production CPU, latency, heap, or GC profiling.

## Per-arm summary

Report, without collapsing into one score:

1. valid runs and correct-path/non-template plan completion;
2. `reported`/`caught`/false-positive results from the benchmark runner;
3. for each risk-bearing case, applicable dimension scores and evidence-state labels;
4. clean-control overtesting/false positives;
5. instability, costs, turns, and missing/empty artifacts.

Show individual repeats and distributions. With a handful of repeats (three to five in these evaluations), avoid significance claims; flag unstable cases and explain whether a difference reflects plan completion, defect recognition, or measurement quality. Do not infer causation from a small score delta or award points for profiling that the case does not justify.
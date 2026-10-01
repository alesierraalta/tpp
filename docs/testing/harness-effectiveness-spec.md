# Harness effectiveness evaluation — specification

Status: **draft, normative once merged** · Spec version: `hee-spec/0.1` · Scope: phase 1 of 5 (specification only).

This document defines how `tpp` measures whether one version of the testing harness finds real
defects better than another: with what precision, at what noise level, at what cost, in which
domains, and which components are responsible. It does not replace the instrument in
`internal/bench` and `bench/`; it extends it. Section 18 maps every rule here to the code that exists
today and to the code that does not.

The words MUST, MUST NOT, SHOULD and MAY are normative.

The question this system answers, with evidence:

> Does the candidate harness find more real defects than the baseline, with what precision, in
> which domains does it improve or regress, what does the improvement cost, and which components
> are responsible for it?

Everything below exists to make that answer **reconstructible from data** and **independent of
anyone's opinion at decision time**. Human judgement enters the system in exactly one place,
adjudication (section 4), and there it records facts, not verdicts.

---

## 1. Vocabulary and populations

| Term | Definition |
|---|---|
| **Issue** | A real, known defect in the ground truth. It exists whether or not the harness detects it. Today: one entry of `defects[]` in a case's `KEY.json`. |
| **Finding** | A claim the harness makes during a case run. Today: one row of the plan's Findings table, plus the Evidence ledger rows it cites. |
| **Case** | One fixture project with its key: `bench/cases/<id>/`. A **clean control** is a case with no Issues (`"control": "clean"`). |
| **Case run** | One execution of the harness against one case, in a fresh workspace. Today: one `result.json`. |
| **Benchmark run** | One pass of one harness version over every case of one suite at one benchmark version. It is the unit "a run" refers to everywhere in this spec: one replicate. |
| **Comparison** | A decision between a baseline and a candidate harness, each represented by K COMPLETED benchmark runs of the same benchmark version. |
| **Harness version** | The system under test (section 8.1). |
| **Benchmark version** | The immutable measuring instrument (section 8.2). |
| **Decision policy version** | The thresholds and rules that turn metrics into a verdict (section 8.3). |

Two populations, never mixed:

```
Issue   = a real problem          → recall, coverage, miss rate, severity accuracy, detection frequency
Finding = what the harness claims → precision, false discovery, noise, reproducibility
```

An Issue count and a Finding count MUST NOT appear in the same ratio, except where this spec states
a by-construction identity (invariant I3).

---

## 2. Data model

### 2.1 Issue (KEY schema v2)

KEY schema v1 already carries `id`, `file`, `line`, `class`, `keywords`, `description`, `trigger`
(`input`, `expected`, `actual`) and `why_missed`. Schema v2 adds:

| Field | Type | Meaning | Source in v1 |
|---|---|---|---|
| `issue_type` | string | Normalized defect type from the taxonomy in section 11. | `class` |
| `domain` | enum | One domain of section 11. | none (mapping proposed in 11.2) |
| `severity` | enum `info·low·medium·high·critical` | Assigned with the rubric in 2.4. | none |
| `severity_rationale` | string | One sentence naming the consequence class and its reach. | none |
| `expected_behavior` | string | What the contract promises. | `trigger.expected` |
| `failure_condition` | string | The input or state under which the promise breaks, and what happens. | `trigger.input` + `trigger.actual` |
| `detection_criteria` | object | Section 3: the mechanism a correct finding must name (`mechanism`), the accepted equivalent mechanisms (`equivalents[]`), and the domain proof obligation (`proof`). | `description`, `keywords` |
| `reproduction` | object | `applies` (bool), `oracle` (`catch`·`command`·`none`), `nondeterministic` (bool), `attempts` (int). | implied by `fix/keep-<ID>/` |

A v2 key MUST still satisfy every v1 rule in `bench/README.md` ("Rules", "Adding a case"): the
trigger is executed, `actual` is pasted from a real run, the fixture suite is green with the defect
present.

### 2.2 Finding

A Finding is extracted from a case run's kept plan (`test-plan.md` beside `result.json`). The
existing template already supplies most fields:

| Field | Source |
|---|---|
| `finding_id` | `<benchmark_run_id>/<case>/<row>` |
| `run_id` | the benchmark run |
| `row_fingerprint` | existing `FindingRowFingerprint` |
| `location` | `Finding (path:line, one line)` column |
| `description` | the same cell's text |
| `reported_severity` | `Severity (consequence class)` column, mapped with table 2.4 |
| `reported_domain`, `reported_issue_type` | not in the template; assigned at adjudication (fact `C1`) |
| `evidence` | the Evidence ledger rows the `Evidence id` cell cites |
| `reproduction` | the `Pinning test` cell, and the agent test files the catch oracle carries |

The harness under test is not required to emit a domain. Requiring it would measure template
compliance, not detection.

### 2.3 Run records

Every case run and benchmark run keeps the records listed in section 16.1. The fields that freeze
at `CREATED` are: benchmark manifest digest, harness id, decision policy id, model, runner,
environment, and seeds.

### 2.4 Severity rubric

The same table classifies an Issue (ground truth) and a Finding's reported consequence class. It
reuses the consequence classes the `test-strategy` skill already ranks
(`references/prioritization.md`):

| Severity | Consequence class and reach |
|---|---|
| `critical` | Money moved or lost, data destroyed or corrupted, or a permission/tenant boundary crossed, reachable with the default configuration and no special preconditions. |
| `high` | The same consequence classes, reachable only under a specific precondition; or a silently wrong answer a human will act on, on a supported input. |
| `medium` | A silently wrong answer on an edge input; or a visible error on a supported input. |
| `low` | A visible error on a rare or unsupported input; degraded diagnostics. |
| `info` | Cosmetic; no behavioural consequence. |

A reported severity that cannot be mapped to this table is `unknown`. An `unknown` severity counts
as an incorrect severity (section 7.5); it is not removed from the denominator.

---

## 3. Detection criteria

### 3.1 The six facts

Whether a finding detects an Issue is decided by six facts. Each one is either **computed** by the
instrument or **recorded** by an adjudicator with a reason. No one records the verdict itself: the
verdict is derived from the facts (3.2).

| Fact | Question | How it is established |
|---|---|---|
| `C1 type` | Does the finding identify the Issue's type, or an accepted equivalent? | Recorded. |
| `C2 location` | Does it locate the affected behaviour? Same file and a line within ±5 of `line`, or the enclosing function named. | Computed (today's "reported" rule, minus keywords). |
| `C3 mechanism` | Does it describe the cause, or a technically equivalent mechanism listed in `detection_criteria.equivalents`? | Recorded. |
| `C4 evidence` | Does it cite at least one Evidence ledger row that exists and that shows the failure? | Computed for existence (today's link rule); recorded for "shows the failure". |
| `C5 reproduction` | Does it reproduce under the defined conditions? | Computed: the catch oracle (`fix/all` green, `fix/keep-<ID>` red; for `nondeterministic` Issues, at least one red attempt in `reproduction.attempts`). `n/a` when `reproduction.applies` is false. |
| `C6 not a repetition` | Is it the primary detection rather than a repetition of another finding? | Derived during primary selection (4.2). |

A fact an adjudicator cannot establish is recorded as `unknown`, and `unknown` counts as false.
Missing knowledge never upgrades a match.

### 3.2 Match level (derived)

```
NONE     unless  C2 ∧ (C1 ∨ C3)
FULL     iff     C1 ∧ C2 ∧ C3 ∧ C4 ∧ (C5 ∨ C5 = n/a)
PARTIAL  otherwise (C2 holds, at least one of C1/C3 holds, and some of C1, C3, C4, C5 fail)
```

"There may be a security problem" fails `C2` and both `C1` and `C3`. Its match level is `NONE`,
whatever the ground truth contains. It enters the unmatched path (4.3) and ends as `FP` or
`LOW_VALUE`.

### 3.3 Domain proof obligations

`detection_criteria.proof` names what `C3`, `C4` and `C5` must show for the Issue's domain. A key
MUST NOT declare an Issue whose proof obligation cannot be checked.

| Domain | The finding must show |
|---|---|
| Security | An exploitable condition, or a concrete violation of a named security property (the input and the boundary it crosses). |
| Correctness | One input or state where the output contradicts `expected_behavior`. |
| Performance | A metric breaching the threshold the key declares, under the key's load profile, measured with the key's sample count. A key without a declared threshold cannot hold a performance Issue. |
| Concurrency | A reproduced inconsistency: a race-detector report, or a wrong outcome in at least one of `attempts` runs. |
| Regression | A named prior version where the behaviour held, and the current failure. |
| Resilience | The injected fault, the expected degradation, and the observed wrong behaviour. |
| Data | The record lost, duplicated or corrupted, and the operation sequence that does it. |
| API / compatibility | The consumer payload or call that the producer change breaks. |
| AI / LLM | The eval case, the declared threshold, and the measured score. |
| Architecture / maintainability | A violation of an executable conformance rule the key names, for example an import edge or a dependency cycle. A claim without such a rule cannot reach `FULL`. |

---

## 4. Adjudication

### 4.1 What an adjudicator records

For each finding, an adjudicator (a person, or a named and versioned rule) appends one decision
event (section 5.5) holding:

```json
{"finding_id":"…","row_fingerprint":"…","candidate_issue":"D1",
 "facts":{"C1":true,"C3":true,"C4_shows_failure":true},
 "equivalent_to":null,
 "unmatched_outcome":null,
 "by":"<person or rule@version>","ts":"…","reason":"one sentence"}
```

`C2`, the citation part of `C4`, and `C5` are copied from the instrument and are never typed in by
hand. `unmatched_outcome` is set only on the unmatched path (4.3). An adjudicator MUST NOT have
access to the harness version label while adjudicating (blind adjudication). The facts above must
not depend on which version produced the finding.

### 4.2 Primary selection and duplicates (derived)

Within one case run:

1. Findings are grouped into **equivalence classes**: a finding with `equivalent_to = F` joins F's
   class. The class representative is the finding that no other member points to; chains and cycles
   are refused (invariant I4).
2. For each Issue, the candidate representatives are those whose match level against it is not
   `NONE`. A finding matches at most one Issue: the one it was adjudicated against. A row that
   describes two Issues is credited to one. This is the existing "one row, one claim" rule.
3. The **primary** finding for an Issue is the candidate with the highest match level
   (`FULL > PARTIAL`). Ties go to the lowest row number. The rule is deterministic.
4. The primary becomes `TP_FINDING` if its level is `FULL`, otherwise `PD_FINDING`. Every other
   candidate for that Issue, and every non-representative member of any class, becomes `DUPLICATE`.

Duplicates never change an Issue's state and never enter precision.

### 4.3 Unmatched findings and the benchmark backlog

A finding whose match level is `NONE` against every Issue is not a false positive by default.
This keeps the rule `MetricsVersion 2` already enforces.

```
UNMATCHED → NOVEL_CANDIDATE → CONFIRMED_NOVEL | FP | LOW_VALUE | INCONCLUSIVE | DUPLICATE
```

| Outcome | Meaning |
|---|---|
| `CONFIRMED_NOVEL` | A real defect the key does not contain. It must meet the same proof obligation as an Issue of its domain, and it must be reproduced by the adjudicator when reproduction applies. |
| `FP` | The claimed defect is not there. |
| `LOW_VALUE` | True, but not a defect against the contract: style, speculative hardening, a test gap with no failure behind it. |
| `INCONCLUSIVE` | Could not be decided within the adjudication budget. |
| `DUPLICATE` | Equivalent to another unmatched finding of the same case run. |

`CONFIRMED_NOVEL` findings are appended to `bench/backlog/<case>.jsonl`, an append-only file, with
the finding, its evidence, the reproduction, and a proposed Issue entry. The backlog never modifies
the current benchmark version. An Issue enters the ground truth only through a new benchmark
version (section 8.2) and the "Adding a case" verification. Prior runs MAY then be re-scored
against the new version as a labelled re-reading; such a re-reading never replaces the original
reading.

The existing verdict `out_of_scope` maps to `LOW_VALUE` when the observation is not a defect, and
to `CONFIRMED_NOVEL` when it is one.

### 4.4 Invalid findings

A finding is `INVALID` when it cannot be adjudicated as a claim: it names no location, the plan is
prose (`plan_format = prose`), the cell is empty or unparsable, or the cited path escapes the
workspace. `INVALID` is a property of the output format, not a judgement of truth. Missing evidence
is not invalidity. A located claim without evidence is adjudicated and can reach `PARTIAL` at most;
today's rule discards such "unlinked" rows outright (deviation D8).

---

## 5. State machines

Transitions not listed in a table are forbidden. Each machine is persisted as an append-only event
log, and the current state is the fold of that log (5.5).

### 5.1 Issue (per case run)

```
PENDING ──case run starts──▶ UNDER_EVALUATION ──case run closes──▶ TP | PD | FN
                                                                     │
                                       terminal ──REOPENED event──▶ UNDER_EVALUATION
```

| From | To | Guard |
|---|---|---|
| `PENDING` | `UNDER_EVALUATION` | The case run reached `SCORED`. |
| `UNDER_EVALUATION` | `TP` | The case run is closing and some finding is its `TP_FINDING`. |
| `UNDER_EVALUATION` | `PD` | The case run is closing, there is no `TP_FINDING`, and some finding is its `PD_FINDING`. |
| `UNDER_EVALUATION` | `FN` | The case run is closing and no finding is its primary. **Only at close.** |
| `TP`·`PD`·`FN` | `UNDER_EVALUATION` | A `REOPENED` event names one of its findings. |

The Issue state is never typed in. It is recomputed from finding states (invariant I5).

### 5.2 Finding

```
RAW ─▶ PENDING_ADJUDICATION ─▶ MATCHED ───────▶ TP_FINDING | PD_FINDING | DUPLICATE
                       │
                       ├─────▶ UNMATCHED ─▶ NOVEL_CANDIDATE ─▶ CONFIRMED_NOVEL | FP | LOW_VALUE | INCONCLUSIVE | DUPLICATE
                       │
                       └─────▶ INVALID
any terminal ──REOPENED event──▶ PENDING_ADJUDICATION
```

| From | To | Guard |
|---|---|---|
| `RAW` | `PENDING_ADJUDICATION` | The row parsed and is fingerprinted. |
| `PENDING_ADJUDICATION` | `INVALID` | One of the conditions in 4.4. |
| `PENDING_ADJUDICATION` | `MATCHED` | A decision event gives a match level other than `NONE` against one Issue. |
| `PENDING_ADJUDICATION` | `UNMATCHED` | A decision event gives `NONE` against every Issue. |
| `MATCHED` | `TP_FINDING`·`PD_FINDING`·`DUPLICATE` | Primary selection (4.2) at case-run close. Derived, never typed. |
| `UNMATCHED` | `NOVEL_CANDIDATE` | Automatic. |
| `NOVEL_CANDIDATE` | `CONFIRMED_NOVEL`·`FP`·`LOW_VALUE`·`INCONCLUSIVE`·`DUPLICATE` | A decision event sets `unmatched_outcome`, or `equivalent_to` for `DUPLICATE`. |
| any terminal | `PENDING_ADJUDICATION` | A `REOPENED` event. |

Terminal finding states: `TP_FINDING`, `PD_FINDING`, `DUPLICATE`, `CONFIRMED_NOVEL`, `FP`,
`LOW_VALUE`, `INCONCLUSIVE`, `INVALID`. `MATCHED`, `UNMATCHED` and `NOVEL_CANDIDATE` are not
terminal.

### 5.3 Case run

```
CREATED ─▶ RUNNING ─▶ SCORED ─▶ ADJUDICATING ─▶ CLOSED
              │
              ├─▶ FAILED_INFRASTRUCTURE ─(retry ≤ max_retries)─▶ RUNNING
              ├─▶ INVALID
              └─▶ ABORTED
```

Whose failure it is decides how it is counted:

| Cause | Classification | Effect on Issues |
|---|---|---|
| External: model API error or outage, rate limit, network, host resource exhaustion, container runtime failure. | `FAILED_INFRASTRUCTURE`, retried. | None while retrying. |
| The harness ran out of its own case budget (turns, tokens, cost, time). | `SCORED` with note `budget_exhausted`. | The plan as it stands is scored; Issues not found are `FN`. |
| The harness crashed, wrote no plan, or wrote an unreadable plan. | `SCORED` with the existing `NO PLAN` or read-failure note. | `FN`. |
| Cause unknown. | `FAILED_INFRASTRUCTURE`, retried. If it persists after `max_retries`, `SCORED` with note `unclassified_failure`. | `FN` after retries. |
| Ground truth reached the agent, the configuration was wrong, or a case changed mid-run. | `INVALID` (section 15). | Excluded. |

A harness that fails within its own budget is being measured, not excused. Excluding such failures
would reward a harness for crashing (deviation D4).

### 5.4 Benchmark run

```
CREATED ─▶ RUNNING ─▶ ADJUDICATING ─▶ COMPLETED
   alternatives: INVALID · FAILED_INFRASTRUCTURE · ABORTED
```

| From | To | Guard |
|---|---|---|
| `CREATED` | `RUNNING` | The manifest digest, harness id, policy id, model, runner, environment and seeds are frozen. |
| `RUNNING` | `ADJUDICATING` | Every case run is `SCORED`. |
| `ADJUDICATING` | `COMPLETED` | Every case run is `CLOSED`, every finding is terminal, and invariants I1–I11 hold. |
| any | `INVALID` | A condition in section 15, including any case run that became `INVALID`. |
| `RUNNING` | `FAILED_INFRASTRUCTURE` | A case run is still `FAILED_INFRASTRUCTURE` after `max_retries`. |
| `RUNNING` | `ABORTED` | An operator stop, or the suite's `max_cost` or `max_runtime` was reached. |

Only `COMPLETED` benchmark runs participate in comparisons. `FAILED_INFRASTRUCTURE` and `INVALID`
runs produce no number at all. They never become 0%.

### 5.5 Event log and reopening

Each state machine writes JSON lines to `<benchmark run>/events.jsonl`:

```json
{"seq":42,"prev_hash":"sha256:…","entity":"finding","id":"…","event":"decide|derive|reopen|transition",
 "previous_state":"FP","new_state":"PENDING_ADJUDICATION","reason":"…","ts":"…","adjudicator":"…","payload":{…}}
```

- Each event carries the hash of the previous event. A rewritten log fails verification (I9).
- A terminal state changes only through a `reopen` event, which records `previous_state`,
  `new_state`, `reason`, `timestamp` and `adjudicator`. Nothing overwrites a classification
  silently. This generalizes today's `Superseded` list in `adjudication.json`.
- A reopen after metrics were computed invalidates those metrics. The next report is a new
  revision that names the reopen events between the two revisions.

---

## 6. Invariants

These are checked automatically when a case run closes, when a benchmark run completes, and before
any metric or decision is computed. A violation blocks the computation and is reported as an
`INSTRUMENT` failure (hard blocker H5).

| Id | Invariant |
|---|---|
| I1 | Every Issue of a `CLOSED` case run is in exactly one of `TP`, `PD`, `FN`. |
| I2 | Every finding of a `CLOSED` case run is in exactly one terminal state. |
| I3 | Per Issue, at most one finding is primary. Per case run, `#TP_FINDING = #TP Issues` and `#PD_FINDING = #PD Issues`. |
| I4 | Every `DUPLICATE` points to a non-duplicate representative of the same case run. No chains, no cycles. |
| I5 | Issue and finding states recomputed from the event log equal the stored states. |
| I6 | The benchmark manifest digest is the same at `CREATED` and at `COMPLETED`. Every case's key, fixture and fix digests match the manifest. |
| I7 | `CONFIRMED_NOVEL` findings change no file covered by the manifest. No Issue of a benchmark version originates from that version's own backlog. |
| I8 | No metric is computed while any finding is `RAW`, `PENDING_ADJUDICATION`, `MATCHED`, `UNMATCHED` or `NOVEL_CANDIDATE`, or is being reopened. |
| I9 | The event log hash chain verifies. A terminal state changes only through a `reopen` event. |
| I10 | `FN` is assigned only at case-run close. |
| I11 | Every transition is an edge of section 5. |
| I12 | A comparison uses only `COMPLETED` runs with an equal manifest digest, equal model, runner and environment, and the policy id fixed when the candidate runs were created. |
| I13 | True negatives and FPR come only from clean controls. |
| I14 | Every number in a report carries the digest of the inputs it was derived from (16.2). |

---

## 7. Metrics

All metrics are computed **per benchmark run first** (section 10 aggregates them). Counts are
micro-averaged across cases. A denominator of zero yields `N/A`, never 0, unless a row says
otherwise.

### 7.1 Issue metrics (population: Issues of defect-bearing cases)

```
Known Issues            = TP + PD + FN
Strict Recall           = TP / (TP + PD + FN)              ← primary detection metric
Detection Coverage      = (TP + PD) / (TP + PD + FN)
Miss Rate               = FN / (TP + PD + FN)
Partial Detection Rate  = PD / (TP + PD + FN)
Weighted Recall         = (TP + w·PD) / (TP + PD + FN)     w is fixed in the benchmark manifest before any run
```

`Strict Recall + Partial Detection Rate + Miss Rate = 1` in every run. The reporter checks it.

### 7.2 Finding metrics (population: findings after deduplication)

```
Unique Adjudicated Findings = TP_FINDING + PD_FINDING + FP
Strict Precision            = TP_FINDING / (TP_FINDING + PD_FINDING + FP)
Accepted Finding Precision  = (TP_FINDING + PD_FINDING) / (TP_FINDING + PD_FINDING + FP)
False Discovery Rate        = FP / (TP_FINDING + PD_FINDING + FP)
```

`DUPLICATE`, `LOW_VALUE`, `INCONCLUSIVE`, `CONFIRMED_NOVEL` and `INVALID` are outside these
denominators. `CONFIRMED_NOVEL` is reported as a separate count: it is a real defect, so it must
not count against precision, and it is not keyed, so it cannot count for it either.

FDR is not an FPR. Classical `FPR = FP / (FP + TN)` needs true negatives. The corpus has them only
in clean controls, at case-run granularity:

```
Control FPR = clean-control case runs with ≥ 1 FP finding / clean-control case runs
```

`Control FPR` is the only FPR this system reports. With no clean controls in a suite it is `N/A`.

### 7.3 F1

```
Strict F1 = 2·P·R / (P + R)      P = Strict Precision, R = Strict Recall
          = 0                    when P = R = 0
          = N/A                  when P or R is N/A
```

The aggregate F1 is the mean of the per-run F1 values, not the F1 of the mean P and mean R.

### 7.4 Noise (population: every finding)

```
Total Raw Findings = TP_FINDING + PD_FINDING + FP + DUPLICATE + LOW_VALUE + INCONCLUSIVE
                   + CONFIRMED_NOVEL + INVALID
Duplicate Rate     = DUPLICATE    / Total Raw Findings
Low-value Rate     = LOW_VALUE    / Total Raw Findings
Inconclusive Rate  = INCONCLUSIVE / Total Raw Findings
Invalid Rate       = INVALID      / Total Raw Findings
```

The request's definition of Total Raw Findings left out `CONFIRMED_NOVEL` and `INVALID`. With those
left out, the rates do not partition the output and the harness's raw volume is understated
(deviation D1).

### 7.5 Reproducibility and severity

`C5` is checked once, at admission. Reproducibility is a separate **confirmation re-run**: the
verifier re-executes each confirmed finding's reproduction artifact in a fresh workspace,
`reproduction.attempts` times. A deterministic Issue reproduces only if every attempt agrees. A
`nondeterministic` Issue reproduces if at least one attempt is red on `keep-<ID>` and every attempt
is green on `fix/all`.

```
Confirmed Findings     = TP_FINDING + PD_FINDING + CONFIRMED_NOVEL
Reproducibility Rate   = confirmed findings that reproduce on confirmation
                       / confirmed findings where reproduction applies
```

A `TP_FINDING` that fails confirmation is reopened and drops to `PD_FINDING`. It also feeds hard
blocker H4.

```
Detected Issues            = TP + PD   (using each Issue's primary finding)
Exact Severity Accuracy    = detected Issues with reported severity = key severity / detected Issues with known key severity
Within-One-Level Accuracy  = detected Issues with |rank(reported) − rank(key)| ≤ 1 / same denominator
                             rank: info 0 < low 1 < medium 2 < high 3 < critical 4; unknown is never within one level
```

### 7.6 Efficiency

```
Cost per TP  = Σ cost_usd / Σ TP          over the runs being reported
Time per TP  = Σ agent_seconds / Σ TP
             = N/A when Σ TP = 0          never 0
```

`agent_seconds` is the sum of case-run durations. It does not change with `--concurrency`, so the
runtime of runs made at different concurrencies stays comparable. Wall-clock time is reported
beside it but is never compared. Tokens are reported the same way as cost.

---

## 8. Versioning

### 8.1 Harness version

```
harness_id = sha256(canonical JSON of {
  skills_digest, orchestrator_assets_digest, runner, agent_config_mode,
  disabled_components (ablation), tool_allowlist })
```

A human label (`v2.1`) maps to exactly one `harness_id` in the append-only file
`bench/harnesses.jsonl`. The model, runner and environment are held constant across a comparison.
`bench compare` already refuses a comparison where they differ. A model change is a different
experiment, not a harness delta.

### 8.2 Benchmark version (immutable)

A benchmark version is a manifest file `bench/benchmarks/<SUITE>-<MAJOR>.<MINOR>.json`:

```json
{"benchmark":"CORE","version":"1.0","status":"published","created":"…","change_reason":"…","supersedes":null,
 "key_schema":2,
 "cases":[{"id":"g01-inventory-reserve","key_sha256":"…","fixture_tree_sha256":"…","fix_tree_sha256":"…"}],
 "domains":["security","correctness","concurrency","resilience","data","api-compat"],
 "critical_domains":["security","data"],
 "detection_criteria_version":"dc-1",
 "metric_config":{"weighted_recall_w":0.5,"ci_level":0.90,"early_stop_ci_level":0.95,
                  "bootstrap_resamples":10000,"bootstrap_seed":1729,"consolidation":"strict-majority"},
 "replicates":{"k_min":2,"k_target":3,"k_max":4},
 "budgets":{…section 9…},
 "seeds":{"case_order":7},
 "canary":"<random 128-bit token>",
 "manifest_sha256":"<sha256 of the canonical JSON without this field>"}
```

- A published manifest is never edited. Changing a case, an Issue, a severity, a domain, a criterion,
  `w`, a budget or `k` creates a new version: MINOR for configuration-only changes, MAJOR for ground
  truth changes. The new manifest records `supersedes` and `change_reason`. CI refuses a diff that
  modifies a published manifest, or a file it hashes.
- **Comparability is equality of `manifest_sha256`.** The semantic version tells a reader how much
  changed. It never makes two versions comparable.
- Today's `corpus` digest covers case names, defect ids, requests and runs. It does not cover the
  bytes of `KEY.json`, `fixture/` or `fix/`, so a silent edit to a key line or a fixture leaves it
  unchanged. The manifest digests close that gap.

### 8.3 Decision policy version

Thresholds are expected to be recalibrated (section 13.6). If they lived in the benchmark manifest,
every recalibration would break comparability for no change in what is measured. They therefore
live in `bench/policies/policy-<n>.json` (deviation D5):

- A policy holds the section 13 thresholds, the significance rule, the benefit test and the trade-off
  rules.
- The candidate's benchmark runs record the policy id at `CREATED`. The comparison is decided under
  that policy.
- A later policy MAY re-decide the same stored data. The result is a separately labelled
  **re-decision** and never replaces the original verdict. Choosing a policy after seeing results is
  thereby visible in the record.

---

## 9. Suites and budgets

The goal is a small, stable, discriminating instrument. A case belongs in CORE only if it can
separate harness versions. The README's observation that 13 of 15 cases moved across skill
versions says the current cases do.

Initial values are below. The dollar figures come from the last recorded 15-case run ($12.83, about
$0.86 per case run) and the slowest case seen (5.4 min). Like the thresholds, they are starting
points.

| Budget | FAST | CORE | DEEP |
|---|---|---|---|
| Purpose | Smoke check on frequent development: evident regressions only. | The comparison instrument. | Releases, research, broad validation. |
| Cases (`max_cases`) | 6: 5 canaries + 1 clean control | 24 (all current cases) | CORE + expansion cases (performance, AI/LLM, architecture) |
| Replicates (`k_min`/`k_target`/`k_max`) | 1 / 1 / 1 | 2 / 3 / 4 | 3 / 5 / 6 |
| `max_attempts_per_case` (per replicate) | 1 | 1 | 1 |
| `max_retries` (infrastructure only) | 1 | 2 | 2 |
| `max_tokens` per case run | 400k | 1.5M | 3M |
| `max_cost` per case run / per suite | $1.50 / $8 | $3 / $90 | $5 / $300 |
| `max_runtime` per case run / suite wall clock | 8 min / 15 min | 12 min / 120 min | 20 min / 8 h |
| Verdicts it may issue | `SMOKE_PASS` · `SMOKE_FAIL` only | `PASS` · `REVIEW` · `FAIL` | `PASS` · `REVIEW` · `FAIL` |

- **FAST** canaries are the cases the baseline detects in at least 80% of its CORE runs. The
  selection is fixed in the FAST manifest. FAST never claims an improvement: it only says
  "nothing obvious broke". It fails on any canary Issue that drops to `FN`, or on any FP in the
  control.
- **CORE** is the only suite whose verdict says the harness improved.
- **DEEP** never blocks routine changes.

### 9.1 Early stopping

- **Replicate level (CORE, DEEP).** After each completed replicate `k ≥ k_min`, compute the decision
  (section 13). Stop when (a) a hard blocker that does not depend on frequencies (H4–H8) already
  holds, or (b) every statistical row of the matrix is **settled**, meaning its
  `early_stop_ci_level` interval of Δ lies entirely inside one band. Otherwise run another
  replicate, up to `k_max`. Stopping early uses the stricter interval because looking at the result
  between replicates inflates false decisions.
- **Hard blockers based on frequencies** (H1–H3) are final only at `k_max`. A hard blocker
  triggered earlier forces replicates up to `k_max`. It is never cut short, because one unlucky
  replicate of a non-deterministic harness should not fail a candidate.
- **Case level.** A case run ends when its own budget runs out. It is scored as it stands (5.3).
  This is the harness's outcome, not an exclusion.

---

## 10. Multiple runs

### 10.1 Per-run first, then aggregate

Every metric of section 7 is computed per benchmark run. Over the K `COMPLETED` runs of one harness
version the report gives the **mean, median, standard deviation, min and max**. Findings of
different runs are never pooled as if they came from one run.

### 10.2 Per-Issue frequencies

```
Detection Frequency(i)     = runs where i is TP        / valid runs
Any Detection Frequency(i) = runs where i is TP or PD  / valid runs
```

### 10.3 Consolidated Issue state (used by hard blockers)

Over K runs, strict majority:

```
TP  if  #runs(TP)      > K/2
PD  if  #runs(TP ∨ PD) > K/2   and not TP
FN  otherwise
```

With an even K, a tie goes to the lower state. The same rule applies to baseline and candidate.

### 10.4 Uncertainty and the resolution floor

CORE has 39 Issues today. In a single run, one Issue moves Strict Recall by **2.56 pp**, so a 2 pp
threshold is below what one run can resolve. Decisions therefore use:

- the **mean over K runs** of each metric, for the point estimate of Δ; and
- a **paired case-cluster bootstrap** for its interval. Cases are resampled with replacement
  (`bootstrap_resamples`, `bootstrap_seed` from the manifest). Each resample recomputes the metric
  difference from both harnesses' runs on those cases. The interval is the percentile interval at
  `ci_level`.

Resampling cases, not Issues or replicates, respects two facts. Issues of one case are correlated,
and the variability that matters is "on other defects like these". The fixed seed makes the
interval deterministic, so the verdict can be reconstructed exactly.

Small domains (one concurrency Issue today) cannot carry statistical rows. Domain rules are stated
in Issues, not percentages (13.2).

---

## 11. Domains

### 11.1 Taxonomy

`security`, `correctness`, `concurrency`, `performance`, `resilience`, `data`, `api-compat`,
`ai-llm`, `architecture`. Every Issue has exactly one domain. The manifest declares which domains
are critical. The initial proposal is `security` and `data`.

```
Domain Strict Recall(d) = TP_d / (TP_d + PD_d + FN_d)          N/A if the domain has no Issues
Global Strict Recall    = Σ_d TP_d / Σ_d (TP_d + PD_d + FN_d)   micro average: the reported result
Macro Strict Recall     = mean over domains with Issues of Domain Strict Recall(d)   diagnostic only
```

### 11.2 Proposed mapping of the current 39 Issues

The maintainer confirms this mapping when writing KEY v2. It is part of the ground truth.

| Domain | `class` values today | Issues |
|---|---|---|
| security | authorization (2), escaping, fail-open, injection, path-traversal, signature-reserialized-body | 7 |
| data | data-loss, idempotence (2) | 3 |
| concurrency | data-race | 1 |
| resilience | unbounded-retry, error-reporting | 2 |
| api-compat | n-1-payload-incompatible | 1 |
| correctness | every other class (off-by-one, rounding, timezone, parsing, normalization, …) | 25 |
| performance · ai-llm · architecture | — | 0 → `N/A` until DEEP adds cases |

The corpus has no performance Issues. Any claim about performance detection is `N/A` until DEEP
adds cases. The spec's performance rules exist so those cases have something to be measured against.

---

## 12. Expressing change

```
Gain_pp              = Candidate − Baseline              for every percentage metric, reported as "pp"
Relative Improvement = (Candidate − Baseline) / Baseline only when Baseline > 0; otherwise N/A
Runtime/Cost change  = (Candidate − Baseline) / Baseline reported as %, since these are not percentages
```

`78% → 84%` is reported as `+6 pp`, never `+6%`.

---

## 13. Decision

The decision is a pure function:

```
decide(baseline runs, candidate runs, manifest, policy) → PASS | REVIEW | FAIL (+ category, + reasons)
```

Its preconditions are I12 and at least `k_min` `COMPLETED` runs on each side. When they do not
hold, it returns `NO_DECISION` with a reason. `NO_DECISION` is not a verdict, and nothing may treat
it as `PASS`.

### 13.1 Matrix (policy-1, initial values)

`drop` means `baseline − candidate` in pp, and `rise` means `candidate − baseline`. The bands are
closed on the PASS side.

| Row | PASS | REVIEW | FAIL | Kind |
|---|---|---|---|---|
| Strict Recall | drop ≤ 2 | 2 < drop ≤ 5 | drop > 5 | statistical |
| Strict Precision | drop ≤ 3 | 3 < drop ≤ 5 | drop > 5 | statistical |
| Strict F1 | drop ≤ 2 | 2 < drop ≤ 5 | drop > 5 | statistical |
| Reproducibility | drop ≤ 5 | 5 < drop ≤ 10 | drop > 10 | statistical |
| False Discovery Rate | rise ≤ 3 | 3 < rise ≤ 5 | rise > 5 | statistical |
| Runtime (Σ agent_seconds) | +≤ 20% | +20% < Δ ≤ +50% | > +50% | ratio |
| Cost (Σ cost_usd) | +≤ 20% | +20% < Δ ≤ +50% | > +50% | ratio |
| New Critical FN | 0 | — | ≥ 1 (hard H1) | consolidated |
| New High FN | 0 | 1 | ≥ 2 | consolidated |
| Each critical domain | no Issue lower | exactly 1 Issue lower | ≥ 2 Issues lower (hard H3) | consolidated |
| Control FPR | no rise | rise of 1 case run | rise ≥ 2 case runs | count |

- A **new Critical FN** is a `critical` Issue whose consolidated state is `FN` in the candidate and
  not `FN` in the baseline. "N Issues lower" counts Issues whose consolidated state is lower in the
  candidate (`TP > PD > FN`).
- **Significance rule.** A statistical row whose point estimate falls in FAIL, but whose `ci_level`
  interval of Δ includes 0, is reported as `REVIEW (not significant)`. A noisy single reading never
  fails a candidate, and it never passes one either.

### 13.2 Domain regressions cannot hide

Critical-domain rows are evaluated independently of the global rows. A global improvement does not
offset them. "Global +3 pp, security 94% → 71%" is FAIL by H3 whatever the global rows say. For
non-critical domains, a drop of ≥ 2 Issues is reported as a REVIEW reason.

### 13.3 Hard blockers (always FAIL)

| Id | Condition | Category |
|---|---|---|
| H1 | A new Critical FN. | `REGRESSION` |
| H2 | A `critical` Issue consolidated `TP` in the baseline is consolidated `FN` in the candidate (reported apart from H1). | `REGRESSION` |
| H3 | ≥ 2 Issues lower in a critical domain. | `REGRESSION` |
| H4 | A candidate `TP_FINDING` fails confirmation (7.5) for an Issue whose baseline primary finding confirmed. | `EVIDENCE` |
| H5 | An invariant fails, or a manifest digest does not match. | `INSTRUMENT` |
| H6 | The ground truth reached the harness (section 15). | `LEAKAGE` |
| H7 | CORE cannot complete because of the harness: suite budget exceeded, or the same harness crash in every replicate. | `COMPLETENESS` |
| H8 | Runtime or cost above +200% and the benefit test (13.4) fails. | `EFFICIENCY` |

`INSTRUMENT` means "there is no evidence the candidate is safe", not "the candidate regressed". It
fails closed so a broken instrument can never produce a PASS (deviation D7). An infrastructure
failure is not a hard blocker: it yields `NO_DECISION`.

### 13.4 Trade-offs (closed list)

A trade-off can only turn a FAIL row into `REVIEW (trade-off <id>)`. It never yields PASS for that
row, and it never applies to a hard blocker, to recall, F1, reproducibility, an FN row or a domain
row.

| Id | Row it relaxes | Condition |
|---|---|---|
| T1 | Runtime, Cost | **Benefit test**: Strict Recall gain ≥ +5 pp with the interval's lower bound > 0, **or** at least one critical/high Issue raised to consolidated `TP` while no critical/high Issue is lower. |
| T2 | Precision, FDR | Strict F1 gain ≥ +3 pp with the interval's lower bound > 0. |

Worked examples under policy-1:

```
Recall 82→88 (+6 pp, CI>0) · Precision 91→90 · Runtime +8% · Critical FN 1→0
→ every row PASS, no blocker                                     → PASS

Recall 82→83 (+1 pp) · Runtime +110% · Cost +140%
→ Runtime and Cost FAIL; T1 fails (+1 pp < +5 pp, no critical gained)  → FAIL
   (with Cost at +240%, H8 also holds)
```

### 13.5 Precedence (no averaging)

```
1. Any hard blocker                               → FAIL — <category>
2. Significant regression in a critical domain    → FAIL — REGRESSION   (H3; listed for clarity)
3. Any row in FAIL after significance and trade-off rules → FAIL — REGRESSION | EFFICIENCY
4. Any row in REVIEW                              → REVIEW
5. Otherwise                                      → PASS
```

### 13.6 Calibration

Policy-1 thresholds are initial values. Once CORE has at least 10 `COMPLETED` runs of one
unchanged harness version, the standard deviation of each row across those runs (the A/A
variability) is measured. A new policy sets each row's PASS edge at no less than twice that
standard deviation. It is published as `policy-2`, before it is used, with that data cited.

---

## 14. Ablation

A component (a skill, a technique inside a skill, a stage of the flow) is kept because removing it
costs detection, not because it exists.

- `bench/components.json` lists each component's `id`, `kind`, and the mechanical way to disable it
  (omit the skill from the throwaway agent config, or set a flag the skill reads). An ablated
  harness is a distinct `harness_id`, because the disabled set is hashed (8.1).
- The experiment runs **full** and each **full − X** on the same CORE version and model, K runs
  each. It also runs a **floor**: the model with no harness skills, which measures the harness's
  total contribution.
- The contribution of X is the section 13 decision with full as baseline and full − X as candidate:

| Decision for full − X | Reading |
|---|---|
| FAIL | X is **essential**. |
| REVIEW | X **contributes**. Its rows name where. |
| PASS, and runtime, cost or component count drops ≥ 10% | X is **redundant**: evidence for removing it. |
| PASS, no saving | X is **neutral**: no evidence either way. |

- Leave-one-out misses redundancy between two components: each looks redundant alone because the
  other covers for it. When two or more components read redundant, the pair (full − X − Y) MUST be
  run before either is removed.

---

## 15. Invalid runs

A case run or benchmark run is `INVALID`, and excluded from every comparison, when:

1. external infrastructure failed and the failure was misrecorded as a harness outcome;
2. ground truth is missing: a case's key, fixture or fix tree does not match the manifest;
3. a case changed during the run (I6);
4. the harness saw the answers: the manifest `canary` (planted in every `KEY.json` and in a
   `fix/CANARY` file) or a path under `bench/cases/*/KEY.json` or `fix/` appears in the agent
   transcript, the workspace or the plan. This also triggers hard blocker H6;
5. the configuration differs from the frozen one (model, runner, agent config, skills digest);
6. findings cannot be adjudicated, for example because the kept plan is missing.

An `INVALID` run is never converted to 0%. Infrastructure that keeps failing produces
`FAILED_INFRASTRUCTURE` and `NO_DECISION`.

---

## 16. Reconstruction and report

### 16.1 The derivation chain

```
raw results ─▶ adjudication events ─▶ Issue/Finding states ─▶ per-run metrics ─▶ aggregates + CIs ─▶ decision ─▶ report
result.json     events.jsonl            states.json            metrics.json        comparison.json     decision.json   report.md
test-plan.md
transcript
```

Every artifact records the sha256 of each input it was computed from, and the scorer build that
computed it. Every stage after adjudication is a pure function. `tpp bench verify <comparison dir>`
recomputes the chain from the raw results and the event log, then byte-compares every derived
artifact. Any difference is an `INSTRUMENT` failure. "PASS" is therefore a recomputable fact, not
a statement.

### 16.2 Report format

The numbers below are illustrative. They are internally consistent with section 7, but they were not measured.

```text
Harness v2.1 vs v2.0                       harness ids 3f9c… / a1d0…
Benchmark: CORE 1.0 (manifest 7e21…)       policy-1 · model sonnet · runner pi · K = 3 / 3

Known Issues: 39 (22 cases) · clean controls: 2
Per run (mean of K):  TP 31.3 · PD 2.7 · FN 5.0 · FP 2.3 · Duplicates 4.0 · Low-value 1.7
                      Inconclusive 0.3 · Confirmed novel 0.7 · Invalid 0.0

                         baseline → candidate      Δ        90% CI          band
Strict Recall            72.6%   → 80.3%        +7.7 pp   [+2.6, +12.8]    PASS
Strict Precision         85.0%   → 86.2%        +1.2 pp   [−3.1, +5.9]     PASS
Strict F1                78.4%   → 83.1%        +4.7 pp   [+0.9, +8.6]     PASS
Detection Coverage       82.1%   → 87.2%        +5.1 pp
False Discovery Rate      8.9%   →  6.3%        −2.6 pp   [−5.9, +1.0]     PASS
Reproducibility          94.0%   → 96.0%        +2.0 pp                    PASS
Control FPR              0/6     → 0/6                                     PASS
New Critical FN / High FN   0 / 0                                          PASS

Domains (consolidated TP, baseline → candidate):
  security 6/7 → 7/7 ↑   data 2/3 → 2/3 =   concurrency 0/1 → 1/1 ↑   correctness 19/25 → 21/25 ↑
  resilience 1/2 → 1/2 =   api-compat 1/1 → 1/1 =   performance N/A

Runtime (Σ agent time, mean per run): 31m → 33m (+6%)     PASS
Cost (mean per run):                  $20.4 → $21.9 (+7%)  PASS
Cost per TP (Σ cost / Σ TP):          $0.72 → $0.70

FINAL DECISION: PASS
```

On failure, the reason names the evidence, not a feeling:

```text
FINAL DECISION: FAIL — REGRESSION
Blocker H3: security consolidated TP 7/7 → 5/7 (2 Issues lower).
Blocker H2: n07-tenant-scope/D1 (critical) consolidated TP in baseline (3/3) is FN in candidate (0/3).
```

The domain arrows compare consolidated TP counts: `↑` higher, `=` equal, `↓` lower. A domain with
no Issues shows `N/A`.

---

## 17. Deviations from the request

| Id | Request | This spec | Why |
|---|---|---|---|
| D1 | Total Raw Findings omits `CONFIRMED_NOVEL` and `INVALID`. | Includes them; adds Invalid Rate. | Otherwise the rates do not partition the output and the raw volume is understated. |
| D2 | `FPR = N/A` unless TNs exist. | `Control FPR` on clean controls. | The corpus has TNs, at case-run level, in its clean controls. |
| D3 | FAIL bands in pp. | Statistical FAIL needs an interval excluding 0, otherwise REVIEW. | 1 Issue = 2.56 pp in a single run: below the 2 pp threshold. |
| D4 | Invalid runs are excluded. | Harness-caused failures within budget count as FN; only external failures are excluded. | Excluding them would reward a harness for crashing. |
| D5 | Thresholds are part of the system. | A separate, versioned decision policy, bound at run creation. | Recalibration (13.6) must not break benchmark comparability. |
| D6 | "Run". | Benchmark run = one replicate; `--runs N` becomes N benchmark runs. | Per-run metrics need one well-defined unit. |
| D7 | "Benchmark corruption → FAIL". | FAIL with category `INSTRUMENT`; infrastructure failure → `NO_DECISION`. | It fails closed without claiming a regression that was never measured. |
| D8 | (implicit) | Findings without evidence are PD-eligible, where `MetricsVersion 2` discards them. | Criterion 4 makes evidence a condition of TP, not of being a claim. This bumps metrics to version 3. |
| D9 | Critical FN → FAIL. | FAIL, but frequency-based blockers are final only at `k_max`. | One unlucky replicate of a non-deterministic harness should not fail a candidate. |

---

## 18. Current instrument and gaps

| Concern | Exists today | Gap closed by phase |
|---|---|---|
| Issue ground truth | `KEY.json` v1, `key.go` (`Key`, `Defect`, `Trigger`), clean controls | KEY v2 fields: `severity`, `domain`, criteria, reproduction (2) |
| Finding extraction | `FindingRowsText`, `FindingRowFingerprint`, Evidence-ledger linking | Finding ids, `INVALID` admission, unlinked rows PD-eligible (2) |
| Adjudication | `adjudication.json`: `defect`, `false_positive`, `out_of_scope`, `Superseded` history | Fact-based decisions, 8 finding outcomes, `equivalent_to`, hash-chained event log, `REOPENED` (2) |
| State machines and invariants | implicit flags (`Invalid`, `Failed`, `PendingAdjudication`) | Explicit machines, transition guard, I1–I14 validators (2) |
| Reproduction | Catch oracle in `discriminate.go`, three attempts for nondeterministic classes | Confirmation re-run, reproducibility rate (3) |
| Metrics | `score.go`: found / confirmed / caught, recall, precision (MetricsVersion 2) | Section 7 in full, per domain, micro/macro, Control FPR (3) |
| Multiple runs | `--runs N`, unique-defect units, `unstable_cases` | Per-run-first aggregation, consolidated states, bootstrap CI (3) |
| Versioning | `Provenance`, `corpus` digest (names and ids only), `compare.go` refusals | Byte-level manifests, harness ids, policies, CI immutability check (4) |
| Suites and budgets | `--max-cost-usd`, per-case timeout, `--cases` glob | FAST/CORE/DEEP manifests, budget enforcement, early stopping (4) |
| Decision | `Comparison.Markdown()` (no verdict) | `decide()`, hard blockers, trade-offs, report, `bench verify` (5) |
| Ablation | — | Component registry, ablated harness ids, floor run (5) |
| Leakage | Key never copied into the workspace (rule) | Canary planting and transcript/workspace scan (4) |

Phases 2–5 are not authorized by this document. Each one is planned as its own chained PR.

## 19. Open decisions for the maintainer

1. Confirm `critical_domains` (`security`, `data` proposed).
2. Assign `severity` and confirm `domain` for the 39 Issues with the rubric in 2.4, before any v2 run.
3. Confirm the CORE budget ($90 ceiling at `k_max = 4`; about $62 expected at `k = 3`).
4. Name the adjudicators: which people, and which versioned rules, may record facts.

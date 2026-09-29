---
name: llm-eval-design
description: "Trigger: llm eval design, llm judge, judge validation, eval regression, prompt regression in CI, eval statistics, judge bias, error analysis, eval contamination, model drift canary. Design evals and validate LLM judges for an LLM feature."
license: Apache-2.0
metadata:
  author: "alesierraalta"
  version: "0.1.0"
  requires_tpp: "0.4.1"
  scope: [llm, evals]
  auto_invoke: "Designing or gating evals for an LLM feature: failure-mode criteria, judge validation, statistics, CI regression, drift"
---

## Activation Contract

Load when building or gating evaluations of an LLM feature: choosing what to measure, using an LLM as judge, comparing prompt or model versions, or wiring eval regression into CI.

Applicability gate: no judge, no prompt or model change, and no quality claim to defend means nothing to load. Skip judge validation when grading is fully deterministic (schema, regex, exact match).

NOT for: agents, tool use or multi-turn state (`agent-eval`); adversarial and injection testing (`llm-redteam`); retrieval and grounding (`rag-audit-evaluator`); the general "metric must drop on a broken pipeline" principle (`silent-degradation`, Rule 3); deterministic unit tests (`test-strategy`).

## Hard Rules

1. **Error analysis before metrics.** Read 50-100 real (or realistic) traces, write down how each fails, and derive criteria from those failures. Generic metrics (helpfulness, coherence) only help pick traces to read. Criteria drift as you read outputs; re-check them.
2. **Binary pass/fail per failure mode**, each with a written definition and examples. No Likert scales as a gate.
3. **Validate every LLM judge against human labels** on a held-out split (dev to tune the prompt, test to report). Report TPR and TNR with confidence intervals (Wilson or bootstrap). There is no universal kappa threshold; kappa is prevalence-sensitive and hides rare-failure recall.
4. **Correct pass rates for judge error.** A raw judge pass rate is biased; apply the sensitivity/specificity correction (Rogan-Gladen) and propagate calibration-set uncertainty. When TPR + TNR is near 1 the correction is unstable: the judge is not usable, say so.
5. **Bias battery for judges.** Position (repeated randomized orders, mapped back to candidate identity; a single disagreement is `INCONSISTENT`, not proof of bias), verbosity (pad one candidate), self-preference (judge family differs from candidate family), plus format and authority cues.
6. **Statistics, not point estimates.** Report bootstrap CIs; compare versions with paired differences on the same items; use clustered SEs when items share a document, user or context; state sample size and power for the effect you need to detect; be cautious with many slices.
7. **Contamination and leakage.** Golden items must not appear in prompts, few-shot examples, fine-tuning data or the judge prompt. Include a shuffled-label control; the general negative-control principle is `silent-degradation` Rule 3.
8. **Regression in CI with pinned model IDs.** Pin the exact dated model ID for candidate and judge, never an alias; record temperature, seed where supported, and prompt version. Run the eval on every prompt or model diff.
9. **Drift canary.** Run a fixed eval set on a schedule against the production model ID; a provider changes behavior behind a stable name.
10. **Cost, latency and token budgets are tracked metrics** with a baseline; a regression gate fails on a missing or exceeded budget.

## Decision Gates

| Situation | Action |
| :--- | :--- |
| No labeled traces yet | Rule 1 first; nothing else is meaningful |
| Output checkable by code | Code grader; no judge |
| Subjective criterion | Binary judge validated per Rules 3-5 |
| Comparing two versions | Paired bootstrap on shared items (Rule 6) |
| Judge fails TPR/TNR needs | Fix criterion or prompt on dev; do not gate with it |
| Structured output | Also test truncation (`max_tokens` cuts JSON) and refusals |

## Execution Steps

1. Error analysis and failure-mode list (Rule 1); grow the eval set from production traces over time.
2. Choose a grader per failure mode: code, judge, or human.
3. Label 100-200 items per judged failure mode; split dev/test; validate with `assets/judge-validation.py` and run the Rule 5 battery.
4. Compute corrected pass rates and CIs; compare versions with paired bootstrap.
5. Add contamination controls, budgets, pinned IDs and the CI trigger; schedule the canary.
6. Report.

## Output Contract

- Failure modes found, with trace counts and their graders.
- Per judge: TPR, TNR, CIs, split sizes, bias-battery results, corrected pass rate.
- Version comparisons: paired difference with CI and sample size.
- Pinned model IDs, budgets, drift-canary status.
- Verdict `PASSED` | `FAILED`; a pass means "not found by these evals".

## References

- `assets/judge-validation.py`: TPR/TNR with Wilson CIs, Rogan-Gladen correction and bootstrap CI (stdlib); run `python3 assets/judge-validation.py --selftest`.

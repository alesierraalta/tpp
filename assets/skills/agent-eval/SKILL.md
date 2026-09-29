---
name: agent-eval
description: "Trigger: agent eval, tool calling accuracy, agent trajectory, pass^k, pass@k, agent loop detection, tau-bench, grader tampering, multi-turn agent test, agent side effects. Evaluate agents that call tools and change state by outcome, not transcript."
license: Apache-2.0
metadata:
  author: "alesierraalta"
  version: "0.1.0"
  requires_tpp: "0.4.1"
  scope: [llm, agents]
  auto_invoke: "Evaluating an LLM agent that calls tools or changes state: final-state graders, pass^k, loops, side effects, grader tampering"
---

## Activation Contract

Load when evaluating an agent: an LLM that chooses tools, takes multiple steps, or changes external state (files, database, API, tickets).

Applicability gate: a single-shot LLM call with no tools has no trajectory; use `llm-eval-design`. Loop detection is pointless on one-call flows.

NOT for: judge validation and eval statistics (`llm-eval-design`); injection, exfiltration and jailbreaks (`llm-redteam`); retrieval quality (`rag-audit-evaluator`).

## Hard Rules

1. **Grade final environment state, not transcript text.** Compare the resulting database, filesystem or API state to the goal state (tau-bench style). An agent that says "done" proves nothing. Reset the environment before every trial.
2. **Tool-call correctness on what matters.** Right tool, correct critical arguments, and order only where order changes the outcome. Tool-name precision/recall alone is a diagnostic, not a gate.
3. **Repeat trials.** One run is not evidence. Run N trials per task with model ID, temperature and seed (if supported) recorded. Report pass^k (all k trials succeed) for reliability and regression, pass@k (any of k) for capability. They answer different questions; never swap them. Use `assets/pass-k.py`.
4. **Detect semantic loops**, not only exact hashes: canonicalize arguments, catch alternating cycles and failed-retry thrashing. A legitimate retry after a transient error or a polling call is not a loop; set a step cap and report step inflation across versions.
5. **Side effects and irreversible actions.** Assert no unintended writes, deletes, sends or spend, and that irreversible actions require the confirmation the design promises. Reaching the right state through a harmful path is a failure.
6. **The agent must not be able to modify its grader.** Run graders and tests outside the agent's writable scope; check the grader files' hash after the run. An agent editing its tests to pass is a reward-hacking finding.
7. **Read transcripts.** Sample failing and passing runs and attribute each failure to the first upstream error (wrong tool, bad argument, misread state, environment bug). An eval that never fails is not challenging enough.
8. **Multi-turn user simulation** only where the task needs dialogue; pin the simulator model ID and check the simulator itself for drift or leaking the answer.
9. **Structured outputs:** validate schema conformance strictly (reject extra fields), and separately check correctness of the values, truncation and refusals.

## Decision Gates

| Situation | Action |
| :--- | :--- |
| Task changes state | Final-state diff grader (Rule 1) |
| Answer-only task | Code or validated judge grader (`llm-eval-design`) |
| Capability exploration | pass@k on a hard suite |
| Regression or release gate | pass^k on a stable suite |
| Agent can write files it is graded on | Rule 6 controls before any score |
| Destructive or paid tools | Rule 5 probes with sandboxed effects |

## Execution Steps

1. List tasks from real failures (20-50 to start) and define each goal state.
2. Build a resettable sandbox; put graders outside the agent's write scope.
3. Run N trials per task; record versions and settings.
4. Grade final state, then side effects, then tool-call diagnostics and loop signals.
5. Compute pass@k and pass^k with `assets/pass-k.py`; read failing transcripts.
6. Report.

## Output Contract

- Tasks, trials, versions and settings.
- pass@k and pass^k per suite, with the trial counts behind them.
- Failures by first upstream cause; loop, step-inflation and side-effect findings.
- Grader-integrity check result.
- Verdict `PASSED` | `FAILED`; a pass means "not found by these tasks".

## References

- `assets/pass-k.py`: unbiased pass@k and pass^k estimators from n trials and c successes (stdlib); run `python3 assets/pass-k.py --selftest`.

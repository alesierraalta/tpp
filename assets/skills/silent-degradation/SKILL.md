---
name: silent-degradation
description: "Trigger: funciona pero está mal, la precisión es baja y nadie se entera, el sistema dice que todo OK, degradación silenciosa, se pierde data sin error, un filtro o permiso que recorta de más, calidad sin métrica."
license: Apache-2.0
metadata:
  author: "alesierraalta"
  version: "1.1"
  scope: [common]
  auto_invoke: "Hunting silent quality loss in a system that reports success"
---

## Activation Contract

Load when the system REPORTS SUCCESS and the outcome is still wrong or degraded: RAG
precision quietly low, a filter or permission scope silently dropping legitimate data,
results getting worse with no error, "we have no idea if this is any good", or before
trusting any pipeline whose only health signal is "it ran".

Sibling of `exploit-testing`, which hunts for a red. This one hunts for a GREEN THAT
LIES. NOT for: crashes, exceptions, failing tests, or security exploitation.

## Hard Rules

1. **A system's self-report is a claim, not a measurement.** "OK", exit 0, 200, "0
   errors" say the code RAN, never that the outcome is CORRECT. Never accept them as
   quality evidence.
2. No quality claim without a NUMBER, an appropriate frozen oracle (goldset, invariant,
   reconciliation equation, or contract; see the oracle table in `references/measurement.md`),
   and the measurement date. "It works well" is not a result.
3. **Validate the metric before the system**: deliberately break the pipeline (empty the
   index, shuffle the retrieval, drop the reranker, corrupt an input) and confirm the
   metric DROPS. A metric that stays green on a broken system measures nothing — that is
   the first finding, and it outranks everything else. A metric or alert must also be
   proven able to FIRE: synthetic breach, observed alert or metric change.
4. Every stage reports **in / out / dropped, with a named reason per drop**, under a
   stage-appropriate equation declared up front. Fan-out, aggregation, joins, retries and
   sampling do not satisfy `out + dropped == in`. An unattributed drop is a defect under
   the declared equation.
5. Every filter, permission scope and truncation must be probed WITH and WITHOUT it, and
   the difference set inspected item by item. Over-restriction is invisible by design.
6. Never conclude from one sample of a nondeterministic stage: judge it over N runs and
   the oracle set. A deterministic stage needs one run over the full oracle set.
7. Report what the system CANNOT observe as a finding, then classify it: release blocker,
   observability debt, or accepted risk with a reason. A missing counter is a risk
   classification, not an automatic defect.

## Decision Gates

| Symptom | Attack |
|---|---|
| No idea whether quality is good | Build the goldset, measure, freeze the baseline — `references/measurement.md` |
| Quality got worse, no error anywhere | Stage counters + drop attribution — `references/silent-loss.md` |
| Everything green, output still bad | Break it on purpose; if the metric holds, the metric is the bug (Rule 3) |
| Don't know WHICH stage loses quality | Ablation + ceiling analysis — `references/measurement.md` |
| Suspect a filter/permission over-restricts | With/without diff of the result sets — `references/silent-loss.md` |
| Nobody would notice a regression tomorrow | Wire the metric to CI/alerting as a threshold, then prove it fires with a synthetic breach (`promtool test rules` for Prometheus rules tests rule logic, not delivery) |
| Loss under concurrency, retries, or time/numeric edge cases | Conservation probe under fault injection — `references/silent-loss.md` |

## Execution Steps

1. Write the OUTCOME the system exists to produce, and the number that would prove it.
   Not "ran", not "no errors" — the result quality.
2. Inventory every silent-loss site along the path (`references/silent-loss.md`) and
   instrument it: in/out/dropped + reason.
3. Pick the oracle type, build or freeze it, measure the baseline; record the number and the date.
4. Validate the metric with a deliberate break (Rule 3) and try to satisfy it while making
   the outcome worse (Goodhart) before believing any of it.
5. Attribute the loss: ablate each stage; replace each stage with a perfect oracle to see
   the ceiling. The stage whose removal changes nothing is broken or unmeasured.
6. Diff every filter/permission with and without it; justify every excluded item.
7. Fix the OBSERVABILITY GAP too, not just the defect: leave the counter and the
   threshold behind, and fire the alert once with a synthetic breach.

## Output Contract

The stage table (stage → in → out → dropped → reason), the baseline number with its
oracle and date, the metric-validation result (what broke, how much it dropped), the alert
fire result, the ablation table, and defects split into `quality loss` vs `system
blindness`. Say plainly what remains unmeasured.

## References

- [references/silent-loss.md](references/silent-loss.md) — where data and quality vanish without an error.
- [references/measurement.md](references/measurement.md) — goldsets, baselines, metric validation, ablation, ceiling analysis.
- [assets/quality-audit-template.md](assets/quality-audit-template.md) — audit artifact.
- `~/.claude/skills/tsp/SKILL.md` — what deserves testing at all (the router).
- `~/.claude/skills/exploit-testing/SKILL.md` — adversarial sibling (hunts reds).
- `~/.claude/skills/implementation-theater/SKILL.md` — sibling for code that runs but does nothing (a dead knob or a silent fallback is often the cause of the quality loss).

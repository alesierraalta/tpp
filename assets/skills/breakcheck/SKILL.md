---
name: breakcheck
description: "Trigger: breakcheck, romper esto, intenta romperlo, testing adversarial, pruebas adversariales, casos limite, edge cases, regresiones, robustez, seguridad, supuestos, before RDD. Bounded adversarial campaign on one candidate: select the few risks worth attacking, run falsifiable probes under a stated budget, and report evidence plus an explicit readiness disposition."
license: Apache-2.0
metadata:
  author: "alesierraalta"
  version: "0.1.4"
  requires_tpp: "0.3.8"
  scope: [common]
  auto_invoke: "Explicit invocation to break a change before RDD: bounded adversarial probes and a readiness disposition"
---

# breakcheck

Break the candidate, not confirm it. The existing Verifier answers "does it work"; breakcheck answers "what invalidates its assumptions". Run a bounded adversarial campaign that turns selected risks into reproducible evidence and a readiness recommendation.

## Activation Contract

Load only on explicit invocation ("breakcheck", "romper esto", or "testing adversarial") for one named candidate. Do not load for whole-repository audits, routine unit tests, PR review, releases, or coverage targets.

## Probe Selection

- **Aim the campaign at what the change just introduced.** New logic is where the specification and the
  change's own tests are most likely to have skipped a case; probing the code around it re-tests what its
  author already believes.
- **Enumerate the cases the specification and the tests do not list, and make one of them a probe.** The
  listed cases are the ones somebody already thought about; the unlisted ones are where a campaign has
  found its material findings.
- Start with assumptions whose failure would block readiness or change the project authority's decision.
- Prefer boundary, malformed, repeated, concurrent, interrupted, and unauthorized inputs when they match the candidate's contract.
- For regressions, compare the smallest safe behavior against the base; do not treat base behavior as correct without a contract.
- For integration risks, exercise the real seam that the candidate claims to use.
- For security risks, use synthetic data and non-destructive inputs; stop before shared or remote state without authorization.
- Define the invariant, input, oracle, and evidence location before spending a probe.
- Prefer one causal change per probe so a failure has a smallest reproducer.
- Bound randomness, retries and iterations inside the stated budget: cap time, seeds and iterations, not concurrency, since fewer workers can suppress the race you are hunting. Record the seed.
- Prefer deterministic inputs and preserve enough output to reproduce a failure.
- Drop probes that cannot change a disposition, and name each omission in the report.

## Hard Rules

1. Identify the candidate before probing: project/root and a human-readable execution label, plus base and HEAD and the relevant staged, unstaged, and untracked content; HEAD alone is not what runs.
2. Risk, not coverage: enumerate the few assumptions worth attacking across correctness, normal and edge cases, regressions, integration, robustness, and security; explicitly drop the rest.
3. State a finite budget (number of probes and wall-clock time) before the first probe; stop at the limit.
4. Every probe is falsifiable: "if <invariant> breaks, this goes red"; record expected and observed state plus an evidence reference.
5. Isolate proportionately to the risk; a worktree is not a sandbox, and shared, production, or remote resources need explicit authorization.
6. Use no global score: use PASS, WARN, FAIL, INCONCLUSIVE, or N/A per applicable check.
7. Missing required evidence or any open blocker means not ready; WARN needs an explicit accepted disposition by the project authority, and accepted risk stays visible with its reason.
8. An honest negative is valid: "no defect observed within scope X under budget Y" never claims the code is free of defects.
9. Readiness is a recommendation only: never claim RDD approval, review authority, delivery authorization, or a substitute for the Verifier.
10. Report a finding only with a reproducible path (input, command, or sequence) and the smallest reproducer; unsupported suspicion is a hypothesis, not a finding.

## Execution Steps

1. Read the candidate, its contracts, and any Verifier evidence — and read the change's own tests, writing
   down what they do not cover before choosing a single probe.
2. List assumptions and choose the few probes worth running; record omitted risks and why.
3. State the probe and time budget before the first probe.
4. Run probes in isolation and stop when the budget is exhausted.
5. Record expected and observed state, evidence references, environment, and disposition.
6. Use exit status, returned values, files, logs, and observable state as evidence; a plan, transcript, or claimed invocation is not execution proof.
7. Re-run the smallest reproducer for a suspected finding before reporting it.
8. If a probe cannot run, mark INCONCLUSIVE or N/A with the reason; never convert inability into PASS.
9. Write the report from `assets/readiness-report-template.md` at the path the caller names.
10. Escalate when green: if the bounded campaign finds nothing on a high-risk candidate, hand method depth (mutation survivors, properties, concurrency, fuzzing) to `exploit-testing` instead of declaring ready; record the handoff as residual risk.
11. Feedback on the run follows the router's feedback rule (`test-strategy`), including blocked or partial runs.

## Disposition Guide

- Mark PASS only when required probes and evidence support the stated outcome with no unresolved blocker.
- Mark WARN for a bounded concern or omission only when the project authority explicitly accepts its disposition.
- Mark FAIL for a wrong required behavior or a blocker that prevents the stated outcome.
- Mark INCONCLUSIVE when the finite evidence cannot distinguish the relevant outcomes.
- Mark N/A only when the probe is outside scope and the reason is recorded.
- Cite the exact scope and budget whenever reporting a negative result.
- Separate missing required evidence from a probe that actually failed.
- State whether each warning was accepted, by whom, and for what reason.
- Do not hide an unresolved blocker inside a nominally successful check.
- Keep accepted risks, missing evidence, and residual risk visible in the report.

## Output Contract

Reply with the candidate identity, budget, each probe and its disposition, findings, omissions and residual risk, readiness disposition, report path, and whether feedback was recorded or failed.

## Non-goals

Do not add a framework, command, schema, or parser. Do not require a full ladder or whole-repository sweep. Do not fix autonomously. Do not add host or Pi integration.

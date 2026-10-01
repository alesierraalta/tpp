---
name: implementation-theater
description: "Trigger: esto realmente se usa, código muerto, nadie lo llama, está cableado, es un stub, finge que funciona, el flag está apagado, hay dos implementaciones, el nombre miente."
license: Apache-2.0
metadata:
  author: "alesierraalta"
  version: "1.1"
  scope: [common]
  auto_invoke: "Proving code is real: reachable, doing the work, unique, and honest"
---

## Activation Contract

Load to decide whether code is REAL or PROPS: nothing calls it, a flag or env may keep it
off, it may be a stub that fakes the work, there may be a second copy that actually runs,
or its name/docs may claim something the body does not do. Standard pass over
AI-generated or long-lived code before trusting or deleting it.

Siblings: `exploit-testing` (hunts reds), `silent-degradation` (hunts a lying green).
This one hunts CODE THAT ISN'T DOING ANYTHING.

## Hard Rules

1. **"No callers" from a tool is a HYPOTHESIS, never a verdict.** Detection is easy;
   triage is the whole job. Resolve every candidate into exactly one: dead ·
   dynamically loaded · referenced from config/DI · public API for a consumer ·
   merged-but-never-wired. If an external consumer, generated registration, production
   configuration, or unavailable environment prevents proof, report `unproven` rather than
   forcing dead or not-dead.
2. Reachability is proven FORWARD from the production entry point, and the evidence is an
   EXECUTION that touches the line (coverage, log, counter, trace) — not a call graph you
   read. State the highest rung proven (`references/reachability.md`, R0–R5). At R5,
   not observed is not the same as does-not-run.
3. **Merged ≠ running.** Flag state, env var, route mounted, DI registered, migration
   applied, consumer/cron actually up: all are part of reachability.
4. A branch you cannot make execute in any proven environment is `unproven`; call it dead
   only after dynamic/config/registry/deployment triage fails.
5. **Test risk-bearing knobs, not every parameter**: before perturbing a parameter, timeout,
   threshold or config value, declare the observable that MUST change (the oracle) and safe
   bounds. A parameter that changes nothing within its contract is decorative — report it.
6. Error masking that fakes success is detected here; the metric/observability defect it
   hides belongs to `silent-degradation`. Intentional fallbacks need an explicit contract
   and telemetry.
7. Two implementations (this skill owns clone detection): find which one RUNS, prove the last
   fix landed there, then consolidate or record the divergence with evidence. Duplication
   alone is not the finding; a violated shared contract or demonstrated behavioral
   divergence is. Clone history is corroboration, not proof.
8. Names, docstrings, READMEs and PR text are CLAIMS. Falsify each against the line that
   would make it true. No such line → the claim is false, and that is a defect.
9. **Concurrency and time claims are falsified by contention, not reading**: a lock created
   per call, a mutex copied by value, an un-awaited coroutine/promise, a goroutine never
   joined, or a timeout accepted but never passed down. Probe in `references/fakery.md`.
10. **The audit proposes deletion, never deletes.** Removal is a separate authorized change.

## Decision Gates

| Suspicion | Move |
|---|---|
| Nothing seems to call it | R0–R5 chain, then the triage taxonomy — `references/reachability.md` |
| It runs but may not do the work | Fakery catalog + a bounded knob test — `references/fakery.md` |
| Proof depends on an external consumer, registry, config, or environment you cannot inspect | Mark `unproven`; name the next proof instead of declaring dead |
| A lock, async, parallel or timeout claim | Contention probe — `references/fakery.md` |
| It works in tests, unknown in prod | Rung R3/R4: flag, env, wiring, then a counter in the real path |
| Two similar implementations (also handed over by `clean-architecture-audit`) | Divergence diff — `references/clones-and-claims.md` |
| The name/doc promises something | Claim falsification — `references/clones-and-claims.md` |
| It is genuinely dead | Propose deletion with evidence; perform it in a separate authorized change |

## Execution Steps

1. List the production entry points (route table, CLI, worker, cron, composition root).
2. For each symbol under audit, climb R0→R5 and record the highest rung PROVEN.
3. Triage every unreachable candidate into the five-way taxonomy (Rule 1).
4. Walk the fakery catalog over the reachable code. Run the knob test only on risk-bearing
   parameters after declaring its oracle and safe bounds; run the contention probe on lock,
   async and timeout claims.
5. Diff every near-duplicate; establish which copy runs and whether a shared contract is
   violated or behavior demonstrably diverges.
6. Falsify every claim: name, docstring, README, PR description, comment.
7. Report; for confirmed dead code, propose deletion with the evidence. Do not delete it in
   the same audit change.

## Output Contract

The reachability table (symbol → highest rung proven → verdict → confidence → evidence →
unknowns → next proof), then findings in three buckets: `not observed as running` · `runs but
fakes` · `claims exceed implementation`. Each with execution evidence, not a reading. Name
explicitly what stays at rung R2 or below — `unproven` in production is a legitimate and
useful verdict.

## References

- [references/reachability.md](references/reachability.md) — R0–R5 chain, triage taxonomy, deployment reality.
- [references/fakery.md](references/fakery.md) — stubs, error masking, disconnected knobs.
- [references/clones-and-claims.md](references/clones-and-claims.md) — divergent copies, false names and docs.
- [assets/theater-audit-template.md](assets/theater-audit-template.md) — audit artifact.
- Siblings: `~/.claude/skills/tsp/SKILL.md` (the router) · `exploit-testing` (hunts reds) · `silent-degradation` (green that lies) · `dependency-legitimacy` (is the dependency real).

---
name: real-run-validation
description: "Trigger: terminé una implementación, validar que funciona de verdad, prueba real, ejercitar end-to-end, real-run, prove it runs. After finishing an implementation, drive the real code with real inputs and observe behavior — not just trust asserts."
license: Apache-2.0
metadata:
  author: "alesierraalta"
  version: "1.2"
---

# Real-Run Validation

## Activation Contract

Run after an implementation that changes wiring, packaging, configuration, an entry
point, or a user journey, BEFORE declaring it done or opening a PR. Green unit tests
prove the cases the author imagined; a real run reveals what actually happens.
Record "not applicable: <reason>" instead of running when the diff is docs/comment/
test-only, or is pure logic whose unit test already exercises the real artifact.

## Hard Rules

- Target safety first: state the target (host, environment, data set) before running.
  Never run against production or shared environments or with real credentials. Use
  synthetic data only, and start the target with an isolated HOME
  (`HOME=$(mktemp -d)`) and no cloud profile or credential variables. `env -i` alone is
  not enough when HOME still points at the operator's: SDKs such as boto3 read
  `~/.aws/credentials` and can reach real services at startup. If only an unsafe target
  exists, stop and report it.
- Exercise the REAL built artifact (installed module / built binary / running
  service), never a mock or reimplementation, and never the source tree shadowing it.
- Prove artifact identity before the run: print which build actually executed (installed
  dist version plus the module's import path; binary path plus `--version`; image digest).
  A path inside the source checkout when an installed build was intended is a defect in
  the run, not a pass.
- Use representative synthetic inputs (a plausible payload, file, or request), not
  `foo`/`bar` placeholders.
- OBSERVE and report actual outputs and side effects; do not infer success from exit
  code alone.
- Run a negative control at the real boundary: one input the contract must reject
  (bad input, tamper, wrong auth), observed rejected fail-closed.
- Final evidence comes from a clean re-run in a fresh environment (new venv/HOME/temp
  dir), not from a session that accumulated state.
- State plainly what was validated vs what was NOT reachable this way.
- Never invent output. If you cannot run it, say so and stop.
- Evidence: every finding carries an executed evidence record per
  `~/.claude/skills/tsp/references/evidence.md`; no finding from reading alone.
- Put throwaway drivers in a scratchpad/temp dir, never in the repo diff. Remove them only after previewing the exact owned path and explicitly confirming destructive deletion; retain rollback/inspection artifacts when required and report skipped ambiguous items.

## Plan Contribution

When invoked by `tsp` in PLAN mode: do not execute. Return target rows for the plan:
target (journey or runtime surface) · check (real-artifact drive: happy path plus one failure
path) · target depth · consequence class · why. Cover the two or three journeys the business
cannot lose and every runtime surface the change touches.
Also fill the plan's "Real-run recipes" table for every journey you contribute (start command, data setup, sample requests, expected observable); EXECUTE reuses it instead of rediscovering ports, seeds, and codes.

## Decision Gates — how to drive by runtime surface

| Surface | How to drive it |
|---------|-----------------|
| Library / pure function | Throwaway driver script that imports the real module; run round-trip + failure case (see `assets/driver_template.py`) |
| HTTP endpoint / API | Real request (curl / http client) against the running service; inspect status + body |
| CLI | Invoke the built binary with real args; check stdout/stderr/exit + effect |
| DB / migration | Apply, then query to confirm the observable state changed |
| UI / flow | Drive the actual flow (the `run` skill); screenshot or read resulting state |

## Execution Steps

1. Identify the runtime surface of the change (table above).
2. Build/install into a fresh env so the driver hits the REAL artifact, then print its
   identity. Record the isolated HOME, environment, and identity check in the recipe so
   EXECUTE starts the target the same way.
3. Write a minimal driver with synthetic inputs covering: happy path (exact
   round-trip / expected effect) AND the negative control.
4. Run it, then repeat in a fresh env for the final evidence. Capture verbatim output.
5. Compare each observed behavior against the intended contract.
6. Report the observation table; flag any mismatch as a defect, not a nit.
7. Preview scratchpad cleanup and explicitly confirm deletion of only the owned driver/artifacts within a bounded target; retain rollback/inspection artifacts and report ambiguous or skipped items. Keep all drivers out of the commit.

## Output Contract

Return a compact table: behavior tested → observed result → matches contract?
Include the artifact identity line and the target. State the two validation layers explicitly (deterministic tests vs observed
real run), and name anything the real run could not exercise.

## References

- `assets/driver_template.py` — starter driver: identity proof, synthetic input, happy path + negative control.

---
name: test-strategy
description: "Trigger: haz el testing, testea esto, prueba esto, test this, test the app, test my change, run the testing, calibra el testing, calibrate the testing, test strategy, what to test, where to start testing, test planning, testing priorities, test altitude, low coverage, legacy code testing, test plan for the app, testing roadmap, plan de testing, roadmap de pruebas. Organic entry point for testing: infers scope and mode from repository state, builds or resumes a persisted test plan, and executes it through specialized testing skills."
license: Apache-2.0
metadata:
  author: "alesierraalta"
  version: "0.4.1"
  requires_tpp: "0.4.1"
  scope: [common]
  auto_invoke: "Any request to test something: infer scope and mode from repo state, build or resume the persisted plan, execute it through specialized testing skills"
---

## Activation Contract

Load on ANY request to test something ("haz el testing", "test this") and on the strategic
questions. The phrase is the whole instruction: read repository state, infer mode and scope
from the state table, act. Never ask which mode; one question only if the target is ambiguous.

- **PLAN**: inventory → layer sweep → rank → assign → persist; then EXECUTE unless asked for
  the plan only. **EXECUTE**: next pending target → sandbox → sibling → update the plan.

This skill decides WHICH targets and routes; siblings do the work.

## Tooling

Written for `tpp 0.4.1` (`tpp version` prints the build). Build it with `make build` (writes
`bin/tpp`) and put it on `PATH`, or use `go install github.com/alesierraalta/tpp/cmd/tpp@latest` once published. Without the binary, fall back and say so in the report, since a
hand-applied gate is a weaker claim than the binary's:

- `plan init` -> copy [assets/test-plan-template.md](assets/test-plan-template.md) (rule 12).
- `plan check` -> apply its checks by hand: Findings is a table; a finding row carries `path:line`
  and an existing evidence id, no id repeats, no `razonado` row sits in the ledger; settled rows name
  a pinning test; statuses stay in the closed vocabulary; the `Run` column holds valid slugs; a
  prose line closes a table and an unclosed fence fails; `Light:` and `Micro:` declarations are
  corroborated by a ranked target or a `path:line` citation (Light gives a reason per skipped layer;
  Micro reads `touches none`, has no Layer matrix and no `Light:`, and its ledger holds an `observado`
  row and a row with a filled `Mutate` cell).
- `doctor` -> decide CodeGraph availability from `codegraph` and `git ls-files`.

## Hard Rules

1. **Risk, not the file, is the unit of decision.** Rank qualitatively
   ([references/prioritization.md](references/prioritization.md)); no numeric scores or targets.
2. **Falsifiable evidence per selected risk**: mutation, contract, invariant, negative control,
   differential, metamorphic, or observed state. Coverage percentage is not evidence. Every
   expected value has a source, best first: written spec or issue text > invariant or round trip >
   independent reference model or differential > metamorphic relation > documented example >
   current behavior. A finding whose only oracle is the code's current behavior is characterization
   (rule 4), never `confirmed`; without a better source, ask or record `razonado`.
3. **Contract boundary that survives a refactor** ([references/altitude.md](references/altitude.md));
   doubles only at process boundaries.
4. **Legacy: characterization tests first**, labeled as such. **Bug: the reddening test first.**
   The two point in opposite directions: a characterization test asserts what the code does
   today, a reddening test asserts what the contract promises. Never let one stand in for the
   other (rule 13).
5. **THE PLAN NEVER SHRINKS.** Every ranked target keeps its row and target rung; budget decides
   order and how far today, never what is dropped. De-scoping is `n/a` with a reason.
6. **Budget is spent by risk, with a floor.** Default: every selected target runs to its target
   rung. Under a user cap or a testability defect, spend top-down by risk and report the remainder
   (`pending` for a cap, `blocked` for a defect). Floor: a target with shared state, concurrency or
   a clock always gets its concurrency and L4-L5 work first; it is never the part a cap cuts.
7. **Routing is an instruction.** INVOKE the sibling (Skill tool, or read
   `~/.claude/skills/<name>/SKILL.md` and apply it inline where no Skill tool exists); what is
   forbidden is approximating it from memory.
8. **No claim without execution.** Every finding and every "works" carries an evidence record
   per [references/evidence.md](references/evidence.md); `razonado` items are hypotheses.
9. **Regression gate.** A diff is validated only when the existing suite is green, its blast-radius
   rows ran to target rung, and `real-run-validation` drove the affected journey.
10. **RDD receipt.** Derived from an evidence ledger row, only when a provider-issued lineage id
    exists (`_shared/test-receipt-contract.md`); otherwise the ledger row is the record. Evidence
    only, never authority.
11. **Findings persist with verdicts** (template "Findings"). Severity is the consequence class;
    state whether data is safe. Never re-propose a `rejected` or `wontfix` finding unless its
    cited-files fingerprint changed; cite the row when skipping.
12. **Persist before you report, in the shipped shape.** The plan is a set of tables, not a
    document you compose: create it with `tpp plan init` (or copy
    [assets/test-plan-template.md](assets/test-plan-template.md) when the binary is absent) and
    fill its rows. Prose replaces no row, and every finding cell opens with `path:line`. When the
    plan is not `docs/testing/test-plan.md`, declare it so the Stop hook and `tpp check` read
    it: write `.tpp.json` at the worktree root.
    ```json
    {"planPath": "docs/testing/<name>.md"}
    ```
    A plan nobody declares is a plan nothing clears. The declaration is repository-local, so a
    scoped plan cannot silently become the default plan for another checkout. The final message is
    written only after the declared plan is on disk and `tpp plan check` passes: it reports
    findings that are not rows, cite no location, cite an evidence id that does not exist, or
    settle without a pinning test. A finding that exists only in chat does not exist, and one
    nothing can parse is the same thing. Asking the user whether to fix something never replaces
    writing the row first. When the binary is absent, the Tooling section names the fallback for
    each call.
13. **Every confirmed finding leaves a pinning test in the suite, asserting the CORRECT
    behaviour.** A probe in the scratchpad proves the defect once; a test in the repository's own
    suite proves it on every run. The assertion states what the contract promises, so the test is
    RED on today's code and GREEN once the defect is fixed, in that order. Run it both ways and
    record which run was which. Then discriminate: it must also go red under at least one adjacent
    mutation of the fixed line (boundary shifted, condition negated, branch removed), else it pins
    only the exact revert and a wrong fix passes. Two failures look like compliance and are not: a test that passes
    either way pins nothing, and a test that asserts the defective output is a characterization
    test (rule 4) that turns red the day someone fixes the bug, so it defends the defect instead
    of demanding its repair. Promote through `no-excess-tests`, name it in the finding row, and
    label any characterization test as such in its own name. A finding whose probe was never
    promoted stays `open`, reason `not pinned`.

14. **Record a retrospective at the end of every run**, including blocked or partial ones. Run
    `tpp feedback --template` into a `mktemp` file outside the repository, fill only the existing
    fields (`ts`, `repo`, `plan`, `skill`, `build`, `paid`, `cost`, `reason`, `verdict` of `paid`,
    `partly` or `ceremony`, optional `guess`, `freeform`; the parser rejects unknown keys), then
    `tpp feedback --file`. Set `skill` to what ran (`<name>` or `<name> <version>`; a breakcheck run
    records `breakcheck <its version>`). Keep it out of chat: after a successful write reply with a
    brief acknowledgment, and if storage fails say so instead of claiming it was recorded.
15. **Ask only when it changes the test.** Ask the user only when the target is ambiguous or the next
    action is irreversible; research everything else. Report each finding consequence first, and mark
    it inferred when it was not observed.

## Decision Gates

**State → mode and scope** (read state first, always)

| State found | Action |
|---|---|
| No declared plan (`.tpp.json` absent or has no `planPath`) | PLAN the blast radius when the change is bounded (a scoped run), the whole app when it is not, then EXECUTE the first rows, same run |
| No declared plan, and the change is one small function eligible for Micro (below) | PLAN micro with `tpp plan init --micro`, then EXECUTE its one pinning test and one mutation |
| Plan exists but predates the template (missing sections or `Baseline:`) | Migrate it: add the missing sections empty, record the baseline (`assets/fingerprint.sh`), treat the tree as changed, then EXECUTE |
| Plan exists, fingerprint unchanged | EXECUTE from the first `pending` row |
| Plan exists, files differ from the plan baseline | Refresh rows in that diff's blast radius, rank first, EXECUTE |
| Plan exists, baseline diff is large or structural | PLAN refresh (keep statuses), then EXECUTE |
| User says "plan only" / "solo el plan" | PLAN, stop |
| User says "calibra el testing" | CALIBRATE ([references/calibration.md](references/calibration.md)) |
| Monorepo with several apps, none named | Ask one question: which app |

**A bounded run plans scoped.** When the trigger is bounded — the operator names one area, file or
module to test, or the diff is confined to one or two files — and it touches none of the classes
below, the run may plan the blast radius instead of the whole app and say so in the plan header:
`Light: <blast radius> · touches <classes>`. Skipped layers keep their row with `n/a` and a reason
in the `Scope` cell; the "Not testing, on purpose" table names what a full run would have added;
and inside the scope nothing is reduced ([references/ordering.md](references/ordering.md)). The blast
radius must equal a Ranked-targets `Target` cell exactly, or be a file path (no `:line`) the plan cites
as `path:line`; anything else fails `plan check`.

Refused, and planned in full, when the trigger is unbounded or touches authentication or
authorization; secrets, credentials or PII; persistence, schema or migrations; money, rounding or
totals; or the public contract — a signature, a format, or documented semantics. A defect fix that
restores the documented contract is eligible. Also refused for a structural diff (renames, a moved
package, a changed interface) or a scope a plan already covers, which is resumed instead.

**One small function plans micro.** When the change is a single function of about ten lines or fewer,
its contract does not change, and it touches none of the refused classes above, the plan may be a micro
plan: `tpp plan init --micro` writes a header declaring `Micro: <file path> · touches none`, the
Findings table and the Evidence ledger, nothing else. It keeps the vacuous-assertion check on the tests
that already cover the function, one pinning test observed red then green, and one mutation that test
kills. `plan check` refuses a `Micro:` plan whose file no `path:line` citation corroborates, whose classes
are not `none`, that also declares `Light:`, that carries a Layer matrix, or whose ledger lacks an
`observado` row or a row with a `Mutate` cell; only a plan that passes owes no breadth to `plan gaps`,
`tpp check` and the Stop gate. A refused declaration reads as a sweep never planned.

Scope: a diff means its blast radius first, closed by the regression gate (rule 9); a clean
tree on main means the whole app. A scope may keep its own plan beside another scope's — for
example `docs/testing/test-plan-reports.md` beside an existing `test-plan.md` — closing against
`plan check` on its own path.

**Reviewing someone else's PR.** Keep the plan outside the author's branch, for example
`~/.config/tpp/reviews/<repo>/pr-<n>.md`, and pass it to every `plan` subcommand with `--path`; run
`plan admit` from inside the checkout, where its commands and `Mutate` replays resolve. Pinning tests ship as
a suggested patch, each named by its suite path :: test name, never as a commit to the author's branch.
`tpp plan export --path <plan>` prints the Findings as a sanitised Markdown comment naming the commit
it covers; post it (`gh pr comment <n> -F -`) only with the operator's approval.

**Verdict per target**: probe · pin · none (`n/a`), profiles in
[references/prioritization.md](references/prioritization.md). **Routing** by need: full table in
[references/ordering.md](references/ordering.md); `exploit-testing` owns adversarial probing.

## Execution Steps

**Always first**: resolve and read the declared plan (`docs/testing/test-plan.md` when the declaration has no `planPath`),
`git status`, the diff against main, and the test runner. Pick mode and scope from the state table; state the inference in one line.

**PLAN**

1. Inventory surfaces and entry points with CodeGraph when `tpp doctor` reports it
   available, otherwise with `git ls-files` plus grep: routes, CLIs, jobs, migrations,
   ports/adapters, critical journeys.
2. Layer sweep: invoke each layer owner in PLAN mode and take the target rows it returns
   (its own checks): security `appsec-adversarial-auditor` · runtime and faults
   `runtime-reliability-testing` and `resilience-fault-injection` · persistence and migrations `database-persistence-testing` ·
   architecture `clean-architecture-audit` · e2e journeys `real-run-validation`; when the
   surface exists: `iac-safe-auditor`, `rag-audit-evaluator`, `llm-eval-design`, `agent-eval`, `llm-redteam`. The router
   ranks what siblings contribute, never invents it.
3. Rank ([references/prioritization.md](references/prioritization.md)).
4. Per target: altitude, target rung L1–L5, sibling skill, verdict.
5. Layer matrix (one row per layer plus sandbox); name anti-priorities with reasons. `plan gaps`
   counts a row as swept when its status reads `done`, `fixed` or `closed`, and `n/a`, `na`, `none`
   and `skipped` leave the breadth denominator: marking a layer out of scope is a decision the plan
   records, never a silent omission. The `Skill` cell only labels what is still owed.
6. Record the baseline (`assets/fingerprint.sh`: repo fingerprint; per-finding cited-files fingerprint) and persist to
   the declared plan path (or `docs/testing/test-plan.md` when no path is declared). Create the file with
   `tpp plan init --path <plan>`, which writes
   [assets/test-plan-template.md](assets/test-plan-template.md) with every table in place; rows
   start `pending`, `none` rows start `n/a`. Close the run with `tpp plan check` (rule 12).

**EXECUTE**

1. Pick the first `pending` row, or the one the user names.
2. Sandbox: throwaway worktree or ephemeral container; environment proof; probes in scratchpad
   or gitignored `testLocales/`.
3. Invoke the assigned sibling (read its `SKILL.md` and apply it inline where no Skill tool
   exists); it climbs to the target rung without reduction.
4. Promote the reddening probe into the suite through `no-excess-tests`, asserting the promised
   behaviour, then run it twice: RED on the current code, GREEN with the minimal fix or the
   mutation reverted. If the first run is green, the assertion is pointing at the defect instead
   of the contract: rewrite it before promoting. Those two runs are one evidence row, and the
   test's identity goes in the finding (rule 13).
5. Update the plan: row status, rung, evidence ledger, findings, testability blocks with the
   minimal change that opens them. Write the file, run `tpp plan check` until it passes,
   then report the delta (rule 12).

## Output Contract

This describes the CHAT REPLY, never the file: the plan's structure is
[assets/test-plan-template.md](assets/test-plan-template.md) and nothing else.

Return: inferred mode and scope (one line); plan path and delta; ranked table; layer matrix;
not-testing list; routing ledger (per sibling: `contributed rows` / `invoked` / `skipped` +
reason; an `invoked` entry says how it ran — the Skill tool, or `inline: <path to its SKILL.md>` where
the harness has none — because nothing in the plan tells an inline run from a skipped one;
assigned-but-never-invoked is an open gap); evidence ledger; findings with verdicts and
precision, each confirmed one naming its pinning test; execution ratio (done / rows excluding
`n/a`) and the ordered remainder.

## References

- [references/prioritization.md](references/prioritization.md) · [references/altitude.md](references/altitude.md) ·
  [references/ordering.md](references/ordering.md) (stop rules, full routing) · [references/evidence.md](references/evidence.md) ·
  [references/calibration.md](references/calibration.md) (`assets/seed-mutants.py`) · [references/comments.md](references/comments.md) ·
  [assets/test-plan-template.md](assets/test-plan-template.md).

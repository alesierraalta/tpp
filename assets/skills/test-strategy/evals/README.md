# Behavioral evals for the testing skill

Two evals measure the skill; they answer different questions.

- **This suite** (behavioral): does "haz el testing" behave as specified? Mode inferred from
  repository state, no needless questions, plan persisted and never shrunk, every finding backed
  by an evidence row, rejected findings not re-proposed, regression gate run on a diff.
- **`calibra el testing`** (detection): does it find defects? Recall and precision against sealed
  seeded mutants, recorded in the plan's Calibration history (`references/calibration.md`).

`claude plugin eval` is the native runner for this kind of suite; it is gated (early access) on
this machine, so `run_evals.py` reproduces its shape with the standard library: fixture
scaffolding, an isolated `claude -p` run per case, deterministic graders, JSON and Markdown
reports, and an optional baseline arm without the skill.

## Run

```bash
cd ~/.claude/skills/test-strategy/evals
python3 run_evals.py --dry-run                      # scaffold every case, no model cost
python3 run_evals.py --case 04-plan-only --model haiku
python3 run_evals.py --runs 3 --model sonnet         # the whole suite, 3 runs per case
python3 run_evals.py --arm both --case '0[125]-*'    # with vs without the skill (baseline cases)
python3 run_evals.py --max-cost-usd 5                # hard cost ceiling
```

Results: `results/<timestamp>/summary.md` (table per case and arm: score, cost, turns, minutes,
failed graders; delta with vs without) and `aggregate-result.json` (every run, every grader with
pass/fail and detail). Workspaces: `.runs/<timestamp>/<case>/<arm>/<n>/ws`, kept on failure or
with `--keep`. Exit code 1 when any `with` case scores below `--threshold` (default 1.0).

## Cases

| Case | Proves | Key graders |
|---|---|---|
| 01-no-plan | No plan → PLAN the app, persist all sections, then EXECUTE in the same run; no closing question; findings carry evidence ids | plan sections, Execution log ≥ 1, output names plan and execution |
| 02-plan-clean | Plan exists, tree clean → resume from the first pending row; the plan never shrinks | ranked rows still 4, a status changed, no "created a plan" |
| 03-plan-dirty | File differs from the baseline → its blast radius first and the suite runs (regression gate) | output and Execution log mention `limits`, `node --test` was run |
| 04-plan-only | "solo el plan" → plan written, nothing executed | Execution log empty, no test run |
| 05-monorepo-ambiguous | Several apps, none named → exactly one question, no plan written | one question line, no plan files |
| 06-rejected-finding | A rejected finding with unchanged fingerprint is not re-proposed | one matching Findings row, one `rejected` row |
| 07-calibrate | "calibra el testing" appends a Calibration history row with a recall value | row count, recall regex |

Cases 01, 02, and 05 carry `"baseline": true`: with `--arm both` they also run without the
skill so the summary shows the delta the skill provides.

## Fixtures

- `fixtures/miniapp`: dependency-free Node project (`node --test`), three modules. `src/parse.js`
  carries one real defect: a quoted field containing a comma is split (`a,"b,c",d` → four fields).
  Happy-path tests stay green with the defect present, so a probe has to find it.
- `fixtures/monorepo`: two independent copies of miniapp under `apps/alpha` and `apps/beta`.
- `fixtures/plans/clean.md` and `rejected.md`: plans written against the current template;
  `{{FINGERPRINT}}` and `{{FP_PARSE}}` are substituted with `assets/fingerprint.sh` at scaffold time.

## Reading the numbers

- A `with` score below 1.0 is a behavioral regression of the skill; the failed grader names the
  rule that broke. Fix the skill, not the grader, unless the grader itself was wrong.
- The delta against `without` is the value the skill adds on that behavior; a small delta on a
  case means the model already behaves that way unprompted.
- Cost and turns per case are the price of the behavior; track them across skill versions.
- Detection quality is not measured here: run `calibra el testing` and compare recall rows.

## Gotcha: run workspaces and receipt-driven development

Each case scaffolds a real git repository under `.runs/`, and cases like `03-plan-dirty` leave a
deliberate uncommitted mutation in it. If your shell ends up inside one of those workspaces, the
receipt-driven review hook sees that mutation as an unreviewed candidate and blocks the turn on a
consent prompt for a throwaway fixture. Stay out of `.runs/` between runs; the harness never needs
you to cd into a workspace, and the paths it reports are for inspection only.

## What a baseline arm can and cannot measure

A no-skill arm is only informative when the case supplies nothing the skill would have produced.

- `01-no-plan` and `08-real-defect` supply an empty repository, so the delta measures the skill.
- `02-plan-clean` supplies the plan itself, which carries the skill's structure; a no-skill arm then
  scores high for free by following the document. It runs `with` only and is a resume-correctly
  regression guard, not a value measurement.
- `05-monorepo-ambiguous` measures a question budget any competent model already respects, so its
  delta is expected to be zero; it guards against the skill regressing into asking more, or none.

When a grader's pattern can match the supplied scaffold instead of the agent's work, it measures
nothing. Scope such patterns to a table data row.

## The graders are tested too

Three graders in this suite once passed for the wrong reason: one counted a placeholder row as a
finding, one matched the word `observado` in the template header instead of in a data row, and a
hook check read an exit code instead of the branch actually taken. All three were green while
measuring nothing.

`selftest.py` gives every grader a positive fixture it must accept and a negative fixture it must
reject, and `run_evals.py` refuses to spend a model call until they all pass. A grader that accepts
its negative fixture cannot fail, so its passes carry no information. This is the same rule the
skill applies to production probes, turned on the measuring tool: prove it can go red first.

Run it alone with `python3 selftest.py` (no cost, no model calls).

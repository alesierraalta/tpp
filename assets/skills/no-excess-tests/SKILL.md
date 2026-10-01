---
name: no-excess-tests
description: "Trigger: writing tests, adding a test file, test cleanup, 'too many tests', reviewing a test diff before push. Keep only behavioral tests; route local-only tests to gitignored testLocales/ when that convention exists; cut low-value tests."
license: Apache-2.0
metadata:
  author: "alesierraalta"
  version: "1.1"
---

## Activation Contract

Apply when writing tests or deciding what test code gets pushed. Author-time companion to `pr-review` §3c (review-time detection). Also invoked by `branch-pr`'s Pre-PR Test Review Gate — a subagent runs this skill against the branch diff before any PR is opened. A test must earn its place by catching a real future regression. Applies to tests being promoted into the repo. Exploration probes produced by `exploit-testing` stay in scratchpad or `testLocales/` and are out of scope until promotion.

## Hard Rules

- For each kept test, name the distinct production behavior, contract, or regression it protects and the falsifiable oracle. Do not require a specific production line; no unique behavior or oracle -> CUT candidate.
- Coverage of BEHAVIOR, not count: derive equivalence classes from the contract and its consequence, never from the branches of the current implementation (`~/.claude/skills/tsp/references/altitude.md`). Keep one representative case per class, plus each boundary of the contract. Extra cases in the same class are redundant; a second case with an independent oracle is not.
- One test per independent oracle, not one per behavior. Never prune a test whose oracle differs from the surviving one (for example a round trip and a spec example on the same function).
- A kept test must fail under a mutation of the behavior it guards. Run or reason through one concrete mutation (invert a condition, drop a call, change a boundary). A test that cannot go red is vacuous (for example an assertion that holds for any output): repair it until it can, or cut it. Redundant (another test already fails) is a different verdict from vacuous (nothing fails); report which.
- A flaky test is untrustworthy evidence, not merely excess: quarantine it, report it as flaky, and never count it toward a confirming result until a deterministic repro exists.
- Characterization tests (pin current behavior during a refactor) are kept, labeled as characterization, and never counted as confirming correctness (oracle ladder: `~/.claude/skills/tsp/SKILL.md` rule 2).
- Tests that pin the API surface of a published library (exported names, signatures consumers import) protect a contract and are not structure-pinning; keep them.
- Markered live-infra/real-LLM tests only behind a marker (`llm_eval`/`memory_eval`); unmarkered live tests do not belong in the suite.
- Comments in promoted tests follow `~/.claude/skills/tsp/references/comments.md`: one-line intent only, the characterization label, and the ledger id; restating or stale comments are cut at promotion.

## Default Action: route, don't delete

The goal is not to delete tests; it is to keep them OUT of the committed repo,
where they rot and can't be maintained. When the repo has a gitignored `testLocales/`
directory, the DEFAULT action for a CUT candidate is to move it there. Delete only
when it is so redundant that even a local copy adds nothing: exact duplicate or pure
tautology. Consolidations that produce a KEPT behavioral test (parametrize,
fold-negatives) stay in the repo as the covering test; only the leftover copies get
routed or cut.

If `testLocales/` is absent or not gitignored, do not stop and do not create it
unasked: leave the candidate in place, list it under "local-only probes" in the
result, and let the human decide.

## Decision Gates

| Test type (BLOCK) | Action |
|-------------------|--------|
| Tautological (`assert X == X`, const vs itself) | Delete only the exact tautology after previewing the candidate; never broaden deletion to neighbouring tests |
| Vacuous (passes under a mutation of the guarded behavior) | Repair so it can go red, else cut |
| Structure-pinning (`inspect.signature`, source-text grep, import presence) of internals | Route to `testLocales/`; delete if exact duplicate. Exception: a published library's API surface |
| File-exists / substring-in-generated-artifact, layout asserts | Route to `testLocales/` (validate by one real run); delete if it adds nothing locally |
| Coupled to LLM phrasing / fragile NL regex | Make behavior deterministic; test THAT in repo, route the regex version |
| Negative-mirror (N `doesn't_X` inverting one positive) | Fold into the positive assertion (kept); leftover negatives routed if they have local signal, else deleted |
| N near-identical cases differing by one input | One `pytest.mark.parametrize` (kept); extra copies dropped |
| Asserts-on-mocks (only echoes `return_value`) | Route; delete if it only reflects `return_value` |
| Flaky | Quarantine and report; not a cut, not evidence |

## Execution Steps

1. For each new test, state the distinct prod bug it catches and the mutation that turns it red. Names both -> keep. Names a bug but no mutation turns it red -> vacuous, repair or cut. Names none -> CUT candidate; preview the exact path and ownership before any move.
2. Exploratory / run-introspection / CUT-candidate tests go to `testLocales/` when it exists and is gitignored (check `.gitignore`). Use bounded, explicit paths; delete only an exact duplicate or pure tautology, and report skipped ambiguous items.
3. Consolidate copies (parametrize), fold negatives into positives, trim docstring bloat; the consolidated behavioral test stays in repo, leftovers route/cut per step 2.

## Output Contract

The minimal covering set (one test per independent oracle). Report target paths, ownership, preview, mutation checked per kept test, retention exceptions, flaky quarantines, local-only probes, and skipped ambiguities. List cuts by `path:line`; for whole-file noise say "delete file".

## References

- `~/.claude/skills/pr-review/SKILL.md` — §3c test quality + `testLocales/` convention.
- `~/.claude/skills/right-size/SKILL.md` — scope-level overengineering.

# Calibration — recall against sealed seeded defects

Trigger: "calibra el testing", "calibrate the testing". Calibration answers one question the
reporter cannot answer about itself: **does this testing find defects that are known to be
there?** The number it produces (recall) is the comparison metric between skill versions.
The reporter's own summary is never that metric.

## Rules

- Calibration never writes to the real checkout. It runs only in a throwaway worktree whose
  branch name starts with `calib-`; `assets/seed-mutants.py` refuses anything else.
- The agent that executes the testing MUST NOT read the answer key. The key lives in the
  scratchpad; its path and sha256 go in the report so a human can verify the comparison.
- Recall is computed against the key, mechanically, after the run. Findings that are not in
  the key are false positives unless independently confirmed real.
- Every calibration appends one row to "## Calibration history" in the plan. Never overwrite.

## Procedure

1. **Throwaway worktree.** `git -C <repo> worktree add -b calib-<date> <repo>-worktrees/calib-<date> HEAD`,
   then copy the uncommitted state in (`rsync -a --exclude node_modules --exclude .git --exclude .codegraph <repo>/ <worktree>/`),
   install dependencies, and confirm the existing suite is green there.
2. **Pick files.** Take the source files cited by the top ranked rows of `docs/testing/test-plan.md`
   (or the diff's blast radius when calibrating a change). Three to five files.
3. **Seed.** `python3 ~/.claude/skills/test-strategy/assets/seed-mutants.py --root <worktree>
   --files <paths...> --count 5 --key-out <scratchpad>/calib-<date>-key.json --seed <n>`.
   Record the printed key path and sha256. Do not open the key.
3b. **Comparing skill versions**: replay the previous run's key with `seed-mutants.py --root <new worktree> --files <same files> --replay <key.json>` so both versions face the identical mutants; a fresh seed measures the skill on new ground, a replay measures the delta.
4. **Run.** EXECUTE the plan rows covering those files inside the worktree, with the normal
   ladder and the normal evidence contract. No hints about which files were seeded beyond
   the rows themselves.
5. **Compare.** Open the key only now. A seeded defect is **found** when a ledger finding's
   evidence row reproduces at that `file:line` (±3 lines) with the matching effect (for
   example: the flipped comparison makes the boundary probe go red). `recall = found / K`.
   List **misses** with the operator and a one-line reason (no probe at that boundary, rung
   not reached, test asserted on a double). List **false positives**: findings not in the key
   and not confirmed real by their own evidence row. Classify **invalid** seeds first (a
   change inside a string literal, comment, or dead code): exclude them from K and report them
   as a seeding defect, never as a miss.
6. **Record.** Append to "## Calibration history":
   `date | skill version | K | found | recall | misses (file:line operator, why) | false positives`.
   Every miss becomes a new `pending` row in Ranked targets (target, rung, the probe that
   would have caught it); calibration is how the plan learns where its probes are blind.
7. **Clean up.** `git worktree remove <worktree> --force && git branch -D calib-<date>`.

## Reading the number

- Recall below 0.6 on flip-comparison or negate-if mutants means the probes are not at the
  boundaries; fix the L1 catalog usage before anything else.
- Misses on drop-await or swap-logical usually mean probes assert on return values instead of
  observed state (`evidence.md`, `exploit-testing/references/ladder.md` L3).
- A false positive is as serious as a miss: it is what a reviewer rejects, and it goes in the
  Findings table with status `rejected` so it is never re-proposed.
- Compare recall across skill versions on the same files and seed; a version that drops recall
  is a regression of the testing itself, whatever its report says.
- Mutants are a valid proxy for real faults, not a complete one: mutant detection correlates
  with real-fault detection independently of coverage, yet some real faults are coupled to no
  mutant (Just et al., FSE 2014). That is why the benchmark pairs seeded mutants with
  hand-planted realistic defects whose happy suites stay green; a skill version must move both
  numbers, and a recall gain on mutants alone is not a gain.

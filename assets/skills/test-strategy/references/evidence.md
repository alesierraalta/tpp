# Evidence contract — no claim without execution

A testing skill reports what it RAN and SAW. Reading code produces hypotheses, never
findings. This contract applies to every testing skill routed by `test-strategy`.

## Rules

1. **Two labels, one privilege.** Every conclusion is `observado` (executed and seen) or
   `razonado` (inferred). Only `observado` conclusions may be reported as a defect or as
   "works". `razonado` items are hypotheses: list them separately, each with the probe that
   would settle it.
2. **One record per observado conclusion**, with exactly these fields:
   - **Claim** — falsifiable, one sentence: "if <invariant> breaks, this goes red".
   - **Executed** — the command, test id, or driver path that ran.
   - **Inputs and parameters** — exact values, not descriptions.
   - **Observed** — verbatim excerpt, at most 10 lines; the full output saved at a path in the
     scratchpad or gitignored `testLocales/`.
   - **Mutation or negative control → result** — what was changed where, and whether the probe
     went red or stayed green (for example `>=` → `>` at `path:line` → probe went red).
   - **Reproduction** — the smallest input or sequence that shows it.
   - **Label** — the literal word `observado`. No synonyms (`observed`, `observed-state`, `verified`); a record with any other label is treated as `razonado`.
3. **Name the test's direction in the field's terms.** A reddening test is FAIL_TO_PASS: it
   fails on the defective version and passes once the fix lands (the SWE-bench and SWT-bench
   definition). The existing suite that must stay green through the change is PASS_TO_PASS.
   Record both per finding: the FAIL_TO_PASS test that proves the defect, and the PASS_TO_PASS
   set that proves the fix broke nothing else.
4. **"It works" needs the same record.** Green is evidence only when the probe was shown to
   go red under mutation or a negative control. A green that cannot be made red is a
   decorative-suite finding, not a pass.
5. **Never paraphrase output; quote it.** Trim, do not rewrite.
6. **Never report a defect from reading alone.** If it was not executed, it is `razonado`.
7. **The probe must survive as a test, pointing at the contract.** A probe that lived only in
   the scratchpad is gone when the session ends: nobody can re-run it, and the next change
   cannot break it visibly. Every `observado` defect names the promoted test (suite path and
   test name) whose outcome flips with the fix, and the evidence row records both runs in order:
   RED on the current code, GREEN once fixed. Two shapes fail this and both can look like
   compliance:
   - green with and without the fix — a decoration with a defect's name on it;
   - green today and red once fixed — the assertion copies the defective output, so the test
     defends the defect and is deleted by whoever repairs it. That is a characterization test
     (rule 4); it is legitimate only under its own label, never as the pinning test.

8. **`Admit` and `Digest` are the machine half of a record.** `Admit` holds one bare shell command,
   with no placeholders and no backticks, because it is the command a binary runs and not prose a
   human reads; a cell that chains commands, or leaves a value for its author to fill in, is refused
   rather than guessed at. `Digest` holds the `sha256:` digest of that command's canonical output, so
   the record can be re-executed and compared byte for byte. A pin means something only over output
   that holds still, so the command is run twice and a row whose two observations disagree is refused
   as unstable instead of pinned. When part of an output legitimately moves, declare it in the optional
   `Normalize` cell as a Go regular expression whose every match becomes `X` before hashing — as narrow
   as the moving part, because a pattern broad enough to swallow the output turns the pin into
   decoration. A row that pins no digest, or whose output cannot be pinned, is refused by
   `tpp plan admit`.
9. **A pin records where it was taken.** The optional `Mode` cell holds `host` or `sandbox`, and an empty
   cell means `host`. The same command digests differently in a container than on this machine, so a row
   pinned in one mode and checked in the other is refused as a mode mismatch rather than as a digest
   mismatch that would say nothing about why. Only a row that carries a pin has a mode to compare: a row
   with no digest was never pinned anywhere, so it is refused for the missing pin rather than told it was
   pinned in one. `--sandbox` observes each command in a container with the tree mounted read-only and no
   network: a row that tries to write is refused, and the write never reaches the machine. That mode needs
   docker and the image it names (pulled on first use, a few hundred MB), and a machine without either is a
   refusal (`sandbox-misconfigured`) rather than a silent fall back to this machine. Recording writes
   the digest and the mode together, because one without the other is not a checkable record.
10. **A falsifiability claim is a value, not a sentence.** The optional `Mutate` cell holds
    `<old> => <new> @ <path>:<line>`: one textual edit, whose old text must occur exactly once in that file and
    on that line, and which names a file inside the tree. It is checked before anything runs, and each defect is
    named: a cell that does not parse, a path that is absolute or climbs out with `..`, a file that is not there,
    a line that does not exist, text that is absent, text that occurs more than once, and an edit that changes
    nothing. A row that declares a mutation is claiming its own command goes red under it and green without it.
    Under `--sandbox` that claim is replayed: the edit lands on a copy of the tree git knows, the command must
    fail there, the file is put back and its bytes verified, and the command must pass again. A command that
    survives the edit (`mutation-not-red`), a restored half that fails (`mutation-not-green`), and a replay the
    sandbox refuses or cannot complete (`mutation-not-replayed`, or the sandbox's own reason) are all refused
    rather than admitted. Outside `--sandbox` there is no copy the tool owns, so the claim is refused there
    instead of admitted unchecked. Two limits are stated rather than hidden: the copy carries what git knows and
    no `.git`, and only the file the edit names is restored, so a row whose command needs repository metadata or
    an ignored input fails its own replay — a refusal, never an admission. `Mutation or negative control →
    result` stays prose for a human; `Mutate` is the part a binary can act on, and undo. One cell may carry a
    survey: edits separated by ` ;; `, every one checked before any runs and each replayed on its own copy. An
    edit prefixed `~ ` is declared equivalent, so the command must stay green under it; one that goes red is
    refused (`mutation-not-equivalent`), because a test told it apart. The first edit that breaks its
    declaration refuses the row and is named by its position (`edit 2 of 3`); an admitted survey reports
    `N killed, M equivalent`, and the restored half of its last replay is the second run. An edit whose own text
    contains ` ;; ` cannot be expressed in a cell.
11. **An expected failure is declared, not smuggled.** The optional `Expect` cell is empty or `pass` (the
    command must exit zero, as before) or `fail` (a FAIL_TO_PASS test observed red). Any other value is refused
    (`expect-invalid`) before anything runs, and so is `fail` beside a `Mutate` cell (`expect-with-mutate`),
    because a mutation already defines its own red and green runs. With `fail`, only an exit from 1 to 125 is the
    observation, on both runs: a zero exit is refused (`expected-failure-passed`), and a timeout or a sandbox
    refusal keeps its own reason. The output is pinned like any other, so it must name the failing test: a
    compile error must not be able to stand in for the red test. Prefer it over `! cmd`, which passes on a
    compile failure too.

## Record template

| Claim | Executed | Inputs and parameters | Observed | Mutation or negative control → result | Reproduction | Label |
|---|---|---|---|---|---|---|

## Example

| Claim | Executed | Inputs and parameters | Observed | Mutation or negative control → result | Reproduction | Label |
|---|---|---|---|---|---|---|
| If a value exactly at the limit is rejected, the boundary probe goes red | `pytest tests/test_limits.py::test_at_limit -q` | `limit=100`, `value=100` | `1 passed in 0.02s` | `>=` → `>` at `src/limits.py:42` → `1 failed: assert accepted is True` | `accept(100)` with `limit=100` | observado |

The mutation column is what separates a test from a decoration: the same probe, one flipped
operator, one red run.

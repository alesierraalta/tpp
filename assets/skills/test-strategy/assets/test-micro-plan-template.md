# Micro test plan — <function>

Created: <date> · Plan path: `docs/testing/test-plan.md`
Declare the run by replacing this line with `Micro: <file path> · touches none`, naming the file that holds the function.

A micro plan covers one small function (about ten lines or fewer) whose contract does not change and that
touches none of the Light refusal classes. It keeps three things: the vacuous-assertion check on the tests
that already cover the function, one pinning test observed red and then green, and one mutation the pinning
test kills. It carries no layer matrix and no ranked targets, so `plan gaps` owes no breadth for it.

`plan check` refuses a `Micro:` plan whose file path no `path:line` citation corroborates, whose classes
are anything but `none`, that also declares `Light:`, that carries a Layer matrix section, or whose
Evidence ledger lacks a row labelled `observado` or a row with a filled `Mutate` cell. A refused
declaration is not a micro plan: the run reads as never planned.

## Findings

A finding is a row whose cell opens with `path:line` and one line of finding. A `confirmed` or `fixed`
finding names the promoted test that asserts the promised behaviour; a finding that never got a test stays
`open`, reason `not pinned`.

| Id | Finding (path:line, one line) | Severity (consequence class) | Data safe? | Evidence id | Pinning test (suite path :: test name) | Status | Verdict by / date | Reason | Cited-files fingerprint at verdict |
|---|---|---|---|---|---|---|---|---|---|

Statuses: open · confirmed · fixed · gap-closed · rejected · wontfix.

## Evidence ledger

One row per `observado` conclusion: the pinning test observed red then green, and the mutation it kills.
`Mutate` holds one edit, `<old> => <new> @ <path>:<line>`, which `tpp plan admit --execute --sandbox`
replays: the command must fail under the edit and pass again once the file is restored. Every row carries
exactly one cell per header column (14 here); an empty cell stays as `| |`.

| Id | Claim | Executed | Admit | Inputs and parameters | Observed | Digest | Normalize | Mode | Mutate | Expect | Mutation or negative control → result | Reproduction | Label (`observado` / `razonado`, literal) |
|---|---|---|---|---|---|---|---|---|---|---|---|---|---|

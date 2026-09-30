# Proposal: a run is the unit, not the repository

Status: proposal, not implemented. Written 2026-09-15 against skill 0.3.8 and binary 0.3.8.

## The failure this fixes, measured in the field

A repository keeps one plan. Rows are never removed by design (`test-plan-template.md:13`), so the
document is the record of every change ever tested in it. Nothing in a row says which change it
belongs to, and two of the three readers treat the whole document as today's work:

| Reader | Rule today | Effect on a new bounded change |
|---|---|---|
| the skill's EXECUTE | "Pick the first `pending` row, or the one the user names" (`SKILL.md:163`) | it executes a row left by an earlier change |
| `plan gaps` | counts every eligible row of the document (`internal/plan/gaps.go`, `Any` at `:42`) | it owes breadth for changes that are closed |
| `check` and the Stop gate | print those same counts (`check.go:93-95`, `gate.go:360-365`) | a finished change reads as unfinished until unrelated rows are swept |

The refresh is scoped — "Refresh rows in that diff's blast radius, rank first, EXECUTE"
(`SKILL.md:105`) — but the ranking is qualitative (blast radius, history, consequence, cost;
`references/prioritization.md:60`) and knows nothing about the change in hand, so it cannot push
another change's rows down. Rows outside the blast radius keep their status and stay in the
denominator.

Observed on a real run: a Redis pool fix picked up `pending` rows about email templates from an
earlier change, executed them, and `check` kept reporting them as owed work. `.rdd-plus.json`
fixed *which file* is read; it cannot fix *which rows* count, because it declares one plan per
repository and a run is not a repository.

## Decision

A run is a first-class attribute of a row. Every row that carries work or evidence names the run it
belongs to, and one run is active at a time. The active run decides what is executed, what is owed,
and what the gate audits. Runs live inside the existing plan; the plan keeps being one document per
repository, because the durable records (findings, evidence, calibration) are worth keeping
together and the accumulation is only harmful where it counts as today's work.

## The row contract

A `Run` column opens each table whose rows are work or evidence. The value is a slug or blank.

| Table | Why it needs the column |
|---|---|
| Layer matrix | the breadth sweep is per run: a new change owes its own six layers, not the six rows an earlier change already swept |
| Ranked targets | it is the work queue; the active run's rows are the queue |
| Findings | a finding belongs to the change that found it; retrieval by run is the point |
| Execution log | it already carries `Date`; `Run` is the key that answers "what did this change execute" |
| Evidence ledger | a pin is an observation of a run; `plan admit --record` stamps it, and a row that leans on another run's pin names that run |

Not in scope: `Inventory`, `Real-run recipes`, `Not testing, on purpose`, `Characterization`,
`Calibration history` (a benchmark record, not a run's work), `Blocked by testability`.

Slug grammar: `[a-z0-9][a-z0-9-]{1,63}`, and the words `all` and `none` are reserved for the flags.
A slug is chosen once per run by the operator. It is **not** the git branch: a branch is renamed,
rebased and shared, and a run outlives none of that.

Evidence crosses runs by name. A row may lean on an observation an earlier run paid for, and it must
say so: the cell that cites the pin names the run that took it. The pin keeps its provenance and
`plan check` refuses a citation that hides where it came from. The observation is not re-verified -- it
is the same digest -- and the row that uses it is the one that has to read honestly.

## The active run

`.rdd-plus.json` gains one key beside `planPath`:

```json
{"planPath": "docs/testing/test-plan.md", "run": "redis-stream-pool"}
```

Resolution order for every command that counts or executes: an explicit `--run <slug>` wins, then the
declared `run`, then — with neither — the document as a whole, which is today's behaviour. A declared
`run` that is malformed is an error, never a fallback, exactly as `planPath` is.

Two refusals carry the honesty of the whole design:

- `run start <slug>` seeds the run's six layer rows as `pending`, so a run that was opened always has
  rows. A slug nobody opened, or one from another repository, is what "no row carries this slug" means:
  `--run <slug>` in that state exits 1 and says so, so a typo reads as work nobody planned and never as
  nothing owed.
- With an active run, rows that carry no slug are **not** counted as owed (they belong to an earlier
  life of the document) and are **not** silently dropped either: `check`, `plan gaps` and the gate
  print how many unscoped rows the plan still holds and name `--all` as the way to see them. Failing
  closed here means disclosing, not hiding and not counting.

## Commands

```
rdd-plus run start <slug>        # declare the run and seed its six layer rows as pending; idempotent; refuses a bad slug (exit 2)
rdd-plus run status              # the active run, what it owes, and the unscoped row count
rdd-plus plan gaps [--run <slug>] [--all]
rdd-plus check [--cwd .] [--path <path>] [--run <slug>] [--all]
rdd-plus plan check              # also validates the Run column: present, and every non-blank cell a slug
rdd-plus plan add-finding ...    # stamps the active run on the row it writes; --run overrides
rdd-plus plan admit --record ... # stamps the active run on the evidence rows it records; --run overrides
```

`plan gaps`, `check` and the gate keep their exit classes: something owed exits 1, a usage or value
refusal exits 2, and the text always names the run it is talking about.

## The gate

The Stop audit counts the active run, and its reason names that run. The telemetry entry gains `run`,
so the log answers "which run did the gate audit" the same way the declaration change made it answer
"which plan did the gate read". A run whose rows are all swept and closed reports nothing owed, and
the unscoped-rows line rides with every verdict so a plan is never presented as smaller than it is.

### The session binding

The Stop audit is opt-in per chat: without an explicit binding the gate sends no plan notices at
all — a working-tree entry or a fresh mtime never proves this session authored a change. The
binding is what connects a chat to the (plan, run) scope above:

```sh
tpp bind --session <host session ID> --path docs/testing/test-plan.md --run <slug>
tpp bind --unset --session <host session ID>
```

- The record lives outside the repository beside the gate log: a 0700 directory holding one 0600
  file per (canonical worktree root, exact session), named by a hash — no raw session ID in the tree.
- The binding is per session and per worktree: two chats in one checkout bind independently and each
  audits its own plan and run; neither inherits the other's record. The worktree declaration still
  resolves `--run` for `check` and `plan *`, but it is never a Stop fallback.
- A binding with no valid run logs `session_run_unbound` instead of auditing the whole document —
  the exact failure this proposal exists to prevent.
- Records persist until `tpp bind --unset`, so retire them with their session or worktree. Keep
  `TESTING_GATE_LOG` absolute: `bind` and Stop derive the binding directory from the log path, and
  a relative path would resolve against each process's working directory, so the binding would
  never be found.
- The binding scopes plan auditing only. When the host exposes no session ID — or when sessions run
  concurrently — isolate each chat in its own worktree and use `tpp check` / `tpp plan gaps` there:
  sharing one working tree still races on source edits, binding or not.

## The skill

- The state table gains one row: the plan exists and the active run's layers are all `pending` with no
  ranked targets → PLAN this run's targets inside the existing plan, then EXECUTE them. That is the
  bounded run, and it no longer needs a separate plan file to stay clean.
- EXECUTE selects from the active run's rows only.
- PLAN stamps every row it writes with the active run, and `run start` is the first step of a run
  that is not the repository's first.
- The routing ledger and the layer matrix keep their meaning: per run, not per document.

## Migration

`rdd-plus plan upgrade [--run <slug>] [--path <path>]` adds the `Run` column to a legacy plan and,
when a slug is given, stamps every existing row with it (the honest reading of a plan that predates
runs: its rows were one continuous effort). Without a slug the cells stay blank and the rows are
unscoped. `plan check` refuses a plan that lacks the column, so exactly one shape exists in the
repository at a time; the coexistence is bounded to the upgrade command.

`docs/testing/light-scope.md` proposed a scoped *mode* for a bounded change; this proposal is
orthogonal and does not depend on it, but a plan whose rows are run-scoped is what makes that mode's
record natural.

## Failure modes, and the tests that must kill their mutations

| Failure | Mutation the test must kill |
|---|---|
| `gaps --run X` counts run Y's pending rows | count every row regardless of the slug |
| an active run counts unscoped rows as owed | treat a blank cell as belonging to the active run |
| unscoped rows disappear from the report | drop the disclosure line |
| a typo'd `--run` reads as nothing owed | treat "no rows for this slug" as a clean plan |
| `run start` accepts `Redis_Pool` or an empty slug | accept any non-empty string |
| the gate audits the wrong run | count the document while naming a run |
| a written row lands without a slug while a run is active | write the row without stamping it |
| `plan check` accepts a table whose Run column is missing | validate the tables but not the new column |
| a row leans on another run's pin without naming that run | accept a cross-run citation with no origin |
| `run start` declares a run and seeds nothing | open a run with no rows, so a fresh run and a typo look alike |

`plan admit` already proves its staleness rule by replay; the run stamp is part of the row it records,
so the same admission path covers it.

## Non-goals

- No branch-derived identity, ever.
- No per-run plan files: `.rdd-plus.json` already declares a scoped plan when a run genuinely needs
  its own document, and one repository may hold one such plan at a time.
- No archiving or rotation of runs. Rows are never removed; they stop counting when their run is not
  the active one.
- No change to `Calibration history`, to the exit classes, or to the declaration's fail-closed rules.

## Decisions taken

**Opening a run writes its debt down.** `run start <slug>` seeds the run's six layer rows as `pending`.
The plan says what is missing before anyone plans it, and the ambiguity that would otherwise exist -- a
run with nothing yet against a run name that was mistyped -- disappears: a mistyped run owes everything,
which is loud, and a name nobody opened is the only thing that can read as work nobody planned.

**Evidence crosses runs by name.** A row may lean on an observation an earlier run paid for, and it must
say so: the cell that cites the pin names the run that took it. The pin keeps its provenance and
`plan check` refuses a citation that hides where it came from.

**The column is named `Run`**, decided here rather than asked: a run is what the operator starts and
what the gate audits, while "change" already means the diff under test.

Both decisions were taken by the operator on 2026-09-15; what remains is implementation.

## Delivery, in three units

The whole contract is one change; it ships as three reviewable ones, because a single diff that touches the
plan checker, the counters, the CLI, the gate, the skill and every fixture is a diff nobody can review.

- **A1 — the contract and the counting.** The `Run` column lands in the two tables the counters read (Layer
  matrix, Ranked targets), appended last so no existing column shifts; `plan check` refuses a plan without
  it and refuses a malformed slug; `plan gaps --run <slug>` counts one run and discloses the unscoped rows;
  `plan upgrade` migrates a legacy plan; `.rdd-plus.json` gains `run`; the template and the repo's own plan
  are upgraded.
- **A2 — the consumers.** `check` and the Stop gate read the active run, the gate logs it and names it, and
  `run start <slug>` seeds a run (with its six layer rows) and reports it. The skill learns the state and the
  stamping rule.
- **B — the record and the provenance.** `Run` reaches Findings, Execution log and Evidence ledger, with the
  cross-run citation by name and the stamp `add-finding` writes.

Each unit is reviewed and committed on its own; the plan shape is valid at every step, because a table the
counters do not read yet keeps whatever shape it had.

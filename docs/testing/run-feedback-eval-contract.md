# Testing, Evals, and Feedback: shared beta contract

**Status: proposed agreement.** This is the minimum shared contract before parallel Testing, Evals, and Feedback work. It distinguishes available capabilities from proposed beta obligations; the TPP feedback review lifecycle below is available, while Learning Loop remains a later consumer, not part of this PR.

## Milestones and scope

- [Testing — milestone 2](https://github.com/alesierraalta/rdd-plus/milestone/2)
- [Evals — milestone 3](https://github.com/alesierraalta/rdd-plus/milestone/3)
- [Feedback — milestone 4](https://github.com/alesierraalta/rdd-plus/milestone/4)
- [Learning Loop — milestone 5](https://github.com/alesierraalta/rdd-plus/milestone/5)

This document adds no framework, schema, API, hook, Pi integration, RAG, reinforcement-learning,
or global score. It does not implement the beta flow.

**Available now:** `tpp feedback` template/file/summary/pending/mark-reviewed CLI, strict parser, JSONL ledger, private review-cursor sidecar, markdown mirror, and benchmark corpus/artifacts.
**Proposed beta obligations:** the shared identity/readiness rule, Testing report, executor-context Feedback,
and Evals coordination below.

## The shared readiness rule

A candidate is ready only when observed behavior and outcomes are reported, blockers are explicit, and evidence identifies the **current candidate**. A report, artifact, or empty blocker field by itself is
not readiness. Every owner uses the same project/root, execution label, and candidate identity below.

| Owner | Responsibility before handoff | Readiness evidence |
|---|---|---|
| Testing | Produce the testing report and readiness disposition. | Probes, expected/observed outcomes, evidence references, omissions, and residual risk for this candidate. |
| Feedback | Record/read back feedback and place the executor-context obligation and procedure before a beta run; coordinate that instruction with Testing. | Persisted agent self-report, storage outcome, and the report/candidate references used for readback. |
| Evals | Own scoring, corpus, adjudication, and benchmark provenance. | Reproducible result references with model, skill, scorer, corpus, and run provenance; no candidate approval claim. |
| Learning Loop (later) | Consume project records after this contract is stable. | No ownership or implementation is claimed by this PR. |

Coordinate changes to this shared contract across owners. Parallel ownership does not mean that one owner silently fills another owner's missing evidence.

## Shared identity

Each report or retained evidence set names:

- **Project/root:** the repository identity and canonical worktree root used for the run.
- **Execution label:** a human-readable label for this document's correlation convention. It is not a
  new CLI key, schema field, or required `run_id`.
- **Base and HEAD:** both commit identities, plus a snapshot of relevant staged, unstaged, and
  untracked content, or retained evidence sufficient to identify that content. `HEAD` plus “dirty”
  is insufficient.
- **Existing references:** reuse Feedback's `repo`, `plan`, and `build` identity. Put run, report,
  and candidate references in `freeform`; do not add a `run_id` field.

The identity is about the content actually evaluated, not merely the branch name or the presence of an artifact directory.

## Testing report

The report contains the scope, a finite budget, probes with expected and observed outcomes plus evidence
references, findings and their disposition, omissions, and residual risk. A compact probe row is enough;
it should answer what was attempted, what happened, and where the evidence is.

Use these dispositions:

- **PASS:** required probes and evidence support the stated outcome; no unresolved blocker.
- **WARN:** a bounded concern or omission remains and the project authority has explicitly accepted
  its disposition.
- **FAIL:** a required behavior is wrong, or a blocker prevents the stated outcome.
- **INCONCLUSIVE:** evidence cannot distinguish the relevant outcomes within the finite budget.
- **N/A:** the probe is outside the candidate scope, with a reason recorded.

Missing required evidence or any blocker means **not ready**, regardless of the nominal status. `WARN` is not an automatic approval: the project authority decides its disposition. This report is not RDD approval and cannot replace the repository's review or delivery controls.

The report path is caller-chosen under `docs/testing/` — for example, the not-yet-created `docs/testing/artifacts/<report>.md`. That path is illustrative, not a new parser or required API. Existing context includes the [testing plan](test-plan.md); this contract does not replace it.

## Feedback from the executing agent

Feedback comes from the **EXECUTING AGENT after the beta flow**, including partial or blocked runs; it is
not human grading. Before the run, executor context must state the obligation and procedure. After successful
persistence, the agent gives only a brief acknowledgment; the full retrospective lives outside chat.

Reuse the existing fields: `ts`, `repo`, `plan`, `skill`, `build`, `paid`, `cost`, `reason`, and
`verdict`, with optional `guess` and `freeform`. Accepted verdicts are `paid`, `partly`, and
`ceremony`. Self-report is useful evidence, not independent truth.

The existing top-level CLI placement is:

```text
tpp feedback --template [--plan <path>] [--config-dir <dir>]
tpp feedback --file <path> [--plan <path>] [--config-dir <dir>]
tpp feedback --summary [--config-dir <dir>]
tpp feedback --pending [--config-dir <dir>]
tpp feedback --mark-reviewed <token> [--config-dir <dir>]
```

With no flags, the command uses the same readback path as `--summary`; `--template` prints only and `--file` records.
These are flags of the top-level `feedback` subcommand; the placement is `tpp feedback`, not a nested command. Reports are persisted at `<config-dir>/telemetry/run-feedback.jsonl` with the existing markdown mirror `<config-dir>/telemetry/run-feedback.md`; `--config-dir` selects that store (default `~/.claude`), independently of `TPP_HOME`, which controls TPP's local installation state. The review cursor is a private local sidecar in the same telemetry directory, not a report or external telemetry. The parser rejects unknown keys.

`--summary` always reads the all-history summary and is cursor-neutral: it never initializes or moves the review cursor. On its first `--pending` invocation, TPP establishes a baseline now at the exact report snapshot: reports already present are counted but not returned for review. Later `--pending` calls return every report appended after the last acknowledged prefix, the total report count from that same snapshot, and one exact snapshot token. Review the full batch before running `tpp feedback --mark-reviewed <token>` with that exact token. The acknowledgment applies only through the token's snapshot; reports appended after that snapshot remain pending. A pending call with no new reports says so and does not offer an acknowledgment token.

If storage fails, disclose the failure. Do not blindly retry when a partial write is uncertain.
Do not load the entire history into every prompt. Later conversations read the project records with
existing readback and manual filtering; do not invent or claim new supported filter flags.

## Evals and benchmark evidence

Reuse the existing benchmark corpus and its `result.json`, `aggregate.json`, `summary.md`, and history artifacts.
Keep these distinctions explicit: **reported**, **claimed pinned**, and **observed caught** are different facts.
An unmatched lexical claim requires adjudication; it is not an observed catch. Provenance records the model,
skill, scorer, corpus, and runs.

Benchmark metrics never approve the target candidate. No paid eval execution is part of this
contract, and this PR invokes no costly benchmark. The existing benchmark prompt (`haz el testing`)
does not guarantee beta Feedback, so a benchmark result cannot stand in for executor feedback.

## One illustrative run

This example is deliberately unmeasured; it only connects a Testing outcome to valid existing-format
agent feedback. The `freeform` value carries the cross-reference without a new field.

Execution label `beta-contract-example`; project/root `/worktree/example`; base `base-commit`; HEAD
`head-commit`; candidate snapshot `docs/testing/artifacts/candidate.txt`; Testing outcome: WARN. The
persisted feedback file remains the existing shape; `freeform` links its report and candidate:

```text
# Existing tpp feedback format (all required fields are present)
ts: 2026-01-01T00:00:00Z
repo: /worktree/example
plan: docs/testing/test-plan.md
skill: test-strategy
build: example-build
paid: partial value; partial ceremony
cost: not measured
reason: partial beta flow completed
verdict: partly
guess:
freeform: Testing outcome WARN; report=docs/testing/artifacts/testing.md; candidate=head-commit; no independent approval claim.
```

## Future proof and non-goals

The future integration proof is one real run, feedback observed across conversations, and a pinned-strategy
benchmark. Those are future checks, not claims made here. This PR implements none of them and edits no source files.

The contract also does not add global scores, new feedback fields, a new report parser, a new benchmark schema,
automatic candidate approval, human grading, RAG/RL integration, or a Learning Loop implementation.

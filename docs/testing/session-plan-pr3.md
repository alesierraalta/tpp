# Test plan — PR3 session-plan isolation

Plan path: `docs/testing/session-plan-pr3.md`
Baseline: `813b949` · Run: `session-plan-pr3` · TPP: `0.4.1 (91019dd)`
Scope: branch `stack/session-plan-isolation-03-core`; this plan belongs only to PR3, not another chat. Commands execute from this worktree. No global configuration or historical testing plan is changed.

## Inventory

| Surface | Entry points | Owner module | Notes |
|---|---|---|---|
| Session binding | tpp bind | internal/gate | Private per-session binding; unset supported |
| Stop audit | tpp gate | internal/gate | No fallback to another session or run |
| Manual checks | tpp check | internal/check | Declared-run verdict remains scoped |

## Ranked targets

| Target | Blast radius | Churn / past fixes | Consequence class | Existing evidence | Altitude | Target rung | Sibling skill | Verdict | Status | Run |
|---|---|---|---|---|---|---|---|---|---|---|
| Session and run isolation | gate notices | Replayed PR3 | Cross-session attribution | Gate behavioral tests | unit | contract | go-testing | pin | pending | session-plan-pr3 |
| Private binding persistence and CLI | binding lifecycle | Replayed PR3 | Incorrect session binding | Binding and CLI tests | integration | contract | go-testing | pin | done | session-plan-pr3 |
| Concurrent binding operations | gate storage | Replayed PR3 | Race or corrupted binding | Race test suite | integration | invariant | go-testing | probe | done | session-plan-pr3 |
| Live host Stop transport | Pi and Claude hooks | No live session exercised | Operational mismatch | None | e2e | real-run | real-run-validation | probe | blocked | session-plan-pr3 |

## Layer matrix

| Layer | Skill | Scope | Status | Run |
|---|---|---|---|---|
| Security | appsec-adversarial-auditor | Session isolation covered by existing negative behavioral tests; adversarial path-race proof deferred | pending | session-plan-pr3 |
| Runtime and faults | go-testing | Binding race detector | done | session-plan-pr3 |
| Persistence and migrations | go-testing | Binding JSON and private storage behavior; no database migration | done | session-plan-pr3 |
| Architecture conformance | go-testing | Static vet and focused module contracts; no architecture refactor | pending | session-plan-pr3 |
| Critical e2e journeys | real-run-validation | Live Pi/Claude Stop hook not exercised | blocked | session-plan-pr3 |
| Sandbox | docker-test-containers | No external services required for these deterministic Go checks | n/a | session-plan-pr3 |

## Not testing, on purpose

| Target | Reason |
|---|---|
| Unrelated cursor worktrees | Owned by other sessions; no attribution or write permission |
| Symlink guard and unreadable-plan telemetry follow-ups | Separate PR4 and PR5, not yet replayed |
| Live hosts | Requires actual host-session execution; no simulated claim substitutes for it |

## Findings

| Id | Finding (path:line, one line) | Severity (consequence class) | Data safe? | Evidence id | Pinning test (suite path :: test name) | Status | Verdict by / date | Reason | Cited-files fingerprint at verdict |
|---|---|---|---|---|---|---|---|---|---|

## Evidence ledger

E1 and E2 were executed twice and admitted by TPP with pinned output digests. E3 was also executed, admitted, and pinned; all three rows passed a subsequent reproducibility check. Existing behavioral tests include negative inputs; a full source-mutation campaign and live-host validation are not claimed.

| Id | Claim | Executed | Admit | Inputs and parameters | Observed | Digest | Normalize | Mode | Mutate | Expect | Mutation or negative control → result | Reproduction | Label (`observado` / `razonado`, literal) |
|---|---|---|---|---|---|---|---|---|---|---|---|---|---|
| E1 | Gate, check and CLI behavioral suites pass | TPP execute and stability probe | rtk proxy go test ./internal/gate ./internal/check ./cmd/tpp -count=1 | Current PR3 worktree | PASS in both observations | sha256:428c90b016832584a88e73f1876e22a63a568c6caf144fa96e83c9312942cced | [0-9]+\.[0-9]+s | host | | pass | Existing negative behavioral cases passed; no source mutation replay claimed | Run Admit in this worktree | observado |
| E2 | Gate suite is race-clean | TPP execute and stability probe | rtk proxy go test -race ./internal/gate -count=1 | Current PR3 worktree | PASS in both observations | sha256:74448199a931bbec904a7ce608569fb52d3e476478c08ebde86e480a33af2843 | [0-9]+\.[0-9]+s | host | | pass | Race instrumentation; not a full fault campaign | Run Admit in this worktree | observado |
| E3 | Static Go validation passes | TPP execute and stability probe | rtk proxy sh -c 'rtk proxy go vet ./... && printf "go-vet: PASS\n"' | Current PR3 worktree | PASS in both observations | sha256:81bc5c8b1d89af0089172d16f882ee7310baa8213ad36e0fe0fb3ab2a0b62acc | | host | | pass | No mutation claim | Run Admit in this worktree | observado |

## Hypotheses

| Hypothesis | Probe that would settle it |
|---|---|
| Correct Stop behavior in live Pi and Claude sessions | Run bound and unbound real sessions and observe transport output |

## Execution log

| Date | Target | Rung reached | Findings (path:line) | Promoted tests | Evidence (ledger id) | Notes |
|---|---|---|---|---|---|---|

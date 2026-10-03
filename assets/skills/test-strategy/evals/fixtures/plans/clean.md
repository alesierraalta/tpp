# Test plan — miniapp

Created: 2026-09-08 · Last updated: 2026-09-08 · Plan path: `docs/testing/test-plan.md` · Sandbox: this git checkout (isolated fixture) · Findings precision: 0 / 0
Baseline: `HEAD` · untracked files: 0 · fingerprint: `{{FINGERPRINT}}` (a change is anything that differs from this fingerprint, not raw `git status`)

## Inventory

| Surface | Entry points | Owner module | Notes |
|---|---|---|---|
| Pure function / library | `withinLimit`, `clamp` | `src/limits.js` | boundary semantics: `n > max` is out |
| Pure function / library | `createLedger().post/balance/entries` | `src/ledger.js` | idempotent by id, finite amounts only |
| Pure function / library | `parseCsvLine` | `src/parse.js` | quoted fields |

## Ranked targets

Rows are never removed by budget; budget changes order and status only. Rows contributed by a
sibling in the layer sweep name that sibling in "Sibling skill".

| Target | Blast radius | Churn / past fixes | Consequence class | Existing evidence | Altitude | Target rung | Sibling skill | Verdict | Status | Run |
|---|---|---|---|---|---|---|---|---|---|---|
| 1. `parseCsvLine` quoted fields, embedded commas, empty fields, whitespace (`src/parse.js`) | 1 caller (tests only) | none | silently wrong answer | 3 happy-path tests | unit | L1 | `exploit-testing` | probe | pending |  |
| 2. `withinLimit` boundary exactly at max and ±1 (`src/limits.js`) | 1 caller | none | silently wrong answer | 2 happy-path tests | unit | L1 | `exploit-testing` | probe | pending |  |
| 3. `createLedger` idempotence, NaN/Infinity amounts, order of entries (`src/ledger.js`) | 1 caller | none | data corrupted | 3 happy-path tests | unit | L1 | `exploit-testing` | probe | pending |  |
| 4. `clamp` with lo > hi and NaN input (`src/limits.js`) | 1 caller | none | visible error | 1 happy-path test | unit | L1 | `exploit-testing` | pin | pending |  |

Verdicts: probe · pin · none. Statuses: pending · in progress · done · blocked · n/a.
A `none` verdict is created with status `n/a`; the execution ratio excludes `n/a` rows.

## Real-run recipes

| Journey | Start command | Data setup | Sample requests | Expected observable |
|---|---|---|---|---|
| Library round-trip | `node --test` | none | `node -e "import('./src/parse.js').then(m => console.log(m.parseCsvLine('a,b')))"` | prints `[ 'a', 'b' ]` |

## Layer matrix

| Layer | Skill | Scope | Status | Run |
|---|---|---|---|---|
| Security | `appsec-adversarial-auditor` | untrusted CSV input | n/a (library, no trust boundary) |  |
| Runtime and faults | `runtime-reliability-testing` | none | n/a |  |
| Persistence and migrations | `database-persistence-testing` | none | n/a |  |
| Architecture conformance | `clean-architecture-audit` | three modules, no layering | n/a |  |
| Critical e2e journeys | `real-run-validation` | library round-trip | pending |  |
| Sandbox | `docker-test-containers` | none needed | n/a |  |

## Not testing, on purpose

| Target | Reason |
|---|---|
| `package.json` scripts | framework behavior |

## Characterization (legacy)

| Test | Behavior pinned | Believed correct? | Promote or delete after the change |
|---|---|---|---|

## Execution log

| Date | Target | Rung reached | Findings (path:line) | Promoted tests | Evidence (ledger id) | Notes |
|---|---|---|---|---|---|---|

## Findings

A rejected or wontfix finding is a known non-issue: it is never re-proposed unless the fingerprint
of its cited files changed; when a run skips it, it cites the row.

| Id | Finding (path:line, one line) | Severity (consequence class) | Data safe? | Evidence id | Status | Verdict by / date | Reason | Cited-files fingerprint at verdict |
|---|---|---|---|---|---|---|---|---|

Statuses: open · confirmed · fixed · rejected · wontfix.

## Evidence ledger

One row per `observado` conclusion (`references/evidence.md`). `razonado` items go under
"Hypotheses" below, never here.

| Id | Claim | Executed | Inputs and parameters | Observed | Mutation or negative control → result | Reproduction | Label (`observado` / `razonado`, literal) |
|---|---|---|---|---|---|---|---|

### Hypotheses (razonado)

| Hypothesis | Probe that would settle it |
|---|---|

## Calibration history

One row per calibration run (`references/calibration.md`); never overwritten.

| Date | Skill version | K | Found | Recall | Misses (file:line operator, why) | False positives |
|---|---|---|---|---|---|---|

## Blocked by testability

| Target | Rung | Why | Minimal change that opens it |
|---|---|---|---|

## Remaining, in order

1. `parseCsvLine` — L1 — `exploit-testing`
2. `withinLimit` boundary — L1 — `exploit-testing`
3. `createLedger` — L1 — `exploit-testing`
4. `clamp` — L1 — `exploit-testing`

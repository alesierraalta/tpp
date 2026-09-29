# Order of Work, Stopping, and Routing

## Characterization tests — before touching legacy you do not fully understand

The purpose is not to prove the code is right. It is to make any change in behavior
VISIBLE in a diff instead of discovered in production (Feathers, *Working Effectively with
Legacy Code*: a characterization test documents what the code does, not what it should do).

1. Pick the seam and drive the existing code with representative inputs.
2. Record what it ACTUALLY does — including the results you believe are wrong.
3. Assert exactly that, and label it: `test_characterization_*`, with a comment saying
   this pins current behavior and is not a specification. Unlabeled, someone will later
   read a bug as a requirement and defend it.
4. Now make the change. Every characterization test that reddens is a behavior change:
   intended ones get their assertion updated in the same commit with the reason;
   unintended ones are the bugs you just avoided shipping.
5. When the change lands, promote the ones that encode real requirements into normal
   tests and delete the rest.

This is also the honest answer to "there are no tests and I cannot add them all": you do
not need them all. You need the ones covering the blast radius of THIS change.

---

## Order of work per situation

**New behavior** — write the contract first (inputs, guarantees, errors, invariants),
then one test per equivalence class, test-first where repo policy requires it. The
strategy layer decides WHICH tests exist; it never overrides a test-first policy.

**A bug** — reproduce in a red test BEFORE the fix: a FAIL_TO_PASS test in SWE-bench terms,
red on the defective version, green after the fix, while the existing suite stays PASS_TO_PASS.
Two reasons: it proves you found the cause rather than a symptom, and it is the only test in
the codebase guaranteed to catch this exact regression. Then ask the systemic question — do sibling call sites share the
defect? — and decide whether the class deserves one test or several.

**A refactor with no behavior change** — the tests must not change. Any test you have to
edit was at the wrong altitude; note it as a finding rather than quietly rewriting it.

**Raising confidence in an untested module** — never "cover the module". Take the top of
the ranked list, write one behavioral test per real behavior, mutate to confirm each one
bites, stop when the budget ends, and report where it ended.

---

## Stop rules

Stop when any of these is true:

- The next test's failure would not change what anyone does.
- You cannot state a falsifiable oracle for it (mutation, contract, invariant, negative
  control, differential, metamorphic, or observed state).
- It would only break on a rename.
- The behavior is already pinned by a higher-altitude test that would catch the same
  regression (keep the higher-value one).
- The remaining targets are anti-priorities.

Then state the stopping point explicitly. "We stopped after the top six; the rest of the
list is in the plan, untested, in this order" is a professional result. Silence is not.

---

## Budget framing

Every test is code maintained forever plus CI time on every run, so it has to earn its place.
The default budget is every selected target to its target rung. Under a user cap or a testability
defect, spend the effort top-down by risk and report where it ended. Floor: a target with shared
state, concurrency or a clock always gets its concurrency and L4-L5 work first, whatever else the
cap cuts. Low-ranked rows are never dropped: they stay `pending`, or become `n/a` with a written
reason when judged not worth testing.

---

## Plan persistence

The plan lives at `docs/testing/test-plan.md` by default (or the path the user names), built from
`assets/test-plan-template.md`. When it is not the default path, declare it at the worktree root in
`.tpp.json`; the Stop hook and `tpp check` read that declared plan:

```json
{"planPath": "docs/testing/<name>.md"}
```

A plan nobody declares is a plan nothing clears. Rows are never deleted by budget. The stopping
point is recorded with the statuses the code enforces (`pending`, `in progress`, `done`, `blocked`,
or `n/a` with a reason), not as prose or invented status words. EXECUTE mode resumes from the first `pending` row; a refreshed PLAN keeps existing statuses.

---

## Scoped runs: what a bounded trigger buys

A bounded trigger — the operator names one area, file or module, or the diff is confined to one or two
files — buys a plan that says what it left out and why, not a cheaper run: a `Light:` run names its blast
radius, the classes it touched, and a reason for every layer it skipped.

What the trigger does not change is the depth: execution inside the scope is not reduced. The target it
reaches climbs to its rung through its sibling, leaves a pinning test and an evidence row, and the run
closes on `tpp plan check` like any other. A bounded request and a bounded diff are the same
decision reached from two sides: one names the surface, the other shows it.

- Eligible: "test the reservation math in `src/inventory.js`"; a two-file fix to a pure function whose
documented contract does not change.
- Not eligible: "test the app"; a rename across packages; a change to what a public function promises,
however few lines it takes; anything in authentication, secrets, persistence or money.

`Light:` is a declaration, and what `plan check` verifies about it is deliberately narrow: the declared
shape, a target the plan itself ranks or cites as `path:line`, and a reason for every layer the plan
left out. It cannot tell whether the trigger was really bounded, whether the named classes are true, or
whether the consequence classes were judged correctly; those stay with the operator and the plan's
reader. That is the point of writing the declaration down — one half is machine-checked, and the half
that cannot be is named where the next reader will find it.

A single small function is the one case where the record costs more than the finding. On a one-function
target the plan template, the layer sweep and the admit ceremony outweigh what they find; the value comes
from the vacuous-assertion check, one pinning test and one mutation. A `Micro:` plan keeps exactly those:
a header declaring `Micro: <file path> · touches none`, the Findings table, and an Evidence ledger with an
`observado` row and a row whose `Mutate` cell names the mutation the pinning test kills. It carries no
Layer matrix, so `plan gaps` owes no breadth for it. Eligibility is Light's, narrowed: one function of about
ten lines or fewer, its contract unchanged, and none of the refused classes. `plan check` enforces the
shape, the corroborated file, the absence of a Layer matrix and of a `Light:` line, and the two ledger
rows; whether the function was really that small stays, as with `Light:`, with the operator and the reader.

Micro is a record that states less; it is not a measured saving.

---

## Central Routing to Sibling Skills

Routing means **INVOKING the skill** — calling the Skill tool with that name, or reading
`~/.claude/skills/<name>/SKILL.md` — not recalling roughly what it does. Each sibling
carries specialized catalogs, harnesses, and procedures that are not reproduced here;
approximating them from memory degrades the method into generic advice.

| Once target and risk are chosen | Invoke Sibling Skill | Purpose |
|---|---|---|
| High risk, needs real adversarial probing | `exploit-testing` | Layered adversarial tests (L1–L5) to actively break the implementation. |
| One candidate needs a bounded adversarial campaign and a readiness disposition before RDD | `breakcheck` | Attack one candidate under a stated budget; report evidence and readiness. |
| Property-based, stateful model-based, differential, metamorphic | `exploit-testing` | Technique catalog and runnable shapes (`Hypothesis`, shrinking, back-to-back). |
| Mutation testing, killed mutants on the PR diff | `exploit-testing` | Mutate the changed lines, classify survivors as equivalent or missed; no numeric target. |
| Races, interleavings, linearizability, shared state or a clock in the target | `exploit-testing` | Concurrency probes; see its `references/concurrency.md`. |
| Operational load, latency p95/p99, soak leaks | `runtime-reliability-testing` | Open-model arrival-rate load testing (`k6`) without coordinated omission. |
| Network faults, circuit breakers, retry jitter | `runtime-reliability-testing` | Deterministic socket fault injection with `Toxiproxy`. |
| API boundary fuzzing | `runtime-reliability-testing` | OpenAPI fuzzing with `Schemathesis`. |
| Business logic authorization, BOLA/IDOR | `appsec-adversarial-auditor` | Dual-persona authorization verification (`403/404` red-under-mutation). |
| Parser safety with untrusted input | `appsec-adversarial-auditor` | Coverage-guided fuzzing (`testing.F`, libFuzzer-style) of parsers and decoders. |
| Secret leaks in diffs, taint tracking | `appsec-adversarial-auditor` | AST taint analysis (`Semgrep`) and high-entropy secret detection (`Gitleaks`). |
| Layer boundaries, clean architecture purity | `clean-architecture-audit` | Architecture conformance tests (`pytest-archon`, `dependency-cruiser`) and architecture-contract mutants only. |
| Duplicated logic, one invariant implemented twice | `clean-architecture-audit` | Clone detection with CodeGraph and similarity search; establish which copy runs, then consolidate to one implementation, or keep the divergence with its evidence record. |
| Database migrations, up/down idempotency, locks | `database-persistence-testing` | Reversible migration verification, DDL lock inspection, and expand/contract patterns. |
| DB deadlocks, isolation, N+1 query budget | `database-persistence-testing` | Concurrency stress, pessimistic locks (`SKIP LOCKED`), and query count assertions. |
| Infrastructure as Code, Terraform, Cloud SAST | `iac-safe-auditor` | Static linting (`tflint`), misconfiguration scans (`checkov`), and OPA Rego guardrails. |
| Cloud unit tests with mocks, FinOps budget | `iac-safe-auditor` | Zero-risk `terraform test` with `mock_provider` and Infracost cost delta gates. |
| RAG retrieval accuracy (Hit Rate, MRR, NDCG) | `rag-audit-evaluator` | Decoupled retrieval evaluation on annotated chunk golden sets. |
| RAG hallucinations, Faithfulness (NLI) | `rag-audit-evaluator` | Atomic claim decomposition, NLI entailment, and RAG triad scoring. |
| RAG cross-tenant or ACL leakage, injected-document canary, citation support, abstention, deletion SLA, embedding upgrade | `rag-audit-evaluator` | Two-principal canary probes, unanswerable and no-context controls, shadow index on the same query set. |
| LLM eval design, judge validation, eval statistics | `llm-eval-design` | Error analysis first, binary criteria, judge TPR/TNR with confidence intervals on held-out human labels, bias battery, bootstrap/paired CIs, pinned-model regression. |
| Agent and tool-use evaluation | `agent-eval` | Final-state grading, tool-call correctness, pass^k over repeated trials, semantic loop detection, grader tampering, side effects. |
| LLM red-teaming: indirect injection, exfiltration, excessive agency, prompt leakage | `llm-redteam` | Lethal-trifecta audit first; attack success rate over repeated trials; deterministic controls stay with `appsec-adversarial-auditor`. |
| No oracle, or quality is unmeasured | `silent-degradation` | Hunt silent data loss and unmeasured degradations. |
| Might never execute in production | `implementation-theater` | Verify code is referenced and alive before spending test budget. |
| A new third-party package appears in the plan | `dependency-legitimacy` | Audit supply chain and hallucinated packages before installation. |
| The test diff is already written and too bloated | `no-excess-tests` | Prune fragile, low-value tests and keep behavioral ones. |
| Final end-to-end reality check of completed change | `real-run-validation` | Exercise real code with real inputs, not just passing mocks. |
| Database / cache ephemeral containers | `docker-test-containers` | Spin up zero-leak ephemeral Postgres/Redis/Mongo containers. |
| Python (pytest) false greens and flaky tests | `python-testing-patterns` | Order-dependent or leaky fixtures, stale mocks and cassettes, swallowed exceptions, unawaited coroutines, lost multiplicity. |
| Go test races, leaks, time flakiness, fuzzing | `go-testing` | Data races, goroutine leaks, order dependence, sleep-based flakiness, parser panics, golden drift. |

Each of these also activates on its own triggers without passing through here. This table
is the path when starting from the strategic question: *"What should I test, and how?"*

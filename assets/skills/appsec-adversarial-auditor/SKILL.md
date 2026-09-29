---
name: appsec-adversarial-auditor
description: "Trigger: security audit, appsec testing, vulnerability test, IDOR, BOLA, SSRF, SAST, DAST, fuzzing, OWASP, secrets leak. Audit code and run adversarial security probes."
license: Apache-2.0
metadata:
  author: "alesierraalta"
  version: "1.2"
---

## Activation Contract

Load to AUDIT AND PROBE code and APIs for security flaws with adversarial intent:
- Verify business logic authorization (BOLA / IDOR, BFLA, parameter tampering).
- Audit implementation against OWASP Top 10 Web & API standards.
- Formulate coverage-guided fuzzing and property-based invariants on parsers and stateful transactions.
- Scan diffs for exposed secrets and run taint triage via Semgrep and Gitleaks (dependency and image scans belong to `dependency-legitimacy`).
- Probe race conditions, authN/session flaws, business-logic abuse and fail-open handling.

NOT for: authoring unconstrained offensive exploits, routine stylistic linting (`pr-review`), or non-adversarial unit tests (`no-excess-tests`).

## Preflight Safety Gate

| Target | Requirement |
|---|---|
| Local isolated: throwaway worktree or ephemeral container, synthetic identities and data, no shared network, credentials, or tenants | Proceed. The sandbox is the authorization; record it in the plan. |
| Shared, remote, staging with real data, multi-tenant, or production | All items in `~/.claude/skills/exploit-testing/references/safety-gate.md` (security subsection) are required. An ephemeral container alone is not authorization here; if any item is missing, stop at planning and report the gap. |

## Plan Contribution

When invoked by `test-strategy` in PLAN mode: do not execute. Return target rows for the plan:
target (path or surface) · check (this skill's domain check: authorization boundary, taint sink,
secret exposure, parser crash-freedom, stateful invariant, race, authN) · target rung or depth ·
consequence class · why. Cover every check this skill would run on this codebase, including
cheap static ones (Semgrep, Gitleaks).

## Hard Rules

1. **Falsifiable security claims**: every probe starts with an explicit failure hypothesis: *"If authorization on <endpoint> fails to enforce tenant isolation, this probe turns RED (403/404/AssertionError)"*. Probes that cannot fail provide zero assurance.
2. **Dual-persona authorization**: for each endpoint handling tenant/user-scoped data, use authorized synthetic identities (User A attempting to read/mutate User B's resource ID). Never real secrets, never cross-tenant exposure; if the target cannot provide safe identities or isolation, record the surface as unreachable.
3. **Falsifiability verification**: mutation or a controlled bypass where meaningful; otherwise a contract/invariant, negative-control, differential, metamorphic, or observed-state check. Do not require mutation or a named production line for every probe.
4. **Isolated sandboxing**: dynamic attacks run only on a target that passes the Preflight Safety Gate (a throwaway worktree or an ephemeral container, `docker-test-containers`). No unconstrained denial-of-service against shared environments.
5. **Severity by impact and exploitability evidence**, not by label, per [references/severity.md](references/severity.md): classify at the sink, name it `file:line`, otherwise the finding is a hypothesis.
6. **Evidence**: every finding carries an executed evidence record per `~/.claude/skills/test-strategy/references/evidence.md`; no finding from reading alone.
7. **Positive control for every denial**: a denied probe counts only when the authorized caller succeeds on the same route in the same run; otherwise a 404 from a wrong route reads as "secure".

## Decision Gates

| Security Focus | Technique & Tool | Target Vector / Invariant |
| :--- | :--- | :--- |
| **Object Authorization** | Dual-Persona Testing (`assets/dual-persona-auth-test.py`) | BOLA / IDOR (`WHERE id = :id AND tenant_id = :auth_tenant`) |
| **Static Taint Triage** | Semgrep (`assets/semgrep-rules.yaml`, Flask sources only; other stacks need scratch rules or the scan cannot fire). A hit is a hypothesis, silence is not proof | SQLi raw escapes (`text()`, `execute()`), SSRF sinks (`requests.get`, `httpx.get`) |
| **Secret Leaks** | Gitleaks on Git diff against base | High-entropy tokens, private keys, API keys in commits |
| **Parsers & Deserializers** | Coverage-Guided Fuzzing (`f.Fuzz`, `Hypothesis`) | Crash-freedom, round-trip invariance (`decode(encode(x)) == x`) |
| **Stateful Logic (Finance/Auth)** | Model-Based PBT (`RuleBasedStateMachine`) | Conservation laws (mass balance, non-negative funds, monotonic states) |
| **Races** | N parallel requests, count successes ([references/race-conditions.md](references/race-conditions.md)) | Limit overrun, TOCTOU on check-then-act flows |
| **AuthN / session / OAuth** | Probe table ([references/authn-session.md](references/authn-session.md)) | JWT, redirect_uri, state, reset-token flaws |
| **Business logic, fail-open, GraphQL, upload, CSRF/CORS, ReDoS, PII logs, LLM tool authz** | Probe rows ([references/business-logic-and-more.md](references/business-logic-and-more.md)) | Step skipping, replay, fail-open, tool-call authorization |
| **Dependencies, images** | Route to `dependency-legitimacy` | CVEs, supply chain |

## Execution Steps

1. **Map attack surface**: exposed endpoints, input parameters, DTO schemas, trust boundaries.
2. **Scan static sinks and secrets**: Gitleaks against `origin/main..HEAD`; Semgrep with security rulesets and custom taint queries.
3. **Dual-persona authorization probes**: synthetic Persona A (owner) and Persona B (attacker) in isolated fixtures with declared tenant scope; submit mutations and queries as B against A's resource IDs; assert the declared denial status, plus an owner positive control and unchanged state after each denied mutation.
4. **Fuzzing invariants for parsers**: arbitrary byte streams into untrusted deserializers; prove crash-freedom and absence of uncaught panics / OOM loops.
5. **Synthesize and triage**: tag each finding with C/M/N severity, CWE/OWASP identifier, observed reproduction payload, and its evidence record.

## Output Contract

1. **Security posture summary**: evaluated boundaries and overall risk level.
2. **Falsifiable claims table**: Level | Claim | Observed result (`observado`) | Falsifiability evidence.
3. **Findings (C/M/N)**: CWE/OWASP, path:line, minimal reproduction payload, remediation diff, evidence record.
4. **Testability and blind spots**: surfaces not exercised dynamically, with rationale.
5. **RDD receipt** when required: [references/rdd-receipt.md](references/rdd-receipt.md).

## References

- [references/attack-vectors.md](references/attack-vectors.md) — BOLA, SSRF bypasses, security invariant design.
- [references/severity.md](references/severity.md) — impact x exploitability matrix and the sink rule.
- [references/race-conditions.md](references/race-conditions.md) · [references/authn-session.md](references/authn-session.md) · [references/business-logic-and-more.md](references/business-logic-and-more.md) — on-demand probe depth.
- [references/rdd-receipt.md](references/rdd-receipt.md) — receipt contract for `lens:security`.
- [assets/semgrep-rules.yaml](assets/semgrep-rules.yaml) — Semgrep triage rules for SQLi and SSRF, Flask request sources only.
- [assets/dual-persona-auth-test.py](assets/dual-persona-auth-test.py) — dual-persona authorization harness.
- Sibling skills: `test-strategy` · `dependency-legitimacy` · `docker-test-containers` · `exploit-testing`.

---
name: clean-architecture-audit
description: "Trigger: clean architecture, architecture audit, layer conformance, import cycles, boundary rules, ArchUnit, import-linter, API compatibility. Prove architectural boundaries with executable conformance rules."
license: Apache-2.0
metadata:
  author: "alesierraalta"
  version: "1.2"
---

## Activation Contract

Load when evaluating layer boundaries, dependency direction, import cycles, or public-API drift in an architectural change, PR diff, or refactoring plan. Objective: prove with executable rules that the code respects its boundaries, and that each rule can fail.

NOT for: test allocation (`tsp`), pruning tests (`no-excess-tests`), dead code, duplicated logic and clone detection (`implementation-theater`), mutation testing in general (`exploit-testing`), swallowed errors and silent fallbacks (`silent-degradation`).

Scope guard: a single-package CLI or a 3-file script needs no fitness functions; the language already enforces what they would check (Go forbids import cycles at compile time).

## Plan Contribution

When invoked by `tsp` in PLAN mode: do not execute. Return target rows for the plan: target (package, module, or symbol) · check (layer boundary, cycle, domain isolation, unearned interface, public-API compatibility) · target depth · consequence class · why. Include cheap static checks (dependency-cruiser / import-linter / depguard / archon run).

## Hard Rules

1. **Architecture is executable code, not diagrams**: boundaries are verified by an automated conformance run (see the tool table in [references/conformance-and-complexity.md](references/conformance-and-complexity.md)).
2. **Every rule needs a negative control**: inject one forbidden edge (a domain file importing the ORM, a two-module cycle), run the rule, and it MUST go red; remove the edge and it goes green. A rule that matches zero modules (wrong root package, typo) passes vacuously; the control catches it.
3. **Domain isolation**: the domain core never imports web frameworks, ORMs, or network clients. Infrastructure depends on domain, never the reverse.
4. **Cycles are strongly connected components of size > 1**, not "any import inside the same package". Domain-to-domain imports are legal; only a cycle is a finding.
5. **Unearned interface**: one implementation and no process boundary is theater, EXCEPT a consumer-owned port that exists for a test double, a second adapter (present or planned), or a team/process boundary. Those are the seam and stay.
6. **Complexity is a finding only when the target configures a limit** (linter or analyzer config); report the score against that limit. Invent no default threshold.
7. **Evidence**: every finding carries an executed record per `~/.claude/skills/tsp/references/evidence.md` (the conformance run output); no finding from reading alone.
8. **Architecture-contract mutants only**: break a boundary on purpose and the conformance test must go red. General mutation testing belongs to `exploit-testing`.

## Decision Gates

| Finding | Classification | Mandatory Agent Action |
| :--- | :--- | :--- |
| Domain imports Infra or ORM | **BLOCKER** | Invert: port in application/domain, implementation in infra. |
| Import cycle (SCC size > 1) between modules of one component | **BLOCKER** | Break it: shared value object, domain event, or inverted dependency. |
| Mutually recursive types inside one package | none | Not a finding; scope cycles to declared components. |
| Interface with one implementation, no test double, no second adapter, no process boundary | **THEATER** | Remove it; depend on the concrete type. |
| Consumer-owned port with a test double or second adapter | none | Keep; it is the seam. |
| Rule never shown red (no negative control) | **WARNING** | Add the control before trusting the green. |
| Published module changed its exported API incompatibly | **BLOCKER** if unversioned | Run the compatibility tool; bump the version or restore the API. |
| Complexity above a limit the target configures | **WARNING** | Report score and configured limit. No configured limit: no finding. |
| Duplicated logic across two live sites | route | Hand to `implementation-theater` (which copy runs). |

## Execution Steps

1. **Pick the tool** the repo already uses, else one row of the tool table; confirm the rules match a nonzero set of modules.
2. **Run conformance**: layer boundaries, framework imports in the domain, cycles.
3. **Negative control per rule**: inject the forbidden edge, observe red, revert, observe green. Record both runs.
4. **Architecture-contract mutants**: for each boundary that matters, break it on purpose; the conformance test must go red. Everything else about mutation goes to `exploit-testing`.
5. **API compatibility** for published modules: `gorelease`/`apidiff` (Go), `cargo-semver-checks` (Rust), `griffe check` (Python), `japicmp` (Java), `@microsoft/api-extractor` (TS).
6. **Theater**: use cases that only pass through, and interfaces per Rule 5. Configured complexity limit exceeded: report it.

## Output Contract

Report: violations (file, line, rule) · per rule, the negative-control result (red on injected edge, green after revert) · compatibility tool output for published modules · configured-limit complexity hotspots · theater to prune · the evidence record per finding. RDD receipt when required: [references/rdd-receipt.md](references/rdd-receipt.md).

## References

- [references/conformance-and-complexity.md](references/conformance-and-complexity.md) — layer rules, tool table with edge-injection one-liners, cycle math, complexity when configured.
- [references/rdd-receipt.md](references/rdd-receipt.md) — receipt contract for `lens:architecture`.
- [assets/pytest-archon-template.py](assets/pytest-archon-template.py) — layer rules plus a real cycle check with negative controls.

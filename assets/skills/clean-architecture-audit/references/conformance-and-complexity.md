# Architecture Conformance

## 1. The Architectural Drift Problem

Architecture diagrams decay into aspiration without automated tests.

### Core Layer Invariants (Clean / Hexagonal):
1. **Domain Isolation**: Domain cannot import `infrastructure`, `adapters`, `ui`, or external frameworks.
2. **Ports Ownership**: The consumer owns the port interface (`domain/ports/` or `application/ports/`). Concrete adapters in infrastructure implement that port.
3. **Cycle Elimination**: within a component the module dependency graph $G = (V, E)$ is a DAG. Any Strongly Connected Component with $|SCC| > 1$ (Tarjan) is a cycle. Importing another module of the same package is not one.

## 2. Tool table (one row per ecosystem)

| Ecosystem | Tool | Inject a forbidden edge |
|---|---|---|
| Python | `import-linter` (`layers`, `independence`, `forbidden` contracts; indirect-import aware); `pytest-archon` for small rule sets; stdlib `ast` + Tarjan as in `assets/pytest-archon-template.py` | add `import sqlalchemy` to a domain module |
| TypeScript | `dependency-cruiser` (`no-circular`, `not-to-dev-dep`), `eslint-plugin-import` `no-cycle` | import an adapter from a domain file |
| Java / Kotlin | ArchUnit (`layeredArchitecture()`, `slices().should().beFreeOfCycles()`) | call an infra class from a domain class |
| Go | `go-arch-lint` (component YAML, deep scan); `depguard` only as an import deny list (no layer model, no transitive check); the compiler already forbids import cycles | import an adapter package from the domain package |
| Rust | `cargo-modules` (graph), `cargo-deny` (crates); `pub` visibility is the boundary | make an internal item `pub` and use it across the boundary |

Each rule is proven twice: red after the injection, green after reverting it. Static import rules miss `TYPE_CHECKING`-only imports, `importlib` dynamic imports, namespace packages and re-export barrels (`index.ts`); say so in the report when the target uses them.

## 3. Architecture-contract mutants

Mutation testing in general (tools, scope, survivor classification, denominators) is owned by `exploit-testing`. This skill keeps only one use: break a boundary on purpose and require the conformance test to go red. A boundary that can be broken without a red test is unenforced.

## 4. API compatibility for published modules

Public-boundary drift is the same class of fitness function. Run the compatibility tool against the previous release: `gorelease`/`apidiff` (Go), `cargo-semver-checks` (Rust), `griffe check` (Python), `japicmp` (Java), `@microsoft/api-extractor` (TS). An incompatible change requires a version bump or a revert.

## 5. Complexity, only when configured

Cognitive complexity (SonarSource) penalizes nesting where McCabe does not. It is a finding only when the target configures a limit (for example `gocognit`, `radon`, `eslint-plugin-sonarjs`, `clippy::cognitive_complexity` in the repo config). Report the score, the configured limit and its source. With no configured limit there is no finding and no invented default.

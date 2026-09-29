---
name: dependency-legitimacy
description: "Trigger: agregar una dependencia, npm install, pip install, un import nuevo, paquete alucinado, slopsquatting, lockfile, pinear versiones, licencia de una librería, auditar dependencias."
license: Apache-2.0
metadata:
  author: "alesierraalta"
  version: "1.1"
  scope: [common]
  auto_invoke: "Verifying a dependency is real, canonical and safe before it enters the project"
---

## Activation Contract

Load BEFORE any new dependency enters the project — a suggested import, an `npm install`,
a `pip install`, a manifest edit, a version bump — and when auditing what is already
there. Mandatory when the package name came from a model, from memory, or from a blog
post rather than from the library's own documentation.

## Hard Rules

1. **Never run the install to find out whether a package exists.** That is precisely how a
   squatted name gets executed. Verify against the registry first, then install.
2. Any name not read from the library's OWN current docs is UNVERIFIED — a model's
   suggestion, your memory, a snippet. Models hallucinate package names at measurable
   rates (5.2% commercial, 21.7% open-source models across 576,000 samples,
   https://arxiv.org/abs/2406.10279), so a plausible name that feels familiar is not evidence.
3. **Existing on the registry proves nothing.** Age, publish history, download volume, a
   real linked repo and a maintainer must all agree. A brand-new package with one version
   and a plausible name is the attack, not the library.
4. Check the name against the CANONICAL name the ecosystem uses and against near
   neighbours (one edit away from a popular package, or a shortened form of the real
   name — the documented `unused-imports` vs `eslint-plugin-unused-imports` shape).
5. Check the OTHER registries too: a name hallucinated for Python may exist on npm, and
   vice versa.
6. **Install with lifecycle scripts disabled by default** (`--ignore-scripts`); it is the
   direct execution path from install to your machine.
7. Nothing enters without: a pinned version, a committed lockfile, CI installing from that
   lockfile only, and a license verified against the project's policy.
8. A compromise usually arrives as a NEW VERSION of a legitimate package. Version bumps
   get reviewed, never auto-merged, when the package runs install scripts.
9. Report what you could not verify. Never guess a package is fine because it is popular.

## Decision Gates

| Situation | Move |
|---|---|
| A model or a snippet suggested an import | Full gate G0–G8 — `references/new-dependency-gate.md` |
| Adding a well-known library | G0 (do we need it?), then G1, G6, G7, G8 |
| Version bump / dependabot PR | Diff the changelog, check install scripts, re-run the vuln scan |
| Auditing an existing tree | `references/hardening.md` — lockfile, provenance, scripts, phantom deps |
| Import in code, absent from the manifest | Phantom dependency: it works via a transitive and breaks on any upgrade — declare it |
| Workflow file, action, or `pull_request_target` | `references/ci-actions.md` |
| MCP server, agent skill, base image, Terraform provider, git hook | Treat as a package: `references/hardening.md` (execution-rights dependencies) |
| Cannot verify the package | Do not install. Say so and propose the stdlib or an existing dependency |

## Execution Steps

1. **G0 — do we need it?** stdlib, an existing dependency, or twenty lines of our own?
   A dependency is permanent; check `right-size` before adding one.
2. Run the gate (`references/new-dependency-gate.md`) in order and collect all cheap evidence; stop when a gate returns an unsafe verdict (G1, G2, G4 red flags). G3 is a signal that raises suspicion, not a stop rule.
3. Install with scripts disabled, pin the exact version, commit the lockfile.
4. Run the vulnerability scan and the license check; record both.
5. Apply the standing hardening items (`references/hardening.md`) if not already in place.
6. Report the verdict per package with the evidence that produced it.

## Output Contract

Per package: name · canonical? · registry age and publish history · linked repo matches? ·
provenance/attestation · known vulns · license · install scripts? · **verdict**
(legitimate / suspicious / unverifiable) with the evidence. Then the manifest and lockfile
changes, and anything left unverified.

## References

- [references/new-dependency-gate.md](references/new-dependency-gate.md) — G0–G8, the pre-install checks and their commands.
- [references/hardening.md](references/hardening.md) — lockfiles, provenance, install scripts, phantom deps, cooldown, dependency confusion, execution-rights dependencies.
- [references/ci-actions.md](references/ci-actions.md) — GitHub Actions and CI hardening.
- [assets/dependency-review-template.md](assets/dependency-review-template.md) — review artifact.
- `~/.claude/skills/right-size/SKILL.md` — G0, do we need it at all.
- `~/.claude/skills/implementation-theater/SKILL.md` — sibling: is our own code real.

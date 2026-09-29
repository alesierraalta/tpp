# Standing posture for the whole tree

The gate protects the moment a package enters. These items protect everything already in,
and every future version bump.

## Lockfile discipline

- The lockfile is committed, always, and it is the source of truth for CI.
- CI installs ONLY from it: `npm ci`, `pnpm install --frozen-lockfile`,
  `uv sync --locked`, `poetry install --sync`. An install that can silently resolve a
  different version makes every other control decorative.
- **Lockfile poisoning is a real PR-level attack**: a change that only edits the lockfile
  to point at a different tarball or registry. Review lockfile diffs like code — check
  changed resolved URLs, integrity hashes and registry hosts, not just version numbers.
  Yarn Berry's hardened mode validates lockfile content against the registry and is on by
  default for public PRs; enable the equivalent where your manager offers one.
- Integrity hashes must be present for every entry. An entry without one is unverifiable.

## Install scripts — the direct execution path

`postinstall` and friends run arbitrary code on every machine and every CI runner that
installs. Set `ignore-scripts` globally and allow-list the few packages that genuinely
need it (native builds), documenting why each is allowed. Treat a NEW install script
appearing in a version bump as a blocking review item.

## Version bumps are where the compromise arrives

The typical incident is not a fake package; it is a legitimate, popular package whose new
version is malicious after a maintainer account is taken over.

- Never auto-merge a bump for a package that runs install scripts or that touches build,
  CI or auth.
- Read the changelog and the diff for anything in the direct dependency set. If there is
  no changelog and no matching git tag, that itself is the finding.
- Release-age cooldown: use the manager's native minimum-release-age setting (pnpm 11
  defaults `minimumReleaseAge` to 1440 minutes, https://pnpm.io/supply-chain-security) so a
  version is not installed within hours of publication; allow a reviewed override for
  security fixes. Cooldown is a cheap signal, not proof: it does not replace the review.
- Re-run the vulnerability scan after every bump, not on a schedule.

## Phantom and unused dependencies

- **Phantom**: the code imports something that is not declared in the manifest and works
  only because a transitive dependency happens to provide it. It breaks on any unrelated
  upgrade. Find them by comparing every import in the source against the declared
  dependencies; declare or remove.
- **Unused**: declared and never imported. Each one is attack surface, install-script
  exposure and audit noise for zero value. Remove them; `knip` (JS/TS) and an
  import-vs-manifest diff (Python) find them.
- **Wrong section**: a build/test-only package sitting in production dependencies ships
  code you never intended to ship.

## Dependency confusion

An internal package name that also resolves on a public registry is installed from the
public one when the higher version wins. Check that every internal name is either
reserved on the public registry or scoped, and pin the source: npm scope-to-registry in
`.npmrc`; pip `--index-url` only (never `--extra-index-url` for a private index);
Go `GOPRIVATE`/`GONOSUMDB`; Maven repository ordering and `<mirrorOf>`; Cargo `[source]`
replacement. Probe: does the internal name exist on the public registry?

## Execution-rights dependencies

Anything that runs code or shapes what runs on your machine is a dependency; apply G1
(canonical source), G2 (age and history), G4 (repo matches), G5 (provenance) to each:
MCP servers, agent skills and plugins, container base images (pin by digest), Terraform
providers and modules (pin version, lock file), git and pre-commit hooks, model files.

## CI and tokens

- The publish/registry token is scoped and short-lived; prefer trusted publishing with
  OIDC over a long-lived token in a secret.
- Workflow hardening (SHA pinning, permissions, `pull_request_target`, script injection)
  lives in `references/ci-actions.md`.
- The dependency install step in CI has no access to production secrets.

## Auditing an existing tree

1. `osv-scanner scan source -r .` over the repo (it also reports malicious-package advisories); triage by whether the vulnerable path is
   actually reachable, not only by CVSS.
2. List packages with install scripts; justify each.
3. List direct dependencies added in the last N months and run G2/G4 on any you do not
   recognise.
4. Diff imports against the manifest (phantom/unused).
5. License report, including transitive.
6. Record the result with a date. Like every other quality number, an undated audit is a
   claim, not a measurement.

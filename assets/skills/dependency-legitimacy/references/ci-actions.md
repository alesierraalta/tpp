# GitHub Actions and CI hardening

An action is a dependency with access to your runner and secrets. Audit workflow files with the same G1/G2/G5 questions, then these checks.

| Check | Rule | Finding if |
|---|---|---|
| Pin | `uses: owner/action@<full 40-char commit SHA>` with the version in a comment; update via Dependabot or Renovate | a tag or branch (`@v4`, `@main`) |
| Permissions | top-level `permissions: {}` or `contents: read`; grant `write` per job only where needed | default token permissions, or `write-all` |
| `pull_request_target` | never check out or run the PR head (`ref: ${{ github.event.pull_request.head.sha }}`) in a job that has secrets or write token | untrusted code runs with base-repo privileges (https://docs.github.com/en/actions/reference/security/securely-using-pull_request_target) |
| `workflow_run` | treat downloaded artifacts as untrusted input | artifact content used in a privileged step |
| Script injection | pass `${{ github.event.* }}` (title, branch, body) through `env:` and quote it in the shell; never inline it in `run:` | expression interpolated inside `run:` |
| Credentials | `actions/checkout` with `persist-credentials: false` unless the job pushes | token left in `.git/config` |
| Separation | build untrusted code in one job, publish in another with OIDC trusted publishing and no long-lived secret | one job builds PR code and holds the publish token |

Lint with `actionlint` and `zizmor` (verify current flags); they are triage, so confirm a hit by reading the workflow.

Worked incident: tj-actions/changed-files (CVE-2025-30066) had version tags repointed to malicious code that dumped runner secrets, which is exactly what SHA pinning prevents: https://www.cisa.gov/news-events/alerts/2025/03/18/supply-chain-compromise-third-party-tj-actionschanged-files-cve-2025-30066-and-reviewdog-action

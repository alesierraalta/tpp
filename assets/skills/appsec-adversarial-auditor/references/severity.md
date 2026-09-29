# Severity (C/M/N) by impact and exploitability

The class is decided by evidence, never by the vulnerability label. Score two axes, then read the matrix.

- **Impact**: data class (secrets/PII/financial vs public) x tenant crossing (cross-tenant vs own data) x write/delete/execute vs read-only.
- **Exploitability**: authentication required (none, any user, privileged), complexity (single request vs chain), and whether an executed probe reproduced it.

| Impact \ Exploitability | Unauthenticated or any user, reproduced | Privileged or chained, reproduced | Not reproduced |
|---|---|---|---|
| Execute, or cross-tenant sensitive read/write/delete | C | M | hypothesis |
| Same-tenant privilege gain, internal-network reach, limited sensitive data | M | M or N | hypothesis |
| Hardening gap, no demonstrated data path | N | N | N |

Reading a cell: a letter (C/M/N) is the reported label. "M or N" means choose M when the probe shows a data path or state change and N otherwise; state the reason. "hypothesis" is reported as a hypothesis (not a C/M/N label) until a probe reproduces it, then re-read the matrix in the reproduced column. Hardening gaps stay N even when not reproduced.

Examples: an authenticated cross-tenant BOLA on sensitive data, or member-to-admin BFLA with export or delete, is C. A missing header with no data path is N. Permissive CORS without credentials is N; with reflected credentials it is M or C by what the origin can reach.

**Classify at the sink, not the source**: severity for SSRF, injection, traversal and deserialization is set by the component that dereferences or executes the tainted value. Accepting a loopback, link-local or `javascript:` URL that only a remote third-party provider ever fetches is CWE-20 input validation (N), not SSRF. Name the sink `file:line` before assigning the class; without a named sink the finding is a hypothesis, not a severity.

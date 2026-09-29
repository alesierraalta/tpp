# Business logic, fail-open, and short probe rows

## Business-logic abuse (OWASP API6, unrestricted access to sensitive business flows)

Reference: https://owasp.org/API-Security/editions/2023/en/0xa6-unrestricted-access-to-sensitive-business-flows/

| Abuse | Probe | Vulnerable if |
|---|---|---|
| Workflow step skipping | call step 3 (pay, confirm, ship) without step 2 | server accepts |
| Negative or overflow quantity/price | `-1`, `0`, `2^31`, `0.001`, client-supplied price | total decreases or wraps |
| Replay | resend the same request or idempotency key | effect applied twice |
| Coupon/credit stacking | apply the same discount twice, across accounts | discount exceeds policy |
| Automation of a sensitive flow | 100 sequential sign-ups, purchases, or invites | no throttle or cost signal |

## Fail-open error handling (OWASP A10:2025, mishandling of exceptional conditions)

Reference: https://owasp.org/Top10/2025/

Probe: make the authorization, validation, or rate-limit dependency fail (kill the auth service, send a malformed token, exhaust a pool) and repeat the denied request. Vulnerable if the request is now allowed, or the error leaks a stack trace or secret. Expected: fail closed with a generic error.

## Short rows

| Class | Probe | Vulnerable if |
|---|---|---|
| GraphQL | introspection on in production; deeply nested query; batching many aliases of one mutation | schema exposed, no depth/cost limit, per-request limits bypassed by aliases |
| File upload | upload with mismatched extension/content-type, oversized file, path-traversal filename, script file | stored executable, served with an executable type, or written outside the upload dir |
| CSRF / CORS | state-changing request from another origin with cookie auth; `Origin: https://evil.example` | request accepted; origin reflected with `Access-Control-Allow-Credentials: true` |
| ReDoS | 30-50 repeated near-match characters against each regex on user input, timed | time grows super-linearly with length |
| PII in logs | seed canary values (`canary-email-7f3a@example.test`) through flows, grep logs, traces, error bodies, URLs | canary found in any sink |

## LLM-app deterministic classes (owned here)

| Class | Probe | Vulnerable if |
|---|---|---|
| Tool-call authorization | as user B, induce a tool call on user A's resource, or a tool the role lacks | tool executes with the agent's privileges instead of the caller's |
| Model output to sink | make the model emit `'; DROP...`, shell metacharacters, `<script>` and follow it to the SQL, shell, or HTML sink | output reaches the sink unparameterized or unescaped |
| Retrieval tenant scoping | as tenant B, query text that only matches tenant A documents | any tenant A chunk is returned |

Behavioral prompt-injection evaluation stays with `ai-evals-auditor`.

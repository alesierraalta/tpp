# AuthN, session, JWT, OAuth/OIDC

Oracle for every row: the probe must be REJECTED, and a valid-token control must be ACCEPTED on the same route (see the positive-control rule in `SKILL.md`). Use synthetic identities only.

| Flaw | Probe | Vulnerable if |
|---|---|---|
| JWT `alg: none` | strip signature, set `alg` to `none` | request accepted |
| JWT alg confusion | RS256 token re-signed HS256 with the public key as secret | request accepted |
| `kid` / `jku` / `x5u` injection | point at attacker-controlled key or path traversal | token verified with attacker key |
| Missing `aud` / `iss` / `exp` checks | token for another audience or issuer; expired token | accepted |
| No revocation | reuse token after logout, password change, role removal | still accepted |
| Session fixation | pre-login session id survives login | same id after auth |
| Reset token | reuse it; use it after password change; tamper the Host header on request | second use works, or link points at attacker host |
| OAuth `redirect_uri` | prefix/subdomain/path-suffix/`@` variants of the registered URI | code delivered to a non-registered URI |
| OAuth `state` / `nonce` | omit, reuse, or replay from another session | callback accepted |
| PKCE downgrade | drop `code_challenge` on a public client | code exchange succeeds without verifier |
| Enumeration / MFA | compare response body and timing for known vs unknown user; skip or brute the MFA step | distinguishable or bypassable |

Traceability: cite ASVS 5.0 requirement ids (https://github.com/OWASP/ASVS , https://asvs.dev/) in the evidence row; do not restate the checklist.

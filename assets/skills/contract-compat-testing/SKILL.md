---
name: contract-compat-testing
description: "Trigger: breaking change, API compatibility, openapi diff, oasdiff, buf breaking, Pact, consumer-driven contract, N-1 compatibility, webhook signature, webhook replay, schema evolution. Prove a change does not break the other side of a boundary."
license: Apache-2.0
metadata:
  author: "alesierraalta"
  version: "0.1.0"
  requires_tpp: "0.4.1"
  scope: [api, contracts, webhooks]
  auto_invoke: "Changing a public API, message schema, persisted payload or webhook: breaking-change diff, consumer contracts, N/N-1 skew, webhook signature and replay"
---

## Activation Contract

Load when the target has a boundary another party depends on: `openapi*.{yaml,json}`, `*.proto`, `schema.graphql`, `pact/` or contract files, versioned public APIs or SDKs, webhook senders or receivers, message schemas or persisted payloads read by another service or a later release.

NOT for a single-process app with no external consumer or durable payload; unit tests of the producer already cover it. API fuzzing stays in `runtime-reliability-testing`; forged-signature and authorization attacks beyond this method stay in `appsec-adversarial-auditor`.

## Hard Rules

1. **The break lives in what the other side assumes.** A green suite of the changed service proves nothing here. Every claim needs a failing consumer interaction or a diff line, then a re-run after the fix.
2. **Diff the spec against the last released version, not against nothing.** OpenAPI: `oasdiff breaking <released> <head>`. Protobuf: `buf breaking --against <released ref>`; choose the category consumers depend on (`FILE` default, `WIRE_JSON` or `WIRE` when only the wire matters). GraphQL: a schema diff tool (verify the tool). Read each hit against the classes that break consumers: removed or renamed field, changed type, new required request field, narrowed enum or response, changed status code, changed default.
3. **Consumer-driven contracts show what consumers really use.** Pact: consumer tests generate the contract, the provider verifies against it, and `pact-broker can-i-deploy` gates release (needs recorded deployments or tags). A provider test that never replays a consumer contract cannot see a consumer's field dependency.
4. **Test skew in both directions.** Old client against new server AND new client against old server, because rolling deploys run both. Check tolerant reading: unknown fields ignored, missing optional fields defaulted, unknown enum values handled.
5. **Persisted and queued payloads: old writer, new reader.** Decode a frozen sample written by the previous release (a committed fixture, not the output of the current encoder). A round trip through the current code always passes.
6. **Webhooks, receiver side.** Verify the signature over the raw request bytes, never over a re-parsed and re-serialized body; compare in constant time; reject a timestamp outside the tolerance (both past and future); reject a replayed id or signature within the window; accept duplicate and out-of-order delivery without double effects; return 2xx only after the effect is durable. Sender side: retries reuse one idempotency key.
7. **A contract test that only reads its own encoder's output is vacuous.** Its input must come from the other side: a released spec, a recorded consumer contract, a captured old payload, or a body signed by an independent signer.

## Decision Gates

| Signal | Probe | Failing evidence |
| --- | --- | --- |
| Spec file changed | breaking-change diff vs released ref | Diff output naming the change class |
| Consumers known and testable | Pact verification, `can-i-deploy` | Failed interaction or non-zero exit |
| Rolling or mixed-version deploy | N/N-1 matrix | Old client vs new server response mismatch |
| Durable queue, cache, stored JSON | Frozen old-writer sample into new reader | Decoded value missing or wrong |
| Webhook handler | Raw-body signing, tolerance, replay, duplicate probes | Valid delivery rejected or forged/replayed accepted |
| No spec, no consumers, no persistence | Stop; record why the gate does not apply | none |

## Execution Steps

1. Name the boundary and each party across it; list every durable payload with a reader in another release.
2. Pick the probes from the table; run the diff against the released ref first (one command, deterministic).
3. For each finding, write the smallest consumer-side interaction that fails, run it red, fix, run it green, and keep it as a regression test.
4. Webhook receivers: sign with an independent helper that emits a pretty-printed body with reordered keys and escapes; expect acceptance. Then tamper with one byte, shift the timestamp, resend the same delivery.
5. Record tools you could not run as `not run` with the reason; tool flags marked verify above are unconfirmed until run.

## Output Contract

Return per boundary: parties, probes run (command and observed output), findings with failing evidence and class, fix confirmation (re-run result), and gates that did not apply with the reason.

## References

- oasdiff: https://github.com/oasdiff/oasdiff
- buf breaking: https://buf.build/docs/breaking/
- Pact can-i-deploy: https://docs.pact.io/pact_broker/can_i_deploy

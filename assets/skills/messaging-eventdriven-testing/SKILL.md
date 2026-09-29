---
name: messaging-eventdriven-testing
description: "Trigger: Kafka, RabbitMQ, SQS, NATS, Redis Streams, Pub/Sub, message queue, consumer, outbox, DLQ, idempotent consumer, redelivery, event handler, webhook consumer. Prove async consumers survive duplicate, reordered, poison and lost messages."
license: Apache-2.0
metadata:
  author: "alesierraalta"
  version: "0.1.0"
  requires_tpp: "0.4.1"
  scope: [messaging, events, queues, async]
  auto_invoke: "Code that consumes or publishes messages or events: broker clients, job queues, outbox tables, async webhook consumers, CDC consumers: redelivery, ordering, poison messages, ack races, outbox atomicity"
---

## Activation Contract

Load when the target consumes or publishes messages through a broker or queue client (Kafka, RabbitMQ, SQS, NATS, Redis Streams, Pub/Sub), runs background jobs, writes an outbox table, handles webhooks asynchronously, or reacts to change-data-capture events.

NOT for synchronous request/response only. Boundaries, do not duplicate: `SKIP LOCKED` and database-backed queue locking stay in `database-persistence-testing`; message schema and N/N-1 compatibility stay in `contract-compat-testing`; general silent loss with no error stays in `silent-degradation`; broker link faults, load and latency stay in `runtime-reliability-testing`.

## Hard Rules

1. **Assume at-least-once; disprove exactly-once claims.** Deliver every message twice, and once more after a simulated crash between handling and ack. Assert the side effect (balance, row, email, outbound call) happened once. Idempotency needs a stable dedupe key taken from the message (event id), recorded in the same transaction as the effect, with a stated retention window; a key that changes per delivery, or is recorded after a non-atomic effect, is a defect.
2. **Kill between handle and ack.** Ack-before-process loses the message on a crash; process-then-ack duplicates it. Fail the ack, restart the consumer, and assert the result matches the delivery guarantee the code claims.
3. **Order is not guaranteed.** Deliver events for one entity reversed and shuffled, within a partition and across partitions or keys. Assert the final state follows the version or sequence in the message, not arrival order; last-writer-wins on arrival is a defect. Ordering holds only per partition key, so check that the key is the entity id.
4. **A poison message must not block.** Publish one malformed or always-failing message between good ones. Expect bounded retries, then routing to a dead-letter queue with the reason, and later messages still processed. Unbounded retry, or a crash loop on one message, is a defect.
5. **Retries need a max and a delay.** Assert the redelivery count is capped, backoff grows, and a retried message keeps its dedupe key. A visibility timeout or ack deadline shorter than worst-case handling time causes concurrent double processing: run a slow handler past the timeout and count effects.
6. **Outbox: state and event commit together.** Crash between the database write and the publish. Expect either both or neither, and a relay that publishes at-least-once (a relay crash after publish, before marking sent, duplicates; consumers must tolerate it). Publishing directly after commit, with no outbox, loses events on a crash.
7. **Rebalance and backpressure.** Trigger a consumer-group rebalance mid-batch: uncommitted offsets are redelivered, so Rule 1 applies. Flood a slow consumer: memory and in-flight counts must stay bounded (prefetch limit, bounded buffer), not grow without limit.
8. **Convergence is asserted with bounded polling.** Eventual consistency is checked by polling for the final state with a deadline and a clear timeout failure, never a fixed sleep.
9. **Prove with a deterministic seam and a re-run.** Use an in-memory broker fake the project already has, or a real broker in a throwaway container (`docker-test-containers`). Record the failing seed or delivery order, the observed side-effect count, fix, and re-run green. A test that never fails on the defective code is not evidence.

## Decision Gates

| Signal | Probe | Failing evidence |
| --- | --- | --- |
| Handler changes money, inventory, counters or sends something | Redeliver each message twice, and after a failed ack | Effect count greater than one |
| Entity state built from events | Reverse and shuffle events per entity | Older event overwrites newer |
| Retry or DLQ configuration | Poison message among good ones | Endless retry, blocked queue, message silently dropped |
| Ack or commit placement | Kill between handle and ack | Message lost, or duplicated with no dedupe |
| Handler slower than visibility or ack timeout | Slow handler past the timeout | Two consumers process one message |
| DB write plus publish | Crash between them | State changed with no event, or event with no state |
| Consumer group or unbounded buffer | Rebalance mid-batch; flood a slow consumer | Duplicate effect; memory grows |
| Synchronous request/response only | Stop; record why the gate does not apply | none |

## Execution Steps

1. List every consumer, its ack point, its effect, its dedupe key, its retry and dead-letter policy, and every publish site.
2. Find or add the seam: a broker abstraction with `redeliver` or `restart` and injectable ack failure. Tool facts to `verify` before relying on them: SQS visibility-timeout and redrive-policy behavior, Kafka offset commit and rebalance semantics, RabbitMQ prefetch and requeue behavior, broker-native dedupe windows.
3. Run the matrix per consumer; keep each failing delivery order as a regression test with its seed.
4. Fix, re-run the same order, and show the side-effect count and final state now correct.
5. Record probes you could not run as `not run` with the reason.

## Output Contract

Return per consumer and publish site: the fault or order injected, the seam, the observed side-effect count and final state, the defect class, and the re-run result after the fix. List gates that did not apply with the reason.

## References

- `docker-test-containers`: throwaway real broker for integration runs.
- `database-persistence-testing`, `contract-compat-testing`, `silent-degradation`: the boundary skills named in the Activation Contract.

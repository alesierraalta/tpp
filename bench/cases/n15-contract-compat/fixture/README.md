# order-events

The consumer side of the orders integration: it receives webhooks from the orders service and reads
order messages from a queue.

- `verifyWebhook(secret, header, rawBody, { now, toleranceSec })` authenticates a delivery. The
  sender signs the exact bytes it puts on the wire: `HMAC-SHA256(secret, "<t>.<rawBody>")`, sent as
  `X-Signature: t=<unix seconds>,v1=<hex>`. A delivery older than `toleranceSec` (default 300) is
  rejected. It returns `{ ok: true, event }` or `{ ok: false, reason }`.
- `decodeOrder(json)` reads one queued order message into `{ id, totalCents, currency }`.
  Messages written by the current release are `{ v: 2, id, totalCents, currency }`. The queue is
  durable and is not drained on deploy, so it still holds messages from the previous release,
  `{ id, total, currency }` where `total` is in currency units (12.5 means 1250 cents); both shapes
  must decode.

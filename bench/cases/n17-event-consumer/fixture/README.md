# order-ledger

A small consumer that keeps a per-order ledger from events published by the payment and order
services.

- `payment.captured` `{ eventId, orderId, amount }` adds `amount` to the order's `paid` total.
- `order.status` `{ eventId, orderId, status, version }` sets the order's `status`. Each order's
  events carry an increasing `version`; the order must show the status of the highest version it
  has been told about.
- Any other event type is acknowledged and counted in `stats.ignored`.
- `getOrder(id)` returns `{ paid, status, version }`; an unknown order is `{ paid: 0, status: 'new', version: 0 }`.

The broker is at-least-once: after a consumer crash or an ack timeout, a message that was handled
but not acknowledged is delivered again. Events for one order may arrive out of order. Each event id
must change the ledger at most once.

`createBroker()` is the in-memory broker used by tests: `publish(body)`, `receive()`, `ack(id)`,
`restart()` (returns every received but unacknowledged message to the queue) and `size()`.
`createConsumer(broker)` returns `{ pump, drain, getOrder, stats }`; `pump()` handles and
acknowledges one message and returns false when the queue is empty.

# charge-client

A small client for a payments service. It creates charges with `POST /charges` and reads them
with `GET /charges`.

- `createClient({ transport, clock, sleep, maxAttempts, attemptTimeoutMs, baseBackoffMs, deadlineMs })`
  returns `{ send }`. Defaults: 3 attempts, 1000 ms per attempt, 100 ms base backoff (doubling),
  5000 ms deadline.
- `send(method, path, body, { deadlineMs })` resolves to `{ ok, status, data }` on a response below
  500, or `{ ok: false, error }` when every attempt failed. A 5xx response or a timeout is retried
  after a backoff sleep.
- The deadline covers the whole call: every attempt and every backoff sleep together finish within
  `deadlineMs` of clock time. A call that cannot finish in time stops and returns
  `{ ok: false, error: 'deadline exceeded' }`; an attempt is never given more time than the call has left.
- A logical `POST` carries one idempotency key, the same on every retry, so the service applies it
  at most once even when a response is lost after the charge was made.

`createClock()` is a fake clock (`now()`, `advance(ms)`); `sleep` advances it. `createLedgerServer(clock)`
is the in-memory service used by tests: `transport` is passed to the client, `script([...])` queues a
behaviour per request (`'ok'`, `'503'`, `'hang'` = not processed and the attempt times out, `'lost'` =
processed but the response is lost so the attempt times out), `always(behaviour)` replaces the default
`'ok'`, `charges` lists what was applied and `calls` counts requests.

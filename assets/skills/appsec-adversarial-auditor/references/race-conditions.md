# Race conditions: limit overrun and TOCTOU

Applies to any check-then-act: balances, coupons and gift cards, quotas, invites, votes, one-time tokens, rate limits, unique-name claims. The sequential state-machine test cannot find these.

## Classes

| Class | Shape | Invariant to check afterwards |
|---|---|---|
| Limit overrun | N parallel requests each pass the check before any write lands | redemptions <= limit; balance >= 0; stock >= 0 |
| TOCTOU on multi-step flows | state read in step 1 is stale when step 2 writes | one-time token used once; a step cannot be replayed |
| Multi-endpoint | two different routes share one resource (checkout and cart edit) | total charged == total of items at completion |
| Partial construction | an object is reachable before it is fully initialised | no request sees a half-built object (null owner, default role) |

## Reproduce

1. Fresh synthetic account with limit L (for example a coupon usable once, balance 100).
2. Negative control first: send N sequential requests. Exactly L must succeed; if not, the test setup is wrong.
3. Send N parallel requests (N = 20-30) released together. Prefer HTTP/2 single-packet attack (all requests in one TCP packet, removes network jitter) or last-byte sync on HTTP/1.1; a thread pool with a barrier is the weak fallback.
4. Count successes and read the final state from the database or a read endpoint.

## Proof

- Finding: successes > L, or the final state violates the invariant. Record N, the success count, the state read, and the request set.
- Not a finding: a 200 on every request with an unchanged final state (idempotent replay), or an overrun that only shows with jitter that a real client cannot produce.
- Repeat 3 runs; races are probabilistic. A single clean run is not a pass.
- Fix check: the same parallel run after the fix (atomic conditional update, unique constraint, row lock) must hold the invariant.

Sources: https://portswigger.net/research/smashing-the-state-machine , https://portswigger.net/research/the-single-packet-attack-making-remote-race-conditions-local , https://portswigger.net/web-security/race-conditions , https://flatt.tech/research/posts/beyond-the-limit-expanding-single-packet-race-condition-with-first-sequence-sync/

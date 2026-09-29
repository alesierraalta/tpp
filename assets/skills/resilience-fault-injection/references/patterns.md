# Fault Injection Patterns

## 0. Scope

- Scope every injected fault to test-owned resources; teardown removes only owned faults, including on assertion failure.
- Use semantic oracles (payload, state, counters), not status-only checks. Missing evidence is `UNVERIFIED`/`INCONCLUSIVE`, never a pass.

## 1. Circuit Breaker State Dynamics

```
       [Failures < Threshold]
       +--------------+
+----->|    CLOSED    |<--------------------------+
|      | Normal state |                           |
|      +--------------+                           |
|             | [Failures >= Threshold]           | [Probe Success]
|             v                                   |
|      +--------------+                           |
|      |     OPEN     |                    +---------------+
|      |  Fail-fast   |                    |   HALF-OPEN   |
|      +--------------+                    |  Probe state  |
|             |                            +---------------+
|             | [Sleep Window Expires]            ^
|             +-----------------------------------+
|               (Allow limited canary traffic)    |
|                                                 |
+-------------------------------------------------+
       [Probe Failure -> Back to OPEN]
```

### Verification Requirements:
1. If the declared circuit-breaker contract specifies fail-fast behavior, calls in `OPEN` should fail within its target-owned bound (for example, $< 20\text{ ms}$) without acquiring sockets or thread pool slots.
2. Where a fallback is part of the contract, verify the fallback payload/schema and observed breaker state, not only an HTTP status.
3. When recovery behavior is in scope, verify the declared transition to `HALF-OPEN` and its configured probe count $K$ before closing.
4. Faults must be scoped to test-owned names and removed in mandatory teardown; preserve pre-existing toxics. Declare the Toxiproxy API/client version assumption and proxy-routing precondition.

---

## 2. Retries and Jitter Math

As a default, do not retry immediately; avoid deterministic exponential backoff that can cause synchronized request waves ("Thundering Herd"). A protocol-specific retry schedule or owner-approved environment may define another policy; test that declared policy and its bounds:

* **Full Jitter Formula (AWS Architecture)**:
  $$t_i = \text{random}(0, \min(t_{\max}, t_{\text{base}} \cdot 2^i))$$
* **Decorrelated Jitter Formula (Database Contention)**:
  $$t_i = \min(t_{\max}, \text{random}(t_{\text{base}}, t_{i-1} \cdot 3))$$

---

## 3. Layered Retry Amplification

If each of L layers retries a failing call up to A attempts, the bottom dependency sees up to $A^L$ calls per user request (3 attempts at 3 layers is 27). A token-bucket retry budget (retries allowed only as a fraction of recent first attempts) caps this at a fixed extra load. Test by counting calls at the dependency while it fails, not by reading the retry configs.

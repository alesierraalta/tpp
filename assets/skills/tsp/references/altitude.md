# What to surround, and at what level

## The one rule

**Surround the contract that survives a refactor.** Everything else is scaffolding that
will be deleted later as noise.

The mechanical check, before writing the test: *if I rename this private function, or
split it in two, does my test break?* Yes → wrong altitude. A test must break when the
BEHAVIOR changes, never when the SHAPE changes. This single question prevents most of the
tests that `no-excess-tests` later has to remove.

## Where the contracts are

In a layered/hexagonal codebase the boundaries that survive are: the use case, the port
(and every adapter must satisfy the same port test), the HTTP/API contract, the message
schema, the persisted schema, the CLI signature. Private helpers, internal method names
and call ordering between your own objects are NOT contracts — they are today's shape.

## Decision table

| The logic is | Surround it at | Why |
|---|---|---|
| Branchy, algorithmic, pure (parsing, pricing, scheduling, validation rules) | Unit, directly | Many equivalence classes, cheap to enumerate, no I/O needed |
| Orchestration (calls three collaborators in order, maps, delegates) | The use case, with real in-process collaborators | A mock-only unit test asserts wiring; focused integration/contract checks cover process boundaries |

| A contract with the outside world (HTTP, queue message, DB schema, file format) | A contract test at the boundary | The consumer's expectation is the thing that must not drift |
| The interaction with a provider | One integration test through a protocol simulator | The bug lives in "our code plus their behavior" (`exploit-testing`) |
| A cross-service expectation | One consumer-driven contract test per consumer | Each side verifies the contract independently; no shared e2e per feature |
| A whole user journey | ONE end-to-end per critical journey, not per feature | E2E is the most expensive and flakiest layer; spend it on the two or three journeys the business cannot lose |
| A port with several adapters | One shared port test suite run against every adapter | Guarantees substitutability, which is the entire point of the port |

## Altitude smells — you are too low

Asserting on private methods · asserting `inspect.signature` or parameter names ·
asserting call counts on your own internal collaborators · asserting on source text ·
a test that mirrors the implementation line by line · needing to change the test in every
refactor that changes no behavior · a mock of a class that lives in your own domain.

These have names. Google's "Change-Detector Tests Considered Harmful" is the test that breaks
on every edit and catches nothing; "Test Behavior, Not Implementation" is the cure. tsDetect
(Peruma et al., FSE 2020) catalogs 19 detectable smells; the ones that matter here are
assertion roulette, eager test, mystery guest, conditional test logic, sensitive equality,
and the unknown test with no assertion at all. Name the smell in the finding; it is checkable.

## Altitude smells — you are too high

Every failure requires a debugging session to locate · a single test covers eight
behaviors so a red says nothing specific · you cannot reach an error branch from the
entry point · the suite takes so long nobody runs it before pushing · flakiness from
infrastructure noise rather than from your logic.

## Doubles: the boundary rule

Use real in-process collaborators. Double only the **process boundary** — network, clock,
randomness, filesystem, external provider, or message broker — and keep that double controlled.
Never replace your own domain with a mock. Pair boundary doubles with focused integration or
contract verification when the boundary behavior matters; otherwise "all green, broken in
prod" remains possible. Prefer an in-memory adapter satisfying the same port over programmed
return values when it is not a process boundary.

Non-determinism must be injected, not patched: pass the clock, the id generator and the
random source. Anything you have to monkeypatch to test is telling you where the seam
should have been.

## Equivalence classes are a starting heuristic

For simple stateless behavior, begin with one representative per equivalence class, not a
completeness promise. Derive the classes from the contract's domain, never from the branches the
code happens to have: a defect whose fix is a MISSING branch is invisible to a class list read
off the implementation. Add boundary values when edges can fail; stateful sequences when history
changes behavior; controlled concurrent schedules for interleavings; repeated statistical
evaluations when sampling risk requires them.

A property or metamorphic check is required, not proportionate-optional, wherever a counterpart
or invariant exists: parse/render and encode/decode round trips, idempotent normalizers and
upserts, a total that must equal the sum of its parts, agreement with a slow obviously-correct
reference. Those find the class nobody imagined, which is the class a hand-written list always
misses. Everything else stays proportionate to the ranked risk.

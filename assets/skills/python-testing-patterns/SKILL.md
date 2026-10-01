---
name: python-testing-patterns
description: "Trigger: pytest, Python tests, flaky pytest, fixture leakage, order-dependent tests, mock drift, unawaited coroutine, pytest-asyncio, Hypothesis, xdist. pytest probes that expose leaked state, stale mocks and cassettes, swallowed exceptions, async gaps and lost multiplicity."
license: Apache-2.0
metadata:
  author: "alesierraalta"
  version: "0.1.0"
  requires_tpp: "0.3.17"
  scope: [python]
  auto_invoke: "Writing or auditing pytest tests: isolation, mock contracts, async, property tests, frozen time"
---

## Activation Contract

Load when writing or reviewing pytest tests, or when a Python suite is flaky, order-dependent, or green while a contract is broken. Owns pytest mechanics only: adversarial ladders are `exploit-testing`, pruning is `no-excess-tests`, DB semantics are `database-persistence-testing`, coverage planning is `tsp`. Do not add a coverage percentage gate.

## Failure-Probe Matrix

| Suspected defect | Probe | Evidence |
|---|---|---|
| Leaked fixture, env, cache, singleton | `pytest -p randomly` (pytest-randomly shuffles order and reseeds `random`; reproduce with `--randomly-seed=N`, verify flag against `pytest --help`); repeat the pair that fails | Fails only after another test ran |
| Shared DB, port, tmp path, cwd | `pytest -n 4` (pytest-xdist) with `--dist load`; `loadscope` and `loadfile` hide collisions | Collision only under parallelism |
| Setup half-done then failure | Raise after the first mutation in a fixture; run the next test | State reset despite partial setup |
| Mock accepts stale contract | `patch("pkg.mod.dep", autospec=True)` at the lookup site of the code under test; apply to boundary collaborators only | Call with a wrong signature fails |
| Swallowed exception | Force the dependency to fail; assert class, message, and resulting state | Failure propagates or suppression is observable |
| Unawaited coroutine | `pytest -W error::RuntimeWarning` (or `filterwarnings = ["error"]`) | Warning becomes a test failure; a hard setup error is a different defect (fixture misuse) |
| Async fixture returns a coroutine | Strict mode plus `pytest_asyncio.fixture` | Test fails instead of silently receiving a coroutine |
| Multiplicity loss | Compare with `Counter`, never `set()` | Duplicate counts preserved |
| Hidden exclusion | `@pytest.mark.xfail(strict=True, reason=...)`; conditional `skipif`; `importorskip` is legitimate for optional deps | Unexpected pass fails; skip reason is visible |
| Parser or round-trip bug | Hypothesis property test | Shrunk counterexample |
| Stale HTTP recording | vcrpy `record_mode="none"` in CI plus a cassette age check; scrub secrets | Live contract drift is caught, not replayed |
| Time-dependent logic | `time-machine` (`travel(..., tick=False)`) over freezegun; advance the clock to cross retry and TTL boundaries | Expiry and backoff exercised without sleeping |

## Isolation rules

- Match fixture scope to the state owned; session or module fixtures must not carry mutable state into other tests. `autouse` must reset concrete state.
- Register cleanup right after each successful setup step (`yield` inside `try/finally`, or `request.addfinalizer`).
- Use `monkeypatch` for env, cwd, and attributes. Default-value tests must `delenv` explicitly.

```python
def test_default_url(monkeypatch):
    monkeypatch.delenv("DATABASE_URL", raising=False)
    assert get_database_url() == "sqlite:///:memory:"
```

## Configuration that finds defects

```toml
[tool.pytest.ini_options]
addopts = "-W error"  # add -p randomly once pytest-randomly is installed
xfail_strict = true
asyncio_mode = "strict"
asyncio_default_fixture_loop_scope = "function"
markers = ["integration: needs real services"]
```

pytest 9 adds `strict = true`, which enables `strict_markers`, `strict_config`, `strict_xfail`, and `strict_parametrization_ids` (verified against the pytest changelog), and a native `subtests` fixture for per-case failures inside one test. Prefer plain `parametrize` when cases are known up front.

## Async

pytest-asyncio 1.x removed the `event_loop` fixture; set loop scope with `asyncio_default_fixture_loop_scope` or `@pytest.mark.asyncio(loop_scope="module")`. Bound awaits with `asyncio.wait_for`, cancel spawned tasks, and assert cleanup after cancellation.

```python
@pytest.mark.asyncio
async def test_timeout_cancels_work(client):
    task = asyncio.create_task(client.fetch())
    with pytest.raises(asyncio.TimeoutError):
        await asyncio.wait_for(task, timeout=0.01)
    assert task.cancelled()
```

## Property and boundary tests

```python
from collections import Counter
from hypothesis import example, given, settings, strategies as st

@given(st.lists(st.text()))
@example(["a", "a"])  # committed regression seed
@settings(max_examples=200, deadline=None)
def test_roundtrip_preserves_multiplicity(items):
    assert Counter(parse_items(join_items(items))) == Counter(items)
```

HTTP: prefer a local boundary (`respx` for httpx, `pytest-httpserver`) over patching a client library; assert status, headers, body, and timeout behavior. Exceptions: `pytest.raises(Err, match=...)` plus a state assertion after the boundary.

## Steps

1. Name the observable contract and the smallest boundary that can falsify it.
2. Pick the probe from the matrix; run the focused test, then random order and `-n 4` when it touches process state.
3. Keep the smallest failing sequence as a regression test. Report skips and probes not run.

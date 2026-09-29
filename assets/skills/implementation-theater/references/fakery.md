# Code that runs and does not do the work

Reachable is not the same as real. These are the shapes that execute happily while
producing nothing. AI-assisted code adds two typical shapes: a hallucinated call wrapped
in `try/except` that never errors, and a `return True` stub behind a green test.

## The knob test — perturb only risk-bearing knobs

Not every parameter. Pick a parameter, timeout, threshold, or config value whose changed
value MUST alter a defined observable. Before perturbing, write the expected oracle and safe
bounds (a controlled value that cannot damage the environment or run up cost; never
`timeout=0` against a real dependency). Then change it, reload if required, drive the real
entry point, observe the defined effect, and restore the value. Unchanged observable means
the knob is disconnected, overridden, or read at the wrong time.

This finds the parameter accepted by the signature, passed down two layers, and never read.

Same test for a config file: change a value, restart, observe. No change means the config
is not loaded, is overridden downstream, or is read once at import before the override.

## The fakery catalog

**Hardcoded and placeholder returns** — a constant where a computation belongs, sample or
seed data reachable from a prod path, `return []` / `return None` / `return True` as a
stand-in, a "response" assembled from the request without consulting anything,
`NotImplementedError` on a path that is actually called.

**Error masking** — `except: pass`, `except Exception:` with a debug-level log or none, an
empty catch block, `try/catch` returning a default, `?? []`, `.get(k, fallback)` on a key
that should always exist, a retry wrapper that swallows the final failure. Each one turns
a defect into fake success. Detect the masking here; the missing metric is
`silent-degradation`'s finding, and an intentional fallback needs a contract plus telemetry.

**Always-one-way branches** — `if False`, a flag never enabled, a condition on a value
that is constant in practice, an `else` that cannot be reached, a `while` that never
loops. Prove it by forcing the other branch: if no proven environment can execute it, say so.

**Functions that assert nothing** — a `validate_*` that never rejects, a `check_*` that
returns `True` unconditionally, a sanitizer that returns its input, a permission check
that only logs. Test them with input that MUST be rejected.

**Machinery that isn't** — a cache that never hits (wrong key, per-request instance), a
rate limiter with no effective limit, a "retry" with `max_attempts=1`, a connection pool
of size one, an index never used by the query planner, a debounce that fires every time,
an async task created and never awaited, a lock acquired on a per-call object.

**Metrics and logs that go nowhere** — a counter emitted but never registered/exported, a
log at a level the deployment filters out, a span with no exporter configured, a
correlation id generated but not propagated. This class is what makes the other classes
invisible; hunt it first when nothing anywhere seems to explain a symptom.

**Concurrency and time theater** — a lock created per call (`threading.Lock()` inside the
function, `synchronized` on a fresh object), a mutex copied by value (`go vet` copylocks), a
coroutine or promise created and never awaited, `await` on a sync stub, a goroutine started
and never joined, a "thread-safe" cache over an unsynchronized map, `parallel`/`batch`
hiding a sequential loop, retry with `sleep(0)`, a timeout parameter accepted but never
passed to the client, a TTL never checked. Contention probe: run N workers released by a
barrier against the claimed critical section (race detector on where available, e.g.
`go test -race`) and observe whether the exclusion, parallelism, timeout or expiry actually
happens. Pass means the effect is observed, not that the code reads correctly.

**Self-admitted debt** — `TODO`, `FIXME`, `XXX`, `HACK` in a reachable path are candidates
for claim falsification, not automatic defects (docs, examples and accepted debt can be
intentional). Verify what the marker claims and what the path does before filing.

## How to prove it, not argue it

For each candidate, the evidence is an execution:

1. Drive the entry point with an input that MUST exercise the line.
2. Observe the effect that proves the work happened — the row written, the request sent,
   the value changed, the error raised — never the return value alone.
3. Then invert: break the thing it depends on (empty the cache, kill the dependency,
   pass an invalid value). If the output is identical either way, the code is not doing
   what its position in the flow implies.

Step 3 is the whole method compressed: **a component that produces the same result when
its input is destroyed is not participating.**

---
name: go-testing
description: "Trigger: Go tests, go test, -race, fuzzing, synctest, flaky Go test, goroutine leak, golden files, testscript. Go test mechanics that expose races, order dependence, leaks, time flakiness and hostile input."
license: Apache-2.0
metadata:
  author: "alesierraalta"
  version: "0.1.0"
  requires_tpp: "0.3.17"
  scope: [go]
  auto_invoke: "Writing or auditing Go tests: races, leaks, order dependence, fuzzing, deterministic time, golden output"
---

## Activation Contract

Load when writing or reviewing Go tests, or when a Go test is flaky, slow, or green for the wrong reason. Owns Go mechanics only: cross-language adversarial design is `exploit-testing`, pruning is `no-excess-tests`, containers are `docker-test-containers`. Check `go.mod` and `go version` first: `synctest.Test` needs Go 1.25; `b.Loop`, `t.Context`, `t.Chdir` need 1.24.

## Hard Rules

- One table row per behavior partition, not per input. Assert error identity with `errors.Is/As`, not only `err != nil`. Loop variables are per-iteration since Go 1.22: `tt := tt` is dead code.
- Compare structs with `cmp.Diff` (`github.com/google/go-cmp`, `cmpopts.IgnoreFields`), not `reflect.DeepEqual`.
- Replace `time.Sleep` synchronization with `testing/synctest` (below). Never assert on wall-clock elapsed time.
- Use `t.TempDir`, `t.Setenv`, `t.Chdir`, `t.Context` and `t.Cleanup` (LIFO, registered right after each setup step). `t.Setenv` and `t.Chdir` panic in a test that called `t.Parallel`; that is the signal the state is process-global.
- Parallelize a test by default only when it touches no env, cwd, port, or global. Run `-race` (needs cgo, several times slower) on packages that own goroutines.
- HTTP: `httptest.NewServer` (or `NewTLSServer`) plus `t.Cleanup(srv.Close)`; always close `resp.Body`.
- Fuzz parsers and decoders only. `f.Add` seeds run on every `go test`; minimized failures land in `testdata/fuzz/FuzzName/` and also replay on plain `go test`. Commit that directory.
- `testing.Short()` skips must be announced (`t.Skip("...")`) and the skipped scope run elsewhere; short-mode green is not integration evidence.
- Golden files: normalize timestamps, absolute paths, locale, line endings; after `-update` review the diff as code, then rerun without `-update`. Golden is for rendering only; assert behavior semantically.

## Decision Gates

| Suspected defect | Probe | Evidence |
|---|---|---|
| Order or shared-state dependence | `go test -shuffle=on -count=3 ./pkg` (print the seed; replay with `-shuffle=<seed>`) | Failure only under reordering or on the second run |
| Data race | `go test -race -count=5 ./pkg` | Race report with both stacks |
| Sleep-based flakiness, timeouts, tickers | `synctest.Test` fake clock | Deterministic pass or fail, no real waiting |
| Goroutine leak | `goleak.VerifyNone(t)` or `goleak.VerifyTestMain(m)` (`go.uber.org/goleak`) | Leaked stack listed at test end |
| fd or port leak | Count `/proc/self/fd` entries (Linux) before and after N iterations; verify: platform specific | Count grows with N |
| Parser panics or invariant breaks | `go test -run='^$' -fuzz=FuzzX -fuzztime=30s` (one target at a time) | Crasher file under `testdata/fuzz` |
| CLI behavior | `testscript` + txtar (`github.com/rogpeppe/go-internal/testscript`; verify current API) | Exit code, stdout, files per script |
| `fs.FS` consumer | `testing/fstest.MapFS`, `fstest.TestFS` | Wrong path, dir, or `io/fs` contract handling |
| Benchmark measures nothing | `for b.Loop() { ... }`, compare with `benchstat` | Setup excluded, no dead-code elimination |
| Lint-detectable slips | `golangci-lint` with `usetesting`, `paralleltest`, `bodyclose`, `noctx`, `errcheck` (verify config against your version) | Mechanical enforcement of the rules above |

## Deterministic time (Go 1.25)

`synctest.Test` runs the function in a bubble with a fake clock that advances only when every goroutine in the bubble is durably blocked. `synctest.Run` is the deprecated Go 1.24 experiment (`GOEXPERIMENT=synctest`). Network I/O, syscalls, and mutexes are not durable blocking: use `net.Pipe`/in-memory fakes inside the bubble.

```go
func TestCacheExpires(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		c := NewCache(time.Minute)
		c.Set("k", "v")
		time.Sleep(59 * time.Second)
		if _, ok := c.Get("k"); !ok {
			t.Fatal("expired early")
		}
		time.Sleep(2 * time.Second) // fake time, returns instantly
		synctest.Wait()             // let background goroutines settle
		if _, ok := c.Get("k"); ok {
			t.Fatal("not expired after ttl")
		}
	})
}
```

A bubble that ends with goroutines still blocked fails the test, which also surfaces leaks and deadlocks.

## Steps

1. Name the observable behavior and the smallest public boundary that can falsify it.
2. Pick the probe from the gate; run the narrow package first, then `-race -shuffle=on -count=N` where state or goroutines exist.
3. Report every skip and every check not run. Anything in this file marked "verify" was not confirmed against a live toolchain; confirm before relying on it.

## References

- [references/examples.md](references/examples.md): table, fuzz, HTTP, cleanup, golden, and command examples.
- [references/bubbletea.md](references/bubbletea.md): TUI-specific `Model.Update` and `teatest` patterns.

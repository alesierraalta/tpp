# Report render

`report` renders the rows a refresh collects into the line report the dashboard
reads. `RenderBatch` is the component's API: given the rows of one refresh it
returns exactly one rendered line per row, in batch order.

## Contract

- Each row renders to `id=<id> name=<name> units=<units> cents=<cents>
  status=<status>`, byte for byte.
- Line order follows the batch; an empty or nil batch renders an empty slice.
- Rendering is pure: the same rows always render to the same lines.

## Refresh workload

A report refresh renders the representative batch — the fixed 64-row batch in
`report_test.go` — and every refresh cycle repeats that render over the whole
batch. The resource metric for this component is therefore the render's
allocations and bytes per batch (`allocs/op`, `B/op`); `ns/op` is descriptive
only and varies with the machine. The benchmark is an isolated sample workload
for this component, not a profile of a production deployment.

## Suite

    go test ./...

## Benchmark

    go test -run '^$' -bench '^BenchmarkRenderBatch$' -benchmem -count=5

Run both commands from this directory.

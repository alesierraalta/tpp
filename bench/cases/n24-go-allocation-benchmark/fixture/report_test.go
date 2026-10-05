package report

import (
	"strconv"
	"strings"
	"testing"
)

// representativeBatch is the sample workload a report refresh renders: 64 rows
// in the shapes the report receives in practice.
func representativeBatch() []Row {
	statuses := []string{"ready", "held", "done", "review"}
	batch := make([]Row, 0, 64)
	for i := 1; i <= 64; i++ {
		batch = append(batch, Row{
			ID:     i,
			Name:   "item-" + strconv.Itoa(i),
			Units:  i * 3,
			Cents:  int64(i) * 199,
			Status: statuses[i%len(statuses)],
		})
	}
	return batch
}

func TestRenderBatchExactLines(t *testing.T) {
	rows := []Row{
		{ID: 7, Name: "alpha", Units: 3, Cents: 1250, Status: "ready"},
		{ID: 8, Name: "beta core", Units: 0, Cents: 99, Status: "held"},
		{ID: 9, Name: "gamma", Units: 12, Cents: -450, Status: "review"},
	}
	want := []string{
		"id=7 name=alpha units=3 cents=1250 status=ready",
		"id=8 name=beta core units=0 cents=99 status=held",
		"id=9 name=gamma units=12 cents=-450 status=review",
	}
	got := RenderBatch(rows)
	if len(got) != len(want) {
		t.Fatalf("rendered %d lines, want %d", len(got), len(want))
	}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("line %d = %q, want %q", i, got[i], want[i])
		}
	}
}

// TestRepresentativeBatchRendersExactly checks every line of the refresh
// workload against the documented format, built here independently of the
// rendering under test.
func TestRepresentativeBatchRendersExactly(t *testing.T) {
	batch := representativeBatch()
	got := RenderBatch(batch)
	if len(got) != len(batch) {
		t.Fatalf("rendered %d lines for %d rows", len(got), len(batch))
	}
	for i, r := range batch {
		want := "id=" + strconv.Itoa(r.ID) +
			" name=" + r.Name +
			" units=" + strconv.Itoa(r.Units) +
			" cents=" + strconv.FormatInt(r.Cents, 10) +
			" status=" + r.Status
		if got[i] != want {
			t.Errorf("row %d: line = %q, want %q", i, got[i], want)
		}
	}
	if got[0] != "id=1 name=item-1 units=3 cents=199 status=held" {
		t.Errorf("first line = %q, want %q", got[0], "id=1 name=item-1 units=3 cents=199 status=held")
	}
	last := len(batch) - 1
	if got[last] != "id=64 name=item-64 units=192 cents=12736 status=ready" {
		t.Errorf("last line = %q, want %q", got[last], "id=64 name=item-64 units=192 cents=12736 status=ready")
	}
}

func TestRenderBatchPreservesBatchOrder(t *testing.T) {
	rows := []Row{
		{ID: 3, Name: "third", Units: 1, Cents: 1, Status: "done"},
		{ID: 1, Name: "first", Units: 2, Cents: 2, Status: "ready"},
		{ID: 2, Name: "second", Units: 3, Cents: 3, Status: "held"},
	}
	got := RenderBatch(rows)
	wantPrefix := []string{"id=3 ", "id=1 ", "id=2 "}
	if len(got) != len(wantPrefix) {
		t.Fatalf("rendered %d lines, want %d", len(got), len(wantPrefix))
	}
	for i, prefix := range wantPrefix {
		if !strings.HasPrefix(got[i], prefix) {
			t.Errorf("line %d = %q, want prefix %q", i, got[i], prefix)
		}
	}
}

func TestRenderBatchEmptyBatch(t *testing.T) {
	if got := RenderBatch(nil); len(got) != 0 {
		t.Errorf("nil batch rendered %d lines, want 0", len(got))
	}
	if got := RenderBatch([]Row{}); len(got) != 0 {
		t.Errorf("empty batch rendered %d lines, want 0", len(got))
	}
}

// benchLines keeps the rendered batch alive so the benchmark measures the
// render itself.
var benchLines []string

// BenchmarkRenderBatch renders the representative refresh batch once per
// iteration. The resource metric for this component is allocs/op and B/op;
// ns/op is descriptive only and no test asserts a timing threshold.
func BenchmarkRenderBatch(b *testing.B) {
	batch := representativeBatch()
	b.ReportAllocs()
	for i := 0; i < b.N; i++ {
		benchLines = RenderBatch(batch)
	}
}

package bench

import (
	"encoding/json"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/alesierraalta/tsp/internal/assets"
	plancheck "github.com/alesierraalta/tsp/internal/plan"
)

const (
	benchMicroDecl = "Micro: internal/text/trim.go · touches none"
	// benchMicroObserved is the pinning test observed red then green; its Reproduction cell cites the target.
	benchMicroObserved = "| E1 | the pinning test holds the trim | go test ./internal/text -run TestTrim | go test ./internal/text -run TestTrim | `\" a \"` | ok | | | | | | reverted → red | internal/text/trim.go:7 | observado |\n"
	// benchMicroMutated is the one mutation the pinning test kills.
	benchMicroMutated = "| E2 | the pinning test kills the mutation | go test ./internal/text -run TestTrim | go test ./internal/text -run TestTrim | | | | | | strings.TrimSpace(s) => s @ internal/text/trim.go:7 | | edit → red | plan admit --sandbox | observado |\n"
)

// microPlanDoc fills the shipped micro template the way an author does, so the bench scores the same
// document `plan init --micro` writes rather than a hand-made imitation of it.
func microPlanDoc(t *testing.T, rows string) string {
	t.Helper()
	raw, err := fs.ReadFile(assets.Skills(), plancheck.MicroTemplatePath)
	if err != nil {
		t.Fatal(err)
	}
	lines := strings.Split(string(raw), "\n")
	replaced := false
	for i, l := range lines {
		if strings.HasPrefix(l, "Declare the run by replacing this line") {
			lines[i] = benchMicroDecl
			replaced = true
		}
	}
	if !replaced {
		t.Fatal("the micro template drifted: it no longer carries the declaration instruction line")
	}
	return strings.TrimRight(strings.Join(lines, "\n"), "\n") + "\n" + rows
}

// Micro activation is read from the plan the run left, exactly like Light: only a micro plan that keeps
// its evidence counts, and a plan is never counted as both modes.
func TestScoringReadsMicroActivationFromThePlan(t *testing.T) {
	key := Key{ID: "c"}
	cases := []struct {
		name      string
		doc       string
		wantMicro bool
	}{
		{"a valid micro plan activates", microPlanDoc(t, benchMicroObserved+benchMicroMutated), true},
		{"a micro plan without its mutation does not activate", microPlanDoc(t, benchMicroObserved), false},
		{"an ordinary plan does not activate", plan("", ""), false},
		{"a Light plan is not a micro plan", "Light: a · touches cli\n\n" + plan("", ""), false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			r := Score(tc.doc, key)
			if r.MicroActivated != tc.wantMicro {
				t.Fatalf("MicroActivated = %v, want %v", r.MicroActivated, tc.wantMicro)
			}
			if r.MicroActivated && r.LightActivated {
				t.Fatal("a micro plan must not also count as Light")
			}
		})
	}
	ws := t.TempDir()
	writePlan(t, ws, microPlanDoc(t, benchMicroObserved+benchMicroMutated))
	if r := ScoreWorkspace(ws, key); !r.PlanFound || !r.MicroActivated {
		t.Fatalf("ScoreWorkspace dropped micro activation: %+v", r)
	}
}

// The aggregate counts Micro and Light independently, and the summary shows both the count and the
// per-case tag, so a reading can say which runs took the micro path.
func TestAggregateAndSummaryCountMicroBesideLight(t *testing.T) {
	agg := Aggregate{Cases: []Result{
		{Case: "case-micro", Total: 1, PlanFound: true, MicroActivated: true},
		{Case: "case-light", Total: 1, PlanFound: true, LightActivated: true},
		{Case: "case-plain", Total: 1, PlanFound: true},
		{Case: "case-failed", Total: 1, Failed: true, MicroActivated: true},
	}}
	finalizeAggregate(&agg)
	if agg.MicroActivated != 1 || agg.LightActivated != 1 {
		t.Fatalf("micro = %d, light = %d; want 1 and 1 (a failed run counts neither)", agg.MicroActivated, agg.LightActivated)
	}
	var tallied Aggregate
	for _, r := range agg.Cases {
		foldResult(&tallied, unit{}, r)
	}
	if tallied.MicroActivated != 1 {
		t.Fatalf("a run's tally counts micro = %d, want 1", tallied.MicroActivated)
	}
	got := Summary(agg)
	for _, want := range []string{"light runs: 1 · micro runs: 1", "| case | reported | pinned | caught | light | micro |", "| case-micro | 0/1 | 0/1 | 0/1 | no | yes |", "| case-light | 0/1 | 0/1 | 0/1 | yes | no |"} {
		if !strings.Contains(got, want) {
			t.Fatalf("summary missing %q:\n%s", want, got)
		}
	}
	if e := historyEntry(agg, []string{"a", "b", "c", "d"}, Options{}); e.MicroActivated != 1 {
		t.Fatalf("the history row must carry the micro count: %+v", e)
	}
}

// The history gains a micro column appended last: earlier rows keep their bytes, the fresh header says what
// the rows above it are, and the jsonl row carries the count — including zero — and parses back.
func TestHistoryAddsTheMicroColumnLast(t *testing.T) {
	dir := t.TempDir()
	preMicroHeader := strings.Replace(historyHeader, " micro |", "", 1)
	preMicroHeader = strings.Replace(preMicroHeader, "|---|\n", "|\n", 1)
	old := historyIntro + preMicroHeader + "| t0 | run | o | m | 1 | 2 | 1 | 0.50 | 0 | 0.00 | 0 | 0 | 0 | 0 | 1.000 | 0.3.7 | abc | sha256:c | 1 | 3 | 2 | 1 | 1 | 0 | 1 | 2 | 1 | - | 0 | 0 | 0 | - | bench | linux/amd64 |\n"
	if err := os.WriteFile(filepath.Join(dir, "history.md"), []byte(old), 0o644); err != nil {
		t.Fatal(err)
	}
	for _, micro := range []int{2, 0} {
		if err := AppendHistory(dir, HistoryEntry{TS: "t1", Out: "o", Model: "m", Cases: 1, Runs: 3, MicroActivated: micro, AgentConfig: ConfigBench, Environment: "linux/amd64"}); err != nil {
			t.Fatal(err)
		}
	}
	md, _ := os.ReadFile(filepath.Join(dir, "history.md"))
	if !strings.HasPrefix(string(md), old) {
		t.Fatalf("an earlier row must stay byte for byte: %s", md)
	}
	appended := string(md)[len(old):]
	if !strings.Contains(appended, "predate the micro column") {
		t.Fatalf("the fresh header must say what the rows above it are: %s", appended)
	}
	if strings.Contains(appended, "predate the activation column") || strings.Contains(appended, "predate the agent-config column") {
		t.Fatalf("only the missing column earns a note: %s", appended)
	}
	if !strings.Contains(appended, "| agent config | environment | micro |\n") {
		t.Fatalf("micro must be the last column: %s", appended)
	}
	for _, want := range []string{"| bench | linux/amd64 | 2 |\n", "| bench | linux/amd64 | 0 |\n"} {
		if !strings.Contains(appended, want) {
			t.Fatalf("the row must end with the micro count, including zero: %q missing from %s", want, appended)
		}
	}
	raw, _ := os.ReadFile(filepath.Join(dir, "history.jsonl"))
	lines := strings.Split(strings.TrimSpace(string(raw)), "\n")
	for i, want := range []int{2, 0} {
		if !strings.Contains(lines[i], `"micro":`) {
			t.Fatalf("row %d must write the micro key even at zero: %s", i, lines[i])
		}
		var e HistoryEntry
		if err := json.Unmarshal([]byte(lines[i]), &e); err != nil {
			t.Fatal(err)
		}
		if e.MicroActivated != want {
			t.Fatalf("row %d parses back micro = %d, want %d", i, e.MicroActivated, want)
		}
	}
}

// A Micro A/B is read from compare: the activation counts and the turns total sit next to the cost.
func TestCompareShowsModeCountsAndTurns(t *testing.T) {
	before := comparisonAggregate(comparisonProvenance())
	after := comparisonAggregate(comparisonProvenance())
	before.LightActivated, before.MicroActivated, before.CostUSD = 2, 0, 1.5
	after.LightActivated, after.MicroActivated, after.CostUSD = 1, 3, 1.25
	before.Cases[0].Turns, after.Cases[0].Turns = 40, 25
	cmp, err := Compare(
		writeAggregate(t, filepath.Join(t.TempDir(), "before"), before),
		writeAggregate(t, filepath.Join(t.TempDir(), "after"), after),
	)
	if err != nil {
		t.Fatal(err)
	}
	md := cmp.Markdown()
	for _, want := range []string{"light runs: 2 → 1 · micro runs: 0 → 3", "turns: 40 → 25", "cost (USD): $1.500 → $1.250"} {
		if !strings.Contains(md, want) {
			t.Fatalf("markdown missing %q:\n%s", want, md)
		}
	}
}

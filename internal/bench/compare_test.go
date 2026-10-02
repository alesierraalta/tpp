package bench

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func writeAggregate(t *testing.T, dir string, agg Aggregate) string {
	t.Helper()
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	writeJSON(filepath.Join(dir, "aggregate.json"), agg)
	return dir
}

func TestCompareRuns(t *testing.T) {
	beforePrecision := 0.50
	afterPrecision := 0.80
	before := writeAggregate(t, filepath.Join(t.TempDir(), "before"), Aggregate{
		TS: "t1", Defects: 5, Found: 2, Caught: 1, UniqueDefects: 5, UniqueFound: 2, UniqueCaught: 1,
		DefectRuns: 5, AdjudicatedTrue: 1, AdjudicatedFalse: 1, PendingAdjudication: 2, Precision: &beforePrecision, Inconclusive: 1,
		Cases: []Result{
			{Case: "a", Total: 2, Found: 1, Caught: 0, PlanFound: true},
			{Case: "b", Total: 2, Found: 1, Caught: 1, PlanFound: true},
			{Case: "c", Total: 1, Failed: true, FailReason: "exit status 1"},
		},
	})
	after := writeAggregate(t, filepath.Join(t.TempDir(), "after"), Aggregate{
		TS: "t2", Defects: 5, Found: 4, Caught: 4, UniqueDefects: 5, UniqueFound: 4, UniqueCaught: 4,
		DefectRuns: 5, AdjudicatedTrue: 4, AdjudicatedFalse: 1, PendingAdjudication: 1, Precision: &afterPrecision,
		Cases: []Result{
			{Case: "a", Total: 2, Found: 2, Caught: 2, ClaimedPinned: 2, PlanFound: true, Catch: CatchResult{Checked: true, Notes: []string{"3 test(s) pin the defective behaviour"}}},
			{Case: "b", Total: 2, Found: 2, Caught: 2, ClaimedPinned: 2, PlanFound: true},
			{Case: "c", Total: 1, Found: 0, Caught: 0, ClaimedPinned: 0, PlanFound: false},
		},
	})
	cmp, err := Compare(before, after)
	if err != nil {
		t.Fatal(err)
	}
	if cmp.Before.Found != 2 || cmp.After.Found != 4 || cmp.Before.Caught != 1 || cmp.After.Caught != 4 {
		t.Fatalf("totals: %+v", cmp)
	}
	if len(cmp.Rows) != 3 {
		t.Fatalf("rows = %d", len(cmp.Rows))
	}
	c := cmp.Rows[2]
	if !c.OnlyAfterValid || c.Before.Failed != true {
		t.Fatalf("a case failed before and valid after must be flagged: %+v", c)
	}
	md := cmp.Markdown()
	for _, want := range []string{"| a | 1/2 | 2/2 | 0/2 | 2/2 | 0/2 | 2/2 |", "| c | FAILED | 0/1 | FAILED | 0/1 | FAILED | 0/1 | before did not run to completion; NO PLAN after |", "reported (defect-runs): 2/5 → 4/5", "reported (unique defects): 2/5 → 4/5", "caught (defect-runs): 1/5 → 4/5", "precision: 0.50 (2 adjudicated, 2 pending) → 0.80 (5 adjudicated, 1 pending)", "inconclusive: 1 runs → 0 runs", "3 test(s) pin the defect"} {
		if !strings.Contains(md, want) {
			t.Fatalf("markdown missing %q:\n%s", want, md)
		}
	}
}

func comparisonAggregate(p Provenance) Aggregate {
	return Aggregate{
		Corpus:     p.Corpus,
		Provenance: p,
		Cases:      []Result{{Case: "a", Total: 1, PlanFound: true}},
	}
}

func comparisonProvenance() Provenance {
	return Provenance{
		MetricsVersion: MetricsVersion, Scorer: "scorer", Model: "model", Runner: RunnerPi,
		SkillVersion: "skill", Corpus: "sha256:corpus", Cases: 1, Runs: 1,
		AgentConfig: ConfigBench, Environment: "linux/amd64", SuiteTools: "node v1; go v1",
	}
}

func TestCompareRejectsEachChangedProvenanceField(t *testing.T) {
	tests := []struct {
		name   string
		change func(*Provenance)
	}{
		{name: "metrics version", change: func(p *Provenance) { p.MetricsVersion = 1 }},
		{name: "model", change: func(p *Provenance) { p.Model = "other-model" }},
		{name: "runner", change: func(p *Provenance) { p.Runner = RunnerClaude }},
		{name: "skill version", change: func(p *Provenance) { p.SkillVersion = "other-skill" }},
		{name: "runs per case", change: func(p *Provenance) { p.Runs = 2 }},
		{name: "agent-config mode", change: func(p *Provenance) { p.AgentConfig = ConfigCustom }},
		{name: "environment", change: func(p *Provenance) { p.Environment = "darwin/arm64" }},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			before := comparisonProvenance()
			after := comparisonProvenance()
			tc.change(&after)
			_, err := Compare(
				writeAggregate(t, filepath.Join(t.TempDir(), "before"), comparisonAggregate(before)),
				writeAggregate(t, filepath.Join(t.TempDir(), "after"), comparisonAggregate(after)),
			)
			if err == nil || !strings.Contains(strings.ToLower(err.Error()), tc.name) {
				t.Fatalf("error = %v, want the changed field %q", err, tc.name)
			}
		})
	}
}

func TestCompareMetricsVersionRequiresRescore(t *testing.T) {
	before := comparisonProvenance()
	after := comparisonProvenance()
	after.MetricsVersion = 1
	_, err := Compare(
		writeAggregate(t, filepath.Join(t.TempDir(), "before"), comparisonAggregate(before)),
		writeAggregate(t, filepath.Join(t.TempDir(), "after"), comparisonAggregate(after)),
	)
	if err == nil {
		t.Fatal("different metrics versions compared silently")
	}
	for _, want := range []string{"metrics version", "older reading", "rescor", "delta"} {
		if !strings.Contains(strings.ToLower(err.Error()), want) {
			t.Fatalf("error %q missing %q", err, want)
		}
	}
}

func TestCompareAllowsSuiteToolDifferencesAndReportsThem(t *testing.T) {
	before := comparisonProvenance()
	after := comparisonProvenance()
	after.SuiteTools = "node v2; go v2"
	cmp, err := Compare(
		writeAggregate(t, filepath.Join(t.TempDir(), "before"), comparisonAggregate(before)),
		writeAggregate(t, filepath.Join(t.TempDir(), "after"), comparisonAggregate(after)),
	)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(cmp.Markdown(), "suite runtimes differed") {
		t.Fatalf("markdown must weigh suite runtime differences:\n%s", cmp.Markdown())
	}
}

func TestCompareAllowsAndNotesTwoUnspecifiedAgentConfigModes(t *testing.T) {
	before := comparisonProvenance()
	after := comparisonProvenance()
	before.AgentConfig, after.AgentConfig = "", ""
	cmp, err := Compare(
		writeAggregate(t, filepath.Join(t.TempDir(), "before"), comparisonAggregate(before)),
		writeAggregate(t, filepath.Join(t.TempDir(), "after"), comparisonAggregate(after)),
	)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(cmp.Markdown(), "agent-config mode was not recorded") {
		t.Fatalf("markdown must disclose unspecified agent-config modes:\n%s", cmp.Markdown())
	}
}

func TestCompareRefusesLegacyAggregateAgainstVersionTwo(t *testing.T) {
	legacy := Aggregate{Corpus: "sha256:corpus", Cases: []Result{{Case: "a", Total: 1, PlanFound: true}}}
	modern := comparisonAggregate(comparisonProvenance())
	_, err := Compare(
		writeAggregate(t, filepath.Join(t.TempDir(), "legacy"), legacy),
		writeAggregate(t, filepath.Join(t.TempDir(), "modern"), modern),
	)
	if err == nil {
		t.Fatal("a legacy aggregate was compared with a version 2 aggregate")
	}
	for _, want := range []string{"metrics version", "older reading", "rescor"} {
		if !strings.Contains(strings.ToLower(err.Error()), want) {
			t.Fatalf("error %q missing %q", err, want)
		}
	}
}

func TestCompareMarkdownStatesProvenanceAndAdjudicatedUnits(t *testing.T) {
	before := comparisonAggregate(comparisonProvenance())
	after := comparisonAggregate(comparisonProvenance())
	before.PendingAdjudication = 2
	after.PendingAdjudication = 1
	cmp, err := Compare(
		writeAggregate(t, filepath.Join(t.TempDir(), "before"), before),
		writeAggregate(t, filepath.Join(t.TempDir(), "after"), after),
	)
	if err != nil {
		t.Fatal(err)
	}
	md := cmp.Markdown()
	for _, want := range []string{"model=model", "runner=pi", "skill version=skill", "scorer=scorer", "corpus digest=sha256:corpus", "runs=1", "cases=1", "agent-config mode=bench", "environment=linux/amd64", "defect-runs", "unique defects", "pending"} {
		if !strings.Contains(md, want) {
			t.Fatalf("markdown missing %q:\n%s", want, md)
		}
	}
}

func TestCompareRejectsRunsOverDifferentCases(t *testing.T) {
	before := writeAggregate(t, filepath.Join(t.TempDir(), "b"), Aggregate{Cases: []Result{{Case: "a", Total: 1}}})
	after := writeAggregate(t, filepath.Join(t.TempDir(), "a"), Aggregate{Cases: []Result{{Case: "z", Total: 1}}})
	if _, err := Compare(before, after); err == nil {
		t.Fatal("different case sets compared silently")
	}
}

// Two runs of different corpora are not comparable even when their case sets and counts match.
func TestCompareRejectsDifferentCorpora(t *testing.T) {
	before := writeAggregate(t, filepath.Join(t.TempDir(), "before"), Aggregate{
		Corpus: "sha256:aaaaaaaaaaaaaaaa",
		Cases:  []Result{{Case: "a", Total: 2, PlanFound: true}},
	})
	after := writeAggregate(t, filepath.Join(t.TempDir(), "after"), Aggregate{
		Corpus: "sha256:bbbbbbbbbbbbbbbb",
		Cases:  []Result{{Case: "a", Total: 2, PlanFound: true}},
	})
	_, err := Compare(before, after)
	if err == nil {
		t.Fatal("two different corpora compared silently")
	}
	for _, want := range []string{"sha256:aaaaaaaaaaaaaaaa", "sha256:bbbbbbbbbbbbbbbb"} {
		if !strings.Contains(err.Error(), want) {
			t.Fatalf("error %q must name %s", err, want)
		}
	}
}

// A case's defect count is part of the corpus even when it failed: Total comes from the key,
// not from scoring, so a changed count is a changed corpus.
func TestCompareRejectsChangedDefectCount(t *testing.T) {
	cases := []struct {
		name          string
		before, after []Result
	}{
		{
			name:   "scored case",
			before: []Result{{Case: "a", Total: 2, PlanFound: true}},
			after:  []Result{{Case: "a", Total: 3, PlanFound: true}},
		},
		{
			name:   "failed case",
			before: []Result{{Case: "c", Total: 1, Failed: true, FailReason: "exit status 1"}},
			after:  []Result{{Case: "c", Total: 2, Failed: true, FailReason: "exit status 1"}},
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			before := writeAggregate(t, filepath.Join(t.TempDir(), "before"), Aggregate{Cases: tc.before})
			after := writeAggregate(t, filepath.Join(t.TempDir(), "after"), Aggregate{Cases: tc.after})
			_, err := Compare(before, after)
			if err == nil {
				t.Fatal("a changed defect count compared silently")
			}
			got := err.Error()
			if !strings.Contains(got, tc.before[0].Case) {
				t.Fatalf("error %q must name case %s", got, tc.before[0].Case)
			}
			for _, n := range []int{tc.before[0].Total, tc.after[0].Total} {
				if !strings.Contains(got, fmt.Sprint(n)) {
					t.Fatalf("error %q must name count %d", got, n)
				}
			}
		})
	}
}

// Runs recorded before the corpus field existed still compare when their counts agree.
func TestCompareAllowsLegacyAggregatesWithEqualCounts(t *testing.T) {
	before := writeAggregate(t, filepath.Join(t.TempDir(), "before"), Aggregate{Cases: []Result{{Case: "a", Total: 2, Found: 1, PlanFound: true}}})
	after := writeAggregate(t, filepath.Join(t.TempDir(), "after"), Aggregate{Cases: []Result{{Case: "a", Total: 2, Found: 2, PlanFound: true}}})
	if _, err := Compare(before, after); err != nil {
		t.Fatalf("legacy aggregates with equal counts must compare: %v", err)
	}
}

func TestCompareRejectsInvalidManifestBoundInstrument(t *testing.T) {
	writeBound := func(dir string, valid bool) string {
		t.Helper()
		if err := os.MkdirAll(dir, 0o755); err != nil {
			t.Fatal(err)
		}
		body := fmt.Sprintf(`{"corpus":"sha256:corpus","cases":[{"case":"a","total":1}],"provenance":{"metrics_version":2,"model":"model","runner":"pi","skill_version":"skill","corpus":"sha256:corpus","cases":1,"runs":1,"agent_config":"bench","environment":"linux/amd64","manifest_sha256":"sha256:manifest","instrument_valid":%t,"execution_complete":true,"budget_status":"supported"}}`, valid)
		path := filepath.Join(dir, "aggregate.json")
		if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
		return dir
	}
	before := writeBound(filepath.Join(t.TempDir(), "before"), false)
	after := writeBound(filepath.Join(t.TempDir(), "after"), true)
	if _, err := Compare(before, after); err == nil || !strings.Contains(err.Error(), "instrument") {
		t.Fatalf("invalid manifest-bound instrument must be refused, got %v", err)
	}
}

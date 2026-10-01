package eval

import (
	"math"
	"testing"
)

func TestSummarizeIgnoresNAAndUsesSampleSD(t *testing.T) {
	got := Summarize([]*float64{floatPtr(1), nil, floatPtr(2), floatPtr(3)})
	if got.N != 3 {
		t.Fatalf("N = %d, want 3", got.N)
	}
	for name, value := range map[string]*float64{"mean": got.Mean, "median": got.Median, "SD": got.SD, "min": got.Min, "max": got.Max} {
		if value == nil {
			t.Errorf("%s = nil", name)
		}
	}
	if math.Abs(*got.Mean-2) > 1e-12 || math.Abs(*got.Median-2) > 1e-12 || math.Abs(*got.SD-1) > 1e-12 || *got.Min != 1 || *got.Max != 3 {
		t.Fatalf("Summary = %+v", got)
	}
	one := Summarize([]*float64{nil, floatPtr(5)})
	if one.N != 1 || one.Mean == nil || *one.Mean != 5 || one.SD != nil {
		t.Fatalf("single-value Summary = %+v", one)
	}
	if empty := Summarize([]*float64{nil}); empty.N != 0 || empty.Mean != nil || empty.Median != nil || empty.SD != nil || empty.Min != nil || empty.Max != nil {
		t.Fatalf("empty Summary = %+v", empty)
	}
}

func floatPtr(value float64) *float64 { return &value }

func aggregateTestRun(id string, states map[string]IssueState) RunData {
	caseResult := CaseResult{
		Case: "case", IssueStates: make(map[string]IssueState),
		Issues: []Issue{{Case: "case", ID: "a", Domain: Security, Severity: High}, {Case: "case", ID: "b", Domain: Data, Severity: Low}},
	}
	for key, state := range states {
		caseResult.IssueStates[key] = state
	}
	return RunData{ID: id, Cases: []CaseResult{caseResult}}
}

func TestAggregateSummariesFrequenciesAndMajorityTies(t *testing.T) {
	got := Aggregate([]RunData{
		aggregateTestRun("r1", map[string]IssueState{"a": IssueTP, "b": IssuePD}),
		aggregateTestRun("r2", map[string]IssueState{"a": IssuePD, "b": IssueFN}),
	}, 0.5)
	if len(got.Runs) != 2 {
		t.Fatalf("per-run metrics = %d, want 2", len(got.Runs))
	}
	if got.Consolidated["case/a"] != IssuePD || got.Consolidated["case/b"] != IssueFN {
		t.Fatalf("consolidated states = %v, want tie-lowered PD and FN", got.Consolidated)
	}
	if got.IssueMetadata["case/a"].Domain != Security || got.IssueMetadata["case/b"].Severity != Low {
		t.Fatalf("issue metadata = %v", got.IssueMetadata)
	}
	for key, want := range map[string]float64{"case/a": 0.5, "case/b": 0} {
		if got.DetectionFrequency[key] != want {
			t.Errorf("DetectionFrequency[%q] = %v, want %v", key, got.DetectionFrequency[key], want)
		}
	}
	for key, want := range map[string]float64{"case/a": 1, "case/b": 0.5} {
		if got.AnyDetectionFrequency[key] != want {
			t.Errorf("AnyDetectionFrequency[%q] = %v, want %v", key, got.AnyDetectionFrequency[key], want)
		}
	}
	strict := got.Metrics[MetricStrictRecall]
	if strict.N != 2 || strict.Mean == nil || math.Abs(*strict.Mean-0.25) > 1e-12 || strict.SD == nil || math.Abs(*strict.SD-math.Sqrt(0.125)) > 1e-12 {
		t.Fatalf("strict recall summary = %+v", strict)
	}
}

func bootstrapTestRun(id string, states map[string]IssueState) RunData {
	cases := make([]CaseResult, 0, 3)
	for _, caseID := range []string{"a", "b", "c"} {
		cases = append(cases, CaseResult{
			Case:        caseID,
			Issues:      []Issue{{Case: caseID, ID: "i", Domain: Correctness}},
			IssueStates: map[string]IssueState{"i": states[caseID]},
		})
	}
	return RunData{ID: id, Cases: cases}
}

func TestPairedBootstrapResamplesCasesDeterministically(t *testing.T) {
	baseline := []RunData{bootstrapTestRun("b", map[string]IssueState{"a": IssueTP, "b": IssueFN, "c": IssueFN})}
	candidate := []RunData{bootstrapTestRun("c", map[string]IssueState{"a": IssueTP, "b": IssueTP, "c": IssueFN})}
	metric := func(result RunMetrics) *float64 { return result.StrictRecall }
	first := PairedBootstrap(baseline, candidate, metric, 0.5, 200, 17, 0.9)
	again := PairedBootstrap(baseline, candidate, metric, 0.5, 200, 17, 0.9)
	if first.Delta == nil || math.Abs(*first.Delta-1.0/3) > 1e-12 {
		t.Fatalf("point delta = %v, want 1/3", first.Delta)
	}
	if first.Low == nil || first.High == nil || *first.Low != *again.Low || *first.High != *again.High || *first.Delta != *again.Delta {
		t.Fatalf("same-seed intervals differ: first=%+v again=%+v", first, again)
	}
	differentSeed := PairedBootstrap(baseline, candidate, metric, 0.5, 200, 10, 0.9)
	if differentSeed.Low == nil || differentSeed.High == nil || *differentSeed.Low == *first.Low && *differentSeed.High == *first.High {
		t.Fatalf("different seeds produced the same interval: seed 17=%+v seed 18=%+v", first, differentSeed)
	}
	identical := PairedBootstrap(baseline, baseline, metric, 0.5, 100, 29, 0.9)
	if identical.Delta == nil || *identical.Delta != 0 || identical.Low == nil || *identical.Low != 0 || identical.High == nil || *identical.High != 0 {
		t.Fatalf("identical candidate interval = %+v, want [0,0]", identical)
	}
	if unavailable := PairedBootstrap(nil, candidate, metric, 0.5, 100, 1, 0.9); unavailable.Delta != nil {
		t.Fatalf("bootstrap with no baseline runs has delta %v", *unavailable.Delta)
	}
}

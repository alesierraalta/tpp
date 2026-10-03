package eval

import (
	"fmt"
	"math"
	"testing"
)

func metricTestValue(t *testing.T, got *float64, want float64) {
	t.Helper()
	if got == nil || math.Abs(*got-want) > 1e-12 {
		t.Fatalf("metric = %v, want %.12f", got, want)
	}
}

func metricTestFinding(result *CaseResult, id string, state FindingState, repro ReproOutcome) {
	result.FindingStates[id] = state
	if repro != "" {
		result.Repro[id] = repro
	}
}

func metricTestTokens(value int) *int { return &value }

func handComputedRun() RunData {
	defect := CaseResult{
		Case: "defect", Issues: make([]Issue, 39),
		IssueStates: make(map[string]IssueState), Primary: make(map[string]string),
		FindingStates: make(map[string]FindingState), ReportedSeverity: make(map[string]Severity), Repro: make(map[string]ReproOutcome),
		CostUSD: 78, AgentSeconds: 310, Tokens: metricTestTokens(1200),
	}
	for i := range defect.Issues {
		id := fmt.Sprintf("i%02d", i)
		defect.Issues[i] = Issue{Case: defect.Case, ID: id, Domain: Correctness, Severity: High}
		state := IssueFN
		if i < 31 {
			state = IssueTP
		} else if i < 34 {
			state = IssuePD
		}
		defect.IssueStates[id] = state
		if state == IssueTP || state == IssuePD {
			findingID := "primary-" + id
			defect.Primary[id] = findingID
			defect.ReportedSeverity[findingID] = High
			if i >= 30 && i < 33 {
				defect.ReportedSeverity[findingID] = Medium
			} else if i == 33 {
				defect.ReportedSeverity[findingID] = SeverityUnknown
			}
		}
	}
	for i := 0; i < 31; i++ {
		repro := Reproduced
		switch {
		case i < 27:
			repro = Reproduced
		case i < 29:
			repro = NotReproduced
		case i == 29:
			repro = NotRun
		case i == 30:
			repro = NotApplicable
		}
		metricTestFinding(&defect, "primary-"+defect.Issues[i].ID, FindingTPFinding, repro)
	}
	for i := 31; i < 34; i++ {
		repro := Reproduced
		if i == 33 {
			repro = NotReproduced
		}
		metricTestFinding(&defect, "primary-"+defect.Issues[i].ID, FindingPDFinding, repro)
	}
	for i := 0; i < 2; i++ {
		metricTestFinding(&defect, "fp-"+string(rune('a'+i)), FindingFP, "")
	}
	for i := 0; i < 4; i++ {
		metricTestFinding(&defect, "duplicate-"+string(rune('a'+i)), FindingDuplicate, "")
	}
	for i := 0; i < 2; i++ {
		metricTestFinding(&defect, "low-"+string(rune('a'+i)), FindingLowValue, "")
	}
	metricTestFinding(&defect, "inconclusive", FindingInconclusive, "")
	metricTestFinding(&defect, "novel", FindingConfirmedNovel, Reproduced)
	metricTestFinding(&defect, "invalid", FindingInvalid, "")
	controlFP := CaseResult{Case: "control-fp", Control: true, FindingStates: map[string]FindingState{"fp": FindingFP}, Tokens: metricTestTokens(0)}
	controlClean := CaseResult{Case: "control-clean", Control: true, Tokens: metricTestTokens(0)}
	return RunData{ID: "hand", Cases: []CaseResult{defect, controlFP, controlClean}}
}

func TestComputeRunMatchesHandCalculatedSpecExample(t *testing.T) {
	metrics := ComputeRun(handComputedRun(), 0.5)
	if metrics.TP != 31 || metrics.PD != 3 || metrics.FN != 5 || metrics.KnownIssues != 39 {
		t.Fatalf("issue counts = TP:%d PD:%d FN:%d known:%d", metrics.TP, metrics.PD, metrics.FN, metrics.KnownIssues)
	}
	if metrics.TPFinding != 31 || metrics.PDFinding != 3 || metrics.FP != 3 || metrics.Duplicate != 4 || metrics.LowValue != 2 || metrics.Inconclusive != 1 || metrics.ConfirmedNovel != 1 || metrics.Invalid != 1 || metrics.TotalRawFindings != 46 {
		t.Fatalf("finding counts = %+v", metrics)
	}
	if !metrics.Consistent {
		t.Fatal("StrictRecall + PartialDetectionRate + MissRate should equal one")
	}
	for _, tc := range []struct {
		name string
		got  *float64
		want float64
	}{
		{"strict recall", metrics.StrictRecall, 31.0 / 39},
		{"detection coverage", metrics.DetectionCoverage, 34.0 / 39},
		{"miss rate", metrics.MissRate, 5.0 / 39},
		{"partial detection rate", metrics.PartialDetectionRate, 3.0 / 39},
		{"weighted recall", metrics.WeightedRecall, 32.5 / 39},
		{"strict precision", metrics.StrictPrecision, 31.0 / 37},
		{"accepted precision", metrics.AcceptedPrecision, 34.0 / 37},
		{"FDR", metrics.FDR, 3.0 / 37},
		{"control FPR", metrics.ControlFPR, 0.5},
		{"strict F1", metrics.StrictF1, 2 * (31.0 / 37) * (31.0 / 39) / (31.0/37 + 31.0/39)},
		{"duplicate rate", metrics.DuplicateRate, 4.0 / 46},
		{"low-value rate", metrics.LowValueRate, 2.0 / 46},
		{"inconclusive rate", metrics.InconclusiveRate, 1.0 / 46},
		{"invalid rate", metrics.InvalidRate, 1.0 / 46},
		{"reproducibility", metrics.Reproducibility, 30.0 / 33},
		{"exact severity accuracy", metrics.ExactSeverityAccuracy, 30.0 / 34},
		{"within-one severity accuracy", metrics.WithinOneSeverityAccuracy, 33.0 / 34},
		{"cost per TP", metrics.CostPerTP, 78.0 / 31},
		{"time per TP", metrics.TimePerTP, 310.0 / 31},
		{"correctness recall", metrics.ByDomain[Correctness].StrictRecall, 31.0 / 39},
	} {
		t.Run(tc.name, func(t *testing.T) { metricTestValue(t, tc.got, tc.want) })
	}
	if metrics.ReproNotRun != 1 {
		t.Fatalf("ReproNotRun = %d, want 1", metrics.ReproNotRun)
	}
	if metrics.IssueStates["defect/i00"] != IssueTP {
		t.Fatalf("IssueStates not keyed by Issue.Key(): %+v", metrics.IssueStates)
	}
	run := RunData{Cases: handComputedRun().Cases}
	if got := run.Cost(); got != 78 {
		t.Fatalf("RunData.Cost() = %v, want 78", got)
	}
	if got := run.AgentSeconds(); got != 310 {
		t.Fatalf("RunData.AgentSeconds() = %v, want 310", got)
	}
	if got := run.Tokens(); got == nil || *got != 1200 {
		t.Fatalf("RunData.Tokens() = %v, want 1200", got)
	}
}

// Proof-derived novelty feeds only the reproducibility denominator: applicable proofs
// count as reproduced, non-applicable conformance proofs are excluded, unproven novelty
// stays NotRun, and no novel finding adds to known-issue recall.
func TestComputeRunNovelProofReproDenominatorKeepsRecallUntouched(t *testing.T) {
	result := CaseResult{
		Case: "novel", Issues: []Issue{{Case: "novel", ID: "i1", Domain: Correctness, Severity: High}},
		IssueStates: map[string]IssueState{"i1": IssueTP}, Primary: map[string]string{"i1": "p1"},
		FindingStates: map[string]FindingState{}, ReportedSeverity: map[string]Severity{}, Repro: map[string]ReproOutcome{},
	}
	metricTestFinding(&result, "p1", FindingTPFinding, Reproduced)
	metricTestFinding(&result, "n1", FindingConfirmedNovel, Reproduced)
	metricTestFinding(&result, "n2", FindingConfirmedNovel, NotApplicable)
	metricTestFinding(&result, "n3", FindingConfirmedNovel, "")
	metrics := ComputeRun(RunData{Cases: []CaseResult{result}}, 0.5)
	if metrics.KnownIssues != 1 || metrics.TP != 1 || metrics.FN != 0 {
		t.Fatalf("known issue counts = known:%d TP:%d FN:%d, want novelty to add nothing to recall", metrics.KnownIssues, metrics.TP, metrics.FN)
	}
	if metrics.ConfirmedNovel != 3 || metrics.ReproNotRun != 1 {
		t.Fatalf("novel counts = confirmed:%d notRun:%d, want 3 confirmed and 1 NotRun", metrics.ConfirmedNovel, metrics.ReproNotRun)
	}
	metricTestValue(t, metrics.Reproducibility, 1)
}

func TestComputeRunMacroRecallAveragesPopulatedDomains(t *testing.T) {
	result := CaseResult{
		Case: "domains",
		Issues: []Issue{
			{Case: "domains", ID: "a1", Domain: Security},
			{Case: "domains", ID: "a2", Domain: Security},
			{Case: "domains", ID: "b1", Domain: Data},
		},
		IssueStates: map[string]IssueState{"a1": IssueTP, "a2": IssueFN, "b1": IssueTP},
	}
	metrics := ComputeRun(RunData{Cases: []CaseResult{result}}, 0.5)
	metricTestValue(t, metrics.ByDomain[Security].StrictRecall, 0.5)
	metricTestValue(t, metrics.ByDomain[Data].StrictRecall, 1)
	metricTestValue(t, metrics.StrictRecall, 2.0/3)
	metricTestValue(t, metrics.MacroStrictRecall, 0.75)
}

func TestComputeRunNADenominatorsAndZeroZeroF1(t *testing.T) {
	empty := ComputeRun(RunData{}, 0.5)
	for name, value := range map[string]*float64{
		"strict recall": empty.StrictRecall, "coverage": empty.DetectionCoverage,
		"strict precision": empty.StrictPrecision, "FDR": empty.FDR,
		"control FPR": empty.ControlFPR, "reproducibility": empty.Reproducibility,
		"severity": empty.ExactSeverityAccuracy, "cost per TP": empty.CostPerTP,
		"time per TP": empty.TimePerTP,
	} {
		if value != nil {
			t.Errorf("empty %s = %v, want N/A", name, *value)
		}
	}
	if !empty.Consistent {
		t.Fatal("undefined recall components should not fail consistency")
	}

	zero := ComputeRun(RunData{Cases: []CaseResult{{
		Case: "case", Issues: []Issue{{Case: "case", ID: "missed"}},
		IssueStates:   map[string]IssueState{"missed": IssueFN},
		FindingStates: map[string]FindingState{"fp": FindingFP},
	}}}, 0.5)
	metricTestValue(t, zero.StrictPrecision, 0)
	metricTestValue(t, zero.StrictRecall, 0)
	metricTestValue(t, zero.StrictF1, 0)
	if zero.ControlFPR != nil {
		t.Fatalf("ControlFPR without controls = %v, want N/A", *zero.ControlFPR)
	}
}

// Aggregation keeps an unknown member unknown instead of summing it as zero; all-measured-zero reports a pointer to zero.
func TestRunTokensAggregationKeepsUnknownDistinctFromMeasuredZero(t *testing.T) {
	t.Run("all members measured zero", func(t *testing.T) {
		measuredZero := 0
		run := RunData{Cases: []CaseResult{{Case: "a", Tokens: &measuredZero}, {Case: "b", Tokens: &measuredZero}}}
		got := ComputeRun(run, 0.5).Tokens
		if got == nil || *got != 0 {
			t.Fatalf("Tokens = %v, want pointer to 0", got)
		}
	})
	t.Run("one member unknown", func(t *testing.T) {
		run := RunData{Cases: []CaseResult{{Case: "a", Tokens: metricTestTokens(7)}, {Case: "b"}}}
		got := ComputeRun(run, 0.5).Tokens
		if got != nil {
			t.Fatalf("Tokens = %d, want nil while a case usage is unknown", *got)
		}
	})
}

package eval

import (
	"reflect"
	"testing"
)

func TestClassifyAblationAndNeedsPairwise(t *testing.T) {
	minusTen := -10.0
	tests := []struct {
		name     string
		decision ComparisonDecision
		want     AblationClass
	}{
		{"fail is essential", ComparisonDecision{Verdict: "FAIL"}, AblationEssential},
		{"review contributes", ComparisonDecision{Verdict: "REVIEW"}, AblationContributing},
		{"runtime saving is redundant", ComparisonDecision{Verdict: "PASS", Rows: []Row{{Name: "Runtime", Delta: &minusTen}}}, AblationRedundant},
		{"cost saving is redundant", ComparisonDecision{Verdict: "PASS", Rows: []Row{{Name: "Cost", Delta: &minusTen}}}, AblationRedundant},
		{"pass without saving is neutral", ComparisonDecision{Verdict: "PASS", Rows: []Row{{Name: "Runtime", Delta: floatValue(0)}}}, AblationNeutral},
		{"no decision is unknown", ComparisonDecision{Verdict: "NO_DECISION"}, AblationUnknown},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := ClassifyAblation(tt.decision); got != tt.want {
				t.Fatalf("ClassifyAblation() = %q, want %q", got, tt.want)
			}
		})
	}
	got := NeedsPairwise(map[string]AblationClass{"z": AblationRedundant, "b": AblationRedundant, "a": AblationRedundant, "x": AblationEssential})
	want := [][2]string{{"a", "b"}, {"a", "z"}, {"b", "z"}}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("NeedsPairwise() = %v, want %v", got, want)
	}
}

func floatValue(v float64) *float64 { return &v }

package eval

import "testing"

func TestShouldStopBranches(t *testing.T) {
	issues := repeatedIssues(20, Low, Correctness, IssueTP, IssueTP)
	states := allStates(len(issues), IssueTP)
	tests := []struct {
		name    string
		prepare func(DecisionInput) DecisionInput
		want    bool
	}{
		{"below minimum continues", func(in DecisionInput) DecisionInput { in.Candidate.Runs = in.Candidate.Runs[:1]; return in }, false},
		{"maximum stops", func(in DecisionInput) DecisionInput { in.Manifest.Replicates.KMax = 2; return in }, true},
		{"settled statistical rows stop", func(in DecisionInput) DecisionInput { in.Manifest.Replicates.KMax = 4; return in }, true},
		{"frequency blocker continues until maximum", func(in DecisionInput) DecisionInput {
			in.Manifest.Replicates.KMax = 4
			in.Baseline.Runs[0].Data.Cases[0].Issues[0].Severity = Critical
			in.Baseline.Runs[1].Data.Cases[0].Issues[0].Severity = Critical
			in.Candidate.Runs[0].Data.Cases[0].Issues[0].Severity = Critical
			in.Candidate.Runs[1].Data.Cases[0].Issues[0].Severity = Critical
			for i := range in.Baseline.Runs {
				in.Baseline.Runs[i].Data.Cases[0].IssueStates["i-000"] = IssuePD
			}
			for i := range in.Candidate.Runs {
				in.Candidate.Runs[i].Data.Cases[0].IssueStates["i-000"] = IssueFN
			}
			return in
		}, false},
		{"evidence blocker stops", func(in DecisionInput) DecisionInput {
			in.Manifest.Replicates.KMax = 4
			for i := range in.Candidate.Runs {
				cr := &in.Candidate.Runs[i].Data.Cases[0]
				cr.FindingStates[cr.Primary["i-000"]] = FindingTPFinding
				cr.Repro[cr.Primary["i-000"]] = NotReproduced
			}
			return in
		}, true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			in := decisionTestInput(issues, decisionTestSide{states: states}, decisionTestSide{states: states})
			in = tt.prepare(in)
			got := ShouldStop(in)
			if got.Stop != tt.want {
				t.Fatalf("ShouldStop() = %+v, want Stop=%v", got, tt.want)
			}
		})
	}

	undefined := decisionTestInput(nil, decisionTestSide{}, decisionTestSide{})
	undefined.Manifest.Replicates.KMax = 4
	if got := ShouldStop(undefined); got.Stop {
		t.Fatalf("undefined statistical intervals must continue: %+v", got)
	}
}

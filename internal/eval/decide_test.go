package eval

import (
	"encoding/json"
	"fmt"
	"strings"
	"testing"
)

type decisionTestIssue struct {
	issue Issue
	base  IssueState
	cand  IssueState
}

type decisionTestSide struct {
	states    []IssueState
	falsePos  int
	controlFP int
	runtime   float64
	cost      float64
}

func decisionTestInput(issues []decisionTestIssue, base, candidate decisionTestSide) DecisionInput {
	manifest := Manifest{
		Benchmark: "CORE", Version: "1.0", ManifestSHA256: "manifest",
		Domains: []Domain{Security, Correctness, Data}, CriticalDomains: []Domain{Security, Data},
		MetricConfig: MetricConfig{WeightedRecallW: .5, CILevel: .9, EarlyStopCILevel: .95, BootstrapResamples: 400, BootstrapSeed: 1729},
		Replicates:   Replicates{KMin: 2, KTarget: 2, KMax: 2},
	}
	policy := Policy{
		Name: "policy-1", PolicySHA256: "policy", SignificanceRule: true,
		Thresholds: PolicyThresholds{
			RecallDropPassPP: 2, RecallDropReviewPP: 5,
			PrecisionDropPassPP: 3, PrecisionDropReviewPP: 5,
			F1DropPassPP: 2, F1DropReviewPP: 5,
			ReproducibilityDropPassPP: 5, ReproducibilityDropReviewPP: 10,
			FDRRisePassPP: 3, FDRRiseReviewPP: 5,
			RuntimeRisePassPct: 20, RuntimeRiseReviewPct: 50,
			CostRisePassPct: 20, CostRiseReviewPct: 50,
			NewCriticalFNFailCount: 1, HighFNReviewCount: 1, HighFNFailCount: 2,
			CriticalDomainIssuesLowerReview: 1, CriticalDomainIssuesLowerFail: 2,
			ControlFPRRiseReview: 1, ControlFPRRiseFail: 2, ExtremeEfficiencyRisePct: 200,
		},
		Tradeoffs: Tradeoffs{T1RecallGainPP: 5, T1RecallCILowerAboveZero: true,
			T1AllowsCriticalHighIssuePromotion: true, T1RequiresNoCriticalHighIssueLower: true,
			T2F1GainPP: 3, T2F1CILowerAboveZero: true},
	}
	return DecisionInput{
		Baseline:  decisionTestSideInput("baseline", "h-baseline", issues, base, 2),
		Candidate: decisionTestSideInput("candidate", "h-candidate", issues, candidate, 2),
		Manifest:  manifest, Policy: policy,
	}
}

func decisionTestSideInput(label, harness string, issues []decisionTestIssue, side decisionTestSide, k int) Side {
	if side.runtime == 0 {
		side.runtime = 100
	}
	if side.cost == 0 {
		side.cost = 100
	}
	runs := make([]RunRecord, 0, k)
	for runIndex := 0; runIndex < k; runIndex++ {
		byCase := make(map[string]*CaseResult)
		for i, item := range issues {
			caseID := item.issue.Case
			if caseID == "" {
				caseID = "case"
			}
			result := byCase[caseID]
			if result == nil {
				result = &CaseResult{Case: caseID, IssueStates: map[string]IssueState{}, Primary: map[string]string{}, FindingStates: map[string]FindingState{}, ReportedSeverity: map[string]Severity{}, Repro: map[string]ReproOutcome{}}
				byCase[caseID] = result
			}
			issue := item.issue
			issue.Case = caseID
			result.Issues = append(result.Issues, issue)
			state := item.base
			if label == "candidate" && i < len(side.states) {
				state = side.states[i]
			} else if label == "baseline" && i < len(side.states) {
				state = side.states[i]
			}
			result.IssueStates[issue.ID] = state
			if state == IssueTP || state == IssuePD {
				finding := fmt.Sprintf("%s/%s", caseID, issue.ID)
				result.Primary[issue.ID] = finding
				result.FindingStates[finding] = FindingTPFinding
				result.Repro[finding] = Reproduced
				if state == IssuePD {
					result.FindingStates[finding] = FindingPDFinding
					result.Repro[finding] = NotApplicable
				}
			}
		}
		caseIDs := make([]string, 0, len(byCase))
		for caseID := range byCase {
			caseIDs = append(caseIDs, caseID)
		}
		// Keep case order stable for deterministic bootstrap input.
		sortStrings(caseIDs)
		cases := make([]CaseResult, 0, len(caseIDs)+1)
		for _, caseID := range caseIDs {
			cases = append(cases, *byCase[caseID])
		}
		if len(cases) > 0 {
			cases[0].AgentSeconds = side.runtime
			cases[0].CostUSD = side.cost
			for n := 0; n < side.falsePos; n++ {
				id := fmt.Sprintf("fp-%d", n)
				cases[0].FindingStates[id] = FindingFP
			}
		}
		control := CaseResult{Case: "control", Control: true, IssueStates: map[string]IssueState{}, Primary: map[string]string{}, FindingStates: map[string]FindingState{}, ReportedSeverity: map[string]Severity{}, Repro: map[string]ReproOutcome{}}
		for n := 0; n < side.controlFP; n++ {
			control.FindingStates[fmt.Sprintf("control-fp-%d", n)] = FindingFP
		}
		cases = append(cases, control)
		data := RunData{ID: fmt.Sprintf("run-%d", runIndex), Cases: cases}
		runs = append(runs, RunRecord{Data: data, State: RunCompleted, ManifestSHA256: "manifest", PolicySHA256: "policy", HarnessID: harness, Model: "model", Runner: "runner", Environment: "env"})
	}
	return Side{Label: label, HarnessID: harness, Runs: runs}
}

func sortStrings(values []string) {
	for i := 0; i < len(values); i++ {
		for j := i + 1; j < len(values); j++ {
			if values[j] < values[i] {
				values[i], values[j] = values[j], values[i]
			}
		}
	}
}

func repeatedIssues(count int, severity Severity, domain Domain, base, candidate IssueState) []decisionTestIssue {
	issues := make([]decisionTestIssue, count)
	for i := range issues {
		issues[i] = decisionTestIssue{issue: Issue{Case: "case", ID: fmt.Sprintf("i-%03d", i), Severity: severity, Domain: domain}, base: base, cand: candidate}
	}
	return issues
}

func statesFor(issues []decisionTestIssue, base, candidate IssueState) ([]IssueState, []IssueState) {
	baseline, candidateStates := make([]IssueState, len(issues)), make([]IssueState, len(issues))
	for i := range issues {
		baseline[i], candidateStates[i] = base, candidate
	}
	return baseline, candidateStates
}

func TestDecideWorkedExamplesAndEfficiencyBlockers(t *testing.T) {
	tests := []struct {
		name        string
		issues      int
		baseTP      int
		candidateTP int
		runtimePct  float64
		costPct     float64
		want        string
		category    string
		wantTag     string
	}{
		{name: "six point recall gain and eight percent runtime rise", issues: 50, baseTP: 42, candidateTP: 45, runtimePct: 8, costPct: 8, want: "PASS"},
		{name: "T1 benefit only downgrades an efficiency failure", issues: 50, baseTP: 42, candidateTP: 45, runtimePct: 110, want: "REVIEW", wantTag: "tradeoff:T1"},
		{name: "small recall benefit cannot offset runtime and cost regressions", issues: 100, baseTP: 50, candidateTP: 51, runtimePct: 110, costPct: 140, want: "FAIL", category: "EFFICIENCY"},
		{name: "extreme cost rise without benefit triggers H8", issues: 100, baseTP: 50, candidateTP: 50, runtimePct: 0, costPct: 240, want: "FAIL", category: "EFFICIENCY"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			issues := repeatedIssues(tt.issues, Low, Correctness, IssueFN, IssueFN)
			baseStates, candidateStates := statesFor(issues, IssueFN, IssueFN)
			for i := 0; i < tt.baseTP; i++ {
				baseStates[i] = IssueTP
			}
			for i := 0; i < tt.candidateTP; i++ {
				candidateStates[i] = IssueTP
			}
			base := decisionTestSide{states: baseStates, runtime: 100, cost: 100}
			candidate := decisionTestSide{states: candidateStates, runtime: 100 * (1 + tt.runtimePct/100), cost: 100 * (1 + tt.costPct/100)}
			got := Decide(decisionTestInput(issues, base, candidate))
			if got.Verdict != tt.want || got.Category != tt.category {
				t.Fatalf("Decide() = %s/%s, want %s/%s; blockers=%+v rows=%+v", got.Verdict, got.Category, tt.want, tt.category, got.Blockers, got.Rows)
			}
			if tt.costPct == 240 && !hasBlocker(got, "H8") {
				t.Fatalf("expected H8 blocker, got %+v", got.Blockers)
			}
			if tt.wantTag != "" && !hasTag(got, tt.wantTag) {
				t.Fatalf("expected tag %q, got %+v", tt.wantTag, got.Rows)
			}
		})
	}
}

func TestDecideCriticalDomainAndNewCriticalFN(t *testing.T) {
	issues := repeatedIssues(100, Low, Correctness, IssueFN, IssueFN)
	for i := range issues {
		if i < 10 {
			issues[i].issue.Domain = Security
		}
	}
	baseStates, candidateStates := statesFor(issues, IssueFN, IssueFN)
	for i := 0; i < 50; i++ {
		baseStates[i] = IssueTP
		candidateStates[i] = IssueTP
	}
	for i := 0; i < 10; i++ {
		baseStates[i] = IssueTP
	}
	for i := 0; i < 8; i++ {
		candidateStates[i] = IssueTP
	}
	candidateStates[8], candidateStates[9] = IssueFN, IssueFN
	for i := 50; i < 54; i++ {
		candidateStates[i] = IssueTP
	}
	in := decisionTestInput(issues, decisionTestSide{states: baseStates}, decisionTestSide{states: candidateStates})
	got := Decide(in)
	if got.Verdict != "FAIL" || !hasBlocker(got, "H3") {
		t.Fatalf("security drop hidden by global gain: %+v", got)
	}

	critical := []decisionTestIssue{{issue: Issue{Case: "case", ID: "critical", Domain: Security, Severity: Critical}, base: IssuePD, cand: IssueFN}}
	in = decisionTestInput(critical, decisionTestSide{states: []IssueState{IssuePD}}, decisionTestSide{states: []IssueState{IssueFN}})
	in.Manifest.Replicates.KMax = 4
	got = Decide(in)
	if got.Verdict != "FAIL" || !hasBlocker(got, "H1") || !got.Provisional {
		t.Fatalf("new critical FN = %+v", got)
	}

	critical[0].base, critical[0].cand = IssueTP, IssueFN
	in = decisionTestInput(critical, decisionTestSide{states: []IssueState{IssueTP}}, decisionTestSide{states: []IssueState{IssueFN}})
	got = Decide(in)
	if !hasBlocker(got, "H1") || !hasBlocker(got, "H2") {
		t.Fatalf("critical TP-to-FN must produce separate H1 and H2 blockers: %+v", got.Blockers)
	}
}

func TestDecideSignificanceTradeoffAndLeakage(t *testing.T) {
	issues := repeatedIssues(100, Low, Correctness, IssueFN, IssueFN)
	baseStates, candidateStates := statesFor(issues, IssueFN, IssueFN)
	for i := 0; i < 50; i++ {
		baseStates[i] = IssueTP
	}
	for i := 0; i < 44; i++ {
		candidateStates[i] = IssueTP
	}
	// Spread the regression across independent case clusters so the paired interval includes zero.
	for i := range issues {
		issues[i].issue.Case = fmt.Sprintf("case-%d", i/10)
	}
	in := decisionTestInput(issues, decisionTestSide{states: baseStates}, decisionTestSide{states: candidateStates})
	got := Decide(in)
	if got.Verdict != "REVIEW" || !hasTag(got, "not_significant") {
		t.Fatalf("insignificant statistical FAIL = %+v", got)
	}

	issues = repeatedIssues(100, Low, Correctness, IssueFN, IssueFN)
	baseStates, candidateStates = statesFor(issues, IssueFN, IssueFN)
	for i := 0; i < 40; i++ {
		baseStates[i] = IssueTP
	}
	for i := 0; i < 90; i++ {
		candidateStates[i] = IssueTP
	}
	in = decisionTestInput(issues, decisionTestSide{states: baseStates}, decisionTestSide{states: candidateStates, falsePos: 90})
	got = Decide(in)
	if got.Verdict != "REVIEW" || !hasTag(got, "tradeoff:T2") {
		t.Fatalf("T2 precision/FDR downgrade = %+v", got)
	}

	in = decisionTestInput(repeatedIssues(20, Low, Correctness, IssueTP, IssueTP), decisionTestSide{states: allStates(20, IssueTP)}, decisionTestSide{states: allStates(20, IssueTP)})
	in.Candidate.Runs[0].Leaks = []Leak{{Kind: "canary", Line: 1}}
	got = Decide(in)
	if got.Verdict != "FAIL" || got.Category != "LEAKAGE" || !hasBlocker(got, "H6") {
		t.Fatalf("leakage must dominate: %+v", got)
	}
}

func TestDecideInstrumentCompletenessAndDeterminism(t *testing.T) {
	issues := repeatedIssues(20, Low, Correctness, IssueTP, IssueTP)
	states := allStates(20, IssueTP)
	in := decisionTestInput(issues, decisionTestSide{states: states}, decisionTestSide{states: states})
	in.Candidate.Runs[0].ManifestSHA256 = "wrong"
	got := Decide(in)
	if got.Verdict != "FAIL" || got.Category != "INSTRUMENT" || !hasBlocker(got, "H5") {
		t.Fatalf("manifest mismatch = %+v", got)
	}

	in = decisionTestInput(issues, decisionTestSide{states: states}, decisionTestSide{states: states})
	in.Candidate.Runs[0].InvariantViolations = []string{"I1 terminal-state mismatch"}
	got = Decide(in)
	if got.Verdict != "FAIL" || got.Category != "INSTRUMENT" || !hasBlocker(got, "H5") {
		t.Fatalf("invariant violation = %+v", got)
	}

	in = decisionTestInput(issues, decisionTestSide{states: states}, decisionTestSide{states: states})
	in.Candidate.Runs = in.Candidate.Runs[:1]
	got = Decide(in)
	if got.Verdict != "NO_DECISION" {
		t.Fatalf("insufficient runs = %+v", got)
	}
	in.Candidate.Runs[0].State = RunAborted
	in.Candidate.Runs[0].AbortReason = "budget"
	got = Decide(in)
	if got.Verdict != "FAIL" || got.Category != "COMPLETENESS" || !hasBlocker(got, "H7") {
		t.Fatalf("budget abort = %+v", got)
	}

	in = decisionTestInput(issues, decisionTestSide{states: states}, decisionTestSide{states: states})
	in.Candidate.Runs[0].Environment = "different-environment"
	got = Decide(in)
	if got.Verdict != "NO_DECISION" || len(got.Reasons) == 0 || !strings.Contains(got.Reasons[0], "I12") {
		t.Fatalf("I12 mismatch = %+v", got)
	}

	in = decisionTestInput(issues, decisionTestSide{states: states}, decisionTestSide{states: states, controlFP: 1})
	for i := range in.Candidate.Runs {
		in.Candidate.Runs[i].Data.Cases = append(in.Candidate.Runs[i].Data.Cases, CaseResult{Control: true, FindingStates: map[string]FindingState{"fp": FindingFP}})
	}
	got = Decide(in)
	if got.Verdict != "FAIL" || got.Category != "REGRESSION" {
		t.Fatalf("control FPR increase = %+v", got)
	}

	in = decisionTestInput(issues, decisionTestSide{states: states}, decisionTestSide{states: states})
	first, err := json.Marshal(Decide(in))
	if err != nil {
		t.Fatal(err)
	}
	second, err := json.Marshal(Decide(in))
	if err != nil {
		t.Fatal(err)
	}
	if string(first) != string(second) {
		t.Fatalf("nondeterministic JSON:\n%s\n%s", first, second)
	}
}

func TestDecideOutcomeIsSerializable(t *testing.T) {
	data, err := json.Marshal(CaseResult{Outcome: "budget_exhausted"})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(data), `"Outcome":"budget_exhausted"`) {
		t.Fatalf("Outcome omitted from report serialization: %s", data)
	}
}

func allStates(n int, state IssueState) []IssueState {
	out := make([]IssueState, n)
	for i := range out {
		out[i] = state
	}
	return out
}
func hasBlocker(d ComparisonDecision, id string) bool {
	for _, b := range d.Blockers {
		if b.ID == id {
			return true
		}
	}
	return false
}
func hasTag(d ComparisonDecision, tag string) bool {
	for _, row := range d.Rows {
		for _, got := range row.Tags {
			if got == tag {
				return true
			}
		}
	}
	return false
}

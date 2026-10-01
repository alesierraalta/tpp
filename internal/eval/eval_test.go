package eval

import (
	"bytes"
	"encoding/json"
	"strings"
	"testing"
)

func TestFactsLevel(t *testing.T) {
	tests := []struct {
		name  string
		facts Facts
		want  MatchLevel
	}{
		{name: "no location", facts: Facts{C1: FactTrue, C2: FactFalse, C3: FactTrue}, want: LevelNone},
		{name: "no type or mechanism", facts: Facts{C2: FactTrue, C4Cited: FactTrue, C4Shows: FactTrue, C5: FactTrue}, want: LevelNone},
		{name: "full with reproduction", facts: Facts{C1: FactTrue, C2: FactTrue, C3: FactTrue, C4Cited: FactTrue, C4Shows: FactTrue, C5: FactTrue}, want: LevelFull},
		{name: "full when reproduction not applicable", facts: Facts{C1: FactTrue, C2: FactTrue, C3: FactTrue, C4Cited: FactTrue, C4Shows: FactTrue, C5: FactNA}, want: LevelFull},
		{name: "partial without type", facts: Facts{C1: FactUnknown, C2: FactTrue, C3: FactTrue, C4Cited: FactTrue, C4Shows: FactTrue, C5: FactTrue}, want: LevelPartial},
		{name: "partial without citation", facts: Facts{C1: FactTrue, C2: FactTrue, C3: FactTrue, C4Cited: FactFalse, C4Shows: FactTrue, C5: FactTrue}, want: LevelPartial},
		{name: "partial without evidence showing failure", facts: Facts{C1: FactTrue, C2: FactTrue, C3: FactTrue, C4Cited: FactTrue, C4Shows: FactFalse, C5: FactTrue}, want: LevelPartial},
		{name: "partial with unknown reproduction", facts: Facts{C1: FactTrue, C2: FactTrue, C3: FactTrue, C4Cited: FactTrue, C4Shows: FactTrue, C5: FactUnknown}, want: LevelPartial},
		{name: "unknown type is false", facts: Facts{C1: FactUnknown, C2: FactTrue, C3: FactFalse}, want: LevelNone},
		{name: "generic security concern is none", facts: Facts{C1: FactFalse, C2: FactFalse, C3: FactFalse, C4Cited: FactUnknown, C4Shows: FactUnknown, C5: FactUnknown}, want: LevelNone},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := tt.facts.Level(); got != tt.want {
				t.Fatalf("Level() = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestSeverityFromReported(t *testing.T) {
	tests := []struct {
		text string
		want Severity
	}{
		{"critical", Critical}, {"high", High}, {"medium", Medium}, {"low", Low}, {"info", Info},
		{"money moved/lost", Critical}, {"data destroyed/corrupted", Critical},
		{"permission boundary crossed", Critical}, {"permission or tenant boundary crossed", Critical}, {"permission/tenant boundary crossed", Critical}, {"silently wrong answer on supported input", High},
		{"visible error", Medium}, {"cosmetic only", Info}, {"degraded diagnostics", SeverityUnknown},
		{"unclear impact", SeverityUnknown},
	}
	for _, tt := range tests {
		t.Run(tt.text, func(t *testing.T) {
			if got := SeverityFromReported(tt.text); got != tt.want {
				t.Fatalf("SeverityFromReported(%q) = %v, want %v", tt.text, got, tt.want)
			}
		})
	}
}

func TestSeverityJSONAndDomains(t *testing.T) {
	for _, severity := range []Severity{SeverityUnknown, Info, Low, Medium, High, Critical} {
		raw, err := json.Marshal(severity)
		if err != nil {
			t.Fatal(err)
		}
		var decoded Severity
		if err := json.Unmarshal(raw, &decoded); err != nil || decoded != severity {
			t.Fatalf("round trip %v = %s, %v", severity, raw, err)
		}
	}
	want := []Domain{Security, Correctness, Concurrency, Performance, Resilience, Data, APICompat, AILLM, Architecture}
	if got := AllDomains(); !equalDomains(got, want) {
		t.Fatalf("AllDomains() = %v, want %v", got, want)
	}
	for _, domain := range want {
		got, ok := ParseDomain(string(domain))
		if !ok || got != domain {
			t.Errorf("ParseDomain(%q) = %q, %v", domain, got, ok)
		}
	}
	if _, ok := ParseDomain("unknown"); ok {
		t.Fatal("ParseDomain accepted an unknown domain")
	}
}

func TestStateTransitionTables(t *testing.T) {
	tests := []struct {
		name string
		ok   bool
	}{
		{"issue initial", CanTransitionIssue(IssuePending, IssueUnderEvaluation)},
		{"issue terminal reopen", CanTransitionIssue(IssueTP, IssueUnderEvaluation)},
		{"issue illegal", !CanTransitionIssue(IssuePending, IssueTP)},
		{"finding admit", CanTransitionFinding(FindingRaw, FindingPendingAdjudication)},
		{"finding unmatched", CanTransitionFinding(FindingUnmatched, FindingNovelCandidate)},
		{"finding reopen", CanTransitionFinding(FindingFP, FindingPendingAdjudication)},
		{"finding illegal", !CanTransitionFinding(FindingRaw, FindingMatched)},
		{"case run retry", CanTransitionCaseRun(CaseRunFailedInfrastructure, CaseRunRunning)},
		{"case run illegal", !CanTransitionCaseRun(CaseRunCreated, CaseRunClosed)},
		{"run invalidate", CanTransitionRun(RunRunning, RunInvalid)},
		{"run terminal", !CanTransitionRun(RunCompleted, RunRunning)},
		{"unknown run state", !CanTransitionRun("", RunInvalid)},
		{"terminal run cannot invalidate", !CanTransitionRun(RunCompleted, RunInvalid)},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if !tt.ok {
				t.Fatal("transition predicate did not match the expected table edge")
			}
		})
	}
}

func TestLogHashChainPersistenceAndTerminalGuard(t *testing.T) {
	var log Log
	admit := Event{Entity: "finding", ID: "f1", Kind: "admit", PreviousState: string(FindingRaw), NewState: string(FindingPendingAdjudication)}
	if _, err := log.Append(admit); err != nil {
		t.Fatal(err)
	}
	if _, err := log.Append(Event{Entity: "finding", ID: "f1", Kind: "decide", PreviousState: string(FindingPendingAdjudication), NewState: string(FindingMatched)}); err != nil {
		t.Fatal(err)
	}
	if _, err := log.Append(Event{Entity: "finding", ID: "f1", Kind: "derive", PreviousState: string(FindingMatched), NewState: string(FindingTPFinding)}); err != nil {
		t.Fatal(err)
	}
	if _, err := log.Append(Event{Entity: "finding", ID: "f1", Kind: "derive", PreviousState: string(FindingTPFinding), NewState: string(FindingPDFinding)}); err == nil {
		t.Fatal("terminal state changed without reopen")
	}
	if _, err := log.Append(Event{Entity: EntityFinding, ID: "f1", Kind: EventDecide, PreviousState: string(FindingTPFinding), NewState: string(FindingTPFinding)}); err != nil {
		t.Fatalf("state-preserving decide: %v", err)
	}
	if err := log.Verify(); err != nil {
		t.Fatal(err)
	}
	var encoded bytes.Buffer
	if err := log.WriteLog(&encoded); err != nil {
		t.Fatal(err)
	}
	loaded, err := ReadLog(&encoded)
	if err != nil || len(loaded.Events) != 4 {
		t.Fatalf("ReadLog() = %d events, %v", len(loaded.Events), err)
	}
	loaded.Events[1].Reason = "tampered"
	if err := loaded.Verify(); err == nil {
		t.Fatal("Verify accepted a modified event")
	}
}

func TestLogRejectsIllegalTransitionsForEveryMachine(t *testing.T) {
	for _, tc := range []struct {
		entity string
		id     string
		from   string
		to     string
	}{
		{"issue", "i", string(IssuePending), string(IssueTP)},
		{"finding", "f", string(FindingRaw), string(FindingTPFinding)},
		{"case_run", "c", string(CaseRunCreated), string(CaseRunClosed)},
		{"run", "r", string(RunCreated), string(RunCompleted)},
	} {
		t.Run(tc.entity, func(t *testing.T) {
			var log Log
			if _, err := log.Append(Event{Entity: tc.entity, ID: tc.id, Kind: "transition", PreviousState: tc.from, NewState: tc.to}); err == nil {
				t.Fatalf("accepted illegal %s transition %s -> %s", tc.entity, tc.from, tc.to)
			}
		})
	}
}

func TestCaseRunPrimarySelectionAndIssueOutcomes(t *testing.T) {
	cr := CaseRun{
		Case:   "case",
		Issues: []Issue{{Case: "case", ID: "D1", Domain: Correctness, Severity: High}},
		Findings: []Finding{
			{ID: "f1", Row: 4, Computed: map[string]ComputedFacts{"D1": {C2: FactTrue, C4Cited: FactTrue, C5: FactTrue}}},
			{ID: "f2", Row: 2, Computed: map[string]ComputedFacts{"D1": {C2: FactTrue, C4Cited: FactTrue, C5: FactTrue}}},
		},
	}
	if err := cr.Admit("t1"); err != nil {
		t.Fatal(err)
	}
	if got := cr.IssueState("D1"); got != IssueUnderEvaluation {
		t.Fatalf("issue before decisions = %s", got)
	}
	for _, id := range []string{"f1", "f2"} {
		if err := cr.Decide(Decision{FindingID: id, CandidateIssue: "D1", C1: FactTrue, C3: FactTrue, C4Shows: FactTrue, By: "reviewer", TS: "t2", Reason: "verified"}); err != nil {
			t.Fatal(err)
		}
	}
	if got := cr.IssueState("D1"); got != IssueUnderEvaluation {
		t.Fatalf("issue changed before Close = %s", got)
	}
	if err := cr.Close("t3"); err != nil {
		t.Fatal(err)
	}
	if got := cr.Primary("D1"); got != "f2" {
		t.Fatalf("Primary() = %q, want lowest row f2", got)
	}
	if cr.FindingState("f2") != FindingTPFinding || cr.FindingState("f1") != FindingDuplicate {
		t.Fatalf("finding states: f1=%s f2=%s", cr.FindingState("f1"), cr.FindingState("f2"))
	}
	if cr.IssueState("D1") != IssueTP {
		t.Fatalf("duplicate changed issue state: %s", cr.IssueState("D1"))
	}
	if got := CheckCaseRun(&cr); len(got) != 0 {
		t.Fatalf("CheckCaseRun() = %+v", got)
	}
}

func TestEquivalentDuplicatePointsToRepresentativeWithoutAffectingIssue(t *testing.T) {
	cr := CaseRun{
		Case:   "case",
		Issues: []Issue{{Case: "case", ID: "D1"}},
		Findings: []Finding{
			{ID: "primary", Row: 1, Computed: map[string]ComputedFacts{"D1": {C2: FactTrue, C4Cited: FactTrue, C5: FactTrue}}},
			{ID: "duplicate", Row: 2, Computed: map[string]ComputedFacts{"D1": {C2: FactFalse}}},
		},
	}
	if err := cr.Admit("1"); err != nil {
		t.Fatal(err)
	}
	decisions := []Decision{
		{FindingID: "primary", CandidateIssue: "D1", C1: FactTrue, C3: FactTrue, C4Shows: FactTrue, By: "reviewer", TS: "2", Reason: "full match"},
		{FindingID: "duplicate", CandidateIssue: "D1", EquivalentTo: "primary", By: "reviewer", TS: "2", Reason: "same observation"},
	}
	for _, decision := range decisions {
		if err := cr.Decide(decision); err != nil {
			t.Fatal(err)
		}
	}
	if err := cr.Close("3"); err != nil {
		t.Fatal(err)
	}
	if cr.FindingState("duplicate") != FindingDuplicate || cr.Primary("D1") != "primary" || cr.IssueState("D1") != IssueTP {
		t.Fatalf("duplicate=%s primary=%s issue=%s", cr.FindingState("duplicate"), cr.Primary("D1"), cr.IssueState("D1"))
	}
	if got := CheckCaseRun(&cr); len(got) != 0 {
		t.Fatalf("CheckCaseRun() = %+v", got)
	}
}

func TestCaseRunPartialAndNoneCloseOutcomes(t *testing.T) {
	tests := []struct {
		name          string
		facts         ComputedFacts
		c1, c3, shows Fact
		unmatched     FindingState
		wantFinding   FindingState
		wantIssue     IssueState
		wantLevel     MatchLevel
	}{
		{name: "partial", facts: ComputedFacts{C2: FactTrue, C4Cited: FactTrue, C5: FactTrue}, c1: FactTrue, c3: FactFalse, shows: FactTrue, wantFinding: FindingPDFinding, wantIssue: IssuePD, wantLevel: LevelPartial},
		{name: "none", facts: ComputedFacts{C2: FactFalse, C4Cited: FactFalse, C5: FactUnknown}, c1: FactFalse, c3: FactFalse, shows: FactFalse, unmatched: FindingFP, wantFinding: FindingFP, wantIssue: IssueFN, wantLevel: LevelNone},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cr := CaseRun{Case: "case", Issues: []Issue{{Case: "case", ID: "D1"}}, Findings: []Finding{{ID: "f", Row: 1, Computed: map[string]ComputedFacts{"D1": tt.facts}}}}
			if err := cr.Admit("1"); err != nil {
				t.Fatal(err)
			}
			decision := Decision{FindingID: "f", CandidateIssue: "D1", C1: tt.c1, C3: tt.c3, C4Shows: tt.shows, UnmatchedOutcome: tt.unmatched, By: "reviewer", TS: "2", Reason: "checked"}
			if err := cr.Decide(decision); err != nil {
				t.Fatal(err)
			}
			if cr.IssueState("D1") == IssueFN {
				t.Fatal("FN assigned before Close")
			}
			if err := cr.Close("3"); err != nil {
				t.Fatal(err)
			}
			if got := cr.FindingState("f"); got != tt.wantFinding {
				t.Errorf("finding state = %s, want %s", got, tt.wantFinding)
			}
			if got := cr.IssueState("D1"); got != tt.wantIssue {
				t.Errorf("issue state = %s, want %s", got, tt.wantIssue)
			}
			if got := cr.MatchLevel("f"); got != tt.wantLevel {
				t.Errorf("MatchLevel() = %v, want %v", got, tt.wantLevel)
			}
		})
	}
}

func TestCaseRunRefusesEquivalentChainsAndCycles(t *testing.T) {
	for _, tc := range []struct {
		name  string
		equiv map[string]string
	}{
		{name: "chain", equiv: map[string]string{"f1": "f2", "f2": "f3"}},
		{name: "cycle", equiv: map[string]string{"f1": "f2", "f2": "f1"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			cr := CaseRun{Case: "case", Issues: []Issue{{Case: "case", ID: "D1"}}}
			for i, id := range []string{"f1", "f2", "f3"} {
				cr.Findings = append(cr.Findings, Finding{ID: id, Row: i + 1, Computed: map[string]ComputedFacts{"D1": {C2: FactTrue, C4Cited: FactTrue, C5: FactTrue}}})
			}
			if err := cr.Admit("1"); err != nil {
				t.Fatal(err)
			}
			for _, finding := range cr.Findings {
				if err := cr.Decide(Decision{FindingID: finding.ID, CandidateIssue: "D1", C1: FactTrue, C3: FactTrue, C4Shows: FactTrue, EquivalentTo: tc.equiv[finding.ID], By: "reviewer", TS: "2", Reason: "checked"}); err != nil {
					t.Fatal(err)
				}
			}
			if err := cr.Close("3"); err == nil {
				t.Fatal("Close accepted an equivalent_to chain or cycle")
			}
		})
	}
}

func TestCaseRunReopenAndReclose(t *testing.T) {
	cr := CaseRun{Case: "case", Issues: []Issue{{Case: "case", ID: "D1"}}, Findings: []Finding{{ID: "f", Row: 1, Computed: map[string]ComputedFacts{"D1": {C2: FactTrue, C4Cited: FactTrue, C5: FactTrue}}}}}
	if err := cr.Admit("1"); err != nil {
		t.Fatal(err)
	}
	if err := cr.Decide(Decision{FindingID: "f", CandidateIssue: "D1", C1: FactTrue, C3: FactTrue, C4Shows: FactTrue, By: "reviewer", TS: "2", Reason: "full"}); err != nil {
		t.Fatal(err)
	}
	if err := cr.Close("3"); err != nil {
		t.Fatal(err)
	}
	if err := cr.Reopen("f", "new evidence", "reviewer2", "4"); err != nil {
		t.Fatal(err)
	}
	var reopened Event
	for i := len(cr.Log.Events) - 1; i >= 0; i-- {
		if cr.Log.Events[i].Entity == EntityFinding && cr.Log.Events[i].ID == "f" && cr.Log.Events[i].Kind == EventReopen {
			reopened = cr.Log.Events[i]
			break
		}
	}
	if reopened.PreviousState != string(FindingTPFinding) || reopened.NewState != string(FindingPendingAdjudication) || reopened.Reason != "new evidence" || reopened.Adjudicator != "reviewer2" {
		t.Fatalf("reopen event = %+v", reopened)
	}
	if err := cr.Decide(Decision{FindingID: "f", CandidateIssue: "D1", C1: FactTrue, C3: FactFalse, C4Shows: FactTrue, By: "reviewer2", TS: "5", Reason: "revised to partial"}); err != nil {
		t.Fatal(err)
	}
	if err := cr.Close("6"); err != nil {
		t.Fatal(err)
	}
	if cr.FindingState("f") != FindingPDFinding || cr.IssueState("D1") != IssuePD || cr.MatchLevel("f") != LevelPartial {
		t.Fatalf("after reclose: finding=%s issue=%s level=%v", cr.FindingState("f"), cr.IssueState("D1"), cr.MatchLevel("f"))
	}
}

func TestDecisionValidate(t *testing.T) {
	if err := (Decision{By: "reviewer", Reason: "checked", UnmatchedOutcome: FindingTPFinding}).Validate(); err == nil || !strings.Contains(err.Error(), "unmatched") {
		t.Fatalf("Validate() error = %v, want invalid unmatched outcome", err)
	}
	if err := (Decision{By: "reviewer", Reason: "checked", UnmatchedOutcome: FindingFP}).Validate(); err != nil {
		t.Fatal(err)
	}
}

func equalDomains(a, b []Domain) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

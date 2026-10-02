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

var (
	confirmDigestA = "sha256:" + strings.Repeat("a", 64)
	confirmDigestB = "sha256:" + strings.Repeat("b", 64)
)

// validConfirmPayload builds a semantically valid confirmation body bound to an issue key.
func validConfirmPayload(issueKey string) json.RawMessage {
	return json.RawMessage(`{"issue_id":"` + issueKey + `","outcome":"REPRODUCED","artifact_digest":"` + confirmDigestA + `","attempts":1}`)
}

// craftConfirm injects an event with a correctly recomputed hash, bypassing Append's
// write-time validation so verification can be probed on its own.
func craftConfirm(t *testing.T, log *Log, event Event) {
	t.Helper()
	event.Seq = len(log.Events) + 1
	event.PrevHash = ""
	if len(log.Events) > 0 {
		event.PrevHash = log.Events[len(log.Events)-1].Hash
	}
	event.Hash = ""
	if hash, err := hashEvent(event); err == nil {
		event.Hash = hash // an unhashable payload is rejected before hash verification
	}
	log.Events = append(log.Events, event)
}

// A state-preserving confirmation extends the hash chain without changing the Issue state.
func TestEventConfirmAppendsToHashChain(t *testing.T) {
	var log Log
	if _, err := log.Append(Event{Entity: EntityIssue, ID: "case/i1", Kind: EventAdmit, PreviousState: string(IssuePending), NewState: string(IssueUnderEvaluation), TS: "t1"}); err != nil {
		t.Fatal(err)
	}
	payload := validConfirmPayload("case/i1")
	if _, err := log.Append(Event{Entity: EntityIssue, ID: "case/i1", Kind: EventConfirm, PreviousState: string(IssueUnderEvaluation), NewState: string(IssueUnderEvaluation), TS: "t2", Adjudicator: ConfirmAdjudicator, Payload: payload}); err != nil {
		t.Fatalf("state-preserving confirmation must append: %v", err)
	}
	if err := log.Verify(); err != nil {
		t.Fatalf("Verify() after confirmation: %v", err)
	}
	if got := log.States()[EntityIssue+":case/i1"]; got != string(IssueUnderEvaluation) {
		t.Fatalf("confirmation changed Issue state to %q", got)
	}
	var encoded bytes.Buffer
	if err := log.WriteLog(&encoded); err != nil {
		t.Fatal(err)
	}
	loaded, err := ReadLog(&encoded)
	if err != nil {
		t.Fatal(err)
	}
	loaded.Events[1].Payload = json.RawMessage(strings.Replace(string(payload), "REPRODUCED", "NOT_REPRODUCED", 1))
	if err := loaded.Verify(); err == nil {
		t.Fatal("Verify accepted a tampered confirmation payload")
	}
}

// Spec 7.5: the latest confirmation overlays admission C5 before primary selection and close.
func TestReproductionConfirmationDrivesCloseClassification(t *testing.T) {
	tests := []struct {
		name        string
		outcome     ReproOutcome
		c5          Fact
		wantLevel   MatchLevel
		wantFinding FindingState
		wantIssue   IssueState
	}{
		{name: "failed confirmation demotes full match", outcome: NotReproduced, c5: FactTrue, wantLevel: LevelPartial, wantFinding: FindingPDFinding, wantIssue: IssuePD},
		{name: "reproduced confirmation keeps full match", outcome: Reproduced, c5: FactTrue, wantLevel: LevelFull, wantFinding: FindingTPFinding, wantIssue: IssueTP},
		{name: "reproduced confirmation supplies unknown admission c5", outcome: Reproduced, c5: FactUnknown, wantLevel: LevelFull, wantFinding: FindingTPFinding, wantIssue: IssueTP},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cr := CaseRun{
				Case:     "case",
				Issues:   []Issue{{Case: "case", ID: "D1", Reproduction: Reproduction{Applies: true, Oracle: "oracle", Attempts: 3}}},
				Findings: []Finding{{ID: "f1", Row: 1, Computed: map[string]ComputedFacts{"D1": {C2: FactTrue, C4Cited: FactTrue, C5: tt.c5}}}},
			}
			if err := cr.Admit("t1"); err != nil {
				t.Fatal(err)
			}
			if err := cr.Decide(Decision{FindingID: "f1", CandidateIssue: "D1", C1: FactTrue, C3: FactTrue, C4Shows: FactTrue, By: "reviewer", TS: "t2", Reason: "verified"}); err != nil {
				t.Fatal(err)
			}
			if err := cr.RecordReproductionConfirmation("D1", string(tt.outcome), confirmDigestA, 3, "t3"); err != nil {
				t.Fatal(err)
			}
			if err := cr.Log.Verify(); err != nil {
				t.Fatalf("confirmation broke the hash chain: %v", err)
			}
			if got := cr.MatchLevel("f1"); got != tt.wantLevel {
				t.Fatalf("MatchLevel after %s = %v, want %v", tt.outcome, got, tt.wantLevel)
			}
			if err := cr.Close("t4"); err != nil {
				t.Fatalf("Close after confirmation: %v", err)
			}
			if cr.FindingState("f1") != tt.wantFinding || cr.IssueState("D1") != tt.wantIssue {
				t.Fatalf("after close finding=%s issue=%s, want %s/%s", cr.FindingState("f1"), cr.IssueState("D1"), tt.wantFinding, tt.wantIssue)
			}
		})
	}
}

// Only applied-reproduction Issues that currently have a primary require a confirmation.
func TestMissingReproductionConfirmations(t *testing.T) {
	cr := CaseRun{
		Case: "case",
		Issues: []Issue{
			{Case: "case", ID: "D2", Reproduction: Reproduction{Applies: true}},
			{Case: "case", ID: "D1", Reproduction: Reproduction{Applies: true}},
			{Case: "case", ID: "D3", Reproduction: Reproduction{Applies: false}},
			{Case: "case", ID: "D4", Reproduction: Reproduction{Applies: true}},
		},
		Findings: []Finding{
			{ID: "f2", Row: 2, Computed: map[string]ComputedFacts{"D2": {C2: FactTrue, C4Cited: FactTrue, C5: FactTrue}}},
			{ID: "f1", Row: 1, Computed: map[string]ComputedFacts{"D1": {C2: FactTrue, C4Cited: FactTrue, C5: FactTrue}}},
		},
	}
	if err := cr.Admit("t1"); err != nil {
		t.Fatal(err)
	}
	for _, pair := range [][2]string{{"f1", "D1"}, {"f2", "D2"}} {
		if err := cr.Decide(Decision{FindingID: pair[0], CandidateIssue: pair[1], C1: FactTrue, C3: FactTrue, C4Shows: FactTrue, By: "reviewer", TS: "t2", Reason: "verified"}); err != nil {
			t.Fatal(err)
		}
	}
	missing, err := cr.MissingReproductionConfirmations()
	if err != nil {
		t.Fatal(err)
	}
	if len(missing) != 2 || missing[0] != "D1" || missing[1] != "D2" {
		t.Fatalf("missing = %v, want sorted [D1 D2]: D3 does not apply, D4 has no primary", missing)
	}
	if err := cr.RecordReproductionConfirmation("D1", string(Reproduced), confirmDigestA, 1, "t3"); err != nil {
		t.Fatal(err)
	}
	if err := cr.Log.Verify(); err != nil {
		t.Fatalf("Verify after confirmation: %v", err)
	}
	if missing, err = cr.MissingReproductionConfirmations(); err != nil || len(missing) != 1 || missing[0] != "D2" {
		t.Fatalf("missing after D1 confirmation = %v, %v; want [D2]", missing, err)
	}
	if err := cr.RecordReproductionConfirmation("D2", string(NotReproduced), confirmDigestB, 2, "t4"); err != nil {
		t.Fatal(err)
	}
	if missing, err = cr.MissingReproductionConfirmations(); err != nil || len(missing) != 0 {
		t.Fatalf("missing after all confirmations = %v, %v; want none", missing, err)
	}
}

// Confirmations are append-only and repeated before close: the latest outcome wins.
func TestRepeatedConfirmationsLatestWins(t *testing.T) {
	cr := CaseRun{
		Case:     "case",
		Issues:   []Issue{{Case: "case", ID: "D1", Reproduction: Reproduction{Applies: true, Oracle: "oracle", Attempts: 3}}},
		Findings: []Finding{{ID: "f1", Row: 1, Computed: map[string]ComputedFacts{"D1": {C2: FactTrue, C4Cited: FactTrue, C5: FactTrue}}}},
	}
	if err := cr.Admit("t1"); err != nil {
		t.Fatal(err)
	}
	if err := cr.Decide(Decision{FindingID: "f1", CandidateIssue: "D1", C1: FactTrue, C3: FactTrue, C4Shows: FactTrue, By: "reviewer", TS: "t2", Reason: "verified"}); err != nil {
		t.Fatal(err)
	}
	if err := cr.RecordReproductionConfirmation("D1", string(Reproduced), confirmDigestA, 1, "t3"); err != nil {
		t.Fatal(err)
	}
	if err := cr.RecordReproductionConfirmation("D1", string(NotReproduced), confirmDigestB, 2, "t4"); err != nil {
		t.Fatal(err)
	}
	confirmations := 0
	for _, event := range cr.Log.Events {
		if event.Kind == EventConfirm {
			confirmations++
		}
	}
	if confirmations != 2 {
		t.Fatalf("confirm events = %d, want both append-only entries", confirmations)
	}
	if got := cr.MatchLevel("f1"); got != LevelPartial {
		t.Fatalf("MatchLevel with latest NOT_REPRODUCED = %v, want partial", got)
	}
	if err := cr.Log.Verify(); err != nil {
		t.Fatal(err)
	}
}

// A confirmation is refused outside an Issue in UNDER_EVALUATION, on any other entity, or with a state change.
func TestEventConfirmRejectsIllegalEntityAndState(t *testing.T) {
	var log Log
	if _, err := log.Append(Event{Entity: EntityIssue, ID: "case/i1", Kind: EventAdmit, PreviousState: string(IssuePending), NewState: string(IssueUnderEvaluation), TS: "t1"}); err != nil {
		t.Fatal(err)
	}
	if _, err := log.Append(Event{Entity: EntityIssue, ID: "case/i1", Kind: EventDerive, PreviousState: string(IssueUnderEvaluation), NewState: string(IssueTP), TS: "t2"}); err != nil {
		t.Fatal(err)
	}
	for name, illegal := range map[string]Event{
		"wrong entity":           {Entity: EntityFinding, ID: "f1", Kind: EventConfirm, PreviousState: string(FindingPendingAdjudication), NewState: string(FindingPendingAdjudication)},
		"pending issue":          {Entity: EntityIssue, ID: "case/i2", Kind: EventConfirm, PreviousState: string(IssuePending), NewState: string(IssuePending)},
		"terminal issue":         {Entity: EntityIssue, ID: "case/i1", Kind: EventConfirm, PreviousState: string(IssueTP), NewState: string(IssueTP)},
		"stale under-evaluation": {Entity: EntityIssue, ID: "case/i1", Kind: EventConfirm, PreviousState: string(IssueUnderEvaluation), NewState: string(IssueUnderEvaluation), Adjudicator: ConfirmAdjudicator, Payload: validConfirmPayload("case/i1")},
		"state-changing":         {Entity: EntityIssue, ID: "case/i1", Kind: EventConfirm, PreviousState: string(IssueTP), NewState: string(IssueUnderEvaluation)},
	} {
		t.Run(name, func(t *testing.T) {
			if _, err := log.Append(illegal); err == nil {
				t.Fatalf("Append accepted a %s confirmation", name)
			}
		})
	}
}

// A confirmation needs an adjudicating case, a known applied-reproduction Issue under
// evaluation with a primary, and a well-formed outcome, digest and attempt count.
func TestRecordReproductionConfirmationGuards(t *testing.T) {
	build := func(t *testing.T) *CaseRun {
		t.Helper()
		cr := &CaseRun{
			Case: "case",
			Issues: []Issue{
				{Case: "case", ID: "D1", Reproduction: Reproduction{Applies: true, Oracle: "oracle"}},
				{Case: "case", ID: "D3", Reproduction: Reproduction{Applies: false}},
				{Case: "case", ID: "D4", Reproduction: Reproduction{Applies: true}},
			},
			Findings: []Finding{{ID: "f1", Row: 1, Computed: map[string]ComputedFacts{"D1": {C2: FactTrue, C4Cited: FactTrue, C5: FactTrue}}}},
		}
		if err := cr.Admit("t1"); err != nil {
			t.Fatal(err)
		}
		return cr
	}
	adjudicate := func(t *testing.T, cr *CaseRun) {
		t.Helper()
		if err := cr.Decide(Decision{FindingID: "f1", CandidateIssue: "D1", C1: FactTrue, C3: FactTrue, C4Shows: FactTrue, By: "reviewer", TS: "t2", Reason: "verified"}); err != nil {
			t.Fatal(err)
		}
	}
	mustFail := func(t *testing.T, err error, want string) {
		t.Helper()
		if err == nil || !strings.Contains(err.Error(), want) {
			t.Fatalf("error = %v, want substring %q", err, want)
		}
	}
	t.Run("case not adjudicating", func(t *testing.T) {
		mustFail(t, build(t).RecordReproductionConfirmation("D1", "REPRODUCED", confirmDigestA, 1, "t2"), "state")
	})
	t.Run("case closed", func(t *testing.T) {
		cr := build(t)
		adjudicate(t, cr)
		if err := cr.RecordReproductionConfirmation("D1", "REPRODUCED", confirmDigestA, 1, "t3"); err != nil {
			t.Fatal(err)
		}
		if err := cr.Close("t4"); err != nil {
			t.Fatal(err)
		}
		mustFail(t, cr.RecordReproductionConfirmation("D1", "REPRODUCED", confirmDigestA, 1, "t5"), "state")
	})
	t.Run("guards", func(t *testing.T) {
		cr := build(t)
		adjudicate(t, cr)
		mustFail(t, cr.RecordReproductionConfirmation("DX", "REPRODUCED", confirmDigestA, 1, "t3"), "unknown issue")
		mustFail(t, cr.RecordReproductionConfirmation("D3", "REPRODUCED", confirmDigestA, 1, "t3"), "does not apply")
		mustFail(t, cr.RecordReproductionConfirmation("D4", "REPRODUCED", confirmDigestA, 1, "t3"), "no primary")
		for _, tc := range []struct {
			name, outcome, digest string
			attempts              int
			want                  string
		}{
			{name: "outcome", outcome: "MAYBE", digest: confirmDigestA, attempts: 1, want: "outcome"},
			{name: "empty digest", outcome: "REPRODUCED", digest: "", attempts: 1, want: "digest"},
			{name: "non-sha256 digest", outcome: "REPRODUCED", digest: "sha256:abc", attempts: 1, want: "digest"},
			{name: "negative attempts", outcome: "REPRODUCED", digest: confirmDigestA, attempts: -1, want: "attempts"},
		} {
			t.Run(tc.name, func(t *testing.T) {
				mustFail(t, cr.RecordReproductionConfirmation("D1", tc.outcome, tc.digest, tc.attempts, "t3"), tc.want)
			})
		}
		if err := cr.RecordReproductionConfirmation("D1", "NOT_REPRODUCED", confirmDigestA, 0, "t3"); err != nil {
			t.Fatalf("zero attempts must be allowed when no agent tests were supplied: %v", err)
		}
	})
}

// A crafted confirmation with a correctly recomputed hash must fail verification on its semantics alone.
func TestEventConfirmVerifyRejectsMalformedPayloads(t *testing.T) {
	open := `{"issue_id":"case/i1","outcome":"REPRODUCED","artifact_digest":"` + confirmDigestA + `","attempts":1`
	tests := []struct {
		name        string
		payload     string
		adjudicator string
	}{
		{name: "unknown outcome", payload: `{"issue_id":"case/i1","outcome":"MAYBE","artifact_digest":"` + confirmDigestA + `","attempts":1}`},
		{name: "duplicate outcome keys", payload: `{"issue_id":"case/i1","outcome":"NOT_REPRODUCED","outcome":"REPRODUCED","artifact_digest":"` + confirmDigestA + `","attempts":1}`},
		{name: "empty digest", payload: `{"issue_id":"case/i1","outcome":"REPRODUCED","artifact_digest":"","attempts":1}`},
		{name: "negative attempts", payload: `{"issue_id":"case/i1","outcome":"NOT_REPRODUCED","artifact_digest":"` + confirmDigestA + `","attempts":-7}`},
		{name: "empty object", payload: `{}`},
		{name: "foreign issue key", payload: `{"issue_id":"i1","outcome":"REPRODUCED","artifact_digest":"` + confirmDigestA + `","attempts":1}`},
		{name: "unknown field", payload: open + `,"extra":true}`},
		{name: "trailing json", payload: open + `} {"x":1}`},
		{name: "missing payload", payload: ""},
		{name: "wrong adjudicator", payload: open + `}`, adjudicator: "reviewer"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var log Log
			if _, err := log.Append(Event{Entity: EntityIssue, ID: "case/i1", Kind: EventAdmit, PreviousState: string(IssuePending), NewState: string(IssueUnderEvaluation), TS: "t1"}); err != nil {
				t.Fatal(err)
			}
			event := Event{Entity: EntityIssue, ID: "case/i1", Kind: EventConfirm, PreviousState: string(IssueUnderEvaluation), NewState: string(IssueUnderEvaluation), TS: "t2", Adjudicator: ConfirmAdjudicator}
			if tt.adjudicator != "" {
				event.Adjudicator = tt.adjudicator
			}
			if tt.payload != "" {
				event.Payload = json.RawMessage(tt.payload)
			}
			craftConfirm(t, &log, event)
			err := log.Verify()
			if err == nil {
				t.Fatal("Verify accepted a malformed confirmation")
			}
			if !strings.Contains(err.Error(), "confirm") {
				t.Fatalf("rejection must come from confirmation semantics, got: %v", err)
			}
		})
	}
}

// A malformed confirmation never satisfies the missing-confirmation guard.
func TestMissingConfirmationIgnoresMalformedEvent(t *testing.T) {
	cr := CaseRun{
		Case:     "case",
		Issues:   []Issue{{Case: "case", ID: "D1", Reproduction: Reproduction{Applies: true, Oracle: "oracle"}}},
		Findings: []Finding{{ID: "f1", Row: 1, Computed: map[string]ComputedFacts{"D1": {C2: FactTrue, C4Cited: FactTrue, C5: FactTrue}}}},
	}
	if err := cr.Admit("t1"); err != nil {
		t.Fatal(err)
	}
	if err := cr.Decide(Decision{FindingID: "f1", CandidateIssue: "D1", C1: FactTrue, C3: FactTrue, C4Shows: FactTrue, By: "reviewer", TS: "t2", Reason: "verified"}); err != nil {
		t.Fatal(err)
	}
	craftConfirm(t, &cr.Log, Event{Entity: EntityIssue, ID: "case/D1", Kind: EventConfirm, PreviousState: string(IssueUnderEvaluation), NewState: string(IssueUnderEvaluation), TS: "t3", Adjudicator: ConfirmAdjudicator, Payload: json.RawMessage(`{"issue_id":"case/D1","outcome":"MAYBE","artifact_digest":"` + confirmDigestA + `","attempts":1}`)})
	missing, err := cr.MissingReproductionConfirmations()
	if err != nil {
		t.Fatal(err)
	}
	if len(missing) != 1 || missing[0] != "D1" {
		t.Fatalf("missing = %v, want [D1]: a malformed confirmation must not count", missing)
	}
	if err := cr.RecordReproductionConfirmation("D1", string(Reproduced), confirmDigestA, 1, "t4"); err != nil {
		t.Fatal(err)
	}
	if missing, err = cr.MissingReproductionConfirmations(); err != nil || len(missing) != 0 {
		t.Fatalf("missing after a valid confirmation = %v, %v; want none", missing, err)
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

package eval

import "testing"

func resultTestClosedCaseRun(t *testing.T) *CaseRun {
	t.Helper()
	cr := &CaseRun{
		Case:   "case-1",
		Issues: []Issue{{Case: "case-1", ID: "i-1", Domain: Correctness, Severity: High}},
		Findings: []Finding{{
			ID: "f-1", ReportedSeverityText: "high",
			Computed: map[string]ComputedFacts{"i-1": {C2: FactTrue, C4Cited: FactTrue, C5: FactTrue}},
		}},
	}
	if err := cr.Admit("1"); err != nil {
		t.Fatal(err)
	}
	if err := cr.Decide(Decision{FindingID: "f-1", CandidateIssue: "i-1", C1: FactTrue, C3: FactTrue, C4Shows: FactTrue, By: "reviewer", TS: "2", Reason: "verified"}); err != nil {
		t.Fatal(err)
	}
	if err := cr.Close("3"); err != nil {
		t.Fatal(err)
	}
	return cr
}

func TestCaseResultFromCopiesClosedRunOutcomes(t *testing.T) {
	cr := resultTestClosedCaseRun(t)
	repro := map[string]ReproOutcome{"f-1": Reproduced}
	got, err := CaseResultFrom(cr, false, repro)
	if err != nil {
		t.Fatal(err)
	}
	if got.Case != "case-1" || got.Control || got.IssueStates["i-1"] != IssueTP || got.Primary["i-1"] != "f-1" || got.FindingStates["f-1"] != FindingTPFinding || got.ReportedSeverity["f-1"] != High || got.Repro["f-1"] != Reproduced {
		t.Fatalf("CaseResultFrom() = %+v", got)
	}
	repro["f-1"] = NotReproduced
	if got.Repro["f-1"] != Reproduced {
		t.Fatal("CaseResultFrom retained the caller's repro map")
	}
}

func TestCaseResultFromRejectsNonterminalOutcomes(t *testing.T) {
	t.Run("finding", func(t *testing.T) {
		cr := &CaseRun{Case: "case-1", Findings: []Finding{{ID: "f-1"}}}
		if err := cr.Admit("1"); err != nil {
			t.Fatal(err)
		}
		if _, err := CaseResultFrom(cr, false, nil); err == nil {
			t.Fatal("CaseResultFrom accepted a pending finding")
		}
	})
	t.Run("issue", func(t *testing.T) {
		cr := &CaseRun{Case: "case-1", Issues: []Issue{{Case: "case-1", ID: "i-1"}}}
		if err := cr.Admit("1"); err != nil {
			t.Fatal(err)
		}
		if _, err := CaseResultFrom(cr, false, nil); err == nil {
			t.Fatal("CaseResultFrom accepted a nonterminal issue")
		}
	})
	t.Run("nil run", func(t *testing.T) {
		if _, err := CaseResultFrom(nil, false, nil); err == nil {
			t.Fatal("CaseResultFrom accepted a nil CaseRun")
		}
	})
}

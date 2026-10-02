package bench

import (
	"reflect"
	"testing"
)

func TestFindingFactsUsesLocationAndExistingLedgerLinksWithoutKeywords(t *testing.T) {
	plan := "## Findings\n\n" +
		"| ID | Type | Cause | Location | Evidence | Severity |\n" +
		"|---|---|---|---|---|---|\n" +
		"| F1 | boundary | ordinary observation | src/a.go:14 | E1 | high |\n" +
		"| F2 | boundary | secretword | src/a.go:99 | E404 | low |\n\n" +
		"## Evidence ledger\n\n" +
		"| ID | Claim |\n|---|---|\n" +
		"| E1 | observed src/a.go:14 |\n"
	key := Key{Defects: []Defect{{ID: "D1", File: "src/a.go", Line: 10, Keywords: []string{"secretword"}}}}

	got := FindingFacts(plan, key)
	if len(got) != 2 {
		t.Fatalf("FindingFacts() returned %d rows, want 2: %+v", len(got), got)
	}
	wantFingerprint, err := FindingRowFingerprint(plan, 1)
	if err != nil {
		t.Fatal(err)
	}
	if got[0].Row != 1 || got[0].Text != "F1 | boundary | ordinary observation | src/a.go:14 | E1 | high" || got[0].Fingerprint != wantFingerprint {
		t.Fatalf("first finding facts = %+v", got[0])
	}
	if got[0].Location != "src/a.go:14" || got[0].ReportedSeverity != "high" || !got[0].HasLocation || !got[0].CitesLedger {
		t.Fatalf("first finding location/link facts = %+v", got[0])
	}
	if !reflect.DeepEqual(got[0].LocatedDefects, []string{"D1"}) {
		t.Fatalf("first LocatedDefects = %v, want D1 without keyword matching", got[0].LocatedDefects)
	}
	if got[1].CitesLedger || !got[1].HasLocation || len(got[1].LocatedDefects) != 0 {
		t.Fatalf("second finding should not match by keyword or nonexistent evidence: %+v", got[1])
	}
}

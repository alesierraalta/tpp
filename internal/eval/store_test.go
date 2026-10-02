package eval

import (
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

func TestSaveLoadRunPreservesMetadataCaseRunsAndSeparateEventLogs(t *testing.T) {
	var log Log
	if _, err := log.Append(Event{
		Entity: EntityFinding, ID: "f1", Kind: EventAdmit,
		PreviousState: string(FindingRaw), NewState: string(FindingPendingAdjudication), TS: "t1",
	}); err != nil {
		t.Fatal(err)
	}
	stored := StoredRun{
		K: 1, HarnessLabel: "baseline", SourceResultsDir: "/results",
		Record: RunRecord{
			Data: RunData{ID: "run-1"}, State: RunAdjudicating,
			ManifestSHA256: "manifest", PolicySHA256: "policy", HarnessID: "harness",
			Model: "model", Runner: "pi", Environment: "linux/amd64",
		},
		CaseRuns: map[string]CaseRun{"case-a": {Case: "case-a", Findings: []Finding{{ID: "f1", Row: 1}}, Log: log}},
	}
	runDir := filepath.Join(t.TempDir(), "run-1")
	if err := SaveRun(runDir, stored); err != nil {
		t.Fatal(err)
	}
	for _, path := range []string{
		filepath.Join(runDir, "run.json"),
		filepath.Join(runDir, "case-a", "caserun.json"),
		filepath.Join(runDir, "case-a", "events.jsonl"),
	} {
		if _, err := os.Stat(path); err != nil {
			t.Fatalf("missing persisted file %s: %v", path, err)
		}
	}
	data, err := os.ReadFile(filepath.Join(runDir, "run.json"))
	if err != nil {
		t.Fatal(err)
	}
	for _, field := range []string{`"k": 1`, `"state": "ADJUDICATING"`, `"harness_label": "baseline"`, `"source_results_dir": "/results"`} {
		if !strings.Contains(string(data), field) {
			t.Fatalf("run.json missing %s: %s", field, data)
		}
	}
	loaded, err := LoadRun(runDir)
	if err != nil {
		t.Fatal(err)
	}
	if loaded.K != stored.K || loaded.HarnessLabel != stored.HarnessLabel || loaded.Record.State != stored.Record.State || loaded.Record.HarnessID != stored.Record.HarnessID {
		t.Fatalf("loaded metadata = %+v", loaded)
	}
	if !reflect.DeepEqual(loaded.CaseRuns["case-a"].Findings, stored.CaseRuns["case-a"].Findings) {
		t.Fatalf("loaded findings = %+v", loaded.CaseRuns["case-a"].Findings)
	}
	if len(loaded.CaseRuns["case-a"].Log.Events) != 1 || loaded.CaseRuns["case-a"].Log.Events[0].Hash != log.Events[0].Hash || loaded.CaseRuns["case-a"].Log.Verify() != nil {
		t.Fatalf("loaded event log = %+v", loaded.CaseRuns["case-a"].Log)
	}
	digest, err := Digest(filepath.Join(runDir, "run.json"))
	if err != nil || !strings.HasPrefix(digest, "sha256:") {
		t.Fatalf("Digest() = %q, %v", digest, err)
	}
}

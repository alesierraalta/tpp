package eval

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/alesierraalta/tpp/internal/bench"
)

// An unlocated row is the harness's own output failure: the finding is INVALID and counted as noise, while the
// run stays scoreable, so a malformed row can never get a whole run excluded (spec 4.4).
func TestImportRunMarksUnlocatableFindingInvalid(t *testing.T) {
	benchDir, manifest, policy := importTestInputs(t)
	results := t.TempDir()
	writeImportResult(t, results, `{"case":"c1","run":1,"plan_found":true,"plan_format":"table"}`,
		"## Findings\n\n| Id | Finding | Severity | Data safe? | Evidence id |\n|---|---|---|---|---|\n| F1 | a claim | high | yes | E1 |\n\n## Evidence ledger\n\n| Id | Claim |\n|---|---|\n| E1 | observed |\n", "")

	got, err := ImportRun(results, benchDir, manifest, policy, "baseline", 1, "t1")
	if err != nil {
		t.Fatal(err)
	}
	if got.Run.Record.State == RunInvalid || len(got.Run.CaseRuns["c1"].Findings) != 1 || !got.Run.CaseRuns["c1"].Findings[0].Invalid {
		t.Fatalf("unlocatable finding import = %+v", got.Run)
	}
}

// A prose plan is scored as the harness delivered it, never excluded: its Issues end FN (spec 5.3).
func TestImportRunScoresAProsePlan(t *testing.T) {
	benchDir, manifest, policy := importTestInputs(t)
	results := t.TempDir()
	writeImportResult(t, results, `{"case":"c1","run":1,"plan_found":true,"plan_format":"prose"}`,
		"## Findings\n\n- the clamp looks wrong somewhere\n", "")

	got, err := ImportRun(results, benchDir, manifest, policy, "baseline", 1, "t1")
	if err != nil {
		t.Fatal(err)
	}
	if got.Run.Record.State == RunInvalid || got.Run.CaseOutcomes["c1"] != "prose_plan" {
		t.Fatalf("prose plan import: state %s, outcome %q", got.Run.Record.State, got.Run.CaseOutcomes["c1"])
	}
}

// A plan the runner found but the results no longer keep cannot be adjudicated, so the run is INVALID (spec 15.6).
func TestImportRunInvalidWhenTheKeptPlanIsMissing(t *testing.T) {
	benchDir, manifest, policy := importTestInputs(t)
	results := t.TempDir()
	writeImportResult(t, results, `{"case":"c1","run":1,"plan_found":true,"plan_format":"table"}`, "", "")

	got, err := ImportRun(results, benchDir, manifest, policy, "baseline", 1, "t1")
	if err != nil {
		t.Fatal(err)
	}
	if got.Run.Record.State != RunInvalid {
		t.Fatalf("missing kept plan: state %s, want %s", got.Run.Record.State, RunInvalid)
	}
}

func TestImportRunComputesLocationEvidenceAndCatchFactsPerIssue(t *testing.T) {
	benchDir := t.TempDir()
	canary := "0123456789abcdef0123456789abcdef"
	writeManifestCase(t, benchDir, canary)
	var key map[string]any
	if err := json.Unmarshal([]byte(validV2Key), &key); err != nil {
		t.Fatal(err)
	}
	key["id"], key["canary"] = "c1", canary
	keyData, err := json.Marshal(key)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(benchDir, "cases", "c1", bench.KeyFile), keyData, 0o644); err != nil {
		t.Fatal(err)
	}
	manifest, err := BuildManifest(benchDir, testManifestSpec(canary))
	if err != nil {
		t.Fatal(err)
	}
	manifest, err = manifest.Seal()
	if err != nil {
		t.Fatal(err)
	}
	policy, err := (Policy{Name: "test"}).Seal()
	if err != nil {
		t.Fatal(err)
	}
	results := t.TempDir()
	writeImportResult(t, results, `{"case":"c1","run":1,"plan_found":true,"plan_format":"table","catch":{"caught":{"D1":true}}}`,
		"## Findings\n\n| Id | Finding | Type | Location | Evidence id | Severity |\n|---|---|---|---|---|---|\n| F1 | record is lost | boundary | src/a.js:3 | E1 | high |\n\n## Evidence ledger\n\n| Id | Claim |\n|---|---|\n| E1 | observed src/a.js:3 |\n", "")
	imported, err := ImportRun(results, benchDir, manifest, policy, "baseline", 1, "t1")
	if err != nil {
		t.Fatal(err)
	}
	finding := imported.Run.CaseRuns["c1"].Findings[0]
	facts := finding.Computed["D1"]
	if facts.C2 != FactTrue || facts.C4Cited != FactTrue || facts.C5 != FactTrue {
		t.Fatalf("computed facts = %+v", facts)
	}
}

func TestImportRunClassifiesNoPlanAndInfrastructureFailuresAndScansLeaks(t *testing.T) {
	benchDir, manifest, policy := importTestInputs(t)

	noPlanResults := t.TempDir()
	writeImportResult(t, noPlanResults, `{"case":"c1","run":1,"plan_found":false,"plan_format":"empty"}`, "", "")
	noPlan, err := ImportRun(noPlanResults, benchDir, manifest, policy, "baseline", 1, "t1")
	if err != nil {
		t.Fatal(err)
	}
	if noPlan.Run.CaseOutcomes["c1"] != "no_plan" || noPlan.Run.Record.State != RunAdjudicating {
		t.Fatalf("no-plan import = %+v", noPlan.Run)
	}

	infraResults := t.TempDir()
	writeImportResult(t, infraResults, `{"case":"c1","run":1,"failed":true,"fail_reason":"HTTP 503"}`, "", "log includes "+manifest.Canary)
	infra, err := ImportRun(infraResults, benchDir, manifest, policy, "baseline", 1, "t2")
	if err != nil {
		t.Fatal(err)
	}
	if infra.Run.Record.State != RunFailedInfrastructure || infra.Run.CaseOutcomes["c1"] != "HTTP 503" {
		t.Fatalf("infra import = %+v", infra.Run)
	}
	if len(infra.Run.Record.Leaks) == 0 || infra.Run.Record.Leaks[0].Kind != "canary" {
		t.Fatalf("agent.log leak not detected: %+v", infra.Run.Record.Leaks)
	}

	failedResults := t.TempDir()
	writeImportResult(t, failedResults, `{"case":"c1","run":1,"failed":true,"fail_reason":"worker crashed"}`, "", "")
	failed, err := ImportRun(failedResults, benchDir, manifest, policy, "baseline", 1, "t3")
	if err != nil {
		t.Fatal(err)
	}
	if failed.Run.Record.State != RunAdjudicating || failed.Run.CaseOutcomes["c1"] != "unclassified_failure" {
		t.Fatalf("unclassified failure import = %+v", failed.Run)
	}
}

func importTestInputs(t *testing.T) (string, Manifest, Policy) {
	t.Helper()
	benchDir := t.TempDir()
	canary := "0123456789abcdef0123456789abcdef"
	writeManifestCase(t, benchDir, canary)
	manifest, err := BuildManifest(benchDir, testManifestSpec(canary))
	if err != nil {
		t.Fatal(err)
	}
	manifest, err = manifest.Seal()
	if err != nil {
		t.Fatal(err)
	}
	policy, err := (Policy{Name: "test"}).Seal()
	if err != nil {
		t.Fatal(err)
	}
	return benchDir, manifest, policy
}

func writeImportResult(t *testing.T, resultsDir, resultJSON, plan, agentLog string) {
	t.Helper()
	caseDir := filepath.Join(resultsDir, "c1", "1")
	if err := os.MkdirAll(caseDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(caseDir, "result.json"), []byte(resultJSON), 0o644); err != nil {
		t.Fatal(err)
	}
	if plan != "" {
		if err := os.WriteFile(filepath.Join(caseDir, "test-plan.md"), []byte(plan), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	if agentLog != "" {
		if err := os.WriteFile(filepath.Join(caseDir, "agent.log"), []byte(agentLog), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	aggregate, err := json.Marshal(map[string]any{
		"model":      "model",
		"provenance": map[string]any{"model": "model", "runner": "pi", "agent_config": "bench", "skills_digest": "sha256:test", "environment": "linux/amd64"},
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(resultsDir, "aggregate.json"), aggregate, 0o644); err != nil {
		t.Fatal(err)
	}
}

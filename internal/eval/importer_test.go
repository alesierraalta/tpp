package eval

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
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

// writeImportProvenance rewrites aggregate.json with a full provenance whose binding fields the
// test chooses; the base fields stay what writeImportResult wrote.
func writeImportProvenance(t *testing.T, resultsDir string, fields map[string]any) {
	t.Helper()
	provenance := map[string]any{"model": "model", "runner": "pi", "agent_config": "bench", "skills_digest": "sha256:test", "environment": "linux/amd64"}
	for key, value := range fields {
		provenance[key] = value
	}
	data, err := json.Marshal(map[string]any{"model": "model", "provenance": provenance})
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(resultsDir, "aggregate.json"), data, 0o644); err != nil {
		t.Fatal(err)
	}
}

const importPlanTable = "## Findings\n\n| Id | Finding | Severity | Data safe? | Evidence id |\n|---|---|---|---|---|\n| F1 | a claim | high | yes | E1 |\n\n## Evidence ledger\n\n| Id | Claim |\n|---|---|\n| E1 | observed |\n"

// A reading sealed under one manifest may not be imported under another: the relabel would
// fabricate comparability with runs the supplied manifest never measured.
func TestImportRunRefusesProvenanceBoundToAnotherManifest(t *testing.T) {
	benchDir, manifest, policy := importTestInputs(t)
	results := t.TempDir()
	writeImportResult(t, results, `{"case":"c1","run":1,"plan_found":true,"plan_format":"table","tokens":5}`, importPlanTable, "")
	writeImportProvenance(t, results, map[string]any{
		"manifest_sha256": "sha256:someone-elses-manifest", "instrument_valid": true,
		"execution_complete": true, "budget_status": "supported",
	})
	_, err := ImportRun(results, benchDir, manifest, policy, "baseline", 1, "t1")
	if err == nil || !strings.Contains(err.Error(), "manifest") {
		t.Fatalf("relabeling another manifest's reading must be refused, got %v", err)
	}
}

// Provenance that does not prove the instrument ran validly and completely must land the record
// INVALID (spec 15), so it can never reach COMPLETED and never contribute comparable numbers.
func TestImportRunMarksCompromisedProvenanceInvalid(t *testing.T) {
	for _, tc := range []struct {
		name        string
		fields      map[string]any
		wantInvalid bool
	}{
		{"instrument invalid", map[string]any{"instrument_valid": false, "execution_complete": true, "budget_status": "supported"}, true},
		{"lacks validity evidence", map[string]any{}, true},
		{"execution incomplete", map[string]any{"instrument_valid": true, "execution_complete": false, "budget_status": "supported"}, true},
		{"budget support unverified", map[string]any{"instrument_valid": true, "execution_complete": true, "budget_status": "unsupported", "budget_unsupported_reason": "usage missing"}, true},
		{"valid provenance stays adjudicable", map[string]any{"instrument_valid": true, "execution_complete": true, "budget_status": "supported"}, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			benchDir, manifest, policy := importTestInputs(t)
			results := t.TempDir()
			writeImportResult(t, results, `{"case":"c1","run":1,"plan_found":true,"plan_format":"table","tokens":5}`, importPlanTable, "")
			fields := map[string]any{"manifest_sha256": manifest.ManifestSHA256}
			for key, value := range tc.fields {
				fields[key] = value
			}
			writeImportProvenance(t, results, fields)
			got, err := ImportRun(results, benchDir, manifest, policy, "baseline", 1, "t1")
			if err != nil {
				t.Fatal(err)
			}
			if tc.wantInvalid {
				if got.Run.Record.State != RunInvalid {
					t.Fatalf("state = %s, want %s so the reading can never complete", got.Run.Record.State, RunInvalid)
				}
				if got.Run.Record.AbortReason == "" {
					t.Fatal("an invalid instrument must say why in the abort reason")
				}
				return
			}
			if got.Run.Record.State != RunAdjudicating {
				t.Fatalf("state = %s, want %s for a valid complete reading", got.Run.Record.State, RunAdjudicating)
			}
		})
	}
}

// The runner's own budget outcome is scored as the plan stands and Issues not found are FN; it is
// never an infrastructure failure (spec 5.3 rows 2 and 1).
func TestImportRunScoresABudgetExhaustedCaseNotInfrastructure(t *testing.T) {
	benchDir, manifest, policy := importTestInputs(t)
	results := t.TempDir()
	locatedPlan := "## Findings\n\n| Id | Finding | Type | Location | Evidence id | Severity |\n|---|---|---|---|---|---|\n| F1 | the clamp is wrong | boundary | src/a.js:3 | E1 | high |\n\n## Evidence ledger\n\n| Id | Claim |\n|---|---|\n| E1 | observed src/a.js:3 |\n"
	writeImportResult(t, results, `{"case":"c1","run":1,"plan_found":true,"plan_format":"table","budget_exhausted":true,"outcome":"budget_exhausted","tokens":5}`, locatedPlan, "")
	got, err := ImportRun(results, benchDir, manifest, policy, "baseline", 1, "t1")
	if err != nil {
		t.Fatal(err)
	}
	if got.Run.Record.State != RunAdjudicating {
		t.Fatalf("state = %s, want the budget-exhausted case admitted for scoring, not %s", got.Run.Record.State, RunFailedInfrastructure)
	}
	if got.Run.CaseOutcomes["c1"] != "budget_exhausted" {
		t.Fatalf("outcome = %q, want budget_exhausted recorded as the case's outcome", got.Run.CaseOutcomes["c1"])
	}
	findings := got.Run.CaseRuns["c1"].Findings
	if len(findings) != 1 || findings[0].Invalid {
		t.Fatalf("findings = %+v, want the plan's finding admitted for scoring", findings)
	}
}

// A manifest-bound reading that budgets tokens but records none would claim measured zero for an
// unknown usage; the import refuses instead (the runner marks such cases invalid itself — this is
// the belt for readings whose results do not say so).
func TestImportRunRefusesMissingTokenUsageUnderATokenBudget(t *testing.T) {
	benchDir, manifest, policy := importTestInputs(t)
	if manifest.Budgets.MaxTokensPerCaseRun <= 0 {
		t.Fatal("test manifest must budget tokens for this probe")
	}
	results := t.TempDir()
	writeImportResult(t, results, `{"case":"c1","run":1,"plan_found":true,"plan_format":"table","outcome":"scored"}`, importPlanTable, "")
	writeImportProvenance(t, results, map[string]any{
		"manifest_sha256": manifest.ManifestSHA256, "instrument_valid": true,
		"execution_complete": true, "budget_status": "supported",
	})
	_, err := ImportRun(results, benchDir, manifest, policy, "baseline", 1, "t1")
	if err == nil || !strings.Contains(err.Error(), "token usage") {
		t.Fatalf("missing token usage under a token budget must be refused, got %v", err)
	}
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

// A saved test snapshot is read only through the safe loader during the leak scan: bytes that no
// longer hash to the digest result.json records are refused at import instead of silently scanned
// as trustworthy evidence.
func TestImportRunRefusesACorruptedSavedTestArtifact(t *testing.T) {
	benchDir, manifest, policy := importTestInputs(t)
	results := t.TempDir()
	claimed := sha256.Sum256([]byte("the bytes the run actually kept"))
	resultJSON := `{"case":"c1","run":1,"plan_found":true,"plan_format":"table",` +
		`"catch":{"checked":true,"test_files":["tests/d1.sh"]},` +
		`"test_artifacts":[{"path":"tests/d1.sh","sha256":"` + hex.EncodeToString(claimed[:]) + `"}]}`
	writeImportResult(t, results, resultJSON, importPlanTable, "")
	artifactPath := filepath.Join(results, "c1", "1", "test-artifacts", "tests", "d1.sh")
	if err := os.MkdirAll(filepath.Dir(artifactPath), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(artifactPath, []byte("the bytes after tampering"), 0o644); err != nil {
		t.Fatal(err)
	}
	_, err := ImportRun(results, benchDir, manifest, policy, "baseline", 1, "t1")
	if err == nil || !strings.Contains(err.Error(), "tests/d1.sh") {
		t.Fatalf("a corrupted snapshot must be refused, got %v", err)
	}
}

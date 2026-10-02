package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/alesierraalta/tpp/internal/bench"
	"github.com/alesierraalta/tpp/internal/eval"
)

func TestBenchEvalCLIImportAdjudicateCompareAndVerify(t *testing.T) {
	bin := buildCLI(t)
	root := t.TempDir()
	benchDir := filepath.Join(root, "bench")
	canary := "0123456789abcdef0123456789abcdef"
	for _, caseID := range []string{"c1", "c2"} {
		caseDir := filepath.Join(benchDir, "cases", caseID)
		for _, tree := range []string{"fixture", "fix"} {
			if err := os.MkdirAll(filepath.Join(caseDir, tree), 0o755); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(filepath.Join(caseDir, tree, "source.txt"), []byte(tree), 0o644); err != nil {
				t.Fatal(err)
			}
		}
		key := bench.Key{Schema: 2, ID: caseID, Language: "go", Suite: "go test", Surface: "library", Canary: canary}
		if caseID == "c1" {
			key.Defects = []bench.Defect{{
				ID: "issue-a", File: "src/a.go", Line: 10, Class: "logic", Keywords: []string{"boundary"},
				Description: "boundary handling defect", IssueType: "logic", Domain: "security", Severity: "high",
				SeverityRationale: "security boundary", ExpectedBehavior: "reject invalid input", FailureCondition: "accepts invalid input",
				DetectionCriteria: &bench.DetectionCriteria{Mechanism: "input validation", Proof: "reproduce invalid acceptance"},
				Reproduction:      &bench.Reproduction{Oracle: "none"},
			}}
		} else {
			key.Control = bench.ControlClean
		}
		keyData, err := json.Marshal(key)
		if err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(caseDir, bench.KeyFile), keyData, 0o644); err != nil {
			t.Fatal(err)
		}
	}
	manifest, err := eval.BuildManifest(benchDir, eval.ManifestSpec{
		Benchmark: "TEST", Version: "1.0", Status: "draft", Created: "2026-10-01T00:00:00Z",
		ChangeReason: "test", KeySchema: 2, Cases: []string{"c1", "c2"},
		Domains: []eval.Domain{eval.Security}, CriticalDomains: []eval.Domain{eval.Security},
		DetectionCriteriaVersion: "dc-1",
		MetricConfig:             eval.MetricConfig{WeightedRecallW: 0.5, CILevel: 0.9, EarlyStopCILevel: 0.95, BootstrapResamples: 100, BootstrapSeed: 7, Consolidation: "strict-majority"},
		Replicates:               eval.Replicates{KMin: 1, KTarget: 1, KMax: 1},
		Budgets:                  eval.Budgets{MaxCases: 2, MaxAttemptsPerCase: 1, MaxRetries: 1, MaxTokensPerCaseRun: 100, MaxCostPerCaseRunUSD: 1, MaxCostSuiteUSD: 2, MaxRuntimePerCaseRunSeconds: 60, MaxRuntimeSuiteSeconds: 120, Verdicts: []string{"PASS", "FAIL", "REVIEW"}},
		Seeds:                    map[string]int64{"case_order": 7}, Canary: canary,
	})
	if err != nil {
		t.Fatal(err)
	}
	manifest, err = manifest.Seal()
	if err != nil {
		t.Fatal(err)
	}
	policy, err := (eval.Policy{Name: "test", Created: "2026-10-01T00:00:00Z"}).Seal()
	if err != nil {
		t.Fatal(err)
	}
	manifestPath, policyPath := filepath.Join(root, "manifest.json"), filepath.Join(root, "policy.json")
	writeCanonical := func(path string, value any) {
		t.Helper()
		data, err := eval.CanonicalJSON(value)
		if err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, data, 0o644); err != nil {
			t.Fatal(err)
		}
	}
	writeCanonical(manifestPath, manifest)
	writeCanonical(policyPath, policy)

	for _, side := range []string{"baseline", "candidate"} {
		resultsDir := filepath.Join(root, side+"-results")
		if err := os.MkdirAll(resultsDir, 0o755); err != nil {
			t.Fatal(err)
		}
		aggregate := bench.Aggregate{Model: "model", Provenance: bench.Provenance{
			Model: "model", Runner: "pi", AgentConfig: bench.ConfigBench,
			SkillsDigest: "sha256:skills", Environment: "linux/amd64",
		}}
		aggregateData, err := json.Marshal(aggregate)
		if err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(resultsDir, "aggregate.json"), aggregateData, 0o644); err != nil {
			t.Fatal(err)
		}
		for _, caseID := range []string{"c1", "c2"} {
			caseRunDir := filepath.Join(resultsDir, caseID, "1")
			if err := os.MkdirAll(caseRunDir, 0o755); err != nil {
				t.Fatal(err)
			}
			result := bench.Result{Case: caseID, Run: 1, PlanFound: true, PlanFormat: bench.FormatTable, CostUSD: 0.1, Seconds: 2}
			if caseID == "c2" {
				// c2 records an explicit measured zero; c1's usage stays unknown.
				measuredZero := 0
				result.Tokens = &measuredZero
			}
			resultData, err := json.Marshal(result)
			if err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(filepath.Join(caseRunDir, "result.json"), resultData, 0o644); err != nil {
				t.Fatal(err)
			}
			plan := "## Findings\n\n| Id | Finding | Type | Location | Evidence | Severity |\n|---|---|---|---|---|---|\n"
			if caseID == "c1" {
				plan += "| F1 | observed concern | boundary | src/a.go:10 | - | high |\n"
			}
			if err := os.WriteFile(filepath.Join(caseRunDir, "test-plan.md"), []byte(plan), 0o644); err != nil {
				t.Fatal(err)
			}
		}
	}

	baselineEval, candidateEval := filepath.Join(root, "baseline-eval"), filepath.Join(root, "candidate-eval")
	for _, args := range [][]string{
		{"bench", "eval", "import", "--results", filepath.Join(root, "baseline-results"), "--manifest", manifestPath, "--policy", policyPath, "--harness", "baseline", "--replicate", "1", "--out", baselineEval, "--bench-dir", benchDir},
		{"bench", "eval", "import", "--results", filepath.Join(root, "candidate-results"), "--manifest", manifestPath, "--policy", policyPath, "--harness", "candidate", "--replicate", "1", "--out", candidateEval, "--bench-dir", benchDir},
	} {
		if out, code := runCLI(t, bin, args...); code != 0 {
			t.Fatalf("import exited %d:\n%s", code, out)
		}
	}
	pendingOut, pendingCode := runCLI(t, bin, "bench", "eval", "pending", "--eval", filepath.Join(baselineEval, "run-1"))
	if pendingCode != 0 || !strings.Contains(pendingOut, "src/a.go:10") || !strings.Contains(pendingOut, "observed concern") {
		t.Fatalf("pending output = %d:\n%s", pendingCode, pendingOut)
	}
	for _, runDir := range []string{filepath.Join(baselineEval, "run-1"), filepath.Join(candidateEval, "run-1")} {
		if out, code := runCLI(t, bin, "bench", "eval", "adjudicate", "--eval", runDir, "--case", "c1", "--finding", "c1-f1", "--outcome", "FP", "--by", "reviewer", "--reason", "verified false positive"); code != 0 {
			t.Fatalf("adjudicate exited %d:\n%s", code, out)
		}
		if out, code := runCLI(t, bin, "bench", "eval", "close", "--eval", runDir); code != 0 {
			t.Fatalf("close exited %d:\n%s", code, out)
		}
	}
	// Unknown usage (c1) must survive import and close as unknown while c2's measured zero stays zero.
	candidateRunJSON, err := os.ReadFile(filepath.Join(candidateEval, "run-1", "run.json"))
	if err != nil {
		t.Fatal(err)
	}
	var closedRun struct {
		CaseResources map[string]map[string]json.RawMessage `json:"case_resources"`
		Data          struct {
			Cases []struct {
				Case   string
				Tokens json.RawMessage
			}
		} `json:"data"`
	}
	if err := json.Unmarshal(candidateRunJSON, &closedRun); err != nil {
		t.Fatal(err)
	}
	if _, present := closedRun.CaseResources["c1"]["tokens"]; present {
		t.Fatalf("unknown token usage recorded as a value: %s", closedRun.CaseResources["c1"])
	}
	if got := string(closedRun.CaseResources["c2"]["tokens"]); got != "0" {
		t.Fatalf("measured zero tokens = %s, want 0", got)
	}
	if len(closedRun.Data.Cases) != 2 {
		t.Fatalf("closed cases = %d, want 2", len(closedRun.Data.Cases))
	}
	for _, caseResult := range closedRun.Data.Cases {
		want := "null"
		if caseResult.Case == "c2" {
			want = "0"
		}
		if got := string(caseResult.Tokens); got != want {
			t.Fatalf("closed %s tokens = %s, want %s", caseResult.Case, got, want)
		}
	}
	metricsBytes, err := os.ReadFile(filepath.Join(candidateEval, "run-1", "metrics.json"))
	if err != nil {
		t.Fatal(err)
	}
	var metrics metricsArtifact
	if err := json.Unmarshal(metricsBytes, &metrics); err != nil {
		t.Fatal(err)
	}
	if metrics.Metrics.Tokens != nil {
		t.Fatalf("run metrics tokens = %d, want nil while a case usage is unknown", *metrics.Metrics.Tokens)
	}
	baselineRun := filepath.Join(baselineEval, "run-1")
	if out, code := runCLI(t, bin, "bench", "eval", "reopen", "--eval", baselineRun, "--case", "c1", "--finding", "c1-f1", "--by", "reviewer2", "--reason", "new evidence"); code != 0 {
		t.Fatalf("reopen exited %d:\n%s", code, out)
	}
	invalidated, err := os.ReadFile(filepath.Join(baselineRun, "metrics.json"))
	if err != nil || !strings.Contains(string(invalidated), `"invalidated": true`) {
		t.Fatalf("metrics were not invalidated after reopen: %v %s", err, invalidated)
	}
	if out, code := runCLI(t, bin, "bench", "eval", "adjudicate", "--eval", baselineRun, "--case", "c1", "--finding", "c1-f1", "--outcome", "FP", "--by", "reviewer2", "--reason", "rechecked"); code != 0 {
		t.Fatalf("readjudicate exited %d:\n%s", code, out)
	}
	if out, code := runCLI(t, bin, "bench", "eval", "close", "--eval", baselineRun); code != 0 {
		t.Fatalf("reclose exited %d:\n%s", code, out)
	}

	comparisonDir := filepath.Join(root, "comparison")
	out, code := runCLI(t, bin, "bench", "eval", "compare", "--baseline", baselineEval, "--candidate", candidateEval, "--manifest", manifestPath, "--policy", policyPath, "--out", comparisonDir)
	if code != 0 && code != 1 && code != 3 && code != 4 {
		t.Fatalf("compare exited %d:\n%s", code, out)
	}
	decisionBytes, err := os.ReadFile(filepath.Join(comparisonDir, "decision.json"))
	if err != nil {
		t.Fatal(err)
	}
	var decision eval.ComparisonDecision
	if err := json.Unmarshal(decisionBytes, &decision); err != nil {
		t.Fatalf("parse decision.json: %v", err)
	}
	wantExit := map[string]int{"PASS": 0, "FAIL": 1, "REVIEW": 3, "NO_DECISION": 4}[decision.Verdict]
	if decision.Verdict == "" || code != wantExit || !strings.Contains(out, decision.Verdict) {
		t.Fatalf("comparison verdict %q exit %d (want %d):\n%s", decision.Verdict, code, wantExit, out)
	}
	if out, code := runCLI(t, bin, "bench", "eval", "verify", "--comparison", comparisonDir); code != 0 {
		t.Fatalf("verify exited %d:\n%s", code, out)
	}
	compareReject := func(label, candidateDir, want string) {
		t.Helper()
		out, code := runCLI(t, bin, "bench", "eval", "compare", "--baseline", baselineEval, "--candidate", candidateDir, "--manifest", manifestPath, "--policy", policyPath, "--out", filepath.Join(root, label))
		if code != 1 || !strings.Contains(out, want) {
			t.Fatalf("compare %s = %d, want rejection containing %q:\n%s", label, code, want, out)
		}
	}
	for _, name := range []string{"states.json", "metrics.json"} {
		path := filepath.Join(candidateEval, "run-1", name)
		original, err := os.ReadFile(path)
		if err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte("{}"), 0o644); err != nil {
			t.Fatal(err)
		}
		compareReject("tampered-"+name, candidateEval, name)
		if out, code := runCLI(t, bin, "bench", "eval", "verify", "--comparison", comparisonDir); code != 1 || !strings.Contains(out, name) {
			t.Fatalf("verify accepted tampered %s: %d:\n%s", name, code, out)
		}
		if err := os.WriteFile(path, original, 0o644); err != nil {
			t.Fatal(err)
		}
	}
	candidateRunFile := filepath.Join(candidateEval, "run-1", "run.json")
	runJSON, err := os.ReadFile(candidateRunFile)
	if err != nil {
		t.Fatal(err)
	}
	var runMetadata map[string]any
	if err := json.Unmarshal(runJSON, &runMetadata); err != nil {
		t.Fatal(err)
	}
	runMetadata["data"].(map[string]any)["Cases"] = []any{}
	tamperedRunJSON, err := json.Marshal(runMetadata)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(candidateRunFile, tamperedRunJSON, 0o644); err != nil {
		t.Fatal(err)
	}
	if out, code := runCLI(t, bin, "bench", "eval", "verify", "--comparison", comparisonDir); code != 1 || !strings.Contains(out, "run.json") {
		t.Fatalf("verify accepted empty run data: %d:\n%s", code, out)
	}
	compareReject("tampered-data-comparison", candidateEval, "run data")
	if err := os.WriteFile(candidateRunFile, runJSON, 0o644); err != nil {
		t.Fatal(err)
	}

	var issueMetadata map[string]any
	if err := json.Unmarshal(runJSON, &issueMetadata); err != nil {
		t.Fatal(err)
	}
	caseResults := issueMetadata["data"].(map[string]any)["Cases"].([]any)
	changedIssueState := false
	for _, value := range caseResults {
		result := value.(map[string]any)
		issueStates, ok := result["IssueStates"].(map[string]any)
		if !ok || len(issueStates) == 0 {
			continue
		}
		for issueID, state := range issueStates {
			if state == string(eval.IssueTP) {
				issueStates[issueID] = string(eval.IssueFN)
			} else {
				issueStates[issueID] = string(eval.IssueTP)
			}
			changedIssueState = true
			break
		}
		if changedIssueState {
			break
		}
	}
	if !changedIssueState {
		t.Fatal("fixture has no issue state to tamper")
	}
	tamperedRunJSON, err = json.Marshal(issueMetadata)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(candidateRunFile, tamperedRunJSON, 0o644); err != nil {
		t.Fatal(err)
	}
	compareReject("tampered-issue-state", candidateEval, "run data")
	if err := os.WriteFile(candidateRunFile, runJSON, 0o644); err != nil {
		t.Fatal(err)
	}

	missingLedgerEval := filepath.Join(root, "missing-ledger-eval")
	missingLedgerRun := filepath.Join(missingLedgerEval, "run-1")
	if err := os.MkdirAll(missingLedgerRun, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(missingLedgerRun, "run.json"), runJSON, 0o644); err != nil {
		t.Fatal(err)
	}
	compareReject("missing-ledger", missingLedgerEval, "case ledgers")

	eventsPath := filepath.Join(candidateEval, "run-1", "c1", "events.jsonl")
	events, err := os.ReadFile(eventsPath)
	if err != nil {
		t.Fatal(err)
	}
	lines := strings.Split(strings.TrimSpace(string(events)), "\n")
	closeEvent := -1
	for i, line := range lines {
		var event eval.Event
		if err := json.Unmarshal([]byte(line), &event); err != nil {
			t.Fatal(err)
		}
		if event.Entity == eval.EntityCaseRun && event.NewState == string(eval.CaseRunClosed) {
			closeEvent = i
			break
		}
	}
	if closeEvent < 0 {
		t.Fatal("fixture has no case-run close event")
	}
	if err := os.WriteFile(eventsPath, []byte(strings.Join(lines[:closeEvent], "\n")+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	compareReject("pending-ledger", candidateEval, "not closed")
	if err := os.WriteFile(eventsPath, events, 0o644); err != nil {
		t.Fatal(err)
	}

	decisionFile := filepath.Join(comparisonDir, "decision.json")
	file, err := os.OpenFile(decisionFile, os.O_APPEND|os.O_WRONLY, 0)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := file.WriteString(" "); err != nil {
		t.Fatal(err)
	}
	if err := file.Close(); err != nil {
		t.Fatal(err)
	}
	if out, code := runCLI(t, bin, "bench", "eval", "verify", "--comparison", comparisonDir); code != 1 || !strings.Contains(out, "decision.json") {
		t.Fatalf("tampered decision verify = %d:\n%s", code, out)
	}
}

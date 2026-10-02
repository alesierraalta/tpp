package main

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"

	"github.com/alesierraalta/tpp/internal/bench"
	"github.com/alesierraalta/tpp/internal/eval"
)

// writeEvalCLIFixture writes a two-case benchmark, its sealed manifest and sealed policy, and
// returns the benchmark, manifest and policy paths.
func writeEvalCLIFixture(t *testing.T, root string) (benchDir, manifestPath, policyPath string) {
	t.Helper()
	benchDir = filepath.Join(root, "bench")
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
	manifestPath, policyPath = filepath.Join(root, "manifest.json"), filepath.Join(root, "policy.json")
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
	return benchDir, manifestPath, policyPath
}

func TestBenchEvalCLIImportAdjudicateCompareAndVerify(t *testing.T) {
	bin := buildCLI(t)
	root := t.TempDir()
	benchDir, manifestPath, policyPath := writeEvalCLIFixture(t, root)

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

// The ledger's order holds end to end: a manifest-bound run refuses to close while a primary
// finding lacks its reproduction confirmation; `confirm` replays the saved agent test bytes with
// the local catch oracle (a fake fixture driven by sh only — no model, no runner, no API); the
// replayed outcome, not the reading's own catch claim, decides the primary's TP/PD state and the
// reproducibility metric; a duplicate confirmation appends nothing; the saved snapshots join the
// close input digests, so verify rejects a tampered snapshot or result; and a saved test carrying
// the canary makes the import record a leak that confirm refuses.
func TestBenchEvalConfirmBeforeCloseDrivesReproAndVerification(t *testing.T) {
	bin := buildCLI(t)
	home := t.TempDir() // isolated HOME: no operator credentials are reachable from the built CLI
	root := t.TempDir()
	benchDir := filepath.Join(root, "bench")
	canary := "0123456789abcdef0123456789abcdef"
	writeFile := func(path string, data []byte) {
		t.Helper()
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, data, 0o644); err != nil {
			t.Fatal(err)
		}
	}
	writeJSON := func(path string, value any) {
		t.Helper()
		data, err := json.Marshal(value)
		if err != nil {
			t.Fatal(err)
		}
		writeFile(path, data)
	}

	caseDir := filepath.Join(benchDir, "cases", "c1")
	// The suite leaves a sentinel wherever it runs, so a refused confirm can prove it never reached
	// the replay command and a successful one can prove it did.
	sentinel := filepath.Join(root, "replay-sentinel")
	for rel, content := range map[string]string{
		"fixture/src.txt":      "D1=bug\n",
		"fixture/run-tests.sh": "for f in tests/*.sh; do [ -e \"$f\" ] || continue; sh \"$f\" || exit 1; done\ntouch " + sentinel + "\nexit 0\n",
		"fix/all/src.txt":      "D1=ok\n",
	} {
		writeFile(filepath.Join(caseDir, filepath.FromSlash(rel)), []byte(content))
	}
	key := bench.Key{
		Schema: 2, ID: "c1", Language: "go", Suite: "sh run-tests.sh", Surface: "library", Canary: canary,
		Defects: []bench.Defect{{
			ID: "issue-a", File: "src.txt", Line: 1, Class: "logic", Keywords: []string{"boundary"},
			Description: "the counter accepts a boundary input", IssueType: "logic", Domain: "security",
			Severity: "high", SeverityRationale: "security boundary", ExpectedBehavior: "reject invalid input",
			FailureCondition:  "accepts invalid input",
			DetectionCriteria: &bench.DetectionCriteria{Mechanism: "input validation", Proof: "reproduce invalid acceptance"},
			Reproduction:      &bench.Reproduction{Applies: true, Oracle: "catch", Attempts: 1},
		}},
	}
	writeJSON(filepath.Join(caseDir, bench.KeyFile), key)
	manifest, err := eval.BuildManifest(benchDir, eval.ManifestSpec{
		Benchmark: "TEST", Version: "1.0", Status: "draft", Created: "2026-10-01T00:00:00Z",
		ChangeReason: "test", KeySchema: 2, Cases: []string{"c1"},
		Domains: []eval.Domain{eval.Security}, CriticalDomains: []eval.Domain{eval.Security},
		DetectionCriteriaVersion: "dc-1",
		MetricConfig:             eval.MetricConfig{WeightedRecallW: 0.5, CILevel: 0.9, EarlyStopCILevel: 0.95, BootstrapResamples: 100, BootstrapSeed: 7, Consolidation: "strict-majority"},
		Replicates:               eval.Replicates{KMin: 1, KTarget: 1, KMax: 1},
		Budgets:                  eval.Budgets{MaxCases: 1, MaxAttemptsPerCase: 1, MaxRetries: 1, MaxTokensPerCaseRun: 100, MaxCostPerCaseRunUSD: 1, MaxCostSuiteUSD: 2, MaxRuntimePerCaseRunSeconds: 60, MaxRuntimeSuiteSeconds: 120, Verdicts: []string{"PASS", "FAIL", "REVIEW"}},
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
	for path, value := range map[string]any{manifestPath: manifest, policyPath: policy} {
		data, err := eval.CanonicalJSON(value)
		if err != nil {
			t.Fatal(err)
		}
		writeFile(path, data)
	}

	plan := "## Findings\n\n| Id | Finding | Type | Location | Evidence | Severity |\n|---|---|---|---|---|---|\n" +
		"| F1 | the counter accepts bad input | boundary | src.txt:1 | E1 | high |\n\n" +
		"## Evidence ledger\n\n| Id | Claim |\n|---|---|\n| E1 | src.txt:1 accepts the boundary input |\n"
	// The reading claims its saved test catches the defect; the saved bytes are green on every
	// variant, so the confirmation replay must overturn the claim to NOT_REPRODUCED.
	testBytes := []byte("grep -q D1= src.txt\n")
	writeResults := func(side string, artifactBytes []byte) string {
		t.Helper()
		resultsDir := filepath.Join(root, side+"-results")
		valid := true
		tokens := 5
		aggregate := bench.Aggregate{Model: "model", Provenance: bench.Provenance{
			Model: "model", Runner: "pi", AgentConfig: bench.ConfigBench, SkillsDigest: "sha256:skills",
			Environment: "linux/amd64", ManifestSHA256: manifest.ManifestSHA256,
			InstrumentValid: &valid, ExecutionComplete: &valid, BudgetStatus: "supported",
		}}
		artifactSum := sha256.Sum256(artifactBytes)
		result := bench.Result{
			Case: "c1", Run: 1, PlanFound: true, PlanFormat: bench.FormatTable, CostUSD: 0.1, Seconds: 2, Tokens: &tokens,
			Catch: bench.CatchResult{
				Checked: true, TestFiles: []string{"tests/d1.sh"}, AllGreen: true,
				Caught: map[string]bool{"issue-a": true}, Attempts: map[string]int{"issue-a": 1},
			},
			TestArtifacts: []bench.TestArtifact{{Path: "tests/d1.sh", SHA256: hex.EncodeToString(artifactSum[:])}},
		}
		writeJSON(filepath.Join(resultsDir, "aggregate.json"), aggregate)
		writeJSON(filepath.Join(resultsDir, "c1", "1", "result.json"), result)
		writeFile(filepath.Join(resultsDir, "c1", "1", "test-plan.md"), []byte(plan))
		writeFile(filepath.Join(resultsDir, "c1", "1", "test-artifacts", "tests", "d1.sh"), artifactBytes)
		return resultsDir
	}

	evalDirs := map[string]string{}
	for _, side := range []string{"baseline", "candidate"} {
		resultsDir := writeResults(side, testBytes)
		evalDir := filepath.Join(root, side+"-eval")
		if out, code := runCLIWithHome(t, home, bin, "bench", "eval", "import",
			"--results", resultsDir, "--manifest", manifestPath, "--policy", policyPath,
			"--harness", side, "--replicate", "1", "--out", evalDir, "--bench-dir", benchDir); code != 0 {
			t.Fatalf("import %s exited %d:\n%s", side, code, out)
		}
		runDir := filepath.Join(evalDir, "run-1")
		if out, code := runCLIWithHome(t, home, bin, "bench", "eval", "adjudicate",
			"--eval", runDir, "--case", "c1", "--finding", "c1-f1", "--issue", "issue-a",
			"--c1", "true", "--c3", "true", "--c4-shows", "true",
			"--by", "reviewer", "--reason", "the row names the planted defect"); code != 0 {
			t.Fatalf("adjudicate %s exited %d:\n%s", side, code, out)
		}
		// F2: every existing event chain is verified before any replay, append, or save — a corrupt
		// chain fails confirm with the run byte-identical on disk and no replay executed.
		if side == "baseline" {
			chainPath := filepath.Join(runDir, "c1", "events.jsonl")
			runBeforeChain, err := os.ReadFile(filepath.Join(runDir, "run.json"))
			if err != nil {
				t.Fatal(err)
			}
			eventsRaw, err := os.ReadFile(chainPath)
			if err != nil {
				t.Fatal(err)
			}
			if _, err := os.Stat(sentinel); !os.IsNotExist(err) {
				t.Fatalf("replay sentinel exists before the first confirm: %v", err)
			}
			chainLines := strings.Split(strings.TrimRight(string(eventsRaw), "\n"), "\n")
			var first eval.Event
			if err := json.Unmarshal([]byte(chainLines[0]), &first); err != nil {
				t.Fatal(err)
			}
			corrupt := strings.Replace(chainLines[0], `"`+first.Hash+`"`, `"sha256:0000000000000000000000000000000000000000000000000000000000000000"`, 1)
			if corrupt == chainLines[0] {
				t.Fatal("fixture event has no hash to corrupt")
			}
			corruptBytes := []byte(strings.Join(append([]string{corrupt}, chainLines[1:]...), "\n") + "\n")
			if err := os.WriteFile(chainPath, corruptBytes, 0o644); err != nil {
				t.Fatal(err)
			}
			if out, code := runCLIWithHome(t, home, bin, "bench", "eval", "confirm",
				"--eval", runDir, "--bench-dir", benchDir); code != 1 || !strings.Contains(out, "event chain") {
				t.Fatalf("confirm with a corrupt event chain = %d, want refusal:\n%s", code, out)
			}
			runAfterChain, err := os.ReadFile(filepath.Join(runDir, "run.json"))
			if err != nil {
				t.Fatal(err)
			}
			if string(runAfterChain) != string(runBeforeChain) {
				t.Fatal("refused confirm mutated the run record")
			}
			eventsCorrupt, err := os.ReadFile(chainPath)
			if err != nil {
				t.Fatal(err)
			}
			if string(eventsCorrupt) != string(corruptBytes) {
				t.Fatal("refused confirm rewrote the event log")
			}
			if _, err := os.Stat(sentinel); !os.IsNotExist(err) {
				t.Fatalf("refused confirm still ran the replay: %v", err)
			}
			if err := os.WriteFile(chainPath, eventsRaw, 0o644); err != nil {
				t.Fatal(err)
			}
		}
		// Close before confirm must refuse, naming the case and issue, and write no completed state.
		out, code := runCLIWithHome(t, home, bin, "bench", "eval", "close", "--eval", runDir)
		if code != 1 || !strings.Contains(out, "c1") || !strings.Contains(out, "issue-a") || !strings.Contains(out, "confirm") {
			t.Fatalf("close before confirm = %d, want a refusal naming case and issue:\n%s", code, out)
		}
		if _, err := os.Stat(filepath.Join(runDir, "states.json")); !os.IsNotExist(err) {
			t.Fatalf("refused close wrote states.json: %v", err)
		}
		runBytes, err := os.ReadFile(filepath.Join(runDir, "run.json"))
		if err != nil {
			t.Fatal(err)
		}
		if !strings.Contains(string(runBytes), `"state": "ADJUDICATING"`) {
			t.Fatalf("refused close changed the run state:\n%s", runBytes)
		}
		if out, code := runCLIWithHome(t, home, bin, "bench", "eval", "confirm",
			"--eval", runDir, "--bench-dir", benchDir); code != 0 || !strings.Contains(out, "confirmed 1") {
			t.Fatalf("confirm %s exited %d:\n%s", side, code, out)
		}
		if _, err := os.Stat(sentinel); err != nil {
			t.Fatalf("confirm did not run the local replay: %v", err)
		}
		eventsPath := filepath.Join(runDir, "c1", "events.jsonl")
		eventsBefore, err := os.ReadFile(eventsPath)
		if err != nil {
			t.Fatal(err)
		}
		if !strings.Contains(string(eventsBefore), `"event":"confirm_reproduction"`) {
			t.Fatalf("confirm event missing from the ledger:\n%s", eventsBefore)
		}
		// Re-invoking with nothing missing is a no-op: no replay, no appended events.
		if out, code := runCLIWithHome(t, home, bin, "bench", "eval", "confirm",
			"--eval", runDir, "--bench-dir", benchDir); code != 0 || !strings.Contains(out, "0 updated") {
			t.Fatalf("repeat confirm exited %d:\n%s", code, out)
		}
		eventsAfter, err := os.ReadFile(eventsPath)
		if err != nil {
			t.Fatal(err)
		}
		if string(eventsBefore) != string(eventsAfter) {
			t.Fatal("repeat confirm appended events")
		}
		// Corrupted or moved source inputs must fail close BEFORE any mutation: the run stays
		// ADJUDICATING, the event log stays byte-identical, no states/metrics artifact appears, and
		// restoring the input makes the ordinary close succeed without hand-editing metadata.
		if side == "baseline" {
			refusedClose := func(label, want, target string, mutate func(original []byte) []byte) {
				t.Helper()
				runBefore, err := os.ReadFile(filepath.Join(runDir, "run.json"))
				if err != nil {
					t.Fatal(err)
				}
				eventsBeforeClose, err := os.ReadFile(eventsPath)
				if err != nil {
					t.Fatal(err)
				}
				original, err := os.ReadFile(target)
				if err != nil {
					t.Fatal(err)
				}
				if err := os.WriteFile(target, mutate(original), 0o644); err != nil {
					t.Fatal(err)
				}
				out, code := runCLIWithHome(t, home, bin, "bench", "eval", "close", "--eval", runDir)
				if code != 1 || !strings.Contains(out, want) {
					t.Fatalf("%s: close = %d, want refusal containing %q:\n%s", label, code, want, out)
				}
				runAfter, err := os.ReadFile(filepath.Join(runDir, "run.json"))
				if err != nil {
					t.Fatal(err)
				}
				if string(runAfter) != string(runBefore) {
					t.Fatalf("%s: refused close mutated the run record; it must stay ADJUDICATING", label)
				}
				eventsAfterClose, err := os.ReadFile(eventsPath)
				if err != nil {
					t.Fatal(err)
				}
				if string(eventsAfterClose) != string(eventsBeforeClose) {
					t.Fatalf("%s: refused close rewrote the event log", label)
				}
				for _, name := range []string{"states.json", "metrics.json"} {
					if _, err := os.Stat(filepath.Join(runDir, name)); !os.IsNotExist(err) {
						t.Fatalf("%s: refused close wrote %s: %v", label, name, err)
					}
				}
				if err := os.WriteFile(target, original, 0o644); err != nil {
					t.Fatal(err)
				}
			}
			refusedClose("corrupted snapshot", "tests/d1.sh",
				filepath.Join(resultsDir, "c1", "1", "test-artifacts", "tests", "d1.sh"),
				func([]byte) []byte { return []byte("grep -q D1=ok src.txt\n") })
			// F1: the confirmation binds to the source result it was recorded against; moving the
			// source (metadata only — snapshot and catch claim unchanged) must not rebind it.
			refusedClose("stale confirmation binding", "confirmation binding",
				filepath.Join(resultsDir, "c1", "1", "result.json"),
				func(original []byte) []byte {
					edited := strings.Replace(string(original), `"seconds":2`, `"seconds":99`, 1)
					if edited == string(original) {
						t.Fatal("fixture result has no seconds to edit")
					}
					return []byte(edited)
				})
		}
		if out, code := runCLIWithHome(t, home, bin, "bench", "eval", "close", "--eval", runDir); code != 0 {
			t.Fatalf("close after confirm exited %d:\n%s", code, out)
		}
		// Confirmation lives strictly before close: a completed run can no longer record one.
		if out, code := runCLIWithHome(t, home, bin, "bench", "eval", "confirm",
			"--eval", runDir, "--bench-dir", benchDir); code != 1 || !strings.Contains(out, "ADJUDICATING") {
			t.Fatalf("confirm after close = %d, want refusal to a completed run:\n%s", code, out)
		}
		evalDirs[side] = evalDir
	}

	// The confirmation replay, not the reading's C5 claim, decided the primary: PD with a
	// NOT_REPRODUCED outcome (a TP would mean the unconfirmed admission fold survived).
	closedBytes, err := os.ReadFile(filepath.Join(evalDirs["candidate"], "run-1", "run.json"))
	if err != nil {
		t.Fatal(err)
	}
	var closed struct {
		Data struct {
			Cases []struct {
				Case          string
				IssueStates   map[string]string
				FindingStates map[string]string
				Repro         map[string]string
			}
		}
	}
	if err := json.Unmarshal(closedBytes, &closed); err != nil {
		t.Fatal(err)
	}
	if len(closed.Data.Cases) != 1 {
		t.Fatalf("closed cases = %d, want 1", len(closed.Data.Cases))
	}
	closedCase := closed.Data.Cases[0]
	if closedCase.IssueStates["issue-a"] != string(eval.IssuePD) || closedCase.FindingStates["c1-f1"] != string(eval.FindingPDFinding) {
		t.Fatalf("confirmation did not downgrade the primary: issues %v findings %v", closedCase.IssueStates, closedCase.FindingStates)
	}
	if closedCase.Repro["c1-f1"] != string(eval.NotReproduced) {
		t.Fatalf("repro outcome = %q, want NOT_REPRODUCED from the replay", closedCase.Repro["c1-f1"])
	}
	metricsBytes, err := os.ReadFile(filepath.Join(evalDirs["candidate"], "run-1", "metrics.json"))
	if err != nil {
		t.Fatal(err)
	}
	var metrics metricsArtifact
	if err := json.Unmarshal(metricsBytes, &metrics); err != nil {
		t.Fatal(err)
	}
	if metrics.Metrics.TP != 0 || metrics.Metrics.PD != 1 || metrics.Metrics.PDFinding != 1 {
		t.Fatalf("run metrics TP=%d PD=%d PDFinding=%d, want 0/1/1", metrics.Metrics.TP, metrics.Metrics.PD, metrics.Metrics.PDFinding)
	}
	if metrics.Metrics.Reproducibility == nil || *metrics.Metrics.Reproducibility != 0 {
		t.Fatalf("reproducibility = %v, want 0 over a failed confirmation", metrics.Metrics.Reproducibility)
	}

	comparisonDir := filepath.Join(root, "comparison")
	out, code := runCLIWithHome(t, home, bin, "bench", "eval", "compare",
		"--baseline", evalDirs["baseline"], "--candidate", evalDirs["candidate"],
		"--manifest", manifestPath, "--policy", policyPath, "--out", comparisonDir)
	if code != 0 && code != 1 && code != 3 && code != 4 {
		t.Fatalf("compare exited %d:\n%s", code, out)
	}
	if _, err := os.Stat(filepath.Join(comparisonDir, "decision.json")); err != nil {
		t.Fatalf("compare wrote no decision.json: %v", err)
	}
	if out, code := runCLIWithHome(t, home, bin, "bench", "eval", "verify", "--comparison", comparisonDir); code != 0 {
		t.Fatalf("verify exited %d:\n%s", code, out)
	}

	verifyRejected := func(label string, want string) {
		t.Helper()
		out, code := runCLIWithHome(t, home, bin, "bench", "eval", "verify", "--comparison", comparisonDir)
		if code != 1 || !strings.Contains(out, want) {
			t.Fatalf("verify after %s = %d, want rejection containing %q:\n%s", label, code, want, out)
		}
	}
	// The saved snapshots are close inputs: bytes that no longer hash to result.json's digest are
	// rejected by name before anything re-derives the decision.
	artifactFile := filepath.Join(root, "candidate-results", "c1", "1", "test-artifacts", "tests", "d1.sh")
	originalArtifact, err := os.ReadFile(artifactFile)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(artifactFile, []byte("grep -q D1=ok src.txt\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	verifyRejected("a tampered snapshot", "tests/d1.sh")
	if err := os.WriteFile(artifactFile, originalArtifact, 0o644); err != nil {
		t.Fatal(err)
	}
	if out, code := runCLIWithHome(t, home, bin, "bench", "eval", "verify", "--comparison", comparisonDir); code != 0 {
		t.Fatalf("verify after restoring the snapshot exited %d:\n%s", code, out)
	}
	resultFile := filepath.Join(root, "candidate-results", "c1", "1", "result.json")
	originalResult, err := os.ReadFile(resultFile)
	if err != nil {
		t.Fatal(err)
	}
	tamperedResult := strings.Replace(string(originalResult), `"issue-a":true`, `"issue-a":false`, 1)
	if tamperedResult == string(originalResult) {
		t.Fatal("fixture result has no catch claim to tamper")
	}
	if err := os.WriteFile(resultFile, []byte(tamperedResult), 0o644); err != nil {
		t.Fatal(err)
	}
	// The result's digest is a recorded close input, so the tamper is rejected by name before the
	// decision is re-derived.
	verifyRejected("a tampered result", "result.json")
	if err := os.WriteFile(resultFile, originalResult, 0o644); err != nil {
		t.Fatal(err)
	}
	if out, code := runCLIWithHome(t, home, bin, "bench", "eval", "verify", "--comparison", comparisonDir); code != 0 {
		t.Fatalf("verify after restoring the result exited %d:\n%s", code, out)
	}

	// F1 completed path: a stale confirmation binding cannot be laundered by regenerating the
	// auxiliary digest artifacts — compare re-derives them consistently and still refuses.
	candidateRun := filepath.Join(evalDirs["candidate"], "run-1")
	completedStored, err := eval.LoadRun(candidateRun)
	if err != nil {
		t.Fatal(err)
	}
	editedResult := strings.Replace(string(originalResult), `"seconds":2`, `"seconds":99`, 1)
	if editedResult == string(originalResult) {
		t.Fatal("fixture result has no seconds to edit")
	}
	if err := os.WriteFile(resultFile, []byte(editedResult), 0o644); err != nil {
		t.Fatal(err)
	}
	statesPath := filepath.Join(candidateRun, "states.json")
	metricsPath := filepath.Join(candidateRun, "metrics.json")
	originalStates, err := os.ReadFile(statesPath)
	if err != nil {
		t.Fatal(err)
	}
	originalMetrics, err := os.ReadFile(metricsPath)
	if err != nil {
		t.Fatal(err)
	}
	inputs, err := closeInputDigests(completedStored, candidateRun)
	if err != nil {
		t.Fatal(err)
	}
	regeneratedStates := statesArtifact{InputDigests: inputs, Cases: make(map[string]caseStates, len(completedStored.CaseRuns))}
	for _, caseID := range sortedCaseIDs(completedStored.CaseRuns) {
		caseRun := completedStored.CaseRuns[caseID]
		state := caseStates{Issues: make(map[string]eval.IssueState), Findings: make(map[string]eval.FindingState), Primary: make(map[string]string)}
		for _, issue := range caseRun.Issues {
			state.Issues[issue.ID] = caseRun.IssueState(issue.ID)
			state.Primary[issue.ID] = caseRun.Primary(issue.ID)
		}
		for _, finding := range caseRun.Findings {
			state.Findings[finding.ID] = caseRun.FindingState(finding.ID)
		}
		regeneratedStates.Cases[caseID] = state
	}
	if err := writeEvalJSON(statesPath, regeneratedStates); err != nil {
		t.Fatal(err)
	}
	statesDigest, err := eval.Digest(statesPath)
	if err != nil {
		t.Fatal(err)
	}
	regeneratedMetrics := metricsArtifact{InputDigests: cloneDigests(inputs), Metrics: eval.ComputeRun(completedStored.Record.Data, completedStored.WeightedRecallW)}
	regeneratedMetrics.InputDigests[filepath.Join(candidateRun, "states.json")] = statesDigest
	if err := writeEvalJSON(metricsPath, regeneratedMetrics); err != nil {
		t.Fatal(err)
	}
	if out, code := runCLIWithHome(t, home, bin, "bench", "eval", "compare",
		"--baseline", evalDirs["baseline"], "--candidate", evalDirs["candidate"],
		"--manifest", manifestPath, "--policy", policyPath, "--out", filepath.Join(root, "rebound-comparison")); code != 1 || !strings.Contains(out, "confirmation binding") {
		t.Fatalf("compare with regenerated auxiliary artifacts = %d, want stale-binding refusal:\n%s", code, out)
	}
	for path, original := range map[string][]byte{resultFile: originalResult, statesPath: originalStates, metricsPath: originalMetrics} {
		if err := os.WriteFile(path, original, 0o644); err != nil {
			t.Fatal(err)
		}
	}

	// A saved test carrying the canary is a ground-truth leak the import records and confirm refuses.
	leakyResults := writeResults("leaky", []byte("grep -q "+canary+" src.txt\n"))
	leakEval := filepath.Join(root, "leaky-eval")
	if out, code := runCLIWithHome(t, home, bin, "bench", "eval", "import",
		"--results", leakyResults, "--manifest", manifestPath, "--policy", policyPath,
		"--harness", "leaky", "--replicate", "1", "--out", leakEval, "--bench-dir", benchDir); code != 0 {
		t.Fatalf("leaky import exited %d:\n%s", code, out)
	}
	leakRunBytes, err := os.ReadFile(filepath.Join(leakEval, "run-1", "run.json"))
	if err != nil {
		t.Fatal(err)
	}
	var leakyRun struct {
		Leaks []eval.Leak
	}
	if err := json.Unmarshal(leakRunBytes, &leakyRun); err != nil {
		t.Fatal(err)
	}
	if len(leakyRun.Leaks) == 0 || leakyRun.Leaks[0].Kind != "canary" || !strings.Contains(leakyRun.Leaks[0].Source, "test-artifacts") {
		t.Fatalf("saved-test canary leak not recorded: %+v", leakyRun.Leaks)
	}
	leakRunPath := filepath.Join(leakEval, "run-1", "run.json")
	if out, code := runCLIWithHome(t, home, bin, "bench", "eval", "confirm",
		"--eval", filepath.Join(leakEval, "run-1"), "--bench-dir", benchDir); code != 1 || !strings.Contains(out, "leak") {
		t.Fatalf("confirm on a leaking run = %d, want refusal:\n%s", code, out)
	}

	// A run stripped of its manifest binding is legacy: it cannot claim a confirmed reproduction,
	// and its close keeps the old behavior — no confirmation is required of it.
	leakRunBytes, err = os.ReadFile(leakRunPath)
	if err != nil {
		t.Fatal(err)
	}
	var unboundRun map[string]any
	if err := json.Unmarshal(leakRunBytes, &unboundRun); err != nil {
		t.Fatal(err)
	}
	unboundRun["manifest_sha256"], unboundRun["manifest_path"] = "", ""
	unboundBytes, err := json.Marshal(unboundRun)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(leakRunPath, unboundBytes, 0o644); err != nil {
		t.Fatal(err)
	}
	if out, code := runCLIWithHome(t, home, bin, "bench", "eval", "confirm",
		"--eval", filepath.Join(leakEval, "run-1"), "--bench-dir", benchDir); code != 1 || !strings.Contains(out, "legacy unbound") {
		t.Fatalf("confirm on an unbound run = %d, want legacy refusal:\n%s", code, out)
	}
	if out, code := runCLIWithHome(t, home, bin, "bench", "eval", "adjudicate",
		"--eval", filepath.Join(leakEval, "run-1"), "--case", "c1", "--finding", "c1-f1", "--issue", "issue-a",
		"--c1", "true", "--c3", "true", "--c4-shows", "true",
		"--by", "reviewer", "--reason", "the row names the planted defect"); code != 0 {
		t.Fatalf("adjudicate unbound run exited %d:\n%s", code, out)
	}
	if out, code := runCLIWithHome(t, home, bin, "bench", "eval", "close", "--eval", filepath.Join(leakEval, "run-1")); code != 0 {
		t.Fatalf("close of an unbound run must keep the old behavior, got %d:\n%s", code, out)
	}
}

// confirm only replays the local catch oracle: a `command` or `none` oracle, an Issue the sealed
// key does not reproduce, and an Issue the key does not plant are each refused before any replay.
func TestRequireCatchOracleRefusesUnsupportedReproduction(t *testing.T) {
	key := bench.Key{ID: "c1", Defects: []bench.Defect{
		{ID: "command-a", Reproduction: &bench.Reproduction{Applies: true, Oracle: "command", Attempts: 1}},
		{ID: "none-a", Reproduction: &bench.Reproduction{Applies: true, Oracle: "none", Attempts: 1}},
		{ID: "no-repro", Reproduction: &bench.Reproduction{Applies: false, Oracle: "catch", Attempts: 1}},
	}}
	for _, tc := range []struct{ issue, want string }{
		{"command-a", "not supported"},
		{"none-a", "not supported"},
		{"no-repro", "does not apply"},
		{"ghost", "no defect"},
	} {
		err := requireCatchOracle(key, "c1", tc.issue)
		if err == nil || !strings.Contains(err.Error(), tc.want) {
			t.Errorf("requireCatchOracle(%s) = %v, want error containing %q", tc.issue, err, tc.want)
		}
	}
}

// F3: with saved tests, an Issue whose fixed variant is missing was never checked. Confirm must
// refuse it before running anything — no appended events, run still ADJUDICATING — instead of
// recording NOT_REPRODUCED from an absent Caught entry.
func TestBenchEvalConfirmRefusesAnIssueWhoseVariantWasNeverChecked(t *testing.T) {
	bin := buildCLI(t)
	home := t.TempDir()
	root := t.TempDir()
	benchDir := filepath.Join(root, "bench")
	canary := "0123456789abcdef0123456789abcdef"
	writeFile := func(path string, data []byte) {
		t.Helper()
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, data, 0o644); err != nil {
			t.Fatal(err)
		}
	}
	writeJSON := func(path string, value any) {
		t.Helper()
		data, err := json.Marshal(value)
		if err != nil {
			t.Fatal(err)
		}
		writeFile(path, data)
	}
	caseDir := filepath.Join(benchDir, "cases", "c1")
	for rel, content := range map[string]string{
		"fixture/src.txt":          "D1=bug\nD2=bug\n",
		"fixture/run-tests.sh":     "for f in tests/*.sh; do [ -e \"$f\" ] || continue; sh \"$f\" || exit 1; done\nexit 0\n",
		"fix/all/src.txt":          "D1=ok\nD2=ok\n",
		"fix/keep-issue-a/src.txt": "D1=bug\nD2=ok\n", // fix/keep-issue-b deliberately absent
	} {
		writeFile(filepath.Join(caseDir, filepath.FromSlash(rel)), []byte(content))
	}
	defect := func(id string, line int, keyword string) bench.Defect {
		return bench.Defect{
			ID: id, File: "src.txt", Line: line, Class: "logic", Keywords: []string{keyword},
			Description: "the counter mishandles a value", IssueType: "logic", Domain: "security",
			Severity: "high", SeverityRationale: "security boundary", ExpectedBehavior: "reject invalid input",
			FailureCondition:  "accepts invalid input",
			DetectionCriteria: &bench.DetectionCriteria{Mechanism: "input validation", Proof: "reproduce invalid acceptance"},
			Reproduction:      &bench.Reproduction{Applies: true, Oracle: "catch", Attempts: 1},
		}
	}
	writeJSON(filepath.Join(caseDir, bench.KeyFile), bench.Key{
		Schema: 2, ID: "c1", Language: "go", Suite: "sh run-tests.sh", Surface: "library", Canary: canary,
		Defects: []bench.Defect{defect("issue-a", 1, "first"), defect("issue-b", 2, "second")},
	})
	manifest, err := eval.BuildManifest(benchDir, eval.ManifestSpec{
		Benchmark: "TEST", Version: "1.0", Status: "draft", Created: "2026-10-01T00:00:00Z",
		ChangeReason: "test", KeySchema: 2, Cases: []string{"c1"},
		Domains: []eval.Domain{eval.Security}, CriticalDomains: []eval.Domain{eval.Security},
		DetectionCriteriaVersion: "dc-1",
		MetricConfig:             eval.MetricConfig{WeightedRecallW: 0.5, CILevel: 0.9, EarlyStopCILevel: 0.95, BootstrapResamples: 100, BootstrapSeed: 7, Consolidation: "strict-majority"},
		Replicates:               eval.Replicates{KMin: 1, KTarget: 1, KMax: 1},
		Budgets:                  eval.Budgets{MaxCases: 1, MaxAttemptsPerCase: 1, MaxRetries: 1, MaxTokensPerCaseRun: 100, MaxCostPerCaseRunUSD: 1, MaxCostSuiteUSD: 2, MaxRuntimePerCaseRunSeconds: 60, MaxRuntimeSuiteSeconds: 120, Verdicts: []string{"PASS", "FAIL", "REVIEW"}},
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
	for path, value := range map[string]any{manifestPath: manifest, policyPath: policy} {
		data, err := eval.CanonicalJSON(value)
		if err != nil {
			t.Fatal(err)
		}
		writeFile(path, data)
	}
	testA, testB := []byte("grep -q D1=ok src.txt\n"), []byte("grep -q D2=ok src.txt\n")
	shaHex := func(b []byte) string { sum := sha256.Sum256(b); return hex.EncodeToString(sum[:]) }
	resultsDir := filepath.Join(root, "results")
	writeJSON(filepath.Join(resultsDir, "aggregate.json"), bench.Aggregate{Model: "model", Provenance: bench.Provenance{
		Model: "model", Runner: "pi", AgentConfig: bench.ConfigBench, SkillsDigest: "sha256:skills", Environment: "linux/amd64",
	}})
	writeJSON(filepath.Join(resultsDir, "c1", "1", "result.json"), bench.Result{
		Case: "c1", Run: 1, PlanFound: true, PlanFormat: bench.FormatTable, CostUSD: 0.1, Seconds: 2,
		Catch: bench.CatchResult{
			Checked: true, TestFiles: []string{"tests/a.sh", "tests/b.sh"}, AllGreen: true,
			Caught: map[string]bool{"issue-a": true, "issue-b": true}, Attempts: map[string]int{"issue-a": 1, "issue-b": 1},
		},
		TestArtifacts: []bench.TestArtifact{{Path: "tests/a.sh", SHA256: shaHex(testA)}, {Path: "tests/b.sh", SHA256: shaHex(testB)}},
	})
	plan := "## Findings\n\n| Id | Finding | Type | Location | Evidence | Severity |\n|---|---|---|---|---|---|\n" +
		"| F1 | first value accepted | boundary | src.txt:1 | E1 | high |\n" +
		"| F2 | second value accepted | boundary | src.txt:2 | E2 | high |\n\n" +
		"## Evidence ledger\n\n| Id | Claim |\n|---|---|\n| E1 | src.txt:1 accepts |\n| E2 | src.txt:2 accepts |\n"
	writeFile(filepath.Join(resultsDir, "c1", "1", "test-plan.md"), []byte(plan))
	writeFile(filepath.Join(resultsDir, "c1", "1", "test-artifacts", "tests", "a.sh"), testA)
	writeFile(filepath.Join(resultsDir, "c1", "1", "test-artifacts", "tests", "b.sh"), testB)
	evalDir := filepath.Join(root, "eval")
	if out, code := runCLIWithHome(t, home, bin, "bench", "eval", "import",
		"--results", resultsDir, "--manifest", manifestPath, "--policy", policyPath,
		"--harness", "h", "--replicate", "1", "--out", evalDir, "--bench-dir", benchDir); code != 0 {
		t.Fatalf("import exited %d:\n%s", code, out)
	}
	runDir := filepath.Join(evalDir, "run-1")
	for _, decide := range [][2]string{{"c1-f1", "issue-a"}, {"c1-f2", "issue-b"}} {
		if out, code := runCLIWithHome(t, home, bin, "bench", "eval", "adjudicate",
			"--eval", runDir, "--case", "c1", "--finding", decide[0], "--issue", decide[1],
			"--c1", "true", "--c3", "true", "--c4-shows", "true",
			"--by", "reviewer", "--reason", "the row names the planted defect"); code != 0 {
			t.Fatalf("adjudicate %s exited %d:\n%s", decide[0], code, out)
		}
	}
	runBefore, err := os.ReadFile(filepath.Join(runDir, "run.json"))
	if err != nil {
		t.Fatal(err)
	}
	eventsBefore, err := os.ReadFile(filepath.Join(runDir, "c1", "events.jsonl"))
	if err != nil {
		t.Fatal(err)
	}
	if out, code := runCLIWithHome(t, home, bin, "bench", "eval", "confirm",
		"--eval", runDir, "--bench-dir", benchDir); code != 1 || !strings.Contains(out, "keep-issue-b") {
		t.Fatalf("confirm with an uncheckable issue = %d, want refusal naming the missing variant:\n%s", code, out)
	}
	runAfter, err := os.ReadFile(filepath.Join(runDir, "run.json"))
	if err != nil {
		t.Fatal(err)
	}
	if string(runAfter) != string(runBefore) || !strings.Contains(string(runAfter), `"state": "ADJUDICATING"`) {
		t.Fatalf("refused confirm changed the run record; it must stay ADJUDICATING:\n%s", runAfter)
	}
	eventsAfter, err := os.ReadFile(filepath.Join(runDir, "c1", "events.jsonl"))
	if err != nil {
		t.Fatal(err)
	}
	if string(eventsAfter) != string(eventsBefore) {
		t.Fatal("refused confirm appended events")
	}
}

// A suite whose own budget ran out never measured the whole candidate, so the built CLI must carry
// the abort through import and report the hard completeness blocker H7 — never a fabricated
// candidate metric, a missing-file error, or a plain NO_DECISION.
func TestBenchEvalCLICompareReportsBudgetAbortAsCompletenessFailure(t *testing.T) {
	bin := buildCLI(t)
	root := t.TempDir()
	benchDir, manifestPath, policyPath := writeEvalCLIFixture(t, root)
	manifest, err := eval.LoadManifest(manifestPath)
	if err != nil {
		t.Fatal(err)
	}
	emptyPlan := "## Findings\n\n| Id | Finding | Type | Location | Evidence | Severity |\n|---|---|---|---|---|---|\n"
	plan := emptyPlan + "| F1 | observed concern | boundary | src/a.go:10 | - | high |\n"
	writeSide := func(name string, suiteBudgetStop bool) string {
		t.Helper()
		resultsDir := filepath.Join(root, name+"-results")
		if err := os.MkdirAll(resultsDir, 0o755); err != nil {
			t.Fatal(err)
		}
		provenance := map[string]any{
			"model": "model", "runner": "pi", "agent_config": bench.ConfigBench,
			"skills_digest": "sha256:skills", "environment": "linux/amd64",
		}
		aggregate := map[string]any{"model": "model", "provenance": provenance}
		if suiteBudgetStop {
			// What internal/bench/run.go seals when the suite budget stops the run: the manifest
			// binding, a valid instrument, a supported budget, and an execution that never finished.
			provenance["manifest_sha256"] = manifest.ManifestSHA256
			provenance["instrument_valid"] = true
			provenance["execution_complete"] = false
			provenance["budget_status"] = "supported"
			aggregate["budget_exhausted"] = true
		}
		aggregateData, err := json.Marshal(aggregate)
		if err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(resultsDir, "aggregate.json"), aggregateData, 0o644); err != nil {
			t.Fatal(err)
		}
		// c2 runs in the baseline only: under the suite budget stop it never started, so the
		// runner kept no result for it.
		cases := []string{"c1"}
		if !suiteBudgetStop {
			cases = append(cases, "c2")
		}
		for _, caseID := range cases {
			caseRunDir := filepath.Join(resultsDir, caseID, "1")
			if err := os.MkdirAll(caseRunDir, 0o755); err != nil {
				t.Fatal(err)
			}
			result := bench.Result{Case: caseID, Run: 1, PlanFound: true, PlanFormat: bench.FormatTable, CostUSD: 0.1, Seconds: 2}
			if caseID == "c1" {
				// The budgeted case did spend tokens before it ran out.
				spent := 120
				result.Tokens = &spent
				if suiteBudgetStop {
					result.BudgetExhausted, result.Outcome = true, "budget_exhausted"
				}
			}
			if caseID == "c2" {
				measured := 0
				result.Tokens = &measured
			}
			resultData, err := json.Marshal(result)
			if err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(filepath.Join(caseRunDir, "result.json"), resultData, 0o644); err != nil {
				t.Fatal(err)
			}
			casePlan := emptyPlan
			if caseID == "c1" {
				casePlan = plan
			}
			if err := os.WriteFile(filepath.Join(caseRunDir, "test-plan.md"), []byte(casePlan), 0o644); err != nil {
				t.Fatal(err)
			}
		}
		return resultsDir
	}

	baselineEval, candidateEval := filepath.Join(root, "baseline-eval"), filepath.Join(root, "candidate-eval")
	if out, code := runCLI(t, bin, "bench", "eval", "import", "--results", writeSide("baseline", false), "--manifest", manifestPath, "--policy", policyPath, "--harness", "baseline", "--replicate", "1", "--out", baselineEval, "--bench-dir", benchDir); code != 0 {
		t.Fatalf("baseline import exited %d:\n%s", code, out)
	}
	if out, code := runCLI(t, bin, "bench", "eval", "adjudicate", "--eval", filepath.Join(baselineEval, "run-1"), "--case", "c1", "--finding", "c1-f1", "--outcome", "FP", "--by", "reviewer", "--reason", "verified false positive"); code != 0 {
		t.Fatalf("adjudicate exited %d:\n%s", code, out)
	}
	if out, code := runCLI(t, bin, "bench", "eval", "close", "--eval", filepath.Join(baselineEval, "run-1")); code != 0 {
		t.Fatalf("close exited %d:\n%s", code, out)
	}
	// The candidate that ran out of suite budget imports as an abort, not as an import failure.
	if out, code := runCLI(t, bin, "bench", "eval", "import", "--results", writeSide("candidate", true), "--manifest", manifestPath, "--policy", policyPath, "--harness", "candidate", "--replicate", "1", "--out", candidateEval, "--bench-dir", benchDir); code != 0 {
		t.Fatalf("candidate budget-abort import exited %d:\n%s", code, out)
	}
	candidateRun, err := os.ReadFile(filepath.Join(candidateEval, "run-1", "run.json"))
	if err != nil {
		t.Fatal(err)
	}
	var abortRecord struct {
		State        string            `json:"state"`
		AbortReason  string            `json:"abort_reason"`
		CaseOutcomes map[string]string `json:"case_outcomes"`
		Data         struct {
			Cases []json.RawMessage `json:"cases"`
		} `json:"data"`
	}
	if err := json.Unmarshal(candidateRun, &abortRecord); err != nil {
		t.Fatal(err)
	}
	if abortRecord.State != "ABORTED" || abortRecord.AbortReason != "budget" {
		t.Fatalf("candidate run record = state %q reason %q, want ABORTED on budget", abortRecord.State, abortRecord.AbortReason)
	}
	if abortRecord.CaseOutcomes["c2"] != "missing_execution" || len(abortRecord.Data.Cases) != 0 {
		t.Fatalf("candidate run kept %q for the unrun case and %d metrics", abortRecord.CaseOutcomes["c2"], len(abortRecord.Data.Cases))
	}

	comparisonDir := filepath.Join(root, "comparison")
	out, code := runCLI(t, bin, "bench", "eval", "compare", "--baseline", baselineEval, "--candidate", candidateEval, "--manifest", manifestPath, "--policy", policyPath, "--out", comparisonDir)
	if code == 0 {
		t.Fatalf("compare must fail on the aborted candidate, got exit 0:\n%s", out)
	}
	if !strings.Contains(out, "H7") || !strings.Contains(out, "FAIL") {
		t.Fatalf("compare report does not carry the completeness blocker:\n%s", out)
	}
	decisionData, err := os.ReadFile(filepath.Join(comparisonDir, "decision.json"))
	if err != nil {
		t.Fatal(err)
	}
	var decision struct {
		Verdict  string
		Category string
		Blockers []struct {
			ID     string
			Detail string
		} `json:"blockers"`
	}
	if err := json.Unmarshal(decisionData, &decision); err != nil {
		t.Fatal(err)
	}
	if decision.Verdict != "FAIL" || decision.Category != "COMPLETENESS" || len(decision.Blockers) == 0 || decision.Blockers[0].ID != "H7" {
		t.Fatalf("decision = %s/%s blockers %+v, want FAIL/COMPLETENESS with H7", decision.Verdict, decision.Category, decision.Blockers)
	}

	// The abort may excuse findings the run never adjudicated, never the integrity of the ledger:
	// rewriting a state in the authoritative event log must be detected, not read as the same H7.
	eventsPath := filepath.Join(candidateEval, "run-1", "c1", "events.jsonl")
	events, err := os.ReadFile(eventsPath)
	if err != nil {
		t.Fatal(err)
	}
	tampered := regexp.MustCompile(`"new_state":"[A-Z_]+"`).ReplaceAllString(string(events), `"new_state":"TAMPERED_STATE"`)
	if tampered == string(events) {
		t.Fatalf("no event state found to tamper in %s", eventsPath)
	}
	if err := os.WriteFile(eventsPath, []byte(tampered), 0o644); err != nil {
		t.Fatal(err)
	}
	tamperedDir := filepath.Join(root, "comparison-tampered")
	tamperedOut, tamperedCode := runCLI(t, bin, "bench", "eval", "compare", "--baseline", baselineEval, "--candidate", candidateEval, "--manifest", manifestPath, "--policy", policyPath, "--out", tamperedDir)
	if tamperedCode == 0 || tamperedOut == out {
		t.Fatalf("a tampered aborted ledger must not read as the ordinary H7:\n%s", tamperedOut)
	}
	tamperedDecision, err := os.ReadFile(filepath.Join(tamperedDir, "decision.json"))
	if err != nil {
		t.Fatalf("tampered compare produced no decision to inspect: %v\n%s", err, tamperedOut)
	}
	if string(tamperedDecision) == string(decisionData) {
		t.Fatal("a tampered aborted ledger produced a decision byte-identical to the untampered H7")
	}
	if !strings.Contains(string(tamperedDecision)+tamperedOut, "I9") && !strings.Contains(string(tamperedDecision)+tamperedOut, "H5") &&
		!strings.Contains(string(tamperedDecision)+tamperedOut, "I11") {
		t.Fatalf("tampering went unreported:\n%s\n%s", tamperedOut, tamperedDecision)
	}
}

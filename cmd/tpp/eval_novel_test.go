package main

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/alesierraalta/tpp/internal/bench"
	"github.com/alesierraalta/tpp/internal/eval"
	plan "github.com/alesierraalta/tpp/internal/plan"
)

// proveNovelFixture is a one-case node benchmark whose saved test fails on the fixture
// (value 1) and passes under controlPass (value 2), imported into an evaluation run with
// c1-f1 parked at NOVEL_CANDIDATE and c1-f2 left pending. The saved test writes marker
// when node runs, so "no node process ran" is observable.
type proveNovelFixture struct {
	root, benchDir, runDir, controlPass, controlFail, marker, savedTest string
}

func writeProveNovelFixture(t *testing.T, bin, suite string) proveNovelFixture {
	t.Helper()
	root := t.TempDir()
	benchDir := filepath.Join(root, "bench")
	caseDir := filepath.Join(benchDir, "cases", "c1")
	canary := "0123456789abcdef0123456789abcdef"
	marker := filepath.Join(root, "node-ran.marker")
	writeCaseFile := func(rel, body string) {
		t.Helper()
		path := filepath.Join(caseDir, rel)
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	writeCaseFile(filepath.Join("fixture", "package.json"), `{"type":"module"}`)
	writeCaseFile(filepath.Join("fixture", "lib.mjs"), "export const value = 1;")
	writeCaseFile(filepath.Join("fix", "lib.mjs"), "export const value = 2;")
	language := "node"
	if strings.HasPrefix(suite, "go") {
		language = "go"
	}
	key := bench.Key{Schema: 2, ID: "c1", Language: language, Suite: suite, Surface: "library", Canary: canary,
		Defects: []bench.Defect{{
			ID: "issue-a", File: "src/lib.mjs", Line: 1, Class: "logic", Keywords: []string{"value"},
			Description: "value is wrong", IssueType: "logic", Domain: "correctness", Severity: "high",
			SeverityRationale: "callers get the wrong value", ExpectedBehavior: "value is 2",
			FailureCondition:  "value is 1",
			DetectionCriteria: &bench.DetectionCriteria{Mechanism: "compare values", Proof: "the saved test asserts value 2"},
			Reproduction:      &bench.Reproduction{Oracle: "none"},
		}}}
	keyData, err := json.Marshal(key)
	if err != nil {
		t.Fatal(err)
	}
	writeCaseFile(bench.KeyFile, string(keyData))

	manifest, err := eval.BuildManifest(benchDir, eval.ManifestSpec{
		Benchmark: "TEST", Version: "1.0", Status: "draft", Created: "2026-10-01T00:00:00Z",
		ChangeReason: "test", KeySchema: 2, Cases: []string{"c1"},
		Domains: []eval.Domain{eval.Correctness}, CriticalDomains: []eval.Domain{eval.Security},
		DetectionCriteriaVersion: "dc-1",
		MetricConfig:             eval.MetricConfig{WeightedRecallW: 0.5, CILevel: 0.9, EarlyStopCILevel: 0.95, BootstrapResamples: 100, BootstrapSeed: 7, Consolidation: "strict-majority"},
		Replicates:               eval.Replicates{KMin: 1, KTarget: 1, KMax: 1},
		Budgets:                  eval.Budgets{MaxCases: 1, MaxAttemptsPerCase: 1, MaxRetries: 1, MaxTokensPerCaseRun: 100, MaxCostPerCaseRunUSD: 1, MaxCostSuiteUSD: 2, MaxRuntimePerCaseRunSeconds: 30, MaxRuntimeSuiteSeconds: 120, Verdicts: []string{"PASS", "FAIL", "REVIEW"}},
		Seeds:                    map[string]int64{"case_order": 7}, Canary: canary,
	})
	if err != nil {
		t.Fatal(err)
	}
	if manifest, err = manifest.Seal(); err != nil {
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

	// The saved test fails on the fixture, passes under controlPass, and writes marker so a
	// real node run is observable from outside the (deleted) staging directory.
	savedTest := fmt.Sprintf(
		"import {test} from 'node:test'; import {strict as a} from 'node:assert'; import {writeFileSync} from 'node:fs'; import {value} from './lib.mjs'; writeFileSync(%s, 'ran'); test('saved-check', () => a.equal(value, 2));",
		strconv.Quote(marker))
	resultsDir := filepath.Join(root, "results")
	caseResults := filepath.Join(resultsDir, "c1", "1")
	if err := os.MkdirAll(filepath.Join(caseResults, "test-artifacts"), 0o755); err != nil {
		t.Fatal(err)
	}
	aggregate, err := json.Marshal(bench.Aggregate{Model: "model", Provenance: bench.Provenance{
		Model: "model", Runner: "pi", AgentConfig: bench.ConfigBench,
		SkillsDigest: "sha256:skills", Environment: "linux/amd64",
	}})
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(resultsDir, "aggregate.json"), aggregate, 0o644); err != nil {
		t.Fatal(err)
	}
	testSum := sha256.Sum256([]byte(savedTest))
	result, err := json.Marshal(bench.Result{Case: "c1", Run: 1, PlanFound: true, PlanFormat: bench.FormatTable, CostUSD: 0.1, Seconds: 2,
		Catch:         bench.CatchResult{Checked: true, TestFiles: []string{"check.test.mjs"}, AllGreen: true},
		TestArtifacts: []bench.TestArtifact{{Path: "check.test.mjs", SHA256: hex.EncodeToString(testSum[:])}}})
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(caseResults, "result.json"), result, 0o644); err != nil {
		t.Fatal(err)
	}
	plan := "## Findings\n\n| Id | Finding | Type | Location | Evidence | Severity |\n|---|---|---|---|---|---|\n" +
		"| F1 | observed concern | logic | src/lib.mjs:1 | - | high |\n" +
		"| F2 | second concern | logic | src/lib.mjs:1 | - | high |\n"
	if err := os.WriteFile(filepath.Join(caseResults, "test-plan.md"), []byte(plan), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(caseResults, "test-artifacts", "check.test.mjs"), []byte(savedTest), 0o644); err != nil {
		t.Fatal(err)
	}
	controlPass, controlFail := filepath.Join(root, "control-pass"), filepath.Join(root, "control-fail")
	for dir, value := range map[string]string{controlPass: "export const value = 2;", controlFail: "export const value = 3;"} {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(dir, "lib.mjs"), []byte(value), 0o644); err != nil {
			t.Fatal(err)
		}
	}

	evalDir := filepath.Join(root, "eval")
	if out, code := runCLI(t, bin, "bench", "eval", "import", "--results", resultsDir, "--manifest", manifestPath,
		"--policy", policyPath, "--harness", "baseline", "--replicate", "1", "--out", evalDir, "--bench-dir", benchDir); code != 0 {
		t.Fatalf("import exited %d:\n%s", code, out)
	}
	runDir := filepath.Join(evalDir, "run-1")
	// Park c1-f1 at NOVEL_CANDIDATE through the NONE-match route the spec documents: an
	// unmatched decision with no outcome leaves the finding at NOVEL_CANDIDATE. (--outcome
	// NOVEL_CANDIDATE is refused by Decision.Validate: only terminal outcomes are allowed.)
	if out, code := runCLI(t, bin, "bench", "eval", "adjudicate", "--eval", runDir, "--case", "c1", "--finding", "c1-f1",
		"--issue", "issue-a", "--c1", "false", "--c3", "false", "--c4-shows", "false", "--by", "reviewer",
		"--reason", "no known issue matches"); code != 0 {
		t.Fatalf("adjudicate to NOVEL_CANDIDATE exited %d:\n%s", code, out)
	}
	return proveNovelFixture{root: root, benchDir: benchDir, runDir: runDir, controlPass: controlPass,
		controlFail: controlFail, marker: marker, savedTest: filepath.Join(caseResults, "test-artifacts", "check.test.mjs")}
}

func proveNovelArgs(fx proveNovelFixture) []string {
	return []string{"bench", "eval", "prove-novel",
		"--eval", fx.runDir, "--case", "c1", "--finding", "c1-f1",
		"--control-dir", fx.controlPass, "--control-name", "explicit-fix",
		"--domain", "correctness", "--severity", "high", "--issue-type", "logic",
		"--expected-behavior", "value is 2", "--failure-condition", "value is 1",
		"--mechanism", "defective computation", "--by", "reviewer",
		"--reason", "saved test isolates the defect", "--bench-dir", fx.benchDir}
}

func withFlag(args []string, name, value string) []string {
	out := append([]string(nil), args...)
	for i := 0; i < len(out)-1; i++ {
		if out[i] == name {
			out[i+1] = value
			return out
		}
	}
	return append(out, name, value)
}

func withoutFlag(args []string, name string) []string {
	var out []string
	for i := 0; i < len(args); i++ {
		if args[i] == name {
			i++
			continue
		}
		out = append(out, args[i])
	}
	return out
}

func snapshotRunDir(t *testing.T, runDir string) map[string][]byte {
	t.Helper()
	files := make(map[string][]byte)
	err := filepath.Walk(runDir, func(path string, info os.FileInfo, err error) error {
		if err != nil || info.IsDir() {
			return err
		}
		rel, err := filepath.Rel(runDir, path)
		if err != nil {
			return err
		}
		data, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		files[rel] = data
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	return files
}

func assertRunDirUnchanged(t *testing.T, runDir string, before map[string][]byte) {
	t.Helper()
	after := snapshotRunDir(t, runDir)
	if len(after) != len(before) {
		t.Fatalf("refused prove-novel changed the run dir file set: %d files -> %d files", len(before), len(after))
	}
	for rel, want := range before {
		if got, ok := after[rel]; !ok {
			t.Fatalf("refused prove-novel removed %s", rel)
		} else if !bytes.Equal(got, want) {
			t.Fatalf("refused prove-novel mutated %s", rel)
		}
	}
}

func sha256HexOf(parts ...[]byte) string {
	hash := sha256.New()
	for _, part := range parts {
		hash.Write(part)
	}
	return hex.EncodeToString(hash.Sum(nil))
}

// TestBenchEvalProveNovelRecordsExactlyOneProof is the positive path: a dry run performs the
// full preflight and exits non-zero without mutating or spawning node; --execute records
// exactly one novel-proof/2 event whose observation digests bind the staged sources; a
// second attempt for the same finding is refused before any replay.
func TestBenchEvalProveNovelRecordsExactlyOneProof(t *testing.T) {
	bin := buildCLI(t)
	fx := writeProveNovelFixture(t, bin, "node --test")

	before := snapshotRunDir(t, fx.runDir)
	out, code := runCLI(t, bin, proveNovelArgs(fx)...)
	if code != 2 {
		t.Fatalf("dry run exited %d, want 2:\n%s", code, out)
	}
	for _, want := range []string{
		"preflight passed", "case: c1", "finding: c1-f1", "control: explicit-fix", fx.controlPass,
		"saved tests: check.test.mjs", "timeout: 30s",
		"pass --execute to run saved tests; no sandbox",
	} {
		if !strings.Contains(out, want) {
			t.Fatalf("dry-run output missing %q:\n%s", want, out)
		}
	}
	assertRunDirUnchanged(t, fx.runDir, before)
	if _, err := os.Stat(fx.marker); err == nil {
		t.Fatal("the dry run spawned a node process (marker exists)")
	} else if !os.IsNotExist(err) {
		t.Fatal(err)
	}

	out, code = runCLI(t, bin, append(proveNovelArgs(fx), "--execute")...)
	if code != 0 {
		t.Fatalf("prove-novel --execute exited %d:\n%s", code, out)
	}
	if !strings.Contains(out, "recorded novel-proof/2 proof for c1/c1-f1") {
		t.Fatalf("success output missing proof schema:\n%s", out)
	}
	if _, err := os.Stat(fx.marker); err != nil {
		t.Fatalf("the saved test never ran (marker missing): %v", err)
	}
	after := snapshotRunDir(t, fx.runDir)
	for _, rel := range []string{"run.json", filepath.Join("c1", "caserun.json")} {
		if !bytes.Equal(after[rel], before[rel]) {
			t.Fatalf("%s changed although only an event may be appended", rel)
		}
	}
	eventsPath := filepath.Join("c1", "events.jsonl")
	if bytes.Equal(after[eventsPath], before[eventsPath]) {
		t.Fatal("events.jsonl gained no event")
	}
	if got := bytes.Count(after[eventsPath], []byte(`"event":"novel_proof"`)); got != 1 {
		t.Fatalf("events.jsonl holds %d novel_proof events, want exactly 1", got)
	}

	// Decode the recorded event and prove its observation digests bind the real sources.
	lines := strings.Split(strings.TrimRight(string(after[eventsPath]), "\n"), "\n")
	var event eval.Event
	if err := json.Unmarshal([]byte(lines[len(lines)-1]), &event); err != nil {
		t.Fatal(err)
	}
	if event.Kind != eval.EventNovelProof || event.Entity != eval.EntityFinding || event.ID != "c1-f1" {
		t.Fatalf("last event = %s %s %s, want a novel_proof for c1-f1", event.Entity, event.ID, event.Kind)
	}
	if event.PreviousState != string(eval.FindingNovelCandidate) || event.NewState != string(eval.FindingNovelCandidate) {
		t.Fatalf("novel_proof event states %s -> %s, want a state-preserving NOVEL_CANDIDATE event", event.PreviousState, event.NewState)
	}
	if event.Adjudicator != eval.NovelProofAdjudicator {
		t.Fatalf("novel_proof adjudicator = %q", event.Adjudicator)
	}
	var proof eval.NovelProof
	if err := json.Unmarshal(event.Payload, &proof); err != nil {
		t.Fatal(err)
	}
	if proof.Schema != eval.NovelProofSchemaV2 {
		t.Fatalf("proof schema = %q, want %q", proof.Schema, eval.NovelProofSchemaV2)
	}
	if proof.ProofKind != eval.NovelProofSavedTest {
		t.Fatalf("proof kind = %q, want %q", proof.ProofKind, eval.NovelProofSavedTest)
	}
	if proof.AdjudicatedBy != "reviewer" {
		t.Fatalf("proof adjudicated by %q", proof.AdjudicatedBy)
	}
	obs := proof.Observation
	if obs == nil {
		t.Fatal("proof carries no replay observation")
	}
	if obs.ControlName != "explicit-fix" {
		t.Fatalf("observation control name = %q", obs.ControlName)
	}
	if obs.Subject.Classification != "assertion_failure" || obs.Control.Classification != "passed" {
		t.Fatalf("observation contrast = subject %q / control %q, want assertion failure vs pass",
			obs.Subject.Classification, obs.Control.Classification)
	}
	for name, digest := range map[string]string{
		"test": obs.TestSHA256, "subject output": obs.Subject.OutputSHA256, "subject source": obs.Subject.SourceSHA256,
		"subject test": obs.Subject.TestSHA256, "control output": obs.Control.OutputSHA256,
		"control source": obs.Control.SourceSHA256, "control test": obs.Control.TestSHA256,
		"control tree": obs.Control.ControlSHA256, "source binding": proof.SourceBinding,
	} {
		if !strings.HasPrefix(digest, "sha256:") || len(digest) != len("sha256:")+64 {
			t.Fatalf("observation %s digest = %q, want a sha256: digest", name, digest)
		}
	}
	testBytes, err := os.ReadFile(fx.savedTest)
	if err != nil {
		t.Fatal(err)
	}
	if want := "sha256:" + sha256HexOf([]byte("check.test.mjs\x00"), testBytes, []byte{0}); obs.TestSHA256 != want {
		t.Fatalf("observation test digest = %s, want the staged saved test %s", obs.TestSHA256, want)
	}
	controlBytes, err := os.ReadFile(filepath.Join(fx.controlPass, "lib.mjs"))
	if err != nil {
		t.Fatal(err)
	}
	if want := "sha256:" + sha256HexOf([]byte("lib.mjs\x00"), controlBytes, []byte{0}); obs.Control.ControlSHA256 != want {
		t.Fatalf("observation control tree digest = %s, want %s", obs.Control.ControlSHA256, want)
	}
	fixtureLib := []byte("export const value = 1;")
	packageJSON := []byte(`{"type":"module"}`)
	wantSource := "sha256:" + sha256HexOf(
		[]byte("check.test.mjs\x00"), testBytes, []byte{0},
		[]byte("lib.mjs\x00"), fixtureLib, []byte{0},
		[]byte("package.json\x00"), packageJSON, []byte{0})
	if obs.Subject.SourceSHA256 != wantSource {
		t.Fatalf("observation subject source digest = %s, want the staged workspace %s", obs.Subject.SourceSHA256, wantSource)
	}
	if proof.SourceBinding != obs.Subject.SourceSHA256 {
		t.Fatalf("source binding %s does not equal the staged subject source %s", proof.SourceBinding, obs.Subject.SourceSHA256)
	}

	// A second proof for the same finding is refused at preflight: no replay, no mutation.
	before2 := snapshotRunDir(t, fx.runDir)
	out, code = runCLI(t, bin, append(proveNovelArgs(fx), "--execute")...)
	if code != 1 {
		t.Fatalf("second proof exited %d, want 1:\n%s", code, out)
	}
	if !strings.Contains(out, "already has an effective novel proof") {
		t.Fatalf("second-proof refusal message:\n%s", out)
	}
	assertRunDirUnchanged(t, fx.runDir, before2)
}

// proveNovelContentionWindow bounds how long the lock-held test watches a prove-novel it
// started while the test itself holds the run-dir lock. Nothing outside the process can
// observe "this process is queued on flock", so the window is an observation period, not a
// sleep used to synchronize: while the test holds the lock a locked writer cannot mutate or
// exit at any moment of it (a longer window never turns a pass into a fail), while an
// unlocked writer reaches SaveRun or exits inside it (the window is what lets the pre-lock
// code be caught).
const proveNovelContentionWindow = 2 * time.Second

// proveNovelFinishWait bounds waiting for a background prove-novel to finish. It is a
// watchdog against a hung child, not a synchronization delay: the happy path proceeds the
// moment the process exits.
const proveNovelFinishWait = 2 * time.Minute

// backgroundCLI runs the built binary in the background with its combined output in a
// file, so the test can observe the run directory and the process's liveness while it is
// still running without reading a buffer the process writes into (the race detector would
// flag that).
type backgroundCLI struct {
	cmd     *exec.Cmd
	outPath string
	done    chan error
}

func startCLI(t *testing.T, bin string, args ...string) backgroundCLI {
	t.Helper()
	outPath := filepath.Join(t.TempDir(), "background.out")
	out, err := os.Create(outPath)
	if err != nil {
		t.Fatal(err)
	}
	cmd := exec.Command(bin, args...)
	cmd.Stdout, cmd.Stderr = out, out
	if err := cmd.Start(); err != nil {
		out.Close()
		t.Fatalf("start %v: %v", args, err)
	}
	out.Close() // the child owns its descriptor now
	t.Cleanup(func() { _ = cmd.Process.Kill() })
	done := make(chan error, 1)
	go func() { done <- cmd.Wait() }()
	return backgroundCLI{cmd: cmd, outPath: outPath, done: done}
}

// awaitExit waits up to limit for the process to finish and kills it before failing, so no
// background prove-novel outlives the test.
func (b backgroundCLI) awaitExit(t *testing.T, limit time.Duration) error {
	t.Helper()
	select {
	case err := <-b.done:
		return err
	case <-time.After(limit):
		_ = b.cmd.Process.Kill()
		t.Fatalf("background %v still running after %s", b.cmd.Args, limit)
		return nil
	}
}

// code classifies a finished process's Wait error the way runCLI does.
func (b backgroundCLI) code(t *testing.T, waitErr error) int {
	t.Helper()
	if waitErr == nil {
		return 0
	}
	var ee *exec.ExitError
	if errors.As(waitErr, &ee) {
		return ee.ExitCode()
	}
	t.Fatalf("running %v: %v", b.cmd.Args, waitErr)
	return -1
}

func (b backgroundCLI) output(t *testing.T) string {
	t.Helper()
	data, err := os.ReadFile(b.outPath)
	if err != nil {
		t.Fatal(err)
	}
	return string(data)
}

// TestBenchEvalProveNovelRunDirLockSerializesWriters pins the serialization itself: the
// test takes the same run-dir lock prove-novel takes, starts prove-novel --execute, and the
// command must hold at LoadRun — no output, no node process, no event, still alive — for as
// long as the lock is held. Releasing the lock lets it finish and record exactly one proof.
// A command that ran unlocked (the pre-lock code) exits or mutates inside the window.
func TestBenchEvalProveNovelRunDirLockSerializesWriters(t *testing.T) {
	bin := buildCLI(t)
	fx := writeProveNovelFixture(t, bin, "node --test")

	lock, err := plan.LockPlan(fx.runDir)
	if err != nil {
		t.Fatalf("take the run-dir lock: %v", err)
	}
	defer plan.UnlockPlan(lock) // a failing assertion must not leave a child blocked forever

	before := snapshotRunDir(t, fx.runDir)
	bg := startCLI(t, bin, append(proveNovelArgs(fx), "--execute")...)
	select {
	case err := <-bg.done:
		t.Fatalf("prove-novel exited (%v) while the test held the run-dir lock: the command ran unlocked", err)
	case <-time.After(proveNovelContentionWindow):
	}
	if size, err := fileSize(bg.outPath); err != nil {
		t.Fatal(err)
	} else if size != 0 {
		t.Fatalf("prove-novel printed %d bytes while the run-dir lock was held:\n%s", size, bg.output(t))
	}
	assertRunDirUnchanged(t, fx.runDir, before)
	if _, err := os.Stat(fx.marker); err == nil {
		t.Fatal("prove-novel spawned node while the run-dir lock was held")
	} else if !os.IsNotExist(err) {
		t.Fatal(err)
	}

	plan.UnlockPlan(lock)
	if code := bg.code(t, bg.awaitExit(t, proveNovelFinishWait)); code != 0 {
		t.Fatalf("prove-novel exited %d after the lock was released:\n%s", code, bg.output(t))
	}
	out := bg.output(t)
	if !strings.Contains(out, "recorded novel-proof/2 proof for c1/c1-f1") {
		t.Fatalf("prove-novel did not record the proof after the lock was released:\n%s", out)
	}
	if _, err := os.Stat(fx.marker); err != nil {
		t.Fatalf("the saved test never ran after the lock was released: %v", err)
	}
	eventsPath := filepath.Join(fx.runDir, "c1", "events.jsonl")
	events, err := os.ReadFile(eventsPath)
	if err != nil {
		t.Fatal(err)
	}
	if got := bytes.Count(events, []byte(`"event":"novel_proof"`)); got != 1 {
		t.Fatalf("events.jsonl holds %d novel_proof events, want exactly 1", got)
	}
}

// TestBenchEvalProveNovelConcurrentExecutionsRecordExactlyOneProof pins the lost-update
// fix: two prove-novel --execute invocations on one run dir must not both report success.
// The lock makes the second one wait, load the first one's saved proof and refuse with
// "already has an effective novel proof"; exactly one novel_proof event persists.
func TestBenchEvalProveNovelConcurrentExecutionsRecordExactlyOneProof(t *testing.T) {
	bin := buildCLI(t)
	fx := writeProveNovelFixture(t, bin, "node --test")

	first := startCLI(t, bin, append(proveNovelArgs(fx), "--execute")...)
	second := startCLI(t, bin, append(proveNovelArgs(fx), "--execute")...)
	type outcome struct {
		code int
		out  string
	}
	runs := []outcome{
		{first.code(t, first.awaitExit(t, proveNovelFinishWait)), first.output(t)},
		{second.code(t, second.awaitExit(t, proveNovelFinishWait)), second.output(t)},
	}
	var recorded, refused int
	for _, run := range runs {
		switch {
		case run.code == 0 && strings.Contains(run.out, "recorded novel-proof/2 proof for c1/c1-f1"):
			recorded++
		case run.code == 1 && strings.Contains(run.out, "already has an effective novel proof"):
			refused++
		default:
			t.Fatalf("neither a recorded proof nor the duplicate refusal (exit %d):\n%s", run.code, run.out)
		}
	}
	if recorded != 1 || refused != 1 {
		t.Fatalf("got %d recorded proof(s) and %d duplicate refusal(s), want exactly one of each:\nfirst:\n%s\nsecond:\n%s",
			recorded, refused, runs[0].out, runs[1].out)
	}
	events, err := os.ReadFile(filepath.Join(fx.runDir, "c1", "events.jsonl"))
	if err != nil {
		t.Fatal(err)
	}
	if got := bytes.Count(events, []byte(`"event":"novel_proof"`)); got != 1 {
		t.Fatalf("events.jsonl holds %d novel_proof events after the race, want exactly 1", got)
	}
}

func fileSize(path string) (int64, error) {
	info, err := os.Stat(path)
	if err != nil {
		return 0, err
	}
	return info.Size(), nil
}

// TestBenchEvalProveNovelRefusalsDoNotMutate is the refusal matrix: every preflight or
// replay refusal exits non-zero with run.json, caserun.json and events.jsonl byte-identical.
func TestBenchEvalProveNovelRefusalsDoNotMutate(t *testing.T) {
	bin := buildCLI(t)
	fx := writeProveNovelFixture(t, bin, "node --test")

	// refuse runs args against the shared fixture and asserts exit code, refusal message,
	// and byte-identical run files.
	refuse := func(t *testing.T, wantCode int, want string, args ...string) {
		t.Helper()
		before := snapshotRunDir(t, fx.runDir)
		out, code := runCLI(t, bin, args...)
		if code != wantCode {
			t.Fatalf("exited %d, want %d:\n%s", code, wantCode, out)
		}
		if !strings.Contains(out, want) {
			t.Fatalf("output missing %q:\n%s", want, out)
		}
		assertRunDirUnchanged(t, fx.runDir, before)
	}

	t.Run("missing blind fact", func(t *testing.T) {
		refuse(t, 2, "--mechanism", withoutFlag(proveNovelArgs(fx), "--mechanism")...)
	})
	t.Run("bad control name", func(t *testing.T) {
		refuse(t, 2, "--control-name", withFlag(proveNovelArgs(fx), "--control-name", "../x")...)
	})
	t.Run("missing control dir", func(t *testing.T) {
		refuse(t, 1, "control directory", withFlag(proveNovelArgs(fx), "--control-dir", filepath.Join(fx.root, "no-such"))...)
	})
	t.Run("finding not novel candidate", func(t *testing.T) {
		refuse(t, 1, "NOVEL_CANDIDATE", withFlag(proveNovelArgs(fx), "--finding", "c1-f2")...)
	})
	t.Run("unsupported go suite", func(t *testing.T) {
		goFx := writeProveNovelFixture(t, bin, "go test")
		before := snapshotRunDir(t, goFx.runDir)
		out, code := runCLI(t, bin, proveNovelArgs(goFx)...)
		if code != 1 {
			t.Fatalf("go suite exited %d, want 1:\n%s", code, out)
		}
		if !strings.Contains(out, "node --test") {
			t.Fatalf("go-suite refusal must name the supported runtime:\n%s", out)
		}
		assertRunDirUnchanged(t, goFx.runDir, before)
	})
	t.Run("inconclusive replay when the control also fails", func(t *testing.T) {
		args := withFlag(proveNovelArgs(fx), "--control-dir", fx.controlFail)
		before := snapshotRunDir(t, fx.runDir)
		out, code := runCLI(t, bin, append(args, "--execute")...)
		if code != 1 {
			t.Fatalf("inconclusive replay exited %d, want 1:\n%s", code, out)
		}
		if !strings.Contains(out, "not conclusive") || !strings.Contains(out, "assertion_failure") {
			t.Fatalf("inconclusive refusal must print the observation classification:\n%s", out)
		}
		assertRunDirUnchanged(t, fx.runDir, before)
	})
	t.Run("lock unavailable refuses before mutation", func(t *testing.T) {
		// A run-dir lock needs a per-user cache directory; with neither $XDG_CACHE_HOME nor
		// $HOME set there is none, and the command must refuse before LoadRun — never run
		// unlocked. (On a platform whose lock primitive exists but cannot be taken the same
		// refusal fires: LockPlan's error is the only path to this message.)
		before := snapshotRunDir(t, fx.runDir)
		out, code := runCLIEnv(t, bin, []string{"HOME=", "XDG_CACHE_HOME="}, append(proveNovelArgs(fx), "--execute")...)
		if code != 1 {
			t.Fatalf("exited %d, want 1:\n%s", code, out)
		}
		if !strings.Contains(out, "run directory lock") || !strings.Contains(out, "refusing to run unlocked") {
			t.Fatalf("lock-failure refusal must name the run directory lock and the unlocked refusal:\n%s", out)
		}
		assertRunDirUnchanged(t, fx.runDir, before)
	})
	t.Run("tampered event chain", func(t *testing.T) {
		eventsPath := filepath.Join(fx.runDir, "c1", "events.jsonl")
		data, err := os.ReadFile(eventsPath)
		if err != nil {
			t.Fatal(err)
		}
		tampered := regexp.MustCompile(`"new_state":"[A-Z_]+"`).ReplaceAllString(string(data), `"new_state":"TAMPERED_STATE"`)
		if tampered == string(data) {
			t.Fatalf("no event state found to tamper in %s", eventsPath)
		}
		if err := os.WriteFile(eventsPath, []byte(tampered), 0o644); err != nil {
			t.Fatal(err)
		}
		before := snapshotRunDir(t, fx.runDir)
		out, code := runCLI(t, bin, append(proveNovelArgs(fx), "--execute")...)
		if code != 1 {
			t.Fatalf("tampered chain exited %d, want 1:\n%s", code, out)
		}
		if !strings.Contains(out, "event chain") {
			t.Fatalf("tampered-chain refusal must name the event chain:\n%s", out)
		}
		assertRunDirUnchanged(t, fx.runDir, before)
	})
}

// TestBenchEvalProveNovelListedInUsageAndHelp pins the synopsis entry and the safety
// contract in the command help: no sandbox claim, repository code executes.
func TestBenchEvalProveNovelListedInUsageAndHelp(t *testing.T) {
	bin := buildCLI(t)
	out, code := runCLI(t, bin, "bench", "eval")
	if code != 2 || !strings.Contains(out, "prove-novel") {
		t.Fatalf("bench eval usage (exit %d) must list prove-novel:\n%s", code, out)
	}
	out, code = runCLI(t, bin, "bench", "eval", "prove-novel", "-h")
	if code != 2 {
		t.Fatalf("prove-novel -h exited %d, want 2:\n%s", code, out)
	}
	if !strings.Contains(out, "no sandbox or secret isolation; tests execute repository code") {
		t.Fatalf("prove-novel help must state the execution safety contract:\n%s", out)
	}
}

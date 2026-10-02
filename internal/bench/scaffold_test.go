package bench

import (
	"context"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// A minimal node:test fixture whose suite is green, plus a sealed key next to it.
func fakeCase(t *testing.T) string {
	t.Helper()
	caseDir := filepath.Join(t.TempDir(), "case-01")
	fx := filepath.Join(caseDir, FixtureDir)
	for p, body := range map[string]string{
		"package.json":     `{"name":"fx","type":"module","scripts":{"test":"node --test"}}`,
		"src/a.mjs":        "export const add = (a, b) => a + b;\n",
		"tests/a.test.mjs": "import { test } from 'node:test';\nimport assert from 'node:assert/strict';\nimport { add } from '../src/a.mjs';\ntest('adds', () => assert.equal(add(1, 2), 3));\n",
	} {
		full := filepath.Join(fx, p)
		if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(full, []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	key := `{"id":"case-01","language":"node","suite":"node --test","surface":"lib","defects":[{"id":"d1","file":"src/a.mjs","line":1,"class":"arith","keywords":["overflow"],"description":"x","trigger":{"input":"i","expected":"e","actual":"a"},"why_missed":"w"}]}`
	if err := os.WriteFile(filepath.Join(caseDir, KeyFile), []byte(key), 0o644); err != nil {
		t.Fatal(err)
	}
	return caseDir
}

func requireNodeAndGit(t *testing.T) {
	t.Helper()
	if testing.Short() {
		t.Skip("integration test skipped in -short")
	}
	for _, bin := range []string{"node", "git"} {
		if _, err := exec.LookPath(bin); err != nil {
			t.Skipf("%s not on PATH", bin)
		}
	}
}

func TestScaffoldCopiesFixtureNeverTheKey(t *testing.T) {
	requireNodeAndGit(t)
	caseDir := fakeCase(t)
	key, err := LoadKey(caseDir)
	if err != nil {
		t.Fatal(err)
	}
	ws := filepath.Join(t.TempDir(), "out", "case-01", "1", "ws")
	suite, err := Scaffold(caseDir, ws, key, time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(ws, "src", "a.mjs")); err != nil {
		t.Fatal("fixture not copied")
	}
	if _, err := os.Stat(filepath.Join(ws, KeyFile)); err == nil {
		t.Fatal("answer key must never reach the workspace")
	}
	out, err := exec.Command("git", "-C", ws, "log", "--oneline").Output()
	if err != nil || strings.Count(strings.TrimSpace(string(out)), "\n")+1 != 1 {
		t.Fatalf("expected exactly one commit: %v %s", err, out)
	}
	if !suite.Green || suite.Passed < 1 || suite.Failed != 0 {
		t.Fatalf("suite = %+v", suite)
	}
}

func TestParseCounts(t *testing.T) {
	cases := []struct {
		name         string
		out          string
		pass, failed int
	}{
		{"node summary", "ℹ tests 3\nℹ pass 2\nℹ fail 1\n", 2, 1},
		{"go packages", "ok  \tx/a\t0.1s\nFAIL\tx/b\t0.2s\n--- FAIL: TestZ\n", 1, 2},
		{"nothing recognizable", "hello", 0, 0},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			p, f := parseCounts(tc.out)
			if p != tc.pass || f != tc.failed {
				t.Fatalf("got %d/%d, want %d/%d", p, f, tc.pass, tc.failed)
			}
		})
	}
}

func TestScaffoldRejectsMissingFixture(t *testing.T) {
	if _, err := Scaffold(t.TempDir(), filepath.Join(t.TempDir(), "ws"), Key{Suite: "true"}, time.Second); err == nil {
		t.Fatal("expected an error for a case without a fixture directory")
	}
}

// A pre-canceled context stops the scaffold before it copies a byte or spawns anything: the
// error it returns is the context's own cancellation, and the workspace it never touched
// stays absent.
func TestScaffoldContextStopsBeforeCopyOrSubprocessWhenCanceled(t *testing.T) {
	caseDir := fakeCase(t)
	key, err := LoadKey(caseDir)
	if err != nil {
		t.Fatal(err)
	}
	ws := filepath.Join(t.TempDir(), "ws")
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := ScaffoldContext(ctx, caseDir, ws, key, time.Minute); !errors.Is(err, context.Canceled) {
		t.Fatalf("err = %v, want the context's own cancellation", err)
	}
	if _, err := os.Stat(ws); !os.IsNotExist(err) {
		t.Fatalf("workspace exists (stat err = %v): a pre-canceled scaffold must copy and spawn nothing", err)
	}
}

// The whole pipeline with an agent double that writes a plan: no claude is spawned.
func TestRunWithFakeAgent(t *testing.T) {
	requireNodeAndGit(t)
	caseDir := fakeCase(t)
	out := filepath.Join(t.TempDir(), "results")
	benchDir := filepath.Join(t.TempDir(), "bench")
	agent := func(ctx context.Context, ws string, key Key, opts Options) (AgentResult, error) {
		if err := os.MkdirAll(filepath.Join(ws, "docs", "testing"), 0o755); err != nil {
			return AgentResult{}, err
		}
		p := plan("| F1 | `src/a.mjs:1` integer overflow on add | M | yes | E1 | open | me | - | - |\n", "| E1 | c | cmd | i | o | m | r | observado |\n")
		return AgentResult{Result: "done", CostUSD: 0.42, Turns: 5}, os.WriteFile(filepath.Join(ws, PlanPath), []byte(p), 0o644)
	}
	agg, code := Run(Options{
		CasesGlob: filepath.Join(filepath.Dir(caseDir), "case-*"), Model: "fake", Runs: 1, MaxTurns: 3,
		Timeout: time.Minute, SuiteTimeout: time.Minute, Out: out, BenchDir: benchDir, Agent: agent,
	})
	if code != 0 {
		t.Fatalf("exit code %d", code)
	}
	if agg.Defects != 1 || agg.Found != 1 || agg.Recall != 1 || agg.CostUSD != 0.42 {
		t.Fatalf("aggregate = %+v", agg)
	}
	for _, f := range []string{"aggregate.json", "summary.md", filepath.Join("case-01", "1", "result.json")} {
		if _, err := os.Stat(filepath.Join(out, f)); err != nil {
			t.Fatalf("missing %s", f)
		}
	}
	if _, err := os.Stat(filepath.Join(out, "case-01", "1", "ws")); err == nil {
		t.Fatal("workspace should be removed after a successful scored run")
	}
	hist, err := os.ReadFile(filepath.Join(benchDir, "history.jsonl"))
	if err != nil || !strings.Contains(string(hist), `"recall":1`) {
		t.Fatalf("history not appended: %v %s", err, hist)
	}
	summary, _ := os.ReadFile(filepath.Join(out, "summary.md"))
	if !strings.Contains(string(summary), "| case-01 | 1/1 | 0/1 | 0/1 | no | no | 0 | 0.420 | 5 |") {
		t.Fatalf("summary row: %s", summary)
	}
}

func TestRunDryRunSpawnsNothingAndWritesNoHistory(t *testing.T) {
	requireNodeAndGit(t)
	caseDir := fakeCase(t)
	out := filepath.Join(t.TempDir(), "results")
	benchDir := filepath.Join(t.TempDir(), "bench")
	called := false
	agent := func(ctx context.Context, ws string, key Key, opts Options) (AgentResult, error) {
		called = true
		return AgentResult{}, nil
	}
	agg, code := Run(Options{CasesGlob: caseDir, Runs: 1, Timeout: time.Minute, SuiteTimeout: time.Minute, Out: out, BenchDir: benchDir, DryRun: true, Agent: agent})
	if code != 0 || called || agg.Recall != 0 {
		t.Fatalf("code %d called %v agg %+v", code, called, agg)
	}
	if _, err := os.Stat(filepath.Join(benchDir, "history.jsonl")); err == nil {
		t.Fatal("dry-run must not append history")
	}
}

func TestRunStopsAtTheCostCeiling(t *testing.T) {
	requireNodeAndGit(t)
	caseDir := fakeCase(t)
	out := filepath.Join(t.TempDir(), "results")
	calls := 0
	agent := func(ctx context.Context, ws string, key Key, opts Options) (AgentResult, error) {
		calls++
		return AgentResult{CostUSD: 3}, nil
	}
	_, code := Run(Options{CasesGlob: caseDir, Runs: 3, Timeout: time.Minute, SuiteTimeout: time.Minute, Out: out, MaxCostUSD: 5, Agent: agent})
	if code != ExitCostCeiling || calls != 2 {
		t.Fatalf("code %d calls %d", code, calls)
	}
}

func TestRunWithNoCases(t *testing.T) {
	if _, code := Run(Options{CasesGlob: filepath.Join(t.TempDir(), "none-*"), Out: t.TempDir()}); code != 1 {
		t.Fatalf("code = %d, want 1", code)
	}
}

package bench

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"
)

// The corpus digest must not depend on iteration order, and must change whenever the corpus
// changes: a renamed case, a renamed defect, a defect added, a defect removed.
func TestCorpusDigestIsStableAndSensitive(t *testing.T) {
	base := []CorpusCase{
		{Name: "case-a", Defects: []string{"D2", "D1"}},
		{Name: "case-b", Defects: []string{"D1"}},
	}
	reordered := []CorpusCase{
		{Name: "case-b", Defects: []string{"D1"}},
		{Name: "case-a", Defects: []string{"D1", "D2"}},
	}
	got := CorpusDigest(base, 1)
	if want := "sha256:24078e5b39423c52"; got != want {
		t.Fatalf("digest = %q, want %q", got, want)
	}
	if len(got) != len("sha256:")+16 || got[:len("sha256:")] != "sha256:" {
		t.Fatalf("digest %q is not sha256: plus 16 hex characters", got)
	}
	if CorpusDigest(reordered, 1) != got {
		t.Fatalf("case and defect order must not change the digest: %s != %s", CorpusDigest(reordered, 1), got)
	}
	for name, cases := range map[string][]CorpusCase{
		"changed defect id": {{Name: "case-a", Defects: []string{"D3", "D1"}}, {Name: "case-b", Defects: []string{"D1"}}},
		"changed case name": {{Name: "case-c", Defects: []string{"D2", "D1"}}, {Name: "case-b", Defects: []string{"D1"}}},
		"added defect":      {{Name: "case-a", Defects: []string{"D2", "D1"}}, {Name: "case-b", Defects: []string{"D1", "D2"}}},
		"removed defect":    {{Name: "case-a", Defects: []string{"D1"}}, {Name: "case-b", Defects: []string{"D1"}}},
	} {
		if CorpusDigest(cases, 1) == got {
			t.Errorf("%s must change the digest", name)
		}
	}
}

// A reading is comparable to another only when both measured the same thing: the same cases, asked the
// same way, run the same number of times. A digest that ignored the request or the run count let a
// three-run row with a tripled denominator sit beside one-run rows as though the series were
// like-for-like, which is what a review caught in the recorded history.
func TestCorpusDigestSeparatesDifferentMeasurements(t *testing.T) {
	plain := []CorpusCase{{Name: "case-a", Defects: []string{"D1"}}}
	asked := []CorpusCase{{Name: "case-a", Request: "the clamp behaviour", Defects: []string{"D1"}}}
	if CorpusDigest(plain, 1) == CorpusDigest(asked, 1) {
		t.Fatal("a case asked for a bounded request is a different measurement")
	}
	if CorpusDigest(plain, 1) == CorpusDigest(plain, 3) {
		t.Fatal("three runs triples the denominator, so it is a different measurement")
	}
	if CorpusDigest(asked, 3) == CorpusDigest(asked, 2) {
		t.Fatal("the run count must change the digest")
	}
	// The parts are length-prefixed. Joined with a bare separator, a name or an id that carries it
	// would impersonate two parts and two different corpora would hash to one value.
	one := []CorpusCase{{Name: "a:b", Defects: []string{"c"}}}
	two := []CorpusCase{{Name: "a", Defects: []string{"b:c"}}}
	if CorpusDigest(one, 1) == CorpusDigest(two, 1) {
		t.Fatalf("a separator inside a name or an id must not merge two corpora: %q", CorpusDigest(one, 1))
	}
}

// fakeCaseNamed writes the same shape of case as fakeCase under a chosen name: the ordering and
// concurrency assertions need cases whose names differ.
func fakeCaseNamed(t *testing.T, root, name string) string {
	t.Helper()
	caseDir := filepath.Join(root, name)
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
	key := fmt.Sprintf(`{"id":%q,"language":"node","suite":"node --test","surface":"lib","defects":[{"id":"d1","file":"src/a.mjs","line":1,"class":"arith","keywords":["overflow"],"description":"x","trigger":{"input":"i","expected":"e","actual":"a"},"why_missed":"w"}]}`, name)
	if err := os.WriteFile(filepath.Join(caseDir, KeyFile), []byte(key), 0o644); err != nil {
		t.Fatal(err)
	}
	return caseDir
}

// setCaseRequest writes a bounded request into a case's sealed key, the way a corpus author would.
func setCaseRequest(t *testing.T, caseDir, value string) {
	t.Helper()
	path := filepath.Join(caseDir, KeyFile)
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	patched := strings.Replace(string(raw), `"surface":`, `"request":`+value+`,"surface":`, 1)
	if patched == string(raw) {
		t.Fatal("the key shape moved: the request was never added")
	}
	if err := os.WriteFile(path, []byte(patched), 0o644); err != nil {
		t.Fatal(err)
	}
}

// Every invocation sees its own case's request: it travels by value with the key, so two cases
// scheduled together cannot borrow each other's scope.
func TestRunHandsEachCaseItsOwnRequest(t *testing.T) {
	requireNodeAndGit(t)
	root := t.TempDir()
	requests := map[string]string{"case-a": "src/a.mjs", "case-b": "src/b.mjs"}
	var globs []string
	for _, name := range []string{"case-a", "case-b"} {
		dir := fakeCaseNamed(t, root, name)
		setCaseRequest(t, dir, `"`+requests[name]+`"`)
		globs = append(globs, dir)
	}
	var mu sync.Mutex
	seen := map[string]string{}
	agent := func(ctx context.Context, ws string, key Key, opts Options) (AgentResult, error) {
		mu.Lock()
		for _, name := range []string{"case-a", "case-b"} {
			if strings.Contains(ws, name) {
				seen[name] = key.RequestText()
			}
		}
		mu.Unlock()
		return AgentResult{Result: "nothing found", CostUSD: 0.01, Turns: 2}, nil
	}
	agg, code := Run(Options{CasesGlob: strings.Join(globs, ","), Runs: 1, Workers: 2, Timeout: time.Minute, SuiteTimeout: time.Minute, Out: t.TempDir(), BenchDir: t.TempDir(), Agent: agent})
	if code != 0 {
		t.Fatalf("exit code %d", code)
	}
	if len(agg.Cases) != len(requests) {
		t.Fatalf("%d cases in the aggregate, want %d", len(agg.Cases), len(requests))
	}
	for name, want := range requests {
		if got := seen[name]; got != want {
			t.Fatalf("%s ran with request %q, want %q (all: %v)", name, got, want, seen)
		}
	}
}

// The request changes what a run is asked to do, so it changes the measurement: the digest has to move
// with it. It did not, and the recorded history then held a reading with requests beside readings
// without them as though one series covered both.
func TestCorpusDigestMovesWithTheCaseRequest(t *testing.T) {
	requireNodeAndGit(t)
	caseDir := fakeCaseNamed(t, t.TempDir(), "case-a")
	agent := func(ctx context.Context, ws string, key Key, opts Options) (AgentResult, error) {
		return AgentResult{Result: "nothing found", CostUSD: 0.01, Turns: 2}, nil
	}
	digest := func(runs int) string {
		agg, code := Run(Options{CasesGlob: caseDir, Runs: runs, Timeout: time.Minute, SuiteTimeout: time.Minute, Out: t.TempDir(), BenchDir: t.TempDir(), Agent: agent})
		if code != 0 {
			t.Fatalf("exit code %d", code)
		}
		return agg.Corpus
	}
	generic := digest(1)
	setCaseRequest(t, caseDir, `"src/a.mjs"`)
	if asked := digest(1); asked == generic {
		t.Fatalf("a case asked for a request is a different measurement, yet both digest to %q", generic)
	}
	// And the run count is part of the measurement too: three runs triple the denominator.
	if three := digest(3); three == digest(1) {
		t.Fatalf("three runs and one run digest to the same value: %q", three)
	}
}

// A reading has to be able to say whether the mode ran: the run records it per case, the aggregate
// counts it, and the summary prints it. A case that only found the defect reports no activation.
func TestRunReportsActivationPerCaseAndInTheAggregate(t *testing.T) {
	requireNodeAndGit(t)
	root := t.TempDir()
	plans := map[string]string{
		"case-scoped": "Light: a · touches cli\n\n" + plan("", ""),
		"case-plain":  plan("", ""),
	}
	var globs []string
	for _, name := range []string{"case-scoped", "case-plain"} {
		globs = append(globs, fakeCaseNamed(t, root, name))
	}
	agent := func(ctx context.Context, ws string, key Key, opts Options) (AgentResult, error) {
		for name, doc := range plans {
			if strings.Contains(ws, name) {
				writePlan(t, ws, doc)
			}
		}
		return AgentResult{Result: "nothing found", CostUSD: 0.01, Turns: 2}, nil
	}
	agg, code := Run(Options{CasesGlob: strings.Join(globs, ","), Runs: 1, Workers: 2, Timeout: time.Minute, SuiteTimeout: time.Minute, Out: t.TempDir(), BenchDir: t.TempDir(), Agent: agent})
	if code != 0 {
		t.Fatalf("exit code %d", code)
	}
	if len(agg.Cases) != len(plans) {
		t.Fatalf("%d cases in the aggregate, want %d", len(agg.Cases), len(plans))
	}
	if agg.LightActivated != 1 {
		t.Fatalf("aggregate activation = %d, want 1", agg.LightActivated)
	}
	for _, c := range agg.Cases {
		if want := c.Case == "case-scoped"; c.LightActivated != want {
			t.Fatalf("%s activation = %v, want %v", c.Case, c.LightActivated, want)
		}
	}
	if summary := Summary(agg); !strings.Contains(summary, "light runs: 1") {
		t.Fatalf("the summary must report the count:\n%s", summary)
	}
}

// The prompt is the whole instruction a run gets: a case without a request receives exactly the prompt
// the bench has always sent, and a case with one names it without deciding the mode, which is the
// skill's call and the thing a reading measures.
func TestAgentPromptCarriesTheRequestOnlyWhenThereIsOne(t *testing.T) {
	if got := agentPrompt(Key{}); got != Prompt {
		t.Fatalf("a generic case got %q, want exactly %q", got, Prompt)
	}
	request := "src/slug.js"
	got := agentPrompt(Key{Request: &request})
	if !strings.HasPrefix(got, Prompt) {
		t.Fatalf("the prompt must stay the instruction: %q", got)
	}
	if !strings.Contains(got, request) {
		t.Fatalf("the request must reach the prompt: %q", got)
	}
	for _, word := range []string{"Light", "scoped", "blast radius"} {
		if strings.Contains(got, word) {
			t.Fatalf("the framing must not decide the mode: %q appears in %q", word, got)
		}
	}
}

// Both runners send the same framing, so a reading never depends on which one ran it. The fake CLIs
// record their arguments: the request has to arrive as the -p value of each.
func TestBothRunnersSendTheSameRequestFraming(t *testing.T) {
	bin := t.TempDir()
	for _, runner := range []string{"pi", "claude"} {
		record := filepath.Join(bin, runner+".argv")
		script := "#!/bin/sh\nprintf '%s\\036' \"$@\" > " + record + "\nexit 3\n"
		if err := os.WriteFile(filepath.Join(bin, runner), []byte(script), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	t.Setenv("PATH", bin+string(os.PathListSeparator)+os.Getenv("PATH"))
	request := "src/slug.js"
	key := Key{Request: &request}
	ws := t.TempDir()
	// Both fake CLIs exit non-zero, which is fine: what this checks is what they were told.
	if _, err := piAgent(context.Background(), ws, key, Options{}); err == nil {
		t.Fatal("the fake pi exits non-zero, so the run reports it")
	}
	if _, err := claudeAgent(context.Background(), ws, key, Options{MaxTurns: 1}); err == nil {
		t.Fatal("the fake claude exits non-zero, so the run reports it")
	}
	want := agentPrompt(key)
	for _, runner := range []string{"pi", "claude"} {
		raw, err := os.ReadFile(filepath.Join(bin, runner+".argv"))
		if err != nil {
			t.Fatalf("%s never ran: %v", runner, err)
		}
		args := strings.Split(strings.TrimSuffix(string(raw), "\x1e"), "\x1e")
		got := ""
		for i, a := range args {
			if a == "-p" && i+1 < len(args) {
				got = args[i+1]
				break
			}
		}
		if got != want {
			t.Fatalf("%s sent %q, want %q", runner, got, want)
		}
	}
}

// A run told to use workers must reach the pool: a sequential loop produces the same aggregate as
// a concurrent one, so nothing else in this file can see whether the flag does anything.
func TestRunRunsCasesSideBySideWithWorkers(t *testing.T) {
	requireNodeAndGit(t)
	root := t.TempDir()
	var globs []string
	for _, name := range []string{"case-a", "case-b", "case-c"} {
		globs = append(globs, fakeCaseNamed(t, root, name))
	}
	var mu sync.Mutex
	live, peak := 0, 0
	agent := func(ctx context.Context, ws string, key Key, opts Options) (AgentResult, error) {
		mu.Lock()
		live++
		if live > peak {
			peak = live
		}
		mu.Unlock()
		// Hold the slot so two cases can be inside at once. However slow this is, a sequential run
		// can never report a peak above one.
		time.Sleep(200 * time.Millisecond)
		mu.Lock()
		live--
		mu.Unlock()
		return AgentResult{Result: "nothing found", CostUSD: 0.01, Turns: 2}, nil
	}
	agg, code := Run(Options{CasesGlob: strings.Join(globs, ","), Runs: 1, Workers: 3, Timeout: time.Minute, SuiteTimeout: time.Minute, Out: t.TempDir(), BenchDir: t.TempDir(), Agent: agent})
	if code != 0 {
		t.Fatalf("exit code %d", code)
	}
	if len(agg.Cases) != 3 {
		t.Fatalf("%d cases in the aggregate, want 3", len(agg.Cases))
	}
	if peak < 2 {
		t.Fatalf("Workers 3 ran at most %d case(s) at once: --concurrency never reaches the pool", peak)
	}
	if peak > 3 {
		t.Fatalf("%d cases ran at once, want at most 3", peak)
	}
}

// Whatever finishes first, the aggregate must stay in case order: a report that reorders itself
// when the machine is busy is not the same report.
func TestRunKeepsCaseOrderWhateverFinishesFirst(t *testing.T) {
	requireNodeAndGit(t)
	root := t.TempDir()
	names := []string{"case-a", "case-b", "case-c"}
	var globs []string
	for _, name := range names {
		globs = append(globs, fakeCaseNamed(t, root, name))
	}
	agent := func(ctx context.Context, ws string, key Key, opts Options) (AgentResult, error) {
		for i, name := range names {
			if strings.Contains(ws, name) {
				// The first case is the slowest, so completion order is the reverse of case order.
				time.Sleep(time.Duration(len(names)-i) * 150 * time.Millisecond)
			}
		}
		return AgentResult{Result: "nothing found", CostUSD: 0.01, Turns: 2}, nil
	}
	agg, code := Run(Options{CasesGlob: strings.Join(globs, ","), Runs: 2, Workers: 3, Timeout: time.Minute, SuiteTimeout: time.Minute, Out: t.TempDir(), BenchDir: t.TempDir(), Agent: agent})
	if code != 0 {
		t.Fatalf("exit code %d", code)
	}
	if want := len(names) * 2; len(agg.Cases) != want {
		t.Fatalf("%d cases in the aggregate, want %d", len(agg.Cases), want)
	}
	i := 0
	for _, name := range names {
		for run := 1; run <= 2; run++ {
			if got := agg.Cases[i]; got.Case != name || got.Run != run {
				t.Fatalf("aggregate[%d] = %s #%d, want %s #%d: the order followed completion", i, got.Case, got.Run, name, run)
			}
			i++
		}
	}
}

// The ceiling stops launching work once it is crossed while the units already in flight finish,
// which is why the total can land just past the ceiling.
func TestRunCostCeilingStopsLaunchingMoreCases(t *testing.T) {
	requireNodeAndGit(t)
	root := t.TempDir()
	var globs []string
	for _, name := range []string{"case-a", "case-b", "case-c", "case-d"} {
		globs = append(globs, fakeCaseNamed(t, root, name))
	}
	agent := func(ctx context.Context, ws string, key Key, opts Options) (AgentResult, error) {
		time.Sleep(150 * time.Millisecond)
		return AgentResult{Result: "nothing found", CostUSD: 1.00, Turns: 2}, nil
	}
	agg, code := Run(Options{CasesGlob: strings.Join(globs, ","), Runs: 1, Workers: 2, MaxCostUSD: 1.00, Timeout: time.Minute, SuiteTimeout: time.Minute, Out: t.TempDir(), BenchDir: t.TempDir(), Agent: agent})
	if code != ExitCostCeiling {
		t.Fatalf("exit code %d, want %d", code, ExitCostCeiling)
	}
	if !agg.CostCeilingHit {
		t.Fatal("the aggregate must record that the ceiling was hit")
	}
	if len(agg.Cases) != 2 {
		t.Fatalf("%d cases ran, want the 2 that were already in flight", len(agg.Cases))
	}
	if agg.CostUSD < 1.00 {
		t.Fatalf("cost %v, want the in-flight units counted past the ceiling", agg.CostUSD)
	}
}

// syncBuffer is a log sink safe to read while the run is still writing to it.
type syncBuffer struct {
	mu sync.Mutex
	b  bytes.Buffer
}

func (s *syncBuffer) Write(p []byte) (int, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.b.Write(p)
}

func (s *syncBuffer) String() string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.b.String()
}

// A run must say what finished while it is still running: the aggregate is ordered by case, but the
// log is progress, and silence until the end hides a corpus that stalled on its first case.
func TestRunLogsEachCaseAsItFinishes(t *testing.T) {
	requireNodeAndGit(t)
	root := t.TempDir()
	var globs []string
	for _, name := range []string{"case-a", "case-b", "case-c"} {
		globs = append(globs, fakeCaseNamed(t, root, name))
	}
	release := make(chan struct{})
	agent := func(ctx context.Context, ws string, key Key, opts Options) (AgentResult, error) {
		if strings.Contains(ws, "case-c") {
			// Hold the last case open until the test has seen the other two lines.
			<-release
		}
		return AgentResult{Result: "nothing found", CostUSD: 0.01, Turns: 2}, nil
	}
	log := &syncBuffer{}
	done := make(chan Aggregate, 1)
	go func() {
		agg, _ := Run(Options{CasesGlob: strings.Join(globs, ","), Runs: 1, Workers: 3, Timeout: time.Minute, SuiteTimeout: time.Minute, Out: t.TempDir(), BenchDir: t.TempDir(), Agent: agent, Log: log})
		done <- agg
	}()
	deadline := time.Now().Add(15 * time.Second)
	for time.Now().Before(deadline) {
		if seen := log.String(); strings.Contains(seen, "case-a") && strings.Contains(seen, "case-b") {
			break
		}
		time.Sleep(50 * time.Millisecond)
	}
	seen := log.String()
	close(release)
	agg := <-done
	for _, name := range []string{"case-a", "case-b"} {
		if !strings.Contains(seen, name) {
			t.Fatalf("the log said nothing about %s while it had already finished; it was:\n%s", name, seen)
		}
	}
	if len(agg.Cases) != 3 {
		t.Fatalf("%d cases in the aggregate, want 3", len(agg.Cases))
	}
}

// A run records the corpus it measured in the aggregate and in its history row, so a number
// whose corpus changed under it cannot read as comparable.
func TestRunRecordsCorpusInHistory(t *testing.T) {
	if testing.Short() {
		t.Skip("spawns node and git")
	}
	caseDir := fakeCase(t)
	benchDir := t.TempDir()
	agent := func(ctx context.Context, ws string, key Key, opts Options) (AgentResult, error) {
		return AgentResult{Result: "nothing found", CostUSD: 0.1, Turns: 3}, nil
	}
	agg, _ := Run(Options{CasesGlob: caseDir, Runs: 1, Timeout: time.Minute, SuiteTimeout: time.Minute, Out: t.TempDir(), BenchDir: benchDir, Agent: agent})
	want := "sha256:1151e18c0107a49d" // case-01 with its single defect d1, no request, one run
	if agg.Corpus != want {
		t.Fatalf("aggregate corpus = %q, want %q", agg.Corpus, want)
	}
	data, err := os.ReadFile(filepath.Join(benchDir, "history.jsonl"))
	if err != nil {
		t.Fatal(err)
	}
	var row HistoryEntry
	if err := json.Unmarshal(data, &row); err != nil {
		t.Fatal(err)
	}
	if row.Corpus != want {
		t.Fatalf("history row corpus = %q, want %q", row.Corpus, want)
	}
}

// A run whose record cannot be written must say so: the numbers exist and nothing persisted them, which is a
// failure a reader has to see rather than a run that looks recorded.
func TestRunReportsTheRecordItCouldNotWrite(t *testing.T) {
	requireNodeAndGit(t)
	root := t.TempDir()
	caseDir := fakeCaseNamed(t, root, "case-a")

	for _, artifact := range []string{"aggregate.json", "summary.md"} {
		t.Run(artifact, func(t *testing.T) {
			out := t.TempDir()
			// A directory where the file has to go: this write fails and nothing else in the run does.
			if err := os.MkdirAll(filepath.Join(out, artifact), 0o755); err != nil {
				t.Fatal(err)
			}
			var log strings.Builder
			agg, code := Run(Options{CasesGlob: caseDir, Runs: 1, DryRun: true, Out: out,
				BenchDir: t.TempDir(), SuiteTimeout: time.Minute, Log: &log})
			if code != ExitArtifact {
				t.Fatalf("code = %d, want %d: the %s was not written", code, ExitArtifact, artifact)
			}
			if !strings.Contains(log.String(), artifact) {
				t.Fatalf("the log does not name %s: %q", artifact, log.String())
			}
			if agg.Out != out {
				t.Fatalf("the numbers walked away with the failure: %+v", agg)
			}
		})
	}
}

// The history row is the measurement's own record: a run that cannot append it must not exit as recorded.
// A dry run records nothing, so this one runs for real with an agent that does nothing.
func TestRunReportsAHistoryRowItCouldNotAppend(t *testing.T) {
	requireNodeAndGit(t)
	caseDir := fakeCaseNamed(t, t.TempDir(), "case-a")
	benchDir := t.TempDir()
	// A directory where the jsonl file has to go: appending the row fails.
	if err := os.MkdirAll(filepath.Join(benchDir, "history.jsonl"), 0o755); err != nil {
		t.Fatal(err)
	}
	agent := func(ctx context.Context, ws string, key Key, opts Options) (AgentResult, error) {
		return AgentResult{Result: "nothing found", CostUSD: 0.01, Turns: 2}, nil
	}
	var log strings.Builder
	_, code := Run(Options{CasesGlob: caseDir, Runs: 1, Out: t.TempDir(), Agent: agent,
		BenchDir: benchDir, Timeout: time.Minute, SuiteTimeout: time.Minute, Log: &log})
	if code != ExitArtifact {
		t.Fatalf("code = %d, want %d: the history row was not written", code, ExitArtifact)
	}
	if !strings.Contains(log.String(), "history") {
		t.Fatalf("the log does not name the history: %q", log.String())
	}
}

// The per-case result is the evidence the aggregate reads: a case whose record cannot be written is reported
// as failed rather than counted from a file that is not there.
func TestACaseWhoseResultCannotBeWrittenIsReportedFailed(t *testing.T) {
	out := t.TempDir()
	res := Result{Case: "case-a", Run: 1, Total: 2, Workspace: filepath.Join(out, "case-a", "1", "ws")}
	if err := os.MkdirAll(filepath.Join(out, "case-a", "1", "result.json"), 0o755); err != nil {
		t.Fatal(err)
	}
	got := finish(res, Options{Out: out, Log: io.Discard}, false)
	if !got.Failed || !strings.Contains(got.FailReason, "result.json") {
		t.Fatalf("finish = %+v, want the case failed with the reason naming result.json", got)
	}
}

// The note about a plan that could not be kept has to be in the file: one appended after the result was
// persisted is a note nobody reads.
func TestTheNoteAboutAnUnkeptPlanReachesTheResultFile(t *testing.T) {
	out := t.TempDir()
	ws := filepath.Join(out, "case-a", "1", "ws")
	if err := os.MkdirAll(filepath.Join(ws, filepath.Dir(PlanPath)), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(ws, PlanPath), []byte("## Findings\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	// A directory where the kept plan has to go: the copy fails and the case keeps working.
	dir := filepath.Join(out, "case-a", "1")
	if err := os.MkdirAll(filepath.Join(dir, "test-plan.md"), 0o755); err != nil {
		t.Fatal(err)
	}
	got := finish(Result{Case: "case-a", Run: 1, Total: 2, Workspace: ws}, Options{Out: out, Log: io.Discard}, false)
	if len(got.Notes) == 0 {
		t.Fatal("finish dropped the note about the plan it could not keep")
	}
	raw, err := os.ReadFile(filepath.Join(dir, "result.json"))
	if err != nil {
		t.Fatal(err)
	}
	var saved Result
	if err := json.Unmarshal(raw, &saved); err != nil {
		t.Fatal(err)
	}
	if len(saved.Notes) == 0 {
		t.Fatalf("result.json carries no note about the plan it could not keep:\n%s", raw)
	}
}

func TestSummaryRendersCleanControlCells(t *testing.T) {
	got := Summary(Aggregate{Cases: []Result{{Case: "clean", Control: true}}})
	if !strings.Contains(got, "| clean | clean | clean | clean |") || !strings.Contains(got, "| - |") {
		t.Fatalf("summary = %s, want clean reported, pinned, caught cells and no precision", got)
	}
	if strings.Contains(got, "| clean | 0/0") {
		t.Fatalf("summary renders a clean control as a fraction: %s", got)
	}
}

func TestSummaryNamesDefectRunAndUniqueUnitsAndPending(t *testing.T) {
	agg := Aggregate{Cases: []Result{
		{Case: "case-a", Run: 1, Total: 1, Found: 1, Defects: []DefectResult{{ID: "D1", Found: true}}, FindingRows: 1, PendingAdjudication: 1},
		{Case: "case-a", Run: 2, Total: 1, Found: 1, Defects: []DefectResult{{ID: "D1", Found: true}}, FindingRows: 1, PendingAdjudication: 1},
	}}
	finalizeAggregate(&agg)
	got := Summary(agg)
	for _, want := range []string{
		"reported (defect-runs): 2/2",
		"reported (unique defects): 1/1",
		"precision: none (0 adjudicated, 2 pending)",
		"inconclusive: 2",
		"controls: 0",
	} {
		if !strings.Contains(got, want) {
			t.Fatalf("summary missing %q:\n%s", want, got)
		}
	}
	if strings.Contains(got, "precision: 0.00") {
		t.Fatalf("pending precision must be none, not 0.00:\n%s", got)
	}
}

// A legacy (non-manifest) run keeps HEAD's per-attempt window: an attempt that spends its whole
// timeout is an infrastructure failure like any other, and the retry it earns opens a fresh
// window instead of inheriting the expired one. Only a manifest-bound run shares one deadline
// across attempts — that is TestManifestPerCaseRuntimeIsOneDeadlineAcrossScaffoldAndAgent.
func TestLegacyRetryAfterATimedOutAttemptGetsAFreshWindow(t *testing.T) {
	requireNodeAndGit(t)
	caseDir := fakeCaseNamed(t, t.TempDir(), "case-a")
	const attemptWindow = 400 * time.Millisecond
	type observation struct {
		hasDeadline bool
		remaining   time.Duration
	}
	var seen []observation
	agent := func(ctx context.Context, _ string, _ Key, _ Options) (AgentResult, error) {
		d, ok := ctx.Deadline()
		obs := observation{hasDeadline: ok}
		if ok {
			obs.remaining = time.Until(d)
		}
		seen = append(seen, obs)
		if len(seen) == 1 {
			<-ctx.Done() // attempt 1 spends its whole window, like a stalled runner
			return AgentResult{TimedOut: true}, nil
		}
		return AgentResult{Result: "retried under a fresh window"}, nil
	}
	agg, _ := Run(Options{
		CasesGlob: caseDir, Runs: 1, Retries: 1, RetryDelay: 0,
		Timeout: attemptWindow, SuiteTimeout: time.Minute,
		Out: t.TempDir(), BenchDir: t.TempDir(), Agent: agent,
	})
	if len(seen) != 2 {
		t.Fatalf("agent called %d times: a legacy attempt that spent its timeout must still earn its retry within --retries", len(seen))
	}
	if !seen[1].hasDeadline {
		t.Fatal("attempt 2 has no deadline, want the fresh per-attempt window")
	}
	if seen[1].remaining < attemptWindow/2 {
		t.Fatalf("attempt 2 sees %s left of a %s window: it must run under a fresh window, not the expired first one", seen[1].remaining, attemptWindow)
	}
	if len(agg.Cases) != 1 || agg.Cases[0].Failed {
		t.Fatalf("case = %+v, want the successful retry recorded as a completed case", agg.Cases)
	}
}

// A cancellation observed during scaffold stops the case before it copies a byte or spawns the
// fixture suite or the agent, and the manifest run records that stop as budget_exhausted — a
// deadline the harness honored cooperatively, never an invalid fixture or a failure.
func TestStrictScaffoldCancellationStopsBeforeCopyAndIsBudgetExhausted(t *testing.T) {
	requireNodeAndGit(t)
	caseDir := fakeCase(t)
	key, err := LoadKey(caseDir)
	if err != nil {
		t.Fatal(err)
	}
	runCtx, cancel := context.WithCancel(context.Background())
	cancel() // the parent budget is already spent when this case would run
	out := t.TempDir()
	calls := 0
	agent := func(context.Context, string, Key, Options) (AgentResult, error) {
		calls++
		return AgentResult{Result: "must not run"}, nil
	}
	opts := Options{
		StrictManifest: true, Timeout: time.Minute, SuiteTimeout: time.Minute,
		Out: out, Agent: agent, runContext: runCtx,
	}
	res := runOnce(caseDir, key, 1, opts)
	ws := filepath.Join(out, filepath.Base(caseDir), "1", "ws")
	if _, err := os.Stat(ws); !os.IsNotExist(err) {
		t.Fatalf("workspace exists (stat err = %v): a canceled scaffold must stop before it copies the fixture or runs any subprocess", err)
	}
	if calls != 0 {
		t.Fatalf("agent called %d times: a canceled scaffold must stop the case before the agent runs", calls)
	}
	if !res.BudgetExhausted || res.Outcome != "budget_exhausted" {
		t.Fatalf("case = %+v, want budget_exhausted for a cancellation during scaffold", res)
	}
	if res.Invalid || res.Failed {
		t.Fatalf("a cancellation during scaffold is spent budget, not invalid or failed: %+v", res)
	}
}

// strictCaseBench lays out a one-case benchmark a manifest-bound run can select.
func strictCaseBench(t *testing.T) string {
	t.Helper()
	requireNodeAndGit(t)
	benchDir := t.TempDir()
	fakeCaseNamed(t, filepath.Join(benchDir, "cases"), "case-a")
	return benchDir
}

func strictOptions(benchDir, out string, agent Agent) Options {
	return Options{
		StrictManifest: true, ManifestSHA256: "sha256:m", ManifestVersion: "1.0", ManifestSuite: "TEST",
		CaseIDs: []string{"case-a"}, BudgetIdentity: "sha256:identity", Runs: 1,
		Timeout: time.Minute, SuiteTimeout: time.Minute,
		Out: out, BenchDir: benchDir, Agent: agent,
	}
}

// setCaseSuite points a case's fixture suite at a command the test controls, the way a corpus
// author writes it into the sealed key.
func setCaseSuite(t *testing.T, caseDir, suite string) {
	t.Helper()
	path := filepath.Join(caseDir, KeyFile)
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	patched := strings.Replace(string(raw), `"suite":"node --test"`, `"suite":"`+suite+`"`, 1)
	if patched == string(raw) {
		t.Fatal("the suite command was never rewritten")
	}
	if err := os.WriteFile(path, []byte(patched), 0o644); err != nil {
		t.Fatal(err)
	}
}

// The manifest's per-case runtime is one deadline for the whole case run: it opens when the
// scheduled case starts, before the fixture scaffold, and the agent inherits what the scaffold
// left of it. If scaffold and agent each got their own window, a slow fixture plus a full agent
// timeout would spend twice the manifest's budget on one case.
func TestManifestPerCaseRuntimeIsOneDeadlineAcrossScaffoldAndAgent(t *testing.T) {
	requireNodeAndGit(t)
	benchDir := strictCaseBench(t)
	// The fixture suite deliberately eats a visible slice of the case budget before the agent runs.
	setCaseSuite(t, filepath.Join(benchDir, "cases", "case-a"), "sleep 0.4")
	const caseRuntime = time.Second
	calls := 0
	var agentDeadline time.Time
	var remainingAtAgent time.Duration
	deadlineSeen := false
	agent := func(ctx context.Context, _ string, _ Key, _ Options) (AgentResult, error) {
		calls++
		agentDeadline, deadlineSeen = ctx.Deadline()
		remainingAtAgent = time.Until(agentDeadline)
		<-ctx.Done() // the agent runs until its deadline cancels it, like a live runner would
		return AgentResult{TimedOut: true}, nil
	}
	opts := strictOptions(benchDir, t.TempDir(), agent)
	opts.Timeout = caseRuntime
	opts.SuiteTimeout = time.Minute // a looser fixture-suite cap must not become a second case window
	opts.WallTimeout = time.Minute  // the outer wall clock is not the binding deadline here
	opts.Retries = 2
	start := time.Now()
	agg, code := Run(opts)
	elapsed := time.Since(start)
	if !deadlineSeen {
		t.Fatal("agent never observed a deadline")
	}
	// The scaffold's suite ran for 400ms before the agent was called: the agent's remaining budget
	// must already be short by that much. A fresh window after the scaffold would still show (nearly)
	// the full case runtime left.
	if remainingAtAgent > caseRuntime-300*time.Millisecond {
		t.Fatalf("agent sees %s left of a %s case budget after a 400ms fixture scaffold: scaffold and agent must spend one shared deadline, not two windows", remainingAtAgent, caseRuntime)
	}
	// No second window anywhere in the run: scaffold time plus a full case timeout would push the
	// total past the case budget by (at least) the scaffold's slice.
	if elapsed > caseRuntime+350*time.Millisecond {
		t.Fatalf("run took %s for a %s per-case budget: the scaffold and the agent spent separate timeout windows", elapsed, caseRuntime)
	}
	if calls != 1 {
		t.Fatalf("agent called %d times: deadline exhaustion must not earn an infrastructure retry", calls)
	}
	if len(agg.Cases) != 1 {
		t.Fatalf("cases = %d, want the budget-exhausted case recorded", len(agg.Cases))
	}
	res := agg.Cases[0]
	if !res.BudgetExhausted || res.Outcome != "budget_exhausted" {
		t.Fatalf("case outcome = %+v, want budget_exhausted", res)
	}
	if res.Invalid || res.Failed {
		t.Fatalf("a case that spent its runtime budget must be scored, not invalid or failed: %+v", res)
	}
	if !agg.BudgetExhausted {
		t.Fatal("aggregate must carry the budget exhaustion")
	}
	if code != 0 {
		t.Fatalf("exit code = %d, want 0: the case ran and was scored; only the case budget, not the suite wall clock, was reached", code)
	}
}

// The suite wall clock stays an outer budget of its own: spent before a scheduled case can start,
// the case never runs and the run records an incomplete, budget-exhausted stop. It composes as the
// parent of each case's deadline, never as a replacement for it.
func TestManifestSuiteWallClockStopsCasesThatHaveNotStarted(t *testing.T) {
	requireNodeAndGit(t)
	benchDir := strictCaseBench(t)
	calls := 0
	agent := func(context.Context, string, Key, Options) (AgentResult, error) {
		calls++
		return AgentResult{Result: "must not run"}, nil
	}
	opts := strictOptions(benchDir, t.TempDir(), agent)
	opts.WallTimeout = time.Nanosecond // spent before the scheduler can launch the unit
	agg, code := Run(opts)
	if calls != 0 {
		t.Fatalf("agent called %d times: a spent suite wall clock must stop cases before they start", calls)
	}
	if !agg.BudgetExhausted {
		t.Fatal("aggregate must record the suite-level budget exhaustion")
	}
	if len(agg.Cases) != 0 {
		t.Fatalf("cases = %d, want the never-started unit skipped, not recorded", len(agg.Cases))
	}
	if code == 0 {
		t.Fatal("exit code must be non-zero: the suite stopped before every unit ran")
	}
}

// A case whose own cost budget is spent stops right there, even when the attempt also failed:
// the budget is the harness's own outcome (spec 5.3), not an infrastructure failure to retry,
// and the run it ended stays a complete, scored reading.
func TestManifestCostBudgetExhaustionBeatsAnInfraRetry(t *testing.T) {
	benchDir := strictCaseBench(t)
	calls := 0
	agent := func(context.Context, string, Key, Options) (AgentResult, error) {
		calls++
		return AgentResult{CostUSD: 2, CostKnown: true}, fmt.Errorf("connection reset by peer")
	}
	opts := strictOptions(benchDir, t.TempDir(), agent)
	opts.Retries, opts.MaxAttemptsPerCase, opts.MaxCostPerCaseRunUSD = 1, 2, 1
	agg, code := Run(opts)
	if calls != 1 {
		t.Fatalf("agent called %d times: a spent budget must end the case before an infra retry", calls)
	}
	if len(agg.Cases) != 1 {
		t.Fatalf("cases = %d, want the budget-exhausted case recorded", len(agg.Cases))
	}
	res := agg.Cases[0]
	if !res.BudgetExhausted || res.Outcome != "budget_exhausted" {
		t.Fatalf("case outcome = %+v, want budget_exhausted", res)
	}
	if res.Failed || res.Invalid {
		t.Fatalf("a budget-exhausted case must be scored, not failed or invalid: %+v", res)
	}
	if !agg.BudgetExhausted || agg.Failed != 0 {
		t.Fatalf("aggregate budget_exhausted=%t failed=%d, want the exhaustion visible and no failure", agg.BudgetExhausted, agg.Failed)
	}
	if code != 0 {
		t.Fatalf("exit code = %d, want 0: every unit ran and was scored", code)
	}
}

// Infrastructure failures are retried within the retry budget and, once it is spent, are excluded
// from recall as failed — the opposite of a budget exhaustion, which is scored.
func TestInfraFailuresAreRetriedThenExcludedNotBudgetExhausted(t *testing.T) {
	benchDir := strictCaseBench(t)
	calls := 0
	agent := func(context.Context, string, Key, Options) (AgentResult, error) {
		calls++
		return AgentResult{}, fmt.Errorf("HTTP 429 rate limited")
	}
	opts := strictOptions(benchDir, t.TempDir(), agent)
	opts.Retries, opts.MaxAttemptsPerCase, opts.RetryDelay = 1, 2, 0
	agg, code := Run(opts)
	if calls != 2 {
		t.Fatalf("agent called %d times, want the first attempt retried once", calls)
	}
	res := agg.Cases[0]
	if !res.Failed {
		t.Fatalf("case = %+v, want failed after the retry budget", res)
	}
	if res.BudgetExhausted || agg.BudgetExhausted {
		t.Fatalf("an infrastructure failure must never be recorded as budget_exhausted: %+v", res)
	}
	if agg.Failed != 1 || agg.Defects != 0 {
		t.Fatalf("failed=%d defects=%d, want the failure excluded from recall", agg.Failed, agg.Defects)
	}
	if code != ExitPartial {
		t.Fatalf("exit code = %d, want ExitPartial for an incomplete corpus", code)
	}
}

// Retries spend from the same case budget as the first attempt: the manifest's token and cost
// ceilings bound one case run, not one attempt.
func TestRetryAttemptsShareTheCaseTokenAndCostBudget(t *testing.T) {
	benchDir := strictCaseBench(t)
	calls := 0
	agent := func(context.Context, string, Key, Options) (AgentResult, error) {
		calls++
		return AgentResult{CostUSD: 0.6, CostKnown: true, Tokens: 100, TokensKnown: true}, fmt.Errorf("transient upstream error")
	}
	opts := strictOptions(benchDir, t.TempDir(), agent)
	opts.Retries, opts.MaxAttemptsPerCase, opts.RetryDelay = 1, 2, 0
	opts.MaxTokensPerCaseRun, opts.MaxCostPerCaseRunUSD = 150, 1
	agg, _ := Run(opts)
	if calls != 2 {
		t.Fatalf("agent called %d times: the second attempt's spend must cross the shared budget", calls)
	}
	res := agg.Cases[0]
	if !res.BudgetExhausted || res.Failed {
		t.Fatalf("case = %+v, want budget_exhausted from the shared ceilings", res)
	}
	if res.Tokens == nil || *res.Tokens != 200 {
		t.Fatalf("tokens = %v, want both attempts' 200 counted against one case budget", res.Tokens)
	}
	if res.CostUSD < 1.2-1e-9 {
		t.Fatalf("cost = %f, want both attempts' 0.6+0.6 counted against one case budget", res.CostUSD)
	}
}

// The suite ceiling is shared too: the attempt that pushes the suite over the line ends the case
// and leaves the run incomplete, which is what makes the reading non-comparable afterwards.
func TestSuiteCostCeilingCrossedByARetryEndsTheRunIncomplete(t *testing.T) {
	benchDir := strictCaseBench(t)
	calls := 0
	agent := func(context.Context, string, Key, Options) (AgentResult, error) {
		calls++
		return AgentResult{CostUSD: 0.6, CostKnown: true, Tokens: 10, TokensKnown: true}, fmt.Errorf("transient upstream error")
	}
	opts := strictOptions(benchDir, t.TempDir(), agent)
	opts.Retries, opts.MaxAttemptsPerCase, opts.RetryDelay = 1, 2, 0
	opts.MaxCostUSD = 1
	agg, code := Run(opts)
	if calls != 2 {
		t.Fatalf("agent called %d times: the retry's spend must cross the shared suite ceiling", calls)
	}
	if !agg.BudgetExhausted {
		t.Fatal("suite ceiling crossing must be visible as budget_exhausted in the aggregate")
	}
	if code != ExitCostCeiling {
		t.Fatalf("exit code = %d, want ExitCostCeiling: the suite stopped early on its ceiling", code)
	}
	if agg.Provenance.ExecutionComplete == nil || *agg.Provenance.ExecutionComplete {
		t.Fatal("provenance must record the truncated execution as incomplete")
	}
}

// A backend that reports no usage leaves the manifest's budgets unverifiable after the call:
// the case is invalid with the unsupported reason, the provenance says so, and the exit code is
// partial — the run fails closed instead of passing an unknown spend off as within budget.
func TestManifestMissingUsageIsInvalidAndUnsupported(t *testing.T) {
	benchDir := strictCaseBench(t)
	calls := 0
	agent := func(context.Context, string, Key, Options) (AgentResult, error) {
		calls++
		return AgentResult{Result: "answer without usage"}, nil
	}
	opts := strictOptions(benchDir, t.TempDir(), agent)
	opts.MaxCostPerCaseRunUSD, opts.MaxTokensPerCaseRun = 1, 100
	agg, code := Run(opts)
	if calls != 1 {
		t.Fatalf("agent called %d times: unknown usage must stop the case, not retry it", calls)
	}
	res := agg.Cases[0]
	if !res.Invalid || !strings.Contains(res.InvalidReason, "usage") {
		t.Fatalf("case = %+v, want invalid with the missing-usage reason", res)
	}
	if agg.Invalid != 1 {
		t.Fatalf("invalid = %d, want the unsupported case counted invalid", agg.Invalid)
	}
	p := agg.Provenance
	if p.BudgetStatus != "unsupported" || p.BudgetUnsupportedReason == "" {
		t.Fatalf("budget status = %q (%q), want unsupported with a reason", p.BudgetStatus, p.BudgetUnsupportedReason)
	}
	if p.InstrumentValid == nil || *p.InstrumentValid {
		t.Fatal("an unsupported reading must not claim a valid instrument")
	}
	if code != ExitPartial {
		t.Fatalf("exit code = %d, want ExitPartial", code)
	}
}

// An infrastructure failure whose response omitted usage is not a terminal unknown: it still takes
// its declared retries, and the case fails closed afterwards — the later attempt's known usage can
// neither complete the reading nor present the aggregate spend as known.
func TestInfraFailureWithUnknownUsageIsRetriedAndStillFailsClosed(t *testing.T) {
	benchDir := strictCaseBench(t)
	calls := 0
	agent := func(context.Context, string, Key, Options) (AgentResult, error) {
		calls++
		if calls == 1 {
			return AgentResult{}, fmt.Errorf("connection reset by peer") // usage unknown
		}
		return AgentResult{Result: "answer with usage", CostUSD: 0.02, CostKnown: true, Tokens: 10, TokensKnown: true}, nil
	}
	opts := strictOptions(benchDir, t.TempDir(), agent)
	opts.Retries, opts.MaxAttemptsPerCase, opts.RetryDelay = 1, 2, 0
	opts.MaxCostPerCaseRunUSD, opts.MaxTokensPerCaseRun = 1, 100
	agg, code := Run(opts)
	if calls != 2 {
		t.Fatalf("agent called %d times, want the unknown-usage infra failure retried once", calls)
	}
	res := agg.Cases[0]
	if !res.Invalid || !strings.Contains(res.InvalidReason, "usage") {
		t.Fatalf("case = %+v, want invalid with the missing-usage reason despite the later known usage", res)
	}
	if res.Failed {
		t.Fatalf("case = %+v, want invalid/unsupported, not an infrastructure failure", res)
	}
	if res.CostKnown {
		t.Fatal("cost_known = true: an unknown-usage attempt must leave the aggregate spend unknown")
	}
	if res.Tokens != nil {
		t.Fatalf("tokens = %d, want nil: known attempts must not be summed into a complete total", *res.Tokens)
	}
	p := agg.Provenance
	if p.BudgetStatus != "unsupported" || p.BudgetUnsupportedReason == "" {
		t.Fatalf("budget status = %q (%q), want unsupported with a reason", p.BudgetStatus, p.BudgetUnsupportedReason)
	}
	if code != ExitPartial {
		t.Fatalf("exit code = %d, want ExitPartial", code)
	}
}

// Infrastructure failures with unknown usage that spend the whole retry allowance settle as
// invalid/unsupported, not as the ordinary failed-after-retries reading: the spend stayed unknown,
// so the run must fail closed either way it ends.
func TestUnknownUsageExhaustingRetriesIsUnsupportedNotInfraFailure(t *testing.T) {
	benchDir := strictCaseBench(t)
	calls := 0
	agent := func(context.Context, string, Key, Options) (AgentResult, error) {
		calls++
		return AgentResult{}, fmt.Errorf("HTTP 429 rate limited") // usage unknown every attempt
	}
	opts := strictOptions(benchDir, t.TempDir(), agent)
	opts.Retries, opts.MaxAttemptsPerCase, opts.RetryDelay = 1, 2, 0
	opts.MaxCostPerCaseRunUSD, opts.MaxTokensPerCaseRun = 1, 100
	agg, code := Run(opts)
	if calls != 2 {
		t.Fatalf("agent called %d times, want the declared retry count honored before settling", calls)
	}
	res := agg.Cases[0]
	if !res.Invalid || !strings.Contains(res.InvalidReason, "usage") {
		t.Fatalf("case = %+v, want invalid with the missing-usage reason after the retries settled", res)
	}
	if res.Failed {
		t.Fatalf("case = %+v, want invalid/unsupported instead of retry exhaustion reported as failed", res)
	}
	p := agg.Provenance
	if p.BudgetStatus != "unsupported" || p.BudgetUnsupportedReason == "" {
		t.Fatalf("budget status = %q (%q), want unsupported with a reason", p.BudgetStatus, p.BudgetUnsupportedReason)
	}
	if code != ExitPartial {
		t.Fatalf("exit code = %d, want ExitPartial", code)
	}
}

// A manifest case changed after the run (the I6 post-run check) invalidates the instrument: the
// provenance records it, the exit code is partial, and the comparator refuses the reading.
func TestPostRunCheckFailureInvalidatesProvenanceAndIsRefusedByCompare(t *testing.T) {
	benchDir := strictCaseBench(t)
	agent := func(context.Context, string, Key, Options) (AgentResult, error) {
		return AgentResult{Result: "no plan in this answer", CostUSD: 0.01, CostKnown: true, Tokens: 10, TokensKnown: true}, nil
	}
	before := t.TempDir()
	badOpts := strictOptions(benchDir, before, agent)
	badOpts.PostRunCheck = func() error { return fmt.Errorf("manifest cases changed during execution: case-a") }
	bad, badCode := Run(badOpts)
	if badCode != ExitPartial {
		t.Fatalf("exit code = %d, want ExitPartial after a failed post-run check", badCode)
	}
	if bad.Provenance.InstrumentValid == nil || *bad.Provenance.InstrumentValid {
		t.Fatal("a failed post-run check must be recorded as an invalid instrument")
	}
	after := t.TempDir()
	goodOpts := strictOptions(benchDir, after, agent)
	goodOpts.PostRunCheck = func() error { return nil }
	good, goodCode := Run(goodOpts)
	if goodCode != 0 {
		t.Fatalf("clean run exit code = %d, want 0", goodCode)
	}
	if good.Provenance.InstrumentValid == nil || !*good.Provenance.InstrumentValid ||
		good.Provenance.ExecutionComplete == nil || !*good.Provenance.ExecutionComplete {
		t.Fatalf("clean run provenance = %+v, want valid and complete", good.Provenance)
	}
	if _, err := Compare(before, after); err == nil || !strings.Contains(err.Error(), "instrument") {
		t.Fatalf("a reading invalidated by the post-run check must be refused by compare, got %v", err)
	}
}

// The budget identity no longer claims a turn cap, and the provenance says plainly which runner
// can enforce one: pi cannot, so a manifest-bound pi reading carries the limitation.
func TestTurnCapLimitationIsRecordedForPiOnly(t *testing.T) {
	benchDir := strictCaseBench(t)
	agent := func(context.Context, string, Key, Options) (AgentResult, error) {
		return AgentResult{Result: "no plan", CostKnown: true, TokensKnown: true}, nil
	}
	provenanceFor := func(runner string) Provenance {
		opts := strictOptions(benchDir, t.TempDir(), agent)
		opts.Runner = runner
		agg, _ := Run(opts)
		return agg.Provenance
	}
	hasTurnCapNote := func(p Provenance) bool {
		for _, limitation := range p.BudgetLimitations {
			if strings.Contains(limitation, "turn cap") {
				return true
			}
		}
		return false
	}
	pi := provenanceFor(RunnerPi)
	if !hasTurnCapNote(pi) {
		t.Fatalf("pi limitations = %v, want the unenforced turn cap named", pi.BudgetLimitations)
	}
	claude := provenanceFor(RunnerClaude)
	if hasTurnCapNote(claude) {
		t.Fatalf("claude limitations = %v, the claude runner enforces the turn cap", claude.BudgetLimitations)
	}
}

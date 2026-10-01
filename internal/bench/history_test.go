package bench

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestAppendHistoryIsAdditive(t *testing.T) {
	dir := t.TempDir()
	for i, r := range []float64{0.5, 0.8} {
		if err := AppendHistory(dir, HistoryEntry{TS: "t" + string(rune('0'+i)), Out: "o", Model: "m", Cases: 1, Defects: 4, Found: 2, Recall: r, SkillVersion: "0.3.0"}); err != nil {
			t.Fatal(err)
		}
	}
	jsonl, _ := os.ReadFile(filepath.Join(dir, "history.jsonl"))
	if n := strings.Count(strings.TrimSpace(string(jsonl)), "\n") + 1; n != 2 {
		t.Fatalf("jsonl lines = %d, want 2: %s", n, jsonl)
	}
	md, _ := os.ReadFile(filepath.Join(dir, "history.md"))
	if strings.Count(string(md), "# Benchmark history") != 1 {
		t.Fatalf("header must be written once: %s", md)
	}
	if !strings.Contains(string(md), "cannot be compared with one that does") {
		t.Fatalf("history intro must explain missing agent-config provenance: %s", md)
	}
	if strings.Count(string(md), "| t0 |") != 1 || strings.Count(string(md), "| t1 |") != 1 {
		t.Fatalf("both rows expected once: %s", md)
	}
	if !strings.Contains(string(md), "| 0.80 |") {
		t.Fatalf("recall formatting: %s", md)
	}
}

// The corpus is the new last column. The history stays append-only: a file written under the
// previous column set keeps its rows and gains a fresh header above the new one.
func TestHistoryAddsTheCorpusColumn(t *testing.T) {
	dir := t.TempDir()
	old := "| ts | kind | out | model | cases | defects | reported | recall | caught | recall caught | false positives | failed | invalid | no plan | cost USD | skill version | scorer |\n|---|---|---|---|---|---|---|---|---|---|---|---|---|---|---|---|---|\n| t0 | run | o | m | 1 | 2 | 1 | 0.50 | 0 | 0.00 | 0 | 0 | 0 | 0 | 1.000 | 0.3.0 | abc |\n"
	if err := os.WriteFile(filepath.Join(dir, "history.md"), []byte(old), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := AppendHistory(dir, HistoryEntry{TS: "t1", Out: "o", Model: "m", Cases: 1, Defects: 2, Corpus: "sha256:0123456789abcdef"}); err != nil {
		t.Fatal(err)
	}
	md, _ := os.ReadFile(filepath.Join(dir, "history.md"))
	if !strings.Contains(string(md), "| t0 | run | o | m | 1 | 2 | 1 | 0.50 | 0 | 0.00 | 0 | 0 | 0 | 0 | 1.000 | 0.3.0 | abc |") {
		t.Fatalf("an earlier row must stay as it was: %s", md)
	}
	if !strings.Contains(string(md), "| scorer | corpus |") {
		t.Fatalf("a fresh header with the corpus column must be appended: %s", md)
	}
	if !strings.Contains(string(md), "| sha256:0123456789abcdef |") {
		t.Fatalf("the new row must carry the corpus: %s", md)
	}
	data, _ := os.ReadFile(filepath.Join(dir, "history.jsonl"))
	if !strings.Contains(string(data), `"corpus":"sha256:0123456789abcdef"`) {
		t.Fatalf("the jsonl row must carry the corpus: %s", data)
	}
}

// A row written before the activation column says nothing about whether the scoped mode ran, and a row
// written now says it as a number, including zero. The history stays append-only: earlier rows keep
// their bytes, and the fresh header carries the line that says what the rows above it are.
func TestHistoryAddsTheActivationColumn(t *testing.T) {
	dir := t.TempDir()
	old := "| ts | kind | out | model | cases | defects | reported | recall | caught | recall caught | false positives | failed | invalid | no plan | cost USD | skill version | scorer | corpus |\n|---|---|---|---|---|---|---|---|---|---|---|---|---|---|---|---|---|---|\n| t0 | run | o | m | 1 | 2 | 1 | 0.50 | 0 | 0.00 | 0 | 0 | 0 | 0 | 1.000 | 0.3.7 | abc | sha256:c |\n"
	if err := os.WriteFile(filepath.Join(dir, "history.md"), []byte(old), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := AppendHistory(dir, HistoryEntry{TS: "t1", Out: "o", Model: "m", Cases: 1, Defects: 2, Corpus: "sha256:c", LightActivated: 1, Runs: 3}); err != nil {
		t.Fatal(err)
	}
	if err := AppendHistory(dir, HistoryEntry{TS: "t2", Out: "o", Model: "m", Cases: 1, Defects: 2, Corpus: "sha256:c", Runs: 1}); err != nil {
		t.Fatal(err)
	}
	md, _ := os.ReadFile(filepath.Join(dir, "history.md"))
	if !strings.HasPrefix(string(md), old) {
		t.Fatalf("an earlier row must stay byte for byte: %s", md)
	}
	appended := string(md)[len(old):]
	if !strings.Contains(appended, "non-activation") {
		t.Fatalf("the fresh header must say what the rows above it are: %s", appended)
	}
	if !strings.Contains(appended, "| light |") {
		t.Fatalf("the fresh header must carry the activation column: %s", appended)
	}
	for _, want := range []string{"| sha256:c | 1 | 3 |", "| sha256:c | 0 | 1 |"} {
		if !strings.Contains(appended, want) {
			t.Fatalf("the rows must carry the count and the run multiplicity, including zero: %q missing from %s", want, appended)
		}
	}
	raw, _ := os.ReadFile(filepath.Join(dir, "history.jsonl"))
	lines := strings.Split(strings.TrimSpace(string(raw)), "\n")
	if len(lines) != 2 {
		t.Fatalf("jsonl rows = %d, want 2", len(lines))
	}
	for i, want := range []string{`"light_activated":1`, `"light_activated":0`} {
		if !strings.Contains(lines[i], want) {
			t.Fatalf("row %d = %s, want %s", i, lines[i], want)
		}
	}
}

func TestHistoryAppendsAdjudicatedMetricsInDeclaredOrder(t *testing.T) {
	dir := t.TempDir()
	entry := HistoryEntry{
		TS: "t", Kind: KindRun, Out: "o", Model: "m", Cases: 1, Defects: 2, Found: 1, Recall: 0.5,
		Caught: 1, RecallCaught: 0.5, SkillVersion: "s", Scorer: "q", Runs: 2, MetricsVersion: MetricsVersion,
		AgentConfig: ConfigBench, Environment: "linux/amd64",
		UniqueDefects: 1, UniqueFound: 1, UniqueConfirmed: 0, UniqueCaught: 1, DefectRuns: 2, Controls: 1,
		PendingAdjudication: 3, OutOfScope: 4, Inconclusive: 5, MicroActivated: 6,
	}
	if err := AppendHistory(dir, entry); err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(filepath.Join(dir, "history.md"))
	if err != nil {
		t.Fatal(err)
	}
	fields := func(line string) []string {
		parts := strings.Split(strings.Trim(line, "|"), "|")
		for i := range parts {
			parts[i] = strings.TrimSpace(parts[i])
		}
		return parts
	}
	header := strings.Split(strings.TrimSpace(historyHeader), "\n")[0]
	var row string
	for _, line := range strings.Split(string(data), "\n") {
		if strings.HasPrefix(line, "| t |") {
			row = line
			break
		}
	}
	if row == "" {
		t.Fatalf("history row missing:\n%s", data)
	}
	h := fields(header)
	r := fields(row)
	if len(h) != len(r) {
		t.Fatalf("header columns = %d, row columns = %d:\nheader %s\nrow %s", len(h), len(r), header, row)
	}
	wantHeaders := []string{"metrics version", "unique defects", "unique found", "unique confirmed", "unique caught", "defect runs", "controls", "precision", "pending", "out of scope", "inconclusive", "unstable", "agent config", "environment", "micro"}
	wantValues := []string{"2", "1", "1", "0", "1", "2", "1", "-", "3", "4", "5", "-", "bench", "linux/amd64", "6"}
	start := len(h) - len(wantHeaders)
	for i := range wantHeaders {
		if h[start+i] != wantHeaders[i] || r[start+i] != wantValues[i] {
			t.Fatalf("column %d = header %q, value %q; want header %q, value %q", start+i, h[start+i], r[start+i], wantHeaders[i], wantValues[i])
		}
	}
}

func TestSkillVersion(t *testing.T) {
	dir := t.TempDir()
	f := filepath.Join(dir, "SKILL.md")
	if err := os.WriteFile(f, []byte("---\nname: tsp\nmetadata:\n  author: x\n  version: \"0.3.0\"\n---\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if got := SkillVersion(f); got != "0.3.0" {
		t.Fatalf("version = %q", got)
	}
	if got := SkillVersion(filepath.Join(dir, "missing.md")); got != "unknown" {
		t.Fatalf("missing file version = %q", got)
	}
	if err := os.WriteFile(f, []byte("---\nname: x\n---\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if got := SkillVersion(f); got != "unknown" {
		t.Fatalf("no version field = %q", got)
	}
}

// A rescore re-reads an earlier run: it spends nothing and belongs to that run's skill version.
// Its row must say so, or a reader summing the cost column counts the same money twice.
func TestRescoreRowIsMarkedAndCostsNothing(t *testing.T) {
	dir := t.TempDir()
	run := HistoryEntry{TS: "2026-01-01T00:00:00Z", Out: "r1", Model: "m", Cases: 2, Defects: 4, Found: 2, Recall: 0.5, Caught: 1, CostUSD: 3.5, SkillVersion: "0.3.0"}
	if err := AppendHistory(dir, run); err != nil {
		t.Fatal(err)
	}
	rescore := HistoryEntry{TS: "2026-01-02T00:00:00Z", Out: "r1/rescored", Model: "m", Cases: 2, Defects: 4, Found: 2, Recall: 0.5, Caught: 3, RecallCaught: 0.75, CostUSD: 3.5, SkillVersion: "0.3.0", Kind: KindRescore, RunTS: run.TS, SourceRun: "r1"}
	if err := AppendHistory(dir, rescore); err != nil {
		t.Fatal(err)
	}
	jsonl, _ := os.ReadFile(filepath.Join(dir, "history.jsonl"))
	var rows []HistoryEntry
	for _, line := range strings.Split(strings.TrimSpace(string(jsonl)), "\n") {
		var e HistoryEntry
		if err := json.Unmarshal([]byte(line), &e); err != nil {
			t.Fatal(err)
		}
		rows = append(rows, e)
	}
	if rows[0].Kind != KindRun {
		t.Fatalf("a run row must say so: %+v", rows[0])
	}
	if rows[1].Kind != KindRescore || rows[1].SourceRun != "r1" || rows[1].RunTS != run.TS {
		t.Fatalf("rescore row: %+v", rows[1])
	}
	if rows[1].CostUSD != 0 {
		t.Fatalf("a rescore spends nothing, got $%.2f", rows[1].CostUSD)
	}
	if rows[1].SkillVersion != "0.3.0" {
		t.Fatalf("a rescore keeps the skill version of the run it re-reads: %q", rows[1].SkillVersion)
	}
	md, _ := os.ReadFile(filepath.Join(dir, "history.md"))
	if !strings.Contains(string(md), "| rescore of r1 |") {
		t.Fatalf("markdown must name the source run: %s", md)
	}
	if !strings.Contains(string(md), "reported and caught are independent") {
		t.Fatalf("the header must say caught can exceed reported: %s", md)
	}
}

// Two rescores of one run can disagree when the scoring rules change between them. A row that
// does not say which build produced it turns that into a contradiction nobody can resolve.
func TestEveryRowRecordsTheScorerThatProducedIt(t *testing.T) {
	dir := t.TempDir()
	run := HistoryEntry{TS: "t0", Out: "r1", Model: "m", Cases: 1, Defects: 4, Found: 2, Caught: 2, SkillVersion: "0.3.1"}
	if err := AppendHistory(dir, run); err != nil {
		t.Fatal(err)
	}
	rescore := HistoryEntry{TS: "t1", Out: "r1/rescored", Model: "m", Cases: 1, Defects: 4, Found: 2, Caught: 3, SkillVersion: "0.3.1", Kind: KindRescore, RunTS: "t0", SourceRun: "r1"}
	if err := AppendHistory(dir, rescore); err != nil {
		t.Fatal(err)
	}
	data, _ := os.ReadFile(filepath.Join(dir, "history.jsonl"))
	for i, line := range strings.Split(strings.TrimSpace(string(data)), "\n") {
		var e HistoryEntry
		if err := json.Unmarshal([]byte(line), &e); err != nil {
			t.Fatal(err)
		}
		if e.Scorer == "" {
			t.Fatalf("row %d records no scorer: %s", i+1, line)
		}
	}
	md, _ := os.ReadFile(filepath.Join(dir, "history.md"))
	if !strings.Contains(string(md), "| scorer |") {
		t.Fatalf("the markdown table must carry the scorer column: %s", md)
	}
	if !strings.Contains(string(md), "supersedes") {
		t.Fatalf("the intro must say a later rescore supersedes an earlier one: %s", md)
	}
}

// An explicit scorer is kept as given, so a row can be attributed to the build it came from.
func TestScorerCanBeStated(t *testing.T) {
	dir := t.TempDir()
	if err := AppendHistory(dir, HistoryEntry{TS: "t", Out: "o", Model: "m", Scorer: "abc1234"}); err != nil {
		t.Fatal(err)
	}
	data, _ := os.ReadFile(filepath.Join(dir, "history.jsonl"))
	if !strings.Contains(string(data), `"scorer":"abc1234"`) {
		t.Fatalf("scorer not kept: %s", data)
	}
}

// A build made from an uncommitted tree cannot be recovered from its commit, so numbers recorded
// under it are not reproducible. The row already says so; the run has to say it out loud, before
// the money is spent rather than after.
func TestDirtyScorerIsAnnounced(t *testing.T) {
	cases := map[string]bool{"abc1234": false, "abc1234+dirty": true, "unknown": true}
	for rev, want := range cases {
		if got := ScorerIsProvisional(rev); got != want {
			t.Errorf("ScorerIsProvisional(%q) = %v, want %v", rev, got, want)
		}
	}
	msg := ProvisionalScorerWarning("abc1234+dirty")
	for _, want := range []string{"abc1234+dirty", "not reproducible", "commit"} {
		if !strings.Contains(msg, want) {
			t.Fatalf("warning missing %q: %s", want, msg)
		}
	}
}

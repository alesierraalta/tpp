package gate

import (
	"bytes"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/alesierraalta/tpp/internal/sanitize"
)

// The operator asked to be asked: the audit reaches the model as context and the user as a line
// in their own terminal, so the offer of feedback does not depend on the model relaying it.
func TestAppendEntrySealsTheIdentityFields(t *testing.T) {
	telemetryDir := t.TempDir()
	key, err := sanitize.LoadKeyIn(telemetryDir)
	if err != nil {
		t.Fatalf("load telemetry key: %v", err)
	}
	entry := &Entry{
		TS:           "2026-09-15T12:00:00Z",
		Session:      "session-literal",
		Repo:         "repository-literal",
		SkillsLoaded: []string{"test-strategy"},
		Audited:      true,
		Fired:        false,
		Skipped:      "skipped-literal",
		OptedOut:     true,
		Plan:         "plan-literal",
	}
	path := filepath.Join(telemetryDir, "testing-gate.jsonl")
	appendEntry(path, entry)

	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read telemetry row: %v", err)
	}
	for _, literal := range []string{entry.Repo, entry.Session, entry.Plan} {
		if strings.Contains(string(raw), literal) {
			t.Fatalf("telemetry row contains raw identity %q: %s", literal, raw)
		}
	}
	var got Entry
	if err := json.Unmarshal(bytes.TrimSpace(raw), &got); err != nil {
		t.Fatalf("decode telemetry row: %v", err)
	}
	if got.Repo != key.ID("repo", entry.Repo) || got.Session != key.ID("sess", entry.Session) || got.Plan != key.ID("plan", entry.Plan) {
		t.Fatalf("sealed identities = repo %q session %q plan %q", got.Repo, got.Session, got.Plan)
	}
	if got.TS != entry.TS || got.Audited != entry.Audited || got.Fired != entry.Fired || got.Skipped != entry.Skipped || got.OptedOut != entry.OptedOut || !reflect.DeepEqual(got.SkillsLoaded, entry.SkillsLoaded) {
		t.Fatalf("decision fields changed: got %#v, want %#v", got, *entry)
	}
}

func TestGateAndFeedbackShareOneResolutionMap(t *testing.T) {
	telemetryDir := t.TempDir()
	key, err := sanitize.LoadKeyIn(telemetryDir)
	if err != nil {
		t.Fatalf("load telemetry key: %v", err)
	}
	repo := "recognisable-repository-literal"
	appendEntry(filepath.Join(telemetryDir, "testing-gate.jsonl"), &Entry{Repo: repo})
	if got, ok := sanitize.Resolve(telemetryDir, key.ID("repo", repo)); !ok || got != repo {
		t.Fatalf("resolve gate pseudonym = %q, %v; want %q, true", got, ok, repo)
	}
	nestedDir := filepath.Join(telemetryDir, filepath.Base(sanitize.TelemetryDir("")))
	if _, err := os.Stat(nestedDir); !os.IsNotExist(err) {
		t.Fatalf("nested telemetry directory = %v, want absent", err)
	}
}

func TestAppendEntryWritesNoRawIdentityWhenTheSaltCannotBeLoaded(t *testing.T) {
	telemetryDir := t.TempDir()
	if err := os.WriteFile(filepath.Join(telemetryDir, ".salt"), []byte("invalid"), 0o600); err != nil {
		t.Fatalf("write invalid salt: %v", err)
	}
	entry := &Entry{
		TS:           "2026-09-15T12:00:00Z",
		Session:      "unloadable-session",
		Repo:         "unloadable-repository",
		SkillsLoaded: []string{},
		Fired:        true,
		Plan:         "unloadable-plan",
	}
	path := filepath.Join(telemetryDir, "testing-gate.jsonl")
	appendEntry(path, entry)

	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read telemetry row after salt failure: %v", err)
	}
	for _, literal := range []string{entry.Repo, entry.Session, entry.Plan} {
		if strings.Contains(string(raw), literal) {
			t.Fatalf("telemetry row contains raw identity %q: %s", literal, raw)
		}
	}
	var got Entry
	if err := json.Unmarshal(bytes.TrimSpace(raw), &got); err != nil {
		t.Fatalf("decode telemetry row after salt failure: %v", err)
	}
	if got.Repo != "" || got.Session != "" || got.Plan != "" {
		t.Fatalf("unloadable salt retained identity: %#v", got)
	}
	if got.TS != entry.TS || got.Fired != entry.Fired || !reflect.DeepEqual(got.SkillsLoaded, entry.SkillsLoaded) {
		t.Fatalf("decision fields changed after salt failure: got %#v, want %#v", got, *entry)
	}
}

func TestAppendEntryDoesNotMutateTheCallersEntry(t *testing.T) {
	entry := &Entry{Session: "raw-session", Repo: "raw-repository", Plan: "raw-plan", SkillsLoaded: []string{"skill"}}
	appendEntry(filepath.Join(t.TempDir(), "testing-gate.jsonl"), entry)
	if entry.Session != "raw-session" || entry.Repo != "raw-repository" || entry.Plan != "raw-plan" || !reflect.DeepEqual(entry.SkillsLoaded, []string{"skill"}) {
		t.Fatalf("appendEntry mutated caller entry: %#v", *entry)
	}
}

func TestSealEntryKeepsNilEntryNil(t *testing.T) {
	if got := sealEntry(t.TempDir(), nil); got != nil {
		t.Fatalf("sealEntry(nil) = %#v, want nil", got)
	}
}

func TestSealEntryLeavesAnAbsentSessionAbsent(t *testing.T) {
	telemetryDir := t.TempDir()
	if _, err := sanitize.LoadKeyIn(telemetryDir); err != nil {
		t.Fatalf("load telemetry key: %v", err)
	}

	empty := sealEntry(telemetryDir, &Entry{})
	if empty.Repo != "" || empty.Session != "" || empty.Plan != "" {
		t.Fatalf("empty identities were sealed: %#v", *empty)
	}

	withSession := sealEntry(telemetryDir, &Entry{Session: "session-literal"})
	if !strings.HasPrefix(withSession.Session, "sess-") {
		t.Fatalf("non-empty session = %q, want sess- pseudonym", withSession.Session)
	}

	mapPath := filepath.Join(telemetryDir, ".pseudonyms.jsonl")
	raw, err := os.ReadFile(mapPath)
	if err != nil {
		t.Fatalf("read local resolution map: %v", err)
	}
	if lines := strings.Split(strings.TrimSpace(string(raw)), "\n"); len(lines) != 1 {
		t.Fatalf("local resolution map has %d lines after empty and non-empty sessions, want 1: %s", len(lines), raw)
	}
}

func TestEmitCarriesAUserFacingLineWhenAsked(t *testing.T) {
	var b bytes.Buffer
	emitWith(&b, "the reason", "tpp: 3 layers assigned and never invoked. Want feedback on this run?")
	var got map[string]any
	if err := json.Unmarshal(b.Bytes(), &got); err != nil {
		t.Fatalf("not JSON: %v %s", err, b.String())
	}
	hs, _ := got["hookSpecificOutput"].(map[string]any)
	if hs["additionalContext"] != "the reason" {
		t.Fatalf("context = %v", hs["additionalContext"])
	}
	if !strings.Contains(got["systemMessage"].(string), "Want feedback") {
		t.Fatalf("systemMessage = %v", got["systemMessage"])
	}
	var plain bytes.Buffer
	emitWith(&plain, "only context", "")
	var got2 map[string]any
	_ = json.Unmarshal(plain.Bytes(), &got2)
	if _, ok := got2["systemMessage"]; ok {
		t.Fatalf("an empty line must not become an empty message: %s", plain.String())
	}
}

// Run must wire the binding directory from the log path it is handed: with the log at
// <base>/telemetry/testing-gate.jsonl and `tpp bind` having stored the file under
// <base>/telemetry/bindings, the hook audits from that file with no environment at all.
func TestRunAuditsFromTheBindingStoredBesideItsLog(t *testing.T) {
	t.Setenv(BindingEnv, "")
	base := t.TempDir()
	repo := filepath.Join(base, "repo")
	if err := os.MkdirAll(repo, 0o755); err != nil {
		t.Fatalf("mkdir repo: %v", err)
	}
	if out, err := exec.Command("git", "init", repo).CombinedOutput(); err != nil {
		t.Fatalf("git init: %v %s", err, out)
	}
	rev := exec.Command("git", "rev-parse", "--show-toplevel")
	rev.Dir = repo
	out, err := rev.Output()
	if err != nil {
		t.Fatalf("rev-parse: %v", err)
	}
	root := strings.TrimSpace(string(out))
	rel := "docs/testing/test-plan.md"
	if err := os.MkdirAll(filepath.Join(repo, filepath.Dir(rel)), 0o755); err != nil {
		t.Fatalf("mkdir plan dir: %v", err)
	}
	if err := os.WriteFile(filepath.Join(repo, rel), []byte(owedRuns), 0o644); err != nil {
		t.Fatalf("write plan: %v", err)
	}
	transcript := filepath.Join(base, "transcript.jsonl")
	if err := os.WriteFile(transcript, []byte(`{"name":"Skill","input":{"skill":"tsp"}}`+"\n"), 0o644); err != nil {
		t.Fatalf("write transcript: %v", err)
	}
	logPath := filepath.Join(base, "telemetry", "testing-gate.jsonl")
	storedBinding(t, BindingsDir(logPath), "sess-run", root, rel, "run-t1")
	payload := `{"session_id":"sess-run","transcript_path":"` + transcript + `","cwd":"` + repo + `"}`
	var stdout bytes.Buffer
	if code := Run(strings.NewReader(payload), &stdout, logPath, auditNow); code != 0 {
		t.Fatalf("Run exit = %d, want 0", code)
	}
	if !strings.Contains(stdout.String(), "layer(s) assigned and never invoked") {
		t.Fatalf("stdout = %q, want the audit line from the stored binding", stdout.String())
	}
	row, err := os.ReadFile(logPath)
	if err != nil {
		t.Fatalf("read log: %v", err)
	}
	if !strings.Contains(string(row), `"audited":true`) {
		t.Fatalf("log row = %s, want the audited entry", row)
	}
}

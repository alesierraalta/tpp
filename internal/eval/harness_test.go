package eval

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestHarnessRecordAppendAndLookup(t *testing.T) {
	components := HarnessComponents{SkillsDigest: "sha256:skills", OrchestratorAssetsDigest: "sha256:assets", Runner: "pi", AgentConfigMode: "bench", DisabledComponents: []string{"z", "a"}, ToolAllowlist: []string{"read", "write"}}
	id := HarnessID(components)
	if !strings.HasPrefix(id, "sha256:") {
		t.Fatalf("HarnessID() = %q", id)
	}
	path := filepath.Join(t.TempDir(), "harnesses.jsonl")
	record := HarnessRecord{Label: "baseline", ID: id, Components: components, Registered: "2026-10-01T00:00:00Z"}
	if err := AppendHarness(path, record); err != nil {
		t.Fatal(err)
	}
	got, found, err := LookupHarness(path, "baseline")
	if err != nil || !found || got.ID != id || got.Label != "baseline" {
		t.Fatalf("LookupHarness() = %+v, %v, %v", got, found, err)
	}
	before, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	conflict := record
	conflict.ID = "sha256:different"
	if err := AppendHarness(path, conflict); err == nil {
		t.Fatal("AppendHarness accepted a label rebound to a different ID")
	}
	after, err := os.ReadFile(path)
	if err != nil || string(after) != string(before) {
		t.Fatalf("conflicting append changed JSONL: %v", err)
	}
}

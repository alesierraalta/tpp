package eval

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func backlogRecord(id string) NovelBacklogRecord {
	return NovelBacklogRecord{
		RunID: "run-1", CaseID: "case-1", FindingID: id, ProofEventDigest: "sha256:" + strings.Repeat("a", 64),
		Finding: json.RawMessage(`{"id":"` + id + `"}`), Evidence: json.RawMessage(`{"digest":"evidence"}`),
		Reproduction: json.RawMessage(`{"applies":true,"outcome":"REPRODUCED"}`), ProposedIssue: json.RawMessage(`{"id":"novel-1","domain":"correctness","severity":"high","issue_type":"logic"}`),
	}
}

func TestAppendNovelBacklogIsCanonicalAppendOnlyAndIdempotent(t *testing.T) {
	root := t.TempDir()
	first := backlogRecord("finding-1")
	if err := AppendNovelBacklog(root, first); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(root, "backlog", "case-1.jsonl")
	prefix, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.HasSuffix(prefix, []byte("\n")) || bytes.Count(prefix, []byte("\n")) != 1 {
		t.Fatalf("first write is not one JSONL line: %q", prefix)
	}
	var decoded NovelBacklogRecord
	if err := json.Unmarshal(bytes.TrimSuffix(prefix, []byte("\n")), &decoded); err != nil || decoded.FindingID != first.FindingID {
		t.Fatalf("decode appended record = %+v, %v", decoded, err)
	}
	if err := AppendNovelBacklog(root, backlogRecord("finding-2")); err != nil {
		t.Fatal(err)
	}
	second, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.HasPrefix(second, prefix) {
		t.Fatal("independent append changed existing bytes")
	}
	if err := AppendNovelBacklog(root, first); err != nil {
		t.Fatalf("identical repeat: %v", err)
	}
	third, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(second, third) {
		t.Fatal("identical repeat changed backlog bytes")
	}
}

func TestAppendNovelBacklogRefusesInvalidInputsWithoutWrites(t *testing.T) {
	for _, tc := range []struct {
		name   string
		record NovelBacklogRecord
	}{
		{"invalid record", NovelBacklogRecord{}},
		{"traversal", func() NovelBacklogRecord { r := backlogRecord("f"); r.CaseID = "../escape"; return r }()},
	} {
		t.Run(tc.name, func(t *testing.T) {
			root := filepath.Join(t.TempDir(), "absent")
			if err := AppendNovelBacklog(root, tc.record); err == nil {
				t.Fatal("expected refusal")
			}
			if _, err := os.Stat(root); !os.IsNotExist(err) {
				t.Fatalf("validation created root: %v", err)
			}
		})
	}
}

func TestAppendNovelBacklogRejectsWrongTypedRequiredFieldsBeforeWrites(t *testing.T) {
	for _, tc := range []struct {
		name, field, value string
	}{
		{"finding id array", "finding", `{"id":[]}`},
		{"proposed id object", "proposed_issue", `{"id":{},"domain":"correctness","severity":"high","issue_type":"logic"}`},
		{"proposed domain number", "proposed_issue", `{"id":"i","domain":1,"severity":"high","issue_type":"logic"}`},
		{"proposed severity null", "proposed_issue", `{"id":"i","domain":"correctness","severity":null,"issue_type":"logic"}`},
		{"proposed issue type whitespace", "proposed_issue", `{"id":"i","domain":"correctness","severity":"high","issue_type":"  "}`},
		{"reproduction applies string", "reproduction", `{"applies":"true"}`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			record := backlogRecord("finding-1")
			switch tc.field {
			case "finding":
				record.Finding = json.RawMessage(tc.value)
			case "proposed_issue":
				record.ProposedIssue = json.RawMessage(tc.value)
			case "reproduction":
				record.Reproduction = json.RawMessage(tc.value)
			}
			root := filepath.Join(t.TempDir(), "absent")
			if err := AppendNovelBacklog(root, record); err == nil {
				t.Fatal("expected invalid field refusal")
			}
			if _, err := os.Stat(root); !os.IsNotExist(err) {
				t.Fatalf("invalid record created root: %v", err)
			}
		})
	}
}

func TestAppendNovelBacklogRefusesConflictsMalformedAndUnsafePaths(t *testing.T) {
	root := t.TempDir()
	first := backlogRecord("finding-1")
	if err := AppendNovelBacklog(root, first); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(root, "backlog", "case-1.jsonl")
	before, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	conflict := first
	conflict.Evidence = json.RawMessage(`{"digest":"different"}`)
	if err := AppendNovelBacklog(root, conflict); err == nil {
		t.Fatal("expected identity conflict refusal")
	}
	if err := os.WriteFile(path, []byte("not json\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	malformed, _ := os.ReadFile(path)
	if err := AppendNovelBacklog(root, first); err == nil {
		t.Fatal("expected malformed backlog refusal")
	}
	after, _ := os.ReadFile(path)
	if !bytes.Equal(malformed, after) {
		t.Fatal("refusal changed malformed backlog")
	}
	_ = before

	crossCase := backlogRecord("finding-2")
	crossCase.CaseID = "case-2"
	foreignLine, err := json.Marshal(crossCase)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, append(foreignLine, '\n'), 0o644); err != nil {
		t.Fatal(err)
	}
	foreignBefore, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if err := AppendNovelBacklog(root, first); err == nil {
		t.Fatal("expected cross-case backlog row refusal")
	}
	foreignAfter, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(foreignBefore, foreignAfter) {
		t.Fatal("cross-case refusal changed backlog")
	}

	outside := t.TempDir()
	if err := os.Symlink(outside, filepath.Join(root, "backlog")); err != nil {
		t.Skipf("symlinks unavailable: %v", err)
	}
	if err := AppendNovelBacklog(root, first); err == nil {
		t.Fatal("expected symlink escape refusal")
	}
}

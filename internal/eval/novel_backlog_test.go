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

// reconcileRecord builds a valid backlog record for a case run built by
// novelFindingsCaseRun (Case "case").
func reconcileRecord(caseID, findingID, runID, digest string) NovelBacklogRecord {
	return NovelBacklogRecord{
		RunID: runID, CaseID: caseID, FindingID: findingID, ProofEventDigest: digest,
		Finding:       json.RawMessage(`{"id":"` + findingID + `"}`),
		Evidence:      json.RawMessage(`{"digest":"evidence"}`),
		Reproduction:  json.RawMessage(`{"applies":true}`),
		ProposedIssue: json.RawMessage(`{"id":"novel-1","domain":"correctness","severity":"high","issue_type":"logic"}`),
	}
}

// confirmedNovelCaseRun drives the controlled flow (proof + confirm) for f1 and returns
// the ledger plus the digest of the proof event its confirmation binds.
func confirmedNovelCaseRun(t *testing.T) (CaseRun, string) {
	t.Helper()
	cr := novelFindingsCaseRun(t, "f1")
	if err := cr.RecordNovelProof(novelProofFor(cr.Findings[0]), "3"); err != nil {
		t.Fatal(err)
	}
	if err := cr.ConfirmNovel("f1", "promoter", "proof verified independently", "4"); err != nil {
		t.Fatal(err)
	}
	for index := len(cr.Log.Events) - 1; index >= 0; index-- {
		if cr.Log.Events[index].Kind == EventNovelProof {
			return *cr, cr.Log.Events[index].Hash
		}
	}
	t.Fatal("no novel proof event recorded")
	return CaseRun{}, ""
}

// The completeness rule: a finding the controlled workflow promoted must have this run's
// backlog record bound to its confirmation's proof digest; the refusal names the case,
// the finding, and the confirm-novel repair, and appending the record reconciles.
func TestReconcileNovelBacklogRequiresRecordForConfirmedPromotion(t *testing.T) {
	cr, digest := confirmedNovelCaseRun(t)
	stored := StoredRun{K: 1, CaseRuns: map[string]CaseRun{cr.Case: cr}}
	root := t.TempDir()

	err := ReconcileNovelBacklog(root, stored)
	if err == nil || !strings.Contains(err.Error(), "finding f1") || !strings.Contains(err.Error(), "confirm-novel") {
		t.Fatalf("missing record reconciliation = %v, want error naming the finding and the confirm-novel repair", err)
	}
	if err := AppendNovelBacklog(root, reconcileRecord("case", "f1", "run-1", digest)); err != nil {
		t.Fatal(err)
	}
	if err := ReconcileNovelBacklog(root, stored); err != nil {
		t.Fatalf("record in place: %v, want reconciliation to pass", err)
	}
}

// The run-id formula must mirror the writer's: the harness prefix is part of the identity,
// so an unprefixed record does not satisfy a harness-bound run.
func TestReconcileNovelBacklogMatchesHarnessPrefixedRunID(t *testing.T) {
	cr, digest := confirmedNovelCaseRun(t)
	stored := StoredRun{K: 1, Record: RunRecord{HarnessID: "h1"}, CaseRuns: map[string]CaseRun{cr.Case: cr}}
	root := t.TempDir()

	if err := AppendNovelBacklog(root, reconcileRecord("case", "f1", "run-1", digest)); err != nil {
		t.Fatal(err)
	}
	if err := ReconcileNovelBacklog(root, stored); err == nil || !strings.Contains(err.Error(), "confirm-novel") {
		t.Fatalf("unprefixed record for a harness-bound run = %v, want missing-record refusal", err)
	}
	if err := AppendNovelBacklog(root, reconcileRecord("case", "f1", "h1/run-1", digest)); err != nil {
		t.Fatal(err)
	}
	if err := ReconcileNovelBacklog(root, stored); err != nil {
		t.Fatalf("prefixed record in place: %v, want reconciliation to pass", err)
	}
}

// The tampering rules: a record referencing a proof digest no confirmation bound, a record
// claiming a finding other than the one that confirmed the digest, and a malformed backlog
// file each fail closed with an explicit tamper refusal.
func TestReconcileNovelBacklogRefusesTamperedAndMalformedRecords(t *testing.T) {
	cr, digest := confirmedNovelCaseRun(t)
	stored := StoredRun{K: 1, CaseRuns: map[string]CaseRun{cr.Case: cr}}
	bound := reconcileRecord("case", "f1", "run-1", digest)

	t.Run("unknown proof digest", func(t *testing.T) {
		root := t.TempDir()
		if err := AppendNovelBacklog(root, bound); err != nil {
			t.Fatal(err)
		}
		unknown := reconcileRecord("case", "f1", "run-1", "sha256:"+strings.Repeat("e", 64))
		if err := AppendNovelBacklog(root, unknown); err != nil {
			t.Fatal(err)
		}
		err := ReconcileNovelBacklog(root, stored)
		if err == nil || !strings.Contains(err.Error(), "no recorded novel confirmation bound") {
			t.Fatalf("unknown-digest record reconciliation = %v, want tamper refusal", err)
		}
	})

	t.Run("record claims another finding", func(t *testing.T) {
		root := t.TempDir()
		misbound := reconcileRecord("case", "f2", "run-1", digest)
		if err := AppendNovelBacklog(root, misbound); err != nil {
			t.Fatal(err)
		}
		err := ReconcileNovelBacklog(root, stored)
		if err == nil || !strings.Contains(err.Error(), "claims finding f2") {
			t.Fatalf("misbound record reconciliation = %v, want tamper refusal naming the claimed finding", err)
		}
	})

	t.Run("malformed file", func(t *testing.T) {
		root := t.TempDir()
		path := filepath.Join(root, "backlog", "case.jsonl")
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte("{\"run_id\":\n"), 0o644); err != nil {
			t.Fatal(err)
		}
		err := ReconcileNovelBacklog(root, stored)
		if err == nil || !strings.Contains(err.Error(), "backlog") {
			t.Fatalf("malformed backlog reconciliation = %v, want refusal", err)
		}
	})
}

// The shared backlog carries other runs' records in the same case file; they must be
// ignored entirely — an unbound digest under another run id is not this run's problem.
func TestReconcileNovelBacklogIgnoresOtherRunsRecords(t *testing.T) {
	cr, digest := confirmedNovelCaseRun(t)
	stored := StoredRun{K: 1, CaseRuns: map[string]CaseRun{cr.Case: cr}}
	root := t.TempDir()
	if err := AppendNovelBacklog(root, reconcileRecord("case", "f1", "run-1", digest)); err != nil {
		t.Fatal(err)
	}
	foreign := reconcileRecord("case", "f1", "other/run-9", "sha256:"+strings.Repeat("d", 64))
	if err := AppendNovelBacklog(root, foreign); err != nil {
		t.Fatal(err)
	}
	if err := ReconcileNovelBacklog(root, stored); err != nil {
		t.Fatalf("foreign-run record must be ignored, got: %v", err)
	}
}

// The legacy rule: a historical direct EventDecide that set CONFIRMED_NOVEL (B0, no
// proof, no confirmation) owes no backlog record — reconciliation passes without one.
func TestReconcileNovelBacklogNeedsNoRecordForLegacyDecidedNovel(t *testing.T) {
	cr := novelFindingsCaseRun(t, "f1")
	decision := Decision{FindingID: "f1", UnmatchedOutcome: FindingConfirmedNovel,
		By: "reviewer", TS: "3", Reason: "verified novel"}
	payload, err := json.Marshal(decision)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := cr.Log.Append(Event{Entity: EntityFinding, ID: "f1", Kind: EventDecide,
		PreviousState: string(FindingNovelCandidate), NewState: string(FindingConfirmedNovel),
		Reason: decision.Reason, TS: decision.TS, Adjudicator: decision.By, Payload: payload}); err != nil {
		t.Fatal(err)
	}
	stored := StoredRun{K: 1, CaseRuns: map[string]CaseRun{cr.Case: *cr}}
	if err := ReconcileNovelBacklog(t.TempDir(), stored); err != nil {
		t.Fatalf("legacy decided CONFIRMED_NOVEL must require no record, got: %v", err)
	}
}

// Append-only history: after reopen -> reprove -> reconfirm the backlog holds two records
// bound to two confirmations. Both are accepted (earlier epochs are history), the current
// confirmation's record is required, and a lone stale record does not satisfy the current
// one — the refusal still names the confirm-novel repair.
func TestReconcileNovelBacklogAcceptsStaleHistoryAfterReopen(t *testing.T) {
	cr, firstDigest := confirmedNovelCaseRun(t)
	if err := cr.Reopen("f1", "needs a second look", "reviewer", "5"); err != nil {
		t.Fatal(err)
	}
	if err := cr.Decide(Decision{FindingID: "f1", By: "reviewer", TS: "6", Reason: "rechecked"}); err != nil {
		t.Fatal(err)
	}
	if err := cr.RecordNovelProof(novelProofFor(cr.Findings[0]), "7"); err != nil {
		t.Fatal(err)
	}
	if err := cr.ConfirmNovel("f1", "promoter", "proof verified independently", "8"); err != nil {
		t.Fatal(err)
	}
	secondDigest := ""
	for index := len(cr.Log.Events) - 1; index >= 0; index-- {
		if cr.Log.Events[index].Kind == EventNovelProof {
			secondDigest = cr.Log.Events[index].Hash
			break
		}
	}
	if firstDigest == secondDigest {
		t.Fatal("reprove produced the same proof event digest")
	}
	stored := StoredRun{K: 1, CaseRuns: map[string]CaseRun{cr.Case: cr}}

	root := t.TempDir()
	if err := AppendNovelBacklog(root, reconcileRecord("case", "f1", "run-1", firstDigest)); err != nil {
		t.Fatal(err)
	}
	if err := AppendNovelBacklog(root, reconcileRecord("case", "f1", "run-1", secondDigest)); err != nil {
		t.Fatal(err)
	}
	if err := ReconcileNovelBacklog(root, stored); err != nil {
		t.Fatalf("both records present: %v, want history accepted", err)
	}

	// Only the stale record: the current confirmation's record is missing.
	staleOnly := t.TempDir()
	if err := AppendNovelBacklog(staleOnly, reconcileRecord("case", "f1", "run-1", firstDigest)); err != nil {
		t.Fatal(err)
	}
	err := ReconcileNovelBacklog(staleOnly, stored)
	if err == nil || !strings.Contains(err.Error(), "confirm-novel") {
		t.Fatalf("stale-only record reconciliation = %v, want missing-current-record refusal", err)
	}
}

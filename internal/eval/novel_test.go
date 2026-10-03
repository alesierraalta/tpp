package eval

import (
	"bytes"
	"encoding/json"
	"fmt"
	"strings"
	"testing"
)

// novelFindingsCaseRun admits the named findings, each with its own row fingerprint and
// location, alongside one known Issue and parks every finding at NOVEL_CANDIDATE.
func novelFindingsCaseRun(t *testing.T, ids ...string) *CaseRun {
	t.Helper()
	cr := &CaseRun{
		Case: "case",
		Issues: []Issue{{Case: "case", ID: "D1", Domain: Correctness, Severity: High,
			Reproduction: Reproduction{Applies: true, Oracle: "oracle"}}},
	}
	for index, id := range ids {
		cr.Findings = append(cr.Findings, Finding{
			ID: id, Row: index + 1, Fingerprint: fmt.Sprintf("%064x", index+1),
			Location: fmt.Sprintf("src/file%d.go:%d", index+1, index+1), ReportedSeverityText: "high",
		})
	}
	if err := cr.Admit("1"); err != nil {
		t.Fatal(err)
	}
	for _, finding := range cr.Findings {
		if err := cr.Decide(Decision{FindingID: finding.ID, By: "reviewer", TS: "2", Reason: "no known issue matches"}); err != nil {
			t.Fatal(err)
		}
	}
	return cr
}

// novelProofFor returns a schema-valid saved-test proof bound to the admitted finding row.
func novelProofFor(f Finding) NovelProof {
	return NovelProof{
		Schema: NovelProofSchema, FindingID: f.ID, Fingerprint: f.Fingerprint, Location: f.Location,
		Domain: Correctness, Severity: High, IssueType: "logic",
		ExpectedBehavior: "the reply is sent exactly once",
		FailureCondition: "a reconnect after a partial write resends the reply",
		Mechanism:        "the idempotency key is dropped when the socket reconnects",
		ProofKind:        NovelProofSavedTest, ProofRule: "novel-saved-test@1",
		EvidenceDigests: []string{confirmDigestA, confirmDigestB}, SourceBinding: "sha256:" + strings.Repeat("d", 64),
		Reproduction:  NovelReproduction{Applies: true, Outcome: Reproduced, ArtifactDigest: confirmDigestA, Attempts: 1},
		AdjudicatedBy: "reviewer", AdjudicatedReason: "rows checked against the ledger", AdjudicatedTS: "3",
	}
}

// The controlled promotion path: direct Decide(CONFIRMED_NOVEL) stays refused, a recorded
// proof preserves NOVEL_CANDIDATE, ConfirmNovel performs the only transition, known Issues
// are untouched, and the closed run passes invariants.
func TestNovelProofControlledPromotionFlow(t *testing.T) {
	direct := pendingCaseRun(t)
	if err := direct.Decide(Decision{FindingID: "f1", UnmatchedOutcome: FindingConfirmedNovel, By: "reviewer", TS: "2", Reason: "looks novel"}); err == nil || !strings.Contains(err.Error(), "proof") {
		t.Fatalf("direct CONFIRMED_NOVEL must stay refused alongside the proof workflow, got %v", err)
	}

	cr := novelFindingsCaseRun(t, "f1")
	issueEvents := 0
	for _, event := range cr.Log.Events {
		if event.Entity == EntityIssue {
			issueEvents++
		}
	}
	if err := cr.RecordNovelProof(novelProofFor(cr.Findings[0]), "3"); err != nil {
		t.Fatalf("RecordNovelProof() = %v, want acceptance", err)
	}
	if got := cr.FindingState("f1"); got != FindingNovelCandidate {
		t.Fatalf("state after proof = %s, want state-preserving NOVEL_CANDIDATE", got)
	}
	if err := cr.ConfirmNovel("f1", "4"); err != nil {
		t.Fatalf("ConfirmNovel() = %v, want controlled promotion", err)
	}
	if got := cr.FindingState("f1"); got != FindingConfirmedNovel {
		t.Fatalf("state after confirm = %s, want CONFIRMED_NOVEL", got)
	}
	if err := cr.Log.Verify(); err != nil {
		t.Fatalf("promoted log must verify: %v", err)
	}
	if len(cr.Issues) != 1 || cr.IssueState("D1") != IssueUnderEvaluation {
		t.Fatalf("known issues mutated: %d issues, D1=%s", len(cr.Issues), cr.IssueState("D1"))
	}
	afterIssueEvents := 0
	for _, event := range cr.Log.Events {
		if event.Entity == EntityIssue {
			afterIssueEvents++
		}
	}
	if afterIssueEvents != issueEvents {
		t.Fatalf("novel workflow wrote issue events: %d -> %d", issueEvents, afterIssueEvents)
	}
	var proofHash string
	for _, event := range cr.Log.Events {
		if event.Kind == EventNovelProof {
			proofHash = event.Hash
		}
	}
	binding, ok := cr.NovelProofs()["f1"]
	if !ok || binding.Proof.FindingID != "f1" || binding.EventDigest != proofHash {
		t.Fatalf("NovelProofs()[f1] = %+v, %v; want effective proof with its recording event digest", binding, ok)
	}
	if err := cr.Close("5"); err != nil {
		t.Fatalf("close after promotion: %v", err)
	}
	if violations := CheckCaseRun(cr); len(violations) != 0 {
		t.Fatalf("closed invariants = %+v, want none", violations)
	}
}

// Invalid proofs are refused before any write: foreign row binding, foreign finding,
// unknown schema, unsorted evidence, and a non-conformance non-applicable claim.
func TestRecordNovelProofRejectsInvalidEvidenceWithoutMutation(t *testing.T) {
	for _, tt := range []struct {
		name   string
		mutate func(*NovelProof)
		want   string
	}{
		{"foreign fingerprint", func(p *NovelProof) { p.Fingerprint = strings.Repeat("9", 64) }, "fingerprint"},
		{"foreign finding", func(p *NovelProof) { p.FindingID = "other" }, "unknown finding"},
		{"unknown schema", func(p *NovelProof) { p.Schema = "novel-proof/9" }, "schema"},
		{"unsorted evidence", func(p *NovelProof) { p.EvidenceDigests = []string{confirmDigestB, confirmDigestA} }, "sorted"},
		{"non-conformance not-applicable", func(p *NovelProof) {
			p.Reproduction = NovelReproduction{Applies: false, Outcome: NotApplicable, ArtifactDigest: confirmDigestA}
		}, "conformance"},
		{"blank mechanism", func(p *NovelProof) { p.Mechanism = " " }, "mechanism"},
	} {
		t.Run(tt.name, func(t *testing.T) {
			cr := novelFindingsCaseRun(t, "f1")
			proof := novelProofFor(cr.Findings[0])
			tt.mutate(&proof)
			before := len(cr.Log.Events)
			err := cr.RecordNovelProof(proof, "3")
			if err == nil || !strings.Contains(strings.ToLower(err.Error()), tt.want) {
				t.Fatalf("RecordNovelProof() = %v, want error mentioning %q", err, tt.want)
			}
			if len(cr.Log.Events) != before || cr.FindingState("f1") != FindingNovelCandidate {
				t.Fatalf("rejected proof mutated the run: events %d, state %s", len(cr.Log.Events), cr.FindingState("f1"))
			}
		})
	}
}

// Log.Append applies the confirmation-grade canonical gate to novel-proof events: strict
// decoding, exact event binding, the fixed instrument rule, and byte-canonical payloads
// (which also reject duplicate keys and alternate encodings).
func TestLogAppendRejectsMalformedNovelProofPayloads(t *testing.T) {
	cr := novelFindingsCaseRun(t, "f1")
	canonical, err := json.Marshal(novelProofFor(cr.Findings[0]))
	if err != nil {
		t.Fatal(err)
	}
	var indented bytes.Buffer
	if err := json.Indent(&indented, canonical, "", "  "); err != nil {
		t.Fatal(err)
	}
	for _, tt := range []struct {
		name        string
		eventID     string
		adjudicator string
		payload     string
		want        string
	}{
		{"unknown field", "f1", NovelProofAdjudicator, strings.Replace(string(canonical), "{", `{"bogus":true,`, 1), "unknown"},
		{"trailing JSON", "f1", NovelProofAdjudicator, string(canonical) + " {}", "trailing"},
		{"duplicate key", "f1", NovelProofAdjudicator, strings.Replace(string(canonical), `"finding_id":"f1"`, `"finding_id":"f1","finding_id":"f1"`, 1), "canonical"},
		{"alternate encoding", "f1", NovelProofAdjudicator, indented.String(), "canonical"},
		{"foreign finding", "other", NovelProofAdjudicator, string(canonical), "event id"},
		{"foreign adjudicator", "f1", "human-reviewer", string(canonical), NovelProofAdjudicator},
	} {
		t.Run(tt.name, func(t *testing.T) {
			before := len(cr.Log.Events)
			_, err := cr.Log.Append(Event{Entity: EntityFinding, ID: tt.eventID, Kind: EventNovelProof,
				PreviousState: string(FindingNovelCandidate), NewState: string(FindingNovelCandidate),
				TS: "3", Adjudicator: tt.adjudicator, Payload: json.RawMessage(tt.payload)})
			if err == nil || !strings.Contains(strings.ToLower(err.Error()), strings.ToLower(tt.want)) {
				t.Fatalf("Append() = %v, want error mentioning %q", err, tt.want)
			}
			if len(cr.Log.Events) != before {
				t.Fatalf("rejected payload mutated the log: %d events", len(cr.Log.Events))
			}
		})
	}
}

// Tampering with recorded proof bytes breaks verification, and rehashing the tampered
// proof event cannot satisfy the confirmation that still references the original hash.
func TestTamperedAndRehashedNovelProofFailVerification(t *testing.T) {
	cr := novelFindingsCaseRun(t, "f1")
	if err := cr.RecordNovelProof(novelProofFor(cr.Findings[0]), "3"); err != nil {
		t.Fatal(err)
	}
	proofIndex := -1
	for index, event := range cr.Log.Events {
		if event.Kind == EventNovelProof {
			proofIndex = index
		}
	}
	if proofIndex < 0 {
		t.Fatal("recorded proof event missing")
	}
	if err := cr.ConfirmNovel("f1", "4"); err != nil {
		t.Fatal(err)
	}
	cr.Log.Events[proofIndex].Payload = bytes.Replace(cr.Log.Events[proofIndex].Payload, []byte(`"high"`), []byte(`"medium"`), 1)
	if err := cr.Log.Verify(); err == nil {
		t.Fatal("tampered proof payload must fail hash-chain verification")
	}
	tampered := cr.Log.Events[proofIndex]
	tampered.Hash = ""
	hash, err := hashEvent(tampered)
	if err != nil {
		t.Fatal(err)
	}
	cr.Log.Events[proofIndex].Hash = hash
	if err := cr.Log.Verify(); err == nil {
		t.Fatal("rehashed proof must fail verification while the confirmation references the original proof hash")
	}
}

// A reopen invalidates the recorded proof: confirmation is refused without a fresh proof
// in the new adjudication epoch, without mutating the run, until one is recorded again.
func TestConfirmNovelRequiresFreshProofAfterReopen(t *testing.T) {
	cr := novelFindingsCaseRun(t, "f1")
	if err := cr.ConfirmNovel("f1", "3"); err == nil || !strings.Contains(err.Error(), "proof") {
		t.Fatalf("ConfirmNovel without any proof = %v, want refusal", err)
	}
	if err := cr.RecordNovelProof(novelProofFor(cr.Findings[0]), "3"); err != nil {
		t.Fatal(err)
	}
	if err := cr.ConfirmNovel("f1", "4"); err != nil {
		t.Fatal(err)
	}
	if err := cr.Reopen("f1", "needs a second look", "reviewer", "5"); err != nil {
		t.Fatal(err)
	}
	if got := len(cr.NovelProofs()); got != 0 {
		t.Fatalf("NovelProofs() after reopen = %d entries, want the old proof invalidated by the new epoch", got)
	}
	if err := cr.Decide(Decision{FindingID: "f1", By: "reviewer", TS: "6", Reason: "rechecked"}); err != nil {
		t.Fatal(err)
	}
	before := len(cr.Log.Events)
	if err := cr.ConfirmNovel("f1", "7"); err == nil || !strings.Contains(err.Error(), "proof") {
		t.Fatalf("ConfirmNovel with only a pre-reopen proof = %v, want stale-proof refusal", err)
	}
	if len(cr.Log.Events) != before || cr.FindingState("f1") != FindingNovelCandidate {
		t.Fatalf("stale refusal mutated the run: events %d, state %s", len(cr.Log.Events), cr.FindingState("f1"))
	}
	if err := cr.RecordNovelProof(novelProofFor(cr.Findings[0]), "8"); err != nil {
		t.Fatalf("fresh proof must be accepted: %v", err)
	}
	if err := cr.ConfirmNovel("f1", "9"); err != nil {
		t.Fatalf("fresh proof must promote: %v", err)
	}
	if got := cr.FindingState("f1"); got != FindingConfirmedNovel {
		t.Fatalf("state after fresh promotion = %s, want CONFIRMED_NOVEL", got)
	}
}

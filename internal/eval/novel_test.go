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

// novelProofFor returns a schema-valid saved-test proof bound to the admitted finding
// row, derived through the pure constructor from a supported fabricated observation so
// its digest fields stay internally coherent.
func novelProofFor(f Finding) NovelProof {
	proof, err := ProofFromReplay(f, replayFacts(), supportedReplayObservation())
	if err != nil {
		panic(fmt.Sprintf("novelProofFor: %v", err))
	}
	return proof
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
	if err := cr.ConfirmNovel("f1", "promoter", "proof verified independently", "4"); err != nil {
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
	if err := cr.ConfirmNovel("f1", "promoter", "proof verified independently", "4"); err != nil {
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
	if err := cr.ConfirmNovel("f1", "promoter", "proof verified independently", "3"); err == nil || !strings.Contains(err.Error(), "proof") {
		t.Fatalf("ConfirmNovel without any proof = %v, want refusal", err)
	}
	if err := cr.RecordNovelProof(novelProofFor(cr.Findings[0]), "3"); err != nil {
		t.Fatal(err)
	}
	if err := cr.ConfirmNovel("f1", "promoter", "proof verified independently", "4"); err != nil {
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
	if err := cr.ConfirmNovel("f1", "promoter", "proof verified independently", "7"); err == nil || !strings.Contains(err.Error(), "proof") {
		t.Fatalf("ConfirmNovel with only a pre-reopen proof = %v, want stale-proof refusal", err)
	}
	if len(cr.Log.Events) != before || cr.FindingState("f1") != FindingNovelCandidate {
		t.Fatalf("stale refusal mutated the run: events %d, state %s", len(cr.Log.Events), cr.FindingState("f1"))
	}
	if err := cr.RecordNovelProof(novelProofFor(cr.Findings[0]), "8"); err != nil {
		t.Fatalf("fresh proof must be accepted: %v", err)
	}
	if err := cr.ConfirmNovel("f1", "promoter", "proof verified independently", "9"); err != nil {
		t.Fatalf("fresh proof must promote: %v", err)
	}
	if got := cr.FindingState("f1"); got != FindingConfirmedNovel {
		t.Fatalf("state after fresh promotion = %s, want CONFIRMED_NOVEL", got)
	}
}

// The promotion payload must record the declared confirmer and reason the human
// separation-of-duties decision requires: a payload missing either field (or carrying a
// blank one) is refused at Append, at Verify under a recomputed hash chain, and as the
// I9 invariant; a tampered recorded by breaks the hash chain; the model layer itself
// refuses the proof's own adjudicator, and the accepted declarations are recorded
// trimmed. Identities are self-declared strings, not authenticated principals.
func TestNovelConfirmPayloadRequiresDeclaredByAndReason(t *testing.T) {
	// legacyPayload is the never-released pre-separation shape: no confirmer, no reason.
	type legacyPayload struct {
		FindingID     string `json:"finding_id"`
		ProofHash     string `json:"proof_hash"`
		SourceBinding string `json:"source_binding"`
	}
	type payloadWithBy struct {
		FindingID     string `json:"finding_id"`
		ProofHash     string `json:"proof_hash"`
		SourceBinding string `json:"source_binding"`
		By            string `json:"by"`
	}
	// prep records the finding's proof and returns the case run, the proof event, and the
	// decoded proof its payload must bind to.
	prep := func(t *testing.T) (*CaseRun, Event, NovelProof) {
		t.Helper()
		cr := novelFindingsCaseRun(t, "f1")
		if err := cr.RecordNovelProof(novelProofFor(cr.Findings[0]), "3"); err != nil {
			t.Fatal(err)
		}
		for _, event := range cr.Log.Events {
			if event.Kind == EventNovelProof {
				proof, err := decodeNovelProof(event)
				if err != nil {
					t.Fatal(err)
				}
				return cr, event, proof
			}
		}
		t.Fatal("recorded proof event missing")
		return nil, Event{}, NovelProof{}
	}
	confirmEvent := func(payload json.RawMessage) Event {
		return Event{Entity: EntityFinding, ID: "f1", Kind: EventNovelConfirm,
			PreviousState: string(FindingNovelCandidate), NewState: string(FindingConfirmedNovel),
			TS: "4", Adjudicator: NovelConfirmAdjudicator, Payload: payload}
	}
	validPayload := func(t *testing.T, pe Event, proof NovelProof) json.RawMessage {
		t.Helper()
		data, err := json.Marshal(novelConfirmPayload{FindingID: "f1", ProofHash: pe.Hash,
			SourceBinding: proof.SourceBinding, By: "promoter", Reason: "second look"})
		if err != nil {
			t.Fatal(err)
		}
		return data
	}
	for _, tt := range []struct {
		name    string
		want    string
		payload func(Event, NovelProof) json.RawMessage
	}{
		{"missing by and reason", "by", func(pe Event, proof NovelProof) json.RawMessage {
			data, _ := json.Marshal(legacyPayload{FindingID: "f1", ProofHash: pe.Hash, SourceBinding: proof.SourceBinding})
			return data
		}},
		{"blank by", "by", func(pe Event, proof NovelProof) json.RawMessage {
			data, _ := json.Marshal(novelConfirmPayload{FindingID: "f1", ProofHash: pe.Hash,
				SourceBinding: proof.SourceBinding, By: " ", Reason: "second look"})
			return data
		}},
		{"missing reason", "reason", func(pe Event, proof NovelProof) json.RawMessage {
			data, _ := json.Marshal(payloadWithBy{FindingID: "f1", ProofHash: pe.Hash,
				SourceBinding: proof.SourceBinding, By: "promoter"})
			return data
		}},
	} {
		t.Run(tt.name, func(t *testing.T) {
			cr, pe, proof := prep(t)
			payload := tt.payload(pe, proof)
			before := len(cr.Log.Events)
			if _, err := cr.Log.Append(confirmEvent(payload)); err == nil || !strings.Contains(err.Error(), tt.want) {
				t.Fatalf("Append() = %v, want error mentioning %q", err, tt.want)
			}
			if len(cr.Log.Events) != before || cr.FindingState("f1") != FindingNovelCandidate {
				t.Fatalf("refused payload mutated the run: events %d, state %s", len(cr.Log.Events), cr.FindingState("f1"))
			}
			craftConfirm(t, &cr.Log, confirmEvent(payload))
			if err := cr.Log.Verify(); err == nil || !strings.Contains(err.Error(), tt.want) {
				t.Fatalf("Verify() = %v, want error mentioning %q", err, tt.want)
			}
			found := false
			for _, violation := range CheckCaseRun(cr) {
				if violation.ID == "I9" && strings.Contains(violation.Detail, tt.want) {
					found = true
				}
			}
			if !found {
				t.Fatalf("CheckCaseRun() = %+v, want I9 mentioning %q", CheckCaseRun(cr), tt.want)
			}
		})
	}

	// A tampered recorded by fails verification through the hash chain: the payload is
	// hashed into the event, so editing the declaration in place cannot verify.
	t.Run("tampered declared by fails verification", func(t *testing.T) {
		cr, pe, proof := prep(t)
		if _, err := cr.Log.Append(confirmEvent(validPayload(t, pe, proof))); err != nil {
			t.Fatal(err)
		}
		if err := cr.Log.Verify(); err != nil {
			t.Fatalf("valid promotion must verify: %v", err)
		}
		last := len(cr.Log.Events) - 1
		tampered := bytes.Replace(cr.Log.Events[last].Payload, []byte(`"promoter"`), []byte(`"mallory"`), 1)
		if bytes.Equal(tampered, cr.Log.Events[last].Payload) {
			t.Fatal("tamper did not change the payload")
		}
		cr.Log.Events[last].Payload = tampered
		if err := cr.Log.Verify(); err == nil || !strings.Contains(err.Error(), "hash") {
			t.Fatalf("Verify() = %v, want the hash chain to refuse the tampered by", err)
		}
	})

	// Separation of duties holds at the model layer too: the proof's own adjudicator
	// cannot promote it, and the refusal leaves the log untouched.
	t.Run("the proof's adjudicator cannot confirm it", func(t *testing.T) {
		cr, _, _ := prep(t)
		before := len(cr.Log.Events)
		err := cr.ConfirmNovel("f1", "reviewer", "second look", "4")
		if err == nil || !strings.Contains(err.Error(), "separation of duties") {
			t.Fatalf("ConfirmNovel(same identity) = %v, want separation-of-duties refusal", err)
		}
		if len(cr.Log.Events) != before || cr.FindingState("f1") != FindingNovelCandidate {
			t.Fatalf("refusal mutated the run: events %d, state %s", len(cr.Log.Events), cr.FindingState("f1"))
		}
	})

	t.Run("records the trimmed declared by and reason", func(t *testing.T) {
		cr, _, _ := prep(t)
		if err := cr.ConfirmNovel("f1", " promoter ", "  second look  ", "4"); err != nil {
			t.Fatalf("ConfirmNovel() = %v, want acceptance", err)
		}
		var payload novelConfirmPayload
		if err := json.Unmarshal(cr.Log.Events[len(cr.Log.Events)-1].Payload, &payload); err != nil {
			t.Fatal(err)
		}
		if payload.By != "promoter" || payload.Reason != "second look" {
			t.Fatalf("recorded by/reason = %q/%q, want the trimmed declarations", payload.By, payload.Reason)
		}
		if err := cr.Log.Verify(); err != nil {
			t.Fatalf("promoted log must verify: %v", err)
		}
	})
}

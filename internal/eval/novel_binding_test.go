package eval

import (
	"bytes"
	"encoding/json"
	"reflect"
	"strings"
	"testing"

	"github.com/alesierraalta/tpp/internal/bench"
)

// supportedReplayObservation fabricates a fully conclusive bench replay observation with
// distinct well-formed bare-hex digests: subject source "d" (the historical binding),
// subject output "b" (the historical artifact), test "a", control output "c", control
// tree "e", control source "f".
func supportedReplayObservation() bench.NovelReplayObservation {
	testHex := strings.Repeat("a", 64)
	return bench.NovelReplayObservation{
		RuleVersion: bench.NovelCorrectnessRuleV1,
		ControlName: "fixture-control",
		TestSHA256:  testHex,
		Subject: bench.NovelProcessObservation{
			Classification: bench.NovelAssertionFailure,
			ExitCode:       1,
			Started:        true,
			Complete:       true,
			OutputSHA256:   strings.Repeat("b", 64),
			SourceSHA256:   strings.Repeat("d", 64),
			TestSHA256:     testHex,
		},
		Control: bench.NovelProcessObservation{
			Classification: bench.NovelPassed,
			ExitCode:       0,
			Started:        true,
			Complete:       true,
			OutputSHA256:   strings.Repeat("c", 64),
			SourceSHA256:   strings.Repeat("f", 64),
			ControlSHA256:  strings.Repeat("e", 64),
			TestSHA256:     testHex,
		},
		SupportedReproduction: true,
		AdjudicationRequired:  true,
	}
}

// replayFacts are the blind semantic facts a caller must supply to ProofFromReplay.
func replayFacts() NovelProofFacts {
	return NovelProofFacts{
		Domain: Correctness, Severity: High, IssueType: "logic",
		ExpectedBehavior: "the reply is sent exactly once",
		FailureCondition: "a reconnect after a partial write resends the reply",
		Mechanism:        "the idempotency key is dropped when the socket reconnects",
		ProofKind:        NovelProofSavedTest, ProofRule: "novel-saved-test@1",
		AdjudicatedBy: "reviewer", AdjudicatedReason: "rows checked against the ledger", AdjudicatedTS: "3",
	}
}

// schemaV1ProofFor builds the never-released novel-proof/1 shape: no observation and
// caller-asserted digests only, the payload every gate must now refuse.
func schemaV1ProofFor(f Finding) NovelProof {
	facts := replayFacts()
	return NovelProof{
		Schema: NovelProofSchema, FindingID: f.ID, Fingerprint: f.Fingerprint, Location: f.Location,
		Domain: facts.Domain, Severity: facts.Severity, IssueType: facts.IssueType,
		ExpectedBehavior: facts.ExpectedBehavior, FailureCondition: facts.FailureCondition,
		Mechanism: facts.Mechanism, ProofKind: facts.ProofKind, ProofRule: facts.ProofRule,
		EvidenceDigests: []string{confirmDigestA, confirmDigestB},
		SourceBinding:   "sha256:" + strings.Repeat("d", 64),
		Reproduction:    NovelReproduction{Applies: true, Outcome: Reproduced, ArtifactDigest: confirmDigestA, Attempts: 1},
		AdjudicatedBy:   facts.AdjudicatedBy, AdjudicatedReason: facts.AdjudicatedReason, AdjudicatedTS: facts.AdjudicatedTS,
	}
}

// A supported replay observation derives a fully bound novel-proof/2 proof whose digest
// fields come from the observation, and the recorded event round-trips: RecordNovelProof
// appends it, the log verifies, the proof reconstructs from the event, and the controlled
// promotion still works.
func TestProofFromReplayDerivesBoundProofAndRecordsV2(t *testing.T) {
	cr := novelFindingsCaseRun(t, "f1")
	finding := cr.Findings[0]
	proof, err := ProofFromReplay(finding, replayFacts(), supportedReplayObservation())
	if err != nil {
		t.Fatalf("ProofFromReplay() = %v, want a derived proof", err)
	}
	if proof.Schema != NovelProofSchemaV2 {
		t.Fatalf("proof schema = %q, want %q", proof.Schema, NovelProofSchemaV2)
	}
	if proof.Observation == nil {
		t.Fatal("proof observation = nil, want the typed replay observation")
	}
	if want := "sha256:" + strings.Repeat("d", 64); proof.SourceBinding != want {
		t.Fatalf("source binding = %q, want the staged subject source %q", proof.SourceBinding, want)
	}
	if want := "sha256:" + strings.Repeat("b", 64); proof.Reproduction.ArtifactDigest != want {
		t.Fatalf("artifact digest = %q, want the subject output %q", proof.Reproduction.ArtifactDigest, want)
	}
	wantEvidence := []string{
		"sha256:" + strings.Repeat("a", 64),
		"sha256:" + strings.Repeat("b", 64),
		"sha256:" + strings.Repeat("c", 64),
		"sha256:" + strings.Repeat("e", 64),
	}
	if !reflect.DeepEqual(proof.EvidenceDigests, wantEvidence) {
		t.Fatalf("evidence digests = %v, want the sorted test/output/control digests %v", proof.EvidenceDigests, wantEvidence)
	}
	if !proof.Reproduction.Applies || proof.Reproduction.Outcome != Reproduced || proof.Reproduction.Attempts != 1 {
		t.Fatalf("reproduction = %+v, want an applicable reproduced proof with one attempt", proof.Reproduction)
	}
	if bytes.Contains([]byte(strings.ToLower(string(mustMarshal(t, proof)))), []byte(`"output":"`)) {
		t.Fatal("proof payload must not carry raw runner output")
	}
	if err := cr.RecordNovelProof(proof, "3"); err != nil {
		t.Fatalf("RecordNovelProof() = %v, want acceptance", err)
	}
	if err := cr.Log.Verify(); err != nil {
		t.Fatalf("log after /2 proof must verify: %v", err)
	}
	payload := cr.Log.Events[len(cr.Log.Events)-1].Payload
	if !bytes.Contains(payload, []byte(`"schema":"`+NovelProofSchemaV2+`"`)) || !bytes.Contains(payload, []byte(`"observation":{`)) {
		t.Fatalf("recorded payload = %s, want a novel-proof/2 event carrying the observation", payload)
	}
	binding, ok := cr.NovelProofs()["f1"]
	if !ok {
		t.Fatal("recorded proof does not reconstruct as the effective proof")
	}
	if !reflect.DeepEqual(binding.Proof, proof) {
		t.Fatalf("reconstructed proof = %+v, want the constructed proof %+v", binding.Proof, proof)
	}
	if err := cr.ConfirmNovel("f1", "promoter", "proof verified independently", "4"); err != nil {
		t.Fatalf("ConfirmNovel() = %v, want controlled promotion", err)
	}
	if got := cr.FindingState("f1"); got != FindingConfirmedNovel {
		t.Fatalf("state after confirmation = %s, want CONFIRMED_NOVEL", got)
	}
}

// ProofFromReplay refuses every inconclusive or malformed observation class and any
// blank blind semantic fact instead of producing caller-asserted evidence.
func TestProofFromReplayRefusesInconclusiveOrMalformedObservations(t *testing.T) {
	finding := Finding{ID: "f1", Row: 1, Fingerprint: strings.Repeat("0", 63) + "1", Location: "src/file1.go:1"}
	for _, tt := range []struct {
		name   string
		mutate func(*bench.NovelReplayObservation)
		want   string
	}{
		{"subject classification", func(o *bench.NovelReplayObservation) { o.Subject.Classification = bench.NovelInconclusive }, "subject classification"},
		{"subject timed out", func(o *bench.NovelReplayObservation) { o.Subject.TimedOut = true }, "timed out"},
		{"subject output truncated", func(o *bench.NovelReplayObservation) { o.Subject.OutputTruncated = true }, "truncated"},
		{"subject incomplete", func(o *bench.NovelReplayObservation) { o.Subject.Complete = false }, "not complete"},
		{"subject not started", func(o *bench.NovelReplayObservation) { o.Subject.Started = false }, "did not start"},
		{"subject reason", func(o *bench.NovelReplayObservation) { o.Subject.Reason = "descendant processes outlived test runner" }, "reason"},
		{"control classification", func(o *bench.NovelReplayObservation) { o.Control.Classification = bench.NovelAssertionFailure }, "control classification"},
		{"control timed out", func(o *bench.NovelReplayObservation) { o.Control.TimedOut = true }, "control timed out"},
		{"control output truncated", func(o *bench.NovelReplayObservation) { o.Control.OutputTruncated = true }, "control output truncated"},
		{"control incomplete", func(o *bench.NovelReplayObservation) { o.Control.Complete = false }, "control run is not complete"},
		{"control not started", func(o *bench.NovelReplayObservation) { o.Control.Started = false }, "control run did not start"},
		{"control reason", func(o *bench.NovelReplayObservation) { o.Control.Reason = "control runner crashed" }, "control reason"},
		{"unsupported reproduction", func(o *bench.NovelReplayObservation) { o.SupportedReproduction = false }, "support"},
		{"adjudication not required", func(o *bench.NovelReplayObservation) { o.AdjudicationRequired = false }, "adjudication"},
		{"malformed subject output digest", func(o *bench.NovelReplayObservation) { o.Subject.OutputSHA256 = "deadbeef" }, "digest"},
		{"missing subject source digest", func(o *bench.NovelReplayObservation) { o.Subject.SourceSHA256 = "" }, "digest"},
		{"missing control tree digest", func(o *bench.NovelReplayObservation) { o.Control.ControlSHA256 = "" }, "digest"},
		{"test digest mismatch", func(o *bench.NovelReplayObservation) { o.TestSHA256 = strings.Repeat("9", 64) }, "does not match the subject test digest"},
		{"unknown observation rule version", func(o *bench.NovelReplayObservation) { o.RuleVersion = "not-a-bench-rule" }, "rule version"},
		{"empty control name", func(o *bench.NovelReplayObservation) { o.ControlName = "" }, "control name"},
		{"control name with path separator", func(o *bench.NovelReplayObservation) { o.ControlName = "fixture/../evil" }, "control name"},
		{"control name with control character", func(o *bench.NovelReplayObservation) { o.ControlName = "fixture\x01control" }, "control name"},
		{"subject control digest set", func(o *bench.NovelReplayObservation) { o.Subject.ControlSHA256 = strings.Repeat("9", 64) }, "subject control digest"},
	} {
		t.Run(tt.name, func(t *testing.T) {
			observation := supportedReplayObservation()
			tt.mutate(&observation)
			_, err := ProofFromReplay(finding, replayFacts(), observation)
			if err == nil || !strings.Contains(strings.ToLower(err.Error()), strings.ToLower(tt.want)) {
				t.Fatalf("ProofFromReplay() = %v, want error mentioning %q", err, tt.want)
			}
		})
	}
}

// The blind semantic facts stay caller-supplied: every blank fact is refused.
func TestProofFromReplayRequiresBlindSemanticFacts(t *testing.T) {
	finding := Finding{ID: "f1", Row: 1, Fingerprint: strings.Repeat("0", 63) + "1", Location: "src/file1.go:1"}
	for _, tt := range []struct {
		name   string
		mutate func(*NovelProofFacts)
		want   string
	}{
		{"issue type", func(f *NovelProofFacts) { f.IssueType = " " }, "issue_type"},
		{"expected behavior", func(f *NovelProofFacts) { f.ExpectedBehavior = "" }, "expected_behavior"},
		{"failure condition", func(f *NovelProofFacts) { f.FailureCondition = "" }, "failure_condition"},
		{"mechanism", func(f *NovelProofFacts) { f.Mechanism = " " }, "mechanism"},
		{"mismatched proof kind", func(f *NovelProofFacts) { f.ProofKind = NovelProofConformance }, "kind"},
		{"mismatched proof rule", func(f *NovelProofFacts) { f.ProofRule = "novel-saved-test@2" }, "derived from observation rule version"},
		{"adjudicated by", func(f *NovelProofFacts) { f.AdjudicatedBy = "" }, "adjudicated_by"},
		{"adjudicated reason", func(f *NovelProofFacts) { f.AdjudicatedReason = "" }, "adjudicated_reason"},
		{"adjudicated ts", func(f *NovelProofFacts) { f.AdjudicatedTS = "" }, "adjudicated_ts"},
	} {
		t.Run(tt.name, func(t *testing.T) {
			facts := replayFacts()
			tt.mutate(&facts)
			_, err := ProofFromReplay(finding, facts, supportedReplayObservation())
			if err == nil || !strings.Contains(strings.ToLower(err.Error()), strings.ToLower(tt.want)) {
				t.Fatalf("ProofFromReplay() = %v, want error mentioning %q", err, tt.want)
			}
		})
	}
}

// A blank ProofKind/ProofRule is derived from the replay observation — the correctness
// saved-test kind and the rule mapped from its rule version — instead of refused. A
// non-blank value that contradicts the derivation is still refused.
func TestProofFromReplayDerivesBlankProofIdentity(t *testing.T) {
	finding := Finding{ID: "f1", Row: 1, Fingerprint: strings.Repeat("0", 63) + "1", Location: "src/file1.go:1"}
	facts := replayFacts()
	facts.ProofKind, facts.ProofRule = "", ""
	proof, err := ProofFromReplay(finding, facts, supportedReplayObservation())
	if err != nil {
		t.Fatalf("ProofFromReplay(blank proof identity) = %v, want the derived kind and rule", err)
	}
	if proof.ProofKind != NovelProofSavedTest {
		t.Fatalf("proof kind = %q, want the derived %q", proof.ProofKind, NovelProofSavedTest)
	}
	if proof.ProofRule != novelSavedTestRuleV1 {
		t.Fatalf("proof rule = %q, want the derived %q", proof.ProofRule, novelSavedTestRuleV1)
	}
	mismatched := replayFacts()
	mismatched.ProofRule = "novel-saved-test@2"
	if _, err := ProofFromReplay(finding, mismatched, supportedReplayObservation()); err == nil || !strings.Contains(err.Error(), "derived from observation rule version") {
		t.Fatalf("ProofFromReplay(mismatched rule) = %v, want a refusal naming the derivation", err)
	}
	wrongKind := replayFacts()
	wrongKind.ProofKind = NovelProofConformance
	if _, err := ProofFromReplay(finding, wrongKind, supportedReplayObservation()); err == nil || !strings.Contains(err.Error(), "kind") {
		t.Fatalf("ProofFromReplay(non-saved-test kind) = %v, want a refusal naming the kind", err)
	}
}

// Writes must be novel-proof/2 with an observation: schema v1 proofs and v2 proofs that
// lost their observation are refused before any mutation.
func TestRecordNovelProofRefusesSchemaV1AndObservationlessWrites(t *testing.T) {
	for _, tt := range []struct {
		name   string
		mutate func(*NovelProof)
	}{
		{"schema v1", func(p *NovelProof) { p.Schema = NovelProofSchema; p.Observation = nil }},
		{"missing observation", func(p *NovelProof) { p.Observation = nil }},
	} {
		t.Run(tt.name, func(t *testing.T) {
			cr := novelFindingsCaseRun(t, "f1")
			proof, err := ProofFromReplay(cr.Findings[0], replayFacts(), supportedReplayObservation())
			if err != nil {
				t.Fatal(err)
			}
			tt.mutate(&proof)
			before := len(cr.Log.Events)
			err = cr.RecordNovelProof(proof, "3")
			if err == nil || !strings.Contains(err.Error(), NovelProofSchemaV2) {
				t.Fatalf("RecordNovelProof() = %v, want refusal naming %s", err, NovelProofSchemaV2)
			}
			if len(cr.Log.Events) != before || cr.FindingState("f1") != FindingNovelCandidate {
				t.Fatalf("refused write mutated the run: events %d, state %s", len(cr.Log.Events), cr.FindingState("f1"))
			}
		})
	}
}

// Novel-proof/1 carried caller-asserted digests and no observation, so it is refused
// everywhere: Append rejects a hand-built /1 event without mutating the log, a log
// already containing one fails Verify even under a recomputed hash, and such a proof
// can never drive the controlled promotion.
func TestSchemaV1NovelProofRefusedAtAppendVerifyAndPromotion(t *testing.T) {
	cr := novelFindingsCaseRun(t, "f1")
	event := craftedNovelProofEvent(t, schemaV1ProofFor(cr.Findings[0]))
	before := len(cr.Log.Events)
	if _, err := cr.Log.Append(event); err == nil || !strings.Contains(err.Error(), NovelProofSchemaV2) {
		t.Fatalf("Append(novel-proof/1) = %v, want refusal naming %s", err, NovelProofSchemaV2)
	}
	if len(cr.Log.Events) != before {
		t.Fatalf("rejected /1 payload mutated the log: %d events", len(cr.Log.Events))
	}
	craftConfirm(t, &cr.Log, event)
	if err := cr.Log.Verify(); err == nil || !strings.Contains(err.Error(), NovelProofSchemaV2) {
		t.Fatalf("Verify(log containing a /1 proof under a recomputed hash) = %v, want refusal naming %s", err, NovelProofSchemaV2)
	}
	if err := cr.ConfirmNovel("f1", "promoter", "proof verified independently", "4"); err == nil {
		t.Fatal("ConfirmNovel promoted a refused novel-proof/1 event")
	}
}

// A hand-tampered /2 payload is rejected by the observation gate itself at Append and at
// Verify (with a recomputed hash chain), not only by the constructor.
func TestTamperedNovelProofObservationRejectedAtAppendAndVerify(t *testing.T) {
	newEvent := func(t *testing.T, proof NovelProof) Event {
		t.Helper()
		payload, err := json.Marshal(proof)
		if err != nil {
			t.Fatal(err)
		}
		return Event{Entity: EntityFinding, ID: proof.FindingID, Kind: EventNovelProof,
			PreviousState: string(FindingNovelCandidate), NewState: string(FindingNovelCandidate),
			TS: "3", Adjudicator: NovelProofAdjudicator, Payload: payload}
	}
	tamperedProof := func(t *testing.T, mutate func(*NovelProof)) NovelProof {
		t.Helper()
		cr := novelFindingsCaseRun(t, "f1")
		proof, err := ProofFromReplay(cr.Findings[0], replayFacts(), supportedReplayObservation())
		if err != nil {
			t.Fatal(err)
		}
		mutate(&proof)
		return proof
	}
	t.Run("digest mismatch at append", func(t *testing.T) {
		proof := tamperedProof(t, func(p *NovelProof) { p.SourceBinding = "sha256:" + strings.Repeat("e", 64) })
		cr := novelFindingsCaseRun(t, "f1")
		before := len(cr.Log.Events)
		if _, err := cr.Log.Append(newEvent(t, proof)); err == nil || !strings.Contains(err.Error(), "source binding") {
			t.Fatalf("Append(tampered digest) = %v, want source binding mismatch", err)
		}
		if len(cr.Log.Events) != before {
			t.Fatalf("rejected payload mutated the log: %d events", len(cr.Log.Events))
		}
	})
	t.Run("inconclusive subject at append", func(t *testing.T) {
		proof := tamperedProof(t, func(p *NovelProof) { p.Observation.Subject.Classification = string(bench.NovelInconclusive) })
		cr := novelFindingsCaseRun(t, "f1")
		before := len(cr.Log.Events)
		if _, err := cr.Log.Append(newEvent(t, proof)); err == nil || !strings.Contains(err.Error(), "subject classification") {
			t.Fatalf("Append(inconclusive subject) = %v, want classification refusal", err)
		}
		if len(cr.Log.Events) != before {
			t.Fatalf("rejected payload mutated the log: %d events", len(cr.Log.Events))
		}
	})
	t.Run("digest tamper fails verify despite a recomputed hash", func(t *testing.T) {
		cr := novelFindingsCaseRun(t, "f1")
		proof, err := ProofFromReplay(cr.Findings[0], replayFacts(), supportedReplayObservation())
		if err != nil {
			t.Fatal(err)
		}
		if err := cr.RecordNovelProof(proof, "3"); err != nil {
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
		tampered := cr.Log.Events[proofIndex]
		tampered.Payload = bytes.Replace(tampered.Payload,
			[]byte(`"source_binding":"sha256:`+strings.Repeat("d", 64)+`"`),
			[]byte(`"source_binding":"sha256:`+strings.Repeat("e", 64)+`"`), 1)
		if bytes.Equal(tampered.Payload, cr.Log.Events[proofIndex].Payload) {
			t.Fatal("tamper did not change the payload")
		}
		hash, err := hashEvent(tampered)
		if err != nil {
			t.Fatal(err)
		}
		tampered.Hash = hash
		cr.Log.Events[proofIndex] = tampered
		if err := cr.Log.Verify(); err == nil || !strings.Contains(err.Error(), "source binding") {
			t.Fatalf("Verify(tampered /2 payload with recomputed hash) = %v, want observation gate rejection", err)
		}
	})
	// Observation identity binding: each tampered /2 payload must be rejected at Append,
	// at Verify under a recomputed hash chain, and as the I9 invariant violation.
	for _, tt := range []struct {
		name   string
		mutate func(*NovelProof)
		want   string
	}{
		{"garbage rule version", func(p *NovelProof) { p.Observation.RuleVersion = "not-a-bench-rule" }, "rule version"},
		{"rule version valid but proof rule mismatched", func(p *NovelProof) { p.ProofRule = "novel-saved-test@2" }, "proof rule"},
		{"empty control name", func(p *NovelProof) { p.Observation.ControlName = "" }, "control name"},
		{"control name with path separator", func(p *NovelProof) { p.Observation.ControlName = "fixture/../evil" }, "control name"},
		{"control name with control character", func(p *NovelProof) { p.Observation.ControlName = "fixture\x01control" }, "control name"},
		{"subject control digest set to the control tree", func(p *NovelProof) { p.Observation.Subject.ControlSHA256 = "sha256:" + strings.Repeat("e", 64) }, "subject control digest"},
		{"subject control digest mismatched", func(p *NovelProof) { p.Observation.Subject.ControlSHA256 = "sha256:" + strings.Repeat("9", 64) }, "subject control digest"},
	} {
		t.Run(tt.name, func(t *testing.T) {
			proof := tamperedProof(t, tt.mutate)
			cr := novelFindingsCaseRun(t, "f1")
			before := len(cr.Log.Events)
			if _, err := cr.Log.Append(newEvent(t, proof)); err == nil || !strings.Contains(err.Error(), tt.want) {
				t.Fatalf("Append(tampered /2 payload) = %v, want refusal mentioning %q", err, tt.want)
			}
			if len(cr.Log.Events) != before {
				t.Fatalf("rejected payload mutated the log: %d events", len(cr.Log.Events))
			}
			craftConfirm(t, &cr.Log, newEvent(t, proof))
			if err := cr.Log.Verify(); err == nil || !strings.Contains(err.Error(), tt.want) {
				t.Fatalf("Verify(tampered /2 payload with recomputed hash) = %v, want refusal mentioning %q", err, tt.want)
			}
			violations := CheckCaseRun(cr)
			if len(violations) == 0 || violations[0].ID != "I9" || !strings.Contains(violations[0].Detail, tt.want) {
				t.Fatalf("CheckCaseRun() = %+v, want I9 mentioning %q", violations, tt.want)
			}
		})
	}
}

func mustMarshal(t *testing.T, value any) []byte {
	t.Helper()
	data, err := json.Marshal(value)
	if err != nil {
		t.Fatal(err)
	}
	return data
}

// craftedNovelProofEvent builds the canonical novel-proof event a writer would emit.
func craftedNovelProofEvent(t *testing.T, proof NovelProof) Event {
	t.Helper()
	return Event{Entity: EntityFinding, ID: proof.FindingID, Kind: EventNovelProof,
		PreviousState: string(FindingNovelCandidate), NewState: string(FindingNovelCandidate),
		TS: "3", Adjudicator: NovelProofAdjudicator, Payload: mustMarshal(t, proof)}
}

// The correctness-only scope of novel-proof/2: a proof whose saved-test kind, saved-test
// rule, or observation-derived reproduction was flipped after construction is refused at
// the constructor, at RecordNovelProof, at Append, and at Verify even with a recomputed
// hash chain.
func TestSchemaV2RejectsConformanceKindAndReproductionFlips(t *testing.T) {
	flipToConformance := func(p *NovelProof) {
		p.ProofKind = NovelProofConformance
		p.ProofRule = "novel-conformance@1"
		p.Reproduction = NovelReproduction{Applies: false, Outcome: NotApplicable, ArtifactDigest: confirmDigestB}
	}
	t.Run("constructor refuses conformance facts", func(t *testing.T) {
		facts := replayFacts()
		facts.ProofKind = NovelProofConformance
		facts.ProofRule = "novel-conformance@1"
		_, err := ProofFromReplay(Finding{ID: "f1", Row: 1, Fingerprint: strings.Repeat("0", 63) + "1", Location: "src/file1.go:1"}, facts, supportedReplayObservation())
		if err == nil || !strings.Contains(err.Error(), "novel proof/2") {
			t.Fatalf("ProofFromReplay(conformance facts) = %v, want novelty/2 correctness-only refusal", err)
		}
	})
	t.Run("record refuses conformance flip", func(t *testing.T) {
		cr := novelFindingsCaseRun(t, "f1")
		proof, err := ProofFromReplay(cr.Findings[0], replayFacts(), supportedReplayObservation())
		if err != nil {
			t.Fatal(err)
		}
		flipToConformance(&proof)
		before := len(cr.Log.Events)
		err = cr.RecordNovelProof(proof, "3")
		if err == nil || !strings.Contains(err.Error(), "novel proof/2") {
			t.Fatalf("RecordNovelProof(conformance flip) = %v, want novelty/2 refusal", err)
		}
		if len(cr.Log.Events) != before {
			t.Fatalf("rejected proof mutated the log: %d events", len(cr.Log.Events))
		}
	})
	t.Run("append refuses conformance flip", func(t *testing.T) {
		cr := novelFindingsCaseRun(t, "f1")
		proof, err := ProofFromReplay(cr.Findings[0], replayFacts(), supportedReplayObservation())
		if err != nil {
			t.Fatal(err)
		}
		flipToConformance(&proof)
		before := len(cr.Log.Events)
		if _, err := cr.Log.Append(craftedNovelProofEvent(t, proof)); err == nil || !strings.Contains(err.Error(), "novel proof/2") {
			t.Fatalf("Append(conformance flip) = %v, want novelty/2 refusal", err)
		}
		if len(cr.Log.Events) != before {
			t.Fatalf("rejected payload mutated the log: %d events", len(cr.Log.Events))
		}
	})
	t.Run("verify refuses conformance flip with recomputed hash", func(t *testing.T) {
		cr := novelFindingsCaseRun(t, "f1")
		proof, err := ProofFromReplay(cr.Findings[0], replayFacts(), supportedReplayObservation())
		if err != nil {
			t.Fatal(err)
		}
		if err := cr.RecordNovelProof(proof, "3"); err != nil {
			t.Fatal(err)
		}
		flipped := proof
		flipToConformance(&flipped)
		index := len(cr.Log.Events) - 1
		if cr.Log.Events[index].Kind != EventNovelProof {
			t.Fatalf("last event = %q, want novel proof", cr.Log.Events[index].Kind)
		}
		tampered := cr.Log.Events[index]
		tampered.Payload = mustMarshal(t, flipped)
		hash, err := hashEvent(tampered)
		if err != nil {
			t.Fatal(err)
		}
		tampered.Hash = hash
		cr.Log.Events[index] = tampered
		if err := cr.Log.Verify(); err == nil || !strings.Contains(err.Error(), "novel proof/2") {
			t.Fatalf("Verify(conformance flip, recomputed hash) = %v, want novelty/2 refusal", err)
		}
	})
	t.Run("record refuses saved-test kind flip", func(t *testing.T) {
		cr := novelFindingsCaseRun(t, "f1")
		proof, err := ProofFromReplay(cr.Findings[0], replayFacts(), supportedReplayObservation())
		if err != nil {
			t.Fatal(err)
		}
		proof.ProofKind = NovelProofConformance
		if err := cr.RecordNovelProof(proof, "3"); err == nil || !strings.Contains(err.Error(), "proof kind") {
			t.Fatalf("RecordNovelProof(kind flip) = %v, want proof kind refusal", err)
		}
	})
	t.Run("append refuses saved-test rule flip", func(t *testing.T) {
		cr := novelFindingsCaseRun(t, "f1")
		proof, err := ProofFromReplay(cr.Findings[0], replayFacts(), supportedReplayObservation())
		if err != nil {
			t.Fatal(err)
		}
		proof.ProofRule = "novel-conformance@1"
		if _, err := cr.Log.Append(craftedNovelProofEvent(t, proof)); err == nil || !strings.Contains(err.Error(), "proof rule") {
			t.Fatalf("Append(rule flip) = %v, want proof rule refusal", err)
		}
	})
	t.Run("record refuses reproduction attempts mismatch", func(t *testing.T) {
		cr := novelFindingsCaseRun(t, "f1")
		proof, err := ProofFromReplay(cr.Findings[0], replayFacts(), supportedReplayObservation())
		if err != nil {
			t.Fatal(err)
		}
		proof.Reproduction.Attempts = 2
		if err := cr.RecordNovelProof(proof, "3"); err == nil || !strings.Contains(err.Error(), "reproduction") {
			t.Fatalf("RecordNovelProof(attempts mismatch) = %v, want reproduction refusal", err)
		}
	})
}

// A stored /2 payload whose control side claims classification "passed" while actually
// timed out, truncated, incomplete, not started, or carrying a reason is rejected.
func TestSchemaV2RejectsInconclusiveControlSideAtAppend(t *testing.T) {
	for _, tt := range []struct {
		name   string
		mutate func(*NovelObservationProcess)
		want   string
	}{
		{"control timed out", func(c *NovelObservationProcess) { c.TimedOut = true }, "control timed out"},
		{"control output truncated", func(c *NovelObservationProcess) { c.OutputTruncated = true }, "control output truncated"},
		{"control incomplete", func(c *NovelObservationProcess) { c.Complete = false }, "control run is not complete"},
		{"control not started", func(c *NovelObservationProcess) { c.Started = false }, "control run did not start"},
		{"control reason", func(c *NovelObservationProcess) { c.Reason = "descendant processes outlived test runner" }, "control reason"},
	} {
		t.Run(tt.name, func(t *testing.T) {
			cr := novelFindingsCaseRun(t, "f1")
			proof, err := ProofFromReplay(cr.Findings[0], replayFacts(), supportedReplayObservation())
			if err != nil {
				t.Fatal(err)
			}
			tt.mutate(&proof.Observation.Control)
			before := len(cr.Log.Events)
			_, err = cr.Log.Append(craftedNovelProofEvent(t, proof))
			if err == nil || !strings.Contains(err.Error(), tt.want) {
				t.Fatalf("Append(inconclusive control) = %v, want error mentioning %q", err, tt.want)
			}
			if len(cr.Log.Events) != before {
				t.Fatalf("rejected payload mutated the log: %d events", len(cr.Log.Events))
			}
		})
	}
}

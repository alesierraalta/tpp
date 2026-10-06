package eval

import (
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"strings"
)

// NovelProofSchema is the historical, never-released schema identity novel-proof/1: it
// carried caller-asserted digest fields only and is refused at every decode and write.
const NovelProofSchema = "novel-proof/1"

// NovelProofSchemaV2 is the current schema identity: v2 binds every proof to a required,
// digest-only typed replay observation from an actual bench replay.
const NovelProofSchemaV2 = "novel-proof/2"

// Novel proof kinds name the evidence class behind a recorded proof.
const (
	NovelProofSavedTest   = "saved_test"
	NovelProofCommand     = "command"
	NovelProofConformance = "conformance"
)

// NovelReproduction is the recorded independent-replay outcome inside a NovelProof.
type NovelReproduction struct {
	Applies        bool         `json:"applies"`
	Outcome        ReproOutcome `json:"outcome"`
	ArtifactDigest string       `json:"artifact_digest"`
	Attempts       int          `json:"attempts"`
}

// NovelObservationProcess is the digest-only process record of one replay side (subject
// or control): classification facts plus "sha256:"-prefixed digests only. Raw runner
// output never enters a recorded observation.
type NovelObservationProcess struct {
	Classification  string `json:"classification"`
	ExitCode        int    `json:"exit_code"`
	Started         bool   `json:"started"`
	TimedOut        bool   `json:"timed_out"`
	Complete        bool   `json:"complete"`
	OutputTruncated bool   `json:"output_truncated"`
	Reason          string `json:"reason,omitempty"`
	OutputSHA256    string `json:"output_sha256"`
	SourceSHA256    string `json:"source_sha256"`
	ControlSHA256   string `json:"control_sha256,omitempty"`
	TestSHA256      string `json:"test_sha256"`
}

// NovelProofObservation is the required typed payload of a novel-proof/2 event: the
// digest-only facts of the actual subject/control replay the proof's digest fields are
// bound to, plus the rule identity and both reproduction gates.
type NovelProofObservation struct {
	RuleVersion           string                  `json:"rule_version"`
	ControlName           string                  `json:"control_name"`
	TestSHA256            string                  `json:"test_sha256"`
	Subject               NovelObservationProcess `json:"subject"`
	Control               NovelObservationProcess `json:"control"`
	SupportedReproduction bool                    `json:"supported_reproduction"`
	AdjudicationRequired  bool                    `json:"adjudication_required"`
}

// NovelProof is typed evidence recorded for a NOVEL_CANDIDATE finding. The pure model
// checks schema, identity, and presence only: it never executes anything and never
// asserts the recorded facts are true; independent runtime verification is a separate
// (B2) concern that supplies the verified facts recorded here. A novel-proof/2 proof
// carries that observation and its digest fields must match it.
type NovelProof struct {
	Schema            string                 `json:"schema"`
	FindingID         string                 `json:"finding_id"`
	Fingerprint       string                 `json:"fingerprint"`
	Location          string                 `json:"location"`
	Domain            Domain                 `json:"domain"`
	Severity          Severity               `json:"severity"`
	IssueType         string                 `json:"issue_type"`
	ExpectedBehavior  string                 `json:"expected_behavior"`
	FailureCondition  string                 `json:"failure_condition"`
	Mechanism         string                 `json:"mechanism"`
	ProofKind         string                 `json:"proof_kind"`
	ProofRule         string                 `json:"proof_rule"`
	EvidenceDigests   []string               `json:"evidence_digests"`
	SourceBinding     string                 `json:"source_binding"`
	Reproduction      NovelReproduction      `json:"reproduction"`
	AdjudicatedBy     string                 `json:"adjudicated_by"`
	AdjudicatedReason string                 `json:"adjudicated_reason"`
	AdjudicatedTS     string                 `json:"adjudicated_ts"`
	Observation       *NovelProofObservation `json:"observation,omitempty"`
}

// NovelProofBinding exposes an effective proof with the digest of its recording event.
type NovelProofBinding struct {
	Proof       NovelProof `json:"proof"`
	EventDigest string     `json:"event_digest"`
}

// rowBound reports whether the proof is bound to the admitted finding row.
func (p NovelProof) rowBound(f Finding) bool {
	return p.Fingerprint == f.Fingerprint && p.Location == f.Location
}

// versionedIdentity reports whether rule is a versioned identity such as "name@3".
func versionedIdentity(rule string) bool {
	at := strings.LastIndex(rule, "@")
	if at < 1 || at == len(rule)-1 {
		return false
	}
	for _, r := range rule[at+1:] {
		if r < '0' || r > '9' {
			return false
		}
	}
	return true
}

// validateNovelProof checks schema, identity, taxonomy, presence, evidence shape, human
// adjudication attribution, and reproduction coherence. Content truth is out of scope.
// Only novel-proof/2 exists: it requires the replay observation and cross-checks it
// against the proof's digest fields and the conclusive-replay refusal rules, so Append,
// Verify, and I9 reject caller-asserted, tampered, or inconclusive payloads too.
func validateNovelProof(proof NovelProof) error {
	if proof.Schema != NovelProofSchemaV2 {
		return fmt.Errorf("novel proof schema %q is not %q", proof.Schema, NovelProofSchemaV2)
	}
	if proof.Observation == nil {
		return fmt.Errorf("novel proof schema %q requires a replay observation", NovelProofSchemaV2)
	}
	if err := validateNovelProofBody(proof); err != nil {
		return err
	}
	return validateNovelObservationBinding(proof)
}

// validateNovelProofBody applies the schema-independent proof checks: identity, taxonomy,
// presence, evidence shape, adjudication attribution, and reproduction coherence.
func validateNovelProofBody(proof NovelProof) error {
	if strings.TrimSpace(proof.FindingID) == "" {
		return fmt.Errorf("novel proof finding id is required")
	}
	if len(proof.Fingerprint) != 64 {
		return fmt.Errorf("novel proof fingerprint %q is not a 64-hex digest", proof.Fingerprint)
	}
	if _, err := hex.DecodeString(proof.Fingerprint); err != nil {
		return fmt.Errorf("novel proof fingerprint %q is not a 64-hex digest", proof.Fingerprint)
	}
	if strings.TrimSpace(proof.Location) == "" {
		return fmt.Errorf("novel proof location is required")
	}
	if _, ok := ParseDomain(string(proof.Domain)); !ok {
		return fmt.Errorf("novel proof domain %q is not a taxonomy domain", proof.Domain)
	}
	if proof.Severity < Info || proof.Severity > Critical {
		return fmt.Errorf("novel proof severity %s is not a known valid severity", proof.Severity)
	}
	for _, field := range []struct{ name, value string }{
		{"issue_type", proof.IssueType}, {"expected_behavior", proof.ExpectedBehavior},
		{"failure_condition", proof.FailureCondition}, {"mechanism", proof.Mechanism},
		{"adjudicated_by", proof.AdjudicatedBy}, {"adjudicated_reason", proof.AdjudicatedReason},
		{"adjudicated_ts", proof.AdjudicatedTS},
	} {
		if strings.TrimSpace(field.value) == "" {
			return fmt.Errorf("novel proof %s is required", field.name)
		}
	}
	switch proof.ProofKind {
	case NovelProofSavedTest, NovelProofCommand, NovelProofConformance:
	default:
		return fmt.Errorf("novel proof kind %q is not saved_test, command, or conformance", proof.ProofKind)
	}
	if !versionedIdentity(proof.ProofRule) {
		return fmt.Errorf("novel proof rule %q is not a versioned identity", proof.ProofRule)
	}
	if len(proof.EvidenceDigests) == 0 {
		return fmt.Errorf("novel proof evidence digests are required")
	}
	for index, digest := range proof.EvidenceDigests {
		if !validSHA256Digest(digest) {
			return fmt.Errorf("novel proof evidence digest %q is not a sha256 digest", digest)
		}
		if index > 0 && digest <= proof.EvidenceDigests[index-1] {
			return fmt.Errorf("novel proof evidence digests must be sorted and unique")
		}
	}
	if !validSHA256Digest(proof.SourceBinding) {
		return fmt.Errorf("novel proof source binding %q is not a sha256 digest", proof.SourceBinding)
	}
	reproduction := proof.Reproduction
	if !validSHA256Digest(reproduction.ArtifactDigest) {
		return fmt.Errorf("novel proof artifact digest %q is not a sha256 digest", reproduction.ArtifactDigest)
	}
	if reproduction.Applies {
		if reproduction.Outcome != Reproduced {
			return fmt.Errorf("applicable novel proof outcome must be %q, got %q", Reproduced, reproduction.Outcome)
		}
		if reproduction.Attempts < 1 {
			return fmt.Errorf("applicable novel proof requires at least one attempt, got %d", reproduction.Attempts)
		}
		return nil
	}
	if reproduction.Outcome != NotApplicable {
		return fmt.Errorf("non-applicable novel proof outcome must be %q, got %q", NotApplicable, reproduction.Outcome)
	}
	if reproduction.Attempts != 0 {
		return fmt.Errorf("non-applicable novel proof cannot record attempts, got %d", reproduction.Attempts)
	}
	if proof.ProofKind != NovelProofConformance {
		return fmt.Errorf("non-applicable novel proof must use %s proof kind, got %q", NovelProofConformance, proof.ProofKind)
	}
	return nil
}

// validStatePreservingNovelProof reports whether the event is the narrow state-preserving
// novel-proof record: an unchanged NOVEL_CANDIDATE finding.
func validStatePreservingNovelProof(event Event) bool {
	return event.Kind == EventNovelProof && event.Entity == EntityFinding &&
		event.PreviousState == string(FindingNovelCandidate) &&
		event.NewState == string(FindingNovelCandidate)
}

// decodeNovelProof validates a novel-proof event with the confirmation-grade gate: exact
// states and instrument rule, strict decoding without unknown or trailing data, payload
// finding equal to the event id, semantic proof checks, and byte equality with the
// writer's canonical encoding (which also rejects duplicate keys and alternate order or
// whitespace). Append and Verify share this gate; consumption points re-run it to fail
// closed.
func decodeNovelProof(event Event) (NovelProof, error) {
	if !validStatePreservingNovelProof(event) {
		return NovelProof{}, fmt.Errorf("novel proof event must be a state-preserving %s event in %s, got entity %q %s -> %s", EntityFinding, FindingNovelCandidate, event.Entity, event.PreviousState, event.NewState)
	}
	if event.Adjudicator != NovelProofAdjudicator {
		return NovelProof{}, fmt.Errorf("novel proof adjudicator must be %q, got %q", NovelProofAdjudicator, event.Adjudicator)
	}
	if len(event.Payload) == 0 {
		return NovelProof{}, fmt.Errorf("novel proof payload is required")
	}
	var proof NovelProof
	decoder := json.NewDecoder(strings.NewReader(string(event.Payload)))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&proof); err != nil {
		return NovelProof{}, fmt.Errorf("decode novel proof payload: %w", err)
	}
	if err := decoder.Decode(&struct{}{}); err != io.EOF {
		return NovelProof{}, fmt.Errorf("novel proof payload has trailing JSON data")
	}
	if proof.FindingID == "" || proof.FindingID != event.ID {
		return NovelProof{}, fmt.Errorf("novel proof payload finding_id %q must equal event id %q", proof.FindingID, event.ID)
	}
	if err := validateNovelProof(proof); err != nil {
		return NovelProof{}, err
	}
	canonical, err := json.Marshal(proof)
	if err != nil {
		return NovelProof{}, fmt.Errorf("canonicalize novel proof payload: %w", err)
	}
	if string(canonical) != string(event.Payload) {
		return NovelProof{}, fmt.Errorf("novel proof payload is not the canonical encoding emitted by the writer")
	}
	return proof, nil
}

// novelConfirmPayload is the recorded body of a controlled novel confirmation event. By
// and Reason record the confirmer's declared identity and justification (human decision:
// separation of duties); identities are self-declared strings, not authenticated
// principals. No confirm event was ever released without them, so every decode requires
// the full set — there is no unreleased-history compatibility to preserve (the
// novel-proof/2 precedent).
type novelConfirmPayload struct {
	FindingID     string `json:"finding_id"`
	ProofHash     string `json:"proof_hash"`
	SourceBinding string `json:"source_binding"`
	By            string `json:"by"`
	Reason        string `json:"reason"`
}

// effectiveNovelProof returns the latest valid novel-proof event in the finding's current
// adjudication epoch. The epoch begins after the most recent reopen, so a proof recorded
// before a reopen can never confirm the reopened finding.
func effectiveNovelProof(events []Event, findingID string) (Event, NovelProof, bool) {
	epoch := 0
	for index, event := range events {
		if event.Entity == EntityFinding && event.ID == findingID && event.Kind == EventReopen {
			epoch = index + 1
		}
	}
	for index := len(events) - 1; index >= epoch; index-- {
		event := events[index]
		if event.Entity != EntityFinding || event.ID != findingID || event.Kind != EventNovelProof {
			continue
		}
		proof, err := decodeNovelProof(event)
		if err != nil {
			continue // a malformed proof never becomes effective
		}
		return event, proof, true
	}
	return Event{}, NovelProof{}, false
}

// decodeNovelConfirmation validates a controlled novel confirmation: the exact
// NOVEL_CANDIDATE -> CONFIRMED_NOVEL finding transition, the fixed instrument rule, a
// canonical payload bound to the event id, the required declared confirmer and reason
// (separation of duties records; identities are self-declared), and a reference to the
// hash and source binding of the finding's effective proof. prior holds the events
// preceding this one so Append and Verify apply the identical gate.
func decodeNovelConfirmation(prior []Event, event Event) (novelConfirmPayload, error) {
	if event.Kind != EventNovelConfirm || event.Entity != EntityFinding ||
		event.PreviousState != string(FindingNovelCandidate) || event.NewState != string(FindingConfirmedNovel) {
		return novelConfirmPayload{}, fmt.Errorf("novel confirm event must be a %s %s -> %s event, got kind %q %s -> %s", EntityFinding, FindingNovelCandidate, FindingConfirmedNovel, event.Kind, event.PreviousState, event.NewState)
	}
	if event.Adjudicator != NovelConfirmAdjudicator {
		return novelConfirmPayload{}, fmt.Errorf("novel confirm adjudicator must be %q, got %q", NovelConfirmAdjudicator, event.Adjudicator)
	}
	if len(event.Payload) == 0 {
		return novelConfirmPayload{}, fmt.Errorf("novel confirm payload is required")
	}
	var payload novelConfirmPayload
	decoder := json.NewDecoder(strings.NewReader(string(event.Payload)))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&payload); err != nil {
		return novelConfirmPayload{}, fmt.Errorf("decode novel confirm payload: %w", err)
	}
	if err := decoder.Decode(&struct{}{}); err != io.EOF {
		return novelConfirmPayload{}, fmt.Errorf("novel confirm payload has trailing JSON data")
	}
	if payload.FindingID == "" || payload.FindingID != event.ID {
		return novelConfirmPayload{}, fmt.Errorf("novel confirm payload finding_id %q must equal event id %q", payload.FindingID, event.ID)
	}
	if payload.ProofHash == "" {
		return novelConfirmPayload{}, fmt.Errorf("novel confirm payload proof_hash is required")
	}
	if strings.TrimSpace(payload.By) == "" {
		return novelConfirmPayload{}, fmt.Errorf("novel confirm payload by is required")
	}
	if strings.TrimSpace(payload.Reason) == "" {
		return novelConfirmPayload{}, fmt.Errorf("novel confirm payload reason is required")
	}
	proofEvent, proof, ok := effectiveNovelProof(prior, event.ID)
	if !ok {
		return novelConfirmPayload{}, fmt.Errorf("finding %q has no effective novel proof to confirm", event.ID)
	}
	if payload.ProofHash != proofEvent.Hash {
		return novelConfirmPayload{}, fmt.Errorf("novel confirm proof_hash %q does not reference the effective proof event %q", payload.ProofHash, proofEvent.Hash)
	}
	if payload.SourceBinding != proof.SourceBinding {
		return novelConfirmPayload{}, fmt.Errorf("novel confirm source binding %q does not match proof binding %q", payload.SourceBinding, proof.SourceBinding)
	}
	canonical, err := json.Marshal(payload)
	if err != nil {
		return novelConfirmPayload{}, fmt.Errorf("canonicalize novel confirm payload: %w", err)
	}
	if string(canonical) != string(event.Payload) {
		return novelConfirmPayload{}, fmt.Errorf("novel confirm payload is not the canonical encoding emitted by the writer")
	}
	return payload, nil
}

// novelProofPromotion returns the proof behind a finding's controlled promotion: the most
// recent event for the finding must be a valid EventNovelConfirm that still references
// the effective proof. Historical CONFIRMED_NOVEL findings decided directly never qualify.
func novelProofPromotion(events []Event, findingID string) (NovelProof, bool) {
	for index := len(events) - 1; index >= 0; index-- {
		event := events[index]
		if event.Entity != EntityFinding || event.ID != findingID {
			continue
		}
		if event.Kind != EventNovelConfirm {
			return NovelProof{}, false
		}
		if _, err := decodeNovelConfirmation(events[:index], event); err != nil {
			return NovelProof{}, false
		}
		_, proof, ok := effectiveNovelProof(events[:index], findingID)
		return proof, ok
	}
	return NovelProof{}, false
}

// RecordNovelProof appends a state-preserving novel-proof event for an existing
// NOVEL_CANDIDATE finding of an adjudicating case run, bound to the admitted row. Only
// novel-proof/2 proofs carrying their replay observation are writable; a novel-proof/1
// payload is refused by the shared validation before any mutation. It is a pure model
// write: no command executes, no runtime authority is granted, and a rejected proof
// never mutates the run.
func (cr *CaseRun) RecordNovelProof(proof NovelProof, ts string) error {
	if cr.caseRunState() != CaseRunAdjudicating {
		return fmt.Errorf("case run %q cannot record a novel proof in state %q", cr.Case, cr.caseRunState())
	}
	finding, ok := cr.finding(proof.FindingID)
	if !ok {
		return fmt.Errorf("unknown finding %q", proof.FindingID)
	}
	if cr.FindingState(finding.ID) != FindingNovelCandidate {
		return fmt.Errorf("finding %q is not a novel candidate", finding.ID)
	}
	if err := validateNovelProof(proof); err != nil {
		return err
	}
	if !proof.rowBound(*finding) {
		return fmt.Errorf("novel proof fingerprint %q or location %q does not match finding %q row", proof.Fingerprint, proof.Location, finding.ID)
	}
	payload, err := json.Marshal(proof)
	if err != nil {
		return fmt.Errorf("marshal novel proof: %w", err)
	}
	_, err = cr.Log.Append(Event{Entity: EntityFinding, ID: finding.ID, Kind: EventNovelProof, PreviousState: string(FindingNovelCandidate), NewState: string(FindingNovelCandidate), TS: ts, Adjudicator: NovelProofAdjudicator, Payload: payload})
	return err
}

// ConfirmNovel promotes a NOVEL_CANDIDATE finding to CONFIRMED_NOVEL through the
// dedicated event that references the exact effective proof. Absent, malformed, stale, or
// re-bound proofs are refused before any write; later CLI callers must additionally have
// verified independent runtime evidence (B2) before invoking this pure transition.
// Separation of duties (human decision): the declared confirmer must differ from the
// proof's adjudicator; both are recorded trimmed in the payload. Identities are
// self-declared strings, not authenticated principals — this refusal enforces the
// declaration, it proves nothing about who spoke.
func (cr *CaseRun) ConfirmNovel(findingID, by, reason, ts string) error {
	finding, ok := cr.finding(findingID)
	if !ok {
		return fmt.Errorf("unknown finding %q", findingID)
	}
	if cr.FindingState(findingID) != FindingNovelCandidate {
		return fmt.Errorf("finding %q is not a novel candidate", findingID)
	}
	proofEvent, proof, ok := effectiveNovelProof(cr.Log.Events, findingID)
	if !ok {
		return fmt.Errorf("finding %q has no effective novel proof to confirm", findingID)
	}
	if !proof.rowBound(*finding) {
		return fmt.Errorf("finding %q novel proof is stale: fingerprint or location no longer matches the admitted row", findingID)
	}
	by, reason = strings.TrimSpace(by), strings.TrimSpace(reason)
	if by == "" || reason == "" {
		return fmt.Errorf("finding %q confirmation requires a nonblank declared confirmer and reason", findingID)
	}
	if by == strings.TrimSpace(proof.AdjudicatedBy) {
		return fmt.Errorf("finding %q separation of duties: confirmer %q is the proof's adjudicated_by; the promotion must be declared by a different identity", findingID, by)
	}
	payload, err := json.Marshal(novelConfirmPayload{FindingID: findingID, ProofHash: proofEvent.Hash, SourceBinding: proof.SourceBinding, By: by, Reason: reason})
	if err != nil {
		return fmt.Errorf("marshal novel confirm: %w", err)
	}
	_, err = cr.Log.Append(Event{Entity: EntityFinding, ID: findingID, Kind: EventNovelConfirm, PreviousState: string(FindingNovelCandidate), NewState: string(FindingConfirmedNovel), TS: ts, Adjudicator: NovelConfirmAdjudicator, Payload: payload})
	return err
}

// NovelProofs returns the row-bound effective novel proofs keyed by finding id, each with
// the digest of the event that recorded it, for later backlog and binding checks.
func (cr CaseRun) NovelProofs() map[string]NovelProofBinding {
	bindings := make(map[string]NovelProofBinding)
	for _, finding := range cr.Findings {
		event, proof, ok := effectiveNovelProof(cr.Log.Events, finding.ID)
		if !ok || !proof.rowBound(finding) {
			continue
		}
		bindings[finding.ID] = NovelProofBinding{Proof: proof, EventDigest: event.Hash}
	}
	return bindings
}

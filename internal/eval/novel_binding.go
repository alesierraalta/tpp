package eval

import (
	"fmt"
	"sort"
	"strings"
	"unicode"

	"github.com/alesierraalta/tpp/internal/bench"
)

// The replay-side classifications a novel-proof/2 observation must show. bench is the
// only producer of observations; eval never imports a run in the other direction.
const (
	novelSubjectClassification = string(bench.NovelAssertionFailure)
	novelControlClassification = string(bench.NovelPassed)
	// novelSavedTestRulePrefix is the proof-rule family of the correctness saved-test
	// rule; novelProofRuleFor maps the known bench rule version to its single member
	// (e.g. "novel-saved-test@1").
	novelSavedTestRulePrefix = "novel-saved-test@"
	// novelSavedTestRuleV1 is the only proof rule novel-proof/2 accepts: the rule mapped
	// from bench.NovelCorrectnessRuleV1.
	novelSavedTestRuleV1 = novelSavedTestRulePrefix + "1"
)

// novelProofRuleFor is the single mapping from a known bench replay rule version to its
// novel-proof/2 proof rule: ProofFromReplay derives ProofRule from it instead of trusting
// a caller value, and stored-payload validation re-derives it.
func novelProofRuleFor(ruleVersion string) (string, error) {
	switch ruleVersion {
	case bench.NovelCorrectnessRuleV1:
		return novelSavedTestRuleV1, nil
	default:
		return "", fmt.Errorf("novel proof observation rule version %q is not a known bench rule", ruleVersion)
	}
}

// validNovelControlName adds the control-character refusal to the package's
// single-path-element name gate, so a recorded control name is a sane identity.
func validNovelControlName(name string) bool {
	return validBacklogCaseID(name) && !strings.ContainsFunc(name, unicode.IsControl)
}

// NovelProofFacts are the blind semantic facts behind a proof: what the finding means and
// who adjudicated it. They remain caller-supplied (never derived from the replay) and are
// required nonblank by proof validation. ProofRule is additionally refused unless it
// equals the rule derived from the replay's rule version.
type NovelProofFacts struct {
	Domain            Domain
	Severity          Severity
	IssueType         string
	ExpectedBehavior  string
	FailureCondition  string
	Mechanism         string
	ProofKind         string
	ProofRule         string
	AdjudicatedBy     string
	AdjudicatedReason string
	AdjudicatedTS     string
}

// ProofFromReplay is the pure constructor of a novel-proof/2 proof from an actual bench
// replay. It derives every digest field from the typed observation — SourceBinding is the
// staged subject source tree, EvidenceDigests the sorted test/output/control digests, the
// artifact the subject run output — plus Applies/Outcome and Attempts=1, derives
// ProofRule from the single rule mapping of the observation's rule version (refusing an
// unknown version or a mismatching caller rule), and refuses any inconclusive
// observation, malformed or missing digest, test digest mismatch, or blank blind semantic
// fact. It executes nothing and records nothing. Correctness-only: conformance or
// command facts are refused until their executable rules exist.
func ProofFromReplay(finding Finding, facts NovelProofFacts, replay bench.NovelReplayObservation) (NovelProof, error) {
	observation := novelObservationFromReplay(replay)
	expectedRule, err := novelProofRuleFor(observation.RuleVersion)
	if err != nil {
		return NovelProof{}, fmt.Errorf("novel proof from replay: %w", err)
	}
	if facts.ProofRule != expectedRule {
		return NovelProof{}, fmt.Errorf("novel proof from replay: novel proof/2 proof rule %q must be the saved-test rule %q derived from observation rule version %q", facts.ProofRule, expectedRule, observation.RuleVersion)
	}
	sourceBinding, evidence, artifact, err := novelDigestsFromObservation(observation)
	if err != nil {
		return NovelProof{}, err
	}
	proof := NovelProof{
		Schema:            NovelProofSchemaV2,
		FindingID:         finding.ID,
		Fingerprint:       finding.Fingerprint,
		Location:          finding.Location,
		Domain:            facts.Domain,
		Severity:          facts.Severity,
		IssueType:         facts.IssueType,
		ExpectedBehavior:  facts.ExpectedBehavior,
		FailureCondition:  facts.FailureCondition,
		Mechanism:         facts.Mechanism,
		ProofKind:         facts.ProofKind,
		ProofRule:         expectedRule,
		EvidenceDigests:   evidence,
		SourceBinding:     sourceBinding,
		Reproduction:      NovelReproduction{Applies: true, Outcome: Reproduced, ArtifactDigest: artifact, Attempts: 1},
		AdjudicatedBy:     facts.AdjudicatedBy,
		AdjudicatedReason: facts.AdjudicatedReason,
		AdjudicatedTS:     facts.AdjudicatedTS,
		Observation:       &observation,
	}
	if err := validateNovelProof(proof); err != nil {
		return NovelProof{}, fmt.Errorf("novel proof from replay: %w", err)
	}
	return proof, nil
}

// novelObservationFromReplay converts a bench replay observation into the digest-only
// event payload: bare hex digests become "sha256:"-prefixed, raw output is dropped.
func novelObservationFromReplay(replay bench.NovelReplayObservation) NovelProofObservation {
	return NovelProofObservation{
		RuleVersion:           replay.RuleVersion,
		ControlName:           replay.ControlName,
		TestSHA256:            prefixedDigest(replay.TestSHA256),
		Subject:               novelProcessFromReplay(replay.Subject),
		Control:               novelProcessFromReplay(replay.Control),
		SupportedReproduction: replay.SupportedReproduction,
		AdjudicationRequired:  replay.AdjudicationRequired,
	}
}

func novelProcessFromReplay(process bench.NovelProcessObservation) NovelObservationProcess {
	return NovelObservationProcess{
		Classification:  string(process.Classification),
		ExitCode:        process.ExitCode,
		Started:         process.Started,
		TimedOut:        process.TimedOut,
		Complete:        process.Complete,
		OutputTruncated: process.OutputTruncated,
		Reason:          process.Reason,
		OutputSHA256:    prefixedDigest(process.OutputSHA256),
		SourceSHA256:    prefixedDigest(process.SourceSHA256),
		ControlSHA256:   prefixedDigest(process.ControlSHA256),
		TestSHA256:      prefixedDigest(process.TestSHA256),
	}
}

// prefixedDigest converts one bare hex digest as bench emits it into the eval "sha256:"
// form. An absent digest stays absent (the required-digest gate refuses it), while junk
// or already-prefixed input becomes malformed here and is refused instead of accepted.
func prefixedDigest(digest string) string {
	if digest == "" {
		return ""
	}
	return "sha256:" + digest
}

// validateNovelObservation applies the conclusive-replay refusal rules to a stored or
// freshly converted observation: a known bench rule version, a sane control name, a
// supported subject assertion failure that started, completed, was not truncated or timed
// out, and carries no reason; a passed control; both reproduction gates; and well-formed,
// mutually consistent digests.
func validateNovelObservation(observation NovelProofObservation) error {
	if _, err := novelProofRuleFor(observation.RuleVersion); err != nil {
		return err
	}
	if !validNovelControlName(observation.ControlName) {
		return fmt.Errorf("novel proof observation control name %q must be a nonblank single name without path separators or control characters", observation.ControlName)
	}
	subject, control := observation.Subject, observation.Control
	if subject.Classification != novelSubjectClassification {
		return fmt.Errorf("novel proof observation subject classification %q is not %q", subject.Classification, novelSubjectClassification)
	}
	if subject.TimedOut {
		return fmt.Errorf("novel proof observation subject timed out")
	}
	if subject.OutputTruncated {
		return fmt.Errorf("novel proof observation subject output truncated")
	}
	if !subject.Complete {
		return fmt.Errorf("novel proof observation subject run is not complete")
	}
	if !subject.Started {
		return fmt.Errorf("novel proof observation subject run did not start")
	}
	if strings.TrimSpace(subject.Reason) != "" {
		return fmt.Errorf("novel proof observation subject reason %q is not empty", subject.Reason)
	}
	if control.Classification != novelControlClassification {
		return fmt.Errorf("novel proof observation control classification %q is not %q", control.Classification, novelControlClassification)
	}
	if control.TimedOut {
		return fmt.Errorf("novel proof observation control timed out")
	}
	if control.OutputTruncated {
		return fmt.Errorf("novel proof observation control output truncated")
	}
	if !control.Complete {
		return fmt.Errorf("novel proof observation control run is not complete")
	}
	if !control.Started {
		return fmt.Errorf("novel proof observation control run did not start")
	}
	if strings.TrimSpace(control.Reason) != "" {
		return fmt.Errorf("novel proof observation control reason %q is not empty", control.Reason)
	}
	if !observation.SupportedReproduction {
		return fmt.Errorf("novel proof observation does not support the reproduction")
	}
	if !observation.AdjudicationRequired {
		return fmt.Errorf("novel proof observation does not require adjudication")
	}
	for _, digest := range []struct{ name, value string }{
		{"test", observation.TestSHA256},
		{"subject output", subject.OutputSHA256},
		{"subject source", subject.SourceSHA256},
		{"subject test", subject.TestSHA256},
		{"control output", control.OutputSHA256},
		{"control source", control.SourceSHA256},
		{"control test", control.TestSHA256},
		{"control tree", control.ControlSHA256},
	} {
		if !validSHA256Digest(digest.value) {
			return fmt.Errorf("novel proof observation %s digest %q is not a sha256 digest", digest.name, digest.value)
		}
	}
	if subject.ControlSHA256 != "" {
		return fmt.Errorf("novel proof observation subject control digest %q must be empty: bench records the control tree digest on the control side only", subject.ControlSHA256)
	}
	if observation.TestSHA256 != subject.TestSHA256 {
		return fmt.Errorf("novel proof observation test digest %q does not match the subject test digest %q", observation.TestSHA256, subject.TestSHA256)
	}
	if control.TestSHA256 != subject.TestSHA256 {
		return fmt.Errorf("novel proof observation control test digest %q does not match the subject test digest %q", control.TestSHA256, subject.TestSHA256)
	}
	return nil
}

// novelDigestsFromObservation derives the proof's digest fields from a validated
// observation: SourceBinding is the staged subject source tree, EvidenceDigests the
// sorted unique test/output/control digests, and the artifact the subject run output.
func novelDigestsFromObservation(observation NovelProofObservation) (sourceBinding string, evidence []string, artifact string, err error) {
	sourceBinding = observation.Subject.SourceSHA256
	artifact = observation.Subject.OutputSHA256
	evidence = []string{
		observation.TestSHA256,
		observation.Subject.OutputSHA256,
		observation.Control.OutputSHA256,
		observation.Control.ControlSHA256,
	}
	sort.Strings(evidence)
	for index := 1; index < len(evidence); index++ {
		if evidence[index] == evidence[index-1] {
			return "", nil, "", fmt.Errorf("novel proof observation evidence digests are not unique: %q", evidence[index])
		}
	}
	return sourceBinding, evidence, artifact, nil
}

// validateNovelObservationBinding cross-checks the stored observation of a
// novel-proof/2 proof against the proof's correctness-only shape and digest fields after
// the refusal rules pass, so tampering with either side of the binding is rejected by
// decodeNovelProof itself — at Append, at Verify, and through I9 — not only by the
// constructor.
func validateNovelObservationBinding(proof NovelProof) error {
	// Correctness-only scope: /2 exists for executable saved-test replays. Conformance
	// and command proofs refuse until their executable rules exist, so no new one is legal.
	if proof.ProofKind != NovelProofSavedTest {
		return fmt.Errorf("novel proof/2 proof kind %q must be the correctness saved-test kind %q", proof.ProofKind, NovelProofSavedTest)
	}
	observation := *proof.Observation
	if err := validateNovelObservation(observation); err != nil {
		return err
	}
	expectedRule, err := novelProofRuleFor(observation.RuleVersion)
	if err != nil {
		return err
	}
	if proof.ProofRule != expectedRule {
		return fmt.Errorf("novel proof/2 proof rule %q does not match the rule %q derived from observation rule version %q", proof.ProofRule, expectedRule, observation.RuleVersion)
	}
	sourceBinding, evidence, artifact, err := novelDigestsFromObservation(observation)
	if err != nil {
		return err
	}
	expected := NovelReproduction{Applies: true, Outcome: Reproduced, ArtifactDigest: artifact, Attempts: 1}
	if proof.Reproduction != expected {
		return fmt.Errorf("novel proof/2 reproduction %+v does not match the observation-derived reproduction %+v", proof.Reproduction, expected)
	}
	if proof.SourceBinding != sourceBinding {
		return fmt.Errorf("novel proof/2 source binding %q does not match the observation subject source %q", proof.SourceBinding, sourceBinding)
	}
	if len(proof.EvidenceDigests) != len(evidence) {
		return fmt.Errorf("novel proof/2 evidence digests do not match the observation test/output/control digests")
	}
	for index := range evidence {
		if proof.EvidenceDigests[index] != evidence[index] {
			return fmt.Errorf("novel proof/2 evidence digests do not match the observation test/output/control digests")
		}
	}
	return nil
}

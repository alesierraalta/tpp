package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"os"
	"strings"
	"time"

	"github.com/alesierraalta/tpp/internal/eval"
	plan "github.com/alesierraalta/tpp/internal/plan"
)

// runBenchEvalConfirmNovel promotes a proven NOVEL_CANDIDATE finding to CONFIRMED_NOVEL
// and appends its backlog record under <bench-dir>/backlog/<case>.jsonl (spec 4.3),
// bound to the digest of the effective novel-proof event. Separation of duties (human
// decision): --by must be a declared identity different from the proof's adjudicated_by;
// both --by and --reason are recorded in the promotion payload. Identities are
// self-declared strings, not authenticated principals — the refusal enforces the
// declaration, it proves nothing about who spoke. No command executes here, so this
// command claims no sandbox or secret isolation — there is nothing to sandbox.
// Serialization: from before LoadRun until after SaveRun (or the refusal that ends the
// command), this command holds the exclusive run-dir lock plan.LockPlan takes — an
// advisory flock keyed on the run directory's canonical path, in the per-user cache
// directory, blocking, and released by the kernel if this process dies — so a concurrent
// prove-novel or confirm-novel waits instead of racing the write; a lock that cannot be
// taken refuses here, before any mutation, never proceeding unlocked.
// Crash order: the backlog record is appended FIRST (an identical repeat is a no-op), and
// only then the promotion event plus its single SaveRun. A crash between the two writes
// heals by rerunning this command: the identical record append is a no-op, then the
// promotion completes.
func runBenchEvalConfirmNovel(args []string) int {
	fs := evalFlagSet("bench eval confirm-novel")
	fs.Usage = func() {
		fmt.Fprintln(fs.Output(), "usage: tpp bench eval confirm-novel --eval <dir> --case <id> --finding <id> --by <who> --reason <why> [--bench-dir <dir>]")
		fmt.Fprintln(fs.Output(), "promotes a proven NOVEL_CANDIDATE finding to CONFIRMED_NOVEL and appends its record to <bench-dir>/backlog/<case>.jsonl, bound to the proof event digest.")
		fmt.Fprintln(fs.Output(), "separation of duties by declared identity only: --by must differ from the proof's adjudicated_by; identities are self-declared, not authenticated.")
		fs.PrintDefaults()
	}
	runDir := fs.String("eval", "", "evaluation run directory")
	caseID := fs.String("case", "", "case ID")
	findingID := fs.String("finding", "", "finding ID")
	by := fs.String("by", "", "confirmer (declared identity, not authenticated; must differ from the proof's adjudicated_by)")
	reason := fs.String("reason", "", "confirmation reason")
	benchDir := fs.String("bench-dir", "bench", "benchmark directory (backlog root)")
	if fs.Parse(args) != nil {
		return 2
	}
	if fs.NArg() != 0 {
		fmt.Fprintln(os.Stderr, "bench eval confirm-novel: takes no positional arguments")
		return 2
	}
	// The flag gate refuses before the run directory is read: a confirmation without a
	// declared confirmer and reason cannot exist (the payload requires both).
	var missing []string
	for _, required := range []struct{ name, value string }{
		{"--eval", *runDir}, {"--case", *caseID}, {"--finding", *findingID},
		{"--by", *by}, {"--reason", *reason},
	} {
		if strings.TrimSpace(required.value) == "" {
			missing = append(missing, required.name)
		}
	}
	if len(missing) > 0 {
		fmt.Fprintf(os.Stderr, "bench eval confirm-novel: requires %s (all flags must be nonblank)\n", strings.Join(missing, ", "))
		return 2
	}

	// One exclusive lock spans the whole transaction: everything from this LoadRun to the
	// SaveRun below (or the refusal that ends the command) runs under it, so a concurrent
	// prove-novel or confirm-novel queues instead of reading stale state. No lock, no run:
	// a lock that cannot be taken refuses here, before the run directory is read or mutated.
	lock, err := plan.LockPlan(*runDir)
	if err != nil {
		return evalCommandError("confirm-novel", fmt.Errorf("run directory lock: %w; refusing to run unlocked", err))
	}
	defer plan.UnlockPlan(lock)

	stored, err := eval.LoadRun(*runDir)
	if err != nil {
		return evalCommandError("confirm-novel", err)
	}
	// I9 first: a corrupt or tampered event chain invalidates every later claim, so it is
	// refused before any state or proof is read as trustworthy.
	for _, id := range sortedCaseIDs(stored.CaseRuns) {
		if err := stored.CaseRuns[id].Log.Verify(); err != nil {
			return evalCommandError("confirm-novel", fmt.Errorf("case %s event chain: %w", id, err))
		}
	}
	// The promotion claims a valid, complete, manifest-bound execution (spec 15) — the
	// same provenance gates prove-novel applies.
	if stored.Record.State != eval.RunAdjudicating {
		return evalCommandError("confirm-novel", fmt.Errorf("run is in state %q; the novelty promotion happens only while the run is %q", stored.Record.State, eval.RunAdjudicating))
	}
	if stored.Record.ManifestSHA256 == "" || stored.ManifestPath == "" {
		return evalCommandError("confirm-novel", fmt.Errorf("run is not bound to a stored sealed manifest; confirm-novel requires manifest-bound evidence"))
	}
	if stored.Record.AbortReason != "" {
		return evalCommandError("confirm-novel", fmt.Errorf("run provenance does not prove a valid and complete execution: %s", stored.Record.AbortReason))
	}
	if len(stored.Record.Leaks) > 0 {
		leak := stored.Record.Leaks[0]
		return evalCommandError("confirm-novel", fmt.Errorf("run records %d leak(s) (first: %s at %s:%d); leaked evidence cannot carry a promotion", len(stored.Record.Leaks), leak.Kind, leak.Source, leak.Line))
	}
	manifest, err := eval.LoadManifest(stored.ManifestPath)
	if err != nil {
		return evalCommandError("confirm-novel", err)
	}
	if manifest.ManifestSHA256 != stored.Record.ManifestSHA256 {
		return evalCommandError("confirm-novel", fmt.Errorf("stored run manifest digest %s does not match the sealed manifest %s", stored.Record.ManifestSHA256, manifest.ManifestSHA256))
	}
	if mismatches := eval.VerifyCases(*benchDir, manifest); len(mismatches) > 0 {
		return evalCommandError("confirm-novel", fmt.Errorf("benchmark cases do not match the sealed manifest: %s", strings.Join(mismatches, ", ")))
	}

	caseRun, ok := stored.CaseRuns[*caseID]
	if !ok {
		return evalCommandError("confirm-novel", fmt.Errorf("unknown case %q", *caseID))
	}
	var finding eval.Finding
	found := false
	for _, candidate := range caseRun.Findings {
		if candidate.ID == *findingID {
			finding, found = candidate, true
			break
		}
	}
	if !found {
		return evalCommandError("confirm-novel", fmt.Errorf("unknown finding %q in case %q", *findingID, *caseID))
	}
	state := caseRun.FindingState(finding.ID)
	if state != eval.FindingNovelCandidate && state != eval.FindingConfirmedNovel {
		return evalCommandError("confirm-novel", fmt.Errorf("finding %s is in state %s; confirm-novel promotes only NOVEL_CANDIDATE and repairs only CONFIRMED_NOVEL", finding.ID, state))
	}
	binding, ok := caseRun.NovelProofs()[finding.ID]
	if !ok {
		return evalCommandError("confirm-novel", fmt.Errorf("finding %s has no effective row-bound novel proof; record one with prove-novel first", finding.ID))
	}
	// Separation of duties (human decision): the confirmer must not be the proof's own
	// adjudicator. The comparison is on trimmed declared identity strings only — they are
	// self-declared, not authenticated.
	if strings.TrimSpace(*by) == strings.TrimSpace(binding.Proof.AdjudicatedBy) {
		return evalCommandError("confirm-novel", fmt.Errorf("separation of duties: --by %q is the proof's adjudicated_by; confirm-novel requires a different declared confirmer (identities are self-declared, not authenticated)", *by))
	}

	record, err := novelBacklogRecordFor(stored, caseRun, finding, binding)
	if err != nil {
		return evalCommandError("confirm-novel", err)
	}
	// Reconcile the backlog before anything is written: an identical record is present, a
	// record of an earlier promotion of this finding (its digest referenced by one of the
	// finding's recorded confirmations) is append-only history, and anything else is a
	// tampered or conflicting record that must refuse with both files untouched.
	benchRoot := absolutePath(*benchDir)
	matches, err := eval.NovelBacklogMatches(benchRoot, *caseID, record.RunID, record.FindingID)
	if err != nil {
		return evalCommandError("confirm-novel", err)
	}
	want, err := json.Marshal(record)
	if err != nil {
		return evalCommandError("confirm-novel", fmt.Errorf("canonicalize backlog record: %w", err))
	}
	confirmedProofHashes, err := novelConfirmationProofHashes(caseRun, finding.ID)
	if err != nil {
		return evalCommandError("confirm-novel", err)
	}
	present := false
	for _, existing := range matches {
		canonical, err := json.Marshal(existing)
		if err != nil {
			return evalCommandError("confirm-novel", fmt.Errorf("canonicalize existing backlog record: %w", err))
		}
		switch {
		case bytes.Equal(canonical, want):
			present = true
		case existing.ProofEventDigest != record.ProofEventDigest && confirmedProofHashes[existing.ProofEventDigest]:
			// a record of an earlier promotion of this finding: append-only history
		default:
			return evalCommandError("confirm-novel", fmt.Errorf("backlog record for finding %s does not match the effective proof (digest %s); refusing to mutate a tampered or stale backlog", finding.ID, existing.ProofEventDigest))
		}
	}

	if state == eval.FindingConfirmedNovel {
		if present {
			fmt.Printf("already confirmed: %s/%s (backlog record in place)\n", *caseID, *findingID)
			return 0
		}
		// The promotion is on disk but its backlog record is missing: repair the record
		// and leave the run byte-identical — this is the heal path for a record deleted
		// after a completed confirmation.
		if err := eval.AppendNovelBacklog(benchRoot, record); err != nil {
			return evalCommandError("confirm-novel", err)
		}
		fmt.Printf("already confirmed: repaired missing backlog record for %s/%s\n", *caseID, *findingID)
		return 0
	}

	// Crash-safe order: (1) the backlog record first — an identical repeat is a no-op and
	// a conflict refused above — then (2) the promotion event and its single SaveRun.
	if err := eval.AppendNovelBacklog(benchRoot, record); err != nil {
		return evalCommandError("confirm-novel", err)
	}
	ts := time.Now().UTC().Format(time.RFC3339Nano)
	if err := caseRun.ConfirmNovel(finding.ID, *by, *reason, ts); err != nil {
		// Unreachable after the preflight above; if it ever fires, the appended record is
		// identical on every rerun, so rerunning the same command heals the run.
		return evalCommandError("confirm-novel", err)
	}
	stored.CaseRuns[*caseID] = caseRun
	if err := eval.SaveRun(*runDir, stored); err != nil {
		return evalCommandError("confirm-novel", err)
	}
	fmt.Printf("confirmed novel %s/%s; backlog record appended\n", *caseID, *findingID)
	return 0
}

// novelBacklogRecordFor builds the append-only backlog record for one confirmed finding
// (spec 4.3): the finding row, the proof's digest-level evidence and adjudication
// attribution, the recorded reproduction, and the proposed Issue entry — all bound to the
// run and to the digest of the proof's recording event. It is pure: the same run, finding
// and proof always produce the same canonical record, so a rerun after a crash appends a
// byte-identical line (a no-op) instead of a duplicate.
func novelBacklogRecordFor(stored eval.StoredRun, caseRun eval.CaseRun, finding eval.Finding, binding eval.NovelProofBinding) (eval.NovelBacklogRecord, error) {
	proof := binding.Proof
	runID := fmt.Sprintf("run-%d", stored.K)
	if harness := strings.TrimSpace(stored.Record.HarnessID); harness != "" {
		runID = harness + "/" + runID
	}
	findingJSON, err := json.Marshal(finding)
	if err != nil {
		return eval.NovelBacklogRecord{}, fmt.Errorf("encode backlog finding: %w", err)
	}
	// The proof's observation is required by novel-proof/2 (decode guarantees it), so the
	// evidence always records which rule and control the replay ran against.
	evidence, err := json.Marshal(struct {
		ProofSchema       string   `json:"proof_schema"`
		ProofKind         string   `json:"proof_kind"`
		ProofRule         string   `json:"proof_rule"`
		EvidenceDigests   []string `json:"evidence_digests"`
		SourceBinding     string   `json:"source_binding"`
		AdjudicatedBy     string   `json:"adjudicated_by"`
		AdjudicatedReason string   `json:"adjudicated_reason"`
		AdjudicatedTS     string   `json:"adjudicated_ts"`
		RuleVersion       string   `json:"rule_version"`
		ControlName       string   `json:"control_name"`
	}{
		ProofSchema: proof.Schema, ProofKind: proof.ProofKind, ProofRule: proof.ProofRule,
		EvidenceDigests: proof.EvidenceDigests, SourceBinding: proof.SourceBinding,
		AdjudicatedBy: proof.AdjudicatedBy, AdjudicatedReason: proof.AdjudicatedReason,
		AdjudicatedTS: proof.AdjudicatedTS,
		RuleVersion:   proof.Observation.RuleVersion,
		ControlName:   proof.Observation.ControlName,
	})
	if err != nil {
		return eval.NovelBacklogRecord{}, fmt.Errorf("encode backlog evidence: %w", err)
	}
	reproduction, err := json.Marshal(proof.Reproduction)
	if err != nil {
		return eval.NovelBacklogRecord{}, fmt.Errorf("encode backlog reproduction: %w", err)
	}
	// The proposed Issue carries the proof's taxonomy and contract facts; the id is the
	// conventional placeholder a later benchmark version (spec 8.2) replaces with the
	// ground-truth Issue id when the finding is admitted.
	proposed, err := json.Marshal(struct {
		ID               string        `json:"id"`
		Domain           eval.Domain   `json:"domain"`
		Severity         eval.Severity `json:"severity"`
		IssueType        string        `json:"issue_type"`
		Description      string        `json:"description"`
		ExpectedBehavior string        `json:"expected_behavior"`
		FailureCondition string        `json:"failure_condition"`
		Mechanism        string        `json:"mechanism"`
	}{
		ID: "novel-" + finding.ID, Domain: proof.Domain, Severity: proof.Severity,
		IssueType: proof.IssueType, Description: finding.Description,
		ExpectedBehavior: proof.ExpectedBehavior, FailureCondition: proof.FailureCondition,
		Mechanism: proof.Mechanism,
	})
	if err != nil {
		return eval.NovelBacklogRecord{}, fmt.Errorf("encode backlog proposed issue: %w", err)
	}
	return eval.NovelBacklogRecord{
		RunID: runID, CaseID: caseRun.Case, FindingID: finding.ID,
		ProofEventDigest: binding.EventDigest,
		Finding:          findingJSON, Evidence: evidence,
		Reproduction: reproduction, ProposedIssue: proposed,
	}, nil
}

// novelConfirmationProofHashes returns the proof event digests the finding's recorded
// novel confirmations reference, so the backlog reconciliation can tell a record of an
// earlier promotion (legitimate append-only history after a reopen) from a tampered one.
// The event chain verified above already decoded every confirmation, so a payload that
// does not parse is unreachable.
func novelConfirmationProofHashes(caseRun eval.CaseRun, findingID string) (map[string]bool, error) {
	hashes := make(map[string]bool)
	for _, event := range caseRun.Log.Events {
		if event.Entity != eval.EntityFinding || event.ID != findingID || event.Kind != eval.EventNovelConfirm {
			continue
		}
		var payload struct {
			ProofHash string `json:"proof_hash"`
		}
		if err := json.Unmarshal(event.Payload, &payload); err != nil || payload.ProofHash == "" {
			return nil, fmt.Errorf("recorded novel confirmation does not carry its proof hash")
		}
		hashes[payload.ProofHash] = true
	}
	return hashes, nil
}

package eval

import (
	"bufio"
	"bytes"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

// NovelBacklogRecord is a minimal, run- and proof-event-bound record for an independently
// admitted novel finding. Its JSON payloads retain the relevant finding, evidence,
// reproduction, and proposed Issue facts without embedding a complete run. Supplying this
// record does not verify those facts; admission must be performed by a trusted caller.
type NovelBacklogRecord struct {
	RunID            string          `json:"run_id"`
	CaseID           string          `json:"case_id"`
	FindingID        string          `json:"finding_id"`
	ProofEventDigest string          `json:"proof_event_digest"`
	Finding          json.RawMessage `json:"finding"`
	Evidence         json.RawMessage `json:"evidence"`
	Reproduction     json.RawMessage `json:"reproduction"`
	ProposedIssue    json.RawMessage `json:"proposed_issue"`
}

func (r NovelBacklogRecord) validate() error {
	for name, value := range map[string]string{"run_id": r.RunID, "finding_id": r.FindingID} {
		if strings.TrimSpace(value) == "" {
			return fmt.Errorf("backlog %s is required", name)
		}
	}
	if !validBacklogCaseID(r.CaseID) {
		return fmt.Errorf("backlog case id %q is not a safe path component", r.CaseID)
	}
	if !validSHA256Digest(r.ProofEventDigest) {
		return fmt.Errorf("backlog proof event digest must be sha256")
	}
	for name, raw := range map[string]json.RawMessage{"finding": r.Finding, "evidence": r.Evidence, "reproduction": r.Reproduction, "proposed_issue": r.ProposedIssue} {
		if len(raw) == 0 || !json.Valid(raw) {
			return fmt.Errorf("backlog %s must be valid JSON", name)
		}
		var value map[string]json.RawMessage
		if err := json.Unmarshal(raw, &value); err != nil || value == nil {
			return fmt.Errorf("backlog %s must be a JSON object", name)
		}
		switch name {
		case "finding":
			if err := requireBacklogStrings(name, value, "id"); err != nil {
				return err
			}
			var findingID string
			if err := json.Unmarshal(value["id"], &findingID); err != nil {
				return fmt.Errorf("backlog finding id must be a JSON string")
			}
			if findingID != r.FindingID {
				return fmt.Errorf("backlog finding id %q does not match record finding id %q", findingID, r.FindingID)
			}
		case "evidence":
			if len(value) == 0 {
				return fmt.Errorf("backlog evidence object must not be empty")
			}
		case "reproduction":
			if err := requireBacklogFields(name, value, "applies"); err != nil {
				return err
			}
			var applies bool
			if err := json.Unmarshal(value["applies"], &applies); err != nil {
				return fmt.Errorf("backlog reproduction applies must be boolean")
			}
		case "proposed_issue":
			if err := requireBacklogStrings(name, value, "id", "domain", "severity", "issue_type"); err != nil {
				return err
			}
			var proposed struct {
				Domain   string `json:"domain"`
				Severity string `json:"severity"`
			}
			if err := json.Unmarshal(raw, &proposed); err != nil {
				return fmt.Errorf("backlog proposed_issue has invalid fields: %w", err)
			}
			if _, ok := ParseDomain(proposed.Domain); !ok {
				return fmt.Errorf("backlog proposed_issue domain %q is invalid", proposed.Domain)
			}
			if _, ok := ParseSeverity(proposed.Severity); !ok {
				return fmt.Errorf("backlog proposed_issue severity %q is invalid", proposed.Severity)
			}
		}

	}
	return nil
}

func requireBacklogStrings(name string, object map[string]json.RawMessage, fields ...string) error {
	for _, field := range fields {
		raw := object[field]
		var value string
		if len(raw) == 0 || json.Unmarshal(raw, &value) != nil || strings.TrimSpace(value) == "" {
			return fmt.Errorf("backlog %s field %q must be a nonblank JSON string", name, field)
		}
	}
	return nil
}

func requireBacklogFields(name string, object map[string]json.RawMessage, fields ...string) error {
	for _, field := range fields {
		if len(object[field]) == 0 || bytes.Equal(bytes.TrimSpace(object[field]), []byte("null")) {
			return fmt.Errorf("backlog %s field %q is required", name, field)
		}
	}
	return nil
}

func validBacklogCaseID(id string) bool {
	return id != "" && id != "." && id != ".." && filepath.Base(id) == id && !strings.ContainsAny(id, "/\\\x00")
}

// AppendNovelBacklog appends a canonical JSONL record to root/backlog/<case>.jsonl.
// A repeated run/case/finding/proof-event identity is a no-op only when its JSON content
// is identical. The caller owns proof verification. This API does not provide locking,
// cross-file atomicity, or protection against concurrent filesystem replacement.
func AppendNovelBacklog(root string, record NovelBacklogRecord) error {
	if err := record.validate(); err != nil {
		return err
	}
	line, err := json.Marshal(record)
	if err != nil {
		return fmt.Errorf("marshal backlog record: %w", err)
	}
	line = append(line, '\n')

	backlogDir := filepath.Join(root, "backlog")
	if err := ensureSafeBacklogDir(root, backlogDir); err != nil {
		return err
	}
	path := filepath.Join(backlogDir, record.CaseID+".jsonl")
	existing, err := readNovelBacklogFile(path, record.CaseID)
	if err != nil {
		return err
	}
	want := bytes.TrimSuffix(line, []byte{'\n'})
	found := false
	for _, prior := range existing {
		if !sameBacklogIdentity(prior, record) {
			continue
		}
		canonical, err := json.Marshal(prior)
		if err != nil || !bytes.Equal(canonical, want) {
			return fmt.Errorf("backlog identity conflicts with existing content")
		}
		found = true
	}
	if found {
		return nil
	}

	file, err := os.OpenFile(path, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o644)
	if err != nil {
		return fmt.Errorf("open backlog for append: %w", err)
	}
	defer file.Close()
	if _, err := file.Write(line); err != nil {
		return fmt.Errorf("append backlog record: %w", err)
	}
	return nil
}

// NovelBacklogMatches returns the records already stored in root/backlog/<caseID>.jsonl
// for runID/findingID, validating the file exactly as AppendNovelBacklog does (regular
// non-symlink file, complete final line, every record valid, canonical, and for this
// case). A backlog file that does not exist yet yields no records. The function only
// reads: how a missing, identical, stale, or conflicting record is reconciled is the
// caller's decision.
func NovelBacklogMatches(root, caseID, runID, findingID string) ([]NovelBacklogRecord, error) {
	if !validBacklogCaseID(caseID) {
		return nil, fmt.Errorf("backlog case id %q is not a safe path component", caseID)
	}
	records, err := readNovelBacklogFile(filepath.Join(root, "backlog", caseID+".jsonl"), caseID)
	if err != nil {
		return nil, err
	}
	var matches []NovelBacklogRecord
	for _, record := range records {
		if record.RunID == runID && record.FindingID == findingID {
			matches = append(matches, record)
		}
	}
	return matches, nil
}

// readNovelBacklogFile parses and validates every canonical record stored at path for
// caseID. A missing file yields no records; a non-regular file, an incomplete final line,
// a malformed or invalid record, a foreign case row, or non-canonical JSON is an error,
// so both the writer and the read-only lookup fail closed on the same gate.
func readNovelBacklogFile(path, caseID string) ([]NovelBacklogRecord, error) {
	info, err := os.Lstat(path)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, fmt.Errorf("inspect backlog: %w", err)
	}
	if info.Mode()&os.ModeSymlink != 0 || !info.Mode().IsRegular() {
		return nil, fmt.Errorf("backlog path %q is not a regular non-symlink file", path)
	}
	contents, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("read backlog: %w", err)
	}
	if len(contents) > 0 && contents[len(contents)-1] != '\n' {
		return nil, fmt.Errorf("backlog %q has an incomplete final line", path)
	}
	var records []NovelBacklogRecord
	scanner := bufio.NewScanner(bytes.NewReader(contents))
	scanner.Buffer(make([]byte, 4096), 16*1024*1024)
	for scanner.Scan() {
		var existing NovelBacklogRecord
		if err := json.Unmarshal(scanner.Bytes(), &existing); err != nil {
			return nil, fmt.Errorf("malformed backlog record: %w", err)
		}
		if err := existing.validate(); err != nil {
			return nil, fmt.Errorf("invalid existing backlog record: %w", err)
		}
		if existing.CaseID != caseID {
			return nil, fmt.Errorf("backlog record case %q does not match file case %q", existing.CaseID, caseID)
		}
		canonical, err := json.Marshal(existing)
		if err != nil || !bytes.Equal(canonical, scanner.Bytes()) {
			return nil, fmt.Errorf("existing backlog record is not canonical JSON")
		}
		records = append(records, existing)
	}
	if err := scanner.Err(); err != nil {
		return nil, fmt.Errorf("read backlog records: %w", err)
	}
	return records, nil
}

func sameBacklogIdentity(a, b NovelBacklogRecord) bool {
	return a.RunID == b.RunID && a.CaseID == b.CaseID && a.FindingID == b.FindingID && a.ProofEventDigest == b.ProofEventDigest
}

func ensureSafeBacklogDir(root, dir string) error {
	if root == "" || !filepath.IsAbs(root) {
		return fmt.Errorf("backlog root must be an absolute path")
	}
	root = filepath.Clean(root)
	for _, path := range []string{root, dir} {
		info, err := os.Lstat(path)
		if os.IsNotExist(err) {
			if path == root {
				return fmt.Errorf("backlog root %q does not exist", root)
			}
			if err := os.Mkdir(path, 0o755); err != nil {
				return fmt.Errorf("create backlog directory: %w", err)
			}
			continue
		}
		if err != nil {
			return fmt.Errorf("inspect backlog directory: %w", err)
		}
		if info.Mode()&os.ModeSymlink != 0 || !info.IsDir() {
			return fmt.Errorf("backlog directory %q is not a non-symlink directory", path)
		}
	}
	resolvedRoot, err := filepath.EvalSymlinks(root)
	if err != nil {
		return fmt.Errorf("resolve backlog root: %w", err)
	}
	resolvedDir, err := filepath.EvalSymlinks(dir)
	if err != nil {
		return fmt.Errorf("resolve backlog directory: %w", err)
	}
	if resolvedDir != resolvedRoot && !strings.HasPrefix(resolvedDir, resolvedRoot+string(filepath.Separator)) {
		return fmt.Errorf("backlog directory escapes root")
	}
	return nil
}

// ReconcileNovelBacklog verifies the spec 4.3 backlog evidence for a run, read-only: every
// finding the controlled workflow currently holds at CONFIRMED_NOVEL must have this run's
// backlog record bound to the proof event digest its effective confirmation references, and
// every record this run appended must be bound by a novel confirmation recorded in that
// case's log (earlier epochs included; other runs' records in the shared file are ignored).
// A missing record names its repair (rerun confirm-novel); an unknown, mis-bound, or
// malformed record is refused as tampering. It never writes: close refuses and leaves the
// decision to the operator (human decision: no auto-append). A legacy B0 CONFIRMED_NOVEL
// that a historical EventDecide set carries no confirmation and owes no record.
func ReconcileNovelBacklog(root string, stored StoredRun) error {
	// The harness-prefixed run identity a confirm-novel record is stamped with. Mirrors
	// novelBacklogRecordFor in cmd/tpp — the two must agree for records to match this run.
	runID := fmt.Sprintf("run-%d", stored.K)
	if harness := strings.TrimSpace(stored.Record.HarnessID); harness != "" {
		runID = harness + "/" + runID
	}
	caseIDs := make([]string, 0, len(stored.CaseRuns))
	for caseID := range stored.CaseRuns {
		caseIDs = append(caseIDs, caseID)
	}
	sort.Strings(caseIDs)
	for _, caseID := range caseIDs {
		caseRun := stored.CaseRuns[caseID]
		if !validBacklogCaseID(caseID) {
			return fmt.Errorf("case %q is not a safe backlog path component", caseID)
		}
		records, err := readNovelBacklogFile(filepath.Join(root, "backlog", caseID+".jsonl"), caseID)
		if err != nil {
			return fmt.Errorf("case %s backlog: %w", caseID, err)
		}
		bound, err := novelConfirmBindings(caseRun.Log.Events)
		if err != nil {
			return fmt.Errorf("case %s: %w", caseID, err)
		}
		// Tampering first: every record this run appended must reference a proof event
		// digest some recorded novel confirmation in this case's log bound — any epoch —
		// and claim the finding that confirmation belongs to. A record bound to nothing,
		// or to another finding, is not stale history but a forgery.
		for _, record := range records {
			if record.RunID != runID {
				continue // another run's record in the shared case file (backlog is shared across runs)
			}
			findingID, ok := bound[record.ProofEventDigest]
			if !ok {
				return fmt.Errorf("case %s: backlog record for finding %s references proof event digest %s that no recorded novel confirmation bound; refusing to seal a tampered backlog", caseID, record.FindingID, record.ProofEventDigest)
			}
			if findingID != record.FindingID {
				return fmt.Errorf("case %s: backlog record claims finding %s for proof event digest %s that finding %s confirmed; refusing to seal a tampered backlog", caseID, record.FindingID, record.ProofEventDigest, findingID)
			}
		}
		// Completeness: every finding currently CONFIRMED_NOVEL through a novel confirmation
		// must have this run's record for the digest that confirmation binds. A legacy decide-
		// promoted CONFIRMED_NOVEL owes nothing (no confirmation, no proof, no record).
		for _, finding := range caseRun.Findings {
			if caseRun.FindingState(finding.ID) != FindingConfirmedNovel {
				continue
			}
			setter, setterIdx, ok := latestConfirmedSetter(caseRun.Log.Events, finding.ID)
			if !ok {
				return fmt.Errorf("case %s finding %s: no recorded event sets the finding to %s", caseID, finding.ID, FindingConfirmedNovel)
			}
			if setter.Kind == EventDecide {
				continue // legacy B0 direct CONFIRMED_NOVEL: no confirmation, no record required
			}
			if setter.Kind != EventNovelConfirm {
				return fmt.Errorf("case %s finding %s: unexpected %s event sets the finding to %s", caseID, finding.ID, setter.Kind, FindingConfirmedNovel)
			}
			payload, err := decodeNovelConfirmation(caseRun.Log.Events[:setterIdx], setter)
			if err != nil {
				return fmt.Errorf("case %s finding %s: recorded novel confirmation does not verify: %w", caseID, finding.ID, err)
			}
			if !hasBacklogRecord(records, runID, finding.ID, payload.ProofHash) {
				return fmt.Errorf("case %s finding %s: CONFIRMED_NOVEL promotion has no backlog record for proof event digest %s; rerun `tpp bench eval confirm-novel` to repair the backlog record", caseID, finding.ID, payload.ProofHash)
			}
		}
	}
	return nil
}

// novelConfirmBindings maps every proof event digest a recorded novel confirmation in the
// log bound — current or earlier epoch, the backlog keeps stale records as history — to
// the finding that confirmed it. A confirmation that does not decode, or two findings
// bound to one digest, is a broken or forged log and fails closed.
func novelConfirmBindings(events []Event) (map[string]string, error) {
	bindings := make(map[string]string)
	for index, event := range events {
		if event.Kind != EventNovelConfirm {
			continue
		}
		payload, err := decodeNovelConfirmation(events[:index], event)
		if err != nil {
			return nil, fmt.Errorf("recorded novel confirmation does not verify: %w", err)
		}
		if prior, exists := bindings[payload.ProofHash]; exists && prior != event.ID {
			return nil, fmt.Errorf("proof event digest %s confirms both finding %s and finding %s", payload.ProofHash, prior, event.ID)
		}
		bindings[payload.ProofHash] = event.ID
	}
	return bindings, nil
}

// latestConfirmedSetter returns the last state-changing event that set the finding to
// CONFIRMED_NOVEL, with its index in the log. State-preserving events (a re-decision) are
// skipped so the transition itself is returned; its kind decides whether the promotion
// was a controlled novel confirmation or a historical direct decide.
func latestConfirmedSetter(events []Event, findingID string) (Event, int, bool) {
	for index := len(events) - 1; index >= 0; index-- {
		event := events[index]
		if event.Entity == EntityFinding && event.ID == findingID &&
			event.PreviousState != event.NewState && event.NewState == string(FindingConfirmedNovel) {
			return event, index, true
		}
	}
	return Event{}, 0, false
}

// hasBacklogRecord reports whether this run appended the record tying the finding's
// promotion to the proof event digest its confirmation references.
func hasBacklogRecord(records []NovelBacklogRecord, runID, findingID, proofDigest string) bool {
	for _, record := range records {
		if record.RunID == runID && record.FindingID == findingID && record.ProofEventDigest == proofDigest {
			return true
		}
	}
	return false
}

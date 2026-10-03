package eval

import (
	"bufio"
	"bytes"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
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
	info, err := os.Lstat(path)
	if err == nil {
		if info.Mode()&os.ModeSymlink != 0 || !info.Mode().IsRegular() {
			return fmt.Errorf("backlog path %q is not a regular non-symlink file", path)
		}
		contents, readErr := os.ReadFile(path)
		if readErr != nil {
			return fmt.Errorf("read backlog: %w", readErr)
		}
		if len(contents) > 0 && contents[len(contents)-1] != '\n' {
			return fmt.Errorf("backlog %q has an incomplete final line", path)
		}
		found := false
		scanner := bufio.NewScanner(bytes.NewReader(contents))
		scanner.Buffer(make([]byte, 4096), 16*1024*1024)
		for scanner.Scan() {
			var existing NovelBacklogRecord
			if err := json.Unmarshal(scanner.Bytes(), &existing); err != nil {
				return fmt.Errorf("malformed backlog record: %w", err)
			}
			if err := existing.validate(); err != nil {
				return fmt.Errorf("invalid existing backlog record: %w", err)
			}
			if existing.CaseID != record.CaseID {
				return fmt.Errorf("backlog record case %q does not match file case %q", existing.CaseID, record.CaseID)
			}
			canonical, err := json.Marshal(existing)
			if err != nil || !bytes.Equal(canonical, scanner.Bytes()) {
				return fmt.Errorf("existing backlog record is not canonical JSON")
			}
			if sameBacklogIdentity(existing, record) {
				if !bytes.Equal(scanner.Bytes(), bytes.TrimSuffix(line, []byte{'\n'})) {
					return fmt.Errorf("backlog identity conflicts with existing content")
				}
				found = true
			}
		}
		if err := scanner.Err(); err != nil {
			return fmt.Errorf("read backlog records: %w", err)
		}
		if found {
			return nil
		}
	} else if !os.IsNotExist(err) {
		return fmt.Errorf("inspect backlog: %w", err)
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

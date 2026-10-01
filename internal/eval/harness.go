package eval

import (
	"bufio"
	"bytes"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"sort"
)

// HarnessComponents are the immutable components that determine an evaluation harness identity.
type HarnessComponents struct {
	SkillsDigest             string   `json:"skills_digest"`
	OrchestratorAssetsDigest string   `json:"orchestrator_assets_digest"`
	Runner                   string   `json:"runner"`
	AgentConfigMode          string   `json:"agent_config_mode"`
	DisabledComponents       []string `json:"disabled_components"`
	ToolAllowlist            []string `json:"tool_allowlist"`
}

// HarnessID hashes canonical component JSON, independent of input ordering in set-like lists.
func HarnessID(components HarnessComponents) string {
	components = normalizedHarnessComponents(components)
	data, err := CanonicalJSON(components)
	if err != nil {
		return ""
	}
	sum := sha256.Sum256(data)
	return sha256Digest(sum[:])
}

// HarnessRecord binds a human label to one immutable harness ID.
type HarnessRecord struct {
	Label      string            `json:"label"`
	ID         string            `json:"id"`
	Components HarnessComponents `json:"components"`
	Registered string            `json:"registered"`
}

// AppendHarness appends a registration unless its label already names a different harness.
func AppendHarness(path string, record HarnessRecord) error {
	if record.Label == "" {
		return errors.New("harness label is empty")
	}
	record.Components = normalizedHarnessComponents(record.Components)
	if record.ID == "" || record.ID != HarnessID(record.Components) {
		return fmt.Errorf("harness %q ID does not match its components", record.Label)
	}
	entries, err := readHarnesses(path)
	if err != nil {
		return err
	}
	for _, existing := range entries {
		if existing.Label == record.Label && existing.ID != record.ID {
			return fmt.Errorf("harness label %q is already bound to %s", record.Label, existing.ID)
		}
	}
	data, err := json.Marshal(record)
	if err != nil {
		return fmt.Errorf("encode harness record: %w", err)
	}
	file, err := os.OpenFile(path, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o644)
	if err != nil {
		return fmt.Errorf("append harness registry %s: %w", path, err)
	}
	defer file.Close()
	if _, err := file.Write(append(data, '\n')); err != nil {
		return fmt.Errorf("append harness registry %s: %w", path, err)
	}
	return nil
}

// LookupHarness returns the most recently registered record for label.
func LookupHarness(path, label string) (HarnessRecord, bool, error) {
	entries, err := readHarnesses(path)
	if err != nil {
		return HarnessRecord{}, false, err
	}
	var found HarnessRecord
	ok := false
	for _, record := range entries {
		if record.Label == label {
			found, ok = record, true
		}
	}
	return found, ok, nil
}

func readHarnesses(path string) ([]HarnessRecord, error) {
	file, err := os.Open(path)
	if errors.Is(err, os.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("read harness registry %s: %w", path, err)
	}
	defer file.Close()
	scanner := bufio.NewScanner(file)
	scanner.Buffer(make([]byte, 4096), 1024*1024)
	var records []HarnessRecord
	for line := 1; scanner.Scan(); line++ {
		var record HarnessRecord
		if err := json.Unmarshal(bytes.TrimSpace(scanner.Bytes()), &record); err != nil {
			return nil, fmt.Errorf("parse harness registry %s line %d: %w", path, line, err)
		}
		records = append(records, record)
	}
	if err := scanner.Err(); err != nil {
		return nil, fmt.Errorf("read harness registry %s: %w", path, err)
	}
	return records, nil
}

func normalizedHarnessComponents(components HarnessComponents) HarnessComponents {
	components.DisabledComponents = append([]string(nil), components.DisabledComponents...)
	components.ToolAllowlist = append([]string(nil), components.ToolAllowlist...)
	sort.Strings(components.DisabledComponents)
	sort.Strings(components.ToolAllowlist)
	return components
}

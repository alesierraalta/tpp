package eval

import (
	"bufio"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"reflect"
	"sort"
)

// StoredRun is a benchmark run record together with its per-case adjudication ledgers.
type StoredRun struct {
	K                int
	HarnessLabel     string
	SourceResultsDir string
	ManifestPath     string
	PolicyPath       string
	WeightedRecallW  float64
	Record           RunRecord
	CaseRuns         map[string]CaseRun
	CaseOutcomes     map[string]string
	CaseControls     map[string]bool
	CaseResources    map[string]CaseRunResources
}

// CaseRunResources are the harness costs recorded with an individual runner result.
type CaseRunResources struct {
	CostUSD      float64 `json:"cost_usd"`
	AgentSeconds float64 `json:"agent_seconds"`
	Tokens       int     `json:"tokens"`
}

// ReconstructCompletedRun derives metric-ready data from the sealed manifest and case ledgers.
func ReconstructCompletedRun(stored StoredRun, manifest Manifest) (RunData, error) {
	if stored.Record.State != RunCompleted {
		return RunData{}, fmt.Errorf("run is not completed")
	}
	if manifest.ManifestSHA256 == "" || stored.Record.ManifestSHA256 != manifest.ManifestSHA256 {
		return RunData{}, fmt.Errorf("completed run manifest digest does not match its recorded manifest")
	}
	if stored.WeightedRecallW != manifest.MetricConfig.WeightedRecallW {
		return RunData{}, fmt.Errorf("completed run metric configuration does not match its recorded manifest")
	}

	expected := make(map[string]struct{}, len(manifest.Cases))
	caseIDs := make([]string, 0, len(manifest.Cases))
	for _, manifestCase := range manifest.Cases {
		if _, exists := expected[manifestCase.ID]; exists {
			return RunData{}, fmt.Errorf("recorded manifest repeats case %q", manifestCase.ID)
		}
		expected[manifestCase.ID] = struct{}{}
		caseIDs = append(caseIDs, manifestCase.ID)
	}
	if len(expected) == 0 {
		return RunData{}, fmt.Errorf("completed run manifest contains no cases")
	}
	sort.Strings(caseIDs)
	checkKeys := func(name string, count int, has func(string) bool) error {
		if count != len(expected) {
			return fmt.Errorf("completed run has %d %s entries for %d manifest cases", count, name, len(expected))
		}
		for caseID := range expected {
			if !has(caseID) {
				return fmt.Errorf("completed run is missing %s for case %q", name, caseID)
			}
		}
		return nil
	}
	if err := checkKeys("case ledgers", len(stored.CaseRuns), func(id string) bool { _, ok := stored.CaseRuns[id]; return ok }); err != nil {
		return RunData{}, err
	}
	if err := checkKeys("case outcomes", len(stored.CaseOutcomes), func(id string) bool { _, ok := stored.CaseOutcomes[id]; return ok }); err != nil {
		return RunData{}, err
	}
	if err := checkKeys("case controls", len(stored.CaseControls), func(id string) bool { _, ok := stored.CaseControls[id]; return ok }); err != nil {
		return RunData{}, err
	}
	if err := checkKeys("case resources", len(stored.CaseResources), func(id string) bool { _, ok := stored.CaseResources[id]; return ok }); err != nil {
		return RunData{}, err
	}

	data := RunData{ID: fmt.Sprintf("run-%d", stored.K), Cases: make([]CaseResult, 0, len(caseIDs))}
	for _, caseID := range caseIDs {
		caseRun := stored.CaseRuns[caseID]
		if caseRun.Case != caseID || caseRun.caseRunState() != CaseRunClosed {
			return RunData{}, fmt.Errorf("completed run case %q ledger is not closed", caseID)
		}
		if violations := CheckCaseRun(&caseRun); len(violations) > 0 {
			return RunData{}, fmt.Errorf("completed run case %q ledger invariant %s: %s", caseID, violations[0].ID, violations[0].Detail)
		}
		result, err := CaseResultFrom(&caseRun, stored.CaseControls[caseID], ReproFromEvents(caseRun))
		if err != nil {
			return RunData{}, fmt.Errorf("reconstruct completed run case %q: %w", caseID, err)
		}
		result.Outcome = stored.CaseOutcomes[caseID]
		resources := stored.CaseResources[caseID]
		result.CostUSD = resources.CostUSD
		result.AgentSeconds = resources.AgentSeconds
		result.Tokens = resources.Tokens
		data.Cases = append(data.Cases, result)
	}
	if !reflect.DeepEqual(stored.Record.Data, data) {
		return RunData{}, fmt.Errorf("completed run data does not match case ledger derivation")
	}
	return data, nil
}

// ReproFromEvents derives reproduction outcomes from the verified adjudication events.
func ReproFromEvents(caseRun CaseRun) map[string]ReproOutcome {
	decisions := make(map[string]Decision)
	for _, event := range caseRun.Log.Events {
		if event.Entity != EntityFinding || event.Kind != EventDecide {
			continue
		}
		var decision Decision
		if json.Unmarshal(event.Payload, &decision) == nil {
			decisions[decision.FindingID] = decision
		}
	}
	outcomes := make(map[string]ReproOutcome)
	for _, finding := range caseRun.Findings {
		decision, ok := decisions[finding.ID]
		if !ok || decision.CandidateIssue == "" {
			continue
		}
		facts, ok := finding.Computed[decision.CandidateIssue]
		if !ok {
			continue
		}
		switch facts.C5 {
		case FactTrue:
			outcomes[finding.ID] = Reproduced
		case FactFalse:
			outcomes[finding.ID] = NotReproduced
		case FactNA:
			outcomes[finding.ID] = NotApplicable
		default:
			outcomes[finding.ID] = NotRun
		}
	}
	return outcomes
}

type runFile struct {
	K                   int                         `json:"k"`
	State               RunState                    `json:"state"`
	ManifestSHA256      string                      `json:"manifest_sha256"`
	PolicySHA256        string                      `json:"policy_sha256"`
	HarnessID           string                      `json:"harness_id"`
	HarnessLabel        string                      `json:"harness_label"`
	Model               string                      `json:"model"`
	Runner              string                      `json:"runner"`
	Environment         string                      `json:"environment"`
	Leaks               []Leak                      `json:"leaks"`
	AbortReason         string                      `json:"abort_reason"`
	SourceResultsDir    string                      `json:"source_results_dir"`
	ManifestPath        string                      `json:"manifest_path,omitempty"`
	PolicyPath          string                      `json:"policy_path,omitempty"`
	WeightedRecallW     float64                     `json:"weighted_recall_w"`
	CaseOutcomes        map[string]string           `json:"case_outcomes,omitempty"`
	CaseControls        map[string]bool             `json:"case_controls,omitempty"`
	CaseResources       map[string]CaseRunResources `json:"case_resources,omitempty"`
	InvariantViolations []string                    `json:"invariant_violations,omitempty"`
	Data                RunData                     `json:"data"`
}

// SaveRun atomically persists run metadata, case ledgers, and each case's append-only events.
func SaveRun(runDir string, stored StoredRun) error {
	if stored.K < 1 {
		return fmt.Errorf("run k must be positive, got %d", stored.K)
	}
	if err := os.MkdirAll(runDir, 0o755); err != nil {
		return fmt.Errorf("create run directory %s: %w", runDir, err)
	}
	metadata := runFile{
		K: stored.K, State: stored.Record.State, ManifestSHA256: stored.Record.ManifestSHA256,
		PolicySHA256: stored.Record.PolicySHA256, HarnessID: stored.Record.HarnessID,
		HarnessLabel: stored.HarnessLabel, Model: stored.Record.Model, Runner: stored.Record.Runner,
		Environment: stored.Record.Environment, Leaks: stored.Record.Leaks,
		AbortReason: stored.Record.AbortReason, SourceResultsDir: stored.SourceResultsDir,
		ManifestPath: stored.ManifestPath, PolicyPath: stored.PolicyPath,
		WeightedRecallW: stored.WeightedRecallW,
		CaseOutcomes:    stored.CaseOutcomes, CaseControls: stored.CaseControls,
		CaseResources:       stored.CaseResources,
		InvariantViolations: stored.Record.InvariantViolations, Data: stored.Record.Data,
	}
	data, err := json.MarshalIndent(metadata, "", "  ")
	if err != nil {
		return fmt.Errorf("encode run metadata: %w", err)
	}
	if err := writeAtomic(filepath.Join(runDir, "run.json"), append(data, '\n'), 0o644); err != nil {
		return err
	}
	for name, caseRun := range stored.CaseRuns {
		if name == "" || filepath.Base(name) != name || name == "." || name == ".." {
			return fmt.Errorf("invalid case run directory name %q", name)
		}
		caseDir := filepath.Join(runDir, name)
		if err := os.MkdirAll(caseDir, 0o755); err != nil {
			return fmt.Errorf("create case directory %s: %w", caseDir, err)
		}
		persisted := caseRun
		persisted.Log = Log{}
		caseData, err := json.MarshalIndent(persisted, "", "  ")
		if err != nil {
			return fmt.Errorf("encode case run %s: %w", name, err)
		}
		if err := writeAtomic(filepath.Join(caseDir, "caserun.json"), append(caseData, '\n'), 0o644); err != nil {
			return err
		}
		var events []byte
		for _, event := range caseRun.Log.Events {
			line, err := json.Marshal(event)
			if err != nil {
				return fmt.Errorf("encode event for case %s: %w", name, err)
			}
			events = append(events, line...)
			events = append(events, '\n')
		}
		if err := writeAtomic(filepath.Join(caseDir, "events.jsonl"), events, 0o644); err != nil {
			return err
		}
	}
	return nil
}

// LoadRun restores run metadata and per-case event logs from a run directory.
func LoadRun(runDir string) (StoredRun, error) {
	data, err := os.ReadFile(filepath.Join(runDir, "run.json"))
	if err != nil {
		return StoredRun{}, fmt.Errorf("read run metadata: %w", err)
	}
	var metadata runFile
	if err := json.Unmarshal(data, &metadata); err != nil {
		return StoredRun{}, fmt.Errorf("parse run metadata: %w", err)
	}
	stored := StoredRun{
		K: metadata.K, HarnessLabel: metadata.HarnessLabel, SourceResultsDir: metadata.SourceResultsDir,
		ManifestPath: metadata.ManifestPath, PolicyPath: metadata.PolicyPath,
		WeightedRecallW: metadata.WeightedRecallW,
		CaseOutcomes:    metadata.CaseOutcomes, CaseControls: metadata.CaseControls,
		CaseResources: metadata.CaseResources,
		Record: RunRecord{
			Data: metadata.Data, State: metadata.State, ManifestSHA256: metadata.ManifestSHA256,
			PolicySHA256: metadata.PolicySHA256, HarnessID: metadata.HarnessID,
			Model: metadata.Model, Runner: metadata.Runner, Environment: metadata.Environment,
			Leaks: metadata.Leaks, AbortReason: metadata.AbortReason,
			InvariantViolations: metadata.InvariantViolations,
		},
		CaseRuns: make(map[string]CaseRun),
	}
	entries, err := os.ReadDir(runDir)
	if err != nil {
		return StoredRun{}, fmt.Errorf("read run directory: %w", err)
	}
	for _, entry := range entries {
		if !entry.IsDir() {
			continue
		}
		caseDir := filepath.Join(runDir, entry.Name())
		caseData, err := os.ReadFile(filepath.Join(caseDir, "caserun.json"))
		if err != nil {
			return StoredRun{}, fmt.Errorf("read case run %s: %w", entry.Name(), err)
		}
		var caseRun CaseRun
		if err := json.Unmarshal(caseData, &caseRun); err != nil {
			return StoredRun{}, fmt.Errorf("parse case run %s: %w", entry.Name(), err)
		}
		if caseRun.Case != entry.Name() {
			return StoredRun{}, fmt.Errorf("case directory %q contains case run %q", entry.Name(), caseRun.Case)
		}
		if err := readEvents(filepath.Join(caseDir, "events.jsonl"), &caseRun.Log); err != nil {
			return StoredRun{}, fmt.Errorf("read events for case %s: %w", entry.Name(), err)
		}
		stored.CaseRuns[entry.Name()] = caseRun
	}
	return stored, nil
}

// Digest returns the sha256 digest of a file using the instrument's sha256: prefix.
func Digest(path string) (string, error) {
	file, err := os.Open(path)
	if err != nil {
		return "", fmt.Errorf("open %s for digest: %w", path, err)
	}
	defer file.Close()
	hash := sha256.New()
	if _, err := io.Copy(hash, file); err != nil {
		return "", fmt.Errorf("digest %s: %w", path, err)
	}
	return "sha256:" + hex.EncodeToString(hash.Sum(nil)), nil
}

func readEvents(path string, log *Log) error {
	file, err := os.Open(path)
	if err != nil {
		return err
	}
	defer file.Close()
	scanner := bufio.NewScanner(file)
	scanner.Buffer(make([]byte, 4096), 8*1024*1024)
	for scanner.Scan() {
		var event Event
		if err := json.Unmarshal(scanner.Bytes(), &event); err != nil {
			return fmt.Errorf("parse event: %w", err)
		}
		log.Events = append(log.Events, event)
	}
	return scanner.Err()
}

func writeAtomic(path string, data []byte, mode os.FileMode) error {
	dir := filepath.Dir(path)
	temp, err := os.CreateTemp(dir, ".atomic-*")
	if err != nil {
		return fmt.Errorf("create temporary file for %s: %w", path, err)
	}
	if err := temp.Chmod(mode); err != nil {
		temp.Close()
		return fmt.Errorf("set permissions for %s: %w", path, err)
	}
	if _, err := temp.Write(data); err != nil {
		temp.Close()
		return fmt.Errorf("write temporary file for %s: %w", path, err)
	}
	if err := temp.Sync(); err != nil {
		temp.Close()
		return fmt.Errorf("sync temporary file for %s: %w", path, err)
	}
	if err := temp.Close(); err != nil {
		return fmt.Errorf("close temporary file for %s: %w", path, err)
	}
	if err := os.Rename(temp.Name(), path); err != nil {
		return fmt.Errorf("replace %s: %w", path, err)
	}
	return nil
}

package eval

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/alesierraalta/tpp/internal/bench"
)

// MetricConfig freezes the metric and bootstrap choices for a benchmark version.
type MetricConfig struct {
	WeightedRecallW    float64 `json:"weighted_recall_w"`
	CILevel            float64 `json:"ci_level"`
	EarlyStopCILevel   float64 `json:"early_stop_ci_level"`
	BootstrapResamples int     `json:"bootstrap_resamples"`
	BootstrapSeed      uint64  `json:"bootstrap_seed"`
	Consolidation      string  `json:"consolidation"`
}

// Replicates freezes the permitted case-run replication range.
type Replicates struct {
	KMin    int `json:"k_min"`
	KTarget int `json:"k_target"`
	KMax    int `json:"k_max"`
}

// Budgets freezes the resource and verdict limits for a suite.
type Budgets struct {
	MaxCases                    int      `json:"max_cases"`
	MaxAttemptsPerCase          int      `json:"max_attempts_per_case"`
	MaxRetries                  int      `json:"max_retries"`
	MaxTokensPerCaseRun         int      `json:"max_tokens_per_case_run"`
	MaxCostPerCaseRunUSD        float64  `json:"max_cost_per_case_run_usd"`
	MaxCostSuiteUSD             float64  `json:"max_cost_suite_usd"`
	MaxRuntimePerCaseRunSeconds int      `json:"max_runtime_per_case_run_seconds"`
	MaxRuntimeSuiteSeconds      int      `json:"max_runtime_suite_seconds"`
	Verdicts                    []string `json:"verdicts"`
}

// ManifestCase records the answer-key and source-tree digests for one case.
type ManifestCase struct {
	ID                string `json:"id"`
	KeySHA256         string `json:"key_sha256"`
	FixtureTreeSHA256 string `json:"fixture_tree_sha256"`
	FixTreeSHA256     string `json:"fix_tree_sha256"`
}

// Manifest is an immutable, sealed benchmark definition.
type Manifest struct {
	Benchmark                string           `json:"benchmark"`
	Version                  string           `json:"version"`
	Status                   string           `json:"status"`
	Created                  string           `json:"created"`
	ChangeReason             string           `json:"change_reason"`
	Supersedes               string           `json:"supersedes"`
	KeySchema                int              `json:"key_schema"`
	Cases                    []ManifestCase   `json:"cases"`
	Domains                  []Domain         `json:"domains"`
	CriticalDomains          []Domain         `json:"critical_domains"`
	DetectionCriteriaVersion string           `json:"detection_criteria_version"`
	MetricConfig             MetricConfig     `json:"metric_config"`
	Replicates               Replicates       `json:"replicates"`
	Budgets                  Budgets          `json:"budgets"`
	Seeds                    map[string]int64 `json:"seeds"`
	Canary                   string           `json:"canary"`
	ManifestSHA256           string           `json:"manifest_sha256"`
}

// ManifestSpec supplies a manifest's configuration and case IDs before their digests are computed.
type ManifestSpec struct {
	Benchmark                string           `json:"benchmark"`
	Version                  string           `json:"version"`
	Status                   string           `json:"status"`
	Created                  string           `json:"created"`
	ChangeReason             string           `json:"change_reason"`
	Supersedes               string           `json:"supersedes"`
	KeySchema                int              `json:"key_schema"`
	Cases                    []string         `json:"cases"`
	Domains                  []Domain         `json:"domains"`
	CriticalDomains          []Domain         `json:"critical_domains"`
	DetectionCriteriaVersion string           `json:"detection_criteria_version"`
	MetricConfig             MetricConfig     `json:"metric_config"`
	Replicates               Replicates       `json:"replicates"`
	Budgets                  Budgets          `json:"budgets"`
	Seeds                    map[string]int64 `json:"seeds"`
	Canary                   string           `json:"canary"`
}

// DefaultManifestSpec supplies the initial spec defaults used by the manifest CLI.
func DefaultManifestSpec(suite, version, reason, benchDir string, cases []string) (ManifestSpec, error) {
	var replicates Replicates
	budgets := Budgets{MaxAttemptsPerCase: 1}
	switch suite {
	case "FAST":
		replicates = Replicates{KMin: 1, KTarget: 1, KMax: 1}
		budgets.MaxCases, budgets.MaxRetries, budgets.MaxTokensPerCaseRun = 6, 1, 400000
		budgets.MaxCostPerCaseRunUSD, budgets.MaxCostSuiteUSD = 1.50, 8
		budgets.MaxRuntimePerCaseRunSeconds, budgets.MaxRuntimeSuiteSeconds = 480, 900
		budgets.Verdicts = []string{"SMOKE_PASS", "SMOKE_FAIL"}
	case "CORE":
		replicates = Replicates{KMin: 2, KTarget: 3, KMax: 4}
		budgets.MaxCases, budgets.MaxRetries, budgets.MaxTokensPerCaseRun = 24, 2, 1500000
		budgets.MaxCostPerCaseRunUSD, budgets.MaxCostSuiteUSD = 3, 90
		budgets.MaxRuntimePerCaseRunSeconds, budgets.MaxRuntimeSuiteSeconds = 720, 7200
		budgets.Verdicts = []string{"PASS", "REVIEW", "FAIL"}
	case "DEEP":
		replicates = Replicates{KMin: 3, KTarget: 5, KMax: 6}
		budgets.MaxCases, budgets.MaxRetries, budgets.MaxTokensPerCaseRun = 24, 2, 3000000
		budgets.MaxCostPerCaseRunUSD, budgets.MaxCostSuiteUSD = 5, 300
		budgets.MaxRuntimePerCaseRunSeconds, budgets.MaxRuntimeSuiteSeconds = 1200, 28800
		budgets.Verdicts = []string{"PASS", "REVIEW", "FAIL"}
	default:
		return ManifestSpec{}, fmt.Errorf("suite %q requires a manifest template", suite)
	}
	if len(cases) == 0 {
		return ManifestSpec{}, errors.New("manifest must include at least one case")
	}
	firstKey, err := bench.LoadKey(filepath.Join(benchDir, "cases", cases[0]))
	if err != nil {
		return ManifestSpec{}, fmt.Errorf("read first case canary: %w", err)
	}
	return ManifestSpec{
		Benchmark: suite, Version: version, Status: "draft", Created: time.Now().UTC().Format(time.RFC3339),
		ChangeReason: reason, KeySchema: 2, Cases: append([]string(nil), cases...),
		Domains:         []Domain{Security, Correctness, Concurrency, Resilience, Data, APICompat},
		CriticalDomains: []Domain{Security, Data}, DetectionCriteriaVersion: "dc-1",
		MetricConfig: MetricConfig{WeightedRecallW: 0.5, CILevel: 0.90, EarlyStopCILevel: 0.95, BootstrapResamples: 10000, BootstrapSeed: 1729, Consolidation: "strict-majority"},
		Replicates:   replicates, Budgets: budgets, Seeds: map[string]int64{"case_order": 7}, Canary: firstKey.Canary,
	}, nil
}

// CanonicalJSON encodes v without HTML escaping or a trailing newline.
func CanonicalJSON(v any) ([]byte, error) {
	var buf bytes.Buffer
	encoder := json.NewEncoder(&buf)
	encoder.SetEscapeHTML(false)
	if err := encoder.Encode(v); err != nil {
		return nil, err
	}
	return bytes.TrimSuffix(buf.Bytes(), []byte{'\n'}), nil
}

// BuildManifest validates and digests all cases selected by spec.
func BuildManifest(benchDir string, spec ManifestSpec) (Manifest, error) {
	if spec.Status != "draft" && spec.Status != "published" {
		return Manifest{}, fmt.Errorf("manifest status %q is not draft or published", spec.Status)
	}
	if spec.KeySchema != 2 {
		return Manifest{}, fmt.Errorf("manifest key schema must be 2, got %d", spec.KeySchema)
	}
	if spec.Replicates.KMin < 1 || spec.Replicates.KMin > spec.Replicates.KTarget || spec.Replicates.KTarget > spec.Replicates.KMax {
		return Manifest{}, fmt.Errorf("replicate counts must satisfy 1 <= k_min <= k_target <= k_max")
	}
	if spec.Budgets.MaxCases < 1 || len(spec.Cases) > spec.Budgets.MaxCases {
		return Manifest{}, fmt.Errorf("case count %d exceeds max_cases %d", len(spec.Cases), spec.Budgets.MaxCases)
	}
	if len(spec.Cases) == 0 {
		return Manifest{}, errors.New("manifest must include at least one case")
	}
	if spec.Canary == "" {
		return Manifest{}, errors.New("manifest canary is empty")
	}
	manifest := Manifest{
		Benchmark: spec.Benchmark, Version: spec.Version, Status: spec.Status, Created: spec.Created,
		ChangeReason: spec.ChangeReason, Supersedes: spec.Supersedes, KeySchema: spec.KeySchema,
		Cases: make([]ManifestCase, 0, len(spec.Cases)), Domains: append([]Domain(nil), spec.Domains...),
		CriticalDomains: append([]Domain(nil), spec.CriticalDomains...), DetectionCriteriaVersion: spec.DetectionCriteriaVersion,
		MetricConfig: spec.MetricConfig, Replicates: spec.Replicates, Budgets: spec.Budgets,
		Seeds: cloneSeeds(spec.Seeds), Canary: spec.Canary,
	}
	manifest.Budgets.Verdicts = append([]string(nil), spec.Budgets.Verdicts...)
	seen := make(map[string]bool, len(spec.Cases))
	for _, id := range spec.Cases {
		if !safeCaseID(id) {
			return Manifest{}, fmt.Errorf("invalid case ID %q", id)
		}
		if seen[id] {
			return Manifest{}, fmt.Errorf("case %q is repeated", id)
		}
		seen[id] = true
		caseDir := filepath.Join(benchDir, "cases", id)
		keyPath := filepath.Join(caseDir, bench.KeyFile)
		raw, err := readRegularFile(keyPath)
		if err != nil {
			return Manifest{}, fmt.Errorf("case %s key: %w", id, err)
		}
		key, err := bench.LoadKey(caseDir)
		if err != nil {
			return Manifest{}, fmt.Errorf("case %s: %w", id, err)
		}
		if key.Schema != 2 {
			return Manifest{}, fmt.Errorf("case %s requires KEY schema 2, got %d", id, key.Schema)
		}
		if key.ID != id {
			return Manifest{}, fmt.Errorf("case directory %q contains key for %q", id, key.ID)
		}
		if key.Canary != spec.Canary {
			return Manifest{}, fmt.Errorf("case %s canary does not match manifest canary", id)
		}
		keySum := sha256.Sum256(raw)
		fixtureDigest, err := TreeDigest(filepath.Join(caseDir, "fixture"))
		if err != nil {
			return Manifest{}, fmt.Errorf("case %s fixture tree: %w", id, err)
		}
		fixDigest, err := TreeDigest(filepath.Join(caseDir, "fix"))
		if err != nil {
			return Manifest{}, fmt.Errorf("case %s fix tree: %w", id, err)
		}
		manifest.Cases = append(manifest.Cases, ManifestCase{
			ID: id, KeySHA256: sha256Digest(keySum[:]), FixtureTreeSHA256: fixtureDigest, FixTreeSHA256: fixDigest,
		})
	}
	return manifest, nil
}

// Seal returns a copy sealed over its canonical JSON representation with the digest field empty.
func (m Manifest) Seal() (Manifest, error) {
	m.ManifestSHA256 = ""
	data, err := CanonicalJSON(m)
	if err != nil {
		return Manifest{}, fmt.Errorf("canonicalize manifest: %w", err)
	}
	sum := sha256.Sum256(data)
	m.ManifestSHA256 = sha256Digest(sum[:])
	return m, nil
}

// LoadManifest reads a manifest and refuses an absent or mismatched self-digest.
func LoadManifest(path string) (Manifest, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return Manifest{}, fmt.Errorf("read manifest %s: %w", path, err)
	}
	var manifest Manifest
	if err := json.Unmarshal(data, &manifest); err != nil {
		return Manifest{}, fmt.Errorf("parse manifest %s: %w", path, err)
	}
	if manifest.ManifestSHA256 == "" {
		return Manifest{}, fmt.Errorf("manifest %s digest is empty", path)
	}
	sealed, err := manifest.Seal()
	if err != nil {
		return Manifest{}, err
	}
	if sealed.ManifestSHA256 != manifest.ManifestSHA256 {
		return Manifest{}, fmt.Errorf("manifest %s digest mismatch: got %s, want %s", path, manifest.ManifestSHA256, sealed.ManifestSHA256)
	}
	return manifest, nil
}

// VerifyCases returns each case whose key or source-tree digest no longer matches the manifest.
func VerifyCases(benchDir string, manifest Manifest) []string {
	var mismatches []string
	for _, expected := range manifest.Cases {
		if !safeCaseID(expected.ID) {
			mismatches = append(mismatches, expected.ID)
			continue
		}
		caseDir := filepath.Join(benchDir, "cases", expected.ID)
		raw, err := readRegularFile(filepath.Join(caseDir, bench.KeyFile))
		if err != nil {
			mismatches = append(mismatches, expected.ID)
			continue
		}
		keySum := sha256.Sum256(raw)
		fixtureDigest, fixtureErr := TreeDigest(filepath.Join(caseDir, "fixture"))
		fixDigest, fixErr := TreeDigest(filepath.Join(caseDir, "fix"))
		if fixtureErr != nil || fixErr != nil || sha256Digest(keySum[:]) != expected.KeySHA256 || fixtureDigest != expected.FixtureTreeSHA256 || fixDigest != expected.FixTreeSHA256 {
			mismatches = append(mismatches, expected.ID)
		}
	}
	return mismatches
}

// TreeDigest hashes sorted relative slash paths and each file's SHA-256 hex digest.
func TreeDigest(dir string) (string, error) {
	info, err := os.Lstat(dir)
	if errors.Is(err, os.ErrNotExist) {
		return "", nil
	}
	if err != nil {
		return "", fmt.Errorf("stat tree %s: %w", dir, err)
	}
	if info.Mode()&os.ModeSymlink != 0 {
		return "", fmt.Errorf("tree root %s is a symlink", dir)
	}
	if !info.IsDir() {
		return "", fmt.Errorf("tree root %s is not a directory", dir)
	}
	type entry struct{ path, digest string }
	var files []entry
	err = filepath.WalkDir(dir, func(path string, item os.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if path == dir {
			return nil
		}
		if item.Type()&os.ModeSymlink != 0 {
			return fmt.Errorf("tree contains symlink %s", path)
		}
		if item.IsDir() {
			return nil
		}
		fileInfo, err := item.Info()
		if err != nil {
			return err
		}
		if !fileInfo.Mode().IsRegular() {
			return fmt.Errorf("tree contains non-regular file %s", path)
		}
		data, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		rel, err := filepath.Rel(dir, path)
		if err != nil {
			return err
		}
		fileSum := sha256.Sum256(data)
		files = append(files, entry{path: filepath.ToSlash(rel), digest: hex.EncodeToString(fileSum[:])})
		return nil
	})
	if err != nil {
		return "", fmt.Errorf("walk tree %s: %w", dir, err)
	}
	sort.Slice(files, func(i, j int) bool { return files[i].path < files[j].path })
	h := sha256.New()
	for _, file := range files {
		_, _ = io.WriteString(h, file.path)
		_, _ = h.Write([]byte{0})
		_, _ = io.WriteString(h, file.digest)
		_, _ = h.Write([]byte{0})
	}
	return sha256Digest(h.Sum(nil)), nil
}

func sha256Digest(sum []byte) string { return "sha256:" + hex.EncodeToString(sum) }

func safeCaseID(id string) bool {
	return id != "" && id != "." && id != ".." && filepath.Base(id) == id && !strings.ContainsAny(id, `/\\`)
}

func readRegularFile(path string) ([]byte, error) {
	info, err := os.Lstat(path)
	if err != nil {
		return nil, err
	}
	if !info.Mode().IsRegular() {
		return nil, fmt.Errorf("%s is not a regular file", path)
	}
	return os.ReadFile(path)
}

func cloneSeeds(seeds map[string]int64) map[string]int64 {
	if seeds == nil {
		return nil
	}
	copy := make(map[string]int64, len(seeds))
	for key, value := range seeds {
		copy[key] = value
	}
	return copy
}

package bench

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"sort"
	"strings"
	"time"

	"github.com/alesierraalta/tpp/internal/assets"
	"github.com/alesierraalta/tpp/internal/buildinfo"
)

// Provenance is the instrument a reading was taken with. Two numbers are comparable only when
// these agree; a comparison that cannot say which of them moved is not a delta.
type Provenance struct {
	MetricsVersion          int      `json:"metrics_version"`
	Scorer                  string   `json:"scorer"` // build revision that produced the numbers
	Model                   string   `json:"model"`
	Runner                  string   `json:"runner"` // pi | claude
	SkillVersion            string   `json:"skill_version"`
	Corpus                  string   `json:"corpus"` // digest of cases, requests and runs per case
	Cases                   int      `json:"cases"`
	Runs                    int      `json:"runs"`                    // runs per case
	AgentConfig             string   `json:"agent_config"`            // bench | inherited | custom | unspecified
	SkillsDigest            string   `json:"skills_digest,omitempty"` // digest of the skills a throwaway config carried
	Environment             string   `json:"environment"`             // os/arch of the instrument
	SuiteTools              string   `json:"suite_tools,omitempty"`   // node and go versions the suites ran with
	ManifestSHA256          string   `json:"manifest_sha256,omitempty"`
	ManifestVersion         string   `json:"manifest_version,omitempty"`
	ManifestSuite           string   `json:"manifest_suite,omitempty"`
	BudgetIdentity          string   `json:"budget_identity,omitempty"`
	BudgetStatus            string   `json:"budget_status,omitempty"` // supported | unsupported
	BudgetUnsupportedReason string   `json:"budget_unsupported_reason,omitempty"`
	BudgetLimitations       []string `json:"budget_limitations,omitempty"`
	InstrumentValid         *bool    `json:"instrument_valid,omitempty"`
	ExecutionComplete       *bool    `json:"execution_complete,omitempty"`
}

// Agent-config modes identify whether a run used the isolated benchmark configuration or an operator-owned one.
const (
	ConfigBench     = "bench"
	ConfigInherited = "inherited"
	ConfigCustom    = "custom"
)

const unspecifiedConfig = "unspecified"

// SkillsDigest returns the stable digest of the relative skill file paths and their bytes. A config directory is
// accepted for callers that have not already selected its skills subdirectory.
func SkillsDigest(skillsRoot string) (string, error) {
	root, err := resolveSkillsRoot(skillsRoot)
	if err != nil {
		return "", err
	}
	files, err := filesystemSkillFiles(root)
	if err != nil {
		return "", err
	}
	return digestSkillFiles(files), nil
}

// VerifyBenchSkills refuses a throwaway config whose skill tree is missing, extra, or changed from the embedded set.
func VerifyBenchSkills(dir string) error {
	root, err := resolveSkillsRoot(filepath.Join(dir, "skills"))
	if err != nil {
		return err
	}
	actual, err := filesystemSkillFiles(root)
	if err != nil {
		return err
	}
	expected, err := embeddedSkillFiles()
	if err != nil {
		return err
	}
	names := make([]string, 0, len(actual)+len(expected))
	seen := make(map[string]bool, len(actual)+len(expected))
	for name := range actual {
		seen[name] = true
		names = append(names, name)
	}
	for name := range expected {
		if !seen[name] {
			names = append(names, name)
		}
	}
	sort.Strings(names)
	for _, name := range names {
		got, present := actual[name]
		want, expectedPresent := expected[name]
		switch {
		case !present && expectedPresent:
			return fmt.Errorf("missing embedded skill file %s", name)
		case present && !expectedPresent:
			return fmt.Errorf("extra skill file %s", name)
		case !bytes.Equal(got, want):
			return fmt.Errorf("skill file %s differs from embedded bytes", name)
		}
	}
	return nil
}

// environment identifies the operating system and architecture of the instrument.
func environment() string {
	return runtime.GOOS + "/" + runtime.GOARCH
}

// suiteTools records optional suite runtimes without making them a prerequisite for a benchmark run.
func suiteTools() string {
	var tools []string
	if version := commandVersion("node", "--version"); version != "" {
		tools = append(tools, "node "+version)
	}
	if version := commandVersion("go", "version"); version != "" {
		tools = append(tools, "go "+version)
	}
	return strings.Join(tools, "; ")
}

func commandVersion(name string, args ...string) string {
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	out, err := exec.CommandContext(ctx, name, args...).Output()
	if err != nil {
		return ""
	}
	return strings.TrimSpace(string(out))
}

func resolveSkillsRoot(root string) (string, error) {
	if root == "" {
		return "", errors.New("skills directory is empty")
	}
	candidate := filepath.Join(root, "skills")
	if info, err := os.Stat(candidate); err == nil {
		if !info.IsDir() {
			return "", fmt.Errorf("skills path %s is not a directory", candidate)
		}
		return candidate, nil
	} else if !errors.Is(err, fs.ErrNotExist) {
		return "", err
	}
	info, err := os.Stat(root)
	if err != nil {
		return "", err
	}
	if !info.IsDir() {
		return "", fmt.Errorf("skills path %s is not a directory", root)
	}
	return root, nil
}

func filesystemSkillFiles(root string) (map[string][]byte, error) {
	files := map[string][]byte{}
	err := filepath.WalkDir(root, func(path string, entry fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if entry.IsDir() {
			return nil
		}
		rel, err := filepath.Rel(root, path)
		if err != nil {
			return err
		}
		name := filepath.ToSlash(rel)
		if entry.Type()&os.ModeSymlink != 0 {
			return fmt.Errorf("skill file %s is a symlink", name)
		}
		info, err := entry.Info()
		if err != nil {
			return err
		}
		if !info.Mode().IsRegular() {
			return fmt.Errorf("skill path %s is not a regular file", name)
		}
		data, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		files[name] = data
		return nil
	})
	return files, err
}

func embeddedSkillFiles() (map[string][]byte, error) {
	files := map[string][]byte{}
	err := fs.WalkDir(assets.Skills(), ".", func(path string, entry fs.DirEntry, walkErr error) error {
		if walkErr != nil || path == "." {
			return walkErr
		}
		if entry.IsDir() {
			return nil
		}
		data, err := fs.ReadFile(assets.Skills(), path)
		if err != nil {
			return err
		}
		files[filepath.ToSlash(path)] = data
		return nil
	})
	return files, err
}

func digestSkillFiles(files map[string][]byte) string {
	paths := make([]string, 0, len(files))
	for path := range files {
		paths = append(paths, path)
	}
	sort.Strings(paths)
	var b strings.Builder
	for _, path := range paths {
		b.WriteString(part(path))
		b.WriteString(part(string(files[path])))
		b.WriteByte('\n')
	}
	sum := sha256.Sum256([]byte(b.String()))
	return "sha256:" + hex.EncodeToString(sum[:])[:16]
}

func provenanceForRun(opts Options, corpus string, cases int) Provenance {
	p := Provenance{
		MetricsVersion: MetricsVersion,
		Scorer:         buildinfo.Revision(),
		Model:          opts.Model,
		Runner:         effectiveRunner(opts.Runner),
		SkillVersion:   SkillVersion(opts.SkillFile),
		Corpus:         corpus,
		Cases:          cases,
		Runs:           opts.Runs,
		AgentConfig:    configMode(opts.ConfigMode),
		Environment:    environment(),
		SuiteTools:     suiteTools(),
	}
	if opts.ConfigDir != "" {
		p.SkillsDigest, _ = SkillsDigest(opts.ConfigDir)
	}
	if opts.StrictManifest {
		p.ManifestSHA256, p.ManifestVersion, p.ManifestSuite, p.BudgetIdentity = opts.ManifestSHA256, opts.ManifestVersion, opts.ManifestSuite, opts.BudgetIdentity
		valid, complete := true, true
		if opts.runtime != nil {
			opts.runtime.mu.Lock()
			valid, complete = opts.runtime.instrumentValid, opts.runtime.executionComplete
			p.BudgetUnsupportedReason = opts.runtime.budgetUnsupported
			if p.BudgetUnsupportedReason != "" {
				p.BudgetStatus = "unsupported"
			} else {
				p.BudgetStatus = "supported"
			}
			opts.runtime.mu.Unlock()
		}
		p.InstrumentValid, p.ExecutionComplete = &valid, &complete
		if opts.MaxCostUSD > 0 || opts.MaxCostPerCaseRunUSD > 0 || opts.MaxTokensPerCaseRun > 0 {
			p.BudgetLimitations = []string{"cost and token thresholds are checked after each complete agent response; the response that crosses a threshold may overshoot it"}
		}
		if effectiveRunner(opts.Runner) == RunnerPi {
			p.BudgetLimitations = append(p.BudgetLimitations, "the pi runner has no turn-cap flag: a turn cap is not enforced for pi and turns are bounded only by the case and suite deadlines")
		}
		p.Cases = len(opts.CaseIDs)
	}
	return p
}

func provenanceForRescore(agg Aggregate, skillVersion string) Provenance {
	p := agg.Provenance
	if p.MetricsVersion == 0 {
		p.MetricsVersion = 1
	}
	if p.Model == "" {
		p.Model = agg.Model
	}
	if p.SkillVersion == "" {
		p.SkillVersion = skillVersion
	}
	if p.Corpus == "" {
		p.Corpus = agg.Corpus
	}
	if p.Cases == 0 {
		p.Cases = distinctCaseCount(agg.Cases)
	}
	if p.Runs == 0 {
		p.Runs = agg.Runs
	}
	p.AgentConfig = configMode(p.AgentConfig)
	p.Scorer = buildinfo.Revision()
	p.MetricsVersion = MetricsVersion
	return p
}

func aggregateProvenance(agg Aggregate) Provenance {
	p := agg.Provenance
	if p.MetricsVersion == 0 {
		p.MetricsVersion = 1
	}
	if p.Model == "" {
		p.Model = agg.Model
	}
	if p.SkillVersion == "" {
		p.SkillVersion = agg.SkillVersion
	}
	if p.Corpus == "" {
		p.Corpus = agg.Corpus
	}
	if p.Cases == 0 {
		p.Cases = distinctCaseCount(agg.Cases)
	}
	if p.Runs == 0 {
		p.Runs = agg.Runs
	}
	p.AgentConfig = configMode(p.AgentConfig)
	return p
}

func configMode(mode string) string {
	if mode == "" {
		return unspecifiedConfig
	}
	return mode
}

func effectiveRunner(runner string) string {
	if runner == "" {
		return RunnerClaude
	}
	return runner
}

func distinctCaseCount(results []Result) int {
	seen := map[string]bool{}
	for _, result := range results {
		seen[result.Case] = true
	}
	return len(seen)
}

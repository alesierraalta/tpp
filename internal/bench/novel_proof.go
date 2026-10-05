package bench

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"runtime"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/alesierraalta/tpp/internal/hookcmd"
)

const NovelCorrectnessRuleV1 = "saved-test-correctness-v1"

type NovelRunClass string

const (
	NovelPassed           NovelRunClass = "passed"
	NovelAssertionFailure NovelRunClass = "assertion_failure"
	NovelInconclusive     NovelRunClass = "inconclusive"
)

type NovelProcessObservation struct {
	Classification  NovelRunClass `json:"classification"`
	ExitCode        int           `json:"exit_code"`
	Started         bool          `json:"started"`
	TimedOut        bool          `json:"timed_out"`
	Complete        bool          `json:"complete"`
	OutputSHA256    string        `json:"output_sha256"`
	Output          string        `json:"output,omitempty"`
	OutputTruncated bool          `json:"output_truncated,omitempty"`
	SourceSHA256    string        `json:"source_sha256"`
	ControlSHA256   string        `json:"control_sha256,omitempty"`
	TestSHA256      string        `json:"test_sha256"`
	Reason          string        `json:"reason,omitempty"`
}

type NovelReplayInput struct {
	RuleVersion string
	CaseDir     string
	ArtifactDir string
	Artifacts   []TestArtifact
	Suite       string
	ControlName string
	ControlDir  string
	Timeout     time.Duration
}
type NovelReplayObservation struct {
	RuleVersion           string                  `json:"rule_version"`
	ControlName           string                  `json:"control_name"`
	TestSHA256            string                  `json:"test_sha256"`
	Subject               NovelProcessObservation `json:"subject"`
	Control               NovelProcessObservation `json:"control"`
	SupportedReproduction bool                    `json:"supported_reproduction"`
	AdjudicationRequired  bool                    `json:"adjudication_required"`
}

// ReplayNovelSavedTest only establishes a bounded, executable saved-test contrast; it does not
// establish semantic causation. A later blind adjudication must inspect domain and findings.
func ReplayNovelSavedTest(in NovelReplayInput) (NovelReplayObservation, error) {
	if in.RuleVersion != NovelCorrectnessRuleV1 {
		return NovelReplayObservation{}, fmt.Errorf("unsupported novel rule %q", in.RuleVersion)
	}
	if in.ControlName == "" || in.CaseDir == "" || in.ArtifactDir == "" || in.ControlDir == "" || in.Timeout <= 0 {
		return NovelReplayObservation{}, errors.New("incomplete novel replay input")
	}
	argv, kind, err := novelCommand(in.Suite)
	if err != nil {
		return NovelReplayObservation{}, err
	}
	if len(in.Artifacts) == 0 {
		return NovelReplayObservation{}, errors.New("saved test list is empty")
	}
	fixture := filepath.Join(in.CaseDir, FixtureDir)
	artifacts := append([]TestArtifact(nil), in.Artifacts...)
	sort.Slice(artifacts, func(i, j int) bool { return artifacts[i].Path < artifacts[j].Path })
	tests := map[string][]byte{}
	var combined bytes.Buffer
	for i, a := range artifacts {
		if i > 0 && a.Path == artifacts[i-1].Path {
			return NovelReplayObservation{}, fmt.Errorf("duplicate saved test %q", a.Path)
		}
		b, e := ReadSavedArtifact(in.ArtifactDir, a)
		if e != nil {
			return NovelReplayObservation{}, e
		}
		tests[a.Path] = b
		combined.WriteString(a.Path)
		combined.WriteByte(0)
		combined.Write(b)
		combined.WriteByte(0)
	}
	if err := validateNovelSelection(argv, tests, kind); err != nil {
		return NovelReplayObservation{}, err
	}
	fixtureHash, err := treeDigest(fixture)
	if err != nil {
		return NovelReplayObservation{}, err
	}
	controlHash, err := treeDigest(in.ControlDir)
	if err != nil {
		return NovelReplayObservation{}, err
	}
	// Preflight control overlay: only regular files within its root, and no test/runner replacement.
	if err := filepath.WalkDir(in.ControlDir, func(p string, d fs.DirEntry, e error) error {
		if e != nil {
			return e
		}
		if d.Type()&fs.ModeSymlink != 0 {
			return fmt.Errorf("control symlink refused: %s", p)
		}
		if d.IsDir() {
			return nil
		}
		if !d.Type().IsRegular() {
			return fmt.Errorf("nonregular control file: %s", p)
		}
		rel, _ := filepath.Rel(in.ControlDir, p)
		if _, ok := tests[filepath.ToSlash(rel)]; ok {
			return fmt.Errorf("control replaces saved test %q", rel)
		}
		if isNovelRunnerFile(rel, kind) {
			return fmt.Errorf("control replaces test runner/config %q", rel)
		}
		return nil
	}); err != nil {
		return NovelReplayObservation{}, err
	}
	// Verify the fixture/control identities again after input checks, before staging either run.
	if h, e := treeDigest(fixture); e != nil || h != fixtureHash {
		return NovelReplayObservation{}, errors.New("fixture changed during preflight")
	}
	if h, e := treeDigest(in.ControlDir); e != nil || h != controlHash {
		return NovelReplayObservation{}, errors.New("control changed during preflight")
	}
	testHash := digest(combined.Bytes())
	obs := NovelReplayObservation{RuleVersion: in.RuleVersion, ControlName: in.ControlName, TestSHA256: testHash, AdjudicationRequired: true}
	obs.Subject = runNovelSide(fixture, "", tests, argv, kind, in.Timeout, testHash)
	obs.Control = runNovelSide(fixture, in.ControlDir, tests, argv, kind, in.Timeout, testHash)
	obs.Control.ControlSHA256 = controlHash
	obs.SupportedReproduction = obs.Subject.Classification == NovelAssertionFailure && obs.Control.Classification == NovelPassed
	return obs, nil
}

func novelCommand(s string) ([]string, string, error) {
	f, e := hookcmd.ShellWords(s)
	if e != nil {
		return nil, "", e
	}
	if len(f) < 3 {
		return nil, "", errors.New("novel suite must use node --test or go test with explicit test selection")
	}
	if f[0] == "node" && f[1] == "--test" {
		return append([]string{"node", "--test", "--test-reporter=tap"}, f[2:]...), "node", nil
	}
	if f[0] == "go" && f[1] == "test" {
		return append([]string{"go", "test", "-json"}, f[2:]...), "go", nil
	}
	return nil, "", errors.New("unsupported novel test runner")
}
func validateNovelSelection(argv []string, tests map[string][]byte, kind string) error {
	if kind == "go" {
		return errors.New("Go novel replay is not supported by correctness rule v1")
	}
	if len(argv) != len(tests)+3 {
		return errors.New("Node command must select exactly the verified saved tests")
	}
	selected := map[string]bool{}
	for _, a := range argv[3:] {
		p := filepath.ToSlash(filepath.Clean(a))
		if p == "." || strings.HasPrefix(p, "../") || filepath.IsAbs(a) {
			return fmt.Errorf("unsafe Node test path %q", a)
		}
		if _, ok := tests[p]; !ok || selected[p] {
			return fmt.Errorf("Node test selection is not an exact saved-test match: %q", a)
		}
		selected[p] = true
	}
	return nil
}
func isNovelRunnerFile(rel, kind string) bool {
	base := strings.ToLower(filepath.Base(rel))
	if kind == "node" {
		return base == "package.json" || strings.HasPrefix(base, "node.config") || strings.HasPrefix(base, "vitest.config")
	}
	return base == "go.mod" || base == "go.work"
}

const novelOutputLimit = 1 << 20

// novelOutputBuffer serializes writes from os/exec's concurrent stdout/stderr copy loops.
type novelOutputBuffer struct {
	mu       sync.Mutex
	data     []byte
	exceeded bool
	cancel   context.CancelFunc
}

func (b *novelOutputBuffer) Write(p []byte) (int, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	remaining := novelOutputLimit - len(b.data)
	if remaining > 0 {
		n := len(p)
		if n > remaining {
			n = remaining
		}
		b.data = append(b.data, p[:n]...)
	}
	if len(p) > remaining && !b.exceeded {
		b.exceeded = true
		b.cancel()
	}
	return len(p), nil
}

func (b *novelOutputBuffer) snapshot() ([]byte, bool) {
	b.mu.Lock()
	defer b.mu.Unlock()
	return append([]byte(nil), b.data...), b.exceeded
}

func runNovelSide(fixture, overlay string, tests map[string][]byte, argv []string, kind string, timeout time.Duration, testHash string) NovelProcessObservation {
	if !novelProcessGroupsSupported() {
		return NovelProcessObservation{Classification: NovelInconclusive, ExitCode: -1, Reason: "novel replay process cleanup is unsupported on " + runtime.GOOS, TestSHA256: testHash}
	}
	dir, e := stage(fixture, overlay, "", nil)
	if e != nil {
		return NovelProcessObservation{Classification: NovelInconclusive, ExitCode: -1, Reason: e.Error(), TestSHA256: testHash}
	}
	defer os.RemoveAll(dir)
	for p, b := range tests {
		dst := filepath.Join(dir, filepath.FromSlash(p))
		if e = os.MkdirAll(filepath.Dir(dst), 0755); e != nil {
			return NovelProcessObservation{Classification: NovelInconclusive, ExitCode: -1, Reason: e.Error(), TestSHA256: testHash}
		}
		if e = os.WriteFile(dst, b, 0644); e != nil {
			return NovelProcessObservation{Classification: NovelInconclusive, ExitCode: -1, Reason: e.Error(), TestSHA256: testHash}
		}
	}
	sourceHash, err := treeDigest(dir)
	if err != nil {
		return NovelProcessObservation{Classification: NovelInconclusive, ExitCode: -1, TestSHA256: testHash, Reason: err.Error()}
	}
	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()
	cmd := exec.CommandContext(ctx, argv[0], argv[1:]...)
	cmd.Dir = dir
	configureNovelProcess(cmd)
	cmd.WaitDelay = novelWaitDelay
	// Deliberately do not inherit user/application variables, HOME, NODE_OPTIONS, or NODE_PATH.
	// PATH selects the already-installed runtime; locale is fixed. This is not a sandbox: process
	// group cleanup is not containment, and a descendant that calls setsid can escape the group.
	cmd.Env = []string{"PATH=" + os.Getenv("PATH"), "LANG=C", "LC_ALL=C", "HOME=" + dir}
	out := &novelOutputBuffer{cancel: cancel}
	cmd.Stdout = out
	cmd.Stderr = out
	err = cmd.Run()
	groupAlive, groupErr := killNovelProcessGroup(cmd)
	output, outputExceeded := out.snapshot()
	code := 0
	started := true
	if err != nil {
		code = -1
		var ee *exec.ExitError
		if errors.As(err, &ee) {
			code = ee.ExitCode()
		} else {
			started = false
		}
	}
	ob := NovelProcessObservation{ExitCode: code, Started: started, TimedOut: ctx.Err() == context.DeadlineExceeded, Output: string(output), OutputSHA256: digest(output), SourceSHA256: sourceHash, TestSHA256: testHash, OutputTruncated: outputExceeded}
	if errors.Is(err, exec.ErrWaitDelay) {
		groupAlive = true // WaitDelay means an inherited pipe remained open after the runner exited.
	}
	if groupAlive || groupErr != nil {
		ob.Classification = NovelInconclusive
		ob.Reason = "descendant processes outlived test runner"
		if errors.Is(err, exec.ErrWaitDelay) {
			ob.Reason += "; output pipe wait delay expired"
		}
		return ob
	}
	if outputExceeded {
		ob.Classification = NovelInconclusive
		ob.Reason = "output limit exceeded; captured output is truncated"
		return ob
	}
	if err == nil {
		if kind == "node" && !nodeTAPPassed(string(output), tests) {
			ob.Classification = NovelInconclusive
			ob.Reason = "successful process did not produce a complete, non-skipped TAP suite"
			return ob
		}
		ob.Complete = true
		ob.Classification = NovelPassed
		return ob
	}
	if !started || ob.TimedOut || code < 0 {
		ob.Classification = NovelInconclusive
		ob.Reason = err.Error()
		return ob
	}
	if kind == "node" && nodeAssertionFailure(string(output), tests) {
		ob.Complete = true
		ob.Classification = NovelAssertionFailure
		return ob
	}
	if kind == "go" && goAssertionFailure(string(output), tests) {
		ob.Complete = true
		ob.Classification = NovelAssertionFailure
		return ob
	}
	ob.Classification = NovelInconclusive
	ob.Reason = "nonzero or incomplete runner result is not a classified assertion failure"
	return ob
}

var tapEntry = regexp.MustCompile(`^(ok|not ok) [0-9]+ - .+`)
var tapPlan = regexp.MustCompile(`^1\.\.([0-9]+)$`)
var tapSummary = regexp.MustCompile(`^# (tests|pass|fail|skipped) ([0-9]+)$`)

func nodeAssertionFailure(out string, tests map[string][]byte) bool {
	if !strings.HasPrefix(out, "TAP version 13\n") {
		return false
	}
	lines := strings.Split(out, "\n")
	var plan, total, passed, failed, skipped int
	var entries []struct{ failed, assertion bool }
	for i, raw := range lines {
		line := strings.TrimSpace(raw)
		if strings.HasPrefix(line, "Bail out!") {
			return false
		}
		if m := tapPlan.FindStringSubmatch(line); m != nil {
			plan, _ = strconv.Atoi(m[1])
		}
		if m := tapSummary.FindStringSubmatch(line); m != nil {
			n, _ := strconv.Atoi(m[2])
			switch m[1] {
			case "tests":
				total = n
			case "pass":
				passed = n
			case "fail":
				failed = n
			case "skipped":
				skipped = n
			}
		}
		if m := tapEntry.FindStringSubmatch(line); m != nil {
			entry := struct{ failed, assertion bool }{failed: m[1] == "not ok"}
			var hasCode, hasName bool
			for j := i + 1; j < len(lines); j++ {
				next := strings.TrimSpace(lines[j])
				if tapEntry.MatchString(next) || tapPlan.MatchString(next) {
					break
				}
				l := strings.ToLower(next)
				if strings.Contains(l, "code: 'err_assertion'") {
					hasCode = true
				}
				if strings.Contains(l, "name: 'assertionerror'") {
					hasName = true
				}
			}
			entry.assertion = hasCode && hasName
			entries = append(entries, entry)
		}
	}
	if len(tests) == 0 || len(entries) == 0 || plan != len(entries) || total != len(entries) || passed+failed+skipped != total || failed == 0 || skipped != 0 {
		return false
	}
	actualFail := 0
	for _, e := range entries {
		if e.failed {
			actualFail++
			if !e.assertion {
				return false
			}
		}
	}
	return actualFail == failed
}
func nodeTAPPassed(out string, tests map[string][]byte) bool {
	if !nodeTAPComplete(out, tests) {
		return false
	}
	for _, line := range strings.Split(out, "\n") {
		if m := tapEntry.FindStringSubmatch(strings.TrimSpace(line)); m != nil && m[1] != "ok" {
			return false
		}
	}
	return !strings.Contains(out, "# skipped ") || strings.Contains(out, "# skipped 0")
}

func nodeTAPComplete(out string, tests map[string][]byte) bool {
	if !strings.HasPrefix(out, "TAP version 13\n") {
		return false
	}
	lines := strings.Split(out, "\n")
	var plan, total, passed, failed, skipped int
	entries := 0
	for _, raw := range lines {
		line := strings.TrimSpace(raw)
		if strings.HasPrefix(line, "Bail out!") {
			return false
		}
		if m := tapPlan.FindStringSubmatch(line); m != nil {
			plan, _ = strconv.Atoi(m[1])
		}
		if m := tapSummary.FindStringSubmatch(line); m != nil {
			n, _ := strconv.Atoi(m[2])
			switch m[1] {
			case "tests":
				total = n
			case "pass":
				passed = n
			case "fail":
				failed = n
			case "skipped":
				skipped = n
			}
		}
		if tapEntry.MatchString(line) {
			entries++
		}
	}
	return len(tests) > 0 && entries > 0 && plan == entries && total == entries && passed+failed+skipped == total
}

func goAssertionFailure(out string, tests map[string][]byte) bool {
	var packageFail, assertion bool
	for _, line := range strings.Split(out, "\n") {
		var e struct{ Action, Package, Test, Output string }
		if json.Unmarshal([]byte(line), &e) != nil {
			continue
		}
		if e.Action == "fail" && e.Test != "" {
			packageFail = true
		}
		s := strings.ToLower(e.Output)
		if strings.Contains(s, "want ") && strings.Contains(s, "got ") {
			assertion = true
		}
	}
	return packageFail && assertion
}
func treeDigest(root string) (string, error) {
	var b bytes.Buffer
	err := filepath.WalkDir(root, func(p string, d fs.DirEntry, e error) error {
		if e != nil {
			return e
		}
		if p == root {
			return nil
		}
		if d.Type()&fs.ModeSymlink != 0 {
			return fmt.Errorf("symlink refused: %s", p)
		}
		if d.IsDir() {
			return nil
		}
		if !d.Type().IsRegular() {
			return fmt.Errorf("nonregular file: %s", p)
		}
		rel, _ := filepath.Rel(root, p)
		data, e := os.ReadFile(p)
		if e != nil {
			return e
		}
		b.WriteString(filepath.ToSlash(rel))
		b.WriteByte(0)
		b.Write(data)
		b.WriteByte(0)
		return nil
	})
	if err != nil {
		return "", err
	}
	return digest(b.Bytes()), nil
}
func digest(b []byte) string { s := sha256.Sum256(b); return hex.EncodeToString(s[:]) }

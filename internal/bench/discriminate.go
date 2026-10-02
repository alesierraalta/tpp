package bench

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"time"
)

// FixDir holds the fixed versions of a case: fix/all with every defect fixed, and fix/keep-<ID>
// with every defect but <ID> fixed. Each holds only the files that differ from the fixture.
const FixDir = "fix"

// CatchResult says which planted defects the agent's tests actually distinguish. A defect is
// caught when the agent's test files are green on the fully fixed code and red on the code where
// only that defect remains; reporting the defect in the plan is a separate measure.
type CatchResult struct {
	Checked       bool                `json:"checked"`               // fix/all exists for the case
	TestFiles     []string            `json:"test_files,omitempty"`  // agent test files carried into the check
	AllGreen      bool                `json:"all_green"`             // every test passes on the fully fixed code
	BrokenTests   int                 `json:"broken_tests"`          // tests red on the fully fixed code; they prove nothing
	InvertedTests int                 `json:"inverted_tests"`        // of those, tests green while the defect is present: they pin the bug
	InvertedBy    map[string][]string `json:"inverted_by,omitempty"` // defect id -> tests that assert its defective behaviour
	Caught        map[string]bool     `json:"caught"`                // defect id -> caught
	CaughtBy      map[string][]string `json:"caught_by,omitempty"`   // defect id -> tests green on fix/all and red on keep-<id>
	Attempts      map[string]int      `json:"attempts,omitempty"`    // defect id -> how many times its variant was run
	Notes         []string            `json:"notes,omitempty"`
}

// Count returns how many defects were caught.
func (c CatchResult) Count() int {
	n := 0
	for _, v := range c.Caught {
		if v {
			n++
		}
	}
	return n
}

var (
	testPathRe    = regexp.MustCompile(`(^|/)(tests?|__tests__|spec)/|(_test\.go|\.(test|spec)\.[cm]?[jt]sx?|_spec\.rb|(^|/)test_[^/]*\.py)$`)
	ignoredPathRe = regexp.MustCompile(`(^|/)(\.git|node_modules|vendor|\.atl|\.claude|docs|testdata)(/|$)`)
)

// isTestFile recognises the test files of the suites the benchmark runs.
func isTestFile(rel string) bool {
	rel = filepath.ToSlash(rel)
	return !ignoredPathRe.MatchString(rel) && testPathRe.MatchString(rel)
}

// Discriminate runs the check for one agent workspace against the case's fixed versions.
func Discriminate(caseDir, ws string, key Key, timeout time.Duration) CatchResult {
	res := CatchResult{Caught: map[string]bool{}}
	if len(key.Defects) == 0 {
		res.Notes = append(res.Notes, "case plants no defect; catch check has nothing to distinguish")
		return res
	}
	fixture := filepath.Join(caseDir, FixtureDir)
	all := filepath.Join(caseDir, FixDir, "all")
	if st, err := os.Stat(all); err != nil || !st.IsDir() {
		res.Notes = append(res.Notes, "no fix/all directory; check skipped")
		return res
	}
	res.Checked = true
	tests, err := agentTestFiles(fixture, ws)
	if err != nil {
		res.Notes = append(res.Notes, "listing agent tests: "+err.Error())
		return res
	}
	res.TestFiles = tests
	if len(tests) == 0 {
		res.Notes = append(res.Notes, "the agent added or changed no test file")
		return res
	}
	onAll, code, out, err := testsOn(fixture, all, ws, tests, key.Suite, timeout)
	if err != nil {
		res.Notes = append(res.Notes, "fix/all: "+err.Error())
		return res
	}
	res.AllGreen = code == 0
	var trusted, red []string // only tests green on the correct code can catch anything
	for name, passed := range onAll {
		if passed {
			trusted = append(trusted, name)
		} else {
			red = append(red, name)
			res.BrokenTests++
		}
	}
	sort.Strings(trusted)
	sort.Strings(red)
	if len(trusted) == 0 {
		// A test red on correct code is broken or written to a different API: it can prove nothing.
		res.Notes = append(res.Notes, "no agent test is green on the fully fixed code: "+tail(out, 400))
		return res
	}
	recordDefectOutcomes(&res, caseDir, fixture, ws, key, tests, trusted, red, timeout)
	res.InvertedTests = countDistinct(res.InvertedBy)
	if res.InvertedTests > 0 {
		res.Notes = append(res.Notes, fmt.Sprintf("%d test(s) pin the defective behaviour: green with the defect, red once it is fixed", res.InvertedTests))
	}
	if other := res.BrokenTests - res.InvertedTests; other > 0 {
		res.Notes = append(res.Notes, fmt.Sprintf("%d test(s) red on the fully fixed code were ignored", other))
	}
	return res
}

func recordDefectOutcomes(res *CatchResult, caseDir, fixture, ws string, key Key, tests, trusted, red []string, timeout time.Duration) {
	for _, d := range key.Defects {
		got := discriminateDefect(caseDir, fixture, ws, key, d, tests, trusted, red, timeout)
		if got.note != "" {
			res.Notes = append(res.Notes, got.note)
		}
		if got.attempts > 0 {
			if res.Attempts == nil {
				res.Attempts = map[string]int{}
			}
			res.Attempts[d.ID] = got.attempts
		}
		if !got.checked {
			continue
		}
		if len(got.caughtBy) > 0 {
			if res.CaughtBy == nil {
				res.CaughtBy = map[string][]string{}
			}
			res.CaughtBy[d.ID] = got.caughtBy
		}
		res.Caught[d.ID] = len(got.caughtBy) > 0
		if len(got.invertedBy) > 0 {
			if res.InvertedBy == nil {
				res.InvertedBy = map[string][]string{}
			}
			res.InvertedBy[d.ID] = got.invertedBy
		}
	}
}

// defectOutcome is what the case's code said about one defect when only that defect was left in place.
type defectOutcome struct {
	checked    bool     // the variant was observed at least once, so its verdict means something
	attempts   int      // how many times the variant ran; zero when it was never reachable
	caughtBy   []string // trusted tests that turn red once only this defect remains
	invertedBy []string // tests that failed on the fixed code and are green while this defect stands
	note       string   // the one thing worth recording, empty when nothing went wrong
}

// discriminateDefect runs the case with only d left defective and reports which of the agent's trusted
// tests turn red, and which tests that failed on the fixed code are green here. A defect whose variant
// directory is missing is not checkable, and a variant that never ran says so: "nothing distinguished it"
// and "nobody looked" are different answers.
func discriminateDefect(caseDir, fixture, ws string, key Key, d Defect, tests, trusted, red []string, timeout time.Duration) defectOutcome {
	overlay := filepath.Join(caseDir, FixDir, "keep-"+d.ID)
	if len(key.Defects) == 1 {
		overlay = "" // the pristine fixture is the code where the only defect remains
	} else if st, err := os.Stat(overlay); err != nil || !st.IsDir() {
		return defectOutcome{note: fmt.Sprintf("%s: no fix/keep-%s directory; not checkable", d.ID, d.ID)}
	}
	// A defect that only manifests on an unlucky interleaving is not observed on every run: one red
	// attempt proves the test can distinguish it, while a green run proves nothing.
	attempts := attemptsForDefect(d)
	redOnKeep, ranOnKeep := map[string]bool{}, map[string]bool{}
	var lastErr error
	for a := 0; a < attempts; a++ {
		outcomes, _, _, err := testsOn(fixture, overlay, ws, tests, key.Suite, timeout)
		if err != nil {
			lastErr = err
			continue
		}
		for name, passed := range outcomes {
			ranOnKeep[name] = true
			if !passed {
				redOnKeep[name] = true
			}
		}
	}
	if len(ranOnKeep) == 0 {
		got := defectOutcome{attempts: attempts}
		if lastErr != nil {
			got.note = d.ID + ": " + lastErr.Error()
		}
		return got
	}
	onKeep := map[string]bool{}
	for name := range ranOnKeep {
		onKeep[name] = !redOnKeep[name]
	}
	got := defectOutcome{checked: true, attempts: attempts}
	for _, name := range trusted {
		if passed, ran := onKeep[name]; ran && !passed {
			got.caughtBy = append(got.caughtBy, name)
		}
	}
	// A test red on the fix and green while the defect stands asserts the defective behaviour: it
	// resists the fix instead of demanding it.
	for _, name := range red {
		if passed, ran := onKeep[name]; ran && passed {
			got.invertedBy = append(got.invertedBy, name)
		}
	}
	return got
}

// countDistinct counts the distinct test names across every defect's list.
func countDistinct(byDefect map[string][]string) int {
	seen := map[string]bool{}
	for _, names := range byDefect {
		for _, n := range names {
			seen[n] = true
		}
	}
	return len(seen)
}

// agentTestFiles lists test files the agent added or changed, relative to the workspace.
func agentTestFiles(fixture, ws string) ([]string, error) {
	var out []string
	err := filepath.Walk(ws, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}
		rel, _ := filepath.Rel(ws, path)
		if info.IsDir() {
			if rel != "." && ignoredPathRe.MatchString(filepath.ToSlash(rel)+"/") {
				return filepath.SkipDir
			}
			return nil
		}
		if !isTestFile(rel) {
			return nil
		}
		got, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		if orig, err := os.ReadFile(filepath.Join(fixture, rel)); err == nil && bytes.Equal(orig, got) {
			return nil
		}
		out = append(out, filepath.ToSlash(rel))
		return nil
	})
	sort.Strings(out)
	return out, err
}

// suiteOn runs the suite on fixture + overlay + the agent's test files, in a fresh directory. A suite the
// shared splitter cannot read is reported instead of run, so the caller records the case as unreadable
// rather than as a suite failure the fixture never had.
func suiteOn(fixture, overlay, ws string, tests []string, suite string, timeout time.Duration) (green bool, output string, err error) {
	dir, err := stage(fixture, overlay, ws, tests)
	if err != nil {
		return false, "", err
	}
	defer os.RemoveAll(dir)
	r, err := RunSuite(dir, suite, timeout)
	if err != nil {
		return false, r.Output, err
	}
	return r.ExitCode == 0, r.Output, nil
}

// testsOn is suiteOn with one outcome per test.
func testsOn(fixture, overlay, ws string, tests []string, suite string, timeout time.Duration) (map[string]bool, int, string, error) {
	dir, err := stage(fixture, overlay, ws, tests)
	if err != nil {
		return nil, -1, "", err
	}
	defer os.RemoveAll(dir)
	return perTest(dir, suite, timeout)
}

// stage builds fixture + overlay + the agent's test files in a fresh directory.
func stage(fixture, overlay, ws string, tests []string) (string, error) {
	dir, err := os.MkdirTemp("", "tpp-catch-")
	if err != nil {
		return "", err
	}
	fail := func(err error) (string, error) { os.RemoveAll(dir); return "", err }
	if err := copyTree(fixture, dir); err != nil {
		return fail(err)
	}
	if overlay != "" {
		if err := copyTree(overlay, dir); err != nil {
			return fail(err)
		}
	}
	for _, rel := range tests {
		dst := filepath.Join(dir, filepath.FromSlash(rel))
		if err := os.MkdirAll(filepath.Dir(dst), 0o755); err != nil {
			return fail(err)
		}
		data, err := os.ReadFile(filepath.Join(ws, filepath.FromSlash(rel)))
		if err != nil {
			return fail(err)
		}
		if err := os.WriteFile(dst, data, 0o644); err != nil {
			return fail(err)
		}
	}
	return dir, nil
}

// attemptsForDefect says how many times a defect's variant is run before concluding that no test
// distinguishes it. A race or a timing defect appears only on some interleavings.
func attemptsForDefect(d Defect) int {
	class := strings.ToLower(d.Class)
	for _, marker := range []string{"race", "concurren", "timing", "flak"} {
		if strings.Contains(class, marker) {
			return 3
		}
	}
	return 1
}

// safeArtifactFile resolves a recorded artifact path under root, for persistence and replay
// verification alike. Only a plain relative path is accepted; the target must be a regular file —
// a symlink or a directory is refused before anything reads it; and both the lexical path and its
// resolved form must stay inside root, so a directory symlink cannot turn a recorded path into a
// read outside the root it was recorded against.
func safeArtifactFile(root, rel string) (string, error) {
	if rel == "" || filepath.IsAbs(rel) {
		return "", fmt.Errorf("artifact path %q is not a relative path", rel)
	}
	clean := filepath.Clean(filepath.FromSlash(rel))
	if clean == "." || escapesRoot(clean) {
		return "", fmt.Errorf("artifact path %q escapes %q", rel, root)
	}
	full := filepath.Join(root, clean)
	if r, err := filepath.Rel(root, full); err != nil || escapesRoot(r) {
		return "", fmt.Errorf("artifact path %q escapes %q", rel, root)
	}
	st, err := os.Lstat(full)
	if err != nil {
		return "", err
	}
	if !st.Mode().IsRegular() {
		return "", fmt.Errorf("artifact %q is not a regular file", rel)
	}
	rootReal, err := filepath.EvalSymlinks(root)
	if err != nil {
		return "", err
	}
	fullReal, err := filepath.EvalSymlinks(full)
	if err != nil {
		return "", err
	}
	if r, err := filepath.Rel(rootReal, fullReal); err != nil || escapesRoot(r) {
		return "", fmt.Errorf("artifact %q resolves outside %q", rel, root)
	}
	return full, nil
}

// escapesRoot reports whether a cleaned relative path leaves the root it was joined to.
func escapesRoot(rel string) bool {
	return rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator))
}

// saveTestArtifacts persists the changed test files a catch check listed, under artifactDir at
// their workspace-relative paths, recording each entry with the sha256 of the bytes written and
// returning the list in path order, so the same run always records the same digest input. Every
// path is validated against the workspace first and every copy lands atomically: a source that is
// not a regular file inside the workspace is refused, never read.
func saveTestArtifacts(ws, artifactDir string, tests []string) ([]TestArtifact, error) {
	artifacts := make([]TestArtifact, 0, len(tests))
	for _, rel := range tests {
		src, err := safeArtifactFile(ws, rel)
		if err != nil {
			return nil, err
		}
		data, err := os.ReadFile(src)
		if err != nil {
			return nil, err
		}
		dst := filepath.Join(artifactDir, filepath.FromSlash(rel))
		if err := os.MkdirAll(filepath.Dir(dst), 0o755); err != nil {
			return nil, err
		}
		if err := writeFileAtomic(dst, data, 0o644); err != nil {
			return nil, err
		}
		sum := sha256.Sum256(data)
		artifacts = append(artifacts, TestArtifact{Path: filepath.ToSlash(rel), SHA256: hex.EncodeToString(sum[:])})
	}
	sort.Slice(artifacts, func(i, j int) bool { return artifacts[i].Path < artifacts[j].Path })
	return artifacts, nil
}

// DiscriminateSavedTests replays the catch check a run recorded, from the artifacts persisted
// beside its result. Every recorded path and sha256 is verified first; then a fresh temporary
// workspace is built from fixture/ plus exactly those saved test bytes — never the key and never
// the fix tree — and the ordinary Discriminate oracle runs against it. It needs no original
// workspace: a reading's catch outcome stays independently verifiable from the artifact directory
// alone, and an empty artifact list replays the same answer as no agent test files: nothing caught.
func DiscriminateSavedTests(caseDir, artifactDir string, key Key, artifacts []TestArtifact, timeout time.Duration) (CatchResult, error) {
	ws, err := restoreTestWorkspace(caseDir, artifactDir, artifacts)
	if err != nil {
		return CatchResult{}, err
	}
	defer os.RemoveAll(ws)
	return Discriminate(caseDir, ws, key, timeout), nil
}

// restoreTestWorkspace verifies each artifact against its recorded digest and lays fixture/ plus
// the saved test bytes into a fresh temporary workspace. A verification failure removes the
// workspace it was building, so a refused artifact set leaves nothing behind to replay.
func restoreTestWorkspace(caseDir, artifactDir string, artifacts []TestArtifact) (string, error) {
	ws, err := os.MkdirTemp("", "tpp-replay-")
	if err != nil {
		return "", err
	}
	fail := func(err error) (string, error) {
		_ = os.RemoveAll(ws)
		return "", err
	}
	if err := copyTree(filepath.Join(caseDir, FixtureDir), ws); err != nil {
		return fail(err)
	}
	ordered := append([]TestArtifact(nil), artifacts...)
	sort.Slice(ordered, func(i, j int) bool { return ordered[i].Path < ordered[j].Path })
	for _, a := range ordered {
		src, err := safeArtifactFile(artifactDir, a.Path)
		if err != nil {
			return fail(err)
		}
		data, err := os.ReadFile(src)
		if err != nil {
			return fail(err)
		}
		sum := sha256.Sum256(data)
		if got := hex.EncodeToString(sum[:]); !strings.EqualFold(got, a.SHA256) {
			return fail(fmt.Errorf("artifact %q: recorded sha256 %s, persisted bytes hash to %s", a.Path, a.SHA256, got))
		}
		dst := filepath.Join(ws, filepath.FromSlash(a.Path))
		if err := os.MkdirAll(filepath.Dir(dst), 0o755); err != nil {
			return fail(err)
		}
		if err := os.WriteFile(dst, data, 0o644); err != nil {
			return fail(err)
		}
	}
	return ws, nil
}

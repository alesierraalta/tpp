package gate

import (
	"bufio"
	"github.com/alesierraalta/tsp/internal/plan"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"time"
)

var Skills = []string{"test-strategy", "exploit-testing", "no-excess-tests", "real-run-validation"}

var Adversarial = []string{"test-strategy", "exploit-testing"}

var recognizedSkills = append(append([]string(nil), Skills...), "tsp")

var sourceExt = map[string]bool{
	".ts": true, ".tsx": true, ".js": true, ".jsx": true, ".mjs": true, ".cjs": true,
	".py": true, ".go": true, ".rs": true, ".java": true, ".rb": true, ".php": true,
	".cs": true, ".swift": true, ".kt": true, ".scala": true, ".ex": true, ".exs": true,
}

var excludedDirs = map[string]bool{
	"test": true, "tests": true, "spec": true, "__tests__": true, "e2e": true,
	"fixture": true, "fixtures": true, "eval": true, "evals": true,
	"node_modules": true, "vendor": true, "dist": true, "build": true, "target": true,
	".venv": true, "venv": true, ".cache": true, ".claude": true,
}

var (
	skillCall = regexp.MustCompile(`"skill"\s*:\s*"(` + strings.Join(recognizedSkills, "|") + `)"`)
	skillRead = regexp.MustCompile(`skills/(` + strings.Join(recognizedSkills, "|") + `)/SKILL\.md`)
)

type Input struct {
	SessionID      string `json:"session_id"`
	TranscriptPath string `json:"transcript_path"`
	Cwd            string `json:"cwd"`
	StopHookActive bool   `json:"stop_hook_active"`
}

// Deps are the process boundaries: git, the filesystem clock, the transcript, and time.
type Deps struct {
	Git            func(dir string, args ...string) (string, error)
	Stat           func(path string) (os.FileInfo, error)
	OpenTranscript func(path string) (io.ReadCloser, error)
	ReadPlan       func(path string) (string, error)
	Now            time.Time
	WorkDir        string
	// BindingDir is where `tpp bind` stores per-session bindings, wired by Run from the log path.
	// Empty keeps the environment-only contract, which is what a unit Deps exercises.
	BindingDir string
}

// Entry is one logged decision; skills_loaded is never null so the log stays queryable.
type Entry struct {
	TS           string   `json:"ts"`
	Session      string   `json:"session"`
	Repo         string   `json:"repo"`
	SkillsLoaded []string `json:"skills_loaded"`
	Audited      bool     `json:"audited,omitempty"`
	Fired        bool     `json:"fired"`
	Skipped      string   `json:"skipped,omitempty"`
	OptedOut     bool     `json:"opted_out,omitempty"`
	Plan         string   `json:"plan,omitempty"`
}

type Result struct {
	// Fire is the generic reminder this Stop no longer sends: a working-tree entry or a fresh
	// source mtime never proves this session authored a change, so nothing in Decide can set it.
	// The field and its emit line remain part of the hook's output contract.
	Fire   bool
	Audit  bool // the discipline ran and left breadth owed; a different question, same Stop
	Reason string
	Entry  *Entry
	// Owed and Pending are the decision itself, structured: layers assigned and never invoked, and
	// ranked targets still pending. The operator line reads them instead of counting phrases inside
	// Reason — prose this package writes and is free to reword, which is how the line-anchored report
	// silently turned three owed layers into "the plan owes nothing".
	Owed    int
	Pending int
	// Unreadable and Unplanned complete the decision: a cut breadth table and a plan with no layer matrix
	// both make Gaps.Any() true without raising Owed or Pending.
	Unreadable int
	Unplanned  bool
	// Micro marks a plan that owes nothing because it is an activated micro plan: complete, with no layer swept.
	Micro bool
	// Problem is a repository state the gate could not act on: a plan declaration it cannot read. It is
	// not an audit — nothing was read — and it is never silence: the model and the operator both hear it.
	Problem string
	// RunProblem is why the bound run's scope could not be audited: a run no row in the plan carries,
	// or a table whose Run column cannot be read. The operator line reports it in place of the counts,
	// because no count over that scope is honest while the scope itself is unreadable — and the other
	// runs' rows it would have to borrow to say "owes nothing" are not this run's business.
	RunProblem string
}

// IsProductionSource decides what the gate protects: source by extension, outside test,
// fixture, vendored, build, and Claude configuration trees.
func IsProductionSource(path string) bool {
	lower := strings.ToLower(path)
	if !sourceExt[filepath.Ext(lower)] {
		return false
	}
	if strings.HasSuffix(lower, ".d.ts") || strings.Contains(lower, ".test.") || strings.Contains(lower, ".spec.") {
		return false
	}
	segments := strings.Split(lower, "/")
	for _, dir := range segments[:len(segments)-1] {
		if excludedDirs[dir] {
			return false
		}
	}
	return true
}

// ParsePorcelain reads `git status --porcelain -z -uall`: a rename or copy carries the old
// path as the following field, which is skipped so the file counts once under its new name;
// deletions have nothing on disk to bound to the session.
func ParsePorcelain(raw string) []string {
	fields := strings.Split(raw, "\x00")
	out := []string{}
	for i := 0; i < len(fields); i++ {
		entry := fields[i]
		if len(entry) < 4 {
			continue
		}
		status := entry[:2]
		path := entry[3:]
		if status[0] == 'R' || status[0] == 'C' {
			i++
		}
		if strings.ContainsRune(status, 'D') {
			continue
		}
		out = append(out, path)
	}
	return out
}

func eachLine(r io.Reader, fn func(line string) bool) {
	br := bufio.NewReaderSize(r, 64*1024)
	for {
		line, err := br.ReadString('\n')
		if len(line) > 0 && !fn(line) {
			return
		}
		if err != nil {
			return
		}
	}
}

// SkillsLoaded counts a skill only on a real invocation: a Skill tool call or a read of its
// SKILL.md. Its name in the available-skills listing does not count.
func SkillsLoaded(r io.Reader) []string {
	found := []string{}
	seen := map[string]bool{}
	add := func(name string) {
		if name == "tsp" {
			name = "test-strategy"
		}
		if !seen[name] {
			seen[name] = true
			found = append(found, name)
		}
	}
	if r == nil {
		return found
	}
	eachLine(r, func(line string) bool {
		if m := skillCall.FindStringSubmatch(line); m != nil {
			add(m[1])
		}
		if m := skillRead.FindStringSubmatch(line); m != nil {
			add(m[1])
		}
		return true
	})
	return found
}

func isAdversarial(loaded []string) bool {
	for _, name := range loaded {
		for _, adv := range Adversarial {
			if name == adv {
				return true
			}
		}
	}
	return false
}

func openOrNil(d Deps, path string) io.ReadCloser {
	if path == "" || d.OpenTranscript == nil {
		return nil
	}
	rc, err := d.OpenTranscript(path)
	if err != nil {
		return nil
	}
	return rc
}

// SkippedRunUnbound is the telemetry reason for a stop whose binding carried no valid run. The
// audit reads one run's rows, so without a run there is no scope to read: the gate stays silent
// with this reason in the log instead of falling back to the whole plan, whose rows belong to
// other runs and would read as this session's debt or its all-clear.
const SkippedRunUnbound = "session_run_unbound"

// SkippedBoundPlanUnreadable records that the exact session-bound plan could not be read.
const SkippedBoundPlanUnreadable = "bound_plan_unreadable"

// Decide is the whole contract: audit the bound run when this session's own transcript evidences an
// adversarial testing skill invocation, and stay silent otherwise. A working-tree entry or a fresh
// source mtime never proves this stop's session authored a change — another session or a shell in
// the same checkout writes both — so there is no reminder to send without a binding, a run, and the
// invocation in the transcript.
func Decide(in Input, d Deps) Result {
	if in.StopHookActive {
		// The host sets this flag when the Stop hook already ran for this stop, and that run wrote its own
		// line: a second line here would count one stop twice, and silence in the log still means `the hook
		// did not run`. A payload that could not be read is the case that leaves nothing behind, and `Run`
		// records that one itself.
		return Result{}
	}
	cwd := in.Cwd
	if cwd == "" {
		cwd = d.WorkDir
	}
	root, ok := repoRoot(d, cwd)
	if !ok {
		return Result{}
	}
	entry := &Entry{
		TS:           d.Now.UTC().Format(time.RFC3339),
		Session:      in.SessionID,
		Repo:         filepath.Base(root),
		SkillsLoaded: []string{},
	}
	// The repository's own opt-out silences every stop in it, bound or not, before any binding is read.
	if _, err := d.Stat(filepath.Join(root, ".no-testing-gate")); err == nil {
		entry.OptedOut = true
		return Result{Entry: entry}
	}
	// The plan comes only from this session's explicit binding — environment or stored file, each
	// exact — and never the worktree-wide declaration, so two sessions in one worktree each audit
	// their own plan. Without a binding the stop audits nothing and says so, whatever the tree shows.
	binding, bound := sessionBinding(in.SessionID, root, d.BindingDir)
	if !bound {
		entry.Skipped = SkippedUnbound
		return Result{Entry: entry}
	}
	entry.Plan = binding.PlanPath
	// The audit is scoped to one run, so the binding has to carry one: a missing or invalid run
	// has no scope to audit, and silence with the reason logged is the only honest answer — a
	// whole-plan fallback would speak for rows the binding never claimed.
	if err := plan.ValidateRun(BindingEnv, binding.Run); err != nil {
		entry.Skipped = SkippedRunUnbound
		return Result{Entry: entry}
	}
	// The only evidence this gate trusts that the discipline ran is this session's own transcript.
	if rc := openOrNil(d, in.TranscriptPath); rc != nil {
		entry.SkillsLoaded = SkillsLoaded(rc)
		rc.Close()
	}
	if !isAdversarial(entry.SkillsLoaded) {
		// Nothing in this session invoked the discipline, so there is nothing to audit, and no
		// source change this stop can be proven to own: silence, with the row saying what was loaded.
		return Result{Entry: entry}
	}
	return audit(d, root, binding.PlanPath, binding.Run, Result{Entry: entry}, entry)
}

// repoRoot is the repository the run happened in. A directory that is not one, or a git that cannot answer,
// leaves the gate nothing to decide about.
func repoRoot(d Deps, cwd string) (string, bool) {
	out, err := d.Git(cwd, "rev-parse", "--show-toplevel")
	root := strings.TrimSpace(out)
	if err != nil || root == "" {
		return "", false
	}
	return root, true
}

// audit asks whether the discipline ran all the way: a layer assigned and never
// invoked leaves the report reading as coverage of a surface nobody examined, so the plan's breadth counters
// are read back and one of three reasons is attached — what is owed, what could not be read in the plan, or
// that it is complete. The plan path and run arrive from the session's binding; the counters are scoped to
// that run alone, and a scope the plan cannot answer for is reported as itself through RunProblem.
func audit(d Deps, root, rel, run string, res Result, entry *Entry) Result {
	planPath := filepath.Join(root, rel)
	body, err := readPlan(d, planPath)
	if err != nil {
		entry.Skipped = SkippedBoundPlanUnreadable
		return res
	}
	gaps, err := plan.GapsForRun(body, run)
	if err != nil {
		return res
	}
	res.Audit = true
	entry.Audited = true
	res.Owed, res.Pending = len(gaps.UnsweptLayers), len(gaps.PendingTargets)
	res.Unreadable, res.Unplanned = len(gaps.InterruptedTables), gaps.NoLayerMatrix
	res.Micro = gaps.Micro != ""
	// A run no row carries, or a table whose Run column cannot be read, fails closed as a scoped
	// problem: the counts above are not an all-clear, and the rows of other runs are never printed
	// here as this run's debt or its proof of completion.
	switch {
	case len(gaps.RunProblems) > 0:
		res.RunProblem = strings.Join(gaps.RunProblems, "; ")
	case gaps.RunMissing:
		res.RunProblem = "no row in " + rel + " carries run \"" + run + "\": tpp plan gaps --all shows every row"
	}
	switch {
	case gaps.Any():
		res.Reason = BuildAuditReason(rel, gaps.Report())
	case res.Micro:
		res.Reason = BuildMicroCompleteReason(rel)
	default:
		res.Reason = BuildCompleteReason(rel)
	}
	return res
}

func readPlan(d Deps, path string) (string, error) {
	if d.ReadPlan != nil {
		return d.ReadPlan(path)
	}
	body, err := os.ReadFile(path)
	return string(body), err
}

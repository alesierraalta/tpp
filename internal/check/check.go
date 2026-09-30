// Package check answers the testing questions from the repository alone: no hook payload, no
// transcript, no host. Claude Code, another agent, a Makefile and CI can all run it, because the
// only thing it needs is git and the persisted plan.
package check

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"github.com/alesierraalta/tpp/internal/gate"
	"github.com/alesierraalta/tpp/internal/plan"
)

// Deps are the process boundaries, injected so the decision is testable without a repository.
type Deps struct {
	Git      func(dir string, args ...string) (string, error)
	ReadFile func(path string) (string, error)
	PlanPath string
}

// Result is what the caller prints and exits with.
type Result struct {
	Exit  int
	Text  string
	Files []string
}

// MaxNamed is how many changed files the text names before counting the rest.
const MaxNamed = 6

// Run reports what the repository at cwd still owes. Exit 1 means there is something to do;
// exit 0 means there is not, and the text says which of the two it is either way.
func Run(cwd string, d Deps) Result {
	if d.Git == nil {
		d.Git = gitAt
	}
	if d.ReadFile == nil {
		d.ReadFile = readFile
	}
	rootOut, err := d.Git(cwd, "rev-parse", "--show-toplevel")
	root := strings.TrimSpace(rootOut)
	if err != nil || root == "" {
		return Result{Text: "not a git repository: nothing to check"}
	}
	planRelPath := d.PlanPath
	// declaredRun scopes the verdict to the run the declaration selected; it stays empty when the
	// caller named a path explicitly, and manual check keeps its legacy whole-plan reading then.
	declaredRun := ""
	if planRelPath != "" {
		if err := plan.ValidatePlanPath("--path", planRelPath); err != nil {
			return Result{Exit: 1, Text: err.Error()}
		}
		planRelPath = filepath.Clean(planRelPath)
	} else {
		var cfgErr error
		planRelPath, declaredRun, cfgErr = plan.Resolve(root, d.ReadFile)
		if cfgErr != nil {
			return Result{Exit: 1, Text: "the plan declaration could not be read: " + cfgErr.Error()}
		}
	}
	planFilePath := filepath.Join(root, planRelPath)
	// The plan is read before the diff so the versioning caveat rides with every verdict, including
	// the quiet ones: a plan git will never version is a half persistence whatever the tree shows.
	body, planErr := d.ReadFile(planFilePath)
	warn := ""
	if planErr == nil {
		warn = ignoredPlanWarning(root, planRelPath, d)
	}
	statusOut, err := d.Git(root, "status", "--porcelain", "-z", "-uall")
	if err != nil {
		return Result{Text: warn + "git status failed: nothing to check"}
	}
	var files []string
	seen := map[string]bool{}
	for _, p := range gate.ParsePorcelain(statusOut) {
		if gate.IsProductionSource(p) && !seen[p] {
			seen[p] = true
			files = append(files, p)
		}
	}
	if len(files) == 0 {
		return Result{Text: warn + "no production source is changed: nothing to check"}
	}
	if planErr != nil {
		return Result{Exit: 1, Files: files, Text: warn + changedLine(files) +
			"\nthere is no test plan at " + planRelPath + ": run the testing discipline, or write down why this change does not warrant it"}
	}
	// The verdict is scoped to the declared run when one is declared: GapsForRun counts only the
	// rows that run owns, so another run's pending rows are never this run's debt and another run's
	// finished rows are never this run's proof of completion. With no run declared the run is empty
	// and every row counts, the legacy whole-plan reading the manual command has always had.
	gaps, err := plan.GapsForRun(body, declaredRun)
	if err != nil {
		return Result{Exit: 1, Files: files, Text: warn + changedLine(files) + "\nthe plan could not be read: " + err.Error()}
	}
	if !gaps.Any() && gaps.Micro != "" {
		return Result{Files: files, Text: warn + changedLine(files) + "\n" + planRelPath + " owes nothing: it is a micro plan for one small function, which owes no layer sweep"}
	}
	if !gaps.Any() {
		owes := planRelPath + " owes nothing"
		if declaredRun != "" {
			owes += " for run " + declaredRun
		}
		return Result{Files: files, Text: warn + changedLine(files) + "\n" + owes + ": every assigned layer was swept and every ranked target is done"}
	}
	return Result{Exit: 1, Files: files, Text: warn + changedLine(files) + "\n" + gaps.Report()}
}

// ignoredPlanWarning names the one thing check cannot see by reading the file: a plan path git will
// never version, so the run's central artifact can be lost while the repository reads as settled.
// It is a warning, never a verdict: check's exit code decides whether a run is blocked.
func ignoredPlanWarning(root, path string, d Deps) string {
	if _, err := d.Git(root, "check-ignore", "-q", path); err == nil {
		return "warning: git ignores " + path + ": the run's evidence cannot be versioned\n"
	}
	return ""
}

func changedLine(files []string) string {
	shown := files
	extra := ""
	if len(files) > MaxNamed {
		shown = files[:MaxNamed]
		extra = fmt.Sprintf(" and %d more", len(files)-MaxNamed)
	}
	return fmt.Sprintf("%d production source file(s) changed: %s%s", len(files), strings.Join(shown, ", "), extra)
}

func gitAt(dir string, args ...string) (string, error) {
	cmd := exec.Command("git", args...)
	cmd.Dir = dir
	out, err := cmd.Output()
	return string(out), err
}

func readFile(path string) (string, error) {
	body, err := os.ReadFile(path)
	return string(body), err
}

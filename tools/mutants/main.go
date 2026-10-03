// mutants applies one literal mutation at a time to a copy of the repository and runs the gate
// package's suite against it. A mutant the suite does not kill is a missing test or an
// equivalent mutant; both are named in the output. Run from the repository root:
// go run ./tools/mutants
package main

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"
)

type mutant struct {
	name string
	file string
	find string
	repl string
}

const gateFile = "internal/gate/gate.go"
const runFile = "internal/gate/run.go"

var mutants = []mutant{
	{"M01 drop the stop_hook_active loop guard", gateFile, "if in.StopHookActive {", "if false {"},
	{"M02 ignore the opt-out file", gateFile, "fire := len(files) > 0 && !entry.OptedOut && !isAdversarial(entry.SkillsLoaded)", "fire := len(files) > 0 && !isAdversarial(entry.SkillsLoaded)"},
	{"M03 ignore whether an adversarial skill was loaded", gateFile, "fire := len(files) > 0 && !entry.OptedOut && !isAdversarial(entry.SkillsLoaded)", "fire := len(files) > 0 && !entry.OptedOut"},
	{"M04 fire even with zero changed files", gateFile, "fire := len(files) > 0 && !entry.OptedOut && !isAdversarial(entry.SkillsLoaded)", "fire := len(files) >= 0 && !entry.OptedOut && !isAdversarial(entry.SkillsLoaded)"},
	{"M05 a non-adversarial sibling silences the gate", gateFile, `var Adversarial = []string{"test-strategy", "exploit-testing"}`, `var Adversarial = []string{"test-strategy", "exploit-testing", "no-excess-tests"}`},
	{"M06 session start ignores the transcript and uses now", gateFile, "since = SessionStart(rc, d.Now)", "since = d.Now"},
	{"M07 mtime boundary: strictly after instead of at-or-after", gateFile, "if !info.ModTime().Before(since) {", "if info.ModTime().After(since) {"},
	{"M08 entry-cap boundary off by one", gateFile, "if len(entries) > MaxStatusEntries {", "if len(entries) >= MaxStatusEntries {"},
	{"M09 rename: do not skip the old-path field", gateFile, "if status[0] == 'R' || status[0] == 'C' {", "if false {"},
	{"M10 count deleted files", gateFile, "if strings.ContainsRune(status, 'D') {", "if false {"},
	{"M11 accept short porcelain fields", gateFile, "if len(entry) < 4 {", "if false {"},
	{"M12 node_modules counts as production source", gateFile, `"node_modules": true, "vendor": true,`, `"vendor": true,`},
	{"M13 python is not source", gateFile, `".py": true, ".go": true,`, `".go": true,`},
	{"M14 log the inverse decision", gateFile, "entry.Fired = fire", "entry.Fired = !fire"},
	{"M15 omit the file list from the reason", gateFile, `shown = append(shown, "  "+f)`, `shown = append(shown, "")`},
	{"M16 opt-out looked up in cwd instead of the root", gateFile, `d.Stat(filepath.Join(root, ".no-testing-gate"))`, `d.Stat(filepath.Join(cwd, ".no-testing-gate"))`},
	{"M17 wide fallback window collapses to zero", gateFile, "const wideWindow = 8 * time.Hour", "const wideWindow = 0 * time.Hour"},
	{"M18 the Skill tool call pattern no longer matches", gateFile, "skillCall = regexp.MustCompile(`\"skill\"", "skillCall = regexp.MustCompile(`\"skillx\""},
	{"M19 SKILL.md read pattern no longer matches", gateFile, "skillRead = regexp.MustCompile(`skills/(", "skillRead = regexp.MustCompile(`skillz/("},
	{"M20 wrong hookEventName", runFile, `"hookEventName":     "Stop",`, `"hookEventName":     "PostToolUse",`},
	{"M21 exit non-zero on unreadable payload", runFile, "if err := json.Unmarshal([]byte(payload), &in); err != nil {\n\t\treturn 0", "if err := json.Unmarshal([]byte(payload), &in); err != nil {\n\t\treturn 1"},
	{"M22 git status failure is not marked as skipped", gateFile, `entry.Skipped = "git_status_failed"`, `entry.Skipped = ""`},
	{"M23 stderr from git leaks", runFile, "cmd.Stderr = io.Discard", "cmd.Stderr = os.Stderr"},
}

// copyRepo copies the working tree without the git metadata and build outputs.
func copyRepo(src, dst string) error {
	return filepath.Walk(src, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}
		rel, _ := filepath.Rel(src, path)
		if rel == "." {
			return nil
		}
		if info.IsDir() {
			if rel == ".git" || rel == "bin" {
				return filepath.SkipDir
			}
			return os.MkdirAll(filepath.Join(dst, rel), 0o755)
		}
		data, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		return os.WriteFile(filepath.Join(dst, rel), data, info.Mode())
	})
}

func main() {
	repo, err := os.Getwd()
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(2)
	}
	if _, err := os.Stat(filepath.Join(repo, gateFile)); err != nil {
		fmt.Fprintln(os.Stderr, "run from the repository root (internal/gate/gate.go not found)")
		os.Exit(2)
	}
	survivors := []string{}
	applied := 0
	for _, m := range mutants {
		work, err := os.MkdirTemp("", "tpp-mutant-")
		if err != nil {
			fmt.Fprintln(os.Stderr, err)
			os.Exit(2)
		}
		if err := copyRepo(repo, work); err != nil {
			fmt.Fprintln(os.Stderr, err)
			os.Exit(2)
		}
		target := filepath.Join(work, m.file)
		src, err := os.ReadFile(target)
		if err != nil {
			fmt.Fprintln(os.Stderr, err)
			os.Exit(2)
		}
		if !strings.Contains(string(src), m.find) {
			fmt.Printf("  [not applied] %s (pattern not found in %s)\n", m.name, m.file)
			os.RemoveAll(work)
			continue
		}
		applied++
		if err := os.WriteFile(target, []byte(strings.Replace(string(src), m.find, m.repl, 1)), 0o644); err != nil {
			fmt.Fprintln(os.Stderr, err)
			os.Exit(2)
		}
		ctx, cancel := context.WithTimeout(context.Background(), 180*time.Second)
		cmd := exec.CommandContext(ctx, "go", "test", "./internal/gate/...", "-count=1")
		cmd.Dir = work
		out, runErr := cmd.CombinedOutput()
		cancel()
		killed := runErr != nil || strings.Contains(string(out), "FAIL")
		if killed {
			fmt.Printf("  [killed] %s\n", m.name)
		} else {
			fmt.Printf("  [SURVIVED] %s\n", m.name)
			survivors = append(survivors, m.name)
		}
		os.RemoveAll(work)
	}
	fmt.Printf("\n%d mutants applied, %d killed, %d survived\n", applied, applied-len(survivors), len(survivors))
	for _, s := range survivors {
		fmt.Println("  survivor:", s)
	}
	if len(survivors) > 0 {
		os.Exit(1)
	}
}

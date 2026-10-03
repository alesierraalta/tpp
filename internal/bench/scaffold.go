package bench

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/alesierraalta/tsp/internal/hookcmd"
)

// SuiteResult is the fixture's own suite outcome before the agent touches it.
type SuiteResult struct {
	Command  string `json:"command"`
	ExitCode int    `json:"exit_code"`
	Passed   int    `json:"passed"`
	Failed   int    `json:"failed"`
	Green    bool   `json:"green"`
	Output   string `json:"output,omitempty"` // tail only
}

// FixtureDir is the case subdirectory copied into every workspace.
const FixtureDir = "fixture"

// Scaffold copies a case's fixture into ws, commits it, and returns the suite outcome.
// The answer key is never copied: it lives beside the fixture, not inside it, and the copy is
// verified afterwards.
func Scaffold(caseDir, ws string, key Key, suiteTimeout time.Duration) (SuiteResult, error) {
	src := filepath.Join(caseDir, FixtureDir)
	if st, err := os.Stat(src); err != nil || !st.IsDir() {
		return SuiteResult{}, fmt.Errorf("case %s has no %s directory", caseDir, FixtureDir)
	}
	if err := os.MkdirAll(ws, 0o755); err != nil {
		return SuiteResult{}, err
	}
	if err := copyTree(src, ws); err != nil {
		return SuiteResult{}, fmt.Errorf("copy fixture: %w", err)
	}
	if _, err := os.Stat(filepath.Join(ws, KeyFile)); err == nil {
		return SuiteResult{}, errors.New("answer key leaked into the workspace")
	}
	// The commit identity keeps its pre-rename name on purpose: recorded runs were scaffolded under it, and
	// the scaffold commit must stay the same object for a run today as for the runs it is compared with.
	for _, args := range [][]string{
		{"init", "-q"},
		{"config", "user.email", "bench@rdd-plus"},
		{"config", "user.name", "rdd-plus bench"},
		{"add", "-A"},
		{"commit", "-qm", "fixture"},
	} {
		if out, err := gitRun(ws, args...); err != nil {
			return SuiteResult{}, fmt.Errorf("git %s: %v: %s", args[0], err, strings.TrimSpace(out))
		}
	}
	return RunSuite(ws, key.Suite, suiteTimeout)
}

// RunSuite executes the fixture suite command in ws and parses pass/fail counts best-effort. A command the
// shared splitter cannot read is refused before any subprocess runs: the words read so far are the head of a
// command nobody wrote, because an unterminated quote is dropped rather than kept, so `sh -c "touch x` would
// run `touch x` and the run would record a suite failure the fixture never had. The refused result keeps the
// command it could not read, so the caller can report which suite was unreadable.
func RunSuite(ws, suite string, timeout time.Duration) (SuiteResult, error) {
	res := SuiteResult{Command: suite, ExitCode: -1}
	fields, err := hookcmd.ShellWords(suite)
	if err != nil {
		return res, err
	}
	if len(fields) == 0 {
		return res, nil
	}
	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()
	cmd := exec.CommandContext(ctx, fields[0], fields[1:]...)
	cmd.Dir = ws
	var buf bytes.Buffer
	cmd.Stdout, cmd.Stderr = &buf, &buf
	err = cmd.Run()
	if err == nil {
		res.ExitCode = 0
	} else if ee := (&exec.ExitError{}); errors.As(err, &ee) {
		res.ExitCode = ee.ExitCode()
	}
	res.Passed, res.Failed = parseCounts(buf.String())
	res.Green = res.ExitCode == 0
	res.Output = tail(buf.String(), 2000)
	return res, nil
}

var (
	nodePass = regexp.MustCompile(`(?m)^ℹ pass (\d+)`)
	nodeFail = regexp.MustCompile(`(?m)^ℹ fail (\d+)`)
	goOK     = regexp.MustCompile(`(?m)^ok\s`)
	goFail   = regexp.MustCompile(`(?m)^(FAIL|--- FAIL)`)
)

// parseCounts understands node --test summaries and go test package lines.
func parseCounts(out string) (passed, failed int) {
	if m := nodePass.FindStringSubmatch(out); m != nil {
		passed, _ = strconv.Atoi(m[1])
		if f := nodeFail.FindStringSubmatch(out); f != nil {
			failed, _ = strconv.Atoi(f[1])
		}
		return passed, failed
	}
	return len(goOK.FindAllString(out, -1)), len(goFail.FindAllString(out, -1))
}

func tail(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[len(s)-n:]
}

func gitRun(dir string, args ...string) (string, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, "git", args...)
	cmd.Dir = dir
	out, err := cmd.CombinedOutput()
	return string(out), err
}

// copyTree copies files and directories, following the source's permissions; symlinks are
// skipped so a fixture cannot reach outside itself.
func copyTree(src, dst string) error {
	return filepath.WalkDir(src, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		rel, _ := filepath.Rel(src, p)
		target := filepath.Join(dst, rel)
		if d.Type()&fs.ModeSymlink != 0 {
			return nil
		}
		if d.IsDir() {
			return os.MkdirAll(target, 0o755)
		}
		in, err := os.Open(p)
		if err != nil {
			return err
		}
		defer in.Close()
		info, err := d.Info()
		if err != nil {
			return err
		}
		out, err := os.OpenFile(target, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, info.Mode().Perm())
		if err != nil {
			return err
		}
		if _, err := io.Copy(out, in); err != nil {
			out.Close()
			return err
		}
		return out.Close()
	})
}

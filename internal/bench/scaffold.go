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

	"github.com/alesierraalta/tpp/internal/hookcmd"
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
// verified afterwards. It is ScaffoldContext on a background context, so a non-manifest caller
// scaffolds exactly as it always has.
func Scaffold(caseDir, ws string, key Key, suiteTimeout time.Duration) (SuiteResult, error) {
	return ScaffoldContext(context.Background(), caseDir, ws, key, suiteTimeout)
}

// ScaffoldContext is Scaffold under ctx: the copy, every git subprocess and the fixture suite
// draw from the caller's deadline, so a manifest run's case budget reaches the scaffold phase
// too. Cancellation is honored cooperatively — checked before any work, per walked path and per
// IO chunk, with the git and suite subprocesses killed by their own context. A single filesystem
// syscall already blocked in the kernel cannot be cancelled from Go: it returns when the kernel
// finishes it, and only then does the scaffold observe the cancellation.
func ScaffoldContext(ctx context.Context, caseDir, ws string, key Key, suiteTimeout time.Duration) (SuiteResult, error) {
	if err := ctx.Err(); err != nil {
		return SuiteResult{}, err
	}
	src := filepath.Join(caseDir, FixtureDir)
	if st, err := os.Stat(src); err != nil || !st.IsDir() {
		return SuiteResult{}, fmt.Errorf("case %s has no %s directory", caseDir, FixtureDir)
	}
	if err := os.MkdirAll(ws, 0o755); err != nil {
		return SuiteResult{}, err
	}
	if err := copyTreeContext(ctx, src, ws); err != nil {
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
		if out, err := gitRunContext(ctx, ws, args...); err != nil {
			return SuiteResult{}, fmt.Errorf("git %s: %v: %s", args[0], err, strings.TrimSpace(out))
		}
	}
	return RunSuiteContext(ctx, ws, key.Suite, suiteTimeout)
}

// RunSuite executes the fixture suite command in ws and parses pass/fail counts best-effort. A command the
// shared splitter cannot read is refused before any subprocess runs: the words read so far are the head of a
// command nobody wrote, because an unterminated quote is dropped rather than kept, so `sh -c "touch x` would
// run `touch x` and the run would record a suite failure the fixture never had. The refused result keeps the
// command it could not read, so the caller can report which suite was unreadable.
// It is RunSuiteContext on a background context, so a non-manifest caller runs its suite as before.
func RunSuite(ws, suite string, timeout time.Duration) (SuiteResult, error) {
	return RunSuiteContext(context.Background(), ws, suite, timeout)
}

// RunSuiteContext runs the fixture suite under ctx with the phase's timeout layered on top: the
// suite is killed when the caller's deadline or its own cap arrives, whichever is earlier, so a
// manifest run's fixture suite can never outlive the case budget it runs inside.
func RunSuiteContext(ctx context.Context, ws, suite string, timeout time.Duration) (SuiteResult, error) {
	res := SuiteResult{Command: suite, ExitCode: -1}
	if err := ctx.Err(); err != nil {
		return res, err
	}
	fields, err := hookcmd.ShellWords(suite)
	if err != nil {
		return res, err
	}
	if len(fields) == 0 {
		return res, nil
	}
	suiteCtx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	cmd := exec.CommandContext(suiteCtx, fields[0], fields[1:]...)
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

// gitRun runs a git subcommand the way the scaffold always has: 60 seconds of its own on a
// background context. It is gitRunContext with that same background parent.
func gitRun(dir string, args ...string) (string, error) {
	return gitRunContext(context.Background(), dir, args...)
}

// gitRunContext runs a git subcommand under ctx, capped at the 60 seconds the scaffold always
// gave it: the child deadline is parented by ctx and can only tighten it, never reopen it.
func gitRunContext(ctx context.Context, dir string, args ...string) (string, error) {
	ctx, cancel := context.WithTimeout(ctx, 60*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, "git", args...)
	cmd.Dir = dir
	out, err := cmd.CombinedOutput()
	return string(out), err
}

// copyTree copies files and directories, following the source's permissions; symlinks are
// skipped so a fixture cannot reach outside itself. It is copyTreeContext on a background
// context, so every non-manifest caller copies exactly as before.
func copyTree(src, dst string) error {
	return copyTreeContext(context.Background(), src, dst)
}

// copyTreeContext copies under ctx with cooperative cancellation: one check per walked path and
// one per chunk written, so a canceled scaffold stops between operations instead of starting the
// next one. This is a cooperation boundary, not preemption: a single read or write syscall
// already blocked in the kernel cannot be cancelled from Go — it returns when the kernel
// finishes it, possibly after the deadline — and only then is the cancellation observed.
func copyTreeContext(ctx context.Context, src, dst string) error {
	return filepath.WalkDir(src, func(p string, d fs.DirEntry, err error) error {
		if cerr := ctx.Err(); cerr != nil {
			return cerr
		}
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
		buf := make([]byte, 32*1024)
		for {
			if cerr := ctx.Err(); cerr != nil {
				out.Close()
				return cerr
			}
			n, rerr := in.Read(buf)
			if n > 0 {
				if _, werr := out.Write(buf[:n]); werr != nil {
					out.Close()
					return werr
				}
			}
			if rerr == io.EOF {
				break
			}
			if rerr != nil {
				out.Close()
				return rerr
			}
		}
		return out.Close()
	})
}

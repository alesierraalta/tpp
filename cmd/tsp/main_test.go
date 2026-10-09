package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
	"time"

	"github.com/alesierraalta/tsp/internal/admit"
	"github.com/alesierraalta/tsp/internal/assets"
	"github.com/alesierraalta/tsp/internal/bench"
	"github.com/alesierraalta/tsp/internal/buildinfo"
	"github.com/alesierraalta/tsp/internal/evidence"
	plancheck "github.com/alesierraalta/tsp/internal/plan"
	"github.com/alesierraalta/tsp/internal/sanitize"
)

// buildCLI compiles the command once per test binary; the contract under test is the process's,
// not a function's, so it has to run as a process.
func buildCLI(t *testing.T, names ...string) string {
	t.Helper()
	if testing.Short() {
		t.Skip("builds a binary")
	}
	name := "tsp"
	if len(names) > 0 {
		name = names[0]
	}
	bin := filepath.Join(t.TempDir(), name)
	out, err := exec.Command("go", "build", "-o", bin, ".").CombinedOutput()
	if err != nil {
		t.Fatalf("build: %v\n%s", err, out)
	}
	return bin
}

// --timeout has to bound the row it names. The shell can start a descendant that inherits the output pipes
// and outlives it, and Go's copy of that output finishes only at end of file, so without WaitDelay and the
// process-group kill the run waits for the descendant: the whole pass runs past the deadline, no timeout
// line is ever printed, and the descendant keeps running after the bound was supposed to end it.
func TestRunShellBoundsARowWhoseDescendantHoldsThePipes(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 300*time.Millisecond)
	defer cancel()
	start := time.Now()
	_, err := runShell(ctx, t.TempDir(), "sleep 10 & sleep 10")
	elapsed := time.Since(start)
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("err = %v, want the deadline classified as the timeout it is", err)
	}
	if elapsed > 3*time.Second {
		t.Fatalf("the row ran %s against a 300ms timeout: the bound held only until the descendant that inherited the pipes exited", elapsed)
	}
}

func TestCLIContract(t *testing.T) {
	bin := buildCLI(t)
	cases := []struct {
		name     string
		args     []string
		stdin    string
		wantExit int
		wantOut  string // substring expected on stdout or stderr
	}{
		{name: "no command prints usage and exits 2", wantExit: 2, wantOut: "usage: tsp"},
		{name: "unknown command prints usage and exits 2", args: []string{"bogus"}, wantExit: 2, wantOut: "usage: tsp"},
		{name: "bench with no subcommand exits 2", args: []string{"bench"}, wantExit: 2, wantOut: "usage: tsp"},
		{name: "bench with an unknown subcommand exits 2", args: []string{"bench", "bogus"}, wantExit: 2, wantOut: "usage: tsp"},
		// A runner the bench cannot spawn must be refused before it scaffolds or spawns anything.
		{name: "bench run with an unknown runner exits 2", args: []string{"bench", "run", "--runner", "gemini"}, wantExit: 2, wantOut: "use pi or claude"},
		{name: "bench score without arguments exits 2", args: []string{"bench", "score"}, wantExit: 2, wantOut: "needs --case"},
		{name: "bench score with both workspace and plan exits 2", args: []string{"bench", "score", "--case", "c", "--workspace", "w", "--plan", "p"}, wantExit: 2, wantOut: "exactly one"},
		{name: "bench compare without two directories exits 2", args: []string{"bench", "compare", "only-one"}, wantExit: 2, wantOut: "two result directories"},
		{name: "bench compare on a missing directory exits 1", args: []string{"bench", "compare", "/nonexistent-a", "/nonexistent-b"}, wantExit: 1, wantOut: "compare:"},
		{name: "bench rescore without a directory exits 2", args: []string{"bench", "rescore"}, wantExit: 2, wantOut: "one results directory"},
		{name: "bench rescore on a missing directory exits 1", args: []string{"bench", "rescore", "/nonexistent"}, wantExit: 1, wantOut: "rescore:"},
		{name: "an unknown flag on a subcommand exits 2", args: []string{"doctor", "--nope"}, wantExit: 2, wantOut: "flag provided but not defined"},
		{name: "check refuses a path outside the repository", args: []string{"check", "--path", "../outside.md"}, wantExit: 2, wantOut: "--path"},
		{name: "check refuses an absolute path", args: []string{"check", "--path", "/abs.md"}, wantExit: 2, wantOut: "--path"},
		{name: "plan with no subcommand exits 2", args: []string{"plan"}, wantExit: 2, wantOut: "usage: tsp"},
		{name: "plan with an unknown subcommand exits 2", args: []string{"plan", "bogus"}, wantExit: 2, wantOut: "usage: tsp"},
		{name: "plan check on a missing file exits 1", args: []string{"plan", "check", "--path", "/nonexistent/plan.md"}, wantExit: 1, wantOut: "plan check:"},
		{name: "plan gaps on a missing file exits 1", args: []string{"plan", "gaps", "--path", "/nonexistent/plan.md"}, wantExit: 1, wantOut: "plan gaps:"},
		{name: "plan gaps run and all are mutually exclusive", args: []string{"plan", "gaps", "--run", "redis-pool", "--all"}, wantExit: 2, wantOut: "cannot combine"},
		{name: "plan gaps rejects a bad run slug", args: []string{"plan", "gaps", "--run", "Bad_Slug"}, wantExit: 2, wantOut: "--run"},
		{name: "plan admit on a missing file exits 1", args: []string{"plan", "admit", "--path", "/nonexistent/plan.md"}, wantExit: 1, wantOut: "plan admit:"},
		{name: "plan admit with an unreadable timeout exits 2", args: []string{"plan", "admit", "--timeout", "soon"}, wantExit: 2, wantOut: "invalid value"},
		// The gate is a hook: whatever it receives, it must not break the turn.
		{name: "gate on empty stdin exits 0", args: []string{"gate"}, stdin: "", wantExit: 0},
		{name: "gate on malformed stdin exits 0", args: []string{"gate"}, stdin: "{not json", wantExit: 0},
		{name: "gate on an unknown flag exits 0", args: []string{"gate", "--nope"}, stdin: "{}", wantExit: 0},
		// The mode contract is refused at the flag, before any work: an unknown value names the
		// valid ones, gentle says why it cannot run, and commands that take no --mode reject it.
		{name: "usage documents --mode", wantExit: 2, wantOut: "--mode <"},
		{name: "doctor with an unknown mode exits 2", args: []string{"doctor", "--mode", "turbo"}, wantExit: 2, wantOut: "unknown mode"},
		{name: "doctor refuses gentle as pending integration", args: []string{"doctor", "--mode", "gentle"}, wantExit: 2, wantOut: "pending integration"},
		{name: "sync does not accept --mode", args: []string{"sync", "--mode", "standalone"}, wantExit: 2, wantOut: "flag provided but not defined"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			cmd := exec.Command(bin, tc.args...)
			cmd.Stdin = strings.NewReader(tc.stdin)
			cmd.Env = append(os.Environ(), "TESTING_GATE_LOG="+filepath.Join(t.TempDir(), "gate.jsonl"))
			out, err := cmd.CombinedOutput()
			code := 0
			if ee := (&exec.ExitError{}); err != nil {
				if ok := asExit(err, ee); ok {
					code = ee.ExitCode()
				} else {
					t.Fatalf("run: %v", err)
				}
			}
			if code != tc.wantExit {
				t.Fatalf("exit = %d, want %d\n%s", code, tc.wantExit, out)
			}
			if tc.wantOut != "" && !strings.Contains(string(out), tc.wantOut) {
				t.Fatalf("output missing %q:\n%s", tc.wantOut, out)
			}
		})
	}
}

// `plan init --micro` writes the micro skeleton rather than the full plan.
func TestPlanInitMicroWritesTheMicroTemplate(t *testing.T) {
	bin := buildCLI(t)
	p := filepath.Join(t.TempDir(), "plan.md")
	if out, err := exec.Command(bin, "plan", "init", "--micro", "--path", p).CombinedOutput(); err != nil {
		t.Fatalf("plan init --micro: %v\n%s", err, out)
	}
	got, err := os.ReadFile(p)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(got), "Micro: <file path> · touches none") || strings.Contains(string(got), "## Layer matrix") {
		t.Fatalf("plan init --micro wrote the wrong skeleton:\n%s", got)
	}
}

// The header of a fresh plan names where the plan was written: the repository-relative path for a plan inside
// the repository, the absolute path for one kept outside it, and the default path when no --path is given.
func TestPlanInitHeaderNamesThePathItWroteTo(t *testing.T) {
	bin := buildCLI(t)
	repo := t.TempDir()
	if out, err := exec.Command("git", "init", "-q", repo).CombinedOutput(); err != nil {
		t.Fatalf("git init: %v\n%s", err, out)
	}
	outside := filepath.Join(t.TempDir(), "reviews", "pr-7.md")
	cases := []struct {
		name, file, want string
		args             []string
	}{
		{"default path", filepath.Join(repo, "docs/testing/test-plan.md"), "docs/testing/test-plan.md", nil},
		{"relative path", filepath.Join(repo, "plans/a.md"), "plans/a.md", []string{"--path", "plans/a.md"}},
		{"absolute path outside the repository", outside, outside, []string{"--path", outside}},
		{"micro plan", filepath.Join(repo, "plans/m.md"), "plans/m.md", []string{"--micro", "--path", "plans/m.md"}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			cmd := exec.Command(bin, append([]string{"plan", "init"}, tc.args...)...)
			cmd.Dir = repo
			if out, err := cmd.CombinedOutput(); err != nil {
				t.Fatalf("plan init: %v\n%s", err, out)
			}
			got, err := os.ReadFile(tc.file)
			if err != nil {
				t.Fatal(err)
			}
			if want := "Plan path: `" + tc.want + "`"; !strings.Contains(string(got), want) {
				t.Fatalf("header does not read %q:\n%s", want, strings.SplitN(string(got), "\n", 4)[2])
			}
		})
	}
}

// The TUI needs a real terminal; on a pipe it must refuse before tui.Run and point at the
// non-interactive equivalents instead of hanging on a loop no one can drive.
func TestTUIRefusesWithoutATerminal(t *testing.T) {
	t.Setenv("TPP_HOME", t.TempDir())
	bin := buildCLI(t)
	cmd := exec.Command(bin, "tui")
	cmd.Stdin = strings.NewReader("")
	out, err := cmd.CombinedOutput()
	code := 0
	if ee := (&exec.ExitError{}); err != nil {
		if ok := asExit(err, ee); ok {
			code = ee.ExitCode()
		} else {
			t.Fatalf("run: %v", err)
		}
	}
	if code != 1 {
		t.Fatalf("exit = %d, want 1\n%s", code, out)
	}
	for _, name := range []string{"status", "feature", "sync --dry-run"} {
		if !strings.Contains(string(out), name) {
			t.Errorf("refusal does not name %q:\n%s", name, out)
		}
	}
}

// /dev/null is a character device, so a mode-bit guard let it through and tui.Run drew a frame
// before failing. The refusal must come first, with no ANSI reaching stdout.
func TestTUIRefusesNullDeviceStdinWithoutDrawing(t *testing.T) {
	t.Setenv("TPP_HOME", t.TempDir())
	bin := buildCLI(t)
	null, err := os.Open("/dev/null")
	if err != nil {
		t.Skipf("cannot open /dev/null: %v", err)
	}
	defer null.Close()
	cmd := exec.Command(bin, "tui")
	cmd.Stdin = null
	var stdout, stderr bytes.Buffer
	cmd.Stdout, cmd.Stderr = &stdout, &stderr
	err = cmd.Run()
	code := 0
	if ee := (&exec.ExitError{}); err != nil {
		if ok := asExit(err, ee); ok {
			code = ee.ExitCode()
		} else {
			t.Fatalf("run: %v", err)
		}
	}
	if code != 1 {
		t.Fatalf("exit = %d, want 1\nstderr: %s\nstdout: %q", code, stderr.String(), stdout.String())
	}
	for _, name := range []string{"status", "feature", "sync --dry-run"} {
		if !strings.Contains(stderr.String(), name) {
			t.Errorf("refusal does not name %q:\n%s", name, stderr.String())
		}
	}
	if strings.Contains(stdout.String(), "\x1b[?25l") {
		t.Fatalf("stdout received a frame before the refusal: %q", stdout.String())
	}
}

// ledgerHeader is the shipped Evidence ledger header. The `Admit` and `Digest` columns are the two a
// recording run reads and writes, so a fixture drifting from this header would test another contract.
const ledgerHeader = "| Id | Claim | Executed | Admit | Inputs and parameters | Observed | Digest | Mutation or negative control → result | Reproduction | Label (`observado` / `razonado`, literal) |\n" +
	"|---|---|---|---|---|---|---|---|---|---|\n"

// writeLedger writes a minimal plan whose one Evidence ledger row carries the given Admit cell, so a
// test drives the real binary over a real file rather than a string it never parsed.
func writeLedger(t *testing.T, dir, admit string) string {
	t.Helper()
	path := filepath.Join(dir, "plan.md")
	doc := "## Evidence ledger\n\n" + ledgerHeader + ledgerRow("E1", admit)
	if err := os.WriteFile(path, []byte(doc), 0o644); err != nil {
		t.Fatal(err)
	}
	return path
}

// ledgerRow is one ledger row with the given id and Admit cell and an empty Digest cell for a
// recording run to fill.
func ledgerRow(id, admit string) string {
	return "| " + id + " | the claim | prose a human reads | " + admit + " | none | the observation | | reverted → red | rerun it | observado |\n"
}

// compliantPlan is the smallest plan `plan check` accepts, so a recording run can be followed by a
// check that still says well formed rather than by a smaller file that merely holds a digest.
func compliantPlan(rows ...string) string {
	return "## Findings\n\n" +
		"| Id | Finding | Severity | Data safe? | Evidence id | Pinning test | Status | Verdict by / date | Reason | Fingerprint |\n" +
		"|---|---|---|---|---|---|---|---|---|---|\n" +
		"| F1 | `src/a.js:5` drops a quoted comma | data loss | yes | E1 | tests/a.test.js :: keeps a comma | fixed | me / 2026-09-10 | - | abc1234 |\n" +
		"\n## Evidence ledger\n\n" + ledgerHeader + strings.Join(rows, "")
}

var digestRe = regexp.MustCompile(`sha256:[0-9a-f]{64}`)

// --record is the flag that turns an observation into a pinned value, so the one combination that
// would pin an observation this run never made is refused before the plan is read and before any
// command reaches the runner.
func TestPlanAdmitRecordRequiresExecute(t *testing.T) {
	bin := buildCLI(t)
	dir := t.TempDir()
	sentinel := filepath.Join(dir, "ran")
	path := writeLedger(t, dir, "touch "+sentinel)
	before, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}

	out, code := runCLI(t, bin, "plan", "admit", "--path", path, "--record", "E1")
	if code != 2 {
		t.Fatalf("exit = %d, want 2\n%s", code, out)
	}
	if !strings.Contains(out, "--record") || !strings.Contains(out, "--execute") {
		t.Fatalf("the usage error must name the flag that was given and the flag it needs:\n%s", out)
	}
	if _, err := os.Stat(sentinel); !os.IsNotExist(err) {
		t.Fatal("a usage error ran the command anyway")
	}
	after, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if string(after) != string(before) {
		t.Fatalf("a usage error wrote the plan:\n%s", after)
	}
}

// A recording run pins what it just observed and writes the plan once for the whole pass. The three
// rows also prove the composed edit: one write leaves both digests and no rewritten bytes.
func TestPlanAdmitRecordPinsTheObservedDigests(t *testing.T) {
	bin := buildCLI(t)
	dir := t.TempDir()
	path := filepath.Join(dir, "plan.md")
	before := compliantPlan(
		ledgerRow("E1", "`printf 'one\\n'`"),
		ledgerRow("E2", "`printf 'two\\n'`"),
		ledgerRow("E3", "`printf 'three\\n'`"),
	)
	if err := os.WriteFile(path, []byte(before), 0o640); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(path, 0o640); err != nil {
		t.Fatal(err)
	}

	out, code := runCLI(t, bin, "plan", "admit", "--path", path, "--execute", "--record", "E1,E2,E3")
	if code != 0 {
		t.Fatalf("recording run exit = %d, want 0\n%s", code, out)
	}
	if n := strings.Count(out, "RECORDED"); n != 3 {
		t.Fatalf("want one RECORDED line per row, got %d:\n%s", n, out)
	}
	if !strings.Contains(out, "3 recorded") {
		t.Fatalf("the summary must count the recorded rows:\n%s", out)
	}

	after, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	digests := digestRe.FindAllString(string(after), -1)
	if len(digests) != 3 {
		t.Fatalf("want three recorded digests, got %v:\n%s", digests, after)
	}
	want := before
	for _, d := range digests {
		want = strings.Replace(want, "| | reverted → red |", "| "+d+" | reverted → red |", 1)
	}
	if string(after) != want {
		t.Fatalf("the recording run rewrote bytes outside the digest cells:\ngot  %q\nwant %q", after, want)
	}
	if info, err := os.Stat(path); err != nil || info.Mode().Perm() != 0o640 {
		t.Fatalf("the run must preserve the plan's mode: %v %v", info.Mode(), err)
	}

	// The rows are runnable, not digest-missing: the dry run reports them as runnable, and an executed
	// run without --record now admits the same observation instead of refusing an unpinned row.
	out, code = runCLI(t, bin, "plan", "admit", "--path", path)
	if code != 0 || strings.Count(out, "WOULD RUN") != 3 || strings.Contains(out, "digest-missing") {
		t.Fatalf("dry run after recording = %d\n%s", code, out)
	}
	out, code = runCLI(t, bin, "plan", "admit", "--path", path, "--execute")
	if code != 0 || strings.Count(out, "ADMITTED") != 3 {
		t.Fatalf("an executed run after recording must admit = %d\n%s", code, out)
	}
	out, code = runCLI(t, bin, "plan", "check", "--path", path)
	if code != 0 || !strings.Contains(out, "well formed") {
		t.Fatalf("plan check after recording = %d\n%s", code, out)
	}
}

// A refusal to splice aborts the whole write rather than skipping the row: a half-recorded ledger is
// exactly the state this feature exists to prevent, so the file must come back byte-identical.
func TestPlanAdmitRecordAbortsTheWholeWriteWhenTheSpliceRefuses(t *testing.T) {
	bin := buildCLI(t)
	dir := t.TempDir()
	path := filepath.Join(dir, "plan.md")
	before := "## Evidence ledger\n\n" +
		"| Id | Claim | Executed | Admit | Inputs | Observed | Mutation | Reproduction | Label |\n" +
		"|---|---|---|---|---|---|---|---|---|\n" +
		"| E1 | the claim | prose | `echo hi` | none | the observation | reverted → red | rerun it | observado |\n"
	if err := os.WriteFile(path, []byte(before), 0o644); err != nil {
		t.Fatal(err)
	}

	out, code := runCLI(t, bin, "plan", "admit", "--path", path, "--execute", "--record", "E1")
	if code != 1 {
		t.Fatalf("exit = %d, want 1\n%s", code, out)
	}
	if !strings.Contains(out, "plan admit:") || !strings.Contains(out, "names no Digest column") {
		t.Fatalf("the refused splice must be reported on stderr with the plan admit: prefix:\n%s", out)
	}
	if strings.Contains(out, "RECORDED") {
		t.Fatalf("a refused splice must record nothing:\n%s", out)
	}
	after, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if string(after) != before {
		t.Fatalf("a refused splice must leave the plan byte-identical:\ngot  %q\nwant %q", after, before)
	}
}

// A dry run is the default for a reason: the runner hands the plan's cell to a shell, so reading a
// ledger must not execute it. The sentinel file is the proof the command never reached one.
func TestPlanAdmitDryRunRunsNothing(t *testing.T) {
	bin := buildCLI(t)
	dir := t.TempDir()
	sentinel := filepath.Join(dir, "ran")
	path := writeLedger(t, dir, "touch "+sentinel)

	out, code := runCLI(t, bin, "plan", "admit", "--path", path)
	if code != 0 {
		t.Fatalf("dry run exit = %d, want 0\n%s", code, out)
	}
	if !strings.Contains(out, "WOULD RUN") {
		t.Fatalf("a dry run must report WOULD RUN:\n%s", out)
	}
	if _, err := os.Stat(sentinel); !os.IsNotExist(err) {
		t.Fatalf("the dry run executed the command: %s exists", sentinel)
	}
}

// A row with no Admit cell is refused by name, and a refusal is exit 1: one refused row is not a clean
// run, whatever the other rows did.
func TestPlanAdmitRefusesARowWithoutACommand(t *testing.T) {
	bin := buildCLI(t)
	path := writeLedger(t, t.TempDir(), "")

	out, code := runCLI(t, bin, "plan", "admit", "--path", path)
	if code != 1 {
		t.Fatalf("exit = %d, want 1\n%s", code, out)
	}
	if !strings.Contains(out, "REFUSED") || !strings.Contains(out, "no-admit-command") {
		t.Fatalf("the refusal must be named on stdout:\n%s", out)
	}
}

// The binary accepts a whole ledger's worth of rows and re-reads the plan file rather than trusting an
// index: `--only` narrows the run. An id the ledger does not carry is a mistake, not a silent no-op,
// so `--only` refuses it the same way `--record` does.
func TestPlanAdmitOnlyKeepsTheNamedRows(t *testing.T) {
	bin := buildCLI(t)
	dir := t.TempDir()
	path := writeLedger(t, dir, "touch "+filepath.Join(dir, "ran"))

	out, code := runCLI(t, bin, "plan", "admit", "--path", path, "--only", "E1")
	if code != 0 || !strings.Contains(out, "E1") || !strings.Contains(out, "WOULD RUN") {
		t.Fatalf("--only E1 = %d\n%s", code, out)
	}
	if _, err := os.Stat(filepath.Join(dir, "ran")); !os.IsNotExist(err) {
		t.Fatal("a narrowed dry run must still run nothing")
	}
	out, code = runCLI(t, bin, "plan", "admit", "--path", path, "--only", "E9")
	if code != 2 || !strings.Contains(out, "E9") || !strings.Contains(out, "available ids") {
		t.Fatalf("--only must refuse an id the ledger does not carry = %d\n%s", code, out)
	}
}

// An id a flag names but the ledger does not carry is a mistake the user must see: it is reported on
// stderr with the plan admit: prefix, names the unknown id and the ids that do exist, and exits 2
// before the plan is touched or a command is run.
func TestPlanAdmitRefusesAnUnknownID(t *testing.T) {
	bin := buildCLI(t)
	dir := t.TempDir()
	sentinel := filepath.Join(dir, "ran")
	path := writeLedger(t, dir, "touch "+sentinel)
	before, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}

	cases := []struct {
		name string
		flag string
		args []string
	}{
		{"an unknown --record id", "--record", []string{"plan", "admit", "--path", path, "--execute", "--record", "ZZZ"}},
		{"an unknown --only id", "--only", []string{"plan", "admit", "--path", path, "--only", "ZZZ"}},
		{"an unknown id beside a known one", "--only", []string{"plan", "admit", "--path", path, "--only", "E1,ZZZ"}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			out, code := runCLI(t, bin, tc.args...)
			if code != 2 {
				t.Fatalf("exit = %d, want 2\n%s", code, out)
			}
			for _, want := range []string{"plan admit:", "ZZZ", tc.flag, "available ids", "E1"} {
				if !strings.Contains(out, want) {
					t.Fatalf("the refusal must carry %q:\n%s", want, out)
				}
			}
		})
	}
	if _, err := os.Stat(sentinel); !os.IsNotExist(err) {
		t.Fatal("a usage error ran the command anyway")
	}
	after, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if string(after) != string(before) {
		t.Fatalf("a usage error wrote the plan:\n%s", after)
	}
}

// An unescaped pipe in an Admit cell splits the row: `plan check` saw a well formed table, but the
// ledger read one command while the shell would have run another. The row is refused, not truncated.
func TestPlanAdmitRefusesAnUnescapedPipeInARow(t *testing.T) {
	bin := buildCLI(t)
	dir := t.TempDir()
	sentinel := filepath.Join(dir, "ran")
	path := filepath.Join(dir, "plan.md")
	row := "| E1 | the claim | prose | printf 'abc' | tr a-z A-Z > " + sentinel + " | none | the observation | | reverted → red | rerun it | observado |\n"
	if err := os.WriteFile(path, []byte("## Evidence ledger\n\n"+ledgerHeader+row), 0o644); err != nil {
		t.Fatal(err)
	}

	out, code := runCLI(t, bin, "plan", "admit", "--path", path, "--execute")
	if code != 1 {
		t.Fatalf("exit = %d, want 1\n%s", code, out)
	}
	if !strings.Contains(out, "malformed-row") {
		t.Fatalf("the truncated row must be refused as malformed:\n%s", out)
	}
	if strings.Contains(out, "WOULD RUN") || strings.Contains(out, "printf 'abc'") {
		t.Fatalf("a malformed row must not report a runnable command:\n%s", out)
	}
	if _, err := os.Stat(sentinel); !os.IsNotExist(err) {
		t.Fatal("the truncated cell still ran and wrote its sentinel")
	}
}

func asExit(err error, target *exec.ExitError) bool {
	ee, ok := err.(*exec.ExitError)
	if ok {
		*target = *ee
	}
	return ok
}

func TestFeedbackCLIRequiresOptInThenRecords(t *testing.T) {
	bin := buildCLI(t)
	home := t.TempDir()
	configDir := t.TempDir()
	report := filepath.Join(t.TempDir(), "report.md")
	body := "ts: 2026-09-10T12:00:00Z\n" +
		"repo: " + configDir + "\n" +
		"plan: docs/testing/test-plan.md\n" +
		"skill: tsp 0.3.6\n" +
		"build: test\n" +
		"paid: it found the defect\n" +
		"cost: one hour\n" +
		"reason: it earned its keep\n" +
		"verdict: paid\n"
	if err := os.WriteFile(report, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	out, code := runCLIWithHomeEnv(t, home, bin, "feedback", "--config-dir", configDir, "--file", report)
	wantRefusal := "feedback is disabled; enable it with: tsp feature enable feedback"
	if code == 0 || !strings.Contains(out, wantRefusal) {
		t.Fatalf("disabled feedback = %d %q", code, out)
	}
	if _, err := os.Stat(filepath.Join(sanitize.TelemetryDir(configDir), "run-feedback.jsonl")); !os.IsNotExist(err) {
		t.Fatalf("disabled feedback wrote ledger: %v", err)
	}

	if out, code = runCLIWithHomeEnv(t, home, bin, "feature", "enable", "feedback"); code != 0 {
		t.Fatalf("enable feedback = %d %q", code, out)
	}
	out, code = runCLIWithHomeEnv(t, home, bin, "feedback", "--config-dir", configDir, "--file", report)
	if code != 0 {
		t.Fatalf("enabled feedback = %d %q", code, out)
	}
	raw, err := os.ReadFile(filepath.Join(sanitize.TelemetryDir(configDir), "run-feedback.jsonl"))
	if err != nil {
		t.Fatalf("ledger: %v", err)
	}
	if rows := len(strings.Split(strings.TrimSpace(string(raw)), "\n")); rows != 1 {
		t.Fatalf("ledger rows = %d, want 1", rows)
	}
	if strings.Contains(out, "warning:") {
		t.Fatalf("a first report must not warn: %q", out)
	}

	// The same report again is recorded, and the operator hears that it repeats the last one.
	out, code = runCLIWithHomeEnv(t, home, bin, "feedback", "--config-dir", configDir, "--file", report)
	if code != 0 || !strings.Contains(out, "warning: this report repeats the repository, plan and verdict of the last report") {
		t.Fatalf("repeated feedback = %d %q", code, out)
	}
	raw, err = os.ReadFile(filepath.Join(sanitize.TelemetryDir(configDir), "run-feedback.jsonl"))
	if err != nil {
		t.Fatalf("ledger: %v", err)
	}
	if rows := len(strings.Split(strings.TrimSpace(string(raw)), "\n")); rows != 2 {
		t.Fatalf("ledger rows = %d, want 2: a warning never refuses", rows)
	}
}

// The feedback command is the destination the gate's offer always lacked: --template prints a
// skeleton, --file records it, and no flags reads the reports back.

func TestDefaultConfigDirUsesRunnerEnvironment(t *testing.T) {
	home := t.TempDir()
	tests := []struct {
		name   string
		claude string
		pi     string
		want   string
	}{
		{
			name:   "CLAUDE_CONFIG_DIR wins and is trimmed",
			claude: "  " + filepath.Join(home, "claude") + "  ",
			pi:     filepath.Join(home, "pi"),
			want:   filepath.Join(home, "claude"),
		},
		{
			name:   "only CLAUDE_CONFIG_DIR",
			claude: filepath.Join(home, "claude"),
			want:   filepath.Join(home, "claude"),
		},
		{
			name: "only PI_CODING_AGENT_DIR and trimmed",
			pi:   "  " + filepath.Join(home, "pi") + "  ",
			want: filepath.Join(home, "pi"),
		},
		{
			name:   "empty CLAUDE_CONFIG_DIR uses PI_CODING_AGENT_DIR",
			claude: " \t ",
			pi:     filepath.Join(home, "pi"),
			want:   filepath.Join(home, "pi"),
		},
		{
			name: "neither uses HOME",
			want: filepath.Join(home, ".claude"),
		},
		{
			name:   "empty values use HOME",
			claude: " \t ",
			pi:     "\n",
			want:   filepath.Join(home, ".claude"),
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Setenv("HOME", home)
			t.Setenv("CLAUDE_CONFIG_DIR", tt.claude)
			t.Setenv("PI_CODING_AGENT_DIR", tt.pi)
			if got := defaultConfigDir(); got != tt.want {
				t.Fatalf("defaultConfigDir() = %q, want %q", got, tt.want)
			}
		})
	}
}

// The feedback command is the destination the gate's offer always lacked: --template prints a
// skeleton, --file records it, and no flags reads the reports back.
func TestFeedbackCLI(t *testing.T) {
	bin := buildCLI(t)
	dir := t.TempDir()

	out, code := runCLI(t, bin, "feedback", "--config-dir", dir, "--template")
	if code != 0 {
		t.Fatalf("template exit = %d\n%s", code, out)
	}
	for _, want := range []string{"ts:", "repo:", "plan:", "skill:", "build:", "--file", "verdict"} {
		if !strings.Contains(out, want) {
			t.Fatalf("template missing %q:\n%s", want, out)
		}
	}
	if _, err := os.Stat(sanitize.TelemetryDir(dir)); !os.IsNotExist(err) {
		t.Fatalf("--template must write nothing, found a telemetry directory")
	}

	// No subcommand is the cheapest path to the answer.
	out, code = runCLI(t, bin, "feedback", "--config-dir", dir)
	if code != 0 || !strings.Contains(strings.ToLower(out), "no reports") {
		t.Fatalf("bare feedback = %d %q", code, out)
	}

	report := filepath.Join(t.TempDir(), "report.md")
	body := "ts: 2026-09-10T12:00:00Z\n" +
		"repo: " + dir + "\n" +
		"plan: docs/testing/test-plan.md\n" +
		"skill: tsp 0.3.11\n" +
		"build: test\n" +
		"paid: it found the defect\n" +
		"cost: one hour\n" +
		"reason: it earned its keep\n" +
		"verdict: paid\n" +
		"guess: what a probe proves\n"
	if err := os.WriteFile(report, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	stateHome := t.TempDir()
	if out, code := runCLIWithHomeEnv(t, stateHome, bin, "feature", "enable", "feedback"); code != 0 {
		t.Fatalf("enable feedback exit = %d\n%s", code, out)
	}
	out, code = runCLIWithHomeEnv(t, stateHome, bin, "feedback", "--config-dir", dir, "--file", report)
	if code != 0 {
		t.Fatalf("submit exit = %d\n%s", code, out)
	}
	if !strings.Contains(out, "recorded paid feedback for "+dir) || strings.Contains(out, "repo-") {
		t.Fatalf("submit must resolve the local repository pseudonym: %q", out)
	}
	out, code = runCLI(t, bin, "feedback", "--config-dir", dir, "--summary")
	if code != 0 || !strings.Contains(out, "1 report") || !strings.Contains(out, "paid: 1") {
		t.Fatalf("summary after one report = %d\n%s", code, out)
	}

	bad := filepath.Join(t.TempDir(), "bad.md")
	if err := os.WriteFile(bad, []byte(body+"surprise: x\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	out, code = runCLI(t, bin, "feedback", "--config-dir", dir, "--file", bad)
	if code != 2 || !strings.Contains(out, "surprise") {
		t.Fatalf("unknown key = %d %q", code, out)
	}

	if err := os.WriteFile(bad, []byte(strings.Replace(body, "verdict: paid", "verdict: maybe", 1)), 0o644); err != nil {
		t.Fatal(err)
	}
	out, code = runCLI(t, bin, "feedback", "--config-dir", dir, "--file", bad)
	if code != 2 || !strings.Contains(out, "paid") || !strings.Contains(out, "partly") || !strings.Contains(out, "ceremony") {
		t.Fatalf("bad verdict = %d %q", code, out)
	}

	if err := os.WriteFile(bad, []byte(strings.Replace(body, "cost: one hour\n", "", 1)), 0o644); err != nil {
		t.Fatal(err)
	}
	out, code = runCLI(t, bin, "feedback", "--config-dir", dir, "--file", bad)
	if code != 2 || !strings.Contains(out, "cost") {
		t.Fatalf("missing field = %d %q", code, out)
	}

	// Every refusal wrote nothing: the ledger still holds the one accepted report.
	raw, err := os.ReadFile(filepath.Join(sanitize.TelemetryDir(dir), "run-feedback.jsonl"))
	if err != nil {
		t.Fatal(err)
	}
	if rows := strings.Count(strings.TrimRight(string(raw), "\n"), "\n") + 1; rows != 1 {
		t.Fatalf("a refusal must write nothing, ledger has %d rows:\n%s", rows, raw)
	}
}

func TestFeedbackPendingCLIReviewLifecycle(t *testing.T) {
	bin := buildCLI(t)
	home := t.TempDir()
	configDir := t.TempDir()
	run := func(args ...string) (string, int) {
		t.Helper()
		return runCLIWithHomeEnv(t, home, bin, args...)
	}

	if out, code := run("feature", "enable", "feedback"); code != 0 {
		t.Fatalf("enable feedback exit = %d\n%s", code, out)
	}

	type reportFixture struct {
		ts, repo, plan, skill, build string
		paid, cost, reason, verdict  string
		guess, freeform              string
	}
	writeReport := func(r reportFixture) string {
		t.Helper()
		path := filepath.Join(t.TempDir(), "report.md")
		body := strings.Join([]string{
			"ts: " + r.ts,
			"repo: " + r.repo,
			"plan: " + r.plan,
			"skill: " + r.skill,
			"build: " + r.build,
			"paid: " + r.paid,
			"cost: " + r.cost,
			"reason: " + r.reason,
			"verdict: " + r.verdict,
			"guess: " + r.guess,
			"freeform: " + r.freeform,
		}, "\n") + "\n"
		if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
			t.Fatal(err)
		}
		out, code := run("feedback", "--config-dir", configDir, "--file", path)
		if code != 0 {
			t.Fatalf("submit feedback exit = %d\n%s", code, out)
		}
		return path
	}
	fixture := func(id string) reportFixture {
		return reportFixture{
			ts: "2026-09-10T12:00:00Z", repo: "/work/repo-" + id, plan: "docs/testing/" + id + ".md",
			skill: "tsp 0.3.11", build: "build-" + id, paid: "paid-" + id,
			cost: "cost-" + id, reason: "reason-" + id, verdict: "paid",
			guess: "guess-" + id, freeform: "freeform-" + id,
		}
	}
	assertFields := func(out string, r reportFixture) {
		t.Helper()
		for _, field := range []struct{ name, value string }{
			{"ts", r.ts}, {"repo", r.repo}, {"plan", r.plan}, {"skill", r.skill}, {"build", r.build},
			{"paid", r.paid}, {"cost", r.cost}, {"reason", r.reason}, {"verdict", r.verdict},
			{"guess", r.guess}, {"freeform", r.freeform},
		} {
			if !strings.Contains(out, field.name+": "+field.value) {
				t.Errorf("pending report missing %s field %q:\n%s", field.name, field.value, out)
			}
		}
	}
	pendingToken := func(out string) string {
		t.Helper()
		const prefix = "review token: "
		var token string
		for _, line := range strings.Split(out, "\n") {
			if strings.HasPrefix(line, prefix) {
				if token != "" {
					t.Fatalf("pending report printed more than one review token:\n%s", out)
				}
				token = strings.TrimPrefix(line, prefix)
				if token == "" || strings.TrimSpace(token) != token {
					t.Fatalf("invalid review token line %q", line)
				}
			}
		}
		if token == "" || !strings.Contains(out, prefix+token) {
			t.Fatalf("pending report missing exact %q line:\n%s", prefix+token, out)
		}
		return token
	}

	historical := []reportFixture{fixture("historical-one"), fixture("historical-two")}
	for _, report := range historical {
		writeReport(report)
	}
	out, code := run("feedback", "--config-dir", configDir, "--pending")
	if code != 0 {
		t.Fatalf("initial pending baseline exit = %d\n%s", code, out)
	}
	for _, report := range historical {
		for _, marker := range []string{report.repo, report.plan, report.paid, report.freeform} {
			if strings.Contains(out, marker) {
				t.Errorf("initial --pending printed historical report marker %q:\n%s", marker, out)
			}
		}
	}

	out, code = run("feedback", "--config-dir", configDir, "--summary")
	if code != 0 || !strings.Contains(out, "run feedback: 2 report(s)") {
		t.Fatalf("summary after baseline = %d\n%s", code, out)
	}

	first := fixture("pending-one")
	firstFile := writeReport(first)
	out, code = run("feedback", "--config-dir", configDir, "--summary")
	if code != 0 || !strings.Contains(out, "run feedback: 3 report(s)") {
		t.Fatalf("all-history summary after append = %d\n%s", code, out)
	}
	out, code = run("feedback", "--config-dir", configDir, "--pending")
	if code != 0 {
		t.Fatalf("pending after summary exit = %d\n%s", code, out)
	}
	assertFields(out, first)
	firstToken := pendingToken(out)

	for _, tc := range []struct {
		name string
		args []string
	}{
		{name: "pending with summary", args: []string{"--pending", "--summary"}},
		{name: "pending with file", args: []string{"--pending", "--file", firstFile}},
		{name: "pending with acknowledgement", args: []string{"--pending", "--mark-reviewed", firstToken}},
		{name: "acknowledgement with summary", args: []string{"--mark-reviewed", firstToken, "--summary"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			args := append([]string{"feedback", "--config-dir", configDir}, tc.args...)
			out, code := run(args...)
			if code != 2 {
				t.Fatalf("incompatible actions exit = %d, want 2\n%s", code, out)
			}
		})
	}

	late := fixture("pending-two")
	writeReport(late)
	for _, args := range [][]string{
		{"feedback", "--config-dir", configDir, "--mark-reviewed", firstToken},
		{"feedback", "--config-dir", configDir, "--mark-reviewed", firstToken},
	} {
		if out, code := run(args...); code != 0 {
			t.Fatalf("acknowledge/replay token exit = %d\n%s", code, out)
		}
	}

	out, code = run("feedback", "--config-dir", configDir, "--pending")
	if code != 0 {
		t.Fatalf("pending after prefix acknowledgement exit = %d\n%s", code, out)
	}
	assertFields(out, late)
	for _, marker := range []string{first.repo, first.plan, first.paid, first.freeform} {
		if strings.Contains(out, marker) {
			t.Errorf("acknowledged report still pending as %q:\n%s", marker, out)
		}
	}
	lateToken := pendingToken(out)
	if lateToken == firstToken {
		t.Fatalf("token after later append = %q, want a new prefix token", lateToken)
	}
	if out, code := run("feedback", "--config-dir", configDir, "--mark-reviewed", lateToken); code != 0 {
		t.Fatalf("final acknowledgement exit = %d\n%s", code, out)
	}

	out, code = run("feedback", "--config-dir", configDir, "--pending")
	if code != 0 {
		t.Fatalf("pending after final acknowledgement exit = %d\n%s", code, out)
	}
	for _, marker := range []string{first.repo, first.plan, first.paid, first.freeform, late.repo, late.plan, late.paid, late.freeform, "review token: "} {
		if strings.Contains(out, marker) {
			t.Errorf("pending queue was not empty; found %q:\n%s", marker, out)
		}
	}
}

func TestTopLevelSynopsisListsFeedbackReviewActions(t *testing.T) {
	bin := buildCLI(t)
	home := t.TempDir()
	out, code := runCLIWithHomeEnv(t, home, bin)
	if code != 2 {
		t.Fatalf("tsp without a command exit = %d, want 2\n%s", code, out)
	}
	var feedbackSynopsis string
	for _, line := range strings.Split(out, "\n") {
		if strings.HasPrefix(line, "feedback [") {
			feedbackSynopsis = line
			break
		}
	}
	for _, want := range []string{"--pending", "--mark-reviewed <token>"} {
		if !strings.Contains(feedbackSynopsis, want) {
			t.Errorf("feedback synopsis missing %q:\n%s", want, out)
		}
	}
}

func TestFeedbackCLISanitizesPersistedSecretsAndFailsClosed(t *testing.T) {
	bin := buildCLI(t)
	configDir := t.TempDir()
	rawRepo := "/home/someone/acme-billing-repo"
	projectName := "acme-billing-repo"
	token := "ghp_1234567890abcdef1234"
	report := filepath.Join(t.TempDir(), "report.md")
	body := "ts: 2026-09-10T12:00:00Z\n" +
		"repo: " + rawRepo + "\n" +
		"plan: docs/testing/test-plan.md\n" +
		"skill: tsp 0.3.6\n" +
		"build: test\n" +
		"paid: it found the defect\n" +
		"cost: one hour\n" +
		"reason: GET /api/invoices returned 500\n" +
		"verdict: paid\n" +
		"freeform: credential observed: " + token + "\n"
	if err := os.WriteFile(report, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	stateHome := t.TempDir()
	if out, code := runCLIWithHomeEnv(t, stateHome, bin, "feature", "enable", "feedback"); code != 0 {
		t.Fatalf("enable feedback exit = %d\n%s", code, out)
	}
	if out, code := runCLIWithHomeEnv(t, stateHome, bin, "feedback", "--config-dir", configDir, "--file", report); code != 0 {
		t.Fatalf("submit exit = %d\n%s", code, out)
	}

	telemetryDir := sanitize.TelemetryDir(configDir)
	jsonlPath := filepath.Join(telemetryDir, "run-feedback.jsonl")
	markdownPath := filepath.Join(telemetryDir, "run-feedback.md")
	jsonl, err := os.ReadFile(jsonlPath)
	if err != nil {
		t.Fatal(err)
	}
	markdown, err := os.ReadFile(markdownPath)
	if err != nil {
		t.Fatal(err)
	}
	for _, artifact := range []string{string(jsonl), string(markdown)} {
		for _, forbidden := range []string{rawRepo, projectName, token} {
			if strings.Contains(artifact, forbidden) {
				t.Fatalf("persisted feedback contains raw %q: %s", forbidden, artifact)
			}
		}
	}

	lines := strings.Split(strings.TrimSpace(string(jsonl)), "\n")
	if len(lines) != 1 {
		t.Fatalf("accepted submission wrote %d JSONL rows, want 1:\n%s", len(lines), jsonl)
	}
	var recorded struct {
		Repo      string `json:"repo"`
		Freeform  string `json:"freeform"`
		Sanitized bool   `json:"sanitized"`
	}
	if err := json.Unmarshal([]byte(lines[0]), &recorded); err != nil {
		t.Fatalf("JSONL row: %v", err)
	}
	if !strings.HasPrefix(recorded.Repo, "repo-") || !recorded.Sanitized {
		t.Fatalf("recorded identity = %+v, want repo pseudonym and sanitized marker", recorded)
	}
	if !strings.Contains(recorded.Freeform, sanitize.Redacted) || strings.Contains(recorded.Freeform, token) {
		t.Fatalf("recorded freeform = %q, want redaction without token", recorded.Freeform)
	}

	entries, err := os.ReadDir(telemetryDir)
	if err != nil {
		t.Fatal(err)
	}
	saltCount, mapCount := 0, 0
	for _, entry := range entries {
		switch entry.Name() {
		case ".salt":
			saltCount++
			if entry.IsDir() {
				t.Fatal(".salt is a directory")
			}
		case ".pseudonyms.jsonl":
			mapCount++
			if entry.IsDir() {
				t.Fatal(".pseudonyms.jsonl is a directory")
			}
		}
	}
	if saltCount != 1 || mapCount != 1 {
		t.Fatalf("telemetry entries have %d .salt and %d .pseudonyms.jsonl; want exactly one of each", saltCount, mapCount)
	}
	if _, err := os.Stat(filepath.Join(telemetryDir, "telemetry")); !os.IsNotExist(err) {
		t.Fatalf("nested telemetry directory = %v, want absent", err)
	}
	if got, ok := sanitize.Resolve(telemetryDir, recorded.Repo); !ok || got != rawRepo {
		t.Fatalf("Resolve(%q) = %q, %t; want original repository path", recorded.Repo, got, ok)
	}

	badReport := filepath.Join(t.TempDir(), "bad-report.md")
	badBody := strings.Replace(body, "reason: GET /api/invoices returned 500", "reason: leaked "+token, 1)
	if err := os.WriteFile(badReport, []byte(badBody), 0o644); err != nil {
		t.Fatal(err)
	}
	if out, code := runCLI(t, bin, "feedback", "--config-dir", configDir, "--file", badReport); code == 0 {
		t.Fatalf("token in required reason was accepted: %s", out)
	}
	unchanged, err := os.ReadFile(jsonlPath)
	if err != nil {
		t.Fatal(err)
	}
	if got := len(strings.Split(strings.TrimSpace(string(unchanged)), "\n")); got != 1 {
		t.Fatalf("fail-closed submission changed ledger row count to %d:\n%s", got, unchanged)
	}
}

func runCLIWithHomeEnv(t *testing.T, home, bin string, args ...string) (string, int) {
	t.Helper()
	return runCLIEnv(t, bin, []string{"TPP_HOME=" + home}, args...)
}

// runCLIEnv runs the binary with extra environment entries; the last entry for a key wins, so a
// test can override PATH or point the update check at a local proxy.
func runCLIEnv(t *testing.T, bin string, env []string, args ...string) (string, int) {
	t.Helper()
	cmd := exec.Command(bin, args...)
	cmd.Env = append(os.Environ(), env...)
	out, err := cmd.CombinedOutput()
	if err == nil {
		return string(out), 0
	}
	var ee exec.ExitError
	if asExit(err, &ee) {
		return string(out), ee.ExitCode()
	}
	t.Fatalf("run %v: %v", args, err)
	return "", -1
}

func runCLI(t *testing.T, bin string, args ...string) (string, int) {
	t.Helper()
	cmd := exec.Command(bin, args...)
	out, err := cmd.CombinedOutput()
	if err == nil {
		return string(out), 0
	}
	var ee exec.ExitError
	if asExit(err, &ee) {
		return string(out), ee.ExitCode()
	}
	t.Fatalf("run %v: %v", args, err)
	return "", -1
}

// runCLIWithoutStdout runs the binary with its stdout on a device that refuses every write, and returns what
// it said on stderr plus its exit code. A CLI whose machine-readable output vanished must not exit 0: the
// consumer parses a truncated document and the exit code says it went fine.
func runCLIWithoutStdout(t *testing.T, bin string, args ...string) (string, int) {
	t.Helper()
	refuses, err := os.OpenFile("/dev/full", os.O_WRONLY, 0)
	if err != nil {
		t.Skipf("no device here that refuses writes: %v", err)
	}
	defer refuses.Close()
	cmd := exec.Command(bin, args...)
	cmd.Stdout = refuses
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	err = cmd.Run()
	if err == nil {
		return stderr.String(), 0
	}
	var ee exec.ExitError
	if asExit(err, &ee) {
		return stderr.String(), ee.ExitCode()
	}
	t.Fatalf("run %v: %v", args, err)
	return "", -1
}

// The doctor's `--json` report is the machine-readable half of the command, and it goes to stdout. A write
// that fails there used to be dropped: with a healthy configuration the tool delivered nothing, said nothing,
// and returned 0, so a caller could not tell a report from a fragment of one — or from no report at all. It now
// names the failure on stderr and returns the artifact code.
//
// The configuration is synced first on purpose: a healthy doctor is the case where the old exit code was 0 for
// a report nobody received, and an unhealthy one returns 1 for a reason that has nothing to do with the write.
func TestDoctorJSONReportsTheReportItCouldNotWrite(t *testing.T) {
	bin := buildCLI(t)
	configDir := t.TempDir()
	if got, code := runCLI(t, bin, "sync", "--config-dir", configDir); code != 0 {
		t.Fatalf("sync exited %d, so the test's premise (a healthy doctor) does not hold\n%s", code, got)
	}
	stderr, code := runCLIWithoutStdout(t, bin, "doctor", "--json", "--config-dir", configDir)
	if code != exitArtifact {
		t.Fatalf("code = %d, want %d: the JSON report was not written\nstderr: %s", code, exitArtifact, stderr)
	}
	if !strings.Contains(stderr, "doctor:") {
		t.Fatalf("the failure must name the command whose output was lost:\n%s", stderr)
	}
}

// A usage text that does not list a command it accepts sends users to the wrong place.
func TestUsageListsEveryBenchSubcommand(t *testing.T) {
	for _, sub := range []string{"bench run", "bench score", "bench history", "bench compare", "bench rescore", "plan init", "plan check", "plan gaps", "plan upgrade", "plan add-finding", "plan admit"} {

		if !strings.Contains(usage, sub) {
			t.Errorf("usage does not document %q", sub)
		}
	}
	for _, cmd := range []string{"gate", "sync", "doctor", "bench", "plan", "feedback", "version", "tui", "update"} {
		if !strings.Contains(usage, "  "+cmd+" ") {
			t.Errorf("usage does not document the %q command", cmd)
		}
	}
}

// The version command names the build: the release and the commit behind it. A bare literal
// would say nothing about which build is installed, so the shape is the contract. The release is
// derived from buildinfo.Version rather than repeated here: a version bump moves the binary and
// the expectation together, so the suite documents the shape without pinning a number to bump.
func TestVersionNamesTheBuild(t *testing.T) {
	bin := buildCLI(t)
	out, err := exec.Command(bin, "version").CombinedOutput()
	if err != nil {
		t.Fatalf("version: %v\n%s", err, out)
	}
	got := strings.TrimSpace(string(out))
	want := regexp.MustCompile("^" + regexp.QuoteMeta(buildinfo.Version) + ` \(([0-9a-f]{7}(\+dirty)?|unknown)\)$`)
	if !want.MatchString(got) {
		t.Fatalf("version printed %q, want %s", got, want)
	}
}

// The probe runs the wired Stop command, so a command it cannot read must be refused before anything is
// run: an unterminated quote used to be an error the probe turned into the doctor's own verdict, and it
// must not become an attempt to execute the fragment. The split-rule cases that used to live here belong
// to the one splitter, hookcmd.ShellWords, and are tested in internal/doctor.
func TestProbeHookRefusesACommandItCannotRead(t *testing.T) {
	for _, command := range []string{"", "   \t", `"unbalanced`, `'unbalanced`, `/h/bin/tpp "gate`} {
		err := probeHook(command)
		if err == nil {
			t.Fatalf("probeHook(%q) must refuse the command rather than run a fragment", command)
		}
		if !strings.Contains(err.Error(), "cannot read the wired command") {
			t.Fatalf("probeHook(%q) = %v, want the probe's own verdict", command, err)
		}
	}
}

// Reading the command with the shared rule must not change the command that gets executed: the probe has
// to keep running the double-quoted path sync writes, and keep reporting a wiring that exits non-zero.
func TestProbeHookRunsTheWiredCommand(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "Program Files")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	write := func(name, body string) string {
		t.Helper()
		p := filepath.Join(dir, name)
		if err := os.WriteFile(p, []byte(body), 0o755); err != nil {
			t.Fatal(err)
		}
		return p
	}
	if err := probeHook(`"` + write("gate", "#!/bin/sh\nexit 0\n") + `"`); err != nil {
		t.Fatalf("a quoted path with a space that answers must pass the probe: %v", err)
	}
	if err := probeHook(`"` + write("broken", "#!/bin/sh\nexit 3\n") + `"`); err == nil {
		t.Fatal("a wired command that exits non-zero must fail the probe")
	}
}

const cliFindingPlan = `## Findings

| Id | Finding | Severity | Data safe? | Evidence id | Pinning test | Status | Verdict by / date | Reason | Fingerprint |
|---|---|---|---|---|---|---|---|---|---|

## Evidence ledger

| Id | Claim |
|---|---|
| E1 | observed |
`

func cliFindingArgs(path, id, evidence string, status ...string) []string {
	findingStatus := "open"
	if len(status) > 0 {
		findingStatus = status[0]
	}
	return []string{
		"plan", "add-finding", "--path", path,
		"--id", id, "--location", "src/a.go:1", "--severity", "bug",
		"--data-safe", "yes", "--evidence", evidence, "--test", "",
		"--status", findingStatus, "--verdict-by", "me / 2026-09-10", "--reason", "not pinned",
		"--fingerprint", "",
	}
}

func TestPlanAddFindingCLI(t *testing.T) {
	bin := buildCLI(t)
	path := filepath.Join(t.TempDir(), "test-plan.md")
	if err := os.WriteFile(path, []byte(cliFindingPlan), 0o644); err != nil {
		t.Fatal(err)
	}

	out, code := runCLI(t, bin, cliFindingArgs(path, "F1", "E1")...)
	if code != 0 || !strings.Contains(out, "added F1") {
		t.Fatalf("successful insertion = %d %q", code, out)
	}
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(raw), "| F1 | src/a.go:1 | bug | yes | E1 |  | open | me / 2026-09-10 | not pinned | - |") {
		t.Fatalf("inserted row missing:\n%s", raw)
	}
}

func TestPlanAddFindingUsageRefusalExits2(t *testing.T) {
	bin := buildCLI(t)
	path := filepath.Join(t.TempDir(), "not-created.md")
	out, code := runCLI(t, bin, cliFindingArgs(path, "", "E1")...)
	if code != 2 || !strings.Contains(out, "--id") {
		t.Fatalf("missing required value = %d %q", code, out)
	}
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Fatalf("usage refusal must not create the plan: %v", err)
	}
}

func TestPlanAddFindingHelp(t *testing.T) {
	bin := buildCLI(t)
	out, code := runCLI(t, bin, "plan", "add-finding", "--help")
	if code != 2 {
		t.Fatalf("help exit = %d, want 2\n%s", code, out)
	}
	for _, want := range []string{"-id", "-location", "-data-safe", "-fingerprint"} {
		if !strings.Contains(out, want) {
			t.Errorf("help missing %q:\n%s", want, out)
		}
	}
}

// A plan git ignores can never reach a pull request: the subcommands that write the plan refuse an ignored,
// unstaged path by name instead of silently persisting evidence nobody will review, the refusal clears once the
// path is staged (git add -f, the workaround the field had to find by hand), usage errors keep their exit 2,
// and the read subcommands are not refused.
func TestPlanWritesRefuseAPathGitIgnores(t *testing.T) {
	bin := buildCLI(t)
	repo := t.TempDir()
	if out, err := exec.Command("git", "init", "-q", repo).CombinedOutput(); err != nil {
		t.Fatalf("git init: %v\n%s", err, out)
	}
	ignore := filepath.Join(repo, ".gitignore")
	if err := os.WriteFile(ignore, []byte("docs/\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	run := func(args ...string) (int, string) {
		cmd := exec.Command(bin, args...)
		cmd.Dir = repo
		out, err := cmd.CombinedOutput()
		code := 0
		ee := &exec.ExitError{}
		if err != nil {
			if asExit(err, ee) {
				code = ee.ExitCode()
			} else {
				t.Fatalf("run %v: %v", args, err)
			}
		}
		return code, string(out)
	}
	refused := func(args ...string) {
		t.Helper()
		code, out := run(args...)
		if code != 1 || !strings.Contains(out, "git ignores docs/testing/test-plan.md") {
			t.Fatalf("%v on an ignored path = %d\n%s", args, code, out)
		}
	}

	// The write is refused before anything lands on disk, and the message names the path.
	code, out := run("plan", "init", "--path", "docs/testing/test-plan.md")
	if code != 1 || !strings.Contains(out, "plan init: git ignores docs/testing/test-plan.md") {
		t.Fatalf("plan init on an ignored path = %d\n%s", code, out)
	}
	if _, err := os.Stat(filepath.Join(repo, "docs/testing/test-plan.md")); !os.IsNotExist(err) {
		t.Fatalf("the refused init must not write the plan: %v", err)
	}

	// A missing required flag is still a usage error, not the refusal: nothing has been read yet.
	code, out = run("plan", "add-finding", "--path", "docs/testing/test-plan.md", "--id", "F1")
	if code != 2 {
		t.Fatalf("a usage error outranks the refusal: %d\n%s", code, out)
	}

	// The rule arriving after the plan exists is the field's situation: every write is still refused.
	if err := os.WriteFile(ignore, nil, 0o644); err != nil {
		t.Fatal(err)
	}
	if code, out := run("plan", "init", "--path", "docs/testing/test-plan.md"); code != 0 {
		t.Fatalf("plan init = %d\n%s", code, out)
	}
	if err := os.WriteFile(ignore, []byte("docs/\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	refused(cliFindingArgs("docs/testing/test-plan.md", "F1", "E1")...)
	refused("plan", "upgrade", "--path", "docs/testing/test-plan.md")

	// A recording admit writes the plan too, so it is refused the same way; --record without
	// --execute is still the usage error it always was.
	code, out = run("plan", "admit", "--path", "docs/testing/test-plan.md", "--execute", "--record", "E1")
	if code != 1 || !strings.Contains(out, "plan admit: git ignores docs/testing/test-plan.md") {
		t.Fatalf("plan admit --record on an ignored path = %d\n%s", code, out)
	}
	if code, out := run("plan", "admit", "--path", "docs/testing/test-plan.md", "--record", "E1"); code != 2 {
		t.Fatalf("--record without --execute = %d, want 2\n%s", code, out)
	}

	// Reads are not refused: plan gaps on the same ignored plan still answers for itself.
	if _, out := run("plan", "gaps", "--path", "docs/testing/test-plan.md"); strings.Contains(out, "git ignores") {
		t.Fatalf("plan gaps must not be refused:\n%s", out)
	}

	// Staged with git add -f — the workaround the field had to find by hand — the writes pass.
	if out, err := exec.Command("git", "-C", repo, "add", "-f", "docs/testing/test-plan.md").CombinedOutput(); err != nil {
		t.Fatalf("git add -f: %v\n%s", err, out)
	}
	if code, out := run("plan", "add-evidence", "--path", "docs/testing/test-plan.md", "--id", "E1",
		"--claim", "observed", "--executed", "ran it", "--observed", "ok"); code != 0 {
		t.Fatalf("plan add-evidence after git add -f = %d\n%s", code, out)
	}
	if code, out := run(cliFindingArgs("docs/testing/test-plan.md", "F1", "E1")...); code != 0 || !strings.Contains(out, "added F1") {
		t.Fatalf("plan add-finding after git add -f = %d\n%s", code, out)
	}
}

// A docker binary that is not there is not a failing row: nothing starts, so no container ever exists and the
// refusal has to name the sandbox rather than the command. exec reports a binary it could not find or start as
// an *exec.Error, while a docker that ran and failed reports an *exec.ExitError, which is the line this test
// pins. Nothing here starts a container.
func TestSandboxRunnerNamesADockerThatIsNotOnPath(t *testing.T) {
	t.Setenv("PATH", t.TempDir()) // an empty directory: there is no docker to find
	run := sandboxRunner(sandboxImageDefault, admit.SandboxReadOnly)
	_, err := run(context.Background(), t.TempDir(), "printf one")
	var refusal evidence.Refusal
	if !errors.As(err, &refusal) || refusal.Reason != evidence.ReasonMisconfigured {
		t.Fatalf("run = %v, want a %s refusal", err, evidence.ReasonMisconfigured)
	}
	if !strings.Contains(refusal.Detail, "docker") {
		t.Fatalf("detail = %q, want it to name the missing docker", refusal.Detail)
	}
}

// The plan add-finding refusal split: a malformed invocation exits 2 — the class `feedback` and
// `bench score` already use — and a plan that refuses the row exits 1, the `plan check` class. One
// case per refusal, and every refusal must leave the plan byte-for-byte unchanged.
const cliFindingHeader = "## Findings\n\n| Id | Finding | Severity | Data safe? | Evidence id | Pinning test | Status | Verdict by / date | Reason | Fingerprint |\n|---|---|---|---|---|---|---|---|---|---|\n"

const cliEvidenceLedger = "\n## Evidence ledger\n\n| Id | Claim |\n|---|---|\n| E1 | c |\n"

// findingArgs builds the happy-path invocation, with one flag overridden per case. An override to the
// empty string is what the CLI sees for `--flag ""`.
func findingArgs(path string, over map[string]string) []string {
	values := map[string]string{
		"id": "F1", "location": "src/a.js:5", "severity": "data loss", "data-safe": "yes",
		"evidence": "E1", "test": "", "status": "open", "verdict-by": "me / 2026-09-10",
		"reason": "not pinned", "fingerprint": "",
	}
	for k, v := range over {
		values[k] = v
	}
	args := []string{"plan", "add-finding", "--path", path}
	for _, k := range []string{"id", "location", "severity", "data-safe", "evidence", "test", "status", "verdict-by", "reason", "fingerprint"} {
		args = append(args, "--"+k, values[k])
	}
	return args
}

// seedLedgerRow adds one evidence row to a fixture by hand: the command under test writes findings
// only, and a finding citing evidence the plan does not carry is refused rather than propped up.
func seedLedgerRow(t *testing.T, doc string) string {
	t.Helper()
	lines := strings.Split(doc, "\n")
	for i, l := range lines {
		if strings.HasPrefix(l, "| Id | Claim") {
			row := "| E1 | it drops the comma | `node --test` | `a,b` | 3 fields | reverted -> red | same input | observado |"
			// The separator follows the header, so the new row goes after it.
			rest := append([]string{lines[i+1], row}, lines[i+2:]...)
			return strings.Join(append(lines[:i+1], rest...), "\n")
		}
	}
	t.Fatal("the fixture moved: the Evidence ledger header was not found")
	return ""
}

func TestPlanAddFindingExitCodes(t *testing.T) {
	bin := buildCLI(t)
	valid := cliFindingHeader + cliEvidenceLedger
	cases := []struct {
		name     string
		plan     string
		over     map[string]string
		missing  bool // the plan path does not exist: the plan cannot be read
		wantExit int
		wantOut  string
	}{
		// Exit 2: the invocation's own data is wrong.
		{name: "an empty required flag", plan: valid, over: map[string]string{"id": ""}, wantExit: 2, wantOut: "--id"},
		{name: "an empty severity", plan: valid, over: map[string]string{"severity": ""}, wantExit: 2, wantOut: "--severity"},
		{name: "a status outside the vocabulary", plan: valid, over: map[string]string{"status": "resolved"}, wantExit: 2, wantOut: "open, confirmed, fixed, gap-closed, rejected, wontfix"},
		{name: "an empty status", plan: valid, over: map[string]string{"status": ""}, wantExit: 2, wantOut: "--status"},
		{name: "a settled status with no pinning test", plan: valid, over: map[string]string{"status": "confirmed"}, wantExit: 2, wantOut: "--test"},
		{name: "a settled status with a placeholder pinning test", plan: valid, over: map[string]string{"status": "fixed", "test": "-"}, wantExit: 2, wantOut: "--test"},
		{name: "a value carrying a newline", plan: valid, over: map[string]string{"reason": "first\nsecond"}, wantExit: 2, wantOut: "newline"},
		{name: "a placeholder id", plan: valid, over: map[string]string{"id": "-"}, wantExit: 2, wantOut: "placeholder"},
		{name: "a location that is not path:line", plan: valid, over: map[string]string{"location": "src/a.js"}, wantExit: 2, wantOut: "path:line"},

		// Exit 1: the plan refuses the row.
		{name: "the plan cannot be read", plan: valid, missing: true, wantExit: 1, wantOut: "add-finding"},
		{name: "no Findings section", plan: cliEvidenceLedger, wantExit: 1, wantOut: "no ## Findings section"},
		{name: "a Findings section that is not a table", plan: "## Findings\n\nprose, not a table\n\n" + cliEvidenceLedger, wantExit: 1, wantOut: "no table"},
		{name: "a Findings table cut by prose", plan: cliFindingHeader + "| F0 | `src/b.js:9` | M | yes | E1 |  | open | me | - | - |\n" + "a sentence that closes the table\n" + "| F2 | `src/c.js:1` | M | yes | E1 |  | open | me | - | - |\n" + cliEvidenceLedger, wantExit: 1, wantOut: "interrupted"},
		{name: "a Findings region ending inside a fence", plan: cliFindingHeader + "\n```markdown\n| Id | Finding |\n|---|---|\n", wantExit: 1, wantOut: "code fence opened"},
		{name: "a header with no Id column", plan: "## Findings\n\n| Finding | Severity | Data safe? | Evidence id | Pinning test | Status | Verdict by / date | Reason | Fingerprint |\n|---|---|---|---|---|---|---|---|---|\n" + cliEvidenceLedger, wantExit: 1, wantOut: "no Id column"},
		{name: "a header without a column the row needs", plan: "## Findings\n\n| Id | Finding | Data safe? | Evidence id | Status | Verdict by / date | Reason | Fingerprint |\n|---|---|---|---|---|---|---|---|\n" + cliEvidenceLedger, wantExit: 1, wantOut: "Severity"},
		{name: "a duplicate id", plan: cliFindingHeader + "| F1 | `src/b.js:9` | M | yes | E1 |  | open | me | - | - |\n" + cliEvidenceLedger, wantExit: 1, wantOut: "already row"},
		{name: "a dangling evidence id", plan: valid, over: map[string]string{"evidence": "E9"}, wantExit: 1, wantOut: "E9"},
		{name: "a plan the checker already rejects", plan: cliFindingHeader + "| F0 | no path here | M | yes | E1 |  | open | me | - | - |\n" + cliEvidenceLedger, wantExit: 1, wantOut: "would not pass plan check"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			dir := t.TempDir()
			p := filepath.Join(dir, "plan.md")
			var before []byte
			if tc.missing {
				p = filepath.Join(dir, "missing", "plan.md")
			} else {
				if err := os.WriteFile(p, []byte(tc.plan), 0o644); err != nil {
					t.Fatal(err)
				}
				var err error
				before, err = os.ReadFile(p)
				if err != nil {
					t.Fatal(err)
				}
			}
			out, code := runCLI(t, bin, findingArgs(p, tc.over)...)
			if code != tc.wantExit {
				t.Fatalf("exit = %d, want %d\n%s", code, tc.wantExit, out)
			}
			if !strings.Contains(out, tc.wantOut) {
				t.Fatalf("output missing %q:\n%s", tc.wantOut, out)
			}
			if tc.missing {
				return
			}
			after, err := os.ReadFile(p)
			if err != nil {
				t.Fatal(err)
			}
			if !bytes.Equal(before, after) {
				t.Fatal("a refusal must leave the plan byte-identical")
			}
		})
	}
}

func TestPlanUpgradeCLIOnALegacyPlan(t *testing.T) {
	bin := buildCLI(t)
	dir := t.TempDir()
	path := filepath.Join(dir, "plan.md")
	doc := "## Findings\n\n| Id | Finding | Severity | Data safe? | Evidence id | Pinning test | Status | Verdict by / date | Reason | Fingerprint |\n|---|---|---|---|---|---|---|---|---|---|\n" +
		"\n## Ranked targets\n\n| Target | Status |\n|---|---|\n| target | pending |\n" +
		"\n## Layer matrix\n\n| Layer | Skill | Scope | Status |\n|---|---|---|---|\n| Security | `appsec` | input | pending |\n"
	if err := os.WriteFile(path, []byte(doc), 0o644); err != nil {
		t.Fatal(err)
	}
	out, code := runCLI(t, bin, "plan", "upgrade", "--path", path)
	if code != 0 {
		t.Fatalf("plan upgrade exit = %d\n%s", code, out)
	}
	out, code = runCLI(t, bin, "plan", "check", "--path", path)
	if code != 0 || !strings.Contains(out, "well formed") {
		t.Fatalf("upgraded plan check = %d\n%s", code, out)
	}
}

// A reviewer keeps the plan outside the author's checkout, so export has to read an absolute path anywhere,
// name the commit it covers (HEAD of the repository it runs in, unless --commit says otherwise), and never
// print the directory the plan lives in.
func TestPlanExportCLI(t *testing.T) {
	bin := buildCLI(t)
	dir := t.TempDir()
	path := filepath.Join(dir, "pr-42.md")
	doc := "## Findings\n\n| Id | Finding (path:line, one line) | Severity | Data safe? | Evidence id | Pinning test | Status | Verdict by / date | Reason | Fingerprint |\n|---|---|---|---|---|---|---|---|---|---|\n" +
		"| F1 | internal/text/trim.go:7 trims inner spaces | correctness | yes | E1 | - | open | reviewer / 2026-09-25 | seen red | - |\n\n" +
		"## Evidence ledger\n\n| Id | Claim | Admit | Digest | Expect | Label |\n|---|---|---|---|---|---|\n" +
		"| E1 | red | `go test ./internal/text` | | fail | observado |\n"
	if err := os.WriteFile(path, []byte(doc), 0o644); err != nil {
		t.Fatal(err)
	}

	out, code := runCLI(t, bin, "plan", "export", "--path", path, "--commit", "deadbee")
	if code != 0 {
		t.Fatalf("plan export exit = %d\n%s", code, out)
	}
	for _, want := range []string{"deadbee", "pr-42.md", "| `F1` |", "E1: go test ./internal/text · Expect: fail"} {
		if !strings.Contains(out, want) {
			t.Errorf("export lacks %q:\n%s", want, out)
		}
	}
	if strings.Contains(out, dir) {
		t.Errorf("the export must not name the directory the plan lives in:\n%s", out)
	}

	head, err := exec.Command("git", "rev-parse", "--short", "HEAD").Output()
	if err != nil {
		t.Skipf("no git HEAD to compare with: %v", err)
	}
	out, code = runCLI(t, bin, "plan", "export", "--path", path)
	if code != 0 || !strings.Contains(out, strings.TrimSpace(string(head))) {
		t.Fatalf("without --commit the export names HEAD %s, got exit %d:\n%s", head, code, out)
	}

	bare := filepath.Join(dir, "bare.md")
	if err := os.WriteFile(bare, []byte("# Plan\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	out, code = runCLI(t, bin, "plan", "export", "--path", bare, "--commit", "deadbee")
	if code != 1 || !strings.Contains(out, "plan export:") {
		t.Fatalf("a plan without Findings exits 1, got %d:\n%s", code, out)
	}
}

func runCLIAt(t *testing.T, dir, bin string, args ...string) (string, int) {
	t.Helper()
	cmd := exec.Command(bin, args...)
	cmd.Dir = dir
	out, err := cmd.CombinedOutput()
	if err == nil {
		return string(out), 0
	}
	var ee exec.ExitError
	if asExit(err, &ee) {
		return string(out), ee.ExitCode()
	}
	t.Fatalf("run %v: %v", args, err)
	return "", -1
}

func TestPlanScopedCLIRealRun(t *testing.T) {
	bin := buildCLI(t)
	dir := t.TempDir()
	git := exec.Command("git", "init", dir)
	if out, err := git.CombinedOutput(); err != nil {
		t.Fatalf("git init: %v\n%s", err, out)
	}
	path := filepath.Join(dir, "plan.md")
	legacy := "## Findings\n\n| Id | Finding | Severity | Data safe? | Evidence id | Pinning test | Status | Verdict by / date | Reason | Fingerprint |\n|---|---|---|---|---|---|---|---|---|---|\n" +
		"\n## Ranked targets\n\n| Target | Status |\n|---|---|\n| first target | pending |\n| second target | pending |\n" +
		"\n## Layer matrix\n\n| Layer | Skill | Scope | Status |\n|---|---|---|---|\n| first layer | skill | scope | pending |\n| second layer | skill | scope | pending |\n"
	if err := os.WriteFile(path, []byte(legacy), 0o644); err != nil {
		t.Fatal(err)
	}

	out, code := runCLIAt(t, dir, bin, "plan", "check", "--path", "plan.md")
	t.Logf("$ %s plan check --path plan.md\n%s", bin, out)
	if code != 1 || !strings.Contains(out, "tsp plan upgrade") {
		t.Fatalf("legacy plan check = %d\n%s", code, out)
	}
	out, code = runCLIAt(t, dir, bin, "plan", "upgrade", "--path", "plan.md")
	t.Logf("$ %s plan upgrade --path plan.md\n%s", bin, out)
	if code != 0 {
		t.Fatalf("legacy plan upgrade = %d\n%s", code, out)
	}
	out, code = runCLIAt(t, dir, bin, "plan", "check", "--path", "plan.md")
	t.Logf("$ %s plan check --path plan.md\n%s", bin, out)
	if code != 0 || !strings.Contains(out, "well formed") {
		t.Fatalf("upgraded plan check = %d\n%s", code, out)
	}
	out, code = runCLIAt(t, dir, bin, "plan", "gaps", "--all", "--path", "plan.md")
	t.Logf("$ %s plan gaps --all --path plan.md\n%s", bin, out)
	if code != 1 || !strings.Contains(out, "layers swept: 0 of 2") || !strings.Contains(out, "ranked targets done: 0 of 2") {
		t.Fatalf("all-row gaps = %d\n%s", code, out)
	}

	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	scoped := strings.Replace(string(raw), "| first target | pending |  |", "| first target | pending | redis-pool |", 1)
	scoped = strings.Replace(scoped, "| first layer | skill | scope | pending |  |", "| first layer | skill | scope | pending | redis-pool |", 1)
	if err := os.WriteFile(path, []byte(scoped), 0o644); err != nil {
		t.Fatal(err)
	}
	out, code = runCLIAt(t, dir, bin, "plan", "gaps", "--run", "redis-pool", "--path", "plan.md")
	t.Logf("$ %s plan gaps --run redis-pool --path plan.md\n%s", bin, out)
	if code != 1 || !strings.Contains(out, "run redis-pool: layers swept: 0 of 1") || !strings.Contains(out, "2 row(s) belong to no run") {
		t.Fatalf("scoped gaps = %d\n%s", code, out)
	}
	out, code = runCLIAt(t, dir, bin, "plan", "gaps", "--run", "typo", "--path", "plan.md")
	t.Logf("$ %s plan gaps --run typo --path plan.md\n%s", bin, out)
	if code != 1 || !strings.Contains(out, `no row carries run "typo"`) {
		t.Fatalf("missing-run gaps = %d\n%s", code, out)
	}
}

func TestPlanGapsUsesTheDeclaredRun(t *testing.T) {
	bin := buildCLI(t)
	dir := t.TempDir()
	git := exec.Command("git", "init", dir)
	if out, err := git.CombinedOutput(); err != nil {
		t.Fatalf("git init: %v\n%s", err, out)
	}
	if err := os.WriteFile(filepath.Join(dir, ".tpp.json"), []byte(`{"planPath":"plan.md","run":"redis-pool"}`), 0o644); err != nil {
		t.Fatal(err)
	}
	doc := "## Findings\n\n| Id | Finding | Severity | Data safe? | Evidence id | Pinning test | Status | Verdict by / date | Reason | Fingerprint |\n|---|---|---|---|---|---|---|---|---|---|\n" +
		"\n## Ranked targets\n\n| Target | Status | Run |\n|---|---|---|\n| own target | pending | redis-pool |\n| old target | pending |  |\n" +
		"\n## Layer matrix\n\n| Layer | Skill | Scope | Status | Run |\n|---|---|---|---|---|\n| own layer | skill | scope | pending | redis-pool |\n| old layer | skill | scope | pending |  |\n"
	if err := os.WriteFile(filepath.Join(dir, "plan.md"), []byte(doc), 0o644); err != nil {
		t.Fatal(err)
	}
	out, code := runCLIAt(t, dir, bin, "plan", "gaps")
	if code != 1 || !strings.Contains(out, "run redis-pool: layers swept: 0 of 1") || !strings.Contains(out, "2 row(s) belong to no run") {
		t.Fatalf("declared-run gaps = %d\n%s", code, out)
	}
	out, code = runCLIAt(t, dir, bin, "plan", "gaps", "--all")
	if code != 1 || !strings.Contains(out, "layers swept: 0 of 2") || strings.Contains(out, "belong to no run") {
		t.Fatalf("--all did not force whole-document counts = %d\n%s", code, out)
	}
}

// The results line is a promise about a file: when the run could not write its record, pointing the operator
// at summary.md sends them to a path that does not exist.
func TestBenchDoesNotPrintAResultsPathItCouldNotWrite(t *testing.T) {
	bin := buildCLI(t)
	out := t.TempDir()
	// A directory where aggregate.json has to go: the record cannot be written.
	if err := os.MkdirAll(filepath.Join(out, "aggregate.json"), 0o755); err != nil {
		t.Fatal(err)
	}
	got, code := runCLI(t, bin, "bench", "run", "--cases", "nope*", "--dry-run", "--out", out, "--bench-dir", t.TempDir())
	if code == 0 {
		t.Fatalf("a run that matched no case exited 0\n%s", got)
	}
	if strings.Contains(got, "results:") {
		t.Fatalf("the CLI points at a summary nobody wrote:\n%s", got)
	}
	if _, err := os.Stat(filepath.Join(out, "summary.md")); err == nil {
		t.Fatal("the test's premise is wrong: something wrote a summary")
	}
}

// The score command's JSON is its whole output, and the `--plan` path is the one a caller scores a finished run
// with. A write that fails there was dropped like the doctor's: the tool printed nothing, said nothing and
// returned 0, so a caller could not tell a score from no score at all.
//
// The `--workspace` path takes the same branch, and it is not pinned here on purpose: reaching its encode means
// running the case's suites, which belongs to the benchmark and to its own budget, not to this test.
func TestBenchScoreReportsTheScoreItCouldNotWrite(t *testing.T) {
	bin := buildCLI(t)
	caseDir := t.TempDir()
	key := `{"id":"t","language":"go","suite":"a_test.go","defects":[{"id":"D1","file":"src/a.js","line":5,"keywords":["alpha"]}]}`
	if err := os.WriteFile(filepath.Join(caseDir, "KEY.json"), []byte(key), 0o644); err != nil {
		t.Fatal(err)
	}
	planFile := filepath.Join(t.TempDir(), "plan.md")
	plan := "## Findings\n\n| Id | Finding | Severity | Data safe? | Evidence id | Pinning test | Status | Verdict by / date | Reason | Fingerprint |\n|---|---|---|---|---|---|---|---|---|---|\n" +
		"| F1 | `src/a.js:5` alpha | M | yes | E1 | t.js :: x | fixed | me | - | - |\n\n" +
		"## Evidence ledger\n\n| Id | Claim | Executed | Admit | Inputs and parameters | Observed | Digest | Normalize | Mode | Mutate | Mutation or negative control → result | Reproduction | Label |\n|---|---|---|---|---|---|---|---|---|---|---|---|---|\n" +
		"| E1 | the claim | go test ./... | go test ./... | none | the observation | sha256:aaaa | none | host | none | reverted → red | rerun it | observado |\n"
	// The fixture is a plan this repository's own checker accepts: a row that declares thirteen columns and writes
	// eight is the cell-count breach the checker refuses, and a fixture is where that mistake would be copied from.
	if problems := plancheck.CheckDocument(plan); len(problems) != 0 {
		t.Fatalf("the fixture is not a plan the checker accepts: %v", problems)
	}
	if err := os.WriteFile(planFile, []byte(plan), 0o644); err != nil {
		t.Fatal(err)
	}
	stderr, code := runCLIWithoutStdout(t, bin, "bench", "score", "--case", caseDir, "--plan", planFile)
	if code != exitArtifact {
		t.Fatalf("code = %d, want %d: the JSON score was not written\nstderr: %s", code, exitArtifact, stderr)
	}
	// The line has to name the command and the write that failed: a prefix check passes for any message.
	if !strings.Contains(stderr, "score:") || !strings.Contains(stderr, "/dev/stdout") {
		t.Fatalf("the failure must name the command and where the write went:\n%s", stderr)
	}
}
func syncTestHome(t *testing.T) string {
	t.Helper()
	home := t.TempDir()
	for _, dir := range []string{".claude", filepath.Join(".config", "opencode"), ".gemini", ".codex"} {
		if err := os.MkdirAll(filepath.Join(home, dir), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	return home
}

func runCLIWithHome(t *testing.T, home, bin string, args ...string) (string, int) {
	t.Helper()
	cmd := exec.Command(bin, args...)
	cmd.Env = append(os.Environ(), "HOME="+home)
	out, err := cmd.CombinedOutput()
	if err == nil {
		return string(out), 0
	}
	var ee exec.ExitError
	if asExit(err, &ee) {
		return string(out), ee.ExitCode()
	}
	t.Fatalf("run %v: %v", args, err)
	return "", -1
}

// A sync that found no host installed nothing, so it must not read as a success: a scripted install
// (dotfiles, CI) only sees the exit code. It says what it looked for and how to proceed, and exits 1.
func TestSyncReportsWhenNoHostIsInstalled(t *testing.T) {
	bin := buildCLI(t)
	home := t.TempDir()
	out, code := runCLIWithHome(t, home, bin, "sync")
	if code != 1 {
		t.Fatalf("sync with no installed host exit = %d, want 1: nothing was installed\n%s", code, out)
	}
	if !strings.Contains(out, "--config-dir") {
		t.Fatalf("no-host sync must say how to proceed (--config-dir):\n%s", out)
	}
	for _, path := range []string{filepath.Join(home, ".claude"), filepath.Join(home, ".config", "opencode"), filepath.Join(home, ".gemini"), filepath.Join(home, ".codex")} {
		if !strings.Contains(out, path) {
			t.Fatalf("no-host report missing looked-for path %q:\n%s", path, out)
		}
	}
	if !strings.Contains(out, "no installed hosts found") {
		t.Fatalf("no-host report is silent about the result:\n%s", out)
	}
	if _, err := os.Stat(filepath.Join(home, ".claude")); !os.IsNotExist(err) {
		t.Fatalf("sync created a missing Claude config dir: %v", err)
	}
}

func TestSyncConfigDirCreatesMissingClaudeTarget(t *testing.T) {
	bin := buildCLI(t)
	home := syncTestHome(t)
	custom := filepath.Join(home, "custom-claude")
	out, code := runCLIWithHome(t, home, bin, "sync", "--config-dir", custom)
	if code != 0 {
		t.Fatalf("sync --config-dir with a missing target exit = %d\n%s", code, out)
	}
	if _, err := os.Stat(filepath.Join(custom, "skills", "test-strategy", "SKILL.md")); err != nil {
		t.Fatalf("missing Claude target did not receive skills: %v", err)
	}
	if _, err := os.Stat(filepath.Join(custom, "settings.json")); err != nil {
		t.Fatalf("missing Claude target did not receive settings: %v", err)
	}
}

func TestSyncConfigDirOnlyTargetsClaude(t *testing.T) {
	bin := buildCLI(t)
	home := syncTestHome(t)
	custom := filepath.Join(home, "custom-claude")
	if err := os.Mkdir(custom, 0o755); err != nil {
		t.Fatal(err)
	}
	out, code := runCLIWithHome(t, home, bin, "sync", "--config-dir", custom)
	if code != 0 {
		t.Fatalf("sync --config-dir exit = %d\n%s", code, out)
	}
	if _, err := os.Stat(filepath.Join(custom, "skills", "breakcheck", "SKILL.md")); err != nil {
		t.Fatalf("custom Claude directory did not receive skills: %v", err)
	}
	if _, err := os.Stat(filepath.Join(custom, "settings.json")); err != nil {
		t.Fatalf("custom Claude directory did not receive settings.json: %v", err)
	}
	for _, dir := range []string{filepath.Join(home, ".claude"), filepath.Join(home, ".config", "opencode"), filepath.Join(home, ".gemini"), filepath.Join(home, ".codex")} {
		if _, err := os.Stat(filepath.Join(dir, "skills")); !os.IsNotExist(err) {
			t.Fatalf("--config-dir installed outside Claude directory %s: %v", dir, err)
		}
	}
}

func TestSyncHostsFlagNarrowsTheInstall(t *testing.T) {
	bin := buildCLI(t)
	home := syncTestHome(t)
	out, code := runCLIWithHome(t, home, bin, "sync", "--hosts", "gemini,codex")
	if code != 0 {
		t.Fatalf("sync --hosts exit = %d\n%s", code, out)
	}
	name := "breakcheck"
	for _, host := range []struct {
		name string
		dir  string
		want bool
	}{
		{"claude", filepath.Join(home, ".claude"), false},
		{"opencode", filepath.Join(home, ".config", "opencode"), false},
		{"gemini", filepath.Join(home, ".gemini"), true},
		{"codex", filepath.Join(home, ".codex"), true},
	} {
		_, err := os.Stat(filepath.Join(host.dir, "skills", name, "SKILL.md"))
		if (err == nil) != host.want {
			t.Errorf("%s installed = %v, want %v: %v", host.name, err == nil, host.want, err)
		}
	}
}

func TestSyncRefusesAnUnknownHostName(t *testing.T) {
	bin := buildCLI(t)
	home := syncTestHome(t)
	out, code := runCLIWithHome(t, home, bin, "sync", "--hosts", "claude,wat")
	if code != 2 {
		t.Fatalf("unknown host exit = %d, want 2\n%s", code, out)
	}
	for _, want := range []string{"unknown host", "claude", "opencode", "gemini", "codex"} {
		if !strings.Contains(out, want) {
			t.Fatalf("unknown host output missing %q:\n%s", want, out)
		}
	}
}

// sync --hosts pi is the explicit opt-in for the extension host discovery never offers: the
// selection resolves to PiHost and nothing else — no discovered host receives skills, no
// settings.json is written for a host that wires no hook — and a dry-run creates nothing.
func TestSyncHostsPiSelectsOnlyThePiHost(t *testing.T) {
	bin := buildCLI(t)
	home := syncTestHome(t)

	dry, code := runCLIWithHome(t, home, bin, "sync", "--hosts", "pi", "--dry-run")
	if code != 0 {
		t.Fatalf("sync --hosts pi --dry-run exit = %d\n%s", code, dry)
	}
	if _, err := os.Stat(filepath.Join(home, ".pi")); !os.IsNotExist(err) {
		t.Fatalf("dry-run created the pi config dir: %v", err)
	}

	out, code := runCLIWithHome(t, home, bin, "sync", "--hosts", "pi")
	if code != 0 {
		t.Fatalf("sync --hosts pi exit = %d\n%s", code, out)
	}
	if !strings.Contains(out, "host: pi") || strings.Contains(out, "host: claude") {
		t.Fatalf("report must name pi and no other host:\n%s", out)
	}
	entry := filepath.Join(home, ".pi", "agent", "extensions", "tpp", "index.ts")
	if _, err := os.Stat(entry); err != nil {
		t.Fatalf("pi extension not installed: %v", err)
	}
	for _, dir := range []string{
		filepath.Join(home, ".claude"),
		filepath.Join(home, ".config", "opencode"),
		filepath.Join(home, ".gemini"),
		filepath.Join(home, ".codex"),
	} {
		if _, err := os.Stat(filepath.Join(dir, "skills")); !os.IsNotExist(err) {
			t.Fatalf("--hosts pi installed skills into %s: %v", dir, err)
		}
	}
	for _, settings := range []string{
		filepath.Join(home, ".claude", "settings.json"),
		filepath.Join(home, ".pi", "settings.json"),
	} {
		if _, err := os.Stat(settings); !os.IsNotExist(err) {
			t.Fatalf("--hosts pi wrote hook settings at %s: %v", settings, err)
		}
	}
}

// setup keeps its Claude-centered contract: --hosts pi stays a usage error there because setup's
// doctor pass and closing report assume skills and a Stop hook. Widening setup is task 5's.
func TestSetupStillRefusesThePiHost(t *testing.T) {
	bin := buildCLI(t)
	home := syncTestHome(t)
	out, code := runCLIWithHome(t, home, bin, "setup", "--hosts", "pi")
	if code != 2 {
		t.Fatalf("setup --hosts pi exit = %d, want 2\n%s", code, out)
	}
	if !strings.Contains(out, `unknown host "pi"`) {
		t.Fatalf("setup --hosts pi must refuse by name:\n%s", out)
	}
}

func TestSyncDryRunNamesEveryHost(t *testing.T) {
	bin := buildCLI(t)
	home := syncTestHome(t)
	out, code := runCLIWithHome(t, home, bin, "sync", "--dry-run")
	if code != 0 {
		t.Fatalf("sync --dry-run exit = %d\n%s", code, out)
	}
	for _, host := range []string{"claude", "opencode", "gemini", "codex"} {
		if !strings.Contains(out, "host: "+host) {
			t.Fatalf("dry-run output missing host %q:\n%s", host, out)
		}
	}
	for _, dir := range []string{".claude", filepath.Join(".config", "opencode"), ".gemini", ".codex"} {
		if _, err := os.Stat(filepath.Join(home, dir, "settings.json")); !os.IsNotExist(err) {
			t.Fatalf("dry-run wrote settings.json under %s: %v", dir, err)
		}
	}
}

func TestSyncForceReplacesAModifiedFileWithABackup(t *testing.T) {
	t.Setenv("TPP_HOME", t.TempDir())
	home := os.Getenv("TPP_HOME")
	bin := buildCLI(t)
	configDir := t.TempDir()
	skillPath := filepath.Join(configDir, "skills", "test-strategy", "SKILL.md")

	out, code := runCLI(t, bin, "sync", "--config-dir", configDir)
	if code != 0 {
		t.Fatalf("initial sync exit = %d\n%s", code, out)
	}
	original, err := os.ReadFile(skillPath)
	if err != nil {
		t.Fatalf("read installed skill: %v", err)
	}
	info, err := os.Stat(skillPath)
	if err != nil {
		t.Fatalf("stat installed skill: %v", err)
	}
	originalMode := uint32(info.Mode().Perm())
	edited := append(append([]byte(nil), original...), []byte("\nuser edit\n")...)
	if err := os.WriteFile(skillPath, edited, info.Mode().Perm()); err != nil {
		t.Fatalf("append user edit: %v", err)
	}

	out, code = runCLI(t, bin, "sync", "--config-dir", configDir)
	if code != 0 {
		t.Fatalf("sync without --force exit = %d\n%s", code, out)
	}
	got, err := os.ReadFile(skillPath)
	if err != nil {
		t.Fatalf("read preserved skill: %v", err)
	}
	if !bytes.Equal(got, edited) {
		t.Fatalf("sync without --force replaced the user edit")
	}

	out, code = runCLI(t, bin, "sync", "--config-dir", configDir, "--force")
	if code != 0 {
		t.Fatalf("sync --force exit = %d\n%s", code, out)
	}
	got, err = os.ReadFile(skillPath)
	if err != nil {
		t.Fatalf("read replaced skill: %v", err)
	}
	if !bytes.Equal(got, original) {
		t.Fatalf("sync --force did not replace the edited skill")
	}

	backupEntries, err := os.ReadDir(filepath.Join(home, "backups"))
	if err != nil {
		t.Fatalf("read backup directory: %v", err)
	}
	var backupDir string
	for _, entry := range backupEntries {
		if !entry.IsDir() {
			continue
		}
		if backupDir != "" {
			t.Fatalf("expected one backup directory, found more than one")
		}
		backupDir = filepath.Join(home, "backups", entry.Name())
	}
	if backupDir == "" {
		t.Fatal("sync --force did not create a backup directory")
	}

	var manifest struct {
		Entries []struct {
			OriginalPath string `json:"original_path"`
			SnapshotPath string `json:"snapshot_path"`
			Mode         uint32 `json:"mode"`
		} `json:"entries"`
	}
	manifestData, err := os.ReadFile(filepath.Join(backupDir, "manifest.json"))
	if err != nil {
		t.Fatalf("read backup manifest: %v", err)
	}
	if err := json.Unmarshal(manifestData, &manifest); err != nil {
		t.Fatalf("parse backup manifest: %v", err)
	}
	if len(manifest.Entries) != 1 {
		t.Fatalf("backup manifest entries = %d, want 1", len(manifest.Entries))
	}
	entry := manifest.Entries[0]
	if entry.OriginalPath != skillPath || entry.Mode != originalMode {
		t.Fatalf("backup manifest entry = %+v, want original path %q and mode %o", entry, skillPath, originalMode)
	}
	snapshot, err := os.ReadFile(filepath.Join(backupDir, filepath.FromSlash(entry.SnapshotPath)))
	if err != nil {
		t.Fatalf("read backup snapshot: %v", err)
	}
	if !bytes.Equal(snapshot, edited) {
		t.Fatalf("backup snapshot does not contain the edited skill")
	}
}

// adjudicateFixture writes the smallest finished run a decision can be recorded against: a case with
// one keyed defect, and one kept plan whose single finding row matches nothing in it. The row is the
// point: it is exactly the row the old scorer counted as a false positive without anyone deciding.
func adjudicateFixture(t *testing.T) (caseDir, runDir, planFile string) {
	t.Helper()
	caseDir = t.TempDir()
	key := `{"id":"t","language":"go","suite":"a_test.go","defects":[{"id":"D1","file":"src/a.js","line":5,"keywords":["alpha"]}]}`
	if err := os.WriteFile(filepath.Join(caseDir, "KEY.json"), []byte(key), 0o644); err != nil {
		t.Fatal(err)
	}
	planText := "## Findings\n\n| Id | Finding | Severity | Data safe? | Evidence id | Pinning test | Status | Verdict by / date | Reason | Fingerprint |\n|---|---|---|---|---|---|---|---|---|---|\n" +
		"| F1 | `src/other.js:3` something the key does not plant | M | yes | E1 | | open | me | - | - |\n\n" +
		"## Evidence ledger\n\n| Id | Claim | Executed | Admit | Inputs and parameters | Observed | Digest | Normalize | Mode | Mutate | Mutation or negative control → result | Reproduction | Label |\n|---|---|---|---|---|---|---|---|---|---|---|---|---|\n" +
		"| E1 | the claim | go test ./... | go test ./... | none | the observation | sha256:aaaa | none | host | none | reverted → red | rerun it | observado |\n"
	if problems := plancheck.CheckDocument(planText); len(problems) != 0 {
		t.Fatalf("the fixture is not a plan the checker accepts: %v", problems)
	}
	runDir = filepath.Join(t.TempDir(), "t", "1")
	if err := os.MkdirAll(runDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(runDir, "result.json"), []byte(`{"case":"t","run":1}`), 0o644); err != nil {
		t.Fatal(err)
	}
	planFile = filepath.Join(runDir, "test-plan.md")
	if err := os.WriteFile(planFile, []byte(planText), 0o644); err != nil {
		t.Fatal(err)
	}
	return caseDir, runDir, planFile
}

// A finding row nobody decided is pending, and the decision that follows is written where the score
// reads it. Without this the adjudication model would have no way in.
func TestBenchAdjudicateRecordsReplacesAndListsPending(t *testing.T) {
	bin := buildCLI(t)
	caseDir, runDir, planFile := adjudicateFixture(t)

	out, code := runCLI(t, bin, "bench", "adjudicate", "--run", runDir, "--pending")
	if code != 0 || !strings.Contains(out, "row 1") {
		t.Fatalf("--pending = %d\n%s", code, out)
	}
	if _, err := os.Stat(filepath.Join(runDir, "adjudication.json")); err == nil {
		t.Fatal("listing the pending rows wrote a record")
	}

	out, code = runCLI(t, bin, "bench", "adjudicate", "--run", runDir, "--row", "1", "--verdict", "false_positive",
		"--by", "reviewer", "--reason", "not a defect claim about this candidate")
	if code != 0 {
		t.Fatalf("recording a decision = %d\n%s", code, out)
	}
	data, err := os.ReadFile(filepath.Join(runDir, "adjudication.json"))
	if err != nil || !strings.Contains(string(data), `"false_positive"`) {
		t.Fatalf("record = %s, %v", data, err)
	}

	out, code = runCLI(t, bin, "bench", "adjudicate", "--run", runDir, "--row", "1", "--verdict", "out_of_scope",
		"--by", "reviewer", "--reason", "second thoughts")
	if code != 1 || !strings.Contains(out, "row 1") {
		t.Fatalf("a second decision without --replace = %d\n%s", code, out)
	}

	out, code = runCLI(t, bin, "bench", "adjudicate", "--run", runDir, "--row", "1", "--verdict", "defect",
		"--defect", "D1", "--by", "reviewer", "--reason", "closer look", "--replace")
	if code != 0 {
		t.Fatalf("--replace = %d\n%s", code, out)
	}
	out, code = runCLI(t, bin, "bench", "adjudicate", "--run", runDir, "--show")
	if code != 0 || !strings.Contains(out, "defect") || !strings.Contains(out, "superseded") {
		t.Fatalf("--show = %d\n%s", code, out)
	}

	// The score applies the record the caller names: one confirmed defect, nothing pending.
	record := filepath.Join(runDir, "adjudication.json")
	out, code = runCLI(t, bin, "bench", "score", "--case", caseDir, "--plan", planFile, "--adjudication", record)
	if code != 0 || !strings.Contains(out, `"confirmed": true`) || !strings.Contains(out, `"pending_adjudication": 0`) {
		t.Fatalf("score with a named record = %d\n%s", code, out)
	}
	// Naming a record that is not there is a refusal, not a silent skip: the caller asked for authority.
	if _, code = runCLI(t, bin, "bench", "score", "--case", caseDir, "--plan", planFile, "--adjudication", filepath.Join(t.TempDir(), "absent.json")); code != 1 {
		t.Fatalf("score with an absent record = %d, want 1", code)
	}
	// ... and the score can be taken without it, which leaves every row pending and no false positive.
	out, code = runCLI(t, bin, "bench", "score", "--case", caseDir, "--plan", planFile, "--adjudication", "none")
	if code != 0 || !strings.Contains(out, `"pending_adjudication": 1`) || !strings.Contains(out, `"false_positives": 0`) {
		t.Fatalf("score --adjudication none = %d\n%s", code, out)
	}
}

// A record is authority over a score, so it is never discovered beside the plan: the plan of a workspace
// sits in the area the evaluated subject writes, and a record found there would let the subject mark its
// own finding rows out of scope, leave the precision denominator and report a clean run. This is the
// regression pin for R1-001 of the native review of this candidate.
func TestBenchScoreNeverDiscoversARecordBesideThePlan(t *testing.T) {
	bin := buildCLI(t)
	caseDir, _, planFile := adjudicateFixture(t)
	seeded := []byte(`{"case":"t","run":1,"decisions":[{"row":1,"row_fingerprint":"` + fingerprintOf(t, planFile) + `","verdict":"out_of_scope","by":"the subject","ts":"2026-09-17T00:00:00Z","reason":"nothing to see"}]}`)
	if err := os.WriteFile(filepath.Join(filepath.Dir(planFile), "adjudication.json"), seeded, 0o644); err != nil {
		t.Fatal(err)
	}
	out, code := runCLI(t, bin, "bench", "score", "--case", caseDir, "--plan", planFile)
	if code != 0 {
		t.Fatalf("score with a seeded record beside the plan = %d\n%s", code, out)
	}
	if !strings.Contains(out, `"pending_adjudication": 1`) || !strings.Contains(out, `"out_of_scope": 0`) || strings.Contains(out, `"precision": 0`) {
		t.Fatalf("a record beside the plan was applied:\n%s", out)
	}
}

// fingerprintOf is the row digest an attacker would have to compute to seed a plausible record.
func fingerprintOf(t *testing.T, planFile string) string {
	t.Helper()
	plan, err := os.ReadFile(planFile)
	if err != nil {
		t.Fatal(err)
	}
	fingerprint, err := bench.FindingRowFingerprint(string(plan), 1)
	if err != nil {
		t.Fatal(err)
	}
	return fingerprint
}

// Bad values are a usage failure, a record the data refuses is not: the distinction the plan commands
// already make, and the one an operator scripting this needs.
func TestBenchAdjudicateRefusalsAndTheirExitCodes(t *testing.T) {
	bin := buildCLI(t)
	_, runDir, _ := adjudicateFixture(t)

	tests := []struct {
		name string
		args []string
		want int
	}{
		{name: "no run", args: []string{"bench", "adjudicate", "--row", "1"}, want: 2},
		{name: "no verdict", args: []string{"bench", "adjudicate", "--run", runDir, "--row", "1", "--by", "r", "--reason", "x"}, want: 2},
		{name: "unknown verdict", args: []string{"bench", "adjudicate", "--run", runDir, "--row", "1", "--verdict", "maybe", "--by", "r", "--reason", "x"}, want: 2},
		{name: "defect without id", args: []string{"bench", "adjudicate", "--run", runDir, "--row", "1", "--verdict", "defect", "--by", "r", "--reason", "x"}, want: 2},
		{name: "defect on a non-defect verdict", args: []string{"bench", "adjudicate", "--run", runDir, "--row", "1", "--verdict", "out_of_scope", "--defect", "D1", "--by", "r", "--reason", "x"}, want: 2},
		{name: "no reason", args: []string{"bench", "adjudicate", "--run", runDir, "--row", "1", "--verdict", "false_positive", "--by", "r"}, want: 2},
		{name: "bad ts", args: []string{"bench", "adjudicate", "--run", runDir, "--row", "1", "--verdict", "false_positive", "--by", "r", "--reason", "x", "--ts", "yesterday"}, want: 2},
		{name: "row outside the table", args: []string{"bench", "adjudicate", "--run", runDir, "--row", "9", "--verdict", "false_positive", "--by", "r", "--reason", "x"}, want: 1},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, code := runCLI(t, bin, tt.args...)
			if code != tt.want {
				t.Fatalf("exit = %d, want %d", code, tt.want)
			}
		})
	}
}

// The provenance records which configuration measured the run, because a throwaway config and the
// operator's own are not the same instrument.
func TestConfigModeForTheAgentConfigFlag(t *testing.T) {
	tests := []struct {
		runner, agentConfig, want string
	}{
		{"pi", "", bench.ConfigBench},
		{"pi", "bench", bench.ConfigBench},
		{"claude", "bench", bench.ConfigBench},
		{"claude", "/tmp/op-config", bench.ConfigCustom},
		{"claude", "", bench.ConfigInherited},
	}
	for _, tt := range tests {
		if got := configModeFor(tt.runner, tt.agentConfig); got != tt.want {
			t.Errorf("configModeFor(%q, %q) = %q, want %q", tt.runner, tt.agentConfig, got, tt.want)
		}
	}
}

// setupEnv isolates a setup run: its own HOME and installation state, and exactly the PATH given, so
// the PATH check sees only what the test decided.
func setupEnv(t *testing.T, home, path string) []string {
	t.Helper()
	return []string{"HOME=" + home, "TPP_HOME=" + t.TempDir(), "PATH=" + path, "CLAUDE_CONFIG_DIR=", "PI_CODING_AGENT_DIR="}
}

// pathWithoutBin is a PATH that still finds git (doctor requires it) and never holds the test binary.
func pathWithoutBin(t *testing.T) string {
	t.Helper()
	git, err := exec.LookPath("git")
	if err != nil {
		t.Skip("git is required by doctor")
	}
	return filepath.Dir(git) + string(os.PathListSeparator) + "/usr/bin" + string(os.PathListSeparator) + "/bin"
}

func lastLine(out string) string {
	lines := strings.Split(strings.TrimRight(out, "\n"), "\n")
	return lines[len(lines)-1]
}

// setup is the whole install in one command: it syncs, verifies with the doctor's checks, and ends on one
// line saying tsp works, so an agent or a person knows the install is done from the exit code and that line.
func TestSetupInstallsVerifiesAndSaysItIsWorking(t *testing.T) {
	bin := buildCLI(t)
	home := t.TempDir()
	if err := os.MkdirAll(filepath.Join(home, ".claude"), 0o755); err != nil {
		t.Fatal(err)
	}
	path := filepath.Dir(bin) + string(os.PathListSeparator) + pathWithoutBin(t)
	out, code := runCLIEnv(t, bin, setupEnv(t, home, path), "setup")
	if code != 0 {
		t.Fatalf("setup on a machine with Claude Code exit = %d, want 0\n%s", code, out)
	}
	if _, err := os.Stat(filepath.Join(home, ".claude", "skills", "test-strategy", "SKILL.md")); err != nil {
		t.Fatalf("setup did not install the skills: %v\n%s", err, out)
	}
	settings, err := os.ReadFile(filepath.Join(home, ".claude", "settings.json"))
	if err != nil || !strings.Contains(string(settings), "gate") {
		t.Fatalf("setup did not wire the Stop hook (err %v):\n%s", err, settings)
	}
	if !strings.Contains(out, "verdict: healthy") {
		t.Fatalf("setup must show the doctor's checks:\n%s", out)
	}
	last := lastLine(out)
	if !strings.HasPrefix(last, "tsp is installed and working: ") || !strings.Contains(last, "in claude") || !strings.HasSuffix(last, "Stop hook wired to "+bin) {
		t.Fatalf("last line = %q, want the working line naming the host and the wired binary\n%s", last, out)
	}
	if strings.Contains(out, "export PATH=") {
		t.Fatalf("the binary's directory is on PATH, so setup must not ask to add it:\n%s", out)
	}
}

// A machine with no host has nothing to install into: setup stops with sync's exit code and never claims
// tsp is working.
func TestSetupStopsWhenNoHostIsInstalled(t *testing.T) {
	bin := buildCLI(t)
	home := t.TempDir()
	out, code := runCLIEnv(t, bin, setupEnv(t, home, pathWithoutBin(t)), "setup")
	if code != 1 {
		t.Fatalf("setup with no host exit = %d, want sync's 1\n%s", code, out)
	}
	if strings.Contains(out, "installed and working") {
		t.Fatalf("setup claimed success with nothing installed:\n%s", out)
	}
	if !strings.Contains(out, "no host found") {
		t.Fatalf("setup must carry sync's reason:\n%s", out)
	}
}

// The Stop hook uses the absolute path, so a binary off PATH still works; setup says so and prints the
// exact line to add, without failing.
func TestSetupPrintsThePathLineWhenTheBinaryIsNotOnPath(t *testing.T) {
	bin := buildCLI(t)
	home := t.TempDir()
	if err := os.MkdirAll(filepath.Join(home, ".claude"), 0o755); err != nil {
		t.Fatal(err)
	}
	out, code := runCLIEnv(t, bin, setupEnv(t, home, pathWithoutBin(t)), "setup")
	if code != 0 {
		t.Fatalf("setup with the binary off PATH exit = %d, want 0: the hook uses the absolute path\n%s", code, out)
	}
	dir, err := filepath.EvalSymlinks(filepath.Dir(bin))
	if err != nil {
		t.Fatal(err)
	}
	if want := `export PATH="` + dir + `:$PATH"`; !strings.Contains(out, want) {
		t.Fatalf("setup must print %q:\n%s", want, out)
	}
	if last := lastLine(out); !strings.HasPrefix(last, "tsp is installed and working: ") || !strings.HasSuffix(last, "Stop hook wired to "+bin) {
		t.Fatalf("a PATH warning is not a failure, and the hook still names the binary it runs; last line = %q\n%s", last, out)
	}
}

// Codex, Gemini and OpenCode get the skills but no Stop hook, so setup must install into them, say the hook
// is not wired rather than claim it, and still finish: the skills are the whole install there.
func TestSetupOnAMachineWithoutClaudeInstallsTheSkillsAndSaysTheHookIsNotWired(t *testing.T) {
	bin := buildCLI(t)
	home := t.TempDir()
	if err := os.MkdirAll(filepath.Join(home, ".codex"), 0o755); err != nil {
		t.Fatal(err)
	}
	out, code := runCLIEnv(t, bin, setupEnv(t, home, pathWithoutBin(t)), "setup")
	if code != 0 {
		t.Fatalf("setup with only Codex installed exit = %d, want 0\n%s", code, out)
	}
	for _, name := range assets.SkillNames() {
		if _, err := os.Stat(filepath.Join(home, ".codex", "skills", name, "SKILL.md")); err != nil {
			t.Fatalf("setup claims the skills are installed in codex, but %s is missing: %v\n%s", name, err, out)
		}
	}
	if _, err := os.Stat(filepath.Join(home, ".claude")); !os.IsNotExist(err) {
		t.Fatalf("setup must not create a Claude config dir on a machine without Claude Code: %v", err)
	}
	last := lastLine(out)
	if !strings.HasPrefix(last, "tsp is installed and working: ") || !strings.Contains(last, "in codex") ||
		!strings.HasSuffix(last, "Stop hook not wired: Claude Code is not installed here, and the gate runs only there") {
		t.Fatalf("last line = %q, want the working line naming codex and saying the hook is not wired\n%s", last, out)
	}
	if strings.Contains(out, "Stop hook wired to") {
		t.Fatalf("setup claimed a wired hook with no Claude Code host:\n%s", out)
	}
}

// After a clean sync the install can still be unusable: git is the one required tool, and without it the
// gate cannot run. setup must then stop on "not finished", name the missing tool, and never claim success.
func TestSetupIsNotFinishedWhenARequiredToolIsMissing(t *testing.T) {
	for _, tc := range []struct {
		name string
		host string
	}{
		{"with Claude Code", ".claude"},
		{"without Claude Code", ".codex"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			bin := buildCLI(t)
			home := t.TempDir()
			if err := os.MkdirAll(filepath.Join(home, tc.host), 0o755); err != nil {
				t.Fatal(err)
			}
			noTools := t.TempDir() // a PATH with no git on it
			out, code := runCLIEnv(t, bin, setupEnv(t, home, noTools), "setup")
			if code != 1 {
				t.Fatalf("setup without git exit = %d, want 1\n%s", code, out)
			}
			last := lastLine(out)
			if !strings.HasPrefix(last, "setup: not finished: ") || !strings.Contains(last, "git") {
				t.Fatalf("last line = %q, want not finished naming git\n%s", last, out)
			}
			if strings.Contains(out, "installed and working") {
				t.Fatalf("setup claimed success with a required tool missing:\n%s", out)
			}
		})
	}
}

// --mode is explicit on setup and doctor: the default and explicit auto/standalone both run and
// report the standalone contract, gentle is refused as pending integration before any work, an
// unknown value is a usage failure, and the doctor's report carries the mode it ran under.
func TestModeFlagSelectsAndReportsTheStandaloneContract(t *testing.T) {
	bin := buildCLI(t)
	configDir := t.TempDir()
	if got, code := runCLI(t, bin, "sync", "--config-dir", configDir); code != 0 {
		t.Fatalf("sync exited %d, so the mode report's premise (a healthy doctor) does not hold\n%s", code, got)
	}

	for _, tc := range []struct {
		name string
		args []string
	}{
		{"the default", []string{"doctor", "--json", "--config-dir", configDir}},
		{"explicit auto", []string{"doctor", "--json", "--mode", "auto", "--config-dir", configDir}},
		{"explicit standalone", []string{"doctor", "--json", "--mode", "standalone", "--config-dir", configDir}},
	} {
		t.Run("doctor reports standalone for "+tc.name, func(t *testing.T) {
			out, code := runCLI(t, bin, tc.args...)
			if code != 0 {
				t.Fatalf("exit = %d, want 0\n%s", code, out)
			}
			if !strings.Contains(out, `"mode": "standalone"`) {
				t.Fatalf("doctor --json must carry the mode it ran under:\n%s", out)
			}
		})
	}

	t.Run("the text report names the mode", func(t *testing.T) {
		out, code := runCLI(t, bin, "doctor", "--mode", "standalone", "--config-dir", configDir)
		if code != 0 {
			t.Fatalf("exit = %d, want 0\n%s", code, out)
		}
		if !strings.Contains(out, "mode standalone") {
			t.Fatalf("the text report must name the mode:\n%s", out)
		}
	})

	t.Run("setup runs and reports an explicit standalone mode", func(t *testing.T) {
		home := t.TempDir()
		if err := os.MkdirAll(filepath.Join(home, ".claude"), 0o755); err != nil {
			t.Fatal(err)
		}
		out, code := runCLIEnv(t, bin, setupEnv(t, home, pathWithoutBin(t)), "setup", "--mode", "standalone")
		if code != 0 {
			t.Fatalf("setup --mode standalone exit = %d, want 0\n%s", code, out)
		}
		if !strings.Contains(out, "mode standalone") {
			t.Fatalf("setup must report the mode it ran under:\n%s", out)
		}
		if !strings.HasPrefix(lastLine(out), "tsp is installed and working: ") {
			t.Fatalf("last line = %q\n%s", lastLine(out), out)
		}
	})

	t.Run("setup refuses gentle before installing anything", func(t *testing.T) {
		home := t.TempDir()
		if err := os.MkdirAll(filepath.Join(home, ".claude"), 0o755); err != nil {
			t.Fatal(err)
		}
		out, code := runCLIEnv(t, bin, setupEnv(t, home, pathWithoutBin(t)), "setup", "--mode", "gentle")
		if code != 2 {
			t.Fatalf("setup --mode gentle exit = %d, want 2\n%s", code, out)
		}
		if !strings.Contains(out, "pending integration") {
			t.Fatalf("the refusal must say why gentle cannot run:\n%s", out)
		}
		if _, err := os.Stat(filepath.Join(home, ".claude", "skills")); !os.IsNotExist(err) {
			t.Fatalf("a refused mode must not install anything: %v", err)
		}
	})

	t.Run("setup rejects an unknown mode", func(t *testing.T) {
		out, code := runCLIEnv(t, bin, setupEnv(t, t.TempDir(), pathWithoutBin(t)), "setup", "--mode", "turbo")
		if code != 2 {
			t.Fatalf("setup --mode turbo exit = %d, want 2\n%s", code, out)
		}
		if !strings.Contains(out, "unknown mode") {
			t.Fatalf("the refusal must name the problem:\n%s", out)
		}
	})
}

// The extension's process-scoped observation flips doctor's auto to gentle; without it — even
// with a gentle-ai binary on PATH — auto stays standalone: the signal is the in-session
// observation, never a program lookup (session UX evidence, not authentication).
func TestDoctorModeFollowsTheExtensionObservationNotThePath(t *testing.T) {
	bin := buildCLI(t)
	configDir := t.TempDir()
	if got, code := runCLI(t, bin, "sync", "--config-dir", configDir); code != 0 {
		t.Fatalf("sync exited %d, so the mode report's premise (a healthy doctor) does not hold\n%s", code, got)
	}

	// A gentle-ai on PATH must be inert: the resolver never looks one up.
	fake := t.TempDir()
	if err := os.WriteFile(filepath.Join(fake, "gentle-ai"), []byte("#!/bin/sh\nexit 0\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	path := fake + string(os.PathListSeparator) + pathWithoutBin(t)

	// Every case starts from a cleared observation — canonical name and legacy fallback — so an
	// ambient value in the test environment cannot stand in for the extension; "last entry wins"
	// makes the override deterministic.
	cleared := "TSP_GENTLE_OBSERVATION="
	clearedLegacy := "TPP_GENTLE_OBSERVATION="
	observed := "TPP_GENTLE_OBSERVATION=pi-session-gentle-active"
	for _, tc := range []struct {
		name string
		env  []string
		args []string
		want string
	}{
		{"auto without the observation stays standalone", []string{cleared, clearedLegacy, "PATH=" + path},
			[]string{"doctor", "--json", "--config-dir", configDir}, `"mode": "standalone"`},
		{"auto with the observation reports gentle", []string{cleared, "PATH=" + path, observed},
			[]string{"doctor", "--json", "--config-dir", configDir}, `"mode": "gentle"`},
		{"explicit gentle with the observation runs", []string{cleared, observed},
			[]string{"doctor", "--json", "--mode", "gentle", "--config-dir", configDir}, `"mode": "gentle"`},
		{"a foreign observation value stays standalone", []string{cleared, "TPP_GENTLE_OBSERVATION=yes"},
			[]string{"doctor", "--json", "--config-dir", configDir}, `"mode": "standalone"`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			out, code := runCLIEnv(t, bin, tc.env, tc.args...)
			if code != 0 {
				t.Fatalf("exit = %d, want 0\n%s", code, out)
			}
			if !strings.Contains(out, tc.want) {
				t.Fatalf("doctor --json must report %s:\n%s", tc.want, out)
			}
		})
	}
}

// The Stop hook is required only from the hosts that wire one: selecting codex on a machine that
// also has Claude installed installs into codex and must not demand — or claim — the Claude hook,
// or standalone health would depend on a host the selection left out.
func TestSetupHookRequirementFollowsTheSelectedHost(t *testing.T) {
	bin := buildCLI(t)
	home := t.TempDir()
	for _, dir := range []string{".claude", ".codex"} {
		if err := os.MkdirAll(filepath.Join(home, dir), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	out, code := runCLIEnv(t, bin, setupEnv(t, home, pathWithoutBin(t)), "setup", "--hosts", "codex")
	if code != 0 {
		t.Fatalf("setup --hosts codex exit = %d, want 0: the selected host wires no Stop hook\n%s", code, out)
	}
	if strings.Contains(out, "Stop hook wired to") {
		t.Fatalf("setup claimed a wired hook for a host that wires none:\n%s", out)
	}
	for _, name := range assets.SkillNames() {
		if _, err := os.Stat(filepath.Join(home, ".codex", "skills", name, "SKILL.md")); err != nil {
			t.Fatalf("codex skill %s not installed: %v\n%s", name, err, out)
		}
	}
	last := lastLine(out)
	if !strings.HasPrefix(last, "tsp is installed and working: ") || !strings.Contains(last, "in codex") {
		t.Fatalf("last line = %q, want the working line naming codex\n%s", last, out)
	}
}

// `tpp bind` is the operator side of the stored binding: the same validations the gate reads, one
// private hashed file per (root, session) outside the repository, and --unset removes only that
// session's own key. Errors are bounded — nothing prints the session identity back.
func TestBindCLIStoresAndUnsetsPerSessionBindings(t *testing.T) {
	repo := t.TempDir()
	if out, err := exec.Command("git", "init", repo).CombinedOutput(); err != nil {
		t.Fatalf("git init: %v %s", err, out)
	}
	nonRepo := t.TempDir()
	base := t.TempDir()
	t.Setenv("TESTING_GATE_LOG", filepath.Join(base, "telemetry", "testing-gate.jsonl"))
	dir := filepath.Join(base, "telemetry", "bindings")

	capture := func(args ...string) (int, string) {
		t.Helper()
		or, ow, err := os.Pipe()
		if err != nil {
			t.Fatalf("stdout pipe: %v", err)
		}
		er, ew, err := os.Pipe()
		if err != nil {
			t.Fatalf("stderr pipe: %v", err)
		}
		outOrig, errOrig := os.Stdout, os.Stderr
		os.Stdout, os.Stderr = ow, ew
		code := runBind(args)
		_ = ow.Close()
		_ = ew.Close()
		os.Stdout, os.Stderr = outOrig, errOrig
		stdout, _ := io.ReadAll(or)
		stderr, _ := io.ReadAll(er)
		return code, string(stdout) + string(stderr)
	}

	refusals := []struct {
		name string
		args []string
	}{
		{name: "no session", args: []string{"--path", "docs/testing/test-plan.md", "--run", "run-t1"}},
		{name: "no path", args: []string{"--session", "sess-cli", "--run", "run-t1"}},
		{name: "escaping path", args: []string{"--session", "sess-cli", "--path", "../escape.md", "--run", "run-t1"}},
		{name: "bad run slug", args: []string{"--session", "sess-cli", "--path", "docs/testing/test-plan.md", "--run", "Bad_Slug!"}},
		{name: "cwd outside a repository", args: []string{"--session", "sess-cli", "--path", "docs/testing/test-plan.md", "--run", "run-t1", "--cwd", nonRepo}},
		{name: "unknown flag", args: []string{"--nope"}},
	}
	for _, tc := range refusals {
		t.Run(tc.name, func(t *testing.T) {
			code, out := capture(tc.args...)
			if code != 2 {
				t.Fatalf("exit = %d, want 2\n%s", code, out)
			}
			if strings.Contains(out, "sess-cli") {
				t.Fatalf("error printed the session identity: %q", out)
			}
		})
	}

	if code, out := capture("--session", "sess-a", "--path", "docs/testing/test-plan.md", "--run", "run-t1", "--cwd", repo); code != 0 {
		t.Fatalf("bind sess-a exit = %d\n%s", code, out)
	}
	if code, out := capture("--session", "sess-b", "--path", "docs/testing/other.md", "--run", "run-t2", "--cwd", repo); code != 0 {
		t.Fatalf("bind sess-b exit = %d\n%s", code, out)
	}
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatalf("read binding dir: %v", err)
	}
	if len(entries) != 2 {
		t.Fatalf("binding dir holds %d files, want one per session", len(entries))
	}
	bySession := map[string]string{}
	for _, e := range entries {
		if strings.Contains(e.Name(), "sess-") {
			t.Fatalf("file name %q carries a raw session ID", e.Name())
		}
		raw, err := os.ReadFile(filepath.Join(dir, e.Name()))
		if err != nil {
			t.Fatalf("read binding: %v", err)
		}
		for _, id := range []string{"sess-a", "sess-b"} {
			if strings.Contains(string(raw), `"session_id":"`+id+`"`) {
				bySession[id] = e.Name()
			}
		}
	}
	if len(bySession) != 2 {
		t.Fatalf("stored sessions = %v, want both (root, session) keys present", bySession)
	}

	if code, out := capture("--session", "sess-a", "--unset", "--cwd", repo); code != 0 {
		t.Fatalf("unset sess-a exit = %d\n%s", code, out)
	}
	entries, err = os.ReadDir(dir)
	if err != nil || len(entries) != 1 || entries[0].Name() != bySession["sess-b"] {
		t.Fatalf("after unset the dir holds %v (err %v), want only sess-b's own file", entries, err)
	}
	if code, out := capture("--session", "sess-a", "--unset", "--cwd", repo); code != 0 {
		t.Fatalf("unset again exit = %d, want idempotent\n%s", code, out)
	}
	if entries, err = os.ReadDir(dir); err != nil || len(entries) != 1 {
		t.Fatalf("after the second unset the dir holds %d entries (err %v), want 1", len(entries), err)
	}
}

// plan add-evidence writes a ledger row from flags that plan admit then reads as runnable; a value the caller
// got wrong exits 2 and a plan that cannot take the row exits 1, both leaving the plan unchanged.
func TestPlanAddEvidenceWritesARowAdmitCanRun(t *testing.T) {
	bin := buildCLI(t)
	repo := t.TempDir()
	if out, err := exec.Command("git", "init", "-q", repo).CombinedOutput(); err != nil {
		t.Fatalf("git init: %v\n%s", err, out)
	}
	p := filepath.Join(repo, "plan.md")
	if out, code := runCLI(t, bin, "plan", "init", "--path", p); code != 0 {
		t.Fatalf("plan init = %d\n%s", code, out)
	}
	args := []string{"plan", "add-evidence", "--path", p, "--id", "E1", "--claim", "echo prints its argument",
		"--executed", "ran echo", "--admit", "echo pinned", "--observed", "pinned", "--reproduction", "rerun"}
	out, code := runCLI(t, bin, args...)
	if code != 0 || !strings.Contains(out, "added E1 to ") {
		t.Fatalf("add-evidence = %d\n%s", code, out)
	}
	if out, code := runCLI(t, bin, "plan", "check", "--path", p); code != 0 {
		t.Fatalf("plan check after add-evidence = %d\n%s", code, out)
	}
	cmd := exec.Command(bin, "plan", "admit", "--path", p)
	cmd.Dir = repo
	admitted, _ := cmd.CombinedOutput()
	if !strings.Contains(string(admitted), "E1  WOULD RUN  echo pinned") {
		t.Fatalf("plan admit does not read the written row as runnable:\n%s", admitted)
	}

	before, _ := os.ReadFile(p)
	if out, code := runCLI(t, bin, append(args[:4:4], "--id", "E2", "--claim", "c", "--executed", "e", "--observed", "o", "--expect", "red")...); code != 2 || !strings.Contains(out, "--expect") {
		t.Fatalf("a bad --expect = %d, want 2\n%s", code, out)
	}
	if out, code := runCLI(t, bin, args...); code != 1 || !strings.Contains(out, "already row") {
		t.Fatalf("a duplicate id = %d, want 1\n%s", code, out)
	}
	for _, bad := range []struct{ admit, reason string }{
		{"go test ./... | tee out.txt", "admit-multiple-commands"},
		{"go test <package>", "admit-has-placeholder"},
		{"go test ./... > out.txt", "admit-has-redirection"},
	} {
		out, code := runCLI(t, bin, append(args[:4:4], "--id", "E4", "--claim", "c", "--executed", "e", "--observed", "o", "--admit", bad.admit)...)
		if code != 2 || !strings.Contains(out, bad.reason) {
			t.Fatalf("--admit %q = %d, want 2 naming %s\n%s", bad.admit, code, bad.reason, out)
		}
	}
	if out, code := runCLI(t, bin, "plan", "add-evidence", "--path", p, "--id", "E3"); code != 2 || !strings.Contains(out, "--claim is required") {
		t.Fatalf("a missing --claim = %d, want 2\n%s", code, out)
	}
	if after, _ := os.ReadFile(p); string(after) != string(before) {
		t.Fatal("a refused add-evidence changed the plan")
	}
}

// The sandbox runs with --read-only and a tmpfs only at /tmp, so the per-user cache os.UserCacheDir
// resolves (the plan lock writes one) and HOME itself do not exist behind it: a row that needs either had
// to set env by hand. Both now live on the writable tmpfs, and the dirs are created before the row's own
// command so a tool that writes HOME on start finds a directory that is there.
func TestSandboxArgsGiveTheContainerAWritableUserHome(t *testing.T) {
	args, err := sandboxArgs(t.TempDir(), "example/golang:1.26", admit.SandboxReadOnly, "go test ./...")
	if err != nil {
		t.Fatal(err)
	}
	joined := strings.Join(args, " ")
	for _, want := range []string{"-e HOME=/tmp/home", "-e XDG_CACHE_HOME=/tmp/xdg"} {
		if !strings.Contains(joined, want) {
			t.Errorf("args = %q, want %q", joined, want)
		}
	}
	script := args[len(args)-1]
	if !strings.HasPrefix(script, "mkdir -p /tmp/home /tmp/xdg\n") {
		t.Errorf("script = %q, want the user dirs created first", script)
	}
	if !strings.HasSuffix(script, "go test ./...") {
		t.Errorf("script = %q, want the row's command at the end", script)
	}
}

// --sandbox-image wins over the repository declaration, the declaration wins over the default, and a
// repository that declares nothing leaves the default to the flag itself. Without --sandbox the
// declaration is not read at all, so a broken .tsp.json cannot refuse an admit that runs on this machine.
func TestResolveSandboxImageOrder(t *testing.T) {
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, ".tsp.json"), []byte(`{"sandboxImage":"golang:1.26"}`), 0o644); err != nil {
		t.Fatal(err)
	}
	cases := []struct {
		name             string
		root             string
		sandbox, flagSet bool
		flagValue        string
		want             string
	}{
		{"flag wins", root, true, true, "alpine:3", "alpine:3"},
		{"declaration when the flag is absent", root, true, false, "", "golang:1.26"},
		{"default untouched when nothing declares", t.TempDir(), true, false, "", ""},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got, err := resolveSandboxImage(c.root, c.sandbox, c.flagSet, c.flagValue)
			if err != nil {
				t.Fatal(err)
			}
			if got != c.want {
				t.Fatalf("image = %q, want %q", got, c.want)
			}
		})
	}
}

func TestResolveSandboxImageDoesNotReadTheDeclarationOffTheSandboxPath(t *testing.T) {
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, ".tsp.json"), []byte(`{not json`), 0o644); err != nil {
		t.Fatal(err)
	}
	got, err := resolveSandboxImage(root, false, false, sandboxImageDefault)
	if err != nil {
		t.Fatalf("a declaration outside --sandbox must not be read: %v", err)
	}
	if got != sandboxImageDefault {
		t.Fatalf("image = %q, want the flag's own default %q", got, sandboxImageDefault)
	}
}

package admit

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"time"

	"github.com/alesierraalta/tsp/internal/evidence"
	"github.com/alesierraalta/tsp/internal/plan"
)

// Runner runs one command in dir and returns its combined output.
type Runner func(ctx context.Context, dir, command string) (string, error)

// Request is the admission run's shape: what to read, whether to execute, and how to narrow or record it.
type Request struct {
	Path    string
	Execute bool
	Timeout time.Duration
	Only    []string
	Record  []string
	Sandbox bool
	Image   string
	Dir     string
}

// Deps are the process boundaries injected by the caller. Out and Err must be non-nil; Run must be non-nil
// whenever a row can execute, and SandboxRunner and Replay are only reached when the request asks for the
// sandbox, so a host-mode caller may leave both empty.
type Deps struct {
	Run           Runner
	SandboxRunner func(image, mount string) Runner
	Replay        func(run Runner, timeout time.Duration) func(plan.Mutation, string, string) evidence.ReplayResult
	Out           io.Writer
	Err           io.Writer
}

// planLockWait bounds only the persistence phase: rows may run for minutes before this lock is needed, but the
// lock guards the local read, fsync and rename. One second allows a slow local write to finish without making a
// writer wait for the lifetime of another process.
const planLockWait = time.Second

type planLockResult struct {
	file *os.File
	err  error
}

// Run admits the requested Evidence rows and returns the process exit code; it reports rows to deps.Out, refusals to
// deps.Err, and records admitted observations only when requested. A valid dry run reads and validates the plan,
// reports WOULD RUN rows, reaches no command runner or plan write, and rejects --record or --sandbox before reading it.
// The pipeline checks the request, reads the document, admits the rows, reports them, and writes what was
// recorded; each is its own function with an outcome a reader can hold, while Run decides the exit code.
func Run(req Request, deps Deps) int {
	onlyIDs := admitIDs(req.Only)
	recordIDs := admitIDs(req.Record)
	mode, code, ok := checkRequest(req, recordIDs, deps)
	if !ok {
		return code
	}
	raw, err := os.ReadFile(req.Path)
	if err != nil {
		fmt.Fprintln(deps.Err, "plan admit:", err)
		return 1
	}
	rows := plan.Ledger(string(raw))
	for _, flag := range []struct {
		name string
		ids  []string
	}{{"--only", onlyIDs}, {"--record", recordIDs}} {
		if id := unknownID(flag.ids, rows); id != "" {
			return ledgerIDError(deps.Err, flag.name, id, rows)
		}
	}
	results := evidence.Admit(rows, evidence.Options{
		Execute: req.Execute,
		Dir:     req.Dir,
		Timeout: req.Timeout,
		Mode:    mode,
		Only:    onlyIDs,
		Record:  recordIDs,
	}, boundaries(req, deps))

	admitted, wouldRun, refused := reportVerdicts(results, deps)
	recordedLines, recorded, ok := recordResults(results, recordIDs, raw, req, deps, mode)
	if !ok {
		return 1
	}
	for _, line := range recordedLines {
		fmt.Fprint(deps.Out, line)
	}
	fmt.Fprintf(deps.Out, "%d rows: %d admitted, %d would run, %d refused, %d recorded\n", len(results), admitted, wouldRun, refused, recorded)
	if refused > 0 {
		return 1
	}
	return 0
}

// checkRequest refuses the two flag combinations that contradict each other and reports the mode the run will
// claim. Each refusal names what to change, because a caller that reads only the exit code cannot.
func checkRequest(req Request, recordIDs []string, deps Deps) (mode string, code int, ok bool) {
	if len(recordIDs) > 0 && !req.Execute {
		fmt.Fprintln(deps.Err, "plan admit: --record requires --execute: recording pins the observation this run makes, and a dry run makes none")
		return "", 2, false
	}
	if req.Sandbox && !req.Execute {
		fmt.Fprintln(deps.Err, "plan admit: --sandbox requires --execute: a dry run executes nothing, so there is nothing to confine")
		return "", 2, false
	}
	mode = evidence.ModeHost
	if req.Sandbox {
		mode = evidence.ModeSandbox
	}
	return mode, 0, true
}

// boundaries wires the process boundaries the rows will reach: the host runner, or the sandboxed one.
//
// The replay is wired exactly where a tree the tool owns exists: a sandbox stages a copy of the tree git knows,
// edits the copy, runs against it and puts the file back, so a row that claims its own command is falsifiable has
// that claim checked instead of admitted unchecked. The host mode has no such copy and leaves Replay nil, which
// refuses such a row.
func boundaries(req Request, deps Deps) evidence.Deps {
	runner := deps.Run
	if req.Sandbox {
		runner = deps.SandboxRunner(req.Image, SandboxReadOnly)
	}
	out := evidence.Deps{Run: runner}
	if req.Sandbox {
		out.Replay = deps.Replay(deps.SandboxRunner(req.Image, SandboxWritable), req.Timeout)
	}
	return out
}

// reportVerdicts prints one line per row and counts what it printed. A refusal prints the human sentence beside
// its machine reason, so a run that stops here still says what to change and what a caller can branch on.
func reportVerdicts(results []evidence.RowResult, deps Deps) (admitted, wouldRun, refused int) {
	for _, r := range results {
		switch r.Verdict {
		case evidence.VerdictAdmitted:
			admitted++
			// An admitted row carries a sentence only when it earned more than its pin, such as a mutation survey's
			// tally, so a row that did not keeps the line it always printed.
			if r.Detail != "" {
				fmt.Fprintf(deps.Out, "%s  %s  %s  %s  %d lines  %s\n", r.ID, r.Verdict, r.Command, r.Digest, r.Lines, r.Detail)
			} else {
				fmt.Fprintf(deps.Out, "%s  %s  %s  %s  %d lines\n", r.ID, r.Verdict, r.Command, r.Digest, r.Lines)
			}
		case evidence.VerdictWouldRun:
			wouldRun++
			fmt.Fprintf(deps.Out, "%s  %s  %s\n", r.ID, r.Verdict, r.Command)
		default:
			refused++
			fmt.Fprintf(deps.Out, "%s  %s  %s  [%s]\n", r.ID, r.Verdict, r.Detail, r.Reason)
		}
	}
	return admitted, wouldRun, refused
}

// recordResults applies every edit to the document in memory and writes the file once, so a run that records
// three rows leaves one write. A splice that refuses aborts the whole write rather than skipping the row: a
// half-recorded ledger is the state this feature exists to prevent.
//
// The write is one transaction under the lock every writer of the plan takes. The document was read before the
// rows ran and a row can take minutes, so the digests this run observed belong to the plan it read: a file
// another writer changed since is refused rather than overwritten, and the comparison and the write it guards
// cannot be split by a cooperating writer. A lock that cannot be taken is a refusal, never an unserialized write.
func recordResults(results []evidence.RowResult, recordIDs []string, raw []byte, req Request, deps Deps, mode string) ([]string, int, bool) {
	recording := map[string]bool{}
	for _, id := range recordIDs {
		recording[id] = true
	}
	doc, recorded := string(raw), 0
	var lines []string
	for _, r := range results {
		if r.Verdict != evidence.VerdictAdmitted || !recording[r.ID] {
			continue
		}
		updated, err := plan.RecordDigest(doc, r.ID, r.Digest)
		if err != nil {
			fmt.Fprintln(deps.Err, "plan admit:", err)
			return nil, 0, false
		}
		// The mode is recorded beside the digest, because a digest without the mode it was taken in is not
		// checkable. A plan written before the Mode column existed cannot carry one; an empty cell there already
		// means the host, so a host recording is still true and a sandbox recording is refused rather than
		// written as a claim the plan cannot hold.
		withMode, err := plan.RecordMode(updated, r.ID, mode)
		if err != nil {
			if mode != evidence.ModeHost || !errors.Is(err, plan.ErrNoColumn) {
				fmt.Fprintln(deps.Err, "plan admit:", err)
				return nil, 0, false
			}
		} else {
			updated = withMode
		}
		doc = updated
		recorded++
		lines = append(lines, fmt.Sprintf("%s  RECORDED  %s  (%s mode)\n", r.ID, r.Digest, mode))
	}
	if recorded == 0 {
		return lines, recorded, true
	}
	if err := writeRecorded(req.Path, raw, doc); err != nil {
		fmt.Fprintln(deps.Err, "plan admit:", err)
		return nil, 0, false
	}
	return lines, recorded, true
}

// renameRecorded is a seam for proving that a failed replacement leaves the original plan untouched.
var renameRecorded = os.Rename

// syncRecordedDirectory is a seam for proving that the plan directory is synced after replacement.
var syncRecordedDirectory = func(dir string) error {
	if runtime.GOOS == "windows" {
		// Windows does not support opening a directory handle for syncing; atomic rename is the supported guarantee there.
		return nil
	}
	directory, err := os.Open(dir)
	if err != nil {
		return err
	}
	defer directory.Close()
	return directory.Sync()
}

// writeRecorded replaces the plan with the document the rows recorded, in one transaction.
//
// The document was read before the rows ran and a row can take minutes, so the digests this run observed belong
// to the plan it read: a file another writer changed since is refused rather than overwritten, and the comparison
// and the write it guards cannot be split by a cooperating writer, because every writer takes this lock. A lock
// that cannot be taken is a refusal, never an unserialized write.
func writeRecorded(path string, raw []byte, doc string) error {
	lock, err := lockPlanWithBound(path)
	if err != nil {
		return err
	}
	defer plan.UnlockPlan(lock)
	now, err := os.ReadFile(path)
	if err != nil {
		return err
	}
	if string(now) != string(raw) {
		return fmt.Errorf("%s changed while the rows ran; nothing was written (the digests this run observed belong to the plan it read, not to the one on disk now)", path)
	}
	info, err := os.Stat(path)
	if err != nil {
		return err
	}
	return writeRecordedAtomically(path, []byte(doc), info.Mode().Perm())
}

func lockPlanWithBound(path string) (*os.File, error) {
	result := make(chan planLockResult, 1)
	go func() {
		file, err := plan.LockPlan(path)
		result <- planLockResult{file: file, err: err}
	}()

	timer := time.NewTimer(planLockWait)
	defer timer.Stop()
	select {
	case locked := <-result:
		return locked.file, locked.err
	case <-timer.C:
		go func() {
			locked := <-result
			if locked.file != nil {
				plan.UnlockPlan(locked.file)
			}
		}()
		return nil, fmt.Errorf("timed out waiting for the plan lock on %s after %s", path, planLockWait)
	}
}

// writeRecordedAtomically writes beside the plan, flushes the complete temporary file, and replaces the plan in
// one rename. The cleanup keeps a failed replacement from leaving an ambiguous second plan behind.
func writeRecordedAtomically(path string, content []byte, mode os.FileMode) error {
	temp, err := os.CreateTemp(filepath.Dir(path), "."+filepath.Base(path)+".tmp-*")
	if err != nil {
		return err
	}
	tempPath := temp.Name()
	keepTemp := true
	defer func() {
		if keepTemp {
			_ = temp.Close()
			_ = os.Remove(tempPath)
		}
	}()
	if _, err := temp.Write(content); err != nil {
		return err
	}
	if err := temp.Sync(); err != nil {
		return err
	}
	if err := temp.Close(); err != nil {
		return err
	}
	if err := os.Chmod(tempPath, mode); err != nil {
		return err
	}
	if err := renameRecorded(tempPath, path); err != nil {
		return err
	}
	if err := syncRecordedDirectory(filepath.Dir(path)); err != nil {
		// Propagate the failure: the rename already landed, but reporting success would claim directory-entry
		// durability that was not confirmed. Callers may therefore observe an error for bytes already on disk.
		return err
	}
	keepTemp = false
	return nil
}

// unknownID returns the first id in ids that names no ledger row, or "" when every id names one. An
// empty list names nothing and is never a mistake.
func unknownID(ids []string, rows []plan.LedgerRow) string {
	if len(ids) == 0 {
		return ""
	}
	known := make(map[string]bool, len(rows))
	for _, row := range rows {
		known[row.ID] = true
	}
	for _, id := range ids {
		if !known[id] {
			return id
		}
	}
	return ""
}

// ledgerIDError reports an id a flag named that the ledger does not carry, and lists every id it does
// carry, so a typo is a message on stderr and exit 2 instead of a silent narrowing.
func ledgerIDError(w io.Writer, flag, id string, rows []plan.LedgerRow) int {
	ids := make([]string, 0, len(rows))
	for _, row := range rows {
		ids = append(ids, row.ID)
	}
	available := "(none)"
	if len(ids) > 0 {
		available = strings.Join(ids, ", ")
	}
	fmt.Fprintf(w, "plan admit: %s names %q, which is not a row in the Evidence ledger; available ids: %s\n", flag, id, available)
	return 2
}

// admitIDs turns the comma-separated flag values into the ids Admit narrows to. An empty list narrows
// nothing, which is why the empty string and a list of blanks both come back empty.
func admitIDs(parts []string) []string {
	var ids []string
	for _, part := range parts {
		if id := strings.TrimSpace(part); id != "" {
			ids = append(ids, id)
		}
	}
	return ids
}

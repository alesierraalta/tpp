package plan

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"
)

// ErrUsage marks a refusal of the invocation's own values (exit 2) rather than a refusal by the plan.
var ErrUsage = errors.New("usage error")

// usageError carries the sentinel without printing it, so errors.Is still finds ErrUsage in the chain.
type usageError struct{ err error }

func (u usageError) Error() string { return u.err.Error() }
func (u usageError) Unwrap() error { return ErrUsage }

func usagef(format string, args ...any) error { return usageError{err: fmt.Errorf(format, args...)} }

// Finding is one Findings row: this command never writes the ledger row its evidence cites.
type Finding struct {
	ID          string
	Location    string
	Severity    string
	DataSafe    string
	Evidence    string
	Test        string
	Status      string
	VerdictBy   string
	Reason      string
	Fingerprint string
}

// AddFinding writes one Findings row into the plan at path and returns the 1-based line it landed on. Every
// refusal leaves the file byte-identical: the candidate is held to CheckDocument — the rules `plan check`
// runs — before a single byte moves.
//
// The whole read, validation and write runs under one lock keyed on the plan's canonical path, so two callers
// — two processes as much as two goroutines — queue behind each other instead of each writing a candidate built
// from the same stale read. Without it the last write wins: every other row vanishes from a plan that still
// checks as `well formed`, which is exactly how the loss stayed silent.
//
// The path is canonicalised once, before anything else: a plan reached through a symlink is the file the link
// names, and every spelling of one plan — relative, absolute, a symlink, a file under a symlinked directory —
// has to reach the same lock and the same bytes. Writing through the spelling instead replaced the link with a
// regular file and left the real plan untouched.
func AddFinding(path string, f Finding) (int, error) {
	target := canonicalPath(path)
	lock, err := LockPlanWithin(target, lockWait)
	if err != nil {
		return 0, err
	}
	defer UnlockPlan(lock)
	return addFinding(target, f)
}

// canonicalPath turns any spelling of a plan into the path of the file itself: absolute, and with every symlink
// resolved. EvalSymlinks needs every element to exist, which a plan the operator is about to create does not, so
// it is best effort — the absolute spelling is what remains, and the read that follows reports a plan that is
// not there.
func canonicalPath(path string) string {
	abs, err := filepath.Abs(path)
	if err != nil {
		abs = path
	}
	if real, err := filepath.EvalSymlinks(abs); err == nil {
		return real
	}
	return abs
}

// addFinding is the transaction AddFinding holds the plan's lock across. The read, the candidate and the write
// are one critical section: a candidate built from a document another writer has already replaced is a row that
// never lands, whatever the checker later says about the file.
func addFinding(path string, f Finding) (int, error) {
	raw, err := os.ReadFile(path)
	if err != nil {
		return 0, err
	}
	lines := strings.Split(string(raw), "\n")

	table, err := findingsTable(lines)
	if err != nil {
		return 0, err
	}
	row, err := rowFor(f, table.header)
	if err != nil {
		return 0, err
	}
	if err := refuseDuplicate(table, f.ID); err != nil {
		return 0, err
	}
	if !pathCiteRe.MatchString(f.Location) {
		return 0, usagef("--location %s is not path:line, so nothing can be located", quote(f.Location))
	}
	if err := checkEvidence(lines, f.Evidence); err != nil {
		return 0, err
	}
	return insertRow(path, lines, table, row)
}

// findingsTable locates the table a finding row lands in, and refuses the three shapes that leave it nowhere to
// land: no section, no table, and a table a stray line cut in two — a row in a cut table sits in the file and
// never in the plan, which is how rows get lost.
func findingsTable(lines []string) (tableScan, error) {
	heading, end := sectionRegion(lines, "Findings")
	if heading < 0 {
		return tableScan{}, fmt.Errorf("the plan has no ## Findings section, so there is nowhere to record the row")
	}
	table := scanTable(lines, heading+1, end)
	if table.header == nil {
		return tableScan{}, fmt.Errorf("the Findings section has no table, so the row has no columns to land in")
	}
	if _, message := interruptedTable("Findings", table); message != "" {
		return tableScan{}, fmt.Errorf("%s; writing into a cut table is how rows get lost", message)
	}
	return table, nil
}

// rowFor validates the values the caller supplied and renders the row they describe: the three checks a finding
// owes, the placeholder an absent fingerprint has a spelling for, the fingerprint the checker accepts, and the
// row itself.
func rowFor(f Finding, header []string) (string, error) {
	if err := f.checkValues(); err != nil {
		return "", err
	}
	status, err := f.checkStatus()
	if err != nil {
		return "", err
	}
	if err := f.checkTest(status); err != nil {
		return "", err
	}
	// An absent fingerprint has a spelling: the placeholder the template uses for a digest not computed.
	if strings.TrimSpace(f.Fingerprint) == "" {
		f.Fingerprint = "-"
	}
	if !ValidFindingFingerprint(f.Fingerprint) {
		return "", usagef("--fingerprint %s is not %s", quote(f.Fingerprint), FindingFingerprintForms)
	}
	return f.row(header, status)
}

// refuseDuplicate keeps the writer what the checker assumes: an id names one row, so a row already carrying the
// id is refused rather than overwritten.
func refuseDuplicate(table tableScan, id string) error {
	for _, r := range table.rows {
		if strings.EqualFold(strings.TrimSpace(cell(r.cells, 0)), strings.TrimSpace(id)) {
			return fmt.Errorf("finding %s is already row %d: this command never overwrites a verdict", quote(id), r.line)
		}
	}
	return nil
}

// checkEvidence reads the ids the finding cites against the ledger, exactly as the checker reads them, so the
// writer validates what the checker will: an id that names no ledger row has to be added to the ledger first,
// because this command writes findings only.
func checkEvidence(lines []string, evidence string) error {
	ledger := scanSection(lines, "Evidence ledger")
	known := map[string]bool{}
	for _, r := range ledger.rows {
		known[cell(r.cells, 0)] = true
	}
	for _, part := range strings.FieldsFunc(evidence, func(r rune) bool { return r == ',' || r == ';' || r == '/' || r == ' ' }) {
		id := strings.Trim(part, "`")
		if id == "" || known[id] {
			continue
		}
		return fmt.Errorf("--evidence names %s, which is not a row in the Evidence ledger: add the ledger row first, this command writes findings only", quote(id))
	}
	return nil
}

// insertRow puts the row at the end of the Findings table and writes the plan once, after the checker has accepted
// the document it would produce: a row this command wrote is a row the checker reads.
func insertRow(path string, lines []string, table tableScan, row string) (int, error) {
	after := findingsEnd(table)
	candidate := strings.Join(insertLine(lines, after, row), "\n")
	if problems := CheckDocument(candidate); len(problems) > 0 {
		return 0, fmt.Errorf("the row would not pass plan check: %s; the plan is unchanged", strings.Join(problems, "; "))
	}
	if err := writePlan(path, candidate); err != nil {
		return 0, err
	}
	return after + 1, nil
}

// field is one value the row carries: the flag that names it and whether the row needs it.
type field struct {
	flag     string
	value    string
	required bool
}

// values lists the row's values in flag order; --status, --test and --fingerprint are conditional.
func (f Finding) values() []field {
	return []field{
		{"id", f.ID, true}, {"location", f.Location, true}, {"severity", f.Severity, true},
		{"data-safe", f.DataSafe, true}, {"evidence", f.Evidence, true}, {"test", f.Test, false},
		{"status", f.Status, false}, {"verdict-by", f.VerdictBy, true}, {"reason", f.Reason, true},
		{"fingerprint", f.Fingerprint, false},
	}
}

// checkValues refuses a pasted newline (one row into two) or an empty required value. Newlines first.
func (f Finding) checkValues() error {
	for _, v := range f.values() {
		if strings.ContainsAny(v.value, "\n\r") {
			return usagef("--%s carries a newline: a Findings row is one line", v.flag)
		}
	}
	for _, v := range f.values() {
		if v.required && strings.TrimSpace(v.value) == "" {
			return usagef("--%s is empty and the row needs it", v.flag)
		}
	}
	return nil
}

// checkStatus reads --status against the closed vocabulary the checker enforces, so writer and checker
// cannot drift: an unknown value is refused with the vocabulary rather than silently read as `open`.
func (f Finding) checkStatus() (string, error) {
	status, breach := findingStatus(f.Status)
	if breach != "" {
		return "", usagef("--status %s is not one of: %s", quote(f.Status), FindingsStatusList)
	}
	return status, nil
}

// checkTest is the writer's half of the checker's rule: a settled finding owes its pinning test.
func (f Finding) checkTest(status string) error {
	if settledStatus.MatchString(status) && placeholder.MatchString(strings.TrimSpace(f.Test)) {
		return usagef("--status %s is settled, so --test must name the pinning test that holds the verdict", status)
	}
	return nil
}

// row renders the row in header order and reads it back through the checker's own split for pipe safety.
func (f Finding) row(header []string, status string) (string, error) {
	cells := make([]string, len(header))

	id := idColumn(header)
	if id < 0 {
		return "", fmt.Errorf("the Findings header has no Id column, so the row cannot carry its identifier")
	}
	if placeholder.MatchString(strings.TrimSpace(f.ID)) {
		return "", usagef("--id %s is a placeholder: the checker skips a row whose Id cell is one, so the finding would never be read", quote(f.ID))
	}
	cells[id] = cellValue(f.ID)

	columns := []struct {
		name   string
		value  string
		needed bool
	}{
		{"Finding", f.Location, true},
		{"Severity", f.Severity, true},
		{"Data safe", f.DataSafe, true},
		{"Evidence", f.Evidence, true},
		// A settled finding owes the test; an unsettled one only carries the column when it has a test to
		// write, so a table that never had the column is not asked to grow one.
		{"Pinning test", f.Test, settledStatus.MatchString(status) || !placeholder.MatchString(strings.TrimSpace(f.Test))},
		{"Status", f.Status, true},
		{"Verdict by", f.VerdictBy, true},
		{"Reason", f.Reason, true},
		{"Fingerprint", f.Fingerprint, !placeholder.MatchString(strings.TrimSpace(f.Fingerprint))},
	}
	for _, c := range columns {
		at := columnIndex(header, c.name)
		switch {
		case at >= 0:
			cells[at] = cellValue(c.value)
		case c.needed:
			return "", fmt.Errorf("the Findings header has no %s column, so the row cannot carry it", c.name)
		}
	}

	line := "| " + strings.Join(cells, " | ") + " |"
	if got := len(split(line)); got != len(header) {
		return "", usagef("a value in the row shifts its columns: the row reads as %d cells where the header has %d", got, len(header))
	}
	return line, nil
}

// cellValue escapes a pipe, which would otherwise split the row and move every column to its right.
func cellValue(v string) string { return strings.ReplaceAll(strings.TrimSpace(v), "|", `\|`) }

// idColumn finds the Id column by exact name, so `Evidence id` is never mistaken for it.
func idColumn(header []string) int {
	for i, h := range header {
		if strings.EqualFold(strings.TrimSpace(h), "id") {
			return i
		}
	}
	return -1
}

// findingsEnd is the 1-based line the new row goes after: the last data row, else the table's heading.
func findingsEnd(scan tableScan) int {
	if len(scan.rows) > 0 {
		return scan.rows[len(scan.rows)-1].line
	}
	return scan.head
}

// insertLine puts row after the 1-based line at and leaves every other line where it was.
func insertLine(lines []string, at int, row string) []string {
	out := make([]string, 0, len(lines)+1)
	out = append(out, lines[:at]...)
	out = append(out, row)
	return append(out, lines[at:]...)
}

// LockPlan takes the exclusive lock that serializes every write to the plan at path and returns it.
//
// Every writer of the plan takes this lock — `plan add-finding`, `plan upgrade` and a recording
// `plan admit` — so a check a writer makes about the file it is about to replace cannot be invalidated by
// another writer between the check and the write.
//
// It is an advisory lock on a file in the user's own cache directory, keyed on the plan's canonical absolute
// path, so every spelling of one plan is one lock. A lock is released by the kernel when the process holding it
// exits, so a writer that crashes mid-transaction leaves no stale lock behind; a directory or PID file would
// have to guess whether the owner is still alive, and guessing wrong blocks every later call forever. The lock
// file is deliberately not in the repository and is never unlinked: removing a lock another process may already
// be waiting on is how two writers end up holding two different files for one plan.
//
// A lock that cannot be taken is an error, never a silent write without it: an unserialized write is the lost
// row this lock exists to prevent.
func LockPlan(path string) (*os.File, error) {
	name, err := planLockPath(canonicalPath(path))
	if err != nil {
		return nil, err
	}
	file, err := os.OpenFile(name, os.O_CREATE|os.O_RDWR, 0o600)
	if err != nil {
		return nil, err
	}
	if err := lockFile(file); err != nil {
		file.Close()
		return nil, err
	}
	return file, nil
}

// planLockPath is the file every writer of the plan at key locks: one name per canonical path, inside the
// per-user lock root. Two processes reach the same file only when they derive the same name, and the name
// depends on nothing but the plan.
func planLockPath(key string) (string, error) {
	root, err := lockRoot()
	if err != nil {
		return "", err
	}
	sum := sha256.Sum256([]byte(key))
	return filepath.Join(root, "rdd-plus-plan-"+hex.EncodeToString(sum[:])+".lock"), nil
}

// lockRoot is the private per-user directory the plan locks live in, and it is deliberately not os.TempDir().
// A temporary directory is per-process configuration: two writers of one plan started with different TMPDIR
// values took two locks on two different files, so neither waited for the other, every writer reported success,
// and every row but the last was erased. A directory under the user's cache is the same directory for every
// process of that user, whichever TMPDIR it was handed.
//
// The directory keeps its pre-rename name, rdd-plus, on purpose: a tpp binary and an rdd-plus binary still
// installed beside it must serialize on the same lock file, and a renamed directory would give each its own.
//
// The directory is created 0700 and re-tightened if it already exists, so lock names held in it are not
// something another user can watch or replace. A root that cannot be resolved or created is an error: there is
// no fallback that writes the plan without serialization, because that write is the defect.
func lockRoot() (string, error) {
	base, err := os.UserCacheDir()
	if err != nil {
		return "", fmt.Errorf("the plan lock needs a per-user directory that no TMPDIR changes, and none is available (%w): refusing to write without serialization", err)
	}
	root := filepath.Join(base, "rdd-plus", "plan-locks")
	if err := os.MkdirAll(root, 0o700); err != nil {
		return "", fmt.Errorf("the plan lock directory %s could not be created (%w): refusing to write without serialization", root, err)
	}
	if err := os.Chmod(root, 0o700); err != nil {
		return "", fmt.Errorf("the plan lock directory %s could not be made private (%w): refusing to write without serialization", root, err)
	}
	return root, nil
}

// UnlockPlan releases the lock LockPlan took, and closes it every time the lock was taken — the write path's
// defer, so a refusal on the read or on the candidate gives the lock back. The unlock is best effort: closing
// the descriptor releases the lock anyway, and the kernel releases whatever a process still holds when it exits.
func UnlockPlan(file *os.File) {
	_ = unlockFile(file)
	_ = file.Close()
}

// writePlan replaces path through a temp file in its own directory and a rename, so a reader never sees a
// half-written plan and a crash leaves either the old file or the new one. The mode is the plan's own: replacing
// the destination inode must not widen a file the operator tightened, and a plan created through `plan init`
// keeps the 0644 it was written with. File content durability across a power loss is a separate decision (an
// fsync before the rename) and is deliberately not taken here.
func writePlan(path, body string) error {
	mode := os.FileMode(0o644)
	if info, err := os.Stat(path); err == nil {
		mode = info.Mode().Perm()
	}
	f, err := os.CreateTemp(filepath.Dir(path), "."+filepath.Base(path)+".tmp")
	if err != nil {
		return err
	}
	tmp := f.Name()
	_, err = f.WriteString(body)
	if cerr := f.Close(); err == nil {
		err = cerr
	}
	if err == nil {
		err = os.Chmod(tmp, mode)
	}
	if err == nil {
		err = os.Rename(tmp, path)
	}
	if err != nil {
		os.Remove(tmp)
		return err
	}
	return nil
}

// lockWait bounds how long add-finding and upgrade wait for another writer's lock. A writer holds it for one read
// and one rename, so a lock still held after this long is stuck rather than busy, and blocking forever on it would
// hang the agent that asked.
var lockWait = 10 * time.Second

// LockPlanWithin takes the plan lock as LockPlan does, giving up after wait with an error that names the plan and
// the bound. The wait that gave up keeps running in the background and releases the lock if it is ever granted,
// so a late grant never leaves the plan locked by a caller that already left.
func LockPlanWithin(path string, wait time.Duration) (*os.File, error) {
	type locked struct {
		file *os.File
		err  error
	}
	result := make(chan locked, 1)
	go func() {
		file, err := LockPlan(path)
		result <- locked{file, err}
	}()
	timer := time.NewTimer(wait)
	defer timer.Stop()
	select {
	case got := <-result:
		return got.file, got.err
	case <-timer.C:
		go func() {
			if got := <-result; got.file != nil {
				UnlockPlan(got.file)
			}
		}()
		return nil, fmt.Errorf("timed out waiting for the plan lock on %s after %s", path, wait)
	}
}

package main

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/alesierraalta/tpp/internal/eval"
)

// confirmNovelFixture records a novel-proof/2 proof for the fixture's NOVEL_CANDIDATE
// finding with --by alice, so separation of duties can be probed against a confirmer.
func confirmNovelFixture(t *testing.T, bin string) proveNovelFixture {
	t.Helper()
	fx := writeProveNovelFixture(t, bin, "node --test")
	out, code := runCLI(t, bin, append(withFlag(proveNovelArgs(fx), "--by", "alice"), "--execute")...)
	if code != 0 {
		t.Fatalf("prove-novel --by alice exited %d:\n%s", code, out)
	}
	if !strings.Contains(out, "recorded novel-proof/2 proof for c1/c1-f1") {
		t.Fatalf("prove-novel did not record the proof:\n%s", out)
	}
	return fx
}

func confirmNovelArgs(fx proveNovelFixture, by, reason string) []string {
	return []string{"bench", "eval", "confirm-novel",
		"--eval", fx.runDir, "--case", "c1", "--finding", "c1-f1",
		"--by", by, "--reason", reason, "--bench-dir", fx.benchDir}
}

// backlogPath is the record path the spec fixes: <bench-dir>/backlog/<case>.jsonl.
func backlogPath(fx proveNovelFixture) string {
	return filepath.Join(fx.benchDir, "backlog", "c1.jsonl")
}

// confirmState snapshots everything confirm-novel may never touch on a refusal: every run
// file plus the backlog file (absence is a state too).
type confirmState struct {
	run           map[string][]byte
	backlog       []byte
	backlogExists bool
}

func snapshotConfirmState(t *testing.T, fx proveNovelFixture) confirmState {
	t.Helper()
	state := confirmState{run: snapshotRunDir(t, fx.runDir)}
	data, err := os.ReadFile(backlogPath(fx))
	switch {
	case err == nil:
		state.backlog, state.backlogExists = data, true
	case os.IsNotExist(err):
	default:
		t.Fatalf("read backlog: %v", err)
	}
	return state
}

// assertConfirmStateUnchanged fails on any byte or file-set difference in the run
// directory or the backlog file.
func assertConfirmStateUnchanged(t *testing.T, fx proveNovelFixture, before confirmState) {
	t.Helper()
	after := snapshotConfirmState(t, fx)
	if len(after.run) != len(before.run) {
		t.Fatalf("refused confirm-novel changed the run dir file set: %d files -> %d files", len(before.run), len(after.run))
	}
	for rel, want := range before.run {
		if got, ok := after.run[rel]; !ok {
			t.Fatalf("refused confirm-novel removed %s", rel)
		} else if !bytes.Equal(got, want) {
			t.Fatalf("refused confirm-novel mutated %s", rel)
		}
	}
	if after.backlogExists != before.backlogExists {
		t.Fatalf("refused confirm-novel changed backlog existence: %t -> %t", before.backlogExists, after.backlogExists)
	}
	if before.backlogExists && !bytes.Equal(after.backlog, before.backlog) {
		t.Fatal("refused confirm-novel mutated the backlog file")
	}
}

func readBacklogRecords(t *testing.T, fx proveNovelFixture) []eval.NovelBacklogRecord {
	t.Helper()
	data, err := os.ReadFile(backlogPath(fx))
	if err != nil {
		if os.IsNotExist(err) {
			return nil
		}
		t.Fatalf("read backlog: %v", err)
	}
	var records []eval.NovelBacklogRecord
	for _, line := range strings.Split(strings.TrimRight(string(data), "\n"), "\n") {
		if line == "" {
			continue
		}
		var record eval.NovelBacklogRecord
		if err := json.Unmarshal([]byte(line), &record); err != nil {
			t.Fatalf("decode backlog record %q: %v", line, err)
		}
		records = append(records, record)
	}
	return records
}

// novelConfirmEvents returns every novel_confirm event of the fixture's finding.
func novelConfirmEvents(t *testing.T, fx proveNovelFixture) []eval.Event {
	t.Helper()
	data, err := os.ReadFile(filepath.Join(fx.runDir, "c1", "events.jsonl"))
	if err != nil {
		t.Fatal(err)
	}
	var events []eval.Event
	for _, line := range strings.Split(strings.TrimRight(string(data), "\n"), "\n") {
		if line == "" {
			continue
		}
		var event eval.Event
		if err := json.Unmarshal([]byte(line), &event); err != nil {
			t.Fatal(err)
		}
		if event.Kind == eval.EventNovelConfirm {
			events = append(events, event)
		}
	}
	return events
}

// restoreRunDir rolls the run directory back to a snapshot, dropping any file the
// snapshot does not hold — the run-side state of a crash between the backlog append and
// the promotion write.
func restoreRunDir(t *testing.T, runDir string, snapshot map[string][]byte) {
	t.Helper()
	for rel := range snapshotRunDir(t, runDir) {
		if _, kept := snapshot[rel]; !kept {
			if err := os.Remove(filepath.Join(runDir, rel)); err != nil {
				t.Fatal(err)
			}
		}
	}
	for rel, data := range snapshot {
		if err := os.WriteFile(filepath.Join(runDir, rel), data, 0o644); err != nil {
			t.Fatal(err)
		}
	}
}

// TestBenchEvalConfirmNovelPromotesAndAppendsBacklogRecord is the positive path: a
// confirmer other than the proof's adjudicator promotes the finding, exactly one
// novel_confirm event records the declared by/reason, and exactly one backlog record
// binds to the proof event digest NovelProofs() exposes.
func TestBenchEvalConfirmNovelPromotesAndAppendsBacklogRecord(t *testing.T) {
	bin := buildCLI(t)
	fx := confirmNovelFixture(t, bin)
	before := snapshotConfirmState(t, fx)

	out, code := runCLI(t, bin, confirmNovelArgs(fx, "bob", "independent second look")...)
	if code != 0 {
		t.Fatalf("confirm-novel exited %d:\n%s", code, out)
	}
	if !strings.Contains(out, "confirmed novel c1/c1-f1") {
		t.Fatalf("success output missing the promotion line:\n%s", out)
	}

	stored, err := eval.LoadRun(fx.runDir)
	if err != nil {
		t.Fatal(err)
	}
	caseRun := stored.CaseRuns["c1"]
	if got := caseRun.FindingState("c1-f1"); got != eval.FindingConfirmedNovel {
		t.Fatalf("finding state = %s, want CONFIRMED_NOVEL", got)
	}
	confirmEvents := novelConfirmEvents(t, fx)
	if len(confirmEvents) != 1 {
		t.Fatalf("log holds %d novel_confirm events, want exactly 1", len(confirmEvents))
	}
	var payload struct {
		By     string `json:"by"`
		Reason string `json:"reason"`
	}
	if err := json.Unmarshal(confirmEvents[0].Payload, &payload); err != nil {
		t.Fatal(err)
	}
	if payload.By != "bob" || payload.Reason != "independent second look" {
		t.Fatalf("confirm payload by/reason = %q/%q, want the declared confirmer and reason", payload.By, payload.Reason)
	}

	records := readBacklogRecords(t, fx)
	if len(records) != 1 {
		t.Fatalf("backlog holds %d records, want exactly 1", len(records))
	}
	binding, ok := caseRun.NovelProofs()["c1-f1"]
	if !ok {
		t.Fatal("confirmed finding lost its effective novel proof")
	}
	if records[0].ProofEventDigest != binding.EventDigest {
		t.Fatalf("backlog ProofEventDigest = %s, want NovelProofs() digest %s", records[0].ProofEventDigest, binding.EventDigest)
	}
	if records[0].FindingID != "c1-f1" || records[0].CaseID != "c1" {
		t.Fatalf("backlog record identity = %s/%s, want c1/c1-f1", records[0].CaseID, records[0].FindingID)
	}
	if records[0].RunID == "" {
		t.Fatal("backlog record run id is blank")
	}
	// The record must carry the finding, its evidence, the reproduction, and a proposed
	// Issue entry as JSON objects (the schema's contract for close/verify reconciliation).
	for name, raw := range map[string]json.RawMessage{
		"finding": records[0].Finding, "evidence": records[0].Evidence,
		"reproduction": records[0].Reproduction, "proposed_issue": records[0].ProposedIssue,
	} {
		var object map[string]json.RawMessage
		if len(raw) == 0 || json.Unmarshal(raw, &object) != nil || object == nil {
			t.Fatalf("backlog %s is not a JSON object: %s", name, raw)
		}
	}
	// run.json and caserun.json describe the same run before and after: only the event
	// chain and the backlog file may change.
	after := snapshotConfirmState(t, fx)
	for _, rel := range []string{"run.json", filepath.Join("caserun.json")} {
		if !bytes.Equal(after.run[rel], before.run[rel]) {
			t.Fatalf("%s changed although only an event may be appended", rel)
		}
	}
	if bytes.Equal(after.run[filepath.Join("c1", "events.jsonl")], before.run[filepath.Join("c1", "events.jsonl")]) {
		t.Fatal("events.jsonl gained no event")
	}
}

// TestBenchEvalConfirmNovelRefusesTheProofsAdjudicator pins separation of duties: the
// proof's own adjudicator may not confirm it, and the refusal mutates nothing.
func TestBenchEvalConfirmNovelRefusesTheProofsAdjudicator(t *testing.T) {
	bin := buildCLI(t)
	fx := confirmNovelFixture(t, bin)
	before := snapshotConfirmState(t, fx)

	out, code := runCLI(t, bin, confirmNovelArgs(fx, "alice", "trying to confirm my own proof")...)
	if code != 1 {
		t.Fatalf("same-person confirm exited %d, want 1:\n%s", code, out)
	}
	if !strings.Contains(out, "separation of duties") {
		t.Fatalf("refusal must name separation of duties:\n%s", out)
	}
	if !strings.Contains(out, "alice") {
		t.Fatalf("refusal must name the declared identity:\n%s", out)
	}
	assertConfirmStateUnchanged(t, fx, before)
	if _, err := os.Stat(backlogPath(fx)); !os.IsNotExist(err) {
		t.Fatalf("refused confirm-novel created the backlog file: %v", err)
	}
}

// TestBenchEvalConfirmNovelRerunIsIdempotent: confirming an already-confirmed finding
// bound to the same proof with the record in place exits 0 and mutates nothing.
func TestBenchEvalConfirmNovelRerunIsIdempotent(t *testing.T) {
	bin := buildCLI(t)
	fx := confirmNovelFixture(t, bin)
	args := confirmNovelArgs(fx, "bob", "independent second look")
	if out, code := runCLI(t, bin, args...); code != 0 {
		t.Fatalf("first confirm exited %d:\n%s", code, out)
	}
	before := snapshotConfirmState(t, fx)

	out, code := runCLI(t, bin, args...)
	if code != 0 {
		t.Fatalf("rerun exited %d, want 0:\n%s", code, out)
	}
	if !strings.Contains(out, "already confirmed") {
		t.Fatalf("rerun must report already confirmed:\n%s", out)
	}
	assertConfirmStateUnchanged(t, fx, before)
	if got := len(novelConfirmEvents(t, fx)); got != 1 {
		t.Fatalf("rerun appended %d confirm events, want still exactly 1", got)
	}
	if got := len(readBacklogRecords(t, fx)); got != 1 {
		t.Fatalf("rerun left %d backlog records, want exactly 1", got)
	}
}

// TestBenchEvalConfirmNovelHealsBacklogFirstCrash pins the crash order: the backlog
// record lands first (here re-created through the primitive alone) and the run writes are
// lost, so rerunning the same command completes the promotion without a duplicate record.
func TestBenchEvalConfirmNovelHealsBacklogFirstCrash(t *testing.T) {
	bin := buildCLI(t)
	fx := confirmNovelFixture(t, bin)
	pre := snapshotRunDir(t, fx.runDir)
	args := confirmNovelArgs(fx, "bob", "independent second look")

	if out, code := runCLI(t, bin, args...); code != 0 {
		t.Fatalf("confirm exited %d:\n%s", code, out)
	}
	records := readBacklogRecords(t, fx)
	if len(records) != 1 {
		t.Fatalf("backlog holds %d records after confirm, want exactly 1", len(records))
	}

	// Crash state: the backlog append persisted, the run-side writes did not.
	restoreRunDir(t, fx.runDir, pre)
	if err := os.Remove(backlogPath(fx)); err != nil {
		t.Fatal(err)
	}
	if err := eval.AppendNovelBacklog(absolutePath(fx.benchDir), records[0]); err != nil {
		t.Fatalf("primitive backlog append: %v", err)
	}
	healBefore := snapshotConfirmState(t, fx)

	out, code := runCLI(t, bin, args...)
	if code != 0 {
		t.Fatalf("heal rerun exited %d:\n%s", code, out)
	}
	after := snapshotConfirmState(t, fx)
	if !after.backlogExists || !bytes.Equal(after.backlog, healBefore.backlog) {
		t.Fatal("healing rerun changed the backlog file")
	}
	healed := readBacklogRecords(t, fx)
	if len(healed) != 1 || !bytes.Equal(mustCanonicalRecord(t, healed[0]), mustCanonicalRecord(t, records[0])) {
		t.Fatalf("healing rerun left %d record(s); want the single original record", len(healed))
	}
	stored, err := eval.LoadRun(fx.runDir)
	if err != nil {
		t.Fatal(err)
	}
	healedRun := stored.CaseRuns["c1"]
	if got := healedRun.FindingState("c1-f1"); got != eval.FindingConfirmedNovel {
		t.Fatalf("finding state after heal = %s, want CONFIRMED_NOVEL", got)
	}
	if got := len(novelConfirmEvents(t, fx)); got != 1 {
		t.Fatalf("heal log holds %d confirm events, want exactly 1", got)
	}
}

func mustCanonicalRecord(t *testing.T, record eval.NovelBacklogRecord) []byte {
	t.Helper()
	data, err := json.Marshal(record)
	if err != nil {
		t.Fatal(err)
	}
	return data
}

// TestBenchEvalConfirmNovelRepairsMissingBacklogRecord: a confirmed finding whose
// backlog record vanished is repaired by the same command, without touching the run.
func TestBenchEvalConfirmNovelRepairsMissingBacklogRecord(t *testing.T) {
	bin := buildCLI(t)
	fx := confirmNovelFixture(t, bin)
	args := confirmNovelArgs(fx, "bob", "independent second look")
	if out, code := runCLI(t, bin, args...); code != 0 {
		t.Fatalf("confirm exited %d:\n%s", code, out)
	}
	wanted, err := os.ReadFile(backlogPath(fx))
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Remove(backlogPath(fx)); err != nil {
		t.Fatal(err)
	}
	beforeRun := snapshotRunDir(t, fx.runDir)

	out, code := runCLI(t, bin, args...)
	if code != 0 {
		t.Fatalf("repair rerun exited %d:\n%s", code, out)
	}
	if !strings.Contains(out, "repaired") {
		t.Fatalf("repair rerun must say the record was repaired:\n%s", out)
	}
	// The run side stays byte-identical; only the backlog file is recreated.
	afterRun := snapshotRunDir(t, fx.runDir)
	if len(afterRun) != len(beforeRun) {
		t.Fatalf("repair changed the run dir file set: %d files -> %d files", len(beforeRun), len(afterRun))
	}
	for rel, want := range beforeRun {
		if got, ok := afterRun[rel]; !ok {
			t.Fatalf("repair removed %s", rel)
		} else if !bytes.Equal(got, want) {
			t.Fatalf("repair mutated %s", rel)
		}
	}
	repaired, err := os.ReadFile(backlogPath(fx))
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(repaired, wanted) {
		t.Fatalf("repaired backlog = %s, want the original record %s", repaired, wanted)
	}
	if got := len(readBacklogRecords(t, fx)); got != 1 {
		t.Fatalf("repair left %d records, want exactly 1", got)
	}
}

// TestBenchEvalConfirmNovelRefusesTamperedBacklogRecord: a backlog record whose proof
// digest no longer matches the effective proof is refused before any write.
func TestBenchEvalConfirmNovelRefusesTamperedBacklogRecord(t *testing.T) {
	bin := buildCLI(t)
	fx := confirmNovelFixture(t, bin)
	args := confirmNovelArgs(fx, "bob", "independent second look")
	out, code := runCLI(t, bin, args...)
	if code != 0 {
		t.Fatalf("confirm exited %d:\n%s", code, out)
	}
	data, err := os.ReadFile(backlogPath(fx))
	if err != nil {
		t.Fatal(err)
	}
	records := readBacklogRecords(t, fx)
	if len(records) != 1 {
		t.Fatalf("backlog holds %d records, want exactly 1", len(records))
	}
	// Swap the digest for another valid sha256 value, keeping the line canonical.
	tampered := bytes.Replace(data,
		[]byte(records[0].ProofEventDigest),
		[]byte("sha256:"+strings.Repeat("f", 64)), 1)
	if bytes.Equal(tampered, data) {
		t.Fatal("no digest found to tamper")
	}
	if err := os.WriteFile(backlogPath(fx), tampered, 0o644); err != nil {
		t.Fatal(err)
	}
	before := snapshotConfirmState(t, fx)

	out, code = runCLI(t, bin, args...)
	if code != 1 {
		t.Fatalf("tampered backlog confirm exited %d, want 1:\n%s", code, out)
	}
	if !strings.Contains(out, "backlog") {
		t.Fatalf("refusal must name the backlog record:\n%s", out)
	}
	assertConfirmStateUnchanged(t, fx, before)
}

// TestBenchEvalConfirmNovelRefusesWithoutProof: promotion requires the effective
// row-bound novel proof prove-novel records; without it nothing is written.
func TestBenchEvalConfirmNovelRefusesWithoutProof(t *testing.T) {
	bin := buildCLI(t)
	fx := writeProveNovelFixture(t, bin, "node --test")
	before := snapshotConfirmState(t, fx)

	out, code := runCLI(t, bin, confirmNovelArgs(fx, "bob", "no proof exists")...)
	if code != 1 {
		t.Fatalf("proofless confirm exited %d, want 1:\n%s", code, out)
	}
	if !strings.Contains(out, "novel proof") {
		t.Fatalf("refusal must name the missing novel proof:\n%s", out)
	}
	assertConfirmStateUnchanged(t, fx, before)
	if _, err := os.Stat(backlogPath(fx)); !os.IsNotExist(err) {
		t.Fatalf("refused confirm-novel created the backlog file: %v", err)
	}
}

// TestBenchEvalConfirmNovelRequiresReason: a blank --reason fails the flag gate before
// the run directory is touched.
func TestBenchEvalConfirmNovelRequiresReason(t *testing.T) {
	bin := buildCLI(t)
	fx := writeProveNovelFixture(t, bin, "node --test")
	before := snapshotConfirmState(t, fx)

	out, code := runCLI(t, bin, withoutFlag(confirmNovelArgs(fx, "bob", "x"), "--reason")...)
	if code != 2 {
		t.Fatalf("missing --reason exited %d, want 2:\n%s", code, out)
	}
	if !strings.Contains(out, "--reason") {
		t.Fatalf("flag refusal must name --reason:\n%s", out)
	}
	assertConfirmStateUnchanged(t, fx, before)
}

// TestBenchEvalConfirmNovelListedInUsageAndHelp pins the synopsis entry and the
// identity contract: separation of duties is by declared, unauthenticated identity.
func TestBenchEvalConfirmNovelListedInUsageAndHelp(t *testing.T) {
	bin := buildCLI(t)
	out, code := runCLI(t, bin, "bench", "eval")
	if code != 2 || !strings.Contains(out, "confirm-novel") {
		t.Fatalf("bench eval usage (exit %d) must list confirm-novel:\n%s", code, out)
	}
	out, code = runCLI(t, bin, "bench", "eval", "confirm-novel", "-h")
	if code != 2 {
		t.Fatalf("confirm-novel -h exited %d, want 2:\n%s", code, out)
	}
	for _, want := range []string{"self-declared", "separation of duties"} {
		if !strings.Contains(out, want) {
			t.Fatalf("confirm-novel help must state %q:\n%s", want, out)
		}
	}
}

// TestBenchEvalConfirmNovelAppendsSecondRecordAfterReopen pins the append-only history
// rule: a reopened finding, proven and confirmed again, earns a second record bound to
// the new proof while the first record stays untouched — and the rerun stays idempotent.
func TestBenchEvalConfirmNovelAppendsSecondRecordAfterReopen(t *testing.T) {
	bin := buildCLI(t)
	fx := confirmNovelFixture(t, bin)
	args := confirmNovelArgs(fx, "bob", "independent second look")
	if out, code := runCLI(t, bin, args...); code != 0 {
		t.Fatalf("first confirm exited %d:\n%s", code, out)
	}
	firstBytes, err := os.ReadFile(backlogPath(fx))
	if err != nil {
		t.Fatal(err)
	}

	// Reopen the confirmed finding and park it back at NOVEL_CANDIDATE through the same
	// NONE-match decision the fixture uses.
	if out, code := runCLI(t, bin, "bench", "eval", "reopen",
		"--eval", fx.runDir, "--case", "c1", "--finding", "c1-f1",
		"--by", "reviewer", "--reason", "needs a second proof"); code != 0 {
		t.Fatalf("reopen exited %d:\n%s", code, out)
	}
	if out, code := runCLI(t, bin, "bench", "eval", "adjudicate",
		"--eval", fx.runDir, "--case", "c1", "--finding", "c1-f1",
		"--issue", "issue-a", "--c1", "false", "--c3", "false", "--c4-shows", "false",
		"--by", "reviewer", "--reason", "no known issue matches"); code != 0 {
		t.Fatalf("re-adjudicate exited %d:\n%s", code, out)
	}
	if out, code := runCLI(t, bin, append(withFlag(proveNovelArgs(fx), "--by", "alice"), "--execute")...); code != 0 {
		t.Fatalf("second prove-novel exited %d:\n%s", code, out)
	}

	out, code := runCLI(t, bin, confirmNovelArgs(fx, "bob", "second independent look")...)
	if code != 0 {
		t.Fatalf("second confirm exited %d:\n%s", code, out)
	}
	records := readBacklogRecords(t, fx)
	if len(records) != 2 {
		t.Fatalf("backlog holds %d records after the second promotion, want exactly 2", len(records))
	}
	after, err := os.ReadFile(backlogPath(fx))
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.HasPrefix(after, firstBytes) {
		t.Fatal("the second promotion rewrote the first backlog record")
	}
	if records[0].ProofEventDigest == records[1].ProofEventDigest {
		t.Fatalf("both records bind to proof digest %s; the second must bind to the new proof", records[0].ProofEventDigest)
	}

	// Idempotent rerun: both records stay, nothing changes.
	before := snapshotConfirmState(t, fx)
	out, code = runCLI(t, bin, confirmNovelArgs(fx, "bob", "second independent look")...)
	if code != 0 {
		t.Fatalf("rerun exited %d:\n%s", code, out)
	}
	if !strings.Contains(out, "already confirmed") {
		t.Fatalf("rerun must report already confirmed:\n%s", out)
	}
	assertConfirmStateUnchanged(t, fx, before)
	if got := len(readBacklogRecords(t, fx)); got != 2 {
		t.Fatalf("rerun left %d records, want still 2", got)
	}
}

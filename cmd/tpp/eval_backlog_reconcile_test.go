package main

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// closeBacklogArgs runs close against the fixture's own backlog root: the reconciliation
// reads <bench-dir>/backlog/<case>.jsonl, so --bench-dir ties the refusal to confirm-novel's
// records the same way the promotion command ties its append to the flag.
func closeBacklogArgs(fx proveNovelFixture) []string {
	return []string{"bench", "eval", "close", "--eval", fx.runDir, "--bench-dir", fx.benchDir}
}

// readyToCloseFixture drives the decided flow — adjudicate (fixture) -> prove-novel (alice,
// inside confirmNovelFixture) -> confirm-novel (bob) — and resolves the fixture's second
// finding so close has no unresolved finding left to refuse on for another reason.
func readyToCloseFixture(t *testing.T, bin string) proveNovelFixture {
	t.Helper()
	fx := confirmNovelFixture(t, bin)
	if out, code := runCLI(t, bin, "bench", "eval", "adjudicate", "--eval", fx.runDir,
		"--case", "c1", "--finding", "c1-f2", "--outcome", "FP",
		"--by", "reviewer", "--reason", "the claimed defect is not there"); code != 0 {
		t.Fatalf("adjudicate c1-f2 exited %d:\n%s", code, out)
	}
	if out, code := runCLI(t, bin, confirmNovelArgs(fx, "bob", "independent second look")...); code != 0 {
		t.Fatalf("confirm-novel exited %d:\n%s", code, out)
	}
	return fx
}

// assertRefusedCloseUnchanged pins fail-closed: a refused close leaves every run file and
// the backlog byte-identical (a missing backlog file is a state too).
func assertRefusedCloseUnchanged(t *testing.T, fx proveNovelFixture, before confirmState) {
	t.Helper()
	after := snapshotConfirmState(t, fx)
	if len(after.run) != len(before.run) {
		t.Fatalf("refused close changed the run dir file set: %d files -> %d files", len(before.run), len(after.run))
	}
	for rel, want := range before.run {
		if got, ok := after.run[rel]; !ok {
			t.Fatalf("refused close removed %s", rel)
		} else if !bytes.Equal(got, want) {
			t.Fatalf("refused close mutated %s", rel)
		}
	}
	if after.backlogExists != before.backlogExists {
		t.Fatalf("refused close changed backlog existence: %t -> %t", before.backlogExists, after.backlogExists)
	}
	if before.backlogExists && !bytes.Equal(after.backlog, before.backlog) {
		t.Fatal("refused close mutated the backlog file")
	}
}

// backlogTamper is one way the spec-required backlog evidence breaks; mutate applies it on
// top of the pristine bytes, want lists the refusal substrings the command must print.
type backlogTamper struct {
	name   string
	want   []string
	mutate func(t *testing.T, fx proveNovelFixture, pristine []byte)
}

func backlogTampers() []backlogTamper {
	return []backlogTamper{
		{name: "record deleted", want: []string{"case c1 finding c1-f1", "tpp bench eval confirm-novel"},
			mutate: func(t *testing.T, fx proveNovelFixture, _ []byte) {
				t.Helper()
				if err := os.Remove(backlogPath(fx)); err != nil {
					t.Fatal(err)
				}
			}},
		{name: "proof event digest edited", want: []string{"tampered backlog", "proof event digest"},
			mutate: func(t *testing.T, fx proveNovelFixture, _ []byte) {
				t.Helper()
				records := readBacklogRecords(t, fx)
				if len(records) != 1 {
					t.Fatalf("backlog holds %d records, want 1", len(records))
				}
				data, err := os.ReadFile(backlogPath(fx))
				if err != nil {
					t.Fatal(err)
				}
				tampered := bytes.Replace(data, []byte(records[0].ProofEventDigest),
					[]byte("sha256:"+strings.Repeat("f", 64)), 1)
				if bytes.Equal(tampered, data) {
					t.Fatal("no proof event digest found to edit")
				}
				if err := os.WriteFile(backlogPath(fx), tampered, 0o644); err != nil {
					t.Fatal(err)
				}
			}},
		{name: "record for an unknown proof appended", want: []string{"tampered backlog", "case c1"},
			mutate: func(t *testing.T, fx proveNovelFixture, pristine []byte) {
				t.Helper()
				records := readBacklogRecords(t, fx)
				if len(records) != 1 {
					t.Fatalf("backlog holds %d records, want 1", len(records))
				}
				records[0].ProofEventDigest = "sha256:" + strings.Repeat("e", 64)
				line, err := json.Marshal(records[0])
				if err != nil {
					t.Fatal(err)
				}
				out := append(append([]byte(nil), pristine...), line...)
				out = append(out, '\n')
				if err := os.WriteFile(backlogPath(fx), out, 0o644); err != nil {
					t.Fatal(err)
				}
			}},
		{name: "backlog file truncated mid-line", want: []string{"incomplete final line"},
			mutate: func(t *testing.T, fx proveNovelFixture, pristine []byte) {
				t.Helper()
				if len(pristine) < 20 {
					t.Fatalf("pristine backlog too short to truncate: %d bytes", len(pristine))
				}
				if err := os.WriteFile(backlogPath(fx), pristine[:len(pristine)-10], 0o644); err != nil {
					t.Fatal(err)
				}
			}},
	}
}

// TestBenchEvalCloseRefusesBrokenBacklogRecord is the close half of B2c-2: after a real
// confirm-novel promotion, every way the backlog evidence breaks (deleted, digest edited,
// unknown-proof record appended, file truncated) must refuse close byte-identically and
// point at the repair; the restored pristine record lets the same close succeed, so the
// refusal comes from the reconciliation itself.
func TestBenchEvalCloseRefusesBrokenBacklogRecord(t *testing.T) {
	bin := buildCLI(t)
	fx := readyToCloseFixture(t, bin)
	pristine, err := os.ReadFile(backlogPath(fx))
	if err != nil {
		t.Fatal(err)
	}
	if got := len(readBacklogRecords(t, fx)); got != 1 {
		t.Fatalf("backlog holds %d records after confirm, want 1", got)
	}
	for _, tc := range backlogTampers() {
		t.Run(tc.name, func(t *testing.T) {
			defer func() {
				if err := os.WriteFile(backlogPath(fx), pristine, 0o644); err != nil {
					t.Fatal(err)
				}
			}()
			tc.mutate(t, fx, pristine)
			before := snapshotConfirmState(t, fx)

			out, code := runCLI(t, bin, closeBacklogArgs(fx)...)
			if code != 1 {
				t.Fatalf("close over %s exited %d, want refusal 1:\n%s", tc.name, code, out)
			}
			for _, want := range tc.want {
				if !strings.Contains(out, want) {
					t.Fatalf("close refusal over %s must contain %q:\n%s", tc.name, want, out)
				}
			}
			assertRefusedCloseUnchanged(t, fx, before)
		})
	}

	out, code := runCLI(t, bin, closeBacklogArgs(fx)...)
	if code != 0 {
		t.Fatalf("close with the restored backlog exited %d:\n%s", code, out)
	}
	if !strings.Contains(out, "closed run 1") {
		t.Fatalf("close output missing the sealed-run line:\n%s", out)
	}
}

// TestBenchEvalCloseAndVerifyWithBacklogRecord is the pristine control of B2c-2: the
// decided flow adjudicate -> prove-novel (alice) -> confirm-novel (bob) -> close succeeds
// with the record in place, and compare plus verify pass against the same backlog root;
// breaking the record afterwards must fail both, with verify recomputing the same message.
func TestBenchEvalCloseAndVerifyWithBacklogRecord(t *testing.T) {
	bin := buildCLI(t)
	fx := readyToCloseFixture(t, bin)

	out, code := runCLI(t, bin, closeBacklogArgs(fx)...)
	if code != 0 {
		t.Fatalf("close exited %d:\n%s", code, out)
	}
	if !strings.Contains(out, "closed run 1") {
		t.Fatalf("close output missing the sealed-run line:\n%s", out)
	}

	evalDir := filepath.Dir(fx.runDir)
	comparisonDir := filepath.Join(fx.root, "comparison")
	compareArgs := []string{"bench", "eval", "compare",
		"--baseline", evalDir, "--candidate", evalDir,
		"--manifest", filepath.Join(fx.root, "manifest.json"),
		"--policy", filepath.Join(fx.root, "policy.json"),
		"--out", comparisonDir, "--bench-dir", fx.benchDir}
	out, code = runCLI(t, bin, compareArgs...)
	if _, err := os.Stat(filepath.Join(comparisonDir, "decision.json")); err != nil {
		t.Fatalf("pristine compare wrote no decision.json (exit %d): %v\n%s", code, err, out)
	}
	verifyArgs := []string{"bench", "eval", "verify", "--comparison", comparisonDir, "--bench-dir", fx.benchDir}
	if out, code = runCLI(t, bin, verifyArgs...); code != 0 {
		t.Fatalf("pristine verify exited %d:\n%s", code, out)
	}
	if !strings.Contains(out, "verified decision.json") {
		t.Fatalf("verify output missing its success line:\n%s", out)
	}

	// Delete the record on the sealed run: verify recomputes the comparison and must fail
	// on the reconciliation, and compare must refuse to rebuild the decision at all.
	pristine, err := os.ReadFile(backlogPath(fx))
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Remove(backlogPath(fx)); err != nil {
		t.Fatal(err)
	}
	out, code = runCLI(t, bin, verifyArgs...)
	if code != 1 {
		t.Fatalf("verify with the record deleted exited %d, want refusal 1:\n%s", code, out)
	}
	if !strings.Contains(out, "case c1 finding c1-f1") || !strings.Contains(out, "confirm-novel") {
		t.Fatalf("verify refusal must name the case, finding, and repair:\n%s", out)
	}
	out, code = runCLI(t, bin, compareArgs...)
	if code != 1 {
		t.Fatalf("compare with the record deleted exited %d, want refusal 1:\n%s", code, out)
	}
	if !strings.Contains(out, "backlog") {
		t.Fatalf("compare refusal must name the backlog:\n%s", out)
	}
	if err := os.WriteFile(backlogPath(fx), pristine, 0o644); err != nil {
		t.Fatal(err)
	}
}

// TestBenchEvalCloseAcceptsBacklogHistoryAfterReopen pins append-only history on the close
// side: reopen -> reprove -> reconfirm leaves two records, the first stale. Both are bound
// by a recorded confirmation (an earlier epoch is allowed), the second is the effective one,
// so close succeeds — history is evidence, not tampering.
func TestBenchEvalCloseAcceptsBacklogHistoryAfterReopen(t *testing.T) {
	bin := buildCLI(t)
	fx := readyToCloseFixture(t, bin)

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
	if out, code := runCLI(t, bin, confirmNovelArgs(fx, "bob", "second independent look")...); code != 0 {
		t.Fatalf("second confirm exited %d:\n%s", code, out)
	}
	records := readBacklogRecords(t, fx)
	if len(records) != 2 {
		t.Fatalf("backlog holds %d records, want 2 (first stale, second effective)", len(records))
	}
	if records[0].ProofEventDigest == records[1].ProofEventDigest {
		t.Fatalf("both records bind proof digest %s; the second must bind the new proof", records[0].ProofEventDigest)
	}

	out, code := runCLI(t, bin, closeBacklogArgs(fx)...)
	if code != 0 {
		t.Fatalf("close over append-only backlog history exited %d:\n%s", code, out)
	}
	if !strings.Contains(out, "closed run 1") {
		t.Fatalf("close output missing the sealed-run line:\n%s", out)
	}
}

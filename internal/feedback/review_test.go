package feedback

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"testing"
)

func reviewTestConfigDir(t *testing.T) string {
	t.Helper()
	t.Setenv("TPP_HOME", t.TempDir())
	return t.TempDir()
}

func reviewTestReport(ts string) Report {
	return Report{
		TS: ts, Repo: "repo-" + ts, Plan: "plan-" + ts, Skill: "skill 1.0.0", Build: "build",
		Paid: "paid", Cost: "one hour", Reason: "reason", Verdict: VerdictPaid,
	}
}

func reviewCursorPath(configDir string) string {
	return filepath.Join(filepath.Dir(LedgerPath(configDir)), "run-feedback.reviewed.json")
}

func forgedReviewToken(token string) string {
	forged := []byte(token)
	for i := len(forged) - 1; i >= 0; i-- {
		switch {
		case forged[i] >= '0' && forged[i] < '9':
			forged[i]++
			return string(forged)
		case forged[i] == '9':
			forged[i] = 'a'
			return string(forged)
		case forged[i] >= 'a' && forged[i] < 'f':
			forged[i]++
			return string(forged)
		case forged[i] == 'f':
			forged[i] = '0'
			return string(forged)
		case forged[i] >= 'A' && forged[i] < 'F':
			forged[i]++
			return string(forged)
		case forged[i] == 'F':
			forged[i] = '0'
			return string(forged)
		}
	}
	return token + ".forged"
}

func writeReviewLedger(t *testing.T, configDir string, reports ...Report) {
	t.Helper()
	path := LedgerPath(configDir)
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		t.Fatalf("create telemetry directory: %v", err)
	}
	var contents []byte
	for _, report := range reports {
		line, err := json.Marshal(report)
		if err != nil {
			t.Fatalf("marshal report: %v", err)
		}
		contents = append(contents, line...)
		contents = append(contents, '\n')
	}
	if err := os.WriteFile(path, contents, 0o600); err != nil {
		t.Fatalf("write feedback ledger: %v", err)
	}
}

func appendReviewLedger(t *testing.T, configDir string, reports ...Report) {
	t.Helper()
	path := LedgerPath(configDir)
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		t.Fatalf("create telemetry directory: %v", err)
	}
	file, err := os.OpenFile(path, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o600)
	if err != nil {
		t.Fatalf("open feedback ledger for append: %v", err)
	}
	for _, report := range reports {
		line, err := json.Marshal(report)
		if err != nil {
			_ = file.Close()
			t.Fatalf("marshal report: %v", err)
		}
		if _, err := file.Write(append(line, '\n')); err != nil {
			_ = file.Close()
			t.Fatalf("append feedback report: %v", err)
		}
	}
	if err := file.Close(); err != nil {
		t.Fatalf("close feedback ledger: %v", err)
	}
}

func writeReviewMarkdown(t *testing.T, configDir, contents string) {
	t.Helper()
	path := MarkdownPath(configDir)
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		t.Fatalf("create telemetry directory: %v", err)
	}
	if err := os.WriteFile(path, []byte(contents), 0o600); err != nil {
		t.Fatalf("write feedback markdown: %v", err)
	}
}

func assertReviewCursorMode(t *testing.T, configDir string) {
	t.Helper()
	info, err := os.Lstat(reviewCursorPath(configDir))
	if err != nil {
		t.Errorf("stat review cursor: %v", err)
		return
	}
	if !info.Mode().IsRegular() {
		t.Errorf("review cursor mode = %v, want a regular file", info.Mode())
	}
	if got := info.Mode().Perm(); got != 0o600 {
		t.Errorf("review cursor permissions = %04o, want 0600", got)
	}
}

func assertFileContents(t *testing.T, path string, want []byte) {
	t.Helper()
	got, err := os.ReadFile(path)
	if err != nil {
		t.Errorf("read %s: %v", path, err)
		return
	}
	if !bytes.Equal(got, want) {
		t.Errorf("%s changed; want append-only content %q, got %q", path, want, got)
	}
}

func TestPendingBaselinesExistingRows(t *testing.T) {
	configDir := reviewTestConfigDir(t)
	writeReviewLedger(t, configDir, reviewTestReport("one"), reviewTestReport("two"))
	writeReviewMarkdown(t, configDir, "# existing feedback\n")
	ledgerBefore, err := os.ReadFile(LedgerPath(configDir))
	if err != nil {
		t.Fatal(err)
	}
	markdownBefore, err := os.ReadFile(MarkdownPath(configDir))
	if err != nil {
		t.Fatal(err)
	}

	first, err := Pending(configDir)
	if err != nil {
		t.Fatalf("first Pending: %v", err)
	}
	if !first.Initialized {
		t.Errorf("first Pending Initialized = false, want true")
	}
	if first.BaselineCount != 2 {
		t.Errorf("first Pending BaselineCount = %d, want 2", first.BaselineCount)
	}
	if first.SnapshotCount != 2 {
		t.Errorf("first Pending SnapshotCount = %d, want 2", first.SnapshotCount)
	}
	if len(first.Reports) != 0 {
		t.Errorf("first Pending returned %d reports, want none while baselining", len(first.Reports))
	}
	assertReviewCursorMode(t, configDir)

	second, err := Pending(configDir)
	if err != nil {
		t.Fatalf("subsequent Pending: %v", err)
	}
	if second.Initialized {
		t.Errorf("subsequent Pending Initialized = true, want false")
	}
	if len(second.Reports) != 0 {
		t.Errorf("subsequent Pending returned %d reports, want none", len(second.Reports))
	}
	if second.SnapshotCount != 2 {
		t.Errorf("subsequent Pending SnapshotCount = %d, want 2", second.SnapshotCount)
	}
	assertFileContents(t, LedgerPath(configDir), ledgerBefore)
	assertFileContents(t, MarkdownPath(configDir), markdownBefore)
}

func TestPendingBaselinesEmptyLedgerAndMarksOnlyExactPrefix(t *testing.T) {
	configDir := reviewTestConfigDir(t)
	writeReviewLedger(t, configDir)
	writeReviewMarkdown(t, configDir, "# append-only feedback\n")

	baseline, err := Pending(configDir)
	if err != nil {
		t.Fatalf("baseline Pending: %v", err)
	}
	if !baseline.Initialized || baseline.BaselineCount != 0 || baseline.SnapshotCount != 0 || len(baseline.Reports) != 0 {
		t.Errorf("empty-ledger baseline = %+v, want initialized with zero rows and no reports", baseline)
	}

	firstReports := []Report{reviewTestReport("one"), reviewTestReport("two")}
	appendReviewLedger(t, configDir, firstReports...)
	first, err := Pending(configDir)
	if err != nil {
		t.Fatalf("Pending after append: %v", err)
	}
	repeated, err := Pending(configDir)
	if err != nil {
		t.Fatalf("repeated Pending: %v", err)
	}
	if first.Initialized {
		t.Errorf("Pending after baseline Initialized = true, want false")
	}
	if first.SnapshotCount != len(firstReports) {
		t.Errorf("Pending after baseline SnapshotCount = %d, want %d", first.SnapshotCount, len(firstReports))
	}
	if !reflect.DeepEqual(first.Reports, firstReports) {
		t.Errorf("pending reports = %+v, want %+v", first.Reports, firstReports)
	}
	if first.Token == "" {
		t.Errorf("Pending returned an empty token for a non-empty prefix")
	}
	if repeated.Token != first.Token {
		t.Errorf("token changed for an unchanged ledger prefix: %q then %q", first.Token, repeated.Token)
	}

	lateReport := reviewTestReport("three")
	appendReviewLedger(t, configDir, lateReport)
	ledgerAfterAppend, err := os.ReadFile(LedgerPath(configDir))
	if err != nil {
		t.Fatal(err)
	}
	markdownBefore, err := os.ReadFile(MarkdownPath(configDir))
	if err != nil {
		t.Fatal(err)
	}
	marked, err := MarkReviewed(configDir, first.Token)
	if err != nil {
		t.Fatalf("MarkReviewed: %v", err)
	}
	if marked.Through != 2 || marked.NewlyReviewed != 2 {
		t.Errorf("MarkReviewed = %+v, want Through=2 NewlyReviewed=2", marked)
	}
	replay, err := MarkReviewed(configDir, first.Token)
	if err != nil {
		t.Fatalf("idempotent MarkReviewed replay: %v", err)
	}
	if replay.Through != 2 || replay.NewlyReviewed != 0 {
		t.Errorf("replayed MarkReviewed = %+v, want Through=2 NewlyReviewed=0", replay)
	}

	late, err := Pending(configDir)
	if err != nil {
		t.Fatalf("Pending after later append: %v", err)
	}
	if !reflect.DeepEqual(late.Reports, []Report{lateReport}) {
		t.Errorf("pending after acknowledging prefix = %+v, want only %+v", late.Reports, lateReport)
	}
	if late.Token == "" || late.Token == first.Token {
		t.Errorf("token for the longer ledger prefix = %q, want a distinct non-empty token", late.Token)
	}
	assertReviewCursorMode(t, configDir)
	assertFileContents(t, LedgerPath(configDir), ledgerAfterAppend)
	assertFileContents(t, MarkdownPath(configDir), markdownBefore)
}

func TestMarkReviewedRejectsMalformedForgedAndBackwardTokens(t *testing.T) {
	configDir := reviewTestConfigDir(t)
	writeReviewLedger(t, configDir)
	if _, err := Pending(configDir); err != nil {
		t.Fatalf("baseline Pending: %v", err)
	}

	firstReport := reviewTestReport("one")
	appendReviewLedger(t, configDir, firstReport)
	first, err := Pending(configDir)
	if err != nil {
		t.Fatalf("first pending read: %v", err)
	}
	if first.Token == "" {
		t.Errorf("first pending read returned an empty token")
	}
	mark, err := MarkReviewed(configDir, first.Token)
	if err != nil {
		t.Fatalf("MarkReviewed first prefix: %v", err)
	}
	if mark.Through != 1 || mark.NewlyReviewed != 1 {
		t.Errorf("first MarkReviewed = %+v, want Through=1 NewlyReviewed=1", mark)
	}
	replay, err := MarkReviewed(configDir, first.Token)
	if err != nil {
		t.Fatalf("replay latest token: %v", err)
	}
	if replay.Through != 1 || replay.NewlyReviewed != 0 {
		t.Errorf("replayed mark = %+v, want Through=1 NewlyReviewed=0", replay)
	}

	secondReport := reviewTestReport("two")
	appendReviewLedger(t, configDir, secondReport)
	second, err := Pending(configDir)
	if err != nil {
		t.Fatalf("second pending read: %v", err)
	}
	if second.Token == "" || second.Token == first.Token {
		t.Errorf("second prefix token = %q, want a distinct non-empty token", second.Token)
	}
	secondMark, err := MarkReviewed(configDir, second.Token)
	if err != nil {
		t.Fatalf("MarkReviewed second prefix: %v", err)
	}
	if secondMark.Through != 2 || secondMark.NewlyReviewed != 1 {
		t.Errorf("second MarkReviewed = %+v, want Through=2 NewlyReviewed=1", secondMark)
	}

	for name, token := range map[string]string{
		"malformed": "not-a-review-token",
		"forged":    forgedReviewToken(second.Token),
		"backward":  first.Token,
	} {
		t.Run(name, func(t *testing.T) {
			if _, err := MarkReviewed(configDir, token); err == nil {
				t.Errorf("MarkReviewed accepted %s token %q", name, token)
			}
		})
	}

	lateReport := reviewTestReport("three")
	appendReviewLedger(t, configDir, lateReport)
	pending, err := Pending(configDir)
	if err != nil {
		t.Fatalf("Pending after rejected tokens: %v", err)
	}
	if !reflect.DeepEqual(pending.Reports, []Report{lateReport}) {
		t.Errorf("reports after rejected tokens = %+v, want only %+v", pending.Reports, lateReport)
	}
}

func TestPendingAndMarkReviewedFailClosedOnCorruptCursor(t *testing.T) {
	configDir := reviewTestConfigDir(t)
	writeReviewLedger(t, configDir, reviewTestReport("one"))
	if _, err := Pending(configDir); err != nil {
		t.Fatalf("baseline Pending: %v", err)
	}
	corrupt := []byte("{not-json\n")
	if err := os.WriteFile(reviewCursorPath(configDir), corrupt, 0o600); err != nil {
		t.Fatalf("corrupt review cursor fixture: %v", err)
	}

	if _, err := Pending(configDir); err == nil {
		t.Errorf("Pending accepted a corrupted review cursor")
	}
	if _, err := MarkReviewed(configDir, "anything"); err == nil {
		t.Errorf("MarkReviewed accepted a corrupted review cursor")
	}
	assertFileContents(t, reviewCursorPath(configDir), corrupt)
}

func TestPendingAndMarkReviewedFailClosedOnReviewedPrefixDrift(t *testing.T) {
	configDir := reviewTestConfigDir(t)
	original := reviewTestReport("one")
	writeReviewLedger(t, configDir, original)
	baseline, err := Pending(configDir)
	if err != nil {
		t.Fatalf("baseline Pending: %v", err)
	}
	if !baseline.Initialized || baseline.BaselineCount != 1 {
		t.Errorf("initial baseline = %+v, want one baselined row", baseline)
	}

	appended := reviewTestReport("two")
	appendReviewLedger(t, configDir, appended)
	pending, err := Pending(configDir)
	if err != nil {
		t.Fatalf("Pending after append: %v", err)
	}
	if pending.Token == "" {
		t.Errorf("Pending returned an empty token for the appended row")
	}

	drifted := reviewTestReport("one")
	drifted.Repo = "changed-repo"
	writeReviewLedger(t, configDir, drifted, appended)
	if _, err := MarkReviewed(configDir, pending.Token); err == nil {
		t.Errorf("MarkReviewed accepted a token after the ledger prefix drifted")
	}
	if _, err := Pending(configDir); err == nil {
		t.Errorf("Pending accepted a drifted reviewed prefix")
	}
}

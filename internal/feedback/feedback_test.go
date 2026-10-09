package feedback

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/alesierraalta/tsp/internal/feature"
	"github.com/alesierraalta/tsp/internal/sanitize"
)

// validText is the shape --template prints, filled in: one key per line, the run's identity in
// place, and the operator's own words.
func validText() string {
	return "# a comment line is ignored\n" +
		"\n" +
		"ts: 2026-09-10T12:00:00Z\n" +
		"repo: /repo\n" +
		"plan: docs/testing/test-plan.md\n" +
		"skill: tsp 0.3.9\n" +
		"build: 0.3.6 (abc1234)\n" +
		"paid: the plan made me write the row first\n" +
		"cost: two hours\n" +
		"reason: it earned its keep\n" +
		"verdict: partly\n" +
		"guess: I cannot tell a probe from a pin\n" +
		"freeform: ship it\n"
}

func TestParseReadsTheTemplateShape(t *testing.T) {
	r, err := Parse(validText())
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	if r.TS != "2026-09-10T12:00:00Z" || r.Repo != "/repo" || r.Plan != "docs/testing/test-plan.md" {
		t.Fatalf("identity mangled: %+v", r)
	}
	if r.Skill != "tsp 0.3.9" || r.Build != "0.3.6 (abc1234)" {
		t.Fatalf("build identity mangled: %+v", r)
	}
	if r.Paid != "the plan made me write the row first" || r.Cost != "two hours" {
		t.Fatalf("fields mangled: %+v", r)
	}
	if r.Reason != "it earned its keep" || r.Verdict != VerdictPartly {
		t.Fatalf("verdict mangled: %+v", r)
	}
	if r.Guess != "I cannot tell a probe from a pin" || r.Freeform != "ship it" {
		t.Fatalf("optional fields mangled: %+v", r)
	}
}

func TestParseValidatesSkillIdentityShape(t *testing.T) {
	tests := []struct {
		name    string
		skill   string
		wantErr bool
	}{
		{name: "breakcheck version", skill: "breakcheck 0.1.0"},
		{name: "canonical TSP version", skill: "tsp 0.3.9"},
		{name: "legacy skill identity remains valid", skill: "test-strategy 0.3.9"},
		{name: "bare name", skill: "breakcheck"},
		{name: "bare version", skill: "0.3.9", wantErr: true},
		{name: "free prose", skill: "bounded campaign, five probes", wantErr: true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			text := strings.Replace(validText(), "skill: tsp 0.3.9", "skill: "+tt.skill, 1)
			_, err := Parse(text)
			if tt.wantErr {
				if err == nil {
					t.Fatal("Parse accepted an invalid skill identity")
				}
				for _, want := range []string{"<name>", "<version>"} {
					if !strings.Contains(err.Error(), want) {
						t.Fatalf("refusal missing %q: %v", want, err)
					}
				}
				return
			}
			if err != nil {
				t.Fatalf("Parse: %v", err)
			}
		})
	}
}

// A value runs to the next key: the operator writes prose, not a single line.
func TestParseContinuesAValueOnTheFollowingLines(t *testing.T) {
	text := strings.Replace(validText(), "paid: the plan made me write the row first\n",
		"paid: the plan made me write the row first\nand then the test, twice\n", 1)
	r, err := Parse(text)
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	if r.Paid != "the plan made me write the row first\nand then the test, twice" {
		t.Fatalf("continuation mangled: %q", r.Paid)
	}
}

func TestParseRefusesUnknownKeysNamingThem(t *testing.T) {
	text := validText() + "surprise: something\n"
	_, err := Parse(text)
	if err == nil || !strings.Contains(err.Error(), "surprise") {
		t.Fatalf("want a refusal naming the unknown key, got %v", err)
	}
}

func TestParseRefusesMissingRequiredFieldsNamingThem(t *testing.T) {
	for _, field := range []string{"ts", "repo", "plan", "skill", "build", "paid", "cost", "reason", "verdict"} {
		t.Run(field, func(t *testing.T) {
			var kept []string
			for _, line := range strings.Split(validText(), "\n") {
				if strings.HasPrefix(line, field+":") {
					continue
				}
				kept = append(kept, line)
			}
			_, err := Parse(strings.Join(kept, "\n"))
			if err == nil || !strings.Contains(err.Error(), field) {
				t.Fatalf("want a refusal naming %q, got %v", field, err)
			}
		})
	}
}

func TestParseRefusesAVerdictOutsideTheThree(t *testing.T) {
	text := strings.Replace(validText(), "verdict: partly", "verdict: maybe", 1)
	_, err := Parse(text)
	if err == nil {
		t.Fatal("want a refusal for an unknown verdict")
	}
	for _, want := range []string{VerdictPaid, VerdictPartly, VerdictCeremony} {
		if !strings.Contains(err.Error(), want) {
			t.Fatalf("the refusal must name all three verdicts; missing %q in %v", want, err)
		}
	}
}

func TestTemplateFillsTheIdentityAndNamesTheSubmitCommand(t *testing.T) {
	got := Template(Report{TS: "t", Repo: "/repo", Plan: "/plan", Skill: "tsp 0.3.9", Build: "0.3.6 (abc)"})
	for _, want := range []string{"ts: t", "repo: /repo", "plan: /plan", "skill: tsp 0.3.9", "build: 0.3.6 (abc)"} {
		if !strings.Contains(got, want) {
			t.Fatalf("template missing %q:\n%s", want, got)
		}
	}
	for _, want := range []string{"paid", "cost", "reason", "verdict", "guess", "freeform"} {
		if !strings.Contains(got, want+":") {
			t.Fatalf("template missing the %q field:\n%s", want, got)
		}
	}
	if !strings.Contains(got, "tsp feedback --file") || strings.Contains(got, "tpp") {
		t.Fatalf("the template must name the tsp command that submits it:\n%s", got)
	}
}

// Record is append-only: the header is written once and a second report never rewrites the first.
func enableFeedbackForTest(t *testing.T) {
	t.Helper()
	t.Setenv("TPP_HOME", t.TempDir())
	if _, err := feature.Set("feedback", true); err != nil {
		t.Fatalf("enable feedback: %v", err)
	}
}

func TestRecordRefusesWhileFeedbackIsDisabled(t *testing.T) {
	t.Setenv("TPP_HOME", t.TempDir())
	dir := t.TempDir()
	r := Report{TS: "t", Repo: "/repo", Plan: NotGiven, Skill: "skill", Build: "build",
		Paid: "paid", Cost: "one hour", Reason: "reason", Verdict: VerdictPaid}

	err := Record(dir, r)
	const want = "feedback is disabled; enable it with: tsp feature enable feedback"
	if err == nil || err.Error() != want {
		t.Fatalf("Record error = %v, want %q", err, want)
	}
	for _, path := range []string{LedgerPath(dir), MarkdownPath(dir)} {
		if _, statErr := os.Stat(path); !os.IsNotExist(statErr) {
			t.Fatalf("refused report left %s behind: %v", path, statErr)
		}
	}
}

func TestRecordProceedsOnceFeedbackIsEnabled(t *testing.T) {
	enableFeedbackForTest(t)
	dir := t.TempDir()
	r := Report{TS: "t", Repo: "/repo", Plan: NotGiven, Skill: "skill", Build: "build",
		Paid: "paid", Cost: "one hour", Reason: "reason", Verdict: VerdictPaid}

	if err := Record(dir, r); err != nil {
		t.Fatalf("Record: %v", err)
	}
	raw, err := os.ReadFile(LedgerPath(dir))
	if err != nil {
		t.Fatalf("ledger: %v", err)
	}
	if rows := strings.Count(strings.TrimRight(string(raw), "\n"), "\n") + 1; rows != 1 {
		t.Fatalf("ledger rows = %d, want 1", rows)
	}
}

func TestRecordAppendsJSONAndMarkdownWithoutRewriting(t *testing.T) {
	enableFeedbackForTest(t)
	dir := t.TempDir()
	first := Report{TS: "2026-09-10T12:00:00Z", Repo: "/repo", Plan: "p", Skill: "0.3.6",
		Build: "b", Paid: "paid words", Cost: "one hour", Reason: "reason one",
		Verdict: VerdictPaid, Guess: "first guess"}
	second := Report{TS: "2026-09-11T12:00:00Z", Repo: "/repo", Plan: NotGiven, Skill: "0.3.6",
		Build: "b", Paid: "ceremony words", Cost: "ten minutes", Reason: "reason two",
		Verdict: VerdictCeremony}
	if err := Record(dir, first); err != nil {
		t.Fatalf("Record first: %v", err)
	}
	if err := Record(dir, second); err != nil {
		t.Fatalf("Record second: %v", err)
	}

	raw, err := os.ReadFile(LedgerPath(dir))
	if err != nil {
		t.Fatalf("ledger: %v", err)
	}
	lines := strings.Split(strings.TrimRight(string(raw), "\n"), "\n")
	if len(lines) != 2 {
		t.Fatalf("ledger has %d lines, want 2:\n%s", len(lines), raw)
	}
	key, err := sanitize.LoadKey(dir)
	if err != nil {
		t.Fatalf("LoadKey: %v", err)
	}
	wantReports := []Report{first, second}
	for i := range wantReports {
		wantReports[i].Repo = key.ID("repo", wantReports[i].Repo)
		if wantReports[i].Plan != "" && wantReports[i].Plan != NotGiven {
			wantReports[i].Plan = key.ID("plan", wantReports[i].Plan)
		}
		wantReports[i].Sanitized = true
	}
	for i, want := range wantReports {
		var got Report
		if err := json.Unmarshal([]byte(lines[i]), &got); err != nil {
			t.Fatalf("line %d not JSON: %v", i, err)
		}
		if got != want {
			t.Fatalf("line %d = %+v, want %+v", i, got, want)
		}
	}

	md, err := os.ReadFile(MarkdownPath(dir))
	if err != nil {
		t.Fatalf("markdown: %v", err)
	}
	if strings.Count(string(md), "# Run feedback") != 1 {
		t.Fatalf("the header must be written once:\n%s", md)
	}
	if n := strings.Count(string(md), "\n## "); n != 2 {
		t.Fatalf("want one section per report, got %d:\n%s", n, md)
	}
	if !strings.Contains(string(md), "ceremony words") || !strings.Contains(string(md), "paid words") {
		t.Fatalf("the rendering lost the operator's words:\n%s", md)
	}
}

func TestRecordRefusesASecretRatherThanPersistingIt(t *testing.T) {
	enableFeedbackForTest(t)
	dir := t.TempDir()
	r := Report{TS: "t", Repo: "/repo", Plan: NotGiven, Skill: "skill", Build: "build",
		Paid: "paid", Cost: "one hour", Reason: "contains ghp_1234567890abcdef1234", Verdict: VerdictPaid}
	recordErr := Record(dir, r)
	if recordErr == nil || !strings.Contains(recordErr.Error(), "reason: detected github_token") {
		t.Fatalf("Record error = %v, want a reason field github_token refusal", recordErr)
	}
	for _, path := range []string{LedgerPath(dir), MarkdownPath(dir)} {
		if _, err := os.Stat(path); !os.IsNotExist(err) {
			t.Fatalf("refused report left %s behind: %v", path, err)
		}
	}
	t.Logf("observed error %q; ledger absent; markdown absent", recordErr)
}

func TestRecordAnonymisesTheRepositoryAndThePlan(t *testing.T) {
	enableFeedbackForTest(t)
	dir := t.TempDir()
	r := Report{TS: "t", Repo: "/home/someone/acme-billing", Plan: "internal/secret-plans/plan.md",
		Skill: "skill", Build: "build", Paid: "paid", Cost: "one hour", Reason: "reason", Verdict: VerdictPaid}
	if err := Record(dir, r); err != nil {
		t.Fatalf("Record: %v", err)
	}
	raw, err := os.ReadFile(LedgerPath(dir))
	if err != nil {
		t.Fatalf("ledger: %v", err)
	}
	line := string(raw)
	for _, unwanted := range []string{"acme-billing", "secret-plans", "/home/someone"} {
		if strings.Contains(line, unwanted) {
			t.Fatalf("ledger contains raw identity %q: %s", unwanted, line)
		}
	}
	if !strings.Contains(line, `"repo-`) || !strings.Contains(line, `"sanitized":true`) {
		t.Fatalf("ledger lacks the sanitized repository marker: %s", line)
	}
}

func TestRecordRedactsASecretInAnOptionalField(t *testing.T) {
	enableFeedbackForTest(t)
	dir := t.TempDir()
	token := "ghp_1234567890abcdef1234"
	r := Report{TS: "t", Repo: "/repo", Plan: NotGiven, Skill: "skill", Build: "build",
		Paid: "paid", Cost: "one hour", Reason: "reason", Freeform: "notes: " + token, Verdict: VerdictPaid}
	if err := Record(dir, r); err != nil {
		t.Fatalf("Record: %v", err)
	}
	ledger, err := os.ReadFile(LedgerPath(dir))
	if err != nil {
		t.Fatalf("ledger: %v", err)
	}
	markdown, err := os.ReadFile(MarkdownPath(dir))
	if err != nil {
		t.Fatalf("markdown: %v", err)
	}
	for _, artifact := range []string{string(ledger), string(markdown)} {
		if strings.Contains(artifact, token) || !strings.Contains(artifact, sanitize.Redacted) {
			t.Fatalf("optional secret handling = %q, want redaction without the token", artifact)
		}
	}
}

func TestRecordWritesTheLocalResolutionMap(t *testing.T) {
	enableFeedbackForTest(t)
	dir := t.TempDir()
	r := Report{TS: "t", Repo: "/home/someone/acme-billing", Plan: NotGiven, Skill: "skill", Build: "build",
		Paid: "paid", Cost: "one hour", Reason: "reason", Verdict: VerdictPaid}
	if err := Record(dir, r); err != nil {
		t.Fatalf("Record: %v", err)
	}
	telemetryInfo, err := os.Stat(sanitize.TelemetryDir(dir))
	if err != nil {
		t.Fatalf("telemetry directory: %v", err)
	}
	if got := telemetryInfo.Mode().Perm(); got != 0o700 {
		t.Fatalf("telemetry mode = %o, want 700", got)
	}
	mapPath := filepath.Join(sanitize.TelemetryDir(dir), ".pseudonyms.jsonl")
	mapInfo, err := os.Stat(mapPath)
	if err != nil {
		t.Fatalf("pseudonym map: %v", err)
	}
	if got := mapInfo.Mode().Perm(); got != 0o600 {
		t.Fatalf("pseudonym map mode = %o, want 600", got)
	}
	line, err := os.ReadFile(LedgerPath(dir))
	if err != nil {
		t.Fatalf("ledger: %v", err)
	}
	var recorded Report
	if err := json.Unmarshal([]byte(strings.TrimSpace(string(line))), &recorded); err != nil {
		t.Fatalf("recorded line: %v", err)
	}
	if got, ok := sanitize.Resolve(sanitize.TelemetryDir(dir), recorded.Repo); !ok || got != r.Repo {
		t.Fatalf("Resolve(%q) = %q, %v; want %q, true", recorded.Repo, got, ok, r.Repo)
	}
}

func TestRecordRefusesToWriteWhenTheLocalMapCannotBeWritten(t *testing.T) {
	enableFeedbackForTest(t)
	dir := t.TempDir()
	telemetryDir := sanitize.TelemetryDir(dir)
	if err := os.MkdirAll(telemetryDir, 0o700); err != nil {
		t.Fatalf("telemetry directory: %v", err)
	}
	if err := os.Mkdir(filepath.Join(telemetryDir, ".pseudonyms.jsonl"), 0o700); err != nil {
		t.Fatalf("map directory: %v", err)
	}
	r := Report{TS: "t", Repo: "/repo", Plan: NotGiven, Skill: "skill", Build: "build",
		Paid: "paid", Cost: "one hour", Reason: "reason", Verdict: VerdictPaid}
	if err := Record(dir, r); err == nil {
		t.Fatal("Record succeeded with an unwritable local map")
	}
	if _, err := os.Stat(LedgerPath(dir)); !os.IsNotExist(err) {
		t.Fatalf("map refusal appended a ledger row: %v", err)
	}
}

func TestRecordRefusesASecretWithoutTouchingTheLocalMap(t *testing.T) {
	enableFeedbackForTest(t)
	dir := t.TempDir()
	accepted := Report{TS: "t", Repo: "/home/someone/acme-billing", Plan: NotGiven, Skill: "skill", Build: "build",
		Paid: "paid", Cost: "one hour", Reason: "reason", Verdict: VerdictPaid}
	if err := Record(dir, accepted); err != nil {
		t.Fatalf("Record accepted report: %v", err)
	}
	mapPath := filepath.Join(sanitize.TelemetryDir(dir), ".pseudonyms.jsonl")
	before, err := os.ReadFile(mapPath)
	if err != nil {
		t.Fatalf("pseudonym map: %v", err)
	}
	refused := accepted
	refused.Reason = "the leaked token was ghp_1234567890abcdef1234"
	if err := Record(dir, refused); err == nil {
		t.Fatal("Record accepted a report whose required field held a secret")
	}
	after, err := os.ReadFile(mapPath)
	if err != nil {
		t.Fatalf("pseudonym map after refusal: %v", err)
	}
	if string(before) != string(after) {
		t.Fatalf("refusal changed the local map:\n before %q\n after  %q", before, after)
	}
	rows, err := os.ReadFile(LedgerPath(dir))
	if err != nil {
		t.Fatalf("ledger: %v", err)
	}
	if got := strings.Count(string(rows), "\n"); got != 1 {
		t.Fatalf("ledger rows = %d, want 1", got)
	}
	empty := t.TempDir()
	if err := Record(empty, refused); err == nil {
		t.Fatal("Record accepted a report whose required field held a secret")
	}
	if _, err := os.Stat(filepath.Join(sanitize.TelemetryDir(empty), ".pseudonyms.jsonl")); !os.IsNotExist(err) {
		t.Fatalf("a refusal created a local map: %v", err)
	}
}

func TestRecordKeepsTheMarkdownInStepWithTheJSONL(t *testing.T) {
	enableFeedbackForTest(t)
	dir := t.TempDir()
	r := Report{TS: "t", Repo: "/home/someone/acme-billing", Plan: NotGiven, Skill: "skill", Build: "build",
		Paid: "paid", Cost: "one hour", Reason: "reason", Verdict: VerdictPaid}
	if err := Record(dir, r); err != nil {
		t.Fatalf("Record: %v", err)
	}
	line, err := os.ReadFile(LedgerPath(dir))
	if err != nil {
		t.Fatalf("ledger: %v", err)
	}
	var recorded Report
	if err := json.Unmarshal([]byte(strings.TrimSpace(string(line))), &recorded); err != nil {
		t.Fatalf("recorded line: %v", err)
	}
	markdown, err := os.ReadFile(MarkdownPath(dir))
	if err != nil {
		t.Fatalf("markdown: %v", err)
	}
	if !strings.Contains(string(markdown), "- repo: "+recorded.Repo) || strings.Contains(string(markdown), r.Repo) {
		t.Fatalf("markdown identity is out of step with JSONL: %s", markdown)
	}
}

func TestSummarySaysHowManyRowsPredateSanitization(t *testing.T) {
	write := func(t *testing.T, dir string, reports ...Report) {
		t.Helper()
		if err := os.MkdirAll(filepath.Dir(LedgerPath(dir)), 0o700); err != nil {
			t.Fatalf("telemetry directory: %v", err)
		}
		var lines []string
		for _, r := range reports {
			line, err := json.Marshal(r)
			if err != nil {
				t.Fatalf("marshal report: %v", err)
			}
			lines = append(lines, string(line))
		}
		if err := os.WriteFile(LedgerPath(dir), []byte(strings.Join(lines, "\n")+"\n"), 0o600); err != nil {
			t.Fatalf("write ledger: %v", err)
		}
	}
	legacy := Report{TS: "t", Repo: "repo-legacy", Plan: NotGiven, Skill: "skill", Build: "build",
		Paid: "paid", Cost: "one hour", Reason: "reason", Verdict: VerdictPaid}
	marked := legacy
	marked.Sanitized = true
	mixedDir := t.TempDir()
	write(t, mixedDir, legacy, marked)
	got, err := Summary(mixedDir)
	if err != nil {
		t.Fatalf("Summary mixed: %v", err)
	}
	if !strings.Contains(got, "1 of 2 report(s) predate sanitization and may carry raw identity") {
		t.Fatalf("summary missing legacy warning:\n%s", got)
	}

	markedDir := t.TempDir()
	write(t, markedDir, marked, marked)
	got, err = Summary(markedDir)
	if err != nil {
		t.Fatalf("Summary marked: %v", err)
	}
	if strings.Contains(got, "predate sanitization") {
		t.Fatalf("summary warns when every row is marked:\n%s", got)
	}
}

func TestParseRefusesTheSanitizedMarkerAsAnInputKey(t *testing.T) {
	_, err := Parse(validText() + "sanitized: true\n")
	if err == nil || !strings.Contains(err.Error(), "sanitized") {
		t.Fatalf("Parse accepted the output-only marker: %v", err)
	}
}

func TestReadLoadsLegacySkillUnchangedAfterRecord(t *testing.T) {
	enableFeedbackForTest(t)
	dir := t.TempDir()
	want := Report{
		TS: "2026-09-10T12:00:00Z", Repo: "/repo", Plan: "p", Skill: "0.3.6", Build: "b",
		Paid: "paid words", Cost: "one hour", Reason: "reason", Verdict: VerdictPaid,
		Sanitized: true, // Record marks what it wrote; Read resolves the pseudonyms back to these values
	}
	if err := Record(dir, want); err != nil {
		t.Fatalf("Record: %v", err)
	}
	got, err := Read(dir)
	if err != nil {
		t.Fatalf("Read: %v", err)
	}
	if len(got) != 1 || got[0] != want {
		t.Fatalf("legacy report changed: got %#v, want %#v", got, []Report{want})
	}
}

func TestReadMissingLedgerIsEmptyNotAnError(t *testing.T) {
	got, err := Read(t.TempDir())
	if err != nil {
		t.Fatalf("a missing ledger is not an error: %v", err)
	}
	if len(got) != 0 {
		t.Fatalf("want no reports, got %d", len(got))
	}
}

func TestSummaryCountsVerdictsAndSkillVersions(t *testing.T) {
	enableFeedbackForTest(t)
	dir := t.TempDir()
	records := []Report{
		{TS: "2026-09-08T00:00:00Z", Repo: "/r", Plan: "p", Skill: "0.3.5", Build: "b",
			Paid: "a", Cost: "b", Reason: "oldest", Verdict: VerdictCeremony, Guess: "oldest guess"},
		{TS: "2026-09-09T00:00:00Z", Repo: "/r", Plan: "p", Skill: "0.3.6", Build: "b",
			Paid: "a", Cost: "b", Reason: "middle", Verdict: VerdictPaid, Guess: "middle guess"},
		{TS: "2026-09-10T00:00:00Z", Repo: "/r", Plan: "p", Skill: "0.3.6", Build: "b",
			Paid: "a", Cost: "b", Reason: "newest", Verdict: VerdictPartly, Guess: "newest guess"},
	}
	for _, r := range records {
		if err := Record(dir, r); err != nil {
			t.Fatalf("Record: %v", err)
		}
	}
	got, err := Summary(dir)
	if err != nil {
		t.Fatalf("Summary: %v", err)
	}
	for _, want := range []string{
		"3 report",
		"paid: 1", "partly: 1", "ceremony: 1",
		"0.3.6: 2 (paid 1, partly 1, ceremony 0)",
		"0.3.5: 1 (paid 0, partly 0, ceremony 1)",
	} {
		if !strings.Contains(got, want) {
			t.Fatalf("summary missing %q:\n%s", want, got)
		}
	}
}

func TestSummaryClassifiesUnknownVerdicts(t *testing.T) {
	enableFeedbackForTest(t)
	dir := t.TempDir()
	if err := Record(dir, Report{TS: "2026-09-11T00:00:00Z", Repo: "/r", Plan: "p", Skill: "0.3.6", Build: "b",
		Paid: "a", Cost: "b", Reason: "unknown", Verdict: "mystery"}); err != nil {
		t.Fatalf("Record: %v", err)
	}
	got, err := Summary(dir)
	if err != nil {
		t.Fatalf("Summary: %v", err)
	}
	for _, want := range []string{
		"unknown: 1",
		"0.3.6: 1 (paid 0, partly 0, ceremony 0, unknown 1)",
	} {
		if !strings.Contains(got, want) {
			t.Fatalf("summary missing %q:\n%s", want, got)
		}
	}
}

func TestSummaryGroupsReportsByProject(t *testing.T) {
	enableFeedbackForTest(t)
	dir := t.TempDir()
	records := []Report{
		{TS: "2026-09-12T00:00:00Z", Repo: "/repo-a", Plan: "p", Skill: "0.3.6", Build: "b",
			Paid: "a", Cost: "b", Reason: "partly", Verdict: VerdictPartly},
		{TS: "2026-09-13T00:00:00Z", Repo: "/repo-z", Plan: "p", Skill: "0.3.6", Build: "b",
			Paid: "a", Cost: "b", Reason: "paid", Verdict: VerdictPaid},
	}
	for _, r := range records {
		if err := Record(dir, r); err != nil {
			t.Fatalf("Record: %v", err)
		}
	}
	got, err := Summary(dir)
	if err != nil {
		t.Fatalf("Summary: %v", err)
	}
	for _, want := range []string{
		"by project:",
		"/repo-z: 1 (paid 1, partly 0, ceremony 0)",
		"/repo-a: 1 (paid 0, partly 1, ceremony 0)",
	} {
		if !strings.Contains(got, want) {
			t.Fatalf("summary missing %q:\n%s", want, got)
		}
	}
	if strings.Index(got, "/repo-z:") > strings.Index(got, "/repo-a:") {
		t.Fatalf("projects must be sorted by name:\n%s", got)
	}
}

func TestSummaryListsRecentGuessesNewestFirst(t *testing.T) {
	enableFeedbackForTest(t)
	dir := t.TempDir()
	for i, guess := range []string{"g1", "g2", "g3", "g4", "g5", "g6"} {
		r := Report{TS: "t", Repo: "/r", Plan: "p", Skill: "0.3.6", Build: "b",
			Paid: "a", Cost: "b", Reason: "x", Verdict: VerdictPaid, Guess: guess}
		if err := Record(dir, r); err != nil {
			t.Fatalf("Record %d: %v", i, err)
		}
	}
	got, err := Summary(dir)
	if err != nil {
		t.Fatalf("Summary: %v", err)
	}
	if strings.Contains(got, "g1") {
		t.Fatalf("only the five most recent guesses belong in the summary:\n%s", got)
	}
	newest := strings.Index(got, "g6")
	older := strings.Index(got, "g2")
	if newest == -1 || older == -1 || newest > older {
		t.Fatalf("guesses must be newest first:\n%s", got)
	}
}

func TestSummaryOnAMissingLedgerSaysSo(t *testing.T) {
	got, err := Summary(t.TempDir())
	if err != nil {
		t.Fatalf("a missing ledger is not an error: %v", err)
	}
	if !strings.Contains(strings.ToLower(got), "no reports") {
		t.Fatalf("want a plain no-reports line, got:\n%s", got)
	}
}

func TestEmbeddedSkillIdentityNamesTheEmbeddedSkill(t *testing.T) {
	if got := EmbeddedSkillIdentity(); got != "test-strategy 0.5.5" {
		t.Fatalf("embedded skill identity = %q, want %q", got, "test-strategy 0.5.5")
	}
}

// The ledger and the rendering live under the same telemetry directory as the gate's own log.
func TestPathsFollowTheGatePrecedent(t *testing.T) {
	if filepath.ToSlash(LedgerPath("/cfg")) != "/cfg/telemetry/run-feedback.jsonl" {
		t.Fatalf("ledger path = %s", LedgerPath("/cfg"))
	}
	if filepath.ToSlash(MarkdownPath("/cfg")) != "/cfg/telemetry/run-feedback.md" {
		t.Fatalf("markdown path = %s", MarkdownPath("/cfg"))
	}
}

// A report with no plan named says so; it never guesses a path.
func TestPlanMissingSaysSo(t *testing.T) {
	text := strings.Replace(validText(), "plan: docs/testing/test-plan.md", "plan: ", 1)
	_, err := Parse(text)
	if err == nil || !strings.Contains(err.Error(), "plan") {
		t.Fatalf("an empty plan is a missing required field naming plan, got %v", err)
	}
}

// An empty ledger points at the command that starts it, under the binary's current name.
func TestSummaryOfAnEmptyLedgerNamesTheTspCommand(t *testing.T) {
	got, err := Summary(t.TempDir())
	if err != nil {
		t.Fatalf("Summary: %v", err)
	}
	if !strings.Contains(got, "`tsp feedback --template`") {
		t.Fatalf("empty summary must name the tsp command:\n%s", got)
	}
}

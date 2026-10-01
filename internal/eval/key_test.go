package eval

import (
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/alesierraalta/tpp/internal/bench"
)

const validV2Key = `{"schema":2,"id":"case-1","language":"node","suite":"node --test","surface":"library","defects":[{"id":"D1","file":"src/a.js","line":3,"class":"boundary","keywords":["limit"],"description":"a defect","trigger":{"input":"run()","expected":"ok","actual":"bad"},"why_missed":"not tested","issue_type":"boundary","domain":"data","severity":"high","severity_rationale":"A record is lost under a specific failure.","expected_behavior":"ok","failure_condition":"run() -> bad","detection_criteria":{"mechanism":"inspect the written record","equivalents":[],"proof":"Show the lost record and operation sequence."},"reproduction":{"applies":true,"oracle":"catch","nondeterministic":false,"attempts":1}}]}`

func loadKeyJSON(t *testing.T, body string) bench.Key {
	t.Helper()
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, bench.KeyFile), []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
	key, err := bench.LoadKey(dir)
	if err != nil {
		t.Fatalf("LoadKey: %v", err)
	}
	return key
}

func TestIssuesFromKey(t *testing.T) {
	got, err := IssuesFromKey(loadKeyJSON(t, validV2Key))
	if err != nil {
		t.Fatalf("IssuesFromKey: %v", err)
	}
	want := []Issue{{
		Case: "case-1", ID: "D1", Domain: Data, Severity: High, IssueType: "boundary",
		Reproduction: Reproduction{Applies: true, Oracle: "catch", Attempts: 1},
	}}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("IssuesFromKey = %+v, want %+v", got, want)
	}
}

func TestIssuesFromKeyRequiresSchemaV2AndReturnsCleanControl(t *testing.T) {
	legacy := strings.Replace(validV2Key, `"schema":2`, `"schema":1`, 1)
	if _, err := IssuesFromKey(loadKeyJSON(t, legacy)); err == nil || !strings.Contains(err.Error(), "schema 2") {
		t.Fatalf("legacy key error = %v, want schema 2 requirement", err)
	}

	clean := `{"schema":2,"id":"clean-1","language":"node","suite":"node --test","surface":"library","control":"clean","defects":[]}`
	got, err := IssuesFromKey(loadKeyJSON(t, clean))
	if err != nil || len(got) != 0 {
		t.Fatalf("clean control issues = %+v, error = %v; want empty issues", got, err)
	}
}

func TestIssuesFromKeyRejectsUnknownEnumsWithDefectContext(t *testing.T) {
	for _, tc := range []struct {
		name, old, replacement, want string
	}{
		{"domain", `"domain":"data"`, `"domain":"mystery"`, "case-1/D1"},
		{"severity", `"severity":"high"`, `"severity":"mystery"`, "case-1/D1"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			body := strings.Replace(validV2Key, tc.old, tc.replacement, 1)
			if _, err := IssuesFromKey(loadKeyJSON(t, body)); err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("error = %v, want context %q", err, tc.want)
			}
		})
	}
}

func TestIssuesFromEveryCorpusKey(t *testing.T) {
	paths, err := filepath.Glob("../../bench/cases/*/KEY.json")
	if err != nil {
		t.Fatal(err)
	}
	if len(paths) != 24 {
		t.Fatalf("found %d keys, want 24", len(paths))
	}
	counts := map[Domain]int{}
	total := 0
	critical := 0
	for _, keyPath := range paths {
		key, err := bench.LoadKey(filepath.Dir(keyPath))
		if err != nil {
			t.Fatalf("LoadKey(%s): %v", keyPath, err)
		}
		issues, err := IssuesFromKey(key)
		if err != nil {
			t.Fatalf("IssuesFromKey(%s): %v", keyPath, err)
		}
		for _, issue := range issues {
			total++
			counts[issue.Domain]++
			if issue.Severity == Critical {
				critical++
			}
		}
	}
	if total != 39 || critical != 5 {
		t.Fatalf("corpus issues = %d, critical = %d; want 39 and 5", total, critical)
	}
	for domain, want := range map[Domain]int{
		Security: 5, Data: 6, Concurrency: 1, Resilience: 5, APICompat: 1, Correctness: 21,
	} {
		if counts[domain] != want {
			t.Errorf("%s issues = %d, want %d", domain, counts[domain], want)
		}
	}
}

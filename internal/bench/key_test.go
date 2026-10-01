package bench

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func writeKey(t *testing.T, body string) string {
	t.Helper()
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, KeyFile), []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	return dir
}

func TestLoadKey(t *testing.T) {
	valid := `{"id":"c1","language":"node","suite":"node --test","surface":"lib","defects":[{"id":"d1","file":"src/a.js","line":3,"class":"boundary","keywords":["limit"],"description":"x","trigger":{"input":"i","expected":"e","actual":"a"},"why_missed":"w"}]}`
	cases := []struct {
		name    string
		body    string
		wantErr string
	}{
		{"valid key parses", valid, ""},
		{"malformed json", "{not json", "parse key"},
		{"language outside node/go", strings.Replace(valid, `"node"`, `"rust"`, 1), "not node or go"},
		{"defect without keywords", strings.Replace(valid, `["limit"]`, `[]`, 1), "no keywords"},
		{"defect without a positive line", strings.Replace(valid, `"line":3`, `"line":0`, 1), "positive line"},
		{"no defects", `{"id":"c1","language":"go","suite":"go test ./...","defects":[]}`, "no defects"},
		{"repeated defect id", strings.Replace(valid, `"defects":[`, `"defects":[{"id":"d1","file":"f.go","line":1,"keywords":["k"]},`, 1), "repeated"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			k, err := LoadKey(writeKey(t, tc.body))
			if tc.wantErr == "" {
				if err != nil {
					t.Fatalf("unexpected error: %v", err)
				}
				if k.ID != "c1" || len(k.Defects) != 1 || k.Defects[0].Trigger.Expected != "e" {
					t.Fatalf("parsed key wrong: %+v", k)
				}
				return
			}
			if err == nil || !strings.Contains(err.Error(), tc.wantErr) {
				t.Fatalf("error = %v, want containing %q", err, tc.wantErr)
			}
		})
	}
}

func TestLoadKeyMissingFile(t *testing.T) {
	if _, err := LoadKey(t.TempDir()); err == nil || !strings.Contains(err.Error(), "read key") {
		t.Fatalf("error = %v", err)
	}
}

func TestLoadKeySchemaV2Validation(t *testing.T) {
	base := `{"id":"c1","language":"node","suite":"node --test","surface":"lib","defects":[{"id":"d1","file":"src/a.js","line":3,"class":"boundary","keywords":["limit"],"description":"x","trigger":{"input":"i","expected":"e","actual":"a"},"why_missed":"w"}]}`
	validV2 := `{"schema":2,"id":"c1","language":"node","suite":"node --test","surface":"lib","defects":[{"id":"d1","file":"src/a.js","line":3,"class":"boundary","keywords":["limit"],"description":"x","trigger":{"input":"i","expected":"e","actual":"a"},"why_missed":"w","issue_type":"boundary","domain":"unchecked-here","severity":"unchecked-here","severity_rationale":"rationale","expected_behavior":"expected","failure_condition":"input -> actual","detection_criteria":{"mechanism":"mechanism","equivalents":[],"proof":"proof"},"reproduction":{"applies":true,"oracle":"catch","nondeterministic":false,"attempts":1}}]}`
	cases := []struct {
		name    string
		body    string
		wantErr string
	}{
		{"schema 2 accepts empty equivalents and leaves enum checks to eval", validV2, ""},
		{"schema 2 accepts command oracle", strings.Replace(validV2, `"oracle":"catch"`, `"oracle":"command"`, 1), ""},
		{"schema 2 accepts none oracle and non-applicable reproduction", strings.Replace(strings.Replace(strings.Replace(validV2, `"oracle":"catch"`, `"oracle":"none"`, 1), `"applies":true`, `"applies":false`, 1), `"attempts":1`, `"attempts":0`, 1), ""},
		{"schema 1 retains legacy validation", strings.Replace(base, `"id":"c1"`, `"schema":1,"id":"c1"`, 1), ""},
		{"schema 2 requires issue type", strings.Replace(validV2, `,"issue_type":"boundary"`, "", 1), "issue_type"},
		{"schema 2 requires domain", strings.Replace(validV2, `"domain":"unchecked-here"`, `"domain":""`, 1), "domain"},
		{"schema 2 requires severity", strings.Replace(validV2, `"severity":"unchecked-here"`, `"severity":""`, 1), "severity"},
		{"schema 2 requires severity rationale", strings.Replace(validV2, `"severity_rationale":"rationale"`, `"severity_rationale":""`, 1), "severity_rationale"},
		{"schema 2 requires expected behavior", strings.Replace(validV2, `"expected_behavior":"expected"`, `"expected_behavior":""`, 1), "expected_behavior"},
		{"schema 2 requires failure condition", strings.Replace(validV2, `"failure_condition":"input -> actual"`, `"failure_condition":""`, 1), "failure_condition"},
		{"schema 2 requires detection criteria", strings.Replace(validV2, `,"detection_criteria":{"mechanism":"mechanism","equivalents":[],"proof":"proof"}`, "", 1), "detection_criteria"},
		{"schema 2 requires detection mechanism", strings.Replace(validV2, `"mechanism":"mechanism"`, `"mechanism":""`, 1), "mechanism"},
		{"schema 2 requires detection proof", strings.Replace(validV2, `"proof":"proof"`, `"proof":""`, 1), "proof"},
		{"schema 2 requires reproduction", strings.Replace(validV2, `,"reproduction":{"applies":true,"oracle":"catch","nondeterministic":false,"attempts":1}`, "", 1), "reproduction"},
		{"applicable reproduction needs attempts", strings.Replace(validV2, `"attempts":1`, `"attempts":0`, 1), "attempts"},
		{"reproduction oracle is enumerated", strings.Replace(validV2, `"oracle":"catch"`, `"oracle":"surprise"`, 1), "oracle"},
		{"unknown schema is refused", strings.Replace(validV2, `"schema":2`, `"schema":3`, 1), "schema"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			_, err := LoadKey(writeKey(t, tc.body))
			if tc.wantErr == "" {
				if err != nil {
					t.Fatalf("unexpected error: %v", err)
				}
			} else if err == nil || !strings.Contains(err.Error(), tc.wantErr) {
				t.Fatalf("error = %v, want containing %q", err, tc.wantErr)
			}
		})
	}
}

func TestLoadKeyCleanControl(t *testing.T) {
	clean := `{"id":"c1","language":"go","suite":"go test ./...","surface":"lib","control":"clean","defects":[]}`
	key, err := LoadKey(writeKey(t, clean))
	if err != nil {
		t.Fatalf("clean control refused: %v", err)
	}
	if !key.IsCleanControl() {
		t.Fatalf("IsCleanControl = false for %+v", key)
	}

	withDefect := `{"id":"c1","language":"go","suite":"go test ./...","surface":"lib","control":"clean","defects":[{"id":"d1","file":"src/a.go","line":3,"keywords":["limit"]}]}`
	if _, err := LoadKey(writeKey(t, withDefect)); err == nil || !strings.Contains(err.Error(), "1 defect") {
		t.Fatalf("clean control with one defect error = %v, want the count", err)
	}

	unknown := strings.Replace(clean, `"clean"`, `"mystery"`, 1)
	if _, err := LoadKey(writeKey(t, unknown)); err == nil || !strings.Contains(err.Error(), `mystery`) || !strings.Contains(err.Error(), "clean") {
		t.Fatalf("unknown control error = %v, want value and accepted control", err)
	}
}

// A case's bounded request is the unit of work the run is asked to test. A key without one is the
// generic case the bench has always run; a key that supplies a blank one is a mistake in the key, so
// it is refused instead of being read back as "no request".
func TestLoadKeyValidatesTheCaseRequest(t *testing.T) {
	valid := `{"id":"c1","language":"node","suite":"node --test","surface":"lib","defects":[{"id":"d1","file":"src/a.js","line":3,"class":"boundary","keywords":["limit"],"description":"x","trigger":{"input":"i","expected":"e","actual":"a"},"why_missed":"w"}]}`
	withRequest := func(value string) string {
		return strings.Replace(valid, `"surface":"lib"`, `"surface":"lib","request":`+value, 1)
	}
	cases := []struct {
		name    string
		body    string
		want    string
		wantErr string
	}{
		{"no request is the generic case", valid, "", ""},
		{"a request names the bounded unit of work", withRequest(`"src/slug.js"`), "src/slug.js", ""},
		{"a request is trimmed", withRequest(`"  src/slug.js  "`), "src/slug.js", ""},
		{"a blank request is refused", withRequest(`"   "`), "", "request"},
		{"a null request is the generic case", withRequest("null"), "", ""},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			k, err := LoadKey(writeKey(t, tc.body))
			if tc.wantErr != "" {
				if err == nil || !strings.Contains(err.Error(), tc.wantErr) {
					t.Fatalf("error = %v, want containing %q", err, tc.wantErr)
				}
				return
			}
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if got := k.RequestText(); got != tc.want {
				t.Fatalf("RequestText = %q, want %q", got, tc.want)
			}
		})
	}
}

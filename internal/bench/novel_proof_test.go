package bench

import (
	"crypto/sha256"
	"encoding/hex"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestReplayNovelSavedTestRequiresFailingFixtureAndPassingControl(t *testing.T) {
	root := t.TempDir()
	caseDir := filepath.Join(root, "case")
	fixture := filepath.Join(caseDir, FixtureDir)
	artifactDir := filepath.Join(root, "artifacts")
	controlDir := filepath.Join(root, "control")
	write := func(path, body string) {
		t.Helper()
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	write(filepath.Join(fixture, "package.json"), `{"type":"module"}`)
	write(filepath.Join(fixture, "lib.mjs"), `export const value = 1;`)
	write(filepath.Join(controlDir, "lib.mjs"), `export const value = 2;`)
	write(filepath.Join(artifactDir, "check.test.mjs"), `import {test} from 'node:test'; import {strict as a} from 'node:assert'; import {value} from './lib.mjs'; test('saved-check',()=>a.equal(value,2));`)
	data, err := os.ReadFile(filepath.Join(artifactDir, "check.test.mjs"))
	if err != nil {
		t.Fatal(err)
	}
	sum := sha256.Sum256(data)
	got, err := ReplayNovelSavedTest(NovelReplayInput{RuleVersion: NovelCorrectnessRuleV1, CaseDir: caseDir, ArtifactDir: artifactDir, Artifacts: []TestArtifact{{Path: "check.test.mjs", SHA256: hex.EncodeToString(sum[:])}}, Suite: "node --test check.test.mjs", ControlName: "explicit-fix", ControlDir: controlDir, Timeout: 5 * time.Second})
	if err != nil {
		t.Fatal(err)
	}
	if got.Subject.Classification != NovelAssertionFailure {
		t.Fatalf("subject not assertion failure: %s %s", got.Subject.Classification, got.Subject.Reason)
	}
	if got.Control.Classification != NovelPassed {
		t.Fatalf("control not pass: %s %s; tap=%t", got.Control.Classification, got.Control.Reason, nodeTAPPassed(got.Control.Output, map[string][]byte{"check.test.mjs": data}))
	}
	if !got.SupportedReproduction {
		t.Fatal("fixture-fail/control-pass contrast not supported")
	}
	if got.TestSHA256 != digest(append(append([]byte("check.test.mjs\x00"), data...), 0)) {
		t.Fatalf("saved test identity mismatch: %s", got.TestSHA256)
	}
	staged := t.TempDir()
	for name, body := range map[string][]byte{"package.json": []byte(`{"type":"module"}`), "lib.mjs": []byte("export const value = 1;"), "check.test.mjs": data} {
		if err := os.WriteFile(filepath.Join(staged, name), body, 0644); err != nil {
			t.Fatal(err)
		}
	}
	wantSource, err := treeDigest(staged)
	if err != nil {
		t.Fatal(err)
	}
	if got.Subject.SourceSHA256 != wantSource {
		t.Fatalf("staged source digest mismatch: got %s want %s", got.Subject.SourceSHA256, wantSource)
	}
	controlHash, err := treeDigest(controlDir)
	if err != nil {
		t.Fatal(err)
	}
	if got.Control.ControlSHA256 != controlHash {
		t.Fatalf("control digest mismatch: %s", got.Control.ControlSHA256)
	}
	if got.Subject.OutputSHA256 != digest([]byte(got.Subject.Output)) || got.Control.OutputSHA256 != digest([]byte(got.Control.Output)) {
		t.Fatal("output digest does not bind captured output")
	}
	if changed, err := os.ReadFile(filepath.Join(controlDir, "lib.mjs")); err != nil || string(changed) != "export const value = 2;" {
		t.Fatalf("replay mutated original control bytes: %q, %v", changed, err)
	}
}

func TestNovelReplayRefusesInvalidInputsBeforeExecution(t *testing.T) {
	root := t.TempDir()
	caseDir, artifactDir, controlDir := filepath.Join(root, "case"), filepath.Join(root, "artifacts"), filepath.Join(root, "control")
	write := func(p, s string) {
		t.Helper()
		if err := os.MkdirAll(filepath.Dir(p), 0755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte(s), 0644); err != nil {
			t.Fatal(err)
		}
	}
	write(filepath.Join(caseDir, FixtureDir, "value.mjs"), "export const value=1;")
	write(filepath.Join(controlDir, "value.mjs"), "export const value=2;")
	write(filepath.Join(artifactDir, "check.test.mjs"), "test('x',()=>{});")
	good, err := os.ReadFile(filepath.Join(artifactDir, "check.test.mjs"))
	if err != nil {
		t.Fatal(err)
	}
	sum := sha256.Sum256(good)
	input := NovelReplayInput{RuleVersion: NovelCorrectnessRuleV1, CaseDir: caseDir, ArtifactDir: artifactDir, Artifacts: []TestArtifact{{Path: "check.test.mjs", SHA256: hex.EncodeToString(sum[:])}}, Suite: "node --test check.test.mjs", ControlName: "fix", ControlDir: controlDir, Timeout: time.Second}
	cases := []struct {
		name   string
		mutate func()
	}{
		{"tampered", func() { write(filepath.Join(artifactDir, "check.test.mjs"), "tampered") }},
		{"missing", func() {
			if err := os.Remove(filepath.Join(artifactDir, "check.test.mjs")); err != nil {
				t.Fatal(err)
			}
		}},
		{"duplicate", func() { input.Artifacts = append(input.Artifacts, input.Artifacts[0]) }},
		{"unsupported runner", func() { input.Suite = "bash -c true" }},
		{"runner option injection", func() { input.Suite = "node --test --require evil.js check.test.mjs" }},
		{"control replaces saved test", func() { write(filepath.Join(controlDir, "check.test.mjs"), "test('fake',()=>{});") }},
		{"control runner config", func() { write(filepath.Join(controlDir, "package.json"), `{"scripts":{"test":"true"}}`) }},
		{"control symlink", func() {
			if err := os.Symlink("/etc/passwd", filepath.Join(controlDir, "link")); err != nil {
				t.Fatal(err)
			}
		}},
		{"Go explicitly unsupported", func() { input.Suite = "go test ./..." }},
		{"unsupported rule", func() { input.RuleVersion = "future-rule" }},
		{"unsafe test path", func() {
			input.Artifacts = []TestArtifact{{Path: "../check.test.mjs", SHA256: hex.EncodeToString(sum[:])}}
			input.Suite = "node --test ../check.test.mjs"
		}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			write(filepath.Join(artifactDir, "check.test.mjs"), string(good))
			input.Artifacts = []TestArtifact{{Path: "check.test.mjs", SHA256: hex.EncodeToString(sum[:])}}
			input.Suite = "node --test check.test.mjs"
			input.RuleVersion = NovelCorrectnessRuleV1
			if err := os.Remove(filepath.Join(controlDir, "check.test.mjs")); err != nil && !os.IsNotExist(err) {
				t.Fatal(err)
			}
			if err := os.Remove(filepath.Join(controlDir, "package.json")); err != nil && !os.IsNotExist(err) {
				t.Fatal(err)
			}
			if err := os.Remove(filepath.Join(controlDir, "link")); err != nil && !os.IsNotExist(err) {
				t.Fatal(err)
			}
			tc.mutate()
			if _, err := ReplayNovelSavedTest(input); err == nil {
				t.Fatal("invalid saved-test selection executed instead of refusing")
			}
		})
	}
}

func TestNovelNodeFailureRequiresCoherentAssertionTAP(t *testing.T) {
	valid := `TAP version 13
# Subtest: saved-check
not ok 1 - saved-check
  ---
  error: |-
    Expected values to be strictly equal:
    1 !== 2
  code: 'ERR_ASSERTION'
  name: 'AssertionError'
  ...
1..1
# tests 1
# pass 0
# fail 1
# cancelled 0
# skipped 0
# todo 0
`
	if !nodeAssertionFailure(valid, map[string][]byte{"check.test.mjs": nil}) {
		t.Fatal("valid completed assertion TAP not recognized")
	}
	for name, out := range map[string]string{
		"spoofed diagnostic":   "TAP version 13\nnot ok 1 - saved-check\n error: AssertionError [ERR_ASSERTION] from user output\n1..1\n# tests 1\n# pass 0\n# fail 1\n# skipped 0\n",
		"empty suite":          "TAP version 13\n1..0\n# tests 0\n# pass 0\n# fail 0\n",
		"skip only":            "TAP version 13\n1..1\nok 1 - saved-check # SKIP\n# tests 1\n# pass 0\n# fail 0\n# skipped 1\n",
		"truncated":            strings.Split(valid, "1..1")[0],
		"bailout":              "TAP version 13\nBail out! setup failed\n",
		"inconsistent summary": strings.Replace(valid, "# fail 1", "# fail 0", 1),
		"uncaught exception":   "TAP version 13\nnot ok 1 - saved-check\n  error: uncaught exception\n1..1\n# tests 1\n# pass 0\n# fail 1\n# skipped 0\n",
	} {
		t.Run(name, func(t *testing.T) {
			if nodeAssertionFailure(out, map[string][]byte{"check.test.mjs": nil}) {
				t.Fatal("untrusted/incomplete TAP classified as assertion failure")
			}
		})
	}
}

func TestNovelProcessDistinguishesStartFailureAndTimeout(t *testing.T) {
	root := t.TempDir()
	fixture := filepath.Join(root, "fixture")
	if err := os.MkdirAll(fixture, 0755); err != nil {
		t.Fatal(err)
	}
	t.Run("start failure", func(t *testing.T) {
		t.Setenv("PATH", t.TempDir())
		got := runNovelSide(fixture, "", map[string][]byte{"check.test.mjs": []byte("test('x',()=>{});")}, []string{"node", "--test", "--test-reporter=tap", "check.test.mjs"}, "node", time.Second, "tests")
		if got.Started || got.Classification != NovelInconclusive {
			t.Fatalf("start failure not distinguished: %+v", got)
		}
	})
	t.Run("timeout", func(t *testing.T) {
		got := runNovelSide(fixture, "", map[string][]byte{"check.test.mjs": []byte("test('x',()=>{});")}, []string{"node", "--test", "--test-reporter=tap", "check.test.mjs"}, "node", time.Nanosecond, "tests")
		if !got.TimedOut || got.Classification != NovelInconclusive {
			t.Fatalf("timeout not distinguished: %+v", got)
		}
	})
}

func TestNovelReplayImportAndUncaughtFailuresAreInconclusive(t *testing.T) {
	for _, tc := range []struct{ name, source string }{
		{"import setup error", "import './missing-module.mjs';"},
		{"uncaught exception", `import {test} from 'node:test';test('saved-check',()=>{throw new Error('uncaught')});`},
		{"assertion plus crash", `import {test} from 'node:test';import {strict as a} from 'node:assert';test('assertion',()=>a.equal(1,2));test('crash',()=>{throw new Error('uncaught')});`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got := novelReplayFixture(t, "export const value=1;", "export const value=2;", tc.source)
			if got.SupportedReproduction || got.Subject.Classification == NovelAssertionFailure {
				t.Fatalf("setup/crash counted as a reproduction: %+v", got.Subject)
			}
		})
	}
}

func TestNovelNodeReplayBothPassAndBothFailAreNotReproduction(t *testing.T) {
	for _, tc := range []struct {
		name             string
		fixture, control string
	}{
		{"both pass", "export const value=2;", "export const value=2;"},
		{"both fail", "export const value=1;", "export const value=1;"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got := novelReplayFixture(t, tc.fixture, tc.control, `import {test} from 'node:test';import {strict as a} from 'node:assert';import {value} from './lib.mjs';test('saved-check',()=>a.equal(value,2));`)
			if got.SupportedReproduction {
				t.Fatal("matching outcomes cannot establish fixture-fail/control-pass")
			}
		})
	}
}

func novelReplayFixture(t *testing.T, fixtureSource, controlSource, testSource string) NovelReplayObservation {
	t.Helper()
	root := t.TempDir()
	caseDir := filepath.Join(root, "case")
	artifactDir := filepath.Join(root, "artifacts")
	controlDir := filepath.Join(root, "control")
	write := func(p, s string) {
		t.Helper()
		if err := os.MkdirAll(filepath.Dir(p), 0755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte(s), 0644); err != nil {
			t.Fatal(err)
		}
	}
	write(filepath.Join(caseDir, FixtureDir, "package.json"), `{"type":"module"}`)
	write(filepath.Join(caseDir, FixtureDir, "lib.mjs"), fixtureSource)
	write(filepath.Join(controlDir, "lib.mjs"), controlSource)
	write(filepath.Join(artifactDir, "check.test.mjs"), testSource)
	b, err := os.ReadFile(filepath.Join(artifactDir, "check.test.mjs"))
	if err != nil {
		t.Fatal(err)
	}
	sum := sha256.Sum256(b)
	got, err := ReplayNovelSavedTest(NovelReplayInput{RuleVersion: NovelCorrectnessRuleV1, CaseDir: caseDir, ArtifactDir: artifactDir, Artifacts: []TestArtifact{{Path: "check.test.mjs", SHA256: hex.EncodeToString(sum[:])}}, Suite: "node --test check.test.mjs", ControlName: "explicit", ControlDir: controlDir, Timeout: 2 * time.Second})
	if err != nil {
		t.Fatal(err)
	}
	return got
}

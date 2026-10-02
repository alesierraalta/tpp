package doctor

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/alesierraalta/tpp/internal/assets"
	"github.com/alesierraalta/tpp/internal/sync"
)

func allPresent(name string) (string, error) { return "/usr/bin/" + name, nil }

func synced(t *testing.T) string {
	t.Helper()
	cfg := t.TempDir()
	if _, err := sync.Sync(cfg, "/opt/tools/tpp", sync.Options{}); err != nil {
		t.Fatal(err)
	}
	return cfg
}

func TestDoctorHealthyAfterSync(t *testing.T) {
	r := Run(synced(t), allPresent)
	if !r.Healthy {
		t.Fatalf("expected healthy, problems: %v", r.Problems)
	}
	if !r.HookWired || !strings.HasSuffix(r.HookCommand, " gate") {
		t.Fatalf("hook not detected: %+v", r)
	}
	for _, s := range r.Skills {
		if !s.Installed || !s.Matches {
			t.Errorf("skill %s: installed=%v matches=%v", s.Name, s.Installed, s.Matches)
		}
	}
	if !strings.Contains(r.String(), "verdict: healthy") {
		t.Fatal("text report should say healthy")
	}
}

func TestDoctorMissingGitIsUnhealthy(t *testing.T) {
	cfg := synced(t)
	noGit := func(name string) (string, error) {
		if name == "git" {
			return "", errors.New("not found")
		}
		return allPresent(name)
	}
	r := Run(cfg, noGit)
	if r.Healthy {
		t.Fatal("missing git must make the report unhealthy")
	}
	found := false
	for _, p := range r.Problems {
		if strings.Contains(p, "git") {
			found = true
		}
	}
	if !found {
		t.Fatalf("problems do not name git: %v", r.Problems)
	}
	if !strings.Contains(r.String(), "ABSENT (required)") {
		t.Fatal("text report should mark git as required and absent")
	}
}

func TestDoctorEmptyConfigDir(t *testing.T) {
	r := Run(t.TempDir(), allPresent)
	if r.Healthy {
		t.Fatal("nothing installed must be unhealthy")
	}
	if r.HookWired {
		t.Fatal("no settings.json, hook cannot be wired")
	}
	if len(r.Problems) < len(assets.SkillNames())+1 {
		t.Fatalf("expected a problem per missing skill plus the hook, got %d", len(r.Problems))
	}
}

func TestDoctorReportsSkillDrift(t *testing.T) {
	cfg := synced(t)
	name := assets.SkillNames()[0]
	if err := os.WriteFile(filepath.Join(cfg, "skills", name, "SKILL.md"), []byte("edited\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	r := Run(cfg, allPresent)
	var st SkillStatus
	for _, s := range r.Skills {
		if s.Name == name {
			st = s
		}
	}
	if !st.Installed || st.Matches {
		t.Fatalf("drift not reported: %+v", st)
	}
	if !r.Healthy {
		t.Fatal("drift is a warning, not a failure")
	}
	if !strings.Contains(r.String(), "differs from the embedded version") {
		t.Fatal("text report should describe the drift")
	}
}

func TestDoctorOptionalCapabilityDegradesExplicitly(t *testing.T) {
	cfg := synced(t)
	noCodegraph := func(name string) (string, error) {
		if name == "codegraph" {
			return "", errors.New("not found")
		}
		return allPresent(name)
	}
	r := Run(cfg, noCodegraph)
	if !r.Healthy {
		t.Fatalf("an optional tool must not fail the verdict: %v", r.Problems)
	}
	if !strings.Contains(r.String(), "git ls-files") {
		t.Fatal("report should say how inventory degrades without CodeGraph")
	}
}

// The sandbox is the one flow that needs a container runtime, and a machine without docker has to learn
// that from doctor rather than from a row that ran on this machine. The tool is optional, so its absence is
// a degradation and never a problem: the plan's rows still run on this machine, but a row pinned in a container
// cannot be checked without one.
func TestDoctorSaysHowTheSandboxDegradesWithoutDocker(t *testing.T) {
	cfg := synced(t)
	noDocker := func(name string) (string, error) {
		if name == "docker" {
			return "", errors.New("not found")
		}
		return allPresent(name)
	}
	r := Run(cfg, noDocker)
	if !r.Healthy {
		t.Fatalf("an optional tool must not fail the verdict: %v", r.Problems)
	}
	if !strings.Contains(r.String(), "--sandbox") {
		t.Fatal("report should say that plan admit --sandbox degrades without docker")
	}
}

func TestDoctorJSONShape(t *testing.T) {
	r := Run(synced(t), allPresent)
	raw, err := json.Marshal(r)
	if err != nil {
		t.Fatal(err)
	}
	var m map[string]any
	if err := json.Unmarshal(raw, &m); err != nil {
		t.Fatal(err)
	}
	for _, key := range []string{"config_dir", "mode", "skills", "hook_wired", "capabilities", "problems", "healthy"} {
		if _, ok := m[key]; !ok {
			t.Errorf("json report lacks %q", key)
		}
	}
	if m["problems"] == nil {
		t.Fatal("problems must serialize as an array, never null")
	}
}

// The report must carry the mode its health contract ran under, in both halves of the output:
// a machine-readable consumer of `tpp doctor --json` needs to know which contract the verdict
// came from, and the text header says the same thing for a person.
func TestReportNamesTheModeItRanUnder(t *testing.T) {
	r := Run(synced(t), allPresent)
	r.Mode = "standalone"
	if !strings.Contains(r.String(), "mode standalone") {
		t.Fatalf("text report does not name the mode:\n%s", r.String())
	}
	raw, err := json.Marshal(r)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(raw), `"mode":"standalone"`) {
		t.Fatalf("json report does not carry the mode:\n%s", raw)
	}
}

// Standalone health is defined without Gentle AI: the contract this build runs must be healthy
// when neither the Gentle runtime nor Engram exists on the machine at all, so their absence is
// reported as a degradation the flow names, never as a missing requirement.
func TestStandaloneHealthDoesNotRequireGentleOrEngram(t *testing.T) {
	noGentle := func(name string) (string, error) {
		if name == "gentle-ai" || name == "engram" {
			return "", errors.New("not found")
		}
		return allPresent(name)
	}
	r := Run(synced(t), noGentle)
	if !r.Healthy {
		t.Fatalf("standalone health must not require Gentle or Engram: %v", r.Problems)
	}
	for _, c := range r.Capabilities {
		if (c.Name == "gentle-ai" || c.Name == "engram") && c.Required {
			t.Errorf("%s must not be required in standalone mode", c.Name)
		}
	}
}

// A Stop hook that runs a standalone gate binary is a wired gate under another name, not a
// missing gate: reporting "action required" for a working setup trains people to ignore doctor.
func TestHookWiredRecognisesAStandaloneGateBinary(t *testing.T) {
	cases := []struct {
		name     string
		command  string
		wantKind string
	}{
		{"tpp subcommand", `"/home/u/go/bin/tpp" gate`, HookTpp},
		{"tpp with flags", `/home/u/go/bin/tpp gate --config-dir /home/u/.claude`, HookTpp},
		{"standalone binary", `"/home/u/.claude/hooks/bin/testing-gate"`, HookStandalone},
		{"standalone node script", `node /home/u/.claude/hooks/testing-gate.mjs`, HookStandalone},
		{"an unrelated hook", `gentle-ai review stop-hook --agent claude-code`, HookNone},
		{"a command merely mentioning the word", `echo "run the gate later"`, HookNone},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			dir := t.TempDir()
			settings := `{"hooks":{"Stop":[{"matcher":"","hooks":[{"type":"command","command":` + jsonString(tc.command) + `}]}]}}`
			if err := os.WriteFile(filepath.Join(dir, "settings.json"), []byte(settings), 0o644); err != nil {
				t.Fatal(err)
			}
			kind, cmd := hookWired(filepath.Join(dir, "settings.json"))
			if kind != tc.wantKind {
				t.Fatalf("kind = %q, want %q", kind, tc.wantKind)
			}
			if kind != HookNone && cmd != tc.command {
				t.Fatalf("command = %q", cmd)
			}
		})
	}
}

// Only a missing gate is a problem; a gate under another name is reported, not flagged.
func TestStandaloneGateIsHealthyAndNamed(t *testing.T) {
	dir := t.TempDir()
	settings := `{"hooks":{"Stop":[{"matcher":"","hooks":[{"type":"command","command":"/h/.claude/hooks/bin/testing-gate"}]}]}}`
	if err := os.WriteFile(filepath.Join(dir, "settings.json"), []byte(settings), 0o644); err != nil {
		t.Fatal(err)
	}
	r := Run(dir, func(string) (string, error) { return "/usr/bin/x", nil })
	for _, p := range r.Problems {
		if strings.Contains(p, "gate hook not wired") {
			t.Fatalf("a wired standalone gate must not be a problem: %v", r.Problems)
		}
	}
	if !r.HookWired || r.HookKind != HookStandalone {
		t.Fatalf("wired = %v kind = %q", r.HookWired, r.HookKind)
	}
	if !strings.Contains(r.String(), "standalone gate binary") {
		t.Fatalf("the report must name what is wired:\n%s", r.String())
	}
}

func jsonString(s string) string {
	b, _ := json.Marshal(s)
	return string(b)
}

// writeSettings wires one Stop hook command into a config dir, the way sync would.
func writeSettings(t *testing.T, dir, command string) {
	t.Helper()
	settings := `{"hooks":{"Stop":[{"matcher":"","hooks":[{"type":"command","command":` + jsonString(command) + `}]}]}}`
	if err := os.WriteFile(filepath.Join(dir, "settings.json"), []byte(settings), 0o644); err != nil {
		t.Fatal(err)
	}
}

// binary writes a stand-in for the tpp executable.
func binary(t *testing.T, dir, name string) string {
	t.Helper()
	p := filepath.Join(dir, name)
	if err := os.WriteFile(p, []byte("#!/bin/sh\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	return p
}

// Today's real layout points the hook and PATH at the same binary through different paths, one of
// them a symlink. Two paths, one file, one verdict: this must stay quiet.
func TestHookBinaryMatchingPathBinaryIsQuiet(t *testing.T) {
	real := binary(t, t.TempDir(), "tpp")
	link := filepath.Join(t.TempDir(), "tpp-link")
	if err := os.Symlink(real, link); err != nil {
		t.Fatal(err)
	}
	cases := []struct {
		name       string
		hook, path string
	}{
		{"same absolute path", real, real},
		{"hook is a symlink to the same file", link, real},
		{"PATH is a symlink to the same file", real, link},
		{"both are symlinks to the same file", link, link},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			dir := t.TempDir()
			writeSettings(t, dir, `"`+tc.hook+`" gate`)
			r := Run(dir, func(name string) (string, error) {
				if name == "tpp" {
					return tc.path, nil
				}
				return "/usr/bin/" + name, nil
			})
			if r.BinariesDiffer {
				t.Fatalf("same file, different path, must not warn: %+v", r)
			}
			if strings.Contains(r.String(), "different verdicts") {
				t.Fatalf("report = %s", r.String())
			}
		})
	}
}

// Two real binaries can give two verdicts: the hook must run the same file the user gets from PATH.
func TestDoctorFlagsAHookBinaryThatDiffersFromPATH(t *testing.T) {
	dir := t.TempDir()
	hookBin := binary(t, t.TempDir(), "tpp")
	pathBin := binary(t, t.TempDir(), "tpp")
	writeSettings(t, dir, `"`+hookBin+`" gate`)
	r := Run(dir, func(name string) (string, error) {
		if name == "tpp" {
			return pathBin, nil
		}
		return "/usr/bin/" + name, nil
	})
	if !r.BinariesDiffer || r.WiredBinary != hookBin || r.PathBinary != pathBin {
		t.Fatalf("report = %+v", r)
	}
	out := r.String()
	for _, want := range []string{hookBin, pathBin, "different verdicts"} {
		if !strings.Contains(out, want) {
			t.Fatalf("report missing %q:\n%s", want, out)
		}
	}
	raw, err := json.Marshal(r)
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{hookBin, pathBin} {
		if !strings.Contains(string(raw), want) {
			t.Fatalf("json report must carry both paths, missing %q:\n%s", want, raw)
		}
	}
}

// A quoted command is one command, not its first word: a hook wired from a path with a space in it was
// read as its truncated head, which resolved to nothing, so doctor said nothing about a hook that does
// point at a different binary.
func TestQuotedHookPathWithASpaceIsComparedWhole(t *testing.T) {
	dir := t.TempDir()
	spaceDir := filepath.Join(t.TempDir(), "Program Files")
	if err := os.MkdirAll(spaceDir, 0o755); err != nil {
		t.Fatal(err)
	}
	hookBin := binary(t, spaceDir, "tpp")
	pathBin := binary(t, t.TempDir(), "tpp")
	writeSettings(t, dir, `"`+hookBin+`" gate`)
	r := Run(dir, func(name string) (string, error) {
		if name == "tpp" {
			return pathBin, nil
		}
		return "/usr/bin/" + name, nil
	})
	if !r.BinariesDiffer || r.WiredBinary != hookBin || r.PathBinary != pathBin {
		t.Fatalf("a quoted path with a space must be compared as one path: %+v", r)
	}
}

// Doctor already has verdicts for an unwired hook and for tpp off PATH; the new line must not
// reinvent them.
func TestDoctorStaysQuietWhenItCannotCompareBinaries(t *testing.T) {
	if r := Run(t.TempDir(), allPresent); r.BinariesDiffer || strings.Contains(r.String(), "different verdicts") {
		t.Fatalf("an unwired hook must stay quiet: %+v", r)
	}

	dir := t.TempDir()
	writeSettings(t, dir, `"`+binary(t, t.TempDir(), "tpp")+`" gate`)
	noTpp := func(name string) (string, error) {
		if name == "tsp" || name == "tpp" || name == "rdd-plus" {
			return "", errors.New("not found")
		}
		return "/usr/bin/" + name, nil
	}
	r := Run(dir, noTpp)
	if r.BinariesDiffer || strings.Contains(r.String(), "different verdicts") {
		t.Fatalf("tpp off PATH must stay quiet: %+v", r)
	}
}

// A tree the doctor cannot walk is not a match. The comparison used to swallow that error and report an
// unreadable skill as identical — a health check answering "verified" about files nothing read — and the
// fail-closed half is this function's, because the shared reading already answers false.
func TestDoctorDoesNotCallASkillVerifiedWhenItCannotReadTheTree(t *testing.T) {
	// An empty embedded tree: the skill is not in it, so the walk cannot be completed.
	skills := os.DirFS(t.TempDir())
	target := t.TempDir()
	if err := os.WriteFile(filepath.Join(target, "SKILL.md"), []byte("skill\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if matches(skills, "demo", target) {
		t.Fatal("a skill whose tree could not be walked was reported as identical")
	}
}

// After the rename the binary on PATH is tpp, and a machine mid-migration may still carry rdd-plus beside
// it: the comparison asks for tpp first, then tsp, and falls back to rdd-plus when neither resolves.
func TestBinaryComparisonLooksUpTppBeforeRddPlus(t *testing.T) {
	tppBin := binary(t, t.TempDir(), "tpp")
	legacyBin := binary(t, t.TempDir(), "rdd-plus")
	tspBin := binary(t, t.TempDir(), "tsp")
	cases := []struct {
		name       string
		hook       string
		onPath     map[string]string
		wantDiffer bool
		wantPath   string
	}{
		{"tpp on PATH is preferred", tppBin, map[string]string{"tpp": tppBin, "rdd-plus": legacyBin}, false, tppBin},
		{"a legacy hook against tpp on PATH differs", legacyBin, map[string]string{"tpp": tppBin}, true, tppBin},
		{"only rdd-plus on PATH still compares", legacyBin, map[string]string{"rdd-plus": legacyBin}, false, legacyBin},
		{"a tsp-wired hook is compared against tsp on PATH", tspBin, map[string]string{"tsp": legacyBin}, true, legacyBin},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			dir := t.TempDir()
			writeSettings(t, dir, `"`+tc.hook+`" gate`)
			r := Run(dir, func(name string) (string, error) {
				if p, ok := tc.onPath[name]; ok {
					return p, nil
				}
				if name == "tsp" || name == "tpp" || name == "rdd-plus" {
					return "", errors.New("not found")
				}
				return "/usr/bin/" + name, nil
			})
			if r.BinariesDiffer != tc.wantDiffer {
				t.Fatalf("BinariesDiffer = %v, want %v: %+v", r.BinariesDiffer, tc.wantDiffer, r)
			}
			if tc.wantDiffer && r.PathBinary != tc.wantPath {
				t.Fatalf("PathBinary = %q, want %q", r.PathBinary, tc.wantPath)
			}
		})
	}
}

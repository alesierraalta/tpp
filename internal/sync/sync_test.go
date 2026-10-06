package sync

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"testing"

	root "github.com/alesierraalta/tsp/assets"
	"github.com/alesierraalta/tsp/internal/assets"
	"github.com/alesierraalta/tsp/internal/state"
)

const bin = "/opt/tools/tpp"

func readSettings(t *testing.T, cfg string) map[string]any {
	t.Helper()
	raw, err := os.ReadFile(filepath.Join(cfg, "settings.json"))
	if err != nil {
		t.Fatalf("settings.json: %v", err)
	}
	var s map[string]any
	if err := json.Unmarshal(raw, &s); err != nil {
		t.Fatalf("settings.json is not JSON: %v", err)
	}
	return s
}

func stopCommands(t *testing.T, s map[string]any) []string {
	t.Helper()
	var out []string
	hooks, _ := s["hooks"].(map[string]any)
	stop, _ := hooks["Stop"].([]any)
	for _, e := range stop {
		entry := e.(map[string]any)
		for _, h := range entry["hooks"].([]any) {
			out = append(out, h.(map[string]any)["command"].(string))
		}
	}
	return out
}

func mustAbs(t *testing.T, path string) string {
	t.Helper()
	absolute, err := filepath.Abs(path)
	if err != nil {
		t.Fatal(err)
	}
	return absolute
}

func TestSyncFreshConfigDir(t *testing.T) {
	t.Setenv("TPP_HOME", t.TempDir())
	cfg := t.TempDir()
	report, err := Sync(cfg, bin, Options{})
	if err != nil {
		t.Fatal(err)
	}
	if len(report.Written) != len(assets.SkillNames()) {
		t.Fatalf("written %d skills, want %d", len(report.Written), len(assets.SkillNames()))
	}
	for _, name := range assets.SkillNames() {
		if _, err := os.Stat(filepath.Join(cfg, "skills", name, "SKILL.md")); err != nil {
			t.Errorf("skill %s not installed: %v", name, err)
		}
	}
	if !report.SettingsChanged {
		t.Fatal("settings should be created on a fresh dir")
	}
	cmds := stopCommands(t, readSettings(t, cfg))
	if len(cmds) != 1 || cmds[0] != HookCommand(bin) {
		t.Fatalf("stop hooks = %q, want exactly %q", cmds, HookCommand(bin))
	}
}

func TestSyncPreservesExistingHooksAndSettings(t *testing.T) {
	t.Setenv("TPP_HOME", t.TempDir())
	cfg := t.TempDir()
	existing := `{
  "model": "opus",
  "hooks": {
    "PreToolUse": [{"matcher": "Bash", "hooks": [{"type": "command", "command": "rtk hook claude"}]}],
    "Stop": [{"matcher": "", "hooks": [{"type": "command", "command": "gentle-ai review stop-hook --agent claude-code", "timeout": 60}]}]
  }
}`
	if err := os.WriteFile(filepath.Join(cfg, "settings.json"), []byte(existing), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := Sync(cfg, bin, Options{}); err != nil {
		t.Fatal(err)
	}
	s := readSettings(t, cfg)
	if s["model"] != "opus" {
		t.Errorf("unrelated setting lost: model=%v", s["model"])
	}
	pre, _ := s["hooks"].(map[string]any)["PreToolUse"].([]any)
	if len(pre) != 1 {
		t.Errorf("PreToolUse hooks lost: %v", pre)
	}
	cmds := stopCommands(t, s)
	if len(cmds) != 2 || cmds[0] != "gentle-ai review stop-hook --agent claude-code" || cmds[1] != HookCommand(bin) {
		t.Fatalf("stop hooks = %q", cmds)
	}
}

func TestSyncIsIdempotent(t *testing.T) {
	t.Setenv("TPP_HOME", t.TempDir())
	cfg := t.TempDir()
	if _, err := Sync(cfg, bin, Options{}); err != nil {
		t.Fatal(err)
	}
	before, _ := os.ReadFile(filepath.Join(cfg, "settings.json"))
	report, err := Sync(cfg, bin, Options{})
	if err != nil {
		t.Fatal(err)
	}
	if len(report.Written) != 0 || len(report.Unchanged) != len(assets.SkillNames()) {
		t.Fatalf("second run wrote %v, unchanged %d", report.Written, len(report.Unchanged))
	}
	if report.SettingsChanged {
		t.Fatal("second run must not change settings")
	}
	after, _ := os.ReadFile(filepath.Join(cfg, "settings.json"))
	if string(before) != string(after) {
		t.Fatal("settings.json bytes changed on an idempotent run")
	}
	if len(stopCommands(t, readSettings(t, cfg))) != 1 {
		t.Fatal("gate hook duplicated")
	}
}

func TestSyncBacksUpADifferingSkill(t *testing.T) {
	t.Setenv("TPP_HOME", t.TempDir())
	cfg := t.TempDir()
	if _, err := Sync(cfg, bin, Options{}); err != nil {
		t.Fatal(err)
	}
	name := assets.SkillNames()[0]
	skillMD := filepath.Join(cfg, "skills", name, "SKILL.md")
	if err := os.WriteFile(skillMD, []byte("local edit\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(skillMD, 0o600); err != nil {
		t.Fatal(err)
	}
	report, err := Sync(cfg, bin, Options{Force: true})
	if err != nil {
		t.Fatal(err)
	}
	backup, ok := report.BackedUp[name]
	if !ok {
		t.Fatalf("expected %s to be backed up; report %+v", name, report)
	}
	var manifest backupManifest
	data, err := os.ReadFile(filepath.Join(backup, "manifest.json"))
	if err != nil || json.Unmarshal(data, &manifest) != nil {
		t.Fatalf("backup manifest: %v", err)
	}
	if manifest.FileCount != 1 || len(manifest.Entries) != 1 {
		t.Fatalf("manifest entries = %+v", manifest.Entries)
	}
	entry := manifest.Entries[0]
	if entry.OriginalPath != mustAbs(t, skillMD) || entry.Mode != 0o600 {
		t.Fatalf("manifest entry = %+v", entry)
	}
	saved, err := os.ReadFile(filepath.Join(backup, filepath.FromSlash(entry.SnapshotPath)))
	if err != nil || string(saved) != "local edit\n" {
		t.Fatalf("backup does not hold the local edit: %v %q", err, saved)
	}
	restored, _ := os.ReadFile(skillMD)
	if string(restored) == "local edit\n" {
		t.Fatal("skill was not replaced with the embedded version")
	}
	if strings.Contains(backup, filepath.Join("skills", ".tpp-backup")) {
		t.Fatalf("backup dir %s still uses the retired per-host layout", backup)
	}
}

func TestSyncRefusesInvalidSettingsAndWritesNothing(t *testing.T) {
	t.Setenv("TPP_HOME", t.TempDir())
	cfg := t.TempDir()
	broken := []byte("{not json")
	if err := os.WriteFile(filepath.Join(cfg, "settings.json"), broken, 0o644); err != nil {
		t.Fatal(err)
	}
	_, err := Sync(cfg, bin, Options{})
	if err == nil {
		t.Fatal("expected an error on invalid settings.json")
	}
	if _, err := os.Stat(filepath.Join(cfg, "skills")); err == nil {
		t.Fatal("skills were written despite invalid settings")
	}
	after, _ := os.ReadFile(filepath.Join(cfg, "settings.json"))
	if string(after) != string(broken) {
		t.Fatal("settings.json was modified")
	}
}

// A report is printed before the error that explains a refusal, so it must not claim a state it never read:
// telling a reader the gate hook is `already wired` in a file the tool could not parse is a sentence about a
// file nobody looked at, and it sits one line above the refusal that says otherwise.
func TestSyncSaysNothingAboutTheHookInSettingsItCouldNotRead(t *testing.T) {
	t.Setenv("TPP_HOME", t.TempDir())
	cfg := t.TempDir()
	if err := os.WriteFile(filepath.Join(cfg, "settings.json"), []byte("{not json"), 0o644); err != nil {
		t.Fatal(err)
	}
	report, err := Sync(cfg, bin, Options{})
	if err == nil {
		t.Fatal("expected an error on invalid settings.json")
	}
	if got := report.String(); strings.Contains(got, "gate hook") {
		t.Fatalf("the report claims a hook state it never read:\n%s", got)
	}
}

func TestSyncDryRunWritesNothing(t *testing.T) {
	t.Setenv("TPP_HOME", t.TempDir())
	cfg := t.TempDir()
	report, err := Sync(cfg, bin, Options{DryRun: true})
	if err != nil {
		t.Fatal(err)
	}
	if !report.DryRun || len(report.Written) == 0 {
		t.Fatalf("dry-run report should plan writes: %+v", report)
	}
	if _, err := os.Stat(filepath.Join(cfg, "skills")); err == nil {
		t.Fatal("dry-run wrote skills")
	}
	if _, err := os.Stat(filepath.Join(cfg, "settings.json")); err == nil {
		t.Fatal("dry-run wrote settings.json")
	}
	if !strings.Contains(report.String(), "[dry-run]") {
		t.Fatal("report should be marked as dry-run")
	}
}

func TestSyncRemovesPreviousGateHooks(t *testing.T) {
	t.Setenv("TPP_HOME", t.TempDir())
	cfg := t.TempDir()
	existing := `{"hooks":{"Stop":[
  {"matcher":"","hooks":[{"type":"command","command":"gentle-ai review stop-hook --agent claude-code","timeout":60}]},
  {"matcher":"","hooks":[{"type":"command","command":"\"/home/u/.claude/hooks/testing-gate.mjs\"","timeout":30}]},
  {"matcher":"","hooks":[{"type":"command","command":"\"/home/u/.claude/hooks/bin/testing-gate\"","timeout":30}]},
  {"matcher":"","hooks":[{"type":"command","command":"\"/old/place/rdd-plus\" gate","timeout":30}]}
]}}`
	if err := os.WriteFile(filepath.Join(cfg, "settings.json"), []byte(existing), 0o644); err != nil {
		t.Fatal(err)
	}
	report, err := Sync(cfg, bin, Options{})
	if err != nil {
		t.Fatal(err)
	}
	if len(report.RemovedHooks) != 3 {
		t.Fatalf("removed %d previous gate hooks, want 3: %q", len(report.RemovedHooks), report.RemovedHooks)
	}
	cmds := stopCommands(t, readSettings(t, cfg))
	if len(cmds) != 2 || cmds[0] != "gentle-ai review stop-hook --agent claude-code" || cmds[1] != HookCommand(bin) {
		t.Fatalf("stop hooks = %q", cmds)
	}
}

// Upgrading from rdd-plus to tpp rewrites the wired gate in place: one Stop entry for the new binary, no
// leftover for the old one, and a neighbour hook that only mentions the old name in its path survives.
func TestSyncRewritesALegacyGateHookToTheNewBinary(t *testing.T) {
	t.Setenv("TPP_HOME", t.TempDir())
	cfg := t.TempDir()
	existing := `{"hooks":{"Stop":[
  {"matcher":"","hooks":[{"type":"command","command":"\"/opt/rdd-plus-tools/notify\" gate","timeout":30}]},
  {"matcher":"","hooks":[{"type":"command","command":"\"/opt/tools/rdd-plus\" gate","timeout":30}]}
]}}`
	if err := os.WriteFile(filepath.Join(cfg, "settings.json"), []byte(existing), 0o644); err != nil {
		t.Fatal(err)
	}
	const tppBin = "/opt/tools/tpp"
	for i := 0; i < 2; i++ {
		if _, err := Sync(cfg, tppBin, Options{}); err != nil {
			t.Fatal(err)
		}
	}
	cmds := stopCommands(t, readSettings(t, cfg))
	want := []string{`"/opt/rdd-plus-tools/notify" gate`, HookCommand(tppBin)}
	if len(cmds) != len(want) || cmds[0] != want[0] || cmds[1] != want[1] {
		t.Fatalf("stop hooks = %q, want %q", cmds, want)
	}
}

// An install made by rdd-plus records its hook in state as wired, so a planner that only asks "is a hook
// wired?" never looks at settings again: the upgrade must still move the hook to the new binary.
func TestSyncRewiresATppGateToTspAndIsIdempotent(t *testing.T) {
	t.Setenv("TPP_HOME", t.TempDir())
	cfg := t.TempDir()
	const tspBin = "/opt/tools/tsp"
	if err := os.WriteFile(filepath.Join(cfg, "settings.json"), []byte(`{"hooks":{"Stop":[{"matcher":"","hooks":[{"type":"command","command":"/opt/tools/tpp gate","timeout":30}]}]}}`), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := Sync(cfg, tspBin, Options{}); err != nil {
		t.Fatal(err)
	}
	first := readSettings(t, cfg)
	if got := stopCommands(t, first); len(got) != 1 || got[0] != HookCommand(tspBin) {
		t.Fatalf("first sync stop hooks = %q, want %q", got, HookCommand(tspBin))
	}
	if _, err := Sync(cfg, tspBin, Options{}); err != nil {
		t.Fatal(err)
	}
	got, err := json.Marshal(readSettings(t, cfg))
	if err != nil {
		t.Fatal(err)
	}
	want, err := json.Marshal(first)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(got, want) {
		t.Fatalf("second sync changed settings: got %s, want %s", got, want)
	}
}

func TestSyncRewiresAHookThatStateRecordsForTheLegacyBinary(t *testing.T) {
	t.Setenv("TPP_HOME", t.TempDir())
	cfg := t.TempDir()
	const legacyBin, tppBin = "/opt/tools/rdd-plus", "/opt/tools/tpp"
	if _, err := Sync(cfg, legacyBin, Options{}); err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 2; i++ {
		if _, err := Sync(cfg, tppBin, Options{}); err != nil {
			t.Fatal(err)
		}
	}
	cmds := stopCommands(t, readSettings(t, cfg))
	if len(cmds) != 1 || cmds[0] != HookCommand(tppBin) {
		t.Fatalf("stop hooks = %q, want only %q", cmds, HookCommand(tppBin))
	}
}

func TestIsPreviousGate(t *testing.T) {
	cases := []struct {
		cmd  string
		want bool
	}{
		{`"/home/u/.claude/hooks/testing-gate.mjs"`, true},
		{`"/home/u/.claude/hooks/bin/testing-gate"`, true},
		{`"/old/rdd-plus" gate`, true},
		{`/usr/local/bin/rdd-plus gate`, true},
		{`"/home/u/go/bin/tpp" gate`, true},
		{`"/opt/Program Files/tpp" gate`, true},
		// Only the executable's own name counts: a directory or a sibling tool that merely mentions the
		// product is somebody else's hook.
		{`"/opt/rdd-plus-tools/notify" gate`, false},
		{`"/home/u/rdd-plus/bin/other" gate`, false},
		{`"/home/u/go/bin/tpp" check`, false},
		{`gentle-ai review stop-hook --agent claude-code`, false},
		{`"/home/u/.claude/hooks/ctx-read-guard.mjs"`, false},
		{`some-other-tool gate`, false},
	}
	for _, c := range cases {
		t.Run(c.cmd, func(t *testing.T) {
			if got := isPreviousGate(c.cmd); got != c.want {
				t.Fatalf("isPreviousGate(%q) = %v, want %v", c.cmd, got, c.want)
			}
		})
	}
}

func testAllHosts(t *testing.T) []Host {
	t.Helper()
	t.Setenv("TPP_HOME", t.TempDir())
	home := t.TempDir()
	for _, dir := range []string{".claude", filepath.Join(".config", "opencode"), ".gemini", ".codex"} {
		if err := os.MkdirAll(filepath.Join(home, dir), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	return DiscoverHosts(home, "").Hosts
}

func hostReport(t *testing.T, report Report, name string) HostReport {
	t.Helper()
	for _, host := range report.Hosts {
		if host.Host.Name == name {
			return host
		}
	}
	t.Fatalf("report has no host %q: %+v", name, report.Hosts)
	return HostReport{}
}

func TestSyncInstallsIntoEveryDiscoveredHost(t *testing.T) {
	hosts := testAllHosts(t)
	report, err := SyncHosts(hosts, bin, Options{})
	if err != nil {
		t.Fatal(err)
	}
	for _, host := range hosts {
		if len(hostReport(t, report, host.Name).Written) != len(assets.SkillNames()) {
			t.Errorf("%s did not report every skill as written", host.Name)
		}
		for _, name := range assets.SkillNames() {
			if _, err := os.Stat(filepath.Join(host.SkillsDir, name, "SKILL.md")); err != nil {
				t.Errorf("%s skill %s not installed: %v", host.Name, name, err)
			}
		}
	}
}

func TestSyncBacksUpADifferingCopyPerHost(t *testing.T) {
	hosts := testAllHosts(t)
	name := assets.SkillNames()[0]
	for _, host := range hosts {
		target := filepath.Join(host.SkillsDir, name)
		if err := os.MkdirAll(target, 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(target, "SKILL.md"), []byte("local edit\n"), 0o644); err != nil {
			t.Fatal(err)
		}
	}

	report, err := SyncHosts(hosts, bin, Options{})
	if err != nil {
		t.Fatal(err)
	}
	var backup string
	for _, host := range hosts {
		current, ok := hostReport(t, report, host.Name).BackedUp[name]
		if !ok {
			t.Fatalf("%s has no central backup: %+v", host.Name, hostReport(t, report, host.Name))
		}
		if backup == "" {
			backup = current
		} else if current != backup {
			t.Fatalf("backup stores differ: %q and %q", backup, current)
		}
	}
	var manifest backupManifest
	data, err := os.ReadFile(filepath.Join(backup, "manifest.json"))
	if err != nil || json.Unmarshal(data, &manifest) != nil {
		t.Fatalf("backup manifest: %v", err)
	}
	if manifest.FileCount != len(hosts) {
		t.Fatalf("manifest file count = %d, want %d", manifest.FileCount, len(hosts))
	}
}

func TestSyncSkipsIdenticalCopiesPerHost(t *testing.T) {
	hosts := testAllHosts(t)
	if _, err := SyncHosts(hosts, bin, Options{}); err != nil {
		t.Fatal(err)
	}
	report, err := SyncHosts(hosts, bin, Options{})
	if err != nil {
		t.Fatal(err)
	}
	for _, host := range hosts {
		got := hostReport(t, report, host.Name)
		if len(got.Written) != 0 || len(got.Unchanged) != len(assets.SkillNames()) {
			t.Errorf("%s second run wrote %v, unchanged %d", host.Name, got.Written, len(got.Unchanged))
		}
	}
}

func TestSyncWritesNoHookOutsideClaude(t *testing.T) {
	hosts := testAllHosts(t)
	before := map[string][]byte{}
	for _, host := range hosts {
		if host.Name == "claude" {
			continue
		}
		path := filepath.Join(host.ConfigDir, "settings.json")
		before[host.Name] = []byte(`{"hooks":{"Stop":[{"hooks":[{"command":"keep me"}]}]}}`)
		if err := os.WriteFile(path, before[host.Name], 0o644); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := SyncHosts(hosts, bin, Options{}); err != nil {
		t.Fatal(err)
	}
	for _, host := range hosts {
		path := filepath.Join(host.ConfigDir, "settings.json")
		got, err := os.ReadFile(path)
		if host.Name == "claude" {
			if err != nil {
				t.Fatalf("Claude settings.json: %v", err)
			}
		} else if err != nil || string(got) != string(before[host.Name]) {
			t.Fatalf("%s settings.json changed: %v %q", host.Name, err, got)
		}
	}
}

func backupDirs(t *testing.T, root string) []string {
	t.Helper()
	entries, err := os.ReadDir(filepath.Join(root, "backups"))
	if os.IsNotExist(err) {
		return nil
	}
	if err != nil {
		t.Fatal(err)
	}
	var dirs []string
	for _, entry := range entries {
		if entry.IsDir() {
			dirs = append(dirs, entry.Name())
		}
	}
	return dirs
}

func TestSyncTwiceIsByteIdentical(t *testing.T) {
	root := t.TempDir()
	t.Setenv("TPP_HOME", root)
	cfg := t.TempDir()
	if _, err := Sync(cfg, bin, Options{}); err != nil {
		t.Fatal(err)
	}
	stateBefore, err := os.ReadFile(filepath.Join(root, "state.json"))
	if err != nil {
		t.Fatal(err)
	}
	settingsBefore, err := os.ReadFile(filepath.Join(cfg, "settings.json"))
	if err != nil {
		t.Fatal(err)
	}
	backupsBefore := backupDirs(t, root)
	report, err := Sync(cfg, bin, Options{})
	if err != nil {
		t.Fatal(err)
	}
	stateAfter, _ := os.ReadFile(filepath.Join(root, "state.json"))
	settingsAfter, _ := os.ReadFile(filepath.Join(cfg, "settings.json"))
	if string(stateBefore) != string(stateAfter) || string(settingsBefore) != string(settingsAfter) {
		t.Fatal("second sync changed state.json or settings.json bytes")
	}
	if report.StateWritten {
		t.Fatal("second sync unexpectedly wrote state")
	}
	backupsAfter := backupDirs(t, root)
	if strings.Join(backupsBefore, "\n") != strings.Join(backupsAfter, "\n") {
		t.Fatalf("backup directories changed: before=%v after=%v", backupsBefore, backupsAfter)
	}
}

func TestSyncRefusesAModifiedFileWithoutForce(t *testing.T) {
	root := t.TempDir()
	t.Setenv("TPP_HOME", root)
	cfg := t.TempDir()
	if _, err := Sync(cfg, bin, Options{}); err != nil {
		t.Fatal(err)
	}
	name := assets.SkillNames()[0]
	path := filepath.Join(cfg, "skills", name, "SKILL.md")
	if err := os.WriteFile(path, []byte("user edit\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	withoutForce, err := Sync(cfg, bin, Options{})
	if err != nil {
		t.Fatal(err)
	}
	got, _ := os.ReadFile(path)
	if string(got) != "user edit\n" {
		t.Fatal("sync without --force replaced the user edit")
	}
	if !contains(withoutForce.Modified, path) || !strings.Contains(withoutForce.String(), "warning: skipped") {
		t.Fatalf("modified file was not reported honestly: %+v\n%s", withoutForce.Modified, withoutForce.String())
	}
	if dirs := backupDirs(t, root); len(dirs) != 0 {
		t.Fatalf("refusal created backups: %v", dirs)
	}
	withForce, err := Sync(cfg, bin, Options{Force: true})
	if err != nil {
		t.Fatal(err)
	}
	got, _ = os.ReadFile(path)
	if string(got) == "user edit\n" {
		t.Fatal("--force did not replace the user edit")
	}
	backup := withForce.BackedUp[name]
	if backup == "" {
		t.Fatal("--force did not report a central backup")
	}
	var manifest backupManifest
	data, _ := os.ReadFile(filepath.Join(backup, "manifest.json"))
	if err := json.Unmarshal(data, &manifest); err != nil {
		t.Fatal(err)
	}
	if len(manifest.Entries) != 1 {
		t.Fatalf("manifest entries = %d, want 1", len(manifest.Entries))
	}
	snapshot, err := os.ReadFile(filepath.Join(backup, filepath.FromSlash(manifest.Entries[0].SnapshotPath)))
	if err != nil || string(snapshot) != "user edit\n" {
		t.Fatalf("snapshot = %q, err=%v", snapshot, err)
	}
}

func TestSyncLeavesForeignSkillsAlone(t *testing.T) {
	root := t.TempDir()
	t.Setenv("TPP_HOME", root)
	cfg := t.TempDir()
	foreign := filepath.Join(cfg, "skills", "unrelated", "README.md")
	if err := os.MkdirAll(filepath.Dir(foreign), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(foreign, []byte("keep me\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	report, err := Sync(cfg, bin, Options{})
	if err != nil {
		t.Fatal(err)
	}
	got, err := os.ReadFile(foreign)
	if err != nil || string(got) != "keep me\n" {
		t.Fatalf("foreign skill changed: %v %q", err, got)
	}
	if !contains(report.Foreign, foreign) {
		t.Fatalf("foreign path was not reported: %v", report.Foreign)
	}
}

func TestSyncWritesSettingsOnlyWhenTheyDiffer(t *testing.T) {
	root := t.TempDir()
	t.Setenv("TPP_HOME", root)
	cfg := t.TempDir()
	if _, err := Sync(cfg, bin, Options{}); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(cfg, "settings.json")
	before, _ := os.ReadFile(path)
	infoBefore, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := Sync(cfg, bin, Options{}); err != nil {
		t.Fatal(err)
	}
	after, _ := os.ReadFile(path)
	infoAfter, _ := os.Stat(path)
	if string(before) != string(after) || !infoBefore.ModTime().Equal(infoAfter.ModTime()) {
		t.Fatal("second sync rewrote settings.json")
	}
}

func TestSyncCreatesNoStateOnADryRun(t *testing.T) {
	root := t.TempDir()
	t.Setenv("TPP_HOME", root)
	cfg := t.TempDir()
	report, err := Sync(cfg, bin, Options{DryRun: true})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(report.String(), "[create]") {
		t.Fatalf("dry-run did not print plan classes:\n%s", report.String())
	}
	for _, path := range []string{
		filepath.Join(root, "state.json"),
		filepath.Join(cfg, "settings.json"),
		filepath.Join(cfg, "skills"),
		filepath.Join(root, "backups"),
	} {
		if _, err := os.Stat(path); err == nil {
			t.Fatalf("dry-run created %s", path)
		}
	}
}

func contains(values []string, want string) bool {
	return containsString(values, want)
}

// A host with thousands of user files must not print thousands of rows: the report lists what
// changes and counts what does not, whether it plans or writes.
func TestReportListsChangesAndCountsTheRest(t *testing.T) {
	host := HostReport{
		Host:   Host{Name: "claude", ConfigDir: "/cfg"},
		Counts: map[ActionClass]int{ActionUpdate: 1, ActionOK: 2, ActionForeign: 3},
		Actions: []Action{
			{Class: ActionOK, Path: "/cfg/skills/a/SKILL.md", Reason: "matches"},
			{Class: ActionOK, Path: "/cfg/skills/b/SKILL.md", Reason: "matches"},
			{Class: ActionUpdate, Path: "/cfg/skills/c/SKILL.md", Reason: "differs"},
			{Class: ActionForeign, Path: "/cfg/skills/mine/x.md", Reason: "user file"},
			{Class: ActionForeign, Path: "/cfg/skills/mine/y.md", Reason: "user file"},
			{Class: ActionForeign, Path: "/cfg/skills/mine/z.md", Reason: "user file"},
		},
		Unchanged: []string{"a", "b"},
		Written:   []string{"c"},
		Foreign:   []string{"/cfg/skills/mine/x.md", "/cfg/skills/mine/y.md", "/cfg/skills/mine/z.md"},
	}
	for _, dryRun := range []bool{true, false} {
		out := Report{DryRun: dryRun, Hosts: []HostReport{host}}.String()
		for _, hidden := range []string{"/cfg/skills/a/SKILL.md", "/cfg/skills/mine/"} {
			if strings.Contains(out, hidden) {
				t.Errorf("dry-run=%v report lists %s row by row:\n%s", dryRun, hidden, out)
			}
		}
		if !strings.Contains(out, "skip-user: 3 files not managed by tpp (left untouched)") {
			t.Errorf("dry-run=%v report does not count the user files:\n%s", dryRun, out)
		}
	}
	if out := (Report{DryRun: true, Hosts: []HostReport{host}}).String(); !strings.Contains(out, "[update] /cfg/skills/c/SKILL.md: differs") {
		t.Errorf("dry-run report hides the update:\n%s", out)
	}
	out := Report{Hosts: []HostReport{host}}.String()
	if !strings.Contains(out, "c ") || !strings.Contains(out, "written") || !strings.Contains(out, "2 skills unchanged") {
		t.Errorf("write report does not list the written skill and count the unchanged ones:\n%s", out)
	}
}

// An explicitly supplied pi host receives the extension and nothing else: no skill copies, no
// hook settings, and a state record that names the installed bytes.
func TestSyncPiHostReceivesOnlyTheExtension(t *testing.T) {
	t.Setenv("TPP_HOME", t.TempDir())
	home := t.TempDir()
	host := Host{Name: "pi", ConfigDir: filepath.Join(home, ".pi"), SkillsDir: filepath.Join(home, ".pi", "agent", "extensions")}
	report, err := SyncHosts([]Host{host}, bin, Options{})
	if err != nil {
		t.Fatal(err)
	}

	entry := filepath.Join(host.SkillsDir, "tpp", "index.ts")
	got, err := os.ReadFile(entry)
	if err != nil {
		t.Fatalf("pi extension not installed: %v", err)
	}
	want, err := fs.ReadFile(root.Root, "hosts/pi/tsp.ts")
	if err != nil {
		t.Fatalf("embedded hosts/pi/tsp.ts: %v", err)
	}
	if !bytes.Equal(got, want) {
		t.Fatalf("installed extension is %d bytes, want the embedded tsp.ts (%d bytes)", len(got), len(want))
	}

	var unwanted []string
	if err := filepath.WalkDir(home, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if !d.IsDir() && (d.Name() == "SKILL.md" || d.Name() == "settings.json" || d.Name() == "settings.stop-hook.json") {
			unwanted = append(unwanted, path)
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	if len(unwanted) != 0 {
		t.Fatalf("pi host received files the manifest never ships for it: %v", unwanted)
	}

	gotReport := hostReport(t, report, "pi")
	if !contains(gotReport.Written, "tpp") {
		t.Fatalf("pi report wrote %v, want the extension component", gotReport.Written)
	}
	if gotReport.SettingsRead || gotReport.SettingsChanged || len(gotReport.RemovedHooks) != 0 {
		t.Fatalf("pi host must not touch hook settings, got %+v", gotReport)
	}

	installationState, err := state.Load()
	if err != nil {
		t.Fatal(err)
	}
	hostState := installationState.Hosts["pi"]
	record, recorded := hostState.Assets[entry]
	digest := sha256.Sum256(want)
	if !recorded || record.SHA256 != hex.EncodeToString(digest[:]) {
		t.Fatalf("state record for the extension = %+v, want sha256:%s", record, hex.EncodeToString(digest[:]))
	}
	if len(hostState.Components) != 1 || hostState.Components[0] != "tpp" {
		t.Fatalf("state components for pi = %v, want exactly [tpp]", hostState.Components)
	}
}

// The shared engine's guarantees come from the same code path: dry-run writes nothing, a second
// run is a no-op, and a user-edited extension is refused until --force snapshots it.
func TestSyncPiHostDryRunIdempotenceAndBackup(t *testing.T) {
	stateRoot := t.TempDir()
	t.Setenv("TPP_HOME", stateRoot)
	home := t.TempDir()
	host := Host{Name: "pi", ConfigDir: filepath.Join(home, ".pi"), SkillsDir: filepath.Join(home, ".pi", "agent", "extensions")}
	entry := filepath.Join(host.SkillsDir, "tpp", "index.ts")

	dry, err := SyncHosts([]Host{host}, bin, Options{DryRun: true})
	if err != nil {
		t.Fatal(err)
	}
	if !errors.Is(mustStat(t, entry), os.ErrNotExist) {
		t.Fatal("dry-run wrote the pi extension")
	}
	if got := hostReport(t, dry, "pi").Counts[ActionCreate]; got != 1 {
		t.Fatalf("dry-run planned %d creates, want exactly the extension", got)
	}

	if _, err := SyncHosts([]Host{host}, bin, Options{}); err != nil {
		t.Fatal(err)
	}
	second, err := SyncHosts([]Host{host}, bin, Options{})
	if err != nil {
		t.Fatal(err)
	}
	if got := hostReport(t, second, "pi"); len(got.Written) != 0 || !contains(got.Unchanged, "tpp") {
		t.Fatalf("second run wrote %v, unchanged %v", got.Written, got.Unchanged)
	}

	user := []byte("user edit\n")
	if err := os.WriteFile(entry, user, 0o644); err != nil {
		t.Fatal(err)
	}
	refused, err := SyncHosts([]Host{host}, bin, Options{})
	if err != nil {
		t.Fatal(err)
	}
	if got, _ := os.ReadFile(entry); !bytes.Equal(got, user) {
		t.Fatal("sync without --force replaced the user edit")
	}
	if got := hostReport(t, refused, "pi"); !contains(got.Modified, entry) {
		t.Fatalf("modified extension not reported: %+v", got.Modified)
	}
	if dirs := backupDirs(t, stateRoot); len(dirs) != 0 {
		t.Fatalf("refusal created backups: %v", dirs)
	}

	forced, err := SyncHosts([]Host{host}, bin, Options{Force: true})
	if err != nil {
		t.Fatal(err)
	}
	want, err := fs.ReadFile(root.Root, "hosts/pi/tsp.ts")
	if err != nil {
		t.Fatal(err)
	}
	if got, _ := os.ReadFile(entry); !bytes.Equal(got, want) {
		t.Fatal("--force did not restore the embedded extension bytes")
	}
	if hostReport(t, forced, "pi").BackedUp["tpp"] == "" {
		t.Fatal("--force did not report a central backup for the extension")
	}
}

func mustStat(t *testing.T, path string) error {
	t.Helper()
	_, err := os.Stat(path)
	return err
}

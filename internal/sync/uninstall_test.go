package sync

import (
	"bytes"
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/alesierraalta/tsp/internal/assets"
	"github.com/alesierraalta/tsp/internal/feature"
	"github.com/alesierraalta/tsp/internal/state"
)

// seededInstall puts a real installation on disk: skills, state.json, and the wired Stop hook.
func seededInstall(t *testing.T) (root, cfg string) {
	t.Helper()
	root = t.TempDir()
	t.Setenv("TPP_HOME", root)
	cfg = t.TempDir()
	if _, err := Sync(cfg, bin, Options{}); err != nil {
		t.Fatal(err)
	}
	return root, cfg
}

// A plain uninstall takes the installation back off the machine: every recorded asset it still
// matches is removed, the gate it wired leaves settings.json while foreign hooks stay, foreign
// files are untouched, the emptied host leaves state, and Features survive a reinstall.
func TestUninstallRemovesManagedAssetsAndTheHook(t *testing.T) {
	_, cfg := seededInstall(t)

	foreign := filepath.Join(cfg, "skills", "unrelated", "README.md")
	if err := os.MkdirAll(filepath.Dir(foreign), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(foreign, []byte("keep me\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	settingsPath := filepath.Join(cfg, "settings.json")
	raw, err := os.ReadFile(settingsPath)
	if err != nil {
		t.Fatal(err)
	}
	var settings map[string]any
	if err := json.Unmarshal(raw, &settings); err != nil {
		t.Fatal(err)
	}
	hooks := settings["hooks"].(map[string]any)
	stop := hooks["Stop"].([]any)
	stop = append(stop, map[string]any{"matcher": "", "hooks": []any{map[string]any{
		"type": "command", "command": "other review hook", "timeout": 60,
	}}})
	hooks["Stop"] = stop
	edited, err := json.Marshal(settings)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(settingsPath, edited, 0o644); err != nil {
		t.Fatal(err)
	}

	if _, err := feature.Set("feedback", true); err != nil {
		t.Fatal(err)
	}

	before, err := state.Load()
	if err != nil {
		t.Fatal(err)
	}
	wantRemoved := 0
	for _, hostState := range before.Hosts {
		wantRemoved += len(hostState.Assets)
	}

	report, err := Uninstall(UninstallOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if len(report.Removed) != wantRemoved {
		t.Fatalf("removed %d paths, want every recorded asset (%d): %v", len(report.Removed), wantRemoved, report.Removed)
	}
	if len(report.BackedUp) != 0 {
		t.Fatalf("intact assets must not be backed up: %v", report.BackedUp)
	}
	for _, name := range assets.SkillNames() {
		if _, err := os.Stat(filepath.Join(cfg, "skills", name, "SKILL.md")); !os.IsNotExist(err) {
			t.Errorf("skill %s still installed: %v", name, err)
		}
	}
	if got, err := os.ReadFile(foreign); err != nil || string(got) != "keep me\n" {
		t.Errorf("foreign file changed: %v %q", err, got)
	}
	if !report.SettingsChanged {
		t.Error("settings should have dropped the gate")
	}
	if !contains(report.RemovedHooks, HookCommand(bin)) {
		t.Errorf("removed hooks = %v, want %q", report.RemovedHooks, HookCommand(bin))
	}
	cmds := stopCommands(t, readSettings(t, cfg))
	if len(cmds) != 1 || cmds[0] != "other review hook" {
		t.Errorf("stop hooks = %q, want only the foreign hook kept", cmds)
	}
	after, err := state.Load()
	if err != nil {
		t.Fatal(err)
	}
	if len(after.Hosts) != 0 {
		t.Errorf("hosts left in state: %+v", after.Hosts)
	}
	if !report.StateWritten {
		t.Error("state should have been written")
	}
	enabled, err := feature.Enabled("feedback")
	if err != nil {
		t.Fatal(err)
	}
	if !enabled {
		t.Error("feature opt-in must survive uninstall")
	}
}

// Modified content is the operator's, not ours: without --force the whole run refuses before
// touching anything and the refusal names the flag; with --force the file is snapshotted to the
// central backup store, then removed.
func TestUninstallRefusesModifiedAssetsWithoutForce(t *testing.T) {
	root, cfg := seededInstall(t)
	path := filepath.Join(cfg, "skills", assets.SkillNames()[0], "SKILL.md")
	if err := os.WriteFile(path, []byte("user edit\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	report, err := Uninstall(UninstallOptions{})
	if err == nil || !strings.Contains(err.Error(), "--force") {
		t.Fatalf("err = %v, want a refusal naming --force", err)
	}
	sawModified := false
	for _, action := range report.Actions {
		if action.Class == UninstallModified && action.Path == path {
			sawModified = true
		}
	}
	if !sawModified {
		t.Fatalf("modified path not classified: %+v", report.Actions)
	}
	for _, name := range assets.SkillNames() {
		skill := filepath.Join(cfg, "skills", name, "SKILL.md")
		if _, statErr := os.Stat(skill); statErr != nil {
			t.Fatalf("refusal must be atomic; %s gone: %v", name, statErr)
		}
	}
	cmds := stopCommands(t, readSettings(t, cfg))
	if !contains(cmds, HookCommand(bin)) {
		t.Fatalf("refusal unwired the hook: %q", cmds)
	}
	current, loadErr := state.Load()
	if loadErr != nil {
		t.Fatal(loadErr)
	}
	if len(current.Hosts) == 0 {
		t.Fatal("refusal cleared state")
	}
	if dirs := backupDirs(t, root); len(dirs) != 0 {
		t.Fatalf("refusal created backups: %v", dirs)
	}

	forced, err := Uninstall(UninstallOptions{Force: true})
	if err != nil {
		t.Fatalf("force: %v", err)
	}
	if _, statErr := os.Stat(path); !os.IsNotExist(statErr) {
		t.Errorf("--force left the modified file: %v", statErr)
	}
	backup, ok := forced.BackedUp[path]
	if !ok {
		t.Fatal("--force did not report a central backup")
	}
	var snapshotManifest backupManifest
	data, _ := os.ReadFile(filepath.Join(backup, "manifest.json"))
	if err := json.Unmarshal(data, &snapshotManifest); err != nil {
		t.Fatal(err)
	}
	if len(snapshotManifest.Entries) != 1 {
		t.Fatalf("manifest entries = %d, want 1", len(snapshotManifest.Entries))
	}
	snapshot, err := os.ReadFile(filepath.Join(backup, filepath.FromSlash(snapshotManifest.Entries[0].SnapshotPath)))
	if err != nil || string(snapshot) != "user edit\n" {
		t.Fatalf("snapshot = %q, err=%v", snapshot, err)
	}
}

// An orphan is still ours (state recorded it) but is no longer shipped: a plain uninstall keeps
// the file and its record so --orphans can still find it; with the flag it goes.
func TestUninstallRemovesOrphansOnlyWithTheFlag(t *testing.T) {
	_, cfg := seededInstall(t)
	orphan := filepath.Join(cfg, "skills", "former-skill", "SKILL.md")
	if err := os.MkdirAll(filepath.Dir(orphan), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(orphan, []byte("old skill\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	current, err := state.Load()
	if err != nil {
		t.Fatal(err)
	}
	hostState := current.Hosts["claude"]
	if hostState.Assets == nil {
		t.Fatal("seeded install recorded no assets")
	}
	hostState.Assets[orphan] = state.AssetRecord{
		SHA256:      fmt.Sprintf("%x", sha256.Sum256([]byte("old skill\n"))),
		Mode:        0o644,
		FromVersion: "test",
	}
	current.Hosts["claude"] = hostState
	if _, err := current.Save(); err != nil {
		t.Fatal(err)
	}

	plain, err := Uninstall(UninstallOptions{})
	if err != nil {
		t.Fatal(err)
	}
	sawOrphan := false
	for _, action := range plain.Actions {
		if action.Class == UninstallOrphan && action.Path == orphan {
			sawOrphan = true
		}
	}
	if !sawOrphan {
		t.Fatalf("orphan not classified: %+v", plain.Actions)
	}
	if _, err := os.Stat(orphan); err != nil {
		t.Fatalf("plain uninstall removed the orphan: %v", err)
	}
	afterPlain, err := state.Load()
	if err != nil {
		t.Fatal(err)
	}
	if _, recorded := afterPlain.Hosts["claude"].Assets[orphan]; !recorded {
		t.Fatal("plain uninstall dropped the orphan record")
	}

	withFlag, err := Uninstall(UninstallOptions{Orphans: true})
	if err != nil {
		t.Fatal(err)
	}
	if !contains(withFlag.Removed, orphan) {
		t.Errorf("--orphans did not report the orphan as removed: %v", withFlag.Removed)
	}
	if _, err := os.Stat(orphan); !os.IsNotExist(err) {
		t.Errorf("--orphans left the orphan file: %v", err)
	}
	after, err := state.Load()
	if err != nil {
		t.Fatal(err)
	}
	if _, recorded := after.Hosts["claude"].Assets[orphan]; recorded {
		t.Error("--orphans left the orphan record")
	}
}

// Dry run classifies the whole plan and writes nothing: not a skill file, not settings.json,
// not state.json, not a backup.
func TestUninstallDryRunClassifiesAndWritesNothing(t *testing.T) {
	root, cfg := seededInstall(t)
	statePath := filepath.Join(root, "state.json")
	settingsPath := filepath.Join(cfg, "settings.json")
	stateBefore, err := os.ReadFile(statePath)
	if err != nil {
		t.Fatal(err)
	}
	settingsBefore, err := os.ReadFile(settingsPath)
	if err != nil {
		t.Fatal(err)
	}

	report, err := Uninstall(UninstallOptions{DryRun: true})
	if err != nil {
		t.Fatal(err)
	}
	counts := map[UninstallClass]int{}
	for _, action := range report.Actions {
		counts[action.Class]++
	}
	if counts[UninstallRemove] == 0 {
		t.Error("dry run listed no removals")
	}
	if counts[UninstallHook] != 1 {
		t.Errorf("hook actions = %d, want 1", counts[UninstallHook])
	}
	if !strings.Contains(report.String(), "[dry-run] [remove]") {
		t.Errorf("plan output missing classified lines:\n%s", report.String())
	}
	if len(report.Removed) != 0 {
		t.Errorf("dry run reported removals: %v", report.Removed)
	}

	stateAfter, _ := os.ReadFile(statePath)
	settingsAfter, _ := os.ReadFile(settingsPath)
	if !bytes.Equal(stateBefore, stateAfter) {
		t.Error("dry run changed state.json")
	}
	if !bytes.Equal(settingsBefore, settingsAfter) {
		t.Error("dry run changed settings.json")
	}
	for _, name := range assets.SkillNames() {
		if _, err := os.Stat(filepath.Join(cfg, "skills", name, "SKILL.md")); err != nil {
			t.Errorf("dry run removed %s: %v", name, err)
		}
	}
	if dirs := backupDirs(t, root); len(dirs) != 0 {
		t.Errorf("dry run created backups: %v", dirs)
	}
}

// The Pi extension is recorded outside ConfigDir/skills (under agent/extensions), so uninstall
// must rebuild the host from state instead of assuming a skills tree: a plain run classifies it
// as the managed asset it is — not an orphan — removes it without --orphans, and drops the
// record; the dry-run plans the same classification and writes nothing.
func TestUninstallTreatsThePiExtensionAsAManagedAsset(t *testing.T) {
	root := t.TempDir()
	t.Setenv("TPP_HOME", root)
	home := t.TempDir()
	host := PiHost(home)
	if _, err := SyncHosts([]Host{host}, bin, Options{}); err != nil {
		t.Fatal(err)
	}
	entry := filepath.Join(host.SkillsDir, "tpp", "index.ts")

	dry, err := Uninstall(UninstallOptions{DryRun: true})
	if err != nil {
		t.Fatal(err)
	}
	sawRemove := false
	for _, action := range dry.Actions {
		if action.Path != entry {
			continue
		}
		if action.Class != UninstallRemove {
			t.Fatalf("dry run classifies the extension as %q, want %q (not an orphan): %+v", action.Class, UninstallRemove, action)
		}
		sawRemove = true
	}
	if !sawRemove {
		t.Fatalf("dry run never classified %s: %+v", entry, dry.Actions)
	}
	if len(dry.Removed) != 0 {
		t.Fatalf("dry run reported removals: %v", dry.Removed)
	}
	if _, err := os.Stat(entry); err != nil {
		t.Fatalf("dry run touched the extension: %v", err)
	}

	report, err := Uninstall(UninstallOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if !contains(report.Removed, entry) {
		t.Fatalf("plain uninstall did not remove the extension: %v", report.Removed)
	}
	if len(report.BackedUp) != 0 {
		t.Fatalf("intact extension must not be backed up: %v", report.BackedUp)
	}
	if _, err := os.Stat(entry); !os.IsNotExist(err) {
		t.Fatalf("extension still on disk: %v", err)
	}
	if dirs := backupDirs(t, root); len(dirs) != 0 {
		t.Fatalf("plain uninstall created backups: %v", dirs)
	}
	after, err := state.Load()
	if err != nil {
		t.Fatal(err)
	}
	if hostState, recorded := after.Hosts["pi"]; recorded {
		t.Fatalf("pi record left in state: %+v", hostState)
	}
}

// A user-edited extension is the operator's: without --force the whole uninstall refuses before
// deleting anything and the refusal names the flag; with --force the edit is snapshotted to the
// central backup store, then removed, and the record leaves state with the file.
func TestUninstallModifiedPiExtensionRefusesUntilForcedWithBackup(t *testing.T) {
	root := t.TempDir()
	t.Setenv("TPP_HOME", root)
	home := t.TempDir()
	host := PiHost(home)
	if _, err := SyncHosts([]Host{host}, bin, Options{}); err != nil {
		t.Fatal(err)
	}
	entry := filepath.Join(host.SkillsDir, "tpp", "index.ts")
	if err := os.WriteFile(entry, []byte("user edit\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	refused, err := Uninstall(UninstallOptions{})
	if err == nil || !strings.Contains(err.Error(), "--force") {
		t.Fatalf("err = %v, want a refusal naming --force", err)
	}
	sawModified := false
	for _, action := range refused.Actions {
		if action.Class == UninstallModified && action.Path == entry {
			sawModified = true
		}
	}
	if !sawModified {
		t.Fatalf("modified extension not classified: %+v", refused.Actions)
	}
	if _, err := os.Stat(entry); err != nil {
		t.Fatalf("refusal must be atomic; extension gone: %v", err)
	}
	if dirs := backupDirs(t, root); len(dirs) != 0 {
		t.Fatalf("refusal created backups: %v", dirs)
	}
	current, err := state.Load()
	if err != nil {
		t.Fatal(err)
	}
	if _, recorded := current.Hosts["pi"].Assets[entry]; !recorded {
		t.Fatal("refusal cleared the extension record")
	}

	forced, err := Uninstall(UninstallOptions{Force: true})
	if err != nil {
		t.Fatalf("force: %v", err)
	}
	if _, err := os.Stat(entry); !os.IsNotExist(err) {
		t.Fatalf("--force left the modified extension: %v", err)
	}
	backup, ok := forced.BackedUp[entry]
	if !ok {
		t.Fatalf("--force did not report a central backup: %+v", forced.BackedUp)
	}
	var snapshotManifest backupManifest
	data, _ := os.ReadFile(filepath.Join(backup, "manifest.json"))
	if err := json.Unmarshal(data, &snapshotManifest); err != nil {
		t.Fatal(err)
	}
	if len(snapshotManifest.Entries) != 1 {
		t.Fatalf("manifest entries = %d, want 1", len(snapshotManifest.Entries))
	}
	snapshot, err := os.ReadFile(filepath.Join(backup, filepath.FromSlash(snapshotManifest.Entries[0].SnapshotPath)))
	if err != nil || string(snapshot) != "user edit\n" {
		t.Fatalf("snapshot = %q, err=%v, want the user's edit", snapshot, err)
	}
	after, err := state.Load()
	if err != nil {
		t.Fatal(err)
	}
	if hostState, recorded := after.Hosts["pi"]; recorded {
		t.Fatalf("forced uninstall left the pi record: %+v", hostState)
	}
}

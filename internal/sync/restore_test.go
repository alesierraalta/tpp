package sync

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// backupFixture writes content at target with mode under the current TPP_HOME, snapshots it
// into a fresh backup store entry, and returns the backup id.
func backupFixture(t *testing.T, target, content string, mode os.FileMode) string {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(target), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(target, []byte(content), mode); err != nil {
		t.Fatal(err)
	}
	// WriteFile applies mode only on creation, and umask masks it then: chmod makes the snapshot
	// record the exact mode the assertion below expects.
	if err := os.Chmod(target, mode); err != nil {
		t.Fatal(err)
	}
	store, err := newBackupStore()
	if err != nil {
		t.Fatal(err)
	}
	if err := store.Snapshot(target); err != nil {
		t.Fatal(err)
	}
	return filepath.Base(store.Dir())
}

// writeManifestFixture plants a hand-written backup entry so a test can drive paths the real
// Snapshot writer would never record.
func writeManifestFixture(t *testing.T, root, id string, entries []backupEntry) string {
	t.Helper()
	dir := filepath.Join(root, "backups", id)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	payload, err := json.Marshal(backupManifest{
		ID: id, CreatedAt: "2099-01-01T00:00:00Z", RootDir: root, Entries: entries,
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "manifest.json"), payload, 0o600); err != nil {
		t.Fatal(err)
	}
	return dir
}

// The listing answers newest first, and an empty id restores the latest backup: exact bytes and
// the mode the manifest recorded come back over whatever now sits at the original path.
func TestListBackupsAnswersNewestFirstAndRestoreUsesTheLatest(t *testing.T) {
	root := t.TempDir()
	t.Setenv("TPP_HOME", root)
	target := filepath.Join(t.TempDir(), "note.txt")
	first := backupFixture(t, target, "first edition\n", 0o644)
	second := backupFixture(t, target, "second edition\n", 0o600)

	backups, err := ListBackups()
	if err != nil {
		t.Fatal(err)
	}
	if len(backups) != 2 {
		t.Fatalf("ListBackups = %d entries, want 2: %+v", len(backups), backups)
	}
	if backups[0].ID != second || backups[1].ID != first {
		t.Fatalf("order = [%s %s], want newest first [%s %s]", backups[0].ID, backups[1].ID, second, first)
	}
	if backups[0].FileCount != 1 {
		t.Errorf("newest backup FileCount = %d, want 1", backups[0].FileCount)
	}

	if err := os.WriteFile(target, []byte("clobbered\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(target, 0o644); err != nil {
		t.Fatal(err)
	}

	report, err := Restore("", false)
	if err != nil {
		t.Fatal(err)
	}
	if report.ID != second {
		t.Errorf("restored id = %q, want the latest (%q)", report.ID, second)
	}
	got, err := os.ReadFile(target)
	if err != nil || string(got) != "second edition\n" {
		t.Fatalf("restored bytes = %q, err=%v, want the latest snapshot", got, err)
	}
	info, err := os.Stat(target)
	if err != nil {
		t.Fatal(err)
	}
	if info.Mode().Perm() != 0o600 {
		t.Errorf("restored mode = %v, want the manifest mode 0600", info.Mode().Perm())
	}
	if len(report.Restored) != 1 || report.Restored[0] != target {
		t.Errorf("Restored = %v, want [%s]", report.Restored, target)
	}
	if want := "restored " + target; !strings.Contains(report.String(), want) {
		t.Errorf("report output missing %q:\n%s", want, report.String())
	}
}

// An explicit id selects that backup even when a newer one exists.
func TestRestoreSelectsTheNamedBackup(t *testing.T) {
	root := t.TempDir()
	t.Setenv("TPP_HOME", root)
	target := filepath.Join(t.TempDir(), "note.txt")
	first := backupFixture(t, target, "first edition\n", 0o644)
	backupFixture(t, target, "second edition\n", 0o644)
	if err := os.WriteFile(target, []byte("clobbered\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	report, err := Restore(first, false)
	if err != nil {
		t.Fatal(err)
	}
	if report.ID != first {
		t.Errorf("restored id = %q, want %q", report.ID, first)
	}
	got, err := os.ReadFile(target)
	if err != nil || string(got) != "first edition\n" {
		t.Fatalf("restored bytes = %q, err=%v, want the named snapshot", got, err)
	}
}

// An id the store does not hold is refused with the ids that exist, so the operator can retry
// without going to the filesystem.
func TestRestoreUnknownIDNamesTheAvailableIDs(t *testing.T) {
	root := t.TempDir()
	t.Setenv("TPP_HOME", root)
	target := filepath.Join(t.TempDir(), "note.txt")
	first := backupFixture(t, target, "first edition\n", 0o644)
	second := backupFixture(t, target, "second edition\n", 0o644)

	_, err := Restore("no-such-backup", false)
	if err == nil {
		t.Fatal("unknown backup id was accepted")
	}
	for _, want := range []string{`unknown backup id "no-such-backup"`, first, second} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("error missing %q: %v", want, err)
		}
	}
}

// Dry run plans the whole restore and proves the backup is readable while leaving the
// destination byte-identical, and the plan line names both ends and the mode.
func TestRestoreDryRunPlansWithoutTouchingTheDestination(t *testing.T) {
	root := t.TempDir()
	t.Setenv("TPP_HOME", root)
	target := filepath.Join(t.TempDir(), "note.txt")
	id := backupFixture(t, target, "backup content\n", 0o600)
	if err := os.WriteFile(target, []byte("current content\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(target, 0o644); err != nil {
		t.Fatal(err)
	}

	report, err := Restore("", true)
	if err != nil {
		t.Fatal(err)
	}
	if len(report.Actions) != 1 {
		t.Fatalf("planned %d actions, want 1", len(report.Actions))
	}
	if report.Actions[0].Mode != 0o600 {
		t.Errorf("planned mode = %d, want the manifest mode 0600", report.Actions[0].Mode)
	}
	if len(report.Restored) != 0 {
		t.Errorf("dry run reported restores: %v", report.Restored)
	}
	snapshot := filepath.Join(root, "backups", id, filepath.FromSlash(snapshotRelativePath(target)))
	for _, want := range []string{"[dry-run] [restore] ", target, " <- ", snapshot, "(mode 0600)"} {
		if !strings.Contains(report.String(), want) {
			t.Errorf("plan output missing %q:\n%s", want, report.String())
		}
	}

	got, err := os.ReadFile(target)
	if err != nil || string(got) != "current content\n" {
		t.Fatalf("dry run changed the destination: %q, err=%v", got, err)
	}
	info, err := os.Stat(target)
	if err != nil {
		t.Fatal(err)
	}
	if info.Mode().Perm() != 0o644 {
		t.Errorf("dry run changed the mode: %v", info.Mode().Perm())
	}
}

// A manifest the store did not write must not aim the restore outside its roots: a snapshot path
// that climbs out of the backup directory and a non-absolute original path are both refused
// before any write happens.
func TestRestoreRefusesManifestPathsOutsideTheStore(t *testing.T) {
	cases := []struct {
		name    string
		entry   func(target string) backupEntry
		wantErr string
	}{
		{
			name: "snapshot outside the backup dir",
			entry: func(target string) backupEntry {
				return backupEntry{OriginalPath: target, SnapshotPath: "../../planted.txt", Existed: true, Mode: 0o644, Kind: "regular"}
			},
			wantErr: "escape",
		},
		{
			name: "relative original path",
			entry: func(string) backupEntry {
				return backupEntry{OriginalPath: "victim.txt", SnapshotPath: "files/victim.txt", Existed: true, Mode: 0o644, Kind: "regular"}
			},
			wantErr: "not absolute",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			root := t.TempDir()
			t.Setenv("TPP_HOME", root)
			// A real file where the escaping snapshot path would land: without the refusal the
			// restore would read it and write its bytes to the destination.
			if err := os.WriteFile(filepath.Join(root, "planted.txt"), []byte("planted\n"), 0o644); err != nil {
				t.Fatal(err)
			}
			target := filepath.Join(t.TempDir(), "victim.txt")
			writeManifestFixture(t, root, "20990101000000.000000000", []backupEntry{tc.entry(target)})

			_, err := Restore("", false)
			if err == nil || !strings.Contains(err.Error(), tc.wantErr) {
				t.Fatalf("err = %v, want a refusal containing %q", err, tc.wantErr)
			}
			if _, statErr := os.Stat(target); !os.IsNotExist(statErr) {
				t.Fatalf("refusal still wrote the destination: %v", statErr)
			}
		})
	}
}

// The full lifecycle for a managed path outside ConfigDir/skills: force-uninstalling a modified
// Pi extension snapshots the operator's edit to the central store, and restore writes those
// bytes back to the extension path — the same authority a skill backup gets.
func TestRestoreReturnsAPiExtensionBackedUpByForceUninstall(t *testing.T) {
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

	uninstalled, err := Uninstall(UninstallOptions{Force: true})
	if err != nil {
		t.Fatal(err)
	}
	backup, ok := uninstalled.BackedUp[entry]
	if !ok {
		t.Fatalf("force uninstall did not back up the extension: %+v", uninstalled.BackedUp)
	}
	if _, err := os.Stat(entry); !os.IsNotExist(err) {
		t.Fatalf("force uninstall left the extension: %v", err)
	}

	report, err := Restore(filepath.Base(backup), false)
	if err != nil {
		t.Fatal(err)
	}
	if len(report.Restored) != 1 || report.Restored[0] != entry {
		t.Fatalf("Restored = %v, want [%s]", report.Restored, entry)
	}
	got, err := os.ReadFile(entry)
	if err != nil || string(got) != "user edit\n" {
		t.Fatalf("restored bytes = %q, err=%v, want the snapshotted edit", got, err)
	}
}

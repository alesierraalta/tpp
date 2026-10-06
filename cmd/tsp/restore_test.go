package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// The command must be discoverable from the usage text alone: the blurb names what it does and
// the flags line carries the exact invocation.
func TestCLIRestoreIsListedInTheUsage(t *testing.T) {
	bin := buildCLI(t)
	out, code := runCLI(t, bin)
	if code != 2 {
		t.Fatalf("no command = %d, want 2\n%s", code, out)
	}
	if !strings.Contains(out, "copy a backup store entry") {
		t.Fatalf("usage does not list the restore command:\n%s", out)
	}
	if !strings.Contains(out, "restore [--id <backup-id>] [--dry-run]") {
		t.Fatalf("usage does not document the restore flags:\n%s", out)
	}
}

// A home that never received a backup is an operational failure, not a crash or a usage error.
func TestCLIRestoreWithoutBackupsExitsOne(t *testing.T) {
	bin := buildCLI(t)
	home := t.TempDir()
	out, code := runCLIEnv(t, bin, []string{"TPP_HOME=" + home}, "restore")
	if code != 1 {
		t.Fatalf("restore without backups = %d, want 1\n%s", code, out)
	}
	if !strings.Contains(out, "no backups") {
		t.Fatalf("refusal does not name the missing store:\n%s", out)
	}
}

// The lifecycle contract against a real backup the binary itself created: --dry-run exits 0,
// prints the plan, and changes nothing; the real run puts the snapshot bytes back.
func TestCLIRestoreDryRunThenRealRunPutsTheSnapshotBack(t *testing.T) {
	bin := buildCLI(t)
	home := t.TempDir()
	cfg := t.TempDir()
	if out, code := runCLIEnv(t, bin, []string{"TPP_HOME=" + home}, "sync", "--config-dir", cfg); code != 0 {
		t.Fatalf("sync = %d\n%s", code, out)
	}
	skills, err := os.ReadDir(filepath.Join(cfg, "skills"))
	if err != nil || len(skills) == 0 {
		t.Fatalf("seeded install has no skills: %v", err)
	}
	target := filepath.Join(cfg, "skills", skills[0].Name(), "SKILL.md")
	if err := os.WriteFile(target, []byte("user edit\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	// --force snapshots the modified file to the central store, then replaces it: the backup now
	// holds "user edit\n" and the destination holds the embedded skill.
	if out, code := runCLIEnv(t, bin, []string{"TPP_HOME=" + home}, "sync", "--config-dir", cfg, "--force"); code != 0 {
		t.Fatalf("sync --force = %d\n%s", code, out)
	}
	before, err := os.ReadFile(target)
	if err != nil {
		t.Fatal(err)
	}

	out, code := runCLIEnv(t, bin, []string{"TPP_HOME=" + home}, "restore", "--dry-run")
	if code != 0 {
		t.Fatalf("restore --dry-run = %d, want 0\n%s", code, out)
	}
	if !strings.Contains(out, "[dry-run] [restore]") {
		t.Fatalf("dry-run output missing the plan:\n%s", out)
	}
	after, err := os.ReadFile(target)
	if err != nil || string(after) != string(before) {
		t.Fatalf("dry run changed the destination: %q, err=%v", after, err)
	}

	out, code = runCLIEnv(t, bin, []string{"TPP_HOME=" + home}, "restore")
	if code != 0 {
		t.Fatalf("restore = %d, want 0\n%s", code, out)
	}
	if !strings.Contains(out, "restored ") {
		t.Fatalf("restore output missing the result lines:\n%s", out)
	}
	got, err := os.ReadFile(target)
	if err != nil || string(got) != "user edit\n" {
		t.Fatalf("restored bytes = %q, err=%v, want the snapshot back", got, err)
	}
}

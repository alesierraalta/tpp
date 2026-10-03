package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// The command must be discoverable from the usage text alone: the blurb row and the
// flags line that shows every flag it accepts.
func TestCLIRepairIsListedInTheUsage(t *testing.T) {
	if !strings.Contains(usage, "  repair ") {
		t.Errorf("usage does not document the %q command:\n%s", "repair", usage)
	}
	if !strings.Contains(usage, "repair [--config-dir <dir>] [--dry-run] [--force]") {
		t.Errorf("usage does not document the repair flags line:\n%s", usage)
	}
	if !strings.Contains(usage, "uninstall, feedback, repair:") {
		t.Errorf("usage does not list repair among the commands sharing --config-dir:\n%s", usage)
	}
}

// The lifecycle contract: a healthy install is already repaired (exit 0, nothing to do),
// and a dry run over a broken one exits 0 while writing nothing.
func TestCLIRepairExitsZeroWhenHealthyAndOnDryRun(t *testing.T) {
	bin := buildCLI(t)
	home := t.TempDir()
	cfg := t.TempDir()
	if out, code := runCLIEnv(t, bin, []string{"TPP_HOME=" + home}, "sync", "--config-dir", cfg); code != 0 {
		t.Fatalf("sync = %d\n%s", code, out)
	}

	out, code := runCLIEnv(t, bin, []string{"TPP_HOME=" + home}, "repair", "--config-dir", cfg)
	if code != 0 {
		t.Fatalf("repair on a healthy install = %d, want 0\n%s", code, out)
	}
	if !strings.Contains(out, "already healthy") {
		t.Fatalf("healthy repair must say so:\n%s", out)
	}

	entries, err := os.ReadDir(filepath.Join(cfg, "skills"))
	if err != nil || len(entries) == 0 {
		t.Fatalf("sync installed no skills: %v", err)
	}
	deleted := filepath.Join(cfg, "skills", entries[0].Name())
	if err := os.RemoveAll(deleted); err != nil {
		t.Fatal(err)
	}

	out, code = runCLIEnv(t, bin, []string{"TPP_HOME=" + home}, "repair", "--config-dir", cfg, "--dry-run")
	if code != 0 {
		t.Fatalf("repair --dry-run = %d, want 0\n%s", code, out)
	}
	if !strings.Contains(out, "[dry-run]") {
		t.Fatalf("dry-run output must carry the prefix:\n%s", out)
	}
	if _, err := os.Stat(filepath.Join(deleted, "SKILL.md")); !os.IsNotExist(err) {
		t.Fatalf("dry run wrote the missing skill: %v", err)
	}
}

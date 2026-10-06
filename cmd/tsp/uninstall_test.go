package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// The command must be discoverable from the usage text alone.
func TestCLIUninstallIsListedInTheUsage(t *testing.T) {
	bin := buildCLI(t)
	out, code := runCLI(t, bin)
	if code != 2 {
		t.Fatalf("no command = %d, want 2\n%s", code, out)
	}
	if !strings.Contains(out, "uninstall") {
		t.Fatalf("usage does not list uninstall:\n%s", out)
	}
}

// The lifecycle contract against a seeded install: --dry-run exits 0 and mutates nothing, and the
// real run takes the skills and the gate off the machine while the foreign file stays.
func TestCLIUninstallDryRunExitsZeroThenARealRunRemoves(t *testing.T) {
	bin := buildCLI(t)
	home := t.TempDir()
	cfg := t.TempDir()
	if out, code := runCLIEnv(t, bin, []string{"TPP_HOME=" + home}, "sync", "--config-dir", cfg); code != 0 {
		t.Fatalf("sync = %d\n%s", code, out)
	}
	foreign := filepath.Join(cfg, "skills", "unrelated", "README.md")
	if err := os.MkdirAll(filepath.Dir(foreign), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(foreign, []byte("keep me\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	statePath := filepath.Join(home, "state.json")
	settingsPath := filepath.Join(cfg, "settings.json")
	stateBefore, err := os.ReadFile(statePath)
	if err != nil {
		t.Fatal(err)
	}
	settingsBefore, err := os.ReadFile(settingsPath)
	if err != nil {
		t.Fatal(err)
	}
	skills, err := os.ReadDir(filepath.Join(cfg, "skills"))
	if err != nil {
		t.Fatal(err)
	}

	out, code := runCLIEnv(t, bin, []string{"TPP_HOME=" + home}, "uninstall", "--config-dir", cfg, "--dry-run")
	if code != 0 {
		t.Fatalf("uninstall --dry-run = %d, want 0\n%s", code, out)
	}
	if !strings.Contains(out, "[remove]") {
		t.Fatalf("dry-run output missing the classified plan:\n%s", out)
	}
	stateAfter, _ := os.ReadFile(statePath)
	settingsAfter, _ := os.ReadFile(settingsPath)
	if string(stateBefore) != string(stateAfter) {
		t.Fatal("dry run changed state.json")
	}
	if string(settingsBefore) != string(settingsAfter) {
		t.Fatal("dry run changed settings.json")
	}
	for _, entry := range skills {
		if entry.Name() == "unrelated" {
			continue // the foreign directory never had a SKILL.md
		}
		if _, err := os.Stat(filepath.Join(cfg, "skills", entry.Name(), "SKILL.md")); err != nil {
			t.Fatalf("dry run removed skill %s: %v", entry.Name(), err)
		}
	}

	out, code = runCLIEnv(t, bin, []string{"TPP_HOME=" + home}, "uninstall", "--config-dir", cfg)
	if code != 0 {
		t.Fatalf("uninstall = %d, want 0\n%s", code, out)
	}
	after, err := os.ReadDir(filepath.Join(cfg, "skills"))
	if err != nil {
		t.Fatal(err)
	}
	for _, entry := range after {
		if !entry.IsDir() || entry.Name() == "unrelated" {
			continue
		}
		if _, err := os.Stat(filepath.Join(cfg, "skills", entry.Name(), "SKILL.md")); !os.IsNotExist(err) {
			t.Errorf("skill %s still installed: %v", entry.Name(), err)
		}
	}
	if got, err := os.ReadFile(foreign); err != nil || string(got) != "keep me\n" {
		t.Errorf("foreign file changed: %v %q", err, got)
	}
	rawSettings, err := os.ReadFile(settingsPath)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(rawSettings), `"command"`) {
		t.Fatalf("gate still wired after uninstall:\n%s", rawSettings)
	}
	rawState, err := os.ReadFile(statePath)
	if err != nil {
		t.Fatal(err)
	}
	var afterState struct {
		Hosts map[string]json.RawMessage `json:"hosts"`
	}
	if err := json.Unmarshal(rawState, &afterState); err != nil {
		t.Fatal(err)
	}
	if len(afterState.Hosts) != 0 {
		t.Fatalf("hosts left in state: %v", afterState.Hosts)
	}
}

// A modified skill exits 1 with a refusal that names --force, and nothing is removed.
func TestCLIUninstallRefusesAModifiedSkillWithoutForce(t *testing.T) {
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

	out, code := runCLIEnv(t, bin, []string{"TPP_HOME=" + home}, "uninstall", "--config-dir", cfg)
	if code != 1 {
		t.Fatalf("uninstall on a modified skill = %d, want 1\n%s", code, out)
	}
	if !strings.Contains(out, "--force") {
		t.Fatalf("refusal must name --force:\n%s", out)
	}
	if _, err := os.Stat(target); err != nil {
		t.Fatalf("refusal removed the modified file: %v", err)
	}
}

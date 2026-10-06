package repair_test

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/alesierraalta/tsp/internal/assets"
	"github.com/alesierraalta/tsp/internal/repair"
	"github.com/alesierraalta/tsp/internal/sync"
)

func currentBin(t *testing.T) string {
	t.Helper()
	exe, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	abs, err := filepath.Abs(exe)
	if err != nil {
		t.Fatal(err)
	}
	return abs
}

// seed puts a healthy installation on disk wired to this test binary, with PATH stubbed so
// doctor's binary comparison cannot see the tpp the developer machine has on PATH.
func seed(t *testing.T) (root, cfg string) {
	t.Helper()
	root = t.TempDir()
	t.Setenv("TPP_HOME", root)
	t.Setenv("PATH", t.TempDir())
	cfg = t.TempDir()
	if _, err := sync.Sync(cfg, currentBin(t), sync.Options{}); err != nil {
		t.Fatal(err)
	}
	return root, cfg
}

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

func writeSettings(t *testing.T, cfg string, s map[string]any) {
	t.Helper()
	raw, err := json.MarshalIndent(s, "", "  ")
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(cfg, "settings.json"), raw, 0o644); err != nil {
		t.Fatal(err)
	}
}

func stopCommands(s map[string]any) []string {
	var out []string
	hooks, _ := s["hooks"].(map[string]any)
	stop, _ := hooks["Stop"].([]any)
	for _, e := range stop {
		entry, _ := e.(map[string]any)
		list, _ := entry["hooks"].([]any)
		for _, h := range list {
			hook, _ := h.(map[string]any)
			cmd, _ := hook["command"].(string)
			out = append(out, cmd)
		}
	}
	return out
}

func contains(values []string, want string) bool {
	for _, v := range values {
		if v == want {
			return true
		}
	}
	return false
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

func plantForeign(t *testing.T, path, body string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
}

func TestRepairOnAHealthyInstallWritesNothing(t *testing.T) {
	root, cfg := seed(t)
	stateBefore, err := os.ReadFile(filepath.Join(root, "state.json"))
	if err != nil {
		t.Fatal(err)
	}
	settingsBefore, err := os.ReadFile(filepath.Join(cfg, "settings.json"))
	if err != nil {
		t.Fatal(err)
	}

	report, err := repair.Run(repair.Options{ConfigDir: cfg})
	if err != nil {
		t.Fatal(err)
	}
	if !report.AlreadyHealthy {
		t.Fatalf("problems = %v, want already healthy", report.Problems)
	}
	if !strings.Contains(report.String(), "already healthy") {
		t.Fatalf("report = %q, want the already-healthy verdict", report.String())
	}

	stateAfter, err := os.ReadFile(filepath.Join(root, "state.json"))
	if err != nil {
		t.Fatal(err)
	}
	settingsAfter, err := os.ReadFile(filepath.Join(cfg, "settings.json"))
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(stateBefore, stateAfter) {
		t.Fatal("repair changed state.json on a healthy install")
	}
	if !bytes.Equal(settingsBefore, settingsAfter) {
		t.Fatal("repair changed settings.json on a healthy install")
	}
	if dirs := backupDirs(t, root); len(dirs) != 0 {
		t.Fatalf("repair created backup dirs on a healthy install: %v", dirs)
	}
}

func TestRepairRestoresADeletedSkillAndKeepsForeignFiles(t *testing.T) {
	_, cfg := seed(t)
	deleted := assets.SkillNames()[0]
	kept := assets.SkillNames()[1]
	foreignBeside := filepath.Join(cfg, "skills", kept, "notes.md")
	foreignTree := filepath.Join(cfg, "skills", "unrelated", "README.md")
	plantForeign(t, foreignBeside, "notes beside a managed skill\n")
	plantForeign(t, foreignTree, "keep me\n")
	if err := os.RemoveAll(filepath.Join(cfg, "skills", deleted)); err != nil {
		t.Fatal(err)
	}

	report, err := repair.Run(repair.Options{ConfigDir: cfg})
	if err != nil {
		t.Fatal(err)
	}
	if len(report.Problems) == 0 {
		t.Fatal("a deleted skill is a problem repair must report")
	}
	if _, err := os.Stat(filepath.Join(cfg, "skills", deleted, "SKILL.md")); err != nil {
		t.Fatalf("deleted skill not restored: %v", err)
	}
	if got, err := os.ReadFile(foreignBeside); err != nil || string(got) != "notes beside a managed skill\n" {
		t.Fatalf("foreign file beside a managed skill changed: %v %q", err, got)
	}
	if got, err := os.ReadFile(foreignTree); err != nil || string(got) != "keep me\n" {
		t.Fatalf("foreign skill tree changed: %v %q", err, got)
	}

	second, err := repair.Run(repair.Options{ConfigDir: cfg})
	if err != nil {
		t.Fatal(err)
	}
	if !second.AlreadyHealthy {
		t.Fatalf("second run problems = %v, want already healthy", second.Problems)
	}
}

func TestRepairRewiresTheStopHook(t *testing.T) {
	foreign := map[string]any{"matcher": "", "hooks": []any{map[string]any{
		"type": "command", "command": "foreign review hook", "timeout": 60,
	}}}
	oldCmd := `"/old/install/tpp" gate`

	t.Run("unwired", func(t *testing.T) {
		_, cfg := seed(t)
		s := readSettings(t, cfg)
		s["model"] = "opus"
		s["hooks"].(map[string]any)["Stop"] = []any{foreign}
		writeSettings(t, cfg, s)

		report, err := repair.Run(repair.Options{ConfigDir: cfg})
		if err != nil {
			t.Fatal(err)
		}
		if !report.SettingsChanged {
			t.Fatal("unwired hook: repair must report a settings change")
		}
		got := stopCommands(readSettings(t, cfg))
		desired := sync.HookCommand(currentBin(t))
		if !contains(got, desired) {
			t.Fatalf("Stop hooks = %v, want %q", got, desired)
		}
		if !contains(got, "foreign review hook") {
			t.Fatalf("Stop hooks = %v, want the foreign hook kept", got)
		}
		if after := readSettings(t, cfg); after["model"] != "opus" {
			t.Fatalf("unrelated setting lost: model=%v", after["model"])
		}
	})

	t.Run("different binary", func(t *testing.T) {
		_, cfg := seed(t)
		s := readSettings(t, cfg)
		s["model"] = "opus"
		hooks := s["hooks"].(map[string]any)
		stop := hooks["Stop"].([]any)
		entry, _ := stop[0].(map[string]any)
		list, _ := entry["hooks"].([]any)
		hook, _ := list[0].(map[string]any)
		hook["command"] = oldCmd
		hooks["Stop"] = append(stop, foreign)
		writeSettings(t, cfg, s)

		report, err := repair.Run(repair.Options{ConfigDir: cfg})
		if err != nil {
			t.Fatal(err)
		}
		if !report.SettingsChanged {
			t.Fatal("wrong-binary hook: repair must report a settings change")
		}
		if !contains(report.RemovedHooks, oldCmd) {
			t.Fatalf("RemovedHooks = %v, want the old gate %q", report.RemovedHooks, oldCmd)
		}
		got := stopCommands(readSettings(t, cfg))
		desired := sync.HookCommand(currentBin(t))
		if !contains(got, desired) {
			t.Fatalf("Stop hooks = %v, want %q", got, desired)
		}
		if contains(got, oldCmd) {
			t.Fatalf("Stop hooks = %v, want the old gate gone", got)
		}
		if !contains(got, "foreign review hook") {
			t.Fatalf("Stop hooks = %v, want the foreign hook kept", got)
		}
		if after := readSettings(t, cfg); after["model"] != "opus" {
			t.Fatalf("unrelated setting lost: model=%v", after["model"])
		}
	})
}

func TestRepairLeavesModifiedFilesWithoutForceAndReplacesWithForce(t *testing.T) {
	root, cfg := seed(t)
	name := assets.SkillNames()[0]
	target := filepath.Join(cfg, "skills", name, "SKILL.md")
	if err := os.WriteFile(target, []byte("user edit\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	foreign := filepath.Join(cfg, "skills", "unrelated", "README.md")
	plantForeign(t, foreign, "keep me\n")

	report, err := repair.Run(repair.Options{ConfigDir: cfg})
	if err != nil {
		t.Fatal(err)
	}
	if got, err := os.ReadFile(target); err != nil || string(got) != "user edit\n" {
		t.Fatalf("modified file replaced without --force: %v %q", err, got)
	}
	if len(report.Remaining) == 0 || !contains(report.Remaining, "skill differs from the embedded version: "+name) {
		t.Fatalf("Remaining = %v, want the modified skill listed", report.Remaining)
	}
	if got, err := os.ReadFile(foreign); err != nil || string(got) != "keep me\n" {
		t.Fatalf("foreign file changed: %v %q", err, got)
	}
	if dirs := backupDirs(t, root); len(dirs) != 0 {
		t.Fatalf("repair without --force created a backup: %v", dirs)
	}

	forced, err := repair.Run(repair.Options{ConfigDir: cfg, Force: true})
	if err != nil {
		t.Fatal(err)
	}
	if got, err := os.ReadFile(target); err != nil || string(got) == "user edit\n" {
		t.Fatalf("modified file not replaced with --force: %v %q", err, got)
	}
	if len(forced.Remaining) != 0 {
		t.Fatalf("Remaining after --force = %v, want empty", forced.Remaining)
	}
	if dirs := backupDirs(t, root); len(dirs) == 0 {
		t.Fatal("--force must snapshot the replaced file into the backup store")
	}
	if got, err := os.ReadFile(foreign); err != nil || string(got) != "keep me\n" {
		t.Fatalf("foreign file changed by --force: %v %q", err, got)
	}

	third, err := repair.Run(repair.Options{ConfigDir: cfg})
	if err != nil {
		t.Fatal(err)
	}
	if !third.AlreadyHealthy {
		t.Fatalf("run after --force problems = %v, want already healthy", third.Problems)
	}
}

func TestRepairDryRunWritesNothing(t *testing.T) {
	root, cfg := seed(t)
	deleted := assets.SkillNames()[0]
	stateBefore, err := os.ReadFile(filepath.Join(root, "state.json"))
	if err != nil {
		t.Fatal(err)
	}
	if err := os.RemoveAll(filepath.Join(cfg, "skills", deleted)); err != nil {
		t.Fatal(err)
	}
	s := readSettings(t, cfg)
	s["hooks"].(map[string]any)["Stop"] = []any{}
	writeSettings(t, cfg, s)
	settingsBroken, err := os.ReadFile(filepath.Join(cfg, "settings.json"))
	if err != nil {
		t.Fatal(err)
	}

	report, err := repair.Run(repair.Options{ConfigDir: cfg, DryRun: true})
	if err != nil {
		t.Fatal(err)
	}
	if len(report.Problems) == 0 {
		t.Fatal("dry run must still classify the problems it would fix")
	}
	stateAfter, err := os.ReadFile(filepath.Join(root, "state.json"))
	if err != nil {
		t.Fatal(err)
	}
	settingsAfter, err := os.ReadFile(filepath.Join(cfg, "settings.json"))
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(stateBefore, stateAfter) {
		t.Fatal("dry run changed state.json")
	}
	if !bytes.Equal(settingsBroken, settingsAfter) {
		t.Fatal("dry run changed settings.json")
	}
	if _, err := os.Stat(filepath.Join(cfg, "skills", deleted, "SKILL.md")); !os.IsNotExist(err) {
		t.Fatalf("dry run wrote the missing skill: %v", err)
	}
	if dirs := backupDirs(t, root); len(dirs) != 0 {
		t.Fatalf("dry run created backup dirs: %v", dirs)
	}
	if !strings.Contains(report.String(), "[dry-run]") {
		t.Fatalf("report = %q, want the dry-run prefix", report.String())
	}
}

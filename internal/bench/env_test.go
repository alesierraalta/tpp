package bench

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"
)

// A benchmark case must measure the testing skills, not whatever else the machine's global
// configuration makes a session do. The agent runs against a config directory the run owns.
func TestMinimalConfigHoldsTheSkillsAndNothingElse(t *testing.T) {
	dir := t.TempDir()
	real := filepath.Join(dir, "real")
	if err := os.MkdirAll(real, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(real, CredentialsFile), []byte(`{"token":"x"}`), 0o600); err != nil {
		t.Fatal(err)
	}
	cfg := filepath.Join(dir, "cfg")
	if err := WriteBenchConfig(cfg, real); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(cfg, "skills", "test-strategy", "SKILL.md")); err != nil {
		t.Fatalf("the skills under test must be installed: %v", err)
	}
	if _, err := os.Stat(filepath.Join(cfg, "CLAUDE.md")); !os.IsNotExist(err) {
		t.Fatal("a global CLAUDE.md would add instructions the benchmark is not measuring")
	}
	data, err := os.ReadFile(filepath.Join(cfg, "settings.json"))
	if err != nil {
		t.Fatal(err)
	}
	for _, unwanted := range []string{"hooks", "mcpServers", "enabledPlugins"} {
		if strings.Contains(string(data), unwanted) {
			t.Fatalf("settings must carry no %s: %s", unwanted, data)
		}
	}
	// Without the operator's credentials the agent cannot log in and every case fails at once.
	// F25: a copy, never a link — a refresh that fails may damage only this throwaway directory.
	cred := filepath.Join(cfg, CredentialsFile)
	st, err := os.Lstat(cred)
	if err != nil {
		t.Fatalf("credentials must be copied into the throwaway config: %v", err)
	}
	if st.Mode()&os.ModeSymlink != 0 {
		t.Fatal("credentials must be a copy, not a link: a write through the link empties the operator's login")
	}
	if st.Mode().Perm() != 0o600 {
		t.Fatalf("credentials mode = %v, want 0600", st.Mode().Perm())
	}
	if src, err := os.Stat(filepath.Join(real, CredentialsFile)); err == nil && os.SameFile(src, st) {
		t.Fatal("the copy must not be the operator's own file")
	}
	got, err := os.ReadFile(cred)
	if err != nil || string(got) != `{"token":"x"}` {
		t.Fatalf("credentials unreadable in the copy: %v %s", err, got)
	}
}

// A config directory built with no credentials to copy is still usable; it simply cannot log in,
// which the run reports as a failed case rather than a recall of zero.
func TestBenchConfigWithoutCredentials(t *testing.T) {
	cfg := filepath.Join(t.TempDir(), "cfg")
	if err := WriteBenchConfig(cfg, filepath.Join(t.TempDir(), "absent")); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Lstat(filepath.Join(cfg, CredentialsFile)); !os.IsNotExist(err) {
		t.Fatal("nothing to link, so nothing must be created")
	}
}

// The skill now tells the flow to run `tpp plan init`, so the agent has to be able to find
// the binary that is under test, not whichever one happens to be installed.
func TestAgentEnvPutsTheRunningBinaryFirstOnPath(t *testing.T) {
	env := agentEnv([]string{"PATH=/bin:/usr/bin"}, "/cfg", "/repo/bin")
	for _, e := range env {
		if strings.HasPrefix(e, "PATH=") {
			if e != "PATH=/repo/bin:/bin:/usr/bin" {
				t.Fatalf("path = %q", e)
			}
			return
		}
	}
	t.Fatal("no PATH in the agent environment")
}

func TestAgentEnvPointsAtTheGivenConfig(t *testing.T) {
	env := agentEnv([]string{"PATH=/bin", "CLAUDE_CONFIG_DIR=/old", "HOME=/h"}, "/new", "")
	var got []string
	for _, e := range env {
		if strings.HasPrefix(e, "CLAUDE_CONFIG_DIR=") {
			got = append(got, e)
		}
	}
	if len(got) != 1 || got[0] != "CLAUDE_CONFIG_DIR=/new" {
		t.Fatalf("config dir env = %v", got)
	}
	if agentEnv([]string{"PATH=/bin"}, "", "") != nil {
		t.Fatal("without a config dir the agent must inherit the environment unchanged")
	}
}

// writePiSource builds a stand-in operator agent directory holding the two files a Pi run needs.
func writePiSource(t *testing.T) string {
	t.Helper()
	src := filepath.Join(t.TempDir(), "operator")
	if err := os.MkdirAll(src, 0o700); err != nil {
		t.Fatal(err)
	}
	for name, body := range map[string]string{
		PiAuthFile:        `{"opencode":{"type":"api_key","key":"operator-token"}}`,
		PiModelsStoreFile: `{"models":[]}`,
	} {
		if err := os.WriteFile(filepath.Join(src, name), []byte(body), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	return src
}

// F25: the throwaway Claude config linked the operator's credentials and a failed refresh wrote an
// empty token through the link, logging the operator out everywhere. Pi must be safe by
// construction, so its credentials are copies: a refresh can only damage the copy the run discards.
func TestPiBenchConfigCopiesCredentialsInsteadOfLinkingThem(t *testing.T) {
	src := writePiSource(t)
	cfg := filepath.Join(t.TempDir(), "cfg")
	if err := WriteBenchPiConfig(cfg, src); err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{PiAuthFile, PiModelsStoreFile} {
		st, err := os.Lstat(filepath.Join(cfg, name))
		if err != nil {
			t.Fatalf("%s must be copied into the throwaway config: %v", name, err)
		}
		if !st.Mode().IsRegular() {
			t.Fatalf("%s must be a regular file, not a link: mode %v", name, st.Mode())
		}
	}
	copy, err := os.ReadFile(filepath.Join(cfg, PiAuthFile))
	if err != nil || !strings.Contains(string(copy), "operator-token") {
		t.Fatalf("the copy must carry the operator's token: %v %s", err, copy)
	}
	// The point of the copy: whatever a run does to its own file, the source keeps its token.
	source, err := os.ReadFile(filepath.Join(src, PiAuthFile))
	if err != nil || !strings.Contains(string(source), "operator-token") {
		t.Fatalf("the source must still hold its token after the writer ran: %v %s", err, source)
	}
}

// A benchmark case must measure the testing skills and nothing else: the Pi config carries the
// same skill files the Claude one does, points Pi at them, and loads no packages at all.
func TestPiBenchConfigHoldsTheSkillsAndNothingElse(t *testing.T) {
	src := writePiSource(t)
	dir := t.TempDir()
	cfg, claude := filepath.Join(dir, "pi"), filepath.Join(dir, "claude")
	if err := WriteBenchPiConfig(cfg, src); err != nil {
		t.Fatal(err)
	}
	if err := WriteBenchConfig(claude, src); err != nil {
		t.Fatal(err)
	}
	// Both runners must measure the same skill content, byte for byte.
	want, err := os.ReadFile(filepath.Join(claude, "skills", "test-strategy", "SKILL.md"))
	if err != nil {
		t.Fatal(err)
	}
	got, err := os.ReadFile(filepath.Join(cfg, "skills", "test-strategy", "SKILL.md"))
	if err != nil || !bytes.Equal(got, want) {
		t.Fatalf("the Pi config must hold the same skills: %v", err)
	}
	data, err := os.ReadFile(filepath.Join(cfg, "settings.json"))
	if err != nil {
		t.Fatal(err)
	}
	var settings map[string]any
	if err := json.Unmarshal(data, &settings); err != nil {
		t.Fatal(err)
	}
	abs, err := filepath.Abs(cfg)
	if err != nil {
		t.Fatal(err)
	}
	if skills, ok := settings["skills"].([]any); !ok || len(skills) != 1 || skills[0] != filepath.Join(abs, "skills") {
		t.Fatalf("skills must point at this directory's skills: %v", settings["skills"])
	}
	if packages, ok := settings["packages"].([]any); !ok || len(packages) != 0 {
		t.Fatalf("packages must be empty so the operator's extensions and MCP servers do not load: %v", settings["packages"])
	}
	if trust, _ := settings["defaultProjectTrust"].(string); trust != "never" {
		t.Fatalf("a non-interactive run must ignore project-local resources: %v", settings["defaultProjectTrust"])
	}
	for _, unwanted := range []string{"extensions", "prompts", "themes", "mcpServers"} {
		if _, ok := settings[unwanted]; ok {
			t.Fatalf("settings must carry no %s: %s", unwanted, data)
		}
	}
	// Nothing else lands in the directory: an agent dir is a whole configuration, not just settings.
	entries, err := os.ReadDir(cfg)
	if err != nil {
		t.Fatal(err)
	}
	var names []string
	for _, e := range entries {
		names = append(names, e.Name())
	}
	sort.Strings(names)
	wantNames := PiAuthFile + ", " + PiModelsStoreFile + ", settings.json, skills"
	if got := strings.Join(names, ", "); got != wantNames {
		t.Fatalf("throwaway config holds %q, want %q", got, wantNames)
	}
}

// Without the operator's credentials the writer must still build a config: the run then reports the
// CLI's own authentication error, which is a failed case, instead of failing to start at all.
func TestPiBenchConfigWithoutCredentials(t *testing.T) {
	cfg := filepath.Join(t.TempDir(), "cfg")
	if err := WriteBenchPiConfig(cfg, filepath.Join(t.TempDir(), "absent")); err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{PiAuthFile, PiModelsStoreFile} {
		if _, err := os.Lstat(filepath.Join(cfg, name)); !os.IsNotExist(err) {
			t.Fatalf("nothing to copy, so %s must not be created", name)
		}
	}
	if _, err := os.Stat(filepath.Join(cfg, "settings.json")); err != nil {
		t.Fatalf("the config must still be usable: %v", err)
	}
}

// PI_CODING_AGENT_DIR is what points Pi at the throwaway config, and PATH is what makes the agent
// run the build under test rather than the installed one.
func TestPiEnvPointsAtTheThrowawayConfigAndTheBinaryUnderTest(t *testing.T) {
	env := piEnv([]string{"PATH=/bin:/usr/bin", "PI_CODING_AGENT_DIR=/old", "HOME=/h"}, "/cfg", "/repo/bin")
	var path, config []string
	for _, e := range env {
		switch {
		case strings.HasPrefix(e, "PATH="):
			path = append(path, e)
		case strings.HasPrefix(e, "PI_CODING_AGENT_DIR="):
			config = append(config, e)
		}
	}
	if len(path) != 1 || path[0] != "PATH=/repo/bin:/bin:/usr/bin" {
		t.Fatalf("path = %v", path)
	}
	if len(config) != 1 || config[0] != "PI_CODING_AGENT_DIR=/cfg" {
		t.Fatalf("agent dir = %v", config)
	}
	if piEnv([]string{"PATH=/bin"}, "", "") != nil {
		t.Fatal("without an agent dir the agent must inherit the environment unchanged")
	}
}

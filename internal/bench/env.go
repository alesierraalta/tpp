package bench

import (
	"encoding/json"
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"strings"

	"github.com/alesierraalta/tsp/internal/assets"
)

// CredentialsFile is the only piece of the operator's configuration a benchmark run needs: the
// agent cannot log in without it.
const CredentialsFile = ".credentials.json"

// PiAuthFile and PiModelsStoreFile are the Pi equivalent: without them an agent dir built from
// scratch starts with no provider credentials at all, and every case fails before the model runs.
const (
	PiAuthFile        = "auth.json"
	PiModelsStoreFile = "models-store.json"
)

// WriteBenchConfig builds a Claude configuration directory holding the embedded skills and
// nothing else. A benchmark case must measure the testing skills, not the operator's global
// instructions, memory protocol, or MCP servers, which cost turns and vary between machines.
func WriteBenchConfig(dir, credentialsFrom string) error {
	if err := writeEmbeddedSkills(dir); err != nil {
		return err
	}
	settings, err := json.MarshalIndent(map[string]any{"includeCoAuthoredBy": false}, "", "  ")
	if err != nil {
		return err
	}
	if err := os.WriteFile(filepath.Join(dir, "settings.json"), append(settings, '\n'), 0o644); err != nil {
		return err
	}
	// Copied, never linked: a refresh writes into this throwaway copy, so a failed refresh cannot
	// empty the operator's login the way it did when the two paths were the same file. F25.
	src := filepath.Join(credentialsFrom, CredentialsFile)
	if err := copyFile(src, filepath.Join(dir, CredentialsFile)); err != nil {
		return err
	}
	return VerifyBenchSkills(dir)
}

// WriteBenchPiConfig builds the Pi equivalent of WriteBenchConfig in the same directory: the
// embedded skills and a settings.json that loads nothing else. Pi derives its package root, and
// therefore the operator's extensions, memory protocol, and MCP servers, from the agent dir the
// run owns, and PI_CODING_AGENT_DIR is what points it there.
func WriteBenchPiConfig(dir, configFrom string) error {
	if err := writeEmbeddedSkills(dir); err != nil {
		return err
	}
	abs, err := filepath.Abs(dir)
	if err != nil {
		return err
	}
	settings, err := json.MarshalIndent(map[string]any{
		// Absolute: the path must resolve to this directory whatever base the agent dir implies.
		"skills": []string{filepath.Join(abs, "skills")},
		// Empty: the operator's packages bring their own skills, extensions, and MCP servers, which
		// would cost turns and differ between machines.
		"packages": []string{},
		// Pinned to its current default. A batch run never types /skill:name, but pinning the value
		// means a future default flip cannot quietly change what a case loads.
		"enableSkillCommands": true,
		// Non-interactive runs never see the trust prompt and fall back to this setting; asking is
		// impossible in a batch run, and trusting would load whatever the fixture happens to carry.
		"defaultProjectTrust": "never",
		// No version ping or install telemetry: the run must not depend on the network being up.
		"enableInstallTelemetry": false,
		// The bench owns the retry policy (--retries, --retry-delay); an agent-level retry would add
		// turns and cost that no case asked for.
		"retry": map[string]any{"enabled": false},
	}, "", "  ")
	if err != nil {
		return err
	}
	if err := os.WriteFile(filepath.Join(dir, "settings.json"), append(settings, '\n'), 0o644); err != nil {
		return err
	}
	// Copied, never linked. A symlink makes the operator's token the working file: a refresh that
	// fails writes the empty token state straight through the link. That is finding F25 in
	// docs/testing/test-plan.md, where a failed refresh logged the operator out of Claude Code
	// everywhere. A copy means a failed refresh can only empty the copy this run throws away.
	for _, name := range []string{PiAuthFile, PiModelsStoreFile} {
		if err := copyFile(filepath.Join(configFrom, name), filepath.Join(dir, name)); err != nil {
			return err
		}
	}
	return VerifyBenchSkills(dir)
}

// copyFile copies src to dst with owner-only permissions and does nothing when src is absent: a
// source without credentials is not a failure to build the config, it is a run that cannot log in.
func copyFile(src, dst string) error {
	data, err := os.ReadFile(src)
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return nil
		}
		return err
	}
	_ = os.Remove(dst)
	return os.WriteFile(dst, data, 0o600)
}

// writeEmbeddedSkills materializes the embedded skills under dir/skills. Both runners write the
// same bytes, so a case measures the same skill content whichever agent runs it.
func writeEmbeddedSkills(dir string) error {
	skills := filepath.Join(dir, "skills")
	if err := os.RemoveAll(skills); err != nil {
		return err
	}
	return fs.WalkDir(assets.Skills(), ".", func(p string, d fs.DirEntry, err error) error {
		if err != nil || p == "." {
			return err
		}
		target := filepath.Join(skills, filepath.FromSlash(p))
		if d.IsDir() {
			return os.MkdirAll(target, 0o755)
		}
		data, err := fs.ReadFile(assets.Skills(), p)
		if err != nil {
			return err
		}
		if err := os.MkdirAll(filepath.Dir(target), 0o755); err != nil {
			return err
		}
		return os.WriteFile(target, data, 0o644)
	})
}

// ClaudeConfigEnv and PiConfigEnv are the variables each runner reads to find its config
// directory: the Claude CLI config dir, and the Pi agent dir.
const (
	ClaudeConfigEnv = "CLAUDE_CONFIG_DIR"
	PiConfigEnv     = "PI_CODING_AGENT_DIR"
)

// agentEnv returns the environment for the agent process: CLAUDE_CONFIG_DIR set to cfgDir,
// replacing any inherited value, and binDir first on PATH so the flow finds the binary under
// test. Empty cfgDir and binDir mean "inherit unchanged".
func agentEnv(base []string, cfgDir, binDir string) []string {
	return agentEnvWith(base, ClaudeConfigEnv, cfgDir, binDir)
}

// piEnv is the same contract for Pi: PI_CODING_AGENT_DIR is the agent directory it loads settings,
// credentials, and packages from (docs/environment-variables.md), so a throwaway config dir is
// what keeps the operator's own configuration out of the measurement.
func piEnv(base []string, cfgDir, binDir string) []string {
	return agentEnvWith(base, PiConfigEnv, cfgDir, binDir)
}

func agentEnvWith(base []string, configKey, cfgDir, binDir string) []string {
	if cfgDir == "" && binDir == "" {
		return nil
	}
	out := make([]string, 0, len(base)+1)
	prefix := configKey + "="
	for _, e := range base {
		switch {
		case strings.HasPrefix(e, prefix):
			continue
		case binDir != "" && strings.HasPrefix(e, "PATH="):
			out = append(out, "PATH="+binDir+string(os.PathListSeparator)+strings.TrimPrefix(e, "PATH="))
		default:
			out = append(out, e)
		}
	}
	if cfgDir != "" {
		out = append(out, prefix+cfgDir)
	}
	return out
}

// DefaultPiConfigDir is where Pi keeps its agent directory when PI_CODING_AGENT_DIR is unset; the
// benchmark reads the operator's credentials from there to copy them into a throwaway config.
func DefaultPiConfigDir() string {
	if dir := os.Getenv(PiConfigEnv); dir != "" {
		return dir
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return ""
	}
	return filepath.Join(home, ".pi", "agent")
}

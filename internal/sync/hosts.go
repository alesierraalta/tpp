package sync

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// Host is an installed agent host and the directory from which it loads skills. For hosts
// whose payload is not a skill tree (Pi), SkillsDir is the host's managed root instead and
// the manifest places components under the host's own convention.
type Host struct {
	Name      string
	ConfigDir string
	SkillsDir string
}

// PiHost is the opt-in Pi host: SkillsDir is Pi's extension directory, so the shared claim
// layout lands each component at <SkillsDir>/<component>/index.ts, which is the extension
// path Pi loads. Discovery never offers it; a caller that wants Pi passes this host directly
// — `sync --hosts pi` is the CLI spelling of that opt-in.
func PiHost(home string) Host {
	return hostForConfig("pi", filepath.Join(home, ".pi"))
}

// hostForConfig rebuilds a host from its name and recorded config directory, deriving the
// managed root that host actually loads content from: the skill hosts read <config>/skills,
// while Pi's root is its extension directory outside any skills path. Uninstall reconstructs
// hosts from state records with it, so a Pi extension recorded under agent/extensions is
// classified against its real claims instead of being mistaken for an orphan.
func hostForConfig(name, configDir string) Host {
	skillsDir := filepath.Join(configDir, "skills")
	if name == "pi" {
		skillsDir = filepath.Join(configDir, "agent", "extensions")
	}
	return Host{Name: name, ConfigDir: configDir, SkillsDir: skillsDir}
}

// Discovery is the non-fatal result of looking for host configuration directories.
type Discovery struct {
	Hosts           []Host
	LookedFor       []string
	Problems        []string
	ClaudeConfigDir string
}

var knownHosts = []string{"claude", "opencode", "gemini", "codex"}

// KnownHosts returns the host names accepted by --hosts, in report order.
func KnownHosts() []string {
	return append([]string(nil), knownHosts...)
}

// DiscoverHosts finds installed hosts under home. The optional Claude directory is the
// --config-dir override; when omitted, Claude is looked for at home/.claude.
func DiscoverHosts(home string, claudeConfigDir ...string) Discovery {
	claudeDir := filepath.Join(home, ".claude")
	claudeOverride := len(claudeConfigDir) > 0 && claudeConfigDir[0] != ""
	if claudeOverride {
		claudeDir = claudeConfigDir[0]
	}
	result := Discovery{ClaudeConfigDir: claudeDir}
	candidates := []Host{
		{Name: "claude", ConfigDir: claudeDir, SkillsDir: filepath.Join(claudeDir, "skills")},
		{Name: "opencode", ConfigDir: filepath.Join(home, ".config", "opencode"), SkillsDir: filepath.Join(home, ".config", "opencode", "skills")},
		{Name: "gemini", ConfigDir: filepath.Join(home, ".gemini"), SkillsDir: filepath.Join(home, ".gemini", "skills")},
		{Name: "codex", ConfigDir: filepath.Join(home, ".codex"), SkillsDir: filepath.Join(home, ".codex", "skills")},
	}
	for _, candidate := range candidates {
		result.LookedFor = append(result.LookedFor, candidate.ConfigDir)
	}

	homeAvailable := true
	if strings.TrimSpace(home) == "" {
		homeAvailable = false
		result.Problems = append(result.Problems, "home directory is missing")
	} else if info, err := os.Stat(home); err != nil {
		homeAvailable = false
		if errors.Is(err, os.ErrNotExist) {
			result.Problems = append(result.Problems, fmt.Sprintf("home directory does not exist: %s", home))
		} else {
			result.Problems = append(result.Problems, fmt.Sprintf("cannot probe home directory %s: %v", home, err))
		}
	} else if !info.IsDir() {
		homeAvailable = false
		result.Problems = append(result.Problems, fmt.Sprintf("home path is not a directory: %s", home))
	}

	for _, candidate := range candidates {
		if !homeAvailable && !(claudeOverride && candidate.Name == "claude") {
			continue
		}
		info, err := os.Stat(candidate.ConfigDir)
		switch {
		case errors.Is(err, os.ErrNotExist) && claudeOverride && candidate.Name == "claude":
			result.Hosts = append(result.Hosts, candidate)
		case errors.Is(err, os.ErrNotExist):
			continue
		case err != nil:
			result.Problems = append(result.Problems, fmt.Sprintf("cannot probe %s config dir %s: %v", candidate.Name, candidate.ConfigDir, err))
		case !info.IsDir():
			result.Problems = append(result.Problems, fmt.Sprintf("%s config path is not a directory: %s", candidate.Name, candidate.ConfigDir))
		default:
			result.Hosts = append(result.Hosts, candidate)
		}
	}
	if len(result.Hosts) == 0 {
		result.Problems = append(result.Problems, fmt.Sprintf("no installed hosts found; looked for: %s", strings.Join(result.LookedFor, ", ")))
	}
	return result
}

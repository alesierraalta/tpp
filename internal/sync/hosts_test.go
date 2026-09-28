package sync

import (
	"os"
	"path/filepath"
	"testing"
)

func TestDiscoverySkipsAHostWithNoConfigDir(t *testing.T) {
	home := t.TempDir()
	for _, dir := range []string{".claude", ".gemini"} {
		if err := os.Mkdir(filepath.Join(home, dir), 0o755); err != nil {
			t.Fatal(err)
		}
	}

	discovery := DiscoverHosts(home, "")
	if got := hostNames(discovery.Hosts); len(got) != 2 || got[0] != "claude" || got[1] != "gemini" {
		t.Fatalf("discovered hosts = %v, want claude and gemini", got)
	}
}

func TestDiscoveryFindsTheFourKnownHosts(t *testing.T) {
	home := t.TempDir()
	for _, dir := range []string{".claude", filepath.Join(".config", "opencode"), ".gemini", ".codex"} {
		if err := os.MkdirAll(filepath.Join(home, dir), 0o755); err != nil {
			t.Fatal(err)
		}
	}

	discovery := DiscoverHosts(home, "")
	if got := hostNames(discovery.Hosts); len(got) != 4 || got[0] != "claude" || got[1] != "opencode" || got[2] != "gemini" || got[3] != "codex" {
		t.Fatalf("discovered hosts = %v, want all four known hosts", got)
	}
	wantSkills := map[string]string{
		"claude":   filepath.Join(home, ".claude", "skills"),
		"opencode": filepath.Join(home, ".config", "opencode", "skills"),
		"gemini":   filepath.Join(home, ".gemini", "skills"),
		"codex":    filepath.Join(home, ".codex", "skills"),
	}
	for _, host := range discovery.Hosts {
		if host.SkillsDir != wantSkills[host.Name] {
			t.Errorf("%s skills dir = %s, want %s", host.Name, host.SkillsDir, wantSkills[host.Name])
		}
	}
}

func hostNames(hosts []Host) []string {
	names := make([]string, 0, len(hosts))
	for _, host := range hosts {
		names = append(names, host.Name)
	}
	return names
}

// Pi is opt-in: discovery never offers it, and the constructor names the extension directory
// Pi actually loads (<config>/agent/extensions/<component>/index.ts).
func TestDiscoveryNeverOffersPiAndPiHostFollowsTheExtensionConvention(t *testing.T) {
	home := t.TempDir()
	if err := os.MkdirAll(filepath.Join(home, ".pi", "agent", "extensions"), 0o755); err != nil {
		t.Fatal(err)
	}
	for _, host := range DiscoverHosts(home, "").Hosts {
		if host.Name == "pi" {
			t.Fatal("discovery must not offer the opt-in pi host")
		}
	}

	host := PiHost(home)
	if host.Name != "pi" || host.ConfigDir != filepath.Join(home, ".pi") || host.SkillsDir != filepath.Join(home, ".pi", "agent", "extensions") {
		t.Fatalf("PiHost(home) = %+v, want the .pi config dir with agent/extensions as the managed root", host)
	}
}

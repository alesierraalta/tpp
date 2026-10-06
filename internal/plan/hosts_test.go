package plan

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// The host adapters are shipped files, not prose. A wiring that does not parse, or that calls a
// command the binary does not have, is a hook that never answers.
func TestShippedHostAdaptersAreUsable(t *testing.T) {
	root := filepath.Join("..", "..", "assets", "hosts")
	if _, err := os.Stat(root); err != nil {
		t.Skip("no shipped host adapters beside the module")
	}
	pi, err := os.ReadFile(filepath.Join(root, "pi", "settings.stop-hook.json"))
	if err != nil {
		t.Fatal(err)
	}
	var settings struct {
		Hooks map[string][]struct {
			Hooks []struct {
				Type    string `json:"type"`
				Command string `json:"command"`
			} `json:"hooks"`
		} `json:"hooks"`
	}
	if err := json.Unmarshal(pi, &settings); err != nil {
		t.Fatalf("pi settings are not JSON: %v", err)
	}
	stop := settings.Hooks["Stop"]
	if len(stop) != 1 || len(stop[0].Hooks) != 1 {
		t.Fatalf("pi settings must wire exactly one Stop hook: %+v", stop)
	}
	if got := stop[0].Hooks[0].Command; got != "tpp gate" {
		t.Fatalf("pi hook command = %q; the binary needs its subcommand", got)
	}
	oc, err := os.ReadFile(filepath.Join(root, "opencode", "tsp.ts"))
	if err != nil {
		t.Fatal(err)
	}
	body := string(oc)
	// Fail-closed: with no session binding, an automatic repo-wide check has no
	// session-scoped evidence behind it. The shipped plugin must not run one, and must not
	// push a toast or a prompt at the user on its own.
	for _, banned := range []string{"appendPrompt", "showToast", "tpp check"} {
		if strings.Contains(body, banned) {
			t.Fatalf("the opencode plugin must not contain %q: automatic notices are disabled", banned)
		}
	}
	// The adapter export stays so an already-copied plugin file keeps loading as a plugin.
	if !strings.Contains(body, "export const TPP") {
		t.Fatal("the opencode plugin must keep exporting TPP")
	}
	readme, err := os.ReadFile(filepath.Join(root, "README.md"))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(readme), "not run here") {
		t.Fatal("the host table must say which adapters were never executed")
	}
}

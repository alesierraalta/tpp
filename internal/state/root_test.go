package state

import (
	"os"
	"path/filepath"
	"testing"
)

// useConfigDir clears both home overrides and points the XDG config directory at a throwaway one, so
// Path resolves the default root the way it does on a machine with no override set.
func useConfigDir(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	t.Setenv("TSP_HOME", "")
	t.Setenv("TPP_HOME", "")
	t.Setenv("RDD_PLUS_HOME", "")
	t.Setenv("XDG_CONFIG_HOME", dir)
	return dir
}

func writeFile(t *testing.T, path, body string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
}

func TestHomeEnvironmentPrecedence(t *testing.T) {
	tsp, tpp, rddPlus := t.TempDir(), t.TempDir(), t.TempDir()
	t.Setenv("TSP_HOME", tsp)
	t.Setenv("TPP_HOME", tpp)
	t.Setenv("RDD_PLUS_HOME", rddPlus)

	for _, root := range []string{tsp, tpp, rddPlus} {
		got, err := Path()
		if err != nil {
			t.Fatal(err)
		}
		if want := filepath.Join(root, "state.json"); got != want {
			t.Fatalf("Path() = %q, want %q", got, want)
		}
		if root == tsp {
			t.Setenv("TSP_HOME", "")
		} else if root == tpp {
			t.Setenv("TPP_HOME", "")
		}
	}
}

// An rdd-plus-only installation moves whole and retains aliases for both older root names.
func TestTheLegacyConfigRootMovesOnce(t *testing.T) {
	dir := useConfigDir(t)
	writeFile(t, filepath.Join(dir, "rdd-plus", "state.json"), `{"schemaVersion":1}`)
	writeFile(t, filepath.Join(dir, "rdd-plus", "backups", "b1", "settings.json"), `{}`)

	got, err := Path()
	if err != nil {
		t.Fatal(err)
	}
	if want := filepath.Join(dir, "tsp", "state.json"); got != want {
		t.Fatalf("Path() = %q, want %q", got, want)
	}
	if _, err := os.Stat(filepath.Join(dir, "tsp", "backups", "b1", "settings.json")); err != nil {
		t.Fatalf("backups did not move with the root: %v", err)
	}
	for _, alias := range []string{"rdd-plus", "tpp"} {
		path := filepath.Join(dir, alias)
		if info, err := os.Lstat(path); err != nil || info.Mode()&os.ModeSymlink == 0 {
			t.Fatalf("legacy root %q is not a symlink: %v %v", path, info, err)
		}
		if body, err := os.ReadFile(filepath.Join(path, "state.json")); err != nil || string(body) != `{"schemaVersion":1}` {
			t.Fatalf("state read through %q = %q, %v", path, body, err)
		}
	}
}

func TestTppRootMovesWithCacheAndRepairsRddPlusAliasChain(t *testing.T) {
	dir := useConfigDir(t)
	tpp := filepath.Join(dir, "tpp")
	writeFile(t, filepath.Join(tpp, "state.json"), `{"schemaVersion":1}`)
	writeFile(t, filepath.Join(tpp, "backups", "b1", "settings.json"), `{}`)
	writeFile(t, filepath.Join(tpp, "update-check.json"), `cached`)
	if err := os.Symlink("tpp", filepath.Join(dir, "rdd-plus")); err != nil {
		t.Fatal(err)
	}

	got, err := Path()
	if err != nil {
		t.Fatal(err)
	}
	if want := filepath.Join(dir, "tsp", "state.json"); got != want {
		t.Fatalf("Path() = %q, want %q", got, want)
	}
	for _, relative := range []string{"state.json", "backups/b1/settings.json", "update-check.json"} {
		if _, err := os.Stat(filepath.Join(dir, "tsp", relative)); err != nil {
			t.Fatalf("migrated tree missing %q: %v", relative, err)
		}
	}
	if body, err := os.ReadFile(filepath.Join(dir, "rdd-plus", "state.json")); err != nil || string(body) != `{"schemaVersion":1}` {
		t.Fatalf("rdd-plus-to-tpp legacy chain is dangling: %q, %v", body, err)
	}
}

// Once the new root exists it is the only one used: a legacy root left beside it is never merged in.
func TestAnExistingTspRootWinsWithoutMergingLegacyRoots(t *testing.T) {
	dir := useConfigDir(t)
	writeFile(t, filepath.Join(dir, "rdd-plus", "state.json"), `{"schemaVersion":1,"installedVersion":"old-rdd"}`)
	writeFile(t, filepath.Join(dir, "tpp", "state.json"), `{"schemaVersion":1,"installedVersion":"old-tpp"}`)
	writeFile(t, filepath.Join(dir, "tsp", "state.json"), `{"schemaVersion":1,"installedVersion":"new"}`)

	got, err := Path()
	if err != nil {
		t.Fatal(err)
	}
	if want := filepath.Join(dir, "tsp", "state.json"); got != want {
		t.Fatalf("Path() = %q, want %q", got, want)
	}
	for path, want := range map[string]string{
		filepath.Join(dir, "rdd-plus", "state.json"): `{"schemaVersion":1,"installedVersion":"old-rdd"}`,
		filepath.Join(dir, "tpp", "state.json"):      `{"schemaVersion":1,"installedVersion":"old-tpp"}`,
	} {
		if body, err := os.ReadFile(path); err != nil || string(body) != want {
			t.Fatalf("legacy state at %q changed: %q, %v", path, body, err)
		}
	}
}

func TestDivergentLegacyRootsAreNotMergedOrOverwritten(t *testing.T) {
	dir := useConfigDir(t)
	writeFile(t, filepath.Join(dir, "tpp", "state.json"), `{"schemaVersion":1,"installedVersion":"old-tpp"}`)
	writeFile(t, filepath.Join(dir, "rdd-plus", "state.json"), `{"schemaVersion":1,"installedVersion":"old-rdd"}`)

	if _, err := Path(); err == nil {
		t.Fatal("Path() accepted divergent legacy roots")
	}
	for path := range map[string]bool{"tpp": true, "rdd-plus": true} {
		if _, err := os.Stat(filepath.Join(dir, path, "state.json")); err != nil {
			t.Fatalf("legacy root %q was not preserved: %v", path, err)
		}
	}
	if _, err := os.Lstat(filepath.Join(dir, "tsp")); !os.IsNotExist(err) {
		t.Fatalf("ambiguous migration created a new root: %v", err)
	}
}

func TestLegacySymlinkOutsideConfigTreeIsNotMoved(t *testing.T) {
	dir := useConfigDir(t)
	external := t.TempDir()
	writeFile(t, filepath.Join(external, "state.json"), `{"schemaVersion":1}`)
	link := filepath.Join(dir, "tpp")
	if err := os.Symlink(external, link); err != nil {
		t.Fatal(err)
	}

	if _, err := Path(); err == nil {
		t.Fatal("Path() accepted a legacy symlink instead of migrating a directory")
	}
	if body, err := os.ReadFile(filepath.Join(external, "state.json")); err != nil || string(body) != `{"schemaVersion":1}` {
		t.Fatalf("external state was changed: %q, %v", body, err)
	}
	if target, err := os.Readlink(link); err != nil || target != external {
		t.Fatalf("legacy symlink changed: %q, %v", target, err)
	}
}

// A move that cannot happen must not strand the installation: the old root stays the one read and written.
func TestAFailedMoveKeepsUsingTheLegacyRoot(t *testing.T) {
	if os.Geteuid() == 0 {
		t.Skip("root ignores directory permissions")
	}
	dir := useConfigDir(t)
	writeFile(t, filepath.Join(dir, "rdd-plus", "state.json"), `{"schemaVersion":1}`)
	if err := os.Chmod(dir, 0o500); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chmod(dir, 0o700) })

	got, err := Path()
	if err != nil {
		t.Fatal(err)
	}
	if want := filepath.Join(dir, "rdd-plus", "state.json"); got != want {
		t.Fatalf("Path() = %q, want the legacy %q", got, want)
	}
	if body, err := os.ReadFile(got); err != nil || string(body) != `{"schemaVersion":1}` {
		t.Fatalf("legacy state = %q, %v", body, err)
	}
}

func TestAFreshMachineResolvesTheNewRoot(t *testing.T) {
	dir := useConfigDir(t)
	got, err := Path()
	if err != nil {
		t.Fatal(err)
	}
	if want := filepath.Join(dir, "tsp", "state.json"); got != want {
		t.Fatalf("Path() = %q, want %q", got, want)
	}
}

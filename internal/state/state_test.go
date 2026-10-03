package state

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"sync"
	"testing"
)

func useHome(t *testing.T) string {
	t.Helper()
	home := filepath.Join(t.TempDir(), "tpp")
	t.Setenv("TSP_HOME", "")
	t.Setenv("TPP_HOME", home)
	return home
}

func TestSaveIsIdempotent(t *testing.T) {
	useHome(t)
	value := &State{
		SchemaVersion:    1,
		InstalledVersion: "1.2.3",
		Channel:          "stable",
		UpdatedAt:        "2025-01-02T03:04:05Z",
	}
	wrote, err := value.Save()
	if err != nil {
		t.Fatalf("first Save returned an error: %v", err)
	}
	if !wrote {
		t.Fatalf("first Save reported wrote = %v, want true", wrote)
	}
	path, err := Path()
	if err != nil {
		t.Fatalf("Path returned an error: %v", err)
	}
	before, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read saved state: %v", err)
	}
	wrote, err = value.Save()
	if err != nil {
		t.Fatalf("second Save returned an error: %v", err)
	}
	if wrote {
		t.Fatalf("second Save reported wrote = %v, want false", wrote)
	}
	after, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read idempotent state: %v", err)
	}
	if !bytes.Equal(after, before) {
		t.Fatalf("second Save changed state bytes: before %q, after %q", before, after)
	}
}

func TestLoadMissingReturnsAnEmptyState(t *testing.T) {
	useHome(t)
	got, err := Load()
	if err != nil {
		t.Fatalf("Load returned an error for a missing state: %v", err)
	}
	want := &State{SchemaVersion: 1}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("Load missing state = %+v, want %+v", got, want)
	}
}

func TestLoadFailsClosedOnACorruptState(t *testing.T) {
	home := useHome(t)
	if err := os.MkdirAll(home, 0o700); err != nil {
		t.Fatalf("create state directory: %v", err)
	}
	path, err := Path()
	if err != nil {
		t.Fatalf("Path returned an error: %v", err)
	}
	garbage := []byte("not json")
	if err := os.WriteFile(path, garbage, 0o600); err != nil {
		t.Fatalf("write corrupt state: %v", err)
	}
	if _, err := Load(); err == nil {
		t.Fatalf("Load accepted corrupt state bytes %q", garbage)
	}
	after, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read corrupt state after Load: %v", err)
	}
	if !bytes.Equal(after, garbage) {
		t.Fatalf("Load changed corrupt state bytes: got %q, want %q", after, garbage)
	}
}

func TestSaveIsPrivateAndAtomic(t *testing.T) {
	home := useHome(t)
	first := &State{SchemaVersion: 1, InstalledVersion: "first"}
	if wrote, err := first.Save(); err != nil || !wrote {
		t.Fatalf("initial Save = (%v, %v), want (true, nil)", wrote, err)
	}
	path, err := Path()
	if err != nil {
		t.Fatalf("Path returned an error: %v", err)
	}
	checkPermissions := func() {
		t.Helper()
		info, err := os.Stat(filepath.Dir(path))
		if err != nil {
			t.Fatalf("stat state directory: %v", err)
		}
		if got := info.Mode().Perm(); got != 0o700 {
			t.Fatalf("state directory mode = %o, want 700", got)
		}
		info, err = os.Stat(path)
		if err != nil {
			t.Fatalf("stat state file: %v", err)
		}
		if got := info.Mode().Perm(); got != 0o600 {
			t.Fatalf("state file mode = %o, want 600", got)
		}
	}
	checkPermissions()

	values := []*State{
		{SchemaVersion: 1, InstalledVersion: "left"},
		{SchemaVersion: 1, InstalledVersion: "right"},
	}
	errs := make(chan error, len(values))
	readErrs := make(chan error, 1)
	stopReading := make(chan struct{})
	var readers sync.WaitGroup
	readers.Add(1)
	go func() {
		defer readers.Done()
		for {
			select {
			case <-stopReading:
				return
			default:
			}
			data, err := os.ReadFile(path)
			if err != nil {
				select {
				case readErrs <- err:
				default:
				}
				return
			}
			var decoded State
			if err := json.Unmarshal(data, &decoded); err != nil {
				select {
				case readErrs <- err:
				default:
				}
				return
			}
		}
	}()
	var group sync.WaitGroup
	for _, value := range values {
		group.Add(1)
		go func(value *State) {
			defer group.Done()
			_, err := value.Save()
			errs <- err
		}(value)
	}
	group.Wait()
	close(stopReading)
	readers.Wait()
	close(errs)
	for err := range errs {
		if err != nil {
			t.Fatalf("concurrent Save returned an error: %v", err)
		}
	}
	select {
	case err := <-readErrs:
		t.Fatalf("reader observed an unparseable state during concurrent Save: %v", err)
	default:
	}
	checkPermissions()

	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read concurrently saved state: %v", err)
	}
	var decoded State
	if err := json.Unmarshal(data, &decoded); err != nil {
		t.Fatalf("concurrently saved bytes %q are not JSON: %v", data, err)
	}
	entries, err := os.ReadDir(home)
	if err != nil {
		t.Fatalf("read state directory: %v", err)
	}
	if len(entries) != 1 || entries[0].Name() != "state.json" {
		t.Fatalf("state directory entries = %v, want only state.json", entries)
	}
}

func TestPathHonoursTheLegacyHomeVariable(t *testing.T) {
	t.Setenv("TPP_HOME", "")
	home := filepath.Join(t.TempDir(), "chosen-home")
	t.Setenv("RDD_PLUS_HOME", home)
	got, err := Path()
	if err != nil {
		t.Fatalf("Path returned an error: %v", err)
	}
	want := filepath.Join(home, "state.json")
	if got != want {
		t.Fatalf("Path = %q, want %q", got, want)
	}
}

func TestRoundTripPreservesHostsAndFeatures(t *testing.T) {
	useHome(t)
	want := &State{
		SchemaVersion:    1,
		InstalledVersion: "2.0.0",
		Channel:          "beta",
		UpdatedAt:        "2025-02-03T04:05:06Z",
		Hosts: map[string]HostState{
			"claude": {
				ConfigDir:  "/home/test/.claude",
				Components: []string{"appsec-adversarial-auditor", "stop-gate"},
				Hook:       &HookState{Command: "/opt/tpp gate", Wired: true},
				Assets: map[string]AssetRecord{
					"skills/a/SKILL.md":   {SHA256: "aaa", Mode: 0o644, FromVersion: "1.0.0"},
					"skills/a/bin/run.sh": {SHA256: "bbb", Mode: 0o755, FromVersion: "1.1.0"},
				},
			},
			"opencode": {
				ConfigDir:  "/home/test/.config/opencode",
				Components: []string{"appsec-adversarial-auditor"},
			},
		},
		Features: map[string]FeatureState{
			"managed-assets": {Enabled: true, UpdatedAt: "2025-02-03T04:05:06Z"},
			"stop-gate":      {Enabled: false},
		},
	}
	if wrote, err := want.Save(); err != nil || !wrote {
		t.Fatalf("Save = (%v, %v), want (true, nil)", wrote, err)
	}
	got, err := Load()
	if err != nil {
		t.Fatalf("Load returned an error after round trip: %v", err)
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("round-trip state = %+v, want %+v", got, want)
	}
}

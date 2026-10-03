// Package state is what tpp actually installed: per host, which components and files, with the
// digest it wrote, plus the feature toggles that survive updates.
package state

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
)

// AssetRecord is one file we wrote for a host.
type AssetRecord struct {
	SHA256      string `json:"sha256"`
	Mode        uint32 `json:"mode"`
	FromVersion string `json:"fromVersion"`
}

// HookState records the Stop hook we wired.
type HookState struct {
	Command string `json:"command"`
	Wired   bool   `json:"wired"`
}

// HostState is one host's installation.
type HostState struct {
	ConfigDir  string                 `json:"configDir"`
	Components []string               `json:"components,omitempty"`
	Hook       *HookState             `json:"hook,omitempty"`
	Assets     map[string]AssetRecord `json:"assets,omitempty"`
}

// FeatureState is one optional feature's toggle.
type FeatureState struct {
	Enabled   bool   `json:"enabled"`
	UpdatedAt string `json:"updatedAt,omitempty"`
}

// State is the whole installation record.
type State struct {
	SchemaVersion    int                     `json:"schemaVersion"`
	InstalledVersion string                  `json:"installedVersion"`
	Channel          string                  `json:"channel,omitempty"`
	UpdatedAt        string                  `json:"updatedAt,omitempty"`
	Hosts            map[string]HostState    `json:"hosts,omitempty"`
	Features         map[string]FeatureState `json:"features,omitempty"`
}

// Path resolves <root>/state.json. Explicit roots use TSP_HOME, then TPP_HOME, then RDD_PLUS_HOME;
// the default is the XDG config directory's tsp folder. A legacy tpp or rdd-plus directory is moved
// there once as a whole tree, with both old paths retained as aliases.
func Path() (string, error) {
	root, err := resolveRoot()
	if err != nil {
		return "", err
	}
	return filepath.Join(root, "state.json"), nil
}

// resolveRoot answers the directory that holds state.json, its backups and the update cache; see Path.
func resolveRoot() (string, error) {
	for _, name := range []string{"TSP_HOME", "TPP_HOME", "RDD_PLUS_HOME"} {
		if root := os.Getenv(name); root != "" {
			return root, nil
		}
	}
	configDir, err := os.UserConfigDir()
	if err != nil {
		return "", fmt.Errorf("resolve XDG config directory: %w", err)
	}
	return migrateConfigRoot(configDir)
}

func migrateConfigRoot(configDir string) (string, error) {
	current := filepath.Join(configDir, "tsp")
	if _, err := os.Lstat(current); err == nil {
		info, statErr := os.Stat(current)
		if statErr != nil {
			return "", fmt.Errorf("inspect TSP state root %s: %w", current, statErr)
		}
		if !info.IsDir() {
			return "", fmt.Errorf("TSP state root %s is not a directory", current)
		}
		return current, nil
	} else if !errors.Is(err, os.ErrNotExist) {
		return "", fmt.Errorf("inspect TSP state root %s: %w", current, err)
	}

	legacyRoots := []string{filepath.Join(configDir, "tpp"), filepath.Join(configDir, "rdd-plus")}
	type legacyAlias struct {
		path string
		info os.FileInfo
	}
	var source string
	var sourceInfo os.FileInfo
	var aliases []legacyAlias
	for _, path := range legacyRoots {
		info, err := os.Lstat(path)
		if errors.Is(err, os.ErrNotExist) {
			continue
		}
		if err != nil {
			return "", fmt.Errorf("inspect legacy state root %s: %w", path, err)
		}
		if info.Mode()&os.ModeSymlink != 0 {
			target, err := os.Stat(path)
			if err != nil {
				return "", fmt.Errorf("inspect legacy state alias %s: %w", path, err)
			}
			if !target.IsDir() {
				return "", fmt.Errorf("legacy state alias %s does not target a directory", path)
			}
			aliases = append(aliases, legacyAlias{path: path, info: target})
			continue
		}
		if !info.IsDir() {
			return "", fmt.Errorf("legacy state root %s is not a directory", path)
		}
		if source != "" {
			return "", fmt.Errorf("ambiguous legacy state roots %s and %s; refusing to merge", source, path)
		}
		source, sourceInfo = path, info
	}
	if source == "" {
		if len(aliases) != 0 {
			return "", fmt.Errorf("legacy state root is only a symlink; refusing to move an alias instead of its contents")
		}
		return current, nil
	}
	for _, alias := range aliases {
		if !os.SameFile(sourceInfo, alias.info) {
			return "", fmt.Errorf("ambiguous legacy state roots %s and %s; refusing to merge", source, alias.path)
		}
	}

	if err := os.Rename(source, current); err != nil {
		return source, nil
	}
	if err := os.Symlink(current, source); err != nil {
		if rollbackErr := os.Rename(current, source); rollbackErr != nil {
			return "", fmt.Errorf("create legacy state alias %s: %v; restore legacy root: %w", source, err, rollbackErr)
		}
		return source, nil
	}
	currentInfo, err := os.Stat(current)
	if err != nil {
		return current, fmt.Errorf("inspect migrated state root %s: %w", current, err)
	}
	for _, alias := range legacyRoots {
		if alias == source {
			continue
		}
		info, err := os.Lstat(alias)
		if err == nil {
			if info.Mode()&os.ModeSymlink == 0 {
				return current, fmt.Errorf("legacy state root %s appeared during migration; refusing to overwrite", alias)
			}
			target, statErr := os.Stat(alias)
			if statErr != nil || !os.SameFile(currentInfo, target) {
				return current, fmt.Errorf("legacy state alias %s no longer points to the migrated root", alias)
			}
			continue
		}
		if !errors.Is(err, os.ErrNotExist) {
			return current, fmt.Errorf("inspect legacy state root %s: %w", alias, err)
		}
		if err := os.Symlink(current, alias); err != nil {
			return current, fmt.Errorf("create legacy state alias %s: %w", alias, err)
		}
	}
	return current, nil
}

// Load answers an empty state when the file is absent, and an error when it exists but cannot be
// trusted — a corrupt state must fail closed, never be silently replaced.
func Load() (*State, error) {
	path, err := Path()
	if err != nil {
		return nil, err
	}
	data, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		return &State{SchemaVersion: 1}, nil
	}
	if err != nil {
		return nil, fmt.Errorf("read state %s: %w", path, err)
	}
	state, err := decode(data, path)
	if err != nil {
		return nil, err
	}
	return state, nil
}

// Save serializes exactly the state it is given and writes only when the bytes would differ, so a
// second Save with the same value writes nothing and leaves the file byte-identical. It reports
// whether it wrote. Save never touches UpdatedAt: the caller decides that, because a no-op run must
// not churn the state.
func (s *State) Save() (bool, error) {
	if s == nil {
		return false, errors.New("cannot save a nil state")
	}
	if s.SchemaVersion != 1 {
		return false, fmt.Errorf("unsupported state schema version %d", s.SchemaVersion)
	}
	path, err := Path()
	if err != nil {
		return false, err
	}
	data, err := json.Marshal(s)
	if err != nil {
		return false, fmt.Errorf("marshal state: %w", err)
	}
	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return false, fmt.Errorf("create state directory %s: %w", dir, err)
	}
	if err := os.Chmod(dir, 0o700); err != nil {
		return false, fmt.Errorf("protect state directory %s: %w", dir, err)
	}

	existing, err := os.ReadFile(path)
	switch {
	case err == nil:
		if _, err := decode(existing, path); err != nil {
			return false, fmt.Errorf("refuse to replace untrusted state: %w", err)
		}
		if bytes.Equal(existing, data) {
			return false, nil
		}
	case !errors.Is(err, os.ErrNotExist):
		return false, fmt.Errorf("read existing state %s: %w", path, err)
	}
	if err := writeAtomic(path, data); err != nil {
		return false, err
	}
	return true, nil
}

func decode(data []byte, path string) (*State, error) {
	var state *State
	if err := json.Unmarshal(data, &state); err != nil {
		return nil, fmt.Errorf("state %s is corrupt: %w", path, err)
	}
	if state == nil {
		return nil, fmt.Errorf("state %s is corrupt: JSON null is not a state", path)
	}
	if state.SchemaVersion != 1 {
		return nil, fmt.Errorf("state %s has unsupported schema version %d", path, state.SchemaVersion)
	}
	return state, nil
}

func writeAtomic(path string, data []byte) error {
	dir := filepath.Dir(path)
	temporary, err := os.CreateTemp(dir, ".state-*.tmp")
	if err != nil {
		return fmt.Errorf("create temporary state file: %w", err)
	}
	temporaryPath := temporary.Name()
	defer func() { _ = os.Remove(temporaryPath) }()

	if err := temporary.Chmod(0o600); err != nil {
		_ = temporary.Close()
		return fmt.Errorf("protect temporary state file: %w", err)
	}
	if _, err := temporary.Write(data); err != nil {
		_ = temporary.Close()
		return fmt.Errorf("write temporary state file: %w", err)
	}
	if err := temporary.Sync(); err != nil {
		_ = temporary.Close()
		return fmt.Errorf("sync temporary state file: %w", err)
	}
	if err := temporary.Close(); err != nil {
		return fmt.Errorf("close temporary state file: %w", err)
	}
	if err := os.Rename(temporaryPath, path); err != nil {
		return fmt.Errorf("replace state %s: %w", path, err)
	}
	return nil
}

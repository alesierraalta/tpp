// Package manifest is the source of truth for what tpp may write: the components it can
// install and the payload behind each one, hashed from the embedded filesystem. A path that is not
// in here is never ours to touch.
package manifest

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io/fs"
	"sort"
	"strings"

	"github.com/alesierraalta/tpp/internal/assets"
)

// Kind classifies what a component installs.
type Kind string

const (
	KindSkill       Kind = "skill"
	KindHook        Kind = "hook"
	KindInstruction Kind = "instruction"
	// KindExtension is one host adapter file installed under the host's entry-point name
	// (Pi loads <extension-dir>/index.ts): opt-in per host, never a default.
	KindExtension Kind = "extension"
)

// skillHosts is the host set the skill tree installs into. A wildcard would also claim the
// opt-in Pi host, whose managed root is Pi's extension directory; naming the hosts keeps a
// pi-only sync to the extension and keeps skills out of extension directories.
var skillHosts = []string{"claude", "opencode", "gemini", "codex"}

// extensionEntry is the destination name inside an extension directory: the payload's embed
// name (tpp.ts) never decides where the host loads it from.
const extensionEntry = "index.ts"

// Component is one thing tpp can install.
type Component struct {
	ID      string
	Kind    Kind
	Source  string   // embed path for file-backed kinds (a skill root or a single adapter file), empty for a hook
	Hosts   []string // {"*"} for every host, otherwise explicit host names
	Default bool
}

// Components returns every component in a stable order.
func Components() []Component {
	entries, err := fs.ReadDir(assets.Skills(), ".")
	if err != nil {
		panic("embedded skills missing: " + err.Error())
	}

	components := make([]Component, 0, len(entries)+2)
	for _, entry := range entries {
		if !entry.IsDir() {
			continue
		}
		components = append(components, Component{
			ID:      entry.Name(),
			Kind:    KindSkill,
			Source:  "skills/" + entry.Name(),
			Hosts:   append([]string(nil), skillHosts...),
			Default: true,
		})
	}
	components = append(components, Component{
		ID:      "stop-gate",
		Kind:    KindHook,
		Hosts:   []string{"claude"},
		Default: true,
	})
	components = append(components, Component{
		ID:      "tpp",
		Kind:    KindExtension,
		Source:  "hosts/pi/tpp.ts",
		Hosts:   []string{"pi"},
		Default: false,
	})
	return components
}

// AppliesTo reports whether a component may be installed into a host; an empty host never matches.
func AppliesTo(host string, c Component) bool {
	if host == "" {
		return false
	}
	for _, allowed := range c.Hosts {
		if allowed == "*" || allowed == host {
			return true
		}
	}
	return false
}

// File is one payload bound for a destination, hashed from the embed FS.
type File struct {
	Component string // component id
	Source    string // path inside the embed FS
	Rel       string // destination relative to the component's own root
	SHA256    string // lowercase hex digest of the embedded bytes
	Size      int64
}

// Files returns the payload of every file-backed component, keyed by component id.
func Files() (map[string][]File, error) {
	files := make(map[string][]File)
	for _, component := range Components() {
		if component.Source == "" {
			continue
		}
		payload, err := filesFor(component)
		if err != nil {
			return nil, err
		}
		files[component.ID] = payload
	}
	return files, nil
}

// ComponentFiles returns the payload of one component, and an error when it has none (a hook).
func ComponentFiles(id string) ([]File, error) {
	for _, component := range Components() {
		if component.ID != id {
			continue
		}
		if component.Source == "" {
			return nil, fmt.Errorf("component %q has no payload", id)
		}
		return filesFor(component)
	}
	return nil, fmt.Errorf("component %q does not exist", id)
}

func filesFor(component Component) ([]File, error) {
	if component.Kind == KindExtension {
		// One embedded file, one destination: Pi loads <extension-dir>/index.ts, and the
		// writer resolves the same bytes through assets.Tree, so the digest spans both roots.
		fsys, rel, err := assets.Tree(component.Source)
		if err != nil {
			return nil, err
		}
		data, err := fs.ReadFile(fsys, rel)
		if err != nil {
			return nil, fmt.Errorf("read embedded extension %q: %w", component.Source, err)
		}
		digest := sha256.Sum256(data)
		return []File{{
			Component: component.ID,
			Source:    component.Source,
			Rel:       extensionEntry,
			SHA256:    hex.EncodeToString(digest[:]),
			Size:      int64(len(data)),
		}}, nil
	}
	var files []File
	skills := assets.Skills()
	err := fs.WalkDir(skills, component.ID, func(rel string, entry fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if entry.IsDir() {
			return nil
		}
		data, err := fs.ReadFile(skills, rel)
		if err != nil {
			return err
		}
		digest := sha256.Sum256(data)
		source := "skills/" + rel
		files = append(files, File{
			Component: component.ID,
			Source:    source,
			Rel:       strings.TrimPrefix(rel, component.ID+"/"),
			SHA256:    hex.EncodeToString(digest[:]),
			Size:      int64(len(data)),
		})
		return nil
	})
	if err != nil {
		return nil, fmt.Errorf("walk embedded component %q: %w", component.ID, err)
	}
	sort.Slice(files, func(i, j int) bool { return files[i].Source < files[j].Source })
	return files, nil
}

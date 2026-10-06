package sync

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"sort"
	"strings"
	"syscall"

	"github.com/alesierraalta/tsp/internal/hookcmd"
	"github.com/alesierraalta/tsp/internal/manifest"
	"github.com/alesierraalta/tsp/internal/state"
)

// ActionClass names what the synchronizer would do with one path.
type ActionClass string

const (
	ActionCreate   ActionClass = "create"    // absent on disk
	ActionUpdate   ActionClass = "update"    // ours, stale: safe to overwrite, with a backup
	ActionOK       ActionClass = "ok"        // already exactly right
	ActionModified ActionClass = "modified"  // our record says we wrote it, the disk says someone changed it
	ActionOrphan   ActionClass = "orphan"    // in state, no longer shipped by the manifest
	ActionForeign  ActionClass = "skip-user" // in the host's directory, claimed by neither
	ActionHook     ActionClass = "hook"      // the Stop gate needs a settings.json decision
)

// Action is one planned step. Nothing here writes anything.
type Action struct {
	Class     ActionClass
	Host      string
	Component string // component id; empty for foreign files
	Path      string // absolute destination; empty for the hook
	Reason    string // one line for --dry-run, naming what was compared
}

// PlanDeps injects disk access so the planner is testable without a filesystem.
type PlanDeps struct {
	// ListDir returns the entry names in a directory, sorted. A missing directory is an empty list,
	// not an error.
	ListDir func(path string) ([]string, error)
	// ReadFile returns a file's bytes. A missing file must be reported as missing, not as empty.
	ReadFile func(path string) ([]byte, error)
	// Resolve returns a directory's path with symlinks resolved, so the walk can tell a link back to an
	// enclosing folder from a new one. Nil disables cycle detection.
	Resolve func(path string) (string, error)
}

// PlanInput carries everything the planner needs; it never touches the disk itself.
type PlanInput struct {
	Components []manifest.Component
	Files      map[string][]manifest.File // keyed by component id
	State      *state.State
	Hosts      []Host
}

// BuildPlan decides every action for every host, deterministically ordered: hosts by name, then
// actions by path, then by class, so two runs over the same inputs produce identical plans.
func BuildPlan(in PlanInput, deps PlanDeps) ([]Action, error) {
	if deps.ListDir == nil || deps.ReadFile == nil {
		return nil, errors.New("plan dependencies must provide ListDir and ReadFile")
	}

	hosts := append([]Host(nil), in.Hosts...)
	sort.SliceStable(hosts, func(i, j int) bool {
		if hosts[i].Name != hosts[j].Name {
			return hosts[i].Name < hosts[j].Name
		}
		if hosts[i].ConfigDir != hosts[j].ConfigDir {
			return hosts[i].ConfigDir < hosts[j].ConfigDir
		}
		return hosts[i].SkillsDir < hosts[j].SkillsDir
	})

	var actions []Action
	for _, host := range hosts {
		hostActions, err := buildHostPlan(in, deps, host)
		if err != nil {
			return nil, fmt.Errorf("plan host %q: %w", host.Name, err)
		}
		actions = append(actions, hostActions...)
	}
	return actions, nil
}

type assetClaim struct {
	component string
	file      manifest.File
	path      string
}

func buildHostPlan(in PlanInput, deps PlanDeps, host Host) ([]Action, error) {
	claims, hook := manifestClaims(host, in.Components, in.Files)
	hostState := hostState(in.State, host.Name)
	actions := make([]Action, 0, len(claims)+len(hostState.Assets)+1)

	paths := make([]string, 0, len(claims))
	for path := range claims {
		paths = append(paths, path)
	}
	sort.Strings(paths)
	for _, path := range paths {
		claim := claims[path]
		record, recorded := hostState.Assets[path]
		action, err := planAsset(deps, host.Name, claim, record, recorded)
		if err != nil {
			return nil, err
		}
		actions = append(actions, action)
	}

	orphanPaths := make([]string, 0, len(hostState.Assets))
	for path := range hostState.Assets {
		if _, shipped := claims[path]; !shipped {
			orphanPaths = append(orphanPaths, path)
		}
	}
	sort.Strings(orphanPaths)
	for _, path := range orphanPaths {
		actions = append(actions, Action{
			Class:     ActionOrphan,
			Host:      host.Name,
			Component: orphanComponent(host, hostState, path),
			Path:      path,
			Reason:    fmt.Sprintf("state records %s, but the manifest no longer ships this path", hostState.Assets[path].SHA256),
		})
	}

	diskFiles, err := listFiles(host.SkillsDir, deps)
	if err != nil {
		return nil, err
	}
	for _, path := range diskFiles {
		if _, shipped := claims[path]; shipped {
			continue
		}
		if _, recorded := hostState.Assets[path]; recorded {
			continue
		}
		actions = append(actions, Action{
			Class:  ActionForeign,
			Host:   host.Name,
			Path:   path,
			Reason: "path is present in the managed directory but is claimed by neither the manifest nor state; skipping user file",
		})
	}

	if hook != "" && (!hookReady(hostState.Hook) || hookcmd.IsLegacyGate(hostState.Hook.Command)) {
		actions = append(actions, Action{
			Class:     ActionHook,
			Host:      host.Name,
			Component: hook,
			Reason:    hookReason(hostState.Hook),
		})
	}

	sortActions(actions)
	return actions, nil
}

func manifestClaims(host Host, components []manifest.Component, files map[string][]manifest.File) (map[string]assetClaim, string) {
	ordered := append([]manifest.Component(nil), components...)
	sort.SliceStable(ordered, func(i, j int) bool {
		if ordered[i].ID != ordered[j].ID {
			return ordered[i].ID < ordered[j].ID
		}
		return ordered[i].Kind < ordered[j].Kind
	})

	claims := make(map[string]assetClaim)
	hook := ""
	for _, component := range ordered {
		if !manifest.AppliesTo(host.Name, component) {
			continue
		}
		if component.Kind == manifest.KindHook {
			if hook == "" {
				hook = component.ID
			}
			continue
		}
		for _, file := range files[component.ID] {
			path := filepath.Join(host.SkillsDir, component.ID, filepath.FromSlash(file.Rel))
			claim := assetClaim{component: component.ID, file: file, path: path}
			if previous, exists := claims[path]; !exists || claimKey(claim) < claimKey(previous) {
				claims[path] = claim
			}
		}
	}
	return claims, hook
}

func claimKey(claim assetClaim) string {
	return claim.component + "\x00" + claim.file.Source + "\x00" + claim.file.Rel + "\x00" + claim.file.SHA256
}

func hostState(value *state.State, host string) state.HostState {
	if value == nil || value.Hosts == nil {
		return state.HostState{}
	}
	return value.Hosts[host]
}

func planAsset(deps PlanDeps, host string, claim assetClaim, record state.AssetRecord, recorded bool) (Action, error) {
	data, err := deps.ReadFile(claim.path)
	if errors.Is(err, os.ErrNotExist) {
		return Action{
			Class:     ActionCreate,
			Host:      host,
			Component: claim.component,
			Path:      claim.path,
			Reason:    "destination is absent; manifest file must be created",
		}, nil
	}
	if err != nil {
		return Action{}, fmt.Errorf("read destination %s: %w", claim.path, err)
	}

	digest := digestBytes(data)
	if digest == claim.file.SHA256 {
		return Action{
			Class:     ActionOK,
			Host:      host,
			Component: claim.component,
			Path:      claim.path,
			Reason:    fmt.Sprintf("disk digest %s matches manifest digest", digest),
		}, nil
	}
	if !recorded {
		return Action{
			Class:     ActionUpdate,
			Host:      host,
			Component: claim.component,
			Path:      claim.path,
			Reason:    fmt.Sprintf("disk digest %s differs from manifest digest %s; path is untracked and will be backed up first", digest, claim.file.SHA256),
		}, nil
	}
	if record.SHA256 == digest {
		return Action{
			Class:     ActionUpdate,
			Host:      host,
			Component: claim.component,
			Path:      claim.path,
			Reason:    fmt.Sprintf("disk digest %s differs from manifest digest %s; state record matches disk and it is safe to update with a backup", digest, claim.file.SHA256),
		}, nil
	}
	return Action{
		Class:     ActionModified,
		Host:      host,
		Component: claim.component,
		Path:      claim.path,
		Reason:    fmt.Sprintf("disk digest %s differs from manifest digest %s; state records %s, so the disk copy was modified", digest, claim.file.SHA256, record.SHA256),
	}, nil
}

func digestBytes(data []byte) string {
	digest := sha256.Sum256(data)
	return hex.EncodeToString(digest[:])
}

// listFiles walks a host's skills directory. Below the root every path is a candidate user path, so one
// that cannot be read, or a link back to an enclosing folder, is returned as a single path rather than
// failing the plan or being walked forever.
func listFiles(root string, deps PlanDeps) ([]string, error) {
	return walkFiles(root, nil, deps)
}

func walkFiles(path string, ancestors []string, deps PlanDeps) ([]string, error) {
	isRoot := ancestors == nil
	entries, err := deps.ListDir(path)
	if err != nil {
		files, err := fileIfReadable(path, deps, err)
		if err != nil && !isRoot {
			return []string{path}, nil
		}
		return files, err
	}
	if deps.Resolve != nil {
		if real, err := deps.Resolve(path); err == nil {
			if slices.Contains(ancestors, real) {
				return []string{path}, nil
			}
			ancestors = append(slices.Clip(ancestors), real)
		}
	}
	if ancestors == nil {
		ancestors = []string{}
	}
	if len(entries) == 0 {
		files, err := fileIfEmptyDirectory(path, deps)
		if err != nil && !isRoot {
			return []string{path}, nil
		}
		return files, err
	}

	sort.Strings(entries)
	var files []string
	for _, entry := range entries {
		children, err := walkFiles(filepath.Join(path, entry), ancestors, deps)
		if err != nil {
			return nil, err
		}
		files = append(files, children...)
	}
	return files, nil
}

func fileIfReadable(path string, deps PlanDeps, listErr error) ([]string, error) {
	if _, err := deps.ReadFile(path); err == nil {
		return []string{path}, nil
	} else if errors.Is(err, os.ErrNotExist) && errors.Is(listErr, os.ErrNotExist) {
		return nil, nil
	}
	return nil, fmt.Errorf("inspect managed path %s: list: %v", path, listErr)
}

func fileIfEmptyDirectory(path string, deps PlanDeps) ([]string, error) {
	if _, err := deps.ReadFile(path); err == nil {
		return []string{path}, nil
	} else if errors.Is(err, os.ErrNotExist) || isDirectoryError(err) {
		return nil, nil
	} else {
		return nil, fmt.Errorf("read managed path %s: %w", path, err)
	}
}

func isDirectoryError(err error) bool {
	return errors.Is(err, syscall.EISDIR)
}

func hookReady(hook *state.HookState) bool {
	return hook != nil && hook.Wired && strings.TrimSpace(hook.Command) != ""
}

func hookReason(hook *state.HookState) string {
	if hook == nil {
		return "state has no wired Stop hook; would wire it in settings.json"
	}
	if !hook.Wired {
		return "state records the Stop hook as not wired; would wire it in settings.json"
	}
	if hookcmd.IsLegacyGate(hook.Command) {
		return fmt.Sprintf("state records the Stop hook for the pre-rename binary (%s); would rewire it in settings.json", hook.Command)
	}
	return "state has no expected Stop hook command; would wire it in settings.json"
}

func orphanComponent(host Host, hostState state.HostState, path string) string {
	rel, err := filepath.Rel(host.SkillsDir, path)
	if err != nil || rel == "." || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
		return ""
	}
	component := strings.Split(rel, string(filepath.Separator))[0]
	for _, installed := range hostState.Components {
		if installed == component {
			return component
		}
	}
	return ""
}

func sortActions(actions []Action) {
	sort.SliceStable(actions, func(i, j int) bool {
		if actions[i].Path != actions[j].Path {
			return actions[i].Path < actions[j].Path
		}
		if actions[i].Class != actions[j].Class {
			return actions[i].Class < actions[j].Class
		}
		if actions[i].Component != actions[j].Component {
			return actions[i].Component < actions[j].Component
		}
		return actions[i].Reason < actions[j].Reason
	})
}

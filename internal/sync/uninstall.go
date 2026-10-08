package sync

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/alesierraalta/tsp/internal/manifest"
	"github.com/alesierraalta/tsp/internal/state"
)

// UninstallClass names what uninstall would do with one recorded path or the wired hook.
type UninstallClass string

const (
	UninstallRemove   UninstallClass = "remove"   // ours to delete: managed intact, forced modified, or orphan under Orphans
	UninstallModified UninstallClass = "modified" // disk differs from the state record; refuses without Force
	UninstallOrphan   UninstallClass = "orphan"   // recorded but no longer shipped; kept unless Orphans
	UninstallHook     UninstallClass = "hook"     // the Stop gate state records as wired
)

// UninstallOptions controls one uninstall run.
type UninstallOptions struct {
	DryRun  bool
	Orphans bool // also remove recorded paths the manifest no longer ships
	Force   bool // snapshot modified content to the central backup store, then remove it
	// ConfigDir, when non-empty, is the explicit Claude config directory for the hook; the
	// recorded host directory wins when the operator does not name one.
	ConfigDir string
}

// UninstallAction is one planned step; building the plan writes nothing.
type UninstallAction struct {
	Class  UninstallClass
	Host   string
	Path   string // settings.json for the hook, the recorded asset path otherwise
	Reason string
	// Backup marks a removal that must snapshot the file first because the disk copy
	// differs from what state recorded.
	Backup bool
}

// UninstallReport says what an uninstall did, or would do under DryRun.
type UninstallReport struct {
	DryRun          bool
	Actions         []UninstallAction
	Removed         []string
	BackedUp        map[string]string // asset path -> snapshot directory
	RemovedHooks    []string
	SettingsChanged bool
	StateWritten    bool

	hookDone bool
}

type uninstallPlan struct {
	report       UninstallReport
	blocked      []string
	settings     map[string]any
	settingsPath string
}

// Uninstall removes what this tool recorded as installed: the managed assets in state and the
// Stop gate it wired. A plain run removes the assets whose disk content still matches the state
// record and unwires the hook; paths the manifest no longer ships (the orphan class) are only
// removed with Orphans, and modified content only with Force, which snapshots it to the central
// backup store first exactly as sync --force does. DryRun writes nothing. Applying drops each
// host's records as its assets go and keeps Features, so a reinstall inherits the operator's
// opt-ins. Foreign paths are never recorded as ours, so they are never touched.
func Uninstall(opts UninstallOptions) (UninstallReport, error) {
	report := UninstallReport{DryRun: opts.DryRun, BackedUp: map[string]string{}}
	installationState, err := state.Load()
	if err != nil {
		return report, err
	}
	components := manifest.Components()
	files, err := manifest.Files()
	if err != nil {
		return report, err
	}
	plan, err := planUninstall(installationState, components, files, opts)
	if err != nil {
		return plan.report, err
	}
	if len(plan.blocked) > 0 {
		return plan.report, fmt.Errorf("%d recorded file(s) were modified after install; pass --force to back them up and remove them: %s",
			len(plan.blocked), strings.Join(plan.blocked, ", "))
	}
	if opts.DryRun {
		return plan.report, nil
	}
	stateDirty, err := applyUninstall(&plan, installationState)
	if err != nil {
		return plan.report, err
	}
	if stateDirty {
		installationState.UpdatedAt = time.Now().UTC().Format(time.RFC3339Nano)
		plan.report.StateWritten, err = installationState.Save()
		if err != nil {
			return plan.report, err
		}
	}
	return plan.report, nil
}

// planUninstall classifies every asset state recorded for every host, in a deterministic order
// (hosts by name, assets by path, then the hook), and reads settings.json when a gate is wired so
// apply never discovers a refusal or an unreadable file after it already deleted something.
func planUninstall(st *state.State, components []manifest.Component, files map[string][]manifest.File, opts UninstallOptions) (uninstallPlan, error) {
	plan := uninstallPlan{report: UninstallReport{DryRun: opts.DryRun, BackedUp: map[string]string{}}}
	hosts := make([]string, 0, len(st.Hosts))
	for name := range st.Hosts {
		hosts = append(hosts, name)
	}
	sort.Strings(hosts)
	for _, name := range hosts {
		hostState := st.Hosts[name]
		host := hostForConfig(name, hostState.ConfigDir)
		claims, _ := manifestClaims(host, components, files)
		paths := make([]string, 0, len(hostState.Assets))
		for path := range hostState.Assets {
			paths = append(paths, path)
		}
		sort.Strings(paths)
		for _, path := range paths {
			action, blocked, err := classifyUninstallAsset(path, hostState.Assets[path], claims, opts)
			if err != nil {
				return plan, err
			}
			if blocked {
				plan.blocked = append(plan.blocked, path)
			}
			action.Host = name
			plan.report.Actions = append(plan.report.Actions, action)
		}
		if hostState.Hook != nil && hostState.Hook.Wired {
			action, err := planUninstallHook(name, hostState, opts, &plan)
			if err != nil {
				return plan, err
			}
			plan.report.Actions = append(plan.report.Actions, action)
		}
	}
	return plan, nil
}

// classifyUninstallAsset decides one recorded path: managed content that still matches state is
// removed; modified content refuses without Force; orphans (no longer shipped) are removed only
// when Orphans asked for them. It reports whether the refusal blocks the whole run.
func classifyUninstallAsset(path string, record state.AssetRecord, claims map[string]assetClaim, opts UninstallOptions) (UninstallAction, bool, error) {
	data, err := os.ReadFile(path)
	absent := errors.Is(err, os.ErrNotExist)
	if err != nil && !absent {
		return UninstallAction{}, false, fmt.Errorf("read recorded asset %s: %w", path, err)
	}
	digest := ""
	if !absent {
		digest = digestBytes(data)
	}
	modified := digest != "" && digest != record.SHA256
	_, claimed := claims[path]
	action := UninstallAction{Path: path}

	switch {
	case !claimed && !opts.Orphans:
		action.Class = UninstallOrphan
		action.Reason = "state records this path, but the manifest no longer ships it; kept without --orphans"
		return action, false, nil
	case modified && !opts.Force:
		action.Class = UninstallModified
		action.Reason = fmt.Sprintf("disk digest %s differs from state record %s; refusing without --force", digest, record.SHA256)
		return action, true, nil
	default:
		action.Class = UninstallRemove
		action.Backup = modified
		switch {
		case absent:
			action.Reason = "recorded path is already absent; the state record will be dropped"
		case modified:
			action.Reason = fmt.Sprintf("disk digest %s differs from state record %s; --force snapshots it to the central backup store before removal", digest, record.SHA256)
		case !claimed:
			action.Reason = "state records this path, but the manifest no longer ships it; removed because --orphans was given"
		default:
			action.Reason = fmt.Sprintf("disk digest %s matches the state record; managed asset", digest)
		}
		return action, false, nil
	}
}

// planUninstallHook reads settings.json and simulates the unwire so DryRun sees the real plan and
// an unreadable settings file stops the run before anything is deleted.
func planUninstallHook(name string, hostState state.HostState, opts UninstallOptions, plan *uninstallPlan) (UninstallAction, error) {
	configDir := hostState.ConfigDir
	if name == "claude" && opts.ConfigDir != "" {
		configDir = opts.ConfigDir
	}
	settingsPath := filepath.Join(configDir, "settings.json")
	settings, _, err := loadSettings(settingsPath)
	if err != nil {
		return UninstallAction{}, err
	}
	changed, removed := unwireHook(settings)
	plan.settings = settings
	plan.settingsPath = settingsPath
	plan.report.SettingsChanged = changed
	plan.report.RemovedHooks = removed
	reason := fmt.Sprintf("state records the Stop hook as wired; would remove %s from %s", strings.Join(removed, ", "), settingsPath)
	if len(removed) == 0 {
		reason = fmt.Sprintf("state records the Stop hook as wired, but %s carries no tsp gate entry", settingsPath)
	}
	return UninstallAction{Class: UninstallHook, Host: name, Path: settingsPath, Reason: reason}, nil
}

// applyUninstall executes the plan: snapshot, delete, unwire, then report whether state must be
// saved. It runs only after every refusal has already stopped the run.
func applyUninstall(plan *uninstallPlan, st *state.State) (bool, error) {
	type cleanup struct {
		dropped     map[string]bool
		hookCleared bool
	}
	cleanups := map[string]*cleanup{}
	cleanupFor := func(host string) *cleanup {
		if cleanups[host] == nil {
			cleanups[host] = &cleanup{dropped: map[string]bool{}}
		}
		return cleanups[host]
	}
	var backups *backupStore

	for _, action := range plan.report.Actions {
		switch action.Class {
		case UninstallRemove:
			if action.Backup {
				if backups == nil {
					created, err := newBackupStore()
					if err != nil {
						return false, err
					}
					backups = created
				}
				if err := backups.Snapshot(action.Path); err != nil {
					return false, err
				}
				plan.report.BackedUp[action.Path] = backups.Dir()
			}
			if err := os.Remove(action.Path); err != nil && !errors.Is(err, os.ErrNotExist) {
				return false, fmt.Errorf("remove %s: %w", action.Path, err)
			}
			plan.report.Removed = append(plan.report.Removed, action.Path)
			cleanupFor(action.Host).dropped[action.Path] = true
		case UninstallHook:
			plan.report.hookDone = true
			if plan.report.SettingsChanged {
				out, err := marshalSettings(plan.settings)
				if err != nil {
					return false, err
				}
				if err := os.MkdirAll(filepath.Dir(plan.settingsPath), 0o755); err != nil {
					return false, err
				}
				if err := os.WriteFile(plan.settingsPath, out, 0o644); err != nil {
					return false, err
				}
			}
			cleanupFor(action.Host).hookCleared = true
		}
	}

	stateDirty := false
	for name, done := range cleanups {
		hostState, recorded := st.Hosts[name]
		if !recorded {
			continue
		}
		for path := range done.dropped {
			if _, ok := hostState.Assets[path]; ok {
				delete(hostState.Assets, path)
				stateDirty = true
			}
		}
		if done.hookCleared && hostState.Hook != nil {
			hostState.Hook = nil
			stateDirty = true
		}
		if len(hostState.Assets) == 0 && hostState.Hook == nil {
			delete(st.Hosts, name)
			stateDirty = true
			continue
		}
		st.Hosts[name] = hostState
	}
	return stateDirty, nil
}

// String renders the report for a terminal: a dry run and a refusal show the plan, an applied run
// shows the results.
func (r UninstallReport) String() string {
	var b strings.Builder
	prefix := ""
	if r.DryRun {
		prefix = "[dry-run] "
	}
	if len(r.Actions) == 0 {
		return prefix + "nothing to uninstall\n"
	}
	for _, action := range r.Actions {
		switch action.Class {
		case UninstallModified:
			fmt.Fprintf(&b, "%smodified: %s (refusing without --force)\n", prefix, action.Path)
		case UninstallOrphan:
			fmt.Fprintf(&b, "%sorphan: %s (kept without --orphans)\n", prefix, action.Path)
		case UninstallRemove:
			if containsString(r.Removed, action.Path) {
				if dir, ok := r.BackedUp[action.Path]; ok {
					fmt.Fprintf(&b, "%sremoved %s (snapshot at %s)\n", prefix, action.Path, dir)
				} else {
					fmt.Fprintf(&b, "%sremoved %s\n", prefix, action.Path)
				}
			} else {
				fmt.Fprintf(&b, "%s[remove] %s: %s\n", prefix, action.Path, action.Reason)
			}
		case UninstallHook:
			switch {
			case r.DryRun:
				fmt.Fprintf(&b, "%s[hook] %s: %s\n", prefix, action.Path, action.Reason)
			case r.hookDone && r.SettingsChanged:
				fmt.Fprintf(&b, "%sunwired Stop gate from %s (%s)\n", prefix, action.Path, strings.Join(r.RemovedHooks, ", "))
			case r.hookDone:
				fmt.Fprintf(&b, "%sStop gate already absent in %s\n", prefix, action.Path)
			default:
				fmt.Fprintf(&b, "%s[hook] %s: %s\n", prefix, action.Path, action.Reason)
			}
		}
	}
	if r.StateWritten {
		fmt.Fprintf(&b, "%sstate updated (hosts cleared, features kept)\n", prefix)
	}
	return b.String()
}

func containsString(values []string, want string) bool {
	for _, value := range values {
		if value == want {
			return true
		}
	}
	return false
}

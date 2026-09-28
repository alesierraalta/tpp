// Package sync installs the embedded skills into every discovered host and wires the gate
// as a Claude Stop hook, merging into settings.json without touching anything it does not own.
// Restore is the deliberate exception about existing content: it overwrites a destination with
// the snapshot bytes and takes no pre-restore backup of the current file, because the backup
// being applied is already the authority the operator chose.
package sync

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/alesierraalta/tpp/internal/assets"
	"github.com/alesierraalta/tpp/internal/buildinfo"
	"github.com/alesierraalta/tpp/internal/hookcmd"
	"github.com/alesierraalta/tpp/internal/manifest"
	"github.com/alesierraalta/tpp/internal/skilltree"
	"github.com/alesierraalta/tpp/internal/state"
)

// Options controls a sync run.
type Options struct {
	DryRun bool
	Force  bool
}

// HostReport says what sync did, or would do, for one host.
type HostReport struct {
	Host            Host
	Written         []string
	Unchanged       []string
	BackedUp        map[string]string
	RemovedHooks    []string
	SettingsPath    string
	SettingsRead    bool
	SettingsChanged bool
	Counts          map[ActionClass]int
	Actions         []Action
	Modified        []string
	Orphans         []string
	Foreign         []string
	Warnings        []string

	stateChanged bool
}

// Report says what a sync did, or would do under DryRun. The fields copied from the Claude
// host remain for callers of Sync; Hosts is the multi-host report used by the CLI.
type Report struct {
	ConfigDir    string
	DryRun       bool
	Written      []string
	Unchanged    []string
	BackedUp     map[string]string
	RemovedHooks []string
	SettingsPath string
	// SettingsRead is false when settings.json could not be read, and then the report says nothing about
	// wiring it: a report is printed before the error that explains a refusal, so a line claiming the hook
	// is `already wired` would be a sentence about a file nobody looked at.
	SettingsRead    bool
	SettingsChanged bool
	Counts          map[ActionClass]int
	Modified        []string
	Orphans         []string
	Foreign         []string
	StateWritten    bool

	Hosts           []HostReport
	LookedFor       []string
	DiscoveryErrors []string
	ClaudeConfigDir string
}

var documentedOnlyHosts = []string{"opencode", "gemini", "codex"}

// String renders the report for a terminal.
func (r Report) String() string {
	var b strings.Builder
	prefix := ""
	if r.DryRun {
		prefix = "[dry-run] "
	}
	if len(r.Hosts) == 0 {
		fmt.Fprintf(&b, "%sno installed hosts found\n", prefix)
		if len(r.LookedFor) > 0 {
			fmt.Fprintf(&b, "%slooked for host config dirs:\n", prefix)
			for _, path := range r.LookedFor {
				fmt.Fprintf(&b, "%s  %s\n", prefix, path)
			}
		}
	} else {
		for _, host := range r.Hosts {
			fmt.Fprintf(&b, "%shost: %s\n", prefix, host.Host.Name)
			fmt.Fprintf(&b, "%sconfig dir: %s\n", prefix, host.Host.ConfigDir)
			if len(host.Counts) > 0 {
				fmt.Fprintf(&b, "%splan: %s\n", prefix, formatCounts(host.Counts))
			}
			if r.DryRun {
				for _, action := range host.Actions {
					// Files that stay as they are only count: the plan line carries them, and a
					// skills directory full of the user's own files would bury the changes.
					if action.Class == ActionOK || action.Class == ActionForeign {
						continue
					}
					path := action.Path
					if path == "" {
						path = action.Component
					}
					fmt.Fprintf(&b, "%s[%s] %s: %s\n", prefix, action.Class, path, action.Reason)
				}
			} else {
				for _, s := range host.Written {
					if dir, ok := host.BackedUp[s]; ok {
						fmt.Fprintf(&b, "%sskill %-32s replaced (snapshot at %s)\n", prefix, s, dir)
					} else {
						fmt.Fprintf(&b, "%sskill %-32s written\n", prefix, s)
					}
				}
				if len(host.Unchanged) > 0 {
					fmt.Fprintf(&b, "%s%d skills unchanged\n", prefix, len(host.Unchanged))
				}
			}
			for _, path := range host.Modified {
				if optsForceHint(host.Warnings, path) {
					fmt.Fprintf(&b, "%smodified: %s (warning: skipped; use --force to replace)\n", prefix, path)
				} else {
					fmt.Fprintf(&b, "%smodified: %s\n", prefix, path)
				}
			}
			for _, path := range host.Orphans {
				fmt.Fprintf(&b, "%sorphan: %s (not removed)\n", prefix, path)
			}
			if len(host.Foreign) > 0 {
				fmt.Fprintf(&b, "%sskip-user: %d files not managed by tpp (left untouched)\n", prefix, len(host.Foreign))
			}
			for _, h := range host.RemovedHooks {
				fmt.Fprintf(&b, "%sremoved previous gate hook: %s\n", prefix, h)
			}
			if host.SettingsRead {
				if host.SettingsChanged {
					fmt.Fprintf(&b, "%ssettings: gate hook wired in %s\n", prefix, host.SettingsPath)
				} else {
					fmt.Fprintf(&b, "%ssettings: gate hook already wired in %s\n", prefix, host.SettingsPath)
				}
			}
		}
	}
	for _, problem := range r.DiscoveryErrors {
		fmt.Fprintf(&b, "%sdiscovery: %s\n", prefix, problem)
	}

	claudeRead := false
	claudePresent := false
	for _, host := range r.Hosts {
		if host.Host.Name == "claude" {
			claudePresent = true
			claudeRead = host.SettingsRead
			break
		}
	}
	if claudeRead || !claudePresent {
		claudeDir := r.ClaudeConfigDir
		if claudeDir == "" {
			claudeDir = r.ConfigDir
		}
		if claudeRead {
			fmt.Fprintf(&b, "%shook: Stop hook wired only in Claude config dir %s\n", prefix, claudeDir)
		} else {
			fmt.Fprintf(&b, "%shook: Stop hook not wired; Claude config dir %s was not discovered\n", prefix, claudeDir)
		}
		fmt.Fprintf(&b, "%shook transport: documented rather than wired for %s\n", prefix, strings.Join(documentedOnlyHosts, ", "))
	}
	return b.String()
}

// HookCommand is the exact command sync wires for a given binary path.
func HookCommand(binPath string) string {
	return fmt.Sprintf("%q gate", binPath)
}

// Sync installs every embedded skill under the Claude config directory and wires its Stop hook.
// It preserves the original single-directory API; the CLI uses SyncHosts after discovery.
func Sync(cfgDir, binPath string, opts Options) (Report, error) {
	return SyncHosts([]Host{{Name: "claude", ConfigDir: cfgDir, SkillsDir: filepath.Join(cfgDir, "skills")}}, binPath, opts)
}

// SyncHosts installs every embedded skill into each supplied host. Only the host named
// claude receives settings.json hook wiring; all other hosts receive skills only.
func SyncHosts(hosts []Host, binPath string, opts Options) (Report, error) {
	report := Report{DryRun: opts.DryRun, BackedUp: map[string]string{}, Counts: map[ActionClass]int{}}
	installationState, err := state.Load()
	if err != nil {
		return report, err
	}
	components := manifest.Components()
	files, err := manifest.Files()
	if err != nil {
		return report, err
	}
	var backups *backupStore
	if !opts.DryRun {
		backups, err = newBackupStore()
		if err != nil {
			return report, err
		}
	}
	stateDirty := false

	for _, host := range hosts {
		if host.Name == "claude" {
			report.ConfigDir = host.ConfigDir
			report.ClaudeConfigDir = host.ConfigDir
		}
		hostReport, syncErr := syncHost(host, binPath, opts, installationState, components, files, backups)
		report.Hosts = append(report.Hosts, hostReport)
		mergeReport(&report, hostReport)
		stateDirty = stateDirty || hostReport.stateChanged
		if host.Name == "claude" {
			report.ConfigDir = host.ConfigDir
			report.Written = hostReport.Written
			report.Unchanged = hostReport.Unchanged
			report.BackedUp = hostReport.BackedUp
			report.RemovedHooks = hostReport.RemovedHooks
			report.SettingsPath = hostReport.SettingsPath
			report.SettingsRead = hostReport.SettingsRead
			report.SettingsChanged = hostReport.SettingsChanged
		}
		if syncErr != nil {
			return report, syncErr
		}
	}
	if !opts.DryRun && len(report.Hosts) > 0 && stateDirty {
		installationState.UpdatedAt = time.Now().UTC().Format(time.RFC3339Nano)
		installationState.InstalledVersion = buildinfo.Version
		report.StateWritten, err = installationState.Save()
		if err != nil {
			return report, err
		}
	}
	return report, nil
}

func syncHost(host Host, binPath string, opts Options, installationState *state.State, components []manifest.Component, files map[string][]manifest.File, backups *backupStore) (HostReport, error) {
	report := HostReport{Host: host, BackedUp: map[string]string{}, Counts: map[ActionClass]int{}}
	var settings map[string]any
	var raw []byte
	var err error
	if host.Name == "claude" {
		report.SettingsPath = filepath.Join(host.ConfigDir, "settings.json")
		settings, raw, err = loadSettings(report.SettingsPath)
		if err != nil {
			return report, err
		}
		report.SettingsRead = true
	}
	deps := PlanDeps{
		ListDir: func(path string) ([]string, error) {
			entries, err := os.ReadDir(path)
			if err != nil {
				return nil, err
			}
			names := make([]string, 0, len(entries))
			for _, entry := range entries {
				names = append(names, entry.Name())
			}
			return names, nil
		},
		ReadFile: os.ReadFile,
	}
	if installationState.Hosts != nil {
		if current, ok := installationState.Hosts[host.Name]; ok && current.ConfigDir != host.ConfigDir {
			installationState.Hosts[host.Name] = state.HostState{}
			if !opts.DryRun {
				report.stateChanged = true
			}
		}
	}
	actions, err := BuildPlan(PlanInput{Components: components, Files: files, State: installationState, Hosts: []Host{host}}, deps)
	if err != nil {
		return report, err
	}
	payloads := payloadsForHost(host, files)
	if !opts.DryRun {
		report.stateChanged = adoptHost(installationState, host, components)
		if installationState.InstalledVersion != buildinfo.Version {
			installationState.InstalledVersion = buildinfo.Version
			report.stateChanged = true
		}
	}
	for _, action := range actions {
		report.Actions = append(report.Actions, action)
		report.Counts[action.Class]++
		if err := executeAction(action, host, binPath, opts, settings, raw, payloads, installationState, backups, &report); err != nil {
			return report, err
		}
	}
	return report, nil
}

func executeAction(action Action, host Host, binPath string, opts Options, settings map[string]any, raw []byte, payloads map[string]manifest.File, installationState *state.State, backups *backupStore, report *HostReport) error {
	switch action.Class {
	case ActionCreate, ActionUpdate:
		return applyAsset(action, host, opts, payloads, installationState, backups, report)
	case ActionModified:
		appendUniqueString(&report.Modified, action.Path)
		if !opts.Force {
			report.Warnings = append(report.Warnings, action.Path)
			return nil
		}
		return applyAsset(action, host, opts, payloads, installationState, backups, report)
	case ActionOK:
		appendUniqueString(&report.Unchanged, action.Component)
		if !opts.DryRun {
			changed, err := recordAsset(installationState, host.Name, action, payloads[action.Path])
			report.stateChanged = report.stateChanged || changed
			return err
		}
	case ActionOrphan:
		appendUniqueString(&report.Orphans, action.Path)
	case ActionForeign:
		appendUniqueString(&report.Foreign, action.Path)
	case ActionHook:
		if err := applyHook(host, binPath, opts, settings, raw, report); err != nil {
			return err
		}
		if !opts.DryRun {
			changed, err := recordHook(installationState, host.Name, HookCommand(binPath))
			report.stateChanged = report.stateChanged || changed
			return err
		}
	}
	return nil
}

func applyAsset(action Action, host Host, opts Options, payloads map[string]manifest.File, installationState *state.State, backups *backupStore, report *HostReport) error {
	payload, ok := payloads[action.Path]
	if !ok {
		return fmt.Errorf("no manifest payload for %s", action.Path)
	}
	if !opts.DryRun && (action.Class == ActionUpdate || action.Class == ActionModified) {
		if backups == nil {
			return fmt.Errorf("backup store is unavailable for %s", action.Path)
		}
		if err := backups.Snapshot(action.Path); err != nil {
			return err
		}
		report.BackedUp[action.Component] = backups.Dir()
	}
	if !opts.DryRun {
		fsys, rel, err := assets.Tree(payload.Source)
		if err != nil {
			return err
		}
		if err := writeSkillFile(fsys, rel, action.Path); err != nil {
			return err
		}
		changed, err := recordAssetValue(installationState, host.Name, action.Path, payload)
		if err != nil {
			return err
		}
		report.stateChanged = report.stateChanged || changed
	}
	if !opts.DryRun {
		report.stateChanged = true
	}
	appendUniqueString(&report.Written, action.Component)
	return nil
}

func payloadsForHost(host Host, files map[string][]manifest.File) map[string]manifest.File {
	payloads := make(map[string]manifest.File)
	for component, entries := range files {
		for _, payload := range entries {
			path := filepath.Join(host.SkillsDir, component, filepath.FromSlash(payload.Rel))
			payloads[path] = payload
		}
	}
	return payloads
}

func adoptHost(value *state.State, host Host, components []manifest.Component) bool {
	if value.Hosts == nil {
		value.Hosts = make(map[string]state.HostState)
	}
	current := value.Hosts[host.Name]
	applicable := applicableComponents(host.Name, components)
	changed := current.ConfigDir != host.ConfigDir || !sameStrings(current.Components, applicable)
	current.ConfigDir = host.ConfigDir
	current.Components = applicable
	if current.Assets == nil {
		current.Assets = make(map[string]state.AssetRecord)
		changed = true
	}
	value.Hosts[host.Name] = current
	return changed
}

func applicableComponents(host string, components []manifest.Component) []string {
	var ids []string
	for _, component := range components {
		if manifest.AppliesTo(host, component) {
			ids = append(ids, component.ID)
		}
	}
	return ids
}

func recordAsset(value *state.State, host string, action Action, payload manifest.File) (bool, error) {
	return recordAssetValue(value, host, action.Path, payload)
}

func recordAssetValue(value *state.State, host, path string, payload manifest.File) (bool, error) {
	hostState := value.Hosts[host]
	if hostState.Assets == nil {
		hostState.Assets = make(map[string]state.AssetRecord)
	}
	mode := uint32(payloadMode(payload.Source).Perm())
	if info, err := os.Stat(path); err == nil {
		mode = uint32(info.Mode().Perm())
	}
	desired := state.AssetRecord{SHA256: payload.SHA256, Mode: mode, FromVersion: buildinfo.Version}
	if existing, ok := hostState.Assets[path]; ok && existing == desired {
		return false, nil
	}
	hostState.Assets[path] = desired
	value.Hosts[host] = hostState
	return true, nil
}

func recordHook(value *state.State, host, command string) (bool, error) {
	hostState := value.Hosts[host]
	desired := &state.HookState{Command: command, Wired: true}
	if hostState.Hook != nil && *hostState.Hook == *desired {
		return false, nil
	}
	hostState.Hook = desired
	value.Hosts[host] = hostState
	return true, nil
}

func mergeReport(report *Report, host HostReport) {
	for class, count := range host.Counts {
		report.Counts[class] += count
	}
	report.Modified = appendUniqueValues(report.Modified, host.Modified)
	report.Orphans = appendUniqueValues(report.Orphans, host.Orphans)
	report.Foreign = appendUniqueValues(report.Foreign, host.Foreign)
}

func appendUniqueString(values *[]string, value string) {
	for _, existing := range *values {
		if existing == value {
			return
		}
	}
	*values = append(*values, value)
}

func appendUniqueValues(values []string, additions []string) []string {
	for _, value := range additions {
		appendUniqueString(&values, value)
	}
	return values
}

func sameStrings(left, right []string) bool {
	if len(left) != len(right) {
		return false
	}
	for i := range left {
		if left[i] != right[i] {
			return false
		}
	}
	return true
}

func formatCounts(counts map[ActionClass]int) string {
	ordered := []ActionClass{ActionCreate, ActionUpdate, ActionModified, ActionOK, ActionOrphan, ActionForeign, ActionHook}
	parts := make([]string, 0, len(ordered))
	for _, class := range ordered {
		if count := counts[class]; count > 0 {
			parts = append(parts, fmt.Sprintf("%s=%d", class, count))
		}
	}
	return strings.Join(parts, ", ")
}

func optsForceHint(warnings []string, path string) bool {
	for _, warning := range warnings {
		if warning == path {
			return true
		}
	}
	return false
}

// applyHook wires the Stop hook into the settings this run read and writes it only when the serialized bytes differ.
func applyHook(host Host, binPath string, opts Options, settings map[string]any, raw []byte, report *HostReport) error {
	if host.Name != "claude" {
		return nil
	}
	changed, removed, err := persistWiredHook(report.SettingsPath, settings, raw, HookCommand(binPath), opts.DryRun)
	report.RemovedHooks = removed
	report.SettingsChanged = changed
	return err
}

// RewireStopHook makes settings.json carry exactly the gate command for binPath: previous gate
// entries are dropped, the desired command is added when absent, and the file is written only
// when the serialized bytes differ. It exists for repair, which must fix a hook the planner
// cannot see because state still records it as wired; dryRun stops before the write.
func RewireStopHook(configDir, binPath string, dryRun bool) (changed bool, removed []string, err error) {
	settingsPath := filepath.Join(configDir, "settings.json")
	settings, raw, err := loadSettings(settingsPath)
	if err != nil {
		return false, nil, err
	}
	return persistWiredHook(settingsPath, settings, raw, HookCommand(binPath), dryRun)
}

// persistWiredHook wires command into settings and writes settingsPath only when the serialized
// bytes differ; dryRun stops before the write.
func persistWiredHook(settingsPath string, settings map[string]any, raw []byte, command string, dryRun bool) (changed bool, removed []string, err error) {
	_, removed = wireHook(settings, command)
	out, err := marshalSettings(settings)
	if err != nil {
		return false, removed, err
	}
	changed = raw == nil || !bytes.Equal(raw, out)
	if !changed || dryRun {
		return changed, removed, nil
	}
	if err := os.MkdirAll(filepath.Dir(settingsPath), 0o755); err != nil {
		return changed, removed, err
	}
	return changed, removed, os.WriteFile(settingsPath, out, 0o644)
}

// loadSettings returns the parsed settings, the raw bytes (nil when the file is absent),
// and an error when the file exists but is not a JSON object.
func loadSettings(path string) (map[string]any, []byte, error) {
	raw, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		return map[string]any{}, nil, nil
	}
	if err != nil {
		return nil, nil, err
	}
	var settings map[string]any
	if err := json.Unmarshal(raw, &settings); err != nil || settings == nil {
		return nil, nil, fmt.Errorf("%s is not a JSON object; nothing was written", path)
	}
	return settings, raw, nil
}

func marshalSettings(settings map[string]any) ([]byte, error) {
	var buf bytes.Buffer
	enc := json.NewEncoder(&buf)
	enc.SetEscapeHTML(false)
	enc.SetIndent("", "  ")
	if err := enc.Encode(settings); err != nil {
		return nil, err
	}
	return buf.Bytes(), nil
}

// identical reports whether every embedded file of the skill exists at target with the same
// bytes; files the user added next to them (run artifacts, notes) do not count as a difference.
//
// The comparison itself lives in `internal/skilltree`: the doctor asks the same question about the same tree,
// and an answer written twice is an answer that drifts. A tree this could not walk is an error here, so sync
// refuses to replace a copy it could not compare rather than overwriting one it never read.
func identical(skills fs.FS, name, target string) (bool, error) {
	return skilltree.Identical(skills, name, target)
}

func writeSkill(skills fs.FS, name, target string) error {
	return fs.WalkDir(skills, name, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		rel, _ := filepath.Rel(name, filepath.FromSlash(p))
		dst := filepath.Join(target, rel)
		if d.IsDir() {
			return os.MkdirAll(dst, 0o755)
		}
		return writeSkillFile(skills, p, dst)
	})
}

func writeSkillFile(fsys fs.FS, source, target string) error {
	data, err := fs.ReadFile(fsys, source)
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(target), 0o755); err != nil {
		return err
	}
	return os.WriteFile(target, data, payloadMode(source))
}

func payloadMode(source string) os.FileMode {
	if strings.HasSuffix(source, ".sh") || strings.HasSuffix(source, ".py") {
		return 0o755
	}
	return 0o644
}

// wireHook makes settings carry exactly one gate hook: previous gate entries (the Node hook,
// the pre-repo binary, or an older tpp or tpp path) are dropped, and the desired command is added
// only when absent. It returns whether settings changed and which commands were removed.
func wireHook(settings map[string]any, command string) (bool, []string) {
	hooks, _ := settings["hooks"].(map[string]any)
	if hooks == nil {
		hooks = map[string]any{}
		settings["hooks"] = hooks
	}
	changed, removed, present := filterStopGates(settings, func(cmd string) bool { return cmd == command })
	if present {
		return changed, removed
	}
	stop, _ := hooks["Stop"].([]any)
	stop = append(stop, map[string]any{
		"matcher": "",
		"hooks": []any{map[string]any{
			"type":          "command",
			"command":       command,
			"timeout":       30,
			"statusMessage": "Checking testing discipline...",
		}},
	})
	hooks["Stop"] = stop
	return true, removed
}

// unwireHook drops every tpp gate command from Stop and leaves every other hook alone; it
// answers whether settings changed and which commands it removed.
func unwireHook(settings map[string]any) (bool, []string) {
	changed, removed, _ := filterStopGates(settings, func(string) bool { return false })
	return changed, removed
}

// filterStopGates rewrites settings.hooks.Stop so that every gate command keep does not accept is
// dropped, every other hook is preserved, and an entry emptied by the drop goes away with it.
// keep decides which gate commands stay wired (the one being wired, or none for an unwire);
// present reports whether keep matched a command already there.
func filterStopGates(settings map[string]any, keep func(cmd string) bool) (changed bool, removed []string, present bool) {
	hooks, _ := settings["hooks"].(map[string]any)
	if hooks == nil {
		return false, nil, false
	}
	stop, _ := hooks["Stop"].([]any)
	kept := make([]any, 0, len(stop))
	for _, e := range stop {
		entry, _ := e.(map[string]any)
		if entry == nil {
			kept = append(kept, e)
			continue
		}
		list, _ := entry["hooks"].([]any)
		keptHooks := make([]any, 0, len(list))
		for _, h := range list {
			hook, _ := h.(map[string]any)
			cmd, _ := hook["command"].(string)
			switch {
			case keep(cmd):
				present = true
				keptHooks = append(keptHooks, h)
			case isPreviousGate(cmd):
				removed = append(removed, cmd)
				changed = true
			default:
				keptHooks = append(keptHooks, h)
			}
		}
		if len(keptHooks) == 0 && len(list) > 0 {
			continue // an entry that only carried a gate we dropped goes away with it
		}
		entry["hooks"] = keptHooks
		kept = append(kept, entry)
	}
	if changed {
		hooks["Stop"] = kept
	}
	return changed, removed, present
}

// isPreviousGate recognizes every earlier way the gate was wired: the Node hook, the pre-repo binary, and
// the gate subcommand of this binary under its current or its pre-rename name, wherever it was installed.
func isPreviousGate(cmd string) bool {
	c := strings.TrimSpace(cmd)
	c = strings.TrimSuffix(c, " gate")
	c = strings.Trim(c, "\"")
	return strings.HasSuffix(c, "/testing-gate.mjs") ||
		strings.HasSuffix(c, "/hooks/bin/testing-gate") ||
		hookcmd.IsGate(cmd)
}

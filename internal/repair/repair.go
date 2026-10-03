// Package repair brings a broken installation back to what doctor reports healthy: it
// re-syncs drifted or missing managed skills through the sync planner and re-wires the Stop
// hook when settings.json lost it or points at a different binary, never touching foreign or
// modified-user files without force.
package repair

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"github.com/alesierraalta/tsp/internal/doctor"
	"github.com/alesierraalta/tsp/internal/sync"
)

// Options controls a repair run.
type Options struct {
	DryRun    bool
	Force     bool
	ConfigDir string
}

// Report says what repair found, did, or would do. Problems is what the first doctor pass
// classified as repairable; Remaining is what the post-repair pass still sees (filled only on
// a real run). Sync carries the re-sync detail.
type Report struct {
	DryRun          bool
	ConfigDir       string
	Problems        []string
	AlreadyHealthy  bool
	SettingsChanged bool
	RemovedHooks    []string
	Sync            sync.Report
	Remaining       []string

	completed bool // the post-repair inspection ran; Remaining is final
}

// String renders the report for a terminal. An empty string means the run failed before it
// classified anything, so the caller prints only the error.
func (r Report) String() string {
	if r.AlreadyHealthy {
		return "already healthy\n"
	}
	if len(r.Problems) == 0 {
		return ""
	}
	var b strings.Builder
	prefix := ""
	if r.DryRun {
		prefix = "[dry-run] "
	}
	fmt.Fprintf(&b, "%srepair found %d problem(s):\n", prefix, len(r.Problems))
	for _, p := range r.Problems {
		fmt.Fprintf(&b, "%s  %s\n", prefix, p)
	}
	if r.SettingsChanged || len(r.RemovedHooks) > 0 {
		path := filepath.Join(r.ConfigDir, "settings.json")
		if r.DryRun {
			fmt.Fprintf(&b, "%sStop gate would be rewired in %s\n", prefix, path)
		} else {
			fmt.Fprintf(&b, "%sStop gate rewired in %s\n", prefix, path)
		}
	}
	for _, h := range r.RemovedHooks {
		fmt.Fprintf(&b, "%sremoved previous gate hook: %s\n", prefix, h)
	}
	b.WriteString(r.Sync.String())
	if r.completed && !r.DryRun {
		if len(r.Remaining) == 0 {
			fmt.Fprintf(&b, "after repair: healthy\n")
		} else {
			fmt.Fprintf(&b, "after repair: %d problem(s) remain:\n", len(r.Remaining))
			for _, p := range r.Remaining {
				fmt.Fprintf(&b, "  %s\n", p)
			}
		}
	}
	return b.String()
}

// Run inspects opts.ConfigDir with the doctor, re-wires the Stop hook when it is missing or
// points at a different binary, re-syncs drifted or missing managed skills through the sync
// planner, and on a real run re-inspects to report what remains. It runs against the single
// Claude host only — discovery multi-host is sync's job, not the doctor-guided fix's. A dry
// run classifies and plans but writes nothing.
func Run(opts Options) (Report, error) {
	binPath, err := os.Executable()
	if err != nil {
		return Report{}, err
	}
	binPath, err = filepath.Abs(binPath)
	if err != nil {
		return Report{}, err
	}
	desired := sync.HookCommand(binPath)
	report := Report{DryRun: opts.DryRun, ConfigDir: opts.ConfigDir}

	before := doctor.Run(opts.ConfigDir, exec.LookPath)
	report.Problems = classify(before, desired)
	if len(report.Problems) == 0 {
		report.AlreadyHealthy = true
		return report, nil
	}

	if !before.HookWired || before.HookCommand != desired {
		changed, removed, err := sync.RewireStopHook(opts.ConfigDir, binPath, opts.DryRun)
		report.SettingsChanged = changed
		report.RemovedHooks = removed
		if err != nil {
			return report, err
		}
	}

	syncReport, err := sync.Sync(opts.ConfigDir, binPath, sync.Options{DryRun: opts.DryRun, Force: opts.Force})
	report.Sync = syncReport
	if err != nil {
		return report, err
	}

	if !opts.DryRun {
		after := doctor.Run(opts.ConfigDir, exec.LookPath)
		report.Remaining = classify(after, desired)
		report.completed = true
	}
	return report, nil
}

// classify extracts the four problems repair fixes from a doctor pass: a missing skill, a
// drifted skill, an unwired Stop hook, and a Stop hook pointing at a different binary.
// doctor's own Problems list is not used — it also carries git/probe findings repair cannot
// fix — and BinariesDiffer is left out on purpose: rewiring to the same command changes nothing.
func classify(r doctor.Report, desired string) []string {
	var out []string
	for _, s := range r.Skills {
		switch {
		case !s.Installed:
			out = append(out, "skill not installed: "+s.Name)
		case !s.Matches:
			out = append(out, "skill differs from the embedded version: "+s.Name)
		}
	}
	switch {
	case !r.HookWired:
		out = append(out, "Stop hook not wired in settings.json")
	case r.HookCommand != desired:
		out = append(out, "Stop hook points at a different binary: "+r.HookCommand)
	}
	return out
}

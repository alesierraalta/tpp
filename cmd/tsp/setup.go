package main

import (
	"flag"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"github.com/alesierraalta/tsp/internal/assets"
	"github.com/alesierraalta/tsp/internal/doctor"
	"github.com/alesierraalta/tsp/internal/hookcmd"
	"github.com/alesierraalta/tsp/internal/mode"
	"github.com/alesierraalta/tsp/internal/sync"
)

// runSetup is the install in one command: the same sync as `tsp sync`, the same checks as `tsp doctor`,
// and a PATH check, ending on one line that says tsp is working (exit 0) or what is left (exit non-zero),
// so whoever runs it — a person or an agent — knows the install is done from the exit code and that line.
func runSetup(args []string) int {
	fs := flag.NewFlagSet("setup", flag.ContinueOnError)
	configDir := fs.String("config-dir", defaultConfigDir(), "Claude config directory")
	hostsFlag := fs.String("hosts", "", "comma-separated hosts to install into")
	modeFlag := fs.String("mode", mode.Auto, "harness mode: auto, standalone, or gentle")
	if err := fs.Parse(args); err != nil {
		return 2
	}
	// The mode is resolved before anything runs: a request this build cannot honor must stop
	// the command at the flag, never install under a name that would misreport the contract.
	runMode, err := mode.Resolve(*modeFlag, mode.VerifiedGentleSignal())
	if err != nil {
		fmt.Fprintln(os.Stderr, "setup:", err)
		return 2
	}
	selected, err := parseSyncHosts(*hostsFlag, flagSet(fs, "hosts"))
	if err != nil {
		fmt.Fprintln(os.Stderr, "setup:", err)
		return 2
	}

	code, discovery := syncInstall(*configDir, flagSet(fs, "config-dir"), selected, sync.Options{})
	if code != 0 {
		return code
	}

	hosts := make([]string, 0, len(discovery.Hosts))
	claude := false
	for _, host := range discovery.Hosts {
		hosts = append(hosts, host.Name)
		claude = claude || host.Name == "claude"
	}

	// The doctor reads the Claude config dir sync just wrote, not the environment's default, so the check
	// verifies the install that happened. Without Claude there is no hook to check, and the skill and hook
	// problems the doctor would name are not problems; only a missing required tool still is.
	report := doctorReport(discovery.ClaudeConfigDir, runMode)
	fmt.Println()
	problems := report.Problems
	if claude {
		fmt.Print(report.String())
	} else {
		fmt.Printf("tsp doctor · mode %s · no Claude Code host: skills and the Stop hook are not checked\n\ncapabilities\n", report.Mode)
		fmt.Print(report.CapabilitiesString())
		problems = missingRequiredTools(report)
	}
	if len(problems) > 0 {
		fmt.Printf("\nsetup: not finished: %s\n", strings.Join(problems, "; "))
		return 1
	}

	fmt.Println()
	if line := pathWarning(); line != "" {
		fmt.Println(line)
	}
	hook := "Stop hook not wired: Claude Code is not installed here, and the gate runs only there"
	if claude {
		hook = "Stop hook wired to " + hookBinary(report.HookCommand)
	}
	fmt.Printf("tsp is installed and working: %d skills in %s, %s\n", len(assets.SkillNames()), strings.Join(hosts, ", "), hook)
	return 0
}

// doctorReport runs the doctor's checks against configDir, probing the wired hook with a payload,
// under the mode the command resolved, so the report (text and JSON alike) names the contract
// its verdict came from.
func doctorReport(configDir, runMode string) doctor.Report {
	report := doctor.RunWith(configDir, exec.LookPath, probeHook)
	report.Mode = runMode
	return report
}

// hookBinary is the program the wired hook runs. The doctor resolves it only when a tpp is on PATH to
// compare against, so setup reads it from the command itself and falls back to the whole command.
func hookBinary(command string) string {
	if words, err := hookcmd.ShellWords(command); err == nil && len(words) > 0 {
		return words[0]
	}
	return command
}

func missingRequiredTools(report doctor.Report) []string {
	var missing []string
	for _, c := range report.Capabilities {
		if c.Required && !c.Present {
			missing = append(missing, "required tool missing: "+c.Name)
		}
	}
	return missing
}

// pathWarning names the line to add when the running binary's directory is not on PATH. It is a warning,
// not a failure: the Stop hook calls the absolute path, so only typing `tpp` in a shell is affected.
func pathWarning() string {
	dir, err := runningBinaryDir()
	if err != nil {
		return ""
	}
	for _, entry := range filepath.SplitList(os.Getenv("PATH")) {
		if entry == "" {
			continue
		}
		if resolved, err := filepath.EvalSymlinks(entry); err == nil {
			entry = resolved
		}
		if filepath.Clean(entry) == dir {
			return ""
		}
	}
	return fmt.Sprintf("setup: warning: %s is not on PATH, so `tsp` is not found in a shell; add it with:\n  export PATH=\"%s:$PATH\"", dir, dir)
}

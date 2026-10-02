// tpp is a deterministic companion for the testing discipline: it installs the skills,
// wires the Stop hook that keeps them invoked, and reports what the environment can do.
package main

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"time"

	"github.com/alesierraalta/tpp/internal/admit"
	"github.com/alesierraalta/tpp/internal/bench"
	"github.com/alesierraalta/tpp/internal/buildinfo"
	"github.com/alesierraalta/tpp/internal/check"
	"github.com/alesierraalta/tpp/internal/eval"
	"github.com/alesierraalta/tpp/internal/evidence"
	"github.com/alesierraalta/tpp/internal/feature"
	"github.com/alesierraalta/tpp/internal/feedback"
	"github.com/alesierraalta/tpp/internal/gate"
	"github.com/alesierraalta/tpp/internal/hookcmd"
	"github.com/alesierraalta/tpp/internal/mode"
	"github.com/alesierraalta/tpp/internal/plan"
	"github.com/alesierraalta/tpp/internal/repair"
	"github.com/alesierraalta/tpp/internal/sanitize"
	"github.com/alesierraalta/tpp/internal/state"
	"github.com/alesierraalta/tpp/internal/sync"
	"github.com/alesierraalta/tpp/internal/tui"
	"github.com/alesierraalta/tpp/internal/update"
)

// exitArtifact is what the CLI returns when its machine-readable output could not be written: the run
// finished, the report exists, and the consumer did not receive it. It is the number the benchmark returns
// for a record it could not persist, used here for the same fact — a document nobody got is not a document,
// and exit 0 would say it arrived.
const exitArtifact = bench.ExitArtifact

const usage = `usage: tpp <command> [flags]

commands:
  gate     Stop hook: read the hook payload on stdin, decide, log, emit feedback
  bind     store or remove this session's Stop-gate plan binding outside the repository
           (bind --session <id> --path <plan> --run <slug> [--cwd <dir>] [--config-dir <dir>],
            bind --unset --session <id> [--cwd <dir>] [--config-dir <dir>])
  setup    install and verify in one step: sync, run the doctor's checks, say whether the binary's
           directory is on PATH, and end on one line saying tpp is working (exit 0) or what is left
  sync     install the embedded skills into discovered hosts and wire Claude's Stop hook
  uninstall remove the installed skills and unwire the Stop hook (--dry-run writes nothing,
           --orphans also removes recorded paths the manifest no longer ships, --force
           snapshots modified files to the central backup store, then removes them)
  restore  copy a backup store entry back onto its original paths (--dry-run writes nothing,
            --id <backup-id> selects one; default is the latest backup)
  repair   bring a broken install back to what doctor reports healthy: re-sync drifted or
           missing managed skills and re-wire the Stop hook (--dry-run writes nothing,
           --force replaces modified managed files after backing them up)
  doctor   report installed skills, the hook wiring, and optional capabilities
  bench    run the testing skill against sealed-key fixtures and score it (run | score | history |
           compare | rescore | adjudicate | manifest)
  plan     write the skeleton, check the contract, name what breadth is still owed, record a
           Findings row from flags, and admit every Evidence row (init | check | gaps | upgrade | add-finding | admit)
  check    say what this repository still owes, from git and the plan alone: no hook payload,
           no transcript, no host. Exit 1 when there is something to do.
  status   report the local installation state and optional features
  update   check the Go module proxy for a newer release and install it through go install,
           printing the exact command when go is absent
  feature  list, enable, or disable an optional feature
  tui      interactive menu over status, feature toggles, and the sync dry-run plan
  feedback record an honest process report on the method itself, read all reports, or review pending reports
           (--template | --file <path> | --summary | --pending | --mark-reviewed <token>)
  version  print the version

flags shared by gate, setup, sync, doctor, uninstall, feedback, repair:
  --config-dir <dir>   Claude config directory (default: ~/.claude)

flags for sync and setup:
  --hosts <a,b,...>    limit installation to named hosts (claude, opencode, gemini, codex);
                       sync additionally accepts pi, the opt-in extension host (setup refuses it)
  --dry-run            print the plan and write nothing (sync only)

flags for setup and doctor:
  --mode <m>           harness mode: auto (default; resolves to standalone until a verified Gentle
                       runtime integration signal exists), standalone, or gentle (refused as
                       pending integration), resolved before any work runs

bench run [--cases <glob>] [--runner pi|claude] [--model <m>] [--runs N] [--max-turns N] [--timeout 30m]
          [--max-cost-usd N] [--out <dir>] [--bench-dir <dir>] [--dry-run] [--keep]
          [--retries N] [--retry-delay 60s] [--agent-config bench|<dir>] [--concurrency N]
          (--cases accepts comma-separated patterns)
          (--model is a name for claude and <provider>/<model>[:<thinking>] for pi; --runner pi
           builds a throwaway agent dir under --out unless --agent-config names one)
bench score --case <dir> --workspace <ws>
bench score --case <dir> --plan <file> [--adjudication <path>|none]
bench history [--bench-dir <dir>]
bench compare <before-results> <after-results>
bench rescore [--bench-dir <dir>] <results>
bench adjudicate --run <results>/<case>/<run> [--plan <path>] --row <n> --verdict <defect|false_positive|out_of_scope>
          [--defect <id>] --by <who> --reason <why> [--replace] [--ts <RFC3339>]
bench adjudicate --run <results>/<case>/<run> [--plan <path>] (--show | --pending)
          (writes <run>/adjudication.json: the decisions a score applies, one per finding row. A row
           nobody decided stays pending and is never counted as a false positive. A score applies a
           record only when --adjudication names it: the workspace the subject wrote must not be able
           to supply the verdicts about its own findings)
plan init [--path <path>] [--force] [--micro]
          (--micro writes the micro skeleton: header, Findings and Evidence ledger, for one small function)
plan check [--path <path>]
plan gaps [--run <slug>] [--all] [--path <path>]
plan upgrade [--run <slug>] [--path <path>]
plan export [--path <path>] [--commit <sha>]
           (prints the Findings as Markdown for a pull request comment, naming the commit covered,
            default the short HEAD, and the plan's base name; it never posts anything)
plan add-finding --id <id> --location <path:line> --severity <class> --data-safe <yes|no> --evidence <ids>
           --status <open|confirmed|fixed|gap-closed|rejected|wontfix> [--test <suite :: name>] --verdict-by <who / date>
           --reason <why> [--fingerprint <digest>] [--path <path>]
           (writes one Findings row; refuses a row plan check would reject, and never writes an
            evidence row. A bad value exits 2; a plan that refuses the row exits 1)
           (swept = status done, fixed or closed; n/a, na, none and skipped leave the denominator)
plan admit [--path <path>] [--execute] [--sandbox] [--sandbox-image <image>] [--timeout 120s] [--only <ids>] [--record <ids>]
           (dry run by default: --execute runs each admitted row's one command through sh -c;
            --record writes the freshly observed digest back into the named rows and requires
            --execute, because a dry run makes no observation to pin; exit 1 when any row is
            refused or a digest cannot be written)
check [--cwd .] [--path <path>]
status [--json]
update [--check]  --check only checks and refreshes the cache; it never installs
restore [--id <backup-id>] [--dry-run]   (default: the latest backup)
repair [--config-dir <dir>] [--dry-run] [--force]
feature list|enable|disable <id> [--preview]
--path: relative values resolve against the worktree root; absolute values are taken as given except in check, which refuses them. Without --path, use the plan declared in .tpp.json when there is one, else docs/testing/test-plan.md
--run: a lowercase slug identifying the active run; plan gaps uses the declaration when omitted, while --all forces whole-document counts
feedback [--config-dir <dir>] [--template] [--file <path>] [--plan <path>] [--summary] [--pending] [--mark-reviewed <token>]
`

func defaultConfigDir() string {
	for _, key := range []string{"CLAUDE_CONFIG_DIR", "PI_CODING_AGENT_DIR"} {
		if value := strings.TrimSpace(os.Getenv(key)); value != "" {
			return value
		}
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return ".claude"
	}
	return filepath.Join(home, ".claude")
}

func main() {
	if len(os.Args) < 2 {
		fmt.Fprint(os.Stderr, usage)
		os.Exit(2)
	}
	switch os.Args[1] {
	case "gate":
		os.Exit(runGate(os.Args[2:]))
	case "bind":
		os.Exit(runBind(os.Args[2:]))
	case "setup":
		os.Exit(runSetup(os.Args[2:]))
	case "sync":
		os.Exit(runSync(os.Args[2:]))
	case "uninstall":
		os.Exit(runUninstall(os.Args[2:]))
	case "restore":
		os.Exit(runRestore(os.Args[2:]))
	case "repair":
		os.Exit(runRepair(os.Args[2:]))
	case "doctor":
		os.Exit(runDoctor(os.Args[2:]))
	case "bench":
		os.Exit(runBench(os.Args[2:]))
	case "plan":
		os.Exit(runPlan(os.Args[2:]))
	case "check":
		os.Exit(runCheck(os.Args[2:]))
	case "status":
		os.Exit(runStatus(os.Args[2:]))
	case "update":
		os.Exit(runUpdate(os.Args[2:]))
	case "feature":
		os.Exit(runFeature(os.Args[2:]))
	case "tui":
		os.Exit(runTUI(os.Args[2:]))
	case "feedback":
		os.Exit(runFeedback(os.Args[2:]))
	case "version":
		fmt.Println(buildinfo.String())
		os.Exit(0)
	default:
		fmt.Fprint(os.Stderr, usage)
		os.Exit(2)
	}
}

func runGate(args []string) int {
	fs := flag.NewFlagSet("gate", flag.ContinueOnError)
	configDir := fs.String("config-dir", "", "Claude config directory")
	if err := fs.Parse(args); err != nil {
		return 0 // a hook must never break the turn, even on a bad flag
	}
	return gate.Run(os.Stdin, os.Stdout, gate.DefaultLogPath(*configDir), time.Now())
}

// runBind writes or removes the file the Stop hook reads when the host environment carries no
// binding: keyed outside the repository by canonical root and exact session, validated with the
// same rules the gate applies on read. It never falls back to the worktree declaration, and the
// session identity never reaches the terminal.
func runBind(args []string) int {
	fs := flag.NewFlagSet("bind", flag.ContinueOnError)
	session := fs.String("session", "", "exact session ID the binding belongs to")
	path := fs.String("path", "", "repository-relative plan path to audit")
	run := fs.String("run", "", "run slug the audit is scoped to")
	unset := fs.Bool("unset", false, "remove this session's binding instead of writing one")
	cwd := fs.String("cwd", ".", "directory inside the worktree to bind")
	configDir := fs.String("config-dir", "", "Claude config directory")
	if err := fs.Parse(args); err != nil {
		return 2
	}
	if strings.TrimSpace(*session) == "" {
		fmt.Fprintln(os.Stderr, "bind: --session is required")
		return 2
	}
	if !*unset {
		if err := plan.ValidatePlanPath("--path", *path); err != nil {
			fmt.Fprintln(os.Stderr, err)
			return 2
		}
		if err := plan.ValidateRun("--run", *run); err != nil {
			fmt.Fprintln(os.Stderr, err)
			return 2
		}
	}
	root, err := gitToplevel(*cwd)
	if err != nil {
		fmt.Fprintln(os.Stderr, "bind:", err)
		return 2
	}
	// The same derivation `tpp gate` uses, so bind and Stop can never hold different directories.
	dir := gate.BindingsDir(gate.DefaultLogPath(*configDir))
	if *unset {
		if err := gate.UnsetBinding(dir, *session, root); err != nil {
			fmt.Fprintln(os.Stderr, "bind:", err)
			return 1
		}
		fmt.Println("binding removed")
		return 0
	}
	b := gate.Binding{Session: *session, Root: root, PlanPath: *path, Run: *run}
	if err := gate.SetBinding(dir, b); err != nil {
		fmt.Fprintln(os.Stderr, "bind:", err)
		return 1
	}
	fmt.Printf("bound %s\n", *path)
	return 0
}

// gitToplevel is the canonical root the gate will derive at stop time from the same directory,
// so what bind stores and what the hook reads name one repository.
func gitToplevel(dir string) (string, error) {
	cmd := exec.Command("git", "rev-parse", "--show-toplevel")
	cmd.Dir = dir
	out, err := cmd.Output()
	root := strings.TrimSpace(string(out))
	if err != nil || root == "" {
		return "", errors.New("--cwd is not inside a git worktree")
	}
	return root, nil
}

type statusFeature struct {
	ID      string `json:"id"`
	Title   string `json:"title"`
	Enabled bool   `json:"enabled"`
}

type statusReport struct {
	StateRoot        string          `json:"stateRoot"`
	StateExists      bool            `json:"stateExists"`
	InstalledVersion string          `json:"installedVersion"`
	AvailableVersion string          `json:"availableVersion"`
	Features         []statusFeature `json:"features"`
}

const unknownAvailableVersion = "unknown (no update check yet)"

// statusView assembles the status table once for both `status` and the TUI's status pane.
func statusView() (tui.StatusView, error) {
	path, err := state.Path()
	if err != nil {
		return tui.StatusView{}, err
	}
	stateExists := true
	if _, err := os.Stat(path); os.IsNotExist(err) {
		stateExists = false
	} else if err != nil {
		return tui.StatusView{}, err
	}
	features, err := currentFeatureStates()
	if err != nil {
		return tui.StatusView{}, err
	}
	// The available version is the last check's cached answer, never a live probe: status and
	// the TUI must work offline, so an unreadable or failed check falls back to the unknown string.
	available := unknownAvailableVersion
	if cache, err := update.LoadCache(); err == nil && cache.AvailableVersion != "" {
		available = cache.AvailableVersion
	}
	return tui.StatusView{
		StateRoot:        filepath.Dir(path),
		StateExists:      stateExists,
		InstalledVersion: buildinfo.Version,
		AvailableVersion: available,
		Features:         features,
	}, nil
}

// runUpdate checks the module proxy for the latest tag, refreshes the cache `status` reads, and
// installs through `go install` when this build is behind. The binary a fresh install lands on
// only runs after a restart, so success points at a new shell instead of claiming this process
// became the new version. TPP_UPDATE_BASE_URL, or the legacy RDD_PLUS_UPDATE_BASE_URL when it is unset,
// points the check at another proxy (tests use a local one); an empty value means the public Go module proxy.
func runUpdate(args []string) int {
	fs := flag.NewFlagSet("update", flag.ContinueOnError)
	checkOnly := fs.Bool("check", false, "only check and refresh the cache; never install")
	if err := fs.Parse(args); err != nil {
		return 2
	}
	if fs.NArg() > 0 {
		fmt.Fprintf(os.Stderr, "update: unexpected argument %q\n", fs.Arg(0))
		return 2
	}
	baseURL := os.Getenv("TPP_UPDATE_BASE_URL")
	if baseURL == "" {
		baseURL = os.Getenv("RDD_PLUS_UPDATE_BASE_URL")
	}
	checker := update.Checker{BaseURL: baseURL}
	result, err := checker.Check(context.Background())
	if err != nil {
		// A failed check still lands in the cache so the record shows a check was attempted;
		// status keeps reporting the unknown string because no version was learned.
		_, _ = update.SaveCache(update.Cache{CheckedAt: time.Now().UTC().Format(time.RFC3339), Error: err.Error()})
		fmt.Fprintln(os.Stderr, "update:", err)
		return 1
	}
	if _, err := update.SaveCache(update.Cache{
		AvailableVersion: result.Latest,
		CheckedAt:        time.Now().UTC().Format(time.RFC3339),
	}); err != nil {
		fmt.Fprintln(os.Stderr, "update: cache:", err)
	}
	if result.Relation == update.Unknown {
		fmt.Println(update.Uncomparable(buildinfo.Version, result.Latest))
		return 0
	}
	if result.Relation != update.Behind {
		fmt.Printf("already up to date (installed %s, latest %s)\n", buildinfo.Version, result.Latest)
		return 0
	}
	if *checkOnly {
		fmt.Printf("update available (installed %s, latest %s)\n", buildinfo.Version, result.Latest)
		return 0
	}
	// go install writes to GOBIN, which need not be where the running binary lives: PATH and the Stop hook
	// would keep the old version (issue #144). The release goes where this binary is.
	gobin, err := runningBinaryDir()
	if err != nil {
		fmt.Fprintln(os.Stderr, "update: cannot locate the running binary:", err)
		return 1
	}
	// A binary in a directory this user cannot write (a system bin dir) cannot be replaced in place; say so
	// before go reports a bare permission error.
	if probe, err := os.CreateTemp(gobin, ".tpp-update-*"); err != nil {
		fmt.Fprintf(os.Stderr, "update: %s is not writable, so the running binary cannot be replaced there: %v\n", gobin, err)
		return 1
	} else {
		probe.Close()
		os.Remove(probe.Name())
	}
	err = update.RunInstall(result.Latest, exec.LookPath, func(name string, argv ...string) error {
		fmt.Printf("go found; running: GOBIN=%s %s\n", gobin, strings.Join(append([]string{name}, argv...), " "))
		cmd := exec.Command(name, argv...)
		cmd.Env = append(os.Environ(), "GOBIN="+gobin)
		cmd.Stdout, cmd.Stderr = os.Stdout, os.Stderr
		return cmd.Run()
	})
	switch {
	case errors.Is(err, update.ErrGoMissing):
		fmt.Printf("go is not on PATH; run this from a shell with Go installed:\nGOBIN=%s %s\n", shellQuote(gobin), update.InstallCommand(result.Latest))
		return 0
	case err != nil:
		fmt.Fprintln(os.Stderr, "update:", err)
		return 1
	}
	fmt.Printf("installed %s; restart your shell and run `tpp version` there to confirm\n", result.Latest)
	return 0
}

func runStatus(args []string) int {
	jsonOutput := false
	for _, arg := range args {
		if arg == "--json" {
			jsonOutput = true
			continue
		}
		fmt.Fprintf(os.Stderr, "status: unknown argument %q\n", arg)
		return 2
	}

	view, err := statusView()
	if err != nil {
		fmt.Fprintln(os.Stderr, "status:", err)
		return 1
	}
	report := statusReport{
		StateRoot:        view.StateRoot,
		StateExists:      view.StateExists,
		InstalledVersion: view.InstalledVersion,
		AvailableVersion: view.AvailableVersion,
		Features:         make([]statusFeature, len(view.Features)),
	}
	for i, row := range view.Features {
		report.Features[i] = statusFeature{ID: row.ID, Title: row.Title, Enabled: row.Enabled}
	}
	if jsonOutput {
		encoded, err := json.Marshal(report)
		if err != nil {
			fmt.Fprintln(os.Stderr, "status:", err)
			return 1
		}
		fmt.Println(string(encoded))
		return 0
	}
	fmt.Printf("State root: %s\nState exists: %s\nInstalled version: %s\nAvailable version: %s\n\nFeatures:\n", report.StateRoot, yesNo(report.StateExists), report.InstalledVersion, report.AvailableVersion)
	fmt.Println("ID\tTitle\tState")
	for _, item := range report.Features {
		fmt.Printf("%s\t%s\t%s\n", item.ID, item.Title, enabledState(item.Enabled))
	}
	return 0
}

func runFeature(args []string) int {
	if len(args) == 0 {
		return featureUsage("missing subcommand")
	}
	subcommand := args[0]
	switch subcommand {
	case "list":
		if len(args) != 1 {
			return featureUsage("list does not take an id or --preview")
		}
		features, err := currentFeatureStates()
		if err != nil {
			fmt.Fprintln(os.Stderr, "feature:", err)
			return 1
		}
		fmt.Println("ID\tTitle\tState")
		for _, item := range features {
			fmt.Printf("%s\t%s\t%s\n", item.ID, item.Title, enabledState(item.Enabled))
		}
		return 0
	case "enable", "disable":
		id, preview, ok := parseFeatureArgs(args[1:])
		if !ok || id == "" {
			return featureUsage("enable and disable require one feature id")
		}
		declaration, known := feature.Get(id)
		if !known {
			return unknownFeatureCLI(id)
		}
		if preview {
			text, err := feature.Preview(id)
			if err != nil {
				fmt.Fprintln(os.Stderr, "feature:", err)
				return 1
			}
			fmt.Println(text)
			return 0
		}
		enabled := subcommand == "enable"
		if _, err := feature.Set(id, enabled); err != nil {
			fmt.Fprintln(os.Stderr, "feature:", err)
			return 1
		}
		fmt.Printf("%s: %s\n", declaration.ID, enabledState(enabled))
		return 0
	default:
		return featureUsage(fmt.Sprintf("unknown subcommand %q", subcommand))
	}
}

func currentFeatureStates() ([]tui.FeatureRow, error) {
	declarations := feature.All()
	states := make([]tui.FeatureRow, 0, len(declarations))
	for _, declaration := range declarations {
		enabled, err := feature.Enabled(declaration.ID)
		if err != nil {
			return nil, err
		}
		states = append(states, tui.FeatureRow{ID: declaration.ID, Title: declaration.Title, Enabled: enabled})
	}
	return states, nil
}

func parseFeatureArgs(args []string) (string, bool, bool) {
	id := ""
	preview := false
	for _, arg := range args {
		switch arg {
		case "--preview":
			preview = true
		default:
			if strings.HasPrefix(arg, "-") || id != "" {
				return "", false, false
			}
			id = arg
		}
	}
	return id, preview, true
}

func featureUsage(reason string) int {
	fmt.Fprintf(os.Stderr, "feature: %s\nusage: tpp feature list|enable|disable <id> [--preview]\n", reason)
	return 2
}

func unknownFeatureCLI(id string) int {
	ids := make([]string, 0, len(feature.All()))
	for _, declaration := range feature.All() {
		ids = append(ids, declaration.ID)
	}
	fmt.Fprintf(os.Stderr, "feature: unknown id %q; known ids: %s\n", id, strings.Join(ids, ", "))
	return 2
}

func enabledState(enabled bool) string {
	if enabled {
		return "enabled"
	}
	return "disabled"
}

func yesNo(value bool) string {
	if value {
		return "yes"
	}
	return "no"
}

// runTUI wires the interactive menu to the same sources the subcommands read. The guard runs before
// tui.Run: raw mode exists only on linux/darwin terminals, and a pipe cannot be driven key-per-key,
// so a non-TTY stdin or stdout (or any other OS) is refused with the non-interactive equivalents instead.
func runTUI(args []string) int {
	if len(args) > 0 {
		fmt.Fprintf(os.Stderr, "tui: unexpected argument %q\n", args[0])
		return 2
	}
	isTTY := tui.IsTerminal(os.Stdin) && tui.IsTerminal(os.Stdout)
	supportedOS := runtime.GOOS == "linux" || runtime.GOOS == "darwin"
	if !isTTY || !supportedOS {
		fmt.Fprintln(os.Stderr, "tui: needs an interactive terminal; use status, feature, or sync --dry-run instead")
		return 1
	}
	if err := tui.Run(os.Stdin, os.Stdout, tui.Deps{
		Status:     statusView,
		Features:   currentFeatureStates,
		SetFeature: feature.Set,
		Preview:    feature.Preview,
		SyncPlan:   syncPlan,
	}); err != nil {
		fmt.Fprintln(os.Stderr, "tui:", err)
		return 1
	}
	return 0
}

func runSync(args []string) int {
	fs := flag.NewFlagSet("sync", flag.ContinueOnError)
	configDir := fs.String("config-dir", defaultConfigDir(), "Claude config directory")
	hostsFlag := fs.String("hosts", "", "comma-separated hosts to install into")
	dryRun := fs.Bool("dry-run", false, "print the plan and write nothing")
	force := fs.Bool("force", false, "replace files this tool installed that were modified afterwards, after backing them up")
	if err := fs.Parse(args); err != nil {
		return 2
	}

	selected, err := parseSyncHostsAllowingPi(*hostsFlag, flagSet(fs, "hosts"))
	if err != nil {
		fmt.Fprintln(os.Stderr, "sync:", err)
		return 2
	}
	code, _ := syncInstall(*configDir, flagSet(fs, "config-dir"), selected, sync.Options{DryRun: *dryRun, Force: *force})
	return code
}

// syncInstall is what sync and setup share: discover the hosts, narrow them to --config-dir and --hosts,
// add the opt-in Pi host only when --hosts named it (setup never can), run the planner, print its
// report, and return the exit code with the hosts it ran against.
func syncInstall(configDir string, explicitConfigDir bool, selected map[string]bool, opts sync.Options) (int, sync.Discovery) {
	discovery := discoverSync(configDir, explicitConfigDir)
	if explicitConfigDir {
		discovery.Hosts = filterSyncHosts(discovery.Hosts, map[string]bool{"claude": true})
	}
	if selected != nil {
		discovery.Hosts = filterSyncHosts(discovery.Hosts, selected)
		if selected["pi"] {
			home, homeErr := os.UserHomeDir()
			if homeErr != nil {
				discovery.Problems = append(discovery.Problems, fmt.Sprintf("the pi host needs a home directory: %v", homeErr))
			} else {
				discovery.Hosts = append(discovery.Hosts, sync.PiHost(home))
			}
		}
	}

	text, err := syncReport(discovery, opts)
	fmt.Print(text)
	if err != nil {
		fmt.Fprintln(os.Stderr, "sync:", err)
		return 1, discovery
	}
	// A sync that found no host installed nothing. Exit 0 would tell a scripted install (dotfiles, CI) it
	// succeeded, so it says how to proceed and exits 1, the code doctor uses for "action required".
	if len(discovery.Hosts) == 0 {
		fmt.Println("sync: nothing installed: no host found; install a host (Claude Code creates ~/.claude) or pass --config-dir <dir>")
		return 1, discovery
	}
	return 0, discovery
}

// shellQuote renders s as one POSIX shell word, so a printed command still works when a path holds spaces.
func shellQuote(s string) string {
	return "'" + strings.ReplaceAll(s, "'", `'\''`) + "'"
}

// runningBinaryDir is the directory of the binary that is executing, with symlinks resolved, so an update
// replaces the file PATH and the Stop hook actually run.
func runningBinaryDir() (string, error) {
	exe, err := os.Executable()
	if err != nil {
		return "", err
	}
	if resolved, err := filepath.EvalSymlinks(exe); err == nil {
		exe = resolved
	}
	return filepath.Dir(exe), nil
}

// discoverSync builds the host discovery sync runs against; an explicit --config-dir narrows the
// result to Claude, and a home directory that cannot be determined is a problem to report, not a
// failure, because discovery still answers with what it could see.
func discoverSync(configDir string, explicitConfigDir bool) sync.Discovery {
	home, homeErr := os.UserHomeDir()
	if homeErr != nil {
		home = ""
	}
	var discovery sync.Discovery
	if explicitConfigDir {
		discovery = sync.DiscoverHosts(home, configDir)
	} else {
		discovery = sync.DiscoverHosts(home)
	}
	if homeErr != nil {
		discovery.Problems = append(discovery.Problems, fmt.Sprintf("cannot determine home directory: %v", homeErr))
	}
	return discovery
}

// syncReport runs the sync planner over discovery and returns the classified report text: the one
// output path `sync` prints and the TUI's dry-run plan shows.
func syncReport(discovery sync.Discovery, opts sync.Options) (string, error) {
	bin, err := os.Executable()
	if err == nil {
		bin, _ = filepath.Abs(bin)
	}
	report, err := sync.SyncHosts(discovery.Hosts, bin, opts)
	report.LookedFor = discovery.LookedFor
	report.DiscoveryErrors = discovery.Problems
	report.ClaudeConfigDir = discovery.ClaudeConfigDir
	return report.String(), err
}

// syncPlan is the TUI's plan view: the dry-run planner only — writes nothing, forces nothing.
func syncPlan() (string, error) {
	return syncReport(discoverSync("", false), sync.Options{DryRun: true})
}

// parseSyncHosts reads --hosts for setup, which offers only the discovered skill hosts: setup's
// doctor pass and closing report are Claude-centered, so the opt-in Pi host stays a usage error
// there until setup itself learns what a Pi install means (the follow-up task owns that).
func parseSyncHosts(raw string, explicit bool) (map[string]bool, error) {
	return parseHostSelection(raw, explicit, nil)
}

// parseSyncHostsAllowingPi reads --hosts for sync, where naming pi opts into the extension host
// discovery never offers. The selection stays explicit: pi appears only when named, and only the
// hosts named are installed.
func parseSyncHostsAllowingPi(raw string, explicit bool) (map[string]bool, error) {
	return parseHostSelection(raw, explicit, []string{"pi"})
}

func parseHostSelection(raw string, explicit bool, extraKnown []string) (map[string]bool, error) {
	if !explicit {
		return nil, nil
	}
	selected := map[string]bool{}
	known := append(sync.KnownHosts(), extraKnown...)
	knownSet := make(map[string]bool, len(known))
	for _, name := range known {
		knownSet[name] = true
	}
	for _, value := range strings.Split(raw, ",") {
		name := strings.TrimSpace(value)
		if !knownSet[name] {
			return nil, fmt.Errorf("unknown host %q; known hosts: %s", name, strings.Join(known, ", "))
		}
		selected[name] = true
	}
	return selected, nil
}

func filterSyncHosts(hosts []sync.Host, selected map[string]bool) []sync.Host {
	filtered := make([]sync.Host, 0, len(hosts))
	for _, host := range hosts {
		if selected[host.Name] {
			filtered = append(filtered, host)
		}
	}
	return filtered
}

// runUninstall takes the installation off the machine: state decides what was installed, so no
// discovery runs here. An explicit --config-dir only redirects the hook unwire; without it the
// recorded host directory wins, because that is where the gate was wired. A modified file stops
// the whole run before anything is deleted (exit 1 naming --force), so a refusal never leaves a
// half-uninstalled tree.
func runUninstall(args []string) int {
	fs := flag.NewFlagSet("uninstall", flag.ContinueOnError)
	configDir := fs.String("config-dir", defaultConfigDir(), "Claude config directory")
	dryRun := fs.Bool("dry-run", false, "print the plan and write nothing")
	orphans := fs.Bool("orphans", false, "also remove recorded assets the manifest no longer ships")
	force := fs.Bool("force", false, "snapshot modified files to the central backup store, then remove them")
	if err := fs.Parse(args); err != nil {
		return 2
	}
	if fs.NArg() > 0 {
		fmt.Fprintf(os.Stderr, "uninstall: unexpected argument %q\n", fs.Arg(0))
		return 2
	}
	// The flag carries a default for --help, but only an explicit value may override what state
	// recorded: the default would unwire ~/.claude while the gate lives wherever sync installed it.
	explicitConfigDir := ""
	if flagSet(fs, "config-dir") {
		explicitConfigDir = *configDir
	}
	report, err := sync.Uninstall(sync.UninstallOptions{
		DryRun:    *dryRun,
		Orphans:   *orphans,
		Force:     *force,
		ConfigDir: explicitConfigDir,
	})
	fmt.Print(report.String())
	if err != nil {
		fmt.Fprintln(os.Stderr, "uninstall:", err)
		return 1
	}
	return 0
}

// runRestore puts a backup store entry back on disk. There is no --config-dir: the manifest
// records absolute original paths, so the store alone decides where the bytes land. Exit 1 is
// operational (no backup, unknown id, a refused path, a failed write), 2 is a usage mistake.
func runRestore(args []string) int {
	fs := flag.NewFlagSet("restore", flag.ContinueOnError)
	id := fs.String("id", "", "backup id to restore (default: the latest backup)")
	dryRun := fs.Bool("dry-run", false, "print the plan and write nothing")
	if err := fs.Parse(args); err != nil {
		return 2
	}
	if fs.NArg() > 0 {
		fmt.Fprintf(os.Stderr, "restore: unexpected argument %q\n", fs.Arg(0))
		return 2
	}
	report, err := sync.Restore(*id, *dryRun)
	// A report from a run that failed before it planned anything says nothing worth printing;
	// a partial apply still shows the files that did land, beside the error naming the one that
	// did not.
	if err == nil || len(report.Restored) > 0 {
		fmt.Print(report.String())
	}
	if err != nil {
		fmt.Fprintln(os.Stderr, "restore:", err)
		return 1
	}
	return 0
}

// runRepair takes a broken install back to what doctor reports healthy. Exit 1 is operational
// (the running binary cannot be resolved, settings cannot be read, the sync fails), 2 is a
// usage mistake; a dry run and an already-healthy install both exit 0.
func runRepair(args []string) int {
	fs := flag.NewFlagSet("repair", flag.ContinueOnError)
	configDir := fs.String("config-dir", defaultConfigDir(), "Claude config directory")
	dryRun := fs.Bool("dry-run", false, "print the plan and write nothing")
	force := fs.Bool("force", false, "replace files this tool installed that were modified afterwards, after backing them up")
	if err := fs.Parse(args); err != nil {
		return 2
	}
	if fs.NArg() > 0 {
		fmt.Fprintf(os.Stderr, "repair: unexpected argument %q\n", fs.Arg(0))
		return 2
	}
	report, err := repair.Run(repair.Options{
		DryRun:    *dryRun,
		Force:     *force,
		ConfigDir: *configDir,
	})
	if text := report.String(); text != "" {
		fmt.Print(text)
	}
	if err != nil {
		fmt.Fprintln(os.Stderr, "repair:", err)
		return 1
	}
	return 0
}

func runDoctor(args []string) int {
	fs := flag.NewFlagSet("doctor", flag.ContinueOnError)
	configDir := fs.String("config-dir", defaultConfigDir(), "Claude config directory")
	modeFlag := fs.String("mode", mode.Auto, "harness mode: auto, standalone, or gentle")
	asJSON := fs.Bool("json", false, "machine-readable output")
	if err := fs.Parse(args); err != nil {
		return 2
	}
	// Resolve before inspecting anything: a mode this build cannot run must be refused at the
	// flag, with the reason, rather than answered with a report from a different contract.
	runMode, err := mode.Resolve(*modeFlag, mode.VerifiedGentleSignal())
	if err != nil {
		fmt.Fprintln(os.Stderr, "doctor:", err)
		return 2
	}
	report := doctorReport(*configDir, runMode)
	if *asJSON {
		enc := json.NewEncoder(os.Stdout)
		enc.SetIndent("", "  ")
		if err := enc.Encode(report); err != nil {
			fmt.Fprintln(os.Stderr, "doctor:", err)
			return exitArtifact
		}
	} else {
		fmt.Print(report.String())
	}
	if report.Healthy {
		return 0
	}
	return 1
}

// selfDir is the directory of the running binary, so a spawned agent runs this build when the
// skill tells it to call `tpp plan init`.
func selfDir() string {
	exe, err := os.Executable()
	if err != nil {
		return ""
	}
	if abs, err := filepath.Abs(exe); err == nil {
		exe = abs
	}
	return filepath.Dir(exe)
}

func runCheck(args []string) int {
	fs := flag.NewFlagSet("check", flag.ContinueOnError)
	cwd := fs.String("cwd", ".", "directory inside the repository to check")
	path := fs.String("path", "", "plan file relative to the repository root")
	if err := fs.Parse(args); err != nil {
		return 2
	}
	if flagSet(fs, "path") {
		if err := plan.ValidatePlanPath("--path", *path); err != nil {
			fmt.Fprintln(os.Stderr, err)
			return 2
		}
	}
	res := check.Run(*cwd, check.Deps{PlanPath: *path})
	fmt.Println(strings.TrimRight(res.Text, "\n"))
	return res.Exit
}

// flagSet reports whether the operator passed the named flag, which is how an explicit empty value is
// told apart from an absent one.
func flagSet(fs *flag.FlagSet, name string) bool {
	set := false
	fs.Visit(func(f *flag.Flag) {
		set = set || f.Name == name
	})
	return set
}

// runFeedback is the destination the gate's Stop offer never had: --template hands the operator a
// fillable report, --file records it, and review actions read or acknowledge a pending snapshot.
func runFeedback(args []string) int {
	fs := flag.NewFlagSet("feedback", flag.ContinueOnError)
	configDir := fs.String("config-dir", defaultConfigDir(), "Claude config directory")
	template := fs.Bool("template", false, "print a fillable skeleton and write nothing")
	file := fs.String("file", "", "submit the report written in this file")
	plan := fs.String("plan", "", "repository-relative plan path the report is about")
	summary := fs.Bool("summary", false, "read the all-history summary without moving the review cursor")
	pending := fs.Bool("pending", false, "print reports appended since the review cursor")
	markReviewed := fs.String("mark-reviewed", "", "acknowledge the exact review snapshot token")
	if err := fs.Parse(args); err != nil {
		return 2
	}
	fileSet := flagSet(fs, "file")
	if fileSet && *file == "" {
		fmt.Fprintln(os.Stderr, "feedback: --file requires a path")
		return 2
	}
	markReviewedSet := flagSet(fs, "mark-reviewed")
	if markReviewedSet && strings.TrimSpace(*markReviewed) == "" {
		fmt.Fprintln(os.Stderr, "feedback: --mark-reviewed requires a non-empty token")
		return 2
	}
	if (*pending || markReviewedSet) && (*template || fileSet || *summary || flagSet(fs, "plan")) {
		fmt.Fprintln(os.Stderr, "feedback: --pending and --mark-reviewed cannot be combined with --template, --file, --plan, or --summary")
		return 2
	}
	actions := 0
	for _, active := range []bool{*template, fileSet, *summary, *pending, markReviewedSet} {
		if active {
			actions++
		}
	}
	if actions > 1 {
		fmt.Fprintln(os.Stderr, "feedback: choose only one action")
		return 2
	}
	switch {
	case *template:
		fmt.Print(feedback.Template(feedback.Report{
			TS:    time.Now().UTC().Format(time.RFC3339),
			Repo:  feedback.RepoRoot("."),
			Plan:  *plan,
			Skill: feedback.EmbeddedSkillIdentity(),
			Build: buildinfo.String(),
		}))
		return 0
	case fileSet:
		raw, err := os.ReadFile(*file)
		if err != nil {
			fmt.Fprintln(os.Stderr, "feedback:", err)
			return 1
		}
		r, err := feedback.Parse(string(raw))
		if err != nil {
			fmt.Fprintln(os.Stderr, "feedback:", err)
			return 2
		}
		if *plan != "" {
			r.Plan = *plan
		}
		if strings.TrimSpace(r.Plan) == "" {
			r.Plan = feedback.NotGiven
		}
		if err := feedback.Record(*configDir, r); err != nil {
			fmt.Fprintln(os.Stderr, "feedback:", err)
			return 1
		}
		recordedRepo := r.Repo
		if r.Repo != "" {
			recordedRepo = "repo-unsealed"
			if key, err := sanitize.LoadKey(*configDir); err == nil {
				recordedRepo = key.ID("repo", r.Repo)
			}
			if resolved, ok := sanitize.Resolve(sanitize.TelemetryDir(*configDir), recordedRepo); ok {
				recordedRepo = resolved
			} else {
				recordedRepo += " (local map unavailable)"
			}
		}
		fmt.Printf("recorded %s feedback for %s\n", r.Verdict, recordedRepo)
		return 0
	case *pending:
		batch, err := feedback.Pending(*configDir)
		if err != nil {
			fmt.Fprintln(os.Stderr, "feedback:", err)
			return 1
		}
		fmt.Printf("run feedback: %d report(s)\n", batch.SnapshotCount)
		if batch.Initialized {
			fmt.Printf(
				"first-use review baseline established: %d existing report(s); historical reports were not included\n",
				batch.BaselineCount,
			)
			fmt.Println("no pending reports")
			return 0
		}
		if len(batch.Reports) == 0 {
			fmt.Println("no pending reports")
			return 0
		}
		for _, report := range batch.Reports {
			fmt.Printf(
				"ts: %s\nrepo: %s\nplan: %s\nskill: %s\nbuild: %s\npaid: %s\ncost: %s\nreason: %s\nverdict: %s\nguess: %s\nfreeform: %s\n\n",
				report.TS, report.Repo, report.Plan, report.Skill, report.Build, report.Paid, report.Cost,
				report.Reason, report.Verdict, report.Guess, report.Freeform,
			)
		}
		fmt.Printf("review token: %s\n", batch.Token)
		return 0
	case markReviewedSet:
		mark, err := feedback.MarkReviewed(*configDir, *markReviewed)
		if err != nil {
			fmt.Fprintln(os.Stderr, "feedback:", err)
			return 1
		}
		fmt.Printf("marked %d new report(s) as reviewed\n", mark.NewlyReviewed)
		return 0
	default:
		_ = *summary // --summary and no flags are the same cheapest path to the answer
		out, err := feedback.Summary(*configDir)
		if err != nil {
			fmt.Fprintln(os.Stderr, "feedback:", err)
			return 1
		}
		fmt.Print(out)
		return 0
	}
}

func runPlan(args []string) int {
	if len(args) == 0 {
		fmt.Fprint(os.Stderr, usage)
		return 2
	}
	fs := flag.NewFlagSet("plan", flag.ContinueOnError)
	path := fs.String("path", "", "plan file")
	run := fs.String("run", "", "active run slug (gaps and upgrade)")
	all := fs.Bool("all", false, "count every row (gaps only)")
	force := fs.Bool("force", false, "replace an existing plan (init only)")
	micro := fs.Bool("micro", false, "write the micro plan skeleton for one small function (init only)")
	id := fs.String("id", "", "Findings row id (add-finding)")
	location := fs.String("location", "", "the `path:line` the finding cites (add-finding)")
	severity := fs.String("severity", "", "consequence class (add-finding)")
	dataSafe := fs.String("data-safe", "", "whether data is safe (add-finding)")
	evidence := fs.String("evidence", "", "Evidence ledger ids the finding cites (add-finding)")
	test := fs.String("test", "", "pinning test, required when the status is confirmed or fixed (add-finding)")
	status := fs.String("status", "", "one of "+plan.FindingsStatusList+" (add-finding)")
	verdictBy := fs.String("verdict-by", "", "who settled it and when (add-finding)")
	reason := fs.String("reason", "", "why the verdict stands (add-finding)")
	fingerprint := fs.String("fingerprint", "", "cited-files fingerprint at verdict, default - (add-finding)")
	execute := fs.Bool("execute", false, "run each admitted command; the default is a dry run (admit only)")
	timeout := fs.Duration("timeout", 120*time.Second, "bound one command; 0 leaves it unbounded (admit only)")
	only := fs.String("only", "", "comma-separated evidence ids to admit; empty means every row (admit only)")
	record := fs.String("record", "", "comma-separated evidence ids whose freshly observed digest is written into the plan (admit only; requires --execute)")
	sandbox := fs.Bool("sandbox", false, "observe each command inside a container instead of on this machine (admit only; requires --execute and docker)")
	sandboxImage := fs.String("sandbox-image", sandboxImageDefault, "image the sandbox runs in (admit only; see --sandbox); the default is pulled on first use")
	commit := fs.String("commit", "", "commit the export covers; the default is the short HEAD of the repository (export only)")
	if err := fs.Parse(args[1:]); err != nil {
		return 2
	}
	if *all && flagSet(fs, "run") {
		fmt.Fprintln(os.Stderr, "plan gaps: --run and --all cannot combine")
		return 2
	}
	if *all && args[0] != "gaps" {
		fmt.Fprintln(os.Stderr, "plan: --all is only valid with gaps")
		return 2
	}
	if flagSet(fs, "run") {
		if err := plan.ValidateRun("--run", *run); err != nil {
			fmt.Fprintln(os.Stderr, "plan:", err)
			return 2
		}
	}
	root := feedback.RepoRoot(".")
	var err error
	var declaredRun string
	effectivePath := *path
	if flagSet(fs, "path") {
		effectivePath, err = plan.ResolveFromRoot(root, "--path", *path)
		if err == nil {
			declaredRun, err = plan.DeclaredRun(root, nil)
		}
	} else {
		effectivePath, declaredRun, err = plan.Resolve(root, nil)
	}
	if err != nil {
		fmt.Fprintln(os.Stderr, "plan:", err)
		return 1
	}
	switch args[0] {
	case "admit":
		return runPlanAdmit(effectivePath, *execute, *timeout, *only, *record, *sandbox, *sandboxImage)
	case "gaps":
		selectedRun := declaredRun
		if *all {
			selectedRun = ""
		} else if flagSet(fs, "run") {
			selectedRun = *run
		}
		g, err := plan.GapsInFileForRun(effectivePath, selectedRun)
		if err != nil {
			fmt.Fprintln(os.Stderr, "plan gaps:", err)
			return 1
		}
		fmt.Print(g.Report())
		if g.Any() {
			return 1
		}
		return 0
	case "upgrade":
		selectedRun := ""
		if flagSet(fs, "run") {
			selectedRun = *run
		}
		changed, err := plan.Upgrade(effectivePath, selectedRun)
		if err != nil {
			fmt.Fprintln(os.Stderr, "plan upgrade:", err)
			return 1
		}
		fmt.Printf("upgraded %s: %d table(s) changed\n", effectivePath, changed)
		return 0
	case "init":
		initPlan := plan.Init
		if *micro {
			initPlan = plan.InitMicro
		}
		if err := initPlan(effectivePath, *force); err != nil {
			fmt.Fprintln(os.Stderr, "plan init:", err)
			return 1
		}
		fmt.Printf("wrote %s\n", effectivePath)
		return 0
	case "export":
		covered := *commit
		if !flagSet(fs, "commit") {
			out, err := exec.Command("git", "-C", root, "rev-parse", "--short", "HEAD").Output()
			if err != nil {
				fmt.Fprintln(os.Stderr, "plan export: no commit to name: pass --commit <sha>")
				return 1
			}
			covered = strings.TrimSpace(string(out))
		}
		out, err := plan.ExportFile(effectivePath, covered)
		if err != nil {
			fmt.Fprintln(os.Stderr, "plan export:", err)
			return 1
		}
		fmt.Print(out)
		return 0
	case "check":
		problems, err := plan.Check(effectivePath)
		if err != nil {
			fmt.Fprintln(os.Stderr, "plan check:", err)
			return 1
		}
		if len(problems) == 0 {
			fmt.Printf("%s: well formed\n", effectivePath)
			return 0
		}
		for _, p := range problems {
			fmt.Fprintf(os.Stderr, "%s: %s\n", effectivePath, p)
		}
		return 1
	case "add-finding":
		// A flag the row cannot be built without is a usage error, not a plan-side refusal: nothing has
		// been read yet, and the operator still holds the whole command on the line. --test is conditional
		// on the status and is answered by the plan package, which also knows whether the table carries
		// the column at all.
		for _, req := range []struct{ flag, value string }{
			{"id", *id}, {"location", *location}, {"severity", *severity}, {"data-safe", *dataSafe},
			{"evidence", *evidence}, {"status", *status}, {"verdict-by", *verdictBy}, {"reason", *reason},
		} {
			if strings.TrimSpace(req.value) == "" {
				fmt.Fprintf(os.Stderr, "plan add-finding: --%s is required\n", req.flag)
				return 2
			}
		}
		line, err := plan.AddFinding(effectivePath, plan.Finding{
			ID: *id, Location: *location, Severity: *severity, DataSafe: *dataSafe,
			Evidence: *evidence, Test: *test, Status: *status, VerdictBy: *verdictBy,
			Reason: *reason, Fingerprint: *fingerprint,
		})
		if err != nil {
			fmt.Fprintln(os.Stderr, "plan add-finding:", err)
			// The package owns the vocabulary, so it owns the split: a value it refuses is a malformed
			// invocation (2), anything else is the plan refusing the row (1).
			if errors.Is(err, plan.ErrUsage) {
				return 2
			}
			return 1
		}
		fmt.Printf("added %s to %s at line %d\n", *id, effectivePath, line)
		return 0
	default:
		fmt.Fprint(os.Stderr, usage)
		return 2
	}
}

// runPlanAdmit decides every row of the plan's Evidence ledger. Without --record it reads the plan and
// writes nothing; with it, the freshness of the observation is pinned into the named rows by rewriting
// the plan file in one pass.
//
// Exit 0 when no row was refused, 1 when at least one was or a digest could not be written, 2 on a
// usage error. --record without --execute is a usage error because recording pins an observation this
// run made: a dry run makes none, and a pinned value nobody observed is the failure this flag exists
// to prevent.
func runPlanAdmit(path string, execute bool, timeout time.Duration, only, record string, sandbox bool, sandboxImage string) int {
	return admit.Run(admit.Request{
		Path:    path,
		Execute: execute,
		Timeout: timeout,
		Only:    strings.Split(only, ","),
		Record:  strings.Split(record, ","),
		Sandbox: sandbox,
		Image:   sandboxImage,
		Dir:     feedback.RepoRoot("."),
	}, admit.Deps{
		Run:           runShell,
		SandboxRunner: sandboxRunner,
		Replay:        admit.Replay,
		Out:           os.Stdout,
		Err:           os.Stderr,
	})
}

// waitDelayAfterKill is how long the output pipes may stay open once the shell is gone: the gap between the
// kill and the copy of its output finishing, so a row that hits its bound is still reported as one.
const waitDelayAfterKill = 2 * time.Second

// runShell runs one admitted command through `sh -c`, so quoting, word splitting, and redirection
// behave the way the ledger's shell commands intend, and points both streams at one buffer so the
// observation is the single stream a row's digest is pinned against. This runs an arbitrary shell
// command, which is exactly why the dry run is the default: --execute is the operator's decision. The
// deadline is classified first, so a command killed by its own timeout is reported as a timeout and
// never as a generic command failure.
func runShell(ctx context.Context, dir, command string) (string, error) {
	cmd := exec.CommandContext(ctx, "sh", "-c", command)
	cmd.Dir = dir
	processGroup(cmd)
	// A bytes.Buffer is served through an operating-system pipe whose copy finishes only at end of file, and
	// the deadline ends the shell, not a descendant that inherited the write end: without WaitDelay that
	// descendant keeps Run blocked past --timeout and no timeout line is ever printed.
	cmd.WaitDelay = waitDelayAfterKill
	var buf bytes.Buffer
	cmd.Stdout, cmd.Stderr = &buf, &buf
	err := cmd.Run()
	// The row is over, so what the shell left behind is reaped here rather than left running past it.
	defer killGroup(cmd)
	if err != nil && errors.Is(ctx.Err(), context.DeadlineExceeded) {
		return buf.String(), fmt.Errorf("run %q: %w", command, context.DeadlineExceeded)
	}
	if errors.Is(err, exec.ErrWaitDelay) {
		return buf.String(), fmt.Errorf("run %q: the command left a process holding its output, so the row's one stream never closed: %w", command, err)
	}
	return buf.String(), err
}

// probeHook runs the wired Stop command the way Claude Code does, with an empty payload on
// stdin, and requires it to exit zero. Matching the command string proves nothing: a binary that
// needs a subcommand looks identical to one that does not. The command is read by the one splitter the
// rest of the tool reads commands with, so a command it cannot read is refused here instead of run as a
// fragment.
func probeHook(command string) error {
	fields, err := hookcmd.ShellWords(command)
	if err != nil || len(fields) == 0 {
		return fmt.Errorf("cannot read the wired command")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, fields[0], fields[1:]...)
	cmd.Stdin = strings.NewReader("{}")
	cmd.Stdout, cmd.Stderr = io.Discard, io.Discard
	return cmd.Run()
}

func runBench(args []string) int {
	if len(args) == 0 {
		fmt.Fprint(os.Stderr, usage)
		return 2
	}
	switch args[0] {
	case "run":
		return runBenchRun(args[1:])
	case "score":
		return runBenchScore(args[1:])
	case "history":
		return runBenchHistory(args[1:])
	case "compare":
		return runBenchCompare(args[1:])
	case "rescore":
		return runBenchRescore(args[1:])
	case "adjudicate":
		return runBenchAdjudicate(args[1:])
	case "eval":
		return runBenchEval(args[1:])
	case "manifest":
		return runBenchManifest(args[1:])
	default:
		fmt.Fprint(os.Stderr, usage)
		return 2
	}
}

func runBenchManifest(args []string) int {
	if len(args) == 0 {
		fmt.Fprint(os.Stderr, usage)
		return 2
	}
	switch args[0] {
	case "build":
		return runBenchManifestBuild(args[1:])
	case "verify":
		return runBenchManifestVerify(args[1:])
	default:
		fmt.Fprint(os.Stderr, usage)
		return 2
	}
}

func runBenchManifestBuild(args []string) int {
	fs := flag.NewFlagSet("bench manifest build", flag.ContinueOnError)
	suite := fs.String("suite", "", "suite name")
	version := fs.String("version", "", "suite version")
	casesFlag := fs.String("cases", "", "comma-separated case IDs or * for every case")
	changeReason := fs.String("change-reason", "", "reason for this benchmark version")
	benchDir := fs.String("bench-dir", "bench", "benchmark directory")
	fromTemplate := fs.String("from-template", "", "JSON manifest specification template")
	out := fs.String("out", "", "output manifest path")
	if err := fs.Parse(args); err != nil {
		return 2
	}
	if fs.NArg() != 0 || *suite == "" || *version == "" || *casesFlag == "" || *changeReason == "" || *out == "" {
		fmt.Fprintln(os.Stderr, "bench manifest build: requires --suite, --version, --cases, --change-reason, and --out")
		return 2
	}
	caseIDs, err := manifestCaseIDs(*benchDir, *casesFlag)
	if err != nil {
		fmt.Fprintf(os.Stderr, "bench manifest build: %v\n", err)
		return 1
	}
	var spec eval.ManifestSpec
	if *fromTemplate != "" {
		data, err := os.ReadFile(*fromTemplate)
		if err != nil {
			fmt.Fprintf(os.Stderr, "bench manifest build: read template: %v\n", err)
			return 1
		}
		if err := json.Unmarshal(data, &spec); err != nil {
			fmt.Fprintf(os.Stderr, "bench manifest build: parse template: %v\n", err)
			return 1
		}
	} else {
		spec, err = eval.DefaultManifestSpec(*suite, *version, *changeReason, *benchDir, caseIDs)
		if err != nil {
			fmt.Fprintf(os.Stderr, "bench manifest build: %v\n", err)
			return 1
		}
	}
	spec.Benchmark, spec.Version, spec.Cases, spec.ChangeReason = *suite, *version, caseIDs, *changeReason
	manifest, err := eval.BuildManifest(*benchDir, spec)
	if err != nil {
		fmt.Fprintf(os.Stderr, "bench manifest build: %v\n", err)
		return 1
	}
	manifest, err = manifest.Seal()
	if err != nil {
		fmt.Fprintf(os.Stderr, "bench manifest build: %v\n", err)
		return 1
	}
	data, err := eval.CanonicalJSON(manifest)
	if err != nil {
		fmt.Fprintf(os.Stderr, "bench manifest build: %v\n", err)
		return 1
	}
	if err := os.WriteFile(*out, data, 0o644); err != nil {
		fmt.Fprintf(os.Stderr, "bench manifest build: write output: %v\n", err)
		return 1
	}
	fmt.Printf("built %s (%s)\n", *out, manifest.ManifestSHA256)
	return 0
}

func runBenchManifestVerify(args []string) int {
	var path string
	if len(args) > 0 && !strings.HasPrefix(args[0], "-") {
		path, args = args[0], args[1:]
	}
	fs := flag.NewFlagSet("bench manifest verify", flag.ContinueOnError)
	benchDir := fs.String("bench-dir", "bench", "benchmark directory")
	if err := fs.Parse(args); err != nil {
		return 2
	}
	if path == "" && fs.NArg() == 1 {
		path = fs.Arg(0)
	} else if fs.NArg() != 0 {
		fmt.Fprintln(os.Stderr, "bench manifest verify: requires one manifest FILE")
		return 2
	}
	if path == "" {
		fmt.Fprintln(os.Stderr, "bench manifest verify: requires one manifest FILE")
		return 2
	}
	manifest, err := eval.LoadManifest(path)
	if err != nil {
		fmt.Fprintf(os.Stderr, "bench manifest verify: %v\n", err)
		return 1
	}
	mismatches := eval.VerifyCases(*benchDir, manifest)
	for _, id := range mismatches {
		fmt.Fprintf(os.Stderr, "bench manifest verify: case %s does not match manifest\n", id)
	}
	if len(mismatches) != 0 {
		return 1
	}
	fmt.Printf("verified %s (%s)\n", path, manifest.ManifestSHA256)
	return 0
}

func manifestCaseIDs(benchDir, selection string) ([]string, error) {
	if selection != "*" {
		ids := strings.Split(selection, ",")
		for i := range ids {
			ids[i] = strings.TrimSpace(ids[i])
			if ids[i] == "" {
				return nil, fmt.Errorf("--cases contains an empty case ID")
			}
		}
		return ids, nil
	}
	entries, err := os.ReadDir(filepath.Join(benchDir, "cases"))
	if err != nil {
		return nil, fmt.Errorf("list cases: %w", err)
	}
	var ids []string
	for _, entry := range entries {
		if entry.IsDir() {
			ids = append(ids, entry.Name())
		}
	}
	if len(ids) == 0 {
		return nil, fmt.Errorf("no case directories under %s", filepath.Join(benchDir, "cases"))
	}
	return ids, nil
}

// configModeFor names the agent configuration a run measured with: a throwaway directory holding only
// the embedded skills, the operator's own configuration, or a directory the operator chose. The
// provenance records it because the three are not the same instrument.
func configModeFor(runner, agentConfig string) string {
	switch {
	case agentConfig == "bench":
		return bench.ConfigBench
	case agentConfig != "":
		return bench.ConfigCustom
	case runner == bench.RunnerPi:
		// A Pi run always gets a throwaway config; the runner exists so a reading is not shaped by
		// the operator's packages, extensions, memory protocol, or MCP servers.
		return bench.ConfigBench
	default:
		return bench.ConfigInherited
	}
}

func runBenchRun(args []string) int {
	fs := flag.NewFlagSet("bench run", flag.ContinueOnError)
	manifestPath := fs.String("manifest", "", "sealed benchmark manifest; selects its exact cases and budgets")
	cases := fs.String("cases", "bench/cases/*", "glob of case directories (each with fixture/ and KEY.json)")
	runner := fs.String("runner", bench.RunnerPi, "agent runner: pi (default) or claude (last resort)")
	model := fs.String("model", "", "model for the agent runs; empty uses the runner's default ("+
		bench.DefaultModel(bench.RunnerPi)+" for pi, "+bench.DefaultModel(bench.RunnerClaude)+" for claude)")
	runs := fs.Int("runs", 1, "runs per case")
	maxTurns := fs.Int("max-turns", 70, "agent turn cap per run (claude runner only; the pi runner has no turn cap)")
	timeout := fs.Duration("timeout", 30*time.Minute, "agent wall-clock cap per run")
	suiteTimeout := fs.Duration("suite-timeout", 10*time.Minute, "fixture suite cap")
	maxCost := fs.Float64("max-cost-usd", 0, "stop when the cumulative cost reaches this (0 = no ceiling)")
	out := fs.String("out", "", "results directory (default: <bench-dir>/results/<timestamp>)")
	benchDir := fs.String("bench-dir", "bench", "benchmark directory holding history.jsonl and history.md")
	dryRun := fs.Bool("dry-run", false, "scaffold and check fixtures, spawn no agent, write no history")
	keep := fs.Bool("keep", false, "keep workspaces after scoring")
	retries := fs.Int("retries", 1, "retries per case on infrastructure failures (exit status, error result)")
	retryDelay := fs.Duration("retry-delay", 60*time.Second, "pause before a retry")
	workers := fs.Int("concurrency", 1, "cases to run side by side; the wall clock shortens, the cost does not")
	agentConfig := fs.String("agent-config", "", "agent config directory; \"bench\" builds a throwaway one holding only the embedded skills (the default for --runner pi)")
	if err := fs.Parse(args); err != nil {
		return 2
	}
	provided := make(map[string]bool)
	fs.Visit(func(f *flag.Flag) { provided[f.Name] = true })
	if !bench.KnownRunner(*runner) {
		fmt.Fprintf(os.Stderr, "bench run: unknown runner %q: use %s or %s\n", *runner, bench.RunnerPi, bench.RunnerClaude)
		return 2
	}
	var manifest *eval.Manifest
	var caseIDs []string
	var budgetIdentity string
	var caseTimeout, suiteWallTimeout time.Duration
	var maxAttemptsPerCase, maxTokensPerCaseRun int
	var maxCostPerCaseRun float64
	if *manifestPath != "" {
		loaded, err := eval.LoadManifest(*manifestPath)
		if err != nil {
			fmt.Fprintf(os.Stderr, "bench run: %v\n", err)
			return 1
		}
		manifest = &loaded
		if mismatches := eval.VerifyCases(*benchDir, loaded); len(mismatches) != 0 {
			fmt.Fprintf(os.Stderr, "bench run: manifest cases do not match %s: %s\n", *benchDir, strings.Join(mismatches, ", "))
			return 1
		}
		if err := validateManifestRun(loaded, *runner); err != nil {
			fmt.Fprintf(os.Stderr, "bench run: %v\n", err)
			return 1
		}
		caseIDs = make([]string, len(loaded.Cases))
		for i, c := range loaded.Cases {
			caseIDs[i] = c.ID
		}
		if provided["cases"] && !sameCaseIDs(*cases, caseIDs) {
			fmt.Fprintln(os.Stderr, "bench run: --cases conflicts with the manifest's exact case set")
			return 2
		}
		if provided["runs"] && *runs != loaded.Replicates.KTarget {
			fmt.Fprintf(os.Stderr, "bench run: --runs=%d conflicts with manifest k_target=%d\n", *runs, loaded.Replicates.KTarget)
			return 2
		}
		caseTimeout = time.Duration(loaded.Budgets.MaxRuntimePerCaseRunSeconds) * time.Second
		suiteWallTimeout = time.Duration(loaded.Budgets.MaxRuntimeSuiteSeconds) * time.Second
		if provided["timeout"] && *timeout != caseTimeout {
			fmt.Fprintln(os.Stderr, "bench run: --timeout conflicts with manifest per-case runtime budget")
			return 2
		}
		if provided["suite-timeout"] && *suiteTimeout != caseTimeout {
			fmt.Fprintln(os.Stderr, "bench run: --suite-timeout conflicts with manifest per-case runtime budget")
			return 2
		}
		if provided["max-cost-usd"] && *maxCost != loaded.Budgets.MaxCostSuiteUSD {
			fmt.Fprintln(os.Stderr, "bench run: --max-cost-usd conflicts with manifest suite cost budget")
			return 2
		}
		if provided["retries"] && *retries != loaded.Budgets.MaxRetries {
			fmt.Fprintln(os.Stderr, "bench run: --retries conflicts with manifest retry budget")
			return 2
		}
		if provided["concurrency"] && *workers != 1 {
			fmt.Fprintln(os.Stderr, "bench run: manifest-bound scheduling requires --concurrency=1 to bound suite-cost overshoot")
			return 2
		}
		if provided["max-turns"] && *maxTurns != 70 {
			fmt.Fprintln(os.Stderr, "bench run: --max-turns is not defined by the manifest and cannot be overridden")
			return 2
		}
		*runs = loaded.Replicates.KTarget
		*timeout, *suiteTimeout = caseTimeout, caseTimeout
		*maxCost, *retries, *workers = loaded.Budgets.MaxCostSuiteUSD, loaded.Budgets.MaxRetries, 1
		maxAttemptsPerCase = loaded.Budgets.MaxAttemptsPerCase
		maxTokensPerCaseRun = loaded.Budgets.MaxTokensPerCaseRun
		maxCostPerCaseRun = loaded.Budgets.MaxCostPerCaseRunUSD
		budgetIdentity = resolvedBudgetIdentity(loaded, caseIDs)
	}
	if *model == "" {
		*model = bench.DefaultModel(*runner)
	}
	if *out == "" {
		*out = filepath.Join(*benchDir, "results", bench.Stamp(time.Now()))
	}
	cfgDir, skillFile := *agentConfig, filepath.Join(defaultConfigDir(), "skills", "test-strategy", "SKILL.md")
	switch {
	case *runner == bench.RunnerPi && (cfgDir == "" || cfgDir == "bench"):
		// A Pi run always gets a throwaway config: the runner exists so a reading is not shaped by
		// the operator's packages, extensions, memory protocol, or MCP servers, and so no credential
		// is shared by link. It is built under the results directory, which the run owns and git
		// ignores, rather than beside the Claude one in bench/.
		cfgDir = filepath.Join(*out, ".pi-agent-config")
		from := bench.DefaultPiConfigDir()
		if err := bench.WriteBenchPiConfig(cfgDir, from); err != nil {
			fmt.Fprintln(os.Stderr, "bench config:", err)
			return 1
		}
		if _, err := os.Stat(filepath.Join(from, bench.PiAuthFile)); err != nil {
			// Nothing to copy and nothing to link: the run stays alive and reports the authentication
			// failure itself, with the CLI's own message, instead of failing here for a wrong reason.
			fmt.Fprintf(os.Stderr, "bench config: no %s in %s; the run will report the authentication error itself\n", bench.PiAuthFile, from)
		}
		if abs, err := filepath.Abs(cfgDir); err == nil {
			cfgDir = abs
		}
	case cfgDir == "bench":
		// A throwaway configuration holding only the embedded skills, so the run measures them
		// and not the operator's global instructions, memory protocol, or MCP servers.
		cfgDir = filepath.Join(*benchDir, ".agent-config")
		if err := bench.WriteBenchConfig(cfgDir, defaultConfigDir()); err != nil {
			fmt.Fprintln(os.Stderr, "bench config:", err)
			return 1
		}
		if abs, err := filepath.Abs(cfgDir); err == nil {
			cfgDir = abs
		}
	}
	if cfgDir != "" {
		skillFile = filepath.Join(cfgDir, "skills", "test-strategy", "SKILL.md")
	}
	options := bench.Options{
		CasesGlob: *cases, CaseIDs: caseIDs, Model: *model, Runner: *runner, Runs: *runs, MaxTurns: *maxTurns,
		Timeout: *timeout, SuiteTimeout: *suiteTimeout, WallTimeout: suiteWallTimeout,
		MaxAttemptsPerCase: maxAttemptsPerCase, MaxTokensPerCaseRun: maxTokensPerCaseRun,
		MaxCostPerCaseRunUSD: maxCostPerCaseRun, MaxCostUSD: *maxCost, Out: *out, BenchDir: *benchDir,
		SkillFile: skillFile, ConfigDir: cfgDir, BinDir: selfDir(), Workers: *workers,
		ConfigMode: configModeFor(*runner, *agentConfig), DryRun: *dryRun, Keep: *keep,
		Retries: *retries, RetryDelay: *retryDelay, Log: os.Stdout,
	}
	if manifest != nil {
		options.StrictManifest, options.ManifestSHA256 = true, manifest.ManifestSHA256
		options.ManifestSuite, options.ManifestVersion = manifest.Benchmark, manifest.Version
		options.BudgetIdentity = budgetIdentity
		options.PostRunCheck = func() error {
			if mismatches := eval.VerifyCases(*benchDir, *manifest); len(mismatches) != 0 {
				return fmt.Errorf("manifest cases changed during execution: %s", strings.Join(mismatches, ", "))
			}
			return nil
		}
	}
	_, code := bench.Run(options)
	// The results line is a promise about a file: a run that matched no case, or one whose record could not be
	// written, has no summary to point at, and the operator is sent to a path that does not exist.
	summaryPath := filepath.Join(*out, "summary.md")
	if _, err := os.Stat(summaryPath); err == nil {
		fmt.Printf("results: %s\n", summaryPath)
	}
	return code
}

func validateManifestRun(m eval.Manifest, runner string) error {
	if m.ManifestSHA256 == "" || len(m.Cases) == 0 {
		return fmt.Errorf("manifest must be sealed and select at least one case")
	}
	if m.Replicates.KMin < 1 || m.Replicates.KMin > m.Replicates.KTarget || m.Replicates.KTarget > m.Replicates.KMax {
		return fmt.Errorf("manifest replicate bounds are invalid")
	}
	b := m.Budgets
	if b.MaxCases < 1 || len(m.Cases) > b.MaxCases || b.MaxAttemptsPerCase < 1 || b.MaxRetries < 0 || b.MaxRuntimePerCaseRunSeconds < 1 || b.MaxRuntimeSuiteSeconds < 1 {
		return fmt.Errorf("manifest has an absent or invalid required case, attempt, retry, or runtime budget")
	}
	if b.MaxTokensPerCaseRun < 0 || b.MaxCostPerCaseRunUSD < 0 || b.MaxCostSuiteUSD < 0 {
		return fmt.Errorf("manifest resource budgets must not be negative")
	}
	if !bench.KnownRunner(runner) {
		return fmt.Errorf("runner %q has no manifest budget support", runner)
	}
	// Pi and Claude expose final usage in the response formats parsed by the bench; cost/token thresholds
	// are observable after a response, not provider-enforced mid-response limits.
	return nil
}

func sameCaseIDs(value string, ids []string) bool {
	parts := strings.Split(value, ",")
	if len(parts) != len(ids) {
		return false
	}
	for i, part := range parts {
		if strings.TrimSpace(part) != ids[i] {
			return false
		}
	}
	return true
}

// resolvedBudgetIdentity digests the budgets the manifest actually defines — cases, replicates,
// attempts, retries, and the runtime and resource ceilings. The turn cap is deliberately absent:
// it is not a manifest budget (the manifest cannot define one) and only the claude runner enforces
// it, so hashing it into the identity would claim an enforcement the pi runner does not have.
func resolvedBudgetIdentity(m eval.Manifest, ids []string) string {
	b := m.Budgets
	resolved := struct {
		Manifest                  string
		Cases                     []string
		Runs, Retries, Attempts   int
		Tokens                    int
		CaseCost, SuiteCost       float64
		CaseRuntime, SuiteRuntime int
	}{m.ManifestSHA256, ids, m.Replicates.KTarget, b.MaxRetries, b.MaxAttemptsPerCase,
		b.MaxTokensPerCaseRun, b.MaxCostPerCaseRunUSD, b.MaxCostSuiteUSD,
		b.MaxRuntimePerCaseRunSeconds, b.MaxRuntimeSuiteSeconds}
	data, _ := json.Marshal(resolved)
	sum := sha256.Sum256(data)
	return "sha256:" + hex.EncodeToString(sum[:])
}

func runBenchScore(args []string) int {
	fs := flag.NewFlagSet("bench score", flag.ContinueOnError)
	caseDir := fs.String("case", "", "case directory holding KEY.json")
	ws := fs.String("workspace", "", "workspace to score")
	planFile := fs.String("plan", "", "plan file to score, such as the test-plan.md a run keeps beside result.json")
	adjFlag := fs.String("adjudication", "", "adjudication record to apply; named here or not at all, never discovered beside the plan (none is the same as omitting it)")
	if err := fs.Parse(args); err != nil || *caseDir == "" || (*ws == "") == (*planFile == "") {
		fmt.Fprintln(os.Stderr, "bench score needs --case and exactly one of --workspace or --plan")
		return 2
	}
	key, err := bench.LoadKey(*caseDir)
	if err != nil {
		fmt.Fprintln(os.Stderr, "score:", err)
		return 1
	}
	adj, err := scoreAdjudication(*adjFlag)
	if err != nil {
		fmt.Fprintln(os.Stderr, "score:", err)
		return 1
	}
	enc := json.NewEncoder(os.Stdout)
	enc.SetIndent("", "  ")
	var res bench.Result
	if *planFile != "" {
		res, err = bench.ScorePlanFileWithAdjudication(*planFile, key, adj)
	} else {
		res, err = bench.ScoreWorkspaceWithAdjudication(*ws, key, adj)
		if err == nil {
			res.Catch = bench.Discriminate(*caseDir, *ws, key, 10*time.Minute)
			res.Caught = res.Catch.Count()
		}
	}
	if err != nil {
		fmt.Fprintln(os.Stderr, "score:", err)
		return 1
	}
	if err := enc.Encode(res); err != nil {
		fmt.Fprintln(os.Stderr, "score:", err)
		return exitArtifact
	}
	return 0
}

// scoreAdjudication resolves the record a score applies. The record has authority over the score, so it is
// applied only when the caller names it here. It is deliberately never discovered beside the plan: the plan a
// workspace holds sits in the area the evaluated subject writes, and a record found there would let the subject
// rule on its own finding rows — closing them as out of scope, leaving the precision denominator, and reporting
// a run that invented findings as one with none. `bench rescore` reads the record of a run from that run's own
// directory, which the runner owns, and names it explicitly.
func scoreAdjudication(flagValue string) (*bench.Adjudication, error) {
	switch flagValue {
	case "", "none":
		return nil, nil
	default:
		loaded, err := bench.LoadAdjudication(flagValue)
		if err != nil {
			return nil, err
		}
		return &loaded, nil
	}
}

// adjudicateInput is one invocation of `bench adjudicate`, parsed and checked before any file is read.
type adjudicateInput struct {
	runDir   string
	planPath string
	row      int
	verdict  string
	defect   string
	by       string
	reason   string
	ts       string
	replace  bool
	show     bool
	pending  bool
}

func runBenchAdjudicate(args []string) int {
	in, code := parseAdjudicate(args)
	if code != 0 {
		return code
	}
	switch {
	case in.show:
		return adjudicateShow(in)
	case in.pending:
		return adjudicatePending(in)
	default:
		return adjudicateRecord(in)
	}
}

// parseAdjudicate keeps every refusal about the invocation's own values here: an unknown verdict or a
// missing actor is a usage error (exit 2), while a row the plan does not have is the data's refusal.
func parseAdjudicate(args []string) (adjudicateInput, int) {
	fs := flag.NewFlagSet("bench adjudicate", flag.ContinueOnError)
	var in adjudicateInput
	fs.StringVar(&in.runDir, "run", "", "results directory of one case run: it holds result.json and the kept test-plan.md")
	fs.StringVar(&in.planPath, "plan", "", "plan file to decide against (default: <run>/test-plan.md)")
	fs.IntVar(&in.row, "row", 0, "1-based Findings row to decide")
	fs.StringVar(&in.verdict, "verdict", "", "defect | false_positive | out_of_scope")
	fs.StringVar(&in.defect, "defect", "", "keyed defect id, required for verdict defect")
	fs.StringVar(&in.by, "by", "", "who decided")
	fs.StringVar(&in.reason, "reason", "", "why, in one line")
	fs.StringVar(&in.ts, "ts", "", "decision timestamp in RFC3339 (default: now, UTC)")
	fs.BoolVar(&in.replace, "replace", false, "displace the decision already standing for the row")
	fs.BoolVar(&in.show, "show", false, "print the record and the rows it decides")
	fs.BoolVar(&in.pending, "pending", false, "print the finding rows still undecided")
	if err := fs.Parse(args); err != nil {
		return in, 2
	}
	if in.runDir == "" {
		fmt.Fprintln(os.Stderr, "bench adjudicate: --run <dir> is required")
		return in, 2
	}
	if in.planPath == "" {
		in.planPath = filepath.Join(in.runDir, "test-plan.md")
	}
	if in.show || in.pending {
		if in.show && in.pending {
			fmt.Fprintln(os.Stderr, "bench adjudicate: --show and --pending are two different readings; pass one")
			return in, 2
		}
		return in, 0
	}
	return in, checkDecisionValues(in)
}

// checkDecisionValues refuses an invocation whose values the record would reject anyway, so the
// operator hears about it before anything is written.
func checkDecisionValues(in adjudicateInput) int {
	problems := []string{}
	switch in.verdict {
	case bench.VerdictDefect:
		if in.defect == "" {
			problems = append(problems, "--defect <id> is required for verdict defect")
		}
	case bench.VerdictFalsePositive, bench.VerdictOutOfScope:
		if in.defect != "" {
			problems = append(problems, "--defect belongs to verdict defect only")
		}
	default:
		problems = append(problems, fmt.Sprintf("--verdict %q is not defect, false_positive or out_of_scope", in.verdict))
	}
	if in.row < 1 {
		problems = append(problems, "--row <n> is required, 1-based")
	}
	if strings.TrimSpace(in.by) == "" {
		problems = append(problems, "--by <who> is required: a decision without an author is not auditable")
	}
	if strings.TrimSpace(in.reason) == "" {
		problems = append(problems, "--reason <why> is required")
	}
	if in.ts != "" {
		if _, err := time.Parse(time.RFC3339, in.ts); err != nil {
			problems = append(problems, "--ts must be RFC3339, such as 2026-09-17T12:00:00Z")
		}
	}
	if len(problems) > 0 {
		fmt.Fprintln(os.Stderr, "bench adjudicate: "+strings.Join(problems, "; "))
		return 2
	}
	return 0
}

// loadAdjudication reads the record of the run the invocation names. It is read from the run's own
// directory rather than from beside the plan: the plan can be named anywhere, including the workspace
// the evaluated subject wrote, while the run directory is the runner's. A missing file means nothing is
// decided yet; a malformed one is a refusal, because a record that cannot be read must not be replaced.
func loadAdjudication(in adjudicateInput) (*bench.Adjudication, error) {
	path := filepath.Join(in.runDir, bench.AdjudicationFile)
	record, err := bench.LoadAdjudication(path)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil, nil
		}
		return nil, err
	}
	return &record, nil
}

// adjudicateRunInfo is the identity a decision is recorded under: the case and run the result.json
// names. It is read rather than inferred from the path, so a moved directory cannot relabel a record.
type adjudicateRunInfo struct {
	Case string `json:"case"`
	Run  int    `json:"run"`
}

func adjudicateRecord(in adjudicateInput) int {
	info, code := readAdjudicateRun(in)
	if code != 0 {
		return code
	}
	plan, err := os.ReadFile(in.planPath)
	if err != nil {
		fmt.Fprintln(os.Stderr, "adjudicate:", err)
		return 1
	}
	fingerprint, err := bench.FindingRowFingerprint(string(plan), in.row)
	if err != nil {
		fmt.Fprintln(os.Stderr, "adjudicate:", err)
		return 1
	}
	record, err := loadAdjudication(in)
	if err != nil {
		fmt.Fprintln(os.Stderr, "adjudicate:", err)
		return 1
	}
	if record == nil {
		record = &bench.Adjudication{Case: info.Case, Run: info.Run}
	}
	if record.Case != info.Case || record.Run != info.Run {
		fmt.Fprintf(os.Stderr, "adjudicate: the record decides case %q run %d, not case %q run %d\n", record.Case, record.Run, info.Case, info.Run)
		return 1
	}
	ts := in.ts
	if ts == "" {
		ts = time.Now().UTC().Format(time.RFC3339)
	}
	decision := bench.Decision{Row: in.row, RowFingerprint: fingerprint, Verdict: in.verdict, Defect: in.defect, By: in.by, TS: ts, Reason: in.reason}
	if err := record.Decide(decision, in.replace); err != nil {
		fmt.Fprintln(os.Stderr, "adjudicate:", err)
		return 1
	}
	if err := record.Validate(); err != nil {
		fmt.Fprintln(os.Stderr, "adjudicate:", err)
		return 1
	}
	if err := writeAdjudication(filepath.Join(in.runDir, bench.AdjudicationFile), *record); err != nil {
		fmt.Fprintln(os.Stderr, "adjudicate:", err)
		return exitArtifact
	}
	rows := bench.FindingRowsText(string(plan))
	fmt.Printf("row %d %s: %s\n%s\n", in.row, shortFingerprint(fingerprint), in.verdict, rows[in.row-1])
	return 0
}

func adjudicateShow(in adjudicateInput) int {
	record, err := loadAdjudication(in)
	if err != nil {
		fmt.Fprintln(os.Stderr, "adjudicate:", err)
		return 1
	}
	if record == nil {
		fmt.Println("no decisions recorded")
		return 0
	}
	fmt.Printf("case %s run %d\n", record.Case, record.Run)
	for _, d := range record.Decisions {
		fmt.Printf("row %d %s %s by %s at %s: %s\n", d.Row, shortFingerprint(d.RowFingerprint), d.Verdict, d.By, d.TS, d.Reason)
	}
	if len(record.Superseded) > 0 {
		fmt.Println("superseded:")
		for _, d := range record.Superseded {
			fmt.Printf("row %d %s %s by %s at %s: %s\n", d.Row, shortFingerprint(d.RowFingerprint), d.Verdict, d.By, d.TS, d.Reason)
		}
	}
	return 0
}

func adjudicatePending(in adjudicateInput) int {
	record, err := loadAdjudication(in)
	if err != nil {
		fmt.Fprintln(os.Stderr, "adjudicate:", err)
		return 1
	}
	decided := map[int]bool{}
	if record != nil {
		for _, d := range record.Decisions {
			decided[d.Row] = true
		}
	}
	plan, err := os.ReadFile(in.planPath)
	if err != nil {
		fmt.Fprintln(os.Stderr, "adjudicate:", err)
		return 1
	}
	rows := bench.FindingRowsText(string(plan))
	pending := 0
	for i := range rows {
		row := i + 1
		if decided[row] {
			continue
		}
		pending++
		fingerprint, err := bench.FindingRowFingerprint(string(plan), row)
		if err != nil {
			fmt.Fprintln(os.Stderr, "adjudicate:", err)
			return 1
		}
		fmt.Printf("row %d %s %s\n", row, shortFingerprint(fingerprint), rows[i])
	}
	if pending == 0 {
		fmt.Println("no pending rows")
	}
	return 0
}

// readAdjudicateRun reads the case and run the directory's result.json names, and refuses a directory
// whose result cannot be read: deciding a run nobody can identify would produce a record the scorer
// refuses anyway.
func readAdjudicateRun(in adjudicateInput) (adjudicateRunInfo, int) {
	var info adjudicateRunInfo
	data, err := os.ReadFile(filepath.Join(in.runDir, "result.json"))
	if err != nil {
		fmt.Fprintln(os.Stderr, "adjudicate:", err)
		return info, 1
	}
	if err := json.Unmarshal(data, &info); err != nil {
		fmt.Fprintf(os.Stderr, "adjudicate: result.json: %v\n", err)
		return info, 1
	}
	if info.Case == "" || info.Run < 1 {
		fmt.Fprintf(os.Stderr, "adjudicate: result.json names case %q run %d; a record needs both\n", info.Case, info.Run)
		return info, 1
	}
	return info, 0
}

// writeAdjudication writes the record so a reader sees either the whole file or the one before it: a
// half-written decision is an audit trail that lies.
func writeAdjudication(path string, record bench.Adjudication) error {
	data, err := json.MarshalIndent(record, "", "  ")
	if err != nil {
		return err
	}
	tmp, err := os.CreateTemp(filepath.Dir(path), ".adjudication-*")
	if err != nil {
		return err
	}
	defer os.Remove(tmp.Name())
	if _, err := tmp.Write(append(data, '\n')); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	if err := os.Chmod(tmp.Name(), 0o644); err != nil {
		return err
	}
	return os.Rename(tmp.Name(), path)
}

func shortFingerprint(fingerprint string) string {
	if len(fingerprint) <= 12 {
		return fingerprint
	}
	return fingerprint[:12]
}

func runBenchRescore(args []string) int {
	fs := flag.NewFlagSet("bench rescore", flag.ContinueOnError)
	benchDir := fs.String("bench-dir", "bench", "benchmark directory holding cases/ and the history")
	suiteTimeout := fs.Duration("suite-timeout", 10*time.Minute, "cap per suite run")
	if err := fs.Parse(args); err != nil || fs.NArg() != 1 {
		fmt.Fprintln(os.Stderr, "bench rescore needs one results directory")
		return 2
	}
	results := fs.Arg(0)
	lookup := func(name string) (string, error) {
		dir := filepath.Join(*benchDir, "cases", name)
		if st, err := os.Stat(dir); err != nil || !st.IsDir() {
			return "", fmt.Errorf("no case directory %s", dir)
		}
		return dir, nil
	}
	agg, err := bench.Rescore(results, lookup, *suiteTimeout)
	if err != nil {
		fmt.Fprintln(os.Stderr, "rescore:", err)
		return 1
	}
	_ = bench.AppendHistory(*benchDir, bench.HistoryEntry{
		TS: agg.TS, Out: agg.Out, Model: agg.Model, Cases: len(agg.Cases), Defects: agg.Defects,
		Found: agg.Found, Recall: agg.Recall, Caught: agg.Caught, RecallCaught: agg.RecallCaught,
		FalsePositives: agg.FalsePositives, Failed: agg.Failed, Invalid: agg.Invalid, NoPlan: agg.NoPlan,
		Kind: bench.KindRescore, RunTS: agg.RunTS, SourceRun: results, SkillVersion: agg.SkillVersion,
		Corpus: agg.Corpus, LightActivated: agg.LightActivated, MicroActivated: agg.MicroActivated, Runs: agg.Runs,
	})
	fmt.Print(bench.Summary(agg))
	return 0
}

func runBenchCompare(args []string) int {
	if len(args) != 2 {
		fmt.Fprintln(os.Stderr, "bench compare needs two result directories: <before> <after>")
		return 2
	}
	cmp, err := bench.Compare(args[0], args[1])
	if err != nil {
		fmt.Fprintln(os.Stderr, "compare:", err)
		return 1
	}
	fmt.Print(cmp.Markdown())
	return 0
}

func runBenchHistory(args []string) int {
	fs := flag.NewFlagSet("bench history", flag.ContinueOnError)
	benchDir := fs.String("bench-dir", "bench", "benchmark directory")
	if err := fs.Parse(args); err != nil {
		return 2
	}
	data, err := os.ReadFile(filepath.Join(*benchDir, "history.md"))
	if err != nil {
		fmt.Println("no benchmark history yet")
		return 0
	}
	fmt.Print(string(data))
	return 0
}

// sandboxImageDefault is the smallest official Go image that satisfies this module's `go 1.26` directive. It
// is a default and not a decision: the image a row needs is the image its command needs, so --sandbox-image
// exists, and the default only spares the common case a flag.
const sandboxImageDefault = "golang:1.26-alpine"

// sandboxRunner returns a runner that observes one command inside a container. mount is how the container sees
// dir, and it is what decides how narrow the confinement is: with a read-only mount a write lands on the mount
// instead of the working tree, while a replay's writable mount is a copy this tool owns and never the tree the
// user is looking at. The network is gone in both. It is the only confinement this tool offers, and the command
// inside it is still arbitrary code.
//
// The invocation was measured rather than guessed, and three of its parts are load-bearing:
//   - `--tmpfs /tmp:exec`: Docker mounts a tmpfs noexec by default, and Go then dies with
//     `fork/exec ...: permission denied`, which reads like a repository permission bug rather than a sandbox one.
//   - a writable `GOCACHE`: the build cache is written on every run, and the image's own cache directory sits
//     behind --read-only. `GOTMPDIR` was measured unnecessary.
//   - `-v <dir>:/w:<mount>` with `-w /w`: the command needs the tree, and the tree is the thing that must not
//     change.
//   - `--quiet`: an image pulled on first use prints its progress into the same buffer as the command, so the
//     first run's output would differ from the second and every row would read as unstable.
//
// `--read-only` and `--network none` do not change whether a command passes; they are exactly the isolation this
// mode claims, so they are asserted here rather than relied on to make anything work.
func sandboxRunner(image, mount string) admit.Runner {
	return func(ctx context.Context, dir, command string) (string, error) {
		if !filepath.IsAbs(dir) {
			return "", evidence.Refusal{
				Reason: evidence.ReasonMisconfigured,
				Detail: fmt.Sprintf("the sandbox mounts the working directory by absolute path, and %q is not one", dir),
			}
		}
		cmd := exec.CommandContext(ctx, "docker",
			"run", "--rm", "--quiet",
			"--network", "none",
			"--read-only",
			"--tmpfs", "/tmp:exec",
			"-v", dir+":/w:"+mount,
			"-w", "/w",
			"-e", "GOCACHE=/tmp/gocache",
			image, "sh", "-c", command,
		)
		var buf bytes.Buffer
		cmd.Stdout, cmd.Stderr = &buf, &buf
		err := cmd.Run()
		output := buf.String()
		if err == nil {
			return output, nil
		}
		// The caller's deadline must stay recognisable to the admission, so it is returned as it is rather
		// than wrapped in a refusal about the sandbox.
		if ctx.Err() != nil {
			return output, ctx.Err()
		}
		return output, sandboxRefusal(image, output, err)
	}
}

// sandboxRefusal names why a sandboxed command produced no observation. Three of these are about the sandbox
// and not about the row, and they are told apart by the text Docker and the container emit, because neither
// reports a machine-readable code for them. That is a heuristic and it is declared as one: naming the common
// failures is worth more than a generic command-failed that sends the reader to the row, as long as what the
// reader is told is what was observed. One more needs no heuristic at all: a docker that never started arrives
// as an *exec.Error, and reading that as a failing command would send the reader to the row for a machine that
// has no container runtime.
//
// One failure has no signal at all and is not invented here: a command that needs a service on this machine
// fails inside the container with an empty stderr, which is indistinguishable from a test that simply failed.
func sandboxRefusal(image, output string, err error) error {
	// A sandbox that never started is about the sandbox, not about the row: exec reports a binary it could not
	// find or start as an *exec.Error, and a docker that ran and failed as an *exec.ExitError, so the two are
	// told apart by type rather than by reading a message.
	var start *exec.Error
	if errors.As(err, &start) {
		return evidence.Refusal{
			Reason: evidence.ReasonMisconfigured,
			Detail: fmt.Sprintf("the sandbox could not start: %v; every row is observed in a container in this mode, so check that docker is installed and on PATH", err),
		}
	}
	var exit *exec.ExitError
	if errors.As(err, &exit) && exit.ExitCode() == 125 {
		return evidence.Refusal{
			Reason: evidence.ReasonMisconfigured,
			Detail: fmt.Sprintf("the sandbox could not start a container: %v; docker run itself failed, so check that the daemon is reachable and that the image %q can be pulled (its own message is above)", err, image),
		}
	}
	switch {
	case strings.Contains(output, "Read-only file system"):
		return evidence.Refusal{
			Reason: evidence.ReasonSandboxReadOnly,
			Detail: "the command tried to write inside the working tree, which the sandbox mounts read-only; a row that writes is not admissible in this mode, and the write did not reach this machine",
		}
	case strings.Contains(output, "fork/exec") && strings.Contains(output, "permission denied"):
		return evidence.Refusal{
			Reason: evidence.ReasonMisconfigured,
			Detail: "the container could not execute the binary it built: this sandbox's own configuration mounts a tmpfs without exec, which is not a statement about the row",
		}
	case strings.Contains(output, "no such host"), strings.Contains(output, "bad address"),
		strings.Contains(output, "network is unreachable"), strings.Contains(output, "dial tcp"),
		strings.Contains(output, "connection refused"), strings.Contains(output, "Temporary failure in name resolution"):
		return evidence.Refusal{
			Reason: evidence.ReasonNoNetwork,
			Detail: "the command needed the network, which the sandbox removes; a row that reaches out or dials a service is not admissible in this mode",
		}
	}
	return err
}

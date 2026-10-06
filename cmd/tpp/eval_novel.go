package main

import (
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"time"
	"unicode"

	"github.com/alesierraalta/tpp/internal/bench"
	"github.com/alesierraalta/tpp/internal/eval"
	"github.com/alesierraalta/tpp/internal/hookcmd"
	plan "github.com/alesierraalta/tpp/internal/plan"
)

// runBenchEvalProveNovel records exactly one novel-proof/2 event for one NOVEL_CANDIDATE
// finding of an ADJUDICATING, manifest-bound, leak-free run, after an actual replay of the
// saved test against an explicit control (bench.ReplayNovelSavedTest). Every check runs
// before the single SaveRun: a refused invocation leaves run.json, caserun.json and
// events.jsonl byte-identical. The replay runs only with --execute; this command claims
// no sandbox or secret isolation — the tests execute repository code on this machine.
// Serialization: from before LoadRun until after SaveRun (or the refusal that ends the
// command), this command holds the exclusive run-dir lock plan.LockPlan takes — an
// advisory flock keyed on the run directory's canonical path, in the per-user cache
// directory, blocking, and released by the kernel if this process dies. A concurrent
// prove-novel waits, then loads the proof the winner saved and refuses "already has an
// effective novel proof" instead of racing the write and reporting a success that was
// overwritten; a lock that cannot be taken refuses here, before any mutation, never
// proceeding unlocked. The dry run takes the same lock — one acquisition point, released
// on return either way — but writes nothing. prove-novel and confirm-novel are
// serialized: the other eval subcommands (adjudicate, confirm, close, verify, ...) are
// NOT yet locked against concurrent writers.
func runBenchEvalProveNovel(args []string) int {
	fs := evalFlagSet("bench eval prove-novel")
	fs.Usage = func() {
		fmt.Fprintln(fs.Output(), "usage: tpp bench eval prove-novel --eval <dir> --case <id> --finding <id> --control-dir <dir> --control-name <name> --domain <domain> --severity <sev> --issue-type <type> --expected-behavior <text> --failure-condition <text> --mechanism <text> --by <who> --reason <why> [--bench-dir <dir>] [--execute]")
		fmt.Fprintln(fs.Output(), "records exactly one novel-proof/2 event for one NOVEL_CANDIDATE finding after an actual saved-test replay.")
		fmt.Fprintln(fs.Output(), "no sandbox or secret isolation; tests execute repository code.")
		fs.PrintDefaults()
	}
	runDir := fs.String("eval", "", "evaluation run directory")
	caseID := fs.String("case", "", "case ID")
	findingID := fs.String("finding", "", "finding ID")
	controlDir := fs.String("control-dir", "", "control directory the replay compares against")
	controlName := fs.String("control-name", "", "control name recorded in the proof")
	domain := fs.String("domain", "", "blind fact: taxonomy domain")
	severity := fs.String("severity", "", "blind fact: severity")
	issueType := fs.String("issue-type", "", "blind fact: issue type")
	expectedBehavior := fs.String("expected-behavior", "", "blind fact: expected behavior")
	failureCondition := fs.String("failure-condition", "", "blind fact: failure condition")
	mechanism := fs.String("mechanism", "", "blind fact: mechanism")
	by := fs.String("by", "", "adjudicator")
	reason := fs.String("reason", "", "decision reason")
	benchDir := fs.String("bench-dir", "bench", "benchmark directory")
	execute := fs.Bool("execute", false, "run the saved tests (no sandbox or secret isolation; tests execute repository code)")
	if fs.Parse(args) != nil {
		return 2
	}
	if fs.NArg() != 0 {
		fmt.Fprintln(os.Stderr, "bench eval prove-novel: takes no positional arguments")
		return 2
	}
	// Flag-level validation refuses before the run directory is touched: no invocation that
	// cannot prove anything may reach LoadRun, let alone the replay.
	var missing []string
	for _, required := range []struct{ name, value string }{
		{"--eval", *runDir}, {"--case", *caseID}, {"--finding", *findingID},
		{"--control-dir", *controlDir}, {"--control-name", *controlName},
		{"--domain", *domain}, {"--severity", *severity}, {"--issue-type", *issueType},
		{"--expected-behavior", *expectedBehavior}, {"--failure-condition", *failureCondition},
		{"--mechanism", *mechanism}, {"--by", *by}, {"--reason", *reason},
	} {
		if strings.TrimSpace(required.value) == "" {
			missing = append(missing, required.name)
		}
	}
	if len(missing) > 0 {
		fmt.Fprintf(os.Stderr, "bench eval prove-novel: requires %s (all flags must be nonblank)\n", strings.Join(missing, ", "))
		return 2
	}
	if !saneControlName(*controlName) {
		fmt.Fprintf(os.Stderr, "bench eval prove-novel: --control-name %q must be a single sane name (no path separators or control characters)\n", *controlName)
		return 2
	}
	domainFact, ok := eval.ParseDomain(*domain)
	if !ok {
		fmt.Fprintf(os.Stderr, "bench eval prove-novel: --domain %q is not a taxonomy domain\n", *domain)
		return 2
	}
	severityFact, ok := eval.ParseSeverity(*severity)
	if !ok {
		fmt.Fprintf(os.Stderr, "bench eval prove-novel: --severity %q is not info, low, medium, high, or critical\n", *severity)
		return 2
	}

	// One exclusive lock spans the whole transaction: everything from this LoadRun to the
	// SaveRun below (or the refusal that ends the command) runs under it, so a second
	// prove-novel queues instead of reading the pre-proof state — it sees the winner's saved
	// proof and refuses instead of overwriting it. The dry run takes it too (one acquisition
	// point, released on return). No lock, no run: a lock that cannot be taken — unsupported
	// platform or any error — refuses here, before the run directory is read or mutated.
	lock, err := plan.LockPlan(*runDir)
	if err != nil {
		return evalCommandError("prove-novel", fmt.Errorf("run directory lock: %w; refusing to run unlocked", err))
	}
	defer plan.UnlockPlan(lock)

	stored, err := eval.LoadRun(*runDir)
	if err != nil {
		return evalCommandError("prove-novel", err)
	}
	// I9 first: a corrupt or tampered event chain invalidates every later claim, so it is
	// refused before any state or evidence is read as trustworthy.
	for _, id := range sortedCaseIDs(stored.CaseRuns) {
		if err := stored.CaseRuns[id].Log.Verify(); err != nil {
			return evalCommandError("prove-novel", fmt.Errorf("case %s event chain: %w", id, err))
		}
	}
	// The proof claims an independent re-run of a valid, complete, budget-supported
	// execution — the same provenance gates confirm applies (spec 15).
	if stored.Record.State != eval.RunAdjudicating {
		return evalCommandError("prove-novel", fmt.Errorf("run is in state %q; a novel proof is recorded only while the run is %q", stored.Record.State, eval.RunAdjudicating))
	}
	if stored.Record.ManifestSHA256 == "" || stored.ManifestPath == "" {
		return evalCommandError("prove-novel", fmt.Errorf("run is not bound to a stored sealed manifest; prove-novel requires manifest-bound evidence"))
	}
	if stored.Record.AbortReason != "" {
		return evalCommandError("prove-novel", fmt.Errorf("run provenance does not prove a valid and complete execution: %s", stored.Record.AbortReason))
	}
	if len(stored.Record.Leaks) > 0 {
		leak := stored.Record.Leaks[0]
		return evalCommandError("prove-novel", fmt.Errorf("run records %d leak(s) (first: %s at %s:%d); leaked evidence cannot carry a proof", len(stored.Record.Leaks), leak.Kind, leak.Source, leak.Line))
	}
	manifest, err := eval.LoadManifest(stored.ManifestPath)
	if err != nil {
		return evalCommandError("prove-novel", err)
	}
	if manifest.ManifestSHA256 != stored.Record.ManifestSHA256 {
		return evalCommandError("prove-novel", fmt.Errorf("stored run manifest digest %s does not match the sealed manifest %s", stored.Record.ManifestSHA256, manifest.ManifestSHA256))
	}
	if mismatches := eval.VerifyCases(*benchDir, manifest); len(mismatches) > 0 {
		return evalCommandError("prove-novel", fmt.Errorf("benchmark cases do not match the sealed manifest: %s", strings.Join(mismatches, ", ")))
	}
	if manifest.Budgets.MaxRuntimePerCaseRunSeconds <= 0 {
		return evalCommandError("prove-novel", fmt.Errorf("manifest per-case runtime budget is missing; refusing an unbounded replay"))
	}
	timeout := time.Duration(manifest.Budgets.MaxRuntimePerCaseRunSeconds) * time.Second

	caseRun, ok := stored.CaseRuns[*caseID]
	if !ok {
		return evalCommandError("prove-novel", fmt.Errorf("unknown case %q", *caseID))
	}
	var finding eval.Finding
	found := false
	for _, candidate := range caseRun.Findings {
		if candidate.ID == *findingID {
			finding, found = candidate, true
			break
		}
	}
	if !found {
		return evalCommandError("prove-novel", fmt.Errorf("unknown finding %q in case %q", *findingID, *caseID))
	}
	// The target must be exactly one unproven NOVEL_CANDIDATE: any other state or an
	// already-effective proof means this invocation would record a duplicate or a
	// state-preserving event the model never sanctioned.
	if state := caseRun.FindingState(finding.ID); state != eval.FindingNovelCandidate {
		return evalCommandError("prove-novel", fmt.Errorf("finding %s is in state %s; prove-novel records a proof only for NOVEL_CANDIDATE", finding.ID, state))
	}
	if _, proved := caseRun.NovelProofs()[finding.ID]; proved {
		return evalCommandError("prove-novel", fmt.Errorf("finding %s already has an effective novel proof; prove-novel records exactly one", finding.ID))
	}
	key, err := bench.LoadKey(filepath.Join(*benchDir, "cases", *caseID))
	if err != nil {
		return evalCommandError("prove-novel", err)
	}
	if !novelRuntimeSupported(key.Suite) {
		return evalCommandError("prove-novel", fmt.Errorf("unsupported suite/runtime: only a node --test suite on unix can be replayed, got suite %q on %s", key.Suite, runtime.GOOS))
	}
	// The saved tests are the replay's input: their bytes must still match the digests the
	// run recorded, and there must be at least one of them to execute.
	sourceDir := filepath.Join(stored.SourceResultsDir, *caseID, fmt.Sprint(stored.K))
	result, err := readSourceResult(filepath.Join(sourceDir, "result.json"))
	if err != nil {
		return evalCommandError("prove-novel", err)
	}
	artifactDir := filepath.Join(sourceDir, "test-artifacts")
	saved, err := bench.VerifiedTestArtifacts(artifactDir, result.Catch, result.TestArtifacts)
	if err != nil {
		return evalCommandError("prove-novel", fmt.Errorf("saved tests: %w", err))
	}
	if len(saved) == 0 {
		return evalCommandError("prove-novel", fmt.Errorf("case %s records no saved test to replay", *caseID))
	}
	if control, err := os.Stat(*controlDir); err != nil || !control.IsDir() {
		return evalCommandError("prove-novel", fmt.Errorf("control directory %q does not exist", *controlDir))
	}

	// The dry run prints the full plan the preflight validated; only --execute turns it
	// into an actual replay, so without it nothing runs and nothing is written.
	fmt.Println("prove-novel: preflight passed; replay has not run")
	fmt.Printf("  case: %s\n  finding: %s\n  control: %s (%s)\n  saved tests: %s\n  timeout: %s\n",
		*caseID, *findingID, *controlName, *controlDir, strings.Join(saved, " "), timeout)
	if !*execute {
		fmt.Fprintln(os.Stderr, "bench eval prove-novel: pass --execute to run saved tests; no sandbox or secret isolation; tests execute repository code")
		return 2
	}

	replay, err := bench.ReplayNovelSavedTest(bench.NovelReplayInput{
		RuleVersion: bench.NovelCorrectnessRuleV1,
		CaseDir:     filepath.Join(*benchDir, "cases", *caseID),
		ArtifactDir: artifactDir,
		Artifacts:   result.TestArtifacts,
		Suite:       strings.TrimSpace(key.Suite) + " " + strings.Join(saved, " "),
		ControlName: *controlName,
		ControlDir:  *controlDir,
		Timeout:     timeout,
	})
	if err != nil {
		return evalCommandError("prove-novel", err)
	}
	// A contrast the observation does not support proves nothing: refuse with the observed
	// classifications before ProofFromReplay, so the caller sees what the replay saw.
	if !replay.SupportedReproduction || !replay.AdjudicationRequired {
		detail := fmt.Sprintf("subject=%s control=%s", replay.Subject.Classification, replay.Control.Classification)
		if replay.Subject.Reason != "" {
			detail += " subject reason: " + replay.Subject.Reason
		}
		if replay.Control.Reason != "" {
			detail += " control reason: " + replay.Control.Reason
		}
		return evalCommandError("prove-novel", fmt.Errorf("replay not conclusive: %s", detail))
	}
	ts := time.Now().UTC().Format(time.RFC3339Nano)
	facts := eval.NovelProofFacts{
		Domain: domainFact, Severity: severityFact,
		IssueType: *issueType, ExpectedBehavior: *expectedBehavior,
		FailureCondition: *failureCondition, Mechanism: *mechanism,
		// ProofKind/ProofRule stay blank: ProofFromReplay derives them from the observation.
		AdjudicatedBy: *by, AdjudicatedReason: *reason, AdjudicatedTS: ts,
	}
	proof, err := eval.ProofFromReplay(finding, facts, replay)
	if err != nil {
		return evalCommandError("prove-novel", err)
	}
	// One mutation point: the event is appended in memory and reaches disk only through the
	// single SaveRun below, so every refusal above this line left the run byte-identical.
	if err := caseRun.RecordNovelProof(proof, ts); err != nil {
		return evalCommandError("prove-novel", err)
	}
	stored.CaseRuns[*caseID] = caseRun
	if err := eval.SaveRun(*runDir, stored); err != nil {
		return evalCommandError("prove-novel", err)
	}
	fmt.Printf("recorded novel-proof/2 proof for %s/%s\n", *caseID, *findingID)
	return 0
}

// saneControlName mirrors eval's unexported control-name gate (validNovelControlName):
// one path element, no separators, no control characters. eval re-checks it when the
// observation is validated; checking here refuses a bad name before anything runs.
func saneControlName(name string) bool {
	return name != "" && name != "." && name != ".." && filepath.Base(name) == name &&
		!strings.ContainsAny(name, "/\\\x00") && !strings.ContainsFunc(name, unicode.IsControl)
}

// novelRuntimeSupported reports whether this host and suite can run a novel replay: the
// saved-test correctness rule v1 supports only node --test, and bench builds its
// process-group cleanup only for these unix GOOS (novel_proof_unix.go build tag). Go and
// other suites, and any other platform, are refused before the replay is planned.
func novelRuntimeSupported(suite string) bool {
	words, err := hookcmd.ShellWords(suite)
	if err != nil || len(words) < 2 || words[0] != "node" || words[1] != "--test" {
		return false
	}
	switch runtime.GOOS {
	case "aix", "android", "darwin", "dragonfly", "freebsd", "illumos", "ios", "linux", "netbsd", "openbsd", "solaris":
		return true
	}
	return false
}

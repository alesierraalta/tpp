package main

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"sort"
	"strings"
	"time"

	"github.com/alesierraalta/tpp/internal/bench"
	"github.com/alesierraalta/tpp/internal/eval"
)

func runBenchEval(args []string) int {
	if len(args) == 0 {
		fmt.Fprintln(os.Stderr, "usage: tpp bench eval <import|pending|adjudicate|confirm|reopen|close|compare|verify>")
		return 2
	}
	switch args[0] {
	case "import":
		return runBenchEvalImport(args[1:])
	case "pending":
		return runBenchEvalPending(args[1:])
	case "adjudicate":
		return runBenchEvalAdjudicate(args[1:])
	case "confirm":
		return runBenchEvalConfirm(args[1:])
	case "reopen":
		return runBenchEvalReopen(args[1:])
	case "close":
		return runBenchEvalClose(args[1:])
	case "compare":
		return runBenchEvalCompare(args[1:])
	case "verify":
		return runBenchEvalVerify(args[1:])
	default:
		fmt.Fprintf(os.Stderr, "bench eval: unknown subcommand %q\n", args[0])
		return 2
	}
}

func runBenchEvalImport(args []string) int {
	fs := evalFlagSet("bench eval import")
	results := fs.String("results", "", "bench results directory")
	manifestPath := fs.String("manifest", "", "sealed manifest file")
	policyPath := fs.String("policy", "", "sealed policy file")
	harness := fs.String("harness", "", "blind harness label")
	replicate := fs.Int("replicate", 0, "replicate number")
	out := fs.String("out", "", "evaluation directory")
	benchDir := fs.String("bench-dir", "bench", "benchmark directory")
	if fs.Parse(args) != nil {
		return 2
	}
	if fs.NArg() != 0 || *results == "" || *manifestPath == "" || *policyPath == "" || *harness == "" || *replicate < 1 || *out == "" {
		fmt.Fprintln(os.Stderr, "bench eval import: requires --results, --manifest, --policy, --harness, --replicate, and --out")
		return 2
	}
	manifest, err := eval.LoadManifest(*manifestPath)
	if err != nil {
		return evalCommandError("import", err)
	}
	policy, err := eval.LoadPolicy(*policyPath)
	if err != nil {
		return evalCommandError("import", err)
	}
	imported, err := eval.ImportRun(*results, *benchDir, manifest, policy, *harness, *replicate, time.Now().UTC().Format(time.RFC3339Nano))
	if err != nil {
		return evalCommandError("import", err)
	}
	imported.Run.ManifestPath = absolutePath(*manifestPath)
	imported.Run.PolicyPath = absolutePath(*policyPath)
	runDir := filepath.Join(*out, fmt.Sprintf("run-%d", *replicate))
	if err := eval.SaveRun(runDir, imported.Run); err != nil {
		return evalCommandError("import", err)
	}
	fmt.Printf("imported replicate %d as %s\n", *replicate, runDir)
	return 0
}

func runBenchEvalPending(args []string) int {
	fs := evalFlagSet("bench eval pending")
	runDir := fs.String("eval", "", "evaluation run directory")
	if fs.Parse(args) != nil {
		return 2
	}
	if fs.NArg() != 0 || *runDir == "" {
		fmt.Fprintln(os.Stderr, "bench eval pending: requires --eval")
		return 2
	}
	stored, err := eval.LoadRun(*runDir)
	if err != nil {
		return evalCommandError("pending", err)
	}
	fmt.Fprintln(os.Stdout, "case\tfinding\trow\tlocation\tlocated issues\ttext")
	for _, caseID := range sortedCaseIDs(stored.CaseRuns) {
		caseRun := stored.CaseRuns[caseID]
		for _, finding := range caseRun.Findings {
			if finding.Invalid || caseRun.FindingState(finding.ID) != eval.FindingPendingAdjudication {
				continue
			}
			var issues []string
			for issueID, facts := range finding.Computed {
				if facts.C2 == eval.FactTrue {
					issues = append(issues, issueID)
				}
			}
			sort.Strings(issues)
			fmt.Fprintf(os.Stdout, "%s\t%s\t%d\t%s\t%s\t%s\n", caseID, finding.ID, finding.Row, finding.Location, strings.Join(issues, ","), finding.Description)
		}
	}
	return 0
}

func runBenchEvalAdjudicate(args []string) int {
	fs := evalFlagSet("bench eval adjudicate")
	runDir := fs.String("eval", "", "evaluation run directory")
	caseID := fs.String("case", "", "case ID")
	findingID := fs.String("finding", "", "finding ID")
	issueID := fs.String("issue", "", "candidate issue ID")
	c1Text := fs.String("c1", "", "type fact: true, false, or unknown")
	c3Text := fs.String("c3", "", "mechanism fact: true, false, or unknown")
	c4Text := fs.String("c4-shows", "", "evidence shows failure fact: true, false, or unknown")
	equivalent := fs.String("equivalent-to", "", "equivalent finding ID")
	outcome := fs.String("outcome", "", "unmatched outcome")
	by := fs.String("by", "", "adjudicator")
	reason := fs.String("reason", "", "decision reason")
	if fs.Parse(args) != nil {
		return 2
	}
	if fs.NArg() != 0 || *runDir == "" || *caseID == "" || *findingID == "" || *by == "" || *reason == "" {
		fmt.Fprintln(os.Stderr, "bench eval adjudicate: requires --eval, --case, --finding, --by, and --reason")
		return 2
	}
	modes := 0
	for _, value := range []string{*issueID, *equivalent, *outcome} {
		if value != "" {
			modes++
		}
	}
	if modes != 1 {
		fmt.Fprintln(os.Stderr, "bench eval adjudicate: choose exactly one of --issue, --equivalent-to, or --outcome")
		return 2
	}
	decision := eval.Decision{FindingID: *findingID, By: *by, TS: time.Now().UTC().Format(time.RFC3339Nano), Reason: *reason}
	switch {
	case *issueID != "":
		if *c1Text == "" || *c3Text == "" || *c4Text == "" {
			fmt.Fprintln(os.Stderr, "bench eval adjudicate: --issue requires --c1, --c3, and --c4-shows")
			return 2
		}
		var err error
		decision.C1, err = parseEvalFact(*c1Text)
		if err != nil {
			return evalCommandError("adjudicate", err)
		}
		decision.C3, err = parseEvalFact(*c3Text)
		if err != nil {
			return evalCommandError("adjudicate", err)
		}
		decision.C4Shows, err = parseEvalFact(*c4Text)
		if err != nil {
			return evalCommandError("adjudicate", err)
		}
		decision.CandidateIssue = *issueID
	case *equivalent != "":
		decision.EquivalentTo = *equivalent
	case *outcome != "":
		decision.UnmatchedOutcome = eval.FindingState(*outcome)
	}
	stored, err := eval.LoadRun(*runDir)
	if err != nil {
		return evalCommandError("adjudicate", err)
	}
	caseRun, ok := stored.CaseRuns[*caseID]
	if !ok {
		return evalCommandError("adjudicate", fmt.Errorf("unknown case %q", *caseID))
	}
	if err := caseRun.Decide(decision); err != nil {
		return evalCommandError("adjudicate", err)
	}
	stored.CaseRuns[*caseID] = caseRun
	if err := eval.SaveRun(*runDir, stored); err != nil {
		return evalCommandError("adjudicate", err)
	}
	return 0
}

// pendingConfirmation is one Issue's replayed confirmation outcome, held in memory until every
// case has verified and replayed.
type pendingConfirmation struct {
	issueID        string
	outcome        string
	artifactDigest string
	attempts       int
}

// confirmationBinding is the canonical set of reproducible inputs a confirmation is recorded over:
// the source result that binds the snapshot paths and their hashes, the sealed manifest the run is
// bound to, and the manifest's digests for this case's key, fixture and fix trees. The recorded
// artifact digest is the sha256 of this struct's canonical JSON, so it changes whenever any of the
// inputs does.
type confirmationBinding struct {
	ResultSHA256      string `json:"result_sha256"`
	ManifestSHA256    string `json:"manifest_sha256"`
	KeySHA256         string `json:"key_sha256"`
	FixtureTreeSHA256 string `json:"fixture_tree_sha256"`
	FixTreeSHA256     string `json:"fix_tree_sha256"`
}

// runBenchEvalConfirm records the reproduction confirmation for every primary finding that still
// lacks one. It refuses unless the run is adjudicating and manifest-bound with its sealed manifest
// intact and leak-free; it verifies every saved snapshot against the source result's metadata and
// replays the local catch oracle from those bytes — never a model, never an agent — before any
// event is appended or any state is saved.
func runBenchEvalConfirm(args []string) int {
	fs := evalFlagSet("bench eval confirm")
	runDir := fs.String("eval", "", "evaluation run directory")
	benchDir := fs.String("bench-dir", "bench", "benchmark directory")
	if fs.Parse(args) != nil {
		return 2
	}
	if fs.NArg() != 0 || *runDir == "" {
		fmt.Fprintln(os.Stderr, "bench eval confirm: requires --eval")
		return 2
	}
	stored, err := eval.LoadRun(*runDir)
	if err != nil {
		return evalCommandError("confirm", err)
	}
	// Preflight the existing record before anything else: every case's event chain must verify and
	// every recorded confirmation must still bind to the current source, so a corrupt log or a moved
	// source fails here with the run byte-identical on disk — before any replay, append, or save.
	for _, caseID := range sortedCaseIDs(stored.CaseRuns) {
		caseRun := stored.CaseRuns[caseID]
		if err := caseRun.Log.Verify(); err != nil {
			return evalCommandError("confirm", fmt.Errorf("case %s event chain: %w", caseID, err))
		}
	}
	if err := validateConfirmationBindings(stored); err != nil {
		return evalCommandError("confirm", err)
	}
	// The order is import → adjudicate → confirm → close: only an adjudicating run may record a
	// confirmation, and only a manifest-bound one can honestly claim an independent re-run.
	if stored.Record.State != eval.RunAdjudicating {
		return evalCommandError("confirm", fmt.Errorf("run is in state %q; reproduction is confirmed only while the run is %q, before close", stored.Record.State, eval.RunAdjudicating))
	}
	if stored.Record.ManifestSHA256 == "" || stored.ManifestPath == "" {
		return evalCommandError("confirm", fmt.Errorf("run is not bound to a stored sealed manifest; a legacy unbound run cannot claim independently confirmed reproduction"))
	}
	if stored.Record.AbortReason != "" {
		return evalCommandError("confirm", fmt.Errorf("run provenance does not prove a valid and complete execution: %s", stored.Record.AbortReason))
	}
	if len(stored.Record.Leaks) > 0 {
		leak := stored.Record.Leaks[0]
		return evalCommandError("confirm", fmt.Errorf("run records %d leak(s) (first: %s at %s:%d); leaked evidence cannot be confirmed", len(stored.Record.Leaks), leak.Kind, leak.Source, leak.Line))
	}
	manifest, err := eval.LoadManifest(stored.ManifestPath)
	if err != nil {
		return evalCommandError("confirm", err)
	}
	if manifest.ManifestSHA256 != stored.Record.ManifestSHA256 {
		return evalCommandError("confirm", fmt.Errorf("stored run manifest digest %s does not match the sealed manifest %s", stored.Record.ManifestSHA256, manifest.ManifestSHA256))
	}
	if mismatches := eval.VerifyCases(*benchDir, manifest); len(mismatches) > 0 {
		return evalCommandError("confirm", fmt.Errorf("benchmark cases do not match the sealed manifest: %s", strings.Join(mismatches, ", ")))
	}
	if manifest.Budgets.MaxRuntimePerCaseRunSeconds <= 0 {
		return evalCommandError("confirm", fmt.Errorf("manifest per-case runtime budget is missing; refusing an unbounded replay"))
	}
	timeout := time.Duration(manifest.Budgets.MaxRuntimePerCaseRunSeconds) * time.Second

	// Preflight every case first: artifact verification and the local replay all happen in memory,
	// and nothing is appended or saved until every artifact verified and every replay returned a
	// decisive CatchResult.
	type plannedCase struct {
		caseID  string
		pending []pendingConfirmation
	}
	var plan []plannedCase
	for _, caseID := range sortedCaseIDs(stored.CaseRuns) {
		caseRun := stored.CaseRuns[caseID]
		missing, err := caseRun.MissingReproductionConfirmations()
		if err != nil {
			return evalCommandError("confirm", err)
		}
		if len(missing) == 0 {
			continue
		}
		pending, err := planCaseConfirmations(stored, manifest, *benchDir, caseID, missing, timeout)
		if err != nil {
			return evalCommandError("confirm", err)
		}
		plan = append(plan, plannedCase{caseID: caseID, pending: pending})
	}
	if len(plan) == 0 {
		fmt.Println("already confirmed: 0 updated")
		return 0
	}
	ts := time.Now().UTC().Format(time.RFC3339Nano)
	total := 0
	for _, item := range plan {
		caseRun := stored.CaseRuns[item.caseID]
		for _, confirmation := range item.pending {
			if err := caseRun.RecordReproductionConfirmation(confirmation.issueID, confirmation.outcome, confirmation.artifactDigest, confirmation.attempts, ts); err != nil {
				// The in-memory ledger is discarded with this error: events reach disk only after
				// every case recorded successfully.
				return evalCommandError("confirm", fmt.Errorf("case %s: %w", item.caseID, err))
			}
			total++
		}
		stored.CaseRuns[item.caseID] = caseRun
	}
	if err := eval.SaveRun(*runDir, stored); err != nil {
		return evalCommandError("confirm", err)
	}
	fmt.Printf("confirmed %d reproduction issue(s) in %d case(s)\n", total, len(plan))
	return 0
}

// planCaseConfirmations preflights one case: it loads the sealed KEY v2 and the source result,
// verifies every saved snapshot against the result's own metadata, checks each missing Issue is
// confirmable, and replays the local catch oracle from the saved bytes.
func planCaseConfirmations(stored eval.StoredRun, manifest eval.Manifest, benchDir, caseID string, missing []string, timeout time.Duration) ([]pendingConfirmation, error) {
	fail := func(err error) ([]pendingConfirmation, error) { return nil, fmt.Errorf("case %s: %w", caseID, err) }
	var manifestCase eval.ManifestCase
	for _, candidate := range manifest.Cases {
		if candidate.ID == caseID {
			manifestCase = candidate
			break
		}
	}
	if manifestCase.ID == "" {
		return fail(fmt.Errorf("case is not in the sealed manifest"))
	}
	caseDir := filepath.Join(benchDir, "cases", caseID)
	key, err := bench.LoadKey(caseDir)
	if err != nil {
		return fail(err)
	}
	sourceDir := filepath.Join(stored.SourceResultsDir, caseID, fmt.Sprint(stored.K))
	resultPath := filepath.Join(sourceDir, "result.json")
	result, err := readSourceResult(resultPath)
	if err != nil {
		return fail(err)
	}
	// The run's manifest binding is the provenance this confirmation stands on: with the sealed
	// manifest it binds the key and both trees, and the result binds every snapshot path and hash
	// that VerifiedTestArtifacts now checks against the files on disk.
	artifactDir := filepath.Join(sourceDir, "test-artifacts")
	if _, err := bench.VerifiedTestArtifacts(artifactDir, result.Catch, result.TestArtifacts); err != nil {
		return fail(err)
	}
	resultDigest, err := eval.Digest(resultPath)
	if err != nil {
		return fail(err)
	}
	artifactDigest, err := confirmationArtifactDigest(resultDigest, manifest, manifestCase)
	if err != nil {
		return fail(err)
	}
	for _, issueID := range missing {
		if err := requireCatchOracle(key, caseID, issueID); err != nil {
			return fail(err)
		}
	}
	// Preflight the fixed versions the replay needs before any command runs: the catch check is
	// decisive only on fix/all and, for a multi-defect case, on fix/keep-<ID> per Issue. A missing
	// variant means the Issue was never checked and must be refused, never recorded as
	// NOT_REPRODUCED from an absent outcome.
	if st, err := os.Stat(filepath.Join(caseDir, bench.FixDir, "all")); err != nil || !st.IsDir() {
		return fail(fmt.Errorf("no fix/all directory; the catch check cannot run"))
	}
	if len(key.Defects) > 1 {
		for _, issueID := range missing {
			if st, err := os.Stat(filepath.Join(caseDir, bench.FixDir, "keep-"+issueID)); err != nil || !st.IsDir() {
				return fail(fmt.Errorf("no fix/keep-%s directory; issue %s/%s was never checked and cannot be confirmed", issueID, caseID, issueID))
			}
		}
	}
	// The replay rebuilds a workspace from fixture/ plus exactly the saved test bytes — the key
	// never enters it, and every artifact path is confined to the artifact root — then runs the
	// ordinary fixed-version oracle against it. No model and no agent is invoked.
	replay, err := bench.DiscriminateSavedTests(caseDir, artifactDir, key, result.TestArtifacts, timeout)
	if err != nil {
		return fail(err)
	}
	if !replay.Checked {
		detail := strings.Join(replay.Notes, "; ")
		if detail == "" {
			detail = "the fixed version was not checked"
		}
		return fail(fmt.Errorf("the catch replay checked nothing (%s); incomplete reproduction evidence", detail))
	}
	pending := make([]pendingConfirmation, 0, len(missing))
	for _, issueID := range missing {
		// With saved tests, an absent Caught entry means the Issue never produced an outcome
		// (variant missing, oracle failed or never ran): that is missing evidence, not a negative
		// one, and it is refused above or here rather than downgrading the primary. With no saved
		// tests at all, nothing distinguished any Issue — NOT_REPRODUCED with attempts 0 is the
		// explicit answer that case records.
		if len(result.TestArtifacts) > 0 {
			if _, checked := replay.Caught[issueID]; !checked {
				return fail(fmt.Errorf("issue %s/%s has no checked outcome after the replay; refusing to record a guess", caseID, issueID))
			}
		}
		outcome := string(eval.NotReproduced)
		if replay.Caught[issueID] {
			outcome = string(eval.Reproduced)
		}
		pending = append(pending, pendingConfirmation{
			issueID: issueID, outcome: outcome, artifactDigest: artifactDigest,
			attempts: replay.Attempts[issueID],
		})
	}
	return pending, nil
}

// requireCatchOracle refuses any Issue whose reproduction needs more than the local replay: the
// `catch` oracle is confirmed from the saved agent test bytes alone, while a `command` oracle
// would have to execute the key's reproduction command in an environment no snapshot rebuilds —
// only `catch` may be confirmed here, and anything else fails closed.
func requireCatchOracle(key bench.Key, caseID, issueID string) error {
	for _, defect := range key.Defects {
		if defect.ID != issueID {
			continue
		}
		if defect.Reproduction == nil || !defect.Reproduction.Applies {
			return fmt.Errorf("issue %s/%s: the sealed key does not apply reproduction, so there is nothing to confirm", caseID, issueID)
		}
		if defect.Reproduction.Oracle != "catch" {
			return fmt.Errorf("issue %s/%s: reproduction oracle %q is not supported by confirm: only the %q oracle replays from saved test bytes", caseID, issueID, defect.Reproduction.Oracle, "catch")
		}
		return nil
	}
	return fmt.Errorf("issue %s/%s has no defect in the sealed key", caseID, issueID)
}

// confirmationArtifactDigest hashes the canonical confirmation binding into the sha256 digest the
// confirmation event records.
func confirmationArtifactDigest(resultDigest string, manifest eval.Manifest, manifestCase eval.ManifestCase) (string, error) {
	data, err := eval.CanonicalJSON(confirmationBinding{
		ResultSHA256: resultDigest, ManifestSHA256: manifest.ManifestSHA256, KeySHA256: manifestCase.KeySHA256,
		FixtureTreeSHA256: manifestCase.FixtureTreeSHA256, FixTreeSHA256: manifestCase.FixTreeSHA256,
	})
	if err != nil {
		return "", err
	}
	sum := sha256.Sum256(data)
	return "sha256:" + hex.EncodeToString(sum[:]), nil
}

// readSourceResult reads a source result.json; confirm and the close digest map parse the same
// file, so snapshots are always validated against the metadata the run recorded.
func readSourceResult(path string) (bench.Result, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return bench.Result{}, fmt.Errorf("read source result %s: %w", path, err)
	}
	var result bench.Result
	if err := json.Unmarshal(data, &result); err != nil {
		return bench.Result{}, fmt.Errorf("parse source result %s: %w", path, err)
	}
	return result, nil
}

// validateConfirmationBindings refuses a run whose recorded confirmations no longer bind to the
// current source inputs. Confirm records the sha256 over the source result in place when the
// confirmation was appended, together with the sealed manifest and the case's key/fixture/fix
// digests — one shared formula in confirmationArtifactDigest — and the confirmation stays bound
// to those inputs: editing the source afterwards must not silently rebind it. Cases without an
// effective confirmation are skipped, so a legacy run never loads a manifest here. The recorded
// digest is evidence of the original inputs, not a signature over them.
func validateConfirmationBindings(stored eval.StoredRun) error {
	type recordedConfirmation struct{ caseID, issueID, digest string }
	var recorded []recordedConfirmation
	for _, caseID := range sortedCaseIDs(stored.CaseRuns) {
		for issueID, digest := range eval.ConfirmationDigests(stored.CaseRuns[caseID]) {
			recorded = append(recorded, recordedConfirmation{caseID: caseID, issueID: issueID, digest: digest})
		}
	}
	if len(recorded) == 0 {
		return nil
	}
	if stored.ManifestPath == "" || stored.Record.ManifestSHA256 == "" {
		return fmt.Errorf("confirmations exist but the run is not bound to a stored sealed manifest")
	}
	manifest, err := eval.LoadManifest(stored.ManifestPath)
	if err != nil {
		return err
	}
	if manifest.ManifestSHA256 != stored.Record.ManifestSHA256 {
		return fmt.Errorf("confirmation binding: stored run manifest digest %s does not match the sealed manifest %s", stored.Record.ManifestSHA256, manifest.ManifestSHA256)
	}
	for _, entry := range recorded {
		var manifestCase eval.ManifestCase
		for _, candidate := range manifest.Cases {
			if candidate.ID == entry.caseID {
				manifestCase = candidate
				break
			}
		}
		if manifestCase.ID == "" {
			return fmt.Errorf("confirmation binding: case %s is not in the sealed manifest", entry.caseID)
		}
		resultPath := filepath.Join(stored.SourceResultsDir, entry.caseID, fmt.Sprint(stored.K), "result.json")
		resultDigest, err := eval.Digest(resultPath)
		if err != nil {
			return fmt.Errorf("case %s: recompute confirmation binding: %w", entry.caseID, err)
		}
		binding, err := confirmationArtifactDigest(resultDigest, manifest, manifestCase)
		if err != nil {
			return err
		}
		if entry.digest != binding {
			return fmt.Errorf("case %s issue %s: confirmation binding %s does not match the current source binding %s; the inputs changed after confirm", entry.caseID, entry.issueID, entry.digest, binding)
		}
	}
	return nil
}

func runBenchEvalReopen(args []string) int {
	fs := evalFlagSet("bench eval reopen")
	runDir := fs.String("eval", "", "evaluation run directory")
	caseID := fs.String("case", "", "case ID")
	findingID := fs.String("finding", "", "finding ID")
	by := fs.String("by", "", "adjudicator")
	reason := fs.String("reason", "", "reopen reason")
	if fs.Parse(args) != nil {
		return 2
	}
	if fs.NArg() != 0 || *runDir == "" || *caseID == "" || *findingID == "" || *by == "" || *reason == "" {
		fmt.Fprintln(os.Stderr, "bench eval reopen: requires --eval, --case, --finding, --by, and --reason")
		return 2
	}
	stored, err := eval.LoadRun(*runDir)
	if err != nil {
		return evalCommandError("reopen", err)
	}
	caseRun, ok := stored.CaseRuns[*caseID]
	if !ok {
		return evalCommandError("reopen", fmt.Errorf("unknown case %q", *caseID))
	}
	if err := caseRun.Reopen(*findingID, *reason, *by, time.Now().UTC().Format(time.RFC3339Nano)); err != nil {
		return evalCommandError("reopen", err)
	}
	stored.CaseRuns[*caseID] = caseRun
	stored.Record.State = eval.RunAdjudicating
	stored.Record.Data = eval.RunData{ID: fmt.Sprintf("run-%d", stored.K)}
	stored.Record.InvariantViolations = nil
	if err := eval.SaveRun(*runDir, stored); err != nil {
		return evalCommandError("reopen", err)
	}
	eventDigest, err := eval.Digest(filepath.Join(*runDir, *caseID, "events.jsonl"))
	if err != nil {
		return evalCommandError("reopen", err)
	}
	invalidated := invalidatedArtifact{Invalidated: true, FindingID: *findingID, By: *by, Reason: *reason, EventLogSHA256: eventDigest}
	for _, name := range []string{"states.json", "metrics.json"} {
		path := filepath.Join(*runDir, name)
		if _, err := os.Stat(path); err == nil {
			if err := writeEvalJSON(path, invalidated); err != nil {
				return evalCommandError("reopen", err)
			}
		} else if !os.IsNotExist(err) {
			return evalCommandError("reopen", err)
		}
	}
	return 0
}

func runBenchEvalClose(args []string) int {
	fs := evalFlagSet("bench eval close")
	runDir := fs.String("eval", "", "evaluation run directory")
	if fs.Parse(args) != nil {
		return 2
	}
	if fs.NArg() != 0 || *runDir == "" {
		fmt.Fprintln(os.Stderr, "bench eval close: requires --eval")
		return 2
	}
	stored, err := eval.LoadRun(*runDir)
	if err != nil {
		return evalCommandError("close", err)
	}
	if stored.Record.State != eval.RunAdjudicating {
		return evalCommandError("close", fmt.Errorf("run cannot close from state %q", stored.Record.State))
	}
	// A manifest-bound run may not close past an unconfirmed primary: the confirmation is the
	// reproducibility evidence the metrics derive from, so every case must carry it first. A legacy
	// unbound run keeps the old behavior — it never claimed a confirmation to require.
	if stored.Record.ManifestSHA256 != "" || stored.ManifestPath != "" {
		var missingCases []string
		for _, caseID := range sortedCaseIDs(stored.CaseRuns) {
			caseRun := stored.CaseRuns[caseID]
			missing, err := caseRun.MissingReproductionConfirmations()
			if err != nil {
				return evalCommandError("close", err)
			}
			if len(missing) > 0 {
				missingCases = append(missingCases, fmt.Sprintf("%s: %s", caseID, strings.Join(missing, ", ")))
			}
		}
		if len(missingCases) > 0 {
			fmt.Fprintf(os.Stderr, "bench eval close: reproduction confirmations missing; run `tpp bench eval confirm --eval %s` first: %s\n", *runDir, strings.Join(missingCases, "; "))
			return 1
		}
	}
	// The authoritative inputs are validated before anything mutates: a corrupted snapshot or
	// result, an unreadable manifest or policy, or a broken event chain must fail with the run still
	// ADJUDICATING on disk rather than completed without its states and metrics.
	if err := preflightClose(stored); err != nil {
		return evalCommandError("close", err)
	}
	violations := make([]string, 0)
	data := eval.RunData{ID: fmt.Sprintf("run-%d", stored.K), Cases: make([]eval.CaseResult, 0, len(stored.CaseRuns))}
	ts := time.Now().UTC().Format(time.RFC3339Nano)
	for _, caseID := range sortedCaseIDs(stored.CaseRuns) {
		caseRun := stored.CaseRuns[caseID]
		closeErr := caseRun.Close(ts)
		found := eval.CheckCaseRun(&caseRun)
		if closeErr != nil {
			violations = append(violations, fmt.Sprintf("%s: %v", caseID, closeErr))
		}
		for _, violation := range found {
			violations = append(violations, fmt.Sprintf("%s: %s: %s", caseID, violation.ID, violation.Detail))
		}
		if closeErr != nil || len(found) > 0 {
			continue
		}
		caseResult, err := eval.CaseResultFrom(&caseRun, stored.CaseControls[caseID], eval.ReproFromEvents(caseRun))
		if err != nil {
			violations = append(violations, fmt.Sprintf("%s: %v", caseID, err))
			continue
		}
		caseResult.Outcome = stored.CaseOutcomes[caseID]
		resources := stored.CaseResources[caseID]
		caseResult.CostUSD = resources.CostUSD
		caseResult.AgentSeconds = resources.AgentSeconds
		caseResult.Tokens = resources.Tokens
		data.Cases = append(data.Cases, caseResult)
		stored.CaseRuns[caseID] = caseRun
	}
	if len(violations) > 0 {
		stored.Record.State = eval.RunAdjudicating
		stored.Record.InvariantViolations = violations
		if err := eval.SaveRun(*runDir, stored); err != nil {
			return evalCommandError("close", err)
		}
		fmt.Fprintf(os.Stderr, "bench eval close: case-run invariants failed: %s\n", strings.Join(violations, "; "))
		return 1
	}
	stored.Record.State = eval.RunCompleted
	stored.Record.InvariantViolations = nil
	stored.Record.Data = data
	if err := eval.SaveRun(*runDir, stored); err != nil {
		return evalCommandError("close", err)
	}
	inputs, err := closeInputDigests(stored, *runDir)
	if err != nil {
		return evalCommandError("close", err)
	}
	states := statesArtifact{InputDigests: inputs, Cases: make(map[string]caseStates, len(stored.CaseRuns))}
	for _, caseID := range sortedCaseIDs(stored.CaseRuns) {
		caseRun := stored.CaseRuns[caseID]
		state := caseStates{Issues: make(map[string]eval.IssueState), Findings: make(map[string]eval.FindingState), Primary: make(map[string]string)}
		for _, issue := range caseRun.Issues {
			state.Issues[issue.ID] = caseRun.IssueState(issue.ID)
			state.Primary[issue.ID] = caseRun.Primary(issue.ID)
		}
		for _, finding := range caseRun.Findings {
			state.Findings[finding.ID] = caseRun.FindingState(finding.ID)
		}
		states.Cases[caseID] = state
	}
	if err := writeEvalJSON(filepath.Join(*runDir, "states.json"), states); err != nil {
		return evalCommandError("close", err)
	}
	metrics := metricsArtifact{InputDigests: cloneDigests(inputs), Metrics: eval.ComputeRun(data, stored.WeightedRecallW)}
	statesDigest, err := eval.Digest(filepath.Join(*runDir, "states.json"))
	if err != nil {
		return evalCommandError("close", err)
	}
	metrics.InputDigests[filepath.Join(*runDir, "states.json")] = statesDigest
	if err := writeEvalJSON(filepath.Join(*runDir, "metrics.json"), metrics); err != nil {
		return evalCommandError("close", err)
	}
	fmt.Printf("closed run %d (%s)\n", stored.K, stored.Record.State)
	return 0
}

type caseStates struct {
	Issues   map[string]eval.IssueState   `json:"issues"`
	Findings map[string]eval.FindingState `json:"findings"`
	Primary  map[string]string            `json:"primary"`
}

type statesArtifact struct {
	InputDigests map[string]string     `json:"input_digests"`
	Cases        map[string]caseStates `json:"cases"`
}

type metricsArtifact struct {
	InputDigests map[string]string `json:"input_digests"`
	Metrics      eval.RunMetrics   `json:"metrics"`
}

type invalidatedArtifact struct {
	Invalidated    bool   `json:"invalidated"`
	FindingID      string `json:"finding_id"`
	By             string `json:"by"`
	Reason         string `json:"reason"`
	EventLogSHA256 string `json:"event_log_sha256"`
}

type inputDigest struct {
	Path   string `json:"path"`
	SHA256 string `json:"sha256"`
}

type comparisonArtifact struct {
	eval.ComparisonDecision
	BaselineDir  string        `json:"baseline_dir"`
	CandidateDir string        `json:"candidate_dir"`
	ManifestPath string        `json:"manifest_path"`
	PolicyPath   string        `json:"policy_path"`
	Inputs       []inputDigest `json:"inputs"`
}

func runBenchEvalCompare(args []string) int {
	fs := evalFlagSet("bench eval compare")
	baselineDir := fs.String("baseline", "", "baseline evaluation directory")
	candidateDir := fs.String("candidate", "", "candidate evaluation directory")
	manifestPath := fs.String("manifest", "", "sealed manifest file")
	policyPath := fs.String("policy", "", "sealed policy file")
	outDir := fs.String("out", "", "comparison output directory")
	if fs.Parse(args) != nil {
		return 2
	}
	if fs.NArg() != 0 || *baselineDir == "" || *candidateDir == "" || *manifestPath == "" || *policyPath == "" || *outDir == "" {
		fmt.Fprintln(os.Stderr, "bench eval compare: requires --baseline, --candidate, --manifest, --policy, and --out")
		return 2
	}
	artifact, report, advice, err := buildComparison(*baselineDir, *candidateDir, *manifestPath, *policyPath)
	if err != nil {
		return evalCommandError("compare", err)
	}
	if err := os.MkdirAll(*outDir, 0o755); err != nil {
		return evalCommandError("compare", err)
	}
	if err := writeEvalJSON(filepath.Join(*outDir, "decision.json"), artifact); err != nil {
		return evalCommandError("compare", err)
	}
	if err := writeEvalAtomic(filepath.Join(*outDir, "report.md"), []byte(report)); err != nil {
		return evalCommandError("compare", err)
	}
	fmt.Print(report)
	fmt.Fprintf(os.Stdout, "ShouldStop: stop=%t — %s\n", advice.Stop, advice.Reason)
	return verdictExitCode(artifact.Verdict)
}

func runBenchEvalVerify(args []string) int {
	fs := evalFlagSet("bench eval verify")
	comparisonDir := fs.String("comparison", "", "comparison output directory")
	if fs.Parse(args) != nil {
		return 2
	}
	if fs.NArg() != 0 || *comparisonDir == "" {
		fmt.Fprintln(os.Stderr, "bench eval verify: requires --comparison")
		return 2
	}
	decisionPath := filepath.Join(*comparisonDir, "decision.json")
	data, err := os.ReadFile(decisionPath)
	if err != nil {
		return evalCommandError("verify", err)
	}
	var recorded comparisonArtifact
	if err := json.Unmarshal(data, &recorded); err != nil {
		fmt.Fprintf(os.Stderr, "bench eval verify: mismatch in decision.json: %v\n", err)
		return 1
	}
	for _, input := range recorded.Inputs {
		actual, err := eval.Digest(input.Path)
		if err != nil || actual != input.SHA256 {
			fmt.Fprintf(os.Stderr, "bench eval verify: input digest mismatch: %s\n", input.Path)
			return 1
		}
	}
	expected, report, _, err := buildComparison(recorded.BaselineDir, recorded.CandidateDir, recorded.ManifestPath, recorded.PolicyPath)
	if err != nil {
		return evalCommandError("verify", err)
	}
	expectedDecision, err := json.MarshalIndent(expected, "", "  ")
	if err != nil {
		return evalCommandError("verify", err)
	}
	expectedDecision = append(expectedDecision, '\n')
	if !bytes.Equal(data, expectedDecision) {
		fmt.Fprintln(os.Stderr, "bench eval verify: mismatch in decision.json")
		return 1
	}
	recordedReport, err := os.ReadFile(filepath.Join(*comparisonDir, "report.md"))
	if err != nil || !bytes.Equal(recordedReport, []byte(report)) {
		fmt.Fprintln(os.Stderr, "bench eval verify: mismatch in report.md")
		return 1
	}
	fmt.Fprintln(os.Stdout, "verified decision.json and report.md")
	return 0
}

func buildComparison(baselineDir, candidateDir, manifestPath, policyPath string) (comparisonArtifact, string, eval.StopAdvice, error) {
	manifestPath, policyPath = absolutePath(manifestPath), absolutePath(policyPath)
	manifest, err := eval.LoadManifest(manifestPath)
	if err != nil {
		return comparisonArtifact{}, "", eval.StopAdvice{}, err
	}
	policy, err := eval.LoadPolicy(policyPath)
	if err != nil {
		return comparisonArtifact{}, "", eval.StopAdvice{}, err
	}
	baseline, baselinePaths, err := loadEvalSide(baselineDir)
	if err != nil {
		return comparisonArtifact{}, "", eval.StopAdvice{}, err
	}
	candidate, candidatePaths, err := loadEvalSide(candidateDir)
	if err != nil {
		return comparisonArtifact{}, "", eval.StopAdvice{}, err
	}
	input := eval.DecisionInput{Baseline: baseline, Candidate: candidate, Manifest: manifest, Policy: policy}
	decision := eval.Decide(input)
	extra := comparisonReportExtra(input)
	report := eval.RenderReport(decision, baseline.Label, candidate.Label, manifest.Benchmark+" "+manifest.Version, extra)
	paths := append([]string{manifestPath, policyPath}, baselinePaths...)
	paths = append(paths, candidatePaths...)
	inputs, err := digestInputs(paths)
	if err != nil {
		return comparisonArtifact{}, "", eval.StopAdvice{}, err
	}
	artifact := comparisonArtifact{
		ComparisonDecision: decision,
		BaselineDir:        absolutePath(baselineDir), CandidateDir: absolutePath(candidateDir),
		ManifestPath: manifestPath, PolicyPath: policyPath, Inputs: inputs,
	}
	return artifact, report, eval.ShouldStop(input), nil
}

func loadEvalSide(evalDir string) (eval.Side, []string, error) {
	evalDir = absolutePath(evalDir)
	entries, err := os.ReadDir(evalDir)
	if err != nil {
		return eval.Side{}, nil, fmt.Errorf("read evaluation directory %s: %w", evalDir, err)
	}
	var runDirs []string
	for _, entry := range entries {
		if entry.IsDir() && strings.HasPrefix(entry.Name(), "run-") {
			runDirs = append(runDirs, filepath.Join(evalDir, entry.Name()))
		}
	}
	sort.Strings(runDirs)
	if len(runDirs) == 0 {
		return eval.Side{}, nil, fmt.Errorf("evaluation directory %s contains no run-* directories", evalDir)
	}
	side := eval.Side{}
	var inputPaths []string
	for _, runDir := range runDirs {
		stored, err := eval.LoadRun(runDir)
		if err != nil {
			return eval.Side{}, nil, err
		}
		record := stored.Record
		if record.State == eval.RunCompleted {
			manifest, err := eval.LoadManifest(stored.ManifestPath)
			if err != nil {
				return eval.Side{}, nil, fmt.Errorf("load recorded manifest for %s: %w", runDir, err)
			}
			data, err := eval.ReconstructCompletedRun(stored, manifest)
			if err != nil {
				return eval.Side{}, nil, fmt.Errorf("validate completed run %s: %w", runDir, err)
			}
			paths, err := validateCompletedArtifacts(stored, manifest, data, runDir)
			if err != nil {
				return eval.Side{}, nil, fmt.Errorf("validate completed run %s artifacts: %w", runDir, err)
			}
			// The digest artifacts validate first, then the binding: a completed run whose source
			// moved after its confirmations is refused even when states/metrics were regenerated to
			// match the moved inputs.
			if err := validateConfirmationBindings(stored); err != nil {
				return eval.Side{}, nil, fmt.Errorf("validate completed run %s confirmation: %w", runDir, err)
			}
			inputPaths = append(inputPaths, paths...)
		}
		if side.Label == "" {
			side.Label, side.HarnessID = stored.HarnessLabel, record.HarnessID
		}
		for _, caseID := range sortedCaseIDs(stored.CaseRuns) {
			caseRun := stored.CaseRuns[caseID]
			for _, violation := range eval.CheckCaseRun(&caseRun) {
				record.InvariantViolations = append(record.InvariantViolations, caseID+": "+violation.ID+": "+violation.Detail)
			}
			inputPaths = append(inputPaths, filepath.Join(runDir, caseID, "caserun.json"), filepath.Join(runDir, caseID, "events.jsonl"))
		}
		inputPaths = append(inputPaths, filepath.Join(runDir, "run.json"))
		side.Runs = append(side.Runs, record)
	}
	return side, inputPaths, nil
}

func validateCompletedArtifacts(stored eval.StoredRun, manifest eval.Manifest, data eval.RunData, runDir string) ([]string, error) {
	inputs, err := closeInputDigests(stored, runDir)
	if err != nil {
		return nil, err
	}

	states := statesArtifact{InputDigests: inputs, Cases: make(map[string]caseStates, len(stored.CaseRuns))}
	for _, caseID := range sortedCaseIDs(stored.CaseRuns) {
		caseRun := stored.CaseRuns[caseID]
		state := caseStates{Issues: make(map[string]eval.IssueState), Findings: make(map[string]eval.FindingState), Primary: make(map[string]string)}
		for _, issue := range caseRun.Issues {
			state.Issues[issue.ID] = caseRun.IssueState(issue.ID)
			state.Primary[issue.ID] = caseRun.Primary(issue.ID)
		}
		for _, finding := range caseRun.Findings {
			state.Findings[finding.ID] = caseRun.FindingState(finding.ID)
		}
		states.Cases[caseID] = state
	}
	statesPath := filepath.Join(runDir, "states.json")
	statesBytes, err := os.ReadFile(statesPath)
	if err != nil {
		return nil, fmt.Errorf("read states.json: %w", err)
	}
	var recordedStates statesArtifact
	if err := json.Unmarshal(statesBytes, &recordedStates); err != nil {
		return nil, fmt.Errorf("parse states.json: %w", err)
	}
	if !reflect.DeepEqual(recordedStates, states) {
		return nil, fmt.Errorf("states.json does not match case ledger derivation")
	}

	statesDigest, err := eval.Digest(statesPath)
	if err != nil {
		return nil, err
	}
	metricsInputs := cloneDigests(inputs)
	metricsInputs[absolutePath(statesPath)] = statesDigest
	expectedMetrics := metricsArtifact{InputDigests: metricsInputs, Metrics: eval.ComputeRun(data, manifest.MetricConfig.WeightedRecallW)}
	metricsPath := filepath.Join(runDir, "metrics.json")
	metricsBytes, err := os.ReadFile(metricsPath)
	if err != nil {
		return nil, fmt.Errorf("read metrics.json: %w", err)
	}
	var recordedMetrics metricsArtifact
	if err := json.Unmarshal(metricsBytes, &recordedMetrics); err != nil {
		return nil, fmt.Errorf("parse metrics.json: %w", err)
	}
	if !reflect.DeepEqual(recordedMetrics, expectedMetrics) {
		return nil, fmt.Errorf("metrics.json does not match recomputed metrics")
	}

	paths := make([]string, 0, len(inputs)+2)
	for path := range inputs {
		paths = append(paths, path)
	}
	return append(paths, statesPath, metricsPath), nil
}

func comparisonReportExtra(input eval.DecisionInput) eval.ReportExtra {
	extra := eval.ReportExtra{
		Policy: input.Policy.Name, BaselineHarnessID: input.Baseline.HarnessID,
		CandidateHarnessID: input.Candidate.HarnessID,
	}
	all := append(append([]eval.RunRecord(nil), input.Baseline.Runs...), input.Candidate.Runs...)
	if len(all) > 0 {
		extra.Model, extra.Runner = all[0].Model, all[0].Runner
	}
	extra.KnownIssuesMean, extra.CasesMean, extra.CleanControlsMean = runAverages(input.Baseline.Runs)
	extra.BaselineTPMean = meanRunTP(input.Baseline.Runs, input.Manifest.MetricConfig.WeightedRecallW)
	extra.CandidateTPMean = meanRunTP(input.Candidate.Runs, input.Manifest.MetricConfig.WeightedRecallW)
	return extra
}

func runAverages(runs []eval.RunRecord) (knownIssues, cases, cleanControls float64) {
	if len(runs) == 0 {
		return 0, 0, 0
	}
	var issues, caseCount, controls float64
	for _, run := range runs {
		caseCount += float64(len(run.Data.Cases))
		for _, result := range run.Data.Cases {
			issues += float64(len(result.Issues))
			if result.Control {
				controls++
			}
		}
	}
	count := float64(len(runs))
	return issues / count, caseCount / count, controls / count
}

func meanRunTP(runs []eval.RunRecord, weight float64) float64 {
	if len(runs) == 0 {
		return 0
	}
	var total float64
	for _, run := range runs {
		total += float64(eval.ComputeRun(run.Data, weight).TP)
	}
	return total / float64(len(runs))
}

func digestInputs(paths []string) ([]inputDigest, error) {
	unique := make(map[string]struct{}, len(paths))
	for _, path := range paths {
		if path != "" {
			unique[absolutePath(path)] = struct{}{}
		}
	}
	sorted := make([]string, 0, len(unique))
	for path := range unique {
		sorted = append(sorted, path)
	}
	sort.Strings(sorted)
	inputs := make([]inputDigest, 0, len(sorted))
	for _, path := range sorted {
		digest, err := eval.Digest(path)
		if err != nil {
			return nil, err
		}
		inputs = append(inputs, inputDigest{Path: path, SHA256: digest})
	}
	return inputs, nil
}

// closeSourceInputs collects the authoritative external inputs a close binds: each case's source
// result files with every saved snapshot verified against the result's own metadata, plus the
// recorded manifest and policy. The checks live here so close can preflight these inputs before it
// mutates anything and the digest map can reuse the same validation.
func closeSourceInputs(stored eval.StoredRun) ([]string, error) {
	var paths []string
	for _, caseID := range sortedCaseIDs(stored.CaseRuns) {
		sourceDir := filepath.Join(stored.SourceResultsDir, caseID, fmt.Sprint(stored.K))
		var sourceResult *bench.Result
		for _, name := range []string{"result.json", "test-plan.md", "agent.log"} {
			path := filepath.Join(sourceDir, name)
			if _, err := os.Stat(path); err == nil {
				paths = append(paths, path)
				if name == "result.json" {
					result, err := readSourceResult(path)
					if err != nil {
						return nil, fmt.Errorf("case %s: %w", caseID, err)
					}
					sourceResult = &result
				}
			} else if !os.IsNotExist(err) {
				return nil, err
			}
		}
		// Every saved test snapshot the result claims is accepted only after its path and bytes
		// verify against the result's own metadata: an unknown path, a missing file, or bytes that
		// changed fail closed. A legacy result persists no snapshot (null) and claims none; a
		// manifest run records its list even when empty.
		if sourceResult != nil && sourceResult.TestArtifacts != nil {
			artifactDir := filepath.Join(sourceDir, "test-artifacts")
			saved, err := bench.VerifiedTestArtifacts(artifactDir, sourceResult.Catch, sourceResult.TestArtifacts)
			if err != nil {
				return nil, fmt.Errorf("case %s: %w", caseID, err)
			}
			for _, rel := range saved {
				paths = append(paths, filepath.Join(artifactDir, filepath.FromSlash(rel)))
			}
		}
	}
	if stored.ManifestPath != "" {
		paths = append(paths, stored.ManifestPath)
	}
	if stored.PolicyPath != "" {
		paths = append(paths, stored.PolicyPath)
	}
	return paths, nil
}

// preflightClose validates the authoritative inputs before any CaseRun.Close or completed-state
// write: every existing event chain must verify, and every external input the close will bind must
// parse, verify and read. A refusal leaves the disk run exactly as it was — ADJUDICATING, event
// logs byte-identical, no states or metrics artifact — so restoring the input makes the ordinary
// close succeed without hand-editing metadata. This is validation-first ordering within one
// invocation, not protection against a concurrent writer landing changes between this check and
// the writes that follow; a fault in the writes themselves after a clean preflight is the separate
// crash-consistency question.
func preflightClose(stored eval.StoredRun) error {
	external, err := closeSourceInputs(stored)
	if err != nil {
		return err
	}
	if _, err := digestInputs(external); err != nil {
		return err
	}
	for _, caseID := range sortedCaseIDs(stored.CaseRuns) {
		caseRun := stored.CaseRuns[caseID]
		if err := caseRun.Log.Verify(); err != nil {
			return fmt.Errorf("case %s event chain: %w", caseID, err)
		}
	}
	// Hash/schema first, then binding: a recorded confirmation must still bind to the current
	// source inputs, or the close would seal a claim its inputs no longer support.
	if err := validateConfirmationBindings(stored); err != nil {
		return err
	}
	return nil
}

func closeInputDigests(stored eval.StoredRun, runDir string) (map[string]string, error) {
	paths := []string{filepath.Join(runDir, "run.json")}
	for _, caseID := range sortedCaseIDs(stored.CaseRuns) {
		caseDir := filepath.Join(runDir, caseID)
		paths = append(paths, filepath.Join(caseDir, "caserun.json"), filepath.Join(caseDir, "events.jsonl"))
	}
	external, err := closeSourceInputs(stored)
	if err != nil {
		return nil, err
	}
	paths = append(paths, external...)
	inputs, err := digestInputs(paths)
	if err != nil {
		return nil, err
	}
	result := make(map[string]string, len(inputs))
	for _, input := range inputs {
		result[input.Path] = input.SHA256
	}
	return result, nil
}

func writeEvalJSON(path string, value any) error {
	data, err := json.MarshalIndent(value, "", "  ")
	if err != nil {
		return err
	}
	return writeEvalAtomic(path, append(data, '\n'))
}

func writeEvalAtomic(path string, data []byte) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	temp, err := os.CreateTemp(filepath.Dir(path), ".eval-*")
	if err != nil {
		return err
	}
	if _, err := temp.Write(data); err != nil {
		temp.Close()
		return err
	}
	if err := temp.Sync(); err != nil {
		temp.Close()
		return err
	}
	if err := temp.Close(); err != nil {
		return err
	}
	if err := os.Rename(temp.Name(), path); err != nil {
		return err
	}
	return nil
}

func cloneDigests(source map[string]string) map[string]string {
	result := make(map[string]string, len(source)+1)
	for path, digest := range source {
		result[path] = digest
	}
	return result
}

func sortedCaseIDs(cases map[string]eval.CaseRun) []string {
	ids := make([]string, 0, len(cases))
	for id := range cases {
		ids = append(ids, id)
	}
	sort.Strings(ids)
	return ids
}

func parseEvalFact(value string) (eval.Fact, error) {
	switch strings.ToLower(value) {
	case "true":
		return eval.FactTrue, nil
	case "false":
		return eval.FactFalse, nil
	case "unknown":
		return eval.FactUnknown, nil
	default:
		return eval.FactUnknown, fmt.Errorf("fact must be true, false, or unknown, got %q", value)
	}
}

func evalFlagSet(name string) *flag.FlagSet {
	fs := flag.NewFlagSet(name, flag.ContinueOnError)
	fs.SetOutput(os.Stderr)
	return fs
}

func evalCommandError(command string, err error) int {
	fmt.Fprintf(os.Stderr, "bench eval %s: %v\n", command, err)
	return 1
}

func absolutePath(path string) string {
	absolute, err := filepath.Abs(path)
	if err != nil {
		return filepath.Clean(path)
	}
	return absolute
}

func verdictExitCode(verdict string) int {
	switch verdict {
	case "PASS":
		return 0
	case "FAIL":
		return 1
	case "REVIEW":
		return 3
	default:
		return 4
	}
}

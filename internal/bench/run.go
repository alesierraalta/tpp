package bench

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/alesierraalta/tsp/internal/buildinfo"
)

// Prompt is the whole instruction the agent receives: the skill must infer everything else.
const Prompt = "haz el testing"

// agentPrompt is what a run is actually told. A case with no request gets the bare prompt the bench has
// always sent, byte for byte; a case with one names it and nothing more. Choosing the mode stays the
// skill's decision, because that is the thing a reading measures.
func agentPrompt(key Key) string {
	if request := key.RequestText(); request != "" {
		return Prompt + "\n\nTest request: " + request
	}
	return Prompt
}

// Runners the bench can spawn. An empty Runner is the default: claude, so a run recorded before
// the flag existed still names the runner it used.
const (
	RunnerClaude = "claude"
	RunnerPi     = "pi"
)

// KnownRunner reports whether name selects a runner the bench can spawn; empty means the default.
func KnownRunner(name string) bool {
	return name == "" || name == RunnerClaude || name == RunnerPi
}

// DefaultModel is the model a runner uses when the operator does not name one: the free Pi model the
// readings run on, and the cheapest Claude model for the last-resort alternative.
func DefaultModel(runner string) string {
	if runner == RunnerClaude {
		return "haiku"
	}
	return "opencode/muse-spark-1.3-contributor-free"
}

// runnerAgent is the constructor a runner name selects. A test that sets Options.Agent bypasses
// this entirely, so the bench suite never spawns either CLI.
func runnerAgent(runner string) Agent {
	if runner == RunnerPi {
		return piAgent
	}
	return claudeAgent
}

// Options configures a benchmark run.
type Options struct {
	CasesGlob    string
	Model        string
	Runner       string // which CLI spawns the agent: RunnerClaude (default) or RunnerPi
	Runs         int
	MaxTurns     int
	Timeout      time.Duration
	SuiteTimeout time.Duration
	ConfigDir    string  // agent config directory (Claude config dir, or Pi agent dir); empty inherits the operator's
	ConfigMode   string  // agent config mode: ConfigBench, ConfigInherited, or ConfigCustom
	BinDir       string  // put first on the agent's PATH, so `tpp plan init` is the build under test
	Workers      int     // cases run side by side; below 1 means one at a time
	MaxCostUSD   float64 // 0 means no ceiling
	Out          string
	BenchDir     string
	SkillFile    string // for the history's skill_version
	DryRun       bool
	Keep         bool
	Retries      int           // agent retries on infrastructure failures (exit status, error result)
	RetryDelay   time.Duration // pause before a retry, so a rate limit has time to lift
	Agent        Agent         // nil means the selected runner's CLI
	Log          io.Writer
}

// Agent runs the model once in a workspace; injected so tests never spawn claude. The case's key
// travels with the call by value, so one case's request can never reach another's run.
type Agent func(ctx context.Context, ws string, key Key, opts Options) (AgentResult, error)

// Aggregate is the whole run's outcome. Recall and RecallCaught use defect-runs as their unit:
// repeated runs contribute repeated denominator entries; the Unique* and RecallUnique fields are
// the distinct-defect view.
type Aggregate struct {
	TS             string   `json:"ts"`
	Out            string   `json:"out"`
	Model          string   `json:"model"`
	DryRun         bool     `json:"dry_run"`
	Cases          []Result `json:"cases"`
	Defects        int      `json:"defects"`
	Found          int      `json:"found"`
	Recall         float64  `json:"recall"`
	ClaimedPinned  int      `json:"claimed_pinned"` // defects whose finding names a pinning test
	Caught         int      `json:"caught"`         // defects distinguished by an agent test
	RecallCaught   float64  `json:"recall_caught"`  // caught over defects of valid runs
	FalsePositives int      `json:"false_positives"`
	CostUSD        float64  `json:"cost_usd"`
	Invalid        int      `json:"invalid"`
	Failed         int      `json:"failed"`  // agent did not run to completion; excluded from recall
	NoPlan         int      `json:"no_plan"` // valid runs whose selected plan file is absent; scored zero
	// LightActivated counts the valid runs whose own plan declares a validated scoped run: the reading's
	// answer to "did the mode run?", next to what it cost and what it caught.
	LightActivated int `json:"light_activated"`
	// MicroActivated counts the valid runs whose own plan is an activated micro plan, apart from
	// LightActivated, so a reading can say how often the micro path ran and what it cost.
	MicroActivated int    `json:"micro_activated"`
	CostCeilingHit bool   `json:"cost_ceiling_hit"`
	RescoredFrom   string `json:"rescored_from,omitempty"` // set when this aggregate re-reads another run with newer rules
	RunTS          string `json:"run_ts,omitempty"`        // rescore: when the run it re-reads happened
	SkillVersion   string `json:"skill_version,omitempty"` // rescore: the version that produced the run
	Corpus         string `json:"corpus,omitempty"`        // digest of the measurement this run made: cases, requests, runs
	// Runs is how many times each case ran: three runs triple the defect denominator, so a reader can
	// reconcile the counts without re-deriving them. A zero means the aggregate never recorded a run
	// count — it was written before the field existed, or read from a file without one — so no reading can
	// be compared on it. Rescore copies this field through rather than guessing a count.
	Runs                 int        `json:"runs"`
	UniqueDefects        int        `json:"unique_defects"`
	UniqueFound          int        `json:"unique_found"`
	UniqueConfirmed      int        `json:"unique_confirmed"`
	UniqueCaught         int        `json:"unique_caught"`
	DefectRuns           int        `json:"defect_runs"`
	Controls             int        `json:"controls"`
	Inconclusive         int        `json:"inconclusive"`
	UnstableCases        []string   `json:"unstable_cases,omitempty"`
	AdjudicatedTrue      int        `json:"adjudicated_true"`
	AdjudicatedFalse     int        `json:"adjudicated_false"`
	OutOfScope           int        `json:"out_of_scope"`
	PendingAdjudication  int        `json:"pending_adjudication"`
	Precision            *float64   `json:"precision"`
	AdjudicationComplete bool       `json:"adjudication_complete"`
	MetricsVersion       int        `json:"metrics_version"`
	RecallUnique         float64    `json:"recall_unique"`
	RecallUniqueCaught   float64    `json:"recall_unique_caught"`
	Provenance           Provenance `json:"provenance"`
}

// CorpusCase is one case of a corpus: its name, the bounded request it is asked for, and the ids of
// the defects planted in it.
type CorpusCase struct {
	Name    string
	Request string
	Defects []string
}

// CorpusDigest identifies the measurement a run makes: the cases it covers, the request each one is
// asked for, and how many times each runs. It is stable under case and defect-id reordering, and moves
// when any of those changes, so two runs whose digests differ did not measure the same ground. The
// parts are length-prefixed: joined with a bare separator, a name or an id carrying that separator
// would impersonate two parts, and two different corpora would hash to one value.
func CorpusDigest(cases []CorpusCase, runs int) string {
	lines := make([]string, 0, len(cases))
	for _, c := range cases {
		ids := append([]string(nil), c.Defects...)
		sort.Strings(ids)
		lines = append(lines, part(c.Name)+part(c.Request)+part(strings.Join(ids, ",")))
	}
	sort.Strings(lines)
	sum := sha256.Sum256([]byte(part(fmt.Sprint(runs)) + "\n" + strings.Join(lines, "\n")))
	return "sha256:" + hex.EncodeToString(sum[:])[:16]
}

// part encodes one part of the digest so that no two different parts can produce the same text: the
// length comes first, so a value containing the separator cannot stand in for two values.
func part(s string) string {
	return fmt.Sprint(len(s)) + ":" + s
}

// defectIDs lists a key's defect ids in key order; the digest sorts them.
func defectIDs(k Key) []string {
	ids := make([]string, 0, len(k.Defects))
	for _, d := range k.Defects {
		ids = append(ids, d.ID)
	}
	return ids
}

// ExitCostCeiling is returned when the run stopped early because the cost ceiling was reached.
const ExitCostCeiling = 2

// ExitArtifact is returned when the run finished but its record could not be written: the numbers exist and
// nothing persisted them, which is a failure a reader has to see rather than a run that looks recorded.
const ExitArtifact = 4

// ExitPartial is returned when some case failed or was invalid: the numbers are incomplete
// evidence and must not be read as a measurement of the whole corpus.
const ExitPartial = 3

// unit is one case at one run: the smallest thing the pool schedules.
type unit struct {
	caseDir string
	key     Key
	run     int
	invalid bool
	reason  string
}

// Run scaffolds, runs, and scores every case; it returns the aggregate and a process exit code.
//
// Each case and each run of it is one unit. The pool runs them side by side and the tally stays in unit order, so
// the report does not depend on which unit happened to finish first.
func Run(opts Options) (Aggregate, int) {
	opts = normalizeOptions(opts)
	agg := Aggregate{TS: time.Now().UTC().Format(time.RFC3339), Out: opts.Out, Model: opts.Model, DryRun: opts.DryRun, Runs: opts.Runs}
	caseDirs, found := resolveCaseDirs(opts)
	if !found {
		return agg, 1
	}
	warnProvisionalScorer(opts)
	if !prepareOutput(opts) {
		return agg, 1
	}
	units := buildUnits(caseDirs, opts.Runs)
	results, skipped := runUnits(units, opts)
	agg, corpus, code := tallyUnits(agg, units, results, skipped, opts)
	agg, code = finalizeRun(agg, corpus, code, opts)
	return writeArtifacts(agg, code, caseDirs, opts)
}

// normalizeOptions fills the defaults a run cannot start without: somewhere to log, at least one run of each case,
// and the agent its runner implies.
func normalizeOptions(opts Options) Options {
	if opts.Log == nil {
		opts.Log = io.Discard
	}
	if opts.Runs <= 0 {
		opts.Runs = 1
	}
	if opts.Agent == nil {
		opts.Agent = runnerAgent(opts.Runner)
	}
	return opts
}

// resolveCaseDirs turns the case globs into directories. A pattern list that matches nothing is not an empty run:
// it is said out loud, and the run stops before it writes anything.
func resolveCaseDirs(opts Options) ([]string, bool) {
	var patterns []string
	for _, g := range strings.Split(opts.CasesGlob, ",") {
		patterns = append(patterns, resolveCasesGlob(opts.BenchDir, strings.TrimSpace(g)))
	}
	caseDirs, err := listCases(strings.Join(patterns, ","))
	if err != nil || len(caseDirs) == 0 {
		fmt.Fprintf(opts.Log, "no cases match %q\n", opts.CasesGlob)
		return nil, false
	}
	return caseDirs, true
}

// warnProvisionalScorer says so when the scorer's numbers are not final: a reader comparing runs across versions
// needs to know which of them were scored by a provision.
func warnProvisionalScorer(opts Options) {
	if rev := buildinfo.Revision(); ScorerIsProvisional(rev) {
		fmt.Fprintln(opts.Log, ProvisionalScorerWarning(rev))
	}
}

// prepareOutput is where the run's numbers will be written. A directory it cannot create stops the run before any
// case is scaffolded, so nothing is spent on a run that could not be recorded.
func prepareOutput(opts Options) bool {
	if err := os.MkdirAll(opts.Out, 0o755); err != nil {
		fmt.Fprintln(opts.Log, "out:", err)
		return false
	}
	return true
}

// buildUnits expands the cases into one unit per case and run. A case whose key will not load is one invalid unit
// carrying the reason, so the run reports it rather than dropping it silently.
func buildUnits(caseDirs []string, runs int) []unit {
	var units []unit
	for _, caseDir := range caseDirs {
		key, err := LoadKey(caseDir)
		if err != nil {
			units = append(units, unit{caseDir: caseDir, invalid: true, reason: err.Error()})
			continue
		}
		for run := 1; run <= runs; run++ {
			units = append(units, unit{caseDir: caseDir, key: key, run: run})
		}
	}
	return units
}

// runUnits schedules every unit and returns the results in unit order, plus which units the cost ceiling kept from
// starting.
//
// The ceiling is checked before a unit starts, so it stops launching work once it has been crossed while the units
// already in flight finish: a run can land just past it. A unit that never started leaves no hole, it is simply not
// part of the run.
//
// The log is progress: a unit says what it found the moment it finishes, in completion order. The pass after this
// one is the opposite and stays in unit order, so the report cannot depend on who was first. The mutex keeps two
// workers from interleaving halves of a line.
func runUnits(units []unit, opts Options) ([]Result, []bool) {
	var spentMu sync.Mutex
	spent := 0.0
	underCeiling := func() bool {
		if opts.MaxCostUSD <= 0 {
			return true
		}
		spentMu.Lock()
		defer spentMu.Unlock()
		return spent < opts.MaxCostUSD
	}
	skipped := make([]bool, len(units))
	var logMu sync.Mutex
	logf := func(format string, args ...any) {
		logMu.Lock()
		defer logMu.Unlock()
		fmt.Fprintf(opts.Log, format, args...)
	}
	results := schedule(units, opts.Workers, func(i int, u unit) Result {
		name := filepath.Base(u.caseDir)
		if u.invalid {
			logf("[%s] skipped: %v\n", name, u.reason)
			return Result{Case: name, Invalid: true, InvalidReason: u.reason}
		}
		if !underCeiling() {
			skipped[i] = true
			return Result{}
		}
		res := runOnce(u.caseDir, u.key, u.run, opts)
		logf("[%s #%d] reported %d/%d pinned %d caught %d/%d fp %d cost $%.3f turns %d%s%s\n",
			name, u.run, res.Found, res.Total, res.ClaimedPinned, res.Caught, res.Total, res.FalsePositives, res.CostUSD, res.Turns, invalidTag(res), noPlanTag(res))
		spentMu.Lock()
		spent += res.CostUSD
		spentMu.Unlock()
		return res
	})
	return results, skipped
}

// tallyUnits folds the results into the aggregate and returns the corpus the run covered and the exit code the
// ceiling earned. A case whose run later fails or is invalid is still part of the corpus.
func tallyUnits(agg Aggregate, units []unit, results []Result, skipped []bool, opts Options) (Aggregate, []CorpusCase, int) {
	code := 0
	var corpus []CorpusCase
	inCorpus := map[string]bool{}
	for i, u := range units {
		if skipped[i] {
			continue
		}
		name := filepath.Base(u.caseDir)
		if !inCorpus[name] {
			inCorpus[name] = true
			corpus = append(corpus, CorpusCase{Name: name, Request: u.key.RequestText(), Defects: defectIDs(u.key)})
		}
		foldResult(&agg, u, results[i])
		if opts.MaxCostUSD > 0 && agg.CostUSD >= opts.MaxCostUSD {
			agg.CostCeilingHit = true
			code = ExitCostCeiling
		}
	}
	return agg, corpus, code
}

// foldResult adds one result to the aggregate: an invalid or failed case is counted as one, and a valid one
// contributes what it found.
func foldResult(agg *Aggregate, u unit, res Result) {
	agg.Cases = append(agg.Cases, res)
	agg.CostUSD += res.CostUSD
	switch {
	case u.invalid, res.Invalid:
		agg.Invalid++
	case res.Failed:
		agg.Failed++
	default:
		agg.Defects += res.Total
		agg.Found += res.Found
		agg.Caught += res.Caught
		agg.ClaimedPinned += res.ClaimedPinned
		agg.FalsePositives += res.FalsePositives
		if !res.PlanFound {
			agg.NoPlan++
		}
		if res.LightActivated {
			agg.LightActivated++
		}
		if res.MicroActivated {
			agg.MicroActivated++
		}
	}
}

// finalizeAggregate derives all count and ratio fields from the cases, so a run and a rescore cannot
// disagree about the measurement represented by the same case results.
func finalizeAggregate(agg *Aggregate) {
	counts := CountUnique(agg.Cases)
	agg.Defects, agg.Found, agg.Caught = 0, 0, 0
	agg.ClaimedPinned, agg.FalsePositives = 0, 0
	agg.Invalid, agg.Failed, agg.NoPlan, agg.LightActivated, agg.MicroActivated = 0, 0, 0, 0, 0
	agg.AdjudicatedTrue, agg.AdjudicatedFalse, agg.OutOfScope = 0, 0, 0
	agg.PendingAdjudication = 0
	complete := true
	for _, r := range agg.Cases {
		if r.Invalid {
			agg.Invalid++
			continue
		}
		if r.Failed {
			agg.Failed++
			continue
		}
		agg.AdjudicatedTrue += r.AdjudicatedTrue
		agg.AdjudicatedFalse += r.AdjudicatedFalse
		agg.OutOfScope += r.OutOfScope
		agg.PendingAdjudication += r.PendingAdjudication
		if !r.AdjudicationComplete {
			complete = false
		}
		agg.FalsePositives += r.FalsePositives
		if !r.PlanFound {
			agg.NoPlan++
		}
		if r.LightActivated {
			agg.LightActivated++
		}
		if r.MicroActivated {
			agg.MicroActivated++
		}
		if r.Control {
			continue
		}
		agg.Defects += r.Total
		agg.Found += r.Found
		agg.Caught += r.Caught
		agg.ClaimedPinned += r.ClaimedPinned
	}
	agg.UniqueDefects, agg.UniqueFound, agg.UniqueConfirmed, agg.UniqueCaught = counts.Defects, counts.Found, counts.Confirmed, counts.Caught
	agg.DefectRuns, agg.Controls, agg.Inconclusive = counts.DefectRuns, counts.Controls, counts.Inconclusive
	agg.UnstableCases = counts.Unstable
	agg.AdjudicationComplete = complete
	agg.MetricsVersion = MetricsVersion
	agg.Recall, agg.RecallCaught = 0, 0
	if agg.Defects > 0 {
		agg.Recall = float64(agg.Found) / float64(agg.Defects)
		agg.RecallCaught = float64(agg.Caught) / float64(agg.Defects)
	}
	agg.RecallUnique, agg.RecallUniqueCaught = 0, 0
	if counts.Defects > 0 {
		agg.RecallUnique = float64(counts.Found) / float64(counts.Defects)
		agg.RecallUniqueCaught = float64(counts.Caught) / float64(counts.Defects)
	}
	denominator := agg.AdjudicatedTrue + agg.AdjudicatedFalse
	agg.Precision = nil
	if denominator > 0 {
		precision := float64(agg.AdjudicatedTrue) / float64(denominator)
		agg.Precision = &precision
	}
}

// finalizeRun closes the numbers: the ceiling it hit, the recall it earned, the exit code a partial run deserves,
// and the digest of the corpus those numbers cover.
func finalizeRun(agg Aggregate, corpus []CorpusCase, code int, opts Options) (Aggregate, int) {
	if agg.CostCeilingHit {
		fmt.Fprintf(opts.Log, "cost ceiling $%.2f reached; stopping\n", opts.MaxCostUSD)
	}
	finalizeAggregate(&agg)
	if code == 0 && (agg.Failed > 0 || agg.Invalid > 0) {
		code = ExitPartial
		fmt.Fprintf(opts.Log, "partial: %d failed, %d invalid; recall covers valid cases only\n", agg.Failed, agg.Invalid)
	}
	agg.Corpus = CorpusDigest(corpus, opts.Runs)
	agg.Provenance = provenanceForRun(opts, agg.Corpus, len(corpus))
	return agg, code
}

// writeArtifacts writes the three records a run leaves — the aggregate, its summary and the history row — and
// returns the aggregate plus the resulting exit code. A record that could not be written is ExitArtifact with the
// numbers still returned: the run happened, and nothing persisted it. A successful write preserves runCode, which
// may already describe a partial run or a cost ceiling.
func writeArtifacts(agg Aggregate, runCode int, caseDirs []string, opts Options) (Aggregate, int) {
	if err := writeJSON(filepath.Join(opts.Out, "aggregate.json"), agg); err != nil {
		return unwritten(agg, opts, "aggregate.json", err)
	}
	if err := os.WriteFile(filepath.Join(opts.Out, "summary.md"), []byte(Summary(agg)), 0o644); err != nil {
		return unwritten(agg, opts, "summary.md", err)
	}
	if !opts.DryRun && opts.BenchDir != "" {
		if err := AppendHistory(opts.BenchDir, historyEntry(agg, caseDirs, opts)); err != nil {
			return unwritten(agg, opts, "the history row", err)
		}
	}
	return agg, runCode
}

// historyEntry is the row a run appends to the history: what it measured, on which corpus, by which skill version,
// so a later run can be read against it.
func historyEntry(agg Aggregate, caseDirs []string, opts Options) HistoryEntry {
	return HistoryEntry{
		TS: agg.TS, Out: opts.Out, Model: opts.Model, Cases: len(caseDirs), Defects: agg.Defects,
		Found: agg.Found, Recall: agg.Recall, Caught: agg.Caught, RecallCaught: agg.RecallCaught,
		FalsePositives: agg.FalsePositives, CostUSD: agg.CostUSD,
		Failed: agg.Failed, Invalid: agg.Invalid, NoPlan: agg.NoPlan, Kind: KindRun,
		SkillVersion: SkillVersion(opts.SkillFile), Corpus: agg.Corpus,
		LightActivated: agg.LightActivated, MicroActivated: agg.MicroActivated, Runs: opts.Runs,
		MetricsVersion: agg.MetricsVersion, UniqueDefects: agg.UniqueDefects, UniqueFound: agg.UniqueFound,
		UniqueConfirmed: agg.UniqueConfirmed, UniqueCaught: agg.UniqueCaught, DefectRuns: agg.DefectRuns,
		Controls: agg.Controls, Precision: agg.Precision, PendingAdjudication: agg.PendingAdjudication,
		OutOfScope: agg.OutOfScope, Inconclusive: agg.Inconclusive, UnstableCases: agg.UnstableCases,
		AgentConfig: agg.Provenance.AgentConfig, Environment: agg.Provenance.Environment,
	}
}

// unwritten reports a record the run could not persist: the numbers are returned so a caller can still read
// them, and the code says the run did not finish as a recorded one.
func unwritten(agg Aggregate, opts Options, artifact string, err error) (Aggregate, int) {
	// The partial state belongs to the numbers that were not recorded, so it travels in the same line: an
	// artifact failure is not a reason to lose the fact that some cases failed on their own.
	partial := ""
	if agg.Failed > 0 || agg.Invalid > 0 {
		partial = fmt.Sprintf(" (the run had %d failed and %d invalid cases)", agg.Failed, agg.Invalid)
	}
	fmt.Fprintf(opts.Log, "%s could not be written: %v; the run's numbers are not recorded%s\n", artifact, err, partial)
	return agg, ExitArtifact
}

func runOnce(caseDir string, key Key, run int, opts Options) Result {
	name := filepath.Base(caseDir)
	ws := filepath.Join(opts.Out, name, fmt.Sprint(run), "ws")
	res := Result{Case: name, Run: run, Total: len(key.Defects), Workspace: ws}
	suite, err := Scaffold(caseDir, ws, key, opts.SuiteTimeout)
	res.Suite = suite
	if err != nil {
		res.Invalid, res.InvalidReason = true, err.Error()
		return finish(res, opts, true)
	}
	if !suite.Green {
		res.Invalid, res.InvalidReason = true, fmt.Sprintf("fixture suite not green (exit %d)", suite.ExitCode)
		return finish(res, opts, true)
	}
	if opts.DryRun {
		res.Notes = append(res.Notes, "dry-run: agent not spawned")
		scored := ScoreWorkspace(ws, key)
		res = merge(res, scored)
		return finish(res, opts, false)
	}
	started := time.Now()
	var ar AgentResult
	for attempt := 0; ; attempt++ {
		ctx, cancel := context.WithTimeout(context.Background(), opts.Timeout)
		ar, err = opts.Agent(ctx, ws, key, opts)
		cancel()
		res.CostUSD += ar.CostUSD
		res.Turns += ar.Turns
		writeAgentLog(filepath.Dir(ws), attempt, ar, err)
		// A timeout is an infrastructure failure like any other: the agent hung or the provider stalled,
		// so the run has no verdict for the case at all. Retrying it is bounded by --retries, and losing
		// a case to a stall costs more than the second attempt.
		infra := err != nil || ar.IsError || ar.TimedOut
		if !infra || attempt >= opts.Retries {
			break
		}
		res.Notes = append(res.Notes, fmt.Sprintf("attempt %d failed, retrying after %s", attempt+1, opts.RetryDelay))
		time.Sleep(opts.RetryDelay)
	}
	res.Seconds = time.Since(started).Seconds()
	switch {
	case ar.TimedOut:
		res.Failed, res.FailReason = true, "agent timed out"
	case err != nil:
		res.Failed, res.FailReason = true, "agent error: "+err.Error()
	case ar.IsError:
		res.Failed, res.FailReason = true, "agent result is an error: "+tail(ar.ErrorText, 200)
	}
	if res.Failed {
		// An agent that never ran is not a detection result; scoring it would read as recall 0.
		res.Notes = append(res.Notes, res.FailReason)
		return finish(res, opts, true)
	}
	res = merge(res, ScoreWorkspace(ws, key))
	res.Catch = Discriminate(caseDir, ws, key, opts.SuiteTimeout)
	res.Caught = res.Catch.Count()
	// Misses of either measure keep their workspace so they can be classified.
	return finish(res, opts, res.Recall < 1 || (res.Catch.Checked && res.Caught < res.Total))
}

// writeAgentLog keeps the tail of each attempt's raw stream beside result.json.
func writeAgentLog(dir string, attempt int, ar AgentResult, err error) {
	var b strings.Builder
	fmt.Fprintf(&b, "attempt %d exit_code=%d is_error=%v timed_out=%v err=%v\n", attempt+1, ar.ExitCode, ar.IsError, ar.TimedOut, err)
	b.WriteString(ar.Raw)
	b.WriteString("\n")
	// A diagnostic log is not a record: a case whose log cannot be written is not a case that failed.
	f, e := os.OpenFile(filepath.Join(dir, "agent.log"), os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o644)
	if e == nil {
		_, _ = f.WriteString(b.String())
		_ = f.Close()
	}
}

// merge keeps the run's bookkeeping fields and takes the scorer's fields.
func merge(res, scored Result) Result {
	scored.Case, scored.Run, scored.Workspace = res.Case, res.Run, res.Workspace
	scored.CostUSD, scored.Turns, scored.Seconds = res.CostUSD, res.Turns, res.Seconds
	scored.Suite, scored.Invalid, scored.InvalidReason = res.Suite, res.Invalid, res.InvalidReason
	scored.Failed, scored.FailReason = res.Failed, res.FailReason
	scored.Notes = append(res.Notes, scored.Notes...)
	return scored
}

func finish(res Result, opts Options, keepWS bool) Result {
	dir := filepath.Dir(res.Workspace)
	// The plan is the run's deliverable: keep it beside result.json so a later scorer can re-read it. A copy
	// that fails is a note on the case, not a failure — the case was scored, what is lost is rescoring it
	// later — and the note is computed here, before the result is persisted, because one added afterwards is
	// a note nobody reads.
	// The copy keeps its name (test-plan.md) whatever the workspace declared, so rescore reads it
	// unchanged. The refusal note, if any, was already added by ScoreWorkspace before this, and a refused
	// path is not read at all: copying it would put the file the guard refused beside the result.
	path, resolution := resolvePlanPath(res.Workspace)
	if !resolution.refused {
		if data, err := os.ReadFile(filepath.Join(res.Workspace, path)); err == nil {
			if err := os.WriteFile(filepath.Join(dir, "test-plan.md"), data, 0o644); err != nil {
				res.Notes = append(res.Notes, "the plan could not be kept beside the result: "+err.Error())
			}
		}
	}
	if err := writeJSON(filepath.Join(dir, "result.json"), res); err != nil {
		// The case ran, and its record is its evidence: a case whose result cannot be written is reported as
		// failed rather than counted from a file that is not there. Marking it failed also keeps the
		// workspace, which is the only copy of what the case produced.
		res.Failed, res.FailReason = true, fmt.Sprintf("result.json could not be written: %v", err)
	}
	if !opts.Keep && !keepWS && !res.Invalid && !res.Failed {
		_ = os.RemoveAll(res.Workspace)
		res.Workspace = ""
	}
	return res
}

// A valid run that wrote no plan scores zero but is reported apart: the flow ran and never
// persisted its deliverable, which is a different failure from missing the defect.
func noPlanTag(r Result) string {
	if !r.Invalid && !r.Failed && !r.PlanFound {
		return " NO PLAN"
	}
	return ""
}

func invalidTag(r Result) string {
	if r.Invalid {
		return " INVALID: " + r.InvalidReason
	}
	if r.Failed {
		return " FAILED: " + r.FailReason
	}
	return ""
}

// A pattern without a path separator names cases under <benchDir>/cases, so `n0*` works from
// anywhere; a pattern with a separator is used as given.
func resolveCasesGlob(benchDir, glob string) string {
	if !strings.ContainsAny(glob, `/\`) {
		return filepath.Join(benchDir, "cases", glob)
	}
	return glob
}

func listCases(glob string) ([]string, error) {
	var matches []string
	for _, g := range strings.Split(glob, ",") {
		m, err := filepath.Glob(strings.TrimSpace(g))
		if err != nil {
			return nil, err
		}
		matches = append(matches, m...)
	}
	var dirs []string
	for _, m := range matches {
		if st, err := os.Stat(m); err == nil && st.IsDir() {
			if _, err := os.Stat(filepath.Join(m, KeyFile)); err == nil {
				dirs = append(dirs, m)
			}
		}
	}
	sort.Strings(dirs)
	return dirs, nil
}

func writeJSON(path string, v any) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	data, err := json.MarshalIndent(v, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(path, append(data, '\n'), 0o644)
}

// Summary renders the aggregate as the markdown table written to summary.md.
func Summary(agg Aggregate) string {
	var b strings.Builder
	fmt.Fprintf(&b, "# Bench %s\n\nModel: %s · case-runs: %d · %s", agg.TS, agg.Model, len(agg.Cases), aggregateTotals(agg))
	if agg.Corpus != "" {
		fmt.Fprintf(&b, " · corpus: %s", agg.Corpus)
	}
	if agg.DryRun {
		b.WriteString(" · dry-run")
	}
	if agg.CostCeilingHit {
		b.WriteString(" · cost ceiling hit")
	}
	b.WriteString("\n\n| case | reported | pinned | caught | light | micro | false positives | cost USD | turns | minutes | note | precision (pending) |\n|---|---|---|---|---|---|---|---|---|---|---|---|\n")
	for _, c := range agg.Cases {
		note := strings.TrimSpace(invalidTag(c) + noPlanTag(c))
		if len(c.Notes) > 0 {
			note = strings.TrimSpace(note + " " + strings.Join(c.Notes, "; "))
		}
		if c.Catch.Checked && len(c.Catch.Notes) > 0 {
			note = strings.TrimSpace(note + " " + strings.Join(c.Catch.Notes, "; "))
		}
		reported := fmt.Sprintf("%d/%d", c.Found, c.Total)
		pinned := fmt.Sprintf("%d/%d", c.ClaimedPinned, c.Total)
		caught := fmt.Sprintf("%d/%d", c.Caught, c.Total)
		precision := casePrecision(c)
		if c.Control {
			reported, pinned, caught = "clean", "clean", "clean"
		}
		fmt.Fprintf(&b, "| %s | %s | %s | %s | %s | %s | %d | %.3f | %d | %.1f | %s | %s |\n",
			c.Case, reported, pinned, caught, lightTag(c), microTag(c), c.FalsePositives, c.CostUSD, c.Turns, c.Seconds/60, note, precision)
	}
	return b.String()
}

func aggregateTotals(agg Aggregate) string {
	line := fmt.Sprintf("reported (defect-runs): %d/%d · reported (unique defects): %d/%d · claimed a pinning test: %d defect-runs · caught (defect-runs): %d/%d · caught (unique defects): %d/%d · precision: %s · out of scope: %d finding rows · inconclusive: %d runs · controls: %d runs · false positives: %d finding rows · failed: %d runs · invalid: %d runs · no plan: %d runs · light runs: %d · micro runs: %d · cost (USD): $%.3f",
		agg.Found, agg.Defects, agg.UniqueFound, agg.UniqueDefects, agg.ClaimedPinned, agg.Caught, agg.Defects, agg.UniqueCaught, agg.UniqueDefects,
		precisionSummary(agg.Precision, agg.AdjudicatedTrue, agg.AdjudicatedFalse, agg.PendingAdjudication), agg.OutOfScope, agg.Inconclusive, agg.Controls,
		agg.FalsePositives, agg.Failed, agg.Invalid, agg.NoPlan, agg.LightActivated, agg.MicroActivated, agg.CostUSD)
	if len(agg.UnstableCases) > 0 {
		line += " · unstable cases: " + strings.Join(agg.UnstableCases, ", ")
	}
	return line
}

func precisionSummary(precision *float64, adjudicatedTrue, adjudicatedFalse, pending int) string {
	return fmt.Sprintf("%s (%d adjudicated, %d pending)", precisionValue(precision), adjudicatedTrue+adjudicatedFalse, pending)
}

func precisionValue(precision *float64) string {
	if precision == nil {
		return "none"
	}
	return fmt.Sprintf("%.2f", *precision)
}

func casePrecision(r Result) string {
	if r.Control {
		return "-"
	}
	return fmt.Sprintf("%s (%d)", precisionValue(r.Precision), r.PendingAdjudication)
}

// lightTag is the per-case column: a reading has to show which runs were scoped, not only how many.
func lightTag(r Result) string {
	return yesNo(r.LightActivated)
}

// microTag is the per-case column for the micro path, read the same way as lightTag.
func microTag(r Result) string {
	return yesNo(r.MicroActivated)
}

func yesNo(b bool) string {
	if b {
		return "yes"
	}
	return "no"
}

// claudeAgent spawns the real CLI with the run's prompt and reads its stream-json.
func claudeAgent(ctx context.Context, ws string, key Key, opts Options) (AgentResult, error) {
	args := []string{"-p", agentPrompt(key), "--output-format", "stream-json", "--verbose",
		"--max-turns", fmt.Sprint(opts.MaxTurns), "--permission-mode", "bypassPermissions"}
	if opts.Model != "" {
		args = append(args, "--model", opts.Model)
	}
	cmd := exec.CommandContext(ctx, "claude", args...)
	cmd.Dir = ws
	cmd.Env = agentEnv(os.Environ(), opts.ConfigDir, opts.BinDir)
	cmd.Stdin = strings.NewReader("")
	var out, errb bytes.Buffer
	cmd.Stdout, cmd.Stderr = &out, &errb
	err := cmd.Run()
	ar := ParseStream(bytes.NewReader(out.Bytes()))
	ar.Raw = tail(out.String(), 64*1024) + "\n--- stderr ---\n" + tail(errb.String(), 4*1024)
	if errors.Is(ctx.Err(), context.DeadlineExceeded) {
		ar.TimedOut = true
	}
	if err != nil {
		var ee *exec.ExitError
		if errors.As(err, &ee) {
			ar.ExitCode = ee.ExitCode()
		}
		if !ar.TimedOut {
			return ar, fmt.Errorf("claude: %v: %s", err, tail(errb.String(), 500))
		}
	}
	return ar, nil
}

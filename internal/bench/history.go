package bench

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"time"

	"github.com/alesierraalta/tsp/internal/buildinfo"
)

// HistoryEntry is one benchmark run as remembered across skill versions.
type HistoryEntry struct {
	TS             string  `json:"ts"`
	Out            string  `json:"out"`
	Model          string  `json:"model"`
	Cases          int     `json:"cases"`
	Defects        int     `json:"defects"`
	Found          int     `json:"found"`
	Recall         float64 `json:"recall"`
	FalsePositives int     `json:"false_positives"`
	CostUSD        float64 `json:"cost_usd"`
	SkillVersion   string  `json:"skill_version"`
	Failed         int     `json:"failed"`
	Invalid        int     `json:"invalid"`
	NoPlan         int     `json:"no_plan"`
	Caught         int     `json:"caught"`
	RecallCaught   float64 `json:"recall_caught"`
	Kind           string  `json:"kind"`                 // KindRun or KindRescore
	RunTS          string  `json:"run_ts,omitempty"`     // rescore: when the run it re-reads happened
	SourceRun      string  `json:"source_run,omitempty"` // rescore: the results directory it re-read
	Scorer         string  `json:"scorer"`               // build that produced the numbers, so two readings of one run are ordered
	Corpus         string  `json:"corpus,omitempty"`     // digest of the measurement the run made: cases, requests, runs
	// LightActivated is how many of the run's cases declared a scoped run their own plan validates. It
	// is written even when it is zero: a row without the field was written before the column existed, so
	// it says nothing about the mode, while a zero says the mode was watched and did not run.
	LightActivated int `json:"light_activated"`
	// Runs is how many times each case ran. It is written so a reader can reconcile the defect
	// denominator without re-deriving it: three runs triple it, and a row that does not say so sits in
	// the history as though it were comparable to a one-run row. A zero means the row never recorded a run
	// count — it was written before the column existed — so no reading can be compared on it.
	Runs                int      `json:"runs"`
	MetricsVersion      int      `json:"metrics_version"`
	UniqueDefects       int      `json:"unique_defects"`
	UniqueFound         int      `json:"unique_found"`
	UniqueConfirmed     int      `json:"unique_confirmed"`
	UniqueCaught        int      `json:"unique_caught"`
	DefectRuns          int      `json:"defect_runs"`
	Controls            int      `json:"controls"`
	Precision           *float64 `json:"precision"`
	PendingAdjudication int      `json:"pending_adjudication"`
	OutOfScope          int      `json:"out_of_scope"`
	Inconclusive        int      `json:"inconclusive"`
	UnstableCases       []string `json:"unstable_cases,omitempty"`
	AgentConfig         string   `json:"agent_config"`
	Environment         string   `json:"environment"`
	// MicroActivated is how many of the run's cases wrote an activated micro plan. Like LightActivated it
	// is written even at zero: a row without the key predates the column and says nothing about Micro.
	MicroActivated int `json:"micro"`
}

// A row is either a run that spawned agents or a rescore that re-read one with newer rules.
const (
	KindRun     = "run"
	KindRescore = "rescore"
)

const historyHeader = "| ts | kind | out | model | cases | defects | reported | recall | caught | recall caught | false positives | failed | invalid | no plan | cost USD | skill version | scorer | corpus | light | runs | metrics version | unique defects | unique found | unique confirmed | unique caught | defect runs | controls | precision | pending | out of scope | inconclusive | unstable | agent config | environment | micro |\n|---|---|---|---|---|---|---|---|---|---|---|---|---|---|---|---|---|---|---|---|---|---|---|---|---|---|---|---|---|---|---|---|---|---|---|---|\n"

// reported and caught count different things, so neither bounds the other.
const historyIntro = "# Benchmark history\n\nOne row per `tpp bench run`; never rewritten. A `rescore` row re-reads an earlier\nrun with newer scoring rules: it spends nothing, so summing the cost column over rescore\nrows would count the same money twice. `reported and caught are independent`: reported\ncounts defects written in the plan, caught counts defects some test distinguishes, and\neither can exceed the other.\n\nEvery row names the `scorer` build that produced its numbers. When the scoring rules change,\na later rescore of one source run supersedes an earlier one, and the scorer column is what\ntells the two apart; rows are never rewritten.\n\nA row written before the adjudicated metrics carries no `metrics version` column and is read as version 1, which is why `bench compare` refuses to compare it with a version 2 row.\n\nA row written before the agent-config column carries no agent-config mode and cannot be compared with one that does.\n\n"

// AppendHistory adds one line to history.jsonl and one row to history.md under benchDir;
// both files are append-only and never rewritten.
func AppendHistory(benchDir string, e HistoryEntry) error {
	if err := os.MkdirAll(benchDir, 0o755); err != nil {
		return err
	}
	if e.Kind == "" {
		e.Kind = KindRun
	}
	if e.Scorer == "" {
		e.Scorer = buildinfo.Revision()
	}
	e.AgentConfig = configMode(e.AgentConfig)
	if e.Environment == "" {
		e.Environment = environment()
	}
	if e.Kind == KindRescore {
		e.CostUSD = 0 // the run it re-reads already carries that cost
	}
	line, err := json.Marshal(e)
	if err != nil {
		return err
	}
	if err := appendFile(filepath.Join(benchDir, "history.jsonl"), append(line, '\n')); err != nil {
		return err
	}
	md := filepath.Join(benchDir, "history.md")
	if err := ensureHistoryHeader(md, historyHeader); err != nil {
		return err
	}
	ts, kind := e.TS, e.Kind
	if e.Kind == KindRescore {
		if e.RunTS != "" {
			ts = e.RunTS // the row describes that run, not the moment it was re-read
		}
		kind = "rescore of " + e.SourceRun
	}
	return appendFile(md, []byte(historyRow(e, ts, kind)))
}

// ensureHistoryHeader writes the current header when the file is new or still carries an older one.
// Rows already written stay as they are under their own header; the note names which column a
// reader will find missing above it, so an absent provenance field is never read as a measured one.
func ensureHistoryHeader(md, header string) error {
	existing, err := os.ReadFile(md)
	if err != nil {
		if os.IsNotExist(err) {
			return appendFile(md, []byte(historyIntro+header))
		}
		return err
	}
	if strings.Contains(string(existing), header) {
		return nil
	}
	var notes []string
	if !strings.Contains(string(existing), "| light |") {
		notes = append(notes, "Rows above this header predate the activation column and record no scoped run either way: they are non-activation measurements, not zero-activation ones.")
	}
	if !strings.Contains(string(existing), "| agent config |") {
		notes = append(notes, "Rows above this header predate the agent-config column and carry no agent-config mode, so they cannot be compared with rows that do.")
	}
	if !strings.Contains(string(existing), "| micro |") {
		notes = append(notes, "Rows above this header predate the micro column and record no micro plan either way: they are non-activation measurements, not zero-activation ones.")
	}
	note := "\n"
	if len(notes) > 0 {
		note += strings.Join(notes, "\n") + "\n"
	}
	return appendFile(md, []byte(note+header))
}

func historyRow(e HistoryEntry, ts, kind string) string {
	unstable := strings.Join(e.UnstableCases, ", ")
	if unstable == "" {
		unstable = "-"
	}
	return fmt.Sprintf("| %s | %s | %s | %s | %d | %d | %d | %.2f | %d | %.2f | %d | %d | %d | %d | %.3f | %s | %s | %s | %d | %d | %d | %d | %d | %d | %d | %d | %d | %s | %d | %d | %d | %s | %s | %s | %d |\n",
		ts, kind, e.Out, e.Model, e.Cases, e.Defects, e.Found, e.Recall, e.Caught, e.RecallCaught, e.FalsePositives, e.Failed, e.Invalid, e.NoPlan, e.CostUSD, e.SkillVersion, e.Scorer, e.Corpus, e.LightActivated, e.Runs,
		e.MetricsVersion, e.UniqueDefects, e.UniqueFound, e.UniqueConfirmed, e.UniqueCaught, e.DefectRuns, e.Controls, historyPrecision(e.Precision), e.PendingAdjudication, e.OutOfScope, e.Inconclusive, unstable, e.AgentConfig, e.Environment, e.MicroActivated)
}

func historyPrecision(precision *float64) string {
	if precision == nil {
		return "-"
	}
	return fmt.Sprintf("%.2f", *precision)
}

func appendFile(path string, data []byte) error {
	f, err := os.OpenFile(path, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o644)
	if err != nil {
		return err
	}
	defer f.Close()
	_, err = f.Write(data)
	return err
}

var versionRe = regexp.MustCompile(`(?m)^\s*version:\s*"?([^"\n]+)"?\s*$`)

// SkillVersion reads the version from a skill's frontmatter, or "unknown".
func SkillVersion(skillFile string) string {
	raw, err := os.ReadFile(skillFile)
	if err != nil {
		return "unknown"
	}
	if m := versionRe.FindSubmatch(raw); m != nil {
		return string(m[1])
	}
	return "unknown"
}

// Stamp is the results directory name for a run started at t.
func Stamp(t time.Time) string {
	return t.UTC().Format("20060102-150405")
}

// ScorerIsProvisional reports whether a scorer revision fails to identify the code that produced
// the numbers: a build from an uncommitted tree, or one with no version control information.
func ScorerIsProvisional(rev string) bool {
	return rev == "" || rev == "unknown" || strings.HasSuffix(rev, "+dirty")
}

// ProvisionalScorerWarning is what a run says before spending anything.
func ProvisionalScorerWarning(rev string) string {
	return "scorer " + rev + ": this build is not reproducible from a commit, so the numbers it " +
		"records cannot be re-derived later. Commit before measuring anything you intend to cite."
}

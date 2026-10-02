package bench

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

// Comparison sets two runs of the same corpus side by side, per case and in total.
type Comparison struct {
	Before, After Aggregate
	Rows          []CompareRow
}

// CompareRow is one case in both runs.
type CompareRow struct {
	Case           string
	Before, After  Result
	OnlyAfterValid bool // before failed or was invalid, after is valid: not a like-for-like delta
}

// Compare reads aggregate.json from two result directories of the same corpus.
func Compare(beforeDir, afterDir string) (Comparison, error) {
	var cmp Comparison
	var err error
	if cmp.Before, err = readAggregate(beforeDir); err != nil {
		return cmp, err
	}
	if cmp.After, err = readAggregate(afterDir); err != nil {
		return cmp, err
	}
	if err := refuseProvenanceMismatch(cmp.Before, cmp.After); err != nil {
		return cmp, err
	}
	if err := refuseCorpusMismatch(cmp.Before, cmp.After); err != nil {
		return cmp, err
	}
	before, after := caseResults(cmp.Before), caseResults(cmp.After)
	if err := refuseCaseSetMismatch(before, after); err != nil {
		return cmp, err
	}
	names := make([]string, 0, len(before))
	for name := range before {
		names = append(names, name)
	}
	sort.Strings(names)
	for _, name := range names {
		if err := refuseDefectCountMismatch(name, before[name], after[name]); err != nil {
			return cmp, err
		}
		row := CompareRow{Case: name, Before: before[name], After: after[name]}
		row.OnlyAfterValid = (row.Before.Failed || row.Before.Invalid) && !(row.After.Failed || row.After.Invalid)
		cmp.Rows = append(cmp.Rows, row)
	}
	return cmp, nil
}

func refuseProvenanceMismatch(before, after Aggregate) error {
	beforeProvenance, afterProvenance := aggregateProvenance(before), aggregateProvenance(after)
	if err := refuseManifestBinding(beforeProvenance, afterProvenance); err != nil {
		return err
	}
	checks := []func(Provenance, Provenance) error{
		refuseMetricsVersion,
		refuseModel,
		refuseRunner,
		refuseSkillVersion,
		refuseRuns,
		refuseAgentConfig,
		refuseEnvironment,
	}
	for _, check := range checks {
		if err := check(beforeProvenance, afterProvenance); err != nil {
			return err
		}
	}
	return nil
}

func refuseManifestBinding(before, after Provenance) error {
	beforeBound, afterBound := before.ManifestSHA256 != "", after.ManifestSHA256 != ""
	if !beforeBound && !afterBound {
		return nil
	}
	if beforeBound != afterBound {
		return fmt.Errorf("manifest-bound and legacy unbound readings cannot be compared")
	}
	for _, side := range []struct {
		name string
		p    Provenance
	}{{"before", before}, {"after", after}} {
		if side.p.InstrumentValid == nil || !*side.p.InstrumentValid {
			return fmt.Errorf("%s manifest-bound instrument is invalid or lacks validity evidence", side.name)
		}
		if side.p.BudgetStatus != "supported" {
			return fmt.Errorf("%s manifest-bound budget support is not verified: %s", side.name, side.p.BudgetUnsupportedReason)
		}
		if side.p.ExecutionComplete == nil || !*side.p.ExecutionComplete {
			return fmt.Errorf("%s manifest-bound execution is incomplete", side.name)
		}
	}
	if before.ManifestSHA256 != after.ManifestSHA256 {
		return fmt.Errorf("different manifest digests: before %s, after %s", before.ManifestSHA256, after.ManifestSHA256)
	}
	if before.BudgetIdentity != after.BudgetIdentity {
		return fmt.Errorf("different resolved manifest budget identities: before %s, after %s", before.BudgetIdentity, after.BudgetIdentity)
	}
	return nil
}

func refuseMetricsVersion(before, after Provenance) error {
	if before.MetricsVersion == after.MetricsVersion {
		return nil
	}
	return fmt.Errorf("different metrics version: before %d, after %d; the older reading has to be rescored before it can be compared, so this pair is not a delta", before.MetricsVersion, after.MetricsVersion)
}

func refuseModel(before, after Provenance) error {
	return refuseProvenanceString("model", before.Model, after.Model)
}

func refuseRunner(before, after Provenance) error {
	return refuseProvenanceString("runner", before.Runner, after.Runner)
}

func refuseSkillVersion(before, after Provenance) error {
	return refuseProvenanceString("skill version", before.SkillVersion, after.SkillVersion)
}

func refuseRuns(before, after Provenance) error {
	if before.Runs == after.Runs {
		return nil
	}
	return fmt.Errorf("different runs per case: before %d, after %d", before.Runs, after.Runs)
}

func refuseAgentConfig(before, after Provenance) error {
	// Two unknown readings can still be shown side by side, but unknown is never silently treated as a real mode.
	if before.AgentConfig == after.AgentConfig {
		return nil
	}
	return fmt.Errorf("different agent-config mode: before %s, after %s; an unspecified mode is unknown, not equivalent to a real mode", before.AgentConfig, after.AgentConfig)
}

func refuseEnvironment(before, after Provenance) error {
	return refuseProvenanceString("environment", before.Environment, after.Environment)
}

func refuseProvenanceString(field, before, after string) error {
	if before == after {
		return nil
	}
	return fmt.Errorf("different %s: before %s, after %s", field, before, after)
}

func refuseCorpusMismatch(before, after Aggregate) error {
	beforeCorpus, afterCorpus := aggregateProvenance(before).Corpus, aggregateProvenance(after).Corpus
	if beforeCorpus == "" || afterCorpus == "" || beforeCorpus == afterCorpus {
		return nil
	}
	return fmt.Errorf("different corpora: before %s, after %s", beforeCorpus, afterCorpus)
}

func caseResults(agg Aggregate) map[string]Result {
	results := map[string]Result{}
	for _, result := range agg.Cases {
		results[result.Case] = result // the last run of a case stands for it
	}
	return results
}

func refuseCaseSetMismatch(before, after map[string]Result) error {
	if len(before) != len(after) {
		return fmt.Errorf("different case sets: %d before, %d after", len(before), len(after))
	}
	for name := range before {
		if _, ok := after[name]; !ok {
			return fmt.Errorf("case %s is only in the before run", name)
		}
	}
	return nil
}

func refuseDefectCountMismatch(name string, before, after Result) error {
	// A case's defect count belongs to the corpus, not to the score: a case that failed in
	// either run still carries the count read from its key, and a change there is a change of
	// ground, not a change of result.
	if before.Total == after.Total {
		return nil
	}
	return fmt.Errorf("case %s: %d defects before, %d after", name, before.Total, after.Total)
}

func readAggregate(dir string) (Aggregate, error) {
	var agg Aggregate
	data, err := os.ReadFile(filepath.Join(dir, "aggregate.json"))
	if err != nil {
		return agg, err
	}
	return agg, json.Unmarshal(data, &agg)
}

// Markdown renders the comparison as a table plus the totals line.
func (c Comparison) Markdown() string {
	var w strings.Builder
	beforeProvenance, afterProvenance := aggregateProvenance(c.Before), aggregateProvenance(c.After)
	fmt.Fprintf(&w, "# Compare %s → %s\n\n", c.Before.TS, c.After.TS)
	writeProvenanceHeader(&w, beforeProvenance, afterProvenance)
	if beforeProvenance.AgentConfig == unspecifiedConfig && afterProvenance.AgentConfig == unspecifiedConfig {
		w.WriteString("note: agent-config mode was not recorded in either reading; it is treated as unknown.\n")
	}
	if beforeProvenance.SuiteTools != afterProvenance.SuiteTools {
		fmt.Fprintf(&w, "note: suite runtimes differed (%s → %s); weigh this when interpreting the comparison.\n", beforeProvenance.SuiteTools, afterProvenance.SuiteTools)
	}
	fmt.Fprintf(&w, "reported (defect-runs): %d/%d → %d/%d · reported (unique defects): %d/%d → %d/%d · pinned (defect-runs): %d → %d · caught (defect-runs): %d/%d → %d/%d · caught (unique defects): %d/%d → %d/%d · precision: %s → %s · pending count: %d → %d · inconclusive: %d runs → %d runs · false positives: %d finding rows → %d finding rows · light runs: %d → %d · micro runs: %d → %d · turns: %d → %d · cost (USD): $%.3f → $%.3f\n\n",
		c.Before.Found, c.Before.Defects, c.After.Found, c.After.Defects,
		c.Before.UniqueFound, c.Before.UniqueDefects, c.After.UniqueFound, c.After.UniqueDefects,
		c.Before.ClaimedPinned, c.After.ClaimedPinned,
		c.Before.Caught, c.Before.Defects, c.After.Caught, c.After.Defects,
		c.Before.UniqueCaught, c.Before.UniqueDefects, c.After.UniqueCaught, c.After.UniqueDefects,
		precisionSummary(c.Before.Precision, c.Before.AdjudicatedTrue, c.Before.AdjudicatedFalse, c.Before.PendingAdjudication),
		precisionSummary(c.After.Precision, c.After.AdjudicatedTrue, c.After.AdjudicatedFalse, c.After.PendingAdjudication),
		c.Before.PendingAdjudication, c.After.PendingAdjudication,
		c.Before.Inconclusive, c.After.Inconclusive, c.Before.FalsePositives, c.After.FalsePositives,
		c.Before.LightActivated, c.After.LightActivated, c.Before.MicroActivated, c.After.MicroActivated,
		totalTurns(c.Before), totalTurns(c.After), c.Before.CostUSD, c.After.CostUSD)
	w.WriteString("| case | reported before | reported after | pinned before | pinned after | caught before | caught after | note |\n|---|---|---|---|---|---|---|---|\n")
	for _, r := range c.Rows {
		var notes []string
		if r.OnlyAfterValid {
			notes = append(notes, "before did not run to completion")
		}
		if !r.After.Failed && !r.After.Invalid && !r.After.PlanFound {
			notes = append(notes, "NO PLAN after")
		}
		notes = append(notes, r.After.Catch.Notes...)
		fmt.Fprintf(&w, "| %s | %s | %s | %s | %s | %s | %s | %s |\n", r.Case,
			cell(r.Before, r.Before.Found), cell(r.After, r.After.Found),
			cell(r.Before, r.Before.ClaimedPinned), cell(r.After, r.After.ClaimedPinned),
			cell(r.Before, r.Before.Caught), cell(r.After, r.After.Caught), strings.Join(notes, "; "))
	}
	return w.String()
}

// totalTurns sums the agent turns of every case-run, failed ones included: a failed run still spent its
// turns, and a mode's cost has to be read in turns as well as dollars.
func totalTurns(agg Aggregate) int {
	turns := 0
	for _, r := range agg.Cases {
		turns += r.Turns
	}
	return turns
}

func writeProvenanceHeader(w *strings.Builder, before, after Provenance) {
	fmt.Fprintf(w, "provenance (before → after): model=%s → %s · runner=%s → %s · skill version=%s → %s · scorer=%s → %s · corpus digest=%s → %s · runs=%d → %d · cases=%d → %d · agent-config mode=%s → %s · environment=%s → %s\n",
		before.Model, after.Model, before.Runner, after.Runner, before.SkillVersion, after.SkillVersion,
		before.Scorer, after.Scorer, before.Corpus, after.Corpus, before.Runs, after.Runs, before.Cases, after.Cases,
		before.AgentConfig, after.AgentConfig, before.Environment, after.Environment)
}

func cell(r Result, n int) string {
	if r.Failed {
		return "FAILED"
	}
	if r.Invalid {
		return "INVALID"
	}
	if r.Control {
		return "clean"
	}
	return fmt.Sprintf("%d/%d", n, r.Total)
}

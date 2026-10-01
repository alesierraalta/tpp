package eval

import (
	"fmt"
	"sort"
	"strings"
)

type RunRecord struct {
	Data                RunData
	State               RunState
	ManifestSHA256      string
	PolicySHA256        string
	HarnessID           string
	Model               string
	Runner              string
	Environment         string
	Leaks               []Leak
	AbortReason         string
	InvariantViolations []string
}

type Side struct {
	Label     string
	HarnessID string
	Runs      []RunRecord
}

type DecisionInput struct {
	Baseline  Side
	Candidate Side
	Manifest  Manifest
	Policy    Policy
}

type Blocker struct {
	ID       string `json:"id"`
	Category string `json:"category"`
	Detail   string `json:"detail"`
}

type Row struct {
	Name      string   `json:"name"`
	Kind      string   `json:"kind"`
	Baseline  *float64 `json:"baseline"`
	Candidate *float64 `json:"candidate"`
	Delta     *float64 `json:"delta"`
	CILow     *float64 `json:"ci_low"`
	CIHigh    *float64 `json:"ci_high"`
	Unit      string   `json:"unit"`
	Band      string   `json:"band"`
	Tags      []string `json:"tags"`
}

type DomainRow struct {
	Domain      string `json:"domain"`
	Critical    bool   `json:"critical"`
	BaselineTP  int    `json:"baseline_tp"`
	CandidateTP int    `json:"candidate_tp"`
	Issues      int    `json:"issues"`
	Lower       int    `json:"lower"`
	Arrow       string `json:"arrow"`
}

type ComparisonDecision struct {
	Verdict        string                            `json:"verdict"`
	Category       string                            `json:"category"`
	Provisional    bool                              `json:"provisional"`
	Reasons        []string                          `json:"reasons"`
	Blockers       []Blocker                         `json:"blockers"`
	Rows           []Row                             `json:"rows"`
	Domains        []DomainRow                       `json:"domains"`
	K              struct{ Baseline, Candidate int } `json:"k"`
	ManifestSHA256 string                            `json:"manifest_sha256"`
	PolicySHA256   string                            `json:"policy_sha256"`
}

func Decide(in DecisionInput) ComparisonDecision {
	decision := ComparisonDecision{
		Verdict: "NO_DECISION", Reasons: []string{}, Blockers: []Blocker{}, Rows: []Row{}, Domains: []DomainRow{},
		ManifestSHA256: in.Manifest.ManifestSHA256, PolicySHA256: in.Policy.PolicySHA256,
	}
	baseline := completedRecords(in.Baseline.Runs)
	candidate := completedRecords(in.Candidate.Runs)
	decision.K.Baseline, decision.K.Candidate = len(baseline), len(candidate)

	addBlocker := func(id, category, detail string) {
		decision.Blockers = append(decision.Blockers, Blocker{ID: id, Category: category, Detail: detail})
	}
	for _, run := range in.Candidate.Runs {
		for _, leak := range run.Leaks {
			addBlocker("H6", "LEAKAGE", fmt.Sprintf("ground-truth leak %s at %s:%d", leak.Kind, leak.Source, leak.Line))
		}
	}
	allRuns := append(append([]RunRecord(nil), in.Baseline.Runs...), in.Candidate.Runs...)
	for index, run := range allRuns {
		if run.ManifestSHA256 != in.Manifest.ManifestSHA256 {
			addBlocker("H5", "INSTRUMENT", fmt.Sprintf("run %d manifest digest %q does not match %q", index+1, run.ManifestSHA256, in.Manifest.ManifestSHA256))
		}
		violations := append([]string(nil), run.InvariantViolations...)
		sort.Strings(violations)
		for _, violation := range violations {
			addBlocker("H5", "INSTRUMENT", "invariant violation: "+violation)
		}
	}
	for index, run := range in.Candidate.Runs {
		if run.PolicySHA256 != in.Policy.PolicySHA256 {
			addBlocker("H5", "INSTRUMENT", fmt.Sprintf("candidate run %d policy digest %q does not match %q", index+1, run.PolicySHA256, in.Policy.PolicySHA256))
		}
	}
	if !comparableRuns(allRuns) {
		if hasBlockerID(decision.Blockers, "H6") || hasBlockerID(decision.Blockers, "H5") {
			finalizeDecision(&decision, in)
			return decision
		}
		decision.Reasons = append(decision.Reasons, "I12 precondition failed: model, runner, or environment differs across runs")
		return decision
	}
	if decision.K.Candidate < in.Manifest.Replicates.KMin && hasBudgetAbort(in.Candidate.Runs) {
		addBlocker("H7", "COMPLETENESS", fmt.Sprintf("candidate completed %d runs, below k_min %d, after a budget abort", decision.K.Candidate, in.Manifest.Replicates.KMin))
		finalizeDecision(&decision, in)
		return decision
	}
	if decision.K.Baseline < in.Manifest.Replicates.KMin || decision.K.Candidate < in.Manifest.Replicates.KMin {
		if hasBlockerID(decision.Blockers, "H6") || hasBlockerID(decision.Blockers, "H5") {
			finalizeDecision(&decision, in)
			return decision
		}
		decision.Reasons = append(decision.Reasons, fmt.Sprintf("insufficient completed runs: baseline %d, candidate %d; k_min is %d", decision.K.Baseline, decision.K.Candidate, in.Manifest.Replicates.KMin))
		return decision
	}

	baseData, candidateData := recordsData(baseline), recordsData(candidate)
	weight := in.Manifest.MetricConfig.WeightedRecallW
	baseAgg, candidateAgg := Aggregate(baseData, weight), Aggregate(candidateData, weight)
	statRows := []struct {
		name, metric string
		value        func(RunMetrics) *float64
		pass, review float64
		direction    string
	}{
		{"Strict Recall", MetricStrictRecall, func(m RunMetrics) *float64 { return m.StrictRecall }, in.Policy.Thresholds.RecallDropPassPP, in.Policy.Thresholds.RecallDropReviewPP, "drop"},
		{"Strict Precision", MetricStrictPrecision, func(m RunMetrics) *float64 { return m.StrictPrecision }, in.Policy.Thresholds.PrecisionDropPassPP, in.Policy.Thresholds.PrecisionDropReviewPP, "drop"},
		{"Strict F1", MetricStrictF1, func(m RunMetrics) *float64 { return m.StrictF1 }, in.Policy.Thresholds.F1DropPassPP, in.Policy.Thresholds.F1DropReviewPP, "drop"},
		{"Reproducibility", MetricReproducibility, func(m RunMetrics) *float64 { return m.Reproducibility }, in.Policy.Thresholds.ReproducibilityDropPassPP, in.Policy.Thresholds.ReproducibilityDropReviewPP, "drop"},
		{"False Discovery Rate", MetricFDR, func(m RunMetrics) *float64 { return m.FDR }, in.Policy.Thresholds.FDRRisePassPP, in.Policy.Thresholds.FDRRiseReviewPP, "rise"},
	}
	for _, metric := range statRows {
		baseMean, candidateMean := baseAgg.Metrics[metric.metric].Mean, candidateAgg.Metrics[metric.metric].Mean
		row := Row{Name: metric.name, Kind: "statistical", Baseline: scaledPointer(baseMean, 100), Candidate: scaledPointer(candidateMean, 100), Unit: "pp", Band: "N/A", Tags: []string{}}
		if baseMean != nil && candidateMean != nil {
			interval := PairedBootstrap(baseData, candidateData, metric.value, weight, in.Manifest.MetricConfig.BootstrapResamples, in.Manifest.MetricConfig.BootstrapSeed, in.Manifest.MetricConfig.CILevel)
			row.Delta, row.CILow, row.CIHigh = scaledPointer(interval.Delta, 100), scaledPointer(interval.Low, 100), scaledPointer(interval.High, 100)
			if row.Delta != nil {
				measure := *row.Delta
				if metric.direction == "drop" {
					measure = -measure
				}
				row.Band = thresholdBand(measure, metric.pass, metric.review)
				if row.Band == "FAIL" && in.Policy.SignificanceRule && intervalContainsZero(interval.Low, interval.High) {
					row.Band = "REVIEW"
					row.Tags = append(row.Tags, "not_significant")
				}
			}
		}
		decision.Rows = append(decision.Rows, row)
	}

	baseRuntime, candidateRuntime := meanRunValue(baseline, func(run RunData) float64 { return run.AgentSeconds() }), meanRunValue(candidate, func(run RunData) float64 { return run.AgentSeconds() })
	baseCost, candidateCost := meanRunValue(baseline, func(run RunData) float64 { return run.Cost() }), meanRunValue(candidate, func(run RunData) float64 { return run.Cost() })
	runtimeRow := ratioRow("Runtime", baseRuntime, candidateRuntime, in.Policy.Thresholds.RuntimeRisePassPct, in.Policy.Thresholds.RuntimeRiseReviewPct)
	costRow := ratioRow("Cost", baseCost, candidateCost, in.Policy.Thresholds.CostRisePassPct, in.Policy.Thresholds.CostRiseReviewPct)
	decision.Rows = append(decision.Rows, runtimeRow, costRow)

	newCritical, newHigh := 0, 0
	baseStates, candidateStates := baseAgg.Consolidated, candidateAgg.Consolidated
	keys := unionIssueKeys(baseAgg, candidateAgg)
	for _, key := range keys {
		metadata := issueMetadataFor(key, baseAgg, candidateAgg)
		before, after := baseStates[key], candidateStates[key]
		if after == IssueFN && before != IssueFN {
			if metadata.Severity == Critical {
				newCritical++
				addBlocker("H1", "REGRESSION", fmt.Sprintf("%s (critical) is a new consolidated FN", key))
				decision.Reasons = append(decision.Reasons, fmt.Sprintf("new critical FN: %s", key))
			}
			if metadata.Severity == High {
				newHigh++
			}
		}
		if metadata.Severity == Critical && before == IssueTP && after == IssueFN {
			addBlocker("H2", "REGRESSION", fmt.Sprintf("%s (critical) consolidated TP in baseline is FN in candidate", key))
		}
	}
	criticalBand := "PASS"
	if newCritical >= in.Policy.Thresholds.NewCriticalFNFailCount {
		criticalBand = "FAIL"
	}
	decision.Rows = append(decision.Rows, countRow("New Critical FN", 0, newCritical, "issues", criticalBand))
	highBand := "PASS"
	if in.Policy.Thresholds.HighFNFailCount > 0 && newHigh >= in.Policy.Thresholds.HighFNFailCount {
		highBand = "FAIL"
	} else if in.Policy.Thresholds.HighFNReviewCount > 0 && newHigh >= in.Policy.Thresholds.HighFNReviewCount {
		highBand = "REVIEW"
	}
	decision.Rows = append(decision.Rows, countRow("New High FN", 0, newHigh, "issues", highBand))

	decision.Domains = buildDomainRows(in.Manifest, baseAgg, candidateAgg)
	criticalDomains := domainSet(in.Manifest.CriticalDomains)
	for _, domain := range decision.Domains {
		if domain.Issues == 0 {
			continue
		}
		critical := criticalDomains[Domain(domain.Domain)]
		row := countRow("Domain: "+domain.Domain, domain.BaselineTP, domain.CandidateTP, "issues", "PASS")
		if critical {
			if domain.Lower >= in.Policy.Thresholds.CriticalDomainIssuesLowerFail && in.Policy.Thresholds.CriticalDomainIssuesLowerFail > 0 {
				row.Band = "FAIL"
				addBlocker("H3", "REGRESSION", fmt.Sprintf("%s domain has %d consolidated Issues lower (%d/%d TP to %d/%d)", domain.Domain, domain.Lower, domain.BaselineTP, domain.Issues, domain.CandidateTP, domain.Issues))
			} else if domain.Lower >= in.Policy.Thresholds.CriticalDomainIssuesLowerReview && in.Policy.Thresholds.CriticalDomainIssuesLowerReview > 0 {
				row.Band = "REVIEW"
			}
		} else if domain.Lower >= 2 {
			row.Band = "REVIEW"
			decision.Reasons = append(decision.Reasons, fmt.Sprintf("non-critical domain %s has %d Issues lower", domain.Domain, domain.Lower))
		}
		decision.Rows = append(decision.Rows, row)
	}

	baseControl, candidateControl := meanControlFPRRuns(baseline), meanControlFPRRuns(candidate)
	controlRow := Row{Name: "Control FPR", Kind: "count", Baseline: baseControl, Candidate: candidateControl, Unit: "case-runs", Band: "PASS", Tags: []string{}}
	if baseControl != nil && candidateControl != nil {
		rise := *candidateControl - *baseControl
		controlRow.Delta = floatPointer(rise)
		if in.Policy.Thresholds.ControlFPRRiseFail > 0 && rise >= float64(in.Policy.Thresholds.ControlFPRRiseFail) {
			controlRow.Band = "FAIL"
		} else if in.Policy.Thresholds.ControlFPRRiseReview > 0 && rise >= float64(in.Policy.Thresholds.ControlFPRRiseReview) {
			controlRow.Band = "REVIEW"
		}
	}
	decision.Rows = append(decision.Rows, controlRow)

	for _, issue := range failedReproductions(baseline, candidate) {
		addBlocker("H4", "EVIDENCE", fmt.Sprintf("%s candidate primary finding was not reproduced; baseline primary finding was reproduced", issue))
	}

	t1Benefit := t1Benefit(in, decision.Rows, keys, baseAgg, candidateAgg)
	t2Benefit := t2Benefit(in, decision.Rows)
	for i := range decision.Rows {
		row := &decision.Rows[i]
		if row.Band != "FAIL" {
			continue
		}
		if t1Benefit && (row.Name == "Runtime" || row.Name == "Cost") {
			row.Band = "REVIEW"
			row.Tags = append(row.Tags, "tradeoff:T1")
		}
		if t2Benefit && (row.Name == "Strict Precision" || row.Name == "False Discovery Rate") {
			row.Band = "REVIEW"
			row.Tags = append(row.Tags, "tradeoff:T2")
		}
	}
	if !t1Benefit && in.Policy.Thresholds.ExtremeEfficiencyRisePct > 0 {
		for _, row := range []Row{runtimeRow, costRow} {
			if row.Delta != nil && *row.Delta > in.Policy.Thresholds.ExtremeEfficiencyRisePct {
				addBlocker("H8", "EFFICIENCY", fmt.Sprintf("%s rose %.1f%% without the T1 benefit", strings.ToLower(row.Name), *row.Delta))
			}
		}
	}
	finalizeDecision(&decision, in)
	return decision
}

func completedRecords(runs []RunRecord) []RunRecord {
	completed := make([]RunRecord, 0, len(runs))
	for _, run := range runs {
		if run.State == RunCompleted {
			completed = append(completed, run)
		}
	}
	return completed
}

func recordsData(runs []RunRecord) []RunData {
	data := make([]RunData, 0, len(runs))
	for _, run := range runs {
		data = append(data, run.Data)
	}
	return data
}

func comparableRuns(runs []RunRecord) bool {
	if len(runs) < 2 {
		return true
	}
	model, runner, environment := runs[0].Model, runs[0].Runner, runs[0].Environment
	for _, run := range runs[1:] {
		if run.Model != model || run.Runner != runner || run.Environment != environment {
			return false
		}
	}
	return true
}

func hasBudgetAbort(runs []RunRecord) bool {
	for _, run := range runs {
		if run.State == RunAborted && run.AbortReason == "budget" {
			return true
		}
	}
	return false
}

func hasBlockerID(blockers []Blocker, id string) bool {
	for _, blocker := range blockers {
		if blocker.ID == id {
			return true
		}
	}
	return false
}

func finalizeDecision(decision *ComparisonDecision, in DecisionInput) {
	if len(decision.Blockers) > 0 {
		sort.SliceStable(decision.Blockers, func(i, j int) bool {
			a, b := blockerOrder(decision.Blockers[i]), blockerOrder(decision.Blockers[j])
			if a != b {
				return a < b
			}
			if decision.Blockers[i].ID != decision.Blockers[j].ID {
				return decision.Blockers[i].ID < decision.Blockers[j].ID
			}
			return decision.Blockers[i].Detail < decision.Blockers[j].Detail
		})
		decision.Verdict = "FAIL"
		decision.Category = decision.Blockers[0].Category
	} else {
		hasFail, hasReview, onlyEfficiencyFail := false, false, true
		for _, row := range decision.Rows {
			if row.Band == "FAIL" {
				hasFail = true
				if row.Name != "Runtime" && row.Name != "Cost" {
					onlyEfficiencyFail = false
				}
			}
			if row.Band == "REVIEW" {
				hasReview = true
			}
		}
		switch {
		case hasFail:
			decision.Verdict = "FAIL"
			if onlyEfficiencyFail {
				decision.Category = "EFFICIENCY"
			} else {
				decision.Category = "REGRESSION"
			}
		case hasReview:
			decision.Verdict = "REVIEW"
		default:
			decision.Verdict = "PASS"
		}
	}
	if decision.Verdict == "FAIL" && decision.Category == "REGRESSION" && decision.K.Candidate < in.Manifest.Replicates.KMax && (hasBlockerID(decision.Blockers, "H1") || hasBlockerID(decision.Blockers, "H2") || hasBlockerID(decision.Blockers, "H3")) {
		decision.Provisional = true
		decision.Reasons = append(decision.Reasons, "frequency-based blocker is final only at k_max")
	}
}

func blockerOrder(blocker Blocker) int {
	switch blocker.Category {
	case "LEAKAGE":
		return 0
	case "INSTRUMENT":
		return 1
	case "REGRESSION":
		return 2
	case "EVIDENCE":
		return 3
	case "COMPLETENESS":
		return 4
	case "EFFICIENCY":
		return 5
	default:
		return 6
	}
}

func thresholdBand(value, pass, review float64) string {
	if value <= pass {
		return "PASS"
	}
	if value <= review {
		return "REVIEW"
	}
	return "FAIL"
}

func intervalContainsZero(low, high *float64) bool {
	return low != nil && high != nil && *low <= 0 && *high >= 0
}

func scaledPointer(value *float64, scale float64) *float64 {
	if value == nil {
		return nil
	}
	return floatPointer(*value * scale)
}

func ratioRow(name string, baseline, candidate *float64, pass, review float64) Row {
	row := Row{Name: name, Kind: "ratio", Baseline: baseline, Candidate: candidate, Unit: "%", Band: "N/A", Tags: []string{}}
	if baseline == nil || candidate == nil || *baseline == 0 {
		return row
	}
	delta := (*candidate - *baseline) / *baseline * 100
	row.Delta = floatPointer(delta)
	row.Band = thresholdBand(delta, pass, review)
	return row
}

func countRow(name string, baseline, candidate int, unit, band string) Row {
	base, cand := float64(baseline), float64(candidate)
	return Row{Name: name, Kind: "consolidated", Baseline: &base, Candidate: &cand, Delta: floatPointer(cand - base), Unit: unit, Band: band, Tags: []string{}}
}

func meanRunValue(runs []RunRecord, value func(RunData) float64) *float64 {
	if len(runs) == 0 {
		return nil
	}
	total := 0.0
	for _, run := range runs {
		total += value(run.Data)
	}
	return floatPointer(total / float64(len(runs)))
}

func meanControlFPRRuns(runs []RunRecord) *float64 {
	if len(runs) == 0 {
		return nil
	}
	total := 0
	for _, run := range runs {
		for _, result := range run.Data.Cases {
			if !result.Control {
				continue
			}
			for _, state := range result.FindingStates {
				if state == FindingFP {
					total++
					break
				}
			}
		}
	}
	return floatPointer(float64(total) / float64(len(runs)))
}

func unionIssueKeys(baseline, candidate AggregateMetrics) []string {
	set := make(map[string]struct{}, len(baseline.Consolidated)+len(candidate.Consolidated))
	for key := range baseline.Consolidated {
		set[key] = struct{}{}
	}
	for key := range candidate.Consolidated {
		set[key] = struct{}{}
	}
	keys := make([]string, 0, len(set))
	for key := range set {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	return keys
}

func issueMetadataFor(key string, baseline, candidate AggregateMetrics) IssueMetadata {
	if metadata, ok := baseline.IssueMetadata[key]; ok {
		return metadata
	}
	return candidate.IssueMetadata[key]
}

func stateRank(state IssueState) int {
	switch state {
	case IssueTP:
		return 2
	case IssuePD:
		return 1
	case IssueFN:
		return 0
	default:
		return -1
	}
}

func buildDomainRows(manifest Manifest, baseline, candidate AggregateMetrics) []DomainRow {
	keys := unionIssueKeys(baseline, candidate)
	counts := make(map[Domain]*DomainRow)
	for _, domain := range manifest.Domains {
		counts[domain] = &DomainRow{Domain: string(domain)}
	}
	for _, key := range keys {
		metadata := issueMetadataFor(key, baseline, candidate)
		row := counts[metadata.Domain]
		if row == nil {
			row = &DomainRow{Domain: string(metadata.Domain)}
			counts[metadata.Domain] = row
		}
		row.Issues++
		if baseline.Consolidated[key] == IssueTP {
			row.BaselineTP++
		}
		if candidate.Consolidated[key] == IssueTP {
			row.CandidateTP++
		}
		if stateRank(candidate.Consolidated[key]) < stateRank(baseline.Consolidated[key]) {
			row.Lower++
		}
	}
	critical := domainSet(manifest.CriticalDomains)
	rows := make([]DomainRow, 0, len(counts))
	added := make(map[Domain]bool)
	for _, domain := range manifest.Domains {
		row := *counts[domain]
		row.Critical = critical[domain]
		row.Arrow = domainArrow(row.BaselineTP, row.CandidateTP)
		rows = append(rows, row)
		added[domain] = true
	}
	extra := make([]string, 0)
	for domain := range counts {
		if !added[domain] {
			extra = append(extra, string(domain))
		}
	}
	sort.Strings(extra)
	for _, name := range extra {
		domain := Domain(name)
		row := *counts[domain]
		row.Critical = critical[domain]
		row.Arrow = domainArrow(row.BaselineTP, row.CandidateTP)
		rows = append(rows, row)
	}
	return rows
}

func domainArrow(baseline, candidate int) string {
	if candidate > baseline {
		return "↑"
	}
	if candidate < baseline {
		return "↓"
	}
	return "="
}

func domainSet(domains []Domain) map[Domain]bool {
	out := make(map[Domain]bool, len(domains))
	for _, domain := range domains {
		out[domain] = true
	}
	return out
}

func failedReproductions(baseline, candidate []RunRecord) []string {
	confirmedBaseline := make(map[string]bool)
	for _, run := range baseline {
		for _, result := range run.Data.Cases {
			for issueID, finding := range result.Primary {
				if result.FindingStates[finding] == FindingTPFinding && result.Repro[finding] == Reproduced {
					confirmedBaseline[result.Case+"/"+issueID] = true
				}
			}
		}
	}
	failed := make(map[string]bool)
	for _, run := range candidate {
		for _, result := range run.Data.Cases {
			for issueID, finding := range result.Primary {
				key := result.Case + "/" + issueID
				if confirmedBaseline[key] && result.FindingStates[finding] == FindingTPFinding && result.Repro[finding] == NotReproduced {
					failed[key] = true
				}
			}
		}
	}
	keys := make([]string, 0, len(failed))
	for key := range failed {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	return keys
}

func t1Benefit(in DecisionInput, rows []Row, keys []string, baseline, candidate AggregateMetrics) bool {
	recall := findRow(rows, "Strict Recall")
	recallBenefit := recall != nil && recall.Delta != nil && *recall.Delta >= in.Policy.Tradeoffs.T1RecallGainPP
	if recallBenefit && in.Policy.Tradeoffs.T1RecallCILowerAboveZero {
		recallBenefit = recall.CILow != nil && *recall.CILow > 0
	}
	promotion := false
	noCriticalHighLower := true
	for _, key := range keys {
		metadata := issueMetadataFor(key, baseline, candidate)
		if metadata.Severity != Critical && metadata.Severity != High {
			continue
		}
		before, after := baseline.Consolidated[key], candidate.Consolidated[key]
		if stateRank(after) < stateRank(before) {
			noCriticalHighLower = false
		}
		if (before == IssueFN || before == IssuePD) && after == IssueTP {
			promotion = true
		}
	}
	promotionBenefit := in.Policy.Tradeoffs.T1AllowsCriticalHighIssuePromotion && promotion
	if in.Policy.Tradeoffs.T1RequiresNoCriticalHighIssueLower && !noCriticalHighLower {
		promotionBenefit = false
	}
	return recallBenefit || promotionBenefit
}

func t2Benefit(in DecisionInput, rows []Row) bool {
	f1 := findRow(rows, "Strict F1")
	benefit := f1 != nil && f1.Delta != nil && *f1.Delta >= in.Policy.Tradeoffs.T2F1GainPP
	if benefit && in.Policy.Tradeoffs.T2F1CILowerAboveZero {
		benefit = f1.CILow != nil && *f1.CILow > 0
	}
	return benefit
}

func findRow(rows []Row, name string) *Row {
	for i := range rows {
		if rows[i].Name == name {
			return &rows[i]
		}
	}
	return nil
}

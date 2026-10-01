package eval

import (
	"fmt"
	"strings"
)

type ReportExtra struct {
	Policy             string
	Model              string
	Runner             string
	BaselineHarnessID  string
	CandidateHarnessID string
	KnownIssuesMean    float64
	CasesMean          float64
	CleanControlsMean  float64
	BaselineTPMean     float64
	CandidateTPMean    float64
}

func RenderReport(decision ComparisonDecision, baselineLabel, candidateLabel, benchmark string, extra ReportExtra) string {
	if decision.Verdict == "NO_DECISION" {
		reason := "reason unavailable"
		if len(decision.Reasons) > 0 {
			reason = decision.Reasons[0]
		}
		return "NO DECISION — " + reason + "\n"
	}
	var out strings.Builder
	heading := "Harness " + candidateLabel + " vs " + baselineLabel
	out.WriteString(heading)
	if padding := 41 - len(heading); padding > 0 {
		out.WriteString(strings.Repeat(" ", padding))
	} else {
		out.WriteByte(' ')
	}
	fmt.Fprintf(&out, "harness ids %s / %s\n", digestPrefix(extra.CandidateHarnessID), digestPrefix(extra.BaselineHarnessID))
	policy := extra.Policy
	if policy == "" {
		policy = digestPrefix(decision.PolicySHA256)
	}
	fmt.Fprintf(&out, "Benchmark: %s (manifest %s)       %s · model %s · runner %s · K = %d / %d\n", benchmark, digestPrefix(decision.ManifestSHA256), policy, extra.Model, extra.Runner, decision.K.Baseline, decision.K.Candidate)
	fmt.Fprintf(&out, "Counts per run (mean): %.1f Issues · %.1f cases · %.1f clean controls\n\n", extra.KnownIssuesMean, extra.CasesMean, extra.CleanControlsMean)
	fmt.Fprintf(&out, "%-26s %-26s %-14s %-14s %s\n", "Metric", "Baseline → Candidate", "Δ", "CI", "Band")
	for _, row := range decision.Rows {
		if row.Name == "Runtime" || row.Name == "Cost" {
			continue
		}
		baseline, candidate := reportPair(row)
		delta := reportDelta(row)
		ci := ""
		if row.CILow != nil && row.CIHigh != nil {
			ci = fmt.Sprintf("[%+.1f, %+.1f]", *row.CILow, *row.CIHigh)
		}
		tags := ""
		if len(row.Tags) > 0 {
			tags = " (" + strings.Join(row.Tags, ", ") + ")"
		}
		fmt.Fprintf(&out, "%-26s %-26s %-14s %-14s %s%s\n", row.Name, baseline+" → "+candidate, delta, ci, row.Band, tags)
	}
	out.WriteString("\nDomains (consolidated TP, baseline → candidate):\n  ")
	domainParts := make([]string, 0, len(decision.Domains))
	for _, domain := range decision.Domains {
		if domain.Issues == 0 {
			domainParts = append(domainParts, domain.Domain+" N/A")
			continue
		}
		domainParts = append(domainParts, fmt.Sprintf("%s %d/%d → %d/%d %s", domain.Domain, domain.BaselineTP, domain.Issues, domain.CandidateTP, domain.Issues, domain.Arrow))
	}
	out.WriteString(strings.Join(domainParts, "   "))
	out.WriteByte('\n')
	runtime := findRow(decision.Rows, "Runtime")
	cost := findRow(decision.Rows, "Cost")
	if runtime != nil {
		fmt.Fprintf(&out, "Runtime (Σ agent time, mean per run): %s → %s (%s)   %s\n", runtimeValue(runtime.Baseline), runtimeValue(runtime.Candidate), reportDelta(*runtime), runtime.Band)
	}
	if cost != nil {
		fmt.Fprintf(&out, "Cost (mean per run):                  %s → %s (%s)   %s\n", costValue(cost.Baseline), costValue(cost.Candidate), reportDelta(*cost), cost.Band)
	}
	fmt.Fprintf(&out, "Cost per TP (Σ cost / Σ TP):          %s → %s\n", costPerTP(cost, extra.BaselineTPMean, decision.Domains, true), costPerTP(cost, extra.CandidateTPMean, decision.Domains, false))
	out.WriteByte('\n')
	if decision.Verdict == "FAIL" {
		fmt.Fprintf(&out, "FINAL DECISION: FAIL — %s\n", decision.Category)
	} else {
		fmt.Fprintf(&out, "FINAL DECISION: %s\n", decision.Verdict)
	}
	if decision.Provisional {
		reason := "frequency-based blocker is final only at k_max"
		for _, candidateReason := range decision.Reasons {
			if strings.Contains(candidateReason, "final only at k_max") {
				reason = candidateReason
				break
			}
		}
		fmt.Fprintf(&out, "(provisional: %s)\n", reason)
	}
	for _, blocker := range decision.Blockers {
		fmt.Fprintf(&out, "Blocker %s: %s\n", blocker.ID, blocker.Detail)
	}
	return out.String()
}

func digestPrefix(value string) string {
	if value == "" {
		return "…"
	}
	if len(value) <= 4 {
		return value
	}
	return value[:4] + "…"
}

func reportPair(row Row) (string, string) {
	if row.Baseline == nil || row.Candidate == nil {
		return "N/A", "N/A"
	}
	switch row.Unit {
	case "pp":
		return fmt.Sprintf("%.1f%%", *row.Baseline), fmt.Sprintf("%.1f%%", *row.Candidate)
	case "%":
		if row.Name == "Runtime" {
			return runtimeValue(row.Baseline), runtimeValue(row.Candidate)
		}
		if row.Name == "Cost" {
			return costValue(row.Baseline), costValue(row.Candidate)
		}
		return fmt.Sprintf("%.1f", *row.Baseline), fmt.Sprintf("%.1f", *row.Candidate)
	case "issues", "case-runs":
		return fmt.Sprintf("%.0f", *row.Baseline), fmt.Sprintf("%.0f", *row.Candidate)
	default:
		return fmt.Sprintf("%.1f", *row.Baseline), fmt.Sprintf("%.1f", *row.Candidate)
	}
}

func reportDelta(row Row) string {
	if row.Delta == nil {
		return ""
	}
	switch row.Unit {
	case "pp":
		return fmt.Sprintf("%+.1f pp", *row.Delta)
	case "%":
		return fmt.Sprintf("%+.1f%%", *row.Delta)
	case "issues":
		return fmt.Sprintf("%+.0f issues", *row.Delta)
	case "case-runs":
		return fmt.Sprintf("%+.1f case-runs", *row.Delta)
	default:
		return fmt.Sprintf("%+.1f", *row.Delta)
	}
}

func runtimeValue(value *float64) string {
	if value == nil {
		return "N/A"
	}
	return fmt.Sprintf("%.1fm", *value/60)
}

func costValue(value *float64) string {
	if value == nil {
		return "N/A"
	}
	return fmt.Sprintf("$%.1f", *value)
}

func costPerTP(cost *Row, tpMean float64, domains []DomainRow, baseline bool) string {
	if cost == nil {
		return "N/A"
	}
	value := cost.Candidate
	if baseline {
		value = cost.Baseline
	}
	if value == nil {
		return "N/A"
	}
	if tpMean <= 0 {
		tpMean = 0
		for _, domain := range domains {
			if baseline {
				tpMean += float64(domain.BaselineTP)
			} else {
				tpMean += float64(domain.CandidateTP)
			}
		}
	}
	if tpMean <= 0 {
		return "N/A"
	}
	return fmt.Sprintf("$%.2f", *value/tpMean)
}

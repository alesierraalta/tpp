package eval

import (
	"strings"
	"testing"
)

func TestRenderReportGolden(t *testing.T) {
	base, candidate, delta, low, high := 40.0, 42.0, 2.0, .5, 3.5
	runtimeBase, runtimeCandidate, runtimeDelta := 1860.0, 1980.0, 6.45
	costBase, costCandidate, costDelta := 20.4, 21.9, 7.35
	d := ComparisonDecision{
		Verdict: "PASS", K: struct{ Baseline, Candidate int }{2, 2}, ManifestSHA256: "7e21abcdef", PolicySHA256: "policy-digest",
		Rows: []Row{
			{Name: "Strict Recall", Kind: "statistical", Baseline: &base, Candidate: &candidate, Delta: &delta, CILow: &low, CIHigh: &high, Unit: "pp", Band: "PASS"},
			{Name: "Runtime", Kind: "ratio", Baseline: &runtimeBase, Candidate: &runtimeCandidate, Delta: &runtimeDelta, Unit: "%", Band: "PASS"},
			{Name: "Cost", Kind: "ratio", Baseline: &costBase, Candidate: &costCandidate, Delta: &costDelta, Unit: "%", Band: "PASS"},
		},
		Domains: []DomainRow{{Domain: "security", Critical: true, BaselineTP: 6, CandidateTP: 7, Issues: 7, Arrow: "↑"}, {Domain: "performance", Issues: 0}},
	}
	got := RenderReport(d, "v2.0", "v2.1", "CORE 1.0", ReportExtra{Policy: "policy-1", Model: "sonnet", Runner: "pi", BaselineHarnessID: "a1d0abcd", CandidateHarnessID: "3f9c1234", KnownIssuesMean: 39, CasesMean: 22, CleanControlsMean: 2, BaselineTPMean: 28, CandidateTPMean: 30})
	want := `Harness v2.1 vs v2.0                     harness ids 3f9c… / a1d0…
Benchmark: CORE 1.0 (manifest 7e21…)       policy-1 · model sonnet · runner pi · K = 2 / 2
Counts per run (mean): 39.0 Issues · 22.0 cases · 2.0 clean controls

Metric                     Baseline → Candidate       Δ              CI             Band
Strict Recall              40.0% → 42.0%              +2.0 pp        [+0.5, +3.5]   PASS

Domains (consolidated TP, baseline → candidate):
  security 6/7 → 7/7 ↑   performance N/A
Runtime (Σ agent time, mean per run): 31.0m → 33.0m (+6.5%)   PASS
Cost (mean per run):                  $20.4 → $21.9 (+7.3%)   PASS
Cost per TP (Σ cost / Σ TP):          $0.73 → $0.73

FINAL DECISION: PASS
`
	if got != want {
		t.Fatalf("RenderReport() mismatch\n--- got ---\n%s\n--- want ---\n%s", got, want)
	}
}

func TestRenderReportFailureNoDecisionAndProvisional(t *testing.T) {
	d := ComparisonDecision{Verdict: "NO_DECISION", Reasons: []string{"model differs"}}
	if got := RenderReport(d, "base", "cand", "CORE", ReportExtra{}); got != "NO DECISION — model differs\n" {
		t.Fatalf("no-decision report = %q", got)
	}
	d = ComparisonDecision{Verdict: "FAIL", Category: "REGRESSION", Provisional: true, Reasons: []string{"frequency-based blocker is final only at k_max"}, Blockers: []Blocker{{ID: "H1", Detail: "critical issue newly FN"}}}
	got := RenderReport(d, "base", "cand", "CORE", ReportExtra{})
	for _, want := range []string{"FINAL DECISION: FAIL — REGRESSION", "(provisional: frequency-based blocker is final only at k_max)", "Blocker H1: critical issue newly FN"} {
		if !strings.Contains(got, want) {
			t.Errorf("failure report missing %q:\n%s", want, got)
		}
	}
}

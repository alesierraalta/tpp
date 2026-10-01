package eval

import "fmt"

type StopAdvice struct {
	Stop   bool
	Reason string
}

func ShouldStop(in DecisionInput) StopAdvice {
	k := len(completedRecords(in.Candidate.Runs))
	if k < in.Manifest.Replicates.KMin {
		return StopAdvice{Reason: fmt.Sprintf("candidate has %d completed runs; need k_min %d", k, in.Manifest.Replicates.KMin)}
	}
	decision := Decide(in)
	for _, id := range []string{"H4", "H5", "H6", "H7", "H8"} {
		if hasBlockerID(decision.Blockers, id) {
			return StopAdvice{Stop: true, Reason: "non-frequency blocker " + id + " is present"}
		}
	}
	if k >= in.Manifest.Replicates.KMax {
		return StopAdvice{Stop: true, Reason: "candidate reached k_max"}
	}
	for _, id := range []string{"H1", "H2", "H3"} {
		if hasBlockerID(decision.Blockers, id) {
			return StopAdvice{Reason: "frequency-based blocker " + id + " must be confirmed at k_max"}
		}
	}

	baseline, candidate := recordsData(completedRecords(in.Baseline.Runs)), recordsData(completedRecords(in.Candidate.Runs))
	weight := in.Manifest.MetricConfig.WeightedRecallW
	metrics := []struct {
		metric       func(RunMetrics) *float64
		pass, review float64
		drop         bool
	}{
		{func(m RunMetrics) *float64 { return m.StrictRecall }, in.Policy.Thresholds.RecallDropPassPP, in.Policy.Thresholds.RecallDropReviewPP, true},
		{func(m RunMetrics) *float64 { return m.StrictPrecision }, in.Policy.Thresholds.PrecisionDropPassPP, in.Policy.Thresholds.PrecisionDropReviewPP, true},
		{func(m RunMetrics) *float64 { return m.StrictF1 }, in.Policy.Thresholds.F1DropPassPP, in.Policy.Thresholds.F1DropReviewPP, true},
		{func(m RunMetrics) *float64 { return m.Reproducibility }, in.Policy.Thresholds.ReproducibilityDropPassPP, in.Policy.Thresholds.ReproducibilityDropReviewPP, true},
		{func(m RunMetrics) *float64 { return m.FDR }, in.Policy.Thresholds.FDRRisePassPP, in.Policy.Thresholds.FDRRiseReviewPP, false},
	}
	for _, metric := range metrics {
		interval := PairedBootstrap(baseline, candidate, metric.metric, weight, in.Manifest.MetricConfig.BootstrapResamples, in.Manifest.MetricConfig.BootstrapSeed, in.Manifest.MetricConfig.EarlyStopCILevel)
		if interval.Low == nil || interval.High == nil {
			return StopAdvice{Reason: "statistical interval is undefined; continue sampling"}
		}
		low, high := *interval.Low*100, *interval.High*100
		if metric.drop {
			low, high = -high, -low
		}
		if thresholdBand(low, metric.pass, metric.review) != thresholdBand(high, metric.pass, metric.review) {
			return StopAdvice{Reason: "statistical interval crosses a decision band"}
		}
	}
	return StopAdvice{Stop: true, Reason: "all statistical intervals are settled within one band"}
}

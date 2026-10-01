package eval

import (
	"math"
	"math/rand/v2"
	"sort"
)

const (
	MetricStrictRecall              = "strict_recall"
	MetricDetectionCoverage         = "detection_coverage"
	MetricMissRate                  = "miss_rate"
	MetricPartialDetectionRate      = "partial_detection_rate"
	MetricWeightedRecall            = "weighted_recall"
	MetricStrictPrecision           = "strict_precision"
	MetricAcceptedPrecision         = "accepted_precision"
	MetricFDR                       = "fdr"
	MetricControlFPR                = "control_fpr"
	MetricStrictF1                  = "strict_f1"
	MetricDuplicateRate             = "duplicate_rate"
	MetricLowValueRate              = "low_value_rate"
	MetricInconclusiveRate          = "inconclusive_rate"
	MetricInvalidRate               = "invalid_rate"
	MetricReproducibility           = "reproducibility"
	MetricExactSeverityAccuracy     = "exact_severity_accuracy"
	MetricWithinOneSeverityAccuracy = "within_one_severity_accuracy"
	MetricCostPerTP                 = "cost_per_tp"
	MetricTimePerTP                 = "time_per_tp"
	MetricMacroStrictRecall         = "macro_strict_recall"
)

// Summary describes the distribution of defined per-run values.
type Summary struct {
	N                int
	Mean, Median, SD *float64
	Min, Max         *float64
}

// Interval contains a paired-bootstrap point estimate and percentile bounds.
type Interval struct {
	Delta, Low, High *float64
}

// AggregateMetrics summarizes per-run metrics and per-Issue outcomes.
type AggregateMetrics struct {
	Runs                  []RunMetrics
	Metrics               map[string]Summary
	DetectionFrequency    map[string]float64
	AnyDetectionFrequency map[string]float64
	Consolidated          map[string]IssueState
	IssueMetadata         map[string]IssueMetadata
}

// Summarize ignores undefined values and uses sample standard deviation.
func Summarize(values []*float64) Summary {
	defined := make([]float64, 0, len(values))
	for _, value := range values {
		if value != nil {
			defined = append(defined, *value)
		}
	}
	if len(defined) == 0 {
		return Summary{}
	}
	sort.Float64s(defined)
	total := 0.0
	for _, value := range defined {
		total += value
	}
	mean := total / float64(len(defined))
	median := defined[len(defined)/2]
	if len(defined)%2 == 0 {
		median = (defined[len(defined)/2-1] + defined[len(defined)/2]) / 2
	}
	var sd *float64
	if len(defined) > 1 {
		sumSquares := 0.0
		for _, value := range defined {
			delta := value - mean
			sumSquares += delta * delta
		}
		sd = floatPointer(math.Sqrt(sumSquares / float64(len(defined)-1)))
	}
	return Summary{
		N: len(defined), Mean: floatPointer(mean), Median: floatPointer(median), SD: sd,
		Min: floatPointer(defined[0]), Max: floatPointer(defined[len(defined)-1]),
	}
}

// Aggregate computes a distribution for every ratio metric and per-Issue run frequencies.
func Aggregate(runs []RunData, weight float64) AggregateMetrics {
	result := AggregateMetrics{
		Runs:                  make([]RunMetrics, 0, len(runs)),
		Metrics:               make(map[string]Summary),
		DetectionFrequency:    make(map[string]float64),
		AnyDetectionFrequency: make(map[string]float64),
		Consolidated:          make(map[string]IssueState),
		IssueMetadata:         make(map[string]IssueMetadata),
	}
	for _, run := range runs {
		result.Runs = append(result.Runs, ComputeRun(run, weight))
	}
	metricValues := []struct {
		name  string
		value func(RunMetrics) *float64
	}{
		{MetricStrictRecall, func(m RunMetrics) *float64 { return m.StrictRecall }},
		{MetricDetectionCoverage, func(m RunMetrics) *float64 { return m.DetectionCoverage }},
		{MetricMissRate, func(m RunMetrics) *float64 { return m.MissRate }},
		{MetricPartialDetectionRate, func(m RunMetrics) *float64 { return m.PartialDetectionRate }},
		{MetricWeightedRecall, func(m RunMetrics) *float64 { return m.WeightedRecall }},
		{MetricStrictPrecision, func(m RunMetrics) *float64 { return m.StrictPrecision }},
		{MetricAcceptedPrecision, func(m RunMetrics) *float64 { return m.AcceptedPrecision }},
		{MetricFDR, func(m RunMetrics) *float64 { return m.FDR }},
		{MetricControlFPR, func(m RunMetrics) *float64 { return m.ControlFPR }},
		{MetricStrictF1, func(m RunMetrics) *float64 { return m.StrictF1 }},
		{MetricDuplicateRate, func(m RunMetrics) *float64 { return m.DuplicateRate }},
		{MetricLowValueRate, func(m RunMetrics) *float64 { return m.LowValueRate }},
		{MetricInconclusiveRate, func(m RunMetrics) *float64 { return m.InconclusiveRate }},
		{MetricInvalidRate, func(m RunMetrics) *float64 { return m.InvalidRate }},
		{MetricReproducibility, func(m RunMetrics) *float64 { return m.Reproducibility }},
		{MetricExactSeverityAccuracy, func(m RunMetrics) *float64 { return m.ExactSeverityAccuracy }},
		{MetricWithinOneSeverityAccuracy, func(m RunMetrics) *float64 { return m.WithinOneSeverityAccuracy }},
		{MetricCostPerTP, func(m RunMetrics) *float64 { return m.CostPerTP }},
		{MetricTimePerTP, func(m RunMetrics) *float64 { return m.TimePerTP }},
		{MetricMacroStrictRecall, func(m RunMetrics) *float64 { return m.MacroStrictRecall }},
	}
	for _, metric := range metricValues {
		values := make([]*float64, 0, len(result.Runs))
		for _, runMetric := range result.Runs {
			values = append(values, metric.value(runMetric))
		}
		result.Metrics[metric.name] = Summarize(values)
	}

	type votes struct{ tp, pd, fn int }
	counts := make(map[string]votes)
	valid := make(map[string]int)
	for _, runMetric := range result.Runs {
		for key, state := range runMetric.IssueStates {
			v := counts[key]
			switch state {
			case IssueTP:
				v.tp++
			case IssuePD:
				v.pd++
			case IssueFN:
				v.fn++
			default:
				continue
			}
			counts[key] = v
			valid[key]++
			if _, exists := result.IssueMetadata[key]; !exists {
				result.IssueMetadata[key] = runMetric.IssueMetadata[key]
			}
		}
	}
	for key, v := range counts {
		denominator := valid[key]
		result.DetectionFrequency[key] = float64(v.tp) / float64(denominator)
		result.AnyDetectionFrequency[key] = float64(v.tp+v.pd) / float64(denominator)
		switch {
		case 2*v.tp > denominator:
			result.Consolidated[key] = IssueTP
		case 2*(v.tp+v.pd) > denominator:
			result.Consolidated[key] = IssuePD
		default:
			result.Consolidated[key] = IssueFN
		}
	}
	return result
}

// PairedBootstrap estimates a candidate-minus-baseline delta by resampling case clusters.
func PairedBootstrap(baseline, candidate []RunData, metric func(RunMetrics) *float64, weight float64, resamples int, seed uint64, level float64) Interval {
	if len(baseline) == 0 || len(candidate) == 0 || metric == nil {
		return Interval{}
	}
	baselineMean := meanRunMetric(baseline, metric, weight)
	candidateMean := meanRunMetric(candidate, metric, weight)
	if baselineMean == nil || candidateMean == nil {
		return Interval{}
	}
	point := floatPointer(*candidateMean - *baselineMean)
	interval := Interval{Delta: point}
	caseIDs := sharedCaseIDs(baseline, candidate)
	if len(caseIDs) == 0 || resamples <= 0 || level <= 0 || level >= 1 {
		return interval
	}
	rng := rand.New(rand.NewPCG(seed, seed^0x9e3779b97f4a7c15))
	deltas := make([]float64, 0, resamples)
	for i := 0; i < resamples; i++ {
		drawn := make([]string, len(caseIDs))
		for j := range drawn {
			drawn[j] = caseIDs[rng.IntN(len(caseIDs))]
		}
		base := meanRunMetric(restrictRuns(baseline, drawn), metric, weight)
		cand := meanRunMetric(restrictRuns(candidate, drawn), metric, weight)
		if base == nil || cand == nil {
			continue
		}
		deltas = append(deltas, *cand-*base)
	}
	if len(deltas) == 0 {
		return interval
	}
	sort.Float64s(deltas)
	tail := (1 - level) / 2
	interval.Low = floatPointer(percentile(deltas, tail))
	interval.High = floatPointer(percentile(deltas, 1-tail))
	return interval
}

func meanRunMetric(runs []RunData, metric func(RunMetrics) *float64, weight float64) *float64 {
	total, count := 0.0, 0
	for _, run := range runs {
		if value := metric(ComputeRun(run, weight)); value != nil {
			total += *value
			count++
		}
	}
	if count == 0 {
		return nil
	}
	return floatPointer(total / float64(count))
}

func sharedCaseIDs(baseline, candidate []RunData) []string {
	present := func(runs []RunData) map[string]bool {
		ids := make(map[string]bool)
		for _, run := range runs {
			for _, result := range run.Cases {
				if result.Control || len(result.Issues) > 0 {
					ids[result.Case] = true
				}
			}
		}
		return ids
	}
	baseIDs, candidateIDs := present(baseline), present(candidate)
	shared := make([]string, 0)
	for id := range baseIDs {
		if candidateIDs[id] {
			shared = append(shared, id)
		}
	}
	sort.Strings(shared)
	return shared
}

func restrictRuns(runs []RunData, caseIDs []string) []RunData {
	restricted := make([]RunData, len(runs))
	for i, run := range runs {
		byID := make(map[string]CaseResult, len(run.Cases))
		for _, result := range run.Cases {
			byID[result.Case] = result
		}
		restricted[i] = RunData{ID: run.ID, Cases: make([]CaseResult, 0, len(caseIDs))}
		for _, id := range caseIDs {
			if result, exists := byID[id]; exists {
				restricted[i].Cases = append(restricted[i].Cases, result)
			}
		}
	}
	return restricted
}

func percentile(values []float64, p float64) float64 {
	if p <= 0 {
		return values[0]
	}
	if p >= 1 {
		return values[len(values)-1]
	}
	position := p * float64(len(values)-1)
	lower := int(math.Floor(position))
	upper := int(math.Ceil(position))
	if lower == upper {
		return values[lower]
	}
	fraction := position - float64(lower)
	return values[lower] + fraction*(values[upper]-values[lower])
}

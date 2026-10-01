package eval

import (
	"math"
	"sort"
)

// DomainMetrics contains issue counts and strict recall for one populated domain.
type DomainMetrics struct {
	TP           int
	PD           int
	FN           int
	StrictRecall *float64
}

// IssueMetadata is the ground-truth taxonomy used for later consolidation rules.
type IssueMetadata struct {
	Domain   Domain
	Severity Severity
}

// RunMetrics contains micro-averaged counts and ratios for one benchmark run.
type RunMetrics struct {
	TP, PD, FN                                       int
	TPFinding, PDFinding, FP                         int
	Duplicate, LowValue, Inconclusive                int
	ConfirmedNovel, Invalid, KnownIssues             int
	TotalRawFindings, ReproNotRun                    int
	CostUSD, AgentSeconds                            float64
	Tokens                                           int
	StrictRecall, DetectionCoverage, MissRate        *float64
	PartialDetectionRate, WeightedRecall             *float64
	StrictPrecision, AcceptedPrecision, FDR          *float64
	ControlFPR, StrictF1                             *float64
	DuplicateRate, LowValueRate, InconclusiveRate    *float64
	InvalidRate, Reproducibility                     *float64
	ExactSeverityAccuracy, WithinOneSeverityAccuracy *float64
	CostPerTP, TimePerTP                             *float64
	ByDomain                                         map[Domain]DomainMetrics
	MacroStrictRecall                                *float64
	IssueStates                                      map[string]IssueState
	IssueMetadata                                    map[string]IssueMetadata
	Consistent                                       bool
}

// ComputeRun derives metrics from case outcomes without mutating run data.
func ComputeRun(run RunData, weight float64) RunMetrics {
	metrics := RunMetrics{
		CostUSD:       run.Cost(),
		AgentSeconds:  run.AgentSeconds(),
		Tokens:        run.Tokens(),
		ByDomain:      make(map[Domain]DomainMetrics),
		IssueStates:   make(map[string]IssueState),
		IssueMetadata: make(map[string]IssueMetadata),
		Consistent:    true,
	}
	controlRuns, controlsWithFP := 0, 0
	severityCount, exactSeverity, withinSeverity := 0, 0, 0
	reproNumerator, reproDenominator := 0, 0

	for _, result := range run.Cases {
		if result.Control {
			controlRuns++
		}
		caseHasFP := false
		for findingID, state := range result.FindingStates {
			switch state {
			case FindingTPFinding:
				metrics.TPFinding++
			case FindingPDFinding:
				metrics.PDFinding++
			case FindingFP:
				metrics.FP++
			case FindingDuplicate:
				metrics.Duplicate++
			case FindingLowValue:
				metrics.LowValue++
			case FindingInconclusive:
				metrics.Inconclusive++
			case FindingConfirmedNovel:
				metrics.ConfirmedNovel++
			case FindingInvalid:
				metrics.Invalid++
			default:
				continue
			}
			metrics.TotalRawFindings++
			if state == FindingFP && result.Control {
				caseHasFP = true
			}
			if state == FindingTPFinding || state == FindingPDFinding || state == FindingConfirmedNovel {
				switch result.Repro[findingID] {
				case Reproduced:
					reproNumerator++
					reproDenominator++
				case NotReproduced:
					reproDenominator++
				case NotApplicable:
					// Excluded from the reproducibility denominator.
				case NotRun, "":
					metrics.ReproNotRun++
				default:
					metrics.ReproNotRun++
				}
			}
		}
		if result.Control {
			if caseHasFP {
				controlsWithFP++
			}
			continue
		}
		for _, issue := range result.Issues {
			state, exists := result.IssueStates[issue.ID]
			if !exists {
				continue
			}
			var domainMetrics DomainMetrics
			switch state {
			case IssueTP:
				metrics.TP++
				domainMetrics.TP++
			case IssuePD:
				metrics.PD++
				domainMetrics.PD++
			case IssueFN:
				metrics.FN++
				domainMetrics.FN++
			default:
				continue
			}
			metrics.KnownIssues++
			key := issueMetricKey(issue, result.Case)
			metrics.IssueStates[key] = state
			metrics.IssueMetadata[key] = IssueMetadata{Domain: issue.Domain, Severity: issue.Severity}
			current := metrics.ByDomain[issue.Domain]
			current.TP += domainMetrics.TP
			current.PD += domainMetrics.PD
			current.FN += domainMetrics.FN
			metrics.ByDomain[issue.Domain] = current
			if state == IssueTP || state == IssuePD {
				if issue.Severity != SeverityUnknown {
					severityCount++
					reported := SeverityUnknown
					if severity, exists := result.ReportedSeverity[result.Primary[issue.ID]]; exists {
						reported = severity
					}
					if reported == issue.Severity {
						exactSeverity++
					}
					if reported != SeverityUnknown && math.Abs(float64(reported-issue.Severity)) <= 1 {
						withinSeverity++
					}
				}
			}
		}
	}

	metrics.StrictRecall = ratio(metrics.TP, metrics.KnownIssues)
	metrics.DetectionCoverage = ratio(metrics.TP+metrics.PD, metrics.KnownIssues)
	metrics.MissRate = ratio(metrics.FN, metrics.KnownIssues)
	metrics.PartialDetectionRate = ratio(metrics.PD, metrics.KnownIssues)
	if metrics.KnownIssues > 0 {
		metrics.WeightedRecall = floatPointer((float64(metrics.TP) + weight*float64(metrics.PD)) / float64(metrics.KnownIssues))
	}
	findingDenominator := metrics.TPFinding + metrics.PDFinding + metrics.FP
	metrics.StrictPrecision = ratio(metrics.TPFinding, findingDenominator)
	metrics.AcceptedPrecision = ratio(metrics.TPFinding+metrics.PDFinding, findingDenominator)
	metrics.FDR = ratio(metrics.FP, findingDenominator)
	if controlRuns > 0 {
		metrics.ControlFPR = floatPointer(float64(controlsWithFP) / float64(controlRuns))
	}
	if metrics.StrictPrecision != nil && metrics.StrictRecall != nil {
		if *metrics.StrictPrecision == 0 && *metrics.StrictRecall == 0 {
			metrics.StrictF1 = floatPointer(0)
		} else {
			metrics.StrictF1 = floatPointer(2 * *metrics.StrictPrecision * *metrics.StrictRecall / (*metrics.StrictPrecision + *metrics.StrictRecall))
		}
	}
	metrics.DuplicateRate = ratio(metrics.Duplicate, metrics.TotalRawFindings)
	metrics.LowValueRate = ratio(metrics.LowValue, metrics.TotalRawFindings)
	metrics.InconclusiveRate = ratio(metrics.Inconclusive, metrics.TotalRawFindings)
	metrics.InvalidRate = ratio(metrics.Invalid, metrics.TotalRawFindings)
	if reproDenominator > 0 {
		// Not-applicable and not-run findings are removed from this denominator.
		metrics.Reproducibility = floatPointer(float64(reproNumerator) / float64(reproDenominator))
	}
	metrics.ExactSeverityAccuracy = ratio(exactSeverity, severityCount)
	metrics.WithinOneSeverityAccuracy = ratio(withinSeverity, severityCount)
	metrics.CostPerTP = ratioFloat(metrics.CostUSD, metrics.TP)
	metrics.TimePerTP = ratioFloat(metrics.AgentSeconds, metrics.TP)

	domains := make([]Domain, 0, len(metrics.ByDomain))
	for domain := range metrics.ByDomain {
		domains = append(domains, domain)
	}
	sort.Slice(domains, func(i, j int) bool { return domains[i] < domains[j] })
	var domainRecalls []float64
	for _, domain := range domains {
		domainMetrics := metrics.ByDomain[domain]
		known := domainMetrics.TP + domainMetrics.PD + domainMetrics.FN
		domainMetrics.StrictRecall = ratio(domainMetrics.TP, known)
		metrics.ByDomain[domain] = domainMetrics
		if domainMetrics.StrictRecall != nil {
			domainRecalls = append(domainRecalls, *domainMetrics.StrictRecall)
		}
	}
	if len(domainRecalls) > 0 {
		total := 0.0
		for _, recall := range domainRecalls {
			total += recall
		}
		metrics.MacroStrictRecall = floatPointer(total / float64(len(domainRecalls)))
	}
	if metrics.StrictRecall != nil && metrics.PartialDetectionRate != nil && metrics.MissRate != nil {
		metrics.Consistent = math.Abs(*metrics.StrictRecall+*metrics.PartialDetectionRate+*metrics.MissRate-1) <= 1e-12
	}
	return metrics
}

func issueMetricKey(issue Issue, caseID string) string {
	if issue.Case == "" {
		issue.Case = caseID
	}
	return issue.Key()
}

func ratio(numerator, denominator int) *float64 {
	if denominator == 0 {
		return nil
	}
	return floatPointer(float64(numerator) / float64(denominator))
}

func ratioFloat(numerator float64, denominator int) *float64 {
	if denominator == 0 {
		return nil
	}
	return floatPointer(numerator / float64(denominator))
}

func floatPointer(value float64) *float64 { return &value }

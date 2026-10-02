package eval

import "fmt"

// ReproOutcome records the confirmation re-run result for a finding.
type ReproOutcome string

const (
	Reproduced    ReproOutcome = "REPRODUCED"
	NotReproduced ReproOutcome = "NOT_REPRODUCED"
	NotApplicable ReproOutcome = "NOT_APPLICABLE"
	NotRun        ReproOutcome = "NOT_RUN"
)

// CaseResult is the metric-ready outcome of one case execution.
type CaseResult struct {
	Case             string
	Control          bool
	Outcome          string
	Issues           []Issue
	IssueStates      map[string]IssueState
	Primary          map[string]string
	FindingStates    map[string]FindingState
	ReportedSeverity map[string]Severity
	Repro            map[string]ReproOutcome
	CostUSD          float64
	AgentSeconds     float64
	Tokens           *int
}

// CaseResultFrom copies the terminal outcomes of a CaseRun for metric computation.
func CaseResultFrom(cr *CaseRun, control bool, repro map[string]ReproOutcome) (CaseResult, error) {
	if cr == nil {
		return CaseResult{}, fmt.Errorf("case run is nil")
	}
	result := CaseResult{
		Case:             cr.Case,
		Control:          control,
		Issues:           append([]Issue(nil), cr.Issues...),
		IssueStates:      make(map[string]IssueState, len(cr.Issues)),
		Primary:          make(map[string]string, len(cr.Issues)),
		FindingStates:    make(map[string]FindingState, len(cr.Findings)),
		ReportedSeverity: make(map[string]Severity, len(cr.Findings)),
		Repro:            make(map[string]ReproOutcome, len(repro)),
	}
	for i := range result.Issues {
		issue := &result.Issues[i]
		if issue.Case == "" {
			issue.Case = cr.Case
		}
		state := cr.IssueState(issue.ID)
		if state != IssueTP && state != IssuePD && state != IssueFN {
			return CaseResult{}, fmt.Errorf("issue %q has non-terminal state %q", issue.ID, state)
		}
		result.IssueStates[issue.ID] = state
		result.Primary[issue.ID] = cr.Primary(issue.ID)
	}
	for _, finding := range cr.Findings {
		state := cr.FindingState(finding.ID)
		if !state.IsTerminal() {
			return CaseResult{}, fmt.Errorf("finding %q has non-terminal state %q", finding.ID, state)
		}
		result.FindingStates[finding.ID] = state
		result.ReportedSeverity[finding.ID] = SeverityFromReported(finding.ReportedSeverityText)
	}
	for findingID, outcome := range repro {
		result.Repro[findingID] = outcome
	}
	return result, nil
}

// RunData contains the case results for one benchmark run.
type RunData struct {
	ID    string
	Cases []CaseResult
}

func (run RunData) Cost() float64 {
	var total float64
	for _, result := range run.Cases {
		total += result.CostUSD
	}
	return total
}

func (run RunData) AgentSeconds() float64 {
	var total float64
	for _, result := range run.Cases {
		total += result.AgentSeconds
	}
	return total
}

// Tokens reports the run's total usage, or nil while any case's usage is unknown.
func (run RunData) Tokens() *int {
	var total int
	for _, result := range run.Cases {
		if result.Tokens == nil {
			return nil
		}
		total += *result.Tokens
	}
	return &total
}

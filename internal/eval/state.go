package eval

// IssueState is the per-case-run classification of an Issue.
type IssueState string

const (
	IssuePending         IssueState = "PENDING"
	IssueUnderEvaluation IssueState = "UNDER_EVALUATION"
	IssueTP              IssueState = "TP"
	IssuePD              IssueState = "PD"
	IssueFN              IssueState = "FN"
)

// FindingState is the adjudication and derived classification of a finding.
type FindingState string

const (
	FindingRaw                 FindingState = "RAW"
	FindingPendingAdjudication FindingState = "PENDING_ADJUDICATION"
	FindingMatched             FindingState = "MATCHED"
	FindingUnmatched           FindingState = "UNMATCHED"
	FindingNovelCandidate      FindingState = "NOVEL_CANDIDATE"
	FindingTPFinding           FindingState = "TP_FINDING"
	FindingPDFinding           FindingState = "PD_FINDING"
	FindingDuplicate           FindingState = "DUPLICATE"
	FindingConfirmedNovel      FindingState = "CONFIRMED_NOVEL"
	FindingFP                  FindingState = "FP"
	FindingLowValue            FindingState = "LOW_VALUE"
	FindingInconclusive        FindingState = "INCONCLUSIVE"
	FindingInvalid             FindingState = "INVALID"
)

// CaseRunState is the lifecycle state of one case execution and its adjudication.
type CaseRunState string

const (
	CaseRunCreated              CaseRunState = "CREATED"
	CaseRunRunning              CaseRunState = "RUNNING"
	CaseRunScored               CaseRunState = "SCORED"
	CaseRunAdjudicating         CaseRunState = "ADJUDICATING"
	CaseRunClosed               CaseRunState = "CLOSED"
	CaseRunFailedInfrastructure CaseRunState = "FAILED_INFRASTRUCTURE"
	CaseRunInvalid              CaseRunState = "INVALID"
	CaseRunAborted              CaseRunState = "ABORTED"
)

// RunState is the lifecycle state of a benchmark run.
type RunState string

const (
	RunCreated              RunState = "CREATED"
	RunRunning              RunState = "RUNNING"
	RunAdjudicating         RunState = "ADJUDICATING"
	RunCompleted            RunState = "COMPLETED"
	RunInvalid              RunState = "INVALID"
	RunFailedInfrastructure RunState = "FAILED_INFRASTRUCTURE"
	RunAborted              RunState = "ABORTED"
)

// CanTransitionIssue reports whether the transition is listed in section 5.1.
func CanTransitionIssue(from, to IssueState) bool {
	if from == IssuePending && to == IssueUnderEvaluation || from == IssueUnderEvaluation && (to == IssueTP || to == IssuePD || to == IssueFN) {
		return true
	}
	return (from == IssueTP || from == IssuePD || from == IssueFN) && to == IssueUnderEvaluation
}

// CanTransitionFinding reports whether the transition is listed in section 5.2.
func CanTransitionFinding(from, to FindingState) bool {
	switch from {
	case FindingRaw:
		return to == FindingPendingAdjudication
	case FindingPendingAdjudication:
		return to == FindingInvalid || to == FindingMatched || to == FindingUnmatched
	case FindingMatched:
		return to == FindingTPFinding || to == FindingPDFinding || to == FindingDuplicate
	case FindingUnmatched:
		return to == FindingNovelCandidate
	case FindingNovelCandidate:
		return to == FindingConfirmedNovel || to == FindingFP || to == FindingLowValue || to == FindingInconclusive || to == FindingDuplicate
	default:
		return from.IsTerminal() && to == FindingPendingAdjudication
	}
}

// CanTransitionCaseRun reports whether the transition is listed in section 5.3.
func CanTransitionCaseRun(from, to CaseRunState) bool {
	switch from {
	case CaseRunCreated:
		return to == CaseRunRunning
	case CaseRunRunning:
		return to == CaseRunScored || to == CaseRunFailedInfrastructure || to == CaseRunInvalid || to == CaseRunAborted
	case CaseRunScored:
		return to == CaseRunAdjudicating
	case CaseRunAdjudicating:
		return to == CaseRunClosed
	case CaseRunFailedInfrastructure:
		return to == CaseRunRunning
	default:
		return false
	}
}

// CanTransitionRun reports whether the transition is listed in section 5.4.
func CanTransitionRun(from, to RunState) bool {
	switch from {
	case RunCreated:
		return to == RunRunning || to == RunInvalid
	case RunRunning:
		return to == RunAdjudicating || to == RunFailedInfrastructure || to == RunAborted || to == RunInvalid
	case RunAdjudicating:
		return to == RunCompleted || to == RunInvalid
	default:
		return false
	}
}

// IsTerminal reports whether the Issue classification can be reopened only by its documented edge.
func (s IssueState) IsTerminal() bool {
	return s == IssueTP || s == IssuePD || s == IssueFN
}

// IsTerminal reports whether the finding has a final classification.
func (s FindingState) IsTerminal() bool {
	switch s {
	case FindingTPFinding, FindingPDFinding, FindingDuplicate, FindingConfirmedNovel, FindingFP, FindingLowValue, FindingInconclusive, FindingInvalid:
		return true
	default:
		return false
	}
}

// IsTerminal reports whether the case-run lifecycle has ended.
func (s CaseRunState) IsTerminal() bool {
	return s == CaseRunClosed || s == CaseRunInvalid || s == CaseRunAborted
}

// IsTerminal reports whether the benchmark-run lifecycle has ended.
func (s RunState) IsTerminal() bool {
	return s == RunCompleted || s == RunInvalid || s == RunFailedInfrastructure || s == RunAborted
}

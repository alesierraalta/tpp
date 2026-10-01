package eval

import (
	"errors"
	"strings"
)

// Decision records the facts and conclusion supplied by an adjudicator for one finding.
type Decision struct {
	FindingID        string       `json:"finding_id"`
	CandidateIssue   string       `json:"candidate_issue,omitempty"`
	C1               Fact         `json:"c1"`
	C3               Fact         `json:"c3"`
	C4Shows          Fact         `json:"c4_shows_failure"`
	EquivalentTo     string       `json:"equivalent_to,omitempty"`
	UnmatchedOutcome FindingState `json:"unmatched_outcome,omitempty"`
	By               string       `json:"by"`
	TS               string       `json:"ts"`
	Reason           string       `json:"reason"`
}

// Validate checks the required adjudicator attribution and allowed unmatched outcomes.
func (d Decision) Validate() error {
	var problems []string
	if strings.TrimSpace(d.By) == "" {
		problems = append(problems, "by is required")
	}
	if strings.TrimSpace(d.Reason) == "" {
		problems = append(problems, "reason is required")
	}
	if d.UnmatchedOutcome != "" && d.UnmatchedOutcome != FindingConfirmedNovel && d.UnmatchedOutcome != FindingFP && d.UnmatchedOutcome != FindingLowValue && d.UnmatchedOutcome != FindingInconclusive {
		problems = append(problems, "unmatched outcome must be CONFIRMED_NOVEL, FP, LOW_VALUE, or INCONCLUSIVE")
	}
	if d.UnmatchedOutcome != "" && d.EquivalentTo != "" {
		problems = append(problems, "unmatched outcome and equivalent_to cannot both be set")
	}
	if len(problems) > 0 {
		return errors.New(strings.Join(problems, "; "))
	}
	return nil
}

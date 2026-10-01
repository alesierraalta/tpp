package eval

import "fmt"

// Violation identifies a violated case-run invariant.
type Violation struct {
	ID     string `json:"id"`
	Detail string `json:"detail"`
}

// CheckCaseRun checks the case-run invariants defined in section 6.
func CheckCaseRun(cr *CaseRun) []Violation {
	if cr == nil {
		return []Violation{{ID: "I5", Detail: "case run is nil"}}
	}
	var violations []Violation
	add := func(id, detail string) {
		violations = append(violations, Violation{ID: id, Detail: detail})
	}
	if err := cr.Log.Verify(); err != nil {
		add("I9", "event log does not verify: "+err.Error())
	}
	for index, event := range cr.Log.Events {
		if event.PreviousState == event.NewState {
			if event.Kind != EventDecide {
				add("I11", fmt.Sprintf("event %d has a state-preserving non-decision", index+1))
			}
		} else if !canTransition(event.Entity, event.PreviousState, event.NewState) {
			add("I11", fmt.Sprintf("event %d has illegal %s transition %q -> %q", index+1, event.Entity, event.PreviousState, event.NewState))
		}
	}

	closed := cr.caseRunState() == CaseRunClosed
	states := cr.Log.States()
	for _, finding := range cr.Findings {
		folded, exists := states[EntityFinding+":"+finding.ID]
		if !exists {
			folded = string(FindingRaw)
		}
		if folded != string(cr.FindingState(finding.ID)) {
			add("I5", fmt.Sprintf("finding %q accessor state %q differs from log state %q", finding.ID, cr.FindingState(finding.ID), folded))
		}
		findingState := FindingState(folded)
		if closed && !findingState.IsTerminal() {
			add("I2", fmt.Sprintf("closed case run has non-terminal finding %q in state %q", finding.ID, folded))
		}
		switch findingState {
		case FindingRaw, FindingPendingAdjudication, FindingMatched, FindingUnmatched, FindingNovelCandidate:
			add("I8", fmt.Sprintf("finding %q is unresolved in state %q", finding.ID, findingState))
		}
	}
	for _, issue := range cr.Issues {
		folded, exists := states[EntityIssue+":"+cr.issueEventID(issue)]
		if !exists {
			folded = string(IssuePending)
		}
		if folded != string(cr.IssueState(issue.ID)) {
			add("I5", fmt.Sprintf("issue %q accessor state %q differs from log state %q", issue.ID, cr.IssueState(issue.ID), folded))
		}
		if closed && !IssueState(folded).IsTerminal() {
			add("I1", fmt.Sprintf("closed case run has non-terminal issue %q in state %q", issue.ID, folded))
		}
	}

	checkEquivalenceLinks(cr, add)
	checkDuplicateReferences(cr, add)
	if closed {
		primaryCounts := make(map[string]int)
		tpIssues, pdIssues, tpFindings, pdFindings := 0, 0, 0, 0
		for _, finding := range cr.Findings {
			switch cr.FindingState(finding.ID) {
			case FindingTPFinding:
				tpFindings++
			case FindingPDFinding:
				pdFindings++
			}
		}
		for _, issue := range cr.Issues {
			issueID := cr.issueEventID(issue)
			state := cr.IssueState(issue.ID)
			primary := cr.Primary(issue.ID)
			if state == IssueTP {
				tpIssues++
			} else if state == IssuePD {
				pdIssues++
			}
			if primary != "" {
				primaryCounts[issueID]++
				_, exists := cr.finding(primary)
				if !exists {
					add("I5", fmt.Sprintf("issue %q names unknown primary %q", issue.ID, primary))
				} else {
					decision, hasDecision := cr.latestDecision(primary)
					if !hasDecision || decision.CandidateIssue == "" || !sameIssue(cr, decision.CandidateIssue, issue) {
						add("I5", fmt.Sprintf("primary %q is not adjudicated against issue %q", primary, issue.ID))
					}
					if cr.FindingState(primary) == FindingTPFinding {
						if state != IssueTP {
							add("I5", fmt.Sprintf("TP primary %q disagrees with issue %q state %q", primary, issue.ID, state))
						}
					} else if cr.FindingState(primary) == FindingPDFinding {
						if state != IssuePD {
							add("I5", fmt.Sprintf("PD primary %q disagrees with issue %q state %q", primary, issue.ID, state))
						}
					} else {
						add("I5", fmt.Sprintf("issue %q primary %q is not a primary finding", issue.ID, primary))
					}
				}
			} else if state != IssueFN {
				add("I5", fmt.Sprintf("issue %q has state %q but no primary", issue.ID, state))
			}
			if primaryCounts[issueID] > 1 {
				add("I3", fmt.Sprintf("issue %q has more than one primary", issue.ID))
			}
		}
		if tpFindings != tpIssues || pdFindings != pdIssues {
			add("I3", fmt.Sprintf("primary counts TP=%d/%d PD=%d/%d (findings/issues)", tpFindings, tpIssues, pdFindings, pdIssues))
		}
		checkCloseSelection(cr, add)
	}
	checkFalseNegativeTiming(cr, add)
	return violations
}

func checkEquivalenceLinks(cr *CaseRun, add func(string, string)) {
	aliases := make(map[string]string)
	for _, finding := range cr.Findings {
		decision, ok := cr.latestDecision(finding.ID)
		if !ok || decision.EquivalentTo == "" {
			continue
		}
		if _, exists := cr.finding(decision.EquivalentTo); !exists {
			add("I4", fmt.Sprintf("finding %q points to unknown equivalent finding %q", finding.ID, decision.EquivalentTo))
			continue
		}
		if finding.ID == decision.EquivalentTo {
			add("I4", fmt.Sprintf("finding %q is equivalent to itself", finding.ID))
			continue
		}
		aliases[finding.ID] = decision.EquivalentTo
	}
	for findingID, targetID := range aliases {
		if _, chained := aliases[targetID]; chained {
			add("I4", fmt.Sprintf("equivalent_to chain or cycle at finding %q", findingID))
		}
	}
}

func checkDuplicateReferences(cr *CaseRun, add func(string, string)) {
	for _, finding := range cr.Findings {
		if cr.FindingState(finding.ID) != FindingDuplicate {
			continue
		}
		target := cr.latestDuplicateOf(finding.ID)
		if target == "" {
			if decision, ok := cr.latestDecision(finding.ID); ok {
				target = decision.EquivalentTo
			}
		}
		if target == "" {
			add("I4", fmt.Sprintf("duplicate finding %q has no representative", finding.ID))
			continue
		}
		representative, exists := cr.finding(target)
		if !exists {
			add("I4", fmt.Sprintf("duplicate finding %q points outside the case run to %q", finding.ID, target))
			continue
		}
		if representative.ID == finding.ID || cr.FindingState(target) == FindingDuplicate {
			add("I4", fmt.Sprintf("duplicate finding %q points to non-representative %q", finding.ID, target))
		}
	}
}

func checkCloseSelection(cr *CaseRun, add func(string, string)) {
	plan, err := cr.selectionPlan()
	if err != nil {
		add("I4", "equivalence classes are invalid: "+err.Error())
		return
	}
	for _, finding := range cr.Findings {
		want, selected := plan.findingStates[finding.ID]
		if !selected {
			continue
		}
		got := cr.FindingState(finding.ID)
		if got != want {
			add("I5", fmt.Sprintf("finding %q state %q differs from recomputed state %q", finding.ID, got, want))
		}
	}
	for _, issue := range cr.Issues {
		key := cr.issueEventID(issue)
		primary := plan.primaries[key]
		want := IssueFN
		if primary != "" {
			if plan.levels[primary] == LevelFull {
				want = IssueTP
			} else {
				want = IssuePD
			}
		}
		if got := cr.IssueState(issue.ID); got != want {
			add("I5", fmt.Sprintf("issue %q state %q differs from recomputed state %q", issue.ID, got, want))
		}
		if got := cr.Primary(issue.ID); got != primary {
			add("I5", fmt.Sprintf("issue %q primary %q differs from recomputed primary %q", issue.ID, got, primary))
		}
	}
}

func checkFalseNegativeTiming(cr *CaseRun, add func(string, string)) {
	caseState := CaseRunCreated
	for index, event := range cr.Log.Events {
		if event.Entity == EntityCaseRun && event.ID == cr.Case {
			caseState = CaseRunState(event.NewState)
		}
		if event.Entity == EntityIssue && event.NewState == string(IssueFN) && caseState != CaseRunClosed {
			add("I10", fmt.Sprintf("event %d assigns FN before case-run close", index+1))
		}
	}
}

func sameIssue(cr *CaseRun, candidate string, issue Issue) bool {
	resolved, ok := cr.issue(candidate)
	return ok && resolved.ID == issue.ID
}

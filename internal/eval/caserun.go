package eval

import (
	"encoding/json"
	"fmt"
	"strings"
)

// Finding is one extracted row in a case run.
type Finding struct {
	ID                   string                   `json:"id"`
	Row                  int                      `json:"row"`
	Fingerprint          string                   `json:"fingerprint"`
	Location             string                   `json:"location"`
	Description          string                   `json:"description"`
	ReportedSeverityText string                   `json:"reported_severity_text"`
	Invalid              bool                     `json:"invalid"`
	InvalidReason        string                   `json:"invalid_reason,omitempty"`
	Computed             map[string]ComputedFacts `json:"computed"`
}

// ComputedFacts holds the instrument-computed facts for one finding/Issue pair.
type ComputedFacts struct {
	C2      Fact `json:"c2"`
	C4Cited Fact `json:"c4_cited"`
	C5      Fact `json:"c5"`
}

// CaseRun is the ledger for one case execution.
type CaseRun struct {
	Case     string    `json:"case"`
	Issues   []Issue   `json:"issues"`
	Findings []Finding `json:"findings"`
	Log      Log       `json:"log"`
}

// Admit records the parsed findings and Issues before adjudication.
func (cr *CaseRun) Admit(ts string) error {
	if strings.TrimSpace(cr.Case) == "" {
		return fmt.Errorf("case is required")
	}
	if cr.caseRunState() != CaseRunCreated {
		return fmt.Errorf("case run %q is already admitted", cr.Case)
	}
	issueIDs := make(map[string]struct{}, len(cr.Issues))
	for _, issue := range cr.Issues {
		if issue.ID == "" {
			return fmt.Errorf("issue id is empty")
		}
		if issue.Case != "" && issue.Case != cr.Case {
			return fmt.Errorf("issue %q belongs to case %q, not %q", issue.ID, issue.Case, cr.Case)
		}
		if _, exists := issueIDs[issue.ID]; exists {
			return fmt.Errorf("duplicate issue id %q", issue.ID)
		}
		issueIDs[issue.ID] = struct{}{}
	}
	findingIDs := make(map[string]struct{}, len(cr.Findings))
	for _, finding := range cr.Findings {
		if finding.ID == "" {
			return fmt.Errorf("finding id is empty")
		}
		if _, exists := findingIDs[finding.ID]; exists {
			return fmt.Errorf("duplicate finding id %q", finding.ID)
		}
		findingIDs[finding.ID] = struct{}{}
	}

	if _, err := cr.Log.Append(Event{Entity: EntityCaseRun, ID: cr.Case, Kind: EventTransition, PreviousState: string(CaseRunCreated), NewState: string(CaseRunRunning), TS: ts}); err != nil {
		return err
	}
	for _, finding := range cr.Findings {
		if _, err := cr.Log.Append(Event{Entity: EntityFinding, ID: finding.ID, Kind: EventAdmit, PreviousState: string(FindingRaw), NewState: string(FindingPendingAdjudication), TS: ts}); err != nil {
			return err
		}
		if finding.Invalid {
			if _, err := cr.Log.Append(Event{Entity: EntityFinding, ID: finding.ID, Kind: EventAdmit, PreviousState: string(FindingPendingAdjudication), NewState: string(FindingInvalid), Reason: finding.InvalidReason, TS: ts}); err != nil {
				return err
			}
		}
	}
	for _, issue := range cr.Issues {
		if _, err := cr.Log.Append(Event{Entity: EntityIssue, ID: cr.issueEventID(issue), Kind: EventAdmit, PreviousState: string(IssuePending), NewState: string(IssueUnderEvaluation), TS: ts}); err != nil {
			return err
		}
	}
	_, err := cr.Log.Append(Event{Entity: EntityCaseRun, ID: cr.Case, Kind: EventTransition, PreviousState: string(CaseRunRunning), NewState: string(CaseRunScored), TS: ts})
	return err
}

// Decide appends an adjudication and follows its matched or unmatched path.
func (cr *CaseRun) Decide(decision Decision) error {
	if err := decision.Validate(); err != nil {
		return err
	}
	if strings.TrimSpace(decision.FindingID) == "" {
		return fmt.Errorf("finding_id is required")
	}
	finding, ok := cr.finding(decision.FindingID)
	if !ok {
		return fmt.Errorf("unknown finding %q", decision.FindingID)
	}
	if cr.FindingState(finding.ID) != FindingPendingAdjudication {
		return fmt.Errorf("finding %q is not pending adjudication", finding.ID)
	}
	if finding.Invalid {
		return fmt.Errorf("finding %q is invalid", finding.ID)
	}
	if decision.EquivalentTo != "" {
		if decision.EquivalentTo == finding.ID {
			return fmt.Errorf("finding %q cannot be equivalent to itself", finding.ID)
		}
		if _, ok := cr.finding(decision.EquivalentTo); !ok {
			return fmt.Errorf("unknown equivalent finding %q", decision.EquivalentTo)
		}
	}
	var computed ComputedFacts
	if decision.CandidateIssue != "" {
		issue, ok := cr.issue(decision.CandidateIssue)
		if !ok {
			return fmt.Errorf("unknown candidate issue %q", decision.CandidateIssue)
		}
		computed = finding.Computed[issue.ID]
	}
	level := makeFacts(decision, computed).Level()
	if level != LevelNone && decision.UnmatchedOutcome != "" {
		return fmt.Errorf("unmatched outcome is only allowed for a NONE match")
	}
	switch cr.caseRunState() {
	case CaseRunScored:
		if _, err := cr.Log.Append(Event{Entity: EntityCaseRun, ID: cr.Case, Kind: EventTransition, PreviousState: string(CaseRunScored), NewState: string(CaseRunAdjudicating), TS: decision.TS}); err != nil {
			return err
		}
	case CaseRunAdjudicating, CaseRunClosed:
	default:
		return fmt.Errorf("case run %q cannot accept a decision in state %q", cr.Case, cr.caseRunState())
	}
	payload, err := json.Marshal(decision)
	if err != nil {
		return fmt.Errorf("marshal decision: %w", err)
	}
	from := FindingPendingAdjudication
	to := FindingMatched
	if level == LevelNone {
		to = FindingUnmatched
	}
	if _, err := cr.Log.Append(Event{Entity: EntityFinding, ID: finding.ID, Kind: EventDecide, PreviousState: string(from), NewState: string(to), Reason: decision.Reason, TS: decision.TS, Adjudicator: decision.By, Payload: payload}); err != nil {
		return err
	}
	if level != LevelNone {
		return nil
	}
	if _, err := cr.Log.Append(Event{Entity: EntityFinding, ID: finding.ID, Kind: EventTransition, PreviousState: string(FindingUnmatched), NewState: string(FindingNovelCandidate), TS: decision.TS, Payload: payload}); err != nil {
		return err
	}
	terminal := decision.UnmatchedOutcome
	if decision.EquivalentTo != "" {
		terminal = FindingDuplicate
	}
	if terminal == "" {
		return nil
	}
	_, err = cr.Log.Append(Event{Entity: EntityFinding, ID: finding.ID, Kind: EventDecide, PreviousState: string(FindingNovelCandidate), NewState: string(terminal), Reason: decision.Reason, TS: decision.TS, Adjudicator: decision.By, Payload: payload})
	return err
}

// Close derives finding and Issue outcomes for the case run.
func (cr *CaseRun) Close(ts string) error {
	state := cr.caseRunState()
	if state != CaseRunScored && state != CaseRunAdjudicating && state != CaseRunClosed {
		return fmt.Errorf("case run %q cannot close from state %q", cr.Case, state)
	}
	for _, finding := range cr.Findings {
		switch cr.FindingState(finding.ID) {
		case FindingRaw, FindingPendingAdjudication, FindingUnmatched, FindingNovelCandidate:
			return fmt.Errorf("finding %q is unresolved in state %q", finding.ID, cr.FindingState(finding.ID))
		}
	}
	plan, err := cr.selectionPlan()
	if err != nil {
		return err
	}
	if state == CaseRunScored {
		if _, err := cr.Log.Append(Event{Entity: EntityCaseRun, ID: cr.Case, Kind: EventTransition, PreviousState: string(CaseRunScored), NewState: string(CaseRunAdjudicating), TS: ts}); err != nil {
			return err
		}
	}

	for _, finding := range cr.Findings {
		desired, ok := plan.findingStates[finding.ID]
		if !ok {
			continue
		}
		if err := cr.deriveFinding(finding.ID, desired, plan.duplicateOf[finding.ID], ts); err != nil {
			return err
		}
	}
	if cr.caseRunState() == CaseRunAdjudicating {
		if _, err := cr.Log.Append(Event{Entity: EntityCaseRun, ID: cr.Case, Kind: EventTransition, PreviousState: string(CaseRunAdjudicating), NewState: string(CaseRunClosed), TS: ts}); err != nil {
			return err
		}
	}
	for _, issue := range cr.Issues {
		key := cr.issueEventID(issue)
		primary := plan.primaries[key]
		state := IssueFN
		if primary != "" {
			if plan.levels[primary] == LevelFull {
				state = IssueTP
			} else {
				state = IssuePD
			}
		}
		current := cr.IssueState(issue.ID)
		if current == state {
			continue
		}
		payload, err := json.Marshal(struct {
			Primary string `json:"primary"`
		}{Primary: primary})
		if err != nil {
			return err
		}
		if _, err := cr.Log.Append(Event{Entity: EntityIssue, ID: key, Kind: EventDerive, PreviousState: string(current), NewState: string(state), TS: ts, Payload: payload}); err != nil {
			return err
		}
	}
	violations := CheckCaseRun(cr)
	if len(violations) > 0 {
		parts := make([]string, 0, len(violations))
		for _, violation := range violations {
			parts = append(parts, violation.ID+": "+violation.Detail)
		}
		return fmt.Errorf("case run invariants failed: %s", strings.Join(parts, "; "))
	}
	return nil
}

// Reopen reopens a terminal finding and its terminal candidate Issue.
func (cr *CaseRun) Reopen(findingID, reason, by, ts string) error {
	if _, ok := cr.finding(findingID); !ok {
		return fmt.Errorf("unknown finding %q", findingID)
	}
	if !cr.FindingState(findingID).IsTerminal() {
		return fmt.Errorf("finding %q is not terminal", findingID)
	}
	if strings.TrimSpace(reason) == "" || strings.TrimSpace(by) == "" {
		return fmt.Errorf("reopen requires reason and adjudicator")
	}
	decision, hasDecision := cr.latestDecision(findingID)
	var candidate *Issue
	if hasDecision && decision.CandidateIssue != "" {
		candidate, _ = cr.issue(decision.CandidateIssue)
	}
	if _, err := cr.Log.Append(Event{Entity: EntityFinding, ID: findingID, Kind: EventReopen, PreviousState: string(cr.FindingState(findingID)), NewState: string(FindingPendingAdjudication), Reason: reason, TS: ts, Adjudicator: by}); err != nil {
		return err
	}
	if candidate != nil {
		issueState := cr.IssueState(candidate.ID)
		if issueState.IsTerminal() {
			_, err := cr.Log.Append(Event{Entity: EntityIssue, ID: cr.issueEventID(*candidate), Kind: EventReopen, PreviousState: string(issueState), NewState: string(IssueUnderEvaluation), Reason: reason, TS: ts, Adjudicator: by})
			return err
		}
	}
	return nil
}

// FindingState returns the state folded from the log, or RAW before admission.
func (cr *CaseRun) FindingState(id string) FindingState {
	if _, ok := cr.finding(id); !ok {
		return ""
	}
	if state, ok := cr.Log.States()[EntityFinding+":"+id]; ok {
		return FindingState(state)
	}
	return FindingRaw
}

// IssueState returns the state folded from the log, or PENDING before admission.
func (cr *CaseRun) IssueState(id string) IssueState {
	issue, ok := cr.issue(id)
	if !ok {
		return ""
	}
	if state, ok := cr.Log.States()[EntityIssue+":"+cr.issueEventID(*issue)]; ok {
		return IssueState(state)
	}
	return IssuePending
}

// MatchLevel returns the latest recorded match level for a finding.
func (cr *CaseRun) MatchLevel(findingID string) MatchLevel {
	decision, ok := cr.latestDecision(findingID)
	if !ok {
		return LevelNone
	}
	finding, ok := cr.finding(findingID)
	if !ok {
		return LevelNone
	}
	var computed ComputedFacts
	if issue, ok := cr.issue(decision.CandidateIssue); ok {
		computed = finding.Computed[issue.ID]
	}
	return makeFacts(decision, computed).Level()
}

// Primary returns the currently derived primary finding ID for an Issue.
func (cr *CaseRun) Primary(issueID string) string {
	issue, ok := cr.issue(issueID)
	if !ok {
		return ""
	}
	key := cr.issueEventID(*issue)
	for i := len(cr.Log.Events) - 1; i >= 0; i-- {
		event := cr.Log.Events[i]
		if event.Entity != EntityIssue || event.ID != key {
			continue
		}
		if event.Kind != EventDerive {
			return ""
		}
		var payload struct {
			Primary string `json:"primary"`
		}
		if json.Unmarshal(event.Payload, &payload) != nil {
			return ""
		}
		return payload.Primary
	}
	return ""
}

type selection struct {
	findingStates map[string]FindingState
	duplicateOf   map[string]string
	primaries     map[string]string
	levels        map[string]MatchLevel
}

func (cr *CaseRun) selectionPlan() (selection, error) {
	plan := selection{
		findingStates: make(map[string]FindingState),
		duplicateOf:   make(map[string]string),
		primaries:     make(map[string]string),
		levels:        make(map[string]MatchLevel),
	}
	aliases := make(map[string]string)
	for _, finding := range cr.Findings {
		decision, ok := cr.latestDecision(finding.ID)
		if !ok || decision.EquivalentTo == "" {
			continue
		}
		target, exists := cr.finding(decision.EquivalentTo)
		if !exists {
			return selection{}, fmt.Errorf("finding %q points to unknown equivalent finding %q", finding.ID, decision.EquivalentTo)
		}
		if target.ID == finding.ID {
			return selection{}, fmt.Errorf("finding %q cannot be equivalent to itself", finding.ID)
		}
		aliases[finding.ID] = target.ID
	}
	for findingID, targetID := range aliases {
		if _, chained := aliases[targetID]; chained {
			return selection{}, fmt.Errorf("equivalent_to chain or cycle at finding %q", findingID)
		}
	}

	type candidate struct {
		finding Finding
		issue   Issue
		level   MatchLevel
	}
	candidates := make(map[string][]candidate)
	for _, finding := range cr.Findings {
		state := cr.FindingState(finding.ID)
		decision, hasDecision := cr.latestDecision(finding.ID)
		if !hasDecision {
			continue
		}
		if state == FindingDuplicate && decision.EquivalentTo != "" {
			plan.findingStates[finding.ID] = FindingDuplicate
			plan.duplicateOf[finding.ID] = decision.EquivalentTo
			continue
		}
		if state != FindingMatched && state != FindingTPFinding && state != FindingPDFinding && state != FindingDuplicate {
			continue
		}
		if decision.EquivalentTo != "" {
			plan.findingStates[finding.ID] = FindingDuplicate
			plan.duplicateOf[finding.ID] = decision.EquivalentTo
			continue
		}
		issue, exists := cr.issue(decision.CandidateIssue)
		if !exists {
			return selection{}, fmt.Errorf("finding %q has no candidate Issue", finding.ID)
		}
		level := cr.MatchLevel(finding.ID)
		if level == LevelNone {
			return selection{}, fmt.Errorf("finding %q is MATCHED with NONE match level", finding.ID)
		}
		candidates[cr.issueEventID(*issue)] = append(candidates[cr.issueEventID(*issue)], candidate{finding: finding, issue: *issue, level: level})
		plan.levels[finding.ID] = level
	}
	for issueKey, group := range candidates {
		primary := group[0]
		for _, candidate := range group[1:] {
			if candidate.level > primary.level || candidate.level == primary.level && (candidate.finding.Row < primary.finding.Row || candidate.finding.Row == primary.finding.Row && candidate.finding.ID < primary.finding.ID) {
				primary = candidate
			}
		}
		plan.primaries[issueKey] = primary.finding.ID
		for _, candidate := range group {
			if candidate.finding.ID == primary.finding.ID {
				if primary.level == LevelFull {
					plan.findingStates[candidate.finding.ID] = FindingTPFinding
				} else {
					plan.findingStates[candidate.finding.ID] = FindingPDFinding
				}
				continue
			}
			plan.findingStates[candidate.finding.ID] = FindingDuplicate
			plan.duplicateOf[candidate.finding.ID] = primary.finding.ID
		}
	}
	for findingID, targetID := range aliases {
		decision, _ := cr.latestDecision(targetID)
		if issue, ok := cr.issue(decision.CandidateIssue); ok {
			if primary := plan.primaries[cr.issueEventID(*issue)]; primary != "" {
				plan.duplicateOf[findingID] = primary
			}
		}
	}
	return plan, nil
}

func (cr *CaseRun) deriveFinding(id string, desired FindingState, duplicateOf, ts string) error {
	current := cr.FindingState(id)
	if current == desired {
		if desired != FindingDuplicate || cr.latestDuplicateOf(id) == duplicateOf {
			return nil
		}
	}
	if current.IsTerminal() {
		reason, by := cr.reopenContext(id)
		if _, err := cr.Log.Append(Event{Entity: EntityFinding, ID: id, Kind: EventReopen, PreviousState: string(current), NewState: string(FindingPendingAdjudication), Reason: reason, TS: ts, Adjudicator: by}); err != nil {
			return err
		}
		current = FindingPendingAdjudication
	}
	if current == FindingPendingAdjudication && desired != FindingInvalid {
		decision, _ := cr.latestDecision(id)
		payload, _ := json.Marshal(decision)
		if _, err := cr.Log.Append(Event{Entity: EntityFinding, ID: id, Kind: EventTransition, PreviousState: string(FindingPendingAdjudication), NewState: string(FindingMatched), TS: ts, Adjudicator: decision.By, Reason: decision.Reason, Payload: payload}); err != nil {
			return err
		}
		current = FindingMatched
	}
	payload, err := json.Marshal(struct {
		DuplicateOf string `json:"duplicate_of,omitempty"`
	}{DuplicateOf: duplicateOf})
	if err != nil {
		return err
	}
	_, err = cr.Log.Append(Event{Entity: EntityFinding, ID: id, Kind: EventDerive, PreviousState: string(current), NewState: string(desired), TS: ts, Payload: payload})
	return err
}

func (cr *CaseRun) latestDuplicateOf(id string) string {
	for i := len(cr.Log.Events) - 1; i >= 0; i-- {
		event := cr.Log.Events[i]
		if event.Entity != EntityFinding || event.ID != id {
			continue
		}
		var payload struct {
			DuplicateOf string `json:"duplicate_of"`
		}
		if event.Kind == EventDerive && json.Unmarshal(event.Payload, &payload) == nil {
			return payload.DuplicateOf
		}
		if event.Kind == EventDecide {
			decision, ok := cr.latestDecision(id)
			if ok {
				return decision.EquivalentTo
			}
		}
		return ""
	}
	return ""
}

func (cr *CaseRun) reopenContext(id string) (string, string) {
	decision, ok := cr.latestDecision(id)
	if ok && decision.CandidateIssue != "" {
		if issue, exists := cr.issue(decision.CandidateIssue); exists {
			for i := len(cr.Log.Events) - 1; i >= 0; i-- {
				event := cr.Log.Events[i]
				if event.Entity == EntityIssue && event.ID == cr.issueEventID(*issue) && event.Kind == EventReopen {
					return event.Reason, event.Adjudicator
				}
			}
		}
	}
	return "primary selection recomputed", "eval@1"
}

func makeFacts(decision Decision, computed ComputedFacts) Facts {
	return Facts{C1: decision.C1, C2: computed.C2, C3: decision.C3, C4Cited: computed.C4Cited, C4Shows: decision.C4Shows, C5: computed.C5}
}

func (cr *CaseRun) latestDecision(id string) (Decision, bool) {
	for i := len(cr.Log.Events) - 1; i >= 0; i-- {
		event := cr.Log.Events[i]
		if event.Entity != EntityFinding || event.ID != id || event.Kind != EventDecide {
			continue
		}
		var decision Decision
		if json.Unmarshal(event.Payload, &decision) != nil {
			return Decision{}, false
		}
		return decision, true
	}
	return Decision{}, false
}

func (cr *CaseRun) caseRunState() CaseRunState {
	if state, ok := cr.Log.States()[EntityCaseRun+":"+cr.Case]; ok {
		return CaseRunState(state)
	}
	return CaseRunCreated
}

func (cr *CaseRun) finding(id string) (*Finding, bool) {
	for i := range cr.Findings {
		if cr.Findings[i].ID == id {
			return &cr.Findings[i], true
		}
	}
	return nil, false
}

func (cr *CaseRun) issue(id string) (*Issue, bool) {
	for i := range cr.Issues {
		if cr.Issues[i].ID == id || cr.Issues[i].Key() == id {
			return &cr.Issues[i], true
		}
	}
	return nil, false
}

func (cr *CaseRun) issueEventID(issue Issue) string {
	if issue.Case == "" {
		return cr.Case + "/" + issue.ID
	}
	return issue.Key()
}

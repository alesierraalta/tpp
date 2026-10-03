package bench

import (
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	plancheck "github.com/alesierraalta/tsp/internal/plan"
)

const (
	// planReadFailureNotePrefix identifies a selected plan path that exists but could not be read.
	planReadFailureNotePrefix = "plan read failure: "

	// VerdictDefect records that a finding row genuinely reports a keyed defect.
	VerdictDefect = "defect"
	// VerdictFalsePositive records that a finding row claims a defect that is not there.
	VerdictFalsePositive = "false_positive"
	// VerdictOutOfScope records a valid observation outside the key's scope.
	VerdictOutOfScope = "out_of_scope"

	// MetricsVersion identifies the meaning of the score counters. Version 1 recorded an unmatched finding as a
	// false positive; version 2 counts only adjudicated false positives.
	MetricsVersion = 2
)

// Decision is the auditable verdict for one finding row in a plan's Findings table.
type Decision struct {
	Row            int    `json:"row"`
	RowFingerprint string `json:"row_fingerprint"`
	Verdict        string `json:"verdict"`
	Defect         string `json:"defect,omitempty"`
	By             string `json:"by"`
	TS             string `json:"ts"`
	Reason         string `json:"reason"`
}

// Adjudication is the set of human or verified-rule decisions for one scored case run.
type Adjudication struct {
	Case      string     `json:"case"`
	Run       int        `json:"run"`
	Decisions []Decision `json:"decisions"`
	// Superseded keeps the decisions a replacement displaced, so the record still says what was judged
	// before. It is history, not a second set of verdicts: only Decisions is applied to a score.
	Superseded []Decision `json:"superseded,omitempty"`
}

// AdjudicationFile is the record that sits beside a run's kept plan and its result.json.
const AdjudicationFile = "adjudication.json"

// LoadAdjudication reads and validates an adjudication record.
func LoadAdjudication(path string) (Adjudication, error) {
	raw, err := os.ReadFile(path)
	if err != nil {
		return Adjudication{}, fmt.Errorf("read adjudication: %w", err)
	}
	var adj Adjudication
	if err := json.Unmarshal(raw, &adj); err != nil {
		return Adjudication{}, fmt.Errorf("parse adjudication: %w", err)
	}
	if err := adj.Validate(); err != nil {
		return Adjudication{}, fmt.Errorf("invalid adjudication: %w", err)
	}
	return adj, nil
}

// Decide records one decision, or replaces the one already standing for that row and keeps the
// displaced entry. The rules are the loader's, so the writer cannot produce a record the scorer
// would refuse to read back.
func (a *Adjudication) Decide(decision Decision, replace bool) error {
	if problems := validateDecision(1, decision); len(problems) > 0 {
		return errors.New(strings.Join(problems, "; "))
	}
	for i, standing := range a.Decisions {
		if standing.Row != decision.Row {
			continue
		}
		if !replace {
			return fmt.Errorf("row %d already has a decision by %s; replace it deliberately", decision.Row, standing.By)
		}
		a.Superseded = append(a.Superseded, standing)
		a.Decisions[i] = decision
		return nil
	}
	a.Decisions = append(a.Decisions, decision)
	return nil
}

// Validate rejects records that could apply ambiguously or leave an incomplete audit trail.
func (a Adjudication) Validate() error {
	var problems []string
	if strings.TrimSpace(a.Case) == "" {
		problems = append(problems, "case is empty")
	}
	if a.Run < 1 {
		problems = append(problems, fmt.Sprintf("run %d is below 1", a.Run))
	}
	problems = append(problems, validateDecisions(a.Decisions, true)...)
	// Two verdicts for one row are exactly what a replacement leaves behind, so the history is not
	// held to the one-decision-per-row rule the standing set carries.
	problems = append(problems, validateDecisions(a.Superseded, false)...)
	if len(problems) == 0 {
		return nil
	}
	return errors.New(strings.Join(problems, "; "))
}

func validateDecisions(decisions []Decision, uniqueRows bool) []string {
	var problems []string
	seenRows := map[int]bool{}
	for i, decision := range decisions {
		problems = append(problems, validateDecision(i+1, decision)...)
		if uniqueRows && seenRows[decision.Row] {
			problems = append(problems, fmt.Sprintf("decision row %d appears more than once", decision.Row))
		}
		seenRows[decision.Row] = true
	}
	return problems
}

func validateDecision(number int, decision Decision) []string {
	var problems []string
	if decision.Row < 1 {
		problems = append(problems, fmt.Sprintf("decision %d row %d is below 1", number, decision.Row))
	}
	if !validVerdict(decision.Verdict) {
		problems = append(problems, fmt.Sprintf("decision %d verdict %q is unknown", number, decision.Verdict))
	}
	if decision.Verdict != VerdictDefect && strings.TrimSpace(decision.Defect) != "" {
		problems = append(problems, fmt.Sprintf("decision %d sets defect %q for verdict %q", number, decision.Defect, decision.Verdict))
	}
	if decision.Verdict == VerdictDefect && strings.TrimSpace(decision.Defect) == "" {
		problems = append(problems, fmt.Sprintf("decision %d defect is empty for verdict defect", number))
	}
	if strings.TrimSpace(decision.By) == "" {
		problems = append(problems, fmt.Sprintf("decision %d by is empty", number))
	}
	if strings.TrimSpace(decision.Reason) == "" {
		problems = append(problems, fmt.Sprintf("decision %d reason is empty", number))
	}
	if strings.TrimSpace(decision.RowFingerprint) == "" {
		problems = append(problems, fmt.Sprintf("decision %d row_fingerprint is empty", number))
	}
	return problems
}

func validVerdict(verdict string) bool {
	switch verdict {
	case VerdictDefect, VerdictFalsePositive, VerdictOutOfScope:
		return true
	default:
		return false
	}
}

// FindingRowsText returns the Findings table's rows as the scorer joins them: the text a fingerprint
// is taken over, so a reader deciding a row sees exactly what it decides about.
func FindingRowsText(plan string) []string {
	_, rows := plancheck.Table(plan, "Findings")
	out := make([]string, 0, len(rows))
	for _, row := range rows {
		out = append(out, strings.Join(row, " | "))
	}
	return out
}

// FindingRowFingerprint returns the digest of one current Findings-table row so an old decision cannot silently
// attach to changed finding text.
func FindingRowFingerprint(plan string, row int) (string, error) {
	rows := FindingRowsText(plan)
	if row < 1 || row > len(rows) {
		return "", fmt.Errorf("finding row %d does not exist", row)
	}
	digest := sha256.Sum256([]byte(rows[row-1]))
	return fmt.Sprintf("%x", digest), nil
}

// ScoreAdjudicated returns mechanical proposals with a validated adjudication applied.
func ScoreAdjudicated(plan string, key Key, adj *Adjudication) (Result, error) {
	return ApplyAdjudication(plan, key, scoreMechanical(plan, key), adj)
}

// ScorePlanFileWithAdjudication scores a plan file and applies its optional adjudication record.
func ScorePlanFileWithAdjudication(path string, key Key, adj *Adjudication) (Result, error) {
	raw, err := os.ReadFile(path)
	if err != nil {
		// A run that never wrote a plan scores zero and is reported as NO PLAN. That is a result, not a
		// refusal: the run happened, and re-scoring it later must not fail for the deliverable it lacked.
		// Any other read failure is a refusal — a plan that is there and unreadable is not an absent one.
		r := Score("", key)
		r.Notes = append(r.Notes, "no plan")
		if !errors.Is(err, os.ErrNotExist) {
			r.Notes = append(r.Notes, planReadFailureNotePrefix+err.Error())
			return r, fmt.Errorf("read plan: %w", err)
		}
		return r, nil
	}
	r, err := ScoreAdjudicated(string(raw), key, adj)
	r.PlanFound = true
	return r, err
}

// ScoreWorkspaceWithAdjudication scores the plan a workspace delivered — the path it declares, else
// PlanPath — and applies a record to it.
func ScoreWorkspaceWithAdjudication(ws string, key Key, adj *Adjudication) (Result, error) {
	path, resolution := resolvePlanPath(ws)
	if resolution.refused {
		return scoreRefusedPlan(key, resolution), nil
	}
	r, err := ScorePlanFileWithAdjudication(filepath.Join(ws, path), key, adj)
	if resolution.refusalNote != "" {
		r.Notes = append(r.Notes, resolution.refusalNote)
	}
	if r.PlanFound {
		r.PlanPath = path
	}
	return noteMissingDeclaredPlan(ws, r, resolution), err
}

// ApplyAdjudication applies decisions only after their case, row text, and keyed defect references are verified.
func ApplyAdjudication(plan string, key Key, r Result, adj *Adjudication) (Result, error) {
	r.MetricsVersion = MetricsVersion
	if adj == nil {
		return finishAdjudication(r, nil), nil
	}
	if err := adj.Validate(); err != nil {
		return r, err
	}
	if adj.Case != key.ID {
		return r, fmt.Errorf("adjudication case %q does not match key id %q", adj.Case, key.ID)
	}
	if err := validateDecisionFingerprints(plan, adj.Decisions); err != nil {
		return r, err
	}
	if err := validateDecisionDefects(key, adj.Decisions); err != nil {
		return r, err
	}
	r.Run = adj.Run
	return finishAdjudication(r, adj.Decisions), nil
}

func validateDecisionFingerprints(plan string, decisions []Decision) error {
	for _, decision := range decisions {
		fingerprint, err := FindingRowFingerprint(plan, decision.Row)
		if err != nil {
			return fmt.Errorf("decision for row %d: %w", decision.Row, err)
		}
		if fingerprint != decision.RowFingerprint {
			return fmt.Errorf("decision for row %d is stale: row fingerprint does not match the current plan", decision.Row)
		}
	}
	return nil
}

func validateDecisionDefects(key Key, decisions []Decision) error {
	known := make(map[string]bool, len(key.Defects))
	for _, defect := range key.Defects {
		known[defect.ID] = true
	}
	for _, decision := range decisions {
		if decision.Verdict == VerdictDefect && !known[decision.Defect] {
			return fmt.Errorf("decision for row %d names unknown defect id %q", decision.Row, decision.Defect)
		}
	}
	return nil
}

func finishAdjudication(r Result, decisions []Decision) Result {
	r.FalsePositives = 0
	r.AdjudicatedTrue = 0
	r.AdjudicatedFalse = 0
	r.OutOfScope = 0
	r.Precision = nil
	r.PendingAdjudication = r.FindingRows - len(decisions)
	r.AdjudicationComplete = r.PendingAdjudication == 0
	for i := range r.Defects {
		r.Defects[i].Confirmed = false
	}
	for _, decision := range decisions {
		applyDecision(&r, decision)
	}
	// Precision is row-based: one finding row is one claim; unique-defect accounting is a later task.
	denominator := r.AdjudicatedTrue + r.AdjudicatedFalse
	if denominator > 0 {
		precision := float64(r.AdjudicatedTrue) / float64(denominator)
		r.Precision = &precision
	}
	return r
}

func applyDecision(r *Result, decision Decision) {
	switch decision.Verdict {
	case VerdictDefect:
		r.AdjudicatedTrue++
		confirmDefect(r, decision.Defect)
	case VerdictFalsePositive:
		r.AdjudicatedFalse++
		r.FalsePositives++
	case VerdictOutOfScope:
		r.OutOfScope++
	}
}

func confirmDefect(r *Result, id string) {
	for i := range r.Defects {
		if r.Defects[i].ID == id {
			r.Defects[i].Confirmed = true
			return
		}
	}
}

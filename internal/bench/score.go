package bench

import (
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"

	plancheck "github.com/alesierraalta/tpp/internal/plan"
)

// PlanPath is where the skill persists its plan inside a workspace.
const PlanPath = "docs/testing/test-plan.md"

// LineTolerance is how far a cited line may sit from the planted line and still count.
const LineTolerance = 5

// DefectResult says whether one planted defect was reported, and by which rule.
type DefectResult struct {
	ID        string `json:"id"`
	File      string `json:"file"`
	Line      int    `json:"line"`
	Found     bool   `json:"found"`
	Confirmed bool   `json:"confirmed"`
	MatchedBy string `json:"matched_by,omitempty"` // "line" or "keyword"
	// ClaimedPinned reports that the finding names a test pinning it; whether that test
	// distinguishes anything is measured separately, by the catch check.
	ClaimedPinned bool   `json:"claimed_pinned,omitempty"`
	Row           string `json:"row,omitempty"` // the finding row that matched, for audit
}

// TestArtifact is one changed agent test file kept beside the result: its workspace-relative path
// and the sha256 of the bytes persisted under test-artifacts/, so a run's test evidence can be
// verified by content hash and replayed after the workspace is gone.
type TestArtifact struct {
	Path   string `json:"path"`
	SHA256 string `json:"sha256"`
}

// Result is the score of one workspace against its key.
type Result struct {
	Case                 string         `json:"case"`
	Run                  int            `json:"run"`
	Control              bool           `json:"control"`
	Defects              []DefectResult `json:"defects"`
	Total                int            `json:"total"`
	Found                int            `json:"found"`
	Recall               float64        `json:"recall"`
	FalsePositives       int            `json:"false_positives"`
	UnmatchedFindings    int            `json:"unmatched_findings"`
	PendingAdjudication  int            `json:"pending_adjudication"`
	AdjudicatedTrue      int            `json:"adjudicated_true"`
	AdjudicatedFalse     int            `json:"adjudicated_false"`
	OutOfScope           int            `json:"out_of_scope"`
	Precision            *float64       `json:"precision"`
	AdjudicationComplete bool           `json:"adjudication_complete"`
	MetricsVersion       int            `json:"metrics_version"`
	FindingRows          int            `json:"finding_rows"`
	FindingsWithEvidence int            `json:"findings_with_evidence"`
	LedgerRows           int            `json:"ledger_rows"`
	Notes                []string       `json:"notes,omitempty"`
	CostUSD              float64        `json:"cost_usd"`
	CostKnown            bool           `json:"cost_known"`
	Tokens               *int           `json:"tokens,omitempty"`
	BudgetExhausted      bool           `json:"budget_exhausted,omitempty"`
	Outcome              string         `json:"outcome,omitempty"`
	Turns                int            `json:"turns"`
	Seconds              float64        `json:"seconds"`
	Invalid              bool           `json:"invalid"`
	InvalidReason        string         `json:"invalid_reason,omitempty"`
	Failed               bool           `json:"failed"`
	FailReason           string         `json:"fail_reason,omitempty"`
	Suite                SuiteResult    `json:"suite"`
	Workspace            string         `json:"workspace,omitempty"`
	PlanFound            bool           `json:"plan_found"`
	PlanPath             string         `json:"plan_path,omitempty"` // workspace-relative path read, set only when a plan was found
	PlanFormat           string         `json:"plan_format"`         // FormatTable, FormatProse or FormatEmpty
	// LightActivated records that the run declared a scoped run its own plan validates. It is the only
	// durable answer to "did the mode run?": a defect found, a well-formed ordinary plan, or a line that
	// merely looks like a declaration leaves it false.
	LightActivated bool `json:"light_activated"`
	// MicroActivated records that the run's plan is an activated micro plan: declared, and accepted by
	// plan check as a whole. It is counted apart from LightActivated; plan check refuses a plan that is both.
	MicroActivated  bool        `json:"micro_activated"`
	RowsWithoutPath int         `json:"rows_without_path"` // finding rows that name no file, so nothing can be located
	ClaimedPinned   int         `json:"claimed_pinned"`    // defects whose finding names a pinning test
	Caught          int         `json:"caught"`            // defects some agent test distinguishes (fixture vs fix)
	Catch           CatchResult `json:"catch"`
	// TestArtifacts is the persisted copy of the changed test files the catch check listed, as
	// relative path plus sha256. A manifest run records it even when empty — no agent test file is
	// an answer; a legacy run persists no snapshot and leaves it null.
	TestArtifacts []TestArtifact `json:"test_artifacts"`
}

var (
	// A cell that means "no rows yet" rather than data.
	placeholderRe = regexp.MustCompile(`^(?i)(|-|—|n/?a|none|\(none\)|tbd)$`)
	// A path with an extension, optionally followed by :line or :first-last.
	citationRe = regexp.MustCompile(`([A-Za-z0-9_./\\-]+\.[A-Za-z][A-Za-z0-9]*)(?::(\d+)(?:-(\d+))?)?`)
)

type citation struct {
	path  string
	first int
	last  int
}

type planPathResolution struct {
	refusalNote  string
	refused      bool
	declared     bool
	declaredPath string
}

// resolvePlanPath returns the workspace-relative plan a run delivered: the path its .tpp.json
// declares, else PlanPath. DeclaredPath only ever returns a validated repository-relative path: an
// absolute or escaping declaration is refused there, not returned, and the fallback to PlanPath carries a
// note naming it. Whatever path will be read then has to resolve inside the workspace; a path that cannot
// be checked, cannot be followed, or resolves outside is refused before it is read.
func resolvePlanPath(ws string) (string, planPathResolution) {
	declared, err := plancheck.DeclaredPath(ws, nil)

	path := PlanPath
	resolution := planPathResolution{}
	switch {
	case err != nil:
		// The declaration is refused lexically and the default path is read instead — and that fallback
		// is guarded like any other selection, because a rejected declaration must not become a way to
		// read an unchecked path.
		resolution.refusalNote = fmt.Sprintf("declared plan path refused (%v); read %s instead", err, PlanPath)
	case declared != "":
		path = declared
		resolution.declared = true
		resolution.declaredPath = declared
	}

	root, rootErr := filepath.EvalSymlinks(ws)
	target, targetErr := filepath.EvalSymlinks(filepath.Join(ws, path))
	if rootErr != nil {
		// Containment cannot be proven without the real workspace, and a path that cannot be checked is
		// not a path that is inside. Returning here would read the plan unchecked.
		resolution.refused = true
		resolution.refusalNote = joinNotes(resolution.refusalNote, fmt.Sprintf("workspace %q could not be resolved (%v); the plan was not read", ws, rootErr))
		return path, resolution
	}
	if targetErr != nil {
		// Absent is the ordinary flow. Anything else — a symlink loop, a link that cannot be
		// followed — is a path that cannot be read as a plan, and saying it is missing would be
		// the same false claim this bench has been removing.
		if os.IsNotExist(targetErr) {
			return path, resolution
		}
		resolution.refused = true
		resolution.refusalNote = joinNotes(resolution.refusalNote, fmt.Sprintf("selected plan path %q refused: %v", path, targetErr))
		return path, resolution
	}
	rel, err := filepath.Rel(root, target)
	if err != nil || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
		resolution.refused = true
		resolution.refusalNote = joinNotes(resolution.refusalNote, fmt.Sprintf("selected plan path %q refused: resolves outside workspace to %q", path, target))
	}
	return path, resolution
}

// joinNotes keeps an earlier refusal and the one that decided the outcome in a single note, so a rejected
// declaration that also escapes does not lose either fact.
func joinNotes(first, second string) string {
	if first == "" {
		return second
	}
	return first + "; " + second
}

// noteMissingDeclaredPlan names the plan a declaration overrode when the declared path yielded no plan.
// It distinguishes a path that is not there from one that is there and was not read as a plan, because a
// note that says "was not found" about an existing file is a false statement in the run's record.
func noteMissingDeclaredPlan(ws string, r Result, resolution planPathResolution) Result {
	if r.PlanFound || !resolution.declared {
		return r
	}
	for _, note := range r.Notes {
		if strings.HasPrefix(note, planReadFailureNotePrefix) {
			return r
		}
	}
	declared := resolution.declaredPath
	note := ""
	switch {
	case pathExists(filepath.Join(ws, declared)):
		note = fmt.Sprintf("declared plan %q exists but was not read as a plan", declared)
	case pathExists(filepath.Join(ws, PlanPath)):
		note = fmt.Sprintf("declared plan %q was not found; default plan %q was not read", declared, PlanPath)
	default:
		note = fmt.Sprintf("declared plan %q was not found; default plan %q holds nothing either", declared, PlanPath)
	}
	r.Notes = append(r.Notes, note)
	return r
}

// pathExists reports whether the path is there, whatever it is: absent and unreadable are different
// answers, and only the caller knows which one the note should carry.
func pathExists(path string) bool {
	_, err := os.Stat(path)
	return err == nil
}

// scoreRefusedPlan returns the zero score for a selected plan path that must not be read.
func scoreRefusedPlan(key Key, resolution planPathResolution) Result {
	r := Score("", key)
	r.Notes = append(r.Notes, resolution.refusalNote)
	return r
}

// ScoreWorkspace scores the plan a workspace delivered: the path it declares, else PlanPath. A
// missing plan scores zero.
func ScoreWorkspace(ws string, key Key) Result {
	path, resolution := resolvePlanPath(ws)
	if resolution.refused {
		return scoreRefusedPlan(key, resolution)
	}
	r := ScorePlanFile(filepath.Join(ws, path), key)
	if resolution.refusalNote != "" {
		r.Notes = append(r.Notes, resolution.refusalNote)
	}
	if r.PlanFound {
		r.PlanPath = path
	}
	return noteMissingDeclaredPlan(ws, r, resolution)
}

// ScorePlanFile scores one plan file, such as the copy a run keeps next to its result.json.
func ScorePlanFile(path string, key Key) Result {
	r, _ := ScorePlanFileWithAdjudication(path, key, nil)
	return r
}

// Score applies the mechanical proposal rules and leaves every finding row pending adjudication.
func Score(plan string, key Key) Result {
	r, _ := ScoreAdjudicated(plan, key, nil)
	return r
}

// scoreMechanical applies the lexical and location rules without deciding whether a proposal is true.
func scoreMechanical(plan string, key Key) Result {
	r := Result{Case: key.ID, Control: key.IsCleanControl(), Total: len(key.Defects), LightActivated: plancheck.LightActivated(plan), MicroActivated: plancheck.MicroActivated(plan), MetricsVersion: MetricsVersion}
	findings := plancheck.Section(plan, "Findings")
	findingsHeader, rows := plancheck.Table(plan, "Findings")
	r.FindingRows = len(rows)
	// The pinning column exists only in plans written under rule 13; find it by its header so
	// its position can move.
	pinCol := columnIndex(findingsHeader, "pinning test")
	r.PlanFormat = planFormat(findings, rows)
	if r.PlanFormat == FormatProse {
		r.Notes = append(r.Notes, "findings are not in the template table; nothing in this plan can be located or re-scored")
	}
	for _, row := range rows {
		if len(row) > 4 && !placeholderRe.MatchString(strings.TrimSpace(row[4])) {
			r.FindingsWithEvidence++
		}
	}
	_, ledgerRows := plancheck.Table(plan, "Evidence ledger")
	r.LedgerRows = len(ledgerRows)
	ledgerByID := map[string]string{}
	for _, lr := range ledgerRows {
		if len(lr) > 0 {
			ledgerByID[strings.Trim(strings.TrimSpace(lr[0]), "`")] = strings.Join(lr, " | ")
		}
	}

	// Attribution is per row, not per defect: one finding row is one claim.
	// A clean control has no keyed defect to propose against, so every finding row is unmatched and stays pending
	// until a decision makes it a false positive or out of scope.
	claims := attribution(rows, key, ledgerByID)
	r.RowsWithoutPath = claims.withoutPath
	byID := map[string]*DefectResult{}
	for i := range key.Defects {
		d := key.Defects[i]
		byID[d.ID] = &DefectResult{ID: d.ID, File: d.File, Line: d.Line}
	}
	credit(rows, claims, key, byID, pinCol)
	for i := range key.Defects {
		dr := *byID[key.Defects[i].ID]
		if dr.Found {
			r.Found++
			if dr.ClaimedPinned {
				r.ClaimedPinned++
			}
		}
		r.Defects = append(r.Defects, dr)
	}
	for _, m := range claims.matched {
		if !m {
			r.UnmatchedFindings++
		}
	}
	if r.RowsWithoutPath > 0 {
		r.Notes = append(r.Notes, fmt.Sprintf("%d finding row(s) cite no file, in the plan or in the evidence they link", r.RowsWithoutPath))
	}
	if r.Total > 0 {
		r.Recall = float64(r.Found) / float64(r.Total)
	}
	if plan == "" {
		r.Recall = 0
	}
	return r
}

// Attribution is per row, not per defect: one finding row is one claim. A row that names a
// defect by keyword is credited to that defect (to several only if it names several); a row
// that matches by line alone goes to the nearest defect, so two defects a few lines apart
// cannot both be credited to a run that noticed one of them.
//
// rowClaims is what the Findings table claims: for each row, the ledger text it links and the defect ids it is
// credited to, plus the rows that name no file at all. It exists so the two passes below read a value instead of
// passing three parallel slices through Score's own loop.
type rowClaims struct {
	linked      []string
	credited    []map[string]bool
	matched     []bool
	withoutPath int
}

// claimed reads one finding row against every defect and reports the ids it is credited to, plus whether it
// has any lexical or location proposal. A proposal is deliberately not a verdict: adjudication decides whether
// the row is a true finding, a false positive, or out of scope. A defect named by keyword is credited directly;
// a row that matches only by line goes to the nearest defect.
func claimed(text, linked string, defects []Defect) (map[string]bool, bool) {
	ids := map[string]bool{}
	var specific []Defect
	var nearest *Defect
	nearestDist := 0
	matched := false
	for j := range defects {
		m := matchDefect(text, linked, defects[j])
		if m.kind == "" {
			continue
		}
		matched = true
		if m.keyword {
			specific = append(specific, defects[j])
		} else if nearest == nil || m.dist < nearestDist {
			nearest, nearestDist = &defects[j], m.dist
		}
	}
	if len(specific) == 0 && nearest != nil {
		specific = append(specific, *nearest)
	}
	for _, d := range specific {
		ids[d.ID] = true
	}
	return ids, matched
}

// attribution reads every finding row of the table through `claimed`.
func attribution(rows [][]string, key Key, ledgerByID map[string]string) rowClaims {
	claims := rowClaims{
		linked:   make([]string, len(rows)),
		credited: make([]map[string]bool, len(rows)),
		matched:  make([]bool, len(rows)),
	}
	for i, row := range rows {
		text := strings.Join(row, " | ")
		// The evidence rows a finding cites are part of its claim: the file is often named there while the
		// finding itself names the symbol.
		claims.linked[i] = strings.Join(linkedLedgerText(row, ledgerByID), " | ")
		if len(citations(text)) == 0 && len(citations(claims.linked[i])) == 0 {
			claims.withoutPath++
		}
		claims.credited[i], claims.matched[i] = claimed(text, claims.linked[i], key.Defects)
	}
	return claims
}

// credit records each defect once, from the rows that claim it: the first row of the table that credits a defect
// is the one kept. A row that names a defect but links no evidence is prose rather than a catch, so it is marked
// unlinked and does not count as found.
func credit(rows [][]string, claims rowClaims, key Key, byID map[string]*DefectResult, pinCol int) {
	for i, row := range rows {
		text := strings.Join(row, " | ")
		for id := range claims.credited[i] {
			dr := byID[id]
			m := matchDefect(text, claims.linked[i], defectByID(key, id))
			if claims.linked[i] == "" {
				if !dr.Found && dr.MatchedBy == "" {
					dr.MatchedBy, dr.Row = "unlinked:"+m.kind, text
				}
				continue
			}
			if !dr.Found {
				dr.Found, dr.MatchedBy, dr.Row = true, m.kind, text
				dr.ClaimedPinned = cellFilled(row, pinCol)
			}
		}
	}
}

// defectMatch is how one finding row matches one defect.
type defectMatch struct {
	kind    string // "line", "keyword", or "" for no match
	dist    int    // lines between the cited location and the planted one
	keyword bool   // the row names the defect, not just its neighbourhood
}

// matchDefect reports how a finding row matches a defect. The evidence rows it cites may supply
// the file and line; only the row itself may supply a keyword, so a shared ledger row cannot
// hand one defect's name to another.
func matchDefect(rowText, evidenceText string, d Defect) defectMatch {
	cites := append(citations(rowText), citations(evidenceText)...)
	fileCited, lineHit, best := false, false, 0
	for _, c := range cites {
		if !samePath(c.path, d.File) {
			continue
		}
		fileCited = true
		if c.first <= 0 || d.Line < c.first-LineTolerance || d.Line > c.last+LineTolerance {
			continue
		}
		dist := 0
		if d.Line < c.first {
			dist = c.first - d.Line
		} else if d.Line > c.last {
			dist = d.Line - c.last
		}
		if !lineHit || dist < best {
			lineHit, best = true, dist
		}
	}
	if !fileCited {
		return defectMatch{}
	}
	lower := strings.ToLower(rowText)
	named := false
	for _, kw := range d.Keywords {
		if kw != "" && strings.Contains(lower, strings.ToLower(kw)) {
			named = true
			break
		}
	}
	switch {
	case lineHit:
		return defectMatch{kind: "line", dist: best, keyword: named}
	case named:
		return defectMatch{kind: "keyword", keyword: true}
	}
	return defectMatch{}
}

// defectByID returns the key's defect with that id.
func defectByID(key Key, id string) Defect {
	for _, d := range key.Defects {
		if d.ID == id {
			return d
		}
	}
	return Defect{}
}

// samePath matches by suffix: "render.js" or "fixture/src/render.js" both name "src/render.js".
func samePath(cited, key string) bool {
	c := strings.TrimPrefix(filepath.ToSlash(cited), "./")
	k := strings.TrimPrefix(filepath.ToSlash(key), "./")
	if c == k {
		return true
	}
	return strings.HasSuffix(c, "/"+k) || strings.HasSuffix(k, "/"+c)
}

func citations(text string) []citation {
	var out []citation
	for _, m := range citationRe.FindAllStringSubmatch(text, -1) {
		c := citation{path: m[1]}
		if m[2] != "" {
			c.first, _ = strconv.Atoi(m[2])
			c.last = c.first
			if m[3] != "" {
				c.last, _ = strconv.Atoi(m[3])
			}
		}
		out = append(out, c)
	}
	return out
}

// citedEvidenceIDs reads the ids in a finding row's evidence cell ("E1, E2", "E1/E3").
func citedEvidenceIDs(row []string) []string {
	if len(row) <= 4 {
		return nil
	}
	cell := strings.TrimSpace(row[4])
	if cell == "" || placeholderRe.MatchString(cell) {
		return nil
	}
	var ids []string
	for _, part := range strings.FieldsFunc(cell, func(c rune) bool { return c == ',' || c == ';' || c == '/' || c == ' ' }) {
		ids = append(ids, strings.Trim(part, "`"))
	}
	return ids
}

// linkedLedgerText returns the text of the ledger rows a finding row cites and that exist.
func linkedLedgerText(row []string, ledgerByID map[string]string) []string {
	var out []string
	for _, id := range citedEvidenceIDs(row) {
		if text, ok := ledgerByID[id]; ok {
			out = append(out, text)
		}
	}
	return out
}

// columnIndex finds the column whose header contains name, case-insensitively.
func columnIndex(header []string, name string) int {
	for i, h := range header {
		if strings.Contains(strings.ToLower(strings.TrimSpace(h)), strings.ToLower(name)) {
			return i
		}
	}
	return -1
}

// cellFilled reports whether a row carries real content in the given column.
func cellFilled(row []string, col int) bool {
	if col < 0 || col >= len(row) {
		return false
	}
	return !placeholderRe.MatchString(strings.TrimSpace(row[col]))
}

// How a plan states its findings. Prose scores zero exactly like an empty plan, so the run has
// to say which of the two happened.
const (
	FormatTable = "table"
	FormatProse = "prose"
	FormatEmpty = "empty"
)

// planFormat classifies the Findings section: template rows, prose, or nothing yet.
func planFormat(section string, rows [][]string) string {
	if len(rows) > 0 {
		return FormatTable
	}
	for _, l := range strings.Split(section, "\n") {
		t := strings.TrimSpace(l)
		if strings.HasPrefix(t, "###") || strings.HasPrefix(t, "- ") || strings.HasPrefix(t, "* ") {
			return FormatProse
		}
	}
	return FormatEmpty
}

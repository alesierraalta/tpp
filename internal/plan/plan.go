// Package plan writes and checks docs/testing/test-plan.md against the contract the
// test-strategy skill persists. Format compliance is deterministic work: a binary does it once
// instead of every session re-deriving the structure from prose.
package plan

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"

	"github.com/alesierraalta/tpp/internal/assets"
)

// TemplatePath is where the skeleton lives inside the embedded skills.
const TemplatePath = "test-strategy/assets/test-plan-template.md"

// DefaultPath is where the skill persists the plan.
const DefaultPath = "docs/testing/test-plan.md"

// Template returns the shipped plan skeleton.
func Template() (string, error) {
	return embeddedTemplate(TemplatePath)
}

func embeddedTemplate(path string) (string, error) {
	data, err := fs.ReadFile(assets.Skills(), path)
	if err != nil {
		return "", fmt.Errorf("read embedded template: %w", err)
	}
	return string(data), nil
}

// MicroTemplatePath is the skeleton `plan init --micro` writes: a header, Findings and the Evidence
// ledger, and nothing a single small function does not owe.
const MicroTemplatePath = "test-strategy/assets/test-micro-plan-template.md"

// Init writes the skeleton to path. An existing plan is never overwritten unless force is set:
// the plan never shrinks, so replacing one is a decision, not a default.
func Init(path string, force bool) error {
	return initFrom(path, TemplatePath, force)
}

// InitMicro writes the micro skeleton to path, refusing an existing plan exactly as Init does.
func InitMicro(path string, force bool) error {
	return initFrom(path, MicroTemplatePath, force)
}

func initFrom(path, template string, force bool) error {
	if _, err := os.Stat(path); err == nil && !force {
		return fmt.Errorf("%s already exists; pass --force to replace it", path)
	}
	body, err := embeddedTemplate(template)
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	return os.WriteFile(path, []byte(body), 0o644)
}

var (
	// A path:line cites a file with an extension, a dotfile (.gitignore:1), or a conventional
	// extensionless build file (Makefile:3). A bare word:digits stays out, so localhost:8080 is no cite.
	pathCiteRe  = regexp.MustCompile(`(?:[A-Za-z0-9_./\\-]*\.[A-Za-z][A-Za-z0-9]*|(?:[A-Za-z0-9_./\\-]*/)?\b(?i:Makefile|GNUmakefile|Dockerfile|Containerfile|Jenkinsfile|Justfile|Procfile|Gemfile|Rakefile|Vagrantfile|Brewfile|Tiltfile|Caddyfile)):\d+`)
	placeholder = regexp.MustCompile(`^(?i)(|-|—|n/?a|none|\(none\)|tbd)$`)
	// A finding whose verdict settles it (a real defect, or a missing test now added) owes the test that holds it.
	settledStatus = regexp.MustCompile(`(?i)\b(confirmed|fixed|gap-closed)\b`)
	// A scoped run declares itself in one plan-header line, `Light: <blast radius> · touches
	// <classes>`. The declaration is the cheap half of the decision: a binary can read its shape and
	// whether the plan corroborates the target it names, never whether the change was really bounded.
	// The whitespace class stays inside the line: a declaration is one header line, and a match that can
	// start on the previous line would name the wrong line as its home.
	lightLineRe  = regexp.MustCompile(`^[ \t]*Light:[ \t]*(.*)$`)
	lightShapeRe = regexp.MustCompile(`^(.+?)[ \t]*·[ \t]*touches[ \t]*(.*)$`)
	// A micro plan declares itself the same way, `Micro: <file path> · touches none`, for one small function
	// whose contract does not change. It reuses the Light shape so both declarations read alike.
	microLineRe = regexp.MustCompile(`^[ \t]*Micro:[ \t]*(.*)$`)

	baselineFingerprintRe = regexp.MustCompile("^[ \t]*Baseline:.*fingerprint:[ \t]*`([^`]*)`")
	fingerprintRe         = regexp.MustCompile(`^[0-9a-f]{64}$`)
	// A Findings fingerprint cell names what the verdict can be re-checked against: a `fingerprint.sh` digest,
	// or one or more git SHAs separated by commas and spaces. A cell starting with `pending` says the value is
	// owed, the way the placeholder vocabulary says it is absent. `{{FP_PARSE}}` is the one token the eval harness
	// substitutes into a fixture plan's Findings, as `{{FINGERPRINT}}` is for the Baseline; any other token is a
	// value nobody filled in.
	findingFingerprintRe = regexp.MustCompile(`^(?:[0-9a-f]{64}|[0-9a-f]{7,40}(?:[ ,]+[0-9a-f]{7,40})*|pending\b.*|\{\{FP_PARSE\}\})$`)
)

// unrecordedFingerprints are the Baseline values that visibly say "not recorded yet": the shipped
// template's placeholder and the token the eval harness substitutes when it scaffolds a fixture plan.
var unrecordedFingerprints = map[string]bool{"<assets/fingerprint.sh output>": true, "{{FINGERPRINT}}": true}

// findingsStatuses is the Findings status vocabulary the plan documents. It is the membership the
// checker enforces; FindingsStatusList spells the same list for every message that has to offer it.
var findingsStatuses = map[string]bool{
	"open": true, "confirmed": true, "fixed": true, "gap-closed": true, "rejected": true, "wontfix": true,
}

// FindingsStatusList is the closed Findings vocabulary in one string, so the list a reader is shown is
// the list the checker enforces: a status the vocabulary does not carry (`resolved`) is answered rather
// than silently read as `open`.
const FindingsStatusList = "open, confirmed, fixed, gap-closed, rejected, wontfix"

// Check reads a plan and returns everything that breaks the contract, most structural first.
// Every breach that names a row or a cell carries its file line, so the reader opens the plan at the row
// instead of grepping for the text the message quotes.
// An empty result means the plan is well formed, not that the testing was good.
func Check(path string) ([]string, error) {
	raw, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	return CheckDocument(string(raw)), nil
}

// CheckDocument is Check on the document itself, so the rules can be read against a document that has
// never been written to disk. The rules live in one place because a second copy would drift.
func CheckDocument(doc string) []string {
	lines := strings.Split(doc, "\n")

	// Every table is measured against its own header before anything is said about its rows: a row that
	// lost a cell moves every column to its right, so the reader refuses the row by count rather than read
	// a machine column out of the cell beside it.
	problems := tableProblems(doc)

	heading, end := sectionRegion(lines, "Findings")
	if heading < 0 {
		// Nothing to point at: a section that does not exist never gets an invented location.
		return append(problems, "no Findings section")
	}
	findings := scanTable(lines, heading+1, end)
	if findings.header == nil {
		prose := strings.Join(lines[heading+1:end], "\n")
		if strings.Contains(prose, "###") || strings.Contains(prose, "- ") {
			return append(problems, fmt.Sprintf("line %d: the Findings section is not a table: prose cannot be located, honoured, or re-scored", heading+1))
		}
		return append(problems, fmt.Sprintf("line %d: the Findings section has no table", heading+1))
	}
	// The ledger is the one other table the check reads back. A plan without one simply has no rows to
	// corroborate against, exactly as before: sectionRegion reports it missing and the scan returns empty.
	ledger := scanSection(lines, "Evidence ledger")
	ranked := scanSection(lines, "Ranked targets")
	layers := scanSection(lines, "Layer matrix")

	// A table a stray line cut in two is reported before the rows it kept: the rows under the cut were never
	// read, so everything the check says below is said about a part of the table.
	for _, t := range []struct {
		name string
		scan tableScan
	}{
		{"Findings", findings}, {"Evidence ledger", ledger}, {"Ranked targets", ranked}, {"Layer matrix", layers},
	} {
		if line, message := interruptedTable(t.name, t.scan); message != "" {
			problems = append(problems, fmt.Sprintf("line %d: %s", line, message))
		}
	}
	for _, t := range []struct {
		name string
		scan tableScan
	}{
		{"Ranked targets", ranked}, {"Layer matrix", layers},
	} {
		problems = append(problems, runColumnProblems(t.name, t.scan)...)
	}
	ledgerIDs, ledgerIssues := ledgerProblems(ledger)
	problems = append(problems, ledgerIssues...)
	problems = append(problems, findingProblems(findings, ledgerIDs)...)
	if lightProblems, declared := lightReport(lines); declared {
		problems = append(problems, lightProblems...)
	}
	if microProblems, _, declared := microReport(lines); declared {
		problems = append(problems, microProblems...)
	}
	return append(problems, baselineProblems(lines)...)
}

// baselineProblems reads the fingerprint a `Baseline:` line records. Every resume compares the tree
// against it, so a value fingerprint.sh could not have written (`pending`, a short hash) is refused
// rather than read as a baseline. An unrecorded placeholder stays legal: it says what it is, and a plan
// fresh from `plan init` has to pass.
func baselineProblems(lines []string) []string {
	var problems []string
	for i, line := range lines {
		m := baselineFingerprintRe.FindStringSubmatch(line)
		if m == nil || unrecordedFingerprints[m[1]] || fingerprintRe.MatchString(m[1]) {
			continue
		}
		problems = append(problems, fmt.Sprintf("line %d: the Baseline fingerprint \"%s\" is not assets/fingerprint.sh output (64 lowercase hex): record the baseline or keep the unrecorded placeholder", i+1, quote(m[1])))
	}
	return problems
}

// ledgerProblems reads the ledger back: the ids a finding may cite, the id that names two rows, and the
// hypothesis that belongs under Hypotheses. The ids come back with the problems because the findings pass
// resolves citations against them.
//
// The id is what a finding cites, so the line that first carried one is kept: a repeat is reported against that
// line, because a citation naming the id would resolve to one of two rows and nothing would say which.
func ledgerProblems(ledger tableScan) (map[string]bool, []string) {
	ids := map[string]bool{}
	idLine := map[string]int{}
	var problems []string
	for _, r := range ledger.rows {
		id := cell(r.cells, 0)
		if first, seen := idLine[id]; seen {
			problems = append(problems, fmt.Sprintf("line %d: evidence %s repeats the id of the row on line %d, so a citation naming it points at two rows", r.line, quote(id), first))
		} else {
			idLine[id] = r.line
		}
		ids[id] = true
		if label := cell(r.cells, len(r.cells)-1); strings.EqualFold(label, "razonado") {
			problems = append(problems, fmt.Sprintf("line %d: evidence %s is labelled razonado: a hypothesis belongs under Hypotheses, never in the ledger", r.line, quote(id)))
		}
	}
	return ids, problems
}

// findingColumns are the machine columns a finding row is read by, resolved by name so a column that
// moves does not move the reading with it.
type findingColumns struct {
	find        int
	evidence    int
	pin         int
	status      int
	fingerprint int
}

func columnsOf(header []string) findingColumns {
	return findingColumns{
		find:        columnIndex(header, "finding"),
		evidence:    columnIndex(header, "evidence"),
		pin:         columnIndex(header, "pinning test"),
		status:      columnIndex(header, "status"),
		fingerprint: columnIndex(header, "fingerprint"),
	}
}

// findingProblems reads every finding row, and reports a repeated id against the line that first carried it.
func findingProblems(findings tableScan, ledgerIDs map[string]bool) []string {
	cols := columnsOf(findings.header)
	idLine := map[string]int{}
	var problems []string
	for _, r := range findings.rows {
		id := quote(cell(r.cells, 0))
		if first, seen := idLine[cell(r.cells, 0)]; seen {
			problems = append(problems, fmt.Sprintf("line %d: finding %s repeats the id of the row on line %d, so the row a verdict or a pinning test belongs to is ambiguous", r.line, id, first))
		} else {
			idLine[cell(r.cells, 0)] = r.line
		}
		problems = append(problems, findingRowProblems(r, id, cols, ledgerIDs)...)
	}
	return problems
}

// findingRowProblems reads one finding row against the five rules a row owes: the path:line that makes it
// locatable, the evidence that has to be a ledger row, the closed status vocabulary, the pinning test a
// settled verdict names, and the fingerprint cell that names what the verdict can be re-checked against.
func findingRowProblems(r row, id string, cols findingColumns, ledgerIDs map[string]bool) []string {
	var problems []string
	if cols.find >= 0 && !pathCiteRe.MatchString(cell(r.cells, cols.find)) {
		problems = append(problems, fmt.Sprintf("line %d: finding %s cites no path:line, so nothing can be located", r.line, id))
	}
	problems = append(problems, evidenceProblems(r, id, cols.evidence, ledgerIDs)...)
	status, breach := findingStatus(cell(r.cells, cols.status))
	if breach != "" {
		problems = append(problems, fmt.Sprintf("line %d: finding %s %s", r.line, id, breach))
	}
	if settledStatus.MatchString(status) && (cols.pin < 0 || placeholder.MatchString(cell(r.cells, cols.pin))) {
		problems = append(problems, fmt.Sprintf("line %d: finding %s is settled but names no pinning test", r.line, id))
	}
	if cols.fingerprint >= 0 {
		if raw := cell(r.cells, cols.fingerprint); !ValidFindingFingerprint(raw) {
			problems = append(problems, fmt.Sprintf("line %d: finding %s fingerprint %s is not %s", r.line, id, quote(raw), FindingFingerprintForms))
		}
	}
	return problems
}

// FindingFingerprintForms names what a Findings fingerprint cell accepts, for a breach to quote.
const FindingFingerprintForms = "a fingerprint.sh digest (64 lowercase hex), git SHAs (7-40 lowercase hex, separated by commas or spaces), a placeholder such as -, or pending"

// ValidFindingFingerprint reports whether one Findings fingerprint cell, backticks stripped, is a value the
// verdict can be re-checked against or a placeholder saying none is recorded yet. A plan whose Findings table
// has no fingerprint column is never asked, so older plans keep passing.
func ValidFindingFingerprint(raw string) bool {
	v := strings.TrimSpace(strings.ReplaceAll(raw, "`", ""))
	return placeholder.MatchString(v) || findingFingerprintRe.MatchString(v)
}

// evidenceProblems reads one finding's evidence cell against the ledger: a row that cites none is refused, and
// every id it names has to be a row of the ledger. The cell holds a list (`E1, E2`, `E1/E3`), so the rule is
// applied to each part rather than to the cell.
func evidenceProblems(r row, id string, col int, ledgerIDs map[string]bool) []string {
	ev := cell(r.cells, col)
	if col < 0 || placeholder.MatchString(ev) {
		return []string{fmt.Sprintf("line %d: finding %s cites no evidence row", r.line, id)}
	}
	var problems []string
	for _, part := range strings.FieldsFunc(ev, func(r rune) bool { return r == ',' || r == ';' || r == '/' || r == ' ' }) {
		if part = strings.Trim(part, "`"); part != "" && !ledgerIDs[part] {
			problems = append(problems, fmt.Sprintf("line %d: finding %s cites evidence %s, which is not a row in the Evidence ledger", r.line, id, quote(part)))
		}
	}
	return problems
}

// findingStatus reads a Findings status cell against the closed vocabulary the plan documents. It is the
// breach the check used to hide: the old `confirmed|fixed` regex read `resolved` as unsettled, so the row
// silently stopped owing the test that holds its verdict and the plan still passed. The returned status
// is trimmed and lowercased for the settled rule; the breach, when there is one, is ready to follow the
// finding id.
func findingStatus(raw string) (string, string) {
	status := strings.ToLower(strings.TrimSpace(raw))
	switch {
	case status == "":
		return "", "has no status: one of " + FindingsStatusList
	case !findingsStatuses[status]:
		return status, fmt.Sprintf("status \"%s\" is not one of: %s", quote(raw), FindingsStatusList)
	}
	return status, ""
}

// LedgerRow is one row of the Evidence ledger, with the cells a machine reads resolved by column name
// rather than by position. `Admit` is the single command the row declares and `Digest` the output that
// command was observed to produce; both are empty on a plan written before those columns existed.
// `Expect` says which way the command must exit: empty or `pass` for zero, `fail` for a test observed red.
//
// Cells and HeaderCells are the number of cells the row's line and its table's header hold as the
// table splitter reads them. The two differ exactly when an unescaped `|` cut a cell, which shifts
// every column to its right: a reader can then refuse the row instead of reading the wrong cell as the
// command. Both are zero for a row a caller assembled by hand rather than read from a document.
type LedgerRow struct {
	ID           string
	Claim        string
	Executed     string
	Admit        string
	Inputs       string
	Observed     string
	Digest       string
	Normalize    string
	Mode         string
	Mutate       string
	Expect       string
	Mutation     string
	Reproduction string
	Label        string
	Cells        int
	HeaderCells  int
}

// Ledger reads the Evidence ledger exactly where Check reads it and resolves every machine column by
// name. Order is the document's; ID stays the first cell and Label the last, the same reading Check
// does, so a row with extra or truncated cells is reported rather than silently dropped. Rows the table
// already skips, a separator or a placeholder row, stay skipped.
func Ledger(doc string) []LedgerRow {
	scan := scanSection(strings.Split(doc, "\n"), "Evidence ledger")
	if scan.header == nil {
		return nil
	}
	// Column names are matched by substring, so each name below is the whole word the header cell
	// carries and shares it with no other column: `admit` never resolves to `Executed`, `digest` never
	// resolves to `Observed` or to the mutation column, and `normalize` and `expect` resolve to nothing else.
	index := map[string]int{}
	for _, name := range []string{"claim", "executed", "admit", "inputs", "observed", "digest", "normalize", "mode", "mutate", "expect", "mutation", "reproduction"} {
		index[name] = columnIndex(scan.header, name)
	}
	ledger := make([]LedgerRow, 0, len(scan.rows))
	for _, scanned := range scan.rows {
		row := scanned.cells
		r := LedgerRow{ID: cell(row, 0), Label: cell(row, len(row)-1), Cells: len(row), HeaderCells: len(scan.header)}
		for _, f := range []struct {
			name  string
			field *string
		}{
			{"claim", &r.Claim},
			{"executed", &r.Executed},
			{"admit", &r.Admit},
			{"inputs", &r.Inputs},
			{"observed", &r.Observed},
			{"digest", &r.Digest},
			{"normalize", &r.Normalize},
			{"mode", &r.Mode},
			{"mutate", &r.Mutate},
			{"expect", &r.Expect},
			{"mutation", &r.Mutation},
			{"reproduction", &r.Reproduction},
		} {
			*f.field = cell(row, index[f.name])
		}
		ledger = append(ledger, r)
	}
	return ledger
}

// digestRe is the only shape a recorded digest may take: it is exactly what Digest returns, so a
// recorded cell is always comparable to a fresh observation and a typo can never be pinned.
var digestRe = regexp.MustCompile(`^sha256:[0-9a-f]{64}$`)

// modeRe is the only shape a recorded Mode may take. The mode is part of what a pin means because the same
// command digests differently in a container than on the host, so a row that pins a digest without pinning
// the mode it was taken in would fail its next check with a digest mismatch that says nothing about why.
var modeRe = regexp.MustCompile(`^(host|sandbox)$`)

// The reason codes a Mutate cell earns when it cannot be used. They live beside the grammar rather than beside
// the admission, because a cell that does not parse and a cell whose edit does not exist are defects of the row
// that this package is the only one able to name. The reasons a replay earns belong to the admission, which is
// the only place that can observe one.
const (
	ReasonMutationMalformed = "mutation-malformed"
	ReasonMutationNotFound  = "mutation-not-found"
	ReasonMutationNoLine    = "mutation-no-line"
	ReasonMutationAmbiguous = "mutation-ambiguous"
	ReasonMutationNoOp      = "mutation-no-op"
)

// MutationError names why a declared mutation cannot be used, with the reason code a caller reports.
type MutationError struct {
	Reason string
	Detail string
}

func (e MutationError) Error() string { return e.Detail }

// Mutation is a row's declared edit: the text to replace, the text that replaces it, and where. It is a value
// and not a shell command because a replay has to be able to undo exactly what it did, and an edit admits an
// exact inverse while a command does not.
type Mutation struct {
	Old  string
	New  string
	Path string
	Line int
	// Equivalent marks an edit declared equivalent (`~ ` in the cell): the command must stay green under it
	// rather than go red, so a survey states which of its mutants it claims no test can tell apart.
	Equivalent bool
}

// MutationSeparator splits a Mutate cell into the edits of a survey. It is spaced on both sides so a `;;` inside
// an edit's text still parses; an edit whose own text carries ` ;; ` cannot be expressed in a cell.
const MutationSeparator = " ;; "

// equivalentPrefix marks an edit the row declares equivalent: the command must stay green under it.
const equivalentPrefix = "~ "

// ParseMutations reads a Mutate cell as a survey: one or more edits separated by ` ;; `, each in the shape
// ParseMutation reads, and each optionally prefixed `~ ` to declare it equivalent. Every edit is parsed before
// any is returned, so a survey with one bad edit is refused whole, and a refusal names the edit's position
// whenever the cell holds more than one, because "the second edit" is what the author has to go and fix.
func ParseMutations(cell string) ([]Mutation, error) {
	edits := strings.Split(strings.TrimSpace(cell), MutationSeparator)
	mutations := make([]Mutation, 0, len(edits))
	for i, edit := range edits {
		edit = strings.TrimSpace(edit)
		equivalent := strings.HasPrefix(edit, equivalentPrefix)
		m, err := ParseMutation(strings.TrimPrefix(edit, equivalentPrefix))
		if err != nil {
			return nil, atEdit(err, i, len(edits))
		}
		m.Equivalent = equivalent
		mutations = append(mutations, m)
	}
	return mutations, nil
}

// ValidateMutations checks every edit of a survey against the tree, in order, and reports the first that a
// replay would refuse, naming its position when there is more than one. Nothing here runs or edits.
func ValidateMutations(root string, mutations []Mutation) error {
	for i, m := range mutations {
		if err := ValidateMutation(root, m); err != nil {
			return atEdit(err, i, len(mutations))
		}
	}
	return nil
}

// MutationPosition names edit i (zero-based) of a survey of n edits, and is empty for a single edit so a row
// that declares one edit reads exactly as it did before surveys existed.
func MutationPosition(i, n int) string {
	if n < 2 {
		return ""
	}
	return fmt.Sprintf("edit %d of %d", i+1, n)
}

// atEdit prefixes a mutation defect with the position of the edit it belongs to, keeping its reason code.
func atEdit(err error, i, n int) error {
	position := MutationPosition(i, n)
	if position == "" {
		return err
	}
	var bad MutationError
	if errors.As(err, &bad) {
		return MutationError{Reason: bad.Reason, Detail: position + ": " + bad.Detail}
	}
	return fmt.Errorf("%s: %w", position, err)
}

// ParseMutation reads the one shape a Mutate cell may take:
//
//	<old> => <new> @ <path>:<line>
//
// The parts are returned rather than interpreted: nothing here runs, and nothing here decides whether the edit
// is present in the tree. The grammar is strict on purpose, because a cell a human reads loosely is a cell the
// replay would apply to the wrong place.
func ParseMutation(cell string) (Mutation, error) {
	malformed := func(why string) error {
		return MutationError{Reason: ReasonMutationMalformed, Detail: fmt.Sprintf(
			"a Mutate cell is `<old> => <new> @ <path>:<line>`, and this one %s", why)}
	}
	trimmed := strings.TrimSpace(cell)
	arrow := strings.Index(trimmed, "=>")
	if arrow < 0 {
		return Mutation{}, malformed("holds no `=>` to say what replaces what")
	}
	m := Mutation{Old: strings.TrimSpace(trimmed[:arrow])}
	rest := strings.TrimSpace(trimmed[arrow+len("=>"):])
	if m.Old == "" {
		return Mutation{}, malformed("names no text to replace")
	}

	// The locator is cut at the LAST `@`, so an edit whose text carries one still parses: the `@` that
	// separates the replacement from its location is the final one by construction.
	at := strings.LastIndex(rest, "@")
	if at < 0 {
		return Mutation{}, malformed("holds no `@` to say where the edit lands")
	}
	m.New = strings.TrimSpace(rest[:at])
	locator := strings.TrimSpace(rest[at+1:])

	colon := strings.LastIndex(locator, ":")
	if colon < 0 {
		return Mutation{}, malformed("holds no `<path>:<line>` locator")
	}
	m.Path = strings.TrimSpace(locator[:colon])
	line, err := strconv.Atoi(strings.TrimSpace(locator[colon+1:]))
	if err != nil || line < 1 {
		return Mutation{}, malformed("ends in a line that is not a positive number")
	}
	m.Line = line
	if m.Path == "" {
		return Mutation{}, malformed("names no file")
	}
	if m.Old == m.New {
		return Mutation{}, MutationError{Reason: ReasonMutationNoOp, Detail: fmt.Sprintf(
			"`%s` is replaced by itself, so the edit changes nothing and cannot make any command go red", m.Old)}
	}
	return m, nil
}

// ValidateMutation reads the file a mutation names, under root, and reports what a replay would refuse before
// running anything. A row that claims its own command is falsifiable is making a claim about this tree, so the
// claim is checked where the tree is: nothing here executes, and nothing here edits.
func ValidateMutation(root string, m Mutation) error {
	if filepath.IsAbs(m.Path) {
		return MutationError{Reason: ReasonMutationMalformed, Detail: fmt.Sprintf(
			"%q is an absolute path, and a mutation names a file inside the tree so the replay can copy that tree and leave this one alone", m.Path)}
	}
	// A path that climbs out of the tree is refused for the same reason an absolute one is: the replay edits a
	// copy of the tree, and `..` would land the edit on a file the copy does not own.
	if clean := filepath.Clean(filepath.FromSlash(m.Path)); clean == ".." || strings.HasPrefix(clean, ".."+string(filepath.Separator)) {
		return MutationError{Reason: ReasonMutationMalformed, Detail: fmt.Sprintf(
			"%q climbs out of the tree with `..`, and a mutation names a file inside the tree so the replay can copy that tree and leave this one alone", m.Path)}
	}
	full := filepath.Join(root, filepath.FromSlash(m.Path))
	info, err := os.Stat(full)
	if err != nil {
		return MutationError{Reason: ReasonMutationNotFound, Detail: fmt.Sprintf(
			"%s is not there, and an edit can only land on a file this tree carries: %v", m.Path, err)}
	}
	if info.IsDir() {
		return MutationError{Reason: ReasonMutationNotFound, Detail: fmt.Sprintf(
			"%s is a directory, and a mutation edits a file", m.Path)}
	}
	content, err := os.ReadFile(full)
	if err != nil {
		return MutationError{Reason: ReasonMutationNotFound, Detail: fmt.Sprintf("%s could not be read: %v", m.Path, err)}
	}
	lines := strings.Split(string(content), "\n")
	if m.Line > len(lines) {
		return MutationError{Reason: ReasonMutationNoLine, Detail: fmt.Sprintf(
			"%s has %d lines and the cell names line %d, so there is nothing there to edit", m.Path, len(lines), m.Line)}
	}
	if !strings.Contains(lines[m.Line-1], m.Old) {
		return MutationError{Reason: ReasonMutationNotFound, Detail: fmt.Sprintf(
			"line %d of %s does not hold `%s`, so the cell points at a line the edit is not on", m.Line, m.Path, m.Old)}
	}
	// Exactly once, so the replay cannot land somewhere the cell did not mean.
	switch n := strings.Count(string(content), m.Old); {
	case n == 0:
		return MutationError{Reason: ReasonMutationNotFound, Detail: fmt.Sprintf(
			"`%s` does not occur in %s", m.Old, m.Path)}
	case n > 1:
		return MutationError{Reason: ReasonMutationAmbiguous, Detail: fmt.Sprintf(
			"`%s` occurs %d times in %s, so an edit that names it cannot say which one it means; narrow the text until only the intended one matches", m.Old, n, m.Path)}
	}
	return nil
}

// ErrNoColumn reports that the ledger's header does not name the column a writer was asked to fill. A caller
// branches on it instead of reading the message when it knows the column is optional: an empty Mode cell
// already means the host, so a host recording into a plan written before that column existed is still true.
var ErrNoColumn = errors.New("the Evidence ledger header names no such column")

// RecordDigest returns doc with the named ledger row's Digest cell replaced by digest, every other byte of
// the document unchanged. It is the one writer in this package: Check, Ledger and Gaps only read, so this is
// where a wrong splice could corrupt a plan.
func RecordDigest(doc, id, digest string) (string, error) {
	if !digestRe.MatchString(digest) {
		return "", fmt.Errorf("digest %q is not a sha256 digest: a Digest cell carries sha256:<64 lowercase hex>, the form a fresh observation takes", digest)
	}
	return recordCell(doc, id, "digest", "Digest", digest)
}

// RecordMode returns doc with the named ledger row's Mode cell replaced by mode, every other byte of the
// document unchanged. Recording a pin records both cells: the digest says what was observed and the mode
// says where, and a row that carries one without the other cannot be checked honestly in either mode.
func RecordMode(doc, id, mode string) (string, error) {
	if !modeRe.MatchString(mode) {
		return "", fmt.Errorf("mode %q is not an execution mode: a Mode cell carries host or sandbox, the two places a row's command can be observed", mode)
	}
	return recordCell(doc, id, "mode", "Mode", mode)
}

// recordCell returns doc with one named column of one ledger row replaced by value, every other byte of the
// document unchanged.
//
// The ledger is located exactly where Check and Ledger locate it, and the column is resolved by the same
// substring match Ledger resolves it with, so the writer and the readers can never disagree about which
// column is which. The row is found by its first cell and the cell is spliced by byte offset: a row may
// carry a backslash-escaped pipe in any cell, so the cell is cut where split cuts it rather than by
// re-rendering the row, which would rewrite escapes and spacing nobody asked to touch. Writing the value a
// cell already holds is a no-op, so a rerun rewrites nothing.
func recordCell(doc, id, column, label, value string) (string, error) {
	lines := strings.Split(doc, "\n")
	heading, end := sectionRegion(lines, "Evidence ledger")
	if heading < 0 {
		return "", fmt.Errorf("the document has no Evidence ledger section, so there is no row %s to record", id)
	}
	scan := scanTable(lines, heading+1, end)
	if scan.header == nil {
		return "", fmt.Errorf("the Evidence ledger section holds no table, so there is no row %s to record", id)
	}
	iColumn := columnIndex(scan.header, column)
	if iColumn < 0 {
		return "", fmt.Errorf("the Evidence ledger header names no %s column, so row %s has nowhere to record %s: %w", label, id, value, ErrNoColumn)
	}

	// scanTable carries the exact source line beside the cells it read, so the row a reader sees is the row
	// this writes. Line starts preserve the original bytes; the splice still uses cellSpan rather than joining
	// cells back together, so escaped pipes and the author's spacing survive.
	lineStarts := make([]int, len(lines))
	for i := 1; i < len(lines); i++ {
		lineStarts[i] = lineStarts[i-1] + len(lines[i-1]) + 1
	}
	for _, scanned := range scan.rows {
		if cell(scanned.cells, 0) != id {
			continue
		}
		lineIndex := scanned.line - 1
		line := lines[lineIndex]
		lineStart := lineStarts[lineIndex]
		cs, ce, ok := cellSpan(line, iColumn)
		if !ok {
			// cellSpan cuts the cells split cuts, so the column has no cell exactly when the row is shorter
			// than it: one refusal, not two spellings of the same fact.
			return "", fmt.Errorf("evidence %s has %d cells, so the %s column (%d) has no cell in that row; restore it before recording", id, len(scanned.cells), label, iColumn)
		}
		// Replace the cell's own bytes and nothing else: a cell that holds a value keeps the spacing its
		// author wrote around it, so a rerun with the same value is byte-identical. A cell that holds no
		// value has no spacing around a value to keep, so the value is framed by one space and the row still
		// reads as a table row instead of colliding with the next pipe.
		raw := line[cs:ce]
		content := strings.TrimSpace(raw)
		if content == "" {
			if raw == "" {
				return doc[:lineStart+cs] + value + doc[lineStart+ce:], nil
			}
			return doc[:lineStart+cs] + " " + value + " " + doc[lineStart+ce:], nil
		}
		first := lineStart + cs + strings.Index(raw, content)
		return doc[:first] + value + doc[first+len(content):], nil
	}
	return "", fmt.Errorf("evidence %s is not a row in the Evidence ledger, so the plan has nothing to record against", id)
}

// cellSpan returns the byte span one cell occupies in a markdown table row, cut exactly where split
// cuts it, so RecordDigest can splice a single cell instead of re-rendering the row. A backslash-
// escaped pipe is part of its cell, never a delimiter.
func cellSpan(line string, i int) (start, end int, ok bool) {
	trimmed := strings.TrimSpace(line)
	if trimmed == "" {
		return 0, 0, false
	}
	base := strings.Index(line, trimmed)
	inner := trimmed
	if strings.HasSuffix(inner, "|") {
		inner = inner[:len(inner)-1]
	}
	if strings.HasPrefix(inner, "|") {
		inner = inner[1:]
		base++
	}
	cellStart, n := 0, 0
	escaped := false
	for j := 0; j < len(inner); j++ {
		switch c := inner[j]; {
		case escaped:
			escaped = false
		case c == '\\':
			escaped = true
		case c == '|':
			if n == i {
				return base + cellStart, base + j, true
			}
			n++
			cellStart = j + 1
		}
	}
	if n == i {
		return base + cellStart, base + len(inner), true
	}
	return 0, 0, false
}

// LightActivated reports whether a plan declares a scoped run that passes every Light-specific rule.
// The bench reads activation from here, so an ordinary plan, a detected defect, or a line that merely
// looks like a declaration is never a scoped run.
func LightActivated(doc string) bool {
	problems, declared := lightReport(strings.Split(doc, "\n"))
	return declared && len(problems) == 0
}

// MicroActivated reports whether a plan declares a micro plan and passes plan check as a whole. Only an
// activated micro plan owes no breadth: a declaration that dropped its evidence is a label, not a plan.
func MicroActivated(doc string) bool {
	_, ok := microTarget(strings.Split(doc, "\n"))
	return ok
}

// microTarget returns the file an activated micro plan names, and whether the plan is one. A micro plan
// trades the layer sweep for a well-formed record, so any breach plan check names, not only a Micro one,
// keeps the plan owing breadth: a plan the checker refuses must not silence the gate.
func microTarget(lines []string) (string, bool) {
	problems, target, declared := microReport(lines)
	if !declared || len(problems) != 0 {
		return target, false
	}
	return target, len(CheckDocument(strings.Join(lines, "\n"))) == 0
}

// microReport returns every breach a declared micro plan owes, the target it names, and whether the plan
// declares one at all. A micro plan gives up the layer sweep, so what it keeps is checked instead of
// believed: an Evidence ledger with an observed row and a row carrying a mutation, one corroborated file,
// no Layer matrix, and no second, Light, declaration.
func microReport(lines []string) ([]string, string, bool) {
	line, raw, found := 0, "", false
	for i, l := range lines {
		if m := microLineRe.FindStringSubmatch(l); m != nil {
			line, raw, found = i+1, strings.TrimSpace(m[1]), true
			break
		}
	}
	if !found {
		return nil, "", false
	}
	var problems []string
	target := ""
	if shape := lightShapeRe.FindStringSubmatch(raw); shape == nil {
		problems = append(problems, fmt.Sprintf("line %d: the Micro declaration must read `Micro: <file path> · touches none`", line))
	} else {
		target = strings.TrimSpace(shape[1])
		if target == "" || placeholder.MatchString(target) {
			problems = append(problems, fmt.Sprintf("line %d: the Micro declaration names no file", line))
			target = ""
		} else if !targetIsInEvidence(lines, target) {
			problems = append(problems, fmt.Sprintf("line %d: the Micro declaration names target %s, which no Ranked-target row and no path:line citation corroborates", line, quote(target)))
		}
		if classes := strings.TrimSpace(shape[2]); !strings.EqualFold(classes, "none") {
			problems = append(problems, fmt.Sprintf("line %d: the Micro declaration touches %s: a micro plan touches none of the Light refusal classes, so its classes read `none`", line, quote(classes)))
		}
	}
	if d := lightDeclaration(lines); d.found {
		problems = append(problems, fmt.Sprintf("line %d: the plan declares both Micro and Light: a run is one or the other", d.line))
	}
	if heading, _ := sectionRegion(lines, "Layer matrix"); heading >= 0 {
		problems = append(problems, fmt.Sprintf("line %d: a Micro plan carries a Layer matrix: a micro plan owes no layer sweep, so a planned sweep makes it an ordinary plan", heading+1))
	}
	problems = append(problems, microLedgerProblems(lines, line)...)
	return problems, target, true
}

// microLedgerProblems enforces the two ledger rows a micro plan keeps in place of the layer sweep: one
// conclusion labelled `observado`, and one row whose `Mutate` cell carries the mutation the pinning test kills.
func microLedgerProblems(lines []string, line int) []string {
	ledger := scanSection(lines, "Evidence ledger")
	if ledger.header == nil {
		return []string{fmt.Sprintf("line %d: a Micro plan has no Evidence ledger table, so nothing records the pinning test or its mutation", line)}
	}
	iMutate := columnIndex(ledger.header, "mutate")
	observed, mutated := false, false
	for _, r := range ledger.rows {
		observed = observed || strings.EqualFold(cell(r.cells, len(r.cells)-1), "observado")
		if m := cell(r.cells, iMutate); iMutate >= 0 && m != "" && !placeholder.MatchString(m) {
			mutated = true
		}
	}
	var problems []string
	if !observed {
		problems = append(problems, fmt.Sprintf("line %d: a Micro plan has no Evidence ledger row labelled observado, so the pinning test was never observed", line))
	}
	if !mutated {
		problems = append(problems, fmt.Sprintf("line %d: a Micro plan has no Evidence ledger row with a Mutate cell, so no mutation shows the pinning test can fail", line))
	}
	return problems
}

// lightDecl is a parsed `Light:` declaration: the target it names, the line it sits on, and the breaches
// its own shape owes. Whether the change was really bounded stays with the operator and the plan's
// reader, never with this check.
type lightDecl struct {
	target   string
	line     int
	problems []string
	found    bool
}

// lightReport returns every breach a declared scoped run owes, and whether the plan declares one at
// all. A plan that never claims to be scoped owes none of these rules.
func lightReport(lines []string) ([]string, bool) {
	d := lightDeclaration(lines)
	if !d.found {
		return nil, false
	}
	problems := d.problems
	if d.target != "" && !targetIsInEvidence(lines, d.target) {
		problems = append(problems, fmt.Sprintf("line %d: the Light declaration names target %s, which no Ranked-target row and no path:line citation corroborates", d.line, quote(d.target)))
	}
	layers := scanSection(lines, "Layer matrix")
	return append(problems, unscopedLayers(layers)...), true
}

// lightDeclaration reads the `Light:` header: the declared shape, a non-empty blast radius and
// non-empty touched classes. The classes are the declaration's own words; a binary can read that they
// are there, never that they are true. It reads the declaration line by line so a breach can name the
// line the declaration sits on.
func lightDeclaration(lines []string) lightDecl {
	for i, l := range lines {
		m := lightLineRe.FindStringSubmatch(l)
		if m == nil {
			continue
		}
		line := i + 1
		shape := lightShapeRe.FindStringSubmatch(strings.TrimSpace(m[1]))
		if shape == nil {
			return lightDecl{line: line, found: true, problems: []string{
				fmt.Sprintf("line %d: the Light declaration must read `Light: <blast radius> · touches <classes>`", line)}}
		}
		target, classes := strings.TrimSpace(shape[1]), strings.TrimSpace(shape[2])
		var problems []string
		if target == "" || placeholder.MatchString(target) {
			problems = append(problems, fmt.Sprintf("line %d: the Light declaration names no blast radius", line))
			// Nothing to corroborate: the breach is reported once, not again as a target the plan
			// cannot vouch for.
			target = ""
		}
		if classes == "" || placeholder.MatchString(classes) {
			problems = append(problems, fmt.Sprintf("line %d: the Light declaration names no touched classes", line))
		}
		return lightDecl{target: target, line: line, problems: problems, found: true}
	}
	return lightDecl{}
}

// targetIsInEvidence corroborates a declared blast radius against what the plan already carries: an
// exact `Target` cell in the ranked targets, or a path the plan cites as path:line. Reading the claim
// back against the plan is all a binary can do; whether the target is bounded stays with the reader.
func targetIsInEvidence(lines []string, target string) bool {
	ranked := scanSection(lines, "Ranked targets")
	iTarget := columnIndex(ranked.header, "target")
	for _, r := range ranked.rows {
		if iTarget >= 0 && cell(r.cells, iTarget) == target {
			return true
		}
	}
	for _, cite := range pathCiteRe.FindAllString(strings.Join(lines, "\n"), -1) {
		if path, _, ok := strings.Cut(cite, ":"); ok && path == target {
			return true
		}
	}
	return false
}

// unscopedLayers enforces the one rule a `Light:` plan owes beyond the ordinary contract: a layer
// the run left out states why in its `Scope` cell, in a sentence rather than a keyword. Without it
// `Light:` is a label anyone can type, and the layer sweep's blind spot comes back unrecorded.
func unscopedLayers(layers tableScan) []string {
	iStatus := columnIndex(layers.header, "status")
	iScope := columnIndex(layers.header, "scope")
	var problems []string
	for _, r := range layers.rows {
		if !naStatus.MatchString(cell(r.cells, iStatus)) {
			continue
		}
		if cell(r.cells, iScope) == "" {
			problems = append(problems, fmt.Sprintf("line %d: layer %s is out of scope in a Light plan and its Scope cell is empty, so nothing records why it was left out", r.line, quote(cell(r.cells, 0))))
		}
	}
	return problems
}

// row is one markdown table data row: its cells and the line it sits on in the plan file. Carrying them
// together is the whole point — `path: problem` left the reader grepping for the row the message was
// about.
type row struct {
	line  int
	cells []string
}

// tableScan is what a region scan found. header is nil when the region carries no table at all. ended and
// resumed are the pair the old scanner hid: a line that is not a row closes the block, and a `|` line after
// it proves the table was cut in two. fenceLine records a fence the region ended inside.
type tableScan struct {
	header    []string
	head      int // 1-based line of the last heading row: the separator when one was read, else the header
	rows      []row
	ended     int    // 1-based line that closed the block, 0 when no line closed it
	endedText string // that line, unsanitised: it is plan text, so every quote of it goes through quote()
	resumed   int    // 1-based line of the first `|` line after the close, 0 when the block was not interrupted
	fenceLine int    // 1-based line an unclosed code fence opened at, 0 when no fence was left open
}

// scanSection reads the first table of a `## <name>` section.
func scanSection(lines []string, name string) tableScan {
	heading, end := sectionRegion(lines, name)
	if heading < 0 || end <= heading+1 {
		return tableScan{}
	}
	return scanTable(lines, heading+1, end)
}

// Section returns the body under the `## <name>` heading, up to the next level-2 heading, through the same
// locator the row readers use. A caller outside this package reads the region the checker validates instead of
// finding the heading with a walk of its own, so a heading this package stops at cannot be one the caller's walk
// runs past. The empty string means the section is not there.
func Section(doc, name string) string {
	lines := strings.Split(doc, "\n")
	heading, end := sectionRegion(lines, name)
	if heading < 0 {
		return ""
	}
	return strings.Join(lines[heading+1:end], "\n")
}

// Table reads the first markdown table of the `## <section>` section the way every reader in this package reads
// it: a fence is documentation, a blank line does not end the table, and separators and placeholder rows stay
// skipped. Header is nil when the section carries no table.
func Table(doc, section string) (header []string, rows [][]string) {
	scan := scanSection(strings.Split(doc, "\n"), section)
	if scan.header == nil {
		return nil, nil
	}
	for i := range scan.header {
		header = append(header, cell(scan.header, i))
	}
	for _, scanned := range scan.rows {
		row := make([]string, len(scanned.cells))
		for i := range scanned.cells {
			row[i] = cell(scanned.cells, i)
		}
		rows = append(rows, row)
	}
	return header, rows
}

// scanTable reads the table of lines[start:end] (0-based, end exclusive), carrying the line each row sits
// on. A blank line is skipped exactly like the separator row, so a blank inserted inside a table no longer
// drops every row under it. Once the block has closed, a `|` line is recorded as the proof that a table was
// cut. A fenced code block is documentation: an example table is never read as a row, or as the proof of a cut.
// The three places a region walk can be in: before the table's own header, inside it, and after a line closed
// the block.
const (
	beforeHeader = iota
	insideTable
	afterEnd
)

// walk is the state one region walk carries: where the table is in its lifecycle, the code fence it is inside when
// a block opened, and what the scan has found so far. It exists so each step of the walk is a function over that
// state rather than a switch inside a loop.
type walk struct {
	state     int
	fence     string
	fenceLine int
	s         tableScan
}

func scanTable(lines []string, start, end int) tableScan {
	w := walk{state: beforeHeader}
	for i := start; i < end && i < len(lines); i++ {
		t := strings.TrimSpace(lines[i])
		if w.skipWhileFenced(t) || w.openFence(t, i) {
			continue
		}
		if w.cutBySubheading(lines, i, end) {
			break
		}
		w.step(lines[i], t, i)
	}
	if w.fence != "" {
		// Every line under the fence was skipped, so the table this region owes may be inside it.
		w.s.fenceLine = w.fenceLine
	}
	return w.s
}

// skipWhileFenced reports whether the line was read inside a code fence, which is documentation: nothing in it is
// a row, a separator or the proof that the block was cut. A closing marker ends the fence; every other line under
// it is skipped.
func (w *walk) skipWhileFenced(t string) bool {
	if w.fence == "" {
		return false
	}
	if strings.HasPrefix(t, w.fence) {
		w.fence, w.fenceLine = "", 0
	}
	return true
}

// openFence records a fence this line opens and reports whether it did.
func (w *walk) openFence(t string, i int) bool {
	marker := fenceMarker(t)
	if marker == "" {
		return false
	}
	w.fence, w.fenceLine = marker, i+1
	return true
}

// cutBySubheading reports whether a subheading ended the block, and stops the walk when it did. A subheading ends
// this block only when a table of its own starts there; when it does not, the line closed the block and the first
// row after it is the proof the table was cut in two.
func (w *walk) cutBySubheading(lines []string, i, end int) bool {
	if w.state != insideTable || !strings.HasPrefix(strings.TrimSpace(lines[i]), "###") {
		return false
	}
	if resumed, opens := tableUnderSubheading(lines, i+1, end); resumed != 0 && !opens {
		w.s.ended, w.s.endedText, w.s.resumed = i+1, lines[i], resumed
	}
	return true
}

// step reads one line that is not under a fence: a blank line is skipped, a row is read against the state the walk
// is in, and any other line closes an open block.
func (w *walk) step(raw, t string, i int) {
	if t == "" {
		return
	}
	if strings.HasPrefix(t, "|") {
		w.readRow(t, i)
		return
	}
	if w.state == insideTable {
		w.s.ended, w.s.endedText, w.state = i+1, raw, afterEnd
	}
}

// readRow takes one markdown row: before the header it is the header, inside the table it is a separator or a data
// row — placeholders stay skipped, so a plan that says "no rows yet" is not read as one — and after a close it is
// the proof that the table was cut.
func (w *walk) readRow(t string, i int) {
	switch w.state {
	case beforeHeader:
		w.s.header, w.state, w.s.head = split(t), insideTable, i+1
	case insideTable:
		cells := split(t)
		switch {
		case isSeparator(cells):
			w.s.head = i + 1
		case !placeholder.MatchString(cell(cells, 0)):
			w.s.rows = append(w.s.rows, row{line: i + 1, cells: cells})
		}
	default:
		if w.s.resumed == 0 {
			w.s.resumed = i + 1
		}
	}
}

// tableUnderSubheading reads what follows a `###` line inside a region: the first line that opens a markdown
// row, and whether that row is a table of its own — GFM's shape, a header row whose delimiter is adjacent —
// which is the boundary a subheading legitimately is. Prose and blank lines before the first pipe row belong
// to the subsection; anything else after it means the row belongs to the table the subheading cut.
func tableUnderSubheading(lines []string, start, end int) (resumed int, opens bool) {
	fence, row := "", 0
	for i := start; i < end && i < len(lines); i++ {
		t := strings.TrimSpace(lines[i])
		if fence != "" {
			if strings.HasPrefix(t, fence) {
				fence = ""
			}
			continue
		}
		if marker := fenceMarker(t); marker != "" {
			fence = marker
			continue
		}
		if row == 0 {
			if !strings.HasPrefix(t, "|") {
				continue
			}
			row = i + 1
			continue
		}
		// A header row and its delimiter are adjacent, so any other line makes this row part of the
		// table the subheading cut.
		if !strings.HasPrefix(t, "|") || !isSeparator(split(t)) {
			return row, false
		}
		return row, true
	}
	return row, false
}

// fenceMarker returns the marker a line opens a code fence with — three backticks or three tildes — or "".
func fenceMarker(t string) string {
	switch {
	case strings.HasPrefix(t, "```"):
		return "```"
	case strings.HasPrefix(t, "~~~"):
		return "~~~"
	}
	return ""
}

// interruptedTable renders the breach a region leaves when part of it was never read, and the line to report
// it at. The text of the cutting line goes through quote(): it is plan text, not a sentence this package wrote.
func interruptedTable(section string, s tableScan) (int, string) {
	if s.resumed != 0 {
		return s.ended, fmt.Sprintf("the %s table is interrupted at line %d by \"%s\", so the rows after it are never read (the next table row is at line %d)",
			section, s.ended, quote(s.endedText), s.resumed)
	}
	if s.fenceLine != 0 {
		return s.fenceLine, fmt.Sprintf("the %s table region ends inside a code fence opened at line %d, so nothing below it was read", section, s.fenceLine)
	}
	return 0, ""
}

// sectionRegion locates a `## <name>` section: its 0-based heading line and the exclusive end of its body,
// or (-1, -1) when the section is missing. A table may sit under a `###`, so the region spans to the next
// level-2 heading and the scan decides which part of it is a table.
func sectionRegion(lines []string, name string) (heading, end int) {
	for i, l := range lines {
		t := strings.TrimSpace(l)
		if !strings.HasPrefix(t, "## ") || !strings.EqualFold(strings.TrimSpace(t[3:]), name) {
			continue
		}
		for j := i + 1; j < len(lines); j++ {
			if strings.HasPrefix(strings.TrimSpace(lines[j]), "## ") {
				return i, j
			}
		}
		return i, len(lines)
	}
	return -1, -1
}

// tableProblems reports every data row in the document whose cell count disagrees with its own table's
// header. A cell holding an unescaped `|` splits into several, so the row declares one thing and carries
// another, and every column to the right of the cut is read from the wrong cell. Each malformed row earns
// one breach, not one per extra cell. It reads the document through the same scanner the row readers use,
// so a breach is reported exactly where a row would be read: an example inside a code fence is documentation,
// and a blank line inside a table does not restart it. A loop with rules of its own is how the checker came
// to invent a breach about a line nobody reads and to miss the count that explains the rows it did read.
func tableProblems(doc string) []string {
	lines := strings.Split(doc, "\n")
	var problems []string
	for _, region := range headingRegions(lines) {
		problems = append(problems, regionTableProblems(lines, region)...)
	}
	return problems
}

// regionTableProblems walks every table one heading region holds. A region can hold more than one: a table ends
// at the first line that is not a row of it, and the next table of the same section may follow that line.
func regionTableProblems(lines []string, region headingRegion) []string {
	var problems []string
	for start := region.start; start < region.end; {
		scan := scanTable(lines, start, region.end)
		problems = append(problems, tableRowBreaches(region.name, scan)...)
		// The block closed at a line inside this region: the next table of the same section may follow it.
		if scan.ended == 0 || scan.ended <= start || scan.ended >= region.end {
			break
		}
		start = scan.ended
	}
	return problems
}

// tableRowBreaches reports the rows of one table that do not carry the cells their header declares. A table with
// no header has no count to hold a row to, so it earns nothing here — whether a section owes a table is a
// different question, asked elsewhere.
func tableRowBreaches(table string, scan tableScan) []string {
	if scan.header == nil {
		return nil
	}
	var problems []string
	for _, r := range scan.rows {
		if len(r.cells) != len(scan.header) {
			problems = append(problems, cellCountBreach(table, cell(r.cells, 0), len(r.cells), len(scan.header)))
		}
	}
	return problems
}

// headingRegion is one heading and the lines it owns: the line under it up to the next heading of any level,
// which is where another table of the same section may start.
type headingRegion struct {
	name  string
	start int // 0-based, first line under the heading
	end   int // 0-based, exclusive
}

// headingRegions returns the region each heading opens, in order. Text before the first heading belongs to no
// region: no reader reads a table there, so the checker does not measure one either.
func headingRegions(lines []string) []headingRegion {
	var regions []headingRegion
	for i, l := range lines {
		name, ok := headingName(strings.TrimSpace(l))
		if !ok {
			continue
		}
		if n := len(regions); n > 0 {
			regions[n-1].end = i
		}
		regions = append(regions, headingRegion{name: name, start: i + 1, end: len(lines)})
	}
	return regions
}

// headingName returns the text of a markdown heading line and whether the line is a heading at all. A `#`
// that is not followed by a space is a tag, not a heading, so the two never read as each other.
func headingName(t string) (string, bool) {
	level := 0
	for level < len(t) && t[level] == '#' {
		level++
	}
	if level == 0 || level > 6 || level == len(t) || t[level] != ' ' {
		return "", false
	}
	return strings.TrimSpace(t[level:]), true
}

// cellCountBreach is the one breach a malformed row earns: it names the table, the row's first cell so the
// reader can find it, and both counts, then says plainly what the mismatch means. A row with more cells
// was cut by an unescaped `|`; a row with fewer is simply missing one.
func cellCountBreach(table, first string, cells, header int) string {
	where := "an unnamed table"
	if table != "" {
		where = "the " + table + " table"
	}
	if cells > header {
		return fmt.Sprintf("%s row %q has %d cells against the header's %d: an unescaped `|` splits a cell, so the row carries more than it declares", where, first, cells, header)
	}
	return fmt.Sprintf("%s row %q has %d cells against the header's %d: a cell is missing, so the row carries less than it declares", where, first, cells, header)
}

// split cuts a markdown row into cells. A backslash-escaped pipe belongs to the cell it sits in;
// splitting on it shifts every column to its right, so a crafted cell could move a layer's owner
// out of the column the report reads.
func split(line string) []string {
	line = strings.TrimPrefix(strings.TrimSuffix(strings.TrimSpace(line), "|"), "|")
	var parts []string
	var cur strings.Builder
	escaped := false
	for _, r := range line {
		switch {
		case escaped:
			if r != '|' {
				cur.WriteRune('\\')
			}
			cur.WriteRune(r)
			escaped = false
		case r == '\\':
			escaped = true
		case r == '|':
			parts = append(parts, strings.TrimSpace(cur.String()))
			cur.Reset()
		default:
			cur.WriteRune(r)
		}
	}
	if escaped {
		cur.WriteRune('\\')
	}
	return append(parts, strings.TrimSpace(cur.String()))
}

func isSeparator(cells []string) bool {
	for _, c := range cells {
		if c != "" && strings.Trim(c, "-: ") != "" {
			return false
		}
	}
	return true
}

func cell(row []string, i int) string {
	if i < 0 || i >= len(row) {
		return ""
	}
	return strings.TrimSpace(row[i])
}

func columnIndex(header []string, name string) int {
	// Most historical readers deliberately use substring matching (`Finding (path:line)` and `Status`).
	// Run is different: `Target rung` contains the substring `run`, so this one machine column must match
	// the whole header cell or a ranked target's rung will be read as its run.
	if strings.EqualFold(strings.TrimSpace(name), "run") {
		for i, h := range header {
			if strings.EqualFold(strings.TrimSpace(h), "run") {
				return i
			}
		}
		return -1
	}
	for i, h := range header {
		if strings.Contains(strings.ToLower(h), strings.ToLower(name)) {
			return i
		}
	}
	return -1
}

func runColumnProblems(name string, scan tableScan) []string {
	if scan.header == nil {
		return nil
	}
	iRun := columnIndex(scan.header, "run")
	if iRun < 0 {
		return []string{fmt.Sprintf("the %s table has no Run column: run tpp plan upgrade to add it", name)}
	}
	var problems []string
	for _, r := range scan.rows {
		run := cell(r.cells, iRun)
		if run == "" {
			continue
		}
		if err := ValidateRun("Run", run); err != nil {
			problems = append(problems, fmt.Sprintf("line %d: the %s table Run cell %q is invalid: %v", r.line, name, run, err))
		}
	}
	return problems
}

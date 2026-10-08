package plan

import (
	"fmt"
	"os"
	"regexp"
	"strings"
)

// Evidence is one Evidence ledger row as add-evidence writes it. Digest and Mode are absent on purpose: they
// record an observation, so only `plan admit --execute --record` writes them, never the operator's hand.
type Evidence struct {
	ID           string
	Claim        string
	Executed     string
	Admit        string
	Inputs       string
	Observed     string
	Normalize    string
	Mutate       string
	Expect       string
	Control      string // the "Mutation or negative control → result" prose
	Reproduction string
	Label        string
}

// AddEvidence writes one Evidence ledger row into the plan at path and returns the 1-based line it landed on.
// It is add-finding's counterpart for the ledger: the same lock, the same checker held to the candidate before a
// byte moves, and a refusal that leaves the file byte-identical.
func AddEvidence(path string, e Evidence) (int, error) {
	if err := e.checkValues(); err != nil {
		return 0, err
	}
	target := canonicalPath(path)
	lock, err := LockPlanWithin(target, lockWait)
	if err != nil {
		return 0, err
	}
	defer UnlockPlan(lock)
	return addEvidence(target, e)
}

func addEvidence(path string, e Evidence) (int, error) {
	raw, err := os.ReadFile(path)
	if err != nil {
		return 0, err
	}
	lines := strings.Split(string(raw), "\n")
	heading, end := sectionRegion(lines, "Evidence ledger")
	if heading < 0 {
		return 0, fmt.Errorf("the plan has no ## Evidence ledger section, so there is nowhere to record the row")
	}
	table := scanTable(lines, heading+1, end)
	if table.header == nil {
		return 0, fmt.Errorf("the Evidence ledger section has no table, so the row has no columns to land in")
	}
	if _, message := interruptedTable("Evidence ledger", table); message != "" {
		return 0, fmt.Errorf("%s; writing into a cut table is how rows get lost", message)
	}
	for _, r := range table.rows {
		if strings.EqualFold(strings.TrimSpace(cell(r.cells, 0)), strings.TrimSpace(e.ID)) {
			return 0, fmt.Errorf("evidence %s is already row %d: this command never overwrites an observation", quote(e.ID), r.line)
		}
	}
	row, err := e.row(table.header)
	if err != nil {
		return 0, err
	}
	after := findingsEnd(table)
	candidate := strings.Join(insertLine(lines, after, row), "\n")
	if problems := CheckDocument(candidate); len(problems) > 0 {
		return 0, fmt.Errorf("the row would not pass plan check: %s; the plan is unchanged", strings.Join(problems, "; "))
	}
	if err := writePlan(path, candidate); err != nil {
		return 0, err
	}
	return after + 1, nil
}

// evidenceColumns maps each value to the ledger column it fills, by the same substring names Ledger reads them
// with, so the writer and every reader agree about which cell is which.
func (e Evidence) columns() []struct{ key, label, flag, value string } {
	return []struct{ key, label, flag, value string }{
		{"claim", "Claim", "claim", e.Claim},
		{"executed", "Executed", "executed", e.Executed},
		{"admit", "Admit", "admit", e.Admit},
		{"inputs", "Inputs", "inputs", e.Inputs},
		{"observed", "Observed", "observed", e.Observed},
		{"normalize", "Normalize", "normalize", e.Normalize},
		{"mutate", "Mutate", "mutate", e.Mutate},
		{"expect", "Expect", "expect", e.Expect},
		{"mutation", "Mutation or negative control", "control", e.Control},
		{"reproduction", "Reproduction", "reproduction", e.Reproduction},
	}
}

// checkValues refuses, before the plan is read, every value the caller got wrong: a newline (one row into two),
// an empty required value, a placeholder id, a label other than observado, and the three cells plan admit would
// refuse before running anything (Expect, Expect beside Mutate, Normalize, Mutate).
func (e Evidence) checkValues() error {
	all := append([]struct{ key, label, flag, value string }{{"", "Id", "id", e.ID}, {"", "Label", "label", e.Label}}, e.columns()...)
	for _, v := range all {
		if strings.ContainsAny(v.value, "\n\r") {
			return usagef("--%s carries a newline: a ledger row is one line", v.flag)
		}
	}
	for _, v := range []struct{ flag, value string }{{"id", e.ID}, {"claim", e.Claim}, {"executed", e.Executed}, {"observed", e.Observed}, {"label", e.Label}} {
		if strings.TrimSpace(v.value) == "" {
			return usagef("--%s is empty and the row needs it", v.flag)
		}
	}
	if placeholder.MatchString(strings.TrimSpace(e.ID)) {
		return usagef("--id %s is a placeholder: the checker skips a row whose Id cell is one, so the row would never be read", quote(e.ID))
	}
	if strings.TrimSpace(e.Label) != observadoLabel {
		return usagef("--label %s: the ledger holds only %s rows; a razonado item goes under Hypotheses, not here", quote(e.Label), observadoLabel)
	}
	switch strings.ToLower(strings.TrimSpace(e.Expect)) {
	case "", "pass":
	case "fail":
		if strings.TrimSpace(e.Mutate) != "" {
			return usagef("--expect fail beside --mutate: a mutation already defines its own red and green runs, so the row declares one or the other")
		}
	default:
		return usagef("--expect %s is not empty, pass or fail", quote(e.Expect))
	}
	if n := strings.TrimSpace(e.Normalize); n != "" {
		if _, err := regexp.Compile(n); err != nil {
			return usagef("--normalize %s does not compile as a Go regular expression: %v", quote(e.Normalize), err)
		}
	}
	if m := strings.TrimSpace(e.Mutate); m != "" {
		if _, err := ParseMutations(m); err != nil {
			return usagef("--mutate %s: %v", quote(e.Mutate), err)
		}
	}
	return nil
}

// observadoLabel is the only label a ledger row may carry.
const observadoLabel = "observado"

// row renders the row in header order: Id first and Label last, as every reader takes them, each other value in
// its named column, and the Digest and Mode cells left empty. A value whose column the header lacks is refused
// rather than dropped, and the rendered row is read back through the checker's own split for pipe safety.
func (e Evidence) row(header []string) (string, error) {
	cells := make([]string, len(header))
	cells[0] = cellValue(e.ID)
	cells[len(header)-1] = observadoLabel
	for _, c := range e.columns() {
		at := columnIndex(header, c.key)
		if at <= 0 || at == len(header)-1 {
			if strings.TrimSpace(c.value) != "" {
				return "", fmt.Errorf("the Evidence ledger header has no %s column, so the row cannot carry --%s; write it in a plan whose ledger has that column", c.label, c.flag)
			}
			continue
		}
		cells[at] = cellValue(c.value)
	}
	line := "| " + strings.Join(cells, " | ") + " |"
	if got := len(split(line)); got != len(header) {
		return "", usagef("a value in the row shifts its columns: the row reads as %d cells where the header has %d", got, len(header))
	}
	return line, nil
}

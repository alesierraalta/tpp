package plan

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// ExportFile reads the plan at path and renders its Findings as Export does, naming the plan by its base name
// only: the export is meant for a public comment, and where a reviewer keeps the plan is nobody else's business.
func ExportFile(path, commit string) (string, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return "", err
	}
	return Export(string(data), filepath.Base(path), commit)
}

// Export renders the Findings table of doc as Markdown a reviewer can post on a pull request: one row per
// finding, with each cited evidence id joined to the command that re-observes it, its digest shortened, and the
// expected exit when the row declares a failing one. planName and commit name what the export covers. Every cell
// read from the plan passes through the same sanitiser the gaps report uses, because a plan cell pasted into a
// public comment must not be able to open a column, a code span, or an instruction the reader would follow.
// A plan without a Findings table is refused rather than exported as a report of nothing.
func Export(doc, planName, commit string) (string, error) {
	scan := scanSection(strings.Split(doc, "\n"), "Findings")
	if scan.header == nil {
		return "", errors.New("the plan has no Findings table to export")
	}
	var b strings.Builder
	b.WriteString("## tsp findings\n\n")
	fmt.Fprintf(&b, "Commit: %s · Plan: %s\n\n", span(quote(commit)), span(quote(planName)))
	if len(scan.rows) == 0 {
		b.WriteString("No findings recorded.\n")
		return b.String(), nil
	}
	ledger := map[string]LedgerRow{}
	for _, r := range Ledger(doc) {
		ledger[r.ID] = r
	}
	cols := columnsOf(scan.header)
	severity := columnIndex(scan.header, "severity")
	b.WriteString("| Id | Location | Severity | Status | Pinning test | Evidence |\n|---|---|---|---|---|---|\n")
	for _, r := range scan.rows {
		cells := []string{
			exportCell(cell(r.cells, 0)),
			exportCell(cell(r.cells, cols.find)),
			exportCell(cell(r.cells, severity)),
			exportCell(cell(r.cells, cols.status)),
			exportCell(cell(r.cells, cols.pin)),
			exportEvidence(cell(r.cells, cols.evidence), ledger),
		}
		fmt.Fprintf(&b, "| %s |\n", strings.Join(cells, " | "))
	}
	return b.String(), nil
}

// exportCell sanitises one plan cell for the comment, printing a placeholder as a dash rather than as the
// sanitiser's marker for an empty quote.
func exportCell(s string) string {
	if placeholder.MatchString(s) {
		return "-"
	}
	return span(quote(s))
}

// span renders already sanitised text as one inline code span. GitHub interprets its own syntax in a comment:
// a mention notifies people, an image loads from any server, a link and HTML render. Inside a code span none of
// that happens, and the text cannot close the span because the sanitiser has already turned every backtick into
// a quote.
func span(sanitised string) string {
	return "`" + sanitised + "`"
}

// exportEvidence joins each evidence id a finding cites to what re-observes it. Each part is sanitised on its
// own, so a long command is bounded without cutting the ids after it; an id the ledger does not carry is
// printed alone, since plan check already reports it.
func exportEvidence(ev string, ledger map[string]LedgerRow) string {
	if placeholder.MatchString(ev) {
		return "-"
	}
	var parts []string
	for _, id := range strings.FieldsFunc(ev, func(r rune) bool { return r == ',' || r == ';' || r == '/' || r == ' ' }) {
		if id = strings.Trim(id, "`"); id == "" {
			continue
		}
		row, ok := ledger[id]
		if !ok {
			parts = append(parts, quote(id))
			continue
		}
		part := quote(id)
		if !placeholder.MatchString(row.Admit) {
			part += ": " + quote(strings.Trim(row.Admit, "`"))
		}
		if digestRe.MatchString(row.Digest) {
			part += " · " + row.Digest[:len("sha256:")+12]
		} else if !placeholder.MatchString(row.Digest) {
			part += " · " + quote(row.Digest)
		}
		if strings.EqualFold(row.Expect, "fail") {
			part += " · Expect: fail"
		}
		parts = append(parts, part)
	}
	return span(strings.Join(parts, "; "))
}

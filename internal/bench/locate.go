package bench

import (
	"fmt"
	"strings"

	plancheck "github.com/alesierraalta/tpp/internal/plan"
)

// RowFacts records the instrument-computed facts for one Findings table row.
type RowFacts struct {
	Row              int
	Text             string
	Fingerprint      string
	Location         string
	ReportedSeverity string
	HasLocation      bool
	CitesLedger      bool
	LocatedDefects   []string
}

// FindingFacts computes location and evidence-link facts without lexical defect matching.
func FindingFacts(plan string, key Key) []RowFacts {
	header, rows := plancheck.Table(plan, "Findings")
	if len(rows) == 0 {
		return nil
	}

	_, ledgerRows := plancheck.Table(plan, "Evidence ledger")
	ledgerByID := make(map[string]string, len(ledgerRows))
	for _, row := range ledgerRows {
		if len(row) > 0 {
			id := strings.Trim(strings.TrimSpace(row[0]), "`")
			if id != "" {
				ledgerByID[id] = strings.Join(row, " | ")
			}
		}
	}

	severityCol := columnIndex(header, "severity")
	facts := make([]RowFacts, 0, len(rows))
	for i, row := range rows {
		text := strings.Join(row, " | ")
		linked := linkedLedgerText(row, ledgerByID)
		linkedText := strings.Join(linked, " | ")
		rowCitations := citations(text)
		allCitations := append(append([]citation(nil), rowCitations...), citations(linkedText)...)

		fact := RowFacts{
			Row:         i + 1,
			Text:        text,
			HasLocation: hasLineCitation(allCitations),
			CitesLedger: len(linked) > 0,
		}
		if fingerprint, err := FindingRowFingerprint(plan, i+1); err == nil {
			fact.Fingerprint = fingerprint
		}
		if severityCol >= 0 && severityCol < len(row) {
			fact.ReportedSeverity = strings.TrimSpace(row[severityCol])
		}
		for _, cite := range rowCitations {
			if cite.path != "" && cite.first > 0 {
				fact.Location = fmt.Sprintf("%s:%d", cite.path, cite.first)
				break
			}
		}
		for _, defect := range key.Defects {
			if locatedAt(allCitations, defect) {
				fact.LocatedDefects = append(fact.LocatedDefects, defect.ID)
			}
		}
		facts = append(facts, fact)
	}
	return facts
}

func hasLineCitation(cites []citation) bool {
	for _, cite := range cites {
		if cite.path != "" && cite.first > 0 {
			return true
		}
	}
	return false
}

func locatedAt(cites []citation, defect Defect) bool {
	for _, cite := range cites {
		if cite.path == "" || cite.first <= 0 || !samePath(cite.path, defect.File) {
			continue
		}
		last := cite.last
		if last < cite.first {
			last = cite.first
		}
		if defect.Line >= cite.first-LineTolerance && defect.Line <= last+LineTolerance {
			return true
		}
	}
	return false
}

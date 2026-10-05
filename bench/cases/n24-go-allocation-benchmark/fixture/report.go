// Package report renders the rows a refresh collects into the line report the dashboard reads.
package report

import "fmt"

// Row is one record of a report.
type Row struct {
	ID     int
	Name   string
	Units  int
	Cents  int64
	Status string
}

// RenderBatch renders every row of the batch as one line, preserving the batch
// order. A row renders to "id=<id> name=<name> units=<units> cents=<cents>
// status=<status>"; an empty batch renders no lines.
func RenderBatch(rows []Row) []string {
	lines := make([]string, 0, len(rows))
	for _, r := range rows {
		line := fmt.Sprintf("id=%d name=%s units=%d cents=%d status=%s",
			r.ID, r.Name, r.Units, r.Cents, r.Status)
		lines = append(lines, line)
	}
	return lines
}

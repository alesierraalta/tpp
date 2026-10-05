// Package report renders the rows a refresh collects into the line report the dashboard reads.
package report

import "strconv"

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
	var buf []byte
	for _, r := range rows {
		buf = buf[:0]
		buf = append(buf, "id="...)
		buf = strconv.AppendInt(buf, int64(r.ID), 10)
		buf = append(buf, " name="...)
		buf = append(buf, r.Name...)
		buf = append(buf, " units="...)
		buf = strconv.AppendInt(buf, int64(r.Units), 10)
		buf = append(buf, " cents="...)
		buf = strconv.AppendInt(buf, r.Cents, 10)
		buf = append(buf, " status="...)
		buf = append(buf, r.Status...)
		lines = append(lines, string(buf))
	}
	return lines
}

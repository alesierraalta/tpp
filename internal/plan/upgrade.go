package plan

import (
	"fmt"
	"os"
	"strings"
)

// Upgrade adds the Run column to the Layer matrix and Ranked targets tables. A non-empty run stamps every
// row it adds; an empty run leaves the rows unscoped. The returned count is the number of tables changed.
func Upgrade(path, run string) (int, error) {
	if run != "" {
		if err := ValidateRun("--run", run); err != nil {
			return 0, err
		}
	}
	target := canonicalPath(path)
	lock, err := LockPlanWithin(target, lockWait)
	if err != nil {
		return 0, err
	}
	defer UnlockPlan(lock)
	return upgrade(target, run)
}

func upgrade(path, run string) (int, error) {
	raw, err := os.ReadFile(path)
	if err != nil {
		return 0, err
	}
	lines := strings.Split(string(raw), "\n")
	updates := map[int]string{}
	changed := 0
	for _, name := range []string{"Layer matrix", "Ranked targets"} {
		scan := scanSection(lines, name)
		if scan.header == nil || columnIndex(scan.header, "run") >= 0 {
			continue
		}
		if _, message := interruptedTable(name, scan); message != "" {
			return 0, fmt.Errorf("%s; writing into a cut table is how rows get lost", message)
		}
		heading, end := sectionRegion(lines, name)
		headerLine, separatorLine := tableHeaderAndSeparator(lines, heading+1, end)
		if headerLine < 0 || separatorLine < 0 {
			return 0, fmt.Errorf("the %s table has no header and separator to upgrade", name)
		}
		updates[headerLine] = appendTableCell(lines[headerLine], "Run", false)
		updates[separatorLine] = appendTableCell(lines[separatorLine], "", true)
		for _, r := range scan.rows {
			updates[r.line-1] = appendTableCell(lines[r.line-1], run, false)
		}
		changed++
	}
	if changed == 0 {
		return 0, nil
	}
	for line, replacement := range updates {
		lines[line] = replacement
	}
	if err := writePlan(path, strings.Join(lines, "\n")); err != nil {
		return 0, err
	}
	return changed, nil
}

// tableHeaderAndSeparator returns the source line indexes for a table's header and separator, without
// rewriting any line it does not need. Valid plans put the separator somewhere after the first pipe row.
func tableHeaderAndSeparator(lines []string, start, end int) (header, separator int) {
	header, separator = -1, -1
	fence := ""
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
		if !strings.HasPrefix(t, "|") {
			continue
		}
		if header < 0 {
			header = i
			continue
		}
		if isSeparator(split(t)) {
			return header, i
		}
	}
	return header, separator
}

// appendTableCell appends one cell while preserving every byte already on the line. The separator gets
// a dash cell; headers and data rows get the value (which is intentionally blank for an unscoped row).
func appendTableCell(line, value string, separator bool) string {
	trailing := ""
	end := len(line)
	for end > 0 && (line[end-1] == ' ' || line[end-1] == '\t' || line[end-1] == '\r') {
		end--
	}
	base := line[:end]
	trailing = line[end:]
	if separator {
		return base + "---|" + trailing
	}
	return base + " " + value + " |" + trailing
}

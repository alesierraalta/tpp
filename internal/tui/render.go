package tui

import (
	"fmt"
	"strings"
)

const (
	helpRoot     = "up/down move | enter select | q quit"
	helpStatus   = "up/down scroll | pgup/pgdn page | esc back | q quit"
	helpFeatures = "up/down | pgup/pgdn | space toggle | enter | esc | q quit"
	helpPlan     = "up/down scroll | pgup/pgdn page | esc back | q quit"
)

const planTitle = "tsp tui: sync plan (dry-run; writes nothing)"

var menuItems = []string{"Status", "Features", "Sync plan", "Quit"}

// joinSections builds one frame from blank-line-separated sections; empty sections are dropped
// so a view with no detail block or no rows does not grow a double blank line.
func joinSections(sections ...string) string {
	kept := make([]string, 0, len(sections))
	for _, section := range sections {
		if section != "" {
			kept = append(kept, section)
		}
	}
	return strings.Join(kept, "\n\n")
}

// columns pads every cell but the last to its column's widest cell plus two spaces, so rows line
// up in a raw terminal where tab stops would not.
func columns(rows [][]string) []string {
	var widths []int
	for _, row := range rows {
		for i, cell := range row {
			if i == len(widths) {
				widths = append(widths, 0)
			}
			widths[i] = max(widths[i], len([]rune(cell)))
		}
	}
	out := make([]string, len(rows))
	for r, row := range rows {
		var b strings.Builder
		for i, cell := range row {
			b.WriteString(cell)
			if i < len(row)-1 {
				b.WriteString(strings.Repeat(" ", widths[i]-len([]rune(cell))+2))
			}
		}
		out[r] = b.String()
	}
	return out
}

func yesNo(v bool) string {
	if v {
		return "yes"
	}
	return "no"
}

func stateLabel(enabled bool) string {
	if enabled {
		return "enabled"
	}
	return "disabled"
}

func renderMenu(sel int) string {
	var items strings.Builder
	for i, name := range menuItems {
		if i > 0 {
			items.WriteByte('\n')
		}
		if i == sel {
			items.WriteString("> ")
		} else {
			items.WriteString("  ")
		}
		items.WriteString(name)
	}
	return joinSections("tsp tui", items.String(), helpRoot)
}

func renderStatus(sv StatusView, err error) string {
	const title = "tsp tui: status"
	if err != nil {
		return joinSections(title, "error: "+err.Error(), helpStatus)
	}
	var body strings.Builder
	fmt.Fprintf(&body, "State root: %s\n", sv.StateRoot)
	fmt.Fprintf(&body, "State exists: %s\n", yesNo(sv.StateExists))
	fmt.Fprintf(&body, "Installed version: %s\n", sv.InstalledVersion)
	fmt.Fprintf(&body, "Available version: %s", sv.AvailableVersion)
	table := [][]string{{"ID", "Title", "State"}}
	for _, row := range sv.Features {
		table = append(table, []string{row.ID, row.Title, stateLabel(row.Enabled)})
	}
	body.WriteString("\n\n" + strings.Join(columns(table), "\n"))
	return joinSections(title, body.String(), helpStatus)
}

func renderFeatures(rows []FeatureRow, sel int, detail string) string {
	const title = "tsp tui: features"
	table := make([][]string, len(rows))
	for i, row := range rows {
		table[i] = []string{row.ID, row.Title, stateLabel(row.Enabled)}
	}
	list := columns(table)
	for i := range list {
		if i == sel {
			list[i] = "> " + list[i]
		} else {
			list[i] = "  " + list[i]
		}
	}
	return joinSections(title, strings.Join(list, "\n"), strings.TrimRight(detail, "\n"), helpFeatures)
}

func renderPlan(report string, err error) string {
	body := report
	if err != nil {
		body = "error: " + err.Error()
	}
	return joinSections(planTitle, strings.TrimRight(body, "\n"), helpPlan)
}

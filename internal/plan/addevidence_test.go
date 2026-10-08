package plan

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// freshPlan writes the shipped skeleton, whose ledger carries every machine column.
func freshPlan(t *testing.T) string {
	t.Helper()
	p := filepath.Join(t.TempDir(), "plan.md")
	if err := Init(p, "", false); err != nil {
		t.Fatal(err)
	}
	return p
}

// observed is a row the checker accepts as it stands, so each test varies exactly one thing.
func observed() Evidence {
	return Evidence{
		ID: "E1", Claim: "trim keeps inner spaces", Executed: "go test of the trim cases",
		Admit: "go test -v ./internal/text -run TestTrim -count=1", Inputs: "inputs a|b and ' x '",
		Observed: "3 cases pass", Normalize: `[0-9]+\.[0-9]+s`,
		Mutate:  "strings.TrimSpace(s) => s @ internal/text/trim.go:7",
		Control: "reverting the fix turns the case red", Reproduction: "rerun the Admit command", Label: "observado",
	}
}

// add-evidence writes the row the checker accepts and admit reads back: every value lands in its own column,
// a pipe inside a value stays inside its cell, and the pin columns stay empty for plan admit --record to fill.
func TestAddEvidenceWritesARowTheLedgerReadsBack(t *testing.T) {
	p := freshPlan(t)
	line, err := AddEvidence(p, observed())
	if err != nil {
		t.Fatalf("AddEvidence: %v", err)
	}
	raw, err := os.ReadFile(p)
	if err != nil {
		t.Fatal(err)
	}
	doc := string(raw)
	if problems := CheckDocument(doc); len(problems) != 0 {
		t.Fatalf("the row add-evidence wrote is rejected by its own checker: %v", problems)
	}
	if lines := strings.Split(doc, "\n"); line < 1 || line > len(lines) || !strings.HasPrefix(lines[line-1], "| E1 |") {
		t.Fatalf("the reported line %d is not the new row", line)
	}
	rows := Ledger(doc)
	if len(rows) != 1 {
		t.Fatalf("ledger rows = %d, want 1", len(rows))
	}
	got, want := rows[0], observed()
	for _, c := range []struct{ name, got, want string }{
		{"Claim", got.Claim, want.Claim}, {"Executed", got.Executed, want.Executed}, {"Admit", got.Admit, want.Admit},
		{"Inputs", got.Inputs, want.Inputs}, {"Observed", got.Observed, want.Observed},
		{"Normalize", got.Normalize, want.Normalize}, {"Mutate", got.Mutate, want.Mutate},
		{"Mutation", got.Mutation, want.Control}, {"Reproduction", got.Reproduction, want.Reproduction},
		{"Label", got.Label, "observado"}, {"Digest", got.Digest, ""}, {"Mode", got.Mode, ""}, {"Expect", got.Expect, ""},
	} {
		if c.got != c.want {
			t.Errorf("%s = %q, want %q", c.name, c.got, c.want)
		}
	}
	if got.Cells != got.HeaderCells {
		t.Fatalf("the row has %d cells and the header %d: a value split the row", got.Cells, got.HeaderCells)
	}

	// A second row lands under the first.
	second := observed()
	second.ID, second.Mutate, second.Expect = "E2", "", "fail"
	if _, err := AddEvidence(p, second); err != nil {
		t.Fatalf("a second row: %v", err)
	}
	raw, _ = os.ReadFile(p)
	if rows := Ledger(string(raw)); len(rows) != 2 || rows[1].ID != "E2" || rows[1].Expect != "fail" {
		t.Fatalf("second row not appended after the first: %+v", rows)
	}
}

// Every refusal leaves the file byte-identical, and a value the caller got wrong is a usage error (exit 2)
// while a plan that cannot take the row is the plan refusing it (exit 1).
func TestAddEvidenceRefusesWithoutMovingTheFile(t *testing.T) {
	cases := []struct {
		name  string
		plan  string
		edit  func(*Evidence)
		usage bool
		want  string
	}{
		{"a duplicate id", "", func(e *Evidence) {}, false, "already row"},
		{"a razonado label", "", func(e *Evidence) { e.ID, e.Label = "E9", "razonado" }, true, "observado"},
		{"an unknown expect", "", func(e *Evidence) { e.ID, e.Mutate, e.Expect = "E9", "", "red" }, true, "--expect"},
		{"expect fail beside a mutation", "", func(e *Evidence) { e.ID, e.Expect = "E9", "fail" }, true, "--mutate"},
		{"a normalize that does not compile", "", func(e *Evidence) { e.ID, e.Normalize = "E9", "(" }, true, "--normalize"},
		{"a mutation that does not parse", "", func(e *Evidence) { e.ID, e.Mutate = "E9", "no arrow here" }, true, "--mutate"},
		{"a newline in a value", "", func(e *Evidence) { e.ID, e.Observed = "E9", "one\ntwo" }, true, "newline"},
		{"an empty required value", "", func(e *Evidence) { e.ID, e.Claim = "E9", " " }, true, "--claim"},
		{"a placeholder id", "", func(e *Evidence) { e.ID = "-" }, true, "--id"},
		{"a value for a column the ledger lacks", addPlan(), func(e *Evidence) { e.ID = "E9" }, false, "Admit column"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			var p string
			if tc.plan == "" {
				p = freshPlan(t)
				if _, err := AddEvidence(p, observed()); err != nil {
					t.Fatal(err)
				}
			} else {
				p = write(t, t.TempDir(), "plan.md", tc.plan)
			}
			before, _ := os.ReadFile(p)
			e := observed()
			tc.edit(&e)
			_, err := AddEvidence(p, e)
			if err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("err = %v, want one naming %q", err, tc.want)
			}
			if errors.Is(err, ErrUsage) != tc.usage {
				t.Fatalf("usage error = %v, want %v: %v", errors.Is(err, ErrUsage), tc.usage, err)
			}
			after, _ := os.ReadFile(p)
			if string(after) != string(before) {
				t.Fatal("a refused row changed the plan")
			}
		})
	}
}

// An older ledger without the machine columns still takes a row that only fills the columns it has.
func TestAddEvidenceFillsOnlyTheColumnsAnOlderLedgerHas(t *testing.T) {
	p := write(t, t.TempDir(), "plan.md", addPlan())
	e := Evidence{ID: "E2", Claim: "it keeps the comma", Executed: "`node --test`", Inputs: "`a,b`",
		Observed: "2 fields", Control: "reverted -> red", Reproduction: "same input", Label: "observado"}
	if _, err := AddEvidence(p, e); err != nil {
		t.Fatalf("AddEvidence on an older ledger: %v", err)
	}
	raw, _ := os.ReadFile(p)
	if problems := CheckDocument(string(raw)); len(problems) != 0 {
		t.Fatalf("checker rejects the row: %v", problems)
	}
	if rows := Ledger(string(raw)); len(rows) != 2 || rows[1].ID != "E2" {
		t.Fatalf("rows = %+v", rows)
	}
}

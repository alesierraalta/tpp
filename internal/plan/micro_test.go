package plan

import (
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/alesierraalta/tsp/internal/assets"
)

const (
	microDecl = "Micro: internal/text/trim.go · touches none"
	// microObserved is the pinning test observed red then green; its Reproduction cell cites the target.
	microObserved = "| E1 | the pinning test holds the trim | go test ./internal/text -run TestTrim | go test ./internal/text -run TestTrim | `\" a \"` | ok | | | | | | reverted → red | internal/text/trim.go:7 | observado |\n"
	// microMutated is the one mutation the pinning test kills.
	microMutated = "| E2 | the pinning test kills the mutation | go test ./internal/text -run TestTrim | go test ./internal/text -run TestTrim | | | | | | strings.TrimSpace(s) => s @ internal/text/trim.go:7 | | edit → red | plan admit --sandbox | observado |\n"
)

// microPlan fills the shipped micro template the way an author does: decl replaces the instruction line
// and rows go under the Evidence ledger header. Building on the template keeps every case honest about
// what `plan init --micro` actually writes.
func microPlan(t *testing.T, decl, rows, extra string) string {
	t.Helper()
	raw, err := fs.ReadFile(assets.Skills(), MicroTemplatePath)
	if err != nil {
		t.Fatal(err)
	}
	lines := strings.Split(string(raw), "\n")
	replaced := false
	for i, l := range lines {
		if strings.HasPrefix(l, "Declare the run by replacing this line") {
			lines[i] = decl
			replaced = true
		}
	}
	if !replaced {
		t.Fatal("the micro template drifted: it no longer carries the declaration instruction line")
	}
	return strings.TrimRight(strings.Join(lines, "\n"), "\n") + "\n" + rows + extra
}

func TestMicroPlanIsActivatedWhenItKeepsItsEvidence(t *testing.T) {
	doc := microPlan(t, microDecl, microObserved+microMutated, "")
	if problems := CheckDocument(doc); len(problems) != 0 {
		t.Fatalf("a valid micro plan was refused: %v", problems)
	}
	if !MicroActivated(doc) {
		t.Fatal("a valid micro plan must be activated")
	}
}

// A Micro declaration that drops what it keeps is theatre: each breach is named, and the plan is not a micro plan.
func TestMicroPlanRefusesEveryBreach(t *testing.T) {
	noLedger := microPlan(t, microDecl+"\nThe target is internal/text/trim.go:7.", "", "")
	noLedger = noLedger[:strings.Index(noLedger, "## Evidence ledger")]
	unlabelled := strings.Replace(microMutated, "| observado |", "| |", 1)
	cases := []struct {
		name string
		doc  string
		want string
	}{
		{"no Evidence ledger", noLedger, "Evidence ledger table"},
		{"no observado row", microPlan(t, microDecl, unlabelled, ""), "observado"},
		{"no Mutate row", microPlan(t, microDecl, microObserved, ""), "Mutate"},
		{"a Layer matrix", microPlan(t, microDecl, microObserved+microMutated, "\n## Layer matrix\n\n| Layer | Skill | Scope | Status | Run |\n|---|---|---|---|---|\n"), "Layer matrix"},
		{"also Light", microPlan(t, microDecl+"\nLight: internal/text/trim.go · touches cli", microObserved+microMutated, ""), "Light"},
		{"uncorroborated target", microPlan(t, "Micro: internal/text/other.go · touches none", microObserved+microMutated, ""), "corroborates"},
		{"classes other than none", microPlan(t, "Micro: internal/text/trim.go · touches persistence", microObserved+microMutated, ""), "none"},
		{"malformed shape", microPlan(t, "Micro: internal/text/trim.go", microObserved+microMutated, ""), "must read"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			joined := strings.Join(CheckDocument(tc.doc), "\n")
			if !strings.Contains(joined, "Micro") || !strings.Contains(joined, tc.want) {
				t.Fatalf("problems = %q, want a Micro breach naming %q", joined, tc.want)
			}
			if MicroActivated(tc.doc) {
				t.Fatal("a micro plan with a breach must not be activated")
			}
			if g, _ := GapsIn(tc.doc); tc.name != "a Layer matrix" && (!g.NoLayerMatrix || !strings.Contains(g.Report(), "never planned")) {
				t.Fatalf("a refused micro plan keeps reading as never planned: %+v", g)
			}
		})
	}
}

// A micro plan trades the layer sweep for a well-formed record, so a plan check refuses for any reason is not a
// micro plan either: a razonado mutation row breaks the ledger contract, and it must not silence the gate.
func TestMicroPlanThatPlanCheckRefusesIsNotActivated(t *testing.T) {
	doc := microPlan(t, microDecl, microObserved+strings.Replace(microMutated, "| observado |", "| razonado |", 1), "")
	if len(CheckDocument(doc)) == 0 {
		t.Fatal("a razonado ledger row must be refused by plan check")
	}
	if MicroActivated(doc) {
		t.Fatal("a micro plan that plan check refuses must not be activated")
	}
	if g, _ := GapsIn(doc); !g.NoLayerMatrix || !g.Any() {
		t.Fatalf("a refused micro plan must keep owing breadth: %+v", g)
	}
}

func TestGapsOweNoBreadthForAMicroPlan(t *testing.T) {
	doc := microPlan(t, microDecl, microObserved+microMutated, "")
	for _, run := range []string{"", "fix-trim"} {
		g, err := GapsForRun(doc, run)
		if err != nil {
			t.Fatal(err)
		}
		if g.Any() || g.NoLayerMatrix {
			t.Fatalf("run %q: a micro plan owes no breadth: %+v", run, g)
		}
		if r := g.Report(); !strings.Contains(r, "micro plan: internal/text/trim.go, no breadth owed") || strings.Contains(r, "never planned") {
			t.Fatalf("run %q: report = %q", run, r)
		}
	}
}

func TestInitMicroWritesTheMicroTemplateAndRefusesToOverwrite(t *testing.T) {
	p := filepath.Join(t.TempDir(), "docs", "testing", "test-plan.md")
	if err := InitMicro(p, false); err != nil {
		t.Fatal(err)
	}
	got, err := os.ReadFile(p)
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"Micro: <file path> · touches none", "## Findings", "## Evidence ledger"} {
		if !strings.Contains(string(got), want) {
			t.Fatalf("micro template missing %q", want)
		}
	}
	if strings.Contains(string(got), "## Layer matrix") {
		t.Fatal("a micro template carries no layer matrix")
	}
	if err := InitMicro(p, false); err == nil {
		t.Fatal("an existing plan must not be silently overwritten")
	}
	if err := InitMicro(p, true); err != nil {
		t.Fatal(err)
	}
}

// The micro template shares its machine lines with the main template, so a plan started micro and later
// widened keeps one Findings and one ledger contract.
func TestMicroTemplateSharesTheMainTemplateTables(t *testing.T) {
	main, err := fs.ReadFile(assets.Skills(), TemplatePath)
	if err != nil {
		t.Fatal(err)
	}
	micro, err := fs.ReadFile(assets.Skills(), MicroTemplatePath)
	if err != nil {
		t.Fatal(err)
	}
	for _, prefix := range []string{"| Id | Finding", "| Id | Claim", "Statuses: open"} {
		want := lineWithPrefix(string(main), prefix)
		if want == "" || lineWithPrefix(string(micro), prefix) != want {
			t.Fatalf("the micro template's %q line drifted from the main template's %q", prefix, want)
		}
	}
}

func lineWithPrefix(doc, prefix string) string {
	for _, l := range strings.Split(doc, "\n") {
		if strings.HasPrefix(l, prefix) {
			return l
		}
	}
	return ""
}

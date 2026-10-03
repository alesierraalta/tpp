package plan

import (
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/alesierraalta/tpp/internal/assets"
)

func write(t *testing.T, dir, name, content string) string {
	t.Helper()
	p := filepath.Join(dir, name)
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(p, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
	return p
}

func TestFindingsStatusVocabularyMatchesShippedTemplate(t *testing.T) {
	data, err := fs.ReadFile(assets.Skills(), TemplatePath)
	if err != nil {
		t.Fatal(err)
	}

	var statusLine string
	inFindings := false
	for _, line := range strings.Split(string(data), "\n") {
		switch {
		case line == "## Findings":
			inFindings = true
		case inFindings && strings.HasPrefix(line, "## "):
			inFindings = false
		case inFindings && strings.HasPrefix(line, "Statuses:"):
			statusLine = strings.TrimSuffix(strings.TrimSpace(strings.TrimPrefix(line, "Statuses:")), ".")
		}
	}
	if statusLine == "" {
		t.Fatal("the Findings section has no Statuses line")
	}

	documented := make(map[string]bool)
	for _, status := range strings.Split(statusLine, " · ") {
		documented[strings.TrimSpace(status)] = true
	}
	accepted := make(map[string]bool)
	for _, status := range strings.Split(FindingsStatusList, ", ") {
		accepted[strings.TrimSpace(status)] = true
	}
	// FindingsStatusList is the human-facing list; findingsStatuses is what Check enforces. A status
	// added to one and not the other is the same drift one door along.
	enforced := map[string]bool{}
	for status := range findingsStatuses {
		enforced[status] = true
	}
	if !reflect.DeepEqual(accepted, enforced) {
		t.Fatalf("the check enforces %v, but FindingsStatusList documents %q", enforced, FindingsStatusList)
	}
	if !reflect.DeepEqual(documented, accepted) {
		t.Fatalf("template statuses %q do not match FindingsStatusList %q", statusLine, FindingsStatusList)
	}
}

// Init writes the shipped skeleton so the flow fills tables instead of inventing a structure.
func TestInitWritesTheTemplateAndRefusesToOverwrite(t *testing.T) {
	dir := t.TempDir()
	p := filepath.Join(dir, "docs", "testing", "test-plan.md")
	if err := Init(p, false); err != nil {
		t.Fatal(err)
	}
	got, err := os.ReadFile(p)
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"## Findings", "## Evidence ledger", "| Id | Finding"} {
		if !strings.Contains(string(got), want) {
			t.Fatalf("template missing %q", want)
		}
	}
	if err := Init(p, false); err == nil {
		t.Fatal("an existing plan must not be silently overwritten")
	}
	write(t, dir, "docs/testing/test-plan.md", "mine")
	if err := Init(p, true); err != nil {
		t.Fatal(err)
	}
	if data, _ := os.ReadFile(p); string(data) == "mine" {
		t.Fatal("--force must rewrite the file")
	}
}

// replaceFixture splices row in after the exact template literal old. A bare strings.Replace over a
// template literal has no guard of its own: when the shipped template drifts, the Replace no-ops and
// the test goes vacuously green while asserting nothing. This fails loudly instead, naming the drift
// and the fixture that has to follow it.
func replaceFixture(t *testing.T, doc, what, old, row string) string {
	t.Helper()
	out := strings.Replace(doc, old, old+row, 1)
	if out == doc {
		t.Fatalf("the template drifted: %s no longer holds the literal this fixture splices into, so the fixture must be updated to track the template (literal was %q)", what, old)
	}
	return out
}

func TestCheckAcceptsACompliantPlan(t *testing.T) {
	dir := t.TempDir()
	p := filepath.Join(dir, "plan.md")
	if err := Init(p, false); err != nil {
		t.Fatal(err)
	}
	body, _ := os.ReadFile(p)
	plan := replaceFixture(t, string(body), "the Findings header",
		"| Id | Finding (path:line, one line) | Severity (consequence class) | Data safe? | Evidence id | Pinning test (suite path :: test name) | Status | Verdict by / date | Reason | Cited-files fingerprint at verdict |\n|---|---|---|---|---|---|---|---|---|---|\n",
		"| F1 | `src/a.js:5` drops a quoted comma | data loss | yes | E1 | tests/a.test.js :: keeps a quoted comma | fixed | me / 2026-09-10 | - | abc1234 |\n")
	plan = replaceFixture(t, plan, "the Evidence ledger header",
		"| Id | Claim | Executed | Admit | Inputs and parameters | Observed | Digest | Normalize | Mode | Mutate | Expect | Mutation or negative control → result | Reproduction | Label (`observado` / `razonado`, literal) |\n|---|---|---|---|---|---|---|---|---|---|---|---|---|---|\n",
		"| E1 | it drops the comma | `node --test` | node --test | `a,\"b,c\"` | 3 fields | sha256:7eada7a897497315d39d2541f5058a9631e80828245781b3c9c96205d9d759ed | | | | | reverted → red | same input | observado |\n")
	if plan == string(body) {
		t.Fatal("the fixture replaced nothing, so this test would read the untouched template as a compliant plan")
	}
	if err := os.WriteFile(p, []byte(plan), 0o644); err != nil {
		t.Fatal(err)
	}
	problems, err := Check(p)
	if err != nil {
		t.Fatal(err)
	}
	if len(problems) != 0 {
		t.Fatalf("compliant plan rejected: %v", problems)
	}
}

func TestCheckNamesEveryContractBreach(t *testing.T) {
	cases := []struct {
		name    string
		plan    string
		wantSub string
	}{
		{
			name:    "findings written as prose",
			plan:    "## Findings\n\n### F1 — something broke\n- Severity: high\n\n## Evidence ledger\n\n| Id | Claim |\n|---|---|\n",
			wantSub: "not a table",
		},
		{
			name: "a finding that cites no path",
			plan: header + "| F1 | `formatCents` rounds a tie down | M | yes | E1 | t.js :: x | fixed | me | - | - |\n" +
				ledger + "| E1 | c | cmd | i | o | m | r | observado |\n",
			wantSub: "cites no path:line",
		},
		{
			name: "a finding citing an evidence id that does not exist",
			plan: header + "| F1 | `src/a.js:5` x | M | yes | E9 | t.js :: x | fixed | me | - | - |\n" +
				ledger + "| E1 | c | cmd | i | o | m | r | observado |\n",
			wantSub: "cites evidence E9",
		},
		{
			name: "a confirmed finding with no pinning test",
			plan: header + "| F1 | `src/a.js:5` x | M | yes | E1 |  | fixed | me | - | - |\n" +
				ledger + "| E1 | c | cmd | i | o | m | r | observado |\n",
			wantSub: "names no pinning test",
		},
		{
			name: "a razonado row inside the evidence ledger",
			plan: header + "| F1 | `src/a.js:5` x | M | yes | E1 | t.js :: x | fixed | me | - | - |\n" +
				ledger + "| E1 | c | cmd | i | o | m | r | razonado |\n",
			wantSub: "labelled razonado",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			dir := t.TempDir()
			p := write(t, dir, "plan.md", tc.plan)
			problems, err := Check(p)
			if err != nil {
				t.Fatal(err)
			}
			if !strings.Contains(strings.Join(problems, "\n"), tc.wantSub) {
				t.Fatalf("problems = %v, want one containing %q", problems, tc.wantSub)
			}
		})
	}
}

// The fingerprint cell is what lets a settled verdict be re-checked against the files it cited, so it holds
// a `fingerprint.sh` digest, one or more git SHAs, or a placeholder that says none is recorded yet. Free text
// such as a test name passes for a fingerprint and re-checks nothing, so it is refused against its row.
func TestCheckReadsTheFindingsFingerprintCell(t *testing.T) {
	digest := strings.Repeat("0123456789abcdef", 4)
	cases := []struct {
		cell   string
		accept bool
	}{
		{digest, true},
		{"953908a", true},
		{"`953908a`", true},
		{"b85f035, f016acd", true},
		{"-", true},
		{"pending", true},
		{"pending commit", true},
		{"", true},
		{"`{{FP_PARSE}}`", true},
		{"{{TODO}}", false},
		{"TestFoo", false},
		{"pilot fingerprint", false},
		{"abc12", false},
		{strings.ToUpper(digest), false},
	}
	for _, tc := range cases {
		t.Run(tc.cell, func(t *testing.T) {
			plan := header + "| F1 | `src/a.js:5` x | M | yes | E1 | t.js :: x | fixed | me | - | " + tc.cell + " |\n" +
				ledger + "| E1 | c | cmd | i | o | m | r | observado |\n"
			problems := strings.Join(CheckDocument(plan), "\n")
			if tc.accept {
				if problems != "" {
					t.Fatalf("fingerprint %q refused: %s", tc.cell, problems)
				}
				return
			}
			if !strings.Contains(problems, "line 5: finding F1") || !strings.Contains(problems, "fingerprint") {
				t.Fatalf("fingerprint %q: problems = %q, want a breach naming line 5, finding F1 and the fingerprint cell", tc.cell, problems)
			}
		})
	}
}

// A data row whose cell count disagrees with its own table's header was cut by an unescaped `|`, so every
// column to the right of the cut shifts and the row declares one thing while carrying another. Both
// directions earn a breach; the separator and placeholder rows table already skips earn none.
func TestCheckReportsRowsWhoseCellsDoNotMatchTheirHeader(t *testing.T) {
	const finding = "| F1 | `src/a.js:5` x | M | yes | E1 | t.js :: x | fixed | me | - | - |\n"
	const ledgerRow = "| E1 | c | cmd | i | o | m | r | observado |\n"
	cases := []struct {
		name     string
		plan     string
		wantSubs []string // every substring the breach must carry; empty means the plan passes
	}{
		{
			name: "a compliant plan owes no breach",
			plan: header + finding + ledger + ledgerRow,
		},
		{
			name:     "a row with more cells than its header",
			plan:     header + "| F1 | `src/a.js:5` gate|sync | M | yes | E1 | t.js :: x | fixed | me | - | - |\n" + ledger + ledgerRow,
			wantSubs: []string{"Findings", "F1", "11 cells", "header's 10", "unescaped"},
		},
		{
			name:     "a row with fewer cells than its header",
			plan:     header + "| F1 | `src/a.js:5` x | M | yes |\n" + ledger + ledgerRow,
			wantSubs: []string{"Findings", "F1", "4 cells", "header's 10"},
		},
		{
			name: "a separator row is not a data row",
			plan: header + finding + "|---|---|---|---|\n" + ledger + ledgerRow,
		},
		{
			name: "a placeholder row is not a data row",
			plan: header + finding + "| - | | |\n" + ledger + ledgerRow,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			p := write(t, t.TempDir(), "plan.md", tc.plan)
			problems, err := Check(p)
			if err != nil {
				t.Fatal(err)
			}
			if len(tc.wantSubs) == 0 {
				if len(problems) != 0 {
					t.Fatalf("compliant plan rejected: %v", problems)
				}
				return
			}
			joined := strings.Join(problems, "\n")
			for _, want := range tc.wantSubs {
				if !strings.Contains(joined, want) {
					t.Fatalf("problems = %v, want one containing %q", problems, want)
				}
			}
		})
	}
}

// An example table inside a code fence is documentation: the rows the checker validates are the ones outside
// it. A second reader with rules of its own invented a breach about a table nobody reads.
func TestCheckDoesNotBlameRowsInsideAFencedExample(t *testing.T) {
	const finding = "| F1 | `src/a.js:5` x | M | yes | E1 | t.js :: x | fixed | me | - | - |\n"
	const ledgerRow = "| E1 | c | cmd | i | o | m | r | observado |\n"
	plan := header + finding + ledger + ledgerRow +
		"\n## How a row looks\n\n```markdown\n| a | b | c |\n|---|---|---|\n| only one |\n```\n"
	p := write(t, t.TempDir(), "plan.md", plan)
	problems, err := Check(p)
	if err != nil {
		t.Fatal(err)
	}
	if len(problems) != 0 {
		t.Fatalf("a fenced example owes no breach: %v", problems)
	}
}

// A blank line inside a table does not restart it, which is what the row readers already know. Reading the
// document with a private loop made this row the next table's header, so the count mismatch that moves every
// column to its right was never reported and only its symptoms were.
func TestCheckBlamesTheCellCountWhenABlankLineSplitsTheTable(t *testing.T) {
	const ledgerRow = "| E1 | c | cmd | i | o | m | r | observado |\n"
	plan := header + "\n| F1 | `src/a.js:5` x | M | yes |\n" + ledger + ledgerRow
	p := write(t, t.TempDir(), "plan.md", plan)
	problems, err := Check(p)
	if err != nil {
		t.Fatal(err)
	}
	joined := strings.Join(problems, "\n")
	if !strings.Contains(joined, "4 cells") || !strings.Contains(joined, "header's 10") {
		t.Fatalf("the count mismatch is the breach that explains the rest: %v", problems)
	}
}

// A line that is not a row closes the block, not the region: the next `|` line of the same section opens
// another table, and a malformed row there earns its breach. A walk that stopped at the first close would
// read the second table and report nothing, which is the drift this test is here to catch.
func TestCheckContinuesTheRegionWalkAfterABlockCloses(t *testing.T) {
	const finding = "| F1 | `src/a.js:5` x | M | yes | E1 | t.js :: x | fixed | me | - | - |\n"
	const ledgerRow = "| E1 | c | cmd | i | o | m | r | observado |\n"
	plan := header + finding + ledger + ledgerRow +
		"\n## How a row looks\n\n| a | b |\n|---|---|\n| 1 | 2 |\n\nprose closes the block\n\n| c | d |\n|---|---|\n| only one |\n"
	p := write(t, t.TempDir(), "plan.md", plan)
	problems, err := Check(p)
	if err != nil {
		t.Fatal(err)
	}
	joined := strings.Join(problems, "\n")
	if len(problems) != 1 || !strings.Contains(joined, `the How a row looks table row "only one" has 1 cells against the header's 2`) {
		t.Fatalf("the table after the close is still read: %v", problems)
	}
}

// Text before the first heading belongs to no region: no reader reads a table there, so the walk does not
// measure one either, and a malformed preamble row earns no breach. A walk that started measuring at the
// top of the document would refuse a plan whose tables are all well formed.
func TestCheckIgnoresTablesBeforeTheFirstHeading(t *testing.T) {
	const finding = "| F1 | `src/a.js:5` x | M | yes | E1 | t.js :: x | fixed | me | - | - |\n"
	const ledgerRow = "| E1 | c | cmd | i | o | m | r | observado |\n"
	plan := "| x | y |\n|---|---|\n| only one |\n\n" + header + finding + ledger + ledgerRow
	p := write(t, t.TempDir(), "plan.md", plan)
	problems, err := Check(p)
	if err != nil {
		t.Fatal(err)
	}
	if len(problems) != 0 {
		t.Fatalf("the preamble is not a region: %v", problems)
	}
}

// Regions are cut at every heading, fence or not: a `## ` line inside a fenced example still ends the
// region above it and opens one of its own, so the example's table is read as a real table and its short
// row is blamed. That is the reader's behavior today, pinned here so making the walk fence-aware is a
// deliberate change and never a silent one.
func TestCheckTreatsAHeadingInsideAFenceAsARegionBoundary(t *testing.T) {
	const finding = "| F1 | `src/a.js:5` x | M | yes | E1 | t.js :: x | fixed | me | - | - |\n"
	const ledgerRow = "| E1 | c | cmd | i | o | m | r | observado |\n"
	plan := header + finding + ledger + ledgerRow +
		"\n## How a row looks\n\n```markdown\n## Example\n| a | b |\n|---|---|\n| only one |\n```\n"
	p := write(t, t.TempDir(), "plan.md", plan)
	problems, err := Check(p)
	if err != nil {
		t.Fatal(err)
	}
	joined := strings.Join(problems, "\n")
	if len(problems) != 1 || !strings.Contains(joined, `the Example table row "only one" has 1 cells against the header's 2`) {
		t.Fatalf("a heading inside a fence still opens a region: %v", problems)
	}
}

// A placeholder row is not a data row: a row whose first cell the placeholder vocabulary reads (empty,
// `-`, `n/a`) is skipped before its cells are counted, so the short placeholder row earns no breach. The
// malformed data row beside it still earns one, which is what proves the table itself was read. `n/a` is
// the first cell on purpose: a row of dashes and blanks is already skipped as a separator, so it would pin
// the separator rule and leave the placeholder exemption unproven.
func TestCheckExemptsPlaceholderRowsFromTheCellCount(t *testing.T) {
	const finding = "| F1 | `src/a.js:5` x | M | yes | E1 | t.js :: x | fixed | me | - | - |\n"
	const ledgerRow = "| E1 | c | cmd | i | o | m | r | observado |\n"
	plan := header + finding + ledger + ledgerRow +
		"\n## How a row looks\n\n| a | b | c |\n|---|---|---|\n| n/a |  |\n| x | y |\n"
	p := write(t, t.TempDir(), "plan.md", plan)
	problems, err := Check(p)
	if err != nil {
		t.Fatal(err)
	}
	joined := strings.Join(problems, "\n")
	if len(problems) != 1 || !strings.Contains(joined, `the How a row looks table row "x" has 2 cells against the header's 3`) {
		t.Fatalf("the placeholder row is exempt and the data row is not: %v", problems)
	}
}

// scopedPlan is the smallest compliant plan with a ranked target and a layer matrix, so a test varies
// only what the scoped-run rule reads: the `Light:` declaration, whether the plan corroborates the
// target it names, and the reason a skipped layer carries.
func scopedPlan(light, scope string) string {
	return light +
		"## Findings\n\n" +
		"| Id | Finding | Severity | Data safe? | Evidence id | Pinning test | Status | Verdict by / date | Reason | Fingerprint |\n" +
		"|---|---|---|---|---|---|---|---|---|---|\n\n" +
		"## Ranked targets\n\n" +
		"| Target | Blast radius | Churn / past fixes | Consequence class | Existing evidence | Altitude | Target rung | Run |\n" +
		"|---|---|---|---|---|---|---|---|\n" +
		"| cli flags | `internal/cli/flags.go:12` | Unknown | incorrect output | | | |  |\n\n" +
		"## Layer matrix\n\n" +
		"| Layer | Skill | Scope | Status | Run |\n|---|---|---|---|---|\n" +
		"| Persistence and migrations | `database-persistence-testing` | " + scope + " | n/a |  |\n"
}

const lightLine = "Light: cli flags · touches cli\n\n"

// The cheap half of the scoped-run decision is the declaration, and this is the one breach it adds:
// an `n/a` row in a plan that declares `Light:` has to say why the layer was left out. Whether the
// change was really bounded stays with the operator and the plan's reader.
func TestCheckLightPlanOwesAReasonForEverySkippedLayer(t *testing.T) {
	cases := []struct {
		name  string
		light string
		scope string
		want  string // the layer the breach must name; empty means the plan passes
	}{
		{
			name:  "a Light plan that skips a layer without a reason",
			light: lightLine,
			scope: "",
			want:  "Persistence and migrations",
		},
		{
			name:  "the same plan with a reason",
			light: lightLine,
			scope: "no persistence in the touched diff",
		},
		{
			name:  "a plan with no Light line is checked as it always was",
			light: "",
			scope: "",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			p := write(t, t.TempDir(), "plan.md", scopedPlan(tc.light, tc.scope))
			problems, err := Check(p)
			if err != nil {
				t.Fatal(err)
			}
			joined := strings.Join(problems, "\n")
			if tc.want == "" {
				if len(problems) != 0 {
					t.Fatalf("compliant plan rejected: %v", problems)
				}
				return
			}
			if !strings.Contains(joined, tc.want) {
				t.Fatalf("problems = %v, want one naming %q", problems, tc.want)
			}
			if !strings.Contains(joined, "Scope") {
				t.Fatalf("the breach must point at the Scope cell: %v", problems)
			}
		})
	}
}

// A `Light:` line is a claim the plan owes evidence for, not a header the check takes on faith: the
// shipped skeleton with the line added names a target the plan never ranked, so the check says so.
func TestCheckRejectsALightHeaderThePlanNeverRanked(t *testing.T) {
	dir := t.TempDir()
	p := filepath.Join(dir, "plan.md")
	if err := Init(p, false); err != nil {
		t.Fatal(err)
	}
	body, err := os.ReadFile(p)
	if err != nil {
		t.Fatal(err)
	}
	scoped := strings.Replace(string(body), "Baseline:", lightLine+"Baseline:", 1)
	if scoped == string(body) {
		t.Fatal("the template header moved: the Light line was never placed")
	}
	if err := os.WriteFile(p, []byte(scoped), 0o644); err != nil {
		t.Fatal(err)
	}
	problems, err := Check(p)
	if err != nil {
		t.Fatal(err)
	}
	if joined := strings.Join(problems, "\n"); !strings.Contains(joined, "cli flags") {
		t.Fatalf("a Light header nothing corroborates must name the target it claims: %v", problems)
	}
}

// The declaration is the cheap half of the scoped-run decision, so the check reads its shape and that
// the plan corroborates the target it names. Whether the change was really bounded stays with the
// operator and the plan's reader, never with this check.
func TestCheckValidatesTheLightDeclaration(t *testing.T) {
	cases := []struct {
		name     string
		light    string
		want     string // a substring of the breach; empty means the plan passes
		wantOnly bool   // the breach must stand alone: one problem, no corroboration noise
	}{
		{
			name:  "a declaration whose target the plan ranked passes",
			light: lightLine,
		},
		{
			name:  "a target the plan cites as path:line corroborates itself",
			light: "Light: internal/cli/flags.go · touches cli\n\n",
		},
		{
			name:  "a declaration without the separator",
			light: "Light: cli flags\n\n",
			want:  "must read",
		},
		{
			name:     "a declaration whose blast radius is a placeholder",
			light:    "Light: n/a · touches cli\n\n",
			want:     "names no blast radius",
			wantOnly: true,
		},
		{
			name:     "a declaration whose touched classes are a placeholder",
			light:    "Light: cli flags · touches none\n\n",
			want:     "names no touched classes",
			wantOnly: true,
		},
		{
			name:  "a declaration with no touched classes",
			light: "Light: cli flags · touches  \n\n",
			want:  "names no touched classes",
		},
		{
			name:  "a target the plan never ranked and never cited",
			light: "Light: ledger rewrites · touches persistence\n\n",
			want:  "corroborates",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			p := write(t, t.TempDir(), "plan.md", scopedPlan(tc.light, "no persistence in the touched diff"))
			problems, err := Check(p)
			if err != nil {
				t.Fatal(err)
			}
			if tc.want == "" {
				if len(problems) != 0 {
					t.Fatalf("compliant plan rejected: %v", problems)
				}
				return
			}
			if tc.wantOnly && len(problems) != 1 {
				t.Fatalf("problems = %v, want the one breach and nothing else", problems)
			}
			if joined := strings.Join(problems, "\n"); !strings.Contains(joined, tc.want) {
				t.Fatalf("problems = %v, want one containing %q", problems, tc.want)
			}
			if tc.want == "corroborates" && !strings.Contains(strings.Join(problems, "\n"), "ledger rewrites") {
				t.Fatalf("the breach must name the target it could not corroborate: %v", problems)
			}
		})
	}
}

// Activation is the narrow reading of a validated declaration. A detected defect, an ordinary plan, or
// a line that merely looks like a declaration is never activation; the bench records what this says.
func TestLightActivatedIsNarrow(t *testing.T) {
	cases := []struct {
		name string
		plan string
		want bool
	}{
		{
			name: "a validated declaration activates",
			plan: scopedPlan(lightLine, "no persistence in the touched diff"),
			want: true,
		},
		{
			name: "no declaration never activates",
			plan: scopedPlan("", "no persistence in the touched diff"),
		},
		{
			name: "a declaration without the declared shape does not activate",
			plan: scopedPlan("Light: cli flags\n\n", "no persistence in the touched diff"),
		},
		{
			name: "an uncorroborated target does not activate",
			plan: scopedPlan("Light: ledger rewrites · touches persistence\n\n", "no persistence in the touched diff"),
		},
		{
			name: "a skipped layer with no reason does not activate",
			plan: scopedPlan(lightLine, ""),
		},
		{
			name: "prose that names the declaration is not a declaration",
			plan: "A plan that declares `Light:` owes a reason per layer.\n" + scopedPlan("", "no persistence in the touched diff"),
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := LightActivated(tc.plan); got != tc.want {
				t.Fatalf("LightActivated = %v, want %v", got, tc.want)
			}
		})
	}
}

// A Light breach is located like every other row-level breach: a declaration that breaks its own shape
// names the line it sits on, so the reader opens the plan where the fix goes instead of grepping for the
// label the message quotes.
func TestCheckNamesTheLineOfTheLightDeclaration(t *testing.T) {
	cases := []struct {
		name   string
		prefix string
		light  string
		want   string
	}{
		{
			name:  "a declaration without the separator",
			light: "Light: cli flags\n\n",
			want:  "line 1: the Light declaration must read",
		},
		{
			name:  "a declaration whose blast radius is a placeholder",
			light: "Light: n/a · touches cli\n\n",
			want:  "line 1: the Light declaration names no blast radius",
		},
		{
			name:  "a declaration whose touched classes are a placeholder",
			light: "Light: cli flags · touches none\n\n",
			want:  "line 1: the Light declaration names no touched classes",
		},
		{
			name:  "a target the plan never ranked",
			light: "Light: ledger rewrites · touches persistence\n\n",
			want:  "line 1: the Light declaration names target ledger rewrites, which no Ranked-target row",
		},
		{
			name:   "a declaration below the plan header",
			prefix: "# Test plan\n\n",
			light:  "Light: ledger rewrites · touches persistence\n\n",
			want:   "line 3: the Light declaration names target ledger rewrites, which no Ranked-target row",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			p := write(t, t.TempDir(), "plan.md", tc.prefix+scopedPlan(tc.light, "no persistence in the touched diff"))
			problems, err := Check(p)
			if err != nil {
				t.Fatal(err)
			}
			if joined := strings.Join(problems, "\n"); !strings.Contains(joined, tc.want) {
				t.Fatalf("problems = %v, want one containing %q", problems, tc.want)
			}
		})
	}
}

// The layer a Light plan left out is named by the line its own row sits on, not by the table that holds
// it: a second `n/a` row must not inherit the line of the first one.
func TestCheckNamesTheLineOfTheLayerItBlames(t *testing.T) {
	doc := scopedPlan(lightLine, "")
	// A compliant layer first, so the breach has a row above it to be confused with.
	doc = strings.Replace(doc, "| Persistence and migrations |", "| API contracts | `none` | reviewed in place | done |\n| Persistence and migrations |", 1)
	row := "| Persistence and migrations |"
	want := fmt.Sprintf("line %d: layer Persistence and migrations is out of scope", strings.Count(doc[:strings.Index(doc, row)], "\n")+1)
	p := write(t, t.TempDir(), "plan.md", doc)
	problems, err := Check(p)
	if err != nil {
		t.Fatal(err)
	}
	if joined := strings.Join(problems, "\n"); !strings.Contains(joined, want) {
		t.Fatalf("problems = %v, want one containing %q", problems, want)
	}
}

func TestCheckOnAMissingFile(t *testing.T) {
	if _, err := Check(filepath.Join(t.TempDir(), "nope.md")); err == nil {
		t.Fatal("a missing plan must be an error, not a clean report")
	}
}

// The repository's own plan is the file this check exists to keep honest. It is read through the same
// os.ReadFile path Check uses, and the only excuse for passing without reading it is that it is genuinely
// absent, so a missing file can never masquerade as a clean report.
func TestRepositoryPlanIsWellFormed(t *testing.T) {
	path := filepath.Join("..", "..", "docs", "testing", "test-plan.md")
	if _, err := os.ReadFile(path); err != nil {
		if os.IsNotExist(err) {
			t.Skipf("the repository plan %s is absent, so there is no checked copy to assert against", path)
		}
		t.Fatal(err)
	}
	problems, err := Check(path)
	if err != nil {
		t.Fatal(err)
	}
	if len(problems) != 0 {
		t.Fatalf("the repository's own plan reports breaches: %v", problems)
	}
}

const header = "## Findings\n\n| Id | Finding | Severity | Data safe? | Evidence id | Pinning test | Status | Verdict by / date | Reason | Fingerprint |\n|---|---|---|---|---|---|---|---|---|---|\n"
const ledger = "\n## Evidence ledger\n\n| Id | Claim | Executed | Inputs | Observed | Mutation | Reproduction | Label |\n|---|---|---|---|---|---|---|---|\n"

// A breach that names a row has to say where the row is. `path: finding F1 cites no path:line` left the
// reader grepping the file for the row the check was talking about, which is the cost the checker
// exists to remove.
func TestCheckNamesTheLineOfTheRowItBlames(t *testing.T) {
	// `header` puts the column header on line 3 and the separator on line 4, so the first data row is
	// line 5: the number the breach has to carry.
	plan := header + "| F1 | `src/a.js:5` x | M | yes | E9 | t.js :: x | fixed | me | - | - |\n" +
		ledger + "| E1 | c | cmd | i | o | m | r | observado |\n"
	p := write(t, t.TempDir(), "plan.md", plan)
	problems, err := Check(p)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(strings.Join(problems, "\n"), "line 5: finding F1 cites evidence E9") {
		t.Fatalf("a row breach must name its line: %v", problems)
	}
}

// A blank line inside a table used to end the block: every row under it was dropped and the plan still
// reported `well formed`. A blank is a separator that lost its pipes, not the end of the table.
func TestCheckReadsARowSeparatedFromTheTableByABlankLine(t *testing.T) {
	// The blank sits on line 5; the row it used to hide is line 6.
	plan := header + "\n| F1 | `src/a.js:5` x | M | yes | E9 | t.js :: x | fixed | me | - | - |\n" +
		ledger + "| E1 | c | cmd | i | o | m | r | observado |\n"
	p := write(t, t.TempDir(), "plan.md", plan)
	problems, err := Check(p)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(strings.Join(problems, "\n"), "line 6: finding F1 cites evidence E9") {
		t.Fatalf("a row under a blank line is still a row: %v", problems)
	}
}

// The Findings table is not the only one the check reads back: a ledger row's breach names the ledger line,
// so the line anchor covers both tables the checker reads.
func TestCheckNamesTheLineOfTheLedgerRowItBlames(t *testing.T) {
	// The Findings row is line 5; the razonado ledger row is line 11.
	plan := header + "| F1 | `src/a.js:5` x | M | yes | E1 | t.js :: x | fixed | me | - | - |\n" +
		ledger + "| E1 | c | cmd | i | o | m | r | razonado |\n"
	p := write(t, t.TempDir(), "plan.md", plan)
	problems, err := Check(p)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(strings.Join(problems, "\n"), "line 11: evidence E1 is labelled razonado") {
		t.Fatalf("a ledger breach must name its line: %v", problems)
	}
}

// The line anchor must not narrow the checker: this repository's own plan and both shipped fixtures are the
// ordinary valid plans it reads, and they stay well formed.
func TestCheckAcceptsTheShippedPlans(t *testing.T) {
	files := []string{
		filepath.Join("..", "..", "docs", "testing", "test-plan.md"),
		filepath.Join("..", "..", "assets", "skills", "test-strategy", "evals", "fixtures", "plans", "clean.md"),
		filepath.Join("..", "..", "assets", "skills", "test-strategy", "evals", "fixtures", "plans", "rejected.md"),
		filepath.Join("..", "..", "assets", "skills", MicroTemplatePath),
	}
	for _, f := range files {
		t.Run(filepath.Base(f), func(t *testing.T) {
			raw, err := os.ReadFile(f)
			if err != nil {
				t.Fatal(err)
			}
			if problems := CheckDocument(string(raw)); len(problems) != 0 {
				t.Fatalf("the unmodified plan is not well formed: %v", problems)
			}
		})
	}
}

// The Evidence ledger is a table a machine reads, so its machine columns are located by name. This is
// the header the template ships: `Admit` and `Digest` are the two new cells, and neither shares a
// substring with the columns around it.
const machineHeader = "## Evidence ledger\n\n" +
	"| Id | Claim | Executed | Admit | Inputs and parameters | Observed | Digest | Mutation or negative control → result | Reproduction | Label (`observado` / `razonado`, literal) |\n" +
	"|---|---|---|---|---|---|---|---|---|---|\n"

// machineColumns lists the names Ledger resolves, in the order it resolves them.
var machineColumns = []string{"id", "claim", "executed", "admit", "inputs", "observed", "digest", "mutation", "reproduction", "label"}

// Every field is filled with a value no other column shares, so a field resolved to the wrong column
// cannot pass.
func TestLedgerResolvesTheColumnsOfTheShippedHeader(t *testing.T) {
	doc := machineHeader +
		"| E1 | it keeps the comma | prose saying what was done | `node --test t.js` | `a,\"b,c\"` | 3 fields | sha256:1111 | reverted → red | rerun the command above | observado |\n"
	got := Ledger(doc)
	want := LedgerRow{
		ID: "E1", Claim: "it keeps the comma", Executed: "prose saying what was done",
		Admit: "`node --test t.js`", Inputs: "`a,\"b,c\"`", Observed: "3 fields",
		Digest: "sha256:1111", Mutation: "reverted → red",
		Reproduction: "rerun the command above", Label: "observado",
		Cells: 10, HeaderCells: 10,
	}
	if len(got) != 1 {
		t.Fatalf("rows = %#v, want the one ledger row", got)
	}
	if got[0] != want {
		t.Fatalf("row = %#v, want %#v", got[0], want)
	}
}

// A fenced example before the real ledger is documentation. The old section reader took its first pipe row as
// the header and stopped at the closing fence, so the machine read an example row instead of the ledger.
func TestLedgerSkipsFencedExamplesBeforeTheRealTable(t *testing.T) {
	doc := "## Evidence ledger\n\n```markdown\n| Example | Value |\n|---|---|\n| not-a-row | documentation |\n```\n\n" +
		strings.TrimPrefix(machineHeader, "## Evidence ledger\n\n") +
		"| E1 | claim | executed | admit | inputs | observed | sha256:1111 | mutation | reproduction | observado |\n"

	got := Ledger(doc)
	if len(got) != 1 || got[0].ID != "E1" {
		t.Fatalf("ledger rows = %#v, want the real E1 row after the fenced example", got)
	}
}

// RecordDigest must splice the real ledger row after documentation, not refuse because the example supplied the
// first header. The write is still byte-preserving: only the named Digest cell may change.
func TestRecordDigestSkipsFencedExamplesBeforeTheRealTable(t *testing.T) {
	doc := "## Evidence ledger\n\n```markdown\n| Example | Value |\n|---|---|\n| not-a-row | documentation |\n```\n\n" +
		strings.TrimPrefix(machineHeader, "## Evidence ledger\n\n") + recordRow(oldDigest)

	got, err := RecordDigest(doc, "E1", newDigest)
	if err != nil {
		t.Fatalf("RecordDigest = %v", err)
	}
	want := strings.Replace(doc, oldDigest, newDigest, 1)
	if got != want {
		t.Fatalf("RecordDigest rewrote bytes outside the real ledger cell:\ngot  %q\nwant %q", got, want)
	}
}

// Historical column names use substring matching, so the collisions are pinned: `Admit` must not resolve
// to `Executed`, `Digest` must not resolve to another column, and no two names may share a column. The Run
// column is the deliberate exact-match exception because `Target rung` contains the same substring.
func TestLedgerColumnNamesResolveToDistinctColumns(t *testing.T) {
	scan := scanSection(strings.Split(machineHeader, "\n"), "Evidence ledger")
	header := scan.header
	if header == nil {
		t.Fatal("the shipped header has no table")
	}
	seen := map[int]string{}
	for _, name := range machineColumns {
		i := columnIndex(header, name)
		if i < 0 {
			t.Fatalf("columnIndex(%q) = -1: the header stopped naming that column", name)
		}
		if other, dup := seen[i]; dup {
			t.Fatalf("%q and %q both resolve to column %d", name, other, i)
		}
		seen[i] = name
	}
	if a, e := columnIndex(header, "admit"), columnIndex(header, "executed"); a == e {
		t.Fatalf("admit resolved to the executed column %d", a)
	}
	if d, e := columnIndex(header, "digest"), columnIndex(header, "executed"); d == e {
		t.Fatalf("digest resolved to the executed column %d", d)
	}
}

// The old header ships no machine columns. A plan written before them still resolves every cell it
// has, and the two new fields stay empty rather than borrowing a neighbour.
func TestLedgerOnTheOldHeaderLeavesTheNewColumnsEmpty(t *testing.T) {
	got := Ledger(ledger + "| E1 | c | cmd | i | o | m | r | observado |\n")
	want := LedgerRow{ID: "E1", Claim: "c", Executed: "cmd", Inputs: "i", Observed: "o", Mutation: "m", Reproduction: "r", Label: "observado", Cells: 8, HeaderCells: 8}
	if len(got) != 1 {
		t.Fatalf("rows = %#v, want the one ledger row", got)
	}
	if got[0] != want {
		t.Fatalf("row = %#v, want %#v", got[0], want)
	}
}

// ID is the first cell and Label the last, the same reading Check does, so extra cells do not move
// them and a truncated row reads its last written cell as the label.
func TestLedgerKeepsTheFirstCellAsIDAndTheLastAsLabel(t *testing.T) {
	cases := []struct {
		name string
		doc  string
		want LedgerRow
	}{
		{
			name: "extra cells",
			doc:  machineHeader + "| E1 | c | prose | `run x` | i | o | sha256:aa | m | r | observado | extra one | extra two |\n",
			want: LedgerRow{ID: "E1", Claim: "c", Executed: "prose", Admit: "`run x`", Inputs: "i", Observed: "o", Digest: "sha256:aa", Mutation: "m", Reproduction: "r", Label: "extra two", Cells: 12, HeaderCells: 10},
		},
		{
			name: "missing cells",
			doc:  machineHeader + "| E1 | c | `run x` | `run x` | i |\n",
			want: LedgerRow{ID: "E1", Claim: "c", Executed: "`run x`", Admit: "`run x`", Inputs: "i", Label: "i", Cells: 5, HeaderCells: 10},
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := Ledger(tc.doc)
			if len(got) != 1 {
				t.Fatalf("rows = %#v, want the one ledger row", got)
			}
			if got[0] != tc.want {
				t.Fatalf("row = %#v, want %#v", got[0], tc.want)
			}
		})
	}
}

// Order is the document's, and the rows table already drops stay dropped: a separator line and a
// placeholder row are not conclusions.
func TestLedgerKeepsDocumentOrderAndSkipsWhatTheTableSkips(t *testing.T) {
	doc := machineHeader +
		"| E1 | first | prose | `run 1` | i | o | sha256:1 | m | r | observado |\n" +
		"|---|---|---|---|---|---|---|---|---|---|\n" +
		"| - | a placeholder row |  |  |  |  |  |  |  |  |\n" +
		"| E2 | second | prose | `run 2` | i | o | sha256:2 | m | r | observado |\n"
	got := Ledger(doc)
	if len(got) != 2 || got[0].ID != "E1" || got[1].ID != "E2" {
		t.Fatalf("rows = %#v, want E1 then E2 and nothing else", got)
	}
	if none := Ledger("## Findings\n\n| Id |\n|---|\n"); len(none) != 0 {
		t.Fatalf("a document with no Evidence ledger returned %#v", none)
	}
}

// recordRow is a ledger row whose Digest cell holds old digest, with a backslash-escaped pipe in
// three cells before it and two after it. A row that re-rendered its cells would rewrite those
// escapes, so the fixture is the proof the splice is by byte offset and not by re-joining.
const (
	oldDigest = "sha256:0000000000000000000000000000000000000000000000000000000000000000"
	newDigest = "sha256:aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"
)

func recordRow(digest string) string {
	return "| E1 | a claim with an escaped \\| pipe | prose a human reads | `run x` | `a\\|b` | observed \\| here | " + digest + " | mutation \\| negative | reproduction \\| again | observado |\n"
}

// RecordDigest is the one function in this package that writes: Check, Ledger and Gaps only read. It
// replaces the bytes of exactly one cell, so an escaped pipe anywhere in the row survives, and a cell
// the splice got wrong would move every column to its right.
func TestRecordDigestReplacesOnlyTheNamedCell(t *testing.T) {
	doc := machineHeader + recordRow(oldDigest)
	got, err := RecordDigest(doc, "E1", newDigest)
	if err != nil {
		t.Fatalf("RecordDigest = %v", err)
	}
	want := strings.Replace(doc, oldDigest, newDigest, 1)
	if got != want {
		t.Fatalf("RecordDigest rewrote bytes outside the digest cell:\ngot  %q\nwant %q", got, want)
	}
	if escaped := strings.Count(got, `\|`); escaped != 5 {
		t.Fatalf("the row has 5 escaped pipes (3 before the Digest cell, 2 after) and %d survived:\n%s", escaped, got)
	}
	if rows := Ledger(got); len(rows) != 1 || rows[0].Digest != newDigest {
		t.Fatalf("the ledger reads back %#v, want the fresh digest", rows)
	}
}

// A digest that was already recorded is the value the cell holds, so recording it again must be a
// no-op: a rerun of a recording command may not rewrite bytes it already wrote.
func TestRecordDigestIsIdempotent(t *testing.T) {
	doc := machineHeader + recordRow(oldDigest)
	once, err := RecordDigest(doc, "E1", newDigest)
	if err != nil {
		t.Fatal(err)
	}
	twice, err := RecordDigest(once, "E1", newDigest)
	if err != nil {
		t.Fatal(err)
	}
	if twice != once {
		t.Fatalf("recording the same digest twice changed the document:\nonce  %q\ntwice %q", once, twice)
	}
}

// The Digest cell is located by name, so its position in the row cannot matter: a truncated row may
// carry it as the last cell.
func TestRecordDigestOnADigestCellThatIsTheLastCell(t *testing.T) {
	doc := machineHeader + "| E1 | the claim | prose | `run x` | none | the observation | " + oldDigest + " |\n"
	got, err := RecordDigest(doc, "E1", newDigest)
	if err != nil {
		t.Fatalf("RecordDigest = %v", err)
	}
	if want := strings.Replace(doc, oldDigest, newDigest, 1); got != want {
		t.Fatalf("got %q, want %q", got, want)
	}
}

// Every refusal is distinct and specific, so a caller can say what to change instead of guessing why
// nothing was written.
func TestRecordDigestRefusesEveryReasonItCannotWrite(t *testing.T) {
	noDigestColumn := "## Evidence ledger\n\n" +
		"| Id | Claim | Executed | Admit | Inputs | Observed | Mutation | Reproduction | Label |\n" +
		"|---|---|---|---|---|---|---|---|---|\n" +
		"| E1 | the claim | prose | `run x` | none | the observation | reverted → red | rerun it | observado |\n"
	cases := []struct {
		name   string
		doc    string
		id     string
		digest string
		want   string // the refusal must name this
	}{
		{
			name: "the id is not a row in the ledger",
			doc:  machineHeader + recordRow(oldDigest), id: "E9", digest: newDigest,
			want: "is not a row in the Evidence ledger",
		},
		{
			name: "the header names no Digest column",
			doc:  noDigestColumn, id: "E1", digest: newDigest,
			want: "names no Digest column",
		},
		{
			name: "the row has fewer cells than the Digest column requires",
			doc:  machineHeader + "| E1 | the claim | `run x` |\n", id: "E1", digest: newDigest,
			want: "so the Digest column",
		},
		{
			name: "the digest is not a sha256 digest",
			doc:  machineHeader + recordRow(oldDigest), id: "E1", digest: "deadbeef",
			want: "is not a sha256 digest",
		},
		{
			name: "the document has no Evidence ledger section",
			doc:  "## Findings\n\n| Id |\n|---|\n", id: "E1", digest: newDigest,
			want: "no Evidence ledger section",
		},
		{
			name: "the Evidence ledger section holds no table",
			doc:  "## Evidence ledger\n\nno rows yet\n", id: "E1", digest: newDigest,
			want: "holds no table",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, err := RecordDigest(tc.doc, tc.id, tc.digest)
			if err == nil {
				t.Fatalf("RecordDigest recorded into a plan it cannot record into, returning %q", got)
			}
			if !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("error = %v, want one naming %q", err, tc.want)
			}
			if got != "" {
				t.Fatalf("a refused record must return no document, got %q", got)
			}
		})
	}
}

// A backslash-escaped pipe is part of its cell. Splitting on it shifts every column to the right,
// which moves a layer's owner out of the column a report reads.
func TestSplitHonoursEscapedPipes(t *testing.T) {
	got := split(`| a \| b | ` + "`owner`" + ` | pending |`)
	want := []string{"a | b", "`owner`", "pending"} // the escape belongs to the syntax, the pipe to the cell
	if len(got) != len(want) {
		t.Fatalf("cells = %q, want %q", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("cell %d = %q, want %q", i, got[i], want[i])
		}
	}
}

// The Findings vocabulary is closed. A status outside it used to be read by a `confirmed|fixed` regex
// and silently treated as unsettled, so a row labelled `resolved` stopped owing the test that holds its
// verdict and the plan still passed.
func TestCheckNamesAStatusOutsideTheFindingsVocabulary(t *testing.T) {
	cases := []struct {
		name    string
		status  string
		pin     string
		want    string
		wantNot string
	}{
		{
			name:   "a status the vocabulary does not carry",
			status: "resolved",
			pin:    "",
			want:   `line 5: finding F1 status "resolved" is not one of: open, confirmed, fixed, gap-closed, rejected, wontfix`,
			// The breach is the vocabulary's. An unknown status is neither valid nor read as a settled one
			// that owes a pinning test, so the row is judged once and on the rule it actually broke.
			wantNot: "names no pinning test",
		},
		{
			name:   "a row with no status at all",
			status: "",
			pin:    "",
			want:   "line 5: finding F1 has no status: one of open, confirmed, fixed, gap-closed, rejected, wontfix",
		},
		{
			name:   "a documented settled status still owes its pinning test",
			status: " FIXED ",
			pin:    "",
			want:   "line 5: finding F1 is settled but names no pinning test",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			plan := header + "| F1 | `src/a.js:5` x | M | yes | E1 | " + tc.pin + " | " + tc.status + " | me | - | - |\n" +
				ledger + "| E1 | c | cmd | i | o | m | r | observado |\n"
			p := write(t, t.TempDir(), "plan.md", plan)
			problems, err := Check(p)
			if err != nil {
				t.Fatal(err)
			}
			joined := strings.Join(problems, "\n")
			if !strings.Contains(joined, tc.want) {
				t.Fatalf("problems = %v, want one containing %q", problems, tc.want)
			}
			if tc.wantNot != "" && strings.Contains(joined, tc.wantNot) {
				t.Fatalf("problems = %v, want none containing %q", problems, tc.wantNot)
			}
		})
	}
}

// A ledger row's id is what a finding cites, so two rows sharing one id make every citation that names it
// ambiguous: the checker collapsed both into one entry and said nothing, and the document still read as `well
// formed`. Two findings sharing an id has the same shape, one table over.
func TestCheckRefusesTwoRowsCarryingTheSameId(t *testing.T) {
	cases := []struct {
		name string
		plan string
		want string
	}{
		{
			name: "two ledger rows with one id",
			plan: header +
				"| F1 | `src/a.js:5` x | M | yes | E1 | t.js :: x | fixed | me | - | - |\n" +
				ledger +
				"| E1 | c | cmd | i | o | m | r | observado |\n" +
				"\n| E1 | other claim | cmd | i | o | m | r | observado |\n",
			want: "evidence E1 repeats the id of the row on line",
		},
		{
			name: "two findings with one id",
			plan: header +
				"| F1 | `src/a.js:5` x | M | yes | E1 | t.js :: x | fixed | me | - | - |\n" +
				"\n| F1 | `src/b.js:9` y | M | yes | E1 | t.js :: x | fixed | me | - | - |\n" +
				ledger + "| E1 | c | cmd | i | o | m | r | observado |\n",
			want: "finding F1 repeats the id of the row on line",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			joined := strings.Join(CheckDocument(tc.plan), "\n")
			if !strings.Contains(joined, tc.want) {
				t.Fatalf("problems = %v, want one containing %q", joined, tc.want)
			}
		})
	}
}

func TestCheckReportsACutWithNoRowReadBeforeIt(t *testing.T) {
	plan := header + "### Notes\n" +
		"| F1 | `src/a.js:5` x | M | yes | E9 | t.js :: x | fixed | me | - | - |\n" +
		ledger + "| E1 | c | cmd | i | o | m | r | observado |\n"
	joined := strings.Join(CheckDocument(plan), "\n")
	if !strings.Contains(joined, "interrupted at line 5") || !strings.Contains(joined, "### Notes") {
		t.Fatalf("a cut before the first row must still be reported:\n%s", joined)
	}
	if strings.Contains(joined, "cites evidence E9") {
		t.Fatalf("a row under an interruption was never read and must not be judged:\n%s", joined)
	}
}

func TestCheckReportsAMalformedRowUnderASubheading(t *testing.T) {
	plan := header +
		"| F1 | `src/a.js:5` x | M | yes | E1 | t.js :: x | fixed | me | - | - |\n" +
		"\n### Hypotheses (razonado)\n\n| Hypothesis | Probe |\n|---|---|\n| H1 | a | b | c |\n" +
		ledger + "| E1 | c | cmd | i | o | m | r | observado |\n"
	joined := strings.Join(CheckDocument(plan), "\n")
	if !strings.Contains(joined, "Hypotheses (razonado) table row \"H1\" has 4 cells against the header's 2") {
		t.Fatalf("a malformed row under a subheading must be reported:\n%s", joined)
	}
}

func TestLedgerReadsARowAfterABlankLine(t *testing.T) {
	head := "## Evidence ledger\n\n" +
		"| Id | Claim | Executed | Admit | Inputs | Observed | Digest | Normalize | Mutation | Reproduction | Label |\n" +
		"|---|---|---|---|---|---|---|---|---|---|---|\n"
	rows := Ledger(head +
		"| E1 | first | c | go version | i | o |  |  | m | r | observado |\n" +
		"\n" +
		"| E2 | second | c | go env GOOS | i | o |  |  | m | r | observado |\n")
	if len(rows) != 2 || rows[0].ID != "E1" || rows[1].ID != "E2" {
		t.Fatalf("rows = %v, want E1 and E2: a blank line inside the section hid the row from the runner", rows)
	}
}

func TestRecordDigestFindsARowAfterABlankLine(t *testing.T) {
	digest := "sha256:" + strings.Repeat("a", 64)
	doc := "## Evidence ledger\n\n" +
		"| Id | Claim | Executed | Admit | Inputs | Observed | Digest | Normalize | Mutation | Reproduction | Label |\n" +
		"|---|---|---|---|---|---|---|---|---|---|---|\n" +
		"| E1 | first | c | go version | i | o |  |  | m | r | observado |\n" +
		"\n" +
		"| E2 | second | c | go env GOOS | i | o |  |  | m | r | observado |\n"
	out, err := RecordDigest(doc, "E2", digest)
	if err != nil {
		t.Fatalf("RecordDigest on a row after a blank line: %v", err)
	}
	if !strings.Contains(out, "| "+digest+" |") {
		t.Fatalf("the digest did not land on E2's Digest cell:\n%s", out)
	}
}

// The ledger's machine columns are resolved by name, and the row's own Normalize expression is one of them.
// A header that carries it must resolve it, and a header that does not must leave the field empty rather
// than borrowing the cell of the column beside it.
func TestLedgerResolvesTheNormalizeColumnByName(t *testing.T) {
	head := "## Evidence ledger\n\n" +
		"| Id | Claim | Executed | Admit | Inputs and parameters | Observed | Digest | Normalize | Mutation or negative control → result | Reproduction | Label (`observado` / `razonado`, literal) |\n" +
		"|---|---|---|---|---|---|---|---|---|---|---|\n"
	rows := Ledger(head + "| E1 | c | prose | go test ./... | i | o | sha256:aa | [0-9]+s | m | r | observado |\n")
	if len(rows) != 1 {
		t.Fatalf("ledger = %#v, want the one row", rows)
	}
	got := rows[0]
	if got.Normalize != "[0-9]+s" || got.Digest != "sha256:aa" || got.Admit != "go test ./..." || got.Label != "observado" {
		t.Fatalf("row = %#v, want the Normalize cell read as its own column", got)
	}
	if got.Cells != 11 || got.HeaderCells != 11 {
		t.Fatalf("row has %d cells against a %d-cell header, want 11 and 11", got.Cells, got.HeaderCells)
	}

	// The previous header has no Normalize column. The field stays empty, and every other column still
	// resolves to its own cell rather than shifting by one.
	old := "## Evidence ledger\n\n" +
		"| Id | Claim | Executed | Admit | Inputs and parameters | Observed | Digest | Mutation or negative control → result | Reproduction | Label (`observado` / `razonado`, literal) |\n" +
		"|---|---|---|---|---|---|---|---|---|---|\n" +
		"| E1 | c | prose | go test ./... | i | o | sha256:aa | m | r | observado |\n"
	rows = Ledger(old)
	if len(rows) != 1 {
		t.Fatalf("ledger = %#v, want the one row", rows)
	}
	got = rows[0]
	if got.Normalize != "" || got.Digest != "sha256:aa" || got.Mutation != "m" || got.Reproduction != "r" || got.Label != "observado" {
		t.Fatalf("row = %#v, want no Normalize column with every other field still resolved", got)
	}

	// A row that lost a cell is reported by the count, not read from the wrong column. The document carries
	// only a ledger, so Check also reports the Findings section it does not have; the breach this asserts is
	// the cell count, named against the ledger.
	dir := t.TempDir()
	short := write(t, dir, "short.md", head+"| E1 | c | prose | go test ./... | i | o | sha256:aa | [0-9]+s | m | r |\n")
	problems, err := Check(short)
	if err != nil {
		t.Fatalf("Check(%s) failed: %v", short, err)
	}
	reported := false
	for _, p := range problems {
		if strings.Contains(p, "Evidence ledger") && strings.Contains(p, "11") {
			reported = true
		}
	}
	if !reported {
		t.Fatalf("Check(%s) = %v, want the lost cell reported against the ledger", short, problems)
	}
}

// The Mode column is resolved like the others, and recording a mode writes that one cell and nothing else:
// the cell keeps the spacing its author wrote, and a rerun with the same mode is byte-identical.
func TestLedgerResolvesTheModeColumnAndRecordModeWritesIt(t *testing.T) {
	head := "## Evidence ledger\n\n" +
		"| Id | Claim | Executed | Admit | Inputs and parameters | Observed | Digest | Normalize | Mode | Mutation or negative control → result | Reproduction | Label (`observado` / `razonado`, literal) |\n" +
		"|---|---|---|---|---|---|---|---|---|---|---|---|\n"
	doc := head + "| E1 | c | prose | go test ./... | i | o | sha256:aa | | sandbox | m | r | observado |\n"
	rows := Ledger(doc)
	if len(rows) != 1 || rows[0].Mode != "sandbox" || rows[0].Digest != "sha256:aa" || rows[0].Mutation != "m" {
		t.Fatalf("ledger = %#v, want the Mode cell read as its own column", rows)
	}
	if rows[0].Cells != 12 || rows[0].HeaderCells != 12 {
		t.Fatalf("row has %d cells against a %d-cell header, want 12 and 12", rows[0].Cells, rows[0].HeaderCells)
	}

	updated, err := RecordMode(doc, "E1", "host")
	if err != nil {
		t.Fatalf("RecordMode failed: %v", err)
	}
	if got := Ledger(updated)[0].Mode; got != "host" {
		t.Fatalf("Mode after recording = %q, want host", got)
	}
	if !strings.Contains(updated, "| host | m | r | observado |") {
		t.Fatalf("recorded document = %q, want only the Mode cell to have moved", updated)
	}
	if again, err := RecordMode(updated, "E1", "host"); err != nil || again != updated {
		t.Fatalf("RecordMode is not idempotent: %v, %q", err, again)
	}

	if _, err := RecordMode(doc, "E1", "container"); err == nil || !strings.Contains(err.Error(), "is not an execution mode") {
		t.Fatalf("RecordMode accepted a mode that is neither host nor sandbox: %v", err)
	}
}

// A Mutate cell is a data field and not a command, so the grammar is strict and the parts come back rather than
// being interpreted. These are the shapes a human writes, including the ones the grammar has to refuse before a
// replay could apply an edit to the wrong place.
func TestParseMutationReadsTheOneShapeACellMayTake(t *testing.T) {
	ok := []struct {
		cell string
		want Mutation
	}{
		{">= => > @ internal/plan/plan.go:214", Mutation{Old: ">=", New: ">", Path: "internal/plan/plan.go", Line: 214}},
		{"  a => b  @  x/y.go:7 ", Mutation{Old: "a", New: "b", Path: "x/y.go", Line: 7}},
		{"a => b => c @ p.go:1", Mutation{Old: "a", New: "b => c", Path: "p.go", Line: 1}},
		{"old => @ p.go:1", Mutation{Old: "old", New: "", Path: "p.go", Line: 1}},
		{"a @ b => c @ p.go:2", Mutation{Old: "a @ b", New: "c", Path: "p.go", Line: 2}},
	}
	for _, tc := range ok {
		t.Run(tc.cell, func(t *testing.T) {
			got, err := ParseMutation(tc.cell)
			if err != nil {
				t.Fatalf("ParseMutation(%q) failed: %v", tc.cell, err)
			}
			if got != tc.want {
				t.Fatalf("ParseMutation(%q) = %#v, want %#v", tc.cell, got, tc.want)
			}
		})
	}

	bad := []struct {
		cell   string
		reason string
	}{
		{"", ReasonMutationMalformed},
		{"no arrow here", ReasonMutationMalformed},
		{"=> > @ p.go:1", ReasonMutationMalformed},
		{"a => b", ReasonMutationMalformed},
		{"a => b @ p.go", ReasonMutationMalformed},
		{"a => b @ p.go:x", ReasonMutationMalformed},
		{"a => b @ p.go:0", ReasonMutationMalformed},
		{"a => b @ p.go:-3", ReasonMutationMalformed},
		{"a => b @ :1", ReasonMutationMalformed},
		{"same => same @ p.go:1", ReasonMutationNoOp},
	}
	for _, tc := range bad {
		t.Run("refuses "+tc.cell, func(t *testing.T) {
			_, err := ParseMutation(tc.cell)
			if err == nil {
				t.Fatalf("ParseMutation(%q) was accepted", tc.cell)
			}
			bad, ok := err.(MutationError)
			if !ok || bad.Reason != tc.reason {
				t.Fatalf("ParseMutation(%q) = %v, want reason %s", tc.cell, err, tc.reason)
			}
		})
	}
}

// A Mutate cell may carry a survey: several edits separated by ` ;; `, each replayed on its own, and an edit
// prefixed `~ ` declared equivalent, so the command must stay green under it instead of going red.
func TestParseMutationsReadsASurvey(t *testing.T) {
	got, err := ParseMutations("a => b @ p.go:1 ;; ~ c => d @ q.go:2 ;; e => @ r.go:3")
	if err != nil {
		t.Fatalf("ParseMutations refused a well-formed survey: %v", err)
	}
	want := []Mutation{
		{Old: "a", New: "b", Path: "p.go", Line: 1},
		{Old: "c", New: "d", Path: "q.go", Line: 2, Equivalent: true},
		{Old: "e", New: "", Path: "r.go", Line: 3},
	}
	if len(got) != len(want) {
		t.Fatalf("ParseMutations = %#v, want %d edits", got, len(want))
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("edit %d = %#v, want %#v", i+1, got[i], want[i])
		}
	}

	_, err = ParseMutations("a => b @ p.go:1 ;; no arrow here")
	bad, ok := err.(MutationError)
	if !ok || bad.Reason != ReasonMutationMalformed || !strings.Contains(bad.Detail, "edit 2 of 2") {
		t.Fatalf("a malformed second edit = %v, want %s naming edit 2 of 2", err, ReasonMutationMalformed)
	}
}

// The claim a Mutate cell makes is about this tree, so it is checked here: nothing here runs, and nothing here
// edits. Every refusal is named, because each one tells the author a different thing to fix.
func TestValidateMutationChecksTheTreeBeforeAnythingRuns(t *testing.T) {
	dir := t.TempDir()
	write(t, dir, "src/a.go", "package a\n\nvar x = 1\nvar y = 1\nvar z = 2\n")
	write(t, dir, "src/dup.go", "a\nb\na\n")
	// A real file outside dir, whose content would let the escaping case validate if the guard did not exist. The
	// case must fail on the verdict, not on a different error string: without the guard this file is found, the
	// line holds the old text exactly once, and the mutation is accepted.
	write(t, filepath.Dir(dir), "outside.go", "package a\n\n\nvar y = 1\n\n")

	cases := []struct {
		name   string
		m      Mutation
		reason string // empty means it validates
	}{
		{"the text is on the named line exactly once", Mutation{Old: "var y", New: "var w", Path: "src/a.go", Line: 4}, ""},
		// `..` that comes back inside is not an escape: the guard refuses a path that leaves the tree, not the token.
		{"a path that climbs and comes back stays inside", Mutation{Old: "var y", New: "var w", Path: "src/../src/a.go", Line: 4}, ""},
		{"the file is not there", Mutation{Old: "x", New: "y", Path: "src/nope.go", Line: 1}, ReasonMutationNotFound},
		{"the line does not exist", Mutation{Old: "x", New: "y", Path: "src/a.go", Line: 99}, ReasonMutationNoLine},
		{"the text is not on that line", Mutation{Old: "var z", New: "var q", Path: "src/a.go", Line: 3}, ReasonMutationNotFound},
		{"the text is not in the file", Mutation{Old: "nope", New: "y", Path: "src/a.go", Line: 3}, ReasonMutationNotFound},
		{"the text occurs twice", Mutation{Old: "a", New: "z", Path: "src/dup.go", Line: 1}, ReasonMutationAmbiguous},
		{"the path is absolute", Mutation{Old: "x", New: "y", Path: "/etc/passwd", Line: 1}, ReasonMutationMalformed},
		{"the path climbs out of the tree", Mutation{Old: "var y", New: "var w", Path: "../outside.go", Line: 4}, ReasonMutationMalformed},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			err := ValidateMutation(dir, tc.m)
			if tc.reason == "" {
				if err != nil {
					t.Fatalf("ValidateMutation = %v, want it valid", err)
				}
				return
			}
			bad, ok := err.(MutationError)
			if !ok || bad.Reason != tc.reason {
				t.Fatalf("ValidateMutation = %v, want reason %s", err, tc.reason)
			}
		})
	}
}

// A closed vocabulary has an accepted side worth pinning too: every documented status is read as
// written, and a settled one that names its pinning test owes nothing.
func TestCheckAcceptsEveryDocumentedFindingsStatus(t *testing.T) {
	for _, status := range []string{"open", "confirmed", "fixed", "rejected", "wontfix"} {
		t.Run(status, func(t *testing.T) {
			plan := header + "| F1 | `src/a.js:5` x | M | yes | E1 | t.js :: x | " + status + " | me | - | - |\n" +
				ledger + "| E1 | c | cmd | i | o | m | r | observado |\n"
			p := write(t, t.TempDir(), "plan.md", plan)
			problems, err := Check(p)
			if err != nil {
				t.Fatal(err)
			}
			if len(problems) != 0 {
				t.Fatalf("status %q is documented and owes nothing here: %v", status, problems)
			}
		})
	}
}

// A line that is not a table row closes the block, and a `|` row after it proves the table was cut in two.
func TestCheckReportsATableInterruptedByProse(t *testing.T) {
	plan := header +
		"| F1 | `src/a.js:5` x | M | yes | E1 | t.js :: x | fixed | me | - | - |\n" +
		"a sentence that closes the table\n" +
		"| F2 | `src/b.js:9` y | M | yes | E9 | t.js :: y | fixed | me | - | - |\n" +
		ledger + "| E1 | c | cmd | i | o | m | r | observado |\n"
	joined := strings.Join(CheckDocument(plan), "\n")
	if !strings.Contains(joined, "line 6: the Findings table is interrupted at line 6") ||
		!strings.Contains(joined, "a sentence that closes the table") || !strings.Contains(joined, "line 7") {
		t.Fatalf("an interrupted table must name the cut, the line that made it and the row it hid:\n%s", joined)
	}
	if strings.Contains(joined, "cites evidence E9") {
		t.Fatalf("a row under an interruption was never read and must not be judged:\n%s", joined)
	}
}

// A `###` after a table header is a boundary only when a table of its own starts there, never a silent cut.
func TestCheckReportsATableInterruptedByASubheading(t *testing.T) {
	plan := header +
		"| F1 | `src/a.js:5` x | M | yes | E1 | t.js :: x | fixed | me | - | - |\n" +
		"### Notes\n" +
		"| F2 | `src/b.js:9` y | M | yes | E9 | t.js :: y | fixed | me | - | - |\n" +
		ledger + "| E1 | c | cmd | i | o | m | r | observado |\n"
	joined := strings.Join(CheckDocument(plan), "\n")
	if !strings.Contains(joined, "line 6: the Findings table is interrupted at line 6") ||
		!strings.Contains(joined, "### Notes") || !strings.Contains(joined, "line 7") {
		t.Fatalf("a subheading that cuts the table must be reported as the cut it is:\n%s", joined)
	}
	if strings.Contains(joined, "cites evidence E9") {
		t.Fatalf("a row under an interruption was never read and must not be judged:\n%s", joined)
	}
}

// A fenced example above the real table must not become the Findings header.
func TestCheckIgnoresAFencedExampleTable(t *testing.T) {
	for _, marker := range []string{"```", "~~~"} {
		t.Run(marker, func(t *testing.T) {
			doc := "## Findings\n\n" + marker + "markdown\n| Id | Finding | Severity |\n|---|---|---|\n" +
				"| F9 | `src/fenced.ts:1` | visible error |\n" + marker + "\n\n" +
				strings.TrimPrefix(header, "## Findings\n\n") +
				"| F1 | `src/real.ts:1` | visible error | yes | E1 |  | resolved | me | - | - |\n" +
				ledger + "| E1 | c | cmd | i | o | m | r | observado |\n"
			joined := strings.Join(CheckDocument(doc), "\n")
			if !strings.Contains(joined, `finding F1 status "resolved"`) {
				t.Fatalf("the real row's breach is what the checker owes:\n%s", joined)
			}
			if strings.Contains(joined, "F9") || strings.Contains(joined, "interrupted") {
				t.Fatalf("the fenced example must not be read as a table:\n%s", joined)
			}
		})
	}
}

// An unclosed fence skipped every line under it: fail closed rather than read a shorter plan as a whole one.
func TestCheckReportsAnUnclosedFence(t *testing.T) {
	doc := header + "\n```markdown\n| Id | Finding |\n|---|---|\n"
	want := "line 6: the Findings table region ends inside a code fence opened at line 6, so nothing below it was read"
	if problems := CheckDocument(doc); !strings.Contains(strings.Join(problems, "\n"), want) {
		t.Fatalf("problems = %v, want one containing %q", problems, want)
	}
}

// A subheading is a boundary when its own table starts there, as the skeleton's `### Hypotheses` does.
func TestCheckAcceptsASubheadingThatOpensItsOwnTable(t *testing.T) {
	nested := header +
		"| F1 | `src/a.js:5` x | M | yes | E1 | t.js :: x | fixed | me | - | - |\n" +
		"### Notes\n\nNotes prose.\n\n| Note | Why |\n|---|---|\n| a note | a reason |\n" +
		ledger + "| E1 | c | cmd | i | o | m | r | observado |\n"
	if problems := CheckDocument(nested); len(problems) != 0 {
		t.Fatalf("a subheading with its own header and delimiter opens a table: %v", problems)
	}
	body, err := Template()
	if err != nil {
		t.Fatal(err)
	}
	if problems := CheckDocument(body); len(problems) != 0 {
		t.Fatalf("the shipped template must stay well formed: %v", problems)
	}
}

// The fix must not narrow the checker: this repository's own plan and both shipped fixtures keep
// passing, and a fenced example placed after the separator leaves every real row readable.
func TestCheckAcceptsTheShippedPlansWithAFencedExample(t *testing.T) {
	files := []string{
		filepath.Join("..", "..", "docs", "testing", "test-plan.md"),
		filepath.Join("..", "..", "assets", "skills", "test-strategy", "evals", "fixtures", "plans", "clean.md"),
		filepath.Join("..", "..", "assets", "skills", "test-strategy", "evals", "fixtures", "plans", "rejected.md"),
	}
	for _, f := range files {
		t.Run(filepath.Base(f), func(t *testing.T) {
			raw, err := os.ReadFile(f)
			if err != nil {
				t.Fatal(err)
			}
			doc := string(raw)
			if problems := CheckDocument(doc); len(problems) != 0 {
				t.Fatalf("the unmodified plan is not well formed: %v", problems)
			}
			sample, ok := insertFencedExample(doc)
			if !ok {
				t.Fatal("the Findings separator was not found")
			}
			if problems := CheckDocument(sample); len(problems) != 0 {
				t.Fatalf("a fenced example after the separator is documentation, not an interruption: %v", problems)
			}
		})
	}
}

// The Evidence ledger legitimately carries its own `### Hypotheses` table. The region scan stops at that
// heading, so the ledger's table is never read as an interrupted Findings table and the shipped
// template stays clean.
func TestCheckAcceptsTheShippedTemplateWithItsHypothesesTable(t *testing.T) {
	dir := t.TempDir()
	p := filepath.Join(dir, "plan.md")
	if err := Init(p, false); err != nil {
		t.Fatal(err)
	}
	body, err := os.ReadFile(p)
	if err != nil {
		t.Fatal(err)
	}
	// A data row under the hypotheses table is where a scan that ran past the `###` heading would
	// report an interruption it invented.
	const hypotheses = "| Hypothesis | Probe that would settle it |\n|---|---|\n"
	plan := strings.Replace(string(body), hypotheses, hypotheses+"| a claim that needs a probe | run it |\n", 1)
	if plan == string(body) {
		t.Fatal("the template moved: the hypotheses table was never placed")
	}
	if err := os.WriteFile(p, []byte(plan), 0o644); err != nil {
		t.Fatal(err)
	}
	problems, err := Check(p)
	if err != nil {
		t.Fatal(err)
	}
	if len(problems) != 0 {
		t.Fatalf("the shipped template must stay well formed: %v", problems)
	}
}

// A fenced block inside another table's region is inert too: an example under Ranked targets must not
// read as the resumed half of a cut table.
func TestCheckIgnoresAFencedBlockInRankedTargets(t *testing.T) {
	doc := header + ledger + "| E1 | c | cmd | i | o | m | r | observado |\n" +
		"## Ranked targets\n\n| Target | Blast radius | Status | Run |\n|---|---|---|---|\n" +
		"| 1. token refresh | every session | done |  |\n" +
		"\nA target row looks like this:\n\n" +
		"```markdown\n| Target | Status |\n|---|---|\n| 9. an example | done |\n```\n"
	if problems := CheckDocument(doc); len(problems) != 0 {
		t.Fatalf("a fenced example inside Ranked targets is documentation: %v", problems)
	}
}

// The Evidence ledger may sit under a `###`; capping the scan at the first `###` left it unread and reported every finding citing it as missing a row.
func TestCheckReadsALedgerUnderASubheading(t *testing.T) {
	doc := header + "| F1 | `src/a.js:1` x | M | yes | E1 | t.js :: x | open | me | - | - |\n" +
		"## Evidence ledger\n\n### Recorded observations\n\n| Id | Claim |\n|---|---|\n| E1 | c |\n"
	if problems := CheckDocument(doc); len(problems) != 0 {
		t.Fatalf("a ledger under a subheading is still the ledger: %v", problems)
	}
}

// A `###` cut is not a table of its own when the first pipe row under it only reaches a later delimiter across
// a blank line: GFM's header and its delimiter are adjacent, so a row separated from the delimiter that way
// belongs to the table the subheading cut. The lookahead used to keep walking over blank lines and prose, read
// the later delimiter as the separator of a new table, and report `well formed` while the row between them was
// never read.
func TestCheckReportsASubheadingCutFollowedByABlankLine(t *testing.T) {
	// `### Notes` sits on line 5, the row it hides on line 6, and the delimiter it is separated from on line 8.
	plan := header +
		"### Notes\n" +
		"| F1 | `src/a.go:1` x | M | yes | E99 | t :: x | open | me | r | - |\n" +
		"\n" +
		"|---|---|---|---|---|---|---|---|---|---|\n" +
		ledger + "| E1 | c | cmd | i | o | m | r | observado |\n"
	problems := CheckDocument(plan)
	joined := strings.Join(problems, "\n")
	if !strings.Contains(joined, "line 5: the Findings table is interrupted at line 5") {
		t.Fatalf("a `###` cut followed by a blank line must be reported at its line: %v", problems)
	}
	if !strings.Contains(joined, "### Notes") {
		t.Fatalf("the interruption must quote the subheading: %v", problems)
	}
	if !strings.Contains(joined, "(the next table row is at line 6)") {
		t.Fatalf("the report must name the row whose reading was skipped: %v", problems)
	}
	if strings.Contains(joined, "E99") {
		t.Fatalf("rows under an interruption were never read, and must not be judged: %v", problems)
	}

	// The same shape with the blank line replaced by another data row: the first row is still not a header,
	// because a header is followed by its delimiter and this one is followed by a row.
	twoRows := header +
		"### Notes\n" +
		"| F1 | `src/a.go:1` x | M | yes | E99 | t :: x | open | me | r | - |\n" +
		"| F2 | `src/b.go:2` y | M | yes | E99 | t :: y | open | me | r | - |\n" +
		"|---|---|---|---|---|---|---|---|---|---|\n" +
		ledger + "| E1 | c | cmd | i | o | m | r | observado |\n"
	problems = CheckDocument(twoRows)
	joined = strings.Join(problems, "\n")
	if !strings.Contains(joined, "line 5: the Findings table is interrupted at line 5") || !strings.Contains(joined, "(the next table row is at line 6)") {
		t.Fatalf("a `###` cut whose first row is followed by another row must be an interruption: %v", problems)
	}
	if strings.Contains(joined, "E99") {
		t.Fatalf("rows under an interruption were never read, and must not be judged: %v", problems)
	}
}

// A `###` cut is only ever a table of its own when its own header and separator follow. The lookahead used
// to keep looking for a later `|` row and skip whatever prose sat between, so `### Notes`, a data row, prose
// and then a delimiter read as a subheading that opened a table: the data row was never read and the plan
// still reported `well formed`, which is the silent half of the defect the subheading rule exists for.
func TestCheckReportsASubheadingCutWithInterveningProse(t *testing.T) {
	// `### Notes` sits on line 6, the data row it hides on line 7, and the delimiter three lines later is
	// what the lookahead used to mistake for that row's separator.
	plan := header +
		"| F1 | `src/a.js:5` x | M | yes | E1 | t.js :: x | fixed | me | - | - |\n" +
		"### Notes\n" +
		"| F2 | `src/b.js:9` y | M | yes | E9 | t.js :: y | fixed | me | - | - |\n" +
		"a sentence between the row and its delimiter\n" +
		"|---|---|---|---|---|---|---|---|---|---|\n" +
		ledger + "| E1 | c | cmd | i | o | m | r | observado |\n"
	problems := CheckDocument(plan)
	joined := strings.Join(problems, "\n")
	if !strings.Contains(joined, "line 6: the Findings table is interrupted at line 6") {
		t.Fatalf("a subheading cut with intervening prose must be reported at its line: %v", problems)
	}
	if !strings.Contains(joined, "### Notes") {
		t.Fatalf("the interruption must quote the subheading: %v", problems)
	}
	if !strings.Contains(joined, "line 7") {
		t.Fatalf("the report must name the row whose reading was skipped: %v", problems)
	}
	if strings.Contains(joined, "cites evidence E9") {
		t.Fatalf("rows under an interruption were never read, and must not be judged: %v", problems)
	}

	// The other side of the same rule, so the fix cannot narrow the check: a subheading whose own header is
	// immediately followed by its separator is still a table of its own, prose before the header included.
	nested := header +
		"| F1 | `src/a.js:5` x | M | yes | E1 | t.js :: x | fixed | me | - | - |\n" +
		"### Notes\n\nNotes prose.\n\n| Note | Why |\n|---|---|\n| a note | a reason |\n" +
		ledger + "| E1 | c | cmd | i | o | m | r | observado |\n"
	if problems := CheckDocument(nested); len(problems) != 0 {
		t.Fatalf("a subheading with its own header and separator opens a table: %v", problems)
	}
	// A section whose first table header sits under a `###` is the same shape one level up: the subheading
	// comes before any header, so it opens the table rather than cutting one.
	underHeading := "## Findings\n\n### Recorded defects\n\n| Id | Finding | Severity | Data safe? | Evidence id | Pinning test | Status | Verdict by / date | Reason | Fingerprint |\n" +
		"|---|---|---|---|---|---|---|---|---|---|\n" +
		"| F1 | `src/a.js:5` x | M | yes | E1 | t.js :: x | fixed | me | - | - |\n" +
		ledger + "| E1 | c | cmd | i | o | m | r | observado |\n"
	if problems := CheckDocument(underHeading); len(problems) != 0 {
		t.Fatalf("a table header under a subheading is a table, not a cut: %v", problems)
	}
}

// insertFencedExample puts a fenced sample table right after the Findings separator. The fence opens
// and closes with nothing between it and the rows, so the rows under it must still read.
func insertFencedExample(doc string) (string, bool) {
	lines := strings.Split(doc, "\n")
	for i, l := range lines {
		if strings.HasPrefix(strings.TrimSpace(l), "| Id | Finding") {
			fence := []string{"", "```markdown", "| Id | Finding | Severity |", "|---|---|---|",
				"| F1 | `src/a.ts:1` | visible error |", "```"}
			out := append(append([]string{}, lines[:i+2]...), fence...)
			return strings.Join(append(out, lines[i+2:]...), "\n"), true
		}
	}
	return doc, false
}

func runCheckPlan(layer, ranked string) string {
	return header + layer + ranked
}

func TestCheckRefusesALayerMatrixWithoutARunColumn(t *testing.T) {
	doc := runCheckPlan("## Layer matrix\n\n| Layer | Skill | Scope | Status |\n|---|---|---|---|\n| Security | `appsec` | input | pending |\n", "## Ranked targets\n\n| Target | Status | Run |\n|---|---|---|\n| target | pending | redis-stream-pool |\n")
	problems := CheckDocument(doc)
	joined := strings.Join(problems, "\n")
	if !strings.Contains(joined, "Layer matrix") || !strings.Contains(joined, "Run") || !strings.Contains(joined, "tpp plan upgrade") {
		t.Fatalf("missing Run-column refusal: %v", problems)
	}
}

func TestCheckRefusesARankedTargetsTableWithoutARunColumn(t *testing.T) {
	doc := runCheckPlan("## Layer matrix\n\n| Layer | Skill | Scope | Status | Run |\n|---|---|---|---|---|\n| Security | `appsec` | input | pending | redis-stream-pool |\n", "## Ranked targets\n\n| Target | Status |\n|---|---|\n| target | pending |\n")
	problems := CheckDocument(doc)
	joined := strings.Join(problems, "\n")
	if !strings.Contains(joined, "Ranked targets") || !strings.Contains(joined, "Run") || !strings.Contains(joined, "tpp plan upgrade") {
		t.Fatalf("missing Run-column refusal: %v", problems)
	}
}

func TestCheckRefusesAMalformedRunCell(t *testing.T) {
	doc := runCheckPlan("## Layer matrix\n\n| Layer | Skill | Scope | Status | Run |\n|---|---|---|---|---|\n| Security | `appsec` | input | pending | Bad_Slug |\n", "## Ranked targets\n\n| Target | Status | Run |\n|---|---|---|\n| target | pending | redis-stream-pool |\n")
	problems := CheckDocument(doc)
	joined := strings.Join(problems, "\n")
	if !strings.Contains(joined, "line") || !strings.Contains(joined, "Bad_Slug") {
		t.Fatalf("malformed Run cell was not named with its line and cell: %v", problems)
	}
}

func TestCheckAcceptsARunCellThatIsASlug(t *testing.T) {
	doc := runCheckPlan("## Layer matrix\n\n| Layer | Skill | Scope | Status | Run |\n|---|---|---|---|---|\n| Security | `appsec` | input | pending | redis-stream-pool |\n", "## Ranked targets\n\n| Target | Status | Run |\n|---|---|---|\n| target | pending | redis-stream-pool |\n")
	if problems := CheckDocument(doc); len(problems) != 0 {
		t.Fatalf("valid Run slug rejected: %v", problems)
	}
}

func TestColumnRunDoesNotResolveToTargetRung(t *testing.T) {
	if got := columnIndex([]string{"Target rung", "Run"}, "run"); got != 1 {
		t.Fatalf("run resolved to column %d, want the Run column at 1", got)
	}
	if got := columnIndex([]string{"Target rung"}, "run"); got != -1 {
		t.Fatalf("run resolved to Target rung at %d, want -1", got)
	}
}

// The Expect column is resolved by name on the shipped header and shares its substring with no other column,
// so `Executed` never reads as it; the old header without the column leaves it empty (see
// TestLedgerOnTheOldHeaderLeavesTheNewColumnsEmpty).
func TestLedgerResolvesTheExpectColumnByName(t *testing.T) {
	doc := "## Evidence ledger\n\n" +
		"| Id | Claim | Executed | Admit | Inputs and parameters | Observed | Digest | Normalize | Mode | Mutate | Expect | Mutation or negative control → result | Reproduction | Label (`observado` / `razonado`, literal) |\n" +
		"|---|---|---|---|---|---|---|---|---|---|---|---|---|---|\n" +
		"| E1 | c | prose | go test ./a | i | o | sha256:aa | | host | | fail | m | r | observado |\n"
	rows := Ledger(doc)
	if len(rows) != 1 || rows[0].Expect != "fail" || rows[0].Executed != "prose" || rows[0].Mutation != "m" {
		t.Fatalf("ledger = %#v, want the Expect cell read as its own column", rows)
	}
}

package check

import (
	"errors"
	"io/fs"
	"path/filepath"
	"strings"
	"testing"

	"github.com/alesierraalta/tsp/internal/plan"
)

type fakeRepo struct {
	root     string
	status   string
	rootErr  error
	plan     string
	planPath string
	plans    map[string]string
	config   string
	planErr  bool
	ignored  bool
}

func (f *fakeRepo) deps() Deps {
	return Deps{
		Git: func(dir string, args ...string) (string, error) {
			if f.rootErr != nil {
				return "", f.rootErr
			}
			switch args[0] {
			case "rev-parse":
				return f.root + "\n", nil
			case "check-ignore":
				// `git check-ignore -q` exits 0 (no error) only when the path is ignored.
				if f.ignored {
					return "", nil
				}
				return "", errors.New("exit status 1")
			}
			return f.status, nil
		},
		ReadFile: func(path string) (string, error) {
			if path == f.root+"/"+plan.ConfigName {
				if f.config == "" {
					return "", fs.ErrNotExist
				}
				return f.config, nil
			}
			if f.plans != nil {
				rel, err := filepath.Rel(f.root, path)
				if err != nil {
					return "", fs.ErrNotExist
				}
				body, ok := f.plans[rel]
				if !ok || f.planErr {
					return "", fs.ErrNotExist
				}
				return body, nil
			}
			declared := f.planPath
			if declared == "" {
				declared = plan.DefaultPath
			}
			if path != f.root+"/"+declared || f.planErr {
				return "", fs.ErrNotExist
			}
			return f.plan, nil
		},
	}
}

const owing = "## Layer matrix\n\n| Layer | Skill | Scope | Status |\n|---|---|---|---|\n" +
	"| Security | `appsec-adversarial-auditor` | input | pending |\n\n" +
	"## Ranked targets\n\n| Target | Verdict | Status |\n|---|---|---|\n| 1. auth | probe | done |\n"

const settled = "## Layer matrix\n\n| Layer | Skill | Scope | Status |\n|---|---|---|---|\n" +
	"| Security | `appsec-adversarial-auditor` | input | done |\n\n" +
	"## Ranked targets\n\n| Target | Verdict | Status |\n|---|---|---|\n| 1. auth | probe | done |\n"

const micro = "Micro: internal/text/trim.go · touches none\n\n" +
	"## Findings\n\n| Id | Finding | Status |\n|---|---|---|\n\n" +
	"## Evidence ledger\n\n| Id | Claim | Mutate | Label |\n|---|---|---|---|\n" +
	"| E1 | pinned | strings.TrimSpace(s) => s @ internal/text/trim.go:7 | observado |\n"

func porcelain(entries ...string) string {
	var b strings.Builder
	for _, e := range entries {
		b.WriteString(e)
		b.WriteByte(0)
	}
	return b.String()
}

// The question a host cannot answer for us is which session did what; the question any host can
// answer is what the repository shows. This one needs no hook payload and no transcript, so it
// works the same from a Claude Stop hook, another agent, a Makefile, or CI.
func TestCheckReadsTheRepositoryAlone(t *testing.T) {
	cases := []struct {
		name     string
		repo     *fakeRepo
		wantExit int
		wantOut  string
	}{
		{
			name:     "changed source and no plan",
			repo:     &fakeRepo{root: "/r", status: porcelain(" M src/app.js"), planErr: true},
			wantExit: 1, wantOut: "no test plan",
		},
		{
			name:     "changed source and a plan that owes breadth",
			repo:     &fakeRepo{root: "/r", status: porcelain(" M src/app.js"), plan: owing},
			wantExit: 1, wantOut: "appsec-adversarial-auditor",
		},
		{
			name:     "changed source and a settled plan",
			repo:     &fakeRepo{root: "/r", status: porcelain(" M src/app.js"), plan: settled},
			wantExit: 0, wantOut: "owes nothing",
		},
		{
			name:     "no production source changed",
			repo:     &fakeRepo{root: "/r", status: porcelain(" M README.md", " M tests/a.test.js"), planErr: true},
			wantExit: 0, wantOut: "no production source",
		},
		{
			name:     "not a repository",
			repo:     &fakeRepo{rootErr: errors.New("not a git repository")},
			wantExit: 0, wantOut: "not a git repository",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			res := Run(".", tc.repo.deps())
			if res.Exit != tc.wantExit {
				t.Fatalf("exit = %d, want %d: %s", res.Exit, tc.wantExit, res.Text)
			}
			if !strings.Contains(res.Text, tc.wantOut) {
				t.Fatalf("text missing %q:\n%s", tc.wantOut, res.Text)
			}
		})
	}
}

// A micro plan owes no layer sweep, so check passes it, and never says a layer was swept.
func TestCheckPassesAMicroPlanWithoutClaimingEveryLayer(t *testing.T) {
	res := Run(".", (&fakeRepo{root: "/r", status: porcelain(" M src/app.js"), plan: micro}).deps())
	if res.Exit != 0 || !strings.Contains(res.Text, "micro plan") || strings.Contains(res.Text, "every assigned layer") {
		t.Fatalf("exit = %d:\n%s", res.Exit, res.Text)
	}
}

func TestCheckReadsTheDeclaredPlan(t *testing.T) {
	const declared = "docs/testing/scoped-plan.md"
	repo := &fakeRepo{
		root:   "/r",
		status: porcelain(" M src/app.js"),
		plans: map[string]string{
			plan.DefaultPath: owing,
			declared:         settled,
		},
		config: `{"planPath":"` + declared + `"}`,
	}
	res := Run(".", repo.deps())
	if res.Exit != 0 {
		t.Fatalf("exit = %d, want 0: %s", res.Exit, res.Text)
	}
	if !strings.Contains(res.Text, declared) || strings.Contains(res.Text, plan.DefaultPath) {
		t.Fatalf("text = %s", res.Text)
	}
}

func TestCheckPathBeatsTheDeclaration(t *testing.T) {
	const explicit = "docs/testing/explicit-plan.md"
	repo := &fakeRepo{
		root:   "/r",
		status: porcelain(" M src/app.js"),
		plans: map[string]string{
			"docs/testing/declared-plan.md": owing,
			explicit:                        settled,
		},
		config: `{"planPath":"docs/testing/declared-plan.md"}`,
	}
	deps := repo.deps()
	deps.PlanPath = explicit
	res := Run(".", deps)
	if res.Exit != 0 {
		t.Fatalf("exit = %d, want 0: %s", res.Exit, res.Text)
	}
	if !strings.Contains(res.Text, explicit) || strings.Contains(res.Text, "declared-plan.md") {
		t.Fatalf("text = %s", res.Text)
	}
}

func TestCheckRefusesAnAbsolutePath(t *testing.T) {
	deps := (&fakeRepo{root: "/r", status: porcelain(" M src/app.js"), plan: settled}).deps()
	deps.PlanPath = "/tmp/outside-plan.md"
	res := Run(".", deps)
	if res.Exit != 1 {
		t.Fatalf("exit = %d, want 1: %s", res.Exit, res.Text)
	}
	if !strings.Contains(res.Text, "--path") {
		t.Fatalf("text = %q, want the flag named", res.Text)
	}
}

func TestCheckResolvesARelativePathAgainstTheRoot(t *testing.T) {
	const rootOnly = "docs/testing/root-only.md"
	repo := &fakeRepo{
		root:   "/repository-root",
		status: porcelain(" M src/app.js"),
		plans:  map[string]string{rootOnly: settled},
	}
	deps := repo.deps()
	deps.PlanPath = rootOnly
	res := Run("/repository-root/subdirectory", deps)
	if res.Exit != 0 {
		t.Fatalf("exit = %d, want 0: %s", res.Exit, res.Text)
	}
	if !strings.Contains(res.Text, rootOnly) {
		t.Fatalf("text = %q, want the root-relative plan named", res.Text)
	}
}

func TestCheckNamesABrokenDeclaration(t *testing.T) {
	repo := &fakeRepo{root: "/r", status: porcelain(" M src/app.js"), config: `{`}
	res := Run(".", repo.deps())
	if res.Exit != 1 {
		t.Fatalf("exit = %d, want 1", res.Exit)
	}
	for _, want := range []string{"the plan declaration could not be read", plan.ConfigName} {
		if !strings.Contains(res.Text, want) {
			t.Fatalf("text missing %q:\n%s", want, res.Text)
		}
	}
}

func TestCheckFallsBackToTheDefaultWhenNothingIsDeclared(t *testing.T) {
	repo := &fakeRepo{root: "/r", status: porcelain(" M src/app.js"), plan: settled}
	res := Run(".", repo.deps())
	if res.Exit != 0 || !strings.Contains(res.Text, plan.DefaultPath) {
		t.Fatalf("exit = %d, text = %s", res.Exit, res.Text)
	}
}

// Every host can read plain text; only some can read a hook schema.
func TestTextNamesEveryChangedFileUpToALimit(t *testing.T) {
	var entries []string
	for _, n := range []string{"a", "b", "c", "d", "e", "f", "g", "h"} {
		entries = append(entries, " M src/"+n+".js")
	}
	res := Run(".", (&fakeRepo{root: "/r", status: porcelain(entries...), planErr: true}).deps())
	if !strings.Contains(res.Text, "src/a.js") || !strings.Contains(res.Text, "and 2 more") {
		t.Fatalf("text = %s", res.Text)
	}
}

// A plan git will never version is persistence without a record: the run's central artifact can
// vanish from history while check reports the repository as settled. It is a warning, not a block.
func TestCheckNamesAPlanGitIgnores(t *testing.T) {
	repo := &fakeRepo{root: "/r", status: porcelain(" M README.md"), plan: settled, ignored: true}
	res := Run(".", repo.deps())
	if res.Exit != 0 {
		t.Fatalf("an ignored plan is a warning, not a block: exit = %d: %s", res.Exit, res.Text)
	}
	for _, want := range []string{plan.DefaultPath, "git ignores", "versioned"} {
		if !strings.Contains(res.Text, want) {
			t.Fatalf("text missing %q:\n%s", want, res.Text)
		}
	}
}

// The caveat must not depend on the diff: a warning that only appears when source changed would
// vanish on exactly the quiet runs whose plan still needs to be persisted.
func TestIgnoredPlanWarnsEvenWithNoChangedSource(t *testing.T) {
	repo := &fakeRepo{root: "/r", status: porcelain(" M README.md", " M tests/a.test.js"), plan: settled, ignored: true}
	res := Run(".", repo.deps())
	if res.Exit != 0 {
		t.Fatalf("exit = %d: %s", res.Exit, res.Text)
	}
	if !strings.Contains(res.Text, "git ignores") {
		t.Fatalf("text = %s", res.Text)
	}
	if !strings.Contains(res.Text, "no production source") {
		t.Fatalf("the ordinary verdict must survive the warning: %s", res.Text)
	}
}

// A tracked plan under version control is the good case and must add nothing.
func TestTrackedPlanAddsNoWarning(t *testing.T) {
	repo := &fakeRepo{root: "/r", status: porcelain(" M src/app.js"), plan: settled}
	res := Run(".", repo.deps())
	if strings.Contains(res.Text, "git ignores") {
		t.Fatalf("a versioned plan needs no caveat:\n%s", res.Text)
	}
}

// No repository, no plan, no claim: the versioning caveat must stay quiet and must not break the
// verdicts check already produced.
func TestVersioningCaveatStaysQuietWithoutARepositoryOrAPlan(t *testing.T) {
	cases := []struct {
		name string
		repo *fakeRepo
	}{
		{"not a repository", &fakeRepo{rootErr: errors.New("not a git repository"), plan: settled, ignored: true}},
		{"no plan file", &fakeRepo{root: "/r", status: porcelain(" M src/app.js"), planErr: true, ignored: true}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			res := Run(".", tc.repo.deps())
			if strings.Contains(res.Text, "git ignores") {
				t.Fatalf("the caveat must stay quiet here:\n%s", res.Text)
			}
		})
	}
}

// Two runs share one plan: the declaration's run decides what check reports. Another run's pending
// rows are not this run's debt, so a settled declared run passes even while another run still owes —
// the whole-plan reading would fail the manual check on rows it was never asked about.
func TestCheckScopesToTheDeclaredRunAcrossTwoRuns(t *testing.T) {
	repo := &fakeRepo{
		root:   "/r",
		status: porcelain(" M src/app.js"),
		plans:  map[string]string{plan.DefaultPath: twoRunPlan},
		config: `{"run":"run-a"}`,
	}
	res := Run(".", repo.deps())
	if res.Exit != 0 {
		t.Fatalf("exit = %d, want 0: run-a owes nothing while run-b is another run's debt:\n%s", res.Exit, res.Text)
	}
	if !strings.Contains(res.Text, "owes nothing") || !strings.Contains(res.Text, "for run run-a") {
		t.Fatalf("text must say the declared run's verdict by name:\n%s", res.Text)
	}
	for _, otherRun := range []string{"database-persistence-testing", "2. billing"} {
		if strings.Contains(res.Text, otherRun) {
			t.Fatalf("text prints run-b's row %q as if it were run-a's:\n%s", otherRun, res.Text)
		}
	}
}

// A declared run no row carries cannot borrow the finished rows of other runs as its own proof of
// completion: check fails closed with the scoped problem named, instead of reporting owes nothing.
func TestCheckFailsClosedWhenTheDeclaredRunHasNoRow(t *testing.T) {
	settledOtherRun := "## Layer matrix\n\n| Layer | Skill | Scope | Status | Run |\n|---|---|---|---|---|\n" +
		"| Security | `appsec-adversarial-auditor` | input | done | run-b |\n\n" +
		"## Ranked targets\n\n| Target | Verdict | Status | Run |\n|---|---|---|---|\n" +
		"| 1. auth | probe | done | run-b |\n"
	repo := &fakeRepo{
		root:   "/r",
		status: porcelain(" M src/app.js"),
		plans:  map[string]string{plan.DefaultPath: settledOtherRun},
		config: `{"run":"run-x"}`,
	}
	res := Run(".", repo.deps())
	if res.Exit != 1 {
		t.Fatalf("exit = %d, want 1: a run nobody opened is never an all-clear:\n%s", res.Exit, res.Text)
	}
	if !strings.Contains(res.Text, `run "run-x"`) {
		t.Fatalf("text must name the missing run:\n%s", res.Text)
	}
	if strings.Contains(res.Text, "owes nothing") {
		t.Fatalf("other runs' finished rows are not this run's completion:\n%s", res.Text)
	}
}

// The manual check keeps its legacy whole-plan reading when the declaration names no run: without a
// selected run there is no scope, and every pending row is still owed work somebody can pick up.
func TestCheckKeepsTheWholePlanWhenNoRunIsDeclared(t *testing.T) {
	repo := &fakeRepo{
		root:   "/r",
		status: porcelain(" M src/app.js"),
		plans:  map[string]string{plan.DefaultPath: twoRunPlan},
		config: `{"planPath":"` + plan.DefaultPath + `"}`,
	}
	res := Run(".", repo.deps())
	if res.Exit != 1 {
		t.Fatalf("exit = %d, want 1: the legacy whole-plan reading counts every pending row:\n%s", res.Exit, res.Text)
	}
	if !strings.Contains(res.Text, "database-persistence-testing") {
		t.Fatalf("text = %s, want the pending row from the whole plan", res.Text)
	}
}

const twoRunPlan = "## Layer matrix\n\n| Layer | Skill | Scope | Status | Run |\n|---|---|---|---|---|\n" +
	"| Security | `appsec-adversarial-auditor` | input | done | run-a |\n" +
	"| Persistence | `database-persistence-testing` | input | pending | run-b |\n\n" +
	"## Ranked targets\n\n| Target | Verdict | Status | Run |\n|---|---|---|---|\n" +
	"| 1. auth | probe | done | run-a |\n" +
	"| 2. billing | probe | pending | run-b |\n"

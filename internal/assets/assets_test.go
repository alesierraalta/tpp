package assets

import (
	"io/fs"
	"regexp"
	"strings"
	"testing"

	"github.com/alesierraalta/tpp/internal/buildinfo"
)

var expected = []string{
	"agent-eval", "appsec-adversarial-auditor", "breakcheck", "clean-architecture-audit", "contract-compat-testing", "crash-and-process-testing", "database-persistence-testing",
	"dependency-legitimacy", "docker-test-containers", "exploit-testing", "go-testing", "iac-safe-auditor", "implementation-theater",
	"llm-eval-design", "llm-redteam", "messaging-eventdriven-testing", "no-excess-tests", "python-testing-patterns", "rag-audit-evaluator", "real-run-validation", "resilience-fault-injection", "runtime-reliability-testing", "silent-degradation",
	"test-strategy",
}

func TestEverySkillIsEmbeddedWithAMatchingName(t *testing.T) {
	names := SkillNames()
	if len(names) != len(expected) {
		t.Fatalf("embedded %d skills, want %d: %v", len(names), len(expected), names)
	}
	nameLine := regexp.MustCompile(`(?m)^name:\s*"?([a-z0-9-]+)"?\s*$`)
	for i, name := range expected {
		t.Run(name, func(t *testing.T) {
			if names[i] != name {
				t.Fatalf("position %d is %q, want %q", i, names[i], name)
			}
			data, err := fs.ReadFile(Skills(), name+"/SKILL.md")
			if err != nil {
				t.Fatalf("SKILL.md missing: %v", err)
			}
			m := nameLine.FindSubmatch(data)
			if m == nil || string(m[1]) != name {
				t.Fatalf("frontmatter name does not match directory: %q", m)
			}
		})
	}
}

func TestTestStrategyDescriptionPreservesDiscoveryTriggers(t *testing.T) {
	data, err := fs.ReadFile(Skills(), "test-strategy/SKILL.md")
	if err != nil {
		t.Fatalf("SKILL.md missing: %v", err)
	}
	description := regexp.MustCompile(`(?m)^description:\s*"?([^"\n]+?)"?\s*$`).FindSubmatch(data)
	if description == nil {
		t.Fatal("test-strategy frontmatter has no single-line description")
	}
	const want = "Trigger: haz test stragety, haz test strategy, test-strategy, haz el testing, testea esto, prueba esto, test this, test the app, test my change, run the testing, calibra el testing, calibrate the testing, test strategy, what to test, where to start testing, test planning, testing priorities, test altitude, low coverage, legacy code testing, test plan for the app, testing roadmap, plan de testing, roadmap de pruebas. Organic entry point for testing: infers scope and mode from repository state, builds or resumes a persisted test plan, and executes it through specialized testing skills."
	if got := string(description[1]); got != want {
		t.Fatalf("test-strategy description = %q, want %q", got, want)
	}
}

// The skill tells a session which binary it was written for; the two versions must move together,
// or a session cannot tell whether the tool it has is the tool the skill expects.
func TestSkillNamesTheTppVersionItRequires(t *testing.T) {
	data, err := fs.ReadFile(Skills(), "test-strategy/SKILL.md")
	if err != nil {
		t.Fatalf("SKILL.md missing: %v", err)
	}
	field := requiredVersion(data)
	if field == nil {
		t.Fatalf("test-strategy frontmatter has no requires_tpp; this build is %s", buildinfo.Version)
	}
	if got := string(field[1]); got != buildinfo.Version {
		t.Fatalf("test-strategy requires tpp %s, but this build is %s", got, buildinfo.Version)
	}
	if !strings.Contains(string(data), "make build") ||
		!strings.Contains(string(data), "go install github.com/alesierraalta/tpp/cmd/tpp@latest") {
		t.Fatalf("test-strategy requires tpp %s but names no install path", buildinfo.Version)
	}
}

// requiredVersion reads the binary version a skill's frontmatter requires: requires_tpp, or requires_tpp
// in a skill written before the rename.
func requiredVersion(skill []byte) [][]byte {
	for _, key := range []string{"requires_tpp", "requires_rdd_plus"} {
		if m := regexp.MustCompile(`(?m)^\s*` + key + `:\s*"?([0-9]+\.[0-9]+\.[0-9]+)"?\s*$`).FindSubmatch(skill); m != nil {
			return m
		}
	}
	return nil
}

func TestNoRunArtifactsAreEmbedded(t *testing.T) {
	banned := regexp.MustCompile(`(^|/)(__pycache__|\.runs|results)(/|$)`)
	err := fs.WalkDir(Skills(), ".", func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if banned.MatchString(p) {
			t.Errorf("run artifact embedded: %s", p)
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
}

// F24: every number `plan gaps` prints is read from the plan's own cells, so a status saying a sibling was
// invoked is believed even when nothing can prove it, and an inline run of a sibling is indistinguishable from a
// skipped one. The mitigation is disclosure, and this skill is what tells the session to write it: the routing
// ledger entry for an invoked sibling has to say which mode it ran in. This pins that rule, because its removal
// is what puts the plan back to reporting coverage nobody can check.
func TestTestStrategySkillRequiresTheInvocationModeInTheRoutingLedger(t *testing.T) {
	data, err := fs.ReadFile(Skills(), "test-strategy/SKILL.md")
	if err != nil {
		t.Fatalf("SKILL.md missing: %v", err)
	}
	text := string(data)
	for _, want := range []struct{ phrase, contract string }{
		{"routing ledger", "the reply section that carries the per-sibling verdicts, so the mode has somewhere to live"},
		{"`inline: <path to its SKILL.md>`", "the mode an inline run has to report, which is the only thing that separates it from a skipped one"},
		{"`skipped`", "the other entry the same ledger rule has to carry"},
	} {
		if !strings.Contains(text, want.phrase) {
			t.Errorf("test-strategy no longer names %s: %s", want.phrase, want.contract)
		}
	}
}

package plan

import (
	"errors"
	"io/fs"
	"path/filepath"
	"strings"
	"testing"
)

// declares answers body for the current declaration and nothing for any other file, the way a repository
// that carries only .tsp.json reads.
func declares(body string) Reader {
	return declaresAs(ConfigName, body)
}

func declaresAs(name, body string) Reader {
	return func(path string) (string, error) {
		if filepath.Base(path) == name {
			return body, nil
		}
		return "", fs.ErrNotExist
	}
}

func TestDeclaredPathReadsTheRepositoryDeclaration(t *testing.T) {
	root := t.TempDir()
	read := declares(`{"planPath":"docs/testing/custom-plan.md"}`)
	var answered string
	got, err := DeclaredPath(root, func(path string) (string, error) {
		body, err := read(path)
		if err == nil {
			answered = path
		}
		return body, err
	})
	if err != nil {
		t.Fatal(err)
	}
	if got != "docs/testing/custom-plan.md" {
		t.Fatalf("path = %q", got)
	}
	if answered != filepath.Join(root, ConfigName) {
		t.Fatalf("declaration read from %q, want %q", answered, filepath.Join(root, ConfigName))
	}
}

func TestDeclaredPathIsAbsentWhenTheRepositoryDeclaresNothing(t *testing.T) {
	got, err := DeclaredPath(t.TempDir(), declares(`{}`))
	if err != nil {
		t.Fatal(err)
	}
	if got != "" {
		t.Fatalf("path = %q, want empty", got)
	}
}

func TestResolvePathFallsBackToTheDefault(t *testing.T) {
	got, err := ResolvePath(t.TempDir(), func(string) (string, error) {
		return "", fs.ErrNotExist
	})
	if err != nil {
		t.Fatal(err)
	}
	if got != DefaultPath {
		t.Fatalf("path = %q, want %q", got, DefaultPath)
	}
}

func TestResolveFromRootTakesAnAbsolutePathAsGiven(t *testing.T) {
	const absolute = "/tmp/absolute-plan.md"
	got, err := ResolveFromRoot("/repository", "--path", absolute)
	if err != nil {
		t.Fatal(err)
	}
	if got != absolute {
		t.Fatalf("path = %q, want %q", got, absolute)
	}
}

func TestResolveFromRootResolvesARelativePathAgainstTheRoot(t *testing.T) {
	root := t.TempDir()
	got, err := ResolveFromRoot(root, "--path", "docs/testing/plan.md")
	if err != nil {
		t.Fatal(err)
	}
	want := filepath.Join(root, "docs/testing/plan.md")
	if got != want {
		t.Fatalf("path = %q, want %q", got, want)
	}
}

func TestResolveFromRootRefusesARelativeEscape(t *testing.T) {
	_, err := ResolveFromRoot("/repository", "--path", "../../outside.md")
	if err == nil || !strings.Contains(err.Error(), "--path") {
		t.Fatalf("error = %v, want an error naming --path", err)
	}
}

func TestDeclaredPathRefusesAnUnknownKey(t *testing.T) {
	_, err := DeclaredPath(t.TempDir(), declares(`{"planpath":"docs/testing/custom-plan.md"}`))
	if err == nil || !strings.Contains(err.Error(), ConfigName) {
		t.Fatalf("error = %v, want an error naming %s", err, ConfigName)
	}
}

func TestDeclaredPathRefusesAnEmptyPlanPath(t *testing.T) {
	for _, value := range []string{`{"planPath":""}`, `{"planPath":"   "}`} {
		t.Run(value, func(t *testing.T) {
			_, err := DeclaredPath(t.TempDir(), declares(value))
			if err == nil || !strings.Contains(err.Error(), ConfigName) {
				t.Fatalf("error = %v, want an error naming %s", err, ConfigName)
			}
		})
	}
}

func TestDeclaredPathRefusesAPathThatEscapesTheWorktree(t *testing.T) {
	_, err := DeclaredPath(t.TempDir(), declares(`{"planPath":"../outside.md"}`))
	if err == nil || !strings.Contains(err.Error(), ConfigName) {
		t.Fatalf("error = %v, want an error naming %s", err, ConfigName)
	}
}

func TestResolvePathRefusesAMalformedDeclaration(t *testing.T) {
	_, err := ResolvePath(t.TempDir(), declares(`{`))
	if err == nil || !strings.Contains(err.Error(), ConfigName) {
		t.Fatalf("error = %v, want an error naming %s", err, ConfigName)
	}
}

func TestDeclaredPathRefusesAnUnreadableDeclaration(t *testing.T) {
	_, err := DeclaredPath(t.TempDir(), func(string) (string, error) {
		return "", errors.New("permission denied")
	})
	if err == nil || !strings.Contains(err.Error(), ConfigName) || !strings.Contains(err.Error(), "permission denied") {
		t.Fatalf("error = %v, want the config name and read error", err)
	}
}

func TestDeclaredRunReadsTheDeclaration(t *testing.T) {
	got, err := DeclaredRun(t.TempDir(), declares(`{"planPath":"docs/testing/custom-plan.md","run":"redis-stream-pool"}`))
	if err != nil {
		t.Fatal(err)
	}
	if got != "redis-stream-pool" {
		t.Fatalf("run = %q, want redis-stream-pool", got)
	}
}

func TestDeclaredRunIsAbsentWhenTheKeyIsAbsent(t *testing.T) {
	got, err := DeclaredRun(t.TempDir(), declares(`{"planPath":"docs/testing/custom-plan.md"}`))
	if err != nil {
		t.Fatal(err)
	}
	if got != "" {
		t.Fatalf("run = %q, want empty", got)
	}
}

func TestDeclaredRunRefusesABadSlug(t *testing.T) {
	_, err := DeclaredRun(t.TempDir(), declares(`{"run":"Bad_Slug"}`))
	if err == nil || !strings.Contains(err.Error(), ConfigName) {
		t.Fatalf("error = %v, want a declaration error", err)
	}
}

func TestDeclaredRunRefusesAllAndNone(t *testing.T) {
	for _, run := range []string{"all", "none"} {
		t.Run(run, func(t *testing.T) {
			_, err := DeclaredRun(t.TempDir(), declares(`{"run":"`+run+`"}`))
			if err == nil || !strings.Contains(err.Error(), ConfigName) {
				t.Fatalf("error = %v, want a declaration error", err)
			}
		})
	}
}

func TestDeclaredRunRefusesAnEmptyValue(t *testing.T) {
	for _, value := range []string{`{"run":""}`, `{"run":"   "}`} {
		t.Run(value, func(t *testing.T) {
			_, err := DeclaredRun(t.TempDir(), declares(value))
			if err == nil || !strings.Contains(err.Error(), ConfigName) {
				t.Fatalf("error = %v, want a declaration error", err)
			}
		})
	}
}

func TestResolveReadsTheDeclarationOnceForPathAndRun(t *testing.T) {
	calls := 0
	read := declares(`{"planPath":"docs/testing/custom-plan.md","run":"redis-stream-pool"}`)
	path, run, err := Resolve(t.TempDir(), func(path string) (string, error) {
		if filepath.Base(path) == ConfigName {
			calls++
		}
		return read(path)
	})
	if err != nil {
		t.Fatal(err)
	}
	if path != "docs/testing/custom-plan.md" || run != "redis-stream-pool" {
		t.Fatalf("resolved %q, %q", path, run)
	}
	if calls != 1 {
		t.Fatalf("declaration read %d times, want once", calls)
	}
}

func TestResolveSupportsEachRepositoryDeclarationName(t *testing.T) {
	const canonicalConfigName = ".tsp.json"
	const tppLegacyConfigName = ".tpp.json"
	tests := []struct {
		name     string
		planPath string
		run      string
	}{
		{name: canonicalConfigName, planPath: "docs/testing/canonical-plan.md", run: "canonical-run"},
		{name: tppLegacyConfigName, planPath: "docs/testing/tpp-plan.md", run: "tpp-legacy"},
		{name: LegacyConfigName, planPath: "docs/testing/rdd-plan.md", run: "rdd-legacy"},
	}
	for _, tc := range tests {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			reads := 0
			path, run, err := Resolve(t.TempDir(), func(path string) (string, error) {
				if filepath.Base(path) != tc.name {
					return "", fs.ErrNotExist
				}
				reads++
				return `{"planPath":"` + tc.planPath + `","run":"` + tc.run + `"}`, nil
			})
			if err != nil {
				t.Fatal(err)
			}
			if path != tc.planPath || run != tc.run {
				t.Fatalf("resolved %q, %q; want %q, %q", path, run, tc.planPath, tc.run)
			}
			if reads != 1 {
				t.Fatalf("declaration body read %d times, want once", reads)
			}
		})
	}
}

func TestResolveRefusesMultipleRepositoryDeclarationNames(t *testing.T) {
	const canonicalConfigName = ".tsp.json"
	const tppLegacyConfigName = ".tpp.json"
	tests := []struct {
		name    string
		present []string
		absent  string
	}{
		{name: "canonical and tpp legacy", present: []string{canonicalConfigName, tppLegacyConfigName}, absent: LegacyConfigName},
		{name: "canonical and rdd-plus legacy", present: []string{canonicalConfigName, LegacyConfigName}, absent: tppLegacyConfigName},
		{name: "both legacy names", present: []string{tppLegacyConfigName, LegacyConfigName}, absent: canonicalConfigName},
		{name: "all names", present: []string{canonicalConfigName, tppLegacyConfigName, LegacyConfigName}},
	}
	for _, tc := range tests {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			declarations := make(map[string]string, len(tc.present))
			for _, name := range tc.present {
				declarations[name] = `{"planPath":"docs/testing/shared-plan.md","run":"shared-run"}`
			}
			_, _, err := Resolve(t.TempDir(), func(path string) (string, error) {
				body, present := declarations[filepath.Base(path)]
				if !present {
					return "", fs.ErrNotExist
				}
				return body, nil
			})
			if err == nil {
				t.Fatal("Resolve succeeded with multiple declaration files")
			}
			message := err.Error()
			for _, name := range tc.present {
				if !strings.Contains(message, name) {
					t.Errorf("error %q does not name present declaration %q", message, name)
				}
			}
			if tc.absent != "" && strings.Contains(message, tc.absent) {
				t.Errorf("error %q names absent declaration %q", message, tc.absent)
			}
		})
	}
}

func TestTheCanonicalDeclarationIsNamedTsp(t *testing.T) {
	if ConfigName != ".tsp.json" {
		t.Fatalf("ConfigName = %q, want .tsp.json", ConfigName)
	}
}

// A repository declared before the rename keeps working without anyone renaming its file.
func TestTheLegacyDeclarationIsReadWhenTheCurrentOneIsAbsent(t *testing.T) {
	got, err := DeclaredPath(t.TempDir(), declaresAs(LegacyConfigName, `{"planPath":"docs/testing/legacy-plan.md"}`))
	if err != nil {
		t.Fatal(err)
	}
	if got != "docs/testing/legacy-plan.md" {
		t.Fatalf("path = %q, want the legacy declaration's", got)
	}
}

func TestAMalformedLegacyDeclarationIsNamedInTheError(t *testing.T) {
	_, err := DeclaredPath(t.TempDir(), declaresAs(LegacyConfigName, `{`))
	if err == nil || !strings.Contains(err.Error(), LegacyConfigName) {
		t.Fatalf("error = %v, want an error naming %s", err, LegacyConfigName)
	}
}

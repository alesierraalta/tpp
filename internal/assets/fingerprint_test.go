package assets

import (
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// fingerprintRepo runs the embedded fingerprint.sh over a fresh git repository holding the given files and
// answers its digest; the declared plan is excluded from it, so editing the plan must not move the digest.
func fingerprintRepo(t *testing.T, files map[string]string) (string, func(path, body string) string) {
	t.Helper()
	for _, tool := range []string{"bash", "git", "sha256sum"} {
		if _, err := exec.LookPath(tool); err != nil {
			t.Skipf("%s not available", tool)
		}
	}
	script, err := fs.ReadFile(Skills(), "test-strategy/assets/fingerprint.sh")
	if err != nil {
		t.Fatal(err)
	}
	scriptPath := filepath.Join(t.TempDir(), "fingerprint.sh")
	if err := os.WriteFile(scriptPath, script, 0o755); err != nil {
		t.Fatal(err)
	}
	repo := t.TempDir()
	if out, err := exec.Command("git", "-C", repo, "init", "-q").CombinedOutput(); err != nil {
		t.Fatalf("git init: %v\n%s", err, out)
	}
	write := func(path, body string) {
		full := filepath.Join(repo, path)
		if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(full, []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	for path, body := range files {
		write(path, body)
	}
	run := func() string {
		out, err := exec.Command("bash", scriptPath, "--root", repo).Output()
		if err != nil {
			t.Fatalf("fingerprint.sh: %v", err)
		}
		return strings.TrimSpace(string(out))
	}
	return run(), func(path, body string) string {
		write(path, body)
		return run()
	}
}

// The script reads the plan declaration under its current name and, for a repository declared before the
// rename, under the legacy one; when both exist the current one decides.
func TestFingerprintExcludesThePlanTheDeclarationNames(t *testing.T) {
	cases := []struct {
		name  string
		files map[string]string
	}{
		{"current declaration", map[string]string{".tpp.json": `{"planPath":"plans/p.md"}`}},
		{"legacy declaration", map[string]string{".rdd-plus.json": `{"planPath":"plans/p.md"}`}},
		{"current declaration wins", map[string]string{
			".tpp.json":      `{"planPath":"plans/p.md"}`,
			".rdd-plus.json": `{"planPath":"plans/other.md"}`,
		}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			files := map[string]string{"src/a.txt": "a", "plans/p.md": "plan v1", "plans/other.md": "other"}
			for k, v := range tc.files {
				files[k] = v
			}
			before, edit := fingerprintRepo(t, files)
			if after := edit("plans/p.md", "plan v2"); after != before {
				t.Fatalf("editing the declared plan moved the fingerprint: %s -> %s", before, after)
			}
			if after := edit("src/a.txt", "changed"); after == before {
				t.Fatal("editing a source file left the fingerprint unchanged")
			}
		})
	}
}

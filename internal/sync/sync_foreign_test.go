package sync

import (
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"
)

// syncWithin runs Sync and fails instead of hanging when the walk never ends: a link to an ancestor
// makes every level re-list the whole tree.
func syncWithin(t *testing.T, cfg string, opts Options) (Report, error) {
	t.Helper()
	type result struct {
		report Report
		err    error
	}
	done := make(chan result, 1)
	go func() {
		report, err := Sync(cfg, bin, opts)
		done <- result{report, err}
	}()
	select {
	case r := <-done:
		return r.report, r.err
	case <-time.After(30 * time.Second):
		t.Fatal("sync did not finish: the skills walk does not terminate on a symlink cycle")
		return Report{}, nil
	}
}

func symlinkOrSkip(t *testing.T, target, link string) {
	t.Helper()
	if err := os.Symlink(target, link); err != nil {
		t.Skipf("symlinks unavailable: %v", err)
	}
}

// A user's skill folder can hold a link back to itself or to an ancestor. Sync reports such a link once
// as a user path and leaves it alone, instead of failing the whole plan or walking it forever.
func TestSyncSkipsSymlinkCyclesInUserSkills(t *testing.T) {
	t.Setenv("TPP_HOME", t.TempDir())
	cfg := t.TempDir()
	mine := filepath.Join(cfg, "skills", "mine")
	if err := os.MkdirAll(mine, 0o755); err != nil {
		t.Fatal(err)
	}
	userFile := filepath.Join(mine, "SKILL.md")
	if err := os.WriteFile(userFile, []byte("user skill"), 0o644); err != nil {
		t.Fatal(err)
	}
	self := filepath.Join(mine, "mine")
	symlinkOrSkip(t, mine, self)
	up := filepath.Join(mine, "up")
	symlinkOrSkip(t, filepath.Join(cfg, "skills"), up)

	for _, dryRun := range []bool{true, false} {
		report, err := syncWithin(t, cfg, Options{DryRun: dryRun})
		if err != nil {
			t.Fatalf("dry-run=%v: sync failed on a user symlink cycle: %v", dryRun, err)
		}
		for _, want := range []string{userFile, self, up} {
			if !slices.Contains(report.Foreign, want) {
				t.Errorf("dry-run=%v: %s not reported as a user path; foreign = %v", dryRun, want, report.Foreign)
			}
		}
		for _, path := range report.Foreign {
			if strings.HasPrefix(path, self+string(filepath.Separator)) || strings.HasPrefix(path, up+string(filepath.Separator)) {
				t.Errorf("dry-run=%v: walked into a cycle: %s", dryRun, path)
			}
		}
	}
	if data, err := os.ReadFile(userFile); err != nil || string(data) != "user skill" {
		t.Fatalf("user file changed: %q, %v", data, err)
	}
	for _, link := range []string{self, up} {
		if fi, err := os.Lstat(link); err != nil || fi.Mode()&os.ModeSymlink == 0 {
			t.Fatalf("user link %s was not left in place: %v", link, err)
		}
	}
}

// A user folder sync cannot read is reported as a user path, not a reason to abort.
func TestSyncReportsAnUnreadableUserFolderAndContinues(t *testing.T) {
	if os.Geteuid() == 0 {
		t.Skip("root reads every folder")
	}
	t.Setenv("TPP_HOME", t.TempDir())
	cfg := t.TempDir()
	private := filepath.Join(cfg, "skills", "private")
	if err := os.MkdirAll(private, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(private, 0o000); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chmod(private, 0o755) })

	report, err := syncWithin(t, cfg, Options{DryRun: true})
	if err != nil {
		t.Fatalf("sync failed on an unreadable user folder: %v", err)
	}
	if !slices.Contains(report.Foreign, private) {
		t.Fatalf("%s not reported as a user path; foreign = %v", private, report.Foreign)
	}
}

// The skills folder itself is sync's to manage: when it cannot be read, sync still refuses.
func TestSyncStillRefusesAnUnreadableSkillsFolder(t *testing.T) {
	if os.Geteuid() == 0 {
		t.Skip("root reads every folder")
	}
	t.Setenv("TPP_HOME", t.TempDir())
	cfg := t.TempDir()
	skills := filepath.Join(cfg, "skills")
	if err := os.MkdirAll(skills, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(skills, 0o000); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chmod(skills, 0o755) })

	if _, err := syncWithin(t, cfg, Options{DryRun: true}); err == nil {
		t.Fatal("sync accepted an unreadable skills folder")
	}
}

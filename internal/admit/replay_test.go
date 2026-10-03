package admit

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/alesierraalta/tsp/internal/evidence"
	"github.com/alesierraalta/tsp/internal/plan"
)

// gitRun runs one git command against dir and fails the test with git's own words when it does.
func gitRun(t *testing.T, dir string, args ...string) {
	t.Helper()
	cmd := exec.Command("git", append([]string{"-C", dir}, args...)...)
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("git %s: %v\n%s", strings.Join(args, " "), err, out)
	}
}

// oneCommitRepo lays down a one-file repository with a single commit, so a replay has a tree git can list.
func oneCommitRepo(t *testing.T, content string) string {
	t.Helper()
	repo := t.TempDir()
	if err := os.WriteFile(filepath.Join(repo, "src.go"), []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
	gitRun(t, repo, "init")
	gitRun(t, repo, "add", "src.go")
	gitRun(t, repo, "-c", "user.name=r", "-c", "user.email=r@x", "commit", "-m", "x")
	return repo
}

// A replay runs both halves of the claim against a copy of the tree, not against the repository: the mutated half
// sees the edit, the restored half sees the bytes the edit started from, and the repository is left untouched. The
// copy is thrown away when the replay returns.
func TestReplayMutationEditsACopyAndPutsItBack(t *testing.T) {
	const original = "package p\n\nvar x = 1\n"
	repo := oneCommitRepo(t, original)
	var dirs []string
	var seen [][]byte
	run := func(_ context.Context, dir, command string) (string, error) {
		dirs = append(dirs, dir)
		data, err := os.ReadFile(filepath.Join(dir, "src.go"))
		if err != nil {
			t.Fatalf("the runner could not read the tree it was handed: %v", err)
		}
		seen = append(seen, data)
		if len(seen) == 1 {
			return "boom\n", errors.New("exit status 1")
		}
		return "one\n", nil
	}
	got := replayWith(run, time.Second)(plan.Mutation{Old: "x", New: "y", Path: "src.go", Line: 3}, repo, "printf one")
	if got.MutatedOutput != "boom\n" || got.MutatedErr == nil || got.RestoredOutput != "one\n" || got.RestoredErr != nil {
		t.Fatalf("replay = %+v, want the mutated half red and the restored half green", got)
	}
	if len(dirs) != 2 {
		t.Fatalf("the runner saw %d calls, want the mutated half and the restored half", len(dirs))
	}
	if dirs[0] != dirs[1] {
		t.Fatalf("the two halves ran in %q and %q, want one copy for both", dirs[0], dirs[1])
	}
	if dirs[0] == repo || strings.HasPrefix(dirs[0], repo+string(filepath.Separator)) {
		t.Fatalf("the replay ran inside the repository %q, want a copy it owns", dirs[0])
	}
	if string(seen[0]) != "package p\n\nvar y = 1\n" {
		t.Fatalf("the mutated half saw %q, want the edit applied", seen[0])
	}
	if string(seen[1]) != original {
		t.Fatalf("the restored half saw %q, want the bytes the edit started from", seen[1])
	}
	if _, err := os.Stat(dirs[0]); !os.IsNotExist(err) {
		t.Fatalf("the staged copy %q still exists after the replay (stat err: %v)", dirs[0], err)
	}
	if data, err := os.ReadFile(filepath.Join(repo, "src.go")); err != nil || string(data) != original {
		t.Fatalf("the repository's src.go = %q (err %v), want it byte-identical to %q", data, err, original)
	}
}

// The staged copy carries what git knows: tracked files, untracked-but-not-ignored files, and each file's
// permission bits exactly. It is not the whole directory: .git is never copied, and an ignored file was never part
// of the tree a row is claiming anything about.
func TestStageTreeCarriesTheGitKnownFilesWithTheirModes(t *testing.T) {
	repo := t.TempDir()
	if err := os.WriteFile(filepath.Join(repo, "keep.txt"), []byte("keep\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(repo, "run.sh"), []byte("#!/bin/sh\necho hi\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(repo, ".gitignore"), []byte("ignored.txt\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	gitRun(t, repo, "init")
	gitRun(t, repo, "add", "keep.txt", "run.sh", ".gitignore")
	gitRun(t, repo, "-c", "user.name=r", "-c", "user.email=r@x", "commit", "-m", "x")
	if err := os.WriteFile(filepath.Join(repo, "loose.txt"), []byte("loose\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(repo, "ignored.txt"), []byte("ignored\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	staged, err := stageTree(repo)
	if err != nil {
		t.Fatalf("stageTree: %v", err)
	}
	defer os.RemoveAll(staged)

	for name, want := range map[string]string{
		"keep.txt":  "keep\n",
		"run.sh":    "#!/bin/sh\necho hi\n",
		"loose.txt": "loose\n",
	} {
		data, err := os.ReadFile(filepath.Join(staged, name))
		if err != nil {
			t.Fatalf("%s is missing from the staged tree: %v", name, err)
		}
		if string(data) != want {
			t.Fatalf("%s = %q in the staged tree, want %q", name, data, want)
		}
	}
	if _, err := os.Stat(filepath.Join(staged, "ignored.txt")); !os.IsNotExist(err) {
		t.Fatalf("the staged tree carries an ignored file (stat err: %v)", err)
	}
	if _, err := os.Stat(filepath.Join(staged, ".git")); !os.IsNotExist(err) {
		t.Fatalf("the staged tree carries a .git directory (stat err: %v)", err)
	}
	info, err := os.Stat(filepath.Join(staged, "run.sh"))
	if err != nil {
		t.Fatal(err)
	}
	if info.Mode().Perm() != 0o755 {
		t.Fatalf("run.sh has mode %v in the staged tree, want 0755", info.Mode().Perm())
	}
}

// The edit lands on the line the cell names and nowhere else: `old` occurs on three lines, and only the named one
// changes. The original bytes and the file's mode come back so the caller can put the file exactly back.
func TestApplyMutationEditsOnlyTheNamedLine(t *testing.T) {
	root := t.TempDir()
	const content = "old a\nold b\nold c\n"
	path := filepath.Join(root, "src.go")
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
	original, mode, err := applyMutation(root, plan.Mutation{Old: "old", New: "new", Path: "src.go", Line: 2})
	if err != nil {
		t.Fatalf("applyMutation: %v", err)
	}
	if string(original) != content {
		t.Fatalf("applyMutation returned %q, want the file's own bytes %q", original, content)
	}
	if mode.Perm() != 0o600 {
		t.Fatalf("applyMutation returned mode %v, want 0600", mode.Perm())
	}
	got, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != "old a\nnew b\nold c\n" {
		t.Fatalf("after the edit the file = %q, want only line 2 changed", got)
	}
}

// Defense in depth for the staged copy: ValidateMutation refuses a path that climbs out of the tree before a replay
// is ever asked for, and applyMutation refuses it again rather than reading or writing outside the copy it owns.
// replayWith turns that into a mutation-not-replayed refusal and runs nothing.
func TestReplayRefusesAMutationThatClimbsOutOfTheStagedTree(t *testing.T) {
	repo := oneCommitRepo(t, "package p\n\nvar x = 1\n")
	climb := plan.Mutation{Old: "x", New: "y", Path: "../outside.go", Line: 1}
	if _, _, err := applyMutation(repo, climb); err == nil {
		t.Fatal("applyMutation accepted a path that climbs out of the staged tree")
	}
	ran := 0
	got := replayWith(func(context.Context, string, string) (string, error) {
		ran++
		return "", nil
	}, time.Second)(climb, repo, "printf one")
	var refusal evidence.Refusal
	if !errors.As(got.MutatedErr, &refusal) || refusal.Reason != evidence.ReasonMutationNotReplay {
		t.Fatalf("replay = %+v, want a %s refusal", got, evidence.ReasonMutationNotReplay)
	}
	if ran != 0 {
		t.Fatalf("the replay ran the command %d times, want none", ran)
	}
}

// A tree git cannot list cannot be staged, so the replay refuses the claim rather than running the command against
// a copy it could not build.
func TestReplayRefusesATreeGitCannotList(t *testing.T) {
	dir := t.TempDir() // a plain directory, not a repository
	ran := 0
	got := replayWith(func(context.Context, string, string) (string, error) {
		ran++
		return "", nil
	}, time.Second)(plan.Mutation{Old: "x", New: "y", Path: "src.go", Line: 1}, dir, "printf one")
	var refusal evidence.Refusal
	if !errors.As(got.MutatedErr, &refusal) || refusal.Reason != evidence.ReasonMutationNotReplay {
		t.Fatalf("replay = %+v, want a %s refusal", got, evidence.ReasonMutationNotReplay)
	}
	if ran != 0 {
		t.Fatalf("the replay ran the command %d times, want none", ran)
	}
}

// The restored half is only a control if the bytes that landed are the bytes the edit started from, so restoreFile
// verifies the file it just wrote instead of trusting the write.
func TestRestoreFileRefusesBytesThatDoNotMatch(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "src.go")
	original := []byte("package p\n\nvar x = 1\n")
	if err := os.WriteFile(path, []byte("mutated\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	sum := sha256.Sum256(original)
	good := hex.EncodeToString(sum[:])
	if err := restoreFile(path, original, 0o644, "not-the-digest"); err == nil {
		t.Fatal("restoreFile accepted bytes that do not match the digest it was given")
	}
	if err := restoreFile(path, original, 0o644, good); err != nil {
		t.Fatalf("restoreFile refused the bytes it just wrote: %v", err)
	}
	if data, err := os.ReadFile(path); err != nil || string(data) != string(original) {
		t.Fatalf("after restore the file = %q (err %v), want %q", data, err, original)
	}
}

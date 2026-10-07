package admit

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"github.com/alesierraalta/tsp/internal/evidence"
	"github.com/alesierraalta/tsp/internal/plan"
)

const (
	// SandboxReadOnly is how the container sees the tree for a row's own command: the command must not reach this
	// machine's working tree, so a write fails against the mount instead of landing.
	SandboxReadOnly = "ro"
	// SandboxWritable is how the container sees the tree for a replay, which edits a copy it owns and then puts the
	// file back, so the mount has to allow the write.
	SandboxWritable = "rw"
)

// stageTree copies the files git knows about dir into a fresh temporary directory and returns its path. The caller
// owns that directory and removes it; a replay does that with a deferred RemoveAll.
//
// The copy is what git knows rather than `cp -a`, and that was measured instead of assumed. This repository's whole
// tree is 142 MB, and dragging it also drags .git, which carries the read-only views a candidate is reviewed
// through; the files git knows were 320 files, 2.9 MB, and 0.236 s to copy. The limit is stated rather than hidden:
// the copy carries what git knows and no `.git`, so a row whose command needs repository metadata is refused by its
// own failing replay instead of being admitted against a tree it never saw.
func stageTree(dir string) (string, error) {
	staged, err := os.MkdirTemp("", "tpp-replay-")
	if err != nil {
		return "", fmt.Errorf("stage the tree: %w", err)
	}
	names, err := gitKnownFiles(dir)
	if err != nil {
		os.RemoveAll(staged)
		return "", err
	}
	for _, name := range names {
		if err := copyInto(dir, staged, name); err != nil {
			os.RemoveAll(staged)
			return "", err
		}
	}
	return staged, nil
}

// gitKnownFiles returns the paths git knows in dir: tracked first, then untracked-but-not-ignored, deduped in that
// order. Each listing gets its own ten-second deadline, so a repository that hangs cannot hang a replay.
func gitKnownFiles(dir string) ([]string, error) {
	tracked, err := gitLsFiles(dir, "ls-files", "-z")
	if err != nil {
		return nil, err
	}
	untracked, err := gitLsFiles(dir, "ls-files", "--others", "--exclude-standard", "-z")
	if err != nil {
		return nil, err
	}
	seen := map[string]bool{}
	names := make([]string, 0, len(tracked)+len(untracked))
	for _, name := range append(tracked, untracked...) {
		if name == "" || seen[name] {
			continue
		}
		seen[name] = true
		names = append(names, name)
	}
	return names, nil
}

// gitLsFiles runs one `git -C dir ls-files` listing and splits its NUL-separated output. Git's own words are kept
// in the error, because "not a git repository" is the whole diagnosis a caller needs.
func gitLsFiles(dir string, args ...string) ([]string, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, "git", append([]string{"-C", dir}, args...)...)
	out, err := cmd.CombinedOutput()
	if err != nil {
		return nil, fmt.Errorf("git -C %s %s: %w: %s", dir, strings.Join(args, " "), err, strings.TrimSpace(string(out)))
	}
	var names []string
	for _, name := range strings.Split(string(out), "\x00") {
		if name != "" {
			names = append(names, name)
		}
	}
	return names, nil
}

// copyInto copies one git-known file from the tree into the staged copy, preserving its bytes and its permission
// bits exactly: the write uses the file's own mode, and the chmod that follows repairs the bits umask would
// otherwise shave off.
func copyInto(tree, staged, name string) error {
	src := filepath.Join(tree, filepath.FromSlash(name))
	info, err := os.Stat(src)
	if err != nil {
		return fmt.Errorf("stage %s: %w", name, err)
	}
	data, err := os.ReadFile(src)
	if err != nil {
		return fmt.Errorf("stage %s: %w", name, err)
	}
	dst := filepath.Join(staged, filepath.FromSlash(name))
	if err := os.MkdirAll(filepath.Dir(dst), 0o755); err != nil {
		return fmt.Errorf("stage %s: %w", name, err)
	}
	if err := os.WriteFile(dst, data, info.Mode().Perm()); err != nil {
		return fmt.Errorf("stage %s: %w", name, err)
	}
	if err := os.Chmod(dst, info.Mode().Perm()); err != nil {
		return fmt.Errorf("stage %s: %w", name, err)
	}
	return nil
}

// applyMutation applies one edit to the staged copy and returns the file's original bytes and mode so the caller
// can put the file exactly back.
//
// The path is checked again here, against the staged copy itself: ValidateMutation already refused a path that
// climbs out of the tree, and this refuses one that would resolve anywhere but inside root before anything is read
// or written. The edit lands on the line the cell names, replaces the first occurrence of the old text there, and
// touches no other line.
func applyMutation(root string, m plan.Mutation) (original []byte, mode fs.FileMode, err error) {
	full := filepath.Join(root, filepath.FromSlash(m.Path))
	rel, relErr := filepath.Rel(root, full)
	if relErr != nil || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
		return nil, 0, fmt.Errorf("%q resolves outside the staged tree %s, so the edit would land on a file the copy does not own", m.Path, root)
	}
	info, err := os.Stat(full)
	if err != nil {
		return nil, 0, fmt.Errorf("%s is not in the staged copy: %w", m.Path, err)
	}
	if info.IsDir() {
		return nil, 0, fmt.Errorf("%s is a directory in the staged copy, and a mutation edits a file", m.Path)
	}
	original, err = os.ReadFile(full)
	if err != nil {
		return nil, 0, fmt.Errorf("%s could not be read in the staged copy: %w", m.Path, err)
	}
	mode = info.Mode()
	lines := strings.Split(string(original), "\n")
	if m.Line < 1 || m.Line > len(lines) {
		return nil, 0, fmt.Errorf("%s has %d lines in the staged copy and the edit names line %d", m.Path, len(lines), m.Line)
	}
	if !strings.Contains(lines[m.Line-1], m.Old) {
		return nil, 0, fmt.Errorf("line %d of %s does not hold %q in the staged copy", m.Line, m.Path, m.Old)
	}
	lines[m.Line-1] = strings.Replace(lines[m.Line-1], m.Old, m.New, 1)
	if err := os.WriteFile(full, []byte(strings.Join(lines, "\n")), mode.Perm()); err != nil {
		return nil, 0, fmt.Errorf("%s could not be written in the staged copy: %w", m.Path, err)
	}
	return original, mode, nil
}

// restoreFile writes the original bytes back, restores the mode, and proves the file is byte-identical to what the
// edit started from: the restored half is only a control if the tree it runs against is the tree the first
// observation was taken from. A mismatch names the file and both digests, because that is the whole diagnosis.
func restoreFile(path string, original []byte, mode fs.FileMode, wantHex string) error {
	if err := os.WriteFile(path, original, mode.Perm()); err != nil {
		return fmt.Errorf("restore %s: %w", path, err)
	}
	if err := os.Chmod(path, mode.Perm()); err != nil {
		return fmt.Errorf("restore %s: %w", path, err)
	}
	got, err := os.ReadFile(path)
	if err != nil {
		return fmt.Errorf("restore %s: %w", path, err)
	}
	sum := sha256.Sum256(got)
	if hex.EncodeToString(sum[:]) != wantHex {
		return fmt.Errorf("restore %s: the bytes written back digest to sha256:%s, not the sha256:%s the edit started from", path, hex.EncodeToString(sum[:]), wantHex)
	}
	return nil
}

// replayWith turns a runner into the replay the admission calls. The sequence is the claim's own shape: stage a
// copy of the tree, apply the edit, hash the bytes it started from, run the command against the mutated copy, put
// the file exactly back under its own digest, and run the command against the restored copy. Each half gets the
// whole timeout as its own deadline, because each half is one command's observation.
//
// What a replay does not undo: only the file the edit names is restored, so the mutated half may leave other files
// changed inside the copy. That is deliberate; the copy is thrown away, and "restoring" files the mutation never
// declared would be inventing edits the row did not make.
//
// Any staging, apply, or restore failure is returned as a mutation-not-replayed refusal: those failures are about
// the replay's own machinery rather than about the row's command, and blaming the row would send the reader to the
// wrong place. Nothing is run once the tree cannot be built or put back.
func replayWith(run Runner, timeout time.Duration) func(plan.Mutation, string, string) evidence.ReplayResult {
	return func(mutation plan.Mutation, dir, command string) evidence.ReplayResult {
		notReplayed := func(what string, err error) evidence.ReplayResult {
			return evidence.ReplayResult{MutatedErr: evidence.Refusal{
				Reason: evidence.ReasonMutationNotReplay,
				Detail: fmt.Sprintf("%s: %v", what, err),
			}}
		}
		staged, err := stageTree(dir)
		if err != nil {
			return notReplayed("the tree could not be staged for a replay", err)
		}
		defer func() {
			if err := os.RemoveAll(staged); err != nil {
				fmt.Fprintf(os.Stderr, "tsp: the staged replay tree %s could not be removed: %v\n", staged, err)
			}
		}()
		original, mode, err := applyMutation(staged, mutation)
		if err != nil {
			return notReplayed("the edit could not be applied to the staged tree", err)
		}
		sum := sha256.Sum256(original)
		path := filepath.Join(staged, filepath.FromSlash(mutation.Path))
		mutatedOutput, mutatedErr := runOnceWithin(run, timeout, staged, command)
		if err := restoreFile(path, original, mode, hex.EncodeToString(sum[:])); err != nil {
			return notReplayed("the tree could not be put back after the edit", err)
		}
		restoredOutput, restoredErr := runOnceWithin(run, timeout, staged, command)
		return evidence.ReplayResult{
			MutatedOutput:  mutatedOutput,
			MutatedErr:     mutatedErr,
			RestoredOutput: restoredOutput,
			RestoredErr:    restoredErr,
		}
	}
}

// runOnceWithin runs one half of a replay under its own full timeout, so --timeout still bounds one command.
func runOnceWithin(run Runner, timeout time.Duration, dir, command string) (string, error) {
	ctx := context.Background()
	if timeout > 0 {
		var cancel context.CancelFunc
		ctx, cancel = context.WithTimeout(ctx, timeout)
		defer cancel()
	}
	return run(ctx, dir, command)
}

// Replay returns the replay the admission calls when it runs in a sandbox. The caller supplies the runner for the
// writable staged copy, because editing the copy and putting it back is exactly what this half of the run is for.
func Replay(run Runner, timeout time.Duration) func(plan.Mutation, string, string) evidence.ReplayResult {
	return replayWith(run, timeout)
}

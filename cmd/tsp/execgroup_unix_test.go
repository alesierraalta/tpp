//go:build unix

package main

import (
	"context"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"testing"
	"time"
)

// The row is over when the shell exits, so the descendant that inherited its pipes is reaped with it:
// before the group kill that descendant kept the row's one stream open and outlived the run itself.
func TestRunShellReapsTheDescendantThatHeldThePipes(t *testing.T) {
	dir := t.TempDir()
	pidFile := filepath.Join(dir, "pid")
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	if output, err := runShell(ctx, dir, "sh -c 'echo $$ > "+pidFile+"; sleep 30' & true"); err == nil || !strings.Contains(err.Error(), "holding its output") {
		t.Fatalf("err = %v (output %q), want the leftover process named for what it is", err, output)
	}
	raw, err := os.ReadFile(pidFile)
	pid, convErr := strconv.Atoi(strings.TrimSpace(string(raw)))
	if err != nil || convErr != nil {
		t.Fatalf("the descendant never recorded a readable pid (%v, %v), raw %q", err, convErr, raw)
	}
	for deadline := time.Now().Add(2 * time.Second); syscall.Kill(pid, 0) == nil; time.Sleep(20 * time.Millisecond) {
		if time.Now().After(deadline) {
			t.Fatalf("descendant pid %d outlived the run that started it", pid)
		}
	}
}

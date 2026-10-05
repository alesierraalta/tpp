//go:build unix

package bench

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"syscall"
	"testing"
	"time"
)

func TestNovelProcessCleansUpDescendants(t *testing.T) {
	if testing.Short() {
		t.Skip("runs the real Node test runner")
	}
	root := t.TempDir()
	fixture := filepath.Join(root, "fixture")
	if err := os.MkdirAll(fixture, 0755); err != nil {
		t.Fatal(err)
	}
	pidFile := filepath.Join(root, "child.pid")
	testFile := filepath.Join(root, "spawn.test.cjs")
	source := `const {test}=require('node:test'); const fs=require('node:fs'); const cp=require('node:child_process'); test('spawn child',()=>{const child=cp.spawn(process.execPath,['-e','setInterval(()=>{},1000)'],{detached:false,stdio:'ignore'}); fs.writeFileSync(` + quoteJS(pidFile) + `,String(child.pid)); child.unref();});`
	if err := os.WriteFile(testFile, []byte(source), 0600); err != nil {
		t.Fatal(err)
	}
	got := runNovelSide(fixture, "", map[string][]byte{"spawn.test.cjs": []byte("ignored")}, []string{"node", "--test", "--test-reporter=tap", testFile}, "node", 5*time.Second, "tests")
	if got.Classification != NovelInconclusive || !strings.Contains(got.Reason, "descendant") {
		t.Fatalf("surviving descendant must make replay inconclusive: %+v", got)
	}
	pidBytes, err := os.ReadFile(pidFile)
	if err != nil {
		t.Fatalf("child pid not recorded: %v", err)
	}
	var pid int
	if _, err := fmt.Sscanf(string(pidBytes), "%d", &pid); err != nil {
		t.Fatal(err)
	}
	deadline := time.Now().Add(time.Second)
	for {
		err := syscall.Kill(pid, 0)
		if errors.Is(err, syscall.ESRCH) {
			break
		}
		if err != nil || time.Now().After(deadline) {
			t.Fatalf("descendant %d still alive or unverifiable: %v", pid, err)
		}
		time.Sleep(10 * time.Millisecond)
	}
}

func TestNovelTimeoutWithPipeHoldingDescendantIsBounded(t *testing.T) {
	if testing.Short() {
		t.Skip("runs the real Node test runner")
	}
	root := t.TempDir()
	fixture := filepath.Join(root, "fixture")
	if err := os.MkdirAll(fixture, 0755); err != nil {
		t.Fatal(err)
	}
	testFile := filepath.Join(root, "hold.test.cjs")
	source := `const {test}=require('node:test'); const cp=require('node:child_process'); test('hang',()=>{cp.spawn(process.execPath,['-e','setInterval(()=>{},1000)'],{detached:false,stdio:'inherit'}); return new Promise(()=>{});});`
	if err := os.WriteFile(testFile, []byte(source), 0600); err != nil {
		t.Fatal(err)
	}
	started := time.Now()
	got := runNovelSide(fixture, "", map[string][]byte{"hold.test.cjs": []byte("ignored")}, []string{"node", "--test", "--test-reporter=tap", testFile}, "node", 250*time.Millisecond, "tests")
	elapsed := time.Since(started)
	if got.Classification != NovelInconclusive || !got.TimedOut {
		t.Fatalf("descendant-holding pipe timeout must be inconclusive: %+v", got)
	}
	if elapsed > 250*time.Millisecond+2*time.Second {
		t.Fatalf("runner exceeded timeout plus bounded WaitDelay: %s", elapsed)
	}
}

//go:build darwin || dragonfly || freebsd || illumos || linux || netbsd || openbsd

package plan

import (
	"os"
	"strings"
	"testing"
	"time"
)

// A writer that finds the plan lock held gives up after the bound and says so, instead of blocking forever;
// the plan it would have written stays byte-identical. Both write commands share the bound.
func TestPlanWritersGiveUpOnAHeldLock(t *testing.T) {
	saved := lockWait
	lockWait = 200 * time.Millisecond
	t.Cleanup(func() { lockWait = saved })

	writers := []struct {
		name  string
		write func(path string) error
	}{
		{"add-finding", func(p string) error { _, err := AddFinding(p, openFinding()); return err }},
		{"upgrade", func(p string) error { _, err := Upgrade(p, ""); return err }},
	}
	for _, w := range writers {
		t.Run(w.name, func(t *testing.T) {
			p := write(t, t.TempDir(), "plan.md", addPlan())
			before, err := os.ReadFile(p)
			if err != nil {
				t.Fatal(err)
			}
			held, err := LockPlan(canonicalPath(p))
			if err != nil {
				t.Fatal(err)
			}
			defer UnlockPlan(held)

			done := make(chan error, 1)
			go func() { done <- w.write(p) }()
			select {
			case err := <-done:
				if err == nil || !strings.Contains(err.Error(), "timed out waiting for the plan lock") {
					t.Fatalf("a held lock must time out with a named error, got %v", err)
				}
			case <-time.After(5 * time.Second):
				t.Fatal("the writer is still blocked on a held lock after 5s")
			}
			after, err := os.ReadFile(p)
			if err != nil {
				t.Fatal(err)
			}
			if string(after) != string(before) {
				t.Fatal("a writer that timed out changed the plan")
			}
		})
	}
}

// Once the holder lets go, the next writer takes the lock and writes: the bound refuses a stuck lock, not a
// briefly busy one.
func TestPlanWriterTakesALockReleasedWithinTheBound(t *testing.T) {
	saved := lockWait
	lockWait = 5 * time.Second
	t.Cleanup(func() { lockWait = saved })

	p := write(t, t.TempDir(), "plan.md", addPlan())
	held, err := LockPlan(canonicalPath(p))
	if err != nil {
		t.Fatal(err)
	}
	go func() {
		time.Sleep(100 * time.Millisecond)
		UnlockPlan(held)
	}()
	if _, err := AddFinding(p, openFinding()); err != nil {
		t.Fatalf("a lock released within the bound must be taken: %v", err)
	}
}

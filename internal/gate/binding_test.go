package gate

import (
	"encoding/json"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/alesierraalta/tpp/internal/plan"
)

// setBinding installs the explicit per-session plan binding the Stop gate audits against. The run
// slug is present only when the caller passes one, so both binding shapes stay exercised.
func setBinding(t *testing.T, session, root, planPath string, run ...string) {
	t.Helper()
	raw := map[string]string{"session_id": session, "root": root, "planPath": planPath}
	if len(run) > 0 {
		raw["run"] = run[0]
	}
	encoded, err := json.Marshal(raw)
	if err != nil {
		t.Fatalf("encode binding: %v", err)
	}
	t.Setenv(BindingEnv, string(encoded))
}

// One worktree, two sessions: each stop audits the plan its own binding names, and the audit
// counts only the rows its own run owns — neither the other session's plan nor the other run's
// rows in the same file can speak for this stop. The binding is the only plan input the gate has,
// so neither session can inherit the other's plan or the worktree declaration.
func TestStopGateBindsEachSessionToItsOwnPlanInOneWorktree(t *testing.T) {
	owed := "## Layer matrix\n\n| Layer | Skill | Scope | Status | Run |\n|---|---|---|---|---|\n" +
		"| Security | `appsec-adversarial-auditor` | input | pending | run-t1 |\n" +
		"| Persistence | `database-persistence-testing` | input | pending | run-t2 |\n\n" +
		"## Ranked targets\n\n| Target | Verdict | Status | Run |\n|---|---|---|---|\n" +
		"| 1. auth | probe | pending | run-t1 |\n" +
		"| 2. billing | probe | pending | run-t2 |\n"
	swept := "## Layer matrix\n\n| Layer | Skill | Scope | Status | Run |\n|---|---|---|---|---|\n" +
		"| Security | `appsec-adversarial-auditor` | input | pending | run-t1 |\n" +
		"| Persistence | `database-persistence-testing` | input | done | run-t2 |\n\n" +
		"## Ranked targets\n\n| Target | Verdict | Status | Run |\n|---|---|---|---|\n" +
		"| 1. auth | probe | pending | run-t1 |\n" +
		"| 2. billing | probe | done | run-t2 |\n"
	repo := &fakeRepo{
		root:       "/repo",
		status:     porcelain(" M src/app.js"),
		files:      map[string]time.Time{"src/app.js": auditNow},
		transcript: stamped(auditStart, `{"name":"Skill","input":{"skill":"test-strategy"}}`),
		plans: map[string]string{
			"docs/testing/plan-a.md": owed,
			"docs/testing/plan-b.md": swept,
		},
	}
	cases := []struct {
		name     string
		session  string
		planPath string
		run      string
		wantOwed int
	}{
		{name: "first session audits its own plan", session: "sess-1", planPath: "docs/testing/plan-a.md", run: "run-t1", wantOwed: 1},
		{name: "second session audits its own plan", session: "sess-2", planPath: "docs/testing/plan-b.md", run: "run-t2", wantOwed: 0},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			setBinding(t, tc.session, "/repo", tc.planPath, tc.run)
			res := Decide(Input{SessionID: tc.session, TranscriptPath: "t"}, repo.deps(auditNow))
			if !res.Audit {
				t.Fatalf("audit = false (%s), want the bound plan audited; entry %#v", res.Reason, res.Entry)
			}
			if res.Entry == nil || res.Entry.Plan != tc.planPath {
				t.Fatalf("entry plan = %#v, want the binding's %q", res.Entry, tc.planPath)
			}
			if res.Owed != tc.wantOwed {
				t.Fatalf("owed = %d, want %d: each session must read its own plan's counts", res.Owed, tc.wantOwed)
			}
			if res.Entry.Skipped != "" {
				t.Fatalf("skipped = %q, want a bound session to audit without a skip", res.Entry.Skipped)
			}
			other := "docs/testing/plan-b.md"
			if tc.planPath == other {
				other = "docs/testing/plan-a.md"
			}
			if strings.Contains(res.Reason, other) {
				t.Fatalf("reason names the other session's plan %q: %s", other, res.Reason)
			}
		})
	}
}

// The gate audits nothing without one exact binding. Missing, malformed, and mismatched are one
// fact to the operator: no audit, no problem, no reason — and the telemetry row says why.
func TestStopGateStaysSilentWithoutAnExactBinding(t *testing.T) {
	planPath := plan.DefaultPath
	owed := "## Layer matrix\n\n| Layer | Skill | Scope | Status |\n|---|---|---|---|\n" +
		"| Security | `appsec-adversarial-auditor` | input | pending |\n\n" +
		"## Ranked targets\n\n| Target | Verdict | Status |\n|---|---|---|---|\n| 1. auth | probe | pending |\n"
	repo := &fakeRepo{
		root:       "/repo",
		status:     porcelain(" M src/app.js"),
		files:      map[string]time.Time{"src/app.js": auditNow},
		transcript: stamped(auditStart, `{"name":"Skill","input":{"skill":"test-strategy"}}`),
		plan:       owed,
	}
	cases := []struct {
		name      string
		env       string
		inSession string
	}{
		{name: "missing binding", env: "", inSession: "sess-1"},
		{name: "malformed binding json", env: `{"session_id":`, inSession: "sess-1"},
		{name: "unknown field fails closed", env: `{"session_id":"sess-1","root":"/repo","planPath":"` + planPath + `","sessionId":"typo"}`, inSession: "sess-1"},
		{name: "escaping plan path fails closed", env: `{"session_id":"sess-1","root":"/repo","planPath":"../outside.md"}`, inSession: "sess-1"},
		{name: "invalid run fails closed", env: `{"session_id":"sess-1","root":"/repo","planPath":"` + planPath + `","run":"NotASlug!"}`, inSession: "sess-1"},
		{name: "another session's binding", env: `{"session_id":"sess-2","root":"/repo","planPath":"` + planPath + `"}`, inSession: "sess-1"},
		{name: "another root's binding", env: `{"session_id":"sess-1","root":"/elsewhere","planPath":"` + planPath + `"}`, inSession: "sess-1"},
		{name: "a sessionless payload never inherits a binding", env: `{"session_id":"sess-1","root":"/repo","planPath":"` + planPath + `"}`, inSession: ""},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Setenv(BindingEnv, tc.env)
			res := Decide(Input{SessionID: tc.inSession, TranscriptPath: "t"}, repo.deps(auditNow))
			if res.Fire || res.Audit || res.Problem != "" || res.Reason != "" {
				t.Fatalf("result = %#v, want silence: no fire, no audit, no problem, no reason", res)
			}
			if res.Entry == nil {
				t.Fatalf("want a telemetry entry recording the skip")
			}
			if res.Entry.Skipped != "session_plan_unbound" {
				t.Fatalf("skipped = %q, want session_plan_unbound", res.Entry.Skipped)
			}
			if res.Entry.Plan != "" || res.Entry.Audited {
				t.Fatalf("entry = %#v, want no plan consulted and nothing audited", res.Entry)
			}
		})
	}
}

// The two roots are compared after resolution, so a symlinked path to the same worktree still
// binds: an operator's absolute path must not read as a foreign root.
func TestCanonicalRootResolvesSymlinkedPathToTheSameRoot(t *testing.T) {
	dir := t.TempDir()
	link := filepath.Join(t.TempDir(), "link")
	if err := os.Symlink(dir, link); err != nil {
		t.Fatalf("symlink: %v", err)
	}
	realRoot, err := canonicalRoot(dir)
	if err != nil {
		t.Fatalf("canonicalRoot(%q): %v", dir, err)
	}
	linkRoot, err := canonicalRoot(link)
	if err != nil {
		t.Fatalf("canonicalRoot(%q): %v", link, err)
	}
	if realRoot != linkRoot {
		t.Fatalf("symlinked root = %q, want the same canonical root as %q (%q)", linkRoot, dir, realRoot)
	}
}

// A relative root cannot be canonicalized against anything, so it is refused rather than guessed:
// fail closed, never bind.
func TestCanonicalRootRefusesARelativePath(t *testing.T) {
	if got, err := canonicalRoot("some/where"); err == nil {
		t.Fatalf("canonicalRoot(relative) = %q, want an error", got)
	}
}

// storedBinding is the operator's write path — `tpp bind` — recorded through SetBinding.
func storedBinding(t *testing.T, dir, session, root, planPath, run string) {
	t.Helper()
	if err := SetBinding(dir, Binding{Session: session, Root: root, PlanPath: planPath, Run: run}); err != nil {
		t.Fatalf("SetBinding: %v", err)
	}
}

var owedRuns = "## Layer matrix\n\n| Layer | Skill | Scope | Status | Run |\n|---|---|---|---|---|\n" +
	"| Security | `appsec-adversarial-auditor` | input | pending | run-t1 |\n" +
	"| Persistence | `database-persistence-testing` | input | pending | run-t2 |\n\n" +
	"## Ranked targets\n\n| Target | Verdict | Status | Run |\n|---|---|---|---|\n" +
	"| 1. auth | probe | pending | run-t1 |\n" +
	"| 2. billing | probe | pending | run-t2 |\n"

var sweptRuns = "## Layer matrix\n\n| Layer | Skill | Scope | Status | Run |\n|---|---|---|---|---|\n" +
	"| Security | `appsec-adversarial-auditor` | input | pending | run-t1 |\n" +
	"| Persistence | `database-persistence-testing` | input | done | run-t2 |\n\n" +
	"## Ranked targets\n\n| Target | Verdict | Status | Run |\n|---|---|---|---|\n" +
	"| 1. auth | probe | pending | run-t1 |\n" +
	"| 2. billing | probe | done | run-t2 |\n"

// The stored binding is per (canonical root, exact session): two sessions in one worktree bind
// different plans and runs on disk, and each stop audits exactly the plan and run its own file
// names — never the other session's file, whatever else sits in the same directory.
func TestStoredBindingsGiveEachSessionItsOwnPlanAndRun(t *testing.T) {
	t.Setenv(BindingEnv, "")
	repo := &fakeRepo{
		root:       "/repo",
		transcript: stamped(auditStart, `{"name":"Skill","input":{"skill":"test-strategy"}}`),
		plans:      map[string]string{"docs/testing/plan-a.md": owedRuns, "docs/testing/plan-b.md": sweptRuns},
	}
	dir := t.TempDir()
	storedBinding(t, dir, "sess-1", "/repo", "docs/testing/plan-a.md", "run-t1")
	storedBinding(t, dir, "sess-2", "/repo", "docs/testing/plan-b.md", "run-t2")
	cases := []struct {
		name     string
		session  string
		plan     string
		wantOwed int
	}{
		{name: "first session audits its stored plan", session: "sess-1", plan: "docs/testing/plan-a.md", wantOwed: 1},
		{name: "second session audits its stored plan", session: "sess-2", plan: "docs/testing/plan-b.md", wantOwed: 0},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			d := repo.deps(auditNow)
			d.BindingDir = dir
			res := Decide(Input{SessionID: tc.session, TranscriptPath: "t"}, d)
			if !res.Audit {
				t.Fatalf("audit = false (%s), entry %#v", res.Reason, res.Entry)
			}
			if res.Entry == nil || res.Entry.Plan != tc.plan {
				t.Fatalf("entry plan = %#v, want the stored binding's %q", res.Entry, tc.plan)
			}
			if res.Owed != tc.wantOwed {
				t.Fatalf("owed = %d, want %d: each session reads its own stored run", res.Owed, tc.wantOwed)
			}
		})
	}
}

// The environment and the stored file both claim this session: equal bindings audit once, and any
// disagreement — another plan or another run — fails closed, with no fallback to either source.
func TestStopGateFailsClosedWhenEnvAndStoredBindingDisagree(t *testing.T) {
	repo := &fakeRepo{
		root:       "/repo",
		transcript: stamped(auditStart, `{"name":"Skill","input":{"skill":"test-strategy"}}`),
		plans:      map[string]string{"docs/testing/plan-a.md": owedRuns, "docs/testing/plan-b.md": sweptRuns},
	}
	dir := t.TempDir()
	cases := []struct {
		name              string
		envPlan, envRun   string
		filePlan, fileRun string
		wantAudit         bool
	}{
		{name: "equal bindings audit", envPlan: "docs/testing/plan-a.md", envRun: "run-t1", filePlan: "docs/testing/plan-a.md", fileRun: "run-t1", wantAudit: true},
		{name: "another plan on disk fails closed", envPlan: "docs/testing/plan-a.md", envRun: "run-t1", filePlan: "docs/testing/plan-b.md", fileRun: "run-t1"},
		{name: "another run on disk fails closed", envPlan: "docs/testing/plan-a.md", envRun: "run-t1", filePlan: "docs/testing/plan-a.md", fileRun: "run-t2"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			setBinding(t, "sess-1", "/repo", tc.envPlan, tc.envRun)
			storedBinding(t, dir, "sess-1", "/repo", tc.filePlan, tc.fileRun)
			d := repo.deps(auditNow)
			d.BindingDir = dir
			res := Decide(Input{SessionID: "sess-1", TranscriptPath: "t"}, d)
			if tc.wantAudit {
				if !res.Audit || res.Entry == nil || res.Entry.Plan != tc.envPlan {
					t.Fatalf("result = %#v, want the agreeing binding audited", res)
				}
				return
			}
			if res.Audit || res.Reason != "" || res.Problem != "" {
				t.Fatalf("result = %#v, want silence: conflicting sources must not fall back", res)
			}
			if res.Entry == nil || res.Entry.Skipped != SkippedUnbound {
				t.Fatalf("entry = %#v, want the unbound skip", res.Entry)
			}
		})
	}
}

// Only a file whose content is this session's binding for this root is read: malformed bytes,
// another session's content, another root's content, or an escaping plan at this key is silence,
// never a fallback to a weaker binding.
func TestStopGateReadsNoBindingFromFileContentThatIsNotExact(t *testing.T) {
	t.Setenv(BindingEnv, "")
	repo := &fakeRepo{
		root:       "/repo",
		transcript: stamped(auditStart, `{"name":"Skill","input":{"skill":"test-strategy"}}`),
		plan:       owedRuns,
	}
	dir := t.TempDir()
	croot, err := canonicalRoot("/repo")
	if err != nil {
		t.Fatalf("canonicalRoot: %v", err)
	}
	key := bindingPath(dir, croot, "sess-1")
	cases := []struct{ name, raw string }{
		{name: "malformed bytes", raw: `{not json`},
		{name: "another session's content", raw: `{"session_id":"sess-2","root":"/repo","planPath":"docs/testing/test-plan.md","run":"run-t1"}`},
		{name: "another root's content", raw: `{"session_id":"sess-1","root":"/elsewhere","planPath":"docs/testing/test-plan.md","run":"run-t1"}`},
		{name: "escaping plan path", raw: `{"session_id":"sess-1","root":"/repo","planPath":"../outside.md","run":"run-t1"}`},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if err := os.WriteFile(key, []byte(tc.raw), 0o600); err != nil {
				t.Fatalf("write binding file: %v", err)
			}
			d := repo.deps(auditNow)
			d.BindingDir = dir
			res := Decide(Input{SessionID: "sess-1", TranscriptPath: "t"}, d)
			if res.Audit || res.Reason != "" || res.Problem != "" {
				t.Fatalf("result = %#v, want silence for non-exact file content", res)
			}
			if res.Entry == nil || res.Entry.Skipped != SkippedUnbound {
				t.Fatalf("entry = %#v, want the unbound skip", res.Entry)
			}
		})
	}
}

// The stored binding lives outside the repository under the log's own directory: a 0700
// directory, a 0600 file named by a hash of root and session — no raw session ID — and nothing
// else beside it, because the write renames into place instead of leaving a partial file behind.
func TestSetBindingStoresAPrivateHashedFileBesideTheLog(t *testing.T) {
	logPath := filepath.Join(t.TempDir(), "telemetry", "testing-gate.jsonl")
	dir := BindingsDir(logPath)
	if want := filepath.Join(filepath.Dir(logPath), "bindings"); dir != want {
		t.Fatalf("BindingsDir(%q) = %q, want %q", logPath, dir, want)
	}
	if BindingsDir("") != "" {
		t.Fatalf("BindingsDir without a log = %q, want no directory", BindingsDir(""))
	}
	storedBinding(t, dir, "sess-secret", "/repo", "docs/testing/test-plan.md", "run-t1")
	di, err := os.Stat(dir)
	if err != nil {
		t.Fatalf("stat binding dir: %v", err)
	}
	if di.Mode().Perm() != 0o700 {
		t.Fatalf("binding dir mode = %v, want 0700", di.Mode().Perm())
	}
	key := bindingPath(dir, mustCanonical(t, "/repo"), "sess-secret")
	fi, err := os.Stat(key)
	if err != nil {
		t.Fatalf("stat binding file: %v", err)
	}
	if fi.Mode().Perm() != 0o600 {
		t.Fatalf("binding file mode = %v, want 0600", fi.Mode().Perm())
	}
	if strings.Contains(filepath.Base(key), "sess-secret") {
		t.Fatalf("binding file name %q carries the raw session ID", filepath.Base(key))
	}
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatalf("read binding dir: %v", err)
	}
	if len(entries) != 1 || entries[0].Name() != filepath.Base(key) {
		t.Fatalf("binding dir holds %d entries, want only the renamed file (no partial write)", len(entries))
	}
	// A rebind replaces the same key atomically: still one file, now the new content.
	storedBinding(t, dir, "sess-secret", "/repo", "docs/testing/plan-b.md", "run-t2")
	if entries, err = os.ReadDir(dir); err != nil || len(entries) != 1 {
		t.Fatalf("after rebind the dir holds %d entries (err %v), want 1", len(entries), err)
	}
	b, present, ok := fileBinding(dir, "sess-secret", "/repo")
	if !present || !ok || b.PlanPath != "docs/testing/plan-b.md" || b.Run != "run-t2" {
		t.Fatalf("read back = %#v present=%v ok=%v, want the rebound binding", b, present, ok)
	}
}

// A bindings directory that already exists looser than private must not stay that way: the write
// path tightens it to 0700 before storing anything, so a binding is never left in a directory
// others can read — and the binding is still stored once the directory is private.
func TestSetBindingTightensAPreExistingLooseBindingsDir(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("unix permission bits are not enforced on windows")
	}
	logPath := filepath.Join(t.TempDir(), "telemetry", "testing-gate.jsonl")
	dir := BindingsDir(logPath)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatalf("pre-create bindings dir: %v", err)
	}
	// MkdirAll is filtered by umask, so force the loose mode the fix must repair.
	if err := os.Chmod(dir, 0o755); err != nil {
		t.Fatalf("loosen bindings dir: %v", err)
	}
	b := Binding{Session: "sess-secret", Root: "/repo", PlanPath: "docs/testing/test-plan.md", Run: "run-t1"}
	if err := SetBinding(dir, b); err != nil {
		t.Fatalf("SetBinding: %v", err)
	}
	di, err := os.Stat(dir)
	if err != nil {
		t.Fatalf("stat bindings dir: %v", err)
	}
	if di.Mode().Perm() != 0o700 {
		t.Fatalf("pre-existing bindings dir mode = %v, want 0700", di.Mode().Perm())
	}
	if got, present, ok := fileBinding(dir, b.Session, b.Root); !present || !ok || got != b {
		t.Fatalf("read back = %#v present=%v ok=%v, want the stored binding %#v", got, present, ok, b)
	}
}

// Concurrent binds from two sessions in one worktree race on the same directory: a bounded race
// of SetBinding calls must leave one private file per session — no clobbered record, no leftover
// partial write — so after the race each stop still audits exactly the plan its own binding names.
func TestConcurrentSetBindingKeepsEachSessionSeparate(t *testing.T) {
	t.Setenv(BindingEnv, "")
	repo := &fakeRepo{
		root:       "/repo",
		transcript: stamped(auditStart, `{"name":"Skill","input":{"skill":"test-strategy"}}`),
		plans:      map[string]string{"docs/testing/plan-a.md": owedRuns, "docs/testing/plan-b.md": sweptRuns},
	}
	dir := t.TempDir()
	binds := []Binding{
		{Session: "sess-1", Root: "/repo", PlanPath: "docs/testing/plan-a.md", Run: "run-t1"},
		{Session: "sess-2", Root: "/repo", PlanPath: "docs/testing/plan-b.md", Run: "run-t2"},
	}
	const rounds = 8
	errs := make(chan error, 2*rounds)
	start := make(chan struct{})
	var wg sync.WaitGroup
	for i := 0; i < rounds; i++ {
		for _, b := range binds {
			wg.Add(1)
			go func(b Binding) {
				defer wg.Done()
				<-start
				errs <- SetBinding(dir, b)
			}(b)
		}
	}
	close(start)
	wg.Wait()
	close(errs)
	for err := range errs {
		if err != nil {
			t.Fatalf("concurrent SetBinding: %v", err)
		}
	}
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatalf("read binding dir: %v", err)
	}
	if len(entries) != len(binds) {
		t.Fatalf("binding dir holds %d entries, want one file per session and no leftover temp: %v", len(entries), entries)
	}
	for _, b := range binds {
		got, present, ok := fileBinding(dir, b.Session, b.Root)
		if !present || !ok || got != b {
			t.Fatalf("readback for %s = %#v present=%v ok=%v, want its own binding %#v", b.Session, got, present, ok, b)
		}
	}
	cases := []struct {
		session, plan string
		wantOwed      int
	}{
		{session: "sess-1", plan: "docs/testing/plan-a.md", wantOwed: 1},
		{session: "sess-2", plan: "docs/testing/plan-b.md", wantOwed: 0},
	}
	for _, tc := range cases {
		d := repo.deps(auditNow)
		d.BindingDir = dir
		res := Decide(Input{SessionID: tc.session, TranscriptPath: "t"}, d)
		if !res.Audit || res.Entry == nil || res.Entry.Plan != tc.plan {
			t.Fatalf("%s: audit result = %#v, want its own bound plan %q after the concurrent binds", tc.session, res, tc.plan)
		}
		if res.Owed != tc.wantOwed {
			t.Fatalf("%s: owed = %d, want %d: each session must still read only its own run", tc.session, res.Owed, tc.wantOwed)
		}
	}
}

func mustCanonical(t *testing.T, path string) string {
	t.Helper()
	c, err := canonicalRoot(path)
	if err != nil {
		t.Fatalf("canonicalRoot(%q): %v", path, err)
	}
	return c
}

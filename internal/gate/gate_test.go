package gate

import (
	"errors"
	"io"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/alesierraalta/tsp/internal/plan"
)

type fakeInfo struct {
	name string
	mod  time.Time
}

func (f fakeInfo) Name() string       { return f.name }
func (f fakeInfo) Size() int64        { return 0 }
func (f fakeInfo) Mode() os.FileMode  { return 0 }
func (f fakeInfo) ModTime() time.Time { return f.mod }
func (f fakeInfo) IsDir() bool        { return false }
func (f fakeInfo) Sys() any           { return nil }

// fakeRepo stands in for git, the filesystem, and the transcript at the process boundary.
type fakeRepo struct {
	root              string
	status            string
	statusErr         error
	rootErr           error
	files             map[string]time.Time
	optOut            bool
	transcript        string
	transcriptMissing bool
	plan              string
	planPath          string
	planConfig        string
	plans             map[string]string
	planMissing       bool
}

func (f *fakeRepo) deps(now time.Time) Deps {
	return Deps{
		Git: func(dir string, args ...string) (string, error) {
			switch args[0] {
			case "rev-parse":
				if f.rootErr != nil {
					return "", f.rootErr
				}
				return f.root + "\n", nil
			case "status":
				if f.statusErr != nil {
					return "", f.statusErr
				}
				return f.status, nil
			}
			return "", errors.New("unexpected git call")
		},
		Stat: func(p string) (os.FileInfo, error) {
			if p == filepath.Join(f.root, ".no-testing-gate") {
				if f.optOut {
					return fakeInfo{name: ".no-testing-gate"}, nil
				}
				return nil, os.ErrNotExist
			}
			rel, err := filepath.Rel(f.root, p)
			if err != nil {
				return nil, os.ErrNotExist
			}
			if mt, ok := f.files[rel]; ok {
				return fakeInfo{name: filepath.Base(p), mod: mt}, nil
			}
			return nil, os.ErrNotExist
		},
		ReadPlan: func(path string) (string, error) {
			if path == filepath.Join(f.root, plan.ConfigName) {
				if f.planConfig == "" {
					return "", os.ErrNotExist
				}
				return f.planConfig, nil
			}
			rel, err := filepath.Rel(f.root, path)
			if err != nil {
				return "", os.ErrNotExist
			}
			body := f.plan
			if f.plans != nil {
				var ok bool
				body, ok = f.plans[rel]
				if !ok {
					return "", os.ErrNotExist
				}
			} else {
				want := f.planPath
				if want == "" {
					want = plan.DefaultPath
				}
				if rel != want {
					return "", os.ErrNotExist
				}
			}
			if f.planMissing || body == "" {
				return "", os.ErrNotExist
			}
			return body, nil
		},
		OpenTranscript: func(string) (io.ReadCloser, error) {
			if f.transcriptMissing {
				return nil, os.ErrNotExist
			}
			return io.NopCloser(strings.NewReader(f.transcript)), nil
		},
		Now:     now,
		WorkDir: f.root,
	}
}

func porcelain(entries ...string) string {
	var b strings.Builder
	for _, e := range entries {
		b.WriteString(e)
		b.WriteByte(0)
	}
	return b.String()
}

func stamped(t time.Time, extra ...string) string {
	lines := []string{`{"type":"x","timestamp":"` + t.UTC().Format(time.RFC3339Nano) + `"}`}
	lines = append(lines, extra...)
	return strings.Join(lines, "\n") + "\n"
}

func TestParsePorcelain(t *testing.T) {
	cases := []struct {
		name string
		raw  string
		want []string
	}{
		{"rename skips the old path and keeps the new name", porcelain("R  src/c.ts", "src/a.ts"), []string{"src/c.ts"}},
		{"deletion is excluded", porcelain(" D src/b.ts", " M src/a.ts"), []string{"src/a.ts"}},
		{"short fields are ignored", porcelain("??", " M src/a.ts", "x"), []string{"src/a.ts"}},
		{"paths with spaces are raw, not quoted", porcelain("?? src/with space.ts"), []string{"src/with space.ts"}},
		{"trailing NUL yields no phantom entry", porcelain(" M src/a.ts") + "\x00", []string{"src/a.ts"}},
		{"empty status yields nothing", "", []string{}},
		{"copy also skips its source field", porcelain("C  src/d.ts", "src/a.ts", "?? src/e.ts"), []string{"src/d.ts", "src/e.ts"}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := ParsePorcelain(tc.raw); !reflect.DeepEqual(got, tc.want) {
				t.Fatalf("got %q, want %q", got, tc.want)
			}
		})
	}
}

func TestIsProductionSource(t *testing.T) {
	positives := []string{"src/a.ts", "lib/x.py", "cmd/main.go", "src/contest/y.ts", "src/with space.ts", "app.mjs"}
	negatives := []string{"tests/a.test.ts", "src/a.spec.js", "src/__tests__/x.ts", "e2e/x.ts", "fixtures/f.ts", "types.d.ts",
		"README.md", ".claude/hooks/x.mjs", "evals/cases/x.js", "node_modules/p/i.js", "vendor/v.go", "dist/b.js", "src/.venv/lib.py"}
	for _, p := range positives {
		t.Run("source "+p, func(t *testing.T) {
			if !IsProductionSource(p) {
				t.Fatalf("%q must count as production source", p)
			}
		})
	}
	for _, p := range negatives {
		t.Run("excluded "+p, func(t *testing.T) {
			if IsProductionSource(p) {
				t.Fatalf("%q must not count as production source", p)
			}
		})
	}
}

func TestSkillsLoaded(t *testing.T) {
	cases := []struct {
		name  string
		lines string
		want  []string
	}{
		{"listing text does not count", "- test-strategy: Trigger: haz el testing ... exploit-testing ...\n- tsp: old alias\n", []string{}},
		{"the canonical Skill tool call counts", `{"type":"tool_use","name":"Skill","input":{"skill":"test-strategy"}}` + "\n", []string{"test-strategy"}},
		{"a mistaken Skill tool alias canonicalizes", `{"type":"tool_use","name":"Skill","input":{"skill":"tsp"}}` + "\n", []string{"test-strategy"}},
		{"a mistaken SKILL.md path canonicalizes", `{"input":{"file_path":"/home/x/.claude/skills/tsp/SKILL.md"}}` + "\n", []string{"test-strategy"}},
		{"a SKILL.md read counts", `{"input":{"file_path":"/home/x/.claude/skills/exploit-testing/SKILL.md"}}` + "\n", []string{"exploit-testing"}},
		{"a sibling alone returns only that sibling", `{"input":{"file_path":"/x/.claude/skills/no-excess-tests/SKILL.md"}}` + "\n", []string{"no-excess-tests"}},
		{"aliases deduplicate to canonical identity in first-seen order", `{"skill":"exploit-testing"}` + "\n" + `{"skill":"test-strategy"}` + "\n" + `{"skill":"tsp"}` + "\n" + `{"skill":"exploit-testing"}` + "\n", []string{"exploit-testing", "test-strategy"}},
		{"empty transcript", "", []string{}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := SkillsLoaded(strings.NewReader(tc.lines)); !reflect.DeepEqual(got, tc.want) {
				t.Fatalf("got %q, want %q", got, tc.want)
			}
		})
	}
	if got := SkillsLoaded(nil); len(got) != 0 || got == nil {
		t.Fatalf("nil reader must yield an empty, non-nil slice")
	}
}

func TestDecide(t *testing.T) {
	now := time.Date(2026, 9, 9, 12, 0, 0, 0, time.UTC)
	start := now.Add(-60 * time.Second)
	fresh := now.Add(120 * time.Second)
	root := "/repo"
	base := func() *fakeRepo {
		return &fakeRepo{
			root:       root,
			status:     porcelain(" M src/a.ts"),
			files:      map[string]time.Time{"src/a.ts": fresh},
			transcript: stamped(start),
		}
	}
	cases := []struct {
		name     string
		in       Input
		repo     func() *fakeRepo
		bind     bool
		audit    bool
		skipped  string
		optOut   bool
		loaded   []string
		wantPlan string
		noEntry  bool
	}{
		{name: "loop guard wins over everything", in: Input{StopHookActive: true, TranscriptPath: "t"}, repo: base, noEntry: true},
		{name: "not a repository: silent, no log", in: Input{TranscriptPath: "t"}, repo: func() *fakeRepo { r := base(); r.rootErr = errors.New("not a git repository"); return r }, noEntry: true},
		{name: "an unbound stop records its reason even when the worktree shows no change", in: Input{TranscriptPath: "t"}, repo: func() *fakeRepo { r := base(); r.status = ""; return r }, skipped: "session_plan_unbound"},
		{name: "another session's fresh edits never speak for an unbound stop", in: Input{SessionID: "sess-decide", TranscriptPath: "t"}, repo: func() *fakeRepo {
			r := base()
			r.status = porcelain(" M src/a.ts", " M src/b.ts")
			r.files["src/b.ts"] = fresh
			return r
		}, skipped: "session_plan_unbound"},
		{name: "a bound stop with no adversarial invocation is silent whatever the timestamps say", bind: true, in: Input{SessionID: "sess-decide", TranscriptPath: "t"}, repo: func() *fakeRepo {
			r := base()
			r.transcript = stamped(start, `{"input":{"file_path":"/x/.claude/skills/no-excess-tests/SKILL.md"}}`)
			return r
		}, loaded: []string{"no-excess-tests"}, wantPlan: plan.DefaultPath},
		{name: "the bound audit does not require git status", bind: true, in: Input{SessionID: "sess-decide", TranscriptPath: "t"}, repo: func() *fakeRepo {
			r := base()
			r.statusErr = errors.New("boom")
			r.transcript = stamped(start, `{"name":"Skill","input":{"skill":"tsp"}}`)
			r.plan = "## Layer matrix\n\n| Layer | Skill | Scope | Status | Run |\n|---|---|---|---|---|\n" +
				"| Security | `appsec-adversarial-auditor` | input | pending | run-t1 |\n\n" +
				"## Ranked targets\n\n| Target | Verdict | Status | Run |\n|---|---|---|---|---|\n" +
				"| 1. auth | probe | pending | run-t1 |\n"
			return r
		}, audit: true, loaded: []string{"test-strategy"}, wantPlan: plan.DefaultPath},
		{name: "opt-out at the root silences a cwd in a subdirectory before any binding is read", in: Input{TranscriptPath: "t", Cwd: root + "/sub/dir"}, repo: func() *fakeRepo { r := base(); r.optOut = true; return r }, optOut: true},
		{name: "a bound stop whose transcript cannot be read audits nothing", bind: true, in: Input{SessionID: "sess-decide", TranscriptPath: "t"}, repo: func() *fakeRepo {
			r := base()
			r.transcriptMissing = true
			return r
		}, loaded: []string{}, wantPlan: plan.DefaultPath},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if tc.bind {
				setBinding(t, "sess-decide", root, plan.DefaultPath, "run-t1")
			}
			res := Decide(tc.in, tc.repo().deps(now))
			// No timestamp and no working-tree entry proves this stop's session authored a change,
			// so the generic reminder never has an honest trigger.
			if res.Fire {
				t.Fatalf("fire = true, the generic reminder must never trigger (entry %+v)", res.Entry)
			}
			if tc.noEntry {
				if res.Entry != nil {
					t.Fatalf("expected no log entry, got %+v", res.Entry)
				}
				return
			}
			if res.Entry == nil {
				t.Fatalf("expected a log entry")
			}
			if res.Entry.Fired {
				t.Fatalf("logged fired = true, the entry must not claim a fire: %+v", res.Entry)
			}
			if res.Audit != tc.audit {
				t.Fatalf("audit = %v, want %v (entry %+v, reason %q)", res.Audit, tc.audit, res.Entry, res.Reason)
			}
			if res.Entry.Skipped != tc.skipped {
				t.Fatalf("skipped = %q, want %q", res.Entry.Skipped, tc.skipped)
			}
			if res.Entry.OptedOut != tc.optOut {
				t.Fatalf("opted_out = %v, want %v", res.Entry.OptedOut, tc.optOut)
			}
			if res.Entry.Plan != tc.wantPlan {
				t.Fatalf("plan = %q, want %q", res.Entry.Plan, tc.wantPlan)
			}
			wantLoaded := tc.loaded
			if wantLoaded == nil {
				wantLoaded = []string{}
			}
			if !reflect.DeepEqual(res.Entry.SkillsLoaded, wantLoaded) {
				t.Fatalf("skills_loaded = %q, want %q", res.Entry.SkillsLoaded, wantLoaded)
			}
			if !tc.audit && res.Reason != "" {
				t.Fatalf("a silent decision must carry no reason, got %q", res.Reason)
			}
		})
	}
}

// Two sessions share one checkout: session B's edits and a shell-generated file land while session
// A's stop runs. Neither a fresh mtime nor a working-tree entry says who wrote them, so the stop's
// only evidence about A is A's own binding and A's own transcript — nothing else decides whether A
// hears anything, and only A's bound run is ever audited.
func TestStopGateDoesNotAttributeAnotherSessionsEditsToThisStop(t *testing.T) {
	now := time.Date(2026, 9, 11, 12, 0, 0, 0, time.UTC)
	started := now.Add(-time.Hour)
	root := "/repo"
	twoRunPlan := "## Layer matrix\n\n| Layer | Skill | Scope | Status | Run |\n|---|---|---|---|---|\n" +
		"| Security | `appsec-adversarial-auditor` | input | pending | run-a |\n" +
		"| Persistence | `database-persistence-testing` | input | done | run-b |\n\n" +
		"## Ranked targets\n\n| Target | Verdict | Status | Run |\n|---|---|---|---|---|\n" +
		"| 1. auth | probe | pending | run-a |\n" +
		"| 2. billing | probe | done | run-b |\n"
	// Session B wrote these while A's turn ran: uncommitted entries and a shell-generated file with
	// fresh metadata, all real and none attributable to A.
	repoWithBothSessions := func() *fakeRepo {
		return &fakeRepo{
			root:   root,
			status: porcelain(" M src/app.js", "?? src/from-b-shell.ts"),
			files: map[string]time.Time{
				"src/app.js":          now.Add(30 * time.Second),
				"src/from-b-shell.ts": now.Add(45 * time.Second),
			},
			transcript: stamped(started),
		}
	}

	t.Run("A is not bound: B's edits are never A's notice", func(t *testing.T) {
		t.Setenv(BindingEnv, "")
		res := Decide(Input{SessionID: "sess-a", TranscriptPath: "t"}, repoWithBothSessions().deps(now))
		if res.Fire || res.Audit || res.Reason != "" {
			t.Fatalf("result = %#v, want silence for an unbound stop", res)
		}
		if res.Entry == nil || res.Entry.Skipped != "session_plan_unbound" {
			t.Fatalf("entry = %#v, want the unbound skip reason recorded", res.Entry)
		}
	})

	t.Run("A is bound but its transcript never invoked a testing skill", func(t *testing.T) {
		setBinding(t, "sess-a", root, plan.DefaultPath, "run-a")
		repo := repoWithBothSessions()
		repo.transcript = stamped(started, `{"input":{"file_path":"/x/.claude/skills/no-excess-tests/SKILL.md"}}`)
		res := Decide(Input{SessionID: "sess-a", TranscriptPath: "t"}, repo.deps(now))
		if res.Fire || res.Audit || res.Reason != "" {
			t.Fatalf("result = %#v, want silence without an adversarial invocation", res)
		}
		if res.Entry == nil || res.Entry.Skipped != "" || res.Entry.Plan != plan.DefaultPath {
			t.Fatalf("entry = %#v, want the bound plan recorded and no skip", res.Entry)
		}
	})

	t.Run("A is bound and invoked the skill: only A's run is audited", func(t *testing.T) {
		setBinding(t, "sess-a", root, plan.DefaultPath, "run-a")
		repo := repoWithBothSessions()
		repo.transcript = stamped(started, `{"name":"Skill","input":{"skill":"tsp"}}`)
		repo.plan = twoRunPlan
		res := Decide(Input{SessionID: "sess-a", TranscriptPath: "t"}, repo.deps(now))
		if res.Fire {
			t.Fatalf("fire = true, an audited stop must not also claim an unattributed change")
		}
		if !res.Audit {
			t.Fatalf("audit = false (%s), the bound session's own run must be audited", res.Reason)
		}
		if res.Owed != 1 || res.Pending != 1 {
			t.Fatalf("owed = %d, pending = %d, want A's run-a alone (1, 1); B's run must not contribute", res.Owed, res.Pending)
		}
		if res.Entry == nil || res.Entry.Skipped != "" || res.Entry.Plan != plan.DefaultPath {
			t.Fatalf("entry = %#v, want the bound plan audited without a skip", res.Entry)
		}
	})

	t.Run("shell-generated changes cannot be attributed by mtime", func(t *testing.T) {
		mtimes := []struct {
			name string
			mod  time.Time
		}{
			{"mtime exactly at the session start", started},
			{"mtime one second before the session start", started.Add(-time.Second)},
			{"mtime fresh after the session started", now.Add(time.Minute)},
			{"mtime written by a shell in the future", now.Add(time.Hour)},
		}
		for _, mt := range mtimes {
			t.Run(mt.name, func(t *testing.T) {
				t.Setenv(BindingEnv, "")
				repo := repoWithBothSessions()
				repo.files["src/from-b-shell.ts"] = mt.mod
				res := Decide(Input{SessionID: "sess-a", TranscriptPath: "t"}, repo.deps(now))
				if res.Fire {
					t.Fatalf("fire = true, mtime %v is not proof this session authored the change", mt.mod)
				}
				if res.Entry == nil || res.Entry.Skipped != "session_plan_unbound" {
					t.Fatalf("entry = %#v, want the unbound skip reason recorded", res.Entry)
				}
			})
		}
	})
}

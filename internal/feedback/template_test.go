package feedback

import (
	"strings"
	"testing"
	"time"
)

// Every field the operator fills is defined in the template itself, on a comment line right above it, so
// `paid` is not read as "were paid endpoints called". The definitions are comments: a filled template parses.
func TestTemplateDefinesEveryFieldItAsksFor(t *testing.T) {
	got := Template(Report{TS: "t", Repo: "/repo", Skill: "s", Build: "b"})
	lines := strings.Split(got, "\n")
	for _, field := range []string{"paid", "cost", "reason", "verdict", "guess", "freeform"} {
		at := -1
		for i, l := range lines {
			if l == field+": " {
				at = i
			}
		}
		if at < 1 || !strings.HasPrefix(lines[at-1], "# "+field+" = ") {
			t.Fatalf("field %q has no definition line above it:\n%s", field, got)
		}
	}
	filled := strings.NewReplacer("paid: \n", "paid: found a defect\n", "cost: \n", "cost: an hour\n",
		"reason: \n", "reason: tests missed it\n", "verdict: \n", "verdict: paid\n").Replace(got)
	r, err := Parse(filled)
	if err != nil {
		t.Fatalf("a filled template must parse: %v", err)
	}
	if r.Paid != "found a defect" || r.Verdict != "paid" {
		t.Fatalf("parsed %+v", r)
	}
}

// A report that repeats the repository, plan and verdict of the last report within the window is flagged, so
// a run that submits twice hears about it; anything else is a new report.
func TestRepeatsLastReportFlagsANearDuplicate(t *testing.T) {
	enableFeedbackForTest(t)
	dir := t.TempDir()
	if dup, err := Repeats(dir, Report{TS: "2026-10-07T20:25:00Z", Repo: "/repo", Plan: "p.md", Verdict: "ceremony"}, 30*time.Minute); err != nil || dup {
		t.Fatalf("an empty ledger has nothing to repeat: %v %v", dup, err)
	}
	last := Report{TS: "2026-10-07T20:25:00Z", Repo: "/repo", Plan: "p.md", Skill: "s", Build: "b",
		Paid: "no", Cost: "0", Reason: "r", Verdict: "ceremony"}
	if err := Record(dir, last); err != nil {
		t.Fatal(err)
	}
	cases := []struct {
		name string
		r    Report
		want bool
	}{
		{"same run five minutes later", Report{TS: "2026-10-07T20:30:00Z", Repo: "/repo", Plan: "p.md", Verdict: "ceremony"}, true},
		{"another verdict", Report{TS: "2026-10-07T20:30:00Z", Repo: "/repo", Plan: "p.md", Verdict: "paid"}, false},
		{"another plan", Report{TS: "2026-10-07T20:30:00Z", Repo: "/repo", Plan: "q.md", Verdict: "ceremony"}, false},
		{"another repository", Report{TS: "2026-10-07T20:30:00Z", Repo: "/other", Plan: "p.md", Verdict: "ceremony"}, false},
		{"outside the window", Report{TS: "2026-10-07T22:00:00Z", Repo: "/repo", Plan: "p.md", Verdict: "ceremony"}, false},
		{"an unreadable timestamp", Report{TS: "later", Repo: "/repo", Plan: "p.md", Verdict: "ceremony"}, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			dup, err := Repeats(dir, tc.r, 30*time.Minute)
			if err != nil || dup != tc.want {
				t.Fatalf("Repeats = %v, %v; want %v", dup, err, tc.want)
			}
		})
	}
}

package buildinfo

import (
	"runtime/debug"
	"strings"
	"testing"
)

// The benchmark records this string in every history row, and a row nobody can attribute to a build is a
// number nobody can reproduce. The reading moved here with the function it belongs to, so the assertion
// moved with it.
func TestRevisionIsNeverEmpty(t *testing.T) {
	if Revision() == "" {
		t.Fatal("a build must always name a revision, even outside a repository")
	}
}

// String is what `tpp version` prints and what this package exists to compose, so both halves have to
// survive in it: a version that lost its revision names a release nobody can check out. The assertion the
// review found here promised this contract and could not check it — `Version` is never empty, so the branch
// that guarded it was unreachable — and this one fails as soon as either half goes missing.
func TestStringCarriesBothTheVersionAndTheRevision(t *testing.T) {
	got := String()
	if !strings.Contains(got, Version) || !strings.Contains(got, Revision()) {
		t.Fatalf("String() = %q, want the version %q and the revision %q in it", got, Version, Revision())
	}
}

// Go's VCS stamp does not see a linked worktree and, when that worktree sits inside another
// repository, names the outer repository's commit instead (#157). The commit `make build` reads from
// git has to win over that stamp, or every bench row built there cites code it never ran.
func TestRevisionFrom(t *testing.T) {
	stamp := func(rev, modified string) []debug.BuildSetting {
		return []debug.BuildSetting{{Key: "vcs.revision", Value: rev}, {Key: "vcs.modified", Value: modified}}
	}
	const outer = "62fdb0da00000000000000000000000000000000"
	const own = "00de6ce82fc3f2412f22cc8da50a682632b85803"
	cases := []struct {
		name     string
		commit   string
		settings []debug.BuildSetting
		ok       bool
		want     string
	}{
		{"the commit from git wins over a conflicting stamp", own, stamp(outer, "true"), true, "00de6ce"},
		{"a dirty commit keeps its suffix", own + "+dirty", stamp(outer, "false"), true, "00de6ce+dirty"},
		{"the commit wins even without build info", own, nil, false, "00de6ce"},
		{"no commit falls back to a clean stamp", "", stamp(own, "false"), true, "00de6ce"},
		{"no commit falls back to a modified stamp", "", stamp(own, "true"), true, "00de6ce+dirty"},
		{"no commit and no stamp is unknown", "", nil, true, "unknown"},
		{"no commit and no build info is unknown", "", nil, false, "unknown"},
	}
	for _, c := range cases {
		if got := revisionFrom(c.commit, c.settings, c.ok); got != c.want {
			t.Errorf("%s: revisionFrom(%q) = %q, want %q", c.name, c.commit, got, c.want)
		}
	}
}

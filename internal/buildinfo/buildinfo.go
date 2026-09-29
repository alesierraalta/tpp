// Package buildinfo names the build: the release it was cut from and the commit behind it.
package buildinfo

import (
	"runtime/debug"
	"strings"
)

// Version is the release this build was cut from. It is overridable at build time:
// -ldflags "-X github.com/alesierraalta/tpp/internal/buildinfo.Version=v1.2.3".
var Version = "0.4.1"

// Commit is the revision `make build` reads from git, as "<sha>" or "<sha>+dirty". It wins over the
// stamp Go embeds, which misses a linked worktree and, inside another repository, names that
// repository's commit instead (#157):
// -ldflags "-X github.com/alesierraalta/tpp/internal/buildinfo.Commit=<sha>".
var Commit string

// String renders the version and the revision the build came from, such as "0.3.5 (fcb4ce6)"
// or "0.3.5 (unknown)" when the build carries no revision.
func String() string {
	return Version + " (" + Revision() + ")"
}

// Revision identifies the commit behind this build. Go embeds it when the binary is built inside
// a repository; a build without that information still names itself, as "unknown", so a version
// is never confused with an attributable one.
func Revision() string {
	info, ok := debug.ReadBuildInfo()
	var settings []debug.BuildSetting
	if ok {
		settings = info.Settings
	}
	return revisionFrom(Commit, settings, ok)
}

// revisionFrom prefers the commit read at build time and falls back to Go's VCS stamp.
func revisionFrom(commit string, settings []debug.BuildSetting, ok bool) string {
	if rev, dirty := strings.CutSuffix(commit, "+dirty"); rev != "" {
		return short(rev, dirty)
	}
	if !ok {
		return "unknown"
	}
	rev, dirty := "", false
	for _, s := range settings {
		switch s.Key {
		case "vcs.revision":
			rev = s.Value
		case "vcs.modified":
			dirty = s.Value == "true"
		}
	}
	if rev == "" {
		return "unknown"
	}
	return short(rev, dirty)
}

func short(rev string, dirty bool) string {
	if len(rev) > 7 {
		rev = rev[:7]
	}
	if dirty {
		return rev + "+dirty"
	}
	return rev
}

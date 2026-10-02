// Package hookcmd reads the command strings a host stores for a hook: how a shell would split one, and
// whether one runs this tool's gate.
package hookcmd

import (
	"errors"
	"path/filepath"
	"strings"
)

// ShellWords splits a command the way a shell would: on whitespace, except inside quotes, where a space
// is part of the word. Every reader of a command stored as one string goes through here — the wiring
// check, the binary comparison, the probe that runs the wired hook, and a bench suite command — so one
// rule reads one field. It also reports whether the command was well formed: a quote left open is not a
// command anyone can read, so the caller that executes the words must refuse it, while a caller that only
// inspects a command reads the words either way. Splitting on whitespace alone, a hook wired as
// `"/opt/Program Files/tpp" gate` reads as three words whose first is a truncated path: the wiring
// check misses the subcommand, the comparison resolves nothing, and doctor stays quiet about a hook it is
// there to judge.
func ShellWords(command string) ([]string, error) {
	var (
		words []string
		cur   strings.Builder
		quote byte
	)
	flush := func() {
		if cur.Len() > 0 {
			words = append(words, cur.String())
			cur.Reset()
		}
	}
	for i := 0; i < len(command); i++ {
		switch c := command[i]; {
		case quote != 0:
			if c == quote {
				quote = 0
				continue
			}
			cur.WriteByte(c)
		case c == '"' || c == '\'':
			quote = c
		case c == ' ' || c == '\t' || c == '\r' || c == '\n':
			flush()
		default:
			cur.WriteByte(c)
		}
	}
	flush()
	if quote != 0 {
		return words, errors.New("unterminated quote")
	}
	return words, nil
}

// GateBinaries are the names the gate command is recognized under, installed first: tpp, then tsp, and
// rdd-plus before those renames. A hook wired by any of them is this tool's gate.
var GateBinaries = []string{"tpp", "tsp", "rdd-plus"}

// IsGate reports whether command runs this tool's gate: a well-formed command whose executable's base name
// is one of GateBinaries and whose next word is gate. The rule reads the executable's own name, never a
// substring of the command, so a sibling tool that merely lives under a directory named after the product
// is somebody else's hook.
func IsGate(command string) bool {
	return gateBinary(command) != ""
}

// InstalledBinary is the gate's installed name: sync moves legacy hooks to it, never to pending tsp.
const InstalledBinary = "tpp"

// IsLegacyGate reports whether command runs the gate under a name InstalledBinary superseded.
func IsLegacyGate(command string) bool {
	name := gateBinary(command)
	return name != "" && name != InstalledBinary && name != "tsp"
}

// gateBinary answers which of GateBinaries command runs the gate subcommand of, or "" for any other command.
func gateBinary(command string) string {
	words, err := ShellWords(command)
	if err != nil || len(words) < 2 || words[1] != "gate" {
		return ""
	}
	name := strings.TrimSuffix(filepath.Base(words[0]), ".exe")
	for _, binary := range GateBinaries {
		if name == binary {
			return binary
		}
	}
	return ""
}

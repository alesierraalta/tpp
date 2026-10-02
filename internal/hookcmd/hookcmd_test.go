package hookcmd

import "testing"

// One rule reads every command stored as one string, so the splitter is tested where it lives. A quoted
// path is one word, and both quote characters count as quoting: the reader that used to live beside the
// probe honoured double quotes only, which is how one command read as something other than what a shell
// would run. The signal is tested with the words: a quote left open is reported, not silently repaired,
// because the caller that executes the words has to refuse a command nobody can read — but the words read
// so far are still returned, so a caller that only inspects a command keeps reading exactly what it did
// before the signal existed.
func TestShellWordsSplitsLikeAShell(t *testing.T) {
	cases := []struct {
		name    string
		in      string
		want    []string
		wantErr bool
	}{
		{"plain command", `/h/bin/tpp gate`, []string{"/h/bin/tpp", "gate"}, false},
		{"double quoted path with a space", `"/h/my bin/tpp" gate`, []string{"/h/my bin/tpp", "gate"}, false},
		{"single quoted path with a space", `'/h/my bin/tpp' gate`, []string{"/h/my bin/tpp", "gate"}, false},
		{"tabs and newlines separate words", "a\tb\r\nc", []string{"a", "b", "c"}, false},
		{"leading and repeated whitespace", "  spaced   out  ", []string{"spaced", "out"}, false},
		{"one quoted word alone", `"/h/gate"`, []string{"/h/gate"}, false},
		{"whitespace only", "   \t\n", nil, false},
		{"empty", "", nil, false},
		// A quote left open is not a command: the probe must not run "unbalanced" as a program.
		{"unterminated double quote", `"/h/my bin/tpp gate`, []string{"/h/my bin/tpp gate"}, true},
		{"unterminated single quote", `'/h/my bin/tpp`, []string{"/h/my bin/tpp"}, true},
		{"unterminated quote after a word", `/h/bin/tpp "gate`, []string{"/h/bin/tpp", "gate"}, true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, err := ShellWords(tc.in)
			if (err != nil) != tc.wantErr {
				t.Fatalf("ShellWords(%q) error = %v, want an error: %v", tc.in, err, tc.wantErr)
			}
			if len(got) != len(tc.want) {
				t.Fatalf("%q -> %q, want %q", tc.in, got, tc.want)
			}
			for i := range got {
				if got[i] != tc.want[i] {
					t.Fatalf("%q -> %q, want %q", tc.in, got, tc.want)
				}
			}
		})
	}
}

// The gate ships as tsp: a `tsp gate` hook is the current wiring, while `tpp gate` and
// `rdd-plus gate` are pre-rename commands that still run this tool's gate and must be
// recognized as legacy so sync can move them to the canonical binary.
func TestGateCommandsRecognizeTSPAsCurrentAndTPPAndRDDPlusAsLegacy(t *testing.T) {
	cases := []struct {
		name       string
		command    string
		wantGate   bool
		wantLegacy bool
	}{
		{"the canonical tsp gate is current", "/h/bin/tsp gate", true, false},
		{"the pre-rename tpp gate is legacy", "/h/bin/tpp gate", true, true},
		{"the older rdd-plus gate is legacy", "/h/bin/rdd-plus gate", true, true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := IsGate(tc.command); got != tc.wantGate {
				t.Fatalf("IsGate(%q) = %v, want %v", tc.command, got, tc.wantGate)
			}
			if got := IsLegacyGate(tc.command); got != tc.wantLegacy {
				t.Fatalf("IsLegacyGate(%q) = %v, want %v", tc.command, got, tc.wantLegacy)
			}
		})
	}
}

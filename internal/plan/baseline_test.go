package plan

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// The Baseline fingerprint is what every resume compares against, so a value that only looks recorded
// (`pending`, a short hash) must be refused; the untouched template placeholder reads as unrecorded.
func TestCheckRefusesABaselineFingerprintThatFingerprintShDidNotWrite(t *testing.T) {
	for value, wantBreach := range map[string]bool{
		strings.Repeat("ab", 32):         false,
		"<assets/fingerprint.sh output>": false,
		"abc123":                         true,
		"pending":                        true,
		strings.Repeat("AB", 32):         true,
		strings.Repeat("ab", 32) + "ff":  true,
	} {
		t.Run(value, func(t *testing.T) {
			p := filepath.Join(t.TempDir(), "plan.md")
			if err := Init(p, "", false); err != nil {
				t.Fatal(err)
			}
			body, _ := os.ReadFile(p)
			plan := strings.Replace(string(body), "`<assets/fingerprint.sh output>`", "`"+value+"`", 1)
			if err := os.WriteFile(p, []byte(plan), 0o644); err != nil {
				t.Fatal(err)
			}
			problems, err := Check(p)
			if err != nil {
				t.Fatal(err)
			}
			var breach string
			for _, problem := range problems {
				if strings.Contains(problem, "Baseline fingerprint") {
					breach = problem
				}
			}
			if wantBreach && !strings.HasPrefix(breach, "line 4: ") {
				t.Fatalf("fingerprint %q: want a Baseline breach on line 4, got %v", value, problems)
			}
			if !wantBreach && breach != "" {
				t.Fatalf("fingerprint %q refused: %s", value, breach)
			}
		})
	}
}

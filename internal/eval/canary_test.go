package eval

import (
	"strings"
	"testing"
)

func TestScanLeaksFindsCanaryAndGroundTruthPaths(t *testing.T) {
	input := "ordinary text\nsecret token-123 present\nbench/cases/a/KEY.json copied\nworkspace/fix/keep-D1/main.go\n"
	leaks, err := ScanLeaks("agent transcript", strings.NewReader(input), "token-123")
	if err != nil {
		t.Fatal(err)
	}
	want := map[string]bool{"canary": false, "key-path": false, "fix-path": false}
	for _, leak := range leaks {
		if leak.Source != "agent transcript" || leak.Line < 1 || len([]rune(leak.Excerpt)) > 120 {
			t.Fatalf("bad leak metadata: %+v", leak)
		}
		want[leak.Kind] = true
	}
	for kind, found := range want {
		if !found {
			t.Errorf("missing %s leak in %+v", kind, leaks)
		}
	}
}

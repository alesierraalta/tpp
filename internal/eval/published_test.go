package eval

import (
	"os"
	"path/filepath"
	"testing"
)

func TestPublishedManifestsAndPolicyAreSealedAndCurrent(t *testing.T) {
	root := filepath.Join("..", "..", "bench")
	for _, dir := range []string{"benchmarks", "policies"} {
		entries, err := os.ReadDir(filepath.Join(root, dir))
		if err != nil {
			t.Fatal(err)
		}
		count := 0
		for _, entry := range entries {
			if entry.IsDir() || filepath.Ext(entry.Name()) != ".json" {
				continue
			}
			count++
			path := filepath.Join(root, dir, entry.Name())
			if dir == "benchmarks" {
				manifest, err := LoadManifest(path)
				if err != nil {
					t.Errorf("LoadManifest(%s): %v", path, err)
					continue
				}
				if got := VerifyCases(root, manifest); len(got) != 0 {
					t.Errorf("VerifyCases(%s) = %v", path, got)
				}
				continue
			}
			if _, err := LoadPolicy(path); err != nil {
				t.Errorf("LoadPolicy(%s): %v", path, err)
			}
		}
		if count == 0 {
			t.Errorf("no published JSON files under %s", filepath.Join(root, dir))
		}
	}
}

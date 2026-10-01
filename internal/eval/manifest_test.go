package eval

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/alesierraalta/tpp/internal/bench"
)

func TestCanonicalJSON(t *testing.T) {
	got, err := CanonicalJSON(struct {
		First  string `json:"first"`
		Second string `json:"second"`
	}{First: "<value>&", Second: "two"})
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != `{"first":"<value>&","second":"two"}` {
		t.Fatalf("CanonicalJSON() = %q", got)
	}
}

func TestTreeDigest(t *testing.T) {
	dir := t.TempDir()
	if got, err := TreeDigest(filepath.Join(dir, "missing")); err != nil || got != "" {
		t.Fatalf("missing tree = %q, %v; want empty digest", got, err)
	}
	if err := os.MkdirAll(filepath.Join(dir, "tree", "nested"), 0o755); err != nil {
		t.Fatal(err)
	}
	for name, body := range map[string]string{"z": "last", "nested/a": "first"} {
		if err := os.WriteFile(filepath.Join(dir, "tree", filepath.FromSlash(name)), []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	first, err := TreeDigest(filepath.Join(dir, "tree"))
	if err != nil {
		t.Fatal(err)
	}
	second, err := TreeDigest(filepath.Join(dir, "tree"))
	if err != nil || first != second || !strings.HasPrefix(first, "sha256:") {
		t.Fatalf("tree digest is not stable: %q / %q, %v", first, second, err)
	}
	if err := os.WriteFile(filepath.Join(dir, "tree", "z"), []byte("changed"), 0o644); err != nil {
		t.Fatal(err)
	}
	third, err := TreeDigest(filepath.Join(dir, "tree"))
	if err != nil || third == first {
		t.Fatalf("changed tree digest = %q, %v; original %q", third, err, first)
	}
	if err := os.Symlink("z", filepath.Join(dir, "tree", "link")); err == nil {
		if _, err := TreeDigest(filepath.Join(dir, "tree")); err == nil {
			t.Fatal("TreeDigest accepted a symlink")
		}
	}
}

func testManifestSpec(canary string) ManifestSpec {
	return ManifestSpec{
		Benchmark: "TEST", Version: "1.0", Status: "draft", Created: "2026-10-01T00:00:00Z",
		ChangeReason: "test", KeySchema: 2, Cases: []string{"c1"}, Domains: []Domain{Security},
		CriticalDomains: []Domain{Security}, DetectionCriteriaVersion: "dc-1",
		MetricConfig: MetricConfig{WeightedRecallW: 0.5, CILevel: 0.9, EarlyStopCILevel: 0.95, BootstrapResamples: 100, BootstrapSeed: 7, Consolidation: "strict-majority"},
		Replicates:   Replicates{KMin: 1, KTarget: 1, KMax: 1},
		Budgets:      Budgets{MaxCases: 1, MaxAttemptsPerCase: 1, MaxRetries: 1, MaxTokensPerCaseRun: 100, MaxCostPerCaseRunUSD: 1, MaxCostSuiteUSD: 1, MaxRuntimePerCaseRunSeconds: 60, MaxRuntimeSuiteSeconds: 60, Verdicts: []string{"PASS"}},
		Seeds:        map[string]int64{"case_order": 7}, Canary: canary,
	}
}

func writeManifestCase(t *testing.T, benchDir, canary string) {
	t.Helper()
	caseDir := filepath.Join(benchDir, "cases", "c1")
	for _, sub := range []string{"fixture", "fix"} {
		if err := os.MkdirAll(filepath.Join(caseDir, sub), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(caseDir, sub, "source.txt"), []byte(sub), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	key := bench.Key{Schema: 2, ID: "c1", Language: "go", Suite: "go test", Surface: "library", Control: bench.ControlClean, Canary: canary, Defects: []bench.Defect{}}
	data, err := json.Marshal(key)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(caseDir, bench.KeyFile), data, 0o644); err != nil {
		t.Fatal(err)
	}
}

func TestBuildSealLoadAndVerifyManifest(t *testing.T) {
	benchDir := t.TempDir()
	canary := "0123456789abcdef0123456789abcdef"
	writeManifestCase(t, benchDir, canary)
	manifest, err := BuildManifest(benchDir, testManifestSpec(canary))
	if err != nil {
		t.Fatal(err)
	}
	manifest, err = manifest.Seal()
	if err != nil {
		t.Fatal(err)
	}
	if manifest.ManifestSHA256 == "" {
		t.Fatal("Seal returned an empty digest")
	}
	if got := VerifyCases(benchDir, manifest); len(got) != 0 {
		t.Fatalf("unchanged case mismatches: %v", got)
	}
	path := filepath.Join(t.TempDir(), "manifest.json")
	data, err := CanonicalJSON(manifest)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, data, 0o644); err != nil {
		t.Fatal(err)
	}
	loaded, err := LoadManifest(path)
	if err != nil || loaded.ManifestSHA256 != manifest.ManifestSHA256 {
		t.Fatalf("LoadManifest() = %q, %v", loaded.ManifestSHA256, err)
	}
	if err := os.WriteFile(filepath.Join(benchDir, "cases", "c1", "fixture", "source.txt"), []byte("tampered"), 0o644); err != nil {
		t.Fatal(err)
	}
	if got := VerifyCases(benchDir, manifest); len(got) != 1 || got[0] != "c1" {
		t.Fatalf("VerifyCases() = %v, want [c1]", got)
	}
	manifest.ChangeReason = "tampered"
	data, err = CanonicalJSON(manifest)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, data, 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := LoadManifest(path); err == nil || !strings.Contains(err.Error(), "digest") {
		t.Fatalf("LoadManifest accepted modified manifest: %v", err)
	}
}

func TestBuildManifestRequiresCanaryAndValidReplicates(t *testing.T) {
	benchDir := t.TempDir()
	writeManifestCase(t, benchDir, "different")
	spec := testManifestSpec("expected")
	if _, err := BuildManifest(benchDir, spec); err == nil || !strings.Contains(err.Error(), "canary") {
		t.Fatalf("BuildManifest canary error = %v", err)
	}
	spec = testManifestSpec("different")
	spec.Replicates.KMin = 2
	if _, err := BuildManifest(benchDir, spec); err == nil || !strings.Contains(err.Error(), "replicate") {
		t.Fatalf("BuildManifest replicate error = %v", err)
	}
}

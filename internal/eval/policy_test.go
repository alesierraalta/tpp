package eval

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestPolicySealAndLoad(t *testing.T) {
	policy := Policy{
		Name: "policy-test", Created: "2026-10-01T00:00:00Z", ChangeReason: "test",
		Thresholds:       PolicyThresholds{RecallDropPassPP: 2, RecallDropReviewPP: 5, ExtremeEfficiencyRisePct: 200},
		SignificanceRule: true, Tradeoffs: Tradeoffs{T1RecallGainPP: 5, T1RecallCILowerAboveZero: true, T1AllowsCriticalHighIssuePromotion: true, T1RequiresNoCriticalHighIssueLower: true, T2F1GainPP: 3, T2F1CILowerAboveZero: true},
		HardBlockers: []HardBlocker{{ID: "H1", Condition: "new critical FN", Category: "REGRESSION"}, {ID: "H5", Condition: "manifest digest mismatch", Category: "INSTRUMENT"}},
	}
	sealed, err := policy.Seal()
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(t.TempDir(), "policy.json")
	data, err := CanonicalJSON(sealed)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, data, 0o644); err != nil {
		t.Fatal(err)
	}
	loaded, err := LoadPolicy(path)
	if err != nil || loaded.PolicySHA256 != sealed.PolicySHA256 || !loaded.Tradeoffs.T1AllowsCriticalHighIssuePromotion || !loaded.Tradeoffs.T2F1CILowerAboveZero {
		t.Fatalf("LoadPolicy() = %+v, %v", loaded, err)
	}
	loaded.Name = "tampered"
	data, err = CanonicalJSON(loaded)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, data, 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := LoadPolicy(path); err == nil || !strings.Contains(err.Error(), "digest") {
		t.Fatalf("LoadPolicy accepted modified policy: %v", err)
	}
}

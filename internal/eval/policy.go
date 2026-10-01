package eval

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
)

// PolicyThresholds records the PASS and REVIEW boundaries from the policy matrix.
type PolicyThresholds struct {
	RecallDropPassPP                float64 `json:"recall_drop_pass_pp"`
	RecallDropReviewPP              float64 `json:"recall_drop_review_pp"`
	PrecisionDropPassPP             float64 `json:"precision_drop_pass_pp"`
	PrecisionDropReviewPP           float64 `json:"precision_drop_review_pp"`
	F1DropPassPP                    float64 `json:"f1_drop_pass_pp"`
	F1DropReviewPP                  float64 `json:"f1_drop_review_pp"`
	ReproducibilityDropPassPP       float64 `json:"reproducibility_drop_pass_pp"`
	ReproducibilityDropReviewPP     float64 `json:"reproducibility_drop_review_pp"`
	FDRRisePassPP                   float64 `json:"fdr_rise_pass_pp"`
	FDRRiseReviewPP                 float64 `json:"fdr_rise_review_pp"`
	RuntimeRisePassPct              float64 `json:"runtime_rise_pass_pct"`
	RuntimeRiseReviewPct            float64 `json:"runtime_rise_review_pct"`
	CostRisePassPct                 float64 `json:"cost_rise_pass_pct"`
	CostRiseReviewPct               float64 `json:"cost_rise_review_pct"`
	NewCriticalFNFailCount          int     `json:"new_critical_fn_fail_count"`
	HighFNReviewCount               int     `json:"high_fn_review_count"`
	HighFNFailCount                 int     `json:"high_fn_fail_count"`
	CriticalDomainIssuesLowerReview int     `json:"critical_domain_issues_lower_review_count"`
	CriticalDomainIssuesLowerFail   int     `json:"critical_domain_issues_lower_fail_count"`
	ControlFPRRiseReview            int     `json:"control_fpr_rise_review_count"`
	ControlFPRRiseFail              int     `json:"control_fpr_rise_fail_count"`
	ExtremeEfficiencyRisePct        float64 `json:"extreme_efficiency_rise_pct"`
}

// Tradeoffs contains the only two benefit thresholds that can relax eligible FAIL rows.
type Tradeoffs struct {
	T1RecallGainPP                     float64 `json:"t1_recall_gain_pp"`
	T1RecallCILowerAboveZero           bool    `json:"t1_recall_ci_lower_above_zero"`
	T1AllowsCriticalHighIssuePromotion bool    `json:"t1_allows_critical_high_issue_promotion"`
	T1RequiresNoCriticalHighIssueLower bool    `json:"t1_requires_no_critical_high_issue_lower"`
	T2F1GainPP                         float64 `json:"t2_f1_gain_pp"`
	T2F1CILowerAboveZero               bool    `json:"t2_f1_ci_lower_above_zero"`
}

// HardBlocker is one unconditional FAIL condition from the decision policy.
type HardBlocker struct {
	ID        string `json:"id"`
	Condition string `json:"condition"`
	Category  string `json:"category"`
}

// Policy is the sealed decision matrix and its version metadata.
type Policy struct {
	Name             string           `json:"name"`
	Created          string           `json:"created"`
	ChangeReason     string           `json:"change_reason"`
	Thresholds       PolicyThresholds `json:"thresholds"`
	SignificanceRule bool             `json:"significance_rule"`
	Tradeoffs        Tradeoffs        `json:"tradeoffs"`
	HardBlockers     []HardBlocker    `json:"hard_blockers"`
	PolicySHA256     string           `json:"policy_sha256"`
}

// Seal returns a copy sealed over canonical JSON with the digest field empty.
func (p Policy) Seal() (Policy, error) {
	p.PolicySHA256 = ""
	data, err := CanonicalJSON(p)
	if err != nil {
		return Policy{}, fmt.Errorf("canonicalize policy: %w", err)
	}
	sum := sha256.Sum256(data)
	p.PolicySHA256 = "sha256:" + hex.EncodeToString(sum[:])
	return p, nil
}

// LoadPolicy reads a policy and refuses an absent or mismatched self-digest.
func LoadPolicy(path string) (Policy, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return Policy{}, fmt.Errorf("read policy %s: %w", path, err)
	}
	var policy Policy
	if err := json.Unmarshal(data, &policy); err != nil {
		return Policy{}, fmt.Errorf("parse policy %s: %w", path, err)
	}
	if policy.PolicySHA256 == "" {
		return Policy{}, fmt.Errorf("policy %s digest is empty", path)
	}
	sealed, err := policy.Seal()
	if err != nil {
		return Policy{}, err
	}
	if sealed.PolicySHA256 != policy.PolicySHA256 {
		return Policy{}, fmt.Errorf("policy %s digest mismatch: got %s, want %s", path, policy.PolicySHA256, sealed.PolicySHA256)
	}
	return policy, nil
}

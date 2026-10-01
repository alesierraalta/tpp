package eval

import (
	"fmt"

	"github.com/alesierraalta/tpp/internal/bench"
)

// IssuesFromKey converts a schema-v2 bench answer key into evaluation Issues.
func IssuesFromKey(k bench.Key) ([]Issue, error) {
	if k.Schema != 2 {
		return nil, fmt.Errorf("case %q requires KEY schema 2, got %d", k.ID, k.Schema)
	}
	if k.IsCleanControl() {
		return []Issue{}, nil
	}

	issues := make([]Issue, 0, len(k.Defects))
	for _, defect := range k.Defects {
		name := k.ID + "/" + defect.ID
		domain, ok := ParseDomain(defect.Domain)
		if !ok {
			return nil, fmt.Errorf("issue %s has unknown domain %q", name, defect.Domain)
		}
		severity, ok := ParseSeverity(defect.Severity)
		if !ok {
			return nil, fmt.Errorf("issue %s has unknown severity %q", name, defect.Severity)
		}
		if defect.Reproduction == nil {
			return nil, fmt.Errorf("issue %s has no reproduction", name)
		}
		issues = append(issues, Issue{
			Case:         k.ID,
			ID:           defect.ID,
			Domain:       domain,
			Severity:     severity,
			IssueType:    defect.IssueType,
			Reproduction: Reproduction(*defect.Reproduction),
		})
	}
	return issues, nil
}

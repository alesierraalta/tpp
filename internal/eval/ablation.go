package eval

import "sort"

type AblationClass string

const (
	AblationEssential    AblationClass = "ESSENTIAL"
	AblationContributing AblationClass = "CONTRIBUTING"
	AblationRedundant    AblationClass = "REDUNDANT"
	AblationNeutral      AblationClass = "NEUTRAL"
	AblationUnknown      AblationClass = "UNKNOWN"
)

func ClassifyAblation(decision ComparisonDecision) AblationClass {
	switch decision.Verdict {
	case "FAIL":
		return AblationEssential
	case "REVIEW":
		return AblationContributing
	case "PASS":
		for _, row := range decision.Rows {
			if (row.Name == "Runtime" || row.Name == "Cost") && row.Delta != nil && *row.Delta <= -10 {
				return AblationRedundant
			}
		}
		return AblationNeutral
	default:
		return AblationUnknown
	}
}

func NeedsPairwise(classes map[string]AblationClass) [][2]string {
	components := make([]string, 0)
	for component, class := range classes {
		if class == AblationRedundant {
			components = append(components, component)
		}
	}
	sort.Strings(components)
	pairs := make([][2]string, 0, len(components)*(len(components)-1)/2)
	for i := 0; i < len(components); i++ {
		for j := i + 1; j < len(components); j++ {
			pairs = append(pairs, [2]string{components[i], components[j]})
		}
	}
	return pairs
}

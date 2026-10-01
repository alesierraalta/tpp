// Package bench runs the testing skill against fixture projects with sealed answer keys and
// scores what its persisted plan reports against those keys.
package bench

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// Trigger is the minimal reproduction recorded for a planted defect.
type Trigger struct {
	Input    string `json:"input"`
	Expected string `json:"expected"`
	Actual   string `json:"actual"`
}

// DetectionCriteria states how a finding must be detected and proved.
type DetectionCriteria struct {
	Mechanism   string   `json:"mechanism"`
	Equivalents []string `json:"equivalents"`
	Proof       string   `json:"proof"`
}

// Reproduction describes the oracle used to reproduce a planted defect.
type Reproduction struct {
	Applies          bool   `json:"applies"`
	Oracle           string `json:"oracle"`
	Nondeterministic bool   `json:"nondeterministic"`
	Attempts         int    `json:"attempts"`
}

// Defect is one planted defect of a case; file paths are relative to the fixture root.
type Defect struct {
	ID                string             `json:"id"`
	File              string             `json:"file"`
	Line              int                `json:"line"`
	Class             string             `json:"class"`
	Keywords          []string           `json:"keywords"`
	Description       string             `json:"description"`
	Trigger           Trigger            `json:"trigger"`
	WhyMissed         string             `json:"why_missed"`
	IssueType         string             `json:"issue_type,omitempty"`
	Domain            string             `json:"domain,omitempty"`
	Severity          string             `json:"severity,omitempty"`
	SeverityRationale string             `json:"severity_rationale,omitempty"`
	ExpectedBehavior  string             `json:"expected_behavior,omitempty"`
	FailureCondition  string             `json:"failure_condition,omitempty"`
	DetectionCriteria *DetectionCriteria `json:"detection_criteria,omitempty"`
	Reproduction      *Reproduction      `json:"reproduction,omitempty"`
}

// Key is the sealed answer key of a case: KEY.json next to the fixture directory.
type Key struct {
	Schema   int    `json:"schema,omitempty"`
	ID       string `json:"id"`
	Language string `json:"language"`
	Suite    string `json:"suite"`
	Surface  string `json:"surface"`
	Control  string `json:"control,omitempty"`
	// Request is the bounded unit of work a run is asked to test, when the case has one. A case without
	// it is the generic case the bench has always run. The pointer is what tells the two apart: a key
	// that supplies a blank request is a mistake, not a generic case, and is refused rather than read
	// back as "no request".
	Request *string  `json:"request,omitempty"`
	Defects []Defect `json:"defects"`
}

// RequestText is the case's bounded request, or "" when it declares none.
func (k Key) RequestText() string {
	if k.Request == nil {
		return ""
	}
	return strings.TrimSpace(*k.Request)
}

func controlProblems(control string, defectCount int) []string {
	switch control {
	case "":
		if defectCount == 0 {
			return []string{"no defects"}
		}
	case ControlClean:
		if defectCount > 0 {
			return []string{fmt.Sprintf("clean control has %d defect(s)", defectCount)}
		}
	default:
		return []string{fmt.Sprintf("control %q is not accepted; accepted value is %q", control, ControlClean)}
	}
	return nil
}

// KeyFile is the answer-key file name; it must never be copied into a workspace.
const KeyFile = "KEY.json"

const ControlClean = "clean"

// IsCleanControl reports whether the key describes a reviewed clean negative control.
func (k Key) IsCleanControl() bool {
	return k.Control == ControlClean
}

// LoadKey reads and validates a case's KEY.json.
func LoadKey(caseDir string) (Key, error) {
	raw, err := os.ReadFile(filepath.Join(caseDir, KeyFile))
	if err != nil {
		return Key{}, fmt.Errorf("read key: %w", err)
	}
	var k Key
	if err := json.Unmarshal(raw, &k); err != nil {
		return Key{}, fmt.Errorf("parse key: %w", err)
	}
	if err := k.Validate(); err != nil {
		return Key{}, fmt.Errorf("invalid key: %w", err)
	}
	return k, nil
}

// Validate rejects keys the scorer could not apply unambiguously.
func (k Key) Validate() error {
	var problems []string
	if k.Schema != 0 && k.Schema != 1 && k.Schema != 2 {
		problems = append(problems, fmt.Sprintf("schema %d is not supported", k.Schema))
	}
	if strings.TrimSpace(k.ID) == "" {
		problems = append(problems, "id is empty")
	}
	if k.Language != "node" && k.Language != "go" {
		problems = append(problems, fmt.Sprintf("language %q is not node or go", k.Language))
	}
	if strings.TrimSpace(k.Suite) == "" {
		problems = append(problems, "suite is empty")
	}
	if k.Request != nil && k.RequestText() == "" {
		problems = append(problems, "request is present but empty")
	}
	problems = append(problems, controlProblems(k.Control, len(k.Defects))...)
	seen := map[string]bool{}
	for i, d := range k.Defects {
		switch {
		case strings.TrimSpace(d.ID) == "":
			problems = append(problems, fmt.Sprintf("defect %d has no id", i))
		case seen[d.ID]:
			problems = append(problems, fmt.Sprintf("defect id %q repeated", d.ID))
		}
		seen[d.ID] = true
		if strings.TrimSpace(d.File) == "" {
			problems = append(problems, fmt.Sprintf("defect %q has no file", d.ID))
		}
		if d.Line <= 0 {
			problems = append(problems, fmt.Sprintf("defect %q has no positive line", d.ID))
		}
		if len(d.Keywords) == 0 {
			problems = append(problems, fmt.Sprintf("defect %q has no keywords", d.ID))
		}
		if k.Schema == 2 {
			for _, field := range []struct{ name, value string }{
				{"issue_type", d.IssueType}, {"domain", d.Domain}, {"severity", d.Severity},
				{"severity_rationale", d.SeverityRationale}, {"expected_behavior", d.ExpectedBehavior},
				{"failure_condition", d.FailureCondition},
			} {
				if strings.TrimSpace(field.value) == "" {
					problems = append(problems, fmt.Sprintf("defect %q has no %s", d.ID, field.name))
				}
			}
			if d.DetectionCriteria == nil {
				problems = append(problems, fmt.Sprintf("defect %q has no detection_criteria", d.ID))
			} else {
				if strings.TrimSpace(d.DetectionCriteria.Mechanism) == "" {
					problems = append(problems, fmt.Sprintf("defect %q has no detection_criteria mechanism", d.ID))
				}
				if strings.TrimSpace(d.DetectionCriteria.Proof) == "" {
					problems = append(problems, fmt.Sprintf("defect %q has no detection_criteria proof", d.ID))
				}
			}
			if d.Reproduction == nil {
				problems = append(problems, fmt.Sprintf("defect %q has no reproduction", d.ID))
			} else {
				if d.Reproduction.Applies && d.Reproduction.Attempts < 1 {
					problems = append(problems, fmt.Sprintf("defect %q has no positive reproduction attempts", d.ID))
				}
				switch d.Reproduction.Oracle {
				case "catch", "command", "none":
				default:
					problems = append(problems, fmt.Sprintf("defect %q reproduction oracle %q is not catch, command, or none", d.ID, d.Reproduction.Oracle))
				}
			}
		}
	}
	if len(problems) > 0 {
		return errors.New(strings.Join(problems, "; "))
	}
	return nil
}

package eval

import (
	"encoding/json"
	"fmt"
	"strings"
)

// Severity ranks the consequence of an Issue or reported finding.
type Severity int

const SeverityUnknown Severity = -1

const (
	Info Severity = iota
	Low
	Medium
	High
	Critical
)

// ParseSeverity parses one of the five canonical lowercase severity words.
func ParseSeverity(s string) (Severity, bool) {
	switch s {
	case "info":
		return Info, true
	case "low":
		return Low, true
	case "medium":
		return Medium, true
	case "high":
		return High, true
	case "critical":
		return Critical, true
	default:
		return SeverityUnknown, false
	}
}

// SeverityFromReported maps a plan's free-text consequence class to its severity.
func SeverityFromReported(text string) Severity {
	text = strings.TrimSpace(text)
	if severity, ok := ParseSeverity(text); ok {
		return severity
	}
	lower := strings.ToLower(text)
	for _, phrase := range []string{"money moved", "money lost", "data destroyed", "data corrupted"} {
		if strings.Contains(lower, phrase) {
			return Critical
		}
	}
	if strings.Contains(lower, "permission") && strings.Contains(lower, "boundary") || strings.Contains(lower, "tenant") && strings.Contains(lower, "boundary") {
		return Critical
	}
	if strings.Contains(lower, "silently wrong") {
		return High
	}
	if strings.Contains(lower, "visible error") {
		return Medium
	}
	if strings.Contains(lower, "cosmetic") {
		return Info
	}
	return SeverityUnknown
}

// String returns the canonical lowercase severity word.
func (s Severity) String() string {
	switch s {
	case Info:
		return "info"
	case Low:
		return "low"
	case Medium:
		return "medium"
	case High:
		return "high"
	case Critical:
		return "critical"
	default:
		return "unknown"
	}
}

// MarshalJSON encodes a severity as its canonical word.
func (s Severity) MarshalJSON() ([]byte, error) {
	return json.Marshal(s.String())
}

// UnmarshalJSON decodes a canonical severity word, including "unknown".
func (s *Severity) UnmarshalJSON(data []byte) error {
	var word string
	if err := json.Unmarshal(data, &word); err != nil {
		return err
	}
	if word == "unknown" {
		*s = SeverityUnknown
		return nil
	}
	severity, ok := ParseSeverity(word)
	if !ok {
		return fmt.Errorf("unknown severity %q", word)
	}
	*s = severity
	return nil
}

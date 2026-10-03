// Package feature is the registry of optional capabilities: what can be turned on, how it is
// asked, and whether it is on. A slice of declarations, never a plugin system.
package feature

import (
	"fmt"
	"time"

	"github.com/alesierraalta/tsp/internal/state"
)

// Feature is one optional capability the human can toggle.
type Feature struct {
	ID      string // "feedback"
	Title   string // "Feedback"
	Prompt  string // the exact question shown during install/configure
	Help    string // one line under the question
	Default bool   // false: opt-in
}

var declarations = []Feature{
	{
		ID:      "feedback",
		Title:   "Feedback",
		Prompt:  "¿Quieres activar el feedback para ayudar a mejorar la herramienta?",
		Help:    "Records are anonymized and kept in two local files that are never publishable.",
		Default: false,
	},
}

// All returns every feature in a stable order.
func All() []Feature {
	return append([]Feature(nil), declarations...)
}

// Get answers one feature by id.
func Get(id string) (Feature, bool) {
	for _, feature := range declarations {
		if feature.ID == id {
			return feature, true
		}
	}
	return Feature{}, false
}

// Enabled answers the persisted toggle; a state with no entry answers the feature's Default.
func Enabled(id string) (bool, error) {
	declaration, ok := Get(id)
	if !ok {
		return false, unknownFeature(id)
	}
	current, err := state.Load()
	if err != nil {
		return false, err
	}
	if saved, ok := current.Features[id]; ok {
		return saved.Enabled, nil
	}
	return declaration.Default, nil
}

// Set persists a toggle and reports the timestamp written.
func Set(id string, enabled bool) (string, error) {
	declaration, ok := Get(id)
	if !ok {
		return "", unknownFeature(id)
	}
	current, err := state.Load()
	if err != nil {
		return "", err
	}
	previous, hadPrevious := current.Features[id]
	wasEnabled := declaration.Default
	if hadPrevious {
		wasEnabled = previous.Enabled
	}
	if hadPrevious && wasEnabled == enabled {
		if _, err := current.Save(); err != nil {
			return "", err
		}
		return previous.UpdatedAt, nil
	}

	if current.Features == nil {
		current.Features = make(map[string]state.FeatureState)
	}
	timestamp := time.Now().UTC().Format(time.RFC3339Nano)
	current.Features[id] = state.FeatureState{Enabled: enabled, UpdatedAt: timestamp}
	if _, err := current.Save(); err != nil {
		return "", err
	}
	return timestamp, nil
}

// Preview shows what turning the feature on would do, without turning it on.
func Preview(id string) (string, error) {
	if _, ok := Get(id); !ok {
		return "", unknownFeature(id)
	}
	return "Feedback records are anonymized in two local files (run-feedback.jsonl and run-feedback.md); both files are never publishable.", nil
}

func unknownFeature(id string) error {
	return fmt.Errorf("unknown feature %q", id)
}

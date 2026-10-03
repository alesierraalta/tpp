package feature

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/alesierraalta/tsp/internal/state"
)

func TestAllReturnsOneOptInFeature(t *testing.T) {
	features := All()
	if len(features) != 1 {
		t.Fatalf("All returned %d features, want 1", len(features))
	}
	feature := features[0]
	if feature.ID != "feedback" || feature.Title != "Feedback" || feature.Default {
		t.Fatalf("feature = %+v, want feedback with opt-in default", feature)
	}
	if feature.Prompt != "¿Quieres activar el feedback para ayudar a mejorar la herramienta?" {
		t.Fatalf("prompt = %q", feature.Prompt)
	}
	if !strings.Contains(feature.Help, "anonymized") || !strings.Contains(feature.Help, "two local files") {
		t.Fatalf("help = %q, want anonymization and local-file safety", feature.Help)
	}
}

func TestEnabledAnswersDefaultOnEmptyState(t *testing.T) {
	t.Setenv("TPP_HOME", t.TempDir())

	enabled, err := Enabled("feedback")
	if err != nil {
		t.Fatalf("Enabled: %v", err)
	}
	if enabled {
		t.Fatal("feedback is enabled without a persisted entry")
	}
}

func TestSetRoundTripsTheToggle(t *testing.T) {
	t.Setenv("TPP_HOME", t.TempDir())

	updatedAt, err := Set("feedback", true)
	if err != nil {
		t.Fatalf("Set true: %v", err)
	}
	if updatedAt == "" {
		t.Fatal("Set true returned an empty timestamp")
	}
	enabled, err := Enabled("feedback")
	if err != nil {
		t.Fatalf("Enabled after true: %v", err)
	}
	if !enabled {
		t.Fatal("feedback did not round-trip as enabled")
	}

	if _, err := Set("feedback", false); err != nil {
		t.Fatalf("Set false: %v", err)
	}
	enabled, err = Enabled("feedback")
	if err != nil {
		t.Fatalf("Enabled after false: %v", err)
	}
	if enabled {
		t.Fatal("feedback did not round-trip as disabled")
	}
}

func TestEnabledFailsOnCorruptState(t *testing.T) {
	t.Setenv("TPP_HOME", t.TempDir())
	path, err := state.Path()
	if err != nil {
		t.Fatalf("state.Path: %v", err)
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		t.Fatalf("state directory: %v", err)
	}
	if err := os.WriteFile(path, []byte("{"), 0o600); err != nil {
		t.Fatalf("corrupt state: %v", err)
	}

	enabled, err := Enabled("feedback")
	if err == nil || enabled {
		t.Fatalf("Enabled = %v, %v; want a fail-closed error", enabled, err)
	}
}

func TestPreviewDoesNotPersistAnything(t *testing.T) {
	t.Setenv("TPP_HOME", t.TempDir())

	preview, err := Preview("feedback")
	if err != nil {
		t.Fatalf("Preview: %v", err)
	}
	if !strings.Contains(preview, "never publishable") && !strings.Contains(preview, "ever publishable") {
		t.Fatalf("preview = %q, want local publication safety", preview)
	}
	path, err := state.Path()
	if err != nil {
		t.Fatalf("state.Path: %v", err)
	}
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Fatalf("Preview wrote state %s: %v", path, err)
	}
}

func TestSetSameValueDoesNotChurnStateBytes(t *testing.T) {
	t.Setenv("TPP_HOME", t.TempDir())

	if _, err := Set("feedback", true); err != nil {
		t.Fatalf("first Set: %v", err)
	}
	path, err := state.Path()
	if err != nil {
		t.Fatalf("state.Path: %v", err)
	}
	before, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("state before no-op Set: %v", err)
	}
	if _, err := Set("feedback", true); err != nil {
		t.Fatalf("second Set: %v", err)
	}
	after, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("state after no-op Set: %v", err)
	}
	if string(after) != string(before) {
		t.Fatalf("same-value Set churned state bytes:\n before %s\n after  %s", before, after)
	}
}

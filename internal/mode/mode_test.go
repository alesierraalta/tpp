package mode

import (
	"strings"
	"testing"
)

// The default request has to land on the standalone harness: this build has no verified Gentle
// runtime integration signal, and auto must never guess one from a binary on PATH or a version.
func TestResolveDefaultsToStandaloneWithoutAVerifiedSignal(t *testing.T) {
	for _, requested := range []string{"", Auto} {
		got, err := Resolve(requested, VerifiedGentleSignal())
		if err != nil {
			t.Fatalf("Resolve(%q) = %v, want no error", requested, err)
		}
		if got != Standalone {
			t.Fatalf("Resolve(%q) = %q, want %q", requested, got, Standalone)
		}
	}
}

// An explicit standalone is a choice, not a default: even under a verified signal it must stay
// the mode that runs, or an operator asking for the contract they tested would get another one.
func TestResolveStandaloneStaysStandaloneEvenUnderASignal(t *testing.T) {
	got, err := Resolve(Standalone, true)
	if err != nil {
		t.Fatalf("Resolve(standalone, signal) = %v, want no error", err)
	}
	if got != Standalone {
		t.Fatalf("Resolve(standalone, signal) = %q, want %q", got, Standalone)
	}
}

// Explicit gentle without a verified signal is refused with a message naming the refusal, and
// never resolves to a stand-in mode: silently answering "standalone" would report a contract
// the operator did not select.
func TestResolveGentleIsRefusedWhileIntegrationIsPending(t *testing.T) {
	got, err := Resolve(Gentle, false)
	if err == nil {
		t.Fatalf("Resolve(gentle) = %q, nil error; want a pending-integration refusal", got)
	}
	if !strings.Contains(err.Error(), "pending integration") {
		t.Fatalf("error = %q, want it to name pending integration", err)
	}
	if got != "" {
		t.Fatalf("a refused mode must not resolve to %q", got)
	}
}

// The contract the integration task wires: with a verified active signal, both auto and an
// explicit gentle name the gentle mode. Until that signal exists these branches stay unreachable
// from the CLI, which is the point — nothing may infer the signal.
func TestResolveFollowsAVerifiedGentleSignal(t *testing.T) {
	for _, tc := range []struct {
		requested string
		want      string
	}{
		{Auto, Gentle},
		{Gentle, Gentle},
	} {
		got, err := Resolve(tc.requested, true)
		if err != nil {
			t.Fatalf("Resolve(%q, signal) = %v, want no error", tc.requested, err)
		}
		if got != tc.want {
			t.Fatalf("Resolve(%q, signal) = %q, want %q", tc.requested, got, tc.want)
		}
	}
}

// A mode outside the documented set is a usage failure that names the set, so the operator sees
// the valid values in the same breath as the refusal.
func TestResolveRejectsAnUnknownMode(t *testing.T) {
	got, err := Resolve("turbo", false)
	if err == nil {
		t.Fatalf("Resolve(turbo) = %q, nil error; want a refusal", got)
	}
	if got != "" {
		t.Fatalf("a refused mode must not resolve to %q", got)
	}
	for _, want := range []string{"unknown mode", Auto, Standalone, Gentle} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("error = %q, want it to contain %q", err, want)
		}
	}
}

// There is no verified Gentle runtime integration in this build, and the seam must say so:
// a signal that answered true would send both commands into a mode nothing implements.
func TestNoVerifiedGentleSignalIsSupplied(t *testing.T) {
	if VerifiedGentleSignal() {
		t.Fatal("VerifiedGentleSignal() = true: no verified Gentle runtime integration exists in this build")
	}
}

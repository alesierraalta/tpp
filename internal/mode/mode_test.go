package mode

import (
	"os"
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

// The protocol with the Pi extension (assets/hosts/pi/tpp.ts); each side's tests pin its
// constant to these literals so neither shore can drift alone. legacyProtocolEnv is the
// pre-rename name: a fallback honoured only while the canonical variable is absent.
const (
	protocolEnv       = "TSP_GENTLE_OBSERVATION"
	legacyProtocolEnv = "TPP_GENTLE_OBSERVATION"
	protocolValue     = "pi-session-gentle-active"
)

// The extension's in-session observation — session UX evidence for mode selection, never
// authentication or security authority (documented in mode.go) — is the one signal it accepts.
func TestVerifiedGentleSignalAcceptsTheExtensionObservation(t *testing.T) {
	if GentleObservationEnv != protocolEnv || GentleObservationValue != protocolValue {
		t.Fatalf("protocol drift: mode declares (%q, %q), the extension protocol is (%q, %q)",
			GentleObservationEnv, GentleObservationValue, protocolEnv, protocolValue)
	}
	t.Setenv(protocolEnv, protocolValue)
	if !VerifiedGentleSignal() {
		t.Fatal("VerifiedGentleSignal() = false under the extension's observation; the adapter's session evidence must reach the CLI")
	}
}

// Anything that is not the exact observation must not select gentle, or the seam would accept
// noise as evidence.
func TestVerifiedGentleSignalRejectsAnyOtherValue(t *testing.T) {
	unbindEnv(t, legacyProtocolEnv)
	for _, value := range []string{"", "1", "true", "gentle-ai", protocolValue + "x", " " + protocolValue} {
		t.Setenv(protocolEnv, value)
		if VerifiedGentleSignal() {
			t.Errorf("VerifiedGentleSignal() = true for %q; only the exact extension observation counts", value)
		}
	}
}

// Without the observation there is no signal: outside the extension auto stays standalone, and
// neither PATH nor versions are consulted (see mode.go).
func TestVerifiedGentleSignalIsAbsentWithoutTheObservation(t *testing.T) {
	unbindEnv(t, protocolEnv)
	unbindEnv(t, legacyProtocolEnv)
	if VerifiedGentleSignal() {
		t.Fatal("VerifiedGentleSignal() = true without the observation; outside the extension auto must stay standalone")
	}
}

// unbindEnv removes a variable for the test and restores it afterwards, so no value ambient in
// the suite's own environment can stand in for either name of the protocol.
func unbindEnv(t *testing.T, name string) {
	t.Helper()
	original, had := os.LookupEnv(name)
	if err := os.Unsetenv(name); err != nil {
		t.Fatalf("unset %s: %v", name, err)
	}
	t.Cleanup(func() {
		if had {
			os.Setenv(name, original)
		}
	})
}

// The pre-rename name still carries the observation while the canonical one is absent, so an
// adapter that has not shipped the rename keeps selecting gentle — and only while TSP is absent.
func TestVerifiedGentleSignalFallsBackToTheLegacyObservationWhileTheCanonicalOneIsAbsent(t *testing.T) {
	unbindEnv(t, protocolEnv)
	t.Setenv(legacyProtocolEnv, protocolValue)
	if !VerifiedGentleSignal() {
		t.Fatal("VerifiedGentleSignal() = false with only the legacy observation set; a pre-rename adapter must keep working")
	}
}

// An explicit but wrong canonical value fails closed: it must not read as absent and slide into
// the legacy fallback, or a typo in the TSP variable would select gentle from the old name.
func TestVerifiedGentleSignalFailsClosedOnAnInvalidCanonicalObservationEvenWhenTheLegacyIsPresent(t *testing.T) {
	t.Setenv(legacyProtocolEnv, protocolValue)
	t.Setenv(protocolEnv, "yes")
	if VerifiedGentleSignal() {
		t.Fatal("VerifiedGentleSignal() = true for an invalid canonical observation while the legacy one was set; an explicit invalid TSP value must fail closed")
	}
}

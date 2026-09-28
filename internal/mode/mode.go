// Package mode resolves the --mode request shared by `tpp setup` and `tpp doctor` into the
// mode the command runs under, so both commands select and report modes the same way.
package mode

import "fmt"

// The values --mode accepts.
const (
	Auto       = "auto"
	Standalone = "standalone"
	Gentle     = "gentle"
)

// VerifiedGentleSignal reports whether a verified active Gentle runtime integration signal is
// present. It answers false in this build: no Gentle runtime interface exists yet, and neither
// a `gentle-ai` binary on PATH nor a version string counts as the signal — only the integration
// itself can supply it, so nothing here may shortcut to "active" to make a mode selectable.
func VerifiedGentleSignal() bool {
	return false
}

// Resolve maps a requested --mode value to the mode the command runs under. The empty request
// means auto. auto selects Gentle only when a verified signal is active and standalone
// otherwise; standalone always selects itself; gentle selects Gentle under that same signal and
// otherwise fails as pending integration, so a mode that cannot run is refused at the flag
// instead of silently substituting another contract. Unknown values fail naming the valid ones.
func Resolve(requested string, gentleActive bool) (string, error) {
	switch requested {
	case "", Auto:
		if gentleActive {
			return Gentle, nil
		}
		return Standalone, nil
	case Standalone:
		return Standalone, nil
	case Gentle:
		if gentleActive {
			return Gentle, nil
		}
		return "", fmt.Errorf("gentle mode: pending integration: no verified Gentle runtime integration signal is active; use %s or %s", Auto, Standalone)
	default:
		return "", fmt.Errorf("unknown mode %q: use %s, %s, or %s", requested, Auto, Standalone, Gentle)
	}
}

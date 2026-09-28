// Package mode resolves the --mode request shared by `tpp setup` and `tpp doctor` into the
// mode the command runs under, so both commands select and report modes the same way.
package mode

import (
	"fmt"
	"os"
)

// The values --mode accepts.
const (
	Auto       = "auto"
	Standalone = "standalone"
	Gentle     = "gentle"
)

// GentleObservationEnv and GentleObservationValue are the protocol with the Pi extension
// (assets/hosts/pi/tpp.ts): the extension passes this observation to the single child tpp process
// it spawns through child_process.execFile's env — no process.env mutation, no file — so it is
// process-scoped. The observation is session UX evidence for mode selection and reporting, not
// authentication or security authority: any process that can set an environment variable can set
// it, nothing here verifies identity or trust, and it may steer only which mode this process
// selects and reports — never anything else.
const (
	GentleObservationEnv   = "TPP_GENTLE_OBSERVATION"
	GentleObservationValue = "pi-session-gentle-active"
)

// VerifiedGentleSignal accepts only that exact observation and consults nothing else — no PATH,
// no version, no file — so outside the extension auto stays standalone even with gentle-ai there.
func VerifiedGentleSignal() bool {
	return os.Getenv(GentleObservationEnv) == GentleObservationValue
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

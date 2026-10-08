//go:build !(darwin || dragonfly || freebsd || illumos || linux || netbsd || openbsd || windows)

package plan

import (
	"errors"
	"os"
	"runtime"
)

// errNoPlanLock is what every other target gets, and it fails closed on purpose: this package has no
// cross-process lock on this platform, and a plan written without serialization is the silently lost row the
// lock exists to prevent. Refusing to write is recoverable; a plan that quietly dropped a finding is not.
var errNoPlanLock = errors.New("tsp has no cross-process file lock on " + runtime.GOOS + ", so it refuses to write a plan without serialization")

// lockFile refuses: there is no lock to take here.
func lockFile(*os.File) error { return errNoPlanLock }

// unlockFile refuses for symmetry. It is only reached when lockFile would have refused first.
func unlockFile(*os.File) error { return errNoPlanLock }

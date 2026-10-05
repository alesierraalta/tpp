//go:build !(aix || android || darwin || dragonfly || freebsd || illumos || ios || linux || netbsd || openbsd || solaris)

package bench

import (
	"os/exec"
	"time"
)

const novelWaitDelay = 500 * time.Millisecond

func novelProcessGroupsSupported() bool             { return false }
func configureNovelProcess(*exec.Cmd)               {}
func killNovelProcessGroup(*exec.Cmd) (bool, error) { return false, nil }

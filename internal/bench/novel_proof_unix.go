//go:build aix || android || darwin || dragonfly || freebsd || illumos || ios || linux || netbsd || openbsd || solaris

package bench

import (
	"errors"
	"os/exec"
	"syscall"
	"time"
)

const novelWaitDelay = 500 * time.Millisecond

func novelProcessGroupsSupported() bool { return true }

func configureNovelProcess(cmd *exec.Cmd) {
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	cmd.Cancel = func() error {
		if cmd.Process == nil {
			return errors.New("novel runner process was not started")
		}
		return syscall.Kill(-cmd.Process.Pid, syscall.SIGKILL)
	}
}

// killNovelProcessGroup reports whether a process remained in the group and was signalled.
func killNovelProcessGroup(cmd *exec.Cmd) (bool, error) {
	if cmd.Process == nil {
		return false, nil
	}
	err := syscall.Kill(-cmd.Process.Pid, syscall.SIGKILL)
	if errors.Is(err, syscall.ESRCH) {
		return false, nil
	}
	return err == nil, err
}

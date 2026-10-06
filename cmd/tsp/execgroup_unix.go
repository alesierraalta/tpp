//go:build unix

package main

import (
	"os/exec"
	"syscall"
)

// processGroup puts the shell in its own process group and points the deadline at that group, so a
// descendant that inherited the row's output pipes is ended with it instead of holding them open.
func processGroup(cmd *exec.Cmd) {
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	cmd.Cancel = func() error { return killGroup(cmd) }
}

// killGroup ends the group the shell leads, falling back to the direct process when no group is left.
func killGroup(cmd *exec.Cmd) error {
	if cmd.Process == nil {
		return nil
	}
	if err := syscall.Kill(-cmd.Process.Pid, syscall.SIGKILL); err != nil {
		return cmd.Process.Kill()
	}
	return nil
}

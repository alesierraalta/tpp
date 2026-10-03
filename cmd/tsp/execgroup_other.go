//go:build !unix

package main

import "os/exec"

// processGroup cannot set a process group outside Unix, so only the direct process ends at the deadline.
// WaitDelay still bounds the wait for the pipes a descendant left open.
func processGroup(cmd *exec.Cmd) {
	cmd.Cancel = func() error { return killGroup(cmd) }
}

// killGroup ends the direct process where there is no group to end.
func killGroup(cmd *exec.Cmd) error {
	if cmd.Process == nil {
		return nil
	}
	return cmd.Process.Kill()
}

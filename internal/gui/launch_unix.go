//go:build !windows

package gui

import (
	"os/exec"
	"syscall"
)

// detach starts the agent in its own session so it outlives the GUI.
func detach(cmd *exec.Cmd) { cmd.SysProcAttr = &syscall.SysProcAttr{Setsid: true} }

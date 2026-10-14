//go:build !windows

package adapter

import (
	"os/exec"
	"syscall"
)

// detach starts the daemon in its own session so it outlives the parent.
func detach(cmd *exec.Cmd) {
	cmd.SysProcAttr = &syscall.SysProcAttr{Setsid: true}
}

package adapter

import "os/exec"

// detach is a no-op on Windows: syscall.SysProcAttr has no Setsid there.
func detach(cmd *exec.Cmd) {}

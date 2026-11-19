package embed

import "syscall"

// procAttr makes the kernel kill the server when this process dies, however
// it dies.
func procAttr() *syscall.SysProcAttr { return &syscall.SysProcAttr{Pdeathsig: syscall.SIGKILL} }

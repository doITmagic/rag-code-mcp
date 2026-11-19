//go:build !linux && !windows

package embed

import "syscall"

func procAttr() *syscall.SysProcAttr { return nil }

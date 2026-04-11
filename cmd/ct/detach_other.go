//go:build !linux

package main

import "syscall"

// detachSysProcAttr returns nil on non-Linux platforms where Setsid is not
// supported. The process will still start; it just won't be detached from the
// controlling terminal.
func detachSysProcAttr() *syscall.SysProcAttr {
	return nil
}

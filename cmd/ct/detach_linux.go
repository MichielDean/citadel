//go:build linux

package main

import "syscall"

// detachSysProcAttr returns a SysProcAttr that detaches the process from the
// controlling terminal by creating a new session (Setsid). Used when spawning
// a detached background Castellarius process as a fallback to systemctl.
func detachSysProcAttr() *syscall.SysProcAttr {
	return &syscall.SysProcAttr{Setsid: true}
}

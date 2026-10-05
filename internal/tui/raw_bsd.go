//go:build darwin || freebsd || netbsd || openbsd || dragonfly

package tui

import "syscall"

const (
	ioctlGet = syscall.TIOCGETA
	ioctlSet = syscall.TIOCSETA
)

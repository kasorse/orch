//go:build darwin || freebsd || netbsd || openbsd || dragonfly || linux

package tui

import (
	"os"
	"syscall"
	"unsafe"
)

// Supported reports whether f is a terminal this package can drive.
func Supported(f *os.File) bool {
	var t syscall.Termios
	return ioctl(f.Fd(), ioctlGet, &t) == nil
}

// makeRaw switches f to non-canonical, no-echo input (keys arrive one by one) and returns
// the function that restores the previous state. Output processing is kept, so "\n" still
// starts a new line. Ctrl-C arrives as a key (ISIG off), so the form can restore the
// terminal before the run is cancelled.
func makeRaw(f *os.File) (func(), error) {
	var old syscall.Termios
	if err := ioctl(f.Fd(), ioctlGet, &old); err != nil {
		return nil, err
	}
	raw := old
	raw.Lflag &^= syscall.ICANON | syscall.ECHO | syscall.IEXTEN | syscall.ISIG
	raw.Iflag &^= syscall.ICRNL
	raw.Cc[syscall.VMIN] = 1
	raw.Cc[syscall.VTIME] = 0
	if err := ioctl(f.Fd(), ioctlSet, &raw); err != nil {
		return nil, err
	}
	return func() { _ = ioctl(f.Fd(), ioctlSet, &old) }, nil
}

func ioctl(fd uintptr, req uint, t *syscall.Termios) error {
	if _, _, errno := syscall.Syscall(syscall.SYS_IOCTL, fd, uintptr(req), uintptr(unsafe.Pointer(t))); errno != 0 {
		return errno
	}
	return nil
}

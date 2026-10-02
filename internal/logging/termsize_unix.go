//go:build unix

package logging

import (
	"syscall"
	"unsafe"
)

// ttyWidth asks the terminal on fd for its width (TIOCGWINSZ); 0 when it is not one.
func ttyWidth(fd uintptr) int {
	var ws struct{ Row, Col, X, Y uint16 }
	if _, _, errno := syscall.Syscall(syscall.SYS_IOCTL, fd, uintptr(syscall.TIOCGWINSZ), uintptr(unsafe.Pointer(&ws))); errno != 0 {
		return 0
	}
	return int(ws.Col)
}

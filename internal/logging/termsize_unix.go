//go:build unix

package logging

import (
	"syscall"
	"unsafe"
)

// ttySize asks the terminal on fd for its size (TIOCGWINSZ); zeros when it is not one.
func ttySize(fd uintptr) (rows, cols int) {
	var ws struct{ Row, Col, X, Y uint16 }
	if _, _, errno := syscall.Syscall(syscall.SYS_IOCTL, fd, uintptr(syscall.TIOCGWINSZ), uintptr(unsafe.Pointer(&ws))); errno != 0 {
		return 0, 0
	}
	return int(ws.Row), int(ws.Col)
}

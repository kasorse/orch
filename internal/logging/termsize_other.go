//go:build !unix

package logging

func ttySize(fd uintptr) (rows, cols int) { return 0, 0 }

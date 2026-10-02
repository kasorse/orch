//go:build !unix

package logging

func ttyWidth(fd uintptr) int { return 0 }

//go:build !(darwin || freebsd || netbsd || openbsd || dragonfly || linux)

package tui

import (
	"errors"
	"os"
)

func Supported(*os.File) bool { return false }

func makeRaw(*os.File) (func(), error) {
	return nil, errors.New("raw terminal input is not supported here")
}

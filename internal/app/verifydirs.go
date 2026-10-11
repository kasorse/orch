package app

import (
	"os"
	"path/filepath"
	"syscall"
	"time"
)

// verifyDirPrefix and staleVerifyAge: an implement step with verify on works in a temporary
// directory called orch-verify-*, removed when the step ends. A killed orchestrator leaves
// it behind. A live one belongs to another running orchestrator and is younger than a step
// can last, so only a day-old directory is swept (specs/quality/SPEC.md §6.4).
const (
	verifyDirPrefix = "orch-verify-"
	staleVerifyAge  = 24 * time.Hour
)

// removeStaleVerifyDirs removes the orch-verify-* directories of tmp that this user owns and
// that were last modified more than a day before now. It never follows a symlink, skips
// anything that is not a plain directory, and ignores every error: a sweep must not fail a
// run.
func removeStaleVerifyDirs(tmp string, now time.Time) {
	matches, err := filepath.Glob(filepath.Join(tmp, verifyDirPrefix+"*"))
	if err != nil {
		return
	}
	uid := uint32(os.Getuid())
	for _, path := range matches {
		info, err := os.Lstat(path)
		if err != nil || !info.IsDir() || info.Mode()&os.ModeSymlink != 0 {
			continue
		}
		if st, ok := info.Sys().(*syscall.Stat_t); !ok || st.Uid != uid {
			continue
		}
		if now.Sub(info.ModTime()) < staleVerifyAge {
			continue
		}
		_ = os.RemoveAll(path)
	}
}

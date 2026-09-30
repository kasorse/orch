package persistence

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"sync"
	"syscall"

	"orchestrator/internal/apperr"
)

// Lock takes an exclusive, non-blocking flock on the run directory itself, so only one
// `run` or `resume` works on a run at a time (specs/resume/SPEC.md §11). The kernel
// releases it when the process dies, so a stale lock never outlives a crash, and there is
// no lock file that could be deleted to bypass it. A run whose lock is held is
// RunStateError{in_use}.
//
// The returned func releases the lock. The caller must keep it until the run is done: the
// open directory it holds is what keeps the lock.
func (s *Store) Lock(runID string) (unlock func(), err error) {
	dir, err := os.Open(s.runDir(runID))
	if errors.Is(err, fs.ErrNotExist) {
		return nil, s.notFound(runID)
	}
	if err != nil {
		return nil, fmt.Errorf("open run directory: %w", err)
	}

	fd := int(dir.Fd())
	if err := syscall.Flock(fd, syscall.LOCK_EX|syscall.LOCK_NB); err != nil {
		dir.Close()
		if errors.Is(err, syscall.EWOULDBLOCK) {
			return nil, &apperr.RunStateError{Kind: apperr.RunInUse, RunID: runID, Msg: "the run is in use by another orchestrator process"}
		}
		return nil, fmt.Errorf("lock run directory: %w", err)
	}

	var once sync.Once
	return func() {
		once.Do(func() {
			_ = syscall.Flock(fd, syscall.LOCK_UN)
			dir.Close()
		})
	}, nil
}

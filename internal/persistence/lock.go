package persistence

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"sync"
	"syscall"
	"time"

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
	if err := flockRetrying(fd); err != nil {
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

// lockRetries and lockRetryWait are how often, and how far apart, Lock tries again a lock it finds
// held: DirLocked's probe holds it for two system calls, and a run that starts meanwhile must not
// be refused for it. A run in use holds it far longer than the retries last.
const (
	lockRetries   = 5
	lockRetryWait = 10 * time.Millisecond
)

// flockRetrying takes the exclusive, non-blocking flock on fd, trying again a few times while it
// is held.
func flockRetrying(fd int) error {
	for i := 0; ; i++ {
		err := syscall.Flock(fd, syscall.LOCK_EX|syscall.LOCK_NB)
		if err == nil || !errors.Is(err, syscall.EWOULDBLOCK) || i == lockRetries {
			return err
		}
		time.Sleep(lockRetryWait)
	}
}

// DirLocked reports whether a process holds the lock Store.Lock takes on the run directory dir:
// that run is in progress. A directory that cannot be opened or probed is not locked. The probe
// takes a shared lock only to let it go again at once; a Lock that meets it tries again.
func DirLocked(dir string) bool {
	d, err := os.Open(dir)
	if err != nil {
		return false
	}
	defer d.Close()
	fd := int(d.Fd())
	if err := syscall.Flock(fd, syscall.LOCK_SH|syscall.LOCK_NB); err != nil {
		return errors.Is(err, syscall.EWOULDBLOCK)
	}
	_ = syscall.Flock(fd, syscall.LOCK_UN)
	return false
}

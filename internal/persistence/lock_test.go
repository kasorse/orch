package persistence

import (
	"os"
	"path/filepath"
	"syscall"
	"testing"
	"time"

	"orchestrator/internal/apperr"
	"orchestrator/internal/domain"
)

func TestLock_ExcludesASecondHolder(t *testing.T) {
	store := NewStore(t.TempDir())
	run, err := store.NewRun(domain.Task{Description: "x"})
	if err != nil {
		t.Fatal(err)
	}

	unlock, err := store.Lock(run.ID)
	if err != nil {
		t.Fatalf("first Lock: %v", err)
	}
	// flock locks belong to the open file description, so a second open in the same
	// process contends exactly as another process would.
	_, err = store.Lock(run.ID)
	assertRunStateKind(t, err, apperr.RunInUse)

	unlock()
	unlock() // idempotent

	unlock2, err := store.Lock(run.ID)
	if err != nil {
		t.Fatalf("Lock after unlock: %v", err)
	}
	unlock2()
}

func TestLock_OtherRunsAreIndependent(t *testing.T) {
	store := NewStore(t.TempDir())
	a, _ := store.NewRun(domain.Task{Description: "a"})
	b, _ := store.NewRun(domain.Task{Description: "b"})

	unlockA, err := store.Lock(a.ID)
	if err != nil {
		t.Fatal(err)
	}
	defer unlockA()
	unlockB, err := store.Lock(b.ID)
	if err != nil {
		t.Fatalf("locking another run: %v", err)
	}
	unlockB()
}

func TestDirLocked(t *testing.T) {
	store := NewStore(t.TempDir())
	run, err := store.NewRun(domain.Task{Description: "x"})
	if err != nil {
		t.Fatal(err)
	}
	dir := store.runDir(run.ID)
	if DirLocked(dir) {
		t.Error("a run nobody works on is locked")
	}
	unlock, err := store.Lock(run.ID)
	if err != nil {
		t.Fatal(err)
	}
	if !DirLocked(dir) {
		t.Error("a run that is locked is not reported")
	}
	unlock()
	if DirLocked(dir) {
		t.Error("the probe kept the lock, or the unlock did not release it")
	}
	if _, err := store.Lock(run.ID); err != nil {
		t.Errorf("Lock after a probe: %v", err)
	}
	if DirLocked(filepath.Join(dir, "missing")) {
		t.Error("a directory that is not there is locked")
	}
}

// A Lock that meets a probe's moment of holding the lock tries again rather than refusing the run:
// the parent probes a sibling's run directory while that sibling may be starting.
func TestLock_WaitsOutAProbe(t *testing.T) {
	store := NewStore(t.TempDir())
	run, err := store.NewRun(domain.Task{Description: "x"})
	if err != nil {
		t.Fatal(err)
	}
	probe, err := os.Open(store.runDir(run.ID))
	if err != nil {
		t.Fatal(err)
	}
	defer probe.Close()
	if err := syscall.Flock(int(probe.Fd()), syscall.LOCK_SH|syscall.LOCK_NB); err != nil {
		t.Fatal(err)
	}
	released := make(chan struct{})
	go func() {
		time.Sleep(2 * lockRetryWait)
		_ = syscall.Flock(int(probe.Fd()), syscall.LOCK_UN)
		close(released)
	}()
	unlock, err := store.Lock(run.ID)
	<-released
	if err != nil {
		t.Fatalf("Lock while a probe held the lock for a moment: %v", err)
	}
	unlock()
}

func TestLock_UnknownRun(t *testing.T) {
	store := NewStore(t.TempDir())
	_, err := store.Lock("00000000-0000-4000-8000-000000000000")
	assertRunStateKind(t, err, apperr.RunNotFound)
}

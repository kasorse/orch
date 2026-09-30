package persistence

import (
	"testing"

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

func TestLock_UnknownRun(t *testing.T) {
	store := NewStore(t.TempDir())
	_, err := store.Lock("00000000-0000-4000-8000-000000000000")
	assertRunStateKind(t, err, apperr.RunNotFound)
}

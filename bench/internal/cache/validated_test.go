package cache

import (
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"orchbench/internal/task"
)

func TestValidatedRecordAndLookup(t *testing.T) {
	v := OpenValidated(Dir(t.TempDir()))
	at := time.Date(2026, 10, 6, 12, 0, 0, 0, time.UTC)

	if _, ok := v.Lookup("a"); ok {
		t.Fatal("an empty store has an entry")
	}
	if err := v.Record("a", "hash-1", at); err != nil {
		t.Fatal(err)
	}

	e, ok := v.Lookup("a")
	if !ok || e.SHA256 != "hash-1" || !e.ValidatedAt.Equal(at) {
		t.Fatalf("Lookup = %+v, %v", e, ok)
	}
	if !v.IsValid("a", "hash-1") {
		t.Error("the recorded hash is not valid")
	}
	if v.IsValid("a", "hash-2") {
		t.Error("another hash is valid")
	}
	if v.IsValid("b", "hash-1") {
		t.Error("an unknown task is valid")
	}
	if v.IsValid("a", "") {
		t.Error("an empty hash is valid")
	}

	if err := v.Record("a", "hash-2", at); err != nil {
		t.Fatal(err)
	}
	if !v.IsValid("a", "hash-2") || v.IsValid("a", "hash-1") {
		t.Error("Record did not replace the hash")
	}
}

func TestValidatedForget(t *testing.T) {
	dir := Dir(t.TempDir())
	v := OpenValidated(dir)
	at := time.Now()

	if err := v.Forget("never-recorded"); err != nil {
		t.Fatalf("forgetting an unknown task: %v", err)
	}
	if err := v.Record("a", "h", at); err != nil {
		t.Fatal(err)
	}
	if err := v.Record("b", "h", at); err != nil {
		t.Fatal(err)
	}
	if err := v.Forget("a"); err != nil {
		t.Fatal(err)
	}
	if _, ok := v.Lookup("a"); ok {
		t.Error("a is still recorded")
	}
	if _, ok := v.Lookup("b"); !ok {
		t.Error("b was forgotten with a")
	}
}

func TestValidatedIsWrittenAtomically(t *testing.T) {
	dir := Dir(t.TempDir())
	v := OpenValidated(dir)
	if err := v.Record("a", "h", time.Now()); err != nil {
		t.Fatal(err)
	}

	left, err := filepath.Glob(filepath.Join(string(dir), ".validated-*"))
	if err != nil || len(left) != 0 {
		t.Fatalf("temporary files left behind: %v (%v)", left, err)
	}
	if _, err := os.Stat(dir.ValidatedFile()); err != nil {
		t.Fatal(err)
	}
}

func TestValidatedCorruptFile(t *testing.T) {
	dir := Dir(t.TempDir())
	if err := os.WriteFile(dir.ValidatedFile(), []byte("{not json"), 0o644); err != nil {
		t.Fatal(err)
	}
	v := OpenValidated(dir)

	if v.IsValid("a", "h") {
		t.Error("a corrupt file validated a task")
	}
	if err := v.Record("a", "h", time.Now()); err == nil {
		t.Error("Record overwrote a corrupt file silently")
	}
}

func TestValidatedConcurrentRecordsLoseNothing(t *testing.T) {
	v := OpenValidated(Dir(t.TempDir()))
	const n = 24

	var wg sync.WaitGroup
	for i := 0; i < n; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			if err := v.Record(fmt.Sprintf("task-%d", i), "h", time.Now()); err != nil {
				t.Error(err)
			}
		}()
	}
	wg.Wait()

	for i := 0; i < n; i++ {
		if _, ok := v.Lookup(fmt.Sprintf("task-%d", i)); !ok {
			t.Errorf("task-%d was lost", i)
		}
	}
}

func TestEditingTheTaskFileInvalidatesIt(t *testing.T) {
	file := filepath.Join(t.TempDir(), "a.yaml")
	if err := os.WriteFile(file, []byte("id: a\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	hash, err := task.SHA256(file)
	if err != nil {
		t.Fatal(err)
	}

	v := OpenValidated(Dir(t.TempDir()))
	if err := v.Record("a", hash, time.Now()); err != nil {
		t.Fatal(err)
	}
	if !v.IsValid("a", hash) {
		t.Fatal("the recorded task is not valid")
	}

	if err := os.WriteFile(file, []byte("id: a\ntask: edited\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	edited, err := task.SHA256(file)
	if err != nil {
		t.Fatal(err)
	}
	if v.IsValid("a", edited) {
		t.Error("an edited task file is still valid")
	}
}

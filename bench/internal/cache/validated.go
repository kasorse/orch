package cache

import (
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"sync"
	"time"
)

// Entry is what validated.json remembers about one task: the hash of the task file that
// passed `validate`, and when.
type Entry struct {
	SHA256      string    `json:"sha256"`
	ValidatedAt time.Time `json:"validated_at"`
}

// Validated is validated.json: task id → Entry. `validate` writes it and `run` refuses a task
// whose file hash is not the recorded one (SPEC.md §6). Tasks are checked in parallel, so
// every change re-reads the file under a mutex and replaces it atomically. The mutex covers
// one process; two `validate` processes on one cache directory can still lose an update.
type Validated struct {
	mu   sync.Mutex
	path string
}

// OpenValidated returns the store of a cache directory. The file need not exist yet.
func OpenValidated(dir Dir) *Validated {
	return &Validated{path: dir.ValidatedFile()}
}

// Record remembers that the task with this id passed with the file hash.
func (v *Validated) Record(id, hash string, at time.Time) error {
	v.mu.Lock()
	defer v.mu.Unlock()

	entries, err := v.read()
	if err != nil {
		return err
	}
	entries[id] = Entry{SHA256: hash, ValidatedAt: at.UTC()}
	return v.write(entries)
}

// Forget removes the entry of a task that no longer passes. A task without one is not an
// error.
func (v *Validated) Forget(id string) error {
	v.mu.Lock()
	defer v.mu.Unlock()

	entries, err := v.read()
	if err != nil {
		return err
	}
	if _, ok := entries[id]; !ok {
		return nil
	}
	delete(entries, id)
	return v.write(entries)
}

// Lookup returns the entry of a task. A file that cannot be read or parsed has no entries:
// the caller then treats every task as unvalidated, which is the safe side.
func (v *Validated) Lookup(id string) (Entry, bool) {
	v.mu.Lock()
	defer v.mu.Unlock()

	entries, err := v.read()
	if err != nil {
		return Entry{}, false
	}
	e, ok := entries[id]
	return e, ok
}

// IsValid reports whether the task passed validation with exactly this file hash. A task
// whose file was edited since has another hash and is unvalidated.
func (v *Validated) IsValid(id, hash string) bool {
	e, ok := v.Lookup(id)
	return ok && hash != "" && e.SHA256 == hash
}

// read returns the entries; a missing file is an empty set and a corrupt one an error.
func (v *Validated) read() (map[string]Entry, error) {
	entries := make(map[string]Entry)

	data, err := os.ReadFile(v.path)
	if errors.Is(err, fs.ErrNotExist) {
		return entries, nil
	}
	if err != nil {
		return nil, err
	}
	if err := json.Unmarshal(data, &entries); err != nil {
		return nil, fmt.Errorf("%s: %w", v.path, err)
	}
	if entries == nil {
		entries = make(map[string]Entry)
	}
	return entries, nil
}

// write replaces the file through a temporary file in the same directory, so a reader or a
// crash sees the old content or the new one, never half of it.
func (v *Validated) write(entries map[string]Entry) (err error) {
	data, err := json.MarshalIndent(entries, "", "  ")
	if err != nil {
		return err
	}
	data = append(data, '\n')

	dir := filepath.Dir(v.path)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}
	tmp, err := os.CreateTemp(dir, ".validated-*.tmp")
	if err != nil {
		return err
	}
	defer func() {
		if err != nil {
			tmp.Close()
			os.Remove(tmp.Name())
		}
	}()

	if _, err = tmp.Write(data); err != nil {
		return err
	}
	if err = tmp.Sync(); err != nil {
		return err
	}
	if err = tmp.Close(); err != nil {
		return err
	}
	if err = os.Chmod(tmp.Name(), 0o644); err != nil {
		return err
	}
	return os.Rename(tmp.Name(), v.path)
}

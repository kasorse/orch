// Package cache is where orchbench keeps what it can rebuild and what it must remember between
// commands: repository mirrors, workspaces, logs and validated.json (specs/benchmark/SPEC.md
// §13.2). Every path under --cache-dir is derived here, so `validate` and `run` agree.
package cache

import (
	"errors"
	"os"
	"path/filepath"
)

// Dir is the --cache-dir.
type Dir string

// DefaultDir is $XDG_CACHE_HOME/orchbench, or ~/.cache/orchbench when XDG_CACHE_HOME is unset
// or not an absolute path (as the XDG specification says to treat it). It is empty when
// neither can be determined.
func DefaultDir() Dir {
	if dir := os.Getenv("XDG_CACHE_HOME"); filepath.IsAbs(dir) {
		return Dir(filepath.Join(dir, "orchbench"))
	}
	if home, err := os.UserHomeDir(); err == nil {
		return Dir(filepath.Join(home, ".cache", "orchbench"))
	}
	return ""
}

// Mirrors holds one bare mirror per repository, named by the SHA-256 of the repository.
func (d Dir) Mirrors() string { return filepath.Join(string(d), "mirrors") }

// Work holds the workspaces.
func (d Dir) Work() string { return filepath.Join(string(d), "work") }

// Logs holds the logs of commands run outside a results directory, such as validate's.
func (d Dir) Logs() string { return filepath.Join(string(d), "logs") }

// ValidatedFile is the file `validate` records valid task hashes in.
func (d Dir) ValidatedFile() string { return filepath.Join(string(d), "validated.json") }

// Ensure creates the directories every command writes into.
func (d Dir) Ensure() error {
	if d == "" {
		return errors.New("the cache directory is not set: pass --cache-dir")
	}
	for _, p := range []string{d.Mirrors(), d.Work(), d.Logs()} {
		if err := os.MkdirAll(p, 0o755); err != nil {
			return err
		}
	}
	return nil
}

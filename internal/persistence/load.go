package persistence

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"orchestrator/internal/apperr"
	"orchestrator/internal/domain"
)

// minRunIDPrefix is the shortest prefix ResolveRunID accepts. Shorter prefixes would too
// easily match a run the user did not mean.
const minRunIDPrefix = 8

// ResolveRunID turns a full run ID, or a unique prefix of at least minRunIDPrefix
// characters, into the ID of an existing run (specs/resume/SPEC.md §6). The input is
// lowercased and may only contain hex digits and dashes, so it can never name a path
// outside the runs directory.
func (s *Store) ResolveRunID(idOrPrefix string) (string, error) {
	in := strings.ToLower(strings.TrimSpace(idOrPrefix))
	if in == "" || strings.Trim(in, "0123456789abcdef-") != "" {
		return "", &apperr.RunStateError{Kind: apperr.RunNotFound, RunID: idOrPrefix, Msg: "not a run ID"}
	}

	if isRunID(in) {
		if _, err := os.Stat(filepath.Join(s.runDir(in), "run.json")); err != nil {
			return "", s.notFound(in)
		}
		return in, nil
	}
	if len(in) < minRunIDPrefix {
		return "", &apperr.RunStateError{Kind: apperr.RunNotFound, RunID: in,
			Msg: fmt.Sprintf("a run ID prefix needs at least %d characters", minRunIDPrefix)}
	}

	entries, err := os.ReadDir(s.baseDir)
	if err != nil && !errors.Is(err, fs.ErrNotExist) {
		return "", fmt.Errorf("list runs: %w", err)
	}
	var matches []string
	for _, e := range entries {
		if e.IsDir() && isRunID(e.Name()) && strings.HasPrefix(e.Name(), in) {
			matches = append(matches, e.Name())
		}
	}
	switch len(matches) {
	case 0:
		return "", s.notFound(in)
	case 1:
		return matches[0], nil
	}
	sort.Strings(matches)
	return "", &apperr.RunStateError{Kind: apperr.RunAmbiguous, RunID: in,
		Msg: "the prefix matches several runs:\n  " + strings.Join(matches, "\n  ")}
}

func (s *Store) notFound(in string) error {
	return &apperr.RunStateError{Kind: apperr.RunNotFound, RunID: in, Msg: "no such run under " + s.baseDir}
}

// LoadRun reads run.json of an existing run and validates its structure strictly: unknown
// fields, unknown enum values and inconsistent executions are refused, never repaired.
// The run directory lives in the worktree and a model's step can write there, so what is
// read back is input, not trusted state (specs/resume/SPEC.md §6.3). Whether the workflow
// matches the decision is checked by workflow.Verify, not here.
func (s *Store) LoadRun(runID string) (*domain.Run, error) {
	if !isRunID(runID) {
		return nil, &apperr.RunStateError{Kind: apperr.RunNotFound, RunID: runID, Msg: "not a run ID"}
	}
	data, err := os.ReadFile(filepath.Join(s.runDir(runID), "run.json"))
	if errors.Is(err, fs.ErrNotExist) {
		return nil, s.notFound(runID)
	}
	if err != nil {
		return nil, &apperr.RunStateError{Kind: apperr.RunCorrupt, RunID: runID, Msg: "cannot read run.json", Err: err}
	}

	var run domain.Run
	dec := json.NewDecoder(bytes.NewReader(data))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&run); err != nil {
		return nil, &apperr.RunStateError{Kind: apperr.RunCorrupt, RunID: runID, Msg: "run.json is not a valid run", Err: err}
	}
	if _, err := dec.Token(); !errors.Is(err, io.EOF) {
		return nil, &apperr.RunStateError{Kind: apperr.RunCorrupt, RunID: runID, Msg: "run.json has data after the run object"}
	}
	if err := validateRun(&run, runID); err != nil {
		return nil, &apperr.RunStateError{Kind: apperr.RunCorrupt, RunID: runID, Msg: "run.json: " + err.Error()}
	}
	return &run, nil
}

// isRunID reports whether name has the shape newUUIDv4 produces: lowercase hex in
// 8-4-4-4-12 groups.
func isRunID(name string) bool {
	if len(name) != 36 {
		return false
	}
	for i, c := range name {
		switch i {
		case 8, 13, 18, 23:
			if c != '-' {
				return false
			}
		default:
			if !('0' <= c && c <= '9' || 'a' <= c && c <= 'f') {
				return false
			}
		}
	}
	return true
}

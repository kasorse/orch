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
	"time"

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

	return decodeRun(data, runID)
}

// decodeRun strictly decodes and validates the content of a run.json. An empty wantID
// accepts the run's own id (a run found by walking a tree, whose directory is not named
// after it); anything else must equal it.
func decodeRun(data []byte, wantID string) (*domain.Run, error) {
	var run domain.Run
	dec := json.NewDecoder(bytes.NewReader(data))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&run); err != nil {
		return nil, &apperr.RunStateError{Kind: apperr.RunCorrupt, RunID: wantID, Msg: "run.json is not a valid run", Err: err}
	}
	if _, err := dec.Token(); !errors.Is(err, io.EOF) {
		return nil, &apperr.RunStateError{Kind: apperr.RunCorrupt, RunID: wantID, Msg: "run.json has data after the run object"}
	}
	if wantID == "" {
		if run.ID == "" {
			return nil, &apperr.RunStateError{Kind: apperr.RunCorrupt, Msg: "run.json: id is empty"}
		}
		wantID = run.ID
	}
	if err := validateRun(&run, wantID); err != nil {
		return nil, &apperr.RunStateError{Kind: apperr.RunCorrupt, RunID: wantID, Msg: "run.json: " + err.Error()}
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

// RunIDs lists the IDs of every run directory under the base directory, sorted. A missing
// base directory is no runs.
func (s *Store) RunIDs() ([]string, error) {
	entries, err := os.ReadDir(s.baseDir)
	if errors.Is(err, fs.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("list runs: %w", err)
	}
	var ids []string
	for _, e := range entries {
		if e.IsDir() && isRunID(e.Name()) {
			ids = append(ids, e.Name())
		}
	}
	sort.Strings(ids)
	return ids, nil
}

// LoadDecision reads decision.json of a run, strictly as LoadRun reads run.json, and
// returns it with the time it was written. A run that never got as far as a decision
// has none: RunStateError not_found.
func (s *Store) LoadDecision(runID string) (domain.DecisionTrace, time.Time, error) {
	if !isRunID(runID) {
		return domain.DecisionTrace{}, time.Time{}, &apperr.RunStateError{Kind: apperr.RunNotFound, RunID: runID, Msg: "not a run ID"}
	}
	path := filepath.Join(s.runDir(runID), "decision.json")
	data, err := os.ReadFile(path)
	if errors.Is(err, fs.ErrNotExist) {
		return domain.DecisionTrace{}, time.Time{}, &apperr.RunStateError{Kind: apperr.RunNotFound, RunID: runID, Msg: "the run has no decision.json"}
	}
	if err != nil {
		return domain.DecisionTrace{}, time.Time{}, &apperr.RunStateError{Kind: apperr.RunCorrupt, RunID: runID, Msg: "cannot read decision.json", Err: err}
	}
	info, err := os.Stat(path)
	if err != nil {
		return domain.DecisionTrace{}, time.Time{}, &apperr.RunStateError{Kind: apperr.RunCorrupt, RunID: runID, Msg: "cannot stat decision.json", Err: err}
	}

	trace, err := decodeDecision(data, runID)
	if err != nil {
		return domain.DecisionTrace{}, time.Time{}, err
	}
	return trace, info.ModTime(), nil
}

// decodeDecision strictly decodes and validates the content of a decision.json.
func decodeDecision(data []byte, runID string) (domain.DecisionTrace, error) {
	var trace domain.DecisionTrace
	dec := json.NewDecoder(bytes.NewReader(data))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&trace); err != nil {
		return domain.DecisionTrace{}, &apperr.RunStateError{Kind: apperr.RunCorrupt, RunID: runID, Msg: "decision.json is not a valid decision trace", Err: err}
	}
	if _, err := dec.Token(); !errors.Is(err, io.EOF) {
		return domain.DecisionTrace{}, &apperr.RunStateError{Kind: apperr.RunCorrupt, RunID: runID, Msg: "decision.json has data after the trace object"}
	}
	if err := validateDecisionUsage(trace.Usage); err != nil {
		return domain.DecisionTrace{}, &apperr.RunStateError{Kind: apperr.RunCorrupt, RunID: runID, Msg: "decision.json is not a valid decision trace", Err: err}
	}
	return trace, nil
}

// FoundRun is a run LoadRunDirs found under a tree.
type FoundRun struct {
	// RunID is the id recorded in run.json, not the directory's name.
	RunID string
	Dir   string
	Run   domain.Run
	// Trace is decision.json, nil for a run that never got as far as a decision.
	Trace *domain.DecisionTrace
	// At is when decision.json was written; zero without a trace.
	At time.Time
}

// UnreadableDir is a run directory whose run.json or decision.json could not be read.
type UnreadableDir struct {
	Dir string
	Err error
}

// LoadRunDirs finds the runs under root: every directory holding a run.json is a run, at any
// depth (the bench layout runs/<task>/<config>/rep<N>/ as well as the flat runs directory),
// and is not searched further, since what lies inside a run directory is the run's own
// artifacts. Symlinks are not followed. A run that cannot be read is returned in the second
// list and does not stop the walk. A missing root is no runs. Both lists are sorted by
// directory.
func LoadRunDirs(root string) ([]FoundRun, []UnreadableDir, error) {
	info, err := os.Stat(root)
	if errors.Is(err, fs.ErrNotExist) {
		return nil, nil, nil
	}
	if err != nil {
		return nil, nil, fmt.Errorf("list runs: %w", err)
	}
	if !info.IsDir() {
		return nil, nil, fmt.Errorf("list runs: %s is not a directory", root)
	}
	// WalkDir does not follow a symlink, the root included: the root is the one the user named.
	if resolved, err := filepath.EvalSymlinks(root); err == nil {
		root = resolved
	}

	var found []FoundRun
	var bad []UnreadableDir
	err = filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			if d != nil && d.IsDir() {
				bad = append(bad, UnreadableDir{Dir: path, Err: err})
				return fs.SkipDir
			}
			return nil
		}
		if !d.IsDir() {
			return nil
		}
		if fi, err := os.Lstat(filepath.Join(path, "run.json")); err != nil || !fi.Mode().IsRegular() {
			return nil
		}
		run, err := loadRunDir(path)
		if err != nil {
			bad = append(bad, UnreadableDir{Dir: path, Err: err})
		} else {
			found = append(found, run)
		}
		return fs.SkipDir
	})
	if err != nil {
		return nil, nil, fmt.Errorf("list runs: %w", err)
	}
	sort.Slice(found, func(i, j int) bool { return found[i].Dir < found[j].Dir })
	sort.Slice(bad, func(i, j int) bool { return bad[i].Dir < bad[j].Dir })
	return found, bad, nil
}

// loadRunDir reads the run and its decision from one directory. A directory named like a
// run ID must hold that run, as LoadRun requires.
func loadRunDir(dir string) (FoundRun, error) {
	wantID := ""
	if base := filepath.Base(dir); isRunID(base) {
		wantID = base
	}
	data, err := os.ReadFile(filepath.Join(dir, "run.json"))
	if err != nil {
		return FoundRun{}, &apperr.RunStateError{Kind: apperr.RunCorrupt, RunID: wantID, Msg: "cannot read run.json", Err: err}
	}
	run, err := decodeRun(data, wantID)
	if err != nil {
		return FoundRun{}, err
	}
	found := FoundRun{RunID: run.ID, Dir: dir, Run: *run}

	path := filepath.Join(dir, "decision.json")
	data, err = os.ReadFile(path)
	if errors.Is(err, fs.ErrNotExist) {
		return found, nil
	}
	if err != nil {
		return FoundRun{}, &apperr.RunStateError{Kind: apperr.RunCorrupt, RunID: run.ID, Msg: "cannot read decision.json", Err: err}
	}
	trace, err := decodeDecision(data, run.ID)
	if err != nil {
		return FoundRun{}, err
	}
	found.Trace = &trace
	if info, err := os.Stat(path); err == nil {
		found.At = info.ModTime()
	}
	return found, nil
}

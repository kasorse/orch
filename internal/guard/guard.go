// Package guard checks what an implement call - or a test run, which executes code a model
// wrote - changed in the protected state of a repository. It lives apart from package action
// so the in-session verify server (internal/verify) can run the same check without importing
// the dispatcher.
package guard

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"sort"

	"orchestrator/internal/domain"
	"orchestrator/internal/repository"
)

// Guard checks what an implement call - or the test command, which runs code a model
// wrote - changed, before anything runs the result (specs/permissions/SPEC.md §7.3). It checks the outcome, not the CLI, so it holds for
// any provider regardless of that CLI's own protected-path rules.
type Guard interface {
	// Snapshot records the protected state before an implement call.
	Snapshot(ctx context.Context) (State, error)
	// Changed returns what differs from before: protected paths the call changed, and
	// ".git (config or hooks)" when the git fingerprint moved. Empty means no violation.
	Changed(ctx context.Context, before State) ([]string, error)
}

// State is an opaque snapshot taken by a Guard. It serializes to JSON so a run can
// keep its run-start state as the resume baseline (specs/resume/SPEC.md §9.1); only the
// guard reads its fields.
type State struct {
	// patterns are the protected patterns the snapshot was taken with, so a resume
	// compares exactly what was protected when the run started.
	patterns       []string
	gitFingerprint string
	// protected maps each protected path that differs from HEAD or is ignored to a hash
	// of its content, so a difference that was already there before the call and left
	// untouched is not a violation (an ignored file, or any change when
	// security.require_clean_worktree is false).
	protected map[string]string
}

// Patterns returns a copy of the protected patterns the snapshot was taken with.
func (s State) Patterns() []string { return append([]string(nil), s.patterns...) }

// GitViolation names a change to .git/config or the hooks in the list Changed returns.
const GitViolation = ".git (config or hooks)"

// RepoGuard is the Guard over a real repository.
type RepoGuard struct {
	Repo     *repository.Repository
	Patterns []string
}

// NewRepoGuard guards domain.DefaultProtectedPaths plus extra (security.protected_paths
// and the config file when it lives in the worktree).
func NewRepoGuard(repo *repository.Repository, extra []string) *RepoGuard {
	patterns := append(append([]string(nil), domain.DefaultProtectedPaths...), extra...)
	return &RepoGuard{Repo: repo, Patterns: patterns}
}

func (g *RepoGuard) Snapshot(ctx context.Context) (State, error) {
	fp, err := g.Repo.GitFingerprint(ctx)
	if err != nil {
		return State{}, err
	}
	return g.snapshotWith(ctx, fp)
}

func (g *RepoGuard) snapshotWith(ctx context.Context, fp string) (State, error) {
	top, err := g.Repo.TopLevel(ctx)
	if err != nil {
		return State{}, err
	}
	changed, err := g.Repo.ChangedOrIgnoredPaths(ctx, g.pathspecs())
	if err != nil {
		return State{}, err
	}
	state := State{patterns: g.Patterns, gitFingerprint: fp, protected: map[string]string{}}
	for _, p := range changed {
		if g.protects(p) {
			state.protected[p] = contentHash(filepath.Join(top, filepath.FromSlash(p)))
		}
	}
	return state, nil
}

func (g *RepoGuard) Changed(ctx context.Context, before State) ([]string, error) {
	// The fingerprint is compared before anything runs git status: a planted
	// core.fsmonitor would run on that very status call. rev-parse, which the fingerprint
	// uses, does not run it.
	fp, err := g.Repo.GitFingerprint(ctx)
	if err != nil {
		return nil, err
	}
	if fp != before.gitFingerprint {
		return []string{GitViolation}, nil
	}
	after, err := g.snapshotWith(ctx, fp)
	if err != nil {
		return nil, err
	}
	var out []string
	seen := map[string]bool{}
	for p, h := range after.protected {
		seen[p] = true
		if before.protected[p] != h {
			out = append(out, p)
		}
	}
	// A protected file whose earlier change was reverted by the call is a change too.
	for p := range before.protected {
		if !seen[p] {
			out = append(out, p)
		}
	}
	sort.Strings(out)
	return out, nil
}

type stateJSON struct {
	Patterns       []string          `json:"patterns"`
	GitFingerprint string            `json:"git_fingerprint"`
	Protected      map[string]string `json:"protected"`
}

func (s State) MarshalJSON() ([]byte, error) {
	return json.Marshal(stateJSON{Patterns: s.patterns, GitFingerprint: s.gitFingerprint, Protected: s.protected})
}

func (s *State) UnmarshalJSON(data []byte) error {
	var j stateJSON
	if err := decodeStrict(data, &j); err != nil {
		return err
	}
	if j.GitFingerprint == "" || len(j.Patterns) == 0 || j.Protected == nil {
		return errors.New("incomplete guard state")
	}
	*s = State{patterns: j.Patterns, gitFingerprint: j.GitFingerprint, protected: j.Protected}
	return nil
}

// CheckBaseline compares the repository with a run's recorded run-start baseline, before a
// resume runs anything (specs/resume/SPEC.md §9.1). It guards the baseline's own patterns.
// The git fingerprint is compared first, before any git status: a planted core.fsmonitor
// would run on that very call. A nil error with an empty list means nothing protected
// changed; otherwise the list names what did, as Changed does.
func CheckBaseline(ctx context.Context, repo *repository.Repository, baseline json.RawMessage) ([]string, error) {
	var before State
	if err := json.Unmarshal(baseline, &before); err != nil {
		return nil, fmt.Errorf("invalid resume baseline: %w", err)
	}
	g := &RepoGuard{Repo: repo, Patterns: before.patterns}
	return g.Changed(ctx, before)
}

// pathspecs renders the patterns as git glob pathspecs anchored at the top and matching
// at any depth, like domain.MatchProtected; protects re-checks every path git returns.
func (g *RepoGuard) pathspecs() []string {
	specs := make([]string, len(g.Patterns))
	for i, p := range g.Patterns {
		specs[i] = ":(top,glob)**/" + p
	}
	return specs
}

func (g *RepoGuard) protects(p string) bool {
	for _, pattern := range g.Patterns {
		if domain.MatchProtected(pattern, p) {
			return true
		}
	}
	return false
}

// contentHash identifies a file's content; a deleted or unreadable file gets a marker.
func contentHash(path string) string {
	data, err := os.ReadFile(path)
	switch {
	case errors.Is(err, fs.ErrNotExist):
		return "missing"
	case err != nil:
		return "unreadable"
	}
	sum := sha256.Sum256(data)
	return hex.EncodeToString(sum[:])
}

func decodeStrict(data []byte, v any) error {
	dec := json.NewDecoder(bytes.NewReader(data))
	dec.DisallowUnknownFields()
	if err := dec.Decode(v); err != nil {
		return fmt.Errorf("invalid guard state: %w", err)
	}
	if _, err := dec.Token(); !errors.Is(err, io.EOF) {
		return errors.New("invalid guard state: data after the object")
	}
	return nil
}

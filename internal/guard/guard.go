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
	// Changed returns what differs from before: protected paths the call changed,
	// ".git (config or hooks)" when the git fingerprint moved, and ".git (worktree pointer)"
	// when a worktree's .git file no longer names its admin directory. Empty means no
	// violation.
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
	// gitPointer is the admin directory the worktree's .git file must name, "" in place
	// (specs/worktree/SPEC.md §8.3). A resume baseline carries it, so CheckBaseline checks
	// the pointer without the caller passing it.
	gitPointer string
}

// Patterns returns a copy of the protected patterns the snapshot was taken with.
func (s State) Patterns() []string { return append([]string(nil), s.patterns...) }

// GitPointer returns the admin directory the snapshot expects the .git file to name, "" in
// place.
func (s State) GitPointer() string { return s.gitPointer }

// GitViolation names a change to .git/config or the hooks in the list Changed returns.
const GitViolation = ".git (config or hooks)"

// PointerViolation names a linked worktree's .git file that no longer names its admin
// directory (or cannot be read) in the list Changed returns.
const PointerViolation = ".git (worktree pointer)"

// RepoGuard is the Guard over a real repository.
type RepoGuard struct {
	Repo     *repository.Repository
	Patterns []string
	// GitPointer is the admin directory Repo's .git file must name when Repo is a run's
	// linked worktree: the one AddWorktree returned, never what the file says now. Empty in
	// place.
	GitPointer string
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
	state := State{patterns: g.Patterns, gitFingerprint: fp, protected: map[string]string{}, gitPointer: g.GitPointer}
	for _, p := range changed {
		if g.protects(p) {
			state.protected[p] = contentHash(filepath.Join(top, filepath.FromSlash(p)))
		}
	}
	return state, nil
}

func (g *RepoGuard) Changed(ctx context.Context, before State) ([]string, error) {
	// In a linked worktree the .git file decides which git directory every git call below
	// reads, so it is read in Go, before any of them (specs/worktree/SPEC.md §8.3).
	if g.pointerChanged(before) {
		return []string{PointerViolation}, nil
	}
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

// pointerChanged reports whether the .git file no longer names the expected admin directory:
// the guard's own GitPointer, else the one the snapshot was taken with. A guard and a snapshot
// that disagree are a change too. Unreadable counts as changed. In place there is nothing to
// compare.
func (g *RepoGuard) pointerChanged(before State) bool {
	want := g.GitPointer
	if want == "" {
		want = before.gitPointer
	} else if before.gitPointer != "" && before.gitPointer != want {
		return true
	}
	if want == "" {
		return false
	}
	got, err := repository.ReadGitPointer(g.Repo.Dir)
	return err != nil || got != want
}

type stateJSON struct {
	Patterns       []string          `json:"patterns"`
	GitFingerprint string            `json:"git_fingerprint"`
	Protected      map[string]string `json:"protected"`
	// Pointer is absent in place and in a baseline recorded before worktree runs existed.
	Pointer string `json:"pointer,omitempty"`
}

func (s State) MarshalJSON() ([]byte, error) {
	return json.Marshal(stateJSON{Patterns: s.patterns, GitFingerprint: s.gitFingerprint, Protected: s.protected, Pointer: s.gitPointer})
}

func (s *State) UnmarshalJSON(data []byte) error {
	var j stateJSON
	if err := decodeStrict(data, &j); err != nil {
		return err
	}
	if j.GitFingerprint == "" || len(j.Patterns) == 0 || j.Protected == nil {
		return errors.New("incomplete guard state")
	}
	if j.Pointer != "" && !filepath.IsAbs(j.Pointer) {
		return errors.New("invalid guard state: pointer is not an absolute path")
	}
	*s = State{patterns: j.Patterns, gitFingerprint: j.GitFingerprint, protected: j.Protected, gitPointer: j.Pointer}
	return nil
}

// CheckBaseline compares the repository with a run's recorded run-start baseline, before a
// resume runs anything (specs/resume/SPEC.md §9.1). It guards the baseline's own patterns,
// and in a worktree the .git pointer the baseline names. The pointer and then the git
// fingerprint are compared first, before any git status: a planted core.fsmonitor would run
// on that very call. A nil error with an empty list means nothing protected changed;
// otherwise the list names what did, as Changed does.
func CheckBaseline(ctx context.Context, repo *repository.Repository, baseline json.RawMessage) ([]string, error) {
	var before State
	if err := json.Unmarshal(baseline, &before); err != nil {
		return nil, fmt.Errorf("invalid resume baseline: %w", err)
	}
	g := &RepoGuard{Repo: repo, Patterns: before.patterns, GitPointer: before.gitPointer}
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

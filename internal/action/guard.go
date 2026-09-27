package action

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"sort"

	"orchestrator/internal/domain"
	"orchestrator/internal/repository"
)

// Guard checks what an implement call changed before anything runs the result
// (specs/permissions/SPEC.md §7.3). It checks the outcome, not the CLI, so it holds for
// any provider regardless of that CLI's own protected-path rules.
type Guard interface {
	// Snapshot records the protected state before an implement call.
	Snapshot(ctx context.Context) (GuardState, error)
	// Changed returns what differs from before: protected paths the call changed, and
	// ".git (config or hooks)" when the git fingerprint moved. Empty means no violation.
	Changed(ctx context.Context, before GuardState) ([]string, error)
}

// GuardState is an opaque snapshot taken by a Guard.
type GuardState struct {
	gitFingerprint string
	// protected maps each protected path that differs from HEAD or is ignored to a hash
	// of its content, so a difference that was already there before the call and left
	// untouched is not a violation (an ignored file, or any change when
	// security.require_clean_worktree is false).
	protected map[string]string
}

// gitViolation names a change to .git/config or the hooks in the list Changed returns.
const gitViolation = ".git (config or hooks)"

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

func (g *RepoGuard) Snapshot(ctx context.Context) (GuardState, error) {
	fp, err := g.Repo.GitFingerprint(ctx)
	if err != nil {
		return GuardState{}, err
	}
	top, err := g.Repo.TopLevel(ctx)
	if err != nil {
		return GuardState{}, err
	}
	changed, err := g.Repo.ChangedOrIgnoredPaths(ctx, g.pathspecs())
	if err != nil {
		return GuardState{}, err
	}
	state := GuardState{gitFingerprint: fp, protected: map[string]string{}}
	for _, p := range changed {
		if g.protects(p) {
			state.protected[p] = contentHash(filepath.Join(top, filepath.FromSlash(p)))
		}
	}
	return state, nil
}

func (g *RepoGuard) Changed(ctx context.Context, before GuardState) ([]string, error) {
	after, err := g.Snapshot(ctx)
	if err != nil {
		return nil, err
	}
	var out []string
	if after.gitFingerprint != before.gitFingerprint {
		out = append(out, gitViolation)
	}
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

package persistence

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"orchestrator/internal/domain"
)

// StateDir is the base of the orchestrator's state outside any repository: $XDG_STATE_HOME when
// it is set to an absolute path, else ~/.local/state.
func StateDir() (string, error) {
	if dir := os.Getenv("XDG_STATE_HOME"); filepath.IsAbs(dir) {
		return dir, nil
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return "", fmt.Errorf("cannot find the state directory: %w", err)
	}
	return filepath.Join(home, ".local", "state"), nil
}

// WorktreePath derives where a run's worktree lives (specs/worktree/SPEC.md §8.1):
// <stateDir>/orchestrator/worktrees/<repo-id>/<runID>, with <repo-id> the first 16 hex digits of
// the sha256 of the common git directory's absolute, symlink-resolved path. The path is derived,
// never read back from run.json.
func WorktreePath(stateDir, commonDir, runID string) string {
	resolved := commonDir
	if abs, err := filepath.Abs(resolved); err == nil {
		resolved = abs
	}
	if real, err := filepath.EvalSymlinks(resolved); err == nil {
		resolved = real
	}
	sum := sha256.Sum256([]byte(filepath.Clean(resolved)))
	return filepath.Join(stateDir, "orchestrator", "worktrees", hex.EncodeToString(sum[:])[:16], runID)
}

// validateWorktree applies the checks that need no repository (specs/worktree/SPEC.md §8.2).
// The path and the admin directory are checked by the resume, which has the repository.
func validateWorktree(run *domain.Run) error {
	switch run.Options.Isolation {
	case "", domain.IsolationInplace, domain.IsolationWorktree:
	default:
		return fmt.Errorf("options.isolation %q is not inplace or worktree", run.Options.Isolation)
	}
	// The record outlives a worktree that git then failed to add.
	if bn := run.BranchNaming; bn != nil {
		if err := bn.Validate(); err != nil {
			return fmt.Errorf("branch_naming: %w", err)
		}
		if run.Options.IsolationMode() != domain.IsolationWorktree {
			return fmt.Errorf("branch_naming recorded on a run with options.isolation %q", run.Options.IsolationMode())
		}
	}
	w := run.Worktree
	if w == nil {
		return nil
	}
	if run.Options.IsolationMode() != domain.IsolationWorktree {
		return fmt.Errorf("worktree recorded on a run with options.isolation %q", run.Options.IsolationMode())
	}
	// The branch was named when the worktree was created (specs/delivery/SPEC.md §4.3): it only has
	// to be a name the rules accept. A story task's may also be the fallback its task input gave,
	// recorded in task.source (validated with it) (§9.1). A review run's worktree is detached: it
	// has no branch.
	defaultBranch := domain.WorktreeBranch(run.ID)
	if run.ReviewTarget != nil {
		if w.Branch != "" {
			return fmt.Errorf("worktree.branch is %q on a review run, want none", w.Branch)
		}
	} else if src := run.Task.Source; src != nil && src.Kind == domain.TaskSourceStory {
		defaultBranch = src.Branch
		if err := domain.CheckBranchName(w.Branch); err != nil && w.Branch != src.Branch {
			return fmt.Errorf("worktree.branch %q is not the story task's fallback %q: %w", w.Branch, src.Branch, err)
		}
	} else if err := domain.CheckBranchName(w.Branch); err != nil {
		return fmt.Errorf("worktree.branch %q: %w", w.Branch, err)
	}
	if bn := run.BranchNaming; bn != nil {
		if bn.Source == domain.BranchSourceDefault && w.Branch != defaultBranch {
			return fmt.Errorf("branch_naming.source is default, but worktree.branch is %q, not %q", w.Branch, defaultBranch)
		}
		if bn.Source == domain.BranchSourceInstructions && w.Branch != bn.Proposed {
			return fmt.Errorf("branch_naming.source is instructions, but worktree.branch is %q, not the proposed %q", w.Branch, bn.Proposed)
		}
	}
	if !isCommitSHA(w.BaseRef) {
		return fmt.Errorf("worktree.base_ref %q is not a full commit SHA", w.BaseRef)
	}
	// A review run's worktree is detached at the head it reviews (specs/delivery/SPEC.md §12.2).
	if t := run.ReviewTarget; t != nil && (w.BaseRef != t.Head || w.BaseBranch != "") {
		return fmt.Errorf("worktree is at %q on branch %q, but review_target.head is %q", w.BaseRef, w.BaseBranch, t.Head)
	}
	if src := run.Task.Source; src != nil && src.Kind == domain.TaskSourceStory {
		if w.BaseRef != src.BaseRef {
			return fmt.Errorf("worktree.base_ref is %q, but task.source.base_ref is %q", w.BaseRef, src.BaseRef)
		}
		if w.BaseBranch != src.BaseBranch {
			return fmt.Errorf("worktree.base_branch is %q, but task.source.base_branch is %q", w.BaseBranch, src.BaseBranch)
		}
	}
	if w.BaseBranch != "" && !validBranchName(w.BaseBranch) {
		return fmt.Errorf("worktree.base_branch %q is not a valid branch name", w.BaseBranch)
	}
	if len(w.Conflicts) > 0 && run.Status != domain.RunCompleted {
		return fmt.Errorf("worktree.conflicts recorded on a %s run; only a completed run has them", run.Status)
	}
	if w.Removed && run.Status == domain.RunRunning {
		return fmt.Errorf("worktree.removed on a running run")
	}
	return nil
}

// validateMR checks run.mr (specs/delivery/SPEC.md §13): its own rules, and that it belongs to a
// completed worktree run, with a commit once anything was pushed or about to be.
func validateMR(run *domain.Run) error {
	m := run.MR
	if m == nil {
		return nil
	}
	if err := m.Validate(); err != nil {
		return fmt.Errorf("mr: %w", err)
	}
	switch {
	case run.Status != domain.RunCompleted:
		return fmt.Errorf("mr recorded on a %s run; only a completed run has one", run.Status)
	case run.Worktree == nil:
		return fmt.Errorf("mr recorded on a run without a worktree")
	case m.State != "" && run.Commit == "":
		return fmt.Errorf("mr.state %s on a run without a commit", m.State)
	case m.Source != "" && m.Source != run.Worktree.Branch:
		return fmt.Errorf("mr.source is %q, but worktree.branch is %q", m.Source, run.Worktree.Branch)
	}
	return nil
}

// validBranchName refuses what git would not accept as a branch name, and what could be read as
// an option or a revision expression.
func validBranchName(name string) bool {
	if name == "" || strings.HasPrefix(name, "-") || strings.HasPrefix(name, "/") || strings.HasSuffix(name, "/") ||
		strings.HasSuffix(name, ".") || strings.HasSuffix(name, ".lock") ||
		strings.Contains(name, "..") || strings.Contains(name, "@{") || strings.Contains(name, "//") {
		return false
	}
	if strings.ContainsAny(name, " ~^:?*[\\\x7f") {
		return false
	}
	for _, c := range name {
		if c < 0x20 {
			return false
		}
	}
	return true
}

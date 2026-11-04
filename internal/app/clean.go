package app

import (
	"context"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/kasorse/orch/internal/apperr"
	"github.com/kasorse/orch/internal/execution"
	"github.com/kasorse/orch/internal/persistence"
	"github.com/kasorse/orch/internal/repository"
)

const runsCleanUsage = "usage: orchestrator runs clean [--older-than <N>d|<duration> | --unreadable] [--dry-run]"

// defaultCleanAge is how old a run must be for `runs clean` to touch its worktree.
const defaultCleanAge = 7 * 24 * time.Hour

type cleanArgs struct {
	olderThan time.Duration
	dryRun    bool
	// unreadable removes the runs this version cannot read instead of old worktrees.
	unreadable bool
}

// parseAge reads "7d" (whole days) or any time.ParseDuration value; it must be positive.
func parseAge(v string) (time.Duration, bool) {
	if days, ok := strings.CutSuffix(v, "d"); ok {
		n, err := strconv.Atoi(days)
		if err != nil || n < 1 || n > 36500 {
			return 0, false
		}
		return time.Duration(n) * 24 * time.Hour, true
	}
	d, err := time.ParseDuration(v)
	return d, err == nil && d > 0
}

// runsCleanArgs parses what follows `runs clean`: --older-than <age> (or =<age>), default 7d,
// and --dry-run, each at most once.
func runsCleanArgs(args []string) (cleanArgs, error) {
	bad := &apperr.ConfigError{Msg: runsCleanUsage}
	out := cleanArgs{olderThan: defaultCleanAge}
	givenAge := false
	for i := 0; i < len(args); i++ {
		switch v, ok := strings.CutPrefix(args[i], "--older-than="); {
		case args[i] == "--dry-run" && !out.dryRun:
			out.dryRun = true
		case args[i] == "--unreadable" && !out.unreadable && !givenAge:
			out.unreadable = true
		case (ok || args[i] == "--older-than" && i+1 < len(args)) && !out.unreadable:
			if !ok {
				i++
				v = args[i]
			}
			age, valid := parseAge(v)
			if givenAge || !valid {
				return cleanArgs{}, bad
			}
			out.olderThan, givenAge = age, true
		default:
			return cleanArgs{}, bad
		}
	}
	return out, nil
}

// runsClean removes the worktrees of old runs of the current repository (specs/worktree/SPEC.md
// §14).
func runsClean(ctx context.Context, stdout io.Writer, args cleanArgs) error {
	cwd, err := os.Getwd()
	if err != nil {
		return &apperr.RepositoryError{Msg: "failed to determine working directory", Err: err}
	}
	repo, err := repository.Open(ctx, cwd, execution.NewOSProcessExecutor())
	if err != nil {
		return err
	}
	if args.unreadable {
		return cleanUnreadable(ctx, stdout, persistence.NewStore(runsBaseDir), repo, args.dryRun)
	}
	return cleanWorktrees(ctx, stdout, persistence.NewStore(runsBaseDir), repo, args, time.Now())
}

// cleanUnreadable removes the run directories whose run.json this version reads but cannot decode
// or validate (persistence.Store.UnreadableRun: a run started before a layout change, a damaged
// file), whatever their age (TODO.md S8). A run another process holds locked is skipped, as by
// cleanWorktrees. A worktree at the run's derived path is removed first with RemoveWorktree, never
// --force: git refusing it keeps the worktree and the run. Branches are never deleted. With dryRun
// nothing is changed.
func cleanUnreadable(ctx context.Context, stdout io.Writer, store *persistence.Store, repo *repository.Repository, dryRun bool) error {
	ids, err := store.RunIDs()
	if err != nil {
		return err
	}
	common, err := repo.CommonDir(ctx)
	if err != nil {
		return err
	}
	state, err := persistence.StateDir()
	if err != nil {
		return err
	}
	removed, kept, worktrees := 0, 0, 0
	for _, id := range ids {
		if store.UnreadableRun(id) == nil {
			continue
		}
		unlock, err := store.Lock(id)
		if err != nil {
			var rse *apperr.RunStateError
			if errors.As(err, &rse) && rse.Kind == apperr.RunInUse {
				fmt.Fprintf(stdout, "skipped %s: the run is in use\n", id)
				continue
			}
			return err
		}
		// The lock was free: the run may have been written meanwhile.
		why := store.UnreadableRun(id)
		if why == nil {
			unlock()
			continue
		}
		dir, wt := store.RunDir(id), persistence.WorktreePath(state, common, id)
		_, statErr := os.Lstat(wt)
		hasWorktree := statErr == nil
		if dryRun {
			unlock()
			if hasWorktree {
				fmt.Fprintf(stdout, "would remove %s and its worktree %s: %v\n", dir, wt, why)
			} else {
				fmt.Fprintf(stdout, "would remove %s: %v\n", dir, why)
			}
			continue
		}
		if hasWorktree {
			if err := removeWorktree(ctx, repo, wt); err != nil {
				unlock()
				kept++
				fmt.Fprintf(stdout, "kept %s and its worktree %s: %v\n", dir, wt, err)
				continue
			}
			worktrees++
		}
		err = os.RemoveAll(dir)
		unlock()
		if err != nil {
			return &apperr.WorkflowError{Msg: "cannot remove the run directory " + dir, Err: err}
		}
		fmt.Fprintf(stdout, "removed %s: %v\n", dir, why)
		removed++
	}
	if dryRun {
		return nil
	}
	if worktrees > 0 {
		if err := pruneWorktrees(ctx, repo); err != nil {
			return err
		}
	}
	fmt.Fprintf(stdout, "%d unreadable run(s) removed, %d kept\n", removed, kept)
	return nil
}

// cleanWorktrees visits every run of the store with a worktree not yet removed that is older
// than args.olderThan (by created_at) and whose lock can be taken, and removes the worktree
// with RemoveWorktree, never --force: git refusing it (uncommitted files) lists the worktree as
// kept with git's reason. A removal is recorded in run.json; the branch is never deleted. Then
// the worktrees git no longer finds are pruned, once. With dryRun nothing is changed.
func cleanWorktrees(ctx context.Context, stdout io.Writer, store *persistence.Store, repo *repository.Repository, args cleanArgs, now time.Time) error {
	ids, err := store.RunIDs()
	if err != nil {
		return err
	}
	removed, kept := 0, 0
	for _, id := range ids {
		run, err := store.LoadRun(id)
		if err != nil {
			if store.UnreadableRun(id) != nil {
				fmt.Fprintf(stdout, "skipped %s: %v (runs clean --unreadable removes it)\n", id, err)
			} else {
				fmt.Fprintf(stdout, "skipped %s: %v\n", id, err)
			}
			continue
		}
		w := run.Worktree
		if w == nil || w.Removed || now.Sub(run.CreatedAt) < args.olderThan {
			continue
		}
		unlock, err := store.Lock(id)
		if err != nil {
			var rse *apperr.RunStateError
			if errors.As(err, &rse) && rse.Kind == apperr.RunInUse {
				fmt.Fprintf(stdout, "skipped %s: the run is in use\n", id)
				continue
			}
			return err
		}
		// The lock was free: reload what a run that just finished may have written.
		run, err = store.LoadRun(id)
		if err != nil || run.Worktree == nil || run.Worktree.Removed {
			unlock()
			continue
		}
		w = run.Worktree
		if args.dryRun {
			unlock()
			fmt.Fprintf(stdout, "would remove %s (run %s, branch %s)\n", w.Path, id, w.Branch)
			continue
		}
		gone := false
		if _, statErr := os.Lstat(w.Path); errors.Is(statErr, fs.ErrNotExist) {
			gone = true // only git's administrative data is left; the prune below drops it
		}
		if !gone {
			if err := removeWorktree(ctx, repo, w.Path); err != nil {
				unlock()
				kept++
				fmt.Fprintf(stdout, "kept %s (run %s, branch %s): %v\n", w.Path, id, w.Branch, err)
				continue
			}
		}
		w.Removed = true
		saveErr := store.SaveRun(run)
		unlock()
		if saveErr != nil {
			fmt.Fprintf(stdout, "removed %s, but run.json of %s could not record it: %v\n", w.Path, id, saveErr)
		} else {
			fmt.Fprintf(stdout, "removed %s (run %s, branch %s kept)\n", w.Path, id, w.Branch)
		}
		removed++
	}
	if args.dryRun {
		return nil
	}
	if err := pruneWorktrees(ctx, repo); err != nil {
		return err
	}
	fmt.Fprintf(stdout, "%d worktree(s) removed, %d kept\n", removed, kept)
	return nil
}

// pruneWorktrees is PruneWorktrees under the repository's worktrees lock: a prune that meets a
// worktree another process is still creating drops its half-written administrative directory.
func pruneWorktrees(ctx context.Context, repo *repository.Repository) error {
	common, err := repo.CommonDir(ctx)
	if err != nil {
		return err
	}
	state, err := persistence.StateDir()
	if err != nil {
		return err
	}
	unlock, err := persistence.LockWorktrees(filepath.Dir(persistence.WorktreePath(state, common, "lock")))
	if err != nil {
		return err
	}
	defer unlock()
	return repo.PruneWorktrees(ctx)
}

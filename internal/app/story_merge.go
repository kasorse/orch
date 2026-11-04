package app

import (
	"context"
	"fmt"
	"maps"
	"slices"

	"github.com/kasorse/orch/internal/apperr"
	"github.com/kasorse/orch/internal/logging"
	"github.com/kasorse/orch/internal/persistence"
	"github.com/kasorse/orch/internal/repository"
	"github.com/kasorse/orch/internal/story"
)

// The spec branch and the merge of a task's parents (specs/parallel-story/SPEC.md §1, §3): the
// story's own branch story/<slug>/spec in each repository, where the commits of several parents
// are merged, by Go, just before the task that needs them is dispatched.

// createSpecBranches gives every repository the story still works in its spec branch, at the
// story base commit, and records it. The first start does it right after the approval; a story
// recorded before the spec branch existed gets it here too, with a note, before anything is
// dispatched. Go never writes the operator's branch.
func (r *storyRunner) createSpecBranches(continued bool) error {
	need := map[string]bool{}
	for _, id := range r.order {
		if r.rec.Tasks[id].Status != story.TaskDone {
			need[r.byID[id].Repo] = true
		}
	}
	for _, name := range slices.Sorted(maps.Keys(need)) {
		b, ok := r.rec.Bases[name]
		if !ok || b.SpecBranch != "" {
			continue
		}
		repo, err := repository.Open(r.ctx, r.rec.Services[name], r.osExec)
		if err != nil {
			return err
		}
		spec := persistence.StorySpecBranch(r.loc.slug)
		if err := repo.UpdateRef(r.ctx, "refs/heads/"+spec, b.SHA, ""); err != nil {
			return &apperr.ConfigError{Msg: fmt.Sprintf("service %s: cannot create the spec branch %s at %s", name, spec, shortSHA(b.SHA)), Err: err}
		}
		b.SpecBranch = spec
		r.rec.Bases[name] = b
		if err := r.persist(); err != nil {
			return err
		}
		if continued {
			r.reporter.StoryNote(fmt.Sprintf("service %s: created the spec branch %s at the story base %s (this story was started without one)", name, spec, shortSHA(b.SHA)))
		}
	}
	return nil
}

// startRef is the commit a task is dispatched from (§1): the story base without parents, the
// parent's recorded commit with one, and with several the tip of the spec branch once the
// parents' commits are merged on it. When they do not merge cleanly the conflict is returned,
// and nothing is dispatched.
func (r *storyRunner) startRef(id string) (string, *persistence.StoryConflict, error) {
	t := r.byID[id]
	parents, err := story.Parents(r.spec.tasks, id)
	if err != nil {
		return "", nil, &apperr.ConfigError{Msg: err.Error()}
	}
	for _, p := range parents {
		if d := r.rec.Tasks[p]; d.Status != story.TaskDone || d.Commit == "" {
			return "", nil, &apperr.WorkflowError{Msg: fmt.Sprintf("%s starts from %s, which is not done", id, p)}
		}
	}
	switch len(parents) {
	case 0:
		return r.rec.Bases[t.Repo].SHA, nil, nil
	case 1:
		return r.rec.Tasks[parents[0]].Commit, nil, nil
	}
	return r.mergeParents(id, parents)
}

// mergeParents merges the commits of the parents of task id, in ID order, on the spec branch of
// the task's repository. Each is, in turn: already in the spec tip (nothing), a descendant of it
// (a fast-forward), or merged into a commit by merge-tree and commit-tree, which write objects
// only. The ref moves once, with its old value, so a branch that moved meanwhile is not overwritten.
func (r *storyRunner) mergeParents(id string, parents []string) (string, *persistence.StoryConflict, error) {
	t := r.byID[id]
	base := r.rec.Bases[t.Repo]
	spec := base.SpecBranch
	if spec == "" {
		return "", nil, &apperr.ConfigError{Msg: fmt.Sprintf("%s: story.json has no spec branch for service %s", id, t.Repo)}
	}
	repo, err := repository.Open(r.ctx, r.rec.Services[t.Repo], r.osExec)
	if err != nil {
		return "", nil, err
	}
	tip, exists, err := repo.BranchTip(r.ctx, spec)
	if err != nil {
		return "", nil, err
	}
	if !exists {
		return "", nil, &apperr.ConfigError{Msg: fmt.Sprintf("service %s: the spec branch %s does not exist any more", t.Repo, spec)}
	}
	if ok, err := repo.IsAncestor(r.ctx, base.SHA, tip); err != nil {
		return "", nil, err
	} else if !ok {
		return "", nil, &apperr.ConfigError{Msg: fmt.Sprintf("service %s: the spec branch %s does not descend from the story base %s any more; "+
			"reset it to a commit that does", t.Repo, spec, shortSHA(base.SHA))}
	}

	commits := make([]string, len(parents))
	for i, p := range parents {
		commits[i] = r.rec.Tasks[p].Commit
	}
	merged, files, err := mergeOnto(r.ctx, repo, tip, commits, func(i int) string {
		return fmt.Sprintf("Merge %s for %s\n\nOrchestrator-Story: %s/%s", r.rec.Tasks[parents[i]].Branch, id, r.loc.slug, id)
	}, func() error { return r.checkFingerprint(t.Repo, repo) })
	if merged != tip {
		if uerr := repo.UpdateRef(r.ctx, "refs/heads/"+spec, merged, tip); uerr != nil {
			return "", nil, uerr
		}
	}
	if err != nil {
		return "", nil, err
	}
	if len(files) > 0 {
		return "", &persistence.StoryConflict{Parents: parents, Files: persistence.ConflictFiles(files)}, nil
	}
	return merged, nil, nil
}

// mergeOnto merges commits onto tip in order: a commit already in the tip adds nothing, a
// descendant of it becomes the tip (a fast-forward), and any other is merged by merge-tree and
// commit-tree, which write objects only, with message(i); ready runs before each merge-tree. It
// moves no ref. On a conflict it returns the tip reached before it and the conflicting files.
func mergeOnto(ctx context.Context, repo *repository.Repository, tip string, commits []string, message func(int) string, ready func() error) (string, []string, error) {
	for i, c := range commits {
		if ok, err := repo.IsAncestor(ctx, c, tip); err != nil {
			return tip, nil, err
		} else if ok {
			continue
		}
		if ok, err := repo.IsAncestor(ctx, tip, c); err != nil {
			return tip, nil, err
		} else if ok {
			tip = c
			continue
		}
		if err := ready(); err != nil {
			return tip, nil, err
		}
		tree, files, err := repo.MergeTreeWrite(ctx, tip, c)
		if err != nil {
			return tip, nil, err
		}
		if len(files) > 0 {
			return tip, files, nil
		}
		commit, err := repo.CommitTree(ctx, tree, []string{tip, c}, message(i))
		if err != nil {
			return tip, nil, err
		}
		tip = commit
	}
	return tip, nil, nil
}

// checkFingerprint compares the git fingerprint of a repository with the one the pre-run checks
// took, before the first merge-tree of this story run in it: a merge driver configured since
// would make merge-tree run code nobody approved.
func (r *storyRunner) checkFingerprint(name string, repo *repository.Repository) error {
	if r.merged[name] {
		return nil
	}
	want, ok := r.fingerprints[name]
	got, err := repo.GitFingerprint(r.ctx)
	if err != nil {
		return err
	}
	if !ok || got != want {
		return &apperr.ConfigError{Msg: fmt.Sprintf("service %s: the git config or hooks of %s changed since the checks of this story run; "+
			"check what changed, then run the story again", name, r.rec.Services[name])}
	}
	if r.merged == nil {
		r.merged = map[string]bool{}
	}
	r.merged[name] = true
	return nil
}

// conflictHints are the lines that tell the operator how to merge the parents of a conflict task
// by hand on the spec branch, and how to go on.
func (r *storyRunner) conflictHints(rt persistence.StoryTask) []string {
	spec := persistence.StorySpecBranch(r.loc.slug)
	repoDir := logging.ShellWord(r.rec.Services[rt.Repo])
	hints := []string{
		"Merge the parents by hand on the spec branch:",
		fmt.Sprintf("  git -C %s worktree add <dir> %s", repoDir, spec),
		"  cd <dir>",
	}
	// One merge per parent: an octopus merge gives up on a conflict.
	for _, p := range rt.Conflict.Parents {
		hints = append(hints, "  git merge "+logging.ShellWord(r.parentBranch(p)))
	}
	return append(hints,
		"  (a merge that stops on a conflict: resolve it, git add the files, git commit)",
		fmt.Sprintf("  git -C %s worktree remove <dir>", repoDir),
		fmt.Sprintf("then: orchestrator story run %s", logging.ShellWord(r.dirArg)))
}

package app

import (
	"fmt"
	"maps"
	"path"
	"path/filepath"
	"slices"
	"strings"

	"orchestrator/internal/apperr"
	"orchestrator/internal/config"
	"orchestrator/internal/logging"
	"orchestrator/internal/persistence"
	"orchestrator/internal/repository"
	"orchestrator/internal/story"
)

// The land (specs/story-land): at the end of a completed story, a service whose config says
// delivery.push: false gets the tasks' result on the branch the story was started from, and the
// story's README and As built are committed there. Derived from git, so a later story run of the
// completed story lands what an earlier one could not, and changes nothing otherwise.

// landAll lands every service that does not push, then prints what is left to push: the base
// branch once landed, else the tasks' branches. A land that was refused makes the story need a
// person (exit 6); the record stays completed.
func (r *storyRunner) landAll() error {
	for _, id := range r.order {
		if name := r.byID[id].Repo; !r.checked(name) {
			if err := r.readDelivery(name); err != nil {
				return err
			}
		}
	}
	var refused []string
	for _, name := range slices.Sorted(maps.Keys(r.noPush)) {
		if !r.noPush[name] {
			continue
		}
		branches := r.unpushedBranches(name)
		if len(branches) == 0 {
			continue
		}
		landed, refusal, err := r.land(name)
		if err != nil {
			return err
		}
		if refusal {
			refused = append(refused, name)
		}
		if landed != "" {
			branches = []string{landed}
		}
		r.reporter.StoryNotPushed(r.remotes[name], branches)
	}
	if len(refused) > 0 {
		return &apperr.NeedsPersonError{Msg: fmt.Sprintf("story %s is completed, but its result is not on the base branch of %s", r.loc.slug, strings.Join(refused, ", "))}
	}
	return nil
}

func (r *storyRunner) checked(name string) bool {
	_, ok := r.noPush[name]
	return ok
}

// readDelivery reads the delivery settings of a service the checks skipped (every task of it was
// done before this story run, checkServices) and takes the fingerprint its land compares.
func (r *storyRunner) readDelivery(name string) error {
	path := r.rec.Services[name]
	svc, err := config.Load(filepath.Join(path, defaultConfigPath))
	if err != nil {
		return &apperr.ConfigError{Msg: fmt.Sprintf("service %s (%s): cannot read its config: %v", name, path, err)}
	}
	repo, err := repository.Open(r.ctx, path, r.osExec)
	if err != nil {
		return err
	}
	fingerprint, err := repo.GitFingerprint(r.ctx)
	if err != nil {
		return err
	}
	if r.noPush == nil {
		r.noPush, r.remotes, r.fingerprints = map[string]bool{}, map[string]string{}, map[string]string{}
	}
	r.noPush[name], r.remotes[name], r.fingerprints[name] = !svc.Delivery.PushEnabled(), svc.Delivery.RemoteName(), fingerprint
	return nil
}

// unpushedBranches are the branches a story left in a service that does not push (specs/
// delivery-push §4): the spec branch first when a task of the service was based on a merge there,
// then the tasks' branches in the story's order.
func (r *storyRunner) unpushedBranches(name string) []string {
	var branches []string
	for _, id := range r.order {
		if parents, _ := story.Parents(r.spec.tasks, id); r.byID[id].Repo == name && len(parents) > 1 {
			branches = append(branches, r.rec.Bases[name].SpecBranch)
			break
		}
	}
	for _, id := range r.order {
		if rt := r.rec.Tasks[id]; rt.Repo == name && rt.Commit != "" && rt.Branch != "" {
			branches = append(branches, rt.Branch)
		}
	}
	return branches
}

// land brings the result of the service's tasks onto its base branch (§2, §3) and records the
// story there (§4). It returns the base branch when it has the result, or refused when it was left
// as it was for a reason the operator settles; an error is git failing.
func (r *storyRunner) land(name string) (landed string, refused bool, err error) {
	base := r.rec.Bases[name].Branch
	if base == "" {
		r.reporter.StoryNote(fmt.Sprintf("service %s: the story was started on a detached HEAD, so its result is left on the tasks' branches", name))
		return "", false, nil
	}
	refuse := func(why string, hints ...string) (string, bool, error) {
		r.reporter.StoryNotLanded(base, why)
		r.reporter.StoryHint(append(hints, "then: orchestrator story run "+logging.ShellWord(r.dirArg))...)
		return "", true, nil
	}
	dir := r.rec.Services[name]
	repo, err := repository.Open(r.ctx, dir, r.osExec)
	if err != nil {
		return "", false, err
	}
	tip, exists, err := repo.BranchTip(r.ctx, base)
	if err != nil {
		return "", false, err
	}
	if !exists {
		return refuse(fmt.Sprintf("the branch %s does not exist any more", base))
	}

	leaves := r.leaves(name)
	commits, branches := make([]string, len(leaves)), make([]string, len(leaves))
	for i, id := range leaves {
		commits[i], branches[i] = r.rec.Tasks[id].Commit, r.rec.Tasks[id].Branch
	}
	target, files, err := mergeOnto(r.ctx, repo, tip, commits, func(i int) string {
		return fmt.Sprintf("Merge %s into %s\n\nOrchestrator-Story: %s", branches[i], base, r.loc.slug)
	}, func() error { return r.checkFingerprint(name, repo) })
	if err != nil {
		return "", false, err
	}
	if len(files) > 0 {
		return refuse(fmt.Sprintf("the tasks' branches do not merge cleanly (%s)", strings.Join(files, ", ")),
			fmt.Sprintf("Merge them by hand on %s: git merge %s", base, shellWords(branches)))
	}

	if target == tip {
		r.reporter.StoryNote(base + " already has the story's result")
	} else {
		unlock, err := r.lockWorktrees(repo)
		if err != nil {
			return "", false, err
		}
		list, err := repo.ListWorktrees(r.ctx)
		unlock()
		if err != nil {
			return "", false, err
		}
		moved := false
		for _, wt := range list {
			if wt.Branch != "refs/heads/"+base {
				continue
			}
			if !sameResolvedPath(wt.Path, dir) {
				return refuse(fmt.Sprintf("%s is checked out in %s", base, wt.Path),
					fmt.Sprintf("Merge the tasks' branches there: git -C %s merge %s", logging.ShellWord(wt.Path), shellWords(branches)))
			}
			// The merge runs the repository's hooks and filters.
			if err := r.checkFingerprint(name, repo); err != nil {
				return "", false, err
			}
			if err := repo.MergeFastForward(r.ctx, target); err != nil {
				return refuse(err.Error(), "Commit or stash the local changes git names")
			}
			moved = true
		}
		if !moved {
			if err := repo.UpdateRef(r.ctx, "refs/heads/"+base, target, tip); err != nil {
				return "", false, err
			}
		}
		r.reporter.StoryLanded(base, shortSHA(target), branches)
	}
	if err := r.recordStory(name, base); err != nil {
		return refuse(err.Error())
	}
	return base, false, nil
}

// leaves are the service's done tasks that no other task of the service starts from, in the
// story's order: their commits hold every other task's.
func (r *storyRunner) leaves(name string) []string {
	inner := map[string]bool{}
	for _, id := range r.order {
		if r.byID[id].Repo == name {
			parents, _ := story.Parents(r.spec.tasks, id)
			for _, p := range parents {
				inner[p] = true
			}
		}
	}
	var out []string
	for _, id := range r.order {
		if rt := r.rec.Tasks[id]; r.byID[id].Repo == name && !inner[id] && rt.Commit != "" {
			out = append(out, id)
		}
	}
	return out
}

// recordStory commits the files story run writes in the spec directory (README.md and the task
// files) on the base branch (§4), when the story home is the service's checkout and is on it.
// Nothing else the operator changed or staged goes in.
func (r *storyRunner) recordStory(name, base string) error {
	if !sameResolvedPath(r.loc.home, r.rec.Services[name]) {
		return nil
	}
	home, err := repository.Open(r.ctx, r.loc.home, r.osExec)
	if err != nil {
		return err
	}
	if branch, err := home.Branch(r.ctx); err != nil || branch != base {
		return err
	}
	ours := map[string]bool{path.Join(r.loc.specRel, "README.md"): true}
	for _, p := range r.spec.files {
		if rel, err := filepath.Rel(r.loc.home, p); err == nil {
			ours[filepath.ToSlash(rel)] = true
		}
	}
	changed, err := home.ChangedFiles(r.ctx)
	if err != nil {
		return err
	}
	var paths []string
	for _, c := range changed {
		if ours[c] {
			paths = append(paths, c)
		}
	}
	if len(paths) == 0 {
		return nil
	}
	slices.Sort(paths)
	if err := r.checkFingerprint(name, home); err != nil {
		return err
	}
	sha, err := home.CommitPaths(r.ctx, fmt.Sprintf("Record the %s story: README and As built\n\nOrchestrator-Story: %s\n", r.loc.slug, r.loc.slug), paths)
	if err != nil {
		return err
	}
	r.reporter.StoryRecorded(base, shortSHA(sha))
	return nil
}

// lockWorktrees takes the repository's worktrees lock (persistence.LockWorktrees).
func (r *storyRunner) lockWorktrees(repo *repository.Repository) (func(), error) {
	common, err := repo.CommonDir(r.ctx)
	if err != nil {
		return nil, err
	}
	state, err := persistence.StateDir()
	if err != nil {
		return nil, err
	}
	return persistence.LockWorktrees(filepath.Dir(persistence.WorktreePath(state, common, "lock")))
}

func shellWords(words []string) string {
	quoted := make([]string, len(words))
	for i, w := range words {
		quoted[i] = logging.ShellWord(w)
	}
	return strings.Join(quoted, " ")
}

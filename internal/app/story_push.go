package app

import (
	"fmt"
	"maps"
	"slices"

	"orchestrator/internal/apperr"
	"orchestrator/internal/logging"
	"orchestrator/internal/persistence"
	"orchestrator/internal/repository"
	"orchestrator/internal/story"
)

// The spec branch on the remote (specs/delivery/SPEC.md §9, D22): the merge requests of the tasks
// with no parent or with several target story/<slug>/spec, so the parent story run pushes it - once
// after the approval, and after every merge it makes on it - before the dispatch that needs it.

// pushSpecBranches pushes the spec branch of every repository the story still works in.
func (r *storyRunner) pushSpecBranches() error {
	need := map[string]bool{}
	for _, id := range r.order {
		if r.rec.Tasks[id].Status != story.TaskDone {
			need[r.byID[id].Repo] = true
		}
	}
	for _, name := range slices.Sorted(maps.Keys(need)) {
		if err := r.pushSpecBranch(name); err != nil {
			return err
		}
	}
	return nil
}

// pushSpecBranch makes the remote's spec branch of repository name the local one. It is pushed from
// the repository's checkout, as the operator, after the git fingerprint matched the one the pre-run
// checks took:
//
//	git push --no-follow-tags <remote> refs/heads/story/<slug>/spec:refs/heads/story/<slug>/spec
//
// with no -u, no push options and never --force. The remote is asked first (ls-remote): at the first
// push it must have no such branch, and later it must be at the commit pushed before, so a push is
// always a fast-forward of that commit and a branch that moved on the remote is not touched. The
// intent is saved in story.json before the push, and a push that failed or was rejected stops the
// story, before the next dispatch, with the command to push it by hand.
func (r *storyRunner) pushSpecBranch(name string) error {
	b, ok := r.rec.Bases[name]
	if !ok || b.SpecBranch == "" || r.noPush {
		return nil
	}
	repo, err := repository.Open(r.ctx, r.rec.Services[name], r.osExec)
	if err != nil {
		return err
	}
	tip, exists, err := repo.BranchTip(r.ctx, b.SpecBranch)
	if err != nil {
		return err
	}
	if !exists {
		return r.specPushStop(name, fmt.Sprintf("service %s: the spec branch %s does not exist any more", name, b.SpecBranch), nil)
	}
	sp := b.SpecPush
	if sp != nil && sp.State == persistence.SpecPushPushed && sp.SHA == tip {
		return nil
	}

	fingerprint, err := repo.GitFingerprint(r.ctx)
	if err != nil {
		return err
	}
	if want, ok := r.fingerprints[name]; !ok || fingerprint != want {
		return r.specPushStop(name, fmt.Sprintf("service %s: the git config or hooks of %s changed since the checks of this story run, "+
			"so the spec branch was not pushed", name, r.rec.Services[name]), nil)
	}
	remote := r.remotes[name]
	if remote == "" {
		remote = "origin"
	}
	remoteSHA, present, err := repo.LsRemoteHeadSHA(r.ctx, remote, b.SpecBranch)
	if err != nil {
		return r.specPushStop(name, fmt.Sprintf("service %s: cannot ask %s whether it has %s: %v", name, remote, b.SpecBranch, err), repo)
	}
	switch {
	case present && remoteSHA == tip:
		// Already there: pushed by an earlier story run whose answer was lost, or by hand.
		return r.recordSpecPush(name, persistence.StorySpecPush{State: persistence.SpecPushPushed, SHA: tip})
	case present && (sp == nil || remoteSHA != sp.SHA):
		return r.specPushStop(name, fmt.Sprintf("service %s: the branch %s is on %s at %s, not at the commit this story pushed; it is not pushed over",
			name, b.SpecBranch, remote, shortSHA(remoteSHA)), repo)
	case !present && sp != nil && sp.State == persistence.SpecPushPushed:
		return r.specPushStop(name, fmt.Sprintf("service %s: the branch %s is not on %s any more, though this story pushed it there", name, b.SpecBranch, remote), repo)
	}

	if err := r.recordSpecPush(name, persistence.StorySpecPush{State: persistence.SpecPushIntent, SHA: tip}); err != nil {
		return err
	}
	stderr, err := repo.PushWithoutUpstream(r.ctx, remote, b.SpecBranch, nil)
	if err != nil {
		why := pushFailure(stderr, err)
		if recErr := r.recordSpecPush(name, persistence.StorySpecPush{State: persistence.SpecPushIntent, SHA: tip, Error: why}); recErr != nil {
			return recErr
		}
		return r.specPushStop(name, fmt.Sprintf("service %s: the spec branch %s was not pushed to %s: %s", name, b.SpecBranch, remote, why), repo)
	}
	return r.recordSpecPush(name, persistence.StorySpecPush{State: persistence.SpecPushPushed, SHA: tip})
}

// recordSpecPush saves the state of a repository's spec push in story.json.
func (r *storyRunner) recordSpecPush(name string, sp persistence.StorySpecPush) error {
	b := r.rec.Bases[name]
	b.SpecPush = &sp
	r.rec.Bases[name] = b
	return r.persist()
}

// specPushStop ends the story at a spec branch that could not be put on the remote: its status is
// recorded, and the command that pushes it by hand is printed (repo is nil when the branch is not
// there to push).
func (r *storyRunner) specPushStop(name, why string, repo *repository.Repository) error {
	r.halt(story.StatusFailed)
	if repo != nil {
		b := r.rec.Bases[name]
		remote := r.remotes[name]
		if remote == "" {
			remote = "origin"
		}
		r.reporter.StoryHint(
			"Why: "+why,
			fmt.Sprintf("Fix it, then push the branch yourself if the remote is as it should be: git -C %s push %s %s:%s",
				logging.ShellWord(r.rec.Services[name]), logging.ShellWord(remote), "refs/heads/"+b.SpecBranch, "refs/heads/"+b.SpecBranch),
			fmt.Sprintf("and run the story again: orchestrator story run %s", logging.ShellWord(r.dirArg)))
	}
	return &apperr.WorkflowError{Msg: why}
}

package integration

import (
	"os"
	"strconv"
	"strings"
	"testing"

	"orchestrator/internal/domain"
	"orchestrator/internal/story"
	"orchestrator/internal/testutil/fakeexec"
)

// story run delivers (specs/delivery/SPEC.md §9, §9.1, TASK-DL05): every task's child pushes its
// branch to its repository's origin, which here is a bare repository with GitLab's push-options
// hook, and opens a merge request stacked on its dependency's branch. The branch is named by the
// repository's rules, story/<slug>/<ID> being the fallback.

// mrTargets are the merge_request.target options the remote received, in the order of the pushes.
func mrTargets(t *testing.T, bare string) []string {
	t.Helper()
	var targets []string
	for _, o := range fakeexec.GitLabPushOptions(t, bare) {
		if target, ok := strings.CutPrefix(o, "merge_request.target="); ok {
			targets = append(targets, target)
		}
	}
	return targets
}

// remoteHas is whether the bare repository has the branch.
func remoteHas(t *testing.T, bare, branch string) bool {
	t.Helper()
	_, err := gitOutput(bare, "rev-parse", "--verify", "-q", "refs/heads/"+branch)
	return err == nil
}

func TestStoryRun_EveryTaskIsAMergeRequestStackedOnItsDependency(t *testing.T) {
	f := newStoryRunFixture(t)
	before := f.checkouts(t)
	configBefore := readFile(t, f.repos["billing"]+"/.git/config")

	stdout, stderr, err := f.run(t)
	mustSucceed(t, stdout, stderr, err)

	rec := f.record(t)
	for id, rt := range rec.Tasks {
		if rt.MR == nil || rt.MR.IID == 0 || !strings.HasSuffix(rt.MR.URL, "/-/merge_requests/"+strconv.Itoa(rt.MR.IID)) {
			t.Errorf("%s: mr = %+v, want the merge request its child opened", id, rt.MR)
		}
	}
	// RF01 has no parent, RF02 is stacked on RF01, RF04 on RF02; RF03 is gateway's first task.
	if got, want := strings.Join(mrTargets(t, f.remotes["billing"]), " "), specBranch+" story/refunds/RF01 story/refunds/RF02"; got != want {
		t.Errorf("billing's merge requests target %q, want %q", got, want)
	}
	if got := strings.Join(mrTargets(t, f.remotes["gateway"]), " "); got != specBranch {
		t.Errorf("gateway's merge requests target %q, want %q", got, specBranch)
	}
	for id, rt := range rec.Tasks {
		if !remoteHas(t, f.remotes[rt.Repo], rt.Branch) {
			t.Errorf("%s: %s is not on the remote", id, rt.Branch)
		}
	}
	// The spec branch is on the remote before the first merge request targets it, and recorded.
	for repo, bare := range f.remotes {
		if !remoteHas(t, bare, specBranch) {
			t.Errorf("%s: %s is not on the remote", repo, specBranch)
		}
		if sp := rec.Bases[repo].SpecPush; sp == nil || sp.State != "pushed" || sp.SHA != f.git(t, repo, "rev-parse", specBranch) {
			t.Errorf("bases.%s.spec_push = %+v, want pushed at the spec branch's tip", repo, sp)
		}
	}
	assertContains(t, "stdout", stdout, "RF02 · billing · story/refunds/RF02 → story/refunds/RF01 · !2 https://", "The merge requests are open, each to its target.")
	assertContains(t, "README", readFile(t, f.specDir+"/README.md"), "[!2](https://")

	// A story child pushes without -u: the shared git config is what it was.
	if after := readFile(t, f.repos["billing"]+"/.git/config"); after != configBefore {
		t.Errorf("billing's git config changed:\n%s\nwas\n%s", after, configBefore)
	}
	f.assertCheckoutsUnchanged(t, before)
}

// The repository's worktree.branch.template names the branch; the dependent's merge request
// targets the name that was recorded, and --retry gets the -<6> suffix of the new run.
func TestStoryRun_ATemplatedBranchIsRecordedTargetedAndSuffixedOnRetry(t *testing.T) {
	f := newStoryRunFixture(t, func(o *storyRunFixtureOpts) {
		o.tasks = []story.Task{
			{ID: "RF01", Repo: "billing", Title: "Store the refunded amount"},
			{ID: "RF02", Repo: "billing", Title: "Round the refunded amount", DependsOn: []string{"RF01"}},
		}
		o.serviceYAML = "\nworktree:\n  branch:\n    template: \"feature/{slug}\"\n"
	})
	writeResponse(t, f.responses, "review_1", storyReviewMalformed)

	if _, _, err := f.run(t); err == nil {
		t.Fatal("a story with a failing child succeeded")
	}
	first := f.record(t).Tasks["RF01"]
	if first.Status != story.TaskFailed || first.Branch != "feature/store-the-refunded-amount" {
		t.Fatalf("RF01 = %+v, want failed on feature/store-the-refunded-amount", first)
	}

	stdout, stderr, err := f.run(t, "--retry", "RF01")
	mustSucceed(t, stdout, stderr, err)
	rec := f.record(t)
	retried := rec.Tasks["RF01"]
	if want := "feature/store-the-refunded-amount-" + retried.RunID[len(retried.RunID)-6:]; retried.Branch != want || retried.Attempt != 2 {
		t.Errorf("RF01 = %+v, want attempt 2 on %s", retried, want)
	}
	if in := f.input(t, "RF01"); in.Branch != "story/refunds/RF01.2" || in.Title != "Store the refunded amount" {
		t.Errorf("RF01 input = %+v, want the fallback story/refunds/RF01.2 and its title", in)
	}
	run := f.runRecord(t, "billing", "RF01", 2)
	if run.Task.Source.Branch != "story/refunds/RF01.2" || run.Worktree.Branch != retried.Branch {
		t.Errorf("task.source.branch = %q, worktree.branch = %q; want the fallback and the name used", run.Task.Source.Branch, run.Worktree.Branch)
	}
	// RF02 is stacked on the recorded name, not on story/refunds/RF01.
	if rf02 := rec.Tasks["RF02"]; rf02.Branch != "feature/round-the-refunded-amount" {
		t.Errorf("RF02 = %+v", rf02)
	}
	if got, want := strings.Join(mrTargets(t, f.remotes["billing"]), " "), specBranch+" "+retried.Branch; got != want {
		t.Errorf("billing's merge requests target %q, want %q", got, want)
	}
	if run := f.runRecord(t, "billing", "RF02", 1); run.Worktree.BaseBranch != retried.Branch || run.MR == nil || run.MR.Target != retried.Branch {
		t.Errorf("RF02's run: base_branch %q, mr %+v; want %s", run.Worktree.BaseBranch, run.MR, retried.Branch)
	}
}

// Without rules a retry is on story/<slug>/<ID>.2.
func TestStoryRun_ARetryWithoutRulesIsOnTheNumberedFallback(t *testing.T) {
	f := newStoryRunFixture(t, func(o *storyRunFixtureOpts) { o.tasks = storyMergeTasks[:1] })
	writeResponse(t, f.responses, "review_1", storyReviewMalformed)
	if _, _, err := f.run(t); err == nil {
		t.Fatal("a story with a failing child succeeded")
	}
	stdout, stderr, err := f.run(t, "--retry", "RF01")
	mustSucceed(t, stdout, stderr, err)
	if rt := f.record(t).Tasks["RF01"]; rt.Branch != "story/refunds/RF01.2" || rt.Attempt != 2 || rt.MR == nil {
		t.Errorf("RF01 = %+v, want attempt 2 on story/refunds/RF01.2 with a merge request", rt)
	}
}

// A child that completes without a commit (exit 6) holds the tasks that depend on it; an
// independent task goes on.
func TestStoryRun_ATaskWithoutACommitHoldsItsDependentsAndNotTheIndependent(t *testing.T) {
	f := newStoryRunFixture(t, func(o *storyRunFixtureOpts) {
		o.tasks = []story.Task{
			{ID: "RF01", Repo: "billing", Title: "Store the refunded amount"},
			{ID: "RF02", Repo: "billing", Title: "Round the refunded amount", DependsOn: []string{"RF01"}},
			{ID: "RF03", Repo: "gateway", Title: "Accept an amount in the API"},
		}
	})
	writeResponse(t, f.responses, "implement_1", storyImplementNoMessage)

	stdout, _, err := f.run(t)
	if err == nil {
		t.Fatalf("a story with a blocked task succeeded\nstdout:\n%s", stdout)
	}
	assertWorkflowError(t, err)
	rec := f.record(t)
	if rec.Status != story.StatusBlocked || rec.Tasks["RF01"].Status != story.TaskBlocked {
		t.Fatalf("status = %s, RF01 = %+v; want a blocked story and task", rec.Status, rec.Tasks["RF01"])
	}
	if rt := rec.Tasks["RF02"]; (rt.Status != "" && rt.Status != story.TaskPending) || rt.RunID != "" {
		t.Errorf("RF02 = %+v, want it not started", rt)
	}
	if rt := rec.Tasks["RF03"]; rt.Status != story.TaskDone || rt.MR == nil {
		t.Errorf("RF03 = %+v, want the independent task done with a merge request", rt)
	}
	assertContains(t, "stdout", stdout, "RF01: blocked", "RF03: done")
}

// A child that commits and pushes but whose push opened no merge request is done without one: the
// story stops before the tasks that depend on it, and independent tasks go on.
func TestStoryRun_ATaskWithoutAMergeRequestHoldsItsDependents(t *testing.T) {
	f := newStoryRunFixture(t, func(o *storyRunFixtureOpts) {
		o.tasks = []story.Task{
			{ID: "RF01", Repo: "billing", Title: "Store the refunded amount"},
			{ID: "RF02", Repo: "billing", Title: "Round the refunded amount", DependsOn: []string{"RF01"}},
			{ID: "RF03", Repo: "gateway", Title: "Accept an amount in the API"},
		}
	})
	// The remote no longer advertises push options: the push is refused, the commit is made.
	if _, err := gitOutput(f.remotes["billing"], "config", "receive.advertisePushOptions", "false"); err != nil {
		t.Fatal(err)
	}

	stdout, _, err := f.run(t)
	if err == nil {
		t.Fatalf("a story whose first task has no merge request succeeded\nstdout:\n%s", stdout)
	}
	assertWorkflowError(t, err)
	rec := f.record(t)
	if rt := rec.Tasks["RF01"]; rt.Status != story.TaskDone || rt.MR != nil || rt.Commit == "" {
		t.Errorf("RF01 = %+v, want done with its commit and without a merge request", rt)
	}
	if run := f.runRecord(t, "billing", "RF01", 1); run.MR == nil || run.MR.State == domain.MRCreated {
		t.Errorf("RF01's run: mr = %+v, want a push that opened none", run.MR)
	}
	if rt := rec.Tasks["RF02"]; (rt.Status != "" && rt.Status != story.TaskPending) || rt.RunID != "" {
		t.Errorf("RF02 = %+v, want it not started", rt)
	}
	if rt := rec.Tasks["RF03"]; rt.Status != story.TaskDone {
		t.Errorf("RF03 = %+v, want the independent task done", rt)
	}
	if rec.Status != story.StatusBlocked {
		t.Errorf("status = %s, want blocked", rec.Status)
	}
	assertContains(t, "stdout", stdout, "blocked: RF01 has no merge request")
}

// A service without its remote stops the story before anything is dispatched.
func TestStoryRun_AServiceWithoutItsRemoteStopsBeforeTheFirstDispatch(t *testing.T) {
	f := newStoryRunFixture(t)
	gitRun(t, f.repos["gateway"], "remote", "remove", "origin")

	_, _, err := f.run(t)
	assertConfigError(t, err, "service gateway", `no git remote "origin"`)
	if got := invocations(t, f.invocationsLog); len(got) != 0 {
		t.Errorf("a story with a service without a remote called %v", got)
	}
	if _, err := os.Stat(f.repos["billing"] + "/.orchestrator/runs"); !os.IsNotExist(err) {
		t.Errorf("a run was started in billing (%v)", err)
	}
}

// The spec branch is on the remote before the first dispatch and again after a merge on it; a task
// with two parents targets it.
func TestStoryRun_TheSpecBranchIsPushedAfterAMerge(t *testing.T) {
	f := newStoryMergeFixture(t)

	stdout, stderr, err := f.run(t)
	mustSucceed(t, stdout, stderr, err)
	rec := f.record(t)
	tip := f.git(t, "billing", "rev-parse", specBranch)
	if tip == rec.Bases["billing"].SHA {
		t.Fatalf("the spec branch is still at the story base %s: nothing was merged on it", tip)
	}
	if got, err := gitOutput(f.remotes["billing"], "rev-parse", "refs/heads/"+specBranch); err != nil || got != tip {
		t.Errorf("the remote's %s = %q (%v), want the merged tip %s", specBranch, got, err, tip)
	}
	if sp := rec.Bases["billing"].SpecPush; sp == nil || sp.State != "pushed" || sp.SHA != tip {
		t.Errorf("spec_push = %+v, want pushed at %s", sp, tip)
	}
	if got, want := strings.Join(mrTargets(t, f.remotes["billing"]), " "), specBranch+" "+specBranch+" "+specBranch; got != want {
		t.Errorf("the merge requests target %q, want %q", got, want)
	}
}

// A spec branch that moved on the remote stops the story, and nothing is forced.
func TestStoryRun_ASpecBranchThatMovedOnTheRemoteStopsTheStory(t *testing.T) {
	f := newStoryMergeFixture(t)
	writeResponse(t, f.responses, "review_1", storyReviewMalformed)
	if _, _, err := f.run(t); err == nil {
		t.Fatal("a story with a failing child succeeded")
	}
	if sp := f.record(t).Bases["billing"].SpecPush; sp == nil || sp.State != "pushed" {
		t.Fatalf("spec_push = %+v, want pushed", sp)
	}

	// Somebody else's commit lands on the remote's spec branch.
	other := t.TempDir()
	gitRun(t, other, "clone", "-q", f.remotes["billing"], ".")
	gitRun(t, other, "checkout", "-q", specBranch)
	gitRun(t, other, "commit", "-q", "--allow-empty", "-m", "somebody else's commit")
	gitRun(t, other, "push", "-q", "origin", "HEAD:refs/heads/"+specBranch)
	remoteTip, _ := gitOutput(f.remotes["billing"], "rev-parse", "refs/heads/"+specBranch)

	// RF01 is resumed and RF02 runs; the merge of their commits on the spec branch is then to be
	// pushed, and the remote's branch is not where this story left it.
	_, _, err := f.run(t)
	if err == nil {
		t.Fatal("a story whose spec branch moved on the remote went on")
	}
	assertWorkflowError(t, err)
	if got, _ := gitOutput(f.remotes["billing"], "rev-parse", "refs/heads/"+specBranch); got != remoteTip {
		t.Errorf("the remote's spec branch is %s, was %s: it was pushed over", got, remoteTip)
	}
	rec := f.record(t)
	if rt := rec.Tasks["RF03"]; (rt.Status != "" && rt.Status != story.TaskPending) || rt.RunID != "" {
		t.Errorf("RF03 = %+v, want it not dispatched", rt)
	}
	if rec.Status != story.StatusFailed {
		t.Errorf("status = %s, want failed", rec.Status)
	}
}

// A service whose config says delivery.push: false (specs/delivery-push §4) needs no remote: its
// tasks end at their commits, a dependent is stacked on its parent's local commit, nothing is
// pushed (the spec branch neither), and the end names the command that pushes what is left.
func TestStoryRun_AServiceThatDoesNotPushStacksOnLocalBranches(t *testing.T) {
	f := newStoryRunFixture(t, func(o *storyRunFixtureOpts) {
		o.tasks = []story.Task{
			{ID: "RF01", Repo: "billing", Title: "Store the refunded amount"},
			{ID: "RF02", Repo: "billing", Title: "Round the refunded amount", DependsOn: []string{"RF01"}},
		}
		o.serviceYAML = "\ndelivery:\n  push: false\n"
	})
	gitRun(t, f.repos["billing"], "remote", "remove", "origin")

	stdout, stderr, err := f.run(t)
	mustSucceed(t, stdout, stderr, err)

	rec := f.record(t)
	first, second := rec.Tasks["RF01"], rec.Tasks["RF02"]
	if first.Status != story.TaskDone || second.Status != story.TaskDone || first.Commit == "" || second.Commit == "" {
		t.Fatalf("RF01 = %+v, RF02 = %+v; want both done with a commit", first, second)
	}
	if second.BaseRef != first.Commit {
		t.Errorf("RF02 is based on %s, want RF01's commit %s", second.BaseRef, first.Commit)
	}
	if first.MR != nil || second.MR != nil || rec.Bases["billing"].SpecPush != nil {
		t.Errorf("mr = %+v / %+v, spec_push = %+v; want none", first.MR, second.MR, rec.Bases["billing"].SpecPush)
	}
	if refs, _ := gitOutput(f.remotes["billing"], "for-each-ref"); refs != "" {
		t.Errorf("billing's remote received refs:\n%s", refs)
	}
	if rec.Status != story.StatusCompleted {
		t.Errorf("status = %s, want completed", rec.Status)
	}
	assertContains(t, "stdout", stdout, "Not pushed (delivery.push: false): git push origin "+first.Branch+" "+second.Branch+"\n")
}

// A story held because its first task's push opened no merge request goes on once the service
// turns the push off (specs/delivery-push §4): the dependent opens none, so it is stacked on the
// parent's local commit.
func TestStoryRun_TurningThePushOffReleasesATaskHeldForAMergeRequest(t *testing.T) {
	f := newStoryRunFixture(t, func(o *storyRunFixtureOpts) {
		o.tasks = []story.Task{
			{ID: "RF01", Repo: "billing", Title: "Store the refunded amount"},
			{ID: "RF02", Repo: "billing", Title: "Round the refunded amount", DependsOn: []string{"RF01"}},
		}
	})
	if _, err := gitOutput(f.remotes["billing"], "config", "receive.advertisePushOptions", "false"); err != nil {
		t.Fatal(err)
	}
	if _, _, err := f.run(t); err == nil || f.record(t).Status != story.StatusBlocked {
		t.Fatalf("the first story run: %v, status %s; want blocked on RF01's merge request", err, f.record(t).Status)
	}

	cfgPath := f.repos["billing"] + "/orchestrator.yaml"
	if err := os.WriteFile(cfgPath, []byte(readFile(t, cfgPath)+"\ndelivery:\n  push: false\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	stdout, stderr, err := f.run(t)
	mustSucceed(t, stdout, stderr, err)
	rec := f.record(t)
	if rt := rec.Tasks["RF02"]; rt.Status != story.TaskDone || rt.BaseRef != rec.Tasks["RF01"].Commit {
		t.Errorf("RF02 = %+v, want done on RF01's commit %s", rt, rec.Tasks["RF01"].Commit)
	}
	if rec.Status != story.StatusCompleted {
		t.Errorf("status = %s, want completed", rec.Status)
	}
}

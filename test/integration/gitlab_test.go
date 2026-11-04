package integration

import (
	"bytes"
	"context"
	"encoding/json"
	"maps"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/kasorse/orch/internal/app"
	"github.com/kasorse/orch/internal/exitcode"
	"github.com/kasorse/orch/internal/story"
	"github.com/kasorse/orch/internal/testutil/fakeexec"
)

// Delivery scenarios 64-77 (specs/delivery/tasks/TASK-DL11-integration.md): a run that ends with a
// commit, a push and a merge request, against fake executables, a fake GitLab remote
// (fakeexec.AddGitLabRemote: a bare repository with receive.advertisePushOptions and a
// post-receive hook that records GIT_PUSH_OPTION_* and prints GitLab's "View merge request"
// lines) and the fake Jira with its write endpoints (jira_test.go).
//
// Scenario 71 (--output json: one line for success, failure and a pre-run error) is
// output_json_test.go, scenario 70's stacked merge requests and blocked dependents are
// story_delivery_test.go, and the refusals of a worktree run that could not deliver are
// worktree_test.go; this file has the rest.

// deliveryRun is the part of run.json the delivery scenarios assert on, on top of the worktree's.
type deliveryRun struct {
	worktreeRunJSON

	BranchNaming *struct {
		Source   string `json:"source"`
		Proposed string `json:"proposed"`
		Rejected string `json:"rejected"`
	} `json:"branch_naming"`

	JiraWrite *struct {
		Comment *struct {
			State string `json:"state"`
			ID    string `json:"id"`
		} `json:"comment"`
		Transition *struct {
			Name  string `json:"name"`
			State string `json:"state"`
		} `json:"transition"`
	} `json:"jira_write"`

	ReviewTarget *struct {
		MergeBase   string `json:"merge_base"`
		BaseRefName string `json:"base_ref_name"`
		HeadRefName string `json:"head_ref_name"`
	} `json:"review_target"`
}

func loadDeliveryRun(t *testing.T, repoDir string) deliveryRun {
	t.Helper()
	raw, ok := runFile(t, repoDir, "run.json")
	if !ok {
		t.Fatal("run.json was not written")
	}
	var run deliveryRun
	if err := json.Unmarshal([]byte(raw), &run); err != nil {
		t.Fatal(err)
	}
	return run
}

func currentBranch(t *testing.T, dir string) string {
	t.Helper()
	branch, err := gitOutput(dir, "symbolic-ref", "--short", "HEAD")
	if err != nil {
		t.Fatal(err)
	}
	return branch
}

// remoteBranches are the branches of the bare repository, one per line; "" when it has none.
func remoteBranches(t *testing.T, bare string) string {
	t.Helper()
	out, err := gitOutput(bare, "branch", "--list", "--format=%(refname:short)")
	if err != nil {
		t.Fatal(err)
	}
	return out
}

// assertOperatorStateUnchanged fails for every part of the operator's checkout that is not what
// operatorState found before the run.
func assertOperatorStateUnchanged(t *testing.T, dir string, before map[string]string) {
	t.Helper()
	after := operatorState(t, dir)
	if maps.Equal(after, before) {
		return
	}
	for k, v := range after {
		if before[k] != v {
			t.Errorf("the operator's %s changed:\nbefore: %q\nafter:  %q", k, before[k], v)
		}
	}
	for k := range before {
		if _, ok := after[k]; !ok {
			t.Errorf("the operator's %s is gone", k)
		}
	}
}

// 64: a worktree run is the default; under --mode auto it commits (type and trailers), pushes its
// branch with the merge request's options, records the merge request and removes the worktree.
func TestDelivery64_AutoCommitsPushesAndOpensTheMergeRequest(t *testing.T) {
	repoDir := fakeexec.InitGitFixture(t)
	r, _, _ := newWorktreeRun(t, repoDir)
	writeResponse(t, os.Getenv("FAKE_RESPONSES_DIR"), "implement", `{
  "summary": "fixed Add to use +",
  "summary_short": "Fix Add to use +",
  "commit_type": "fix",
  "changed_files": ["fixture.go", "added.txt"],
  "tests_added": []
}`)
	target := currentBranch(t, repoDir)

	stdout, stderr, err := runOrchestrator(t, repoDir, r.cfg, "run", "--stage", "development", "--stage", "testing", "fix Add in fixture.go")
	mustSucceed(t, stdout, stderr, err)

	run := loadDeliveryRun(t, repoDir)
	w := run.Worktree
	if run.Status != "completed" || run.Options.Isolation != "worktree" || run.Commit == "" || w == nil || !w.Removed {
		t.Fatalf("status = %s, isolation = %q, commit = %q, worktree = %+v; want a completed worktree run, committed, removed",
			run.Status, run.Options.Isolation, run.Commit, w)
	}
	wantMessage := "fix: Fix Add to use +\n\nOrchestrator-Run: " + run.ID + "\nOrchestrator-Decision: manual/development,testing"
	if msg, _ := gitOutput(repoDir, "log", "-1", "--format=%B", run.Commit); msg != wantMessage {
		t.Errorf("commit message = %q, want %q", msg, wantMessage)
	}
	if tip, err := gitOutput(r.remote, "rev-parse", w.Branch); err != nil || tip != run.Commit {
		t.Errorf("the remote's %s = %q (%v), want the run's commit %s", w.Branch, tip, err, run.Commit)
	}

	// Four options and nothing else: no line feed reached the remote, the breaks went as \n.
	options := fakeexec.GitLabPushOptions(t, r.remote)
	if len(options) != 4 || options[0] != "merge_request.create" || options[1] != "merge_request.target="+target ||
		options[2] != "merge_request.title=fix: Fix Add to use +" || !strings.HasPrefix(options[3], `merge_request.description=## fix Add in fixture.go\n\n`) {
		t.Fatalf("the remote received %q", options)
	}
	description := strings.ReplaceAll(strings.TrimPrefix(options[3], "merge_request.description="), `\n`, "\n")
	assertContains(t, "the description", description, "**Passed stages:**", "Run `"+run.ID+"`", "commit `"+run.Commit[:7]+"`")

	const url = "https://" + fakeexec.GitLabHost + "/group/project/-/merge_requests/1"
	if mr := run.MR; mr == nil || mr.State != "created" || mr.IID != 1 || mr.URL != url || mr.Source != w.Branch || mr.Target != target {
		t.Errorf("mr = %+v, want created as !1 at %s from %s to %s", mr, url, w.Branch, target)
	}
	if _, err := os.Stat(w.Path); !os.IsNotExist(err) {
		t.Errorf("the worktree %s is still there: %v", w.Path, err)
	}
	assertContains(t, "stdout", stdout, "Merge request !1: "+url+"\n")
	if report, _ := runFile(t, repoDir, "report.md"); !strings.Contains(report, "merge request [!1]("+url+")") {
		t.Errorf("report.md does not name the merge request:\n%s", report)
	}
}

// 65: under --mode auto nothing is pushed for a run that did not commit. A review that keeps
// failing stops the run (nothing is committed, the worktree stays); a completed run that cannot
// commit by itself - here an implementation that proposed no commit message - records why in
// mr.skipped and needs a person (exit 6).
func TestDelivery65_AutoDeliversNothingWithoutACommit(t *testing.T) {
	t.Run("the review keeps failing", func(t *testing.T) {
		repoDir := fakeexec.InitGitFixture(t)
		r, _, _ := newWorktreeRun(t, repoDir)
		writeResponse(t, os.Getenv("FAKE_RESPONSES_DIR"), "review", reviewFailResponse)

		stdout, stderr, err := runOrchestrator(t, repoDir, r.cfg, "run", "--stage", "development", "--stage", "testing", "--stage", "review", "fix Add in fixture.go")
		if err == nil {
			t.Fatalf("a run whose review keeps failing succeeded\nstdout:\n%s\nstderr:\n%s", stdout, stderr)
		}

		run := loadDeliveryRun(t, repoDir)
		w := run.Worktree
		if run.Status == "completed" || run.Commit != "" || (run.MR != nil && run.MR.State != "") || w == nil || w.Removed {
			t.Fatalf("status = %s, commit = %q, mr = %+v, worktree = %+v; want no commit, no delivery, the worktree kept", run.Status, run.Commit, run.MR, w)
		}
		if _, err := os.Stat(w.Path); err != nil {
			t.Errorf("the worktree is gone: %v", err)
		}
		if got := remoteBranches(t, r.remote); got != "" {
			t.Errorf("the remote has branches %q", got)
		}
		if options := fakeexec.GitLabPushOptions(t, r.remote); len(options) != 0 {
			t.Errorf("the remote received push options %q", options)
		}
		assertContains(t, "stdout", stdout, "The worktree stays at "+w.Path+"\n")
		assertNotContains(t, "stdout", stdout, "Committed ", "Merge request")
	})
	t.Run("no commit message", func(t *testing.T) {
		repoDir := fakeexec.InitGitFixture(t)
		r, _, _ := newWorktreeRun(t, repoDir)
		writeResponse(t, os.Getenv("FAKE_RESPONSES_DIR"), "implement", storyImplementNoMessage)

		stdout, stderr, err := runOrchestrator(t, repoDir, r.cfg, "run", "--stage", "development", "--stage", "testing", "fix Add in fixture.go")
		if exitcode.FromError(err) != exitcode.NeedsPerson {
			t.Fatalf("exit code %d (%v), want %d\nstdout:\n%s\nstderr:\n%s", exitcode.FromError(err), err, exitcode.NeedsPerson, stdout, stderr)
		}

		run := loadDeliveryRun(t, repoDir)
		w := run.Worktree
		if run.Status != "completed" || run.Commit != "" || w == nil || w.Removed {
			t.Fatalf("status = %s, commit = %q, worktree = %+v; want completed, no commit, the worktree kept", run.Status, run.Commit, w)
		}
		if mr := run.MR; mr == nil || mr.State != "" || !strings.Contains(mr.Skipped, "no commit message") {
			t.Errorf("mr = %+v, want skipped for the missing commit message", mr)
		}
		if _, err := os.Stat(w.Path); err != nil {
			t.Errorf("the worktree is gone: %v", err)
		}
		if got := remoteBranches(t, r.remote); got != "" {
			t.Errorf("the remote has branches %q", got)
		}
		assertContains(t, "stdout", stdout, "No push and no merge request: ", "The worktree stays at "+w.Path+"\n")
	})
}

// 66: a remote that takes no push options (a plain bare repository) refuses the push. The intent
// stays, the error and the command to push by hand are shown, the exit code is 6, and the push is
// not tried again without the options: the remote has no branch.
func TestDelivery66_ARemoteWithoutPushOptionsNeedsAPersonAndIsPushedOnce(t *testing.T) {
	repoDir := fakeexec.InitGitFixture(t)
	r, _, _ := newWorktreeRun(t, repoDir)
	plain := filepath.Join(t.TempDir(), "plain.git")
	gitRun(t, repoDir, "init", "--bare", "-q", plain)
	gitRun(t, repoDir, "remote", "set-url", "origin", "file://"+fakeexec.GitLabHost+plain)

	stdout, stderr, err := runOrchestrator(t, repoDir, r.cfg, "run", "--stage", "development", "--stage", "testing", "fix Add in fixture.go")
	if exitcode.FromError(err) != exitcode.NeedsPerson {
		t.Fatalf("exit code %d (%v), want %d\nstdout:\n%s\nstderr:\n%s", exitcode.FromError(err), err, exitcode.NeedsPerson, stdout, stderr)
	}

	run := loadDeliveryRun(t, repoDir)
	if run.Status != "completed" || run.Commit == "" || run.Worktree == nil {
		t.Fatalf("status = %s, commit = %q, worktree = %+v; want completed and committed", run.Status, run.Commit, run.Worktree)
	}
	if mr := run.MR; mr == nil || mr.State != "intent" || !strings.Contains(mr.Error, "does not support push options") || mr.URL != "" {
		t.Errorf("mr = %+v, want the intent kept with git's error", mr)
	}
	assertContains(t, "stdout", stdout, "does not support push options", "To push it yourself: git push -u origin "+run.Worktree.Branch+"\n")
	if got := remoteBranches(t, plain); got != "" {
		t.Errorf("the plain remote has branches %q: the push was repeated without its options", got)
	}
	if tip, err := gitOutput(repoDir, "rev-parse", run.Worktree.Branch); err != nil || tip != run.Commit {
		t.Errorf("%s = %q (%v), want the run's commit: the branch holds the result", run.Worktree.Branch, tip, err)
	}
}

// 67: every mode but auto asks before the commit, the push and the merge request, and a no stops
// there with exit 0.
func TestDelivery67_NonAutoAnswers(t *testing.T) {
	cases := []struct {
		name, input string
		committed   bool
		pushed      bool
		skipped     string
	}{
		{"no at the commit", "a\nn\n", false, false, "declined at commit"},
		{"yes, no", "a\ny\nn\n", true, false, "declined at push"},
		{"yes, yes, no", "a\ny\ny\nn\n", true, true, "declined at merge request"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			repoDir := fakeexec.InitGitFixture(t)
			r, _, _ := newWorktreeRun(t, repoDir)

			stdout, stderr, err := runOrchestratorInteractive(t, repoDir, r.cfg, tc.input,
				"--mode", "accept-edits", "run", "--stage", "development", "--stage", "testing", "fix Add in fixture.go")
			mustSucceed(t, stdout, stderr, err)

			run := loadDeliveryRun(t, repoDir)
			w := run.Worktree
			if run.Status != "completed" || w == nil || (run.Commit != "") != tc.committed {
				t.Fatalf("status = %s, commit = %q, worktree = %+v; want completed, committed: %v", run.Status, run.Commit, w, tc.committed)
			}
			if mr := run.MR; mr == nil || mr.Skipped != tc.skipped || mr.Error != "" || mr.URL != "" {
				t.Errorf("mr = %+v, want skipped: %s", mr, tc.skipped)
			}
			if tc.committed {
				if tip, err := gitOutput(repoDir, "rev-parse", w.Branch); err != nil || tip != run.Commit {
					t.Errorf("%s = %q (%v), want the run's commit %s", w.Branch, tip, err, run.Commit)
				}
			}
			wantRemote := ""
			if tc.pushed {
				wantRemote = w.Branch
			}
			if got := remoteBranches(t, r.remote); got != wantRemote {
				t.Errorf("the remote has branches %q, want %q", got, wantRemote)
			}
			if options := fakeexec.GitLabPushOptions(t, r.remote); len(options) != 0 {
				t.Errorf("the remote received push options %q", options)
			}
		})
	}
}

// 68: --inplace is the way out of the delivery chain: the §42 question on the current branch, no
// push and no merge request, and no remote is needed. (A repository without a remote is refused
// without it, naming --inplace, before the worktree, and a workflow without implement runs in
// place: worktree_test.go.)
func TestDelivery68_InplaceAsksTheCommitQuestionAndNeverDelivers(t *testing.T) {
	repoDir := fakeexec.InitGitFixture(t)
	r, _, _ := newWorktreeRun(t, repoDir)
	gitRun(t, repoDir, "remote", "remove", "origin")

	stdout, stderr, err := runOrchestratorInteractive(t, repoDir, r.cfg, "a\ny\n",
		"--mode", "accept-edits", "run", "--inplace", "--stage", "development", "--stage", "testing", "fix Add in fixture.go")
	mustSucceed(t, stdout, stderr, err)

	run := loadDeliveryRun(t, repoDir)
	if run.Status != "completed" || run.Options.Isolation != "inplace" || run.Worktree != nil || run.MR != nil || run.Commit == "" {
		t.Fatalf("status = %s, isolation = %q, worktree = %+v, mr = %+v, commit = %q; want an in-place run that committed and delivered nothing",
			run.Status, run.Options.Isolation, run.Worktree, run.MR, run.Commit)
	}
	if head, _ := gitOutput(repoDir, "rev-parse", "HEAD"); head != run.Commit {
		t.Errorf("HEAD = %s, want the run's commit %s", head, run.Commit)
	}
	if got := remoteBranches(t, r.remote); got != "" {
		t.Errorf("the remote has branches %q", got)
	}
	assertContains(t, "stdout", stdout, "Commit to "+currentBranch(t, repoDir)+"? ")
	assertNotContains(t, "stdout", stdout, "Push ", "Merge request")
}

// newJiraDeliveryRun is a worktree run for the Jira issue PMC-1 at a fake Jira, with jira.write
// asking for a comment and the transition, ending at a remote that answers like GitLab.
func newJiraDeliveryRun(t *testing.T, transition string) (repoDir, cfg string, jira *fakeJira) {
	t.Helper()
	repoDir = fakeexec.InitGitFixture(t)
	t.Setenv("XDG_STATE_HOME", t.TempDir())
	decisionEnv(t, repoDir)
	fakeexec.AddGitLabRemote(t, repoDir)
	// The fix lands in the worktree, relative to the working directory of the call.
	t.Setenv("FAKE_FIX_TARGET_FILE", "fixture.go")
	jev := startJev(t, scriptBugfixMedium())
	jira = startJira(t, map[string]string{"PMC-1": jiraIssue(t, "PMC-1", pmc1Summary, pmc1Description, nil)})
	write := "  write:\n    comment: true\n    transition: " + yamlQuote(transition) + "\n"
	cfg = jevConfig(t, jev, func(o *configOptions) {
		o.isolation = isolationAbsent
		o.jira = &jiraOptions{baseURL: jira.URL(), write: write}
	})
	return repoDir, cfg, jira
}

// jiraTraffic is what the fake Jira received, one "METHOD path" per request (a status read with
// its query).
func jiraTraffic(j *fakeJira) []string {
	var got []string
	for _, req := range j.Requests() {
		method := "GET"
		if req.Method != "" {
			method = req.Method
		}
		line := method + " " + req.Path
		if req.Query == "fields=status" {
			line += "?fields=status"
		}
		got = append(got, line)
	}
	return got
}

// 69: a run for a Jira issue under --mode auto comments on the issue and moves it after the merge
// request was opened; a transition the issue does not have is a warning, not a failure.
func TestDelivery69_JiraCommentAndTransitionAfterTheMergeRequest(t *testing.T) {
	const issue = "/rest/api/2/issue/PMC-1"
	t.Run("comment and transition", func(t *testing.T) {
		repoDir, cfg, jira := newJiraDeliveryRun(t, "In Review")

		stdout, stderr, err := runOrchestrator(t, repoDir, cfg, "run", "--jira", "pmc-1")
		mustSucceed(t, stdout, stderr, err)

		run := loadDeliveryRun(t, repoDir)
		const url = "https://" + fakeexec.GitLabHost + "/group/project/-/merge_requests/1"
		if run.Status != "completed" || run.MR == nil || run.MR.State != "created" || run.MR.URL != url {
			t.Fatalf("status = %s, mr = %+v; want completed with the merge request", run.Status, run.MR)
		}
		want := []string{"GET " + issue, "POST " + issue + "/comment", "GET " + issue + "?fields=status", "GET " + issue + "/transitions", "POST " + issue + "/transitions"}
		reqs := jira.Requests()
		if got := jiraTraffic(jira); !equalStrings(got, want) {
			t.Fatalf("Jira received %q, want %q", got, want)
		}
		var comment struct{ Body string }
		if err := json.Unmarshal([]byte(reqs[1].Body), &comment); err != nil {
			t.Fatalf("the comment is not JSON: %v\n%s", err, reqs[1].Body)
		}
		assertContains(t, "the comment", comment.Body, "Merge request [!1|"+url+"]", `PMC\-1: Fix Add to use`)
		if reqs[4].Body != `{"transition":{"id":"31"}}` {
			t.Errorf("transition = %s, want id 31 (Send to review, to In Review)", reqs[4].Body)
		}

		jw := run.JiraWrite
		if jw == nil || jw.Comment == nil || jw.Comment.State != "done" || jw.Comment.ID != "10042" ||
			jw.Transition == nil || jw.Transition.State != "done" || jw.Transition.Name != "In Review" {
			t.Errorf("jira_write = %+v", jw)
		}
		merge, commented, moved := strings.Index(stdout, "Merge request !1"), strings.Index(stdout, "Commented on PMC-1 in Jira."), strings.Index(stdout, `Moved PMC-1 in Jira by "Send to review".`)
		if merge < 0 || commented < merge || moved < commented {
			t.Errorf("stdout is not merge request, comment, transition (%d, %d, %d):\n%s", merge, commented, moved, stdout)
		}
		for _, secret := range []string{jiraToken, jiraEmail} {
			if strings.Contains(stdout+stderr, secret) {
				t.Errorf("output contains %q", secret)
			}
		}
	})
	t.Run("a missing transition", func(t *testing.T) {
		repoDir, cfg, jira := newJiraDeliveryRun(t, "Ship it")

		stdout, stderr, err := runOrchestrator(t, repoDir, cfg, "run", "--jira", "pmc-1")
		mustSucceed(t, stdout, stderr, err)

		run := loadDeliveryRun(t, repoDir)
		if run.Status != "completed" || run.MR == nil || run.MR.State != "created" {
			t.Fatalf("status = %s, mr = %+v; want completed with the merge request: a warning does not fail the run", run.Status, run.MR)
		}
		want := []string{"GET " + issue, "POST " + issue + "/comment", "GET " + issue + "?fields=status", "GET " + issue + "/transitions"}
		if got := jiraTraffic(jira); !equalStrings(got, want) {
			t.Errorf("Jira received %q, want %q: no transition is made", got, want)
		}
		assertContains(t, "stdout", stdout, `Warning: PMC-1 has no transition "Ship it" (available: Start progress, Send to review)`)
		if jw := run.JiraWrite; jw == nil || jw.Comment == nil || jw.Comment.State != "done" || jw.Transition == nil || jw.Transition.State != "missing" {
			t.Errorf("jira_write = %+v, want the comment done and the transition missing", jw)
		}
	})
}

// 70: with a service's worktree.branch.template the branches of a story follow it, and the
// dependent's merge request targets the templated branch of the task it is stacked on.
func TestDelivery70_StoryBranchesFollowTheServiceTemplate(t *testing.T) {
	f := newStoryRunFixture(t, func(o *storyRunFixtureOpts) {
		o.tasks = []story.Task{
			{ID: "RF01", Repo: "billing", Title: "Store the refunded amount"},
			{ID: "RF02", Repo: "billing", Title: "Round the refunded amount", DependsOn: []string{"RF01"}},
		}
		o.serviceYAML = "\nworktree:\n  branch:\n    template: \"feature/{slug}\"\n"
	})

	stdout, stderr, err := f.run(t)
	mustSucceed(t, stdout, stderr, err)

	rec := f.record(t)
	first, second := rec.Tasks["RF01"], rec.Tasks["RF02"]
	if first.Branch != "feature/store-the-refunded-amount" || second.Branch != "feature/round-the-refunded-amount" {
		t.Fatalf("branches = %q and %q, want the template's", first.Branch, second.Branch)
	}
	for _, id := range []string{"RF01", "RF02"} {
		rt := rec.Tasks[id]
		if rt.MR == nil || rt.MR.IID == 0 || !remoteHas(t, f.remotes["billing"], rt.Branch) {
			t.Errorf("%s (%s): mr = %+v, on the remote: %v; want a merge request from a pushed branch", id, rt.Branch, rt.MR, remoteHas(t, f.remotes["billing"], rt.Branch))
		}
	}
	if got, want := strings.Join(mrTargets(t, f.remotes["billing"]), " "), specBranch+" "+first.Branch; got != want {
		t.Errorf("the merge requests target %q, want %q", got, want)
	}
	if run := f.runRecord(t, "billing", "RF02", 1); run.MR == nil || run.MR.Target != first.Branch {
		t.Errorf("RF02's run: mr = %+v, want it to target %s", run.MR, first.Branch)
	}
}

// 72: a command that would ask a question with nobody to answer it is refused at once, before the
// configuration is read: exit 2, no run, no call.
func TestDelivery72_NoTerminalAndNoModeIsRefusedAtOnce(t *testing.T) {
	missing := filepath.Join(t.TempDir(), "none.yaml")
	for _, tc := range []struct {
		name string
		args []string
	}{
		{"no mode", []string{"--config", missing, "run", "--stage", "development", "fix Add in fixture.go"}},
		{"manual mode", []string{"--config", missing, "--mode", "manual", "run", "--stage", "development", "fix Add in fixture.go"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			repoDir := fakeexec.InitGitFixture(t)
			r := newStageRun(t, repoDir)

			stdout, stderr, err := runOnAPipe(t, repoDir, tc.args...)
			if exitcode.FromError(err) != exitcode.ConfigOrDependency {
				t.Fatalf("exit code %d (%v), want %d", exitcode.FromError(err), err, exitcode.ConfigOrDependency)
			}
			assertContains(t, "error", err.Error(), "stdin is not a terminal: pass --mode auto")
			if stdout != "" || stderr != "" {
				t.Errorf("printed stdout %q, stderr %q", stdout, stderr)
			}
			assertNoRuns(t, repoDir)
			if got := invocations(t, r.invocationsLog); len(got) != 0 {
				t.Errorf("invocations = %v, want none", got)
			}
			if bodies := r.jev.Bodies(); len(bodies) != 0 {
				t.Errorf("Jev got %d requests, want none", len(bodies))
			}
		})
	}
	t.Run("json", func(t *testing.T) {
		repoDir := fakeexec.InitGitFixture(t)
		args := []string{"--config", missing, "--output", "json", "run", "--stage", "development", "fix Add in fixture.go"}
		stdout, stderr, err := runOnAPipe(t, repoDir, args...)
		if exitcode.FromError(err) != exitcode.ConfigOrDependency {
			t.Fatalf("exit code %d (%v), want %d", exitcode.FromError(err), err, exitcode.ConfigOrDependency)
		}
		m := resultLine(t, stdout, stderr, err)
		if m["status"] != "error" || m["run_id"] != "" || !strings.Contains(m["error"].(string), "stdin is not a terminal") {
			t.Errorf("line = %v", m)
		}
	})
}

// runOnAPipe is app.Run with a stdin that is a regular file: a real stdin that is no terminal, as a
// CI runner's is.
func runOnAPipe(t *testing.T, repoDir string, args ...string) (stdout, stderr string, err error) {
	t.Helper()
	stdin, createErr := os.Create(filepath.Join(t.TempDir(), "stdin"))
	if createErr != nil {
		t.Fatal(createErr)
	}
	t.Cleanup(func() { stdin.Close() })

	origWD, wdErr := os.Getwd()
	if wdErr != nil {
		t.Fatalf("getwd: %v", wdErr)
	}
	if chdirErr := os.Chdir(repoDir); chdirErr != nil {
		t.Fatalf("chdir %s: %v", repoDir, chdirErr)
	}
	defer func() {
		if chdirErr := os.Chdir(origWD); chdirErr != nil {
			t.Fatalf("restore chdir: %v", chdirErr)
		}
	}()

	var outBuf, errBuf bytes.Buffer
	err = app.Run(context.Background(), args, stdin, &outBuf, &errBuf)
	return outBuf.String(), errBuf.String(), err
}

// 73: review --base main reviews merge-base(main, HEAD)..HEAD in a detached worktree: the commits
// main gained after the fork and the operator's uncommitted work are not in the diff, the
// operator's tree is not touched, and a failing verdict is exit 6.
func TestDelivery73_ReviewBaseReviewsTheMergeBaseDiff(t *testing.T) {
	repoDir := fakeexec.InitGitFixture(t)
	write := func(name, content string) {
		t.Helper()
		if err := os.WriteFile(filepath.Join(repoDir, name), []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	gitRun(t, repoDir, "branch", "-M", "main")
	fork, err := gitOutput(repoDir, "rev-parse", "HEAD")
	if err != nil {
		t.Fatal(err)
	}
	gitRun(t, repoDir, "switch", "-q", "-c", "feature")
	write("feature.go", "package fixture\n\nvar FeatureOnly = 1\n")
	gitRun(t, repoDir, "add", "-A")
	gitRun(t, repoDir, "commit", "-qm", "Add the feature")
	gitRun(t, repoDir, "switch", "-q", "main")
	write("main_only.go", "package fixture\n\nvar MainOnly = 1\n")
	gitRun(t, repoDir, "add", "-A")
	gitRun(t, repoDir, "commit", "-qm", "Change main after the fork")
	gitRun(t, repoDir, "switch", "-q", "feature")
	// The operator's work in progress: a tracked file edited and an untracked one.
	f, err := os.OpenFile(filepath.Join(repoDir, "fixture.go"), os.O_APPEND|os.O_WRONLY, 0)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := f.WriteString("\n// the operator's work in progress\n"); err != nil {
		t.Fatal(err)
	}
	f.Close()
	write("dirty.txt", "not reviewed\n")

	t.Setenv("XDG_STATE_HOME", t.TempDir())
	r := newStageRun(t, repoDir, func(o *configOptions) { o.testCommand = "true" })
	writeResponse(t, os.Getenv("FAKE_RESPONSES_DIR"), "review", reviewFailResponse)
	settleTree(t, repoDir)
	before := operatorState(t, repoDir)

	stdout, stderr, err := runOrchestrator(t, repoDir, r.cfg, "review", "--base", "main")
	if exitcode.FromError(err) != exitcode.NeedsPerson {
		t.Fatalf("exit code %d (%v), want %d: completed, but the verdict fails\nstdout:\n%s\nstderr:\n%s", exitcode.FromError(err), err, exitcode.NeedsPerson, stdout, stderr)
	}

	assertOperatorStateUnchanged(t, repoDir, before)
	run := loadDeliveryRun(t, repoDir)
	if run.Status != "completed" || run.ReviewTarget == nil || run.ReviewTarget.MergeBase != fork ||
		run.ReviewTarget.BaseRefName != "main" || run.ReviewTarget.HeadRefName != "HEAD" {
		t.Errorf("status = %s, review_target = %+v; want completed, reviewing main...HEAD from the fork %s", run.Status, run.ReviewTarget, fork)
	}
	if w := run.Worktree; w == nil || w.Branch != "" {
		t.Errorf("worktree = %+v, want a detached one", w)
	}
	prompt, err := os.ReadFile(filepath.Join(r.promptsDir, "review.txt"))
	if err != nil {
		t.Fatalf("review prompt: %v", err)
	}
	assertContains(t, "the review prompt", string(prompt), "FeatureOnly")
	assertNotContains(t, "the review prompt", string(prompt), "MainOnly", "main_only.go", "dirty.txt", "work in progress")
	if got := invocations(t, r.invocationsLog); !equalStrings(got, []string{"review"}) {
		t.Errorf("invocations = %v, want [review]", got)
	}
}

// recordPushEnv makes the bare repository write the environment its receive-pack ran in to
// push-env.log: git starts it as a child of the push, so that is the push's environment.
func recordPushEnv(t *testing.T, bare string) {
	t.Helper()
	hook := filepath.Join(bare, "hooks", "pre-receive")
	if err := os.WriteFile(hook, []byte("#!/bin/sh\nenv > push-env.log\n"), 0o755); err != nil {
		t.Fatal(err)
	}
}

// 74: the push runs with the operator's whole environment (their credential helper reads a token
// from it), while no process the orchestrator starts for a model or for the tests gets a GitLab
// token variable: the orchestrator has none of its own, and the test command only gets its
// allowlist.
func TestDelivery74_ThePushGetsTheFullEnvironment(t *testing.T) {
	gitlabNames := []string{"GITLAB_TOKEN", "GL_TOKEN", "GITLAB_PRIVATE_TOKEN", "CI_JOB_TOKEN"}
	setup := func(t *testing.T) (repoDir string, r stageRun, testEnvPath, envDir string) {
		t.Helper()
		repoDir = fakeexec.InitGitFixture(t)
		testEnvPath = filepath.Join(t.TempDir(), "test.env")
		r, _, _ = newWorktreeRun(t, repoDir, func(o *configOptions) { o.testCommand = "env > '" + testEnvPath + "' && go test ./..." })
		envDir = t.TempDir()
		t.Setenv("FAKE_ENV_DIR", envDir)
		recordPushEnv(t, r.remote)
		for _, name := range gitlabNames {
			unsetenv(t, name)
		}
		t.Setenv("ORCH_DL11_PUSH_MARKER", "visible-to-the-push")
		return repoDir, r, testEnvPath, envDir
	}
	// gitlabTokenName is a variable of env that looks like a GitLab token and is not in skip.
	gitlabTokenName := func(env map[string]string, skip map[string]bool) string {
		for name := range env {
			upper := strings.ToUpper(name)
			if !skip[name] && (strings.Contains(upper, "GITLAB") && strings.Contains(upper, "TOKEN") || name == "GL_TOKEN" || name == "CI_JOB_TOKEN") {
				return name
			}
		}
		return ""
	}

	t.Run("no token is added", func(t *testing.T) {
		repoDir, r, testEnvPath, envDir := setup(t)
		// The orchestrator's own environment may hold other GitLab variables (this suite can run in
		// a CI job): only a variable it did not start with would be one the orchestrator added.
		own := map[string]bool{}
		for _, kv := range os.Environ() {
			name, _, _ := strings.Cut(kv, "=")
			own[name] = true
		}

		stdout, stderr, err := runOrchestrator(t, repoDir, r.cfg, "run", "--stage", "development", "--stage", "testing", "fix Add in fixture.go")
		mustSucceed(t, stdout, stderr, err)
		if run := loadDeliveryRun(t, repoDir); run.MR == nil || run.MR.State != "created" {
			t.Fatalf("mr = %+v, want the merge request", run.MR)
		}

		if push := readEnvDump(t, filepath.Join(r.remote, "push-env.log")); push["ORCH_DL11_PUSH_MARKER"] != "visible-to-the-push" || push["PATH"] == "" {
			t.Errorf("the push did not get the operator's environment: marker %q", push["ORCH_DL11_PUSH_MARKER"])
		}
		testEnv := readEnvDump(t, testEnvPath)
		if name := gitlabTokenName(testEnv, nil); name != "" {
			t.Errorf("the test command got %s", name)
		}
		if _, ok := testEnv["ORCH_DL11_PUSH_MARKER"]; ok {
			t.Error("the test command got a variable outside its allowlist")
		}
		providerEnv := readEnvDump(t, filepath.Join(envDir, "implement.env"))
		if name := gitlabTokenName(providerEnv, own); name != "" {
			t.Errorf("the provider CLI got %s, which the orchestrator did not start with", name)
		}
		if providerEnv["ORCH_DL11_PUSH_MARKER"] != "visible-to-the-push" {
			t.Error("the provider CLI lost the operator's environment, which only the test command gets narrowed")
		}
	})
	t.Run("the operator's token", func(t *testing.T) {
		repoDir, r, testEnvPath, _ := setup(t)
		// Not a real credential: the helper of a real CI job would read the variable.
		t.Setenv("GITLAB_TOKEN", "placeholder-for-the-credential-helper")

		stdout, stderr, err := runOrchestrator(t, repoDir, r.cfg, "run", "--stage", "development", "--stage", "testing", "fix Add in fixture.go")
		mustSucceed(t, stdout, stderr, err)

		if push := readEnvDump(t, filepath.Join(r.remote, "push-env.log")); push["GITLAB_TOKEN"] != "placeholder-for-the-credential-helper" {
			t.Errorf("the push did not get the operator's GITLAB_TOKEN")
		}
		if _, ok := readEnvDump(t, testEnvPath)["GITLAB_TOKEN"]; ok {
			t.Error("the test command, which runs code a model wrote, got GITLAB_TOKEN")
		}
		assertNotContains(t, "output", stdout+stderr, "placeholder-for-the-credential-helper")
		run, _ := runFile(t, repoDir, "run.json")
		assertNotContains(t, "run.json", run, "placeholder-for-the-credential-helper")
	})
}

// branchNameRun is a worktree run in a repository whose committed AGENTS.md has a rule for branch
// names, with worktree.branch.from_instructions routed to the fake claude and, when template is
// not empty, worktree.branch.template after it. proposeBranch says what the call answers; the
// file it returns holds the branch the fake implement ran on.
func branchNameRun(t *testing.T, template string) (repoDir string, r stageRun, branchFile string) {
	t.Helper()
	repoDir = fakeexec.InitGitFixture(t)
	if err := os.WriteFile(filepath.Join(repoDir, "AGENTS.md"), []byte("Branches are named <type>/<ticket>-<slug>.\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	gitRun(t, repoDir, "add", "AGENTS.md")
	gitRun(t, repoDir, "commit", "-qm", "Name the branches in AGENTS.md")
	yaml := "\nworktree:\n  branch:\n    from_instructions:\n      provider: claude\n      model: fake\n      timeout: 10s\n"
	if template != "" {
		yaml += "    template: " + yamlQuote(template) + "\n"
	}
	r, _, _ = newWorktreeRun(t, repoDir, func(o *configOptions) { o.extraYAML += yaml })
	branchFile = filepath.Join(t.TempDir(), "implement.branch")
	t.Setenv("FAKE_IMPLEMENT_BRANCH_FILE", branchFile)
	return repoDir, r, branchFile
}

// proposeBranch is the answer of the branch-name call.
func proposeBranch(t *testing.T, name string) {
	t.Helper()
	answer, err := json.Marshal(map[string]string{"branch": name})
	if err != nil {
		t.Fatal(err)
	}
	writeResponse(t, os.Getenv("FAKE_RESPONSES_DIR"), "branch-name", string(answer))
}

// 75: AGENTS.md names a format, the tool-less call proposes a branch in it, and the worktree is on
// that branch, the commit is on it and it is the branch that is pushed.
func TestDelivery75_BranchFromTheInstructions(t *testing.T) {
	repoDir, r, branchFile := branchNameRun(t, "")
	proposeBranch(t, "fix/PAY-12-x")

	stdout, stderr, err := runOrchestrator(t, repoDir, r.cfg, "run", "--stage", "development", "--stage", "testing", "fix Add in fixture.go")
	mustSucceed(t, stdout, stderr, err)

	const want = "fix/PAY-12-x"
	run := loadDeliveryRun(t, repoDir)
	if run.Status != "completed" || run.Worktree == nil || run.Worktree.Branch != want || run.Commit == "" {
		t.Fatalf("status = %s, worktree = %+v, commit = %q; want a completed run on %s", run.Status, run.Worktree, run.Commit, want)
	}
	if bn := run.BranchNaming; bn == nil || bn.Source != "instructions" || bn.Proposed != want || bn.Rejected != "" {
		t.Errorf("branch_naming = %+v, want the instructions' %s", bn, want)
	}
	if got := readTrimmed(t, branchFile); got != want {
		t.Errorf("implement ran on %q, want %s", got, want)
	}
	if got := remoteBranches(t, r.remote); got != want {
		t.Errorf("the remote has branches %q, want only %s", got, want)
	}
	if tip, err := gitOutput(r.remote, "rev-parse", want); err != nil || tip != run.Commit {
		t.Errorf("the remote's %s = %q (%v), want the run's commit %s", want, tip, err, run.Commit)
	}
	if mr := run.MR; mr == nil || mr.State != "created" || mr.Source != want {
		t.Errorf("mr = %+v, want created from %s", mr, want)
	}
	if got, _ := gitOutput(repoDir, "branch", "--list", "orchestrator/*"); got != "" {
		t.Errorf("the default branch was made as well: %s", got)
	}
	if got := invocations(t, r.invocationsLog); !equalStrings(got, []string{"branch-name", "implement"}) {
		t.Errorf("invocations = %v, want [branch-name implement]", got)
	}
	prompt, err := os.ReadFile(filepath.Join(r.promptsDir, "branch-name.txt"))
	if err != nil {
		t.Fatalf("branch-name prompt: %v", err)
	}
	assertContains(t, "the branch-name prompt", string(prompt), "Branches are named <type>/<ticket>-<slug>.", "fix Add in fixture.go")
	assertContains(t, "stdout", stdout, "Committed "+run.Commit[:12]+" to "+want+": ")
}

// 76: a proposal Go refuses (the base branch, a path out of the branch namespace, a branch that
// exists) is a warning, and the branch comes from the template; the record says what was proposed
// and why it was not used.
func TestDelivery76_BranchFromTheTemplateWhenTheProposalIsInvalid(t *testing.T) {
	cases := []struct {
		name string
		// proposal is "" for the base branch, whatever the fixture calls it.
		proposal, reason string
		exists           bool
	}{
		{"the base branch", "", "it is the base branch ", false},
		{"a path out of the namespace", "../x", "", false},
		{"an existing branch", "taken", "the branch taken exists", true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			repoDir, r, _ := branchNameRun(t, "feature/{slug}")
			proposal := tc.proposal
			if proposal == "" {
				proposal = currentBranch(t, repoDir)
			}
			proposeBranch(t, proposal)
			if tc.exists {
				gitRun(t, repoDir, "branch", proposal)
			}

			stdout, stderr, err := runOrchestrator(t, repoDir, r.cfg, "run", "--stage", "development", "--stage", "testing", "fix Add in fixture.go")
			mustSucceed(t, stdout, stderr, err)

			run := loadDeliveryRun(t, repoDir)
			if run.Worktree == nil || !strings.HasPrefix(run.Worktree.Branch, "feature/") || run.Worktree.Branch == proposal {
				t.Fatalf("worktree = %+v, want a branch of the template", run.Worktree)
			}
			bn := run.BranchNaming
			if bn == nil || bn.Source != "template" || bn.Proposed != proposal || bn.Rejected == "" || !strings.Contains(bn.Rejected, tc.reason) {
				t.Errorf("branch_naming = %+v, want template, proposed %q, rejected naming %q", bn, proposal, tc.reason)
			}
			assertContains(t, "stdout", stdout, "Warning: branch name from the instructions not used: ")
			if got := remoteBranches(t, r.remote); got != run.Worktree.Branch {
				t.Errorf("the remote has branches %q, want only %s", got, run.Worktree.Branch)
			}
			if mr := run.MR; mr == nil || mr.State != "created" || mr.Source != run.Worktree.Branch {
				t.Errorf("mr = %+v, want created from %s", mr, run.Worktree.Branch)
			}
		})
	}
}

// 77: a branch of that name already on the remote is somebody else's: nothing is pushed, the
// branch stays where it was, the commit stays on the local branch and the run needs a person.
func TestDelivery77_ABranchThatExistsOnTheRemoteIsNotPushedOver(t *testing.T) {
	repoDir, r, _ := branchNameRun(t, "")
	proposeBranch(t, "taken-on-remote")
	gitRun(t, repoDir, "push", "-q", r.remote, "HEAD:refs/heads/taken-on-remote")
	taken, err := gitOutput(r.remote, "rev-parse", "taken-on-remote")
	if err != nil {
		t.Fatal(err)
	}

	stdout, stderr, err := runOrchestrator(t, repoDir, r.cfg, "run", "--stage", "development", "--stage", "testing", "fix Add in fixture.go")
	if exitcode.FromError(err) != exitcode.NeedsPerson {
		t.Fatalf("exit code %d (%v), want %d\nstdout:\n%s\nstderr:\n%s", exitcode.FromError(err), err, exitcode.NeedsPerson, stdout, stderr)
	}

	run := loadDeliveryRun(t, repoDir)
	if run.Status != "completed" || run.Commit == "" || run.Worktree == nil || run.Worktree.Branch != "taken-on-remote" {
		t.Fatalf("status = %s, commit = %q, worktree = %+v; want completed and committed on taken-on-remote", run.Status, run.Commit, run.Worktree)
	}
	if mr := run.MR; mr == nil || mr.State != "" || !strings.Contains(mr.Error, "the branch taken-on-remote already exists on origin") || mr.URL != "" {
		t.Errorf("mr = %+v, want the error naming the branch and the remote, no push", mr)
	}
	if tip, _ := gitOutput(r.remote, "rev-parse", "taken-on-remote"); tip != taken {
		t.Errorf("the remote's branch moved from %s to %s", taken, tip)
	}
	if got := remoteBranches(t, r.remote); got != "taken-on-remote" {
		t.Errorf("the remote has branches %q, want only the one that was there", got)
	}
	if options := fakeexec.GitLabPushOptions(t, r.remote); len(options) != 0 {
		t.Errorf("the remote received push options %q", options)
	}
	if tip, err := gitOutput(repoDir, "rev-parse", "taken-on-remote"); err != nil || tip != run.Commit {
		t.Errorf("the local branch = %q (%v), want the run's commit %s", tip, err, run.Commit)
	}
	assertContains(t, "stdout", stdout, "already exists on origin")
}

// 78: the push is the story's to turn off (story.push, specs/delivery-push): delivery.push is
// not a key, and a run's config that names it is refused before anything runs.
func TestDelivery78_DeliveryPushIsNotAKey(t *testing.T) {
	repoDir := fakeexec.InitGitFixture(t)
	r, _, _ := newWorktreeRun(t, repoDir, func(o *configOptions) { o.extraYAML += "delivery:\n  push: false\n" })
	_, _, err := runOrchestrator(t, repoDir, r.cfg, "run", "--mode", "auto", "--stage", "development", "--stage", "testing", "fix Add in fixture.go")
	if err == nil || !strings.Contains(err.Error(), "push") {
		t.Fatalf("err = %v, want the unknown key push refused", err)
	}
}

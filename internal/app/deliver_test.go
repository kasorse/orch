package app

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/kasorse/orch/internal/apperr"
	"github.com/kasorse/orch/internal/domain"
	"github.com/kasorse/orch/internal/exitcode"
	"github.com/kasorse/orch/internal/interaction"
	"github.com/kasorse/orch/internal/report"
	"github.com/kasorse/orch/internal/testutil/fakeexec"
)

const gitLabAnswer = "remote: \n" +
	"remote: View merge request for orchestrator/r1:        \n" +
	"remote:   https://gitlab.com/group/sub/proj/-/merge_requests/12        \n" +
	"remote: \n"

func TestMergeRequestURL(t *testing.T) {
	const remote = "https://gitlab.com/group/sub/proj.git"
	cases := []struct {
		name, stderr, remote string
		url                  string
		iid                  int
	}{
		{"gitlab's answer", gitLabAnswer, remote, "https://gitlab.com/group/sub/proj/-/merge_requests/12", 12},
		{"no remote: prefix", "View merge request for b:\n  https://gitlab.com/g/p/-/merge_requests/3\n", remote, "https://gitlab.com/g/p/-/merge_requests/3", 3},
		{"userinfo in the remote is dropped", gitLabAnswer, "https://oauth2:glpat-secret@gitlab.com/group/sub/proj.git", "https://gitlab.com/group/sub/proj/-/merge_requests/12", 12},
		{"scp remote", gitLabAnswer, "git@gitlab.com:group/sub/proj.git", "https://gitlab.com/group/sub/proj/-/merge_requests/12", 12},
		{"ssh remote with a port", gitLabAnswer, "ssh://git@gitlab.com:2222/group/sub/proj.git", "https://gitlab.com/group/sub/proj/-/merge_requests/12", 12},
		{"the host is compared without case", gitLabAnswer, "https://GitLab.com/group/sub/proj.git", "https://gitlab.com/group/sub/proj/-/merge_requests/12", 12},
		{"the create link comes first", "remote: To create a merge request for b, visit:\n" +
			"remote:   https://gitlab.com/g/p/-/merge_requests/new?merge_request%5Bsource_branch%5D=b\n" + gitLabAnswer, remote,
			"https://gitlab.com/group/sub/proj/-/merge_requests/12", 12},
		{"only the create link", "remote: To create a merge request for b, visit:\n" +
			"remote:   https://gitlab.com/g/p/-/merge_requests/new?merge_request%5Bsource_branch%5D=b\n", remote, "", 0},
		{"another host", strings.Replace(gitLabAnswer, "gitlab.com", "evil.example.com", 1), remote, "", 0},
		{"a lookalike host", strings.Replace(gitLabAnswer, "gitlab.com", "gitlab.com.evil.example", 1), remote, "", 0},
		{"userinfo in the answer", strings.Replace(gitLabAnswer, "https://", "https://x:y@", 1), remote, "", 0},
		{"http", strings.Replace(gitLabAnswer, "https://", "http://", 1), remote, "", 0},
		{"a query", strings.Replace(gitLabAnswer, "/12", "/12?x=1", 1), remote, "", 0},
		{"trailing text", strings.Replace(gitLabAnswer, "merge_requests/12", "merge_requests/12 and more", 1), remote, "", 0},
		{"not a number", strings.Replace(gitLabAnswer, "/12", "/12a", 1), remote, "", 0},
		{"no project path", strings.Replace(gitLabAnswer, "/group/sub/proj/-/", "/-/", 1), remote, "", 0},
		{"no marker", "remote:   https://gitlab.com/g/p/-/merge_requests/3\n", remote, "", 0},
		{"the URL before the marker", "remote: https://gitlab.com/g/p/-/merge_requests/3\n" + "remote: View merge request for b:\n", remote, "", 0},
		{"a remote without a host", gitLabAnswer, "/srv/git/proj.git", "", 0},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			url, iid, ok := mergeRequestURL(c.stderr, c.remote)
			if url != c.url || iid != c.iid || ok != (c.url != "") {
				t.Errorf("mergeRequestURL = %q, %d, %v; want %q, %d", url, iid, ok, c.url, c.iid)
			}
		})
	}
}

func TestCreateLink(t *testing.T) {
	const stderr = "remote: \nremote: To create a merge request for b, visit:\nremote:   https://gitlab.com/g/p/-/merge_requests/new?merge_request%5Bsource_branch%5D=b\nremote: \n"
	if got, want := createLink(stderr, "https://gitlab.com/g/p.git"), "https://gitlab.com/g/p/-/merge_requests/new?merge_request%5Bsource_branch%5D=b"; got != want {
		t.Errorf("createLink = %q, want %q", got, want)
	}
	if got := createLink(stderr, "https://other.example.com/g/p.git"); got != "" {
		t.Errorf("createLink for another host = %q", got)
	}
}

// A push option never holds a line feed or a NUL: line breaks are the two characters \n, a
// carriage return is dropped.
func TestPushOptionText(t *testing.T) {
	cases := map[string]string{
		"a\nb":         `a\nb`,
		"a\r\nb\r\n":   `a\nb\n`,
		"a\rb":         "ab",
		"a\x00b":       "ab",
		"\n\n":         `\n\n`,
		`already \n a`: `already \n a`,
		"":             "",
	}
	for in, want := range cases {
		got := pushOptionText(in)
		if got != want {
			t.Errorf("pushOptionText(%q) = %q, want %q", in, got, want)
		}
		if strings.ContainsAny(got, "\n\x00") {
			t.Errorf("pushOptionText(%q) = %q holds a LF or a NUL", in, got)
		}
	}
}

// The description option is cut to fit one push option once encoded: the report is rendered
// smaller, and in the end cut at a line.
func TestMergeRequestDescription_FitsOneOption(t *testing.T) {
	s, _, _ := finishedWorktreeRun(t, domain.ModeAuto, nil, nil)
	ctx := context.Background()
	sha := offerCommit(ctx, s, false)
	if sha == "" {
		t.Fatal("no commit")
	}
	s.run.Task.Description = "fix it\n" + strings.Repeat("a long line of the task\n", 30)

	full := mergeRequestDescription(*s.run, nil, "", maxPushOptionBytes)
	if len(descriptionOption)+len(full) > maxPushOptionBytes || strings.ContainsAny(full, "\n\x00") || !strings.HasPrefix(full, `## fix it\n\n`) ||
		strings.Contains(full, "…(cut)") {
		t.Errorf("description is %d bytes: %q", len(full), full)
	}
	if want := pushOptionText(report.RunReport(*s.run, nil)); full != want {
		t.Errorf("a report that fits is not report.md encoded\n%q\n%q", full, want)
	}

	// The report is rendered again with a shorter task quote, then cut at a line.
	for _, limit := range []int{len(descriptionOption) + len(full) - 1, len(descriptionOption) + len(full)/2, len(descriptionOption) + 150} {
		got := mergeRequestDescription(*s.run, nil, "", limit)
		if len(descriptionOption)+len(got) > limit || strings.ContainsAny(got, "\n\x00") || !strings.HasPrefix(got, `## fix it\n`) {
			t.Errorf("limit %d: description is %d bytes: %q", limit, len(descriptionOption)+len(got), got)
		}
	}
	if got := mergeRequestDescription(*s.run, nil, "", len(descriptionOption)+150); !strings.HasSuffix(got, `\n…(cut)\n`) {
		t.Errorf("description %q is not cut with a mark", got)
	}
}

func TestMergeRequestOptions_TitleIsBounded(t *testing.T) {
	s, _, _ := finishedWorktreeRun(t, domain.ModeAuto, nil, nil)
	ctx := context.Background()
	sha := offerCommit(ctx, s, false)
	if sha == "" {
		t.Fatal("no commit")
	}
	gitIn(t, s.worktree.Path, "commit", "--allow-empty", "-qm", strings.Repeat("é", 200))
	options := mergeRequestOptions(ctx, s, gitIn(t, s.worktree.Path, "rev-parse", "HEAD"), "main")
	if title := strings.TrimPrefix(options[2], "merge_request.title="); len(title) > maxMRTitleBytes || !strings.HasPrefix(title, "éé") || strings.Contains(title, "\ufffd") {
		t.Errorf("title is %d bytes: %q", len(title), title)
	}
}

func TestPushFailure(t *testing.T) {
	cases := []struct{ name, stderr, want string }{
		{"no push options", "fatal: the receiving end does not support push options\n", "fatal: the receiving end does not support push options"},
		{"rejected", "remote: error: nope\nTo https://u:pw@gitlab.com/g/p.git\n ! [remote rejected] b -> b (pre-receive hook declined)\nerror: failed to push some refs to 'https://u:pw@gitlab.com/g/p.git'\nhint: x\n",
			"! [remote rejected] b -> b (pre-receive hook declined)"},
		{"only the failure line", "error: failed to push some refs to 'https://u:pw@gitlab.com/g/p.git'\n", "error: failed to push some refs to 'https://gitlab.com/g/p.git'"},
		{"nothing", "", "failed to run git push"},
	}
	for _, c := range cases {
		got := pushFailure(c.stderr, errors.New("failed to run git push"))
		if got != c.want || strings.Contains(got, "pw@") {
			t.Errorf("%s: pushFailure = %q, want %q", c.name, got, c.want)
		}
	}
	if got := pushFailure(strings.Repeat("é", 400), errors.New("x")); len(got) > maxMRRecordBytes || strings.Contains(got, "\ufffd") {
		t.Errorf("a long line is %d bytes: %q", len(got), got)
	}
}

// remoteDir is the bare repository origin names, made by fakeexec.AddGitLabRemote.
func remoteDir(t *testing.T, dir string) string {
	t.Helper()
	return strings.TrimPrefix(gitIn(t, dir, "config", "remote.origin.url"), "file://"+fakeexec.GitLabHost)
}

func savedMR(t *testing.T, s *session) *domain.RunMR {
	t.Helper()
	saved, err := s.store.LoadRun(s.run.ID)
	if err != nil {
		t.Fatal(err)
	}
	if saved.Status != domain.RunCompleted {
		t.Errorf("status = %s, want completed: delivery never changes it", saved.Status)
	}
	return saved.MR
}

// Under --mode auto nothing is asked: the branch is pushed with the merge request's options,
// the URL and number are recorded, and the worktree is removed.
func TestDeliver_AutoOpensTheMergeRequest(t *testing.T) {
	s, dir, out := finishedWorktreeRun(t, domain.ModeAuto, nil, nil)
	ctx := context.Background()
	sha := offerCommit(ctx, s, false)
	if sha == "" {
		t.Fatalf("no commit:\n%s", out)
	}
	description := report.RunReportFit(*s.run, loadTrace(s.store, s.run.ID), func(string) bool { return true })

	if err := finishWorktree(ctx, s, sha); err != nil {
		t.Fatalf("finishWorktree = %v\n%s", err, out)
	}
	remote := remoteDir(t, dir)
	if got := gitIn(t, remote, "rev-parse", s.worktree.Branch); got != sha {
		t.Errorf("the remote's %s = %s, want %s", s.worktree.Branch, got, sha)
	}
	got := fakeexec.GitLabPushOptions(t, remote)
	want := []string{"merge_request.create", "merge_request.target=main", "merge_request.title=Add main",
		"merge_request.description=" + strings.ReplaceAll(description, "\n", `\n`)}
	if strings.Join(got, "\n") != strings.Join(want, "\n") {
		t.Errorf("the remote received\n%q\nwant\n%q", got, want)
	}
	if mr := savedMR(t, s); mr == nil || mr.State != domain.MRCreated || mr.IID != 1 || mr.URL != "https://gitlab.test/group/project/-/merge_requests/1" ||
		mr.Remote != "origin" || mr.Source != s.worktree.Branch || mr.Target != "main" || mr.Skipped != "" || mr.Error != "" {
		t.Errorf("mr = %+v", mr)
	}
	if !s.worktree.Removed {
		t.Error("the worktree was not removed")
	}
	if upstream := gitIn(t, dir, "config", "branch."+s.worktree.Branch+".remote"); upstream != "origin" {
		t.Errorf("the upstream of %s = %q, want origin: the push is -u", s.worktree.Branch, upstream)
	}
}

// Any other mode asks, in order: yes to the commit, yes to the push, no to the merge request is a
// push without options.
func TestDeliver_DecliningTheMergeRequestPushesWithoutOptions(t *testing.T) {
	s, dir, out := finishedWorktreeRun(t, domain.ModeAcceptEdits, nil, nil)
	s.prompter = interaction.NewTerminalPrompter(strings.NewReader("y\ny\nn\n"), out)
	ctx := context.Background()
	sha := offerCommit(ctx, s, true)
	if sha == "" {
		t.Fatalf("no commit:\n%s", out)
	}
	if err := finishWorktree(ctx, s, sha); err != nil {
		t.Fatalf("finishWorktree = %v\n%s", err, out)
	}
	for _, want := range []string{"Push " + s.worktree.Branch + " to origin? [y]es / [N]o: ",
		"Open a merge request " + s.worktree.Branch + " → main? [y]es / [N]o: ", "Pushed " + s.worktree.Branch + " to origin.\n"} {
		if !strings.Contains(out.String(), want) {
			t.Errorf("output lacks %q:\n%s", want, out)
		}
	}
	remote := remoteDir(t, dir)
	if got := gitIn(t, remote, "rev-parse", s.worktree.Branch); got != sha {
		t.Errorf("the remote's %s = %s, want %s", s.worktree.Branch, got, sha)
	}
	if got := fakeexec.GitLabPushOptions(t, remote); len(got) != 0 {
		t.Errorf("the remote received push options %q", got)
	}
	want := domain.RunMR{State: domain.MRPushed, Remote: "origin", Source: s.worktree.Branch, Target: "main", Skipped: "declined at merge request"}
	if mr := savedMR(t, s); mr == nil || *mr != want {
		t.Errorf("mr = %+v, want %+v", mr, want)
	}
	if !s.worktree.Removed {
		t.Error("the worktree was not removed")
	}
}

// A no to the push stops there: the merge request is not asked, nothing is pushed, the exit is 0
// and the worktree is removed all the same (the commit succeeded).
func TestDeliver_DecliningThePush(t *testing.T) {
	s, dir, out := finishedWorktreeRun(t, domain.ModeAcceptEdits, nil, nil)
	s.prompter = interaction.NewTerminalPrompter(strings.NewReader("y\n\n"), out)
	ctx := context.Background()
	sha := offerCommit(ctx, s, true)
	if err := finishWorktree(ctx, s, sha); err != nil {
		t.Fatalf("finishWorktree = %v\n%s", err, out)
	}
	if strings.Contains(out.String(), "Open a merge request") {
		t.Errorf("the merge request was asked after a no:\n%s", out)
	}
	if got := gitIn(t, dir, "ls-remote", "origin"); got != "" {
		t.Errorf("the remote has refs:\n%s", got)
	}
	if mr := savedMR(t, s); mr == nil || *mr != (domain.RunMR{Skipped: "declined at push"}) {
		t.Errorf("mr = %+v", mr)
	}
	if !s.worktree.Removed {
		t.Error("the worktree was not removed")
	}
}

// A no to the commit: nothing is pushed or asked, the worktree stays with its changes, and the
// exit is 0 - the person decided.
func TestDeliver_DecliningTheCommit(t *testing.T) {
	s, dir, out := finishedWorktreeRun(t, domain.ModeAcceptEdits, nil, nil)
	s.prompter = interaction.NewTerminalPrompter(strings.NewReader("n\n"), out)
	ctx := context.Background()
	sha := offerCommit(ctx, s, true)
	if sha != "" {
		t.Fatalf("committed %s", sha)
	}
	if err := finishWorktree(ctx, s, sha); err != nil {
		t.Fatalf("finishWorktree = %v, want nil: a no is not a failure\n%s", err, out)
	}
	if strings.Contains(out.String(), "Push ") || strings.Contains(out.String(), "No push and no merge request") {
		t.Errorf("output after a no to the commit:\n%s", out)
	}
	if got := gitIn(t, dir, "ls-remote", "origin"); got != "" {
		t.Errorf("the remote has refs:\n%s", got)
	}
	if _, err := os.Stat(s.worktree.Path); err != nil || s.worktree.Removed {
		t.Errorf("the worktree: %v, removed %v; want kept", err, s.worktree.Removed)
	}
	if mr := savedMR(t, s); mr == nil || *mr != (domain.RunMR{Skipped: "declined at commit"}) {
		t.Errorf("mr = %+v", mr)
	}
}

// Without a terminal to ask on, a mode that asks commits nothing and needs a person.
func TestDeliver_NoTerminalToAsk(t *testing.T) {
	s, _, out := finishedWorktreeRun(t, domain.ModeAcceptEdits, nil, nil)
	ctx := context.Background()
	sha := offerCommit(ctx, s, false)
	err := finishWorktree(ctx, s, sha)
	if sha != "" || exitcode.FromError(err) != exitcode.NeedsPerson {
		t.Errorf("commit %q, finishWorktree = %v; want none and exit 6\n%s", sha, err, out)
	}
	if !strings.Contains(out.String(), "there is no terminal to ask on") {
		t.Errorf("output:\n%s", out)
	}
}

// A remote that takes no push options refuses the push: the error and the command to push by
// hand are shown, the intent stays, the exit is 6, and the push is not repeated.
func TestDeliver_PushOptionsRefused(t *testing.T) {
	s, dir, out := finishedWorktreeRun(t, domain.ModeAuto, nil, nil)
	gitIn(t, remoteDir(t, dir), "config", "receive.advertisePushOptions", "false")
	ctx := context.Background()
	sha := offerCommit(ctx, s, false)
	err := finishWorktree(ctx, s, sha)

	var person *apperr.NeedsPersonError
	if !errors.As(err, &person) || exitcode.FromError(err) != exitcode.NeedsPerson {
		t.Fatalf("finishWorktree = %v, want a NeedsPersonError\n%s", err, out)
	}
	for _, want := range []string{"Error: fatal: the receiving end does not support push options\n", "To push it yourself: git push -u origin " + s.worktree.Branch + "\n"} {
		if !strings.Contains(out.String(), want) {
			t.Errorf("output lacks %q:\n%s", want, out)
		}
	}
	want := domain.RunMR{State: domain.MRIntent, Remote: "origin", Source: s.worktree.Branch, Target: "main", Error: "fatal: the receiving end does not support push options"}
	if mr := savedMR(t, s); mr == nil || *mr != want {
		t.Errorf("mr = %+v, want %+v", mr, want)
	}
	if got := gitIn(t, dir, "ls-remote", "origin"); got != "" {
		t.Errorf("the remote has refs:\n%s", got)
	}
	if !s.worktree.Removed {
		t.Error("the worktree stays after a commit: the branch holds the result")
	}
}

// A branch of that name on the remote - even an ancestor of the commit - is not pushed over.
func TestDeliver_BranchAlreadyOnTheRemote(t *testing.T) {
	s, dir, out := finishedWorktreeRun(t, domain.ModeAuto, nil, nil)
	ctx := context.Background()
	gitIn(t, dir, "push", "-q", "origin", "HEAD:refs/heads/"+s.worktree.Branch)
	sha := offerCommit(ctx, s, false)
	err := finishWorktree(ctx, s, sha)

	if exitcode.FromError(err) != exitcode.NeedsPerson {
		t.Fatalf("finishWorktree = %v, want exit 6\n%s", err, out)
	}
	if got := gitIn(t, remoteDir(t, dir), "rev-parse", s.worktree.Branch); got != s.worktree.BaseRef {
		t.Errorf("the remote's %s = %s, want it untouched at %s", s.worktree.Branch, got, s.worktree.BaseRef)
	}
	mr := savedMR(t, s)
	if mr == nil || mr.State != "" || !strings.Contains(mr.Error, "already exists on origin") {
		t.Errorf("mr = %+v", mr)
	}
	if got := fakeexec.GitLabPushOptions(t, remoteDir(t, dir)); len(got) != 0 {
		t.Errorf("a push reached the remote: %q", got)
	}
}

// A remote that cannot be asked is an error before the push, not a push.
func TestDeliver_UnreachableRemote(t *testing.T) {
	s, _, out := finishedWorktreeRun(t, domain.ModeAuto, nil, func(dir string) {
		gitIn(t, dir, "remote", "set-url", "origin", "file://"+fakeexec.GitLabHost+"/nonexistent/remote.git")
	})
	ctx := context.Background()
	sha := offerCommit(ctx, s, false)
	err := finishWorktree(ctx, s, sha)
	if exitcode.FromError(err) != exitcode.NeedsPerson {
		t.Fatalf("finishWorktree = %v, want exit 6\n%s", err, out)
	}
	if mr := savedMR(t, s); mr == nil || mr.State != "" || !strings.Contains(mr.Error, "cannot ask origin whether it has") {
		t.Errorf("mr = %+v", mr)
	}
}

// A push the remote accepts without naming a merge request is pushed, with the error and exit 6.
func TestDeliver_NoMergeRequestInTheAnswer(t *testing.T) {
	s, dir, out := finishedWorktreeRun(t, domain.ModeAuto, nil, nil)
	hook := remoteDir(t, dir) + "/hooks/post-receive"
	if err := os.WriteFile(hook, []byte("#!/bin/sh\nexit 0\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	sha := offerCommit(ctx, s, false)
	err := finishWorktree(ctx, s, sha)
	if exitcode.FromError(err) != exitcode.NeedsPerson {
		t.Fatalf("finishWorktree = %v, want exit 6\n%s", err, out)
	}
	want := domain.RunMR{State: domain.MRPushed, Remote: "origin", Source: s.worktree.Branch, Target: "main", Error: "the remote reported no merge request"}
	if mr := savedMR(t, s); mr == nil || *mr != want {
		t.Errorf("mr = %+v, want %+v", mr, want)
	}
	if got := gitIn(t, remoteDir(t, dir), "rev-parse", s.worktree.Branch); got != sha {
		t.Errorf("the remote's %s = %s, want the pushed %s", s.worktree.Branch, got, sha)
	}
}

// push: false recorded in the run (specs/delivery-push §2): after the commit nothing is
// asked or pushed, mr.skipped says why, the worktree is removed, and the exit is 0.
func TestDeliver_PushDisabled(t *testing.T) {
	for _, mode := range []domain.RunMode{domain.ModeAuto, domain.ModeAcceptEdits} {
		t.Run(string(mode), func(t *testing.T) {
			s, dir, out := finishedWorktreeRun(t, mode, nil, nil)
			s.run.Options.Push = new(bool)
			s.prompter = interaction.NewTerminalPrompter(strings.NewReader("y\n"), out)
			ctx := context.Background()
			sha := offerCommit(ctx, s, mode != domain.ModeAuto)
			if sha == "" {
				t.Fatalf("no commit:\n%s", out)
			}
			if err := finishWorktree(ctx, s, sha); err != nil {
				t.Fatalf("finishWorktree = %v, want nil (exit 0)\n%s", err, out)
			}
			for _, unwanted := range []string{"Push " + s.worktree.Branch + " to origin?", "Open a merge request"} {
				if strings.Contains(out.String(), unwanted) {
					t.Errorf("asked %q:\n%s", unwanted, out)
				}
			}
			want := "Push disabled (story.push: false); the branch " + s.worktree.Branch + " stays in this repository.\nTo publish it: git push origin " + s.worktree.Branch + "\n"
			if !strings.Contains(out.String(), want) {
				t.Errorf("output lacks %q:\n%s", want, out)
			}
			if got := gitIn(t, dir, "ls-remote", "origin"); got != "" {
				t.Errorf("the remote has refs:\n%s", got)
			}
			if mr := savedMR(t, s); mr == nil || *mr != (domain.RunMR{Skipped: domain.MRSkippedPushDisabled}) {
				t.Errorf("mr = %+v", mr)
			}
			if !s.worktree.Removed {
				t.Error("the worktree was not removed")
			}
			if got := gitIn(t, dir, "rev-parse", s.worktree.Branch); got != sha {
				t.Errorf("the branch %s = %s, want %s", s.worktree.Branch, got, sha)
			}
		})
	}
}

// The merge request's title is the commit's summary in the instructions' pattern, else
// "<KEY>: <summary>" for a task with a Jira key, whatever type the commit header led with; else
// the commit subject.
func TestMergeRequestTitle(t *testing.T) {
	for _, c := range []struct{ subject, key, pattern, want string }{
		{"fix: PMC-2924 Run nightly price import", "PMC-2924", "", "PMC-2924: Run nightly price import"},
		{"PMC-2924: Run nightly price import", "PMC-2924", "", "PMC-2924: Run nightly price import"},
		{"feat: Add a thing", "PAY-1", "", "PAY-1: Add a thing"},
		{"Typed by hand: yes", "PAY-1", "", "PAY-1: Typed by hand: yes"},
		{"PAY-1", "PAY-1", "", "PAY-1"},
		{"fix: Add main", "", "", "fix: Add main"},
		{"fix: PAY-1 Add a thing", "PAY-1", "[{key}] {summary}", "[PAY-1] Add a thing"},
		{"fix: Add main", "", "{summary} (bot)", "Add main (bot)"},
		{"fix: Add main", "", "{key}: {summary}", "fix: Add main"},
	} {
		if got := mergeRequestTitle(c.subject, c.key, c.pattern); got != c.want {
			t.Errorf("mergeRequestTitle(%q, %q, %q) = %q, want %q", c.subject, c.key, c.pattern, got, c.want)
		}
	}
}

// The project's merge request template, as the base commit holds it, follows report.md in the
// description: GitLab runs its /assign_reviewer line when it opens the merge request.
func TestDeliver_DescriptionEndsWithTheTemplate(t *testing.T) {
	const template = "/assign_reviewer @alice @bob"
	s, dir, out := finishedWorktreeRun(t, domain.ModeAuto, nil, func(dir string) {
		if err := os.MkdirAll(filepath.Join(dir, ".gitlab", "merge_request_templates"), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(dir, filepath.FromSlash(mrTemplatePath)), []byte(template+"\n"), 0o644); err != nil {
			t.Fatal(err)
		}
		gitIn(t, dir, "add", ".gitlab")
		gitIn(t, dir, "commit", "-qm", "the template")
	})
	ctx := context.Background()
	sha := offerCommit(ctx, s, false)
	if sha == "" {
		t.Fatalf("no commit:\n%s", out)
	}
	if err := finishWorktree(ctx, s, sha); err != nil {
		t.Fatalf("finishWorktree = %v\n%s", err, out)
	}
	got := fakeexec.GitLabPushOptions(t, remoteDir(t, dir))
	if len(got) != 4 || !strings.HasSuffix(got[3], `\n\n`+template+`\n`) {
		t.Errorf("the description does not end with the template: %q", got)
	}
}

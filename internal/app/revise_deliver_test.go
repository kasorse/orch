package app

import (
	"bytes"
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/kasorse/orch/internal/config"
	"github.com/kasorse/orch/internal/domain"
	"github.com/kasorse/orch/internal/exitcode"
	"github.com/kasorse/orch/internal/interaction"
	"github.com/kasorse/orch/internal/jira"
	"github.com/kasorse/orch/internal/testutil/fakeexec"
)

const reviseMRURL = "https://gitlab.test/group/project/-/merge_requests/7"

// finishedReviseRun is a revise update of !7 whose implement step changed main.go in the
// worktree on fix/x, completed under mode.
func finishedReviseRun(t *testing.T, mode domain.RunMode) (*session, string, string, *bytes.Buffer) {
	t.Helper()
	ctx := context.Background()
	s, dir, _, fixSHA, out := reviseSession(t)
	r := s.run.Task.Source.Revise
	r.MR = &domain.MergeRequest{Project: "acme/billing", IID: 7, URL: reviseMRURL, State: domain.MRStateOpened,
		SourceBranch: "fix/x", TargetBranch: "main", SHA: fixSHA, Title: "Fix x"}
	s.run.Task.Description = "Round half up" + reviseMarker + "merge request !7"
	s.run.Workflow = &domain.Workflow{Steps: []domain.Step{{Action: domain.ActionImplement}, {Action: domain.ActionReview}}}
	if err := enterWorktree(ctx, s); err != nil {
		t.Fatal(err)
	}
	run := s.run
	run.PreRunRef = s.worktree.BaseRef
	if err := recordBaseline(ctx, s.repo, run, s.newRepoGuard(s.protected)); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(s.repo.Dir, "main.go"), []byte("package main\n\nfunc main() {}\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	run.Status, run.Mode = domain.RunCompleted, mode
	run.Executions = []domain.StepExecution{{Index: 0, Action: domain.ActionImplement, Status: domain.StepCompleted}}
	if err := s.dispatcher.Restore(run, memArtifacts{0: `{"summary":"Round half up","summary_short":"Round half up","changed_files":null,"tests_added":null}`}); err != nil {
		t.Fatal(err)
	}
	out.Reset()
	return s, dir, fixSHA, out
}

func TestDeliver_ReviseUpdatePushesToTheMergeRequestsBranch(t *testing.T) {
	s, dir, fixSHA, out := finishedReviseRun(t, domain.ModeAuto)
	ctx := context.Background()
	sha := offerCommit(ctx, s, false)
	if sha == "" {
		t.Fatalf("no commit:\n%s", out)
	}
	if err := finishWorktree(ctx, s, sha); err != nil {
		t.Fatalf("finishWorktree = %v\n%s", err, out)
	}
	remote := remoteDir(t, dir)
	if got := gitIn(t, remote, "rev-parse", "fix/x"); got != sha {
		t.Fatalf("the remote's fix/x = %s, want %s", got, sha)
	}
	if got := gitIn(t, remote, "rev-parse", sha+"^"); got != fixSHA {
		t.Errorf("the new commit's parent = %s, want the merge request's tip %s", got, fixSHA)
	}
	if got := fakeexec.GitLabPushOptions(t, remote); len(got) != 0 {
		t.Errorf("push options sent: %q", got)
	}
	if msg := gitIn(t, dir, "log", "-1", "--format=%B", sha); !strings.Contains(msg, "Orchestrator-Revises: !7") {
		t.Errorf("commit message = %q", msg)
	}
	mr := savedMR(t, s)
	if mr == nil || mr.State != domain.MRUpdated || mr.IID != 7 || mr.URL != reviseMRURL || mr.Source != "fix/x" {
		t.Fatalf("mr = %+v", mr)
	}
	if !strings.Contains(out.String(), "Merge request !7 updated: "+reviseMRURL) {
		t.Errorf("output:\n%s", out)
	}
	if err := exec.Command("git", "-C", dir, "config", "branch.fix/x.remote").Run(); err == nil {
		t.Error("an upstream was written for the merge request's branch")
	}
	if !s.worktree.Removed {
		t.Error("the worktree was not removed")
	}
	if got := buildResult(&resultRecorder{run: s.run, store: s.store}, nil).MR; got == nil || got.IID != 7 {
		t.Errorf("--output json mr = %+v", got)
	}
}

func TestDeliver_ReviseUpdateRefusesAMovedBranch(t *testing.T) {
	s, dir, _, out := finishedReviseRun(t, domain.ModeAuto)
	ctx := context.Background()
	// Somebody pushes to the merge request's branch while the run worked.
	other := t.TempDir()
	gitIn(t, other, "clone", "-q", gitIn(t, dir, "config", "remote.origin.url"), ".")
	gitIn(t, other, "config", "user.email", "o@example.com")
	gitIn(t, other, "config", "user.name", "O")
	gitIn(t, other, "checkout", "-q", "fix/x")
	if err := os.WriteFile(filepath.Join(other, "theirs.go"), []byte("package main\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	gitIn(t, other, "add", "-A")
	gitIn(t, other, "commit", "-qm", "theirs")
	gitIn(t, other, "push", "-q", "origin", "fix/x")
	theirs := gitIn(t, other, "rev-parse", "HEAD")

	sha := offerCommit(ctx, s, false)
	err := finishWorktree(ctx, s, sha)
	if exitcode.FromError(err) != exitcode.NeedsPerson {
		t.Fatalf("finishWorktree = %v, want exit 6\n%s", err, out)
	}
	if got := gitIn(t, remoteDir(t, dir), "rev-parse", "fix/x"); got != theirs {
		t.Errorf("the remote's fix/x = %s, want theirs %s untouched", got, theirs)
	}
	if mr := savedMR(t, s); mr == nil || mr.State != "" || !strings.Contains(mr.Error, "somebody pushed") {
		t.Errorf("mr = %+v", mr)
	}
}

func TestDeliver_ReviseUpdateAsksNoMergeRequestQuestion(t *testing.T) {
	s, dir, _, out := finishedReviseRun(t, domain.ModeAcceptEdits)
	// Yes to the commit and to the push; a third question would read EOF and fail the push.
	s.prompter = interaction.NewTerminalPrompter(strings.NewReader("y\ny\n"), out)
	ctx := context.Background()
	sha := offerCommit(ctx, s, true)
	if err := finishWorktree(ctx, s, sha); err != nil {
		t.Fatalf("finishWorktree = %v\n%s", err, out)
	}
	if strings.Contains(out.String(), "Open a merge request") || !strings.Contains(out.String(), "This push updates !7") {
		t.Errorf("output:\n%s", out)
	}
	if got := gitIn(t, remoteDir(t, dir), "rev-parse", "fix/x"); got != sha {
		t.Errorf("the remote's fix/x = %s, want %s", got, sha)
	}
}

func TestWriteReviseDescription(t *testing.T) {
	var mu sync.Mutex
	var puts []string
	gets := 0
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		defer mu.Unlock()
		switch r.Method {
		case http.MethodGet:
			gets++
			_, _ = io.WriteString(w, `{"fields":{"description":"Totals are wrong."}}`)
		case http.MethodPut:
			b, _ := io.ReadAll(r.Body)
			puts = append(puts, string(b))
			w.WriteHeader(http.StatusNoContent)
		}
	}))
	defer srv.Close()
	t.Setenv("REVISE_JIRA_EMAIL", "me@example.com")
	t.Setenv("REVISE_JIRA_TOKEN", "tok")

	s, _, _, out := finishedReviseRun(t, domain.ModeAuto)
	s.cfg.Jira = &config.JiraConfig{BaseURL: srv.URL, Auth: jira.AuthBasic, EmailEnv: "REVISE_JIRA_EMAIL", TokenEnv: "REVISE_JIRA_TOKEN", Timeout: 5 * time.Second}
	s.run.Task.Source.Key = "PAY-31"
	s.run.Task.CreatedAt = time.Date(2026, 11, 11, 9, 0, 0, 0, time.UTC)
	s.run.Commit = strings.Repeat("d", 40)
	s.run.MR = &domain.RunMR{State: domain.MRUpdated, Remote: "origin", Source: "fix/x", Target: "main", IID: 7, URL: reviseMRURL}

	writeReviseDescription(context.Background(), s)
	if len(puts) != 1 || puts[0] != `{"fields":{"description":"Totals are wrong.\n\nUpdate 2026-11-11: Round half up"}}` {
		t.Fatalf("puts = %q\n%s", puts, out)
	}
	if d := s.run.JiraWrite.Description; d == nil || d.State != domain.JiraCommentDone {
		t.Fatalf("jira_write = %+v", s.run.JiraWrite)
	}
	// Never twice: a second call (a resume) finds the record and sends nothing.
	writeReviseDescription(context.Background(), s)
	if len(puts) != 1 || gets != 1 {
		t.Fatalf("written again: %d puts, %d gets", len(puts), gets)
	}
	if _, err := s.store.LoadRun(s.run.ID); err != nil {
		t.Fatalf("run.json with the description record does not load: %v", err)
	}
}

func TestWriteReviseDescription_NotWithoutAPush(t *testing.T) {
	s, _, _, _ := finishedReviseRun(t, domain.ModeAuto)
	s.run.Task.Source.Key = "PAY-31"
	s.run.MR = &domain.RunMR{Error: "the branch is gone"}
	writeReviseDescription(context.Background(), s)
	if s.run.JiraWrite != nil {
		t.Fatalf("jira_write = %+v without a push", s.run.JiraWrite)
	}
}

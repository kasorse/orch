package persistence

import (
	"strings"
	"testing"
	"time"

	"orchestrator/internal/domain"
)

const storyBase = "8d0e4b2c1f3a5b7d9e0f1a2b3c4d5e6f7a8b9c0d"

func storySource() *domain.TaskSource {
	return &domain.TaskSource{
		Kind: domain.TaskSourceStory, Story: "refunds", Task: "RF02", Attempt: 1, Jira: "PAY-124",
		Branch: "story/refunds/RF02", BaseRef: storyBase, BaseBranch: "main",
	}
}

func storyRun() *domain.Run {
	run := &domain.Run{ID: "r", Status: domain.RunFailed}
	run.Task = domain.Task{Description: "text", Acceptance: []string{"one", "two"}, Source: storySource()}
	run.Options.Isolation = domain.IsolationWorktree
	run.Worktree = &domain.RunWorktree{
		Path: "/state/w", Branch: "story/refunds/RF02", BaseRef: storyBase, BaseBranch: "main", AdminDir: "/repo/.git/worktrees/r",
	}
	return run
}

func TestValidateRun_StoryTask(t *testing.T) {
	if err := validateRun(storyRun(), "r"); err != nil {
		t.Fatalf("a valid story run is refused: %v", err)
	}

	retry := storyRun()
	retry.Task.Source.Attempt = 2
	retry.Task.Source.Branch, retry.Worktree.Branch = "story/refunds/RF02.2", "story/refunds/RF02.2"
	retry.Task.Source.Jira = ""
	if err := validateRun(retry, "r"); err != nil {
		t.Errorf("a retry without a Jira key is refused: %v", err)
	}

	notStarted := storyRun()
	notStarted.Worktree = nil
	if err := validateRun(notStarted, "r"); err != nil {
		t.Errorf("a story run without its worktree yet is refused: %v", err)
	}
}

func TestValidateRun_StoryTaskRefusals(t *testing.T) {
	tests := []struct {
		name   string
		mutate func(r *domain.Run)
		want   string
	}{
		{"branch of another story", func(r *domain.Run) { r.Task.Source.Branch = "story/other/RF02" }, "task.source: branch"},
		{"branch of another task", func(r *domain.Run) { r.Task.Source.Branch = "story/refunds/RF03" }, "task.source: branch"},
		{"the default branch", func(r *domain.Run) { r.Task.Source.Branch = "main" }, "task.source: branch"},
		{"option as branch", func(r *domain.Run) { r.Task.Source.Branch = "--detach" }, "task.source: branch"},
		{"bad slug", func(r *domain.Run) { r.Task.Source.Story = "Refunds" }, "task.source: story"},
		{"bad task ID", func(r *domain.Run) { r.Task.Source.Task = "rf02" }, "task.source: task"},
		{"attempt zero", func(r *domain.Run) { r.Task.Source.Attempt = 0 }, "task.source: attempt"},
		{"bad Jira key", func(r *domain.Run) { r.Task.Source.Jira = "pay-1" }, "task.source: jira"},
		{"short base_ref", func(r *domain.Run) { r.Task.Source.BaseRef = "HEAD" }, "task.source: base_ref"},
		{"option as base_branch", func(r *domain.Run) { r.Task.Source.BaseBranch = "-x" }, "task.source: base_branch"},
		{"Jira fields on a story", func(r *domain.Run) { r.Task.Source.Key = "PAY-1" }, "no Jira ticket fields"},
		{"fetched_at on a story", func(r *domain.Run) { r.Task.Source.FetchedAt = time.Now() }, "no Jira ticket fields"},
		{"story fields on a Jira task", func(r *domain.Run) {
			r.Task.Source = &domain.TaskSource{Kind: domain.TaskSourceJira, Key: "PAY-1", URL: "https://x.example/browse/PAY-1",
				FetchedAt: time.Now(), Story: "refunds"}
			r.Task.Acceptance, r.Worktree = nil, nil
		}, "no story fields"},
		{"unknown kind", func(r *domain.Run) { r.Task.Source.Kind = "epic" }, "unknown kind"},
		{"acceptance without a source", func(r *domain.Run) { r.Task.Source, r.Worktree = nil, nil }, "task.acceptance on a task that is not a story task"},
		{"acceptance on a Jira task", func(r *domain.Run) {
			r.Task.Source = &domain.TaskSource{Kind: domain.TaskSourceJira, Key: "PAY-1", URL: "https://x.example/browse/PAY-1", FetchedAt: time.Now()}
			r.Worktree = nil
		}, "task.acceptance on a task that is not a story task"},
		{"blank acceptance item", func(r *domain.Run) { r.Task.Acceptance = []string{"one", " "} }, "task.acceptance[1] is empty"},
		{"multi-line acceptance item", func(r *domain.Run) { r.Task.Acceptance = []string{"a\nb"} }, "more than one line"},
		{"oversized acceptance item", func(r *domain.Run) { r.Task.Acceptance = []string{strings.Repeat("я", 2001)} }, "2001 characters"},
		{"too many acceptance items", func(r *domain.Run) { r.Task.Acceptance = make([]string, 11) }, "11 items"},
		{"worktree on the run's own branch", func(r *domain.Run) { r.Worktree.Branch = "orchestrator/r" }, "worktree.branch"},
		{"worktree on another story's branch", func(r *domain.Run) { r.Worktree.Branch = "story/other/RF02" }, "worktree.branch"},
		{"worktree from another base", func(r *domain.Run) { r.Worktree.BaseRef = strings.Repeat("b", 40) }, "worktree.base_ref"},
		{"worktree for another base branch", func(r *domain.Run) { r.Worktree.BaseBranch = "develop" }, "worktree.base_branch"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			run := storyRun()
			tc.mutate(run)
			err := validateRun(run, "r")
			if err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Errorf("validateRun() = %v, want an error containing %q", err, tc.want)
			}
		})
	}
}

func TestValidateRun_WorktreeBranchOfAnOrdinaryRun(t *testing.T) {
	run := &domain.Run{
		ID: "r", Status: domain.RunFailed, Options: domain.RunOptions{Isolation: domain.IsolationWorktree},
		Worktree: &domain.RunWorktree{Path: "/w", Branch: "story/refunds/RF02", BaseRef: storyBase, AdminDir: "/a"},
	}
	if err := validateRun(run, "r"); err == nil || !strings.Contains(err.Error(), "worktree.branch") {
		t.Errorf("validateRun() = %v, want a worktree.branch error: a story branch needs a story task", err)
	}
	run.Worktree.Branch = "orchestrator/r"
	if err := validateRun(run, "r"); err != nil {
		t.Errorf("validateRun() = %v, want the ordinary branch accepted", err)
	}
}

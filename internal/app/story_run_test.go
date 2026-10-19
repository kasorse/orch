package app

import (
	"bytes"
	"context"
	"io"
	"strings"
	"testing"
	"time"

	"orchestrator/internal/domain"
	"orchestrator/internal/persistence"
	"orchestrator/internal/story"
)

func TestMapRunStatus(t *testing.T) {
	cases := []struct {
		name string
		run  domain.Run
		want story.TaskStatus
	}{
		{"completed with a commit", domain.Run{Status: domain.RunCompleted, Commit: "1c9f"}, story.TaskDone},
		{"completed without a commit", domain.Run{Status: domain.RunCompleted}, story.TaskBlocked},
		{"failed", domain.Run{Status: domain.RunFailed, Commit: "1c9f"}, story.TaskFailed},
		{"stopped", domain.Run{Status: domain.RunStopped}, story.TaskStopped},
		{"died while running", domain.Run{Status: domain.RunRunning}, story.TaskStopped},
		{"never started", domain.Run{Status: domain.RunPending}, story.TaskStopped},
	}
	for _, tc := range cases {
		if got := mapRunStatus(&tc.run); got != tc.want {
			t.Errorf("%s: mapRunStatus = %s, want %s", tc.name, got, tc.want)
		}
	}
}

func storyRunAt(id, slug, task string, attempt int, created time.Time) persistence.FoundRun {
	return persistence.FoundRun{RunID: id, Run: domain.Run{
		ID: id, CreatedAt: created,
		Task: domain.Task{Source: &domain.TaskSource{Kind: domain.TaskSourceStory, Story: slug, Task: task, Attempt: attempt}},
	}}
}

// The run of a task is chosen by story, task and attempt, newest first; a stale run of another
// attempt is never taken.
func TestPickStoryRun(t *testing.T) {
	t0 := time.Date(2026, 10, 19, 9, 0, 0, 0, time.UTC)
	found := []persistence.FoundRun{
		storyRunAt("old", "refunds", "RF02", 1, t0),
		storyRunAt("new", "refunds", "RF02", 1, t0.Add(time.Hour)),
		storyRunAt("retry", "refunds", "RF02", 2, t0.Add(2*time.Hour)),
		storyRunAt("other-task", "refunds", "RF03", 1, t0.Add(3*time.Hour)),
		storyRunAt("other-story", "payouts", "RF02", 1, t0.Add(4*time.Hour)),
		{RunID: "plain", Run: domain.Run{ID: "plain", CreatedAt: t0.Add(5 * time.Hour)}},
		{RunID: "jira", Run: domain.Run{ID: "jira", CreatedAt: t0.Add(6 * time.Hour), Task: domain.Task{Source: &domain.TaskSource{Kind: domain.TaskSourceJira}}}},
	}
	pick := func(slug, id string, attempt int, runID string) string {
		if run := pickStoryRun(found, slug, id, attempt, runID); run != nil {
			return run.ID
		}
		return ""
	}
	if got := pick("refunds", "RF02", 1, ""); got != "new" {
		t.Errorf("attempt 1 = %q, want the newest of its runs", got)
	}
	if got := pick("refunds", "RF02", 2, ""); got != "retry" {
		t.Errorf("attempt 2 = %q", got)
	}
	if got := pick("refunds", "RF02", 1, "old"); got != "old" {
		t.Errorf("the recorded run = %q, want it even when it is not the newest", got)
	}
	if got := pick("refunds", "RF02", 2, "old"); got != "retry" {
		t.Errorf("a recorded run of another attempt = %q, want the attempt's own", got)
	}
	if got := pick("refunds", "RF02", 3, ""); got != "" {
		t.Errorf("attempt 3 = %q, want no run: a stale run is not taken", got)
	}
	if got := pick("refunds", "RF04", 1, ""); got != "" {
		t.Errorf("a task without a run = %q", got)
	}
}

func TestRunCostUSD(t *testing.T) {
	if _, known := runCostUSD(&domain.Run{}); known {
		t.Error("a run without an outcome has a known cost")
	}
	run := &domain.Run{Outcome: &domain.Outcome{Usage: domain.OutcomeUsage{Usage: domain.Usage{CostUSD: 0.42}, Calls: 3}}}
	if cost, known := runCostUSD(run); !known || cost != 0.42 {
		t.Errorf("cost = %v, %v", cost, known)
	}
}

// The answer to the question is one line, and nothing past it is read: the children share stdin.
func TestConfirmed(t *testing.T) {
	cases := map[string]bool{
		"\n": true, "y\n": true, "Y\n": true, "yes\n": true, " YES \n": true,
		"n\n": false, "no\n": false, "maybe\n": false, "": false,
	}
	for input, want := range cases {
		if got := confirmed(strings.NewReader(input)); got != want {
			t.Errorf("confirmed(%q) = %v, want %v", input, got, want)
		}
	}
	in := strings.NewReader("y\nthe rest")
	if !confirmed(in) {
		t.Fatal("not confirmed")
	}
	if rest, _ := io.ReadAll(in); string(rest) != "the rest" {
		t.Errorf("after the answer %q was left, want %q", rest, "the rest")
	}
}

func TestRunChild(t *testing.T) {
	dir := t.TempDir()
	var out, errOut bytes.Buffer
	code, err := runChild(context.Background(), "/bin/sh", dir, []string{"-c", "echo out; echo err >&2; pwd; exit 3"}, strings.NewReader(""), &out, &errOut)
	if err != nil || code != 3 {
		t.Fatalf("code = %d, err = %v; want 3", code, err)
	}
	if !strings.HasPrefix(out.String(), "out\n") || errOut.String() != "err\n" {
		t.Errorf("stdout = %q, stderr = %q", out.String(), errOut.String())
	}

	code, err = runChild(context.Background(), "/nonexistent/orchestrator", dir, []string{"validate"}, strings.NewReader(""), &out, &errOut)
	if err == nil || code != -1 {
		t.Errorf("a missing executable: code = %d, err = %v", code, err)
	}
}

// Cancelling the context interrupts the child (as Ctrl-C does) and waits for it to finish what
// it does on SIGINT, instead of killing it.
func TestRunChild_CancelInterrupts(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	var out, errOut bytes.Buffer
	type result struct {
		code int
		err  error
	}
	done := make(chan result, 1)
	dir := t.TempDir()
	go func() {
		code, err := runChild(ctx, "/bin/sh", dir,
			[]string{"-c", "trap 'echo interrupted; exit 7' INT; echo ready; while true; do sleep 0.1; done"},
			strings.NewReader(""), &out, &errOut)
		done <- result{code, err}
	}()
	time.Sleep(500 * time.Millisecond)
	cancel()
	select {
	case res := <-done:
		if res.err != nil || res.code != 7 {
			t.Errorf("code = %d, err = %v; want the child's own exit 7", res.code, res.err)
		}
	case <-time.After(20 * time.Second):
		t.Fatal("the child was not interrupted")
	}
}

func TestKnownBranches(t *testing.T) {
	r := &storyRunner{loc: storyLocation{slug: "refunds"}}
	r.rec.Tasks = map[string]persistence.StoryTask{
		"RF01": {Status: story.TaskDone, Attempt: 1},
		"RF02": {Status: story.TaskPending, Attempt: 3},
		"RF03": {Status: story.TaskPending},
	}
	got := r.knownBranches()
	want := map[string]bool{
		"story/refunds/RF01":   true,
		"story/refunds/RF02":   true,
		"story/refunds/RF02.2": true,
		"story/refunds/RF02.3": true,
	}
	if len(got) != len(want) {
		t.Errorf("known = %v, want %v", got, want)
	}
	for b := range want {
		if !got[b] {
			t.Errorf("%s is not known: %v", b, got)
		}
	}
}

func TestRetryable(t *testing.T) {
	for status, want := range map[story.TaskStatus]bool{
		story.TaskFailed: true, story.TaskStopped: true, story.TaskBlocked: true,
		story.TaskPending: false, story.TaskDone: false, story.TaskRunning: false, "": false,
	} {
		if got := retryable(status, false); got != want {
			t.Errorf("retryable(%q, false) = %v, want %v", status, got, want)
		}
	}
	if !retryable(story.TaskRunning, true) {
		t.Error("a running task is settled first, then retried")
	}
}

func TestJiraKey(t *testing.T) {
	if got := jiraKey(story.Task{Jira: "PAY-5"}, persistence.StoryTask{Jira: "PAY-9"}); got != "PAY-5" {
		t.Errorf("the front matter's key comes first: %q", got)
	}
	if got := jiraKey(story.Task{Jira: story.JiraNone}, persistence.StoryTask{Jira: "PAY-9"}); got != "PAY-9" {
		t.Errorf("none falls back to the record: %q", got)
	}
	if got := jiraKey(story.Task{}, persistence.StoryTask{}); got != "" {
		t.Errorf("no key: %q", got)
	}
}

func TestRunDetail(t *testing.T) {
	if got := runDetail(&domain.Run{Status: domain.RunCompleted}); got != "the run completed without a commit" {
		t.Errorf("completed: %q", got)
	}
	if got := runDetail(&domain.Run{ID: "r1", Status: domain.RunFailed, FailureReason: "review\nfailed   badly"}); got != "review failed badly" {
		t.Errorf("failed: %q", got)
	}
	if got := runDetail(&domain.Run{ID: "r1", Status: domain.RunStopped}); got != "run r1" {
		t.Errorf("no reason: %q", got)
	}
	if got := runDetail(&domain.Run{Status: domain.RunFailed, FailureReason: strings.Repeat("x", 500)}); len(got) > 170 {
		t.Errorf("a long reason is not cut: %d bytes", len(got))
	}
}

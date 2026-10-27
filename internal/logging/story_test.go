package logging

import (
	"bytes"
	"strings"
	"testing"
)

func withHome(t *testing.T, home string) {
	t.Helper()
	previous := userHomeDir
	userHomeDir = func() (string, error) { return home, nil }
	t.Cleanup(func() { userHomeDir = previous })
}

func TestStoryLines(t *testing.T) {
	withHome(t, "/home/op")
	var buf bytes.Buffer
	r := NewConsoleReporter(&buf)
	r.StoryServices([]StoryService{{"billing", "/home/op/src/billing"}, {"gateway", "/srv/gateway"}})
	r.StoryPlanDir(".orchestrator/stories/.plan-1")
	r.StorySurvey("billing")
	r.StorySpecify()
	r.StorySpecWritten("specs/refunds", 3)
	r.StoryPlanKept(".orchestrator/stories/.plan-1")

	want := "Services: billing (~/src/billing), gateway (/srv/gateway)\n" +
		"Story directory: .orchestrator/stories/.plan-1\n" +
		"\nSurvey of billing\n" +
		"\nSpecify\n" +
		"\n✓ Spec written to specs/refunds (3 tasks)\nNext: read it, edit it, commit it, then: orchestrator story run specs/refunds\n" +
		"The plan's prompts, answers and logs are kept in .orchestrator/stories/.plan-1\n"
	if buf.String() != want {
		t.Errorf("got\n%s\nwant\n%s", buf.String(), want)
	}
}

func TestStoryRunLines(t *testing.T) {
	withHome(t, "/home/op")
	var buf bytes.Buffer
	r := NewConsoleReporter(&buf)
	r.StoryChecking("billing", "/home/op/src/billing")
	r.StoryRunPlan([]StoryPlanTask{
		{ID: "RF01", Repo: "billing", Base: "main@8d0e4b2", Branch: "story/refunds/RF01", Jira: "PAY-2"},
		{ID: "RF02", Repo: "gateway", Base: "story/refunds/RF01", Branch: "story/refunds/RF02"},
	}, 2)
	r.StoryConfirm(2, 2)
	r.StoryCancelled()
	r.StoryTaskHeader("RF02", "gateway", 2, 6)
	r.StoryResume("RF02", "3f2a")
	r.StoryRetry("RF02", "3f2a", 2, "story/refunds/RF02.2")
	r.StoryRetry("RF03", "", 2, "story/refunds/RF03.2")
	r.StoryTaskResult("RF01", "done", "commit 1c9f2e7")
	r.StoryTaskResult("RF02", "failed", "")
	r.StoryHint("run it again to resume", "or --retry RF02")

	want := "\nChecking billing (~/src/billing)\n" +
		"\nStory plan: 2 tasks in 2 repositories\n" +
		"  RF01 · billing · story/refunds/RF01 · from main@8d0e4b2 · PAY-2\n" +
		"  RF02 · gateway · story/refunds/RF02 · from story/refunds/RF01\n" +
		"\nRun 2 tasks in 2 repositories? [Y/n] " +
		"Cancelled: nothing was started and nothing was written.\n" +
		"\n── RF02 · gateway · 2/6 ──\n" +
		"RF02: resuming run 3f2a\n" +
		"RF02: run 3f2a is abandoned (its branch stays); attempt 2 goes on story/refunds/RF02.2\n" +
		"RF03: its run is abandoned (its branch stays); attempt 2 goes on story/refunds/RF03.2\n" +
		"\nRF01: done (commit 1c9f2e7)\n" +
		"\nRF02: failed\n" +
		"  run it again to resume\n  or --retry RF02\n"
	if buf.String() != want {
		t.Errorf("got\n%s\nwant\n%s", buf.String(), want)
	}
}

func TestStoryTaskStarted(t *testing.T) {
	var buf bytes.Buffer
	r := NewConsoleReporter(&buf)
	r.StoryTaskStarted("PS02", "orche", 2, 4, ".orchestrator/stories/p/logs/task-PS02.1.log")
	want := "▶ PS02 · orche · 2/4 · log .orchestrator/stories/p/logs/task-PS02.1.log\n"
	if buf.String() != want {
		t.Errorf("got %q, want %q", buf.String(), want)
	}

	buf.Reset()
	pretty := &ConsoleReporter{Out: &buf, Pretty: true}
	pretty.StoryTaskStarted("PS02", "orche", 2, 4, "logs/task-PS02.1.log")
	if !strings.Contains(buf.String(), "▶ PS02 · orche · 2/4 · log logs/task-PS02.1.log") {
		t.Errorf("the pretty line lacks the start text:\n%s", buf.String())
	}
}

func TestStoryConflict(t *testing.T) {
	var buf bytes.Buffer
	r := NewConsoleReporter(&buf)
	r.StoryConflict("RF03", []string{"RF01", "RF02"}, []string{"a.go", "pkg/b.go"}, "story/refunds/spec", "merge them by hand", "then: orchestrator story run specs/refunds")
	want := "\nRF03: conflict (RF01 + RF02 do not merge cleanly on story/refunds/spec)\n" +
		"  Conflicting files:\n    a.go\n    pkg/b.go\n" +
		"  merge them by hand\n  then: orchestrator story run specs/refunds\n"
	if buf.String() != want {
		t.Errorf("got\n%s\nwant\n%s", buf.String(), want)
	}

	buf.Reset()
	pretty := &ConsoleReporter{Out: &buf, Pretty: true}
	pretty.StoryConflict("RF03", []string{"RF01", "RF02"}, []string{"a.go", "pkg/b.go"}, "story/refunds/spec", "merge them by hand")
	for _, w := range []string{"RF03: conflict (RF01 + RF02 do not merge cleanly on story/refunds/spec)", "a.go, pkg/b.go", "merge them by hand"} {
		if !strings.Contains(buf.String(), w) {
			t.Errorf("the pretty line lacks %q:\n%s", w, buf.String())
		}
	}
}

func TestStoryEnd(t *testing.T) {
	var buf bytes.Buffer
	r := NewConsoleReporter(&buf)
	r.StoryEnd([]StoryEndRow{
		{ID: "RF01", Repo: "billing", Branch: "story/refunds/RF01", Target: "main", Commit: "8d0e4b2a1c9f2e7d", Jira: "PAY-2"},
		{ID: "RF02", Repo: "gateway", Branch: "story/refunds/RF02", Target: "main", Commit: "1c9f2e7d3f2a", Conflicts: "go.mod"},
	})
	want := "\n✓ Story completed: all 2 tasks done\n" +
		"  RF01 · billing · story/refunds/RF01 → main · 8d0e4b2 · PAY-2 · conflicts: none\n" +
		"  RF02 · gateway · story/refunds/RF02 → main · 1c9f2e7 · — · conflicts: go.mod\n" +
		"Push the branches and open the merge requests, each to its target.\n"
	if buf.String() != want {
		t.Errorf("got\n%s\nwant\n%s", buf.String(), want)
	}

	// The merge request is printed next to the target branch.
	buf.Reset()
	mr := StoryMRLabel(12, "https://gitlab.example/g/p/-/merge_requests/12")
	r.StoryEnd([]StoryEndRow{
		{ID: "RF01", Repo: "billing", Branch: "feature/PAY-2-a", Target: "story/refunds/spec", Commit: "8d0e4b2a1c9f2e7d", Jira: "PAY-2", MR: mr},
		{ID: "RF02", Repo: "billing", Branch: "feature/PAY-3-b", Target: "feature/PAY-2-a", Commit: "1c9f2e7d3f2a"},
	})
	for _, w := range []string{
		"RF01 · billing · feature/PAY-2-a → story/refunds/spec · !12 https://gitlab.example/g/p/-/merge_requests/12 · 8d0e4b2 · PAY-2",
		"RF02 · billing · feature/PAY-3-b → feature/PAY-2-a · 1c9f2e7",
		"Open the merge requests of the tasks without one, each to its target.",
	} {
		if !strings.Contains(buf.String(), w) {
			t.Errorf("the end table lacks %q:\n%s", w, buf.String())
		}
	}

	buf.Reset()
	r.StoryStatusTable("# T\n\ntable\n")
	if buf.String() != "# T\n\ntable\n" {
		t.Errorf("StoryStatusTable printed %q", buf.String())
	}
}

// The one-task command quotes the model's title and an unusual path for the shell.
func TestStoryOneTask_QuotesForTheShell(t *testing.T) {
	cases := []struct {
		path, title, want string
	}{
		{"/src/billing", "Refund an amount", "cd /src/billing && orchestrator run 'Refund an amount'"},
		{"/src/my repo", "it's $(rm -rf ~); `x`", `cd '/src/my repo' && orchestrator run 'it'\''s $(rm -rf ~); ` + "`x`'"},
	}
	for _, tc := range cases {
		var buf bytes.Buffer
		NewConsoleReporter(&buf).StoryOneTask("billing", tc.path, tc.title)
		want := "\nThe answer is one task, not a story: no spec is written.\nTask (billing): " + tc.title + "\nRun it with:\n  " + tc.want + "\n"
		if buf.String() != want {
			t.Errorf("got\n%q\nwant\n%q", buf.String(), want)
		}
	}
}

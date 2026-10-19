package logging

import (
	"bytes"
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

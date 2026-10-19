package integration

import (
	"encoding/json"
	"fmt"
	"strings"
	"testing"

	"orchestrator/internal/story"
)

// createdFields is the part of a creation request these scenarios read.
type createdFields struct {
	Project struct {
		Key string `json:"key"`
	} `json:"project"`
	IssueType struct {
		Name string `json:"name"`
	} `json:"issuetype"`
	Summary string `json:"summary"`
	Parent  struct {
		Key string `json:"key"`
	} `json:"parent"`
	Labels []string `json:"labels"`
}

// ST10 (specs/story/SPEC.md §16): a story planned from a Jira issue, run with jira.create. The
// fake Jira creates the children before the first dispatch, one per task in the story's order,
// and each key reaches story.json, the task file and the task's commit; then the story runs to
// the end as in TestStoryRun_OrderStackingAsBuiltAndTheEnd, the operator's checkouts untouched.
func TestStoryRun_CreatesJiraChildrenBeforeTheFirstDispatch(t *testing.T) {
	jira := startJira(t, nil)
	f := newStoryRunFixture(t, func(o *storyRunFixtureOpts) {
		o.jira = &jiraOptions{
			baseURL: jira.URL(),
			create:  "  create:\n    issue_type: Story\n    link: parent\n    labels: [orchestrator]\n",
		}
	})
	f.writePlanRecordFrom(t, "PAY-1")
	before := f.checkouts(t)

	stdout, stderr, err := f.run(t)
	mustSucceed(t, stdout, stderr, err)

	reqs := jira.Requests()
	if len(reqs) != len(storyRunTasks) {
		t.Fatalf("Jira requests = %+v, want %d creations", reqs, len(storyRunTasks))
	}
	rec := f.record(t)
	if rec.Status != story.StatusCompleted {
		t.Fatalf("status = %s, want completed", rec.Status)
	}
	for i, task := range storyRunTasks {
		req := reqs[i]
		if req.Method != "POST" || req.Path != "/rest/api/2/issue" {
			t.Errorf("request %d = %s %s, want POST /rest/api/2/issue", i, req.Method, req.Path)
		}
		var body struct {
			Fields createdFields `json:"fields"`
		}
		if err := json.Unmarshal([]byte(req.Body), &body); err != nil {
			t.Fatalf("request %d body: %v\n%s", i, err, req.Body)
		}
		fields := body.Fields
		if fields.Project.Key != "PAY" || fields.IssueType.Name != "Story" || fields.Parent.Key != "PAY-1" ||
			!strings.HasPrefix(fields.Summary, task.ID+" · "+task.Title) ||
			len(fields.Labels) != 1 || fields.Labels[0] != "orchestrator" {
			t.Errorf("request %d fields = %+v, want %s under PAY-1", i, fields, task.ID)
		}

		// The key is in the record, the task file's front matter and the task's commit.
		key := fmt.Sprintf("PAY-%d", 101+i)
		rt := rec.Tasks[task.ID]
		if rt.Jira != key || rt.JiraIntent {
			t.Errorf("tasks.%s: jira = %q, intent = %v, want %s and no intent", task.ID, rt.Jira, rt.JiraIntent, key)
		}
		data := readFile(t, f.taskPath(task.ID))
		parsed, perr := story.ParseTaskFile(story.TaskFileName(task.ID, task.Title), []byte(data))
		if perr != nil || parsed.Jira != key {
			t.Errorf("%s: task file jira = %q (%v), want %s", task.ID, parsed.Jira, perr, key)
		}
		msg, _ := gitOutput(f.repos[rt.Repo], "log", "-1", "--format=%B", rt.Commit)
		if !strings.HasPrefix(msg, key+": ") || !strings.Contains(msg, "Orchestrator-Story: "+storyRunSlug+"/"+task.ID) {
			t.Errorf("%s: commit message %q lacks the key %s and the story trailer", task.ID, msg, key)
		}
	}

	// Nothing is created twice: a completed story sends nothing.
	stdout, stderr, err = f.run(t)
	mustSucceed(t, stdout, stderr, err)
	if n := len(jira.Requests()); n != len(storyRunTasks) {
		t.Errorf("Jira requests after a second story run = %d, want %d", n, len(storyRunTasks))
	}
	f.assertCheckoutsUnchanged(t, before)
}

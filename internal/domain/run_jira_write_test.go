package domain

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestRunJiraWriteValidate(t *testing.T) {
	opts := JiraWriteOptions{Comment: true, Transition: "In Review"}
	done := func() RunJiraWrite {
		return RunJiraWrite{
			Comment:    &RunJiraComment{State: JiraCommentDone, ID: "10042"},
			Transition: &RunJiraTransition{Name: "In Review", State: JiraTransitionDone},
		}
	}
	cases := []struct {
		name   string
		mutate func(*RunJiraWrite)
		opts   JiraWriteOptions
		want   string // "" = valid
	}{
		{"done", func(*RunJiraWrite) {}, opts, ""},
		{"comment only", func(j *RunJiraWrite) { j.Transition = nil }, opts, ""},
		{"transition only", func(j *RunJiraWrite) { j.Comment = nil }, opts, ""},
		{"intent kept", func(j *RunJiraWrite) { j.Comment = &RunJiraComment{State: JiraCommentIntent} }, opts, ""},
		{"failed comment", func(j *RunJiraWrite) { j.Comment = &RunJiraComment{State: JiraCommentFailed} }, opts, ""},
		{"missing", func(j *RunJiraWrite) { j.Transition.State = JiraTransitionMissing }, opts, ""},
		{"failed", func(j *RunJiraWrite) { j.Transition.State = JiraTransitionFailed }, opts, ""},
		{"unchanged", func(j *RunJiraWrite) { j.Transition.State = JiraTransitionUnchanged }, opts, ""},
		{"empty", func(j *RunJiraWrite) { *j = RunJiraWrite{} }, opts, "needs a comment, a transition or a description"},
		{"comment not asked for", func(*RunJiraWrite) {}, JiraWriteOptions{Transition: "In Review"}, "comment recorded"},
		{"transition not asked for", func(*RunJiraWrite) {}, JiraWriteOptions{Comment: true}, "transition recorded"},
		{"another transition", func(j *RunJiraWrite) { j.Transition.Name = "Done" }, opts, "transition.name"},
		{"unknown comment state", func(j *RunJiraWrite) { j.Comment.State = "posted" }, opts, "comment.state"},
		{"unknown transition state", func(j *RunJiraWrite) { j.Transition.State = "moved" }, opts, "transition.state"},
		{"done without an id", func(j *RunJiraWrite) { j.Comment.ID = "" }, opts, "comment.id"},
		{"an id with an intent", func(j *RunJiraWrite) { j.Comment.State = JiraCommentIntent }, opts, "comment.id"},
		{"an id that is not a number", func(j *RunJiraWrite) { j.Comment.ID = "../x" }, opts, "not a number"},
	}
	for _, c := range cases {
		j := done()
		c.mutate(&j)
		err := j.Validate(c.opts)
		if c.want == "" {
			if err != nil {
				t.Errorf("%s: Validate = %v, want nil", c.name, err)
			}
			continue
		}
		if err == nil || !strings.Contains(err.Error(), c.want) {
			t.Errorf("%s: Validate = %v, want an error naming %q", c.name, err, c.want)
		}
	}
}

func TestJiraWriteOptionsValidate(t *testing.T) {
	for _, ok := range []string{"", "In Review", strings.Repeat("a", 255)} {
		if err := (JiraWriteOptions{Transition: ok}).Validate(); err != nil {
			t.Errorf("%q: %v", ok, err)
		}
	}
	for _, bad := range []string{"In\nReview", "a\x00b", strings.Repeat("a", 256)} {
		if err := (JiraWriteOptions{Transition: bad}).Validate(); err == nil {
			t.Errorf("%q accepted", bad)
		}
	}
}

// A run that asks for nothing writes no options, so a run.json from before the field is unchanged.
func TestRunOptions_JiraWriteIsOmittedWhenEmpty(t *testing.T) {
	data, err := json.Marshal(Run{Options: RunOptions{}})
	if err != nil || strings.Contains(string(data), "jira_write") {
		t.Errorf("an empty run writes %s (%v)", data, err)
	}
	data, err = json.Marshal(Run{Options: RunOptions{JiraWrite: JiraWriteOptions{Comment: true}}})
	if err != nil || !strings.Contains(string(data), `"jira_write":{"comment":true}`) {
		t.Errorf("a run that comments writes %s (%v)", data, err)
	}
}

func TestTaskSource_JiraKey(t *testing.T) {
	var none *TaskSource
	cases := []struct {
		src  *TaskSource
		want string
	}{
		{none, ""},
		{&TaskSource{Kind: TaskSourceJira, Key: "PAY-12"}, "PAY-12"},
		{&TaskSource{Kind: TaskSourceStory, Jira: "PAY-124"}, "PAY-124"},
		{&TaskSource{Kind: TaskSourceStory}, ""},
	}
	for _, c := range cases {
		if got := c.src.JiraKey(); got != c.want {
			t.Errorf("%+v: JiraKey = %q, want %q", c.src, got, c.want)
		}
	}
}

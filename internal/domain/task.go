package domain

import (
	"strconv"
	"time"
)

type Task struct {
	// Description is the complete task text every prompt and the decision read - for a
	// Jira task, the rendered ticket plus any operator notes.
	Description string `json:"description"`
	// Acceptance are the criteria of a story task (specs/story/SPEC.md §11.1): review
	// criteria ahead of the plan's own, kept here so a resume uses the same ones. Empty for
	// every other task.
	Acceptance []string `json:"acceptance,omitempty"`
	// Source records where the task came from; nil for a task typed on the command line.
	Source *TaskSource `json:"source,omitempty"`
	// Clarification is what was agreed about an unclear task before the decision (specs/clarify):
	// written once, never changed. Nil when the first check cleared the threshold, under
	// clarify.enabled: false, for a story task and after a failed pre-check.
	Clarification *Clarification `json:"clarification,omitempty"`
	CreatedAt     time.Time      `json:"created_at"`
}

// TaskSourceJira is TaskSource.Kind for a task rendered from a Jira issue.
const TaskSourceJira = "jira"

// TaskSourceStory is TaskSource.Kind for a task of a story (run --story-task).
const TaskSourceStory = "story"

// TaskSource says where a task came from. For a Jira task (specs/jira/SPEC.md §11.1) it says
// which ticket it was rendered from; it is only displayed, never used to make a request:
// resume never re-fetches (J3). For a story task (specs/story/SPEC.md §11.1) it names the
// story and the task, and records the branch and base the run was asked for, so that a
// resume needs no story file.
type TaskSource struct {
	Kind string `json:"kind"`
	// Key and URL are the Jira issue's; empty for a story task.
	Key string `json:"key,omitempty"`
	// RequestedKey is set only when the issue has moved and answered under Key.
	RequestedKey string    `json:"requested_key,omitempty"`
	URL          string    `json:"url,omitempty"`
	FetchedAt    time.Time `json:"fetched_at"`
	// Notes reports whether operator notes were appended to the rendered ticket.
	Notes bool `json:"notes"`

	// Story, Task, Attempt and Jira identify a story task: the story's slug, the task's ID,
	// the attempt (1 for the first, n for the retry on <ID>.<n>) and the task's Jira key,
	// "" when it has none.
	Story   string `json:"story,omitempty"`
	Task    string `json:"task,omitempty"`
	Attempt int    `json:"attempt,omitempty"`
	Jira    string `json:"jira,omitempty"`
	// Title is the story task's heading, one line: what {slug} of its branch name and the naming
	// call's task line are made of (specs/delivery/SPEC.md §9.1). Empty in a run recorded before it.
	Title string `json:"title,omitempty"`
	// Branch is the fallback name of a story task's branch, story/<slug>/<ID>[.<n>], used when its
	// repository's rules give none; the branch the worktree is on is worktree.branch. BaseRef and
	// BaseBranch are what the worktree is created from, and the branch the change finally goes to
	// (the conflict check's, and the merge request's target).
	Branch     string `json:"branch,omitempty"`
	BaseRef    string `json:"base_ref,omitempty"`
	BaseBranch string `json:"base_branch,omitempty"`
}

// JiraKey is the key of the Jira issue the task stands for: the issue a run --jira task was
// rendered from, or a story task's child issue. It is empty for a typed task, for a story task
// without an issue and for a nil source.
func (s *TaskSource) JiraKey() string {
	switch {
	case s == nil:
		return ""
	case s.Kind == TaskSourceStory:
		return s.Jira
	}
	return s.Key
}

// StoryRef is <story>/<task>, how a story task is named in a commit trailer and on screen.
func (s TaskSource) StoryRef() string { return s.Story + "/" + s.Task }

// StoryDetail is "attempt n" for a story task, with its Jira key after a comma when it has one.
func (s TaskSource) StoryDetail() string {
	detail := "attempt " + strconv.Itoa(s.Attempt)
	if s.Jira != "" {
		detail += ", " + s.Jira
	}
	return detail
}

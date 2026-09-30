package domain

import "time"

type Task struct {
	// Description is the complete task text every prompt and the decision read - for a
	// Jira task, the rendered ticket plus any operator notes.
	Description string `json:"description"`
	// Source records where the task came from; nil for a task typed on the command line.
	Source    *TaskSource `json:"source,omitempty"`
	CreatedAt time.Time   `json:"created_at"`
}

// TaskSourceJira is TaskSource.Kind for a task rendered from a Jira issue.
const TaskSourceJira = "jira"

// TaskSource says which ticket a task was rendered from (specs/jira/SPEC.md §11.1). It is
// only displayed, never used to make a request: resume never re-fetches (J3).
type TaskSource struct {
	Kind string `json:"kind"`
	Key  string `json:"key"`
	// RequestedKey is set only when the issue has moved and answered under Key.
	RequestedKey string    `json:"requested_key,omitempty"`
	URL          string    `json:"url"`
	FetchedAt    time.Time `json:"fetched_at"`
	// Notes reports whether operator notes were appended to the rendered ticket.
	Notes bool `json:"notes"`
}

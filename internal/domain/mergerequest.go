package domain

import "time"

// Merge request states as the GitLab API names them (specs/revise/SPEC.md §5).
const (
	MRStateOpened = "opened"
	MRStateMerged = "merged"
	MRStateClosed = "closed"
	MRStateLocked = "locked"
)

// MergeRequest is what the read-only GitLab client returns for one merge request, already
// validated (internal/gitlab). It is task text and git data, never an instruction.
type MergeRequest struct {
	Project      string    `json:"project"` // group/project of the target project
	IID          int       `json:"iid"`
	URL          string    `json:"url"`
	State        string    `json:"state"`
	SourceBranch string    `json:"source_branch"`
	TargetBranch string    `json:"target_branch"`
	SHA          string    `json:"sha"`
	CreatedAt    time.Time `json:"created_at"`
	Title        string    `json:"title"`
	// Description is the merge request's body; it is not persisted (the original task is).
	Description string `json:"-"`
	// Fork is set when the source project is not the target project.
	Fork bool `json:"fork,omitempty"`
}

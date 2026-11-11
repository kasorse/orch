package domain

import (
	"strconv"
	"time"
)

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

// Revise target kinds and modes (specs/revise/SPEC.md §1, §5).
const (
	ReviseTargetJira   = "jira"
	ReviseTargetMR     = "mr"
	ReviseTargetRun    = "run"
	ReviseTargetBranch = "branch"

	// ReviseUpdate pushes a new commit to the merge request's own branch; ReviseNew starts a new
	// branch from the target branch and opens a new merge request.
	ReviseUpdate = "update"
	ReviseNew    = "new"
)

// ReviseSource is what Go resolved a revise target into, before the run existed: where the change
// goes, recorded so that a resume needs no GitLab call (SPEC.md §9).
type ReviseSource struct {
	Target     string `json:"target"`
	TargetKind string `json:"target_kind"`
	Project    string `json:"project"`
	Mode       string `json:"mode"`
	// MR is the merge request the target resolved to; nil for a branch with none.
	MR *MergeRequest `json:"mr,omitempty"`
	// Branch is the branch an update pushes to; empty for ReviseNew.
	Branch string `json:"branch,omitempty"`
	// BaseRef is the commit the worktree starts from: the merge request's tip for an update, ""
	// until the target branch is fetched for ReviseNew (then recorded in worktree.base_ref).
	BaseRef string `json:"base_ref,omitempty"`
	// BaseBranch is the branch the change goes to in the end: the merge request's target.
	BaseBranch string `json:"base_branch"`
	// FromRun is the run a run target named.
	FromRun string `json:"from_run,omitempty"`
}

// Label names the revised work on screen: "!1294" or the branch.
func (r ReviseSource) Label() string {
	if r.MR != nil {
		return "!" + strconv.Itoa(r.MR.IID)
	}
	return r.Branch
}

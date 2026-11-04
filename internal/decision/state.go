package decision

import "github.com/kasorse/orch/internal/domain"

// The state every question is asked against. Both providers send exactly this, so
// their answers are comparable (T22).

// PreCheckState is the pre-check's state: the request alone, since deciding whether the
// repository is needed must not depend on the repository.
type PreCheckState struct {
	Request string `json:"request"`
}

// ClarityState is the state of a clarity re-check (specs/clarify): text only, the request and
// the rendered clarification so far (empty when nothing was clarified). It carries no project
// context and no instruction file: the provider judges the task as it is written.
type ClarityState struct {
	Request       string `json:"request"`
	Clarification string `json:"clarification,omitempty"`
}

// ClassificationState is the final classification's state (MVP.md §40).
// ProjectContextAvailable is false both for depth none and for a failed collection;
// ContextTruncated says the context shown is incomplete.
type ClassificationState struct {
	Request                 string                 `json:"request"`
	ProjectContextAvailable bool                   `json:"project_context_available"`
	ContextTruncated        bool                   `json:"context_truncated,omitempty"`
	Project                 *domain.ProjectContext `json:"project,omitempty"`
}

// NewClassificationState builds the state for input.
func NewClassificationState(input domain.ClassificationInput) ClassificationState {
	state := ClassificationState{Request: input.Request}
	if input.Project != nil {
		state.ProjectContextAvailable = true
		state.ContextTruncated = input.Project.Truncated
		state.Project = input.Project
	}
	return state
}

package decision

import "orchestrator/internal/domain"

// The state every question is asked against. Both providers send exactly this, so
// their answers are comparable (T22).

// PreCheckState is the pre-check's state: the request alone, since deciding whether the
// repository is needed must not depend on the repository.
type PreCheckState struct {
	Request string `json:"request"`
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

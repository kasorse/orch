package apperr

import "fmt"

// RunStateKind classifies why a persisted run cannot be used (specs/resume/SPEC.md §13).
type RunStateKind string

const (
	RunNotFound     RunStateKind = "not_found"     // no run matches the ID or prefix
	RunAmbiguous    RunStateKind = "ambiguous"     // the prefix matches several runs
	RunInUse        RunStateKind = "in_use"        // another process holds the run's lock
	RunNotResumable RunStateKind = "not_resumable" // e.g. no workflow was ever decided
	RunCorrupt      RunStateKind = "corrupt"       // persisted state fails validation
)

// RunStateError is a problem with a persisted run rather than with the config, a
// provider or the workflow. Every kind maps to exit code 1.
type RunStateError struct {
	Kind RunStateKind
	// RunID is the run ID, or the ID or prefix the user gave when no run was resolved.
	RunID string
	Msg   string
	Err   error
}

func (e *RunStateError) Error() string {
	msg := e.Msg
	if e.RunID != "" {
		msg = fmt.Sprintf("run %s: %s", e.RunID, e.Msg)
	}
	if e.Err != nil {
		return fmt.Sprintf("%s: %v", msg, e.Err)
	}
	return msg
}

func (e *RunStateError) Unwrap() error { return e.Err }

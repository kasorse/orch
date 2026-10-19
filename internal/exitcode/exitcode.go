// Package exitcode centralizes the mapping from returned errors to process exit codes.
// main.go is the only call site for os.Exit in the codebase.
package exitcode

import (
	"context"
	"errors"

	"orchestrator/internal/apperr"
)

const (
	OK                 = 0
	GenericError       = 1
	ConfigOrDependency = 2
	ProviderExecution  = 3
	WorkflowOrLimit    = 4
	UserCancellation   = 5
)

// FromError maps a returned error to a process exit code. nil maps to OK.
func FromError(err error) int {
	if err == nil {
		return OK
	}

	if errors.Is(err, context.Canceled) {
		return UserCancellation
	}

	// Checked before the other categories: a run that cannot be used is exit 1 whatever
	// underlying error it wraps (specs/resume/SPEC.md §13).
	var runErr *apperr.RunStateError
	if errors.As(err, &runErr) {
		return GenericError
	}

	var configErr *apperr.ConfigError
	if errors.As(err, &configErr) {
		return ConfigOrDependency
	}

	var depErr *apperr.DependencyError
	if errors.As(err, &depErr) {
		return ConfigOrDependency
	}

	var authErr *apperr.AuthorityError
	if errors.As(err, &authErr) {
		return ConfigOrDependency
	}

	var providerErr *apperr.ProviderError
	if errors.As(err, &providerErr) {
		if providerErr.Kind == apperr.FailureCancelled {
			return UserCancellation
		}
		// A credential the operator must fix, like a rejected Jev key (DecisionAuth).
		if providerErr.Kind == apperr.FailureAuth {
			return ConfigOrDependency
		}
		return ProviderExecution
	}

	var decisionErr *apperr.DecisionError
	if errors.As(err, &decisionErr) {
		switch decisionErr.Kind {
		case apperr.DecisionCancelled:
			return UserCancellation
		case apperr.DecisionAuth:
			return ConfigOrDependency
		}
		return ProviderExecution
	}

	// A story's planning call answered with something its rules refuse: the provider
	// failing, like an invalid decision answer.
	var invalidErr *apperr.InvalidResponseError
	if errors.As(err, &invalidErr) {
		return ProviderExecution
	}

	// A ticket that cannot be read is the operator's to fix (credentials, key, base_url):
	// exit 2, like a rejected Jev key. Everything else is Jira failing: exit 3.
	var jiraErr *apperr.JiraError
	if errors.As(err, &jiraErr) {
		switch jiraErr.Kind {
		case apperr.JiraCancelled:
			return UserCancellation
		case apperr.JiraAuth, apperr.JiraNotFound, apperr.JiraRedirect, apperr.JiraRejected:
			return ConfigOrDependency
		}
		return ProviderExecution
	}

	var limitErr *apperr.LimitExceededError
	if errors.As(err, &limitErr) {
		return WorkflowOrLimit
	}

	var workflowErr *apperr.WorkflowError
	if errors.As(err, &workflowErr) {
		return WorkflowOrLimit
	}

	return GenericError
}

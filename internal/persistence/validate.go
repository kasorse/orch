package persistence

import (
	"fmt"
	"net/url"
	"slices"
	"unicode"

	"orchestrator/internal/domain"
	"orchestrator/internal/provider"
)

// validateRun applies the structural checks of specs/resume/SPEC.md §6.3 to a decoded
// run.json. It returns the first violation, phrased for the user.
func validateRun(run *domain.Run, runID string) error {
	if run.ID != runID {
		return fmt.Errorf("id is %q, want %q (the directory name)", run.ID, runID)
	}
	if !run.Status.Valid() {
		return fmt.Errorf("unknown status %q", run.Status)
	}
	if run.Mode != "" && !run.Mode.Valid() {
		return fmt.Errorf("unknown mode %q", run.Mode)
	}
	if run.Task.Source != nil {
		if err := validateTaskSource(*run.Task.Source); err != nil {
			return fmt.Errorf("task.source: %w", err)
		}
	}
	if run.Workflow != nil {
		for i, step := range run.Workflow.Steps {
			if !step.Action.Valid() {
				return fmt.Errorf("workflow step %d: unknown action %q", i, step.Action)
			}
		}
	}

	for i, exec := range run.Executions {
		if err := validateExecution(exec, i); err != nil {
			return fmt.Errorf("executions[%d]: %w", i, err)
		}
	}

	if err := validateProviderCLI(run.ProviderCLI); err != nil {
		return fmt.Errorf("provider_cli: %w", err)
	}
	if t := run.PreRunTest; t != nil && (t.Success != (t.ExitCode == 0) || t.Duration < 0) {
		return fmt.Errorf("pre_run_test: inconsistent result (exit code %d, success %v, duration %s)", t.ExitCode, t.Success, t.Duration)
	}
	for i, r := range run.Resumes {
		if err := validateProviderCLI(r.ProviderCLI); err != nil {
			return fmt.Errorf("resumes[%d].provider_cli: %w", i, err)
		}
		if !r.FromStatus.Valid() {
			return fmt.Errorf("resumes[%d]: unknown status %q", i, r.FromStatus)
		}
		if !r.Entry.Valid() {
			return fmt.Errorf("resumes[%d]: unknown action %q", i, r.Entry)
		}
		if r.Mode != "" && !r.Mode.Valid() {
			return fmt.Errorf("resumes[%d]: unknown mode %q", i, r.Mode)
		}
	}
	return nil
}

func validateExecution(exec domain.StepExecution, i int) error {
	if exec.Index != i {
		return fmt.Errorf("index is %d, want %d", exec.Index, i)
	}
	if !exec.Action.Valid() {
		return fmt.Errorf("unknown action %q", exec.Action)
	}
	// The engine appends an execution only once it has finished, so a persisted entry
	// is never pending or running.
	if exec.Status != domain.StepCompleted && exec.Status != domain.StepFailed {
		return fmt.Errorf("status is %q, want completed or failed", exec.Status)
	}
	if exec.Provider != nil && exec.Provider.Authority != "" && !exec.Provider.Authority.Valid() {
		return fmt.Errorf("unknown authority %q", exec.Provider.Authority)
	}
	if exec.Attempt < 0 {
		return fmt.Errorf("attempt is %d", exec.Attempt)
	}
	// The path is only compared, never used: readers derive it from index and action.
	if want := ArtifactRelPath(i, exec.Action); exec.ArtifactPath != "" && exec.ArtifactPath != want {
		return fmt.Errorf("artifact_path is %q, want %q", exec.ArtifactPath, want)
	}
	return nil
}

// validateTaskSource checks where a task came from (specs/jira/SPEC.md §11.1). The fields
// are only displayed, never used to make a request, but the run directory is
// model-writable, so they are validated like everything else read back.
func validateTaskSource(src domain.TaskSource) error {
	if src.Kind != domain.TaskSourceJira {
		return fmt.Errorf("unknown kind %q", src.Kind)
	}
	if !domain.ValidTicketKey(src.Key) {
		return fmt.Errorf("key %q is not an issue key", src.Key)
	}
	if src.RequestedKey != "" && !domain.ValidTicketKey(src.RequestedKey) {
		return fmt.Errorf("requested_key %q is not an issue key", src.RequestedKey)
	}
	if u, err := url.Parse(src.URL); err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Host == "" {
		return fmt.Errorf("url %q is not an absolute http(s) URL", src.URL)
	}
	if src.FetchedAt.IsZero() {
		return fmt.Errorf("fetched_at is not set")
	}
	return nil
}

// validateProviderCLI checks a recorded provider CLI map: known providers, printable
// versions of at most 64 bytes.
func validateProviderCLI(m map[string]domain.ProviderCLI) error {
	for id, cli := range m {
		if !slices.Contains(provider.SupportedProviders(), provider.ID(id)) {
			return fmt.Errorf("unknown provider %q", id)
		}
		if len(cli.Version) > 64 {
			return fmt.Errorf("%s: version is %d bytes, at most 64", id, len(cli.Version))
		}
		for _, r := range cli.Version {
			if !unicode.IsPrint(r) {
				return fmt.Errorf("%s: version has a non-printable character", id)
			}
		}
	}
	return nil
}

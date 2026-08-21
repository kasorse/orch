// Package action is the composition point that implements workflow.StepExecutor using
// config, provider, repository, and testexec. This is the only package that wires
// AI-provider calls, git inspection, and test execution together into one action.
package action

import (
	"context"
	"errors"
	"fmt"

	"orchestrator/internal/apperr"
	"orchestrator/internal/config"
	"orchestrator/internal/domain"
	"orchestrator/internal/provider"
	"orchestrator/internal/repository"
	"orchestrator/internal/testexec"
)

// ArtifactWriter persists one action's structured output to disk. Dispatcher tolerates a
// nil ArtifactWriter (artifacts are then only carried in memory/RawStdout, not written to
// disk) - this is the documented no-op-persistence state during early build-out.
type ArtifactWriter interface {
	SaveArtifact(runID string, stepIndex int, action domain.ActionType, artifact any) (string, error)
}

// Dispatcher holds per-run in-memory state (last analysis/plan/implementation/test/review)
// so later actions can build context-aware prompts without re-reading artifacts from disk
// mid-run. One Dispatcher instance must be scoped to exactly one run.
type Dispatcher struct {
	Config    *config.Config
	Registry  *provider.Registry
	Repo      *repository.Repository
	Runner    *testexec.Runner
	Artifacts ArtifactWriter

	lastAnalysis *domain.AnalysisArtifact
	lastPlan     *domain.PlanArtifact
	lastImpl     *domain.ImplementationArtifact
	lastTest     *domain.TestResult

	// pendingTestFailure/pendingReviewIssues carry retry context into the next
	// executeImplement call; both are cleared once consumed, since the engine's
	// transition table never has a test failure and a review failure pending at once.
	pendingTestFailure  *domain.TestResult
	pendingReviewIssues []domain.ReviewIssue
}

func New(cfg *config.Config, registry *provider.Registry, repo *repository.Repository, runner *testexec.Runner) *Dispatcher {
	return &Dispatcher{Config: cfg, Registry: registry, Repo: repo, Runner: runner}
}

// call resolves the configured provider for pm, executes the prompt, and - on a
// *apperr.ProviderError - fills in the Action field the adapter deliberately leaves blank.
func (d *Dispatcher) call(ctx context.Context, pm config.ProviderModel, actionName domain.ActionType, prompt string) (provider.Result, domain.ProviderRef, error) {
	ref := domain.ProviderRef{Provider: pm.Provider, Model: pm.Model}

	client, err := d.Registry.Resolve(pm.Provider)
	if err != nil {
		return provider.Result{}, ref, &apperr.WorkflowError{
			Msg: fmt.Sprintf("no provider client registered for %q (action %q)", pm.Provider, actionName),
			Err: err,
		}
	}

	result, err := client.Execute(ctx, provider.Request{Prompt: prompt, Model: pm.Model, WorkingDir: d.Repo.Dir})
	if err != nil {
		var providerErr *apperr.ProviderError
		if errors.As(err, &providerErr) && providerErr.Action == "" {
			providerErr.Action = string(actionName)
		}
		return provider.Result{}, ref, err
	}
	return result, ref, nil
}

// saveArtifact is best-effort: a failure to persist an artifact does not fail the run,
// since the in-memory artifact (carried via Dispatcher state and StepOutcome.RawStdout)
// remains the one actually driving the workflow.
func (d *Dispatcher) saveArtifact(run *domain.Run, actionName domain.ActionType, artifact any) string {
	if d.Artifacts == nil {
		return ""
	}
	path, err := d.Artifacts.SaveArtifact(run.ID, len(run.Executions), actionName, artifact)
	if err != nil {
		return ""
	}
	return path
}

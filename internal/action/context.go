// Package action is the composition point that implements workflow.StepExecutor using
// config, provider, repository, and testexec. This is the only package that wires
// AI-provider calls, git inspection, and test execution together into one action.
package action

import (
	"context"
	"errors"
	"fmt"
	"io"
	"time"

	"orchestrator/internal/apperr"
	"orchestrator/internal/config"
	"orchestrator/internal/domain"
	"orchestrator/internal/projectctx"
	"orchestrator/internal/provider"
	"orchestrator/internal/repository"
	"orchestrator/internal/testexec"
	"orchestrator/internal/workflow"
)

// CallObserver receives progress notifications around each provider call, so app.go can
// wire in CLI progress reporting (the "-> running <provider>..." line, and the
// --verbose argv/heartbeat block) without this package importing internal/logging.
// Both methods are called unconditionally; a nil Observer on Dispatcher means no-op.
type CallObserver interface {
	ProviderCallStarted(providerID, model string)
	// ProviderCallHeartbeat fires periodically while a call is in flight (see
	// heartbeatInterval below). Whether anything is printed for it is entirely a
	// rendering decision (e.g. --verbose plus an interactive terminal) - Dispatcher has
	// no notion of verbosity or terminals and always fires it.
	ProviderCallHeartbeat(elapsed time.Duration)
	ProviderCallFinished(providerID, model string, duration time.Duration, err error)
}

// heartbeatInterval is how often CallObserver.ProviderCallHeartbeat fires while a
// provider call is in flight (spec §6.4 Q4).
const heartbeatInterval = 30 * time.Second

// ArtifactWriter persists one action's structured output to disk. Dispatcher tolerates a
// nil ArtifactWriter (artifacts are then only carried in memory/RawStdout, not written to
// disk) - this is the documented no-op-persistence state during early build-out.
type ArtifactWriter interface {
	SaveArtifact(runID string, stepIndex int, action domain.ActionType, artifact any) (string, error)
}

// LogPathBuilder returns the path a step's raw provider output will be written to, without
// writing anything itself - the actual write happens later, in app.runObserver, once
// Execute returns (StepOutcome.RawStdout/RawStderr). This only lets Dispatcher populate
// apperr.ProviderError.LogPath so a failure message can point at that file up front.
type LogPathBuilder interface {
	LogPath(runID string, stepIndex int, actionType domain.ActionType, stream string) string
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
	Logs      LogPathBuilder

	// Observer, if set, receives a start/finish notification around every provider
	// call (classification is notified separately, by the classifier package).
	Observer CallObserver
	// Stream, if set (--verbose only), is passed through to provider.Request.Stream
	// as a live tee of the CLI's combined output. Nil means capture-only.
	Stream io.Writer

	lastResearch *domain.ResearchArtifact
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
// *apperr.ProviderError - fills in the Action field the adapter deliberately leaves blank
// and, if Logs is set, the LogPath the failing step's output will be written to.
func (d *Dispatcher) call(ctx context.Context, run *domain.Run, pm config.ProviderModel, actionName domain.ActionType, prompt string) (provider.Result, domain.ProviderRef, error) {
	authority := domain.AuthorityFor(actionName)
	ref := domain.ProviderRef{Provider: pm.Provider, Model: pm.Model, Authority: authority}

	client, err := d.Registry.Resolve(pm.Provider)
	if err != nil {
		return provider.Result{}, ref, &apperr.WorkflowError{
			Msg: fmt.Sprintf("no provider client registered for %q (action %q)", pm.Provider, actionName),
			Err: err,
		}
	}

	if d.Observer != nil {
		d.Observer.ProviderCallStarted(pm.Provider, pm.Model)
	}
	start := time.Now()

	stopHeartbeat := d.startHeartbeat()
	result, err := client.Execute(ctx, provider.Request{
		Prompt: prompt, Model: pm.Model, WorkingDir: d.Repo.Dir, Stream: d.Stream,
		Authority: authority,
		// The same secret deny list the Context Manager applies: a step that can read
		// files must not read these either (specs/permissions/SPEC.md §6.3).
		DenyRead: projectctx.DenyPatterns(d.Config.Context.ExtraDeny),
	})
	stopHeartbeat()

	if d.Observer != nil {
		d.Observer.ProviderCallFinished(pm.Provider, pm.Model, time.Since(start), err)
	}

	if err != nil {
		var providerErr *apperr.ProviderError
		if errors.As(err, &providerErr) {
			if providerErr.Action == "" {
				providerErr.Action = string(actionName)
			}
			if providerErr.Kind == apperr.FailureTimeout {
				if t, ok := ctx.Value(actionTimeoutKey{}).(time.Duration); ok {
					providerErr.ConfiguredTimeout = t
				}
			}
			if d.Logs != nil && providerErr.LogPath == "" {
				stream := "stderr"
				if result.Output != "" {
					stream = "stdout"
				}
				providerErr.LogPath = d.Logs.LogPath(run.ID, len(run.Executions), actionName, stream)
			}
		}
		// result is still returned here (never provider.Result{}) - it carries whatever
		// stdout/stderr the CLI produced before failing, which the caller needs to
		// populate StepOutcome.RawStdout/RawStderr for logging and diagnostics.
		return result, ref, err
	}
	return result, ref, nil
}

// startHeartbeat notifies d.Observer every heartbeatInterval while a provider call is in
// flight, until the returned stop func is called. A nil Observer is a no-op (no goroutine
// spawned).
func (d *Dispatcher) startHeartbeat() func() {
	if d.Observer == nil {
		return func() {}
	}
	done := make(chan struct{})
	go func() {
		ticker := time.NewTicker(heartbeatInterval)
		defer ticker.Stop()
		start := time.Now()
		for {
			select {
			case <-done:
				return
			case <-ticker.C:
				d.Observer.ProviderCallHeartbeat(time.Since(start))
			}
		}
	}()
	return func() { close(done) }
}

// failureOutcome builds the StepOutcome to return alongside a provider-call error so the
// engine's existing StepFinished wiring (called on both the error and success return
// paths) persists the raw stdout/stderr the CLI produced before failing.
func (d *Dispatcher) failureOutcome(ref domain.ProviderRef, result provider.Result) workflow.StepOutcome {
	return workflow.StepOutcome{Provider: &ref, RawStdout: result.Output, RawStderr: result.Stderr}
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

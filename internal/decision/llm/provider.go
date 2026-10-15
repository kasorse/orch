// Package llm is the LLM decision provider (specs/JEV integration/TASKS.md T22): a
// provider CLI (claude, codex, gemini) answers the same questions Jev does, over the
// same state, and returns the same domain types. It is a static config choice
// (decision.provider), never an automatic fallback from Jev.
package llm

import (
	"context"
	"errors"
	"fmt"
	"io"
	"strings"
	"time"

	"orchestrator/internal/apperr"
	"orchestrator/internal/decision"
	"orchestrator/internal/domain"
	"orchestrator/internal/provider"
)

// Log names of the two decision rounds: logs/decide-{context,classify}.{stdout,stderr}.log.
const (
	logContext  = "decide-context"
	logClassify = "decide-classify"
)

// maxErrorBody bounds the model output carried by an InvalidResponse error, like the
// Jev client's response body excerpt.
const maxErrorBody = 2 << 10

const heartbeatInterval = 30 * time.Second

// CallObserver mirrors action.CallObserver structurally, so the console shows the
// decision calls like any other provider call.
type CallObserver interface {
	ProviderCallStarted(providerID, model string)
	ProviderCallHeartbeat(elapsed time.Duration)
	ProviderCallFinished(providerID, model string, duration time.Duration, err error)
}

// Logger persists each call's raw output. Satisfied by *persistence.Store.
type Logger interface {
	AppendCallLog(runID, name, stream, content string) error
	CallLogPath(runID, name, stream string) string
}

// Provider asks a provider CLI. One Provider serves one run (RunID keys its logs).
type Provider struct {
	Client     provider.Client
	ProviderID string
	Model      string
	// Timeout bounds each CLI call (decision.timeout); it runs inside the run's
	// execution.timeout.
	Timeout time.Duration
	// Sources are the complexity questions to ask (TASKS.md В1).
	Sources []domain.ComplexitySource

	RunID    string
	Logger   Logger
	Observer CallObserver
	Stream   io.Writer
}

var _ decision.Provider = (*Provider)(nil)

func (p *Provider) Name() string { return p.ProviderID }

func (p *Provider) Source() domain.DecisionSource { return domain.DecisionSourceLLM }

func (p *Provider) ContextDepth(ctx context.Context, request string) (domain.ContextDepthDecision, error) {
	prompt, err := buildContextPrompt(request)
	if err != nil {
		return domain.ContextDepthDecision{}, p.fail(apperr.DecisionBadRequest, "", err)
	}
	out, usage, err := p.call(ctx, logContext, prompt)
	if err != nil {
		return domain.ContextDepthDecision{}, err
	}
	d, err := parseContextDepth(out)
	if err != nil {
		derr := p.fail(apperr.DecisionInvalidResponse, out, err)
		derr.Usage = usage
		return domain.ContextDepthDecision{}, derr
	}
	d.Usage = usage
	return d, nil
}

func (p *Provider) Classify(ctx context.Context, input domain.ClassificationInput) (domain.TaskClassification, error) {
	prompt, err := buildClassifyPrompt(input, p.Sources)
	if err != nil {
		return domain.TaskClassification{}, p.fail(apperr.DecisionBadRequest, "", err)
	}
	out, usage, err := p.call(ctx, logClassify, prompt)
	if err != nil {
		return domain.TaskClassification{}, err
	}
	tc, err := parseClassification(out, input, p.Sources, p.ProviderID, p.Model)
	if err != nil {
		derr := p.fail(apperr.DecisionInvalidResponse, out, err)
		derr.Usage = usage
		return domain.TaskClassification{}, derr
	}
	tc.Usage = usage
	return tc, nil
}

// call runs one CLI call and turns any failure into a DecisionError, so the
// Classification Service handles it exactly like a Jev failure (manual choice or
// on_failure: fail). There is no retry: a CLI call is slow and its failures are rarely
// transient in the way an HTTP 529 is. The call's usage is returned whenever the CLI
// reported it, and also put on the DecisionError of a failed call.
func (p *Provider) call(ctx context.Context, logName, prompt string) (string, *domain.Usage, error) {
	callCtx, cancel := context.WithTimeout(ctx, p.Timeout)
	defer cancel()

	if p.Observer != nil {
		p.Observer.ProviderCallStarted(p.ProviderID, p.Model)
	}
	start := time.Now()
	stop := p.startHeartbeat()
	// The questions are answered from the request and the project context in the prompt
	// alone, so the call gets no tools.
	result, err := p.Client.Execute(callCtx, provider.Request{
		Prompt: prompt, Model: p.Model, Stream: p.Stream, Authority: domain.AuthorityNone,
		Activity: provider.ActivityHook(p.Observer),
	})
	stop()
	provider.ReportUsage(p.Observer, result)
	if p.Observer != nil {
		p.Observer.ProviderCallFinished(p.ProviderID, p.Model, time.Since(start), err)
	}
	wroteStdout := p.log(logName, "stdout", result.Output)
	wroteStderr := p.log(logName, "stderr", result.Stderr)

	if err == nil {
		return result.Output, result.Usage, nil
	}

	var providerErr *apperr.ProviderError
	if errors.As(err, &providerErr) {
		if providerErr.Action == "" {
			providerErr.Action = logName
		}
		// Point only at a log that exists: a call killed before it printed anything
		// (claude --print writes only when done) leaves no file (MVP.md §23).
		if providerErr.LogPath == "" {
			switch {
			case wroteStderr:
				providerErr.LogPath = p.Logger.CallLogPath(p.RunID, logName, "stderr")
			case wroteStdout:
				providerErr.LogPath = p.Logger.CallLogPath(p.RunID, logName, "stdout")
			}
		}
		if providerErr.Kind == apperr.FailureTimeout && callDeadlineBinds(ctx, callCtx) {
			providerErr.ConfiguredTimeout = p.Timeout
			providerErr.TimeoutSetting = "decision.timeout"
		}
	}
	kind := apperr.DecisionUnavailable
	switch {
	case errors.Is(ctx.Err(), context.Canceled) || (providerErr != nil && providerErr.Kind == apperr.FailureCancelled):
		kind = apperr.DecisionCancelled
	case errors.Is(callCtx.Err(), context.DeadlineExceeded) || (providerErr != nil && providerErr.Kind == apperr.FailureTimeout):
		kind = apperr.DecisionTimeout
	case providerErr != nil && providerErr.APIStatus != 0:
		// The CLI ran and the API refused it: a 400 for a model the endpoint does not serve is
		// a configuration error, not an outage.
		kind = apperr.DecisionKindForStatus(providerErr.APIStatus)
	}
	derr := p.fail(kind, "", fmt.Errorf("%s call failed: %w", logName, err))
	derr.Attempts = 1
	if providerErr != nil {
		derr.StatusCode = providerErr.APIStatus
	}
	derr.Usage = result.Usage
	return "", result.Usage, derr
}

func (p *Provider) fail(kind apperr.DecisionFailureKind, output string, err error) *apperr.DecisionError {
	body := strings.TrimSpace(output)
	if len(body) > maxErrorBody {
		body = strings.ToValidUTF8(body[:maxErrorBody], "") + "... [truncated]"
	}
	return &apperr.DecisionError{Provider: p.ProviderID, Kind: kind, Body: body, Err: err}
}

func (p *Provider) startHeartbeat() func() {
	if p.Observer == nil {
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
				p.Observer.ProviderCallHeartbeat(time.Since(start))
			}
		}
	}()
	return func() { close(done) }
}

// callDeadlineBinds reports whether decision.timeout, rather than the run's
// execution.timeout on ctx, is the deadline callCtx fires at.
func callDeadlineBinds(ctx, callCtx context.Context) bool {
	callDeadline, _ := callCtx.Deadline()
	parentDeadline, ok := ctx.Deadline()
	return !ok || callDeadline.Before(parentDeadline)
}

// log is best-effort: losing a log never fails the run. It reports whether the log
// was written.
func (p *Provider) log(name, stream, content string) bool {
	if p.Logger == nil || content == "" {
		return false
	}
	return p.Logger.AppendCallLog(p.RunID, name, stream, content) == nil
}

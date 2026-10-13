// Package classification is the Classification Service (specs/JEV integration/SPEC.md
// §14, TASKS.md T09): it runs the decision flow for one request - translation,
// pre-check, context collection, final classification, policy - and falls back to the
// user's manual choice when the decision provider fails. It knows the provider only as
// decision.Provider.
package classification

import (
	"context"
	"errors"
	"fmt"
	"time"

	"orchestrator/internal/apperr"
	"orchestrator/internal/decision"
	"orchestrator/internal/domain"
	"orchestrator/internal/interaction"
	"orchestrator/internal/policy"
	"orchestrator/internal/translate"
)

// Translator prepares the request text for the decision provider (T15).
type Translator interface {
	Prepare(ctx context.Context, runID, request string) (translate.Result, error)
}

// ContextBuilder collects project context (T08).
type ContextBuilder interface {
	Build(ctx context.Context, request, requestEN string) (*domain.ProjectContext, error)
}

// Chooser asks the user when the decision provider fails (T16).
type Chooser interface {
	Choose(ctx context.Context, cause error, project *domain.ProjectContext, p domain.DecisionPolicy) (interaction.ManualChoice, error)
}

type Service struct {
	Translator Translator
	Decisions  decision.Provider
	Context    ContextBuilder
	Chooser    Chooser
	Policy     domain.DecisionPolicy
	// Primary is the complexity question the policy decides on (TASKS.md В1).
	Primary domain.ComplexitySource
	// AskOnFailure is decision.on_failure == ask: a provider failure leads to the
	// manual choice. Otherwise the failure is returned.
	AskOnFailure bool
}

// Request is one classification.
type Request struct {
	RunID string
	// Text is the user's request, as typed.
	Text string
	// UserCtx is the context for waiting on the user's manual choice: cancelled by
	// Ctrl-C like the run's, but without execution.timeout's deadline - time the user
	// spends deciding is not the run's time (T12). Nil means the run's context.
	UserCtx context.Context
}

// Timings of each step. A step that did not run is zero.
type Timings struct {
	Translation    time.Duration `json:"translation"`
	PreCheck       time.Duration `json:"pre_check"`
	Context        time.Duration `json:"context"`
	Classification time.Duration `json:"classification"`
	ManualChoice   time.Duration `json:"manual_choice,omitempty"`
}

// Result is everything the decision trace (T11) records.
type Result struct {
	// Provider is the decision provider's name, e.g. "jev".
	Provider string
	// PolicyHash is domain.PolicyHash of the Service's policy, which both the provider's
	// decision and a manual choice after its failure are made under.
	PolicyHash  string
	Translation translate.Result
	Decision    domain.Decision
	// Depth and Classification are the provider's raw answers; nil when that round
	// failed or never ran.
	Depth          *domain.ContextDepthDecision
	Classification *domain.TaskClassification
	// Project is exactly what was sent to the provider; nil for depth none or a
	// failed collection.
	Project *domain.ProjectContext
	// ContextLimited is set when context was needed but could not be collected;
	// ContextError says why (event context_collection_failed).
	ContextLimited bool
	ContextError   string
	// DecisionError is the provider failure that led to the manual choice.
	DecisionError error
	Timings       Timings
	// Usage is what each decision call cost, failed calls included.
	Usage domain.DecisionUsage
}

// Classify runs the decision flow for req. Errors are a cancelled run, a provider
// failure with on_failure: fail (or no terminal to ask on), and wiring bugs.
func (s *Service) Classify(ctx context.Context, req Request) (Result, error) {
	res := Result{Provider: s.Decisions.Name(), PolicyHash: domain.PolicyHash(s.Policy)}

	start := time.Now()
	tr, err := s.Translator.Prepare(ctx, req.RunID, req.Text)
	res.Timings.Translation = time.Since(start)
	res.Usage.Translation = tr.Usage
	if err != nil {
		return res, err
	}
	res.Translation = tr

	start = time.Now()
	depth, err := s.Decisions.ContextDepth(ctx, tr.Text)
	res.Timings.PreCheck = time.Since(start)
	res.Usage.ContextDepth = callUsage(depth.Usage, err)
	if err != nil {
		return s.manual(ctx, req, res, err)
	}
	res.Depth = &depth
	selectedDepth := policy.SelectContextDepth(depth, s.Policy)

	if selectedDepth == domain.ContextDepthProject {
		requestEN := ""
		if tr.Translated {
			requestEN = tr.Text
		}
		start = time.Now()
		project, err := s.Context.Build(ctx, req.Text, requestEN)
		res.Timings.Context = time.Since(start)
		switch {
		case ctx.Err() != nil:
			return res, ctx.Err()
		case err != nil:
			// SPEC.md §14: classification continues with limited context.
			res.ContextLimited = true
			res.ContextError = err.Error()
		default:
			res.Project = project
		}
	}

	start = time.Now()
	tc, err := s.Decisions.Classify(ctx, domain.ClassificationInput{
		Request:        tr.Text,
		ContextDepth:   selectedDepth,
		ContextLimited: res.ContextLimited,
		Project:        res.Project,
	})
	res.Timings.Classification = time.Since(start)
	res.Usage.Classification = callUsage(tc.Usage, err)
	if err != nil {
		return s.manual(ctx, req, res, err)
	}
	res.Classification = &tc

	dec, err := policy.Evaluate(depth, tc, s.Primary, s.Policy)
	if err != nil {
		return res, err
	}
	dec.Source = s.Decisions.Source()
	res.Decision = dec
	return res, nil
}

// manual handles a provider failure: the manual choice when allowed, else the error.
// Cancellation never offers a choice - the user already said stop - and neither does
// the run's own deadline expiring: there would be no time left to run the stages.
func (s *Service) manual(ctx context.Context, req Request, res Result, cause error) (Result, error) {
	if ctx.Err() != nil || isCancellation(ctx, cause) {
		return res, cause
	}
	if !s.AskOnFailure {
		return res, cause
	}
	res.DecisionError = cause

	userCtx := req.UserCtx
	if userCtx == nil {
		userCtx = ctx
	}
	start := time.Now()
	choice, err := s.Chooser.Choose(userCtx, cause, res.Project, s.Policy)
	res.Timings.ManualChoice = time.Since(start)
	if err != nil {
		return res, err
	}
	if choice.Complexity == "" {
		return res, fmt.Errorf("manual choice returned no complexity")
	}

	res.Decision = domain.Decision{
		Source:     domain.DecisionSourceUser,
		Complexity: domain.ComplexityOutcome{Selected: choice.Complexity},
		Stages:     policy.ApplyForced(choice.Complexity, choice.Stages, choice.Excluded, s.Policy),
	}
	if res.Depth != nil {
		res.Decision.ContextDepth = domain.ContextDepthOutcome{
			Selected:    policy.SelectContextDepth(*res.Depth, s.Policy),
			Probability: res.Depth.Probabilities[domain.ContextDepthProject],
			Threshold:   s.Policy.ProjectContextThreshold,
		}
	}
	return res, nil
}

// callUsage is a decision call's usage: the answer's, or a failed call's from its
// DecisionError.
func callUsage(answered *domain.Usage, err error) *domain.Usage {
	if err == nil {
		return answered
	}
	var derr *apperr.DecisionError
	if errors.As(err, &derr) {
		return derr.Usage
	}
	return nil
}

func isCancellation(ctx context.Context, err error) bool {
	if errors.Is(ctx.Err(), context.Canceled) || errors.Is(err, context.Canceled) {
		return true
	}
	var derr *apperr.DecisionError
	return errors.As(err, &derr) && derr.Kind == apperr.DecisionCancelled
}

// Trace builds decision.json from the result and the workflow built from it.
func (r Result) Trace(wf domain.Workflow) domain.DecisionTrace {
	tr := r.Translation
	t := domain.DecisionTrace{
		Request: tr.Original,
		Translation: domain.TranslationTrace{
			Detected:      tr.Detected.Language,
			CyrillicShare: tr.Detected.CyrillicShare,
			Translated:    tr.Translated,
			Failed:        tr.Failed,
			Reason:        tr.Reason,
			Provider:      tr.Provider,
			Model:         tr.Model,
			LatencyMS:     tr.Latency.Milliseconds(),
		},
		Source:         r.Decision.Source,
		Provider:       r.Provider,
		PolicyHash:     r.PolicyHash,
		ContextLimited: r.ContextLimited,
		ContextError:   r.ContextError,
		Complexity:     r.Decision.Complexity,
		Stages:         r.Decision.Stages.Stages,
		Latency: domain.DecisionLatency{
			TranslationMS:    r.Timings.Translation.Milliseconds(),
			PreCheckMS:       r.Timings.PreCheck.Milliseconds(),
			ContextMS:        r.Timings.Context.Milliseconds(),
			ClassificationMS: r.Timings.Classification.Milliseconds(),
			ManualChoiceMS:   r.Timings.ManualChoice.Milliseconds(),
		},
	}
	if tr.Translated {
		t.RequestEN = tr.Text
	}
	if !r.Usage.Empty() {
		u := r.Usage
		t.Usage = &u
	}
	if r.DecisionError != nil {
		t.DecisionError = r.DecisionError.Error()
	}
	if r.Classification != nil {
		t.Model = r.Classification.Model
	}
	if r.Depth != nil {
		t.ContextDepth = &domain.ContextDepthTrace{
			Probabilities:       r.Depth.Probabilities,
			Confidence:          r.Depth.Confidence,
			ContextDepthOutcome: r.Decision.ContextDepth,
		}
	}
	for _, step := range wf.Steps {
		t.Workflow = append(t.Workflow, step.Action)
	}
	return t
}

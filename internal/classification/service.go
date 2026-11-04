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

	"github.com/kasorse/orch/internal/apperr"
	"github.com/kasorse/orch/internal/decision"
	"github.com/kasorse/orch/internal/domain"
	"github.com/kasorse/orch/internal/interaction"
	"github.com/kasorse/orch/internal/policy"
	"github.com/kasorse/orch/internal/translate"
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
	// Clarify is the clarify loop an unclear task goes through (specs/clarify). It may be nil only
	// with ClarifyDisabled: a task below the threshold then has no step to go to.
	Clarify *ClarifyLoop
	// ClarifyDisabled is clarify.enabled: false: the clarity question is asked and recorded, and
	// the run goes on whatever P(clear) is.
	ClarifyDisabled bool
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
	// StoryTask is run --story-task: the shadow scope question is not asked, because a
	// story task is never a story (specs/story/SPEC.md §13). QuestionsHash stays the same.
	StoryTask bool
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
	PolicyHash string
	// QuestionsHash is decision.QuestionsHash() of the questions the provider is asked.
	QuestionsHash string
	Translation   translate.Result
	Decision      domain.Decision
	// Depth and Classification are the provider's raw answers; nil when that round
	// failed or never ran.
	Depth          *domain.ContextDepthDecision
	Classification *domain.TaskClassification
	// Clarity is the trace of the clarity checks (the pre-check's P(clear) against the
	// threshold, then each re-check's); nil when the pre-check failed and for a story task.
	Clarity *domain.ClarityTrace
	// Clarification is what the clarify step agreed about an unclear task, run.json's
	// task.clarification; nil when the task was clear at the first check, the step was off, and
	// when a re-check failed. It is set also when the result comes with a *NeedsInputError.
	Clarification *domain.Clarification
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
	res := Result{Provider: s.Decisions.Name(), PolicyHash: domain.PolicyHash(s.Policy), QuestionsHash: decision.QuestionsHash()}

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

	// The text the decision provider and the Context Manager see: the request, and after a
	// clarification the request followed by the rendered clarification.
	text, query, queryEN := tr.Text, req.Text, ""
	if tr.Translated {
		queryEN = tr.Text
	}

	// P(clear) came in the same call. A story task has its plan in the task file and is not asked.
	if !req.StoryTask {
		clarity := policy.Clarity([]float64{depth.PClear}, s.Policy)
		res.Clarity = &clarity
		switch {
		case s.ClarifyDisabled:
			// res.Clarity points at clarity.
			clarity.Status, clarity.Reason = domain.ClarityDisabled, "clarify.enabled is false"
		case clarity.Status == domain.ClarityUnclear:
			recheckErr, err := s.clarify(ctx, req, &res, tr)
			if err != nil {
				return res, err
			}
			if recheckErr != nil {
				return s.manual(ctx, req, res, recheckErr)
			}
			if rendered := res.Clarification.Render(); rendered != "" {
				// The decision is made on the request and what was agreed about it, translated again
				// when the translator applies to the whole.
				query = req.Text + "\n\n" + rendered
				start = time.Now()
				tr2, err := s.Translator.Prepare(ctx, req.RunID, query)
				res.Timings.Translation += time.Since(start)
				res.Usage.TranslationClarified = tr2.Usage
				if err != nil {
					return res, err
				}
				tr2.Original = req.Text
				res.Translation = tr2
				text, queryEN = tr2.Text, ""
				if tr2.Translated {
					queryEN = tr2.Text
				}
			}
		}
	}

	if selectedDepth == domain.ContextDepthProject {
		start = time.Now()
		project, err := s.Context.Build(ctx, query, queryEN)
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
		Request:        text,
		ContextDepth:   selectedDepth,
		ContextLimited: res.ContextLimited,
		Project:        res.Project,
		SkipScope:      req.StoryTask,
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

// clarify sends an unclear task through the clarify loop and records what it did on res: the
// usage, the operator's time, the clarity trace with every check, and the clarification. recheckErr
// is a re-check that failed, which takes the provider-failure path; err ends the run - a failed
// clarify call, a cancellation, and a *NeedsInputError when the task stays unclear.
func (s *Service) clarify(ctx context.Context, req Request, res *Result, tr translate.Result) (recheckErr, err error) {
	if s.Clarify == nil {
		return nil, &apperr.WorkflowError{Msg: "the task is unclear but no clarify step is configured"}
	}
	requestEN := ""
	if tr.Translated {
		requestEN = tr.Text
	}
	out, err := s.Clarify.Run(ctx, ClarifyInput{
		RunID: req.RunID, Request: req.Text, RequestEN: requestEN, CheckText: tr.Text,
		First: res.Depth.PClear, UserCtx: req.UserCtx,
	})
	res.Usage.Clarify = out.Usage.Clarify
	res.Usage.ClarityRecheck = out.Usage.ClarityRecheck
	res.Timings.ManualChoice += out.Operator
	if err != nil {
		return nil, err
	}
	clarity := policy.Clarity(out.Checks, s.Policy)
	res.Clarity = &clarity
	if out.RecheckErr != nil {
		return out.RecheckErr, nil
	}
	res.Clarification = out.Clarification
	if out.NeedsInput {
		return nil, &NeedsInputError{Clarification: out.Clarification}
	}
	return nil, nil
}

// manual handles a provider failure: the manual choice when allowed, else the error.
// Cancellation and an authentication failure never offer a choice - the user already said stop - and neither does
// the run's own deadline expiring: there would be no time left to run the stages.
func (s *Service) manual(ctx context.Context, req Request, res Result, cause error) (Result, error) {
	if ctx.Err() != nil || isCancellation(ctx, cause) {
		return res, cause
	}
	if !s.AskOnFailure || isAuthFailure(cause) {
		return res, cause
	}
	res.DecisionError = cause

	userCtx := req.UserCtx
	if userCtx == nil {
		userCtx = ctx
	}
	start := time.Now()
	choice, err := s.Chooser.Choose(userCtx, cause, res.Project, s.Policy)
	// The operator's time answering the clarify step, if any, is already in it.
	res.Timings.ManualChoice += time.Since(start)
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

// isAuthFailure: a credential the operator must fix is never answered with a manual
// choice (specs/claude-auth/SPEC.md §9).
func isAuthFailure(err error) bool {
	var perr *apperr.ProviderError
	return errors.As(err, &perr) && perr.Kind == apperr.FailureAuth
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
		QuestionsHash:  r.QuestionsHash,
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
		if r.Classification.Scope != nil {
			scope := *r.Classification.Scope
			t.Scope = &scope
		}
	}
	if r.Depth != nil {
		t.ContextDepth = &domain.ContextDepthTrace{
			Probabilities:       r.Depth.Probabilities,
			Confidence:          r.Depth.Confidence,
			ContextDepthOutcome: r.Decision.ContextDepth,
		}
	}
	if r.Clarity != nil {
		clarity := *r.Clarity
		clarity.Checks = append([]float64(nil), clarity.Checks...)
		t.Clarity = &clarity
	}
	for _, step := range wf.Steps {
		t.Workflow = append(t.Workflow, step.Action)
	}
	return t
}

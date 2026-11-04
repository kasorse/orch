package classification

import (
	"context"
	"strings"
	"time"

	"github.com/kasorse/orch/internal/action"
	"github.com/kasorse/orch/internal/apperr"
	"github.com/kasorse/orch/internal/decision"
	"github.com/kasorse/orch/internal/domain"
	"github.com/kasorse/orch/internal/policy"
)

// The clarify loop (specs/clarify, Approach §2-§4): when the pre-check rates the task below
// policy.clarify.threshold, one clarify call finds what is unclear and what the repository's
// instructions settle, the operator answers the rest (or, under --mode auto, the model's own
// answers stand), the decision provider checks the clarity again, and an operator who is there may
// add to the task up to clarify.max_rounds times. Go decides at every step: the model's points
// never say whether the run goes on, the re-check does.

// Instructions chooses the rules and documentation excerpts the clarify call reads
// (projectctx.Docs).
type Instructions interface {
	Docs(ctx context.Context, request, requestEN string) (domain.Instructions, error)
}

// ClarifyReply is what the clarify call gave: its parsed answer, and its usage also when the call
// failed.
type ClarifyReply struct {
	Answer action.ClarifyAnswer
	Usage  *domain.Usage
}

// ClarifyCaller makes the clarify step's one call, number n (from 1) of the run, with
// domain.AuthorityNone and no tools. Its errors are the run's: a failed clarify call never lets an
// unclear task go on.
type ClarifyCaller interface {
	Clarify(ctx context.Context, runID string, n int, in action.ClarifyPromptInput) (ClarifyReply, error)
}

// Operator is the person at the terminal: they answer the model's points and add to the task.
// *interaction.Clarifier is one.
type Operator interface {
	Answer(ctx context.Context, pClear, threshold float64, points []domain.ClarifyPoint) ([]domain.ClarifyPoint, error)
	Add(ctx context.Context, pClear, threshold float64, open []domain.ClarifyPoint) (string, error)
}

// ClarifyLoop is the clarify step with its re-checks. Operator may be nil when Interactive is
// false; nothing is then read from anyone.
type ClarifyLoop struct {
	Docs      Instructions
	Caller    ClarifyCaller
	Operator  Operator
	Decisions decision.Provider
	Policy    domain.DecisionPolicy
	// Interactive is a run at a terminal outside --mode auto: the operator answers the points and
	// may add to the task. Otherwise the model's own answers stand and an unclear task stops.
	Interactive bool
	// MaxRounds is clarify.max_rounds: how many additions the operator may make.
	MaxRounds int
}

// ClarifyInput is one task to clarify.
type ClarifyInput struct {
	RunID string
	// Request is the task as typed, which the clarify prompt shows and the excerpts are ranked
	// against; RequestEN is its translation, "" when there is none.
	Request   string
	RequestEN string
	// CheckText is the request as the decision provider sees it, which every re-check is asked
	// about together with the rendered clarification.
	CheckText string
	// First is the pre-check's P(clear).
	First float64
	// UserCtx waits on the operator: cancelled by Ctrl-C like the run's, without
	// execution.timeout's deadline. Nil means the run's context.
	UserCtx context.Context
}

// ClarifyResult is how a clarification ended.
type ClarifyResult struct {
	// Clarification is what was agreed, with a check for every round and one more. Nil when a
	// re-check failed (RecheckErr): a clarification without its check is not recorded.
	Clarification *domain.Clarification
	// Checks are the P(clear) of every check made, the pre-check's first.
	Checks []float64
	// NeedsInput: the last check stayed below the threshold and nobody could add to the task.
	NeedsInput bool
	// RecheckErr is the provider failure of a re-check, which takes the decision's provider-failure
	// path; the call's usage is recorded all the same.
	RecheckErr error
	// Usage is what the clarify call and the re-checks cost, failed calls included.
	Usage domain.DecisionUsage
	// Operator is the time the operator spent answering, which is not the run's time.
	Operator time.Duration
}

// Run clarifies in. The errors are a cancelled run, a failed clarify call, instructions that
// could not be chosen and wiring bugs; a failed re-check is in the result.
func (l *ClarifyLoop) Run(ctx context.Context, in ClarifyInput) (ClarifyResult, error) {
	res := ClarifyResult{Checks: []float64{in.First}}
	userCtx := in.UserCtx
	if userCtx == nil {
		userCtx = ctx
	}
	mode := domain.ClarifyAuto
	if l.Interactive {
		mode = domain.ClarifyInteractive
		if l.Operator == nil {
			return res, &apperr.WorkflowError{Msg: "the clarify step is interactive but has no operator to ask"}
		}
	}
	threshold := l.Policy.ClarityThreshold

	instructions, err := l.Docs.Docs(ctx, in.Request, in.RequestEN)
	if err != nil {
		return res, &apperr.WorkflowError{Msg: "failed to choose the instructions for the clarify step", Err: err}
	}
	reply, err := l.Caller.Clarify(ctx, in.RunID, 1, action.ClarifyPromptInput{Task: in.Request, Instructions: instructions, Mode: mode})
	if reply.Usage != nil {
		res.Usage.Clarify = append(res.Usage.Clarify, *reply.Usage)
	}
	if err != nil {
		return res, err
	}

	points := clarifyPoints(reply.Answer, mode)
	if l.Interactive && len(points) > 0 {
		start := time.Now()
		points, err = l.Operator.Answer(userCtx, in.First, threshold, points)
		res.Operator += time.Since(start)
		if err != nil {
			return res, err
		}
	}
	c := &domain.Clarification{
		Threshold: threshold,
		Rounds:    []domain.ClarifyRound{{Kind: domain.ClarifyRoundClarify, Mode: mode, Points: points}},
	}

	for additions := 0; ; {
		d, err := l.Decisions.Clarity(ctx, in.CheckText, c.Render())
		if u := callUsage(d.Usage, err); u != nil {
			res.Usage.ClarityRecheck = append(res.Usage.ClarityRecheck, *u)
		}
		if err != nil {
			if ctx.Err() != nil || isCancellation(ctx, err) {
				return res, err
			}
			res.RecheckErr = err
			return res, nil
		}
		res.Checks = append(res.Checks, d.PClear)
		if policy.ClearEnough(d.PClear, l.Policy) {
			c.Status = domain.ClarityClear
			break
		}
		c.Status = domain.ClarityUnclear
		if !l.Interactive || additions >= l.MaxRounds {
			res.NeedsInput = true
			break
		}
		start := time.Now()
		text, err := l.Operator.Add(userCtx, d.PClear, threshold, c.Open())
		res.Operator += time.Since(start)
		if err != nil {
			return res, err
		}
		if strings.TrimSpace(text) == "" {
			res.NeedsInput = true
			break
		}
		additions++
		c.Rounds = append(c.Rounds, domain.ClarifyRound{Kind: domain.ClarifyRoundAddition, Text: text})
	}
	c.Checks = append([]float64(nil), res.Checks...)
	res.Clarification = c
	return res, nil
}

// clarifyPoints turns the model's answer into the points of the clarify round: basis docs is
// answered by docs, assumption (auto only) by assumption, and none stays open.
func clarifyPoints(a action.ClarifyAnswer, mode domain.ClarifyMode) []domain.ClarifyPoint {
	var out []domain.ClarifyPoint
	for _, p := range a.Points {
		point := domain.ClarifyPoint{Unclear: p.Unclear, Question: p.Question}
		switch p.Basis {
		case action.ClarifyBasisDocs:
			point.Answer, point.By, point.Source = p.Answer, domain.ClarifyByDocs, p.Source
		case action.ClarifyBasisAssumption:
			if mode == domain.ClarifyAuto {
				point.Answer, point.By = p.Answer, domain.ClarifyByAssumption
			}
		}
		out = append(out, point)
	}
	return out
}

// NeedsInputError is a task that stayed unclear after the clarify step and everything an operator
// could add: the run ends stopped, with "task needs input". The Result returned with it holds the
// clarification.
type NeedsInputError struct {
	Clarification *domain.Clarification
}

func (e *NeedsInputError) Error() string { return domain.FailureTaskNeedsInput }

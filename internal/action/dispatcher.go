package action

import (
	"context"
	"time"

	"orchestrator/internal/apperr"
	"orchestrator/internal/domain"
	"orchestrator/internal/workflow"
)

// Execute implements workflow.StepExecutor. Actions with a configured
// actions.<name>.timeout run under a context nested inside ctx (never past it - see
// actionTimeout) so that, for example, a slow review no longer inherits whatever
// happens to be left of the run-wide execution.timeout after earlier steps.
func (d *Dispatcher) Execute(ctx context.Context, actionType domain.ActionType, run *domain.Run) (workflow.StepOutcome, error) {
	ctx, cancel := d.withActionTimeout(ctx, actionType)
	defer cancel()

	// Every action makes at most one provider call; call records its usage here, so the
	// outcome carries it whichever return path the action took (failures included).
	d.stepUsage, d.stepCall, d.stepTruncated = nil, nil, nil
	outcome, err := d.execute(ctx, actionType, run)
	outcome.Usage = d.stepUsage
	outcome.Call = d.stepCall
	return outcome, err
}

func (d *Dispatcher) execute(ctx context.Context, actionType domain.ActionType, run *domain.Run) (workflow.StepOutcome, error) {
	switch actionType {
	case domain.ActionResearch:
		return d.executeResearch(ctx, run)
	case domain.ActionAnalyze:
		return d.executeAnalyze(ctx, run)
	case domain.ActionPlan:
		return d.executePlan(ctx, run)
	case domain.ActionImplement:
		return d.executeImplement(ctx, run)
	case domain.ActionTest:
		return d.executeTest(ctx, run)
	case domain.ActionReview:
		return d.executeReview(ctx, run)
	default:
		return workflow.StepOutcome{}, &apperr.WorkflowError{Msg: "no action handler for " + string(actionType)}
	}
}

// actionTimeoutKey carries the action's timeout override on ctx, but only when it is
// actually the binding constraint (see withActionTimeout) - so call (context.go) can
// tell a resulting *apperr.ProviderError which limit really fired: the action's own
// override, or (key absent) the run-wide execution.timeout budget it never got the
// chance to reach. Threading this on ctx, rather than through mutable Dispatcher state,
// keeps it scoped to the one Execute call it belongs to.
type actionTimeoutKey struct{}

// withActionTimeout wraps ctx with the action's configured timeout, if any. Since ctx
// already carries the run-wide execution.timeout deadline (app.go), context.WithTimeout
// can only tighten that bound, never extend it - there is still no un-timed phase and
// no way for one action's override to let the run outlive execution.timeout (MVP.md
// §25). An unset (zero) override leaves ctx untouched, exactly matching pre-existing
// behavior. The binding-constraint comparison uses the two contexts' own Deadline()
// values (both anchored to the single time.Now() that context.WithTimeout reads
// internally) rather than a separately-sampled time.Until, so the two can't disagree
// about which deadline is earlier.
func (d *Dispatcher) withActionTimeout(ctx context.Context, actionType domain.ActionType) (context.Context, context.CancelFunc) {
	timeout := d.actionTimeout(actionType)
	if timeout <= 0 {
		return ctx, func() {}
	}
	newCtx, cancel := context.WithTimeout(ctx, timeout)
	newDeadline, _ := newCtx.Deadline()
	if parentDeadline, ok := ctx.Deadline(); !ok || newDeadline.Before(parentDeadline) {
		newCtx = context.WithValue(newCtx, actionTimeoutKey{}, timeout)
	}
	return newCtx, cancel
}

func (d *Dispatcher) actionTimeout(actionType domain.ActionType) time.Duration {
	switch actionType {
	case domain.ActionResearch:
		return d.Config.Actions.Research.Timeout
	case domain.ActionAnalyze:
		return d.Config.Actions.Analyze.Timeout
	case domain.ActionPlan:
		return d.Config.Actions.Plan.Timeout
	case domain.ActionImplement:
		return d.Config.Actions.Implement.Timeout
	case domain.ActionReview:
		return d.Config.Actions.Review.Timeout
	default:
		return 0
	}
}

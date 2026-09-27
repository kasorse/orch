package interaction

import (
	"context"
	"fmt"
	"io"
	"strings"

	"orchestrator/internal/domain"
	"orchestrator/internal/workflow"
)

// TerminalGate implements workflow.Gate for manual mode: it confirms before every
// step, and on decline offers retry/abort (stage editing returns in T19). Once the user picks "all
// remaining", every later BeforeStep call proceeds without prompting.
type TerminalGate struct {
	prompter    Prompter
	out         io.Writer
	passthrough bool

	// Changes, if set, lists the working tree's uncommitted changes; the gate shows it
	// before the step that follows an implement, so the human sees what was written
	// before anything (test in particular) executes it.
	Changes func(ctx context.Context) (string, error)
}

func NewTerminalGate(p Prompter, out io.Writer) *TerminalGate {
	return &TerminalGate{prompter: p, out: out}
}

func (g *TerminalGate) BeforeStep(ctx context.Context, run *domain.Run, index int, action domain.ActionType) (workflow.GateDecision, error) {
	if g.passthrough {
		return workflow.GateProceed, nil
	}
	g.showImplementChanges(ctx, run)

	for {
		ans, err := g.prompter.Ask("Run this step? [y]es / [n]o / [A]ll remaining: ")
		if err != nil {
			return "", err
		}

		switch strings.ToLower(ans) {
		case "y", "yes", "":
			return workflow.GateProceed, nil

		case "a", "all":
			g.passthrough = true
			return workflow.GateProceed, nil

		case "n", "no":
			decision, err := g.declineMenu()
			if err != nil {
				return "", err
			}
			return decision, nil

		default:
			fmt.Fprintln(g.out, "unrecognized option")
		}
	}
}

func (g *TerminalGate) declineMenu() (workflow.GateDecision, error) {
	for {
		ans, err := g.prompter.Ask("[r]etry / [a]bort: ")
		if err != nil {
			return "", err
		}

		switch strings.ToLower(ans) {
		case "r", "retry":
			return workflow.GateProceed, nil

		case "a", "abort":
			return workflow.GateAbort, nil

		default:
			fmt.Fprintln(g.out, "unrecognized option")
		}
	}
}

// showImplementChanges prints what the last step wrote when that step was a completed
// implement. Reaching the gate means the protected-path guard passed.
func (g *TerminalGate) showImplementChanges(ctx context.Context, run *domain.Run) {
	if g.Changes == nil || len(run.Executions) == 0 {
		return
	}
	last := run.Executions[len(run.Executions)-1]
	if last.Action != domain.ActionImplement || last.Status != domain.StepCompleted {
		return
	}
	summary, err := g.Changes(ctx)
	if err != nil {
		fmt.Fprintf(g.out, "Changes: unavailable (%v)\n", err)
		return
	}
	if summary == "" {
		summary = "(none)"
	}
	fmt.Fprintf(g.out, "Changes in the working tree:\n%s\nProtected paths: unchanged\n", summary)
}

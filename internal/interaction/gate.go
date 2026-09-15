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
// step, and on decline offers retry/edit-plan/abort. Once the user picks "all
// remaining", every later BeforeStep call proceeds without prompting.
type TerminalGate struct {
	prompter    Prompter
	out         io.Writer
	passthrough bool
}

func NewTerminalGate(p Prompter, out io.Writer) *TerminalGate {
	return &TerminalGate{prompter: p, out: out}
}

func (g *TerminalGate) BeforeStep(ctx context.Context, run *domain.Run, index int, action domain.ActionType) (workflow.GateDecision, error) {
	if g.passthrough {
		return workflow.GateProceed, nil
	}

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
			decision, err := g.declineMenu(run, action)
			if err != nil {
				return "", err
			}
			return decision, nil

		default:
			fmt.Fprintln(g.out, "unrecognized option")
		}
	}
}

func (g *TerminalGate) declineMenu(run *domain.Run, action domain.ActionType) (workflow.GateDecision, error) {
	for {
		ans, err := g.prompter.Ask("[r]etry / [e]dit plan / [a]bort: ")
		if err != nil {
			return "", err
		}

		switch strings.ToLower(ans) {
		case "r", "retry":
			return workflow.GateProceed, nil

		case "e", "edit":
			if err := editLoop(g.prompter, g.out, run, false); err != nil {
				return "", err
			}
			printRemainingWorkflow(g.out, run.Workflow, action)

		case "a", "abort":
			return workflow.GateAbort, nil

		default:
			fmt.Fprintln(g.out, "unrecognized option")
		}
	}
}

// printRemainingWorkflow prints the actions from fromAction onward in wf, after a
// mid-run edit. If fromAction can't be located (e.g. it was edited out), it prints the
// full remaining workflow.
func printRemainingWorkflow(out io.Writer, wf *domain.Workflow, fromAction domain.ActionType) {
	if wf == nil {
		return
	}
	start := 0
	for i, s := range wf.Steps {
		if s.Action == fromAction {
			start = i
			break
		}
	}
	names := make([]string, 0, len(wf.Steps)-start)
	for _, s := range wf.Steps[start:] {
		names = append(names, string(s.Action))
	}
	fmt.Fprintf(out, "\nWorkflow (remaining): %s\n", strings.Join(names, " → "))
}

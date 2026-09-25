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

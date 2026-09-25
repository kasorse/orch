package interaction

import (
	"fmt"
	"io"
	"strings"

	"orchestrator/internal/domain"
)

// ReviewAndSelectMode asks the user to approve the decided workflow (already printed by
// the caller), then resolves the execution mode - pinnedMode if the user passed --mode,
// otherwise an interactive 4-way choice. approved is false only when the user cancels,
// in which case mode is meaningless and the run must not proceed.
//
// There is no editing here until T19 brings stage editing back on top of the stage
// decision (the old type/complexity editor went with the classifier in T12).
func ReviewAndSelectMode(p Prompter, out io.Writer, pinnedMode domain.RunMode) (mode domain.RunMode, approved bool, err error) {
	for {
		ans, err := p.Ask("\nApprove this workflow? [a]pprove / [c]ancel: ")
		if err != nil {
			return "", false, err
		}

		switch strings.ToLower(ans) {
		case "a", "approve", "":
			mode, err := resolveMode(p, out, pinnedMode)
			if err != nil {
				return "", false, err
			}
			return mode, true, nil

		case "c", "cancel":
			return "", false, nil

		default:
			fmt.Fprintln(out, "unrecognized option")
		}
	}
}

func resolveMode(p Prompter, out io.Writer, pinnedMode domain.RunMode) (domain.RunMode, error) {
	if pinnedMode != "" {
		return pinnedMode, nil
	}

	for {
		ans, err := p.Ask("\nChoose execution mode: [1] plan  [2] manual  [3] accept-edits  [4] auto: ")
		if err != nil {
			return "", err
		}
		switch strings.ToLower(ans) {
		case "1", "plan":
			return domain.ModePlan, nil
		case "2", "manual":
			return domain.ModeManual, nil
		case "3", "accept-edits":
			return domain.ModeAcceptEdits, nil
		case "4", "auto":
			return domain.ModeAuto, nil
		default:
			fmt.Fprintln(out, "unrecognized option")
		}
	}
}

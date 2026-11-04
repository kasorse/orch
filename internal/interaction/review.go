package interaction

import (
	"fmt"
	"io"
	"strings"

	"github.com/kasorse/orch/internal/domain"
)

// ReviewAndSelectMode asks the user to approve the decided workflow (already printed by
// the caller), then resolves the execution mode - pinnedMode if the user passed --mode,
// otherwise an interactive 4-way choice, where an empty answer picks defaultMode when it
// is set (a resume offers the mode the run had; specs/resume/SPEC.md §10). approved is
// false only when the user cancels, in which case mode is meaningless and the run must
// not proceed.
//
// There is no editing here until T19 brings stage editing back on top of the stage
// decision (the old type/complexity editor went with the classifier in T12).
func ReviewAndSelectMode(p Prompter, out io.Writer, pinnedMode, defaultMode domain.RunMode) (mode domain.RunMode, approved bool, err error) {
	for {
		ans, err := p.Ask("\nApprove this workflow? [a]pprove / [c]ancel: ")
		if err != nil {
			return "", false, err
		}

		switch strings.ToLower(ans) {
		case "a", "approve", "":
			mode, err := resolveMode(p, out, pinnedMode, defaultMode)
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

func resolveMode(p Prompter, out io.Writer, pinnedMode, defaultMode domain.RunMode) (domain.RunMode, error) {
	if pinnedMode != "" {
		return pinnedMode, nil
	}

	question := "\nChoose execution mode: [1] plan  [2] manual  [3] accept-edits  [4] auto: "
	if defaultMode != "" {
		question = fmt.Sprintf("\nChoose execution mode: [1] plan  [2] manual  [3] accept-edits  [4] auto (Enter: %s): ", defaultMode)
	}
	for {
		ans, err := p.Ask(question)
		if err != nil {
			return "", err
		}
		if ans == "" && defaultMode != "" {
			return defaultMode, nil
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

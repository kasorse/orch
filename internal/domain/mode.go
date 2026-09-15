package domain

import "fmt"

// RunMode selects how much user interaction happens before and during a run
// (MVP.md §1/§27). It is orthogonal to Classification/Workflow - the same
// classification always produces the same workflow regardless of mode.
type RunMode string

const (
	ModePlan        RunMode = "plan"
	ModeManual      RunMode = "manual"
	ModeAcceptEdits RunMode = "accept-edits"
	ModeAuto        RunMode = "auto"
)

var validRunModes = map[RunMode]bool{
	ModePlan:        true,
	ModeManual:      true,
	ModeAcceptEdits: true,
	ModeAuto:        true,
}

// ParseRunMode validates a --mode flag value. An empty string is not a valid
// RunMode here - callers treat "no --mode given" as its own case before calling this.
func ParseRunMode(s string) (RunMode, error) {
	m := RunMode(s)
	if !validRunModes[m] {
		return "", fmt.Errorf("unsupported mode %q (want one of: plan, manual, accept-edits, auto)", s)
	}
	return m, nil
}

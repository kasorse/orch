package domain

import (
	"fmt"
	"regexp"
	"strings"
	"time"
)

// The two tools of the in-session verify server (specs/quality/SPEC.md §6.1).
const (
	VerifyToolRunTests = "run_tests"
	VerifyToolRunCheck = "run_check"
)

// VerifyFilterPattern is the filter a run_tests call may carry, as the JSON Schema of the
// tool states it (specs/quality/SPEC.md §6.2). The filter reaches the test command only as
// the environment variable ORCH_TEST_FILTER, so it can hold no shell syntax.
const VerifyFilterPattern = `^[A-Za-z0-9_./:-]{1,200}$`

// VerifyFilterRE is VerifyFilterPattern as Go enforces it: \z, not $, so that a trailing
// newline is never accepted whatever the matching mode.
var VerifyFilterRE = regexp.MustCompile(`^[A-Za-z0-9_./:-]{1,200}\z`)

// MaxVerifyCalls is the most calls per step limits.verify.max_calls_per_step may allow.
const MaxVerifyCalls = 50

// VerifyCall is one tools/call the verify server answered or refused, one JSON line of its
// calls.jsonl (specs/quality/SPEC.md §6.8). A refused call ran nothing: Refused holds the
// one-line reason, and the result fields stay zero.
type VerifyCall struct {
	Tool string `json:"tool"`
	// Check is the check's name, set iff Tool is run_check.
	Check string `json:"check,omitempty"`
	// Filter is the validated run_tests filter, when one was given.
	Filter   string        `json:"filter,omitempty"`
	ExitCode int           `json:"exit_code"`
	Success  bool          `json:"success"`
	Duration time.Duration `json:"duration"`
	// OutputCut is true when the answer shown to the model was shortened; the call's log
	// files keep everything.
	OutputCut bool   `json:"output_cut,omitempty"`
	Refused   string `json:"refused,omitempty"`
}

// maxVerifyRefusedBytes bounds the reason of a refused call; the server's own reasons are
// far shorter.
const maxVerifyRefusedBytes = 300

// Validate checks a recorded call. calls.jsonl is written by a process that runs model code,
// so everything is checked as if it were hostile: a check name ends up in a log file name and
// a reason in the report. A refused run_check may name no check, because its arguments were
// the reason it was refused.
func (c VerifyCall) Validate() error {
	switch c.Tool {
	case VerifyToolRunTests:
		if c.Check != "" {
			return fmt.Errorf("check %q on a %s call", c.Check, c.Tool)
		}
	case VerifyToolRunCheck:
		if c.Check == "" && c.Refused == "" {
			return fmt.Errorf("%s call without a check", c.Tool)
		}
		if c.Check != "" && !CheckNameRE.MatchString(c.Check) {
			return fmt.Errorf("check %q is not a check name", c.Check)
		}
		if c.Filter != "" {
			return fmt.Errorf("filter on a %s call", c.Tool)
		}
	default:
		return fmt.Errorf("unknown tool %q", c.Tool)
	}
	if c.Filter != "" && !VerifyFilterRE.MatchString(c.Filter) {
		return fmt.Errorf("filter %q does not match %s", c.Filter, VerifyFilterPattern)
	}
	if c.Duration < 0 {
		return fmt.Errorf("duration is %s", c.Duration)
	}
	if c.Refused != "" {
		switch {
		case len(c.Refused) > maxVerifyRefusedBytes || strings.ContainsAny(c.Refused, "\r\n"):
			return fmt.Errorf("refused is not a short single line")
		case c.ExitCode != 0 || c.Success || c.Duration != 0 || c.OutputCut:
			return fmt.Errorf("a refused call carries a result")
		}
		return nil
	}
	if c.Success != (c.ExitCode == 0) {
		return fmt.Errorf("inconsistent result (exit code %d, success %v)", c.ExitCode, c.Success)
	}
	return nil
}

package domain

import (
	"regexp"
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

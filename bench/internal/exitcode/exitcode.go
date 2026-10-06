// Package exitcode centralizes the mapping from returned errors to process exit codes.
// cmd/orchbench/main.go is the only call site for os.Exit in this module.
package exitcode

import (
	"context"
	"errors"
)

const (
	OK          = 0
	Failed      = 1
	Usage       = 2
	Budget      = 3
	Interrupted = 130
)

// UsageError is a mistake in how orchbench was called, or something it needs and does not
// find: a bad flag, an unknown command, a missing prerequisite, a command not built yet.
// Nothing ran.
type UsageError struct {
	Msg string
}

func (e UsageError) Error() string { return e.Msg }

// BudgetError reports that `run` stopped launching cells because the recorded cost reached
// --max-cost.
type BudgetError struct {
	Msg string
}

func (e BudgetError) Error() string { return e.Msg }

// FromError maps a returned error to a process exit code. nil maps to OK.
func FromError(err error) int {
	if err == nil {
		return OK
	}

	if errors.Is(err, context.Canceled) {
		return Interrupted
	}

	var usageErr UsageError
	if errors.As(err, &usageErr) {
		return Usage
	}

	var budgetErr BudgetError
	if errors.As(err, &budgetErr) {
		return Budget
	}

	return Failed
}

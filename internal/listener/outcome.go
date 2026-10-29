package listener

import (
	"strings"

	"orchestrator/internal/domain"
)

// The exit codes of a child run (specs/delivery/SPEC.md §11.3), as the listener reads them. The
// package does not import exitcode: it is pure, and the numbers are an interface.
const (
	ExitOK        = 0
	ExitError     = 1
	ExitConfig    = 2
	ExitProvider  = 3
	ExitWorkflow  = 4
	ExitCancelled = 5
	ExitNeeds     = 6
)

// FailureKindAuth is the "failure_kind" of a child that failed on a credential.
const FailureKindAuth = "auth"

// CommentKind is which of Go's templates the comment of an outcome is made from.
type CommentKind string

const (
	// CommentNone writes no comment.
	CommentNone CommentKind = ""
	// CommentMergeRequest links the merge request the run opened.
	CommentMergeRequest CommentKind = "merge_request"
	// CommentNeedsInput lists the open points of a task that stayed unclear.
	CommentNeedsInput CommentKind = "needs_input"
	// CommentFailure gives the reason a run did not deliver, and the last test or check that failed.
	CommentFailure CommentKind = "failure"
	// CommentError gives the error a child printed.
	CommentError CommentKind = "error"
)

// RunOutcome is what the listener knows of a finished child: its exit code, the one JSON line it
// printed and the run.json it left. Fields it could not read are zero.
type RunOutcome struct {
	ExitCode int
	// FailureKind is "failure_kind" of the JSON line.
	FailureKind string
	// FailureReason is run.failure_reason.
	FailureReason string
	// MRCreated is run.mr.state == created.
	MRCreated bool
}

// Decision is what the table of SPEC §8 makes of a RunOutcome.
type Decision struct {
	Comment    CommentKind
	Transition Result
	// Counted says the run is one of the ticket's attempts.
	Counted bool
	// Backoff says the ticket waits before it is picked again (an infrastructure failure).
	Backoff bool
	// Stop says the listener stops with exit 2 after this (a credential).
	Stop bool
}

// Decide is the table of SPEC §8: from the child's exit code, its failure kind and its run to the
// comment, the transition, whether an attempt is used up, and whether the listener stops.
func Decide(o RunOutcome) Decision {
	if o.FailureKind == FailureKindAuth {
		return Decision{Transition: ResultRetry, Stop: true}
	}
	switch o.ExitCode {
	case ExitOK:
		if o.MRCreated {
			return Decision{Comment: CommentMergeRequest, Transition: ResultDone, Counted: true}
		}
		// Delivered nothing a reviewer can open: the service's config does not push, or the line lied.
		return Decision{Comment: CommentFailure, Transition: ResultFailed, Counted: true}
	case ExitWorkflow:
		if o.FailureReason == domain.FailureTaskNeedsInput {
			return Decision{Comment: CommentNeedsInput, Transition: ResultBlocked, Counted: true}
		}
		return Decision{Comment: CommentFailure, Transition: ResultFailed, Counted: true}
	case ExitNeeds:
		return Decision{Comment: CommentFailure, Transition: ResultFailed, Counted: true}
	case ExitProvider:
		return Decision{Transition: ResultRetry, Backoff: true}
	case ExitCancelled:
		return Decision{Transition: ResultNone}
	}
	// 1, 2 without a credential, and anything else: the error as printed.
	return Decision{Comment: CommentError, Transition: ResultFailed, Counted: true}
}

// Infrastructure is the decision for a clone or fetch that failed on the network or the remote: the
// ticket goes back to the queue, no attempt is counted, and it waits.
func Infrastructure() Decision { return Decision{Transition: ResultRetry, Backoff: true} }

// CutOff reports whether a child that ended with code was cut off by the listener's own stop: while
// the listener is stopping, only a child that reached a verdict of its own (delivered, a workflow
// failure, needs a person) has its outcome written. Any other end (a provider call or a step the
// interrupt cut, exit 1, 2, 3 or 5) is resumed when the listener starts again (SPEC §8, §10).
func CutOff(code int, stopping bool) bool {
	if !stopping {
		return false
	}
	switch code {
	case ExitOK, ExitWorkflow, ExitNeeds:
		return false
	}
	return true
}

// Before is the decision for a ticket that cannot be run at all (the service or the project is not
// named, the ticket is too long, an Epic): a comment and the blocked or the failed transition, no
// attempt.
func Before(blocked bool) Decision {
	if blocked {
		return Decision{Comment: CommentError, Transition: ResultBlocked}
	}
	return Decision{Comment: CommentError, Transition: ResultFailed}
}

// ExitFromRun is the exit code and the failure kind a child of a run that ended without anyone
// reading its line would have had, from run.json: used when the listener restarts and finds the run
// finished. A failed run's reason is the error that ended it as rendered, which names a provider
// failure and its kind, so a provider failure still goes to retry and a credential still stops.
func ExitFromRun(status domain.RunStatus, failureReason string, mrCreated bool) (int, string) {
	switch status {
	case domain.RunCompleted:
		if mrCreated {
			return ExitOK, ""
		}
		return ExitNeeds, ""
	case domain.RunStopped:
		if ResumableReason(failureReason) {
			return ExitCancelled, ""
		}
		return ExitWorkflow, ""
	case domain.RunFailed:
		if failureReason == domain.FailureTaskNeedsInput {
			return ExitWorkflow, ""
		}
		return exitFromFailure(failureReason)
	}
	return ExitCancelled, ""
}

// exitFromFailure reads a failed run's reason as the exit code mapping reads the error it was
// rendered from (exitcode.FromError): a provider CLI's error (apperr.ProviderError) and a decision
// provider's (apperr.DecisionError) say how they failed; anything else is exit 1.
func exitFromFailure(reason string) (int, string) {
	if strings.Contains(reason, "provider execution failed\n") {
		switch {
		case strings.Contains(reason, "\nfailure: auth ("):
			return ExitConfig, FailureKindAuth
		case strings.Contains(reason, "\nfailure: cancelled ("):
			return ExitCancelled, ""
		}
		return ExitProvider, ""
	}
	if _, rest, ok := strings.Cut(reason, "decision provider "); ok {
		line, _, _ := strings.Cut(rest, "\n")
		switch {
		case strings.Contains(line, " failed: auth ("):
			return ExitConfig, FailureKindAuth
		case strings.Contains(line, " failed: cancelled ("):
			return ExitCancelled, ""
		case strings.Contains(line, " failed: "):
			return ExitProvider, ""
		}
	}
	return ExitError, ""
}

// ResumableReason reports whether a stopped run's failure reason says it was interrupted by the
// operator (or the listener's own stop), so that resuming it carries on instead of failing again.
func ResumableReason(reason string) bool {
	return reason == "execution cancelled" || reason == "cancelled by user" || reason == "aborted by user"
}

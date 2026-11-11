package app

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"os"

	"github.com/kasorse/orch/internal/apperr"
	"github.com/kasorse/orch/internal/domain"
	"github.com/kasorse/orch/internal/exitcode"
	"github.com/kasorse/orch/internal/persistence"
)

// outputJSON is the only value of --output: stdout gets the one result line (specs/delivery/SPEC.md
// §11.2).
const outputJSON = "json"

// resultVersion is the "v" of the result line; it changes only on an incompatible change.
const resultVersion = 1

// noTerminalMessage is the usage error of a run, resume or review that would ask a question with
// nobody to answer it (specs/delivery/SPEC.md §11.1).
const noTerminalMessage = "stdin is not a terminal: pass --mode auto (or plan, accept-edits) to run without questions"

// headlessCommand reports whether --output json is valid with the command.
func headlessCommand(name string) bool {
	return name == "run" || name == "resume" || name == "review"
}

// checkTerminal is the usage error for a command that asks questions (no --mode, or --mode
// manual) when stdin cannot answer them. It runs before anything is loaded, created or called.
func checkTerminal(mode domain.RunMode, stdin io.Reader) error {
	if (mode == "" || mode == domain.ModeManual) && !isTerminal(stdin) {
		return &apperr.ConfigError{Msg: noTerminalMessage}
	}
	return nil
}

// resultRecorder remembers the run a command works on, so the result line can be built from it
// when the command returns. It stays empty while no run exists.
type resultRecorder struct {
	run   *domain.Run
	store *persistence.Store
}

type resultRecorderKey struct{}

// withResultRecorder returns a context that carries a recorder for recordResultRun.
func withResultRecorder(ctx context.Context) (context.Context, *resultRecorder) {
	rec := &resultRecorder{}
	return context.WithValue(ctx, resultRecorderKey{}, rec), rec
}

// recordResultRun tells the recorder in ctx, if there is one, which run the command works on. The
// run is a pointer the engine goes on updating, so the line shows how it ended.
func recordResultRun(ctx context.Context, store *persistence.Store, run *domain.Run) {
	if rec, ok := ctx.Value(resultRecorderKey{}).(*resultRecorder); ok {
		rec.run, rec.store = run, store
	}
}

// runResult is the line --output json writes to stdout (specs/delivery/SPEC.md §11.2). Every field
// is always there: one that does not apply is empty or null.
type runResult struct {
	V                int       `json:"v"`
	RunID            string    `json:"run_id"`
	Status           string    `json:"status"`
	ExitCode         int       `json:"exit_code"`
	FailureReason    string    `json:"failure_reason"`
	FailureKind      string    `json:"failure_kind"`
	Error            string    `json:"error"`
	Workflow         []string  `json:"workflow"`
	Commit           string    `json:"commit"`
	Branch           string    `json:"branch"`
	MR               *resultMR `json:"mr"`
	MRSkipped        string    `json:"mr_skipped"`
	MRError          string    `json:"mr_error"`
	ReviewVerdict    string    `json:"review_verdict"`
	TestsPassedFinal *bool     `json:"tests_passed_final"`
	ApprovalRequired bool      `json:"approval_required"`
	CostUSD          float64   `json:"cost_usd"`
	CostPartial      bool      `json:"cost_partial"`
	RunDir           string    `json:"run_dir"`
	Report           string    `json:"report"`
}

// resultMR is the merge request a run's push opened.
type resultMR struct {
	IID int    `json:"iid"`
	URL string `json:"url"`
}

// buildResult is the result line of a command that returned err (nil for success). With no run it
// is the error line: status "error" and the message as main prints it.
func buildResult(rec *resultRecorder, err error) runResult {
	code := exitcode.FromError(err)
	res := runResult{V: resultVersion, Status: "error", ExitCode: code, FailureKind: failureKind(err), Workflow: []string{}}
	if err != nil {
		res.Error = err.Error()
	}
	run := rec.run
	if run == nil {
		return res
	}

	res.RunID = run.ID
	res.Status = string(run.Status)
	// An error that is not the run's own ending (a resume refused, a cancelled prompt after the
	// run was loaded) leaves the status of an earlier attempt: it is not what this call did.
	if err != nil && code != exitcode.NeedsPerson && run.Status != domain.RunFailed && run.Status != domain.RunStopped {
		res.Status = "error"
	}
	res.FailureReason = run.FailureReason
	if run.Workflow != nil {
		for _, step := range run.Workflow.Steps {
			res.Workflow = append(res.Workflow, string(step.Action))
		}
	}
	res.Commit = run.Commit
	if run.Worktree != nil {
		res.Branch = run.Worktree.Branch
	}
	if mr := run.MR; mr != nil {
		if mr.HasMergeRequest() {
			res.MR = &resultMR{IID: mr.IID, URL: mr.URL}
		}
		res.MRSkipped, res.MRError = mr.Skipped, mr.Error
	}

	outcome := domain.ComputeOutcome(*run, nil)
	if run.Outcome != nil {
		outcome = *run.Outcome
	}
	res.ReviewVerdict = string(outcome.ReviewVerdict)
	res.TestsPassedFinal = outcome.TestsPassedFinal
	res.ApprovalRequired = outcome.ApprovalRequired
	res.CostUSD = outcome.Usage.CostUSD
	res.CostPartial = outcome.Usage.PartialCalls > 0

	if rec.store != nil {
		res.RunDir = rec.store.RunDir(run.ID)
		if _, statErr := os.Stat(rec.store.ReportPath(run.ID)); statErr == nil {
			res.Report = rec.store.ReportPath(run.ID)
		}
	}
	return res
}

// failureKindAuth is the "failure_kind" of a failure that was a credential: a provider CLI that did
// not use the run's credential or was refused it (apperr.FailureAuth), or a decision key that was
// rejected (apperr.DecisionAuth). It is "" for everything else.
const failureKindAuth = "auth"

// failureKind is the "failure_kind" of the result line of a command that returned err.
func failureKind(err error) string {
	var providerErr *apperr.ProviderError
	if errors.As(err, &providerErr) && providerErr.Kind == apperr.FailureAuth {
		return failureKindAuth
	}
	var decisionErr *apperr.DecisionError
	if errors.As(err, &decisionErr) && decisionErr.Kind == apperr.DecisionAuth {
		return failureKindAuth
	}
	return ""
}

// writeResult writes the result line, once, as the last thing a --output json command does.
func writeResult(stdout io.Writer, rec *resultRecorder, err error) {
	var buf bytes.Buffer
	enc := json.NewEncoder(&buf)
	// The line is read by machines, but a < or & in a message is still better as itself.
	enc.SetEscapeHTML(false)
	if encErr := enc.Encode(buildResult(rec, err)); encErr != nil {
		return
	}
	_, _ = stdout.Write(buf.Bytes())
}

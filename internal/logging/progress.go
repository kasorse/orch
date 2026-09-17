// Package logging renders human-readable CLI progress for a run (MVP.md §27). Plain text
// only - terminal formatting is deliberately not a dependency of the MVP.
package logging

import (
	"bytes"
	"fmt"
	"io"
	"math"
	"strings"
	"time"

	"orchestrator/internal/domain"
	"orchestrator/internal/workflow"
)

// ConsoleReporter implements workflow.Observer and also provides the surrounding
// announcements (config/classification/workflow) that happen before the engine runs. It
// also implements action.CallObserver structurally (ProviderCallStarted/Heartbeat/
// Finished), rendering the "-> running <provider>..." line and, in --verbose, a fuller
// per-call block - action.Dispatcher is wired to a *ConsoleReporter directly.
type ConsoleReporter struct {
	Out io.Writer

	// Verbose switches on the fuller per-call rendering from SPEC-diagnostics.md §6.4
	// (heartbeat lines, a completion line for provider calls). It is the sole
	// verbosity switch (--verbose/-v flag only, no config key or env var).
	Verbose bool

	// MaxFixIterations is only used to render the "N/M" in the fix-iteration notice; it
	// has no effect on control flow (the engine enforces the real limit).
	MaxFixIterations int
}

func NewConsoleReporter(out io.Writer) *ConsoleReporter {
	return &ConsoleReporter{Out: out}
}

// RunHeader prints the run header line once the run ID and directory are known (right
// after persistence.Store.NewRun returns).
func (r *ConsoleReporter) RunHeader(runID, dir string) {
	fmt.Fprintf(r.Out, "Run: %s  (%s)\n", runID, dir)
}

// ProviderCallStarted implements action.CallObserver: it announces a workflow step's
// provider call in real time, before the call happens - this is why Provider:/Model: are
// no longer printed by StepFinished (which only runs after the call returns).
func (r *ConsoleReporter) ProviderCallStarted(providerID, model string) {
	fmt.Fprintf(r.Out, "Provider: %s\nModel: %s\n→ running %s...\n", providerID, model, providerID)
}

// ProviderCallHeartbeat implements action.CallObserver. Printing is suppressed outside
// --verbose (heartbeat lines are a --verbose-only mechanic per spec §6.4).
func (r *ConsoleReporter) ProviderCallHeartbeat(elapsed time.Duration) {
	if !r.Verbose {
		return
	}
	fmt.Fprintf(r.Out, "   … still running (%s)\n", formatDuration(elapsed))
}

// ProviderCallFinished implements action.CallObserver. In the default mode the
// completion line (with duration) is instead printed post-hoc by printSuccess, from the
// step's own StartedAt/FinishedAt - this hook only prints in --verbose, and only on
// success (a failure's detail comes from the returned *apperr.ProviderError's own
// rendering, not from here).
func (r *ConsoleReporter) ProviderCallFinished(providerID, model string, duration time.Duration, err error) {
	if !r.Verbose || err != nil {
		return
	}
	fmt.Fprintf(r.Out, "✓ completed (%s)\n", formatDuration(duration))
}

// formatDuration renders a duration the way SPEC-diagnostics.md's examples do: always in
// seconds (never "1m12.4s" the way time.Duration.String() would render 72.4s), rounded to
// the nearest 100ms, with the decimal dropped when it lands on a whole second (matching
// the heartbeat examples "30s"/"60s" alongside completion examples like "12.8s"/"72.4s").
func formatDuration(d time.Duration) string {
	secs := d.Round(100 * time.Millisecond).Seconds()
	if secs == math.Trunc(secs) {
		return fmt.Sprintf("%.0fs", secs)
	}
	return fmt.Sprintf("%.1fs", secs)
}

// ClassifyReporter adapts a *ConsoleReporter to classifier.CallObserver's shape.
// classifier.CallObserver and action.CallObserver share the exact same method set
// structurally, but the classification call's "started" line reads differently from a
// workflow step's ("-> claude (model sonnet)" vs "Provider: .../-> running claude..."),
// so it cannot be the same method on *ConsoleReporter - this wrapper exists purely to
// give it a second, independent implementation. Construct with
// ClassifyReporter{ConsoleReporter: reporter} and assign to Classifier.Observer.
type ClassifyReporter struct {
	*ConsoleReporter
}

func (c ClassifyReporter) ProviderCallStarted(providerID, model string) {
	fmt.Fprintf(c.Out, "→ %s (model %s)\n", providerID, model)
}

func (c ClassifyReporter) ProviderCallHeartbeat(elapsed time.Duration) {
	if !c.Verbose {
		return
	}
	fmt.Fprintf(c.Out, "   … still running (%s)\n", formatDuration(elapsed))
}

// ProviderCallFinished is a no-op: the default example shows no explicit completion line
// for classification (it's immediately followed by "✓ type:/✓ complexity:"), and a
// verbose rendering isn't specified distinctly from the step case, so it is intentionally
// left unrendered here rather than guessed at.
func (c ClassifyReporter) ProviderCallFinished(providerID, model string, duration time.Duration, err error) {
}

// PrefixWriter tees a provider CLI's live combined output to Out, one line at a time, each
// prefixed with Prefix (spec §6.5: "  | ", --verbose only). It shows whatever the CLI
// prints verbatim - no provider-specific parsing.
type PrefixWriter struct {
	Out    io.Writer
	Prefix string
	buf    []byte
}

func NewPrefixWriter(out io.Writer, prefix string) *PrefixWriter {
	return &PrefixWriter{Out: out, Prefix: prefix}
}

func (p *PrefixWriter) Write(b []byte) (int, error) {
	p.buf = append(p.buf, b...)
	for {
		i := bytes.IndexByte(p.buf, '\n')
		if i < 0 {
			break
		}
		line := p.buf[:i]
		p.buf = p.buf[i+1:]
		if _, err := fmt.Fprintf(p.Out, "%s%s\n", p.Prefix, line); err != nil {
			return 0, err
		}
	}
	return len(b), nil
}

func (r *ConsoleReporter) ValidatingConfig() {
	fmt.Fprintln(r.Out, "Validating configuration...")
}

func (r *ConsoleReporter) ConfigValid() {
	fmt.Fprintln(r.Out, "✓ configuration valid")
}

func (r *ConsoleReporter) ClassifyingTask() {
	fmt.Fprintln(r.Out, "\nClassifying task...")
}

func (r *ConsoleReporter) Classified(c domain.Classification) {
	fmt.Fprintf(r.Out, "✓ type: %s\n✓ complexity: %s\n", c.Type, c.Complexity)
}

func (r *ConsoleReporter) WorkflowBuilt(wf domain.Workflow) {
	names := make([]string, len(wf.Steps))
	for i, s := range wf.Steps {
		names[i] = string(s.Action)
	}
	fmt.Fprintf(r.Out, "\nWorkflow:\n%s\n", strings.Join(names, " → "))
}

// DirtyRepoWarning prints the exact warning text from MVP.md §16 - the orchestrator never
// stashes, resets, checks out, or reverts existing changes, only warns about them.
func DirtyRepoWarning(w io.Writer) {
	fmt.Fprintln(w, "Warning: repository contains uncommitted changes.")
	fmt.Fprintln(w, "Existing changes will not be reverted.")
}

func (r *ConsoleReporter) StepStarted(run *domain.Run, index int, action domain.ActionType) {
	fmt.Fprintf(r.Out, "\n[%d] %s\n", index+1, strings.ToUpper(string(action)))
}

func (r *ConsoleReporter) StepFinished(run *domain.Run, index int, exec domain.StepExecution, outcome workflow.StepOutcome) {
	// Provider:/Model: are announced by ProviderCallStarted, in real time, before the
	// call happens - printing them again here (post-hoc) would duplicate that.
	if exec.TestResult != nil {
		fmt.Fprintf(r.Out, "Command: %s\n", exec.TestResult.Command)
	}

	switch outcome.Outcome {
	case workflow.OutcomeSuccess:
		r.printSuccess(exec)
	case workflow.OutcomeFailure:
		r.printFailure(run, exec)
	}
}

func (r *ConsoleReporter) printSuccess(exec domain.StepExecution) {
	switch exec.Action {
	case domain.ActionTest:
		fmt.Fprintln(r.Out, "✓ passed")
	case domain.ActionReview:
		fmt.Fprintln(r.Out, "✓ PASS")
	default:
		if r.Verbose {
			// ProviderCallFinished already printed the completion line in real time.
			return
		}
		fmt.Fprintf(r.Out, "✓ completed (%s)\n", formatDuration(exec.FinishedAt.Sub(exec.StartedAt)))
	}
}

// printFailure handles test and review failures, both of which always loop back to
// implement (see workflow.Next) - so the fix-iteration notice can be printed
// unconditionally here, using the iteration count the engine is about to reach.
func (r *ConsoleReporter) printFailure(run *domain.Run, exec domain.StepExecution) {
	switch exec.Action {
	case domain.ActionTest:
		fmt.Fprintln(r.Out, "✗ test failed")
		fmt.Fprintln(r.Out)
		r.printFixIterationNotice(run)
	case domain.ActionReview:
		fmt.Fprintln(r.Out, "✗ review failed")
		fmt.Fprintln(r.Out)
		r.printReviewIssueCounts(exec.ReviewResult)
		r.printFixIterationNotice(run)
	default:
		fmt.Fprintln(r.Out, "✗ failed")
	}
}

func (r *ConsoleReporter) printFixIterationNotice(run *domain.Run) {
	fmt.Fprintf(r.Out, "Returning to implementation (iteration %d/%d).\n", run.FixIterations+1, r.MaxFixIterations)
}

func (r *ConsoleReporter) printReviewIssueCounts(review *domain.ReviewResult) {
	if review == nil {
		return
	}
	counts := map[domain.Severity]int{}
	for _, issue := range review.Issues {
		counts[issue.Severity]++
	}
	for _, sev := range []domain.Severity{domain.SeverityCritical, domain.SeverityHigh, domain.SeverityMedium, domain.SeverityLow} {
		if n := counts[sev]; n > 0 {
			plural := ""
			if n != 1 {
				plural = "s"
			}
			fmt.Fprintf(r.Out, "%d %s severity issue%s found.\n", n, sev, plural)
		}
	}
}

func (r *ConsoleReporter) RunFinished(run *domain.Run) {
	switch run.Status {
	case domain.RunCompleted:
		fmt.Fprintln(r.Out, "\nRun completed successfully.")
	case domain.RunFailed:
		fmt.Fprintf(r.Out, "\nRun failed.\nReason: %s\n", run.FailureReason)
	case domain.RunStopped:
		fmt.Fprintf(r.Out, "\nRun stopped.\nReason: %s\n", run.FailureReason)
	}
}

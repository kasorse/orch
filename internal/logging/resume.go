package logging

import (
	"fmt"
	"strings"

	"orchestrator/internal/domain"
)

// The resume overview (specs/resume/SPEC.md §12) is printed before the approval prompt, so
// the operator sees what happened, what is on disk and what runs next before agreeing to
// it: ResumeHeader, History, WorkingTree, NextStep, then the existing Authority and
// RestorePoint lines. --verbose changes nothing here.

// ResumeHeader opens `orchestrator resume`: the run, its directory, where its task came
// from when that was a Jira ticket, and the state the previous attempt left it in. A run found "running" is one whose process died.
func (r *ConsoleReporter) ResumeHeader(run *domain.Run, dir string) {
	status := string(run.Status)
	switch {
	case run.Status == domain.RunRunning:
		status = "interrupted"
	case run.Status == domain.RunPending && run.FailureReason == "":
		status = "pending — no step has run yet"
	}
	if r.Pretty {
		st := r.st()
		title := st.accent("✻") + " " + st.bold("AI Orchestrator") + " " + st.gray("· resume")
		lines := []string{title, "", st.gray("run   ") + run.ID, st.gray("dir   ") + shortenHome(dir)}
		if run.Task.Source != nil {
			lines = append(lines, st.gray("task  ")+taskSourceLine(run.Task.Source))
		}
		last := status
		if run.FailureReason != "" {
			last += " — " + firstLine(run.FailureReason)
		}
		lines = append(lines, st.gray("last  ")+st.yellow(last))
		fmt.Fprint(r.Out, box(st, st.accent, lines, 44))
		return
	}
	fmt.Fprintf(r.Out, "Resuming run %s  (%s)\n", run.ID, dir)
	if run.Task.Source != nil {
		fmt.Fprintln(r.Out, taskSourceLine(run.Task.Source))
	}
	if run.FailureReason != "" {
		fmt.Fprintf(r.Out, "Previous attempt: %s — %s\n", status, firstLine(run.FailureReason))
	} else {
		fmt.Fprintf(r.Out, "Previous attempt: %s\n", status)
	}
}

// History lists every recorded execution, one line each: ✓/✗, index, action, the
// provider/model and authority it ran with, and how it ended. A test or review line
// shows the trusted verdict - the exit code, ReviewResult.Decide - so a test that ran to
// completion but failed is ✗. A failed step shows the first line of its error. Steps run
// by a resume are marked with its number. Nothing is printed for an empty history.
func (r *ConsoleReporter) History(run *domain.Run) {
	if len(run.Executions) == 0 {
		return
	}
	type row struct{ mark, head, who, authority, result string }
	rows := make([]row, len(run.Executions))
	whoWidth, authWidth := 0, 0
	for i, exec := range run.Executions {
		ok, result := executionResult(exec)
		rw := row{mark: "✓", head: fmt.Sprintf("%03d %-11s", exec.Index, exec.Action), result: result}
		if !ok {
			rw.mark = "✗"
		}
		switch {
		case exec.Provider != nil:
			rw.who = exec.Provider.Provider + "/" + exec.Provider.Model
			rw.authority = string(exec.Provider.Authority)
		case exec.Action == domain.ActionTest:
			rw.who = "test command"
		}
		if exec.Attempt > 0 {
			rw.result += fmt.Sprintf("  (resume %d)", exec.Attempt)
		}
		whoWidth, authWidth = max(whoWidth, len(rw.who)), max(authWidth, len(rw.authority))
		rows[i] = rw
	}

	if r.Pretty {
		st := r.st()
		fmt.Fprintln(r.Out)
		r.bullet(st.blue, "History", countOf(len(rows), "step"))
		for i, rw := range rows {
			hook := " "
			if i == 0 {
				hook = st.gray("⎿")
			}
			mark := st.green(rw.mark)
			if rw.mark != "✓" {
				mark = st.red(rw.mark)
			}
			line := fmt.Sprintf("  %s  %s %s  %s  %s  %s", hook, mark, rw.head, st.gray(fmt.Sprintf("%-*s", whoWidth, rw.who)),
				st.gray(fmt.Sprintf("%-*s", authWidth, rw.authority)), rw.result)
			fmt.Fprintln(r.Out, strings.TrimRight(line, " "))
		}
		return
	}
	fmt.Fprintln(r.Out, "History:")
	for _, rw := range rows {
		line := fmt.Sprintf("  %s %s  %-*s  %-*s  %s", rw.mark, rw.head, whoWidth, rw.who, authWidth, rw.authority, rw.result)
		fmt.Fprintln(r.Out, strings.TrimRight(line, " "))
	}
}

// executionResult is how one execution ended, from what the orchestrator itself
// established - never from a model's own claim of success.
func executionResult(exec domain.StepExecution) (ok bool, result string) {
	if exec.Status != domain.StepCompleted {
		if exec.Error == "" {
			return false, "failed"
		}
		return false, firstLine(exec.Error)
	}
	switch {
	case exec.Action == domain.ActionTest && exec.TestResult != nil:
		if exec.TestResult.Success {
			return true, "passed"
		}
		return false, fmt.Sprintf("tests failed (exit %d)", exec.TestResult.ExitCode)
	case exec.Action == domain.ActionReview && exec.ReviewResult != nil:
		if exec.ReviewResult.Decide() == domain.ReviewFail {
			counts := countOf(len(exec.ReviewResult.Issues), "issue")
			if n := len(exec.ReviewResult.Criteria); n > 0 {
				counts += fmt.Sprintf(", %d/%d criteria met", n-len(exec.ReviewResult.UnmetCriteria()), n)
			}
			return false, fmt.Sprintf("FAIL (%s)", counts)
		}
		return true, "PASS"
	}
	return true, "completed"
}

// WorkingTree shows the uncommitted changes a resume continues on: the run's own and any
// the operator made since, which cannot be told apart. It is printed only once the
// protected state matched the run's start, and not at all on a clean tree. err is the
// failure to list them, which is shown rather than stopping the resume.
func (r *ConsoleReporter) WorkingTree(summary string, err error) {
	if r.Pretty {
		st := r.st()
		if err != nil {
			fmt.Fprintln(r.Out, st.yellow("⚠ working tree unavailable: "+firstLine(err.Error())))
			return
		}
		if summary == "" {
			return
		}
		fmt.Fprintln(r.Out)
		r.bullet(st.blue, "Working tree", "")
		for i, line := range strings.Split(summary, "\n") {
			hook := " "
			if i == 0 {
				hook = st.gray("⎿")
			}
			fmt.Fprintf(r.Out, "  %s  %s\n", hook, line)
		}
		return
	}
	if err != nil {
		fmt.Fprintf(r.Out, "Working tree: unavailable (%s)\n", firstLine(err.Error()))
		return
	}
	if summary == "" {
		return
	}
	fmt.Fprintln(r.Out, "Working tree:")
	for _, line := range strings.Split(summary, "\n") {
		fmt.Fprintf(r.Out, "  %s\n", line)
	}
}

// NextStep names where a resume continues, why, and the workflow from there on as it
// would run forward; retries are not predicted.
func (r *ConsoleReporter) NextStep(entry domain.ActionType, why string, remaining []domain.ActionType) {
	if r.Pretty {
		st := r.st()
		names := make([]string, len(remaining))
		for i, a := range remaining {
			names[i] = st.cyan(string(a))
		}
		fmt.Fprintln(r.Out)
		r.bullet(st.accent, "Next: "+string(entry), why)
		r.rows([][2]string{{"workflow", strings.Join(names, st.gray(" → "))}})
		return
	}
	fmt.Fprintf(r.Out, "Next: %s — %s\n", entry, why)
	names := make([]string, len(remaining))
	for i, a := range remaining {
		names[i] = string(a)
	}
	fmt.Fprintf(r.Out, "Remaining workflow: %s\n", strings.Join(names, " → "))
}

// AlreadyCompleted reports a resume of a run that has nothing left to do.
func (r *ConsoleReporter) AlreadyCompleted(runID string) {
	if r.Pretty {
		fmt.Fprintln(r.Out, r.st().green("✓ Run "+runID+" already completed.")+" "+r.st().gray("Nothing to resume."))
		return
	}
	fmt.Fprintf(r.Out, "Run %s already completed. Nothing to resume.\n", runID)
}

func countOf(n int, noun string) string {
	if n == 1 {
		return fmt.Sprintf("%d %s", n, noun)
	}
	return fmt.Sprintf("%d %ss", n, noun)
}

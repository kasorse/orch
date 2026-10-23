// Package logging renders human-readable CLI progress for a run (MVP.md §27). Off a
// terminal it is plain text exactly as the spec describes; on an interactive terminal the
// same information is rendered with colour and a spinner (pretty.go), using nothing but
// ANSI escapes - no terminal library is a dependency.
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

	// Verbose switches on the fuller per-call rendering from MVP.md §27
	// (heartbeat lines, a completion line for provider calls). It is the sole
	// verbosity switch (--verbose/-v flag only, no config key or env var).
	Verbose bool

	// MaxFixIterations is only used to render the "N/M" in the fix-iteration notice; it
	// has no effect on control flow (the engine enforces the real limit).
	MaxFixIterations int

	// Pretty switches on the terminal renderer (colour, spinner, boxes). Set it only when
	// Out is an interactive terminal (see PrettyFor); the plain renderer is the default.
	Pretty bool
	// Version, Cwd and TestCommand only decorate the pretty renderer's header and spinner.
	Version     string
	Cwd         string
	TestCommand string

	// action is the step in flight, for the spinner's label; spin the running spinner.
	action domain.ActionType
	spin   *spinner

	// Pretty-only extras: the repository card, and token/cost/limit totals.
	repo      *RepoInfo
	stepUsage domain.Usage
	runUsage  domain.Usage
	hasUsage  bool
	// stepDiff is what the finishing implement step changed (unified diff).
	stepDiff string

	// dock is the right-hand sidebar on wide terminals (dock.go); rawOut the terminal
	// writer Out wrapped when it opened. run is the run the sidebar describes, inStep
	// whether r.action is executing right now.
	dock   *dock
	rawOut io.Writer
	run    *domain.Run
	inStep bool

	// ReviewForm is set when the review screen will follow the decision: the decision
	// block then leaves the stage table to the form, which shows it editable.
	ReviewForm bool
}

func NewConsoleReporter(out io.Writer) *ConsoleReporter {
	return &ConsoleReporter{Out: out}
}

// NewTerminalReporter is NewConsoleReporter with the pretty renderer switched on when out
// is an interactive terminal (and NO_COLOR/ORCHESTRATOR_PLAIN are unset).
func NewTerminalReporter(out io.Writer) *ConsoleReporter {
	return &ConsoleReporter{Out: out, Pretty: PrettyFor(out)}
}

// Pause stops a running spinner, so a prompt or other output can use the terminal. The
// next provider call starts a new one.
func (r *ConsoleReporter) Pause() {
	r.stopSpin()
}

// Finish ends everything live on the terminal - the spinner and the sidebar - so whatever
// is printed after the run (an error from main) gets the full width. Safe to call twice.
func (r *ConsoleReporter) Finish() {
	r.stopSpin()
	r.CloseDock()
}

// SetRun names the run the sidebar describes, once its workflow is known.
func (r *ConsoleReporter) SetRun(run *domain.Run) {
	r.run = run
	r.refreshDock()
}

// RunHeader prints the run header line once the run ID and directory are known (right
// after persistence.Store.NewRun returns).
func (r *ConsoleReporter) RunHeader(runID, dir string) {
	if r.Pretty {
		r.prettyRunHeader(runID, dir)
		return
	}
	fmt.Fprintf(r.Out, "Run: %s  (%s)\n", runID, dir)
}

// ProviderCallStarted implements action.CallObserver: it announces a workflow step's
// provider call in real time, before the call happens - this is why Provider:/Model: are
// no longer printed by StepFinished (which only runs after the call returns).
func (r *ConsoleReporter) ProviderCallStarted(providerID, model string) {
	if r.Pretty {
		r.prettyProviderCallStarted(providerID, model)
		return
	}
	fmt.Fprintf(r.Out, "Provider: %s\nModel: %s\n→ running %s...\n", providerID, model, providerID)
}

// ProviderCallHeartbeat implements action.CallObserver. Printing is suppressed outside
// --verbose (heartbeat lines are a --verbose-only mechanic per MVP.md §27).
func (r *ConsoleReporter) ProviderCallHeartbeat(elapsed time.Duration) {
	if !r.Verbose || r.Pretty {
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
	if r.Pretty {
		r.stopSpin()
		return
	}
	if !r.Verbose || err != nil {
		return
	}
	fmt.Fprintf(r.Out, "✓ completed (%s)\n", formatDuration(duration))
}

// formatDuration renders a duration the way MVP.md §27's examples do: always in
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

// CallReporter adapts a *ConsoleReporter to the CallObserver shape for provider calls
// made outside workflow steps - translation and the LLM decision rounds. They share
// action.CallObserver's method set, but their "started" line reads differently from a
// workflow step's ("-> claude (model haiku)" vs "Provider: .../-> running claude..."),
// so it cannot be the same method on *ConsoleReporter.
type CallReporter struct {
	*ConsoleReporter
	// Label is what the pretty renderer's spinner says during the call ("Translating").
	Label string
}

func (c CallReporter) ProviderCallStarted(providerID, model string) {
	if c.Pretty {
		label := "Thinking"
		if c.Label != "" {
			label = c.Label
		}
		if c.Verbose {
			c.detail(c.st().gray(label + " · " + providerID + " · " + model))
			return
		}
		c.startSpin(label, providerID+" · "+model)
		return
	}
	fmt.Fprintf(c.Out, "→ %s (model %s)\n", providerID, model)
}

func (c CallReporter) ProviderCallHeartbeat(elapsed time.Duration) {
	if !c.Verbose || c.Pretty {
		return
	}
	fmt.Fprintf(c.Out, "   … still running (%s)\n", formatDuration(elapsed))
}

// ProviderCallFinished is a no-op: the decision summary printed right after these calls
// says what came of them. The pretty renderer keeps its spinner running across the
// decision rounds; Decided (or Pause) stops it.
func (c CallReporter) ProviderCallFinished(providerID, model string, duration time.Duration, err error) {
}

// PrefixWriter tees a provider CLI's live combined output to Out, one line at a time, each
// prefixed with Prefix (MVP.md §27: "  | ", --verbose only). It shows whatever the CLI
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
	if r.Pretty {
		return
	}
	fmt.Fprintln(r.Out, "Validating configuration...")
}

func (r *ConsoleReporter) ConfigValid() {
	if r.Pretty {
		st := r.st()
		fmt.Fprintln(r.Out, st.green("✓")+" "+st.gray("configuration valid"))
		return
	}
	fmt.Fprintln(r.Out, "✓ configuration valid")
}

// ClaudeAuth reports how the claude CLI is authenticated (specs/claude-auth/SPEC.md §4), the
// providers.claude.api_env line in api mode (§17.5; "" for none) and the notices that came
// with the decision. None holds the key.
func (r *ConsoleReporter) ClaudeAuth(line, envLine string, notices []string) {
	if r.Pretty {
		st := r.st()
		fmt.Fprintln(r.Out, st.green("✓")+" "+st.gray(line))
		if envLine != "" {
			fmt.Fprintln(r.Out, st.green("✓")+" "+st.gray(envLine))
		}
		for _, n := range notices {
			fmt.Fprintln(r.Out, st.red("⚠ "+n))
		}
		return
	}
	fmt.Fprintf(r.Out, "✓ %s\n", line)
	if envLine != "" {
		fmt.Fprintf(r.Out, "✓ %s\n", envLine)
	}
	for _, n := range notices {
		fmt.Fprintf(r.Out, "Warning: %s\n", n)
	}
}

// CodexAuth reports the resolved Codex credential mode. It deliberately has no environment
// detail because Codex currently has no provider-specific api_env configuration.
func (r *ConsoleReporter) CodexAuth(line string, notices []string) {
	if r.Pretty {
		st := r.st()
		fmt.Fprintln(r.Out, st.green("✓")+" "+st.gray(line))
		for _, n := range notices {
			fmt.Fprintln(r.Out, st.red("⚠ "+n))
		}
		return
	}
	fmt.Fprintf(r.Out, "✓ %s\n", line)
	for _, n := range notices {
		fmt.Fprintf(r.Out, "Warning: %s\n", n)
	}
}

// OrgConfig reports the org file the configuration was merged with, or "none" for an empty
// path (validate only).
func (r *ConsoleReporter) OrgConfig(path string) {
	text := "org config: " + path
	if path == "" {
		text = "org config: none"
	}
	if r.Pretty {
		st := r.st()
		fmt.Fprintln(r.Out, st.green("✓")+" "+st.gray(text))
		return
	}
	fmt.Fprintf(r.Out, "✓ %s\n", text)
}

// ProviderCLIUnverified warns, in every run's header, that a provider CLI outside its
// verified version range runs because security.allow_unverified_cli is set.
func (r *ConsoleReporter) ProviderCLIUnverified(id, version, verifiedRange string) {
	msg := fmt.Sprintf("%s %s is outside the verified range %s (security.allow_unverified_cli)", id, version, verifiedRange)
	if r.Pretty {
		fmt.Fprintln(r.Out, r.st().red("⚠ "+msg))
		return
	}
	fmt.Fprintf(r.Out, "Warning: %s\n", msg)
}

// ProviderCLIVersion reports a provider CLI's version and range (validate only).
func (r *ConsoleReporter) ProviderCLIVersion(id, version, verifiedRange string, verified bool) {
	text := fmt.Sprintf("%s %s (verified range %s)", id, version, verifiedRange)
	if !verified {
		text = fmt.Sprintf("%s %s (outside the verified range %s, allowed by security.allow_unverified_cli)", id, version, verifiedRange)
	}
	if r.Pretty {
		st := r.st()
		mark := st.green("✓")
		if !verified {
			mark = st.red("⚠")
		}
		fmt.Fprintln(r.Out, mark+" "+st.gray(text))
		return
	}
	mark := "✓"
	if !verified {
		mark = "⚠"
	}
	fmt.Fprintf(r.Out, "%s %s\n", mark, text)
}

// JevModelServed reports that Jev serves the configured decision model (validate only;
// a run stays silent when the check passes).
func (r *ConsoleReporter) JevModelServed(model string) {
	if r.Pretty {
		st := r.st()
		fmt.Fprintln(r.Out, st.green("✓")+" "+st.gray("jev serves "+model))
		return
	}
	fmt.Fprintf(r.Out, "✓ jev serves %s\n", model)
}

// JevModelUnchecked warns that the decision model could not be checked; the run goes on
// because decision.on_failure is ask.
func JevModelUnchecked(w io.Writer, err error) {
	fmt.Fprintf(w, "Warning: could not check decision.model with Jev, continuing (on_failure: ask): %s\n", firstLine(err.Error()))
}

func (r *ConsoleReporter) Deciding() {
	if r.Pretty {
		fmt.Fprintln(r.Out)
		r.startSpin("Deciding the workflow", "")
		return
	}
	fmt.Fprintln(r.Out, "\nDeciding the workflow...")
}

// DirtyRepoWarning prints the exact warning text from MVP.md §16 - the orchestrator never
// stashes, resets, checks out, or reverts existing changes, only warns about them.
// RestorePoint announces the ref recorded before the first step, so the operator can
// always get back to where the run started (specs/permissions/SPEC.md §7.2).
func (r *ConsoleReporter) RestorePoint(ref string) {
	if r.Pretty {
		st := r.st()
		fmt.Fprintf(r.Out, "     %s %s\n", st.gray(fmt.Sprintf("%-12s", "restore")), st.gray(ref))
		return
	}
	fmt.Fprintf(r.Out, "Restore point: %s\n", ref)
}

// SetupStarted announces worktree.setup (specs/worktree/SPEC.md §9), which can run for a
// while and has the network.
func (r *ConsoleReporter) SetupStarted() {
	if r.Pretty {
		st := r.st()
		fmt.Fprintf(r.Out, "     %s %s\n", st.gray(fmt.Sprintf("%-12s", "setup")), st.gray("running worktree.setup (network allowed)"))
		return
	}
	fmt.Fprintln(r.Out, "Setup: running worktree.setup (network allowed)...")
}

// Worktree announces, after approval, the git worktree a run executes in and where it starts
// (specs/worktree/SPEC.md §13): the base branch at the base commit, or only the commit when
// HEAD was detached.
func (r *ConsoleReporter) Worktree(path, branch, baseBranch, baseRef string) {
	base := worktreeBase(baseBranch, baseRef)
	if r.Pretty {
		st := r.st()
		fmt.Fprintf(r.Out, "     %s %s %s\n", st.gray(fmt.Sprintf("%-12s", "worktree")), path, st.gray(fmt.Sprintf("(%s from %s)", branch, base)))
		return
	}
	fmt.Fprintf(r.Out, "Worktree: %s (%s from %s)\n", path, branch, base)
}

// WorktreeCommitted follows the commit on a worktree run's branch with where the branch starts
// and how to take it (specs/worktree/SPEC.md §10.2).
func (r *ConsoleReporter) WorktreeCommitted(branch, baseBranch, baseRef string) {
	base := worktreeBase(baseBranch, baseRef)
	if r.Pretty {
		fmt.Fprintf(r.Out, "%s\n  git merge %s\n", r.st().gray("Based on "+base+". To take it:"), branch)
		return
	}
	fmt.Fprintf(r.Out, "Based on %s. To take it:\n  git merge %s\n", base, branch)
}

// maxConflictFiles is how many conflicting files WorktreeConflicts lists.
const maxConflictFiles = 20

// WorktreeConflicts warns that merging a worktree run's branch into the operator's branch
// will conflict (specs/worktree/SPEC.md §10.3): the count, and the first maxConflictFiles
// files.
func (r *ConsoleReporter) WorktreeConflicts(branch, baseBranch string, files []string) {
	r.stopSpin()
	noun := "files"
	if len(files) == 1 {
		noun = "file"
	}
	head := fmt.Sprintf("merging %s into %s will conflict in %d %s:", branch, baseBranch, len(files), noun)
	if r.Pretty {
		fmt.Fprintln(r.Out, r.st().yellow("⚠ "+head))
	} else {
		fmt.Fprintf(r.Out, "Warning: %s\n", head)
	}
	shown := files
	if len(shown) > maxConflictFiles {
		shown = shown[:maxConflictFiles]
	}
	for _, f := range shown {
		fmt.Fprintf(r.Out, "  %s\n", f)
	}
	if n := len(files) - len(shown); n > 0 {
		fmt.Fprintf(r.Out, "  ... and %d more\n", n)
	}
}

// ConflictCheckSkipped reports that the conflict check against baseBranch did not run, and
// why; the run's branch is there all the same.
func (r *ConsoleReporter) ConflictCheckSkipped(baseBranch, why string) {
	text := fmt.Sprintf("no conflict check against %s: %s", baseBranch, firstLine(why))
	if r.Pretty {
		fmt.Fprintln(r.Out, r.st().yellow("⚠ "+text))
		return
	}
	fmt.Fprintf(r.Out, "Warning: %s\n", text)
}

// MergeTreeUnsupported notes that git is older than 2.38, which has no merge-tree
// --write-tree, so the conflict check against baseBranch was skipped.
func (r *ConsoleReporter) MergeTreeUnsupported(baseBranch string) {
	text := fmt.Sprintf("git older than 2.38 cannot check for conflicts with %s (merge-tree --write-tree); the check was skipped", baseBranch)
	if r.Pretty {
		fmt.Fprintln(r.Out, r.st().gray(text))
		return
	}
	fmt.Fprintf(r.Out, "Note: %s\n", text)
}

// WorktreeKept reports at the end of a worktree run that its worktree stays at path: nothing
// was committed, or git did not remove it (why, "" when nothing went wrong).
func (r *ConsoleReporter) WorktreeKept(path, why string) {
	if why != "" {
		why = " (" + firstLine(why) + ")"
	}
	if r.Pretty {
		st := r.st()
		if why != "" {
			why = st.gray(why)
		}
		fmt.Fprintf(r.Out, "%s %s%s\n", st.gray("The worktree stays at"), path, why)
		return
	}
	fmt.Fprintf(r.Out, "The worktree stays at %s%s\n", path, why)
}

// worktreeBase is where a worktree run starts: the base branch at the base commit, or only the
// commit when HEAD was detached.
func worktreeBase(baseBranch, baseRef string) string {
	base := shortRef(baseRef)
	if baseBranch != "" {
		base = baseBranch + "@" + base
	}
	return base
}

// shortRef is the 7-character abbreviation of a commit SHA.
func shortRef(ref string) string {
	if len(ref) > 7 {
		return ref[:7]
	}
	return ref
}

// RestorePointUnavailable reports that no restore point could be recorded; the run goes on.
func (r *ConsoleReporter) RestorePointUnavailable(err error) {
	if r.Pretty {
		fmt.Fprintln(r.Out, r.st().yellow("⚠ no restore point recorded: "+firstLine(err.Error())))
		return
	}
	fmt.Fprintf(r.Out, "Warning: no restore point recorded: %s\n", firstLine(err.Error()))
}

// RestorePointNotPinned reports that the restore point could not be pinned under its ref;
// the run goes on, but git gc may later prune the restore point.
func (r *ConsoleReporter) RestorePointNotPinned(err error) {
	if r.Pretty {
		fmt.Fprintln(r.Out, r.st().yellow("⚠ restore point not pinned, git gc may prune it: "+firstLine(err.Error())))
		return
	}
	fmt.Fprintf(r.Out, "Warning: restore point not pinned, git gc may prune it: %s\n", firstLine(err.Error()))
}

func DirtyRepoWarning(w io.Writer) {
	if PrettyFor(w) {
		st := style{on: true}
		fmt.Fprintln(w, st.yellow("⚠ Repository contains uncommitted changes.")+" "+st.gray("Existing changes will not be reverted."))
		return
	}
	fmt.Fprintln(w, "Warning: repository contains uncommitted changes.")
	fmt.Fprintln(w, "Existing changes will not be reverted.")
}

func (r *ConsoleReporter) StepStarted(run *domain.Run, index int, action domain.ActionType) {
	r.action = action
	if r.Pretty {
		r.prettyStepStarted(index, action)
		return
	}
	fmt.Fprintf(r.Out, "\n[%d] %s\n", index+1, strings.ToUpper(string(action)))
	// --verbose names the step's authority up front; test makes no AI call, so it has none.
	if r.Verbose && action != domain.ActionTest {
		fmt.Fprintf(r.Out, "Authority: %s\n", domain.AuthorityFor(action))
	}
}

func (r *ConsoleReporter) StepFinished(run *domain.Run, index int, exec domain.StepExecution, outcome workflow.StepOutcome) {
	if r.Pretty {
		r.prettyStepFinished(run, exec, outcome)
		return
	}
	// Provider:/Model: are announced by ProviderCallStarted, in real time, before the
	// call happens - printing them again here (post-hoc) would duplicate that.
	if exec.TestResult != nil {
		fmt.Fprintf(r.Out, "Command: %s\n", exec.TestResult.Command)
	}

	switch outcome.Outcome {
	case workflow.OutcomeSuccess:
		r.printSuccess(exec)
		r.printResult(exec, outcome)
	case workflow.OutcomeFixable:
		// A review that passed, but whose medium issues send the run back to implement once
		// (review.fix_medium): the pass is shown, then why implement runs again.
		r.printSuccess(exec)
		r.printResult(exec, outcome)
		fmt.Fprintln(r.Out)
		r.printReviewIssueCounts(exec.ReviewResult)
		fmt.Fprintln(r.Out, "review.fix_medium is on: fixing the medium issues.")
		r.printFixIterationNotice(run)
	case workflow.OutcomeFailure:
		r.printFailure(run, exec, outcome)
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
func (r *ConsoleReporter) printFailure(run *domain.Run, exec domain.StepExecution, outcome workflow.StepOutcome) {
	switch exec.Action {
	case domain.ActionTest:
		fmt.Fprintln(r.Out, "✗ test failed")
		r.printResult(exec, outcome)
		fmt.Fprintln(r.Out)
		r.printFixIterationNotice(run)
	case domain.ActionReview:
		fmt.Fprintln(r.Out, "✗ review failed")
		r.printResult(exec, outcome)
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
	if r.Pretty {
		r.prettyRunFinished(run)
		return
	}
	switch run.Status {
	case domain.RunCompleted:
		fmt.Fprintln(r.Out, "\nRun completed successfully.")
	case domain.RunFailed:
		fmt.Fprintf(r.Out, "\nRun failed.\nReason: %s\n", run.FailureReason)
	case domain.RunStopped:
		fmt.Fprintf(r.Out, "\nRun stopped.\nReason: %s\n", run.FailureReason)
	}
	// The ID is repeated at the end, so it need not be hunted for in the header above.
	if run.ID != "" {
		fmt.Fprintf(r.Out, "Run ID: %s\n", run.ID)
	}
}

// PreRunTestStarted announces the test command's run before the first step
// (specs/hardening/SPEC.md §8).
func (r *ConsoleReporter) PreRunTestStarted() {
	if r.Pretty {
		r.startSpin("Running the tests before any change", "")
		return
	}
	fmt.Fprintln(r.Out, "\nPre-run test: running the tests before any change...")
}

// PreRunTestFinished reports the pre-run test: passed, failed and stopping the run, or
// failed and going on because the run was started with --allow-red-tests. A nil result
// (the command never ran) only ends the progress display.
func (r *ConsoleReporter) PreRunTestFinished(result *domain.TestResult, allowRed bool) {
	r.stopSpin()
	if result == nil {
		return
	}
	took := result.Duration.Round(100 * time.Millisecond)
	// A red check names itself: the tests did not run (specs/quality/SPEC.md §5.5).
	failed := fmt.Sprintf("exit %d, %s", result.ExitCode, took)
	if result.FailedCheck != "" {
		failed = fmt.Sprintf("check %s, %s", result.FailedCheck, failed)
	}
	var text string
	switch {
	case result.Success:
		text = fmt.Sprintf("passed (%s)", took)
	case allowRed:
		text = fmt.Sprintf("failed (%s) - going on: --allow-red-tests, the task is to make them pass", failed)
	default:
		text = fmt.Sprintf("failed (%s) - the run stops before any change", failed)
	}
	if r.Pretty {
		st := r.st()
		mark := st.green("✓")
		if !result.Success {
			mark = st.yellow("⚠")
		}
		fmt.Fprintf(r.Out, "     %s %s %s\n", st.gray(fmt.Sprintf("%-12s", "pre-run test")), mark, text)
		return
	}
	fmt.Fprintf(r.Out, "Pre-run test: %s\n", text)
}

// SecretScanOff says, before approval, that security.secret_scan.mode is off.
func (r *ConsoleReporter) SecretScanOff() {
	if r.Pretty {
		st := r.st()
		fmt.Fprintf(r.Out, "     %s %s\n", st.gray(fmt.Sprintf("%-12s", "secret scan")), st.yellow("off: what steps add is not checked for secrets"))
		return
	}
	fmt.Fprintln(r.Out, "Secret scan: off - what steps add is not checked for secrets")
}

// Warning reports a problem a step went on despite.
func (r *ConsoleReporter) Warning(step domain.ActionType, msg string) {
	r.stopSpin()
	text := fmt.Sprintf("%s: %s", step, msg)
	if r.Pretty {
		fmt.Fprintln(r.Out, r.st().yellow("⚠ "+text))
		return
	}
	fmt.Fprintf(r.Out, "Warning: %s\n", text)
}

// SecretFound reports a likely secret a step added, under security.secret_scan.mode:
// warn: where and which rule, never the value.
func (r *ConsoleReporter) SecretFound(step domain.ActionType, file string, line int, rule string) {
	r.stopSpin()
	text := fmt.Sprintf("%s added what looks like a secret: %s:%d (%s); recorded, not sent to review", step, file, line, rule)
	if r.Pretty {
		fmt.Fprintln(r.Out, r.st().yellow("⚠ "+text))
		return
	}
	fmt.Fprintf(r.Out, "Warning: %s\n", text)
}

package app

import (
	"context"
	"fmt"
	"strings"
	"unicode/utf8"

	"orchestrator/internal/action"
	"orchestrator/internal/domain"
	"orchestrator/internal/interaction"
)

// commitHeaderMax is the longest first line of a proposed commit message, in runes.
const commitHeaderMax = 72

// offerCommit asks, at the end of a completed run that implemented something, whether to
// commit what it changed on the current branch (MVP.md §42). Nothing here can fail a run
// that already completed: a refusal or a git error is reported and the changes stay in the
// working tree. Go writes the message from the run - no provider is called. With
// auto_commit the same checks run and the commit is made without the question, unless
// there is something the question would have warned about or no message to use.
func offerCommit(ctx context.Context, s *session) {
	run := s.run
	if run.Status != domain.RunCompleted || run.Workflow == nil || !run.Workflow.Contains(domain.ActionImplement) {
		return
	}
	out := s.reporter.PausingWriter()
	auto := s.cfg.AutoCommit
	skip := func(why string) {
		if auto {
			fmt.Fprintf(out, "\nNot committing automatically: %s\n", why)
		} else {
			fmt.Fprintf(out, "\nNot offering a commit: %s\n", why)
		}
	}

	// A run on top of uncommitted changes would commit them together with its own.
	if run.PreRunRef == "" || run.PreRunRef != run.HeadRef {
		skip("the run did not start from a clean commit; commit what you want yourself.")
		return
	}
	// The git fingerprint first, before any git status; then the protected paths. git
	// commit runs the repository's hooks, so they must be the ones the run started with.
	changed, err := action.CheckBaseline(ctx, s.repo, run.ResumeBaseline)
	if err != nil {
		skip("cannot compare the repository with the run-start state: " + err.Error())
		return
	}
	if len(changed) > 0 {
		skip("protected state changed since the run started: " + strings.Join(changed, ", "))
		return
	}
	changes, err := s.repo.StatusSummary(ctx)
	if err != nil {
		skip(err.Error())
		return
	}
	if changes == "" {
		fmt.Fprintln(out, "\nNo changes to commit.")
		return
	}
	branch, err := s.repo.Branch(ctx)
	if err != nil {
		skip(err.Error())
		return
	}

	offer := commitOffer(run, s.dispatcher.CommitImplementation())
	offer.Branch, offer.Changes = branch, changes
	header := offer.Header
	if auto {
		switch {
		case len(offer.Warnings) > 0:
			skip(strings.Join(offer.Warnings, "; ") + "; commit what you want yourself.")
			return
		case header == "":
			skip("the implementation proposed no commit message; commit what you want yourself.")
			return
		}
	} else {
		var ok bool
		header, ok, err = interaction.AskCommit(s.prompter, out, offer)
		if err != nil {
			fmt.Fprintf(out, "\nNo answer (%v); nothing committed.\n", err)
			return
		}
		if !ok {
			fmt.Fprintln(out, "Not committed; the changes stay in the working tree.")
			return
		}
	}
	sha, err := s.repo.CommitAll(ctx, offer.Message(header))
	if err != nil {
		fmt.Fprintf(out, "Commit failed: %v\n", err)
		return
	}
	run.Commit = sha
	if err := s.store.SaveRun(run); err != nil {
		fmt.Fprintf(out, "Committed %s, but run.json could not record it: %v\n", shortSHA(sha), err)
		return
	}
	fmt.Fprintf(out, "\nCommitted %s to %s: %s\n", shortSHA(sha), branch, header)
}

// commitOffer proposes the message and the warnings for run. The header is the last
// implementation's summary_short, cut to one line of commitHeaderMax runes and prefixed
// with the Jira key for a run --jira task; whatever the cut left out goes into the body,
// followed by the Orchestrator-Run trailer. Without a summary_short the header is "" and
// the operator is asked to write it.
func commitOffer(run *domain.Run, impl *domain.ImplementationArtifact) interaction.CommitOffer {
	var body strings.Builder
	var header string
	if impl != nil {
		if source := strings.TrimSpace(impl.SummaryShort); source != "" {
			prefix := ""
			if run.Task.Source != nil && run.Task.Source.Key != "" {
				prefix = run.Task.Source.Key + ": "
			}
			first, _, _ := strings.Cut(source, "\n")
			header = truncateRunes(prefix+strings.TrimSpace(first), commitHeaderMax)
			if prefix+source != header {
				body.WriteString(source + "\n\n")
			}
		}
	}
	body.WriteString("Orchestrator-Run: " + run.ID + "\n")

	var warnings []string
	if r := lastReview(run); r != nil && r.Decide() == domain.ReviewFail {
		warnings = append(warnings, "the review did not pass: "+firstNonEmpty(r.SummaryShort, r.Summary))
	}
	for _, f := range run.SecretFindings {
		warnings = append(warnings, fmt.Sprintf("a likely secret was added at %s:%d (%s)", f.File, f.Line, f.Rule))
	}
	return interaction.CommitOffer{Header: header, Body: body.String(), Warnings: warnings}
}

// lastReview is the result of the run's last completed review, or nil.
func lastReview(run *domain.Run) *domain.ReviewResult {
	for i := len(run.Executions) - 1; i >= 0; i-- {
		if e := run.Executions[i]; e.ReviewResult != nil {
			return e.ReviewResult
		}
	}
	return nil
}

// firstNonEmpty is the first of values that is not blank, trimmed, or "".
func firstNonEmpty(values ...string) string {
	for _, v := range values {
		if v = strings.TrimSpace(v); v != "" {
			return v
		}
	}
	return ""
}

// truncateRunes cuts s to at most n runes, ending a cut string with "...".
func truncateRunes(s string, n int) string {
	if utf8.RuneCountInString(s) <= n {
		return s
	}
	return strings.TrimSpace(string([]rune(s)[:n-3])) + "..."
}

func shortSHA(sha string) string {
	if len(sha) > 12 {
		return sha[:12]
	}
	return sha
}

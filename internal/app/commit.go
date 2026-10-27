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
//
// A worktree run commits on its own branch, never the operator's, and without the question only
// under --mode auto, by the same checks (specs/delivery/SPEC.md §7.2): auto_commit has no
// effect there. Any other mode asks, with the warnings, and needs somebody to ask (canAsk); a
// no is a person's decision. In place canAsk is not read: the caller asked only when someone
// is there. It returns the commit, or "" when nothing was committed, and s.noCommit says why.
func offerCommit(ctx context.Context, s *session, canAsk bool) string {
	run := s.run
	s.noCommit = commitStop{}
	if run.Status != domain.RunCompleted || run.Workflow == nil || !run.Workflow.Contains(domain.ActionImplement) {
		return ""
	}
	out := s.reporter.PausingWriter()
	inWorktree := s.worktree != nil
	// A manual run is confirmed by a person step by step: it ends with the question too.
	auto := (inWorktree && run.Mode == domain.ModeAuto) || (!inWorktree && s.cfg.AutoCommit && run.Mode != domain.ModeManual)
	skip := func(why string) {
		s.noCommit = commitStop{why: why}
		if auto {
			fmt.Fprintf(out, "\nNot committing automatically: %s\n", why)
		} else {
			fmt.Fprintf(out, "\nNot offering a commit: %s\n", why)
		}
	}

	// A run on top of uncommitted changes would commit them together with its own.
	if run.PreRunRef == "" || run.PreRunRef != run.HeadRef {
		skip("the run did not start from a clean commit; commit what you want yourself.")
		return ""
	}
	// The git fingerprint first, before any git status; then the protected paths. git
	// commit runs the repository's hooks, so they must be the ones the run started with. In
	// a worktree the baseline holds its .git pointer, which is compared before either.
	changed, err := action.CheckBaseline(ctx, s.repo, run.ResumeBaseline)
	if err != nil {
		skip("cannot compare the repository with the run-start state: " + err.Error())
		return ""
	}
	if len(changed) > 0 {
		skip("protected state changed since the run started: " + strings.Join(changed, ", "))
		return ""
	}
	changes, err := s.repo.StatusSummary(ctx)
	if err != nil {
		skip(err.Error())
		return ""
	}
	if changes == "" {
		s.noCommit = commitStop{why: "the run changed nothing, so there is nothing to commit"}
		fmt.Fprintln(out, "\nNo changes to commit.")
		return ""
	}
	branch, err := s.repo.Branch(ctx)
	if err != nil {
		skip(err.Error())
		return ""
	}
	if inWorktree && branch != s.worktree.Branch {
		skip(fmt.Sprintf("the worktree is on %q, not on the run's branch %s; commit what you want yourself.", branch, s.worktree.Branch))
		return ""
	}

	offer := commitOffer(run, s.dispatcher.CommitImplementation())
	offer.Branch, offer.Changes = branch, changes
	header := offer.Header
	ask := !auto
	if auto {
		var why string
		switch {
		// approval: manual is a person's yes at the question; auto_commit's yes was given in
		// advance, before anyone knew which files would change (specs/governance/SPEC.md §4.5).
		case len(offer.PathRules) > 0:
			why = approvalReason(offer.PathRules)
		case len(offer.Warnings) > 0:
			why = strings.Join(offer.Warnings, "; ") + "; commit what you want yourself."
		case header == "":
			why = "the implementation proposed no commit message; commit what you want yourself."
		}
		if why != "" {
			skip(why)
			return ""
		}
	}
	if ask {
		if inWorktree && !canAsk {
			skip("there is no terminal to ask on; run with --mode auto to commit and deliver without questions.")
			return ""
		}
		var ok bool
		header, ok, err = interaction.AskCommit(s.prompter, out, offer)
		if err != nil {
			// A closed stdin is the default answer: no.
			s.noCommit = commitStop{why: "no answer to the commit question", declined: true}
			fmt.Fprintf(out, "\nNo answer (%v); nothing committed.\n", err)
			return ""
		}
		if !ok {
			s.noCommit = commitStop{why: "declined at commit", declined: true}
			if inWorktree {
				fmt.Fprintln(out, "Not committed; the changes stay in the worktree.")
			} else {
				fmt.Fprintln(out, "Not committed; the changes stay in the working tree.")
			}
			return ""
		}
	}
	sha, err := s.repo.CommitAll(ctx, offer.Message(header))
	if err != nil {
		s.noCommit = commitStop{why: "the commit failed: " + firstNonEmpty(strings.SplitN(err.Error(), "\n", 2)[0])}
		fmt.Fprintf(out, "Commit failed: %v\n", err)
		return ""
	}
	run.Commit = sha
	if err := s.store.SaveRun(run); err != nil {
		fmt.Fprintf(out, "Committed %s, but run.json could not record it: %v\n", shortSHA(sha), err)
		return sha
	}
	fmt.Fprintf(out, "\nCommitted %s to %s: %s\n", shortSHA(sha), branch, header)
	return sha
}

// commitOffer proposes the message and the warnings for run. The header is the last
// implementation's summary_short, cut to one line of commitHeaderMax runes and prefixed
// with its commit_type ("fix: ") and the Jira key for a run --jira task or a story task that
// has one ("fix: PAY-12 summary", "PAY-12: summary" without a type); whatever the cut left
// out goes into the body, followed by the Orchestrator-Run trailer, the Orchestrator-Decision
// one (when the run has a decision) and, for a story task, the Orchestrator-Story one. Without a summary_short the header is "" and
// the operator is asked to write it.
func commitOffer(run *domain.Run, impl *domain.ImplementationArtifact) interaction.CommitOffer {
	var body strings.Builder
	var header string
	if impl != nil {
		if source := strings.TrimSpace(impl.SummaryShort); source != "" {
			key := ticketKey(run.Task.Source)
			prefix := ""
			switch commitType, ok := domain.ValidCommitType(impl.CommitType); {
			case ok && key != "":
				prefix = commitType + ": " + key + " "
			case ok:
				prefix = commitType + ": "
			case key != "":
				prefix = key + ": "
			}
			first, _, _ := strings.Cut(source, "\n")
			header = truncateRunes(prefix+strings.TrimSpace(first), commitHeaderMax)
			if prefix+source != header {
				body.WriteString(source + "\n\n")
			}
		}
	}
	body.WriteString("Orchestrator-Run: " + run.ID + "\n")
	if d := run.Decision; d != nil {
		label := string(d.Complexity.Selected)
		if d.Source == domain.DecisionSourceManual || label == "" {
			label = "manual"
		}
		stages := d.Stages.EnabledStages()
		names := make([]string, len(stages))
		for i, st := range stages {
			names[i] = string(st)
		}
		body.WriteString("Orchestrator-Decision: " + label + "/" + strings.Join(names, ",") + "\n")
	}
	if src := run.Task.Source; src != nil && src.Kind == domain.TaskSourceStory {
		body.WriteString("Orchestrator-Story: " + src.StoryRef() + "\n")
	}

	var warnings []string
	if r := lastReview(run); r != nil && r.Decide() == domain.ReviewFail {
		warnings = append(warnings, "the review did not pass: "+firstNonEmpty(r.SummaryShort, r.Summary))
	}
	for _, f := range run.SecretFindings {
		warnings = append(warnings, fmt.Sprintf("a likely secret was added at %s:%d (%s)", f.File, f.Line, f.Rule))
	}
	return interaction.CommitOffer{Header: header, Body: body.String(), Warnings: warnings,
		PathRules: domain.ManualApprovalMatches(domain.LastImplementPathRules(*run))}
}

// ticketKey is the Jira key a commit header starts with: the issue a run --jira task was
// rendered from, or the key of a story task; "" when there is none.
func ticketKey(src *domain.TaskSource) string { return src.JiraKey() }

// approvalReason is why auto_commit does not commit a change that matched approval: manual
// rules.
func approvalReason(matches []domain.PathRuleMatch) string {
	parts := make([]string, len(matches))
	for i, m := range matches {
		n := "files"
		if len(m.Files) == 1 {
			n = "file"
		}
		parts[i] = fmt.Sprintf("%s (%d %s)", m.Match, len(m.Files), n)
	}
	return "policy.paths requires manual approval: " + strings.Join(parts, ", ") + "; commit what you want yourself."
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

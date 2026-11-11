package app

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"strings"

	"github.com/kasorse/orch/internal/config"
	"github.com/kasorse/orch/internal/domain"
	"github.com/kasorse/orch/internal/jira"
	"github.com/kasorse/orch/internal/story"
)

// maxJiraWarningBytes bounds one warning about the write-back.
const maxJiraWarningBytes = 400

// jiraWriteOptions is jira.write as a new run records it in options.jira_write: what the run
// writes to its task's issue once its push opened a merge request (specs/delivery/SPEC.md §10).
func jiraWriteOptions(cfg config.Config) domain.JiraWriteOptions {
	if !cfg.JiraWrites() {
		return domain.JiraWriteOptions{}
	}
	return domain.JiraWriteOptions{Comment: cfg.Jira.Write.Comment, Transition: cfg.Jira.Write.Transition}
}

// jiraWriteDependencies reports whether a run of a story task has to find the Jira credentials
// before it starts: jira.write is set, the task has a child issue (its key), and a story task is
// always a worktree run. A run --jira needs them anyway.
func jiraWriteDependencies(cfg config.Config, storyIn *story.TaskInput) bool {
	return storyIn != nil && storyIn.Jira != "" && cfg.JiraWrites()
}

// newJiraWriteClient builds the client for the write-back, as taskFromJira does for a fetch. This,
// taskFromJira and storyRunner.newJiraClient are the places the credential values are read from
// the environment. It refuses what a resume can find missing: a configuration without the jira
// section, or without the variables the credentials come from.
func newJiraWriteClient(cfg *config.Config) (*jira.Client, error) {
	j := cfg.Jira
	if j == nil {
		return nil, errors.New("the configuration has no jira section")
	}
	if problems := j.CredentialProblems(); len(problems) > 0 {
		return nil, errors.New(strings.Join(problems, "; "))
	}
	creds := jira.NewCredentials(j.Auth, os.Getenv(j.EmailEnv), os.Getenv(j.TokenEnv))
	return jira.NewHTTPClient(jira.Options{
		BaseURL: j.BaseURL, Auth: j.Auth, EmailEnv: j.EmailEnv, TokenEnv: j.TokenEnv,
		Timeout: j.Timeout, MaxRetries: j.MaxRetries,
		UserAgent: "orchestrator/" + Version,
	}, creds), nil
}

// writeJira comments on the task's Jira issue and moves it after this run's push opened a merge
// request (specs/delivery/SPEC.md §10): the comment first, then the transition, each recorded in
// run.json: jira_write. What it writes comes from options.jira_write, never the current config,
// and it reads the issue only to find the transition to make. A task without a key, and a run
// whose push did not create a merge request, write nothing. Nothing here changes the run's
// status or exit code: a failure is a warning, the merge request being the result.
func writeJira(ctx context.Context, s *session, sha string) {
	opts, run := s.run.Options.JiraWrite, s.run
	key := run.Task.Source.JiraKey()
	if (!opts.Comment && opts.Transition == "") || key == "" || !domain.ValidTicketKey(key) ||
		!run.MR.HasMergeRequest() {
		return
	}
	out := s.reporter.PausingWriter()
	warn := func(format string, args ...any) {
		fmt.Fprintf(out, "Warning: %s\n", oneLineText(fmt.Sprintf(format, args...), maxJiraWarningBytes))
	}
	client, err := newJiraWriteClient(s.cfg)
	if err != nil {
		warn("nothing was written to %s: %v", key, err)
		return
	}

	run.JiraWrite = &domain.RunJiraWrite{}
	if opts.Comment {
		commentOnIssue(ctx, s, client, key, sha, out, warn)
	}
	if opts.Transition != "" {
		run.JiraWrite.Transition = &domain.RunJiraTransition{Name: opts.Transition,
			State: moveIssue(ctx, client, key, opts.Transition, out, warn)}
		saveJiraWrite(s, out)
	}
	// A record with neither part is not a record: run.json refuses it on load.
	if run.JiraWrite.Comment == nil && run.JiraWrite.Transition == nil {
		run.JiraWrite = nil
	}
}

// commentOnIssue posts the merge request as a comment. The intent is saved before the request and
// the id after the answer: a failure that may have reached Jira keeps the intent, and the comment
// is never posted again.
func commentOnIssue(ctx context.Context, s *session, client *jira.Client, key, sha string, out io.Writer, warn func(string, ...any)) {
	rec := s.run.JiraWrite
	rec.Comment = &domain.RunJiraComment{State: domain.JiraCommentIntent}
	if !saveJiraWrite(s, out) {
		rec.Comment = nil
		warn("nothing was written to %s: run.json could not record the intent", key)
		return
	}
	id, err := client.Comment(ctx, key, mergeRequestComment(ctx, s, sha).Wiki())
	switch {
	case err == nil:
		rec.Comment = &domain.RunJiraComment{State: domain.JiraCommentDone, ID: id}
		fmt.Fprintf(out, "Commented on %s in Jira.\n", key)
	case jira.MayExist(err):
		warn("the comment on %s may have been posted and is not repeated: %s", key, firstLineOf(err.Error()))
	default:
		rec.Comment = &domain.RunJiraComment{State: domain.JiraCommentFailed}
		warn("cannot comment on %s: %s", key, firstLineOf(err.Error()))
	}
	saveJiraWrite(s, out)
}

// mergeRequestComment is what the comment says: the merge request, its title (the commit header)
// and what the run found.
func mergeRequestComment(ctx context.Context, s *session, sha string) jira.MergeRequestComment {
	run := s.run
	title, err := s.operator.CommitSubject(ctx, sha)
	if err != nil || strings.TrimSpace(title) == "" {
		title = s.worktree.Branch
	}
	outcome := domain.ComputeOutcome(*run, loadTrace(s.store, run.ID))
	c := jira.MergeRequestComment{IID: run.MR.IID, URL: run.MR.URL, Title: title, Review: string(outcome.ReviewVerdict), RunID: run.ID,
		Updated: run.MR.State == domain.MRUpdated}
	if p := outcome.TestsPassedFinal; p != nil {
		c.Tests = map[bool]string{true: "passed", false: "failed"}[*p]
	}
	if outcome.Usage.Calls > 0 {
		c.Cost = fmt.Sprintf("$%.2f", outcome.Usage.CostUSD)
		if outcome.Usage.PartialCalls > 0 {
			c.Cost += " or more"
		}
	}
	return c
}

// moveIssue moves key by the transition whose name, or whose target status's name, is name (case
// does not matter). An issue already in a status called name is left alone, and so is one with no
// such transition: its warning lists the names it has. It returns the state to record.
func moveIssue(ctx context.Context, client *jira.Client, key, name string, out io.Writer, warn func(string, ...any)) string {
	status, err := client.Status(ctx, key)
	if err != nil {
		warn("cannot read the status of %s, so it is not moved: %s", key, firstLineOf(err.Error()))
		return domain.JiraTransitionFailed
	}
	if strings.EqualFold(status, name) {
		fmt.Fprintf(out, "%s is already in %s in Jira; not moved.\n", key, status)
		return domain.JiraTransitionUnchanged
	}
	transitions, err := client.Transitions(ctx, key)
	if err != nil {
		warn("cannot read the transitions of %s, so it is not moved: %s", key, firstLineOf(err.Error()))
		return domain.JiraTransitionFailed
	}
	chosen, ok := findTransition(transitions, name)
	if !ok {
		names := make([]string, len(transitions))
		for i, t := range transitions {
			names[i] = t.Name
		}
		if len(names) == 0 {
			names = []string{"none"}
		}
		warn("%s has no transition %q (available: %s)", key, name, strings.Join(names, ", "))
		return domain.JiraTransitionMissing
	}
	if err := client.Transition(ctx, key, chosen.ID); err != nil {
		if jira.MayExist(err) {
			warn("%s may have been moved by %q and is not moved again: %s", key, chosen.Name, firstLineOf(err.Error()))
		} else {
			warn("cannot move %s by %q: %s", key, chosen.Name, firstLineOf(err.Error()))
		}
		return domain.JiraTransitionFailed
	}
	fmt.Fprintf(out, "Moved %s in Jira by %q.\n", key, chosen.Name)
	return domain.JiraTransitionDone
}

// findTransition is the first transition named name, else the first that leads to a status named
// name, ignoring case.
func findTransition(list []jira.Transition, name string) (jira.Transition, bool) {
	for _, t := range list {
		if strings.EqualFold(t.Name, name) {
			return t, true
		}
	}
	for _, t := range list {
		if t.To != "" && strings.EqualFold(t.To, name) {
			return t, true
		}
	}
	return jira.Transition{}, false
}

// saveJiraWrite saves run.json after a step of the write-back. The run completed: a failure to
// save is shown, not returned.
func saveJiraWrite(s *session, out io.Writer) bool {
	if err := s.store.SaveRun(s.run); err != nil {
		fmt.Fprintf(out, "run.json could not record the Jira write-back: %v\n", err)
		return false
	}
	return true
}

// firstLineOf is the first line of s: a Jira error carries the start of the response body after
// its headline, which a warning leaves out.
func firstLineOf(s string) string {
	line, _, _ := strings.Cut(s, "\n")
	return line
}

// writeReviseDescription appends the requirement to the revise task's Jira issue after its push
// (specs/revise/SPEC.md §8, R2, R9): one GET of the description, then one PUT of it with
// "Update <date>: <requirement>" at the end. The intent is saved before the PUT and the state
// after; a write that may have reached Jira is never repeated, and a resume that finds the record
// writes nothing again. A failure is a warning: the merge request is the result.
//
// shortcut: Jira has no conditional update, so an edit made between the GET and the PUT is lost;
// the window is one round trip (A8). Upgrade if Jira ever offers an ETag on the issue.
func writeReviseDescription(ctx context.Context, s *session) {
	run := s.run
	src := run.Task.Source
	if src == nil || src.Kind != domain.TaskSourceRevise || src.Key == "" || !run.MR.HasMergeRequest() {
		return
	}
	if run.JiraWrite != nil && run.JiraWrite.Description != nil {
		return
	}
	key, out := src.Key, s.reporter.PausingWriter()
	warn := func(format string, args ...any) {
		fmt.Fprintf(out, "Warning: %s\n", oneLineText(fmt.Sprintf(format, args...), maxJiraWarningBytes))
	}
	client, err := newJiraWriteClient(s.cfg)
	if err != nil {
		warn("the requirement was not added to %s: %v", key, err)
		return
	}
	old, err := client.Description(ctx, key)
	if err != nil {
		warn("cannot read the description of %s, so the requirement was not added: %s", key, firstLineOf(err.Error()))
		return
	}
	requirement := reviseRequirement(run.Task.Description)
	text := jira.AppendUpdate(old, run.Task.CreatedAt.UTC().Format("2006-01-02"), requirement)

	if run.JiraWrite == nil {
		run.JiraWrite = &domain.RunJiraWrite{}
	}
	run.JiraWrite.Description = &domain.RunJiraComment{State: domain.JiraCommentIntent}
	if !saveJiraWrite(s, out) {
		run.JiraWrite.Description = nil
		if run.JiraWrite.Comment == nil && run.JiraWrite.Transition == nil {
			run.JiraWrite = nil
		}
		warn("the requirement was not added to %s: run.json could not record the intent", key)
		return
	}
	switch err := client.SetDescription(ctx, key, text); {
	case err == nil:
		run.JiraWrite.Description.State = domain.JiraCommentDone
		fmt.Fprintf(out, "Added the requirement to the description of %s in Jira.\n", key)
	case jira.MayExist(err):
		warn("the description of %s may have been updated and is not written again: %s", key, firstLineOf(err.Error()))
	default:
		run.JiraWrite.Description.State = domain.JiraCommentFailed
		warn("cannot update the description of %s: %s", key, firstLineOf(err.Error()))
	}
	saveJiraWrite(s, out)
}

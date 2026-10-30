package app

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"

	"orchestrator/internal/action"
	"orchestrator/internal/apperr"
	"orchestrator/internal/domain"
	"orchestrator/internal/logging"
	"orchestrator/internal/repository"
)

// The branch name of a worktree run (specs/delivery/SPEC.md §4.3): proposed by a tool-less call
// that reads the repository's instructions, else rendered from worktree.branch.template, else the
// fallback. The name is fixed when the worktree is created and never changes; Go checks every
// name, whichever source it came from.

// Bounds of what branch_naming records.
const (
	maxProposedBytes = 255
	maxRejectedBytes = 500
)

// branchToday is the clock {date} and the call's date read; a test replaces it.
var branchToday = time.Now

// branchInput is what nameBranch names a branch from. A story task (specs/delivery/SPEC.md §9.1)
// is named with its own Key, Task and Fallback; a run's are its Jira key, its task's first line and
// orchestrator/<run-id>.
type branchInput struct {
	// Key is the Jira key of {key} and of the call, "" when there is none.
	Key string
	// Task is the task's first line, what {slug} is made of and the call is given as the task.
	Task string
	// Fallback is the name when no rule gives one. It is not checked here: the story pattern of a
	// story task is not a rule name, and its existence is the caller's check.
	Fallback string
	// BaseRef is the commit the instruction files are read from, and BaseBranch the branch the
	// worktree starts from, which no rule may name.
	BaseRef    string
	BaseBranch string
	// Reserved are further branches no rule may name, such as delivery.target_branch.
	Reserved []string
}

// nameBranch chooses the branch of the worktree s is about to create, in the operator's
// repository, before git worktree add. The first source that gives a name that passes the checks
// wins: the instructions (only when worktree.branch.from_instructions is set, the files at
// BaseRef mention branches, and the call gives a name), then worktree.branch.template, then
// in.Fallback. What is dropped on the way is a warning, and the record says why. The record also
// holds the call's usage. The errors are a credential problem (apperr.FailureAuth), the run's own
// end, and git failing; a failed call or a refused name is never one.
func nameBranch(ctx context.Context, s *session, in branchInput) (string, *domain.BranchNaming, error) {
	naming := &domain.BranchNaming{}
	var rejected []string
	reject := func(source, why string) {
		why = oneLineCut(why, maxRejectedBytes)
		fmt.Fprintf(s.reporter.PausingWriter(), "Warning: branch name from %s not used: %s\n", source, why)
		rejected = append(rejected, why)
	}
	done := func(name, source string) (string, *domain.BranchNaming, error) {
		naming.Source = source
		naming.Rejected = oneLineCut(strings.Join(rejected, "; "), maxRejectedBytes)
		return name, naming, nil
	}
	cfg := s.cfg.Worktree.Branch
	repo := s.repo

	if cfg.FromInstructions != nil {
		files, err := readInstructionFiles(ctx, repo, in.BaseRef)
		if err != nil {
			if ctx.Err() != nil {
				return "", nil, ctx.Err()
			}
			reject("the instructions", "cannot read the instruction files: "+err.Error())
		} else if action.MentionsBranch(files) {
			res, err := action.BranchName(ctx, action.PlanningCall{
				Registry: s.dispatcher.Registry, Route: cfg.FromInstructions.ProviderModel, Timeout: cfg.FromInstructions.Timeout,
				Observer: logging.CallReporter{ConsoleReporter: s.reporter, Label: "Naming the branch"}, Stream: s.dispatcher.Stream,
				Logger: runCallLogger{store: s.store, runID: s.run.ID},
			}, action.BranchNameInput{
				Task: in.Task, Key: in.Key, RunID: s.run.ID, Date: branchToday().Format("2006-01-02"), Files: files,
			})
			naming.Usage = res.Usage
			if res.Title != "" {
				if err := domain.CheckTitleTemplate(res.Title); err != nil {
					fmt.Fprintf(s.reporter.PausingWriter(), "Warning: merge request title from the instructions not used: %s\n", err)
				} else {
					naming.Title = res.Title
				}
			}
			naming.Call = &domain.CallReport{PromptBytes: len(res.Prompt)}
			var providerErr *apperr.ProviderError
			switch {
			case ctx.Err() != nil:
				return "", nil, ctx.Err()
			case errors.As(err, &providerErr) && providerErr.Kind == apperr.FailureAuth:
				return "", nil, err
			case err != nil:
				reject("the instructions", callFailure(err, cfg.FromInstructions.Timeout))
			case res.Name == "":
				reject("the instructions", "they set no rule for branch names")
			default:
				naming.Proposed = printableCut(res.Name, maxProposedBytes)
				why, _, err := branchProblem(ctx, repo, in, res.Name)
				if err != nil {
					return "", nil, err
				}
				if why == "" {
					return done(res.Name, domain.BranchSourceInstructions)
				}
				reject("the instructions", why)
			}
		}
	}

	if cfg.Template != "" {
		name := domain.RenderBranchTemplate(cfg.Template, domain.BranchVars{
			ID: s.run.ID, Key: in.Key, Slug: domain.Slug(in.Task), Date: branchToday().Format("20060102"),
		})
		why, exists, err := branchProblem(ctx, repo, in, name)
		if err != nil {
			return "", nil, err
		}
		if exists {
			// A retry under the same template, or a branch somebody else made: the run's own ID
			// tells this one apart, once.
			name += "-" + idTail(s.run.ID)
			if why, _, err = branchProblem(ctx, repo, in, name); err != nil {
				return "", nil, err
			}
		}
		if why == "" {
			return done(name, domain.BranchSourceTemplate)
		}
		reject("worktree.branch.template", why)
	}
	return done(in.Fallback, domain.BranchSourceDefault)
}

// readInstructionFiles reads CLAUDE.md, AGENTS.md and CONTRIBUTING.md at the root of base as that
// commit records them, never from the working tree, and bounds them together. A file the commit
// does not hold is skipped.
func readInstructionFiles(ctx context.Context, repo *repository.Repository, base string) ([]action.BranchFile, error) {
	var files []action.BranchFile
	for _, name := range action.BranchInstructionFiles {
		text, ok, err := repo.ShowFile(ctx, base, name)
		if err != nil {
			return nil, err
		}
		if ok {
			files = append(files, action.BranchFile{Path: name, Text: text})
		}
	}
	return action.BoundBranchFiles(files), nil
}

// branchProblem is why name cannot be the branch of the worktree, or "": the checks that need no
// git (domain.CheckBranchName), then git's own (check-ref-format --branch), then that it is not the
// base branch, not a reserved one and not an existing local branch. exists is set when the only
// problem is that the branch exists, which is what the template's suffix is for.
func branchProblem(ctx context.Context, repo *repository.Repository, in branchInput, name string) (why string, exists bool, err error) {
	if err := domain.CheckBranchName(name); err != nil {
		return err.Error(), false, nil
	}
	ok, err := repo.ValidBranchRef(ctx, name)
	if err != nil {
		return "", false, err
	}
	switch {
	case !ok:
		return "git does not accept it as a branch name", false, nil
	case name == in.BaseBranch:
		return "it is the base branch " + name, false, nil
	case slices.Contains(in.Reserved, name):
		return "it is the target branch " + name, false, nil
	}
	exists, err = repo.BranchExists(ctx, name)
	if err != nil {
		return "", false, err
	}
	if exists {
		return "the branch " + name + " exists", true, nil
	}
	return "", false, nil
}

// callFailure says in a line why the branch-name call gave no name; a provider error's own message
// starts with a heading, so its kind is named instead.
func callFailure(err error, timeout time.Duration) string {
	var providerErr *apperr.ProviderError
	if errors.As(err, &providerErr) {
		switch providerErr.Kind {
		case apperr.FailureTimeout:
			return fmt.Sprintf("the call timed out after %s", timeout)
		case "":
		default:
			return fmt.Sprintf("the call failed (%s)", providerErr.Kind)
		}
	}
	return firstLine(err.Error())
}

// idTail is the last 6 characters of a run ID, the suffix that tells a retry's template name apart.
func idTail(id string) string {
	if len(id) > 6 {
		return id[len(id)-6:]
	}
	return id
}

// printableCut keeps the printable characters of s, at most limit bytes.
func printableCut(s string, limit int) string {
	var b strings.Builder
	for _, r := range s {
		if !unicode.IsPrint(r) || b.Len()+utf8.RuneLen(r) > limit {
			continue
		}
		b.WriteRune(r)
	}
	return b.String()
}

// oneLineCut is s on one line, at most limit bytes, cut on a character boundary.
func oneLineCut(s string, limit int) string {
	s = strings.Join(strings.Fields(s), " ")
	if len(s) <= limit {
		return s
	}
	for limit > 0 && !utf8.RuneStart(s[limit]) {
		limit--
	}
	return s[:limit]
}

// firstLine is s up to its first line break.
func firstLine(s string) string {
	line, _, _ := strings.Cut(s, "\n")
	return line
}

// runJiraKey is the key of the Jira issue a run was started from, "" for any other task.
func runJiraKey(run *domain.Run) string {
	if src := run.Task.Source; src != nil && src.Kind == domain.TaskSourceJira {
		return src.Key
	}
	return ""
}

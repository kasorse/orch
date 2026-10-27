package app

import (
	"context"
	"fmt"
	"strings"

	"orchestrator/internal/apperr"
	"orchestrator/internal/domain"
	"orchestrator/internal/persistence"
	"orchestrator/internal/repository"
)

const reviewUsage = `usage: orchestrator review --base <ref> [--head <ref>] [--task "<text>" | --jira <KEY|URL>]`

// maxReviewSubjects bounds the commit subjects of the task Go writes when the operator gave none
// (specs/delivery/SPEC.md §12.2).
const maxReviewSubjects = 50

// maxReviewTaskBytes bounds that task in bytes.
const maxReviewTaskBytes = 8 << 10

// maxReviewSubjectRunes bounds one subject of that task.
const maxReviewSubjectRunes = 200

// reviewOptions are the flags of `orchestrator review`: base and head are the refs as typed (head
// is empty for HEAD); task and jiraRef say what the review is for, and at most one is set.
type reviewOptions struct {
	configPath string
	base       string
	head       string
	task       string
	jiraRef    string
	mode       domain.RunMode
	claudeAuth string
	codexAuth  string
	verbose    bool
}

// reviewVerdictError is the error of a completed review run whose verdict fails: the command
// exits 6, "completed, but needs a person" (specs/delivery/SPEC.md §11.3). A run that is not
// completed, or passed, is nil.
func reviewVerdictError(run *domain.Run, store *persistence.Store) error {
	if run.Status != domain.RunCompleted || run.Outcome == nil || run.Outcome.ReviewVerdict != domain.ReviewFail {
		return nil
	}
	return &apperr.NeedsPersonError{Msg: fmt.Sprintf("the review found issues: verdict fail (report: %s)", store.ReportPath(run.ID))}
}

// resolveReviewTarget resolves the two refs to commits (rev-parse --verify --end-of-options), finds
// their merge base and refuses a change that is empty (specs/delivery/SPEC.md §12.1). The
// reviewed change is merge_base..head, like a merge request's.
func resolveReviewTarget(ctx context.Context, repo *repository.Repository, baseName, headName string) (domain.ReviewTarget, error) {
	target := domain.ReviewTarget{BaseRefName: baseName, HeadRefName: headName}
	var err error
	if target.Base, err = resolveReviewRef(ctx, repo, "--base", baseName); err != nil {
		return domain.ReviewTarget{}, err
	}
	if target.Head, err = resolveReviewRef(ctx, repo, "--head", headName); err != nil {
		return domain.ReviewTarget{}, err
	}
	mergeBase, ok, err := repo.MergeBase(ctx, target.Base, target.Head)
	if err != nil {
		return domain.ReviewTarget{}, err
	}
	if !ok {
		return domain.ReviewTarget{}, &apperr.ConfigError{Msg: fmt.Sprintf("%s and %s have no common ancestor: there is no change to review", baseName, headName)}
	}
	target.MergeBase = mergeBase
	empty, err := repo.SameTree(ctx, mergeBase, target.Head)
	if err != nil {
		return domain.ReviewTarget{}, err
	}
	if empty {
		return domain.ReviewTarget{}, &apperr.ConfigError{Msg: fmt.Sprintf("nothing to review: %s adds nothing to %s", headName, baseName)}
	}
	return target, nil
}

// resolveReviewRef is the commit name (a ref as typed after flag) names, or a usage error.
func resolveReviewRef(ctx context.Context, repo *repository.Repository, flag, name string) (string, error) {
	sha, ok, err := repo.ResolveCommit(ctx, name)
	if err != nil {
		return "", err
	}
	if !ok {
		return "", &apperr.ConfigError{Msg: fmt.Sprintf("%s %q is not a commit in this repository", flag, name)}
	}
	return sha, nil
}

// reviewTaskFromCommits is the task Go writes for a review that was given none: the refs and the
// subjects of the commits under review, at most maxReviewSubjects of them and maxReviewTaskBytes
// in all (specs/delivery/SPEC.md §12.2). The commits are the repository's data; the task is fenced
// like any other when it reaches a prompt.
func reviewTaskFromCommits(ctx context.Context, repo *repository.Repository, t domain.ReviewTarget) (string, error) {
	subjects, more, err := repo.CommitSubjects(ctx, t.MergeBase, t.Head, maxReviewSubjects)
	if err != nil {
		return "", err
	}
	var b strings.Builder
	fmt.Fprintf(&b, "Review the change from %s to %s. Its commits:", t.BaseRefName, t.HeadRefName)
	for i, subject := range subjects {
		line := "\n- " + cutRunes(subject, maxReviewSubjectRunes)
		if b.Len()+len(line) > maxReviewTaskBytes {
			more += len(subjects) - i
			break
		}
		b.WriteString(line)
	}
	if more > 0 {
		fmt.Fprintf(&b, "\n- …and %d more", more)
	}
	return b.String(), nil
}

// cutRunes is s cut to at most n runes, with an ellipsis when it was cut.
func cutRunes(s string, n int) string {
	if r := []rune(s); len(r) > n {
		return string(r[:n]) + "…"
	}
	return s
}

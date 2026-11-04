package action

import (
	"context"
	"encoding/json"
	"path"
	"slices"

	"github.com/kasorse/orch/internal/apperr"
	"github.com/kasorse/orch/internal/guard"
	"github.com/kasorse/orch/internal/repository"
)

// The guard lives in package guard so the verify server can use it without importing this
// package. These aliases keep it under its old names here; the JSON of GuardState is the
// one persisted as a run's resume baseline and has not changed.
type (
	Guard      = guard.Guard
	GuardState = guard.State
	RepoGuard  = guard.RepoGuard
)

// NewRepoGuard guards the patterns it is given; there is no built-in list.
var NewRepoGuard = guard.NewRepoGuard

// gitViolation names a change to .git/config or the hooks in the list Changed returns.
const gitViolation = guard.GitViolation

// RunDirPatterns are the protected patterns for the files a resume reads back - every
// run's run.json, workflow.json, decision.json and artifacts - given the runs directory
// relative to the repository top level (specs/resume/SPEC.md §9.3). Logs are output and
// never read back, so they are not included. Nil when the runs directory is outside the
// worktree. These belong to the per-step guard only: run.json changes with every step,
// so they can never be part of the run-start baseline.
func RunDirPatterns(runsRel string) []string {
	if runsRel == "" {
		return nil
	}
	return []string{
		path.Join(runsRel, "*", "run.json"),
		path.Join(runsRel, "*", "workflow.json"),
		path.Join(runsRel, "*", "decision.json"),
		path.Join(runsRel, "*", "artifacts", "**"),
	}
}

// CheckBaseline compares the repository with a run's recorded run-start baseline, before a
// resume runs anything (specs/resume/SPEC.md §9.1); see guard.CheckBaseline.
func CheckBaseline(ctx context.Context, repo *repository.Repository, baseline json.RawMessage) ([]string, error) {
	return guard.CheckBaseline(ctx, repo, baseline)
}

// SetupViolation is the error a run stops with when worktree.setup changed protected state
// (specs/worktree/SPEC.md §9); restoreRef is the run's restore point.
func SetupViolation(changed []string, restoreRef string) error {
	return &apperr.WorkflowError{Msg: violationMessage("worktree.setup changed protected paths; the run stops before any step:\n", changed, restoreRef)}
}

// BaselineViolation is the error a resume stops with when protected state changed since
// the run started.
func BaselineViolation(changed []string, restoreRef string) error {
	msg := violationMessage("protected paths changed since the run started; the resume stops before anything runs:\n", changed, restoreRef)
	if slices.Contains(changed, gitViolation) {
		// The fingerprint covers absolute paths, so a moved checkout looks the same.
		msg += "\nMoving or renaming the repository since the run started is also reported as a .git change; " +
			"such a run cannot be resumed from the new location."
	}
	return &apperr.WorkflowError{Msg: msg}
}

package app

import (
	"context"
	"fmt"

	"orchestrator/internal/apperr"
	"orchestrator/internal/config"
	"orchestrator/internal/domain"
	"orchestrator/internal/repository"
	"orchestrator/internal/workflow"
)

// inplaceHint ends every message of the checks below and the unborn-HEAD refusal: the way out of
// a worktree run that could not deliver (specs/delivery/SPEC.md §8).
const inplaceHint = "run in place: run with --inplace, or set execution.isolation: inplace."

// checkDeliverable refuses a run that will be a worktree run when it could not deliver
// (specs/delivery/SPEC.md §8), before its worktree is created and before any step: the remote is
// there, a target is known, and the secret scan is on. Each failure is a ConfigError naming
// --inplace. enterWorktree runs it once the workflow is approved; a --stage run, whose workflow
// the flags name, runs it before any call too (checkStagesDeliverable).
func checkDeliverable(ctx context.Context, repo *repository.Repository, cfg config.Config, source *domain.TaskSource) error {
	var src *domain.TaskSource
	if source != nil && source.Kind == domain.TaskSourceStory {
		src = source
	}
	if err := checkDeliveryRemote(ctx, repo, cfg); err != nil {
		return err
	}
	if err := checkDeliveryTarget(ctx, repo, cfg, src); err != nil {
		return err
	}
	return checkDeliverySecretScan(cfg)
}

// checkStagesDeliverable is checkDeliverable for the stages named with --stage, before the
// decision provider is asked anything: a run that could not deliver makes no call. Stages that
// make no workflow are left to decideManual and workflow.Build to report; a workflow without
// implement runs in place and needs none of the checks.
func checkStagesDeliverable(ctx context.Context, repo *repository.Repository, cfg config.Config, stages []domain.Stage, source *domain.TaskSource) error {
	dec, err := domain.ManualDecision(stages)
	if err != nil {
		return nil
	}
	wf, err := workflow.Build(dec.Stages)
	if err != nil || !cfg.WorktreeRun(wf) {
		return nil
	}
	return checkDeliverable(ctx, repo, cfg, source)
}

// checkDeliveryRemote is check 1: git remote get-url <delivery.remote> succeeds.
func checkDeliveryRemote(ctx context.Context, repo *repository.Repository, cfg config.Config) error {
	remote := cfg.Delivery.RemoteName()
	if _, err := repo.RemoteURL(ctx, remote); err != nil {
		return &apperr.ConfigError{Msg: fmt.Sprintf("a worktree run delivers its branch to the git remote %q (delivery.remote), and this repository has none by that name.\n\n"+
			"Add the remote, or %s", remote, inplaceHint), Err: err}
	}
	return nil
}

// checkDeliveryTarget is check 2: the merge request has a target. A story task's is the base
// branch its input names; otherwise the current branch, else delivery.target_branch.
func checkDeliveryTarget(ctx context.Context, repo *repository.Repository, cfg config.Config, src *domain.TaskSource) error {
	if src != nil && src.BaseBranch != "" {
		return nil
	}
	if src == nil {
		branch, err := repo.Branch(ctx)
		if err != nil {
			return err
		}
		if branch != "" {
			return nil
		}
	}
	if cfg.Delivery.TargetBranch != "" {
		return nil
	}
	return &apperr.ConfigError{Msg: "a worktree run opens its merge request against a target branch, and HEAD is detached, so there is none.\n\n" +
		"Set delivery.target_branch, or " + inplaceHint}
}

// checkDeliverySecretScan is check 3: a push publishes the tree, so security.secret_scan.mode
// must not be off.
func checkDeliverySecretScan(cfg config.Config) error {
	if !cfg.Security.SecretScan.Enabled() {
		return &apperr.ConfigError{Msg: "a worktree run pushes its branch, and security.secret_scan.mode is off, so nothing would look for a secret first.\n\n" +
			"Turn the scan on (stop or warn), or " + inplaceHint}
	}
	return nil
}

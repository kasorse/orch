package app

import (
	"context"
	"os"

	"github.com/kasorse/orch/internal/config"
	"github.com/kasorse/orch/internal/execution"
	"github.com/kasorse/orch/internal/repository"
)

// checkValidateDelivery is what `orchestrator validate` adds when the configured isolation is
// worktree (specs/delivery/SPEC.md §8): the delivery remote exists in the repository validate runs
// in, and the secret scan is on. The target (check 2) depends on the run's branch and is left to
// the run. Outside a git repository there is no remote to look for, and validate says nothing
// about it, as it never needed a repository before. Without a push (the hidden --story-no-push of
// a story with story.push: false) the remote is not needed.
func checkValidateDelivery(ctx context.Context, cfg config.Config, push bool, executor execution.ProcessExecutor) error {
	if cfg.Execution.IsolationMode() != config.IsolationWorktree {
		return nil
	}
	if cwd, err := os.Getwd(); err == nil && push {
		if repo, err := repository.Open(ctx, cwd, executor); err == nil {
			if err := checkDeliveryRemote(ctx, repo, cfg); err != nil {
				return err
			}
		}
	}
	return checkDeliverySecretScan(cfg)
}

package app

import (
	"context"
	"os"

	"orchestrator/internal/config"
	"orchestrator/internal/execution"
	"orchestrator/internal/repository"
)

// checkValidateDelivery is what `orchestrator validate` adds when the configured isolation is
// worktree (specs/delivery/SPEC.md §8): the delivery remote exists in the repository validate runs
// in, and the secret scan is on. The target (check 2) depends on the run's branch and is left to
// the run. Outside a git repository there is no remote to look for, and validate says nothing
// about it, as it never needed a repository before.
func checkValidateDelivery(ctx context.Context, cfg config.Config, executor execution.ProcessExecutor) error {
	if cfg.Execution.IsolationMode() != config.IsolationWorktree {
		return nil
	}
	if cwd, err := os.Getwd(); err == nil && cfg.Delivery.PushEnabled() {
		if repo, err := repository.Open(ctx, cwd, executor); err == nil {
			if err := checkDeliveryRemote(ctx, repo, cfg); err != nil {
				return err
			}
		}
	}
	return checkDeliverySecretScan(cfg)
}

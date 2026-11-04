package app

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/kasorse/orch/internal/apperr"
	"github.com/kasorse/orch/internal/config"
	"github.com/kasorse/orch/internal/domain"
)

// The checks before the worktree (specs/delivery/SPEC.md §8): each failure is a ConfigError that
// names --inplace, and enterWorktree creates nothing when one fails.
func TestEnterWorktree_RefusesARunThatCouldNotDeliver(t *testing.T) {
	cases := []struct {
		name  string
		setup func(t *testing.T, s *session, dir string)
		want  string
	}{
		{"no remote", func(t *testing.T, s *session, dir string) { gitIn(t, dir, "remote", "remove", "origin") }, `remote "origin"`},
		{"another remote", func(t *testing.T, s *session, dir string) { s.cfg.Delivery.Remote = "upstream" }, `remote "upstream"`},
		{"detached HEAD", func(t *testing.T, s *session, dir string) { gitIn(t, dir, "checkout", "-q", "--detach") }, "delivery.target_branch"},
		{"secret scan off", func(t *testing.T, s *session, dir string) { s.cfg.Security.SecretScan.Mode = config.SecretScanOff }, "security.secret_scan.mode is off"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			s, dir, _ := worktreeSession(t, true, implementTest)
			tc.setup(t, s, dir)

			err := enterWorktree(context.Background(), s)
			var cerr *apperr.ConfigError
			if !errors.As(err, &cerr) || !strings.Contains(err.Error(), tc.want) || !strings.Contains(err.Error(), "--inplace") {
				t.Fatalf("got %T %v, want a ConfigError naming %q and --inplace", err, err, tc.want)
			}
			if s.worktree != nil || s.run.Worktree != nil {
				t.Errorf("a worktree was opened: %+v", s.run.Worktree)
			}
			if list := gitIn(t, dir, "worktree", "list", "--porcelain"); strings.Count(list, "worktree ") != 1 {
				t.Errorf("a worktree was added:\n%s", list)
			}
		})
	}
}

func TestEnterWorktree_DetachedHeadWithATargetBranchDelivers(t *testing.T) {
	s, dir, _ := worktreeSession(t, true, implementTest)
	gitIn(t, dir, "checkout", "-q", "--detach")
	s.cfg.Delivery.TargetBranch = "main"

	if err := enterWorktree(context.Background(), s); err != nil {
		t.Fatal(err)
	}
	if s.worktree == nil {
		t.Error("no worktree was opened")
	}
}

// A --stage run is checked from its flags, before any call: development makes it a worktree
// run, review alone does not, and --inplace takes it out of the checks.
func TestCheckStagesDeliverable(t *testing.T) {
	s, dir, _ := worktreeSession(t, true, implementTest)
	gitIn(t, dir, "remote", "remove", "origin")
	ctx := context.Background()

	err := checkStagesDeliverable(ctx, s.repo, *s.cfg, []domain.Stage{domain.StageDevelopment}, nil)
	var cerr *apperr.ConfigError
	if !errors.As(err, &cerr) || !strings.Contains(err.Error(), "--inplace") {
		t.Errorf("development without a remote: got %T %v, want a ConfigError naming --inplace", err, err)
	}
	if err := checkStagesDeliverable(ctx, s.repo, *s.cfg, []domain.Stage{domain.StageReview}, nil); err != nil {
		t.Errorf("review alone: %v, want no check", err)
	}
	inplace := *s.cfg
	inplace.Execution.Isolation = config.IsolationInplace
	if err := checkStagesDeliverable(ctx, s.repo, inplace, []domain.Stage{domain.StageDevelopment}, nil); err != nil {
		t.Errorf("in place: %v, want no check", err)
	}
}

// A workflow without implement runs in place, so it needs neither remote nor target.
func TestEnterWorktree_WithoutImplementNeedsNoRemote(t *testing.T) {
	s, dir, _ := worktreeSession(t, true, domain.Workflow{Steps: []domain.Step{{Action: domain.ActionReview}}})
	gitIn(t, dir, "remote", "remove", "origin")

	if err := enterWorktree(context.Background(), s); err != nil {
		t.Fatal(err)
	}
	if s.worktree != nil || s.run.Options.Isolation != domain.IsolationInplace {
		t.Errorf("worktree = %v, isolation = %q; want a run in place", s.worktree, s.run.Options.Isolation)
	}
}

// Without a push (a story task of a story with story.push: false, specs/delivery-push §3) a worktree run needs no remote and no target branch;
// the secret scan stays required.
func TestEnterWorktree_PushDisabledNeedsNoRemote(t *testing.T) {
	s, dir, _ := worktreeSession(t, true, implementTest)
	gitIn(t, dir, "remote", "remove", "origin")
	gitIn(t, dir, "checkout", "-q", "--detach")
	s.run.Options.Push = new(bool)
	if err := enterWorktree(context.Background(), s); err != nil {
		t.Fatalf("enterWorktree = %v, want a worktree", err)
	}

	s, _, _ = worktreeSession(t, true, implementTest)
	s.run.Options.Push = new(bool)
	s.cfg.Security.SecretScan.Mode = config.SecretScanOff
	if err := enterWorktree(context.Background(), s); err == nil || !strings.Contains(err.Error(), "security.secret_scan.mode is off") {
		t.Errorf("secret scan off without a push: got %v, want the refusal", err)
	}
}

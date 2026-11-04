package app

import (
	"bytes"
	"context"
	"errors"
	"slices"
	"strings"
	"testing"

	"github.com/kasorse/orch/internal/apperr"
	"github.com/kasorse/orch/internal/config"
	"github.com/kasorse/orch/internal/domain"
)

func TestParseArgs_Isolation(t *testing.T) {
	for _, flag := range []string{"inplace", "worktree"} {
		got, err := parseArgs([]string{"run", "--" + flag, "x"})
		if err != nil {
			t.Fatal(err)
		}
		if got.isolation != flag || !slices.Equal(got.rest, []string{"run", "x"}) {
			t.Errorf("parseArgs = %+v, want isolation %s with rest [run x]", got, flag)
		}
	}
	if _, err := parseArgs([]string{"run", "--inplace", "--worktree", "x"}); err == nil {
		t.Error("--inplace with --worktree was accepted")
	}
}

// --inplace and --worktree belong to run alone; on resume the mode is the one the run started in.
func TestRun_IsolationFlagsOnlyWithRun(t *testing.T) {
	for _, flag := range []string{"--inplace", "--worktree"} {
		for _, command := range [][]string{{"version"}, {"validate"}, {"resume", "3f2a9c1e"}} {
			t.Run(flag+" "+command[0], func(t *testing.T) {
				var out bytes.Buffer
				args := append([]string{"--config", "/nonexistent.yaml", flag}, command...)
				err := Run(context.Background(), args, strings.NewReader(""), &out, &out)
				var cerr *apperr.ConfigError
				if !errors.As(err, &cerr) || err.Error() != flag+" is only valid with run" {
					t.Fatalf("got %v, want the %s usage error", err, flag)
				}
			})
		}
	}
}

func TestCheckWorktree_PerMode(t *testing.T) {
	implement := domain.Workflow{Steps: []domain.Step{{Action: domain.ActionImplement}, {Action: domain.ActionTest}}}
	readOnly := domain.Workflow{Steps: []domain.Step{{Action: domain.ActionReview}}}
	inplaceCfg := config.Config{Execution: config.ExecutionConfig{Isolation: config.IsolationInplace}}
	worktreeCfg := config.Config{Execution: config.ExecutionConfig{Isolation: config.IsolationWorktree}}
	defaultCfg := config.Config{}

	cases := []struct {
		name       string
		cfg        config.Config
		wf         domain.Workflow
		dirty      bool
		wantRefuse bool
		wantWarn   string
	}{
		{"worktree, implement, dirty: warning", worktreeCfg, implement, true, false, "not part of it (commit them first, or run with --inplace)."},
		{"worktree, implement, clean", worktreeCfg, implement, false, false, ""},
		{"inplace config, implement, dirty: refusal", inplaceCfg, implement, true, true, ""},
		{"default is worktree, implement, dirty: warning", defaultCfg, implement, true, false, "not part of it (commit them first, or run with --inplace)."},
		{"read-only, dirty: never a refusal", worktreeCfg, readOnly, true, false, "uncommitted changes"},
		{"read-only, inplace config, dirty: never a refusal", inplaceCfg, readOnly, true, false, "uncommitted changes"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			var stderr bytes.Buffer
			err := checkWorktree(context.Background(), nil, tc.cfg, tc.wf, tc.dirty, &stderr)
			if tc.wantRefuse != (err != nil) {
				t.Fatalf("err = %v, want refusal %v", err, tc.wantRefuse)
			}
			if tc.wantRefuse && !strings.Contains(err.Error(), "security.require_clean_worktree: false") {
				t.Errorf("refusal = %q, want today's text", err)
			}
			if !strings.Contains(stderr.String(), tc.wantWarn) || (tc.wantWarn == "" && stderr.Len() != 0) {
				t.Errorf("stderr = %q, want it to contain %q", stderr.String(), tc.wantWarn)
			}
		})
	}
}

// The org floor security.require_clean_worktree: true does not refuse a worktree run.
func TestCheckWorktree_OrgFloorDoesNotRefuseWorktreeRun(t *testing.T) {
	yes := true
	cfg := config.Config{Security: config.SecurityConfig{RequireCleanWorktree: &yes},
		Execution: config.ExecutionConfig{Isolation: config.IsolationWorktree}}
	wf := domain.Workflow{Steps: []domain.Step{{Action: domain.ActionImplement}}}
	var stderr bytes.Buffer
	if err := checkWorktree(context.Background(), nil, cfg, wf, true, &stderr); err != nil {
		t.Fatalf("worktree run refused: %v", err)
	}
}

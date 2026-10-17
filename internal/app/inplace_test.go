package app

import (
	"bytes"
	"context"
	"errors"
	"slices"
	"strings"
	"testing"

	"orchestrator/internal/apperr"
	"orchestrator/internal/config"
	"orchestrator/internal/domain"
)

func TestParseArgs_Inplace(t *testing.T) {
	got, err := parseArgs([]string{"run", "--inplace", "x"})
	if err != nil {
		t.Fatal(err)
	}
	if !got.inplace || !slices.Equal(got.rest, []string{"run", "x"}) {
		t.Errorf("parseArgs = %+v, want inplace with rest [run x]", got)
	}
}

// --inplace belongs to run alone; on resume the mode is the one the run started in.
func TestRun_InplaceOnlyWithRun(t *testing.T) {
	for _, command := range [][]string{{"version"}, {"validate"}, {"resume", "3f2a9c1e"}} {
		t.Run(command[0], func(t *testing.T) {
			var out bytes.Buffer
			args := append([]string{"--config", "/nonexistent.yaml", "--inplace"}, command...)
			err := Run(context.Background(), args, strings.NewReader(""), &out, &out)
			var cerr *apperr.ConfigError
			if !errors.As(err, &cerr) || err.Error() != "--inplace is only valid with run" {
				t.Fatalf("got %v, want the --inplace usage error", err)
			}
		})
	}
}

func TestCheckWorktree_PerMode(t *testing.T) {
	implement := domain.Workflow{Steps: []domain.Step{{Action: domain.ActionImplement}, {Action: domain.ActionTest}}}
	readOnly := domain.Workflow{Steps: []domain.Step{{Action: domain.ActionReview}}}
	inplaceCfg := config.Config{Execution: config.ExecutionConfig{Isolation: config.IsolationInplace}}
	worktreeCfg := config.Config{}

	cases := []struct {
		name        string
		cfg         config.Config
		wf          domain.Workflow
		inplaceFlag bool
		dirty       bool
		wantRefuse  bool
		wantWarn    string
	}{
		{"worktree, implement, dirty: warning", worktreeCfg, implement, false, true, false, "not part of it (commit them first, or run with --inplace)."},
		{"worktree, implement, clean", worktreeCfg, implement, false, false, false, ""},
		{"inplace config, implement, dirty: refusal", inplaceCfg, implement, false, true, true, ""},
		{"--inplace over worktree config, implement, dirty: refusal", worktreeCfg, implement, true, true, true, ""},
		{"read-only, dirty: never a refusal", worktreeCfg, readOnly, false, true, false, "uncommitted changes"},
		{"read-only, inplace config, dirty: never a refusal", inplaceCfg, readOnly, false, true, false, "uncommitted changes"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			var stderr bytes.Buffer
			err := checkWorktree(context.Background(), nil, tc.cfg, tc.wf, tc.inplaceFlag, tc.dirty, &stderr)
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
	cfg := config.Config{Security: config.SecurityConfig{RequireCleanWorktree: &yes}}
	wf := domain.Workflow{Steps: []domain.Step{{Action: domain.ActionImplement}}}
	var stderr bytes.Buffer
	if err := checkWorktree(context.Background(), nil, cfg, wf, false, true, &stderr); err != nil {
		t.Fatalf("worktree run refused: %v", err)
	}
}

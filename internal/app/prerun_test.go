package app

import (
	"bytes"
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/kasorse/orch/internal/apperr"
	"github.com/kasorse/orch/internal/config"
	"github.com/kasorse/orch/internal/domain"
)

func TestNeedsPreRunTest(t *testing.T) {
	wf := func(actions ...domain.ActionType) *domain.Workflow {
		w := &domain.Workflow{}
		for _, a := range actions {
			w.Steps = append(w.Steps, domain.Step{Action: a})
		}
		return w
	}
	implTest := wf(domain.ActionInvestigate, domain.ActionImplement, domain.ActionTest, domain.ActionReview)
	for name, tc := range map[string]struct {
		baseline string
		run      *domain.Run
		want     bool
	}{
		"implement and test":   {"", &domain.Run{Workflow: implTest}, true},
		"require_green":        {config.BaselineRequireGreen, &domain.Run{Workflow: implTest}, true},
		"baseline off":         {config.BaselineOff, &domain.Run{Workflow: implTest}, false},
		"test only":            {"", &domain.Run{Workflow: wf(domain.ActionTest)}, false},
		"implement only":       {"", &domain.Run{Workflow: wf(domain.ActionImplement, domain.ActionReview)}, false},
		"a step already ran":   {"", &domain.Run{Workflow: implTest, Executions: []domain.StepExecution{{Action: domain.ActionInvestigate}}}, false},
		"no workflow (refuse)": {"", &domain.Run{}, false},
	} {
		t.Run(name, func(t *testing.T) {
			cfg := config.Config{Test: config.TestConfig{Baseline: tc.baseline}}
			if got := needsPreRunTest(cfg, tc.run); got != tc.want {
				t.Errorf("needsPreRunTest = %v, want %v", got, tc.want)
			}
		})
	}
}

func TestParseArgs_AllowRedTests(t *testing.T) {
	cli, err := parseArgs([]string{"run", "--allow-red-tests", "fix the failing test"})
	if err != nil || !cli.allowRedTests || strings.Join(cli.rest, "|") != "run|fix the failing test" {
		t.Errorf("parseArgs = %+v, %v", cli, err)
	}
}

// --allow-red-tests belongs to run alone: a resume continues what the run was started with.
func TestRun_AllowRedTestsOnlyWithRun(t *testing.T) {
	for _, command := range [][]string{{"version"}, {"validate"}, {"resume", "3f2a9c1e"}} {
		t.Run(command[0], func(t *testing.T) {
			var out bytes.Buffer
			args := append([]string{"--config", "/nonexistent.yaml", "--allow-red-tests"}, command...)
			err := Run(context.Background(), args, strings.NewReader(""), &out, &out)
			var cerr *apperr.ConfigError
			if !errors.As(err, &cerr) || err.Error() != "--allow-red-tests is only valid with run and revise" {
				t.Fatalf("got %v, want the usage error", err)
			}
		})
	}
}

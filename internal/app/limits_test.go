package app

import (
	"testing"

	"orchestrator/internal/config"
	"orchestrator/internal/domain"
	"orchestrator/internal/workflow"
)

func TestRunOptions_ReviewFixMedium(t *testing.T) {
	for _, on := range []bool{false, true} {
		cfg := config.Config{Review: config.ReviewConfig{FixMedium: on}}
		if got := runOptions(cfg); got.ReviewFixMedium != on {
			t.Errorf("runOptions(review.fix_medium: %v) = %+v", on, got)
		}
	}
}

// resumeRun takes only limitsView(cfg) from the current config into workflow.Entry; the
// fix_medium rule comes from run.Options. A run started with the rule on therefore enters
// implement after its first passing review with medium issues, whatever the config says now.
func TestResumeEntry_UsesTheRunsFixMediumNotTheConfigs(t *testing.T) {
	medium := &domain.ReviewResult{Status: domain.ReviewPass, Issues: []domain.ReviewIssue{{Severity: domain.SeverityMedium, File: "a.go", Description: "no test for the empty input"}}}
	for _, tc := range []struct {
		name   string
		runOn  bool
		cfgOn  bool
		expect workflow.State
	}{
		{"run on, config now off", true, false, workflow.StateImplement},
		{"run off, config now on", false, true, workflow.StateDone},
	} {
		t.Run(tc.name, func(t *testing.T) {
			cfg := config.Config{
				Limits: config.LimitsConfig{MaxSteps: 20, MaxFixIterations: 3},
				Review: config.ReviewConfig{FixMedium: tc.cfgOn},
			}
			run := &domain.Run{
				Options: domain.RunOptions{ReviewFixMedium: tc.runOn},
				Workflow: &domain.Workflow{Steps: []domain.Step{
					{Action: domain.ActionImplement}, {Action: domain.ActionTest}, {Action: domain.ActionReview}}},
				Executions: []domain.StepExecution{
					{Index: 0, Action: domain.ActionImplement, Status: domain.StepCompleted},
					{Index: 1, Action: domain.ActionTest, Status: domain.StepCompleted, TestResult: &domain.TestResult{Success: true}},
					{Index: 2, Action: domain.ActionReview, Status: domain.StepCompleted, ReviewResult: medium},
				},
			}
			got, _, err := workflow.Entry(run, limitsView(cfg))
			if err != nil || got != tc.expect {
				t.Errorf("Entry = %s, %v; want %s", got, err, tc.expect)
			}
		})
	}
}

func TestLimitsView_MaxCostUSD(t *testing.T) {
	budget := 2.5
	if got := limitsView(config.Config{Limits: config.LimitsConfig{MaxCostUSD: &budget}}).MaxCostUSD; got != 2.5 {
		t.Errorf("MaxCostUSD = %v, want 2.5", got)
	}
	if got := limitsView(config.Config{}).MaxCostUSD; got != 0 {
		t.Errorf("an absent budget must be 0 (off), got %v", got)
	}
}

func TestLimitsView_StopWithoutProgress(t *testing.T) {
	yes, no := true, false
	for name, tc := range map[string]struct {
		set  *bool
		want bool
	}{
		"absent defaults to on": {nil, true},
		"true":                  {&yes, true},
		"false":                 {&no, false},
	} {
		t.Run(name, func(t *testing.T) {
			cfg := config.Config{Limits: config.LimitsConfig{MaxSteps: 12, MaxFixIterations: 2, StopWithoutProgress: tc.set}}
			view := limitsView(cfg)
			if view.StopWithoutProgress != tc.want || view.MaxSteps != 12 || view.MaxFixIterations != 2 {
				t.Errorf("limitsView = %+v, want StopWithoutProgress %v", view, tc.want)
			}
		})
	}
}

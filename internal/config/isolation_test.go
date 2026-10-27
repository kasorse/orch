package config

import (
	"strings"
	"testing"

	"orchestrator/internal/domain"
)

func workflowOf(actions ...domain.ActionType) domain.Workflow {
	var wf domain.Workflow
	for _, a := range actions {
		wf.Steps = append(wf.Steps, domain.Step{Action: a})
	}
	return wf
}

func TestLoad_IsolationDefaultsToWorktree(t *testing.T) {
	cfg, err := Load(writeTempConfig(t, validV2("")))
	if err != nil {
		t.Fatal(err)
	}
	if got := cfg.Execution.IsolationMode(); got != IsolationWorktree {
		t.Errorf("IsolationMode() = %q, want worktree", got)
	}
	if err := Validate(cfg); err != nil && strings.Contains(err.Error(), "isolation") {
		t.Errorf("the default isolation is refused: %v", err)
	}
}

func TestLoad_IsolationInplace(t *testing.T) {
	yaml := strings.Replace(validV2(""), "execution: {timeout: 15m}", "execution: {timeout: 15m, isolation: inplace}", 1)
	cfg, err := Load(writeTempConfig(t, yaml))
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Execution.IsolationMode() != IsolationInplace {
		t.Errorf("IsolationMode() = %q, want inplace", cfg.Execution.IsolationMode())
	}
	if cfg.WorktreeRun(workflowOf(domain.ActionImplement)) {
		t.Error("WorktreeRun with inplace isolation")
	}
}

func TestValidate_Isolation(t *testing.T) {
	cfg := validConfig()
	cfg.Execution.Isolation = "sideways"
	err := Validate(cfg)
	if err == nil || !strings.Contains(err.Error(), "execution.isolation") || !strings.Contains(err.Error(), "sideways") {
		t.Errorf("isolation: sideways: got %v, want an error naming execution.isolation", err)
	}

	cfg = validConfig()
	cfg.Execution.Isolation = IsolationInplace
	cfg.Worktree.Setup = "npm ci"
	err = Validate(cfg)
	if err == nil || !strings.Contains(err.Error(), "worktree.setup") {
		t.Errorf("worktree.setup with inplace: got %v, want an error naming worktree.setup", err)
	}

	cfg.Execution.Isolation = IsolationWorktree
	if err := Validate(cfg); err != nil {
		t.Errorf("worktree.setup with worktree: %v", err)
	}

	// With the key absent the run is a worktree run, so setup runs.
	cfg.Execution.Isolation = ""
	if err := Validate(cfg); err != nil {
		t.Errorf("worktree.setup with isolation absent: %v", err)
	}
}

func TestLoad_DeliveryDefaultsAndIsStrict(t *testing.T) {
	cfg, err := Load(writeTempConfig(t, validV2("")))
	if err != nil {
		t.Fatal(err)
	}
	if got := cfg.Delivery.RemoteName(); got != "origin" {
		t.Errorf("RemoteName() = %q, want origin", got)
	}
	if cfg.Delivery.TargetBranch != "" {
		t.Errorf("TargetBranch = %q, want none", cfg.Delivery.TargetBranch)
	}

	cfg, err = Load(writeTempConfig(t, validV2("delivery: {remote: upstream, target_branch: main}\n")))
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Delivery.RemoteName() != "upstream" || cfg.Delivery.TargetBranch != "main" {
		t.Errorf("Delivery = %+v, want upstream and main", cfg.Delivery)
	}

	_, err = Load(writeTempConfig(t, validV2("delivery: {remtoe: origin}\n")))
	if err == nil || !strings.Contains(err.Error(), "remtoe") {
		t.Errorf("unknown key under delivery: got %v, want an unknown-key error", err)
	}
}

func TestLoad_WorktreeSectionIsStrict(t *testing.T) {
	_, err := Load(writeTempConfig(t, validV2("worktree: {setp: npm ci}\n")))
	if err == nil || !strings.Contains(err.Error(), "setp") {
		t.Errorf("unknown key under worktree: got %v, want an unknown-key error", err)
	}
	cfg, err := Load(writeTempConfig(t, validV2("worktree: {setup: npm ci}\n")))
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Worktree.Setup != "npm ci" {
		t.Errorf("Worktree.Setup = %q", cfg.Worktree.Setup)
	}
}

func TestConfig_WorktreeRun(t *testing.T) {
	cfg := validConfig()
	impl := workflowOf(domain.ActionImplement, domain.ActionTest)
	readOnly := workflowOf(domain.ActionReview)
	if !cfg.WorktreeRun(impl) {
		t.Error("default isolation with implement is not a worktree run")
	}
	cfg.Execution.Isolation = IsolationWorktree
	if !cfg.WorktreeRun(impl) {
		t.Error("worktree isolation with implement is not a worktree run")
	}
	if cfg.WorktreeRun(readOnly) {
		t.Error("a workflow without implement is a worktree run")
	}
	cfg.Execution.Isolation = IsolationInplace
	if cfg.WorktreeRun(impl) {
		t.Error("inplace isolation is a worktree run")
	}
}

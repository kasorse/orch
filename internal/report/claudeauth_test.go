package report

import (
	"strings"
	"testing"

	"orchestrator/internal/domain"
)

func TestRunSummary_ClaudeAuth(t *testing.T) {
	run := summaryRun()
	if got := RunSummary(run, nil); strings.Contains(got, "Claude auth") || strings.Contains(got, "billed to the subscription") {
		t.Errorf("a run without the field shows auth:\n%s", got)
	}

	api := &domain.ClaudeAuth{Requested: domain.ClaudeAuthAuto, Mode: domain.ClaudeAuthAPI, DecidedBy: domain.ClaudeAuthByDefault, KeyEnv: "ANTHROPIC_API_KEY"}
	login := &domain.ClaudeAuth{Requested: domain.ClaudeAuthLogin, Mode: domain.ClaudeAuthLogin, DecidedBy: domain.ClaudeAuthByFlag, KeyEnv: "ANTHROPIC_API_KEY"}

	run.ClaudeAuth = api
	got := RunSummary(run, nil)
	if !strings.Contains(got, "| Claude auth | api key ($ANTHROPIC_API_KEY, auto) |") || strings.Contains(got, "billed to the subscription") {
		t.Errorf("api run:\n%s", got)
	}

	run.Resumes = []domain.Resume{{ClaudeAuth: login}, {ClaudeAuth: login}}
	got = RunSummary(run, nil)
	if !strings.Contains(got, "api key ($ANTHROPIC_API_KEY, auto); resume 1: CLI login") || strings.Contains(got, "resume 2") {
		t.Errorf("resumed run:\n%s", got)
	}
	if !strings.Contains(got, "(estimate; billed to the subscription)") {
		t.Errorf("login part without the cost note:\n%s", got)
	}
}

// specs/claude-auth/SPEC.md §17.5: api mode counts the api_env variables; login, which only
// records the names it removed, shows no count.
func TestRunSummary_ClaudeAuthAPIEnv(t *testing.T) {
	run := summaryRun()
	run.ClaudeAuth = &domain.ClaudeAuth{Requested: domain.ClaudeAuthAPI, Mode: domain.ClaudeAuthAPI, DecidedBy: domain.ClaudeAuthByConfig,
		KeyEnv: "ANTHROPIC_API_KEY", APIEnv: []string{"ANTHROPIC_BASE_URL", "AWS_PROFILE", "AWS_REGION"}}
	run.Resumes = []domain.Resume{{ClaudeAuth: &domain.ClaudeAuth{Requested: domain.ClaudeAuthLogin, Mode: domain.ClaudeAuthLogin,
		DecidedBy: domain.ClaudeAuthByFlag, KeyEnv: "ANTHROPIC_API_KEY", APIEnv: []string{"ANTHROPIC_BASE_URL"}}}}
	got := RunSummary(run, nil)
	if !strings.Contains(got, "| Claude auth | api key ($ANTHROPIC_API_KEY), env: 3 variables; resume 1: CLI login |") {
		t.Errorf("summary:\n%s", got)
	}
}

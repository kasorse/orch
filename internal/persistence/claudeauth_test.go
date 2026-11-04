package persistence

import (
	"strings"
	"testing"

	"github.com/kasorse/orch/internal/domain"
)

// claude_auth (specs/claude-auth/SPEC.md §10): optional on the run and on each resume, the mode
// from the closed set; a run written before the field has none.
func TestValidateRun_ClaudeAuth(t *testing.T) {
	good := &domain.ClaudeAuth{Requested: domain.ClaudeAuthAuto, Mode: domain.ClaudeAuthAPI, DecidedBy: domain.ClaudeAuthByDefault, KeyEnv: "ANTHROPIC_API_KEY"}
	login := &domain.ClaudeAuth{Requested: domain.ClaudeAuthLogin, Mode: domain.ClaudeAuthLogin, DecidedBy: domain.ClaudeAuthByFlag, KeyEnv: "ANTHROPIC_API_KEY"}
	withEnv := &domain.ClaudeAuth{Requested: domain.ClaudeAuthAPI, Mode: domain.ClaudeAuthAPI, DecidedBy: domain.ClaudeAuthByConfig, KeyEnv: "ANTHROPIC_API_KEY",
		APIEnv: []string{"ANTHROPIC_BASE_URL", "AWS_PROFILE"}}
	withRun := func(a *domain.ClaudeAuth) *domain.Run {
		return &domain.Run{ID: "r", Status: domain.RunFailed, ClaudeAuth: a}
	}
	withResume := func(a *domain.ClaudeAuth) *domain.Run {
		return &domain.Run{ID: "r", Status: domain.RunFailed, ClaudeAuth: good, Resumes: []domain.Resume{
			{FromStatus: domain.RunFailed, Entry: domain.ActionImplement, ClaudeAuth: a}}}
	}

	for name, run := range map[string]*domain.Run{
		"an old run without the field": withRun(nil),
		"a run in api mode":            withRun(good),
		"a resume in another mode":     withResume(login),
		"a resume without the field":   withResume(nil),
		"api_env names":                withRun(withEnv),
	} {
		if err := validateRun(run, "r"); err != nil {
			t.Errorf("%s: %v", name, err)
		}
	}

	for name, a := range map[string]*domain.ClaudeAuth{
		"unknown requested": {Requested: "subscription", Mode: "api", DecidedBy: "flag", KeyEnv: "K"},
		"auto as the mode":  {Requested: "auto", Mode: "auto", DecidedBy: "flag", KeyEnv: "K"},
		"empty mode":        {Requested: "auto", DecidedBy: "flag", KeyEnv: "K"},
		"unknown decider":   {Requested: "auto", Mode: "api", DecidedBy: "env", KeyEnv: "K"},
		"space in key_env":  {Requested: "auto", Mode: "api", DecidedBy: "flag", KeyEnv: "A B"},
		// SPEC §17.5: api_env holds names, so a value cannot be recorded there.
		"a value in api_env": {Requested: "api", Mode: "api", DecidedBy: "flag", KeyEnv: "K", APIEnv: []string{"AWS_PROFILE=claude-bedrock"}},
		"empty api_env name": {Requested: "api", Mode: "api", DecidedBy: "flag", KeyEnv: "K", APIEnv: []string{""}},
	} {
		if err := validateRun(withRun(a), "r"); err == nil || !strings.Contains(err.Error(), "claude_auth:") {
			t.Errorf("run, %s: got %v, want a claude_auth error", name, err)
		}
		if err := validateRun(withResume(a), "r"); err == nil || !strings.Contains(err.Error(), "resumes[0].claude_auth:") {
			t.Errorf("resume, %s: got %v, want a resumes[0].claude_auth error", name, err)
		}
	}
}

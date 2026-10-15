package app

import (
	"testing"

	"orchestrator/internal/domain"
)

func TestClaudeAuthChangeNotice(t *testing.T) {
	api := domain.ClaudeAuth{Requested: domain.ClaudeAuthAPI, Mode: domain.ClaudeAuthAPI}
	login := domain.ClaudeAuth{Requested: domain.ClaudeAuthLogin, Mode: domain.ClaudeAuthLogin}

	if got := claudeAuthChangeNotice(&domain.Run{}, api); got != "" {
		t.Errorf("a run without a record: %q", got)
	}
	run := &domain.Run{ClaudeAuth: &login}
	if got := claudeAuthChangeNotice(run, login); got != "" {
		t.Errorf("same mode: %q", got)
	}
	if got, want := claudeAuthChangeNotice(run, api), "claude auth changed since the run started: login → api"; got != want {
		t.Errorf("got %q, want %q", got, want)
	}
	run.Resumes = []domain.Resume{{ClaudeAuth: &api}}
	if got := claudeAuthChangeNotice(run, api); got != "" {
		t.Errorf("same as the last resume: %q", got)
	}
}

package app

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/kasorse/orch/internal/action"
	"github.com/kasorse/orch/internal/config"
	"github.com/kasorse/orch/internal/domain"
	"github.com/kasorse/orch/internal/execution"
	"github.com/kasorse/orch/internal/guard"
	"github.com/kasorse/orch/internal/logging"
	"github.com/kasorse/orch/internal/persistence"
	"github.com/kasorse/orch/internal/repository"
)

// memArtifacts serves one implementation artifact to Dispatcher.Restore.
type memArtifacts map[int]string

func (m memArtifacts) ReadArtifact(_ string, index int, _ domain.ActionType) ([]byte, error) {
	s, ok := m[index]
	if !ok {
		return nil, os.ErrNotExist
	}
	return []byte(s), nil
}

// commitSession is a finished run that changed migrations/a.sql in a real repository, ready
// for offerCommit under auto_commit. matches are what its implement recorded.
func commitSession(t *testing.T, matches []domain.PathRuleMatch) (*session, *bytes.Buffer, string) {
	t.Helper()
	dir := t.TempDir()
	git := func(args ...string) string {
		cmd := exec.Command("git", args...)
		cmd.Dir = dir
		out, err := cmd.CombinedOutput()
		if err != nil {
			t.Fatalf("git %v: %v\n%s", args, err, out)
		}
		return strings.TrimSpace(string(out))
	}
	git("init", "-q")
	git("config", "user.email", "t@example.com")
	git("config", "user.name", "T")
	if err := os.WriteFile(filepath.Join(dir, "main.go"), []byte("package main\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	git("add", "-A")
	git("commit", "-qm", "init")
	head := git("rev-parse", "HEAD")

	repo := &repository.Repository{Dir: dir, Executor: execution.NewOSProcessExecutor()}
	state, err := guard.NewRepoGuard(repo, []string{"protected.txt"}).Snapshot(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	baseline, err := json.Marshal(state)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(dir, "migrations"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "migrations", "a.sql"), []byte("select 1;\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	store := persistence.NewStore(t.TempDir())
	run, err := store.NewRun(domain.Task{Description: "add a migration"})
	if err != nil {
		t.Fatal(err)
	}
	run.Status = domain.RunCompleted
	run.Workflow = &domain.Workflow{Steps: []domain.Step{{Action: domain.ActionImplement}}}
	run.PreRunRef, run.HeadRef, run.ResumeBaseline = head, head, baseline
	run.Executions = []domain.StepExecution{{Index: 0, Action: domain.ActionImplement, Status: domain.StepCompleted, PathRules: matches}}

	cfg := &config.Config{AutoCommit: true}
	dispatcher := action.New(cfg, nil, repo, nil)
	if err := dispatcher.Restore(run, memArtifacts{0: `{"summary":"Add a migration","summary_short":"Add a migration","changed_files":null,"tests_added":null}`}); err != nil {
		t.Fatal(err)
	}
	var out bytes.Buffer
	s := &session{cfg: cfg, repo: repo, store: store, run: run, reporter: logging.NewConsoleReporter(&out), dispatcher: dispatcher}
	return s, &out, head
}

func headOf(t *testing.T, s *session) string {
	t.Helper()
	cmd := exec.Command("git", "rev-parse", "HEAD")
	cmd.Dir = s.repo.Dir
	out, err := cmd.Output()
	if err != nil {
		t.Fatal(err)
	}
	return strings.TrimSpace(string(out))
}

func TestOfferCommit_AutoCommitNeedsApprovalForManualRules(t *testing.T) {
	matches := []domain.PathRuleMatch{
		{Match: "migrations/**", Files: []string{"migrations/a.sql"}, Missing: []domain.Stage{}, Approval: domain.PathApprovalManual},
		{Match: "**/*.sql", Files: []string{"migrations/a.sql"}, Missing: []domain.Stage{}},
	}
	s, out, head := commitSession(t, matches)
	offerCommit(context.Background(), s, true)

	want := "\nNot committing automatically: policy.paths requires manual approval: migrations/** (1 file); commit what you want yourself.\n"
	if out.String() != want {
		t.Errorf("output = %q, want %q", out.String(), want)
	}
	if got := headOf(t, s); got != head {
		t.Errorf("HEAD moved from %s to %s", head, got)
	}
	if s.run.Commit != "" {
		t.Errorf("run.Commit = %q", s.run.Commit)
	}
}

func TestOfferCommit_AutoCommitWithoutApprovalCommits(t *testing.T) {
	for _, c := range []struct {
		name    string
		matches []domain.PathRuleMatch
	}{
		{"no rule matched", nil},
		{"a rule without approval", []domain.PathRuleMatch{{Match: "**/*.sql", Files: []string{"migrations/a.sql"}, Missing: []domain.Stage{}}}},
	} {
		t.Run(c.name, func(t *testing.T) {
			s, out, head := commitSession(t, c.matches)
			offerCommit(context.Background(), s, true)

			if got := headOf(t, s); got == head || s.run.Commit != got {
				t.Errorf("HEAD = %s (run.Commit %q), want a new commit after %s\n%s", got, s.run.Commit, head, out)
			}
			if !strings.Contains(out.String(), "Committed ") || strings.Contains(out.String(), "manual approval") {
				t.Errorf("output = %q", out.String())
			}
		})
	}
}

func TestCommitOffer_PathRules(t *testing.T) {
	manual := domain.PathRuleMatch{Match: "migrations/**", Files: []string{"migrations/a.sql"}, Approval: domain.PathApprovalManual}
	plain := domain.PathRuleMatch{Match: "docs/**", Files: []string{"docs/a.md"}}
	earlier := domain.PathRuleMatch{Match: "old/**", Files: []string{"old/a.go"}, Approval: domain.PathApprovalManual}
	run := domain.Run{ID: "r1", Executions: []domain.StepExecution{
		{Action: domain.ActionImplement, PathRules: []domain.PathRuleMatch{earlier}},
		{Action: domain.ActionImplement, PathRules: []domain.PathRuleMatch{plain, manual}},
	}}
	if got, want := commitOffer(&run, nil).PathRules, []domain.PathRuleMatch{manual}; !reflect.DeepEqual(got, want) {
		t.Errorf("PathRules = %+v, want %+v", got, want)
	}
	run.Executions[1].PathRules = []domain.PathRuleMatch{plain}
	if got := commitOffer(&run, nil).PathRules; len(got) != 0 {
		t.Errorf("a rule without approval is offered: %+v", got)
	}
}

func TestApprovalReason(t *testing.T) {
	got := approvalReason([]domain.PathRuleMatch{
		{Match: "migrations/**", Files: []string{"migrations/a.sql"}},
		{Match: "**/*.sql", Files: []string{"a.sql", "b.sql"}},
	})
	want := fmt.Sprintf("policy.paths requires manual approval: %s; commit what you want yourself.",
		"migrations/** (1 file), **/*.sql (2 files)")
	if got != want {
		t.Errorf("reason = %q, want %q", got, want)
	}
}

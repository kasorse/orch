package app

import (
	"bytes"
	"context"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"orchestrator/internal/action"
	"orchestrator/internal/apperr"
	"orchestrator/internal/config"
	"orchestrator/internal/domain"
	"orchestrator/internal/execution"
	"orchestrator/internal/logging"
	"orchestrator/internal/persistence"
	"orchestrator/internal/repository"
)

// worktreeSession is a fresh worktree run with an approved workflow over a real repository
// at dir, its state directory in a temporary $XDG_STATE_HOME. commit makes the repository's
// first commit; without it HEAD is unborn.
func worktreeSession(t *testing.T, commit bool, wf domain.Workflow) (*session, string, *bytes.Buffer) {
	t.Helper()
	t.Setenv("XDG_STATE_HOME", t.TempDir())
	dir := t.TempDir()
	gitIn(t, dir, "init", "-q", "-b", "main")
	gitIn(t, dir, "config", "user.email", "t@example.com")
	gitIn(t, dir, "config", "user.name", "T")
	if commit {
		if err := os.WriteFile(filepath.Join(dir, "main.go"), []byte("package main\n"), 0o644); err != nil {
			t.Fatal(err)
		}
		gitIn(t, dir, "add", "-A")
		gitIn(t, dir, "commit", "-qm", "init")
	}
	repo := &repository.Repository{Dir: dir, Executor: execution.NewOSProcessExecutor()}
	store := persistence.NewStore(t.TempDir())
	run, err := store.NewRun(domain.Task{Description: "fix it"})
	if err != nil {
		t.Fatal(err)
	}
	run.Options.Isolation = domain.IsolationWorktree
	run.Workflow = &wf
	cfg := &config.Config{}
	var out bytes.Buffer
	s := &session{cfg: cfg, repo: repo, store: store, run: run, reporter: logging.NewConsoleReporter(&out),
		dispatcher: action.New(cfg, nil, repo, nil), runsRel: ".orchestrator/runs"}
	s.dispatcher.RunsDir = s.runsRel
	return s, dir, &out
}

func gitIn(t *testing.T, dir string, args ...string) string {
	t.Helper()
	cmd := exec.Command("git", args...)
	cmd.Dir = dir
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("git %v: %v\n%s", args, err, out)
	}
	return strings.TrimSpace(string(out))
}

var implementTest = domain.Workflow{Steps: []domain.Step{{Action: domain.ActionImplement}, {Action: domain.ActionTest}}}

func TestOpenWorktree_UnbornHeadIsAConfigError(t *testing.T) {
	s, _, _ := worktreeSession(t, false, implementTest)
	_, err := openWorktree(context.Background(), s)
	var cerr *apperr.ConfigError
	if !errors.As(err, &cerr) || !strings.Contains(err.Error(), "isolation: inplace") {
		t.Fatalf("got %T %v, want a ConfigError naming isolation: inplace", err, err)
	}
	if s.run.Worktree != nil {
		t.Errorf("worktree recorded: %+v", s.run.Worktree)
	}
}

func TestOpenWorktree_ExistingBranchIsRefused(t *testing.T) {
	s, dir, _ := worktreeSession(t, true, implementTest)
	branch := domain.WorktreeBranch(s.run.ID)
	gitIn(t, dir, "branch", branch)

	_, err := openWorktree(context.Background(), s)
	if err == nil || !strings.Contains(err.Error(), branch) || !strings.Contains(err.Error(), "not reused") {
		t.Fatalf("got %v, want the refusal naming %s", err, branch)
	}
	if s.run.Worktree != nil {
		t.Errorf("worktree recorded: %+v", s.run.Worktree)
	}
	if list := gitIn(t, dir, "worktree", "list", "--porcelain"); strings.Count(list, "worktree ") != 1 {
		t.Errorf("a worktree was added:\n%s", list)
	}
}

func TestOpenWorktree_ExistingPathIsRefused(t *testing.T) {
	s, dir, _ := worktreeSession(t, true, implementTest)
	state, err := persistence.StateDir()
	if err != nil {
		t.Fatal(err)
	}
	path := persistence.WorktreePath(state, filepath.Join(dir, ".git"), s.run.ID)
	if err := os.MkdirAll(path, 0o700); err != nil {
		t.Fatal(err)
	}
	if _, err := openWorktree(context.Background(), s); err == nil || !strings.Contains(err.Error(), path) {
		t.Fatalf("got %v, want the refusal naming %s", err, path)
	}
	if out := gitIn(t, dir, "branch", "--list", domain.WorktreeBranch(s.run.ID)); out != "" {
		t.Errorf("the branch was created: %q", out)
	}
}

// A worktree run gets its worktree recorded at once, and the session switches to it.
func TestEnterWorktree_CreatesRecordsAndSwitches(t *testing.T) {
	s, dir, out := worktreeSession(t, true, implementTest)
	operator := s.repo
	head := gitIn(t, dir, "rev-parse", "HEAD")

	if err := enterWorktree(context.Background(), s); err != nil {
		t.Fatal(err)
	}
	w := s.run.Worktree
	if w == nil || s.worktree != w {
		t.Fatalf("worktree = %+v, session worktree = %+v", w, s.worktree)
	}
	state, _ := persistence.StateDir()
	if want := persistence.WorktreePath(state, filepath.Join(dir, ".git"), s.run.ID); w.Path != want {
		t.Errorf("path = %s, want the derived %s", w.Path, want)
	}
	if w.Branch != domain.WorktreeBranch(s.run.ID) || w.BaseRef != head || w.BaseBranch != "main" {
		t.Errorf("worktree = %+v, want orchestrator/<id> from main@%s", w, head)
	}
	if pointer, err := repository.ReadGitPointer(w.Path); err != nil || pointer != w.AdminDir {
		t.Errorf("the .git pointer = %q, %v; want the recorded admin dir %q", pointer, err, w.AdminDir)
	}
	if s.repo == operator || s.repo.Dir != w.Path || s.dispatcher.Repo != s.repo {
		t.Errorf("the session did not switch: repo %s, dispatcher %s", s.repo.Dir, s.dispatcher.Repo.Dir)
	}
	if s.runsRel != "" || s.dispatcher.RunsDir != "" {
		t.Errorf("runs directory still guarded in the worktree: %q / %q", s.runsRel, s.dispatcher.RunsDir)
	}
	if s.gitPointer() != w.AdminDir || s.newRepoGuard(nil).GitPointer != w.AdminDir {
		t.Errorf("the guards do not check the pointer")
	}
	if files := s.readOnlyFiles(); len(files) != 1 || filepath.Base(files[0]) != ".git" {
		t.Errorf("readOnlyFiles = %q", files)
	}
	if got := out.String(); !strings.Contains(got, "Worktree: "+w.Path+" ("+w.Branch+" from main@"+head[:7]+")") {
		t.Errorf("output lacks the Worktree line:\n%s", got)
	}

	saved, err := s.store.LoadRun(s.run.ID)
	if err != nil {
		t.Fatalf("run.json with a worktree does not load: %v", err)
	}
	if saved.Worktree == nil || !reflect.DeepEqual(*saved.Worktree, *w) {
		t.Errorf("saved worktree = %+v, want %+v", saved.Worktree, w)
	}
	if status := gitIn(t, dir, "status", "--porcelain"); status != "" {
		t.Errorf("the operator's tree changed:\n%s", status)
	}
}

// O3: a workflow without implement runs in place, and the record says so.
func TestEnterWorktree_ReadOnlyWorkflowRunsInPlace(t *testing.T) {
	s, dir, _ := worktreeSession(t, true, domain.Workflow{Steps: []domain.Step{{Action: domain.ActionReview}}})
	operator := s.repo
	if err := enterWorktree(context.Background(), s); err != nil {
		t.Fatal(err)
	}
	if s.run.Worktree != nil || s.worktree != nil || s.repo != operator {
		t.Errorf("a read-only workflow left its tree: %+v", s.run.Worktree)
	}
	if s.run.Options.Isolation != domain.IsolationInplace {
		t.Errorf("options.isolation = %q, want inplace", s.run.Options.Isolation)
	}
	saved, err := s.store.LoadRun(s.run.ID)
	if err != nil || saved.Options.Isolation != domain.IsolationInplace {
		t.Errorf("saved isolation = %+v, %v", saved, err)
	}
	if list := gitIn(t, dir, "worktree", "list", "--porcelain"); strings.Count(list, "worktree ") != 1 {
		t.Errorf("a worktree was added:\n%s", list)
	}
}

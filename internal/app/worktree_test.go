package app

import (
	"bytes"
	"context"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"slices"
	"strings"
	"testing"
	"time"

	"orchestrator/internal/action"
	"orchestrator/internal/apperr"
	"orchestrator/internal/config"
	"orchestrator/internal/domain"
	"orchestrator/internal/execution"
	"orchestrator/internal/guard"
	"orchestrator/internal/logging"
	"orchestrator/internal/persistence"
	"orchestrator/internal/provider"
	"orchestrator/internal/repository"
	"orchestrator/internal/testexec"
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

// setupSession is worktreeSession with a real test runner, test.sandbox: none (the host may
// have no bwrap), an empty provider registry (the first provider call fails) and the given
// worktree.setup and test.command.
func setupSession(t *testing.T, setup, testCommand string) (*session, string) {
	t.Helper()
	s, dir, _ := worktreeSession(t, true, implementTest)
	s.cfg.Test.Command = testCommand
	s.cfg.Test.Sandbox = config.SandboxNone
	s.cfg.Worktree.Setup = setup
	s.runner = testexec.New(execution.NewOSProcessExecutor())
	s.dispatcher.Runner = s.runner
	s.dispatcher.Registry = provider.NewRegistry()
	s.dispatcher.Artifacts = s.store
	s.dispatcher.Logs = s.store
	s.dispatcher.Observer = s.reporter
	return s, dir
}

func runTail(s *session) error {
	return executeWorkflow(context.Background(), s, tailOptions{
		mode: domain.ModeAuto,
		engineContext: func() (context.Context, context.CancelFunc) {
			return context.WithCancel(context.Background())
		},
	})
}

// runTailWithin is runTail with the engine's share of execution.timeout set to d.
func runTailWithin(s *session, d time.Duration) error {
	return executeWorkflow(context.Background(), s, tailOptions{
		mode: domain.ModeAuto,
		engineContext: func() (context.Context, context.CancelFunc) {
			return context.WithTimeout(context.Background(), d)
		},
	})
}

// setupExecutor records the one process the setup runner starts and reports a clean exit.
type setupExecutor struct{ specs []execution.Spec }

func (e *setupExecutor) Run(_ context.Context, spec execution.Spec) (execution.ProcessResult, error) {
	e.specs = append(e.specs, spec)
	return execution.ProcessResult{Stdout: "installed\n"}, nil
}

// worktree.setup writes what the pre-run test needs: the run gets past the pre-run test, and
// the setup is recorded as done with its output in the logs.
func TestExecuteWorkflow_SetupBeforePreRunTest(t *testing.T) {
	s, _ := setupSession(t, "echo installed; echo ready > ready.txt", "test -f ready.txt")

	err := runTail(s)
	if err != nil && (strings.Contains(err.Error(), "worktree setup") || strings.Contains(err.Error(), "before any change")) {
		t.Fatalf("setup or the pre-run test failed: %v", err)
	}
	if w := s.run.Worktree; w == nil || !w.SetupDone {
		t.Errorf("worktree = %+v, want setup_done", w)
	}
	if r := s.run.PreRunTest; r == nil || !r.Success {
		t.Errorf("pre-run test = %+v, want a pass", r)
	}
	saved, loadErr := s.store.LoadRun(s.run.ID)
	if loadErr != nil || saved.Worktree == nil || !saved.Worktree.SetupDone {
		t.Errorf("saved worktree = %+v, %v; want setup_done", saved, loadErr)
	}
	log, readErr := os.ReadFile(s.store.CallLogPath(s.run.ID, "setup", "stdout"))
	if readErr != nil || !strings.Contains(string(log), "installed") {
		t.Errorf("setup.stdout.log = %q, %v", log, readErr)
	}
}

// A failing setup fails the run before the restore point, the baseline, the pre-run test and
// any provider call.
func TestExecuteWorkflow_SetupFailureStopsTheRun(t *testing.T) {
	s, _ := setupSession(t, "echo boom >&2; exit 3", "true")

	err := runTail(s)
	if err == nil || !strings.Contains(err.Error(), "worktree setup failed (exit 3)") {
		t.Fatalf("got %v, want the setup failure", err)
	}
	if !strings.Contains(err.Error(), "setup.stderr.log") {
		t.Errorf("the error does not name the log: %v", err)
	}
	if s.run.Status != domain.RunFailed || len(s.run.Executions) != 0 || s.run.PreRunTest != nil || s.run.ResumeBaseline != nil {
		t.Errorf("run = %s, %d executions, pre-run %v, baseline %q", s.run.Status, len(s.run.Executions), s.run.PreRunTest, s.run.ResumeBaseline)
	}
	if s.run.Worktree == nil || s.run.Worktree.SetupDone {
		t.Errorf("worktree = %+v, want setup_done unset", s.run.Worktree)
	}
	if log, _ := os.ReadFile(s.store.CallLogPath(s.run.ID, "setup", "stderr")); !strings.Contains(string(log), "boom") {
		t.Errorf("setup.stderr.log = %q", log)
	}
}

// Unsandboxed, setup can reach the shared common directory; the guard fails the run.
func TestExecuteWorkflow_SetupChangingGitConfigIsAViolation(t *testing.T) {
	s, dir := setupSession(t, "", "true")
	s.cfg.Worktree.Setup = "printf '[orchtest]\\n\\tx = 1\\n' >> " + filepath.Join(dir, ".git", "config")

	err := runTail(s)
	if err == nil || !strings.Contains(err.Error(), "worktree.setup changed protected paths") || !strings.Contains(err.Error(), guard.GitViolation) {
		t.Fatalf("got %v, want a guard violation naming %s", err, guard.GitViolation)
	}
	if s.run.Status != domain.RunFailed || s.run.Worktree.SetupDone || s.run.PreRunTest != nil {
		t.Errorf("run = %s, setup_done %v, pre-run %v", s.run.Status, s.run.Worktree.SetupDone, s.run.PreRunTest)
	}
}

// Setup runs inside the engine's share of execution.timeout: one that outlives it is killed and
// fails the run, with the reason in its log.
func TestExecuteWorkflow_SetupTimeoutFailsTheRun(t *testing.T) {
	s, _ := setupSession(t, "sleep 30", "true")

	start := time.Now()
	err := runTailWithin(s, 2*time.Second)
	if err == nil || !strings.Contains(err.Error(), "worktree setup failed (timed out") || !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("got %v, want the setup timeout", err)
	}
	if elapsed := time.Since(start); elapsed > 15*time.Second {
		t.Errorf("setup was not killed at the deadline: the run took %s", elapsed)
	}
	if s.run.Status != domain.RunFailed || !strings.Contains(s.run.FailureReason, "worktree setup failed") {
		t.Errorf("run = %s, failure_reason %q", s.run.Status, s.run.FailureReason)
	}
	if s.run.Worktree.SetupDone || s.run.PreRunTest != nil || s.run.ResumeBaseline != nil {
		t.Errorf("setup_done %v, pre-run %v, baseline %q", s.run.Worktree.SetupDone, s.run.PreRunTest, s.run.ResumeBaseline)
	}
	if log, _ := os.ReadFile(s.store.CallLogPath(s.run.ID, "setup", "stderr")); !strings.Contains(string(log), "did not finish") {
		t.Errorf("setup.stderr.log = %q, want why there is no output", log)
	}
}

// A setup killed at the deadline is still checked: what it changed in the git config before it
// was killed fails the run as a guard violation.
func TestExecuteWorkflow_SetupKilledIsStillGuarded(t *testing.T) {
	s, dir := setupSession(t, "", "true")
	s.cfg.Worktree.Setup = "printf '[orchtest]\\n\\tx = 1\\n' >> " + filepath.Join(dir, ".git", "config") + "; sleep 30"

	err := runTailWithin(s, 2*time.Second)
	if err == nil || !strings.Contains(err.Error(), "worktree.setup changed protected paths") || !strings.Contains(err.Error(), guard.GitViolation) {
		t.Fatalf("got %v, want a guard violation naming %s", err, guard.GitViolation)
	}
	if s.run.Status != domain.RunFailed || s.run.Worktree.SetupDone {
		t.Errorf("run = %s, setup_done %v", s.run.Status, s.run.Worktree.SetupDone)
	}
}

// Under test.sandbox: bwrap setup gets the test sandbox's mounts - the worktree's .git file and
// its git directories read-only - and test.env_pass, but the network; the test steps' runner
// keeps no network.
func TestRunSetup_SandboxedWithTheNetwork(t *testing.T) {
	s, _ := setupSession(t, "npm ci", "true")
	s.cfg.Test.Sandbox = config.SandboxBwrap
	execution.AllowTestEnv("ORCHTEST_SETUP_PASS")
	t.Setenv("ORCHTEST_SETUP_PASS", "yes")
	fake := &setupExecutor{}
	s.runner = testexec.New(fake)
	if err := enterWorktree(context.Background(), s); err != nil {
		t.Fatal(err)
	}

	if err := runSetup(context.Background(), s); err != nil {
		t.Fatal(err)
	}
	if len(fake.specs) != 1 || fake.specs[0].Executable != "bwrap" {
		t.Fatalf("setup ran %+v, want one bwrap call", fake.specs)
	}
	spec := fake.specs[0]
	args := spec.Args
	if slices.Contains(args, "--unshare-net") {
		t.Errorf("setup has no network: %q", args)
	}
	for _, f := range []string{"--unshare-user", "--unshare-pid", "--die-with-parent", "--new-session"} {
		if !slices.Contains(args, f) {
			t.Errorf("setup lost %s: %q", f, args)
		}
	}
	roBound := func(p string) bool {
		for i := 0; i+2 < len(args); i++ {
			if args[i] == "--ro-bind" && args[i+1] == p && args[i+2] == p {
				return true
			}
		}
		return false
	}
	files := s.readOnlyFiles()
	if len(files) != 1 || !roBound(files[0]) {
		t.Errorf("the .git file %q is not bound read-only: %q", files, args)
	}
	gitDirs, err := s.repo.GitDirs(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if len(gitDirs) != 2 {
		t.Fatalf("git dirs = %q, want the admin and the common directory", gitDirs)
	}
	for _, d := range gitDirs {
		if !roBound(d) {
			t.Errorf("git directory %s is not bound read-only: %q", d, args)
		}
	}
	if top, _ := s.repo.TopLevel(context.Background()); !slices.Contains(args, top) || args[len(args)-1] != "npm ci" {
		t.Errorf("setup does not run in the worktree %s: %q", top, args)
	}
	if !slices.Contains(spec.Env, "ORCHTEST_SETUP_PASS=yes") {
		t.Errorf("test.env_pass does not reach setup: %q", spec.Env)
	}
	if s.runner.Sandbox != nil {
		t.Errorf("the test steps' runner got setup's sandbox: %+v", s.runner.Sandbox)
	}
	if !s.run.Worktree.SetupDone {
		t.Errorf("worktree = %+v, want setup_done", s.run.Worktree)
	}
}

// Setup never runs on a tree a model touched, and never twice.
func TestRunSetup_OnlyForARunWithoutExecutionsAndSetupNotDone(t *testing.T) {
	s, _ := setupSession(t, "", "true")
	if err := enterWorktree(context.Background(), s); err != nil {
		t.Fatal(err)
	}
	marker := filepath.Join(s.repo.Dir, "marker")
	s.cfg.Worktree.Setup = "echo x >> marker"
	ran := func() bool { _, err := os.Stat(marker); return err == nil }

	// A resume of a run with one execution.
	s.run.Executions = []domain.StepExecution{{}}
	if err := runSetup(context.Background(), s); err != nil || ran() || s.run.Worktree.SetupDone {
		t.Fatalf("setup ran for a run with an execution: err %v, marker %v, worktree %+v", err, ran(), s.run.Worktree)
	}

	// A resume of a run with none and setup_done: false.
	s.run.Executions = nil
	if err := runSetup(context.Background(), s); err != nil || !ran() || !s.run.Worktree.SetupDone {
		t.Fatalf("setup did not run: err %v, marker %v, worktree %+v", err, ran(), s.run.Worktree)
	}

	// Done once.
	if err := runSetup(context.Background(), s); err != nil {
		t.Fatal(err)
	}
	if data, _ := os.ReadFile(marker); string(data) != "x\n" {
		t.Errorf("setup ran again: marker = %q", data)
	}
}

// Without worktree.setup, or in place, nothing runs and nothing is recorded.
func TestRunSetup_NothingToRun(t *testing.T) {
	s, _ := setupSession(t, "  ", "true")
	if err := enterWorktree(context.Background(), s); err != nil {
		t.Fatal(err)
	}
	if err := runSetup(context.Background(), s); err != nil || s.run.Worktree.SetupDone {
		t.Errorf("empty setup: err %v, worktree %+v", err, s.run.Worktree)
	}
	s.cfg.Worktree.Setup = "exit 1"
	s.worktree = nil
	if err := runSetup(context.Background(), s); err != nil {
		t.Errorf("setup ran in place: %v", err)
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

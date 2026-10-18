package app

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"orchestrator/internal/apperr"
	"orchestrator/internal/domain"
	"orchestrator/internal/execution"
	"orchestrator/internal/guard"
	"orchestrator/internal/repository"
)

// Resume in the worktree (specs/worktree/SPEC.md §11, TASK-WT07): the recorded worktree is
// verified before any git command runs in it, and the session then switches to it.

// recordingExecutor runs every process for real and records its Spec.
type recordingExecutor struct {
	inner execution.ProcessExecutor
	specs []execution.Spec
}

func (e *recordingExecutor) Run(ctx context.Context, spec execution.Spec) (execution.ProcessResult, error) {
	e.specs = append(e.specs, spec)
	return e.inner.Run(ctx, spec)
}

// noGitIn fails the test for any recorded process that ran in dir or below it.
func (e *recordingExecutor) noGitIn(t *testing.T, dir string) {
	t.Helper()
	for _, spec := range e.specs {
		if spec.WorkingDir == dir || strings.HasPrefix(spec.WorkingDir, dir+string(filepath.Separator)) {
			t.Errorf("%s %q ran in the worktree before it was verified", spec.Executable, spec.Args)
		}
	}
}

// resumableWorktreeRun is a worktree run whose worktree was created before it stopped, and the
// session a resume builds for it: over the operator's repository at dir, its executor recording
// every command, and not switched to the worktree.
func resumableWorktreeRun(t *testing.T) (s *session, dir string, rec *recordingExecutor) {
	t.Helper()
	s, dir, _ = worktreeSession(t, true, implementTest)
	if err := enterWorktree(context.Background(), s); err != nil {
		t.Fatal(err)
	}
	rec = &recordingExecutor{inner: execution.NewOSProcessExecutor()}
	operator := &repository.Repository{Dir: dir, Executor: rec}
	s.repo, s.dispatcher.Repo = operator, operator
	s.runsRel, s.dispatcher.RunsDir = ".orchestrator/runs", ".orchestrator/runs"
	s.worktree, s.operator, s.operatorFingerprint = nil, nil, ""
	s.run.Status = domain.RunFailed
	return s, dir, rec
}

// assertRefused checks that err is a RunStateError of kind naming want.
func assertRefused(t *testing.T, err error, kind apperr.RunStateKind, want string) {
	t.Helper()
	var rerr *apperr.RunStateError
	if !errors.As(err, &rerr) || rerr.Kind != kind || !strings.Contains(err.Error(), want) {
		t.Fatalf("got %T %v, want a %s RunStateError naming %q", err, err, kind, want)
	}
}

func TestVerifyRunWorktree_TheRunsOwnWorktree(t *testing.T) {
	s, _, rec := resumableWorktreeRun(t)
	w := s.run.Worktree

	wt, err := verifyRunWorktree(context.Background(), s.repo, s.run)
	if err != nil {
		t.Fatal(err)
	}
	if wt.Dir != w.Path || wt.Executor != s.repo.Executor {
		t.Errorf("repository = %+v, want the worktree %s", wt, w.Path)
	}
	rec.noGitIn(t, w.Path)
	listed := false
	for _, spec := range rec.specs {
		listed = listed || strings.Join(spec.Args, " ") == "worktree list --porcelain -z"
	}
	if !listed {
		t.Errorf("git worktree list did not run in the operator's repository: %+v", rec.specs)
	}
}

func TestVerifyRunWorktree_DirectoryRemoved(t *testing.T) {
	s, _, rec := resumableWorktreeRun(t)
	w := s.run.Worktree
	if err := os.RemoveAll(w.Path); err != nil {
		t.Fatal(err)
	}
	_, err := verifyRunWorktree(context.Background(), s.repo, s.run)
	assertRefused(t, err, apperr.RunNotResumable, "the run's worktree "+w.Path+" is gone; start a new run")
	rec.noGitIn(t, w.Path)
}

func TestVerifyRunWorktree_PointerRewritten(t *testing.T) {
	s, dir, rec := resumableWorktreeRun(t)
	w := s.run.Worktree
	other := filepath.Join(dir, ".git", "worktrees", "other")
	if err := os.WriteFile(filepath.Join(w.Path, ".git"), []byte("gitdir: "+other+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	_, err := verifyRunWorktree(context.Background(), s.repo, s.run)
	assertRefused(t, err, apperr.RunNotResumable, guard.PointerViolation)
	if !strings.Contains(err.Error(), other) {
		t.Errorf("the refusal does not name what the pointer says: %v", err)
	}
	rec.noGitIn(t, w.Path)
}

func TestVerifyRunWorktree_PointerUnreadable(t *testing.T) {
	s, _, rec := resumableWorktreeRun(t)
	w := s.run.Worktree
	if err := os.WriteFile(filepath.Join(w.Path, ".git"), []byte("not a pointer\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	_, err := verifyRunWorktree(context.Background(), s.repo, s.run)
	assertRefused(t, err, apperr.RunNotResumable, guard.PointerViolation)
	rec.noGitIn(t, w.Path)
}

// A path recorded differently from the derived one is refused before the directory, the
// pointer or git are looked at: here it names a copy of the real worktree.
func TestVerifyRunWorktree_PathRecordedDifferently(t *testing.T) {
	s, _, rec := resumableWorktreeRun(t)
	w := s.run.Worktree
	elsewhere := filepath.Join(t.TempDir(), s.run.ID)
	if err := os.MkdirAll(elsewhere, 0o700); err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(filepath.Join(w.Path, ".git"))
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(elsewhere, ".git"), data, 0o644); err != nil {
		t.Fatal(err)
	}
	derived := w.Path
	w.Path = elsewhere

	_, err = verifyRunWorktree(context.Background(), s.repo, s.run)
	assertRefused(t, err, apperr.RunCorrupt, "worktree.path is "+elsewhere)
	if !strings.Contains(err.Error(), derived) {
		t.Errorf("the refusal does not name the derived path %s: %v", derived, err)
	}
	rec.noGitIn(t, elsewhere)
	rec.noGitIn(t, derived)
	if len(rec.specs) != 1 || !strings.Contains(strings.Join(rec.specs[0].Args, " "), "--git-common-dir") {
		t.Errorf("git ran beyond the common directory lookup: %+v", rec.specs)
	}
}

func TestVerifyRunWorktree_OnAnotherBranch(t *testing.T) {
	s, _, rec := resumableWorktreeRun(t)
	w := s.run.Worktree
	gitIn(t, w.Path, "switch", "-q", "-c", "other")

	_, err := verifyRunWorktree(context.Background(), s.repo, s.run)
	assertRefused(t, err, apperr.RunNotResumable, "is on refs/heads/other, not refs/heads/"+w.Branch)
	rec.noGitIn(t, w.Path)
}

func TestVerifyRunWorktree_Detached(t *testing.T) {
	s, _, rec := resumableWorktreeRun(t)
	w := s.run.Worktree
	gitIn(t, w.Path, "switch", "-q", "--detach")

	_, err := verifyRunWorktree(context.Background(), s.repo, s.run)
	assertRefused(t, err, apperr.RunNotResumable, "is on a detached HEAD")
	rec.noGitIn(t, w.Path)
}

// git lists the admin directory's worktree where its gitdir file says: pointed elsewhere, the
// run's path is not listed.
func TestVerifyRunWorktree_NotListed(t *testing.T) {
	s, _, rec := resumableWorktreeRun(t)
	w := s.run.Worktree
	if err := os.WriteFile(filepath.Join(w.AdminDir, "gitdir"), []byte(filepath.Join(t.TempDir(), "x", ".git")+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	_, err := verifyRunWorktree(context.Background(), s.repo, s.run)
	assertRefused(t, err, apperr.RunNotResumable, "git does not list the run's worktree "+w.Path)
	rec.noGitIn(t, w.Path)
}

// An admin directory outside <common>/worktrees/ is refused even when the pointer agrees.
func TestVerifyRunWorktree_AdminDirOutsideTheCommonDir(t *testing.T) {
	s, _, rec := resumableWorktreeRun(t)
	w := s.run.Worktree
	foreign := filepath.Join(t.TempDir(), "worktrees", "x")
	if err := os.WriteFile(filepath.Join(w.Path, ".git"), []byte("gitdir: "+foreign+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	w.AdminDir = foreign

	_, err := verifyRunWorktree(context.Background(), s.repo, s.run)
	assertRefused(t, err, apperr.RunCorrupt, "worktree.admin_dir "+foreign)
	rec.noGitIn(t, w.Path)
}

func TestVerifyRunWorktree_Removed(t *testing.T) {
	s, _, rec := resumableWorktreeRun(t)
	w := s.run.Worktree
	w.Removed = true

	_, err := verifyRunWorktree(context.Background(), s.repo, s.run)
	assertRefused(t, err, apperr.RunNotResumable, "was removed; its branch "+w.Branch+" stays")
	if len(rec.specs) != 0 {
		t.Errorf("git ran: %+v", rec.specs)
	}
}

// Under a symlinked $XDG_STATE_HOME git may spell the worktree's path resolved; the run's own
// worktree still verifies.
func TestVerifyRunWorktree_SymlinkedStateDir(t *testing.T) {
	s, dir, _ := worktreeSession(t, true, implementTest)
	link := filepath.Join(t.TempDir(), "state")
	if err := os.Symlink(t.TempDir(), link); err != nil {
		t.Fatal(err)
	}
	t.Setenv("XDG_STATE_HOME", link)
	operator := s.repo
	if err := enterWorktree(context.Background(), s); err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(s.run.Worktree.Path, link) {
		t.Fatalf("worktree path %s is not under the symlinked state directory %s", s.run.Worktree.Path, link)
	}
	if _, err := verifyRunWorktree(context.Background(), operator, s.run); err != nil {
		t.Fatalf("the run's own worktree is refused: %v (git lists:\n%s)", err, gitIn(t, dir, "worktree", "list", "--porcelain"))
	}
}

// A verified worktree becomes the session's repository as for a fresh run, with the operator's
// fingerprint for the conflict check; enterWorktree then creates no second worktree.
func TestResumeInWorktree_SwitchesTheSession(t *testing.T) {
	s, dir, rec := resumableWorktreeRun(t)
	operator := s.repo
	w := s.run.Worktree

	if err := resumeInWorktree(context.Background(), s); err != nil {
		t.Fatal(err)
	}
	if s.repo.Dir != w.Path || s.dispatcher.Repo != s.repo || s.operator != operator || s.worktree != w {
		t.Errorf("the session did not switch: repo %s, dispatcher %s, operator %v, worktree %+v", s.repo.Dir, s.dispatcher.Repo.Dir, s.operator, s.worktree)
	}
	if s.runsRel != "" || s.dispatcher.RunsDir != "" {
		t.Errorf("runs directory still guarded in the worktree: %q / %q", s.runsRel, s.dispatcher.RunsDir)
	}
	if want, err := operator.GitFingerprint(context.Background()); err != nil || s.operatorFingerprint != want {
		t.Errorf("operator fingerprint = %q, want %q (%v)", s.operatorFingerprint, want, err)
	}
	rec.noGitIn(t, w.Path)

	if err := enterWorktree(context.Background(), s); err != nil {
		t.Fatal(err)
	}
	if list := gitIn(t, dir, "worktree", "list", "--porcelain"); strings.Count(list, "worktree ") != 2 {
		t.Errorf("worktrees:\n%s\nwant the operator's and the run's", list)
	}
	if s.repo.Dir != w.Path || s.run.Worktree != w {
		t.Errorf("enterWorktree replaced the worktree: %s, %+v", s.repo.Dir, s.run.Worktree)
	}
}

// A refused verification leaves the session in the operator's repository.
func TestResumeInWorktree_RefusedLeavesTheSession(t *testing.T) {
	s, _, _ := resumableWorktreeRun(t)
	operator := s.repo
	if err := os.RemoveAll(s.run.Worktree.Path); err != nil {
		t.Fatal(err)
	}
	if err := resumeInWorktree(context.Background(), s); err == nil {
		t.Fatal("a resume into a gone worktree was accepted")
	}
	if s.repo != operator || s.worktree != nil || s.operatorFingerprint != "" {
		t.Errorf("the session switched: repo %s, worktree %+v", s.repo.Dir, s.worktree)
	}
}

// Once the run-start baseline is recorded, a step may have started: setup never runs again.
func TestRunSetup_NotAfterTheBaseline(t *testing.T) {
	s, _ := setupSession(t, "", "true")
	if err := enterWorktree(context.Background(), s); err != nil {
		t.Fatal(err)
	}
	marker := filepath.Join(s.repo.Dir, "marker")
	s.cfg.Worktree.Setup = "echo x >> marker"
	s.run.ResumeBaseline = json.RawMessage(`{}`)
	if err := runSetup(context.Background(), s); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(marker); err == nil || s.run.Worktree.SetupDone {
		t.Errorf("setup ran over a recorded baseline: setup_done %v", s.run.Worktree.SetupDone)
	}
}

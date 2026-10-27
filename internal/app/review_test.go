package app

import (
	"bytes"
	"context"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"orchestrator/internal/apperr"
	"orchestrator/internal/domain"
	"orchestrator/internal/exitcode"
	"orchestrator/internal/persistence"
)

func TestParseArgs_ReviewFlags(t *testing.T) {
	cases := []struct {
		name    string
		args    []string
		want    cliArgs
		wantErr string
	}{
		{"base only", []string{"review", "--base", "main"},
			cliArgs{configPath: defaultConfigPath, base: "main", rest: []string{"review"}}, ""},
		{"all flags", []string{"--mode=auto", "review", "--base=v1.3.0", "--head", "feature/x", "--task", "no API change"},
			cliArgs{configPath: defaultConfigPath, mode: domain.ModeAuto, base: "v1.3.0", head: "feature/x", reviewTask: "no API change", rest: []string{"review"}}, ""},
		{"jira", []string{"review", "--base", "main", "--jira", "PAY-12"},
			cliArgs{configPath: defaultConfigPath, base: "main", jiraRef: "PAY-12", rest: []string{"review"}}, ""},
		{"base twice", []string{"review", "--base", "a", "--base=b"}, cliArgs{}, "--base given twice"},
		{"head twice", []string{"review", "--head", "a", "--head=b"}, cliArgs{}, "--head given twice"},
		{"task twice", []string{"review", "--task", "a", "--task=b"}, cliArgs{}, "--task given twice"},
		{"base missing value", []string{"review", "--base"}, cliArgs{}, "--base requires a value"},
		{"base empty", []string{"review", "--base="}, cliArgs{}, "--base requires a ref"},
		{"head empty", []string{"review", "--base", "main", "--head="}, cliArgs{}, "--head requires a ref"},
		{"task blank", []string{"review", "--base", "main", "--task", "  "}, cliArgs{}, "--task requires the text of the task"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, err := parseArgs(tc.args)
			if tc.wantErr != "" {
				if err == nil || !strings.Contains(err.Error(), tc.wantErr) {
					t.Fatalf("got %v, want an error containing %q", err, tc.wantErr)
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			if got.configPath != tc.want.configPath || got.mode != tc.want.mode || got.base != tc.want.base || got.head != tc.want.head ||
				got.reviewTask != tc.want.reviewTask || got.jiraRef != tc.want.jiraRef || !slices.Equal(got.rest, tc.want.rest) {
				t.Errorf("got %+v, want %+v", got, tc.want)
			}
		})
	}
}

// The flag errors of review are usage errors (exit 2) before a config is read or anything else
// happens.
func TestRun_ReviewFlagErrors(t *testing.T) {
	for _, tc := range []struct {
		name string
		args []string
		want string
	}{
		{"no --base", []string{"review"}, "review requires --base"},
		{"--head without --base", []string{"review", "--head", "feature"}, "--head requires --base"},
		{"--task with --jira", []string{"review", "--base", "main", "--task", "x", "--jira", "PAY-1"}, "--task cannot be combined with --jira"},
		{"text", []string{"review", "--base", "main", "some", "text"}, "usage: orchestrator review"},
		{"--stage", []string{"review", "--base", "main", "--stage", "review"}, "--stage is only valid with run"},
		{"--inplace", []string{"review", "--base", "main", "--inplace"}, "--inplace is only valid with run"},
		{"--worktree", []string{"review", "--base", "main", "--worktree"}, "--worktree is only valid with run"},
		{"--allow-red-tests", []string{"review", "--base", "main", "--allow-red-tests"}, "--allow-red-tests is only valid with run"},
		{"--base on run", []string{"run", "--base", "main", "x"}, "--base is only valid with review"},
		{"--head on resume", []string{"resume", "--head", "main", "3f2a9c1e"}, "--head is only valid with review"},
		{"--task on run", []string{"run", "--task", "x", "y"}, "--task is only valid with review"},
		{"base with a newline", []string{"review", "--base", "ma\nin"}, "--base has a non-printable character"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var out bytes.Buffer
			args := append([]string{"--config", "/nonexistent.yaml"}, tc.args...)
			err := Run(context.Background(), args, strings.NewReader(""), &out, &out)
			var cerr *apperr.ConfigError
			if !errors.As(err, &cerr) || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("got %v, want a usage error containing %q", err, tc.want)
			}
			if exitcode.FromError(err) != exitcode.ConfigOrDependency {
				t.Errorf("exit code = %d, want %d", exitcode.FromError(err), exitcode.ConfigOrDependency)
			}
			if out.Len() != 0 {
				t.Errorf("printed %q", out.String())
			}
		})
	}
}

// execGit runs git in dir and returns its output and error, for a command that may fail.
func execGit(dir string, args ...string) (string, error) {
	cmd := exec.Command("git", args...)
	cmd.Dir = dir
	out, err := cmd.CombinedOutput()
	return strings.TrimSpace(string(out)), err
}

// reviewRepo is a repository whose main moved on after feature forked from it, and a session of a
// review run of feature against main whose worktree has not been made yet.
func reviewRepo(t *testing.T) (s *session, dir string, target domain.ReviewTarget) {
	t.Helper()
	wf := domain.Workflow{Steps: []domain.Step{{Action: domain.ActionTest}, {Action: domain.ActionReview}}}
	s, dir, _ = worktreeSession(t, true, wf)
	fork := gitIn(t, dir, "rev-parse", "HEAD")
	write := func(name, content string) {
		if err := os.WriteFile(filepath.Join(dir, name), []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	gitIn(t, dir, "checkout", "-qb", "feature")
	write("feature.go", "package main\n")
	gitIn(t, dir, "add", "-A")
	gitIn(t, dir, "commit", "-qm", "Add the feature")
	write("feature.go", "package main\n\nvar X = 1\n")
	gitIn(t, dir, "commit", "-qam", "Use X")
	feature := gitIn(t, dir, "rev-parse", "HEAD")
	gitIn(t, dir, "checkout", "-q", "main")
	write("main_only.go", "package main\n")
	gitIn(t, dir, "add", "-A")
	gitIn(t, dir, "commit", "-qm", "Change main after the fork")

	target = domain.ReviewTarget{Base: gitIn(t, dir, "rev-parse", "main"), Head: feature, MergeBase: fork, BaseRefName: "main", HeadRefName: "feature"}
	return s, dir, target
}

func TestResolveReviewTarget(t *testing.T) {
	s, _, want := reviewRepo(t)
	ctx := context.Background()

	got, err := resolveReviewTarget(ctx, s.repo, "main", "feature")
	if err != nil || got != want {
		t.Fatalf("resolveReviewTarget = %+v, %v; want %+v", got, err, want)
	}

	for _, tc := range []struct{ name, base, head, want string }{
		{"base is no commit", "nope", "feature", `--base "nope" is not a commit`},
		{"head is no commit", "main", "nope", `--head "nope" is not a commit`},
		{"base is an option", "--all", "feature", `--base "--all" is not a commit`},
		{"head is behind base", "main", "main~1", "nothing to review: main~1 adds nothing to main"},
		{"same ref", "main", "main", "nothing to review: main adds nothing to main"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			_, err := resolveReviewTarget(ctx, s.repo, tc.base, tc.head)
			var cerr *apperr.ConfigError
			if !errors.As(err, &cerr) || !strings.Contains(err.Error(), tc.want) {
				t.Errorf("got %T %v, want a ConfigError containing %q", err, err, tc.want)
			}
		})
	}
}

func TestReviewTaskFromCommits(t *testing.T) {
	s, dir, target := reviewRepo(t)
	ctx := context.Background()

	got, err := reviewTaskFromCommits(ctx, s.repo, target)
	if want := "Review the change from main to feature. Its commits:\n- Use X\n- Add the feature"; err != nil || got != want {
		t.Errorf("task = %q, %v; want %q", got, err, want)
	}

	// More commits than the task lists are counted, not dropped silently.
	gitIn(t, dir, "checkout", "-q", "feature")
	for i := 0; i < maxReviewSubjects; i++ {
		gitIn(t, dir, "commit", "-q", "--allow-empty", "-m", "Empty commit")
	}
	target.Head = gitIn(t, dir, "rev-parse", "HEAD")
	got, err = reviewTaskFromCommits(ctx, s.repo, target)
	if err != nil {
		t.Fatal(err)
	}
	if n := strings.Count(got, "\n- "); n != maxReviewSubjects+1 || !strings.HasSuffix(got, "\n- …and 2 more") {
		t.Errorf("%d lines, ending %q; want %d lines ending in …and 2 more", n, got[max(0, len(got)-30):], maxReviewSubjects+1)
	}
}

func TestReviewTaskFromCommits_IsBoundedInBytes(t *testing.T) {
	s, dir, target := reviewRepo(t)
	gitIn(t, dir, "checkout", "-q", "feature")
	for i := 0; i < 40; i++ {
		gitIn(t, dir, "commit", "-q", "--allow-empty", "-m", strings.Repeat("long subject ", 40))
	}
	target.Head = gitIn(t, dir, "rev-parse", "HEAD")
	got, err := reviewTaskFromCommits(context.Background(), s.repo, target)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) > maxReviewTaskBytes+64 {
		t.Errorf("the task is %d bytes, want about %d at most", len(got), maxReviewTaskBytes)
	}
	if !strings.Contains(got, "more") {
		t.Errorf("the cut commits are not counted: ...%q", got[len(got)-40:])
	}
}

// The worktree of a review run is detached at head, has no branch, and leaves the operator's
// tree alone, dirty file included; the record it leaves loads and verifies; it goes at the end of
// a run that completed or failed, and stays with a stopped one.
func TestReviewWorktree_DetachedVerifiedAndRemoved(t *testing.T) {
	s, dir, target := reviewRepo(t)
	ctx := context.Background()
	s.run.ReviewTarget = &target
	if err := os.WriteFile(filepath.Join(dir, "dirty.txt"), []byte("not reviewed\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	before := gitIn(t, dir, "status", "--porcelain")
	operator := s.repo

	if err := enterWorktree(ctx, s); err != nil {
		t.Fatal(err)
	}
	w := s.run.Worktree
	if w == nil || s.worktree != w || s.operator != operator {
		t.Fatalf("worktree = %+v, session worktree = %+v", w, s.worktree)
	}
	if w.Branch != "" || w.BaseBranch != "" || w.BaseRef != target.Head {
		t.Errorf("worktree = %+v, want detached at %s", w, target.Head)
	}
	if got := gitIn(t, w.Path, "rev-parse", "HEAD"); got != target.Head {
		t.Errorf("the worktree is at %s, want %s", got, target.Head)
	}
	if out, err := execGit(w.Path, "symbolic-ref", "-q", "HEAD"); err == nil {
		t.Errorf("the worktree is on a branch: %s", out)
	}
	if _, err := os.Stat(filepath.Join(w.Path, "main_only.go")); !os.IsNotExist(err) {
		t.Errorf("the worktree has a file of main, which came after the fork: %v", err)
	}
	if _, err := os.Stat(filepath.Join(w.Path, "dirty.txt")); !os.IsNotExist(err) {
		t.Errorf("the worktree has the operator's dirty file: %v", err)
	}
	if after := gitIn(t, dir, "status", "--porcelain"); after != before {
		t.Errorf("the operator's tree changed:\n%s\nwas:\n%s", after, before)
	}
	if got := gitIn(t, dir, "rev-parse", "--abbrev-ref", "HEAD"); got != "main" {
		t.Errorf("the operator's branch = %s", got)
	}

	// What loads and verifies is what resume continues in.
	saved, err := s.store.LoadRun(s.run.ID)
	if err != nil {
		t.Fatalf("run.json of a review run does not load: %v", err)
	}
	if saved.ReviewTarget == nil || *saved.ReviewTarget != target || saved.Worktree == nil || saved.Worktree.Branch != "" {
		t.Errorf("saved run = %+v / %+v", saved.ReviewTarget, saved.Worktree)
	}
	wt, err := verifyRunWorktree(ctx, operator, saved)
	if err != nil || wt.Dir != w.Path {
		t.Fatalf("verifyRunWorktree = %v, %v", wt, err)
	}
	other := *saved
	moved := target
	moved.Head = target.MergeBase
	other.ReviewTarget = &moved
	if _, err := verifyRunWorktree(ctx, operator, &other); err == nil || !strings.Contains(err.Error(), "not at "+moved.Head) {
		t.Errorf("a worktree at another commit verified: %v", err)
	}

	// A stopped run keeps its worktree for resume.
	s.run.Status = domain.RunStopped
	endReviewWorktree(ctx, s)
	if w.Removed {
		t.Error("a stopped review run lost its worktree")
	}
	if _, err := os.Stat(w.Path); err != nil {
		t.Errorf("the worktree of a stopped run is gone: %v", err)
	}

	// A completed one removes it, and a resume then says so.
	s.run.Status = domain.RunCompleted
	endReviewWorktree(ctx, s)
	if !w.Removed {
		t.Error("the worktree was not recorded as removed")
	}
	if _, err := os.Stat(w.Path); !os.IsNotExist(err) {
		t.Errorf("the worktree is still there: %v", err)
	}
	if _, err := verifyRunWorktree(ctx, operator, s.run); err == nil || !strings.Contains(err.Error(), "was removed") {
		t.Errorf("verifyRunWorktree of a removed worktree = %v", err)
	}
	if saved, err := s.store.LoadRun(s.run.ID); err != nil || !saved.Worktree.Removed {
		t.Errorf("the removal is not in run.json: %v", err)
	}
}

// A removal git refuses (something a test left in the worktree) keeps the worktree and says where.
func TestEndReviewWorktree_KeepsWhatGitRefusesToRemove(t *testing.T) {
	s, _, target := reviewRepo(t)
	ctx := context.Background()
	s.run.ReviewTarget = &target
	if err := enterWorktree(ctx, s); err != nil {
		t.Fatal(err)
	}
	w := s.run.Worktree
	if err := os.WriteFile(filepath.Join(w.Path, "left-by-a-test.txt"), []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	s.run.Status = domain.RunFailed
	endReviewWorktree(ctx, s)
	if w.Removed {
		t.Error("a worktree git refused to remove is recorded as removed")
	}
	if _, err := os.Stat(w.Path); err != nil {
		t.Errorf("the worktree is gone: %v", err)
	}
}

func TestReviewVerdictError(t *testing.T) {
	store := persistence.NewStore(t.TempDir())
	for _, tc := range []struct {
		name    string
		status  domain.RunStatus
		verdict domain.ReviewStatus
		wantErr bool
	}{
		{"completed, failing verdict", domain.RunCompleted, domain.ReviewFail, true},
		{"completed, passing verdict", domain.RunCompleted, domain.ReviewPass, false},
		{"failed run", domain.RunFailed, domain.ReviewFail, false},
		{"no verdict", domain.RunCompleted, "", false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			run := &domain.Run{ID: "20261023-101500-ab12cd", Status: tc.status, Outcome: &domain.Outcome{Status: tc.status, ReviewVerdict: tc.verdict}}
			err := reviewVerdictError(run, store)
			if (err != nil) != tc.wantErr {
				t.Fatalf("reviewVerdictError = %v, want an error: %v", err, tc.wantErr)
			}
			if tc.wantErr && exitcode.FromError(err) != exitcode.NeedsPerson {
				t.Errorf("exit code = %d, want %d", exitcode.FromError(err), exitcode.NeedsPerson)
			}
		})
	}
	if err := reviewVerdictError(&domain.Run{Status: domain.RunCompleted}, store); err != nil {
		t.Errorf("a run without an outcome = %v", err)
	}
}

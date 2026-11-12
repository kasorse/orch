package app

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/kasorse/orch/internal/domain"
	"github.com/kasorse/orch/internal/repository"
)

// reviseSession is a worktree session over a repository whose origin (a bare repository) has
// main and the merge request's branch fix/x, one commit ahead; the local fix/x is deleted. The
// run's task is a revise update of that branch.
func reviseSession(t *testing.T) (s *session, dir, mainSHA, fixSHA string, out *bytes.Buffer) {
	t.Helper()
	s, dir, out = worktreeSession(t, true, implementTest)
	mainSHA = gitIn(t, dir, "rev-parse", "HEAD")
	gitIn(t, dir, "push", "-q", "origin", "main")
	gitIn(t, dir, "checkout", "-q", "-b", "fix/x")
	if err := os.WriteFile(filepath.Join(dir, "first.go"), []byte("package main\n// the merge request's first commit\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	gitIn(t, dir, "add", "-A")
	gitIn(t, dir, "commit", "-qm", "first")
	fixSHA = gitIn(t, dir, "rev-parse", "HEAD")
	gitIn(t, dir, "push", "-q", "origin", "fix/x")
	gitIn(t, dir, "checkout", "-q", "main")
	gitIn(t, dir, "branch", "-q", "-D", "fix/x")

	s.run.Task.Source = &domain.TaskSource{Kind: domain.TaskSourceRevise, Revise: &domain.ReviseSource{
		Target: "fix/x", TargetKind: domain.ReviseTargetBranch, Project: "acme/billing", Mode: domain.ReviseUpdate,
		Branch: "fix/x", BaseRef: fixSHA, BaseBranch: "main",
	}}
	return s, dir, mainSHA, fixSHA, out
}

func TestOpenWorktree_ReviseUpdateStartsFromTheMergeRequestsBranch(t *testing.T) {
	s, dir, mainSHA, fixSHA, _ := reviseSession(t)
	refsBefore := gitIn(t, dir, "for-each-ref", "--format=%(refname) %(objectname)", "refs/heads", "refs/remotes")

	wt, err := openWorktree(context.Background(), s)
	if err != nil {
		t.Fatal(err)
	}
	w := s.run.Worktree
	if w.Branch != domain.ReviseBranch("fix/x", s.run.ID) || w.BaseRef != fixSHA || w.BaseBranch != "main" || !w.ExistingBranch {
		t.Fatalf("worktree = %+v", w)
	}
	if s.run.ReviewBase != mainSHA || s.run.BranchNaming.Source != domain.BranchSourceExisting {
		t.Fatalf("review_base = %s, naming = %+v", s.run.ReviewBase, s.run.BranchNaming)
	}
	if got := gitIn(t, wt.Dir, "rev-parse", "HEAD"); got != fixSHA {
		t.Fatalf("the worktree is at %s, want %s", got, fixSHA)
	}
	if _, err := os.Stat(filepath.Join(wt.Dir, "first.go")); err != nil {
		t.Fatalf("the merge request's file is missing: %v", err)
	}
	// Only the run's branch (created by the worktree) and refs/orchestrator/ are new.
	refsAfter := gitIn(t, dir, "for-each-ref", "--format=%(refname) %(objectname)", "refs/heads", "refs/remotes")
	if want := strings.Split(refsBefore+"\nrefs/heads/"+w.Branch+" "+fixSHA, "\n"); !sameLines(strings.Split(refsAfter, "\n"), want) {
		t.Errorf("refs changed:\nbefore\n%s\nafter\n%s", refsBefore, refsAfter)
	}
	if got := gitIn(t, dir, "rev-parse", repository.ReviseRef(s.run.ID, "-target")); got != mainSHA {
		t.Errorf("target ref = %s", got)
	}
	if gitIn(t, dir, "symbolic-ref", "--short", "HEAD") != "main" || gitIn(t, dir, "status", "--porcelain") != "" {
		t.Error("the operator's checkout changed")
	}
	if _, err := s.store.LoadRun(s.run.ID); err != nil {
		t.Fatalf("run.json does not load: %v", err)
	}
}

func sameLines(a, b []string) bool {
	a, b = slices.Clone(a), slices.Clone(b)
	slices.Sort(a)
	slices.Sort(b)
	return slices.Equal(a, b)
}

func TestOpenWorktree_ReviseLocalBranch(t *testing.T) {
	t.Run("behind: the fetched tip, the local branch untouched", func(t *testing.T) {
		s, dir, mainSHA, fixSHA, _ := reviseSession(t)
		gitIn(t, dir, "branch", "fix/x", mainSHA)
		if _, err := openWorktree(context.Background(), s); err != nil {
			t.Fatal(err)
		}
		if s.run.Worktree.BaseRef != fixSHA || gitIn(t, dir, "rev-parse", "fix/x") != mainSHA {
			t.Fatalf("base = %s, fix/x = %s", s.run.Worktree.BaseRef, gitIn(t, dir, "rev-parse", "fix/x"))
		}
	})
	t.Run("ahead and checked out: the local tip", func(t *testing.T) {
		s, dir, _, fixSHA, _ := reviseSession(t)
		gitIn(t, dir, "checkout", "-q", "-b", "fix/x", fixSHA)
		mine := commitFile(t, dir, "mine.go")
		if _, err := openWorktree(context.Background(), s); err != nil {
			t.Fatal(err)
		}
		if s.run.Worktree.BaseRef != mine {
			t.Fatalf("base = %s, want the local tip %s", s.run.Worktree.BaseRef, mine)
		}
		if gitIn(t, dir, "symbolic-ref", "--short", "HEAD") != "fix/x" || gitIn(t, dir, "rev-parse", "HEAD") != mine {
			t.Error("the operator's checkout changed")
		}
	})
}

// commitFile commits a new file name in dir and returns the commit.
func commitFile(t *testing.T, dir, name string) string {
	t.Helper()
	if err := os.WriteFile(filepath.Join(dir, name), []byte("package main\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	gitIn(t, dir, "add", "-A")
	gitIn(t, dir, "commit", "-qm", name)
	return gitIn(t, dir, "rev-parse", "HEAD")
}

func TestOpenWorktree_ReviseRefusals(t *testing.T) {
	t.Run("diverged", func(t *testing.T) {
		s, dir, mainSHA, _, _ := reviseSession(t)
		gitIn(t, dir, "checkout", "-q", "-b", "fix/x", mainSHA)
		commitFile(t, dir, "mine.go")
		gitIn(t, dir, "checkout", "-q", "main")
		if _, err := openWorktree(context.Background(), s); err == nil || !strings.Contains(err.Error(), "have diverged") {
			t.Fatalf("err = %v", err)
		}
	})
	t.Run("moved", func(t *testing.T) {
		s, _, mainSHA, _, _ := reviseSession(t)
		s.run.Task.Source.Revise.BaseRef = mainSHA
		if _, err := openWorktree(context.Background(), s); err == nil || !strings.Contains(err.Error(), "somebody pushed") {
			t.Fatalf("err = %v", err)
		}
		if s.run.Worktree != nil {
			t.Fatal("a worktree was recorded")
		}
	})
}

func TestOpenWorktree_ReviseNewStartsFromTheTarget(t *testing.T) {
	s, dir, mainSHA, _, _ := reviseSession(t)
	// The operator's main moves on locally; the new branch starts from the remote's main.
	if err := os.WriteFile(filepath.Join(dir, "local.go"), []byte("package main\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	gitIn(t, dir, "add", "-A")
	gitIn(t, dir, "commit", "-qm", "local only")
	r := s.run.Task.Source.Revise
	r.Mode, r.Branch, r.BaseRef = domain.ReviseNew, "", ""
	r.MR = &domain.MergeRequest{Project: "acme/billing", IID: 3, URL: "https://gitlab.test/acme/billing/-/merge_requests/3",
		State: domain.MRStateMerged, SourceBranch: "fix/x", TargetBranch: "main", SHA: mainSHA, Title: "x"}
	if _, err := openWorktree(context.Background(), s); err != nil {
		t.Fatal(err)
	}
	w := s.run.Worktree
	if w.BaseRef != mainSHA || w.BaseBranch != "main" || w.ExistingBranch || w.Branch == "fix/x" || s.run.ReviewBase != "" {
		t.Fatalf("worktree = %+v, review_base %q", w, s.run.ReviewBase)
	}
}

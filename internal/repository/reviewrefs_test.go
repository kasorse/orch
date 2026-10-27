package repository

import (
	"context"
	"path/filepath"
	"slices"
	"strings"
	"testing"
)

// forkedRepo is a repository whose main (the initial branch, renamed) moved on after feature
// forked from it: feature has two commits, main one more.
func forkedRepo(t *testing.T) (repo *Repository, dir, fork, feature string) {
	t.Helper()
	repo, dir = realRepo(t)
	gitCmd(t, dir, "branch", "-M", "main")
	fork = gitOut(t, dir, "rev-parse", "HEAD")

	gitCmd(t, dir, "checkout", "-qb", "feature")
	writeFile(t, filepath.Join(dir, "feature.txt"), "one\n")
	gitCmd(t, dir, "add", "-A")
	gitCmd(t, dir, "commit", "-qm", "Add the feature file")
	writeFile(t, filepath.Join(dir, "feature.txt"), "one\ntwo\n")
	gitCmd(t, dir, "commit", "-qam", "Extend the feature file")
	feature = gitOut(t, dir, "rev-parse", "HEAD")

	gitCmd(t, dir, "checkout", "-q", "main")
	writeFile(t, filepath.Join(dir, "main-only.txt"), "main\n")
	gitCmd(t, dir, "add", "-A")
	gitCmd(t, dir, "commit", "-qm", "Change main after the fork")
	return repo, dir, fork, feature
}

func TestResolveCommit(t *testing.T) {
	repo, dir, _, feature := forkedRepo(t)
	ctx := context.Background()

	for _, ref := range []string{"feature", "refs/heads/feature", feature} {
		if sha, ok, err := repo.ResolveCommit(ctx, ref); err != nil || !ok || sha != feature {
			t.Errorf("ResolveCommit(%q) = %q, %v, %v; want %s", ref, sha, ok, err, feature)
		}
	}
	if sha, ok, err := repo.ResolveCommit(ctx, "main"); err != nil || !ok || sha != gitOut(t, dir, "rev-parse", "HEAD") {
		t.Errorf("ResolveCommit(main) = %q, %v, %v", sha, ok, err)
	}
	// Not a commit, and an option: both are a plain "no", never an option for git.
	for _, ref := range []string{"nope", "--all", "-h", "", "feature:feature.txt"} {
		if sha, ok, err := repo.ResolveCommit(ctx, ref); err != nil || ok {
			t.Errorf("ResolveCommit(%q) = %q, %v, %v; want not found", ref, sha, ok, err)
		}
	}
}

func TestMergeBaseAndSameTree(t *testing.T) {
	repo, dir, fork, feature := forkedRepo(t)
	ctx := context.Background()
	mainSHA := gitOut(t, dir, "rev-parse", "main")

	got, ok, err := repo.MergeBase(ctx, mainSHA, feature)
	if err != nil || !ok || got != fork {
		t.Fatalf("MergeBase = %q, %v, %v; want the fork %s", got, ok, err, fork)
	}
	if same, err := repo.SameTree(ctx, fork, feature); err != nil || same {
		t.Errorf("SameTree(fork, feature) = %v, %v; want false", same, err)
	}
	if same, err := repo.SameTree(ctx, feature, feature); err != nil || !same {
		t.Errorf("SameTree(feature, feature) = %v, %v; want true", same, err)
	}
	// An empty commit changes nothing.
	gitCmd(t, dir, "checkout", "-q", "feature")
	gitCmd(t, dir, "commit", "-q", "--allow-empty", "-m", "Nothing")
	empty := gitOut(t, dir, "rev-parse", "HEAD")
	if same, err := repo.SameTree(ctx, feature, empty); err != nil || !same {
		t.Errorf("SameTree(feature, empty commit) = %v, %v; want true", same, err)
	}

	// Unrelated histories have no merge base.
	gitCmd(t, dir, "checkout", "-q", "--orphan", "other")
	gitCmd(t, dir, "commit", "-q", "--allow-empty", "-m", "Root")
	other := gitOut(t, dir, "rev-parse", "HEAD")
	if got, ok, err := repo.MergeBase(ctx, other, feature); err != nil || ok {
		t.Errorf("MergeBase of unrelated commits = %q, %v, %v; want none", got, ok, err)
	}
	if _, _, err := repo.MergeBase(ctx, "main", feature); err == nil {
		t.Error("MergeBase must refuse a ref that is not a full SHA")
	}
}

func TestCommitSubjects(t *testing.T) {
	repo, _, fork, feature := forkedRepo(t)
	ctx := context.Background()

	got, more, err := repo.CommitSubjects(ctx, fork, feature, 50)
	if err != nil || more != 0 || !slices.Equal(got, []string{"Extend the feature file", "Add the feature file"}) {
		t.Errorf("CommitSubjects = %q, %d, %v", got, more, err)
	}
	got, more, err = repo.CommitSubjects(ctx, fork, feature, 1)
	if err != nil || more != 1 || !slices.Equal(got, []string{"Extend the feature file"}) {
		t.Errorf("CommitSubjects(limit 1) = %q, %d, %v", got, more, err)
	}
	if _, _, err := repo.CommitSubjects(ctx, "main", feature, 5); err == nil {
		t.Error("CommitSubjects must refuse a ref that is not a full SHA")
	}
}

func TestAddDetachedWorktree(t *testing.T) {
	repo, dir, _, feature := forkedRepo(t)
	ctx := context.Background()
	path := filepath.Join(t.TempDir(), "review")

	admin, err := repo.AddDetachedWorktree(ctx, path, feature)
	if err != nil {
		t.Fatal(err)
	}
	if got, err := ReadGitPointer(path); err != nil || !sameDir(got, admin) {
		t.Errorf("ReadGitPointer = %q, %v; want %q", got, err, admin)
	}
	list, err := repo.ListWorktrees(ctx)
	if err != nil {
		t.Fatal(err)
	}
	var found bool
	for _, w := range list {
		if sameDir(w.Path, path) {
			found = true
			if w.Branch != "" || w.Head != feature {
				t.Errorf("worktree entry = %+v, want detached at %s", w, feature)
			}
		}
	}
	if !found {
		t.Errorf("%s not listed in %v", path, list)
	}
	// The operator's checkout is where it was.
	if branch := gitOut(t, dir, "rev-parse", "--abbrev-ref", "HEAD"); branch != "main" {
		t.Errorf("the operator's branch = %s, want main", branch)
	}
	if err := repo.RemoveWorktree(ctx, path); err != nil {
		t.Errorf("RemoveWorktree: %v", err)
	}

	for _, tc := range []struct{ path, commit string }{{"relative/wt", feature}, {path, "feature"}, {path, "--detach"}} {
		if _, err := repo.AddDetachedWorktree(ctx, tc.path, tc.commit); err == nil {
			t.Errorf("AddDetachedWorktree(%+v) = nil, want an error", tc)
		}
	}
}

func TestReviewDiff_BaseIsTheMergeBase(t *testing.T) {
	repo, dir, fork, feature := forkedRepo(t)
	ctx := context.Background()
	path := filepath.Join(t.TempDir(), "review")
	if _, err := repo.AddDetachedWorktree(ctx, path, feature); err != nil {
		t.Fatal(err)
	}
	wt := &Repository{Dir: path, Executor: repo.Executor}
	// What a test step may leave behind is part of the diff, too.
	writeFile(t, filepath.Join(path, "generated.txt"), "by a test\n")
	// The operator's dirty file is in another tree.
	writeFile(t, filepath.Join(dir, "dirty.txt"), "not reviewed\n")

	opts := reviewOpts()
	opts.Base = fork
	d, err := wt.ReviewDiff(ctx, opts)
	if err != nil {
		t.Fatal(err)
	}
	if f := fileByPath(t, d, "feature.txt"); f.Added != 2 || f.Untracked {
		t.Errorf("feature.txt = %+v", f)
	}
	fileByPath(t, d, "generated.txt")
	for _, name := range []string{"main-only.txt", "dirty.txt"} {
		if strings.Contains(d.Text, name) {
			t.Errorf("the review diff names %s, which is not part of the change:\n%s", name, d.Text)
		}
	}

	// Against HEAD, as before, only what the tests wrote.
	d, err = wt.ReviewDiff(ctx, reviewOpts())
	if err != nil {
		t.Fatal(err)
	}
	if len(d.Files) != 1 || d.Files[0].Path != "generated.txt" {
		t.Errorf("against HEAD: %+v", d.Files)
	}

	for _, base := range []string{"main", "-p", "HEAD~1"} {
		opts.Base = base
		if _, err := wt.ReviewDiff(ctx, opts); err == nil {
			t.Errorf("ReviewDiff with Base %q = nil error, want a refusal", base)
		}
	}
}

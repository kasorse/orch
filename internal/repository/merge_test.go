package repository

import (
	"context"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"orchestrator/internal/execution"
)

// mergeFixture is a repository with a base commit and three branches from it: "left" and "right"
// change a.go differently, "clean" adds b.go.
func mergeFixture(t *testing.T) (repo *Repository, dir string, base string) {
	t.Helper()
	repo, dir = realRepo(t)
	base = gitOut(t, dir, "rev-parse", "HEAD")
	trunk := gitOut(t, dir, "symbolic-ref", "--short", "HEAD")

	gitCmd(t, dir, "checkout", "-q", "-b", "left", base)
	writeFile(t, filepath.Join(dir, "a.go"), "package a // left\n")
	gitCmd(t, dir, "commit", "-qam", "left")
	gitCmd(t, dir, "checkout", "-q", "-b", "right", base)
	writeFile(t, filepath.Join(dir, "a.go"), "package a // right\n")
	gitCmd(t, dir, "commit", "-qam", "right")
	gitCmd(t, dir, "checkout", "-q", "-b", "clean", base)
	writeFile(t, filepath.Join(dir, "b.go"), "package b\n")
	gitCmd(t, dir, "add", "b.go")
	gitCmd(t, dir, "commit", "-qm", "clean")
	gitCmd(t, dir, "checkout", "-q", trunk)
	return repo, dir, base
}

func skipWithoutMergeTree(t *testing.T, repo *Repository) {
	t.Helper()
	if ok, err := repo.mergeTreeSupported(context.Background()); err != nil || !ok {
		t.Skip("git older than 2.38")
	}
}

func TestMergeTreeWrite(t *testing.T) {
	repo, dir, _ := mergeFixture(t)
	skipWithoutMergeTree(t, repo)
	ctx := context.Background()

	tree, conflicts, err := repo.MergeTreeWrite(ctx, "left", "clean")
	if err != nil {
		t.Fatal(err)
	}
	if len(conflicts) != 0 || len(tree) < 40 {
		t.Fatalf("clean merge = %q, %q; want a tree and no conflicts", tree, conflicts)
	}
	if got := gitOut(t, dir, "ls-tree", "--name-only", tree); !strings.Contains(got, "a.go") || !strings.Contains(got, "b.go") {
		t.Errorf("the merged tree holds %q, want a.go and b.go", got)
	}

	tree, conflicts, err = repo.MergeTreeWrite(ctx, "left", "right")
	if err != nil {
		t.Fatal(err)
	}
	if tree != "" || !slices.Equal(conflicts, []string{"a.go"}) {
		t.Errorf("conflicting merge = %q, %q; want no tree and [a.go]", tree, conflicts)
	}

	if _, _, err := repo.MergeTreeWrite(ctx, "no-such-branch", "right"); err == nil {
		t.Error("an unknown ref must be an error, not a clean merge")
	}
}

func TestCommitTreeAndUpdateRef(t *testing.T) {
	repo, dir, base := mergeFixture(t)
	skipWithoutMergeTree(t, repo)
	ctx := context.Background()
	left := gitOut(t, dir, "rev-parse", "left")
	clean := gitOut(t, dir, "rev-parse", "clean")

	tree, _, err := repo.MergeTreeWrite(ctx, left, clean)
	if err != nil {
		t.Fatal(err)
	}
	commit, err := repo.CommitTree(ctx, tree, []string{left, clean}, "Merge clean for RF03\n\nOrchestrator-Story: refunds/RF03")
	if err != nil {
		t.Fatal(err)
	}
	if parents := strings.Fields(gitOut(t, dir, "rev-list", "--parents", "-n", "1", commit)); len(parents) != 3 || parents[1] != left || parents[2] != clean {
		t.Errorf("the commit's parents are %v, want %s and %s", parents[1:], left, clean)
	}
	if msg := gitOut(t, dir, "log", "-1", "--format=%B", commit); !strings.Contains(msg, "Merge clean for RF03") || !strings.Contains(msg, "Orchestrator-Story: refunds/RF03") {
		t.Errorf("the commit's message is %q", msg)
	}
	if tipTree := gitOut(t, dir, "rev-parse", commit+"^{tree}"); tipTree != tree {
		t.Errorf("the commit's tree is %s, want %s", tipTree, tree)
	}

	// The ref must not exist yet with an empty old value.
	if err := repo.UpdateRef(ctx, "refs/heads/spec", base, ""); err != nil {
		t.Fatalf("create: %v", err)
	}
	if err := repo.UpdateRef(ctx, "refs/heads/spec", left, ""); err == nil {
		t.Error("creating a ref that exists must be refused")
	}
	// A wrong old value is refused and the ref stays.
	if err := repo.UpdateRef(ctx, "refs/heads/spec", commit, clean); err == nil {
		t.Error("an old value that is not the ref's value must be refused")
	}
	if got := gitOut(t, dir, "rev-parse", "refs/heads/spec"); got != base {
		t.Errorf("spec is at %s after a refused update, want %s", got, base)
	}
	if err := repo.UpdateRef(ctx, "refs/heads/spec", left, base); err != nil {
		t.Fatalf("move: %v", err)
	}
	if got := gitOut(t, dir, "rev-parse", "refs/heads/spec"); got != left {
		t.Errorf("spec is at %s, want %s", got, left)
	}
}

func TestIsAncestor(t *testing.T) {
	repo, dir, base := mergeFixture(t)
	ctx := context.Background()
	left := gitOut(t, dir, "rev-parse", "left")

	if ok, err := repo.IsAncestor(ctx, base, left); err != nil || !ok {
		t.Errorf("IsAncestor(base, left) = %v, %v; want true", ok, err)
	}
	if ok, err := repo.IsAncestor(ctx, left, base); err != nil || ok {
		t.Errorf("IsAncestor(left, base) = %v, %v; want false", ok, err)
	}
	if ok, err := repo.IsAncestor(ctx, left, left); err != nil || !ok {
		t.Errorf("IsAncestor(left, left) = %v, %v; want true", ok, err)
	}
	if _, err := repo.IsAncestor(ctx, "no-such-branch", left); err == nil {
		t.Error("an unknown ref must be an error, not false")
	}
}

func TestMergeHelpersRefuseOptionLikeRefsWithoutGit(t *testing.T) {
	ctx := context.Background()
	sha := "a1b2c3d4e5f60718293a4b5c6d7e8f9012345678"
	calls := map[string]func(r *Repository) error{
		"MergeTreeWrite ours":   func(r *Repository) error { _, _, err := r.MergeTreeWrite(ctx, "-x", sha); return err },
		"MergeTreeWrite theirs": func(r *Repository) error { _, _, err := r.MergeTreeWrite(ctx, sha, ""); return err },
		"CommitTree tree":       func(r *Repository) error { _, err := r.CommitTree(ctx, "--amend", []string{sha}, "m"); return err },
		"CommitTree parent":     func(r *Repository) error { _, err := r.CommitTree(ctx, sha, []string{sha, "-p"}, "m"); return err },
		"UpdateRef ref":         func(r *Repository) error { return r.UpdateRef(ctx, "-d", sha, "") },
		"UpdateRef empty ref":   func(r *Repository) error { return r.UpdateRef(ctx, "", sha, "") },
		"UpdateRef new":         func(r *Repository) error { return r.UpdateRef(ctx, "refs/heads/x", "-x", "") },
		"UpdateRef old":         func(r *Repository) error { return r.UpdateRef(ctx, "refs/heads/x", sha, "-x") },
		"IsAncestor":            func(r *Repository) error { _, err := r.IsAncestor(ctx, "--all", sha); return err },
		"IsAncestor empty":      func(r *Repository) error { _, err := r.IsAncestor(ctx, sha, ""); return err },
	}
	for name, call := range calls {
		fake := &execution.FakeProcessExecutor{}
		if err := call(&Repository{Dir: t.TempDir(), Executor: fake}); err == nil || len(fake.Calls) != 0 {
			t.Errorf("%s = %v with calls %v; want a refusal without git", name, err, fake.Calls)
		}
	}
}

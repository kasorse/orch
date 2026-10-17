package repository

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"slices"
	"testing"

	"orchestrator/internal/execution"
)

func addTestWorktree(t *testing.T, repo *Repository, dir string) (wtPath, admin string) {
	t.Helper()
	wtPath = filepath.Join(t.TempDir(), "wt")
	admin, err := repo.AddWorktree(context.Background(), wtPath, "orchestrator/r1", gitOut(t, dir, "rev-parse", "HEAD"))
	if err != nil {
		t.Fatalf("AddWorktree: %v", err)
	}
	return wtPath, admin
}

func TestWorktree_Lifecycle(t *testing.T) {
	repo, dir := realRepo(t)
	ctx := context.Background()
	wtPath, admin := addTestWorktree(t, repo, dir)

	list, err := repo.ListWorktrees(ctx)
	if err != nil || len(list) != 2 {
		t.Fatalf("ListWorktrees = %v, %v", list, err)
	}
	var found bool
	for _, w := range list {
		if sameDir(w.Path, wtPath) {
			found = true
			if w.Branch != "refs/heads/orchestrator/r1" || len(w.Head) != 40 {
				t.Errorf("worktree entry = %+v", w)
			}
		}
	}
	if !found {
		t.Errorf("%s not listed in %v", wtPath, list)
	}

	got, err := ReadGitPointer(wtPath)
	if err != nil || !sameDir(got, admin) {
		t.Errorf("ReadGitPointer = %q, %v; want %q", got, err, admin)
	}

	// An untracked file makes git refuse, and the directory stays.
	writeFile(t, filepath.Join(wtPath, "new.txt"), "x")
	err = repo.RemoveWorktree(ctx, wtPath)
	if !errors.Is(err, ErrWorktreeKept) {
		t.Fatalf("RemoveWorktree with an untracked file = %v, want ErrWorktreeKept", err)
	}
	if _, err := os.Stat(wtPath); err != nil {
		t.Errorf("the refused worktree must stay: %v", err)
	}

	if err := os.Remove(filepath.Join(wtPath, "new.txt")); err != nil {
		t.Fatal(err)
	}
	if err := repo.RemoveWorktree(ctx, wtPath); err != nil {
		t.Fatalf("RemoveWorktree: %v", err)
	}
	if _, err := os.Stat(wtPath); !os.IsNotExist(err) {
		t.Errorf("the worktree directory must be gone: %v", err)
	}
	if err := repo.PruneWorktrees(ctx); err != nil {
		t.Errorf("PruneWorktrees: %v", err)
	}
	if branch := gitOut(t, dir, "branch", "--list", "orchestrator/r1"); branch == "" {
		t.Error("the branch must stay after removal")
	}
}

func TestBranchExistsAndCommonDir(t *testing.T) {
	repo, dir := realRepo(t)
	ctx := context.Background()
	if ok, err := repo.BranchExists(ctx, "orchestrator/r1"); err != nil || ok {
		t.Fatalf("BranchExists before = %v, %v; want false", ok, err)
	}
	wtPath, _ := addTestWorktree(t, repo, dir)
	if ok, err := repo.BranchExists(ctx, "orchestrator/r1"); err != nil || !ok {
		t.Errorf("BranchExists after = %v, %v; want true", ok, err)
	}

	common, err := repo.CommonDir(ctx)
	if err != nil || !sameDir(common, filepath.Join(dir, ".git")) {
		t.Errorf("CommonDir = %q, %v", common, err)
	}
	wtCommon, err := (&Repository{Dir: wtPath, Executor: repo.Executor}).CommonDir(ctx)
	if err != nil || !sameDir(wtCommon, common) {
		t.Errorf("CommonDir in the worktree = %q, %v; want %q", wtCommon, err, common)
	}

	fake := &execution.FakeProcessExecutor{}
	if _, err := (&Repository{Dir: dir, Executor: fake}).BranchExists(ctx, "-x"); err == nil || len(fake.Calls) != 0 {
		t.Errorf("BranchExists(-x) = %v with calls %v; want a refusal without git", err, fake.Calls)
	}
}

func sameDir(a, b string) bool {
	ra, _ := filepath.EvalSymlinks(a)
	rb, _ := filepath.EvalSymlinks(b)
	return ra == rb
}

func TestAddWorktree_RefusesBadInputWithoutGit(t *testing.T) {
	head := "a1b2c3d4e5f60718293a4b5c6d7e8f9012345678"
	abs := filepath.Join(t.TempDir(), "wt")
	for _, tc := range []struct{ path, branch, base string }{
		{abs, "orchestrator/r1", "-x"},
		{abs, "orchestrator/r1", "HEAD"},
		{abs, "a..b", head},
		{abs, "-b", head},
		{abs, "", head},
		{abs, "x y", head},
		{"relative/wt", "orchestrator/r1", head},
	} {
		fake := &execution.FakeProcessExecutor{}
		repo := &Repository{Dir: t.TempDir(), Executor: fake}
		if _, err := repo.AddWorktree(context.Background(), tc.path, tc.branch, tc.base); err == nil {
			t.Errorf("AddWorktree(%+v) = nil, want an error", tc)
		}
		if len(fake.Calls) != 0 {
			t.Errorf("AddWorktree(%+v) ran git: %v", tc, fake.Calls)
		}
	}
}

func TestReadGitPointer_Refusals(t *testing.T) {
	write := func(content string) string {
		d := t.TempDir()
		writeFile(t, filepath.Join(d, ".git"), content)
		return d
	}
	if got, err := ReadGitPointer(write("gitdir: /a/b/c\n")); err != nil || got != "/a/b/c" {
		t.Errorf("valid pointer = %q, %v", got, err)
	}
	for name, d := range map[string]string{
		"trailing data": write("gitdir: /a/b\nextra\n"),
		"no newline":    write("gitdir: /a/b"),
		"relative":      write("gitdir: a/b\n"),
		"wrong prefix":  write("gitdir:/a/b\n"),
		"empty":         write(""),
		"too large":     write("gitdir: /" + string(make([]byte, 5000)) + "\n"),
	} {
		if got, err := ReadGitPointer(d); err == nil {
			t.Errorf("%s: ReadGitPointer = %q, want an error", name, got)
		}
	}

	dirPtr := t.TempDir()
	if err := os.Mkdir(filepath.Join(dirPtr, ".git"), 0o755); err != nil {
		t.Fatal(err)
	}
	if _, err := ReadGitPointer(dirPtr); err == nil {
		t.Error("a .git directory must be refused")
	}

	link := t.TempDir()
	target := filepath.Join(t.TempDir(), "target")
	writeFile(t, target, "gitdir: /a/b\n")
	if err := os.Symlink(target, filepath.Join(link, ".git")); err != nil {
		t.Skip("symlinks unavailable")
	}
	if _, err := ReadGitPointer(link); err == nil {
		t.Error("a .git symlink must be refused")
	}
}

func TestGitFingerprint_LinkedWorktree(t *testing.T) {
	repo, dir := realRepo(t)
	ctx := context.Background()
	mainBefore, err := repo.GitFingerprint(ctx)
	if err != nil {
		t.Fatal(err)
	}
	wtPath, admin := addTestWorktree(t, repo, dir)
	if mainAfter, _ := repo.GitFingerprint(ctx); mainAfter != mainBefore {
		t.Error("adding a worktree must not change the main tree's fingerprint")
	}

	wt := &Repository{Dir: wtPath, Executor: repo.Executor}
	base, err := wt.GitFingerprint(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if base == mainBefore {
		t.Error("a linked worktree must fingerprint differently from the main tree")
	}
	writeFile(t, filepath.Join(admin, "config.worktree"), "[core]\n\tfsmonitor = true\n")
	if got, _ := wt.GitFingerprint(ctx); got == base {
		t.Error("writing config.worktree must change the fingerprint")
	}
}

func TestMergeConflicts(t *testing.T) {
	repo, dir := realRepo(t)
	ctx := context.Background()
	base := gitOut(t, dir, "rev-parse", "HEAD")
	trunk := gitOut(t, dir, "symbolic-ref", "--short", "HEAD")

	gitCmd(t, dir, "checkout", "-q", "-b", "side", base)
	writeFile(t, filepath.Join(dir, "a.go"), "package a // side\n")
	gitCmd(t, dir, "commit", "-qam", "side")
	gitCmd(t, dir, "checkout", "-q", "-b", "clean", base)
	writeFile(t, filepath.Join(dir, "b.go"), "package b\n")
	gitCmd(t, dir, "add", "b.go")
	gitCmd(t, dir, "commit", "-qm", "clean")
	gitCmd(t, dir, "checkout", "-q", trunk)
	writeFile(t, filepath.Join(dir, "a.go"), "package a // trunk\n")
	gitCmd(t, dir, "commit", "-qam", "trunk")

	files, supported, err := repo.MergeConflicts(ctx, trunk, "side")
	if err != nil {
		t.Fatal(err)
	}
	if !supported {
		t.Skip("git older than 2.38")
	}
	if !slices.Equal(files, []string{"a.go"}) {
		t.Errorf("conflicting pair = %q, want [a.go]", files)
	}
	files, _, err = repo.MergeConflicts(ctx, trunk, "clean")
	if err != nil || len(files) != 0 {
		t.Errorf("clean pair = %q, %v; want none", files, err)
	}
	if _, _, err := repo.MergeConflicts(ctx, "-x", "side"); err == nil {
		t.Error("an option-like ref must be refused")
	}
}

package repository

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"orchestrator/internal/execution"
)

// realRepo creates a committed git repository and opens it with the real executor, for
// the methods whose behaviour depends on git itself rather than on argv.
func realRepo(t *testing.T) (*Repository, string) {
	t.Helper()
	dir := t.TempDir()
	for _, args := range [][]string{
		{"init", "-q"},
		{"config", "user.email", "t@example.com"},
		{"config", "user.name", "T"},
	} {
		gitCmd(t, dir, args...)
	}
	writeFile(t, filepath.Join(dir, "a.go"), "package a\n")
	gitCmd(t, dir, "add", "-A")
	gitCmd(t, dir, "commit", "-qm", "init")
	return &Repository{Dir: dir, Executor: execution.NewOSProcessExecutor()}, dir
}

func gitCmd(t *testing.T, dir string, args ...string) {
	t.Helper()
	cmd := exec.Command("git", args...)
	cmd.Dir = dir
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("git %v: %v\n%s", args, err, out)
	}
}

func writeFile(t *testing.T, path, content string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}

func TestChangedOrIgnoredPaths(t *testing.T) {
	repo, dir := realRepo(t)
	writeFile(t, filepath.Join(dir, ".gitignore"), "orchestrator.yaml\n*.local.json\nbuild/\n")
	gitCmd(t, dir, "add", ".gitignore")
	gitCmd(t, dir, "commit", "-qm", "ignore")

	writeFile(t, filepath.Join(dir, "a.go"), "package a // changed\n")
	writeFile(t, filepath.Join(dir, ".claude", "settings.json"), "{}")
	writeFile(t, filepath.Join(dir, ".claude", "settings.local.json"), "{}") // ignored
	writeFile(t, filepath.Join(dir, "orchestrator.yaml"), "version: 2\n")    // ignored
	writeFile(t, filepath.Join(dir, "build", "out.bin"), "x")                // ignored, not asked for
	writeFile(t, filepath.Join(dir, "sub", "x", "y.txt"), "y")

	got, err := repo.ChangedOrIgnoredPaths(context.Background(), []string{":(top,glob)**/.claude/**", ":(top,glob)**/orchestrator.yaml", ":(top,glob)**/*.go"})
	if err != nil {
		t.Fatal(err)
	}
	slices.Sort(got)
	want := []string{".claude/settings.json", ".claude/settings.local.json", "a.go", "orchestrator.yaml"}
	if !slices.Equal(got, want) {
		t.Errorf("ChangedOrIgnoredPaths = %q, want %q", got, want)
	}
}

func TestHeadRefAndSnapshotCommit(t *testing.T) {
	repo, dir := realRepo(t)
	ctx := context.Background()
	head, err := repo.HeadRef(ctx)
	if err != nil || len(head) != 40 {
		t.Fatalf("HeadRef = %q, %v", head, err)
	}

	writeFile(t, filepath.Join(dir, "a.go"), "package a // changed\n")
	writeFile(t, filepath.Join(dir, "notes.txt"), "untracked\n")
	writeFile(t, filepath.Join(dir, ".orchestrator", "runs", "r1", "run.json"), "{}")
	gitCmd(t, dir, "config", "--unset", "user.email") // commit-tree must not need an identity

	ref, err := repo.SnapshotCommit(ctx)
	if err != nil || len(ref) != 40 || ref == head {
		t.Fatalf("SnapshotCommit = %q, %v", ref, err)
	}
	files := gitOut(t, dir, "show", "--name-only", "--format=", ref)
	if !strings.Contains(files, "a.go") || !strings.Contains(files, "notes.txt") || strings.Contains(files, ".orchestrator") {
		t.Errorf("snapshot must hold a.go and the untracked notes.txt, not the run directory; has:\n%s", files)
	}
	if parent := gitOut(t, dir, "rev-parse", ref+"^"); parent != head {
		t.Errorf("snapshot parent = %s, want HEAD %s", parent, head)
	}
	// Nothing was touched: tree, index, stash list.
	if staged := gitOut(t, dir, "diff", "--cached", "--name-only"); staged != "" {
		t.Errorf("the real index must be untouched, has staged: %q", staged)
	}
	if status := gitOut(t, dir, "status", "--porcelain"); !strings.Contains(status, "M a.go") || !strings.Contains(status, "?? notes.txt") {
		t.Errorf("the working tree must be unchanged, status:\n%s", status)
	}
	if stashes := gitOut(t, dir, "stash", "list"); stashes != "" {
		t.Errorf("the stash list must stay empty, got %q", stashes)
	}
}

func gitOut(t *testing.T, dir string, args ...string) string {
	t.Helper()
	cmd := exec.Command("git", args...)
	cmd.Dir = dir
	out, err := cmd.Output()
	if err != nil {
		t.Fatalf("git %v: %v", args, err)
	}
	return strings.TrimSpace(string(out))
}

func TestGitFingerprint(t *testing.T) {
	repo, dir := realRepo(t)
	ctx := context.Background()
	base, err := repo.GitFingerprint(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if again, _ := repo.GitFingerprint(ctx); again != base {
		t.Fatal("fingerprint is not stable")
	}

	// Source changes and *.sample hooks do not count.
	writeFile(t, filepath.Join(dir, "a.go"), "package a // changed\n")
	writeFile(t, filepath.Join(dir, ".git", "hooks", "pre-push.sample"), "#!/bin/sh\n")
	if got, _ := repo.GitFingerprint(ctx); got != base {
		t.Error("a source change or a .sample hook changed the fingerprint")
	}

	writeFile(t, filepath.Join(dir, ".git", "hooks", "pre-commit"), "#!/bin/sh\n")
	withHook, _ := repo.GitFingerprint(ctx)
	if withHook == base {
		t.Error("adding a hook must change the fingerprint")
	}

	gitCmd(t, dir, "config", "core.fsmonitor", "true")
	if got, _ := repo.GitFingerprint(ctx); got == withHook {
		t.Error("changing .git/config must change the fingerprint")
	}
}

func TestGitFingerprint_FollowsHooksPath(t *testing.T) {
	repo, dir := realRepo(t)
	ctx := context.Background()
	gitCmd(t, dir, "config", "core.hooksPath", ".githooks")
	base, err := repo.GitFingerprint(ctx)
	if err != nil {
		t.Fatal(err)
	}
	writeFile(t, filepath.Join(dir, ".githooks", "pre-commit"), "#!/bin/sh\n")
	if got, _ := repo.GitFingerprint(ctx); got == base {
		t.Error("a hook in core.hooksPath must change the fingerprint")
	}
}

func TestStatusSummary_ExcludesRunDirectory(t *testing.T) {
	repo, dir := realRepo(t)
	writeFile(t, filepath.Join(dir, "a.go"), "package a // changed\n")
	writeFile(t, filepath.Join(dir, ".orchestrator", "runs", "r1", "run.json"), "{}")

	got, err := repo.StatusSummary(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if got != " M a.go" {
		t.Errorf("StatusSummary = %q, want %q", got, " M a.go")
	}
}

func TestIsDirty_IgnoresRunDirectory(t *testing.T) {
	repo, dir := realRepo(t)
	ctx := context.Background()
	writeFile(t, filepath.Join(dir, ".orchestrator", "runs", "r1", "run.json"), "{}")
	if dirty, err := repo.IsDirty(ctx); err != nil || dirty {
		t.Fatalf("IsDirty with only the run directory = %v, %v; want clean", dirty, err)
	}
	writeFile(t, filepath.Join(dir, "notes.txt"), "untracked\n")
	if dirty, err := repo.IsDirty(ctx); err != nil || !dirty {
		t.Errorf("IsDirty with an untracked file = %v, %v; want dirty", dirty, err)
	}
}

func TestCommitExists(t *testing.T) {
	repo, dir := realRepo(t)
	ctx := context.Background()
	head := gitOut(t, dir, "rev-parse", "HEAD")
	tree := gitOut(t, dir, "rev-parse", "HEAD^{tree}")
	for _, tc := range []struct {
		ref  string
		want bool
	}{
		{head, true},
		{tree, false}, // an object, but not a commit
		{strings.Repeat("0", 40), false},
		{head[:12], false}, // abbreviations are not accepted
		{"HEAD", false},
		{"--output=/tmp/x", false},
		{strings.ToUpper(head), false},
	} {
		if got, err := repo.CommitExists(ctx, tc.ref); err != nil || got != tc.want {
			t.Errorf("CommitExists(%q) = %v, %v; want %v", tc.ref, got, err, tc.want)
		}
	}
}

func TestPinRestorePoint_SurvivesGC(t *testing.T) {
	repo, dir := realRepo(t)
	ctx := context.Background()
	writeFile(t, filepath.Join(dir, "notes.txt"), "untracked\n")
	snap, err := repo.SnapshotCommit(ctx)
	if err != nil {
		t.Fatal(err)
	}
	const runID = "2f3c1a8b-0000-4000-8000-000000000001"
	if err := repo.PinRestorePoint(ctx, runID, snap); err != nil {
		t.Fatalf("PinRestorePoint: %v", err)
	}
	if err := repo.PinRestorePoint(ctx, runID, snap); err != nil {
		t.Fatalf("pinning the same commit again must be a no-op: %v", err)
	}

	gitCmd(t, dir, "reflog", "expire", "--expire=now", "--all")
	gitCmd(t, dir, "gc", "-q", "--prune=now")
	if got := gitOut(t, dir, "rev-parse", RestorePointRef(runID)); got != snap {
		t.Errorf("%s = %s, want %s", RestorePointRef(runID), got, snap)
	}
	if ok, err := repo.CommitExists(ctx, snap); err != nil || !ok {
		t.Errorf("the pinned snapshot must survive gc: CommitExists = %v, %v", ok, err)
	}
	// The pin is the only ref added: no branch, tag or HEAD change.
	want := gitOut(t, dir, "symbolic-ref", "HEAD") + "\n" + RestorePointRef(runID)
	if refs := gitOut(t, dir, "for-each-ref", "--sort=refname", "--format=%(refname)"); refs != want {
		t.Errorf("refs after pinning:\n%s\nwant:\n%s", refs, want)
	}
}

func TestPinRestorePoint_RefusesBadInput(t *testing.T) {
	head := strings.Repeat("a", 40)
	for _, tc := range []struct{ runID, sha string }{
		{"", head},
		{"../../heads/master", head},
		{"ABC", head},
		{"2f3c1a8b", "HEAD"},
		{"2f3c1a8b", head[:12]},
	} {
		fake := &execution.FakeProcessExecutor{}
		repo := &Repository{Dir: t.TempDir(), Executor: fake}
		if err := repo.PinRestorePoint(context.Background(), tc.runID, tc.sha); err == nil {
			t.Errorf("PinRestorePoint(%q, %q) = nil, want an error", tc.runID, tc.sha)
		}
		if len(fake.Calls) != 0 {
			t.Errorf("PinRestorePoint(%q, %q) ran git: %v", tc.runID, tc.sha, fake.Calls)
		}
	}
}

func TestGitDirs(t *testing.T) {
	repo, dir := realRepo(t)
	got, err := repo.GitDirs(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	want, _ := filepath.EvalSymlinks(filepath.Join(dir, ".git"))
	if len(got) != 1 || (got[0] != want && got[0] != filepath.Join(dir, ".git")) {
		t.Errorf("GitDirs = %q, want [%s]", got, want)
	}
}

// TestWorktreeTreeDiff: the before/after trees of a step diff to exactly what it changed,
// untracked files included, the (git-ignored) run directory excluded, and nothing in the
// real index or worktree touched.
func TestWorktreeTreeDiff(t *testing.T) {
	r, dir := realRepo(t)
	ctx := context.Background()
	writeFile(t, filepath.Join(dir, ".gitignore"), ".orchestrator/\n")
	gitCmd(t, dir, "add", ".gitignore")
	gitCmd(t, dir, "commit", "-qm", "ignore")
	writeFile(t, filepath.Join(dir, ".orchestrator", "runs", "x", "run.json"), "{}")

	before, err := r.WorktreeTree(ctx)
	if err != nil || before == "" {
		t.Fatalf("before: %q %v", before, err)
	}
	writeFile(t, filepath.Join(dir, "added.go"), "package x\n")
	after, err := r.WorktreeTree(ctx)
	if err != nil {
		t.Fatal(err)
	}
	diff, err := r.DiffTrees(ctx, before, after)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(diff, "+++ b/added.go") || strings.Contains(diff, ".orchestrator") {
		t.Errorf("unexpected diff:\n%s", diff)
	}
	if files, _ := r.ChangedFiles(ctx); len(files) != 1 || files[0] != "added.go" {
		t.Errorf("the real index must be untouched, status: %q", files)
	}
}

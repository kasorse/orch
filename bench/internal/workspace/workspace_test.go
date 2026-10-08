package workspace

import (
	"bytes"
	"context"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"orchbench/internal/cache"
	"orchbench/internal/task"
	"orchbench/internal/testutil"
)

func newManager(t *testing.T) (*Manager, *bytes.Buffer) {
	t.Helper()
	var log bytes.Buffer
	return New(cache.Dir(t.TempDir()), &log), &log
}

func taskFor(p *testutil.SHProject) task.Task {
	return p.Task("fixture")
}

func gitIn(t *testing.T, dir string, args ...string) (string, error) {
	t.Helper()
	cmd := exec.Command("git", args...)
	cmd.Dir = dir
	out, err := cmd.CombinedOutput()
	return strings.TrimSpace(string(out)), err
}

func mustGit(t *testing.T, dir string, args ...string) string {
	t.Helper()
	out, err := gitIn(t, dir, args...)
	if err != nil {
		t.Fatalf("git %s: %v\n%s", strings.Join(args, " "), err, out)
	}
	return out
}

func create(t *testing.T, m *Manager, p *testutil.SHProject, commit string) *Workspace {
	t.Helper()
	ws, err := m.Create(context.Background(), taskFor(p), commit)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { ws.Remove() })
	return ws
}

func TestCreateHoldsTheBaseAndNothingAfter(t *testing.T) {
	p := testutil.NewSHProject(t)
	m, _ := newManager(t)
	ws := create(t, m, p, p.Base)

	if got := mustGit(t, ws.Dir, "rev-parse", "HEAD"); got != p.Base {
		t.Fatalf("HEAD = %s, want %s", got, p.Base)
	}
	if got, _ := os.ReadFile(filepath.Join(ws.Dir, "calc.sh")); string(got) != testutil.CalcBuggy {
		t.Errorf("calc.sh = %q: not the tree of the base commit", got)
	}
	if _, err := gitIn(t, ws.Dir, "symbolic-ref", "-q", "HEAD"); err == nil {
		t.Error("HEAD is on a branch, want detached")
	}

	for _, future := range []string{p.Fix, p.Later} {
		if _, err := gitIn(t, ws.Dir, "cat-file", "-e", future); err == nil {
			t.Errorf("git cat-file -e %s succeeded: the future is in the workspace", future)
		}
	}

	history := strings.Fields(mustGit(t, ws.Dir, "log", "--all", "--format=%H"))
	if len(history) != 2 || history[0] != p.Base || history[1] != p.Root {
		t.Errorf("git log --all = %v, want [%s %s]", history, p.Base, p.Root)
	}
	if out := mustGit(t, ws.Dir, "remote"); out != "" {
		t.Errorf("git remote = %q, want none", out)
	}
	if out := mustGit(t, ws.Dir, "for-each-ref"); out != "" {
		t.Errorf("refs = %q, want none", out)
	}
	if out := mustGit(t, ws.Mirror, "for-each-ref", "refs/orchbench"); out != "" {
		t.Errorf("the temporary ref was left in the mirror: %q", out)
	}
	if out := mustGit(t, ws.Dir, "config", "user.email"); out == "" {
		t.Error("no git identity in the workspace")
	}
}

func TestCreateAtTheFixCommit(t *testing.T) {
	p := testutil.NewSHProject(t)
	m, _ := newManager(t)
	ws := create(t, m, p, p.Fix)

	if got, _ := os.ReadFile(filepath.Join(ws.Dir, "calc.sh")); string(got) != testutil.CalcFixed {
		t.Errorf("calc.sh = %q: not the tree of the fix commit", got)
	}
	if _, err := gitIn(t, ws.Dir, "cat-file", "-e", p.Later); err == nil {
		t.Error("a commit after the fix is in the workspace")
	}
}

func TestEveryWorkspaceIsItsOwn(t *testing.T) {
	p := testutil.NewSHProject(t)
	m, _ := newManager(t)

	a := create(t, m, p, p.Base)
	b := create(t, m, p, p.Base)
	if a.Dir == b.Dir {
		t.Fatal("two workspaces share a directory")
	}
	if !strings.HasPrefix(a.Dir, m.Cache.Work()+string(filepath.Separator)) {
		t.Errorf("workspace %s is not under %s", a.Dir, m.Cache.Work())
	}
}

func TestRemove(t *testing.T) {
	p := testutil.NewSHProject(t)
	m, _ := newManager(t)
	ws := create(t, m, p, p.Base)

	if err := ws.Remove(); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(ws.Dir); !os.IsNotExist(err) {
		t.Fatalf("the workspace is still there: %v", err)
	}
}

func TestDiffShowsTheChangeButNotOrchestrator(t *testing.T) {
	p := testutil.NewSHProject(t)
	m, _ := newManager(t)
	ws := create(t, m, p, p.Base)

	testutil.Apply(t, ws.Dir, map[string]string{"calc.sh": testutil.CalcFixed})
	testutil.Apply(t, ws.Dir, map[string]string{"tests/test_add.sh": testutil.Delete})
	testutil.Apply(t, ws.Dir, map[string]string{"notes.txt": "new file\n"})
	testutil.Apply(t, ws.Dir, map[string]string{".orchestrator/runs/r1/run.json": "{}\n"})

	diff, err := ws.Diff(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"calc.sh", "tests/test_add.sh", "notes.txt", "+mul() { echo $(($1 * $2)); }"} {
		if !strings.Contains(diff, want) {
			t.Errorf("the diff does not mention %q:\n%s", want, diff)
		}
	}
	if strings.Contains(diff, ".orchestrator") {
		t.Errorf("the diff contains .orchestrator:\n%s", diff)
	}

	// The workspace's own index is untouched: nothing was staged, the new file is still
	// untracked.
	if staged := mustGit(t, ws.Dir, "diff", "--cached", "--name-only"); staged != "" {
		t.Errorf("Diff staged %q", staged)
	}
	status, err := ws.Status(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(status, "?? notes.txt") {
		t.Errorf("status = %q, want notes.txt untracked", status)
	}
}

func TestDiffOfAnUnchangedTreeIsEmpty(t *testing.T) {
	p := testutil.NewSHProject(t)
	m, _ := newManager(t)
	ws := create(t, m, p, p.Base)

	testutil.Apply(t, ws.Dir, map[string]string{".orchestrator/state.json": "{}\n"})
	diff, err := ws.Diff(context.Background())
	if err != nil || diff != "" {
		t.Fatalf("Diff = %q, %v", diff, err)
	}
}

func TestStatusIgnoresOrchestrator(t *testing.T) {
	p := testutil.NewSHProject(t)
	m, _ := newManager(t)
	ws := create(t, m, p, p.Base)

	status := func() string {
		t.Helper()
		out, err := ws.Status(context.Background())
		if err != nil {
			t.Fatal(err)
		}
		return out
	}

	if got := status(); got != "" {
		t.Fatalf("a fresh workspace has status %q", got)
	}
	testutil.Apply(t, ws.Dir, map[string]string{".orchestrator/runs/r1/run.json": "{}\n"})
	if got := status(); got != "" {
		t.Fatalf("a change under .orchestrator shows in the status: %q", got)
	}
	testutil.Apply(t, ws.Dir, map[string]string{"calc.sh": "changed\n", "dir/new.txt": "x\n"})
	got := status()
	if !strings.Contains(got, " M calc.sh") || !strings.Contains(got, "?? dir/new.txt") {
		t.Fatalf("status = %q", got)
	}
}

func TestMirrorIsClonedOnce(t *testing.T) {
	p := testutil.NewSHProject(t)
	m, log := newManager(t)

	first, err := m.Mirror(context.Background(), p.Repo.Dir)
	if err != nil {
		t.Fatal(err)
	}
	second, err := m.Mirror(context.Background(), p.Repo.Dir)
	if err != nil {
		t.Fatal(err)
	}
	if first != second {
		t.Fatalf("mirror paths differ: %s, %s", first, second)
	}
	if !strings.HasPrefix(first, m.Cache.Mirrors()) || !strings.HasSuffix(first, ".git") {
		t.Errorf("mirror %s is not <cache>/mirrors/<hash>.git", first)
	}
	if n := strings.Count(log.String(), "$ git clone"); n != 1 {
		t.Errorf("%d clones, want 1:\n%s", n, log)
	}

	// A workspace made from it clones nothing and fetches nothing from the origin.
	create(t, m, p, p.Base)
	if n := strings.Count(log.String(), "$ git clone"); n != 1 {
		t.Errorf("%d clones after Create, want 1", n)
	}
	if strings.Contains(log.String(), "$ git fetch -q    (in "+first) {
		t.Error("Create fetched into the mirror although the commit was there")
	}
}

func TestMirrorOfParallelCallsIsClonedOnce(t *testing.T) {
	p := testutil.NewSHProject(t)
	m, log := newManager(t)

	var wg sync.WaitGroup
	for i := 0; i < 6; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			if _, err := m.Mirror(context.Background(), p.Repo.Dir); err != nil {
				t.Error(err)
			}
		}()
	}
	wg.Wait()

	if n := strings.Count(log.String(), "$ git clone"); n != 1 {
		t.Errorf("%d clones, want 1", n)
	}
}

func TestMirrorKeyIsTheAbsolutePath(t *testing.T) {
	p := testutil.NewSHProject(t)
	m, _ := newManager(t)

	t.Chdir(filepath.Dir(p.Repo.Dir))
	relative, err := m.Mirror(context.Background(), filepath.Base(p.Repo.Dir))
	if err != nil {
		t.Fatal(err)
	}
	absolute, err := m.Mirror(context.Background(), p.Repo.Dir)
	if err != nil {
		t.Fatal(err)
	}
	if relative != absolute {
		t.Fatalf("a relative path and its absolute form are mirrored apart: %s, %s", relative, absolute)
	}
}

func TestEnsureCommitsFetchesOnDemand(t *testing.T) {
	p := testutil.NewSHProject(t)
	m, log := newManager(t)
	ctx := context.Background()

	mirror, err := m.Mirror(ctx, p.Repo.Dir)
	if err != nil {
		t.Fatal(err)
	}
	if err := m.EnsureCommits(ctx, mirror, p.Base, p.Fix, p.Later); err != nil {
		t.Fatal(err)
	}
	if strings.Contains(log.String(), "$ git fetch") {
		t.Error("fetched although every commit was in the mirror")
	}

	added := p.Repo.Commit("added after mirroring", map[string]string{"new.txt": "x\n"})
	if _, err := gitIn(t, mirror, "cat-file", "-e", added); err == nil {
		t.Fatal("the mirror has a commit made after it")
	}
	if err := m.EnsureCommits(ctx, mirror, added); err != nil {
		t.Fatal(err)
	}
	if _, err := gitIn(t, mirror, "cat-file", "-e", added); err != nil {
		t.Errorf("the commit was not fetched: %v", err)
	}
	if !strings.Contains(log.String(), "$ git fetch") {
		t.Error("no fetch was run")
	}
}

func TestEnsureCommitsReportsAMissingCommit(t *testing.T) {
	p := testutil.NewSHProject(t)
	m, _ := newManager(t)
	ctx := context.Background()

	mirror, err := m.Mirror(ctx, p.Repo.Dir)
	if err != nil {
		t.Fatal(err)
	}
	unknown := strings.Repeat("ab", 20)
	err = m.EnsureCommits(ctx, mirror, p.Base, unknown)

	var missing ErrMissingCommit
	if !errors.As(err, &missing) || missing.Commit != unknown {
		t.Fatalf("err = %v, want ErrMissingCommit for %s", err, unknown)
	}
}

func TestCreateWithAMissingCommitLeavesNothingBehind(t *testing.T) {
	p := testutil.NewSHProject(t)
	m, _ := newManager(t)

	_, err := m.Create(context.Background(), taskFor(p), strings.Repeat("cd", 20))
	var missing ErrMissingCommit
	if !errors.As(err, &missing) {
		t.Fatalf("err = %v, want ErrMissingCommit", err)
	}
	if entries, _ := os.ReadDir(m.Cache.Work()); len(entries) != 0 {
		t.Errorf("work directory holds %d leftovers", len(entries))
	}
}

func TestGitErrorsCarryTheExitCode(t *testing.T) {
	m, _ := newManager(t)

	_, err := m.Git(context.Background(), t.TempDir(), "rev-parse", "--verify", "-q", "nothing")
	code, ok := ExitCode(err)
	if err == nil || !ok || code == 0 {
		t.Fatalf("err = %v, exit code %d (%v)", err, code, ok)
	}
	if _, ok := ExitCode(errors.New("other")); ok {
		t.Error("ExitCode accepted an error that is not git's")
	}
}

func TestTreeAndReadBlobs(t *testing.T) {
	p := testutil.NewSHProject(t)
	m, _ := newManager(t)
	mirror, err := m.Mirror(context.Background(), p.Repo.Dir)
	if err != nil {
		t.Fatal(err)
	}

	tree, err := m.Tree(context.Background(), mirror, p.Fix)
	if err != nil {
		t.Fatal(err)
	}
	var paths []string
	byPath := map[string]Entry{}
	for _, e := range tree {
		paths = append(paths, e.Path)
		byPath[e.Path] = e
	}
	want := []string{"README", "calc.sh", "tests/test_add.sh", "tests/test_mul.sh"}
	if strings.Join(paths, ",") != strings.Join(want, ",") {
		t.Fatalf("tree = %q, want %q", paths, want)
	}
	if byPath["calc.sh"].Mode != "100644" {
		t.Errorf("mode = %q", byPath["calc.sh"].Mode)
	}

	oids := []string{byPath["calc.sh"].OID, byPath["tests/test_mul.sh"].OID}
	blobs, err := m.ReadBlobs(context.Background(), mirror, oids)
	if err != nil {
		t.Fatal(err)
	}
	if string(blobs[oids[0]]) != testutil.CalcFixed || string(blobs[oids[1]]) != testutil.MulTestNew {
		t.Fatalf("blobs = %q", blobs)
	}

	if _, err := m.ReadBlobs(context.Background(), mirror, []string{strings.Repeat("0", 40)}); err == nil {
		t.Error("a missing blob was read")
	}
}

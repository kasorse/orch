package repository

import (
	"context"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"orchestrator/internal/execution"
)

// gitAs runs git in dir with an identity and returns its trimmed output.
func gitAs(t *testing.T, dir string, args ...string) string {
	t.Helper()
	cmd := exec.Command("git", args...)
	cmd.Dir = dir
	cmd.Env = append(os.Environ(),
		"GIT_AUTHOR_NAME=T", "GIT_AUTHOR_EMAIL=t@example.com", "GIT_COMMITTER_NAME=T", "GIT_COMMITTER_EMAIL=t@example.com")
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("git %v: %v\n%s", args, err, out)
	}
	return strings.TrimSpace(string(out))
}

// remoteWithMain is a bare repository whose default branch main holds one commit, and a working
// clone of it to add more commits from.
func remoteWithMain(t *testing.T) (bare, work string) {
	t.Helper()
	root := t.TempDir()
	bare = filepath.Join(root, "remote.git")
	work = filepath.Join(root, "work")
	gitAs(t, root, "init", "-q", "--bare", bare)
	gitAs(t, bare, "symbolic-ref", "HEAD", "refs/heads/main")
	gitAs(t, root, "init", "-q", work)
	gitAs(t, work, "checkout", "-q", "-b", "main")
	writeFile(t, filepath.Join(work, "a.txt"), "one\n")
	gitAs(t, work, "add", "-A")
	gitAs(t, work, "commit", "-qm", "one")
	gitAs(t, work, "remote", "add", "origin", bare)
	gitAs(t, work, "push", "-q", "origin", "main")
	return bare, work
}

func pushCommit(t *testing.T, work, file, content string) {
	t.Helper()
	writeFile(t, filepath.Join(work, file), content)
	gitAs(t, work, "add", "-A")
	gitAs(t, work, "commit", "-qm", "add "+file)
	gitAs(t, work, "push", "-q", "origin", "main")
}

func TestClone_Argv(t *testing.T) {
	fake := &execution.FakeProcessExecutor{Results: []execution.ProcessResult{{}}}
	repo := &Repository{Dir: "/srv", Executor: fake}
	if err := repo.Clone(context.Background(), "https://h/g/p.git", ".clone-x-1", []string{"A=b"}); err != nil {
		t.Fatal(err)
	}
	call := fake.Calls[0]
	want := []string{"clone", "--no-recurse-submodules", "--", "https://h/g/p.git", ".clone-x-1"}
	if call.Executable != "git" || !slices.Equal(call.Args, want) || call.WorkingDir != "/srv" {
		t.Errorf("call = %+v, want git %v in /srv", call, want)
	}
	if !slices.Contains(call.Env, "A=b") || !slices.Contains(call.Env, "GIT_TERMINAL_PROMPT=0") {
		t.Errorf("env = %v", call.Env)
	}
	for _, bad := range [][2]string{{"", "d"}, {"-u", "d"}, {"https://h/x", ""}, {"https://h/x", "--d"}} {
		if err := repo.Clone(context.Background(), bad[0], bad[1], nil); err == nil {
			t.Errorf("Clone(%q, %q) succeeded", bad[0], bad[1])
		}
	}
	if len(fake.Calls) != 1 {
		t.Errorf("a refused clone ran git: %d calls", len(fake.Calls))
	}
}

func TestClone_AndDefaultBranch(t *testing.T) {
	bare, _ := remoteWithMain(t)
	parent := t.TempDir()
	repo := &Repository{Dir: parent, Executor: execution.NewOSProcessExecutor()}
	if err := repo.Clone(context.Background(), "file://"+bare, "c", nil); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(parent, "c", "a.txt")); err != nil {
		t.Fatal(err)
	}
	cloned := &Repository{Dir: filepath.Join(parent, "c"), Executor: repo.Executor}
	if got, err := cloned.DefaultBranch(context.Background()); err != nil || got != "main" {
		t.Errorf("DefaultBranch = %q, %v", got, err)
	}

	err := repo.Clone(context.Background(), "file://"+filepath.Join(parent, "missing.git"), "d", nil)
	if err == nil || errors.Is(err, ErrAuthRefused) {
		t.Errorf("a missing remote: %v", err)
	}
}

func TestDefaultBranch_NotSet(t *testing.T) {
	repo, _ := realRepo(t)
	if got, err := repo.DefaultBranch(context.Background()); err == nil {
		t.Errorf("DefaultBranch of a repository without origin = %q", got)
	}
}

func TestFetchFastForward(t *testing.T) {
	bare, work := remoteWithMain(t)
	parent := t.TempDir()
	ex := execution.NewOSProcessExecutor()
	if err := (&Repository{Dir: parent, Executor: ex}).Clone(context.Background(), "file://"+bare, "c", nil); err != nil {
		t.Fatal(err)
	}
	dir := filepath.Join(parent, "c")
	repo := &Repository{Dir: dir, Executor: ex}
	ctx := context.Background()

	// Up to date: nothing to do.
	if err := repo.FetchFastForward(ctx, "main"); err != nil {
		t.Fatalf("up to date: %v", err)
	}

	// Behind: fast-forwarded.
	pushCommit(t, work, "b.txt", "two\n")
	if err := repo.FetchFastForward(ctx, "main"); err != nil {
		t.Fatalf("behind: %v", err)
	}
	if got, err := os.ReadFile(filepath.Join(dir, "b.txt")); err != nil || string(got) != "two\n" {
		t.Errorf("b.txt = %q, %v", got, err)
	}

	// Diverged: a commit of its own, and another on the remote.
	writeFile(t, filepath.Join(dir, "own.txt"), "own\n")
	gitAs(t, dir, "add", "-A")
	gitAs(t, dir, "commit", "-qm", "own")
	pushCommit(t, work, "c.txt", "three\n")
	before := gitAs(t, dir, "rev-parse", "HEAD")
	err := repo.FetchFastForward(ctx, "main")
	if !errors.Is(err, ErrDiverged) {
		t.Fatalf("diverged: %v", err)
	}
	if after := gitAs(t, dir, "rev-parse", "HEAD"); after != before {
		t.Error("a diverged checkout was moved")
	}

	if err := repo.FetchFastForward(ctx, "nope"); err == nil || errors.Is(err, ErrDiverged) {
		t.Errorf("a branch origin lacks: %v", err)
	}
	if err := repo.FetchFastForward(ctx, "--x"); err == nil {
		t.Error("an option was taken for a branch")
	}
}

func TestFetchFastForward_UnreachableRemote(t *testing.T) {
	bare, _ := remoteWithMain(t)
	parent := t.TempDir()
	ex := execution.NewOSProcessExecutor()
	if err := (&Repository{Dir: parent, Executor: ex}).Clone(context.Background(), "file://"+bare, "c", nil); err != nil {
		t.Fatal(err)
	}
	if err := os.RemoveAll(bare); err != nil {
		t.Fatal(err)
	}
	err := (&Repository{Dir: filepath.Join(parent, "c"), Executor: ex}).FetchFastForward(context.Background(), "main")
	if err == nil || errors.Is(err, ErrDiverged) || errors.Is(err, ErrAuthRefused) {
		t.Errorf("an unreachable remote: %v", err)
	}
}

func TestAuthRefused(t *testing.T) {
	for _, s := range []string{
		"fatal: Authentication failed for 'https://gitlab.corp/g/p.git/'",
		"fatal: could not read Username for 'https://gitlab.corp': terminal prompts disabled",
		"remote: HTTP Basic: Access denied",
		"fatal: unable to access 'https://h/x.git/': The requested URL returned error: 403",
		"fatal: unable to access 'https://h/x.git/': The requested URL returned error: 401",
	} {
		if !authRefused(s) {
			t.Errorf("authRefused(%q) = false", s)
		}
	}
	for _, s := range []string{
		"fatal: unable to access 'https://h/x.git/': Could not resolve host: h",
		"fatal: unable to access 'https://h/x.git/': The requested URL returned error: 404",
		"fatal: repository 'file:///x' does not exist",
		"",
	} {
		if authRefused(s) {
			t.Errorf("authRefused(%q) = true", s)
		}
	}
}

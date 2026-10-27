package repository

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// pushFixture is a repository on branch feature/x with a bare origin that takes push options and
// records the ones it receives, one per line, in push-options.log.
func pushFixture(t *testing.T, advertise bool) (repo *Repository, dir, bare string) {
	t.Helper()
	repo, dir = realRepo(t)
	bare = filepath.Join(t.TempDir(), "origin.git")
	gitCmd(t, dir, "init", "--bare", "-q", bare)
	if advertise {
		gitCmd(t, bare, "config", "receive.advertisePushOptions", "true")
	}
	hook := "#!/bin/sh\ni=0\nwhile [ \"$i\" -lt \"${GIT_PUSH_OPTION_COUNT:-0}\" ]; do\n" +
		"  eval \"opt=\\$GIT_PUSH_OPTION_$i\"\n  printf '%s\\n' \"$opt\" >> push-options.log\n  i=$((i + 1))\ndone\n" +
		"echo 'hello from the remote' >&2\n"
	if err := os.WriteFile(filepath.Join(bare, "hooks", "post-receive"), []byte(hook), 0o755); err != nil {
		t.Fatal(err)
	}
	gitCmd(t, dir, "remote", "add", "origin", bare)
	gitCmd(t, dir, "checkout", "-q", "-b", "feature/x")
	return repo, dir, bare
}

func TestPush_WithOptions(t *testing.T) {
	repo, dir, bare := pushFixture(t, true)
	options := []string{"merge_request.create", "merge_request.title=A title with = and \"quotes\"", `merge_request.description=line one\nline two`}

	stderr, err := repo.Push(context.Background(), "origin", "feature/x", options)
	if err != nil {
		t.Fatalf("Push: %v\n%s", err, stderr)
	}
	if !strings.Contains(stderr, "remote: hello from the remote") {
		t.Errorf("stderr = %q, want what the remote printed", stderr)
	}
	if got, want := gitOut(t, bare, "rev-parse", "feature/x"), gitOut(t, dir, "rev-parse", "HEAD"); got != want {
		t.Errorf("the remote's feature/x = %s, want %s", got, want)
	}
	data, err := os.ReadFile(filepath.Join(bare, "push-options.log"))
	if err != nil || string(data) != strings.Join(options, "\n")+"\n" {
		t.Errorf("the remote received %q (%v), want %q", data, err, options)
	}
	// -u: the upstream is recorded.
	if got := gitOut(t, dir, "config", "branch.feature/x.remote"); got != "origin" {
		t.Errorf("branch.feature/x.remote = %q, want origin", got)
	}
}

func TestPush_WithoutOptions(t *testing.T) {
	repo, _, bare := pushFixture(t, false)
	if _, err := repo.Push(context.Background(), "origin", "feature/x", nil); err != nil {
		t.Fatalf("Push: %v", err)
	}
	if _, err := os.Stat(filepath.Join(bare, "push-options.log")); !os.IsNotExist(err) {
		t.Errorf("push options reached the remote: %v", err)
	}
	if gitOut(t, bare, "branch", "--list", "feature/x") == "" {
		t.Error("the branch is not on the remote")
	}
}

// A remote that does not advertise push options refuses a push with -o, and the error says so.
func TestPush_RemoteWithoutPushOptions(t *testing.T) {
	repo, _, bare := pushFixture(t, false)
	stderr, err := repo.Push(context.Background(), "origin", "feature/x", []string{"merge_request.create"})
	if err == nil || !strings.Contains(stderr, "does not support push options") {
		t.Fatalf("Push = %v, stderr %q; want a refusal naming push options", err, stderr)
	}
	if gitOut(t, bare, "branch", "--list") != "" {
		t.Error("a branch reached the remote")
	}
}

// Names git could read as options or refspecs, and options git would refuse, never reach git.
func TestPush_RefusesWhatGitWouldMisread(t *testing.T) {
	repo, _, bare := pushFixture(t, true)
	ctx := context.Background()
	for _, c := range []struct{ remote, branch string }{
		{"-v", "feature/x"}, {"", "feature/x"}, {"origin", ""}, {"origin", "-f"}, {"origin", "feature/x:main"}, {"origin", "a b"}, {"origin", "a\nb"},
	} {
		if _, err := repo.Push(ctx, c.remote, c.branch, nil); err == nil {
			t.Errorf("Push(%q, %q) succeeded", c.remote, c.branch)
		}
		if _, err := repo.LsRemoteHead(ctx, c.remote, c.branch); err == nil {
			t.Errorf("LsRemoteHead(%q, %q) succeeded", c.remote, c.branch)
		}
	}
	for _, o := range []string{"a\nb", "a\x00b"} {
		if _, err := repo.Push(ctx, "origin", "feature/x", []string{o}); err == nil {
			t.Errorf("Push with the option %q succeeded", o)
		}
	}
	if gitOut(t, bare, "branch", "--list") != "" {
		t.Error("a branch reached the remote")
	}
}

func TestLsRemoteHead(t *testing.T) {
	repo, dir, _ := pushFixture(t, true)
	ctx := context.Background()
	if present, err := repo.LsRemoteHead(ctx, "origin", "feature/x"); err != nil || present {
		t.Fatalf("LsRemoteHead before the push = %v, %v; want false", present, err)
	}
	gitCmd(t, dir, "push", "-q", "origin", "HEAD:refs/heads/feature/xy", "HEAD:refs/heads/other/feature/x", "HEAD:refs/tags/feature/x")
	if present, err := repo.LsRemoteHead(ctx, "origin", "feature/x"); err != nil || present {
		t.Errorf("LsRemoteHead with only look-alikes = %v, %v; want false", present, err)
	}
	gitCmd(t, dir, "push", "-q", "origin", "HEAD:refs/heads/feature/x")
	if present, err := repo.LsRemoteHead(ctx, "origin", "feature/x"); err != nil || !present {
		t.Errorf("LsRemoteHead after the push = %v, %v; want true", present, err)
	}
	if _, err := repo.LsRemoteHead(ctx, "nowhere", "feature/x"); err == nil {
		t.Error("a remote that does not exist was answered")
	}
}

func TestCommitSubject(t *testing.T) {
	repo, dir := realRepo(t)
	gitCmd(t, dir, "commit", "--allow-empty", "-qm", "The subject\n\nThe body.")
	sha := gitOut(t, dir, "rev-parse", "HEAD")
	got, err := repo.CommitSubject(context.Background(), sha)
	if err != nil || got != "The subject" {
		t.Errorf("CommitSubject = %q, %v", got, err)
	}
	for _, ref := range []string{"HEAD", "-n", ""} {
		if _, err := repo.CommitSubject(context.Background(), ref); err == nil {
			t.Errorf("CommitSubject(%q) succeeded: only a full SHA names a commit", ref)
		}
	}
}

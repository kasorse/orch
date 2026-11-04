package app

import (
	"context"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/kasorse/orch/internal/execution"
	"github.com/kasorse/orch/internal/listener"
	"github.com/kasorse/orch/internal/story"
)

const checkoutTestToken = "glpat-checkout-test-value"

func ckGit(t *testing.T, dir string, args ...string) string {
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

func ckWrite(t *testing.T, path, content string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}

// ckRemote is a "GitLab" on disk: root/g/p.git is a bare repository whose default branch main has
// one commit, and work is a clone to add commits from.
func ckRemote(t *testing.T) (root, work string) {
	t.Helper()
	t.Setenv("GIT_CONFIG_GLOBAL", os.DevNull)
	root = t.TempDir()
	bare := filepath.Join(root, "g", "p.git")
	work = filepath.Join(t.TempDir(), "work")
	if err := os.MkdirAll(filepath.Dir(bare), 0o755); err != nil {
		t.Fatal(err)
	}
	ckGit(t, root, "init", "-q", "--bare", bare)
	ckGit(t, bare, "symbolic-ref", "HEAD", "refs/heads/main")
	ckGit(t, root, "init", "-q", work)
	ckGit(t, work, "checkout", "-q", "-b", "main")
	ckWrite(t, filepath.Join(work, "a.txt"), "one\n")
	ckGit(t, work, "add", "-A")
	ckGit(t, work, "commit", "-qm", "one")
	ckGit(t, work, "remote", "add", "origin", bare)
	ckGit(t, work, "push", "-q", "origin", "main")
	return root, work
}

func ckPush(t *testing.T, work, file, content string) {
	t.Helper()
	ckWrite(t, filepath.Join(work, file), content)
	ckGit(t, work, "add", "-A")
	ckGit(t, work, "commit", "-qm", "add "+file)
	ckGit(t, work, "push", "-q", "origin", "main")
}

// ckCheckout is a checkout of the services directory against the remote under root, with the
// credential helper's environment set and a token in it.
func ckCheckout(t *testing.T, root string) *serviceCheckout {
	t.Helper()
	base := "file://" + root
	env := append(os.Environ(), "GITLAB_TEST_TOKEN="+checkoutTestToken)
	return &serviceCheckout{
		servicesDir:   filepath.Join(t.TempDir(), "services"),
		baseURL:       base,
		allowedGroups: []string{"g"},
		cloneTimeout:  time.Minute,
		executor:      execution.NewOSProcessExecutor(),
		env:           withListenerGit(env, "/nonexistent/orchestrator", base, "--config", "/nonexistent/listener.yaml"),
	}
}

func checkoutKindOf(err error) checkoutKind {
	var ce *checkoutError
	if errors.As(err, &ce) {
		return ce.Kind
	}
	return ""
}

func TestPrepare_ClonesWithoutLeavingTheToken(t *testing.T) {
	root, _ := ckRemote(t)
	c := ckCheckout(t, root)

	res, err := c.prepare(context.Background(), "svc", "g/p")
	if err != nil {
		t.Fatal(err)
	}
	dir := filepath.Join(c.servicesDir, "svc")
	if res.Dir != dir || res.Default != "main" || !res.Cloned {
		t.Errorf("result = %+v", res)
	}
	if got, err := os.ReadFile(filepath.Join(dir, "a.txt")); err != nil || string(got) != "one\n" {
		t.Errorf("a.txt = %q, %v", got, err)
	}

	config, err := os.ReadFile(filepath.Join(dir, ".git", "config"))
	if err != nil {
		t.Fatal(err)
	}
	for _, leak := range []string{checkoutTestToken, "git-credential", "GIT_CONFIG", "password"} {
		if strings.Contains(string(config), leak) {
			t.Errorf(".git/config holds %q:\n%s", leak, config)
		}
	}
	entries, _ := os.ReadDir(c.servicesDir)
	if len(entries) != 1 || entries[0].Name() != "svc" {
		t.Errorf("the services directory holds %v", entries)
	}

	// A second call finds it, checks the project and fast-forwards.
	res, err = c.prepare(context.Background(), "svc", "g/p")
	if err != nil || res.Cloned {
		t.Errorf("second prepare = %+v, %v", res, err)
	}
	// Without a project label the directory is used as it is.
	if _, err := c.prepare(context.Background(), "svc", ""); err != nil {
		t.Errorf("without a project: %v", err)
	}
}

func TestPrepare_ACutCloneLeavesNoService(t *testing.T) {
	root, _ := ckRemote(t)
	cuts := []struct {
		name  string
		tweak func(*serviceCheckout)
	}{
		{"missing remote", func(c *serviceCheckout) {
			c.baseURL = "file://" + filepath.Join(root, "nowhere")
		}},
		{"timeout", func(c *serviceCheckout) {
			c.cloneTimeout = time.Nanosecond
		}},
	}
	for _, cut := range cuts {
		t.Run(cut.name, func(t *testing.T) {
			c := ckCheckout(t, root)
			cut.tweak(c)
			_, err := c.prepare(context.Background(), "svc", "g/p")
			if k := checkoutKindOf(err); k != checkoutCloneFailed {
				t.Fatalf("kind = %q, err = %v", k, err)
			}
			if k := checkoutKindOf(err); k.class() != classRetry {
				t.Errorf("class = %q", k.class())
			}
			entries, _ := os.ReadDir(c.servicesDir)
			if len(entries) != 0 {
				t.Errorf("the services directory holds %v", entries)
			}
		})
	}
}

func TestPrepare_ACancelledContextIsNotACheckoutFailure(t *testing.T) {
	root, _ := ckRemote(t)
	c := ckCheckout(t, root)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	_, err := c.prepare(ctx, "svc", "g/p")
	if !errors.Is(err, context.Canceled) || checkoutKindOf(err) != "" {
		t.Errorf("err = %v", err)
	}
	if entries, _ := os.ReadDir(c.servicesDir); len(entries) != 0 {
		t.Errorf("the services directory holds %v", entries)
	}
}

func TestPrepare_NothingToCloneFrom(t *testing.T) {
	root, _ := ckRemote(t)
	c := ckCheckout(t, root)
	tests := []struct {
		project string
		kind    checkoutKind
		class   checkoutClass
	}{
		{"", checkoutNoProject, classBlocked},
		{"other/p", checkoutNotAllowed, classBlocked},
		{"g", checkoutNotAllowed, classBlocked},
	}
	for _, tt := range tests {
		_, err := c.prepare(context.Background(), "svc", tt.project)
		if k := checkoutKindOf(err); k != tt.kind || k.class() != tt.class {
			t.Errorf("project %q: kind = %q, class = %q, err = %v", tt.project, k, k.class(), err)
		}
	}
	if _, err := os.Stat(c.servicesDir); err == nil {
		if entries, _ := os.ReadDir(c.servicesDir); len(entries) != 0 {
			t.Errorf("the services directory holds %v", entries)
		}
	}
	if _, err := c.prepare(context.Background(), "../escape", "g/p"); err == nil || checkoutKindOf(err) != "" {
		t.Errorf("an invalid service name: %v", err)
	}
}

func TestPrepare_ExistingDirectories(t *testing.T) {
	root, work := ckRemote(t)
	ctx := context.Background()

	// cloned is a fresh service checkout, and its directory.
	cloned := func(t *testing.T) (*serviceCheckout, string) {
		c := ckCheckout(t, root)
		if _, err := c.prepare(ctx, "svc", "g/p"); err != nil {
			t.Fatal(err)
		}
		return c, filepath.Join(c.servicesDir, "svc")
	}

	kinds := map[checkoutKind]bool{}
	tests := []struct {
		name  string
		setup func(t *testing.T, c *serviceCheckout, dir string)
		kind  checkoutKind
	}{
		{"dirty", func(t *testing.T, c *serviceCheckout, dir string) {
			ckWrite(t, filepath.Join(dir, "scratch.txt"), "x")
		}, checkoutDirty},
		{"edited", func(t *testing.T, c *serviceCheckout, dir string) {
			ckWrite(t, filepath.Join(dir, "a.txt"), "edited\n")
		}, checkoutDirty},
		{"another branch, dirty", func(t *testing.T, c *serviceCheckout, dir string) {
			ckGit(t, dir, "checkout", "-q", "-b", "feature")
			ckWrite(t, filepath.Join(dir, "scratch.txt"), "x")
		}, checkoutDirty},
		{"the default branch is held by another worktree", func(t *testing.T, c *serviceCheckout, dir string) {
			def := ckGit(t, dir, "symbolic-ref", "--short", "HEAD")
			ckGit(t, dir, "checkout", "-q", "-b", "feature")
			ckGit(t, dir, "worktree", "add", "-q", filepath.Join(t.TempDir(), "wt"), def)
		}, checkoutOffDefault},
		{"diverged", func(t *testing.T, c *serviceCheckout, dir string) {
			ckWrite(t, filepath.Join(dir, "own.txt"), "own\n")
			ckGit(t, dir, "add", "-A")
			ckGit(t, dir, "commit", "-qm", "own")
			ckPush(t, work, "remote.txt", "remote\n")
		}, checkoutDiverged},
		{"ahead of origin", func(t *testing.T, c *serviceCheckout, dir string) {
			ckWrite(t, filepath.Join(dir, "own.txt"), "own\n")
			ckGit(t, dir, "add", "-A")
			ckGit(t, dir, "commit", "-qm", "own")
		}, checkoutDiverged},
		{"another project", func(t *testing.T, c *serviceCheckout, dir string) {
			ckGit(t, dir, "remote", "set-url", "origin", "file://"+root+"/g/q.git")
		}, checkoutWrongProject},
		{"no origin", func(t *testing.T, c *serviceCheckout, dir string) {
			ckGit(t, dir, "remote", "remove", "origin")
		}, checkoutWrongProject},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			c, dir := cloned(t)
			tt.setup(t, c, dir)
			_, err := c.prepare(ctx, "svc", "g/p")
			if k := checkoutKindOf(err); k != tt.kind {
				t.Fatalf("kind = %q, want %q (err = %v)", k, tt.kind, err)
			}
			kinds[tt.kind] = true
			var ce *checkoutError
			if errors.As(err, &ce) && strings.Contains(ce.Msg, "file://") {
				t.Errorf("the comment line holds a URL: %q", ce.Msg)
			}
		})
	}
	if !kinds[checkoutDirty] || !kinds[checkoutOffDefault] || !kinds[checkoutDiverged] {
		t.Errorf("kinds seen: %v", kinds)
	}
}

func TestPrepare_AnUnreachableRemoteIsRetried(t *testing.T) {
	root, _ := ckRemote(t)
	c := ckCheckout(t, root)
	ctx := context.Background()
	if _, err := c.prepare(ctx, "svc", "g/p"); err != nil {
		t.Fatal(err)
	}
	if err := os.RemoveAll(filepath.Join(root, "g", "p.git")); err != nil {
		t.Fatal(err)
	}
	_, err := c.prepare(ctx, "svc", "g/p")
	if k := checkoutKindOf(err); k != checkoutFetchFailed || k.class() != classRetry {
		t.Errorf("kind = %q, err = %v", k, err)
	}
}

func TestPrepare_FastForwardsABehindCheckout(t *testing.T) {
	root, work := ckRemote(t)
	c := ckCheckout(t, root)
	ctx := context.Background()
	if _, err := c.prepare(ctx, "svc", "g/p"); err != nil {
		t.Fatal(err)
	}
	ckPush(t, work, "new.txt", "new\n")

	// A leftover of a run does not make it dirty.
	dir := filepath.Join(c.servicesDir, "svc")
	ckWrite(t, filepath.Join(dir, ".orchestrator", "runs", "x", "run.json"), "{}")

	res, err := c.prepare(ctx, "svc", "g/p")
	if err != nil {
		t.Fatal(err)
	}
	if got, err := os.ReadFile(filepath.Join(res.Dir, "new.txt")); err != nil || string(got) != "new\n" {
		t.Errorf("new.txt = %q, %v", got, err)
	}
	if head, origin := ckGit(t, dir, "rev-parse", "HEAD"), ckGit(t, work, "rev-parse", "HEAD"); head != origin {
		t.Errorf("HEAD = %s, origin = %s", head, origin)
	}
}

func TestPrepare_SwitchesACleanCheckoutToTheDefaultBranch(t *testing.T) {
	root, work := ckRemote(t)
	c := ckCheckout(t, root)
	ctx := context.Background()
	if _, err := c.prepare(ctx, "svc", "g/p"); err != nil {
		t.Fatal(err)
	}
	dir := filepath.Join(c.servicesDir, "svc")
	def := ckGit(t, dir, "symbolic-ref", "--short", "HEAD")

	for _, leave := range [][]string{{"checkout", "-q", "-b", "feature"}, {"checkout", "-q", "--detach", def}} {
		ckGit(t, dir, leave...)
		ckPush(t, work, "new.txt", strings.Join(leave, " ")+"\n")
		res, err := c.prepare(ctx, "svc", "g/p")
		if err != nil {
			t.Fatalf("%v: %v", leave, err)
		}
		if res.Default != def {
			t.Errorf("%v: default = %q, want %q", leave, res.Default, def)
		}
		if got := ckGit(t, dir, "symbolic-ref", "--short", "HEAD"); got != def {
			t.Errorf("%v: on %q, want %q", leave, got, def)
		}
		if head, origin := ckGit(t, dir, "rev-parse", "HEAD"), ckGit(t, work, "rev-parse", "HEAD"); head != origin {
			t.Errorf("%v: HEAD = %s, origin = %s", leave, head, origin)
		}
	}
}

func TestPrepare_NotAWorkTree(t *testing.T) {
	root, _ := ckRemote(t)
	c := ckCheckout(t, root)
	ctx := context.Background()

	// An empty directory, a file, and a directory inside the work tree of the services directory's
	// own repository are none of them a service checkout.
	ckWrite(t, filepath.Join(c.servicesDir, "empty", ".keep"), "")
	ckWrite(t, filepath.Join(c.servicesDir, "file"), "x")
	ckGit(t, c.servicesDir, "init", "-q")
	ckWrite(t, filepath.Join(c.servicesDir, "inner", "x.txt"), "x")
	for _, service := range []string{"empty", "file", "inner"} {
		_, err := c.prepare(ctx, service, "")
		if k := checkoutKindOf(err); k != checkoutNotWorkTree {
			t.Errorf("%s: kind = %q, err = %v", service, k, err)
		}
	}
	if err := os.Symlink(t.TempDir(), filepath.Join(c.servicesDir, "link")); err != nil {
		t.Fatal(err)
	}
	if _, err := c.prepare(ctx, "link", ""); checkoutKindOf(err) != checkoutNotWorkTree {
		t.Errorf("a symlink: %v", err)
	}
}

func TestCheckoutKindClasses(t *testing.T) {
	want := []struct {
		kind  checkoutKind
		class checkoutClass
	}{
		{checkoutNoProject, classBlocked},
		{checkoutNotAllowed, classBlocked},
		{checkoutNotWorkTree, classFailed},
		{checkoutWrongProject, classFailed},
		{checkoutNoDefault, classFailed},
		{checkoutOffDefault, classFailed},
		{checkoutDirty, classFailed},
		{checkoutDiverged, classFailed},
		{checkoutTampered, classFailed},
		{checkoutCloneFailed, classRetry},
		{checkoutFetchFailed, classRetry},
		{checkoutAuth, classStop},
	}
	seen := map[checkoutKind]bool{}
	for _, w := range want {
		seen[w.kind] = true
		if got := w.kind.class(); got != w.class {
			t.Errorf("%s: class = %q, want %q", w.kind, got, w.class)
		}
	}
	if len(seen) != len(want) {
		t.Error("a kind is listed twice")
	}
}

func TestRemoveStaleClones(t *testing.T) {
	c := &serviceCheckout{servicesDir: filepath.Join(t.TempDir(), "services")}
	if err := c.removeStaleClones(); err != nil {
		t.Fatalf("no services directory: %v", err)
	}
	ckWrite(t, filepath.Join(c.servicesDir, ".clone-billing-ab12", "x.txt"), "x")
	ckWrite(t, filepath.Join(c.servicesDir, ".clone-web-cd34", ".git", "HEAD"), "x")
	ckWrite(t, filepath.Join(c.servicesDir, "billing", "x.txt"), "x")
	ckWrite(t, filepath.Join(c.servicesDir, ".other", "x.txt"), "x")
	if err := c.removeStaleClones(); err != nil {
		t.Fatal(err)
	}
	entries, _ := os.ReadDir(c.servicesDir)
	var names []string
	for _, e := range entries {
		names = append(names, e.Name())
	}
	if !slices.Equal(names, []string{".other", "billing"}) {
		t.Errorf("left: %v", names)
	}
}

func TestServiceDirs(t *testing.T) {
	c := &serviceCheckout{servicesDir: filepath.Join(t.TempDir(), "services")}
	if dirs, err := c.serviceDirs(); err != nil || dirs != nil {
		t.Fatalf("no services directory: %v, %v", dirs, err)
	}
	ckWrite(t, filepath.Join(c.servicesDir, "billing", "x"), "x")
	ckWrite(t, filepath.Join(c.servicesDir, "web", "x"), "x")
	ckWrite(t, filepath.Join(c.servicesDir, ".clone-web-1", "x"), "x")
	ckWrite(t, filepath.Join(c.servicesDir, "README.md"), "x")
	dirs, err := c.serviceDirs()
	if err != nil || !slices.Equal(dirs, []string{"billing", "web"}) {
		t.Errorf("serviceDirs = %v, %v", dirs, err)
	}
}

// listener cannot import story, so it keeps its own copy of story.ValidRepo; they must agree.
func TestValidServiceAgreesWithStory(t *testing.T) {
	names := []string{
		"", "a", "billing", "Billing-2", "a.b_c", "a b", "-a", ".a", "_a", "a/b", "..", ".", "a\n", "é", "0", "a-", "a.",
		strings.Repeat("a", 64), strings.Repeat("a", 65), ".clone-x-1", "a:b", "a\x00",
	}
	for _, n := range names {
		if got, want := listener.ValidService(n), story.ValidRepo(n); got != want {
			t.Errorf("ValidService(%q) = %v, story.ValidRepo = %v", n, got, want)
		}
	}
}

package verify

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/kasorse/orch/internal/domain"
)

func TestResolveFetch(t *testing.T) {
	f := &FetchSpec{BaseURL: "https://gitlab.example", AllowedGroups: []string{"grp", "org/sub"}}
	cases := []struct {
		url, project, clone, reason string
	}{
		{"https://gitlab.example/grp/proj", "grp/proj", "https://gitlab.example/grp/proj.git", ""},
		{"https://gitlab.example/grp/proj.git/", "grp/proj", "https://gitlab.example/grp/proj.git", ""},
		{"https://gitlab.example/grp/sub/proj/-/blob/main/internal/handler.go", "grp/sub/proj", "https://gitlab.example/grp/sub/proj.git", ""},
		{"https://gitlab.example/org/sub/proj/-/tree/main", "org/sub/proj", "https://gitlab.example/org/sub/proj.git", ""},
		{"HTTPS://GITLAB.EXAMPLE/grp/proj", "grp/proj", "https://gitlab.example/grp/proj.git", ""},
		{"https://gitlab.example/other/proj", "", "", reasonURLGroup},
		{"https://gitlab.example/org/proj", "", "", reasonURLGroup},
		{"https://gitlab.example/grp", "", "", reasonURLShape},
		{"https://gitlab.example/grp/../etc", "", "", reasonURLShape},
		{"https://gitlab.example/grp/.hidden", "", "", reasonURLShape},
		{"https://evil.example/grp/proj", "", "", reasonURLNotGitLab},
		{"https://gitlab.example.evil/grp/proj", "", "", reasonURLNotGitLab},
		{"http://gitlab.example/grp/proj", "", "", reasonURLNotGitLab},
		{"https://user@gitlab.example/grp/proj", "", "", reasonURLNotGitLab},
		{"git@gitlab.example:grp/proj.git", "", "", reasonURLNotGitLab},
		{"grp/proj", "", "", reasonURLNotGitLab},
	}
	for _, c := range cases {
		req, reason := resolveFetch(f, c.url)
		if reason != c.reason || req.project != c.project || req.clone != c.clone {
			t.Errorf("%s: got (%q, %q, %q), want (%q, %q, %q)", c.url, req.project, req.clone, reason, c.project, c.clone, c.reason)
		}
		if reason == "" && req.name != filepath.Base(c.project) {
			t.Errorf("%s: name %q", c.url, req.name)
		}
	}

	// A base URL with a context path: the path is part of what every URL must start with.
	f = &FetchSpec{BaseURL: "https://git.example/gitlab", AllowedGroups: []string{"grp"}}
	if req, reason := resolveFetch(f, "https://git.example/gitlab/grp/proj"); reason != "" || req.project != "grp/proj" {
		t.Errorf("context path: got (%q, %q)", req.project, reason)
	}
	if _, reason := resolveFetch(f, "https://git.example/grp/proj"); reason != reasonURLNotGitLab {
		t.Errorf("outside the context path: got %q", reason)
	}
}

func TestSSHForm(t *testing.T) {
	if got := sshForm("https://gitlab.example/grp/proj.git"); got != "git@gitlab.example:grp/proj.git" {
		t.Errorf("got %q", got)
	}
}

func TestFetchSpec_Validate(t *testing.T) {
	spec := &Spec{Version: SpecVersion, RepoRoot: "/repo", Before: testState(t),
		Fetch: &FetchSpec{BaseURL: "https://gitlab.example", AllowedGroups: []string{"grp"}, Timeout: time.Minute}}
	if err := spec.Validate(); err != nil {
		t.Fatalf("fetch alone: %v", err)
	}
	if got := spec.ToolNames(); len(got) != 1 || got[0] != domain.VerifyToolFetchRepository {
		t.Errorf("tools: %v", got)
	}
	for name, bad := range map[string]FetchSpec{
		"no groups":      {BaseURL: "https://gitlab.example", Timeout: time.Minute},
		"bad group":      {BaseURL: "https://gitlab.example", AllowedGroups: []string{"../x"}, Timeout: time.Minute},
		"userinfo":       {BaseURL: "https://u@gitlab.example", AllowedGroups: []string{"grp"}, Timeout: time.Minute},
		"trailing slash": {BaseURL: "https://gitlab.example/", AllowedGroups: []string{"grp"}, Timeout: time.Minute},
		"file scheme":    {BaseURL: "file:///srv/git", AllowedGroups: []string{"grp"}, Timeout: time.Minute},
		"no timeout":     {BaseURL: "https://gitlab.example", AllowedGroups: []string{"grp"}},
	} {
		s := *spec
		b := bad
		s.Fetch = &b
		if err := s.Validate(); err == nil {
			t.Errorf("%s: accepted", name)
		}
	}
}

func git(t testing.TB, dir string, args ...string) string {
	t.Helper()
	cmd := exec.Command("git", append([]string{"-c", "user.name=t", "-c", "user.email=t@example.com", "-c", "init.defaultBranch=main"}, args...)...)
	cmd.Dir = dir
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("git %v: %v\n%s", args, err, out)
	}
	return string(out)
}

// fetchFixture is a bare project grp/proj under a file:// base, and an empty repository to
// fetch into. Validate refuses a file base; the server's own check is what the test covers.
func fetchFixture(t *testing.T) (*Spec, string) {
	t.Helper()
	tmp := t.TempDir()
	work := filepath.Join(tmp, "work")
	os.MkdirAll(work, 0o755)
	git(t, work, "init", "-q")
	os.WriteFile(filepath.Join(work, "handler.go"), []byte("package handler\n"), 0o644)
	git(t, work, "add", "handler.go")
	git(t, work, "commit", "-q", "-m", "init")
	origin := filepath.Join(tmp, "origin")
	os.MkdirAll(filepath.Join(origin, "grp"), 0o755)
	git(t, tmp, "clone", "-q", "--bare", work, filepath.Join(origin, "grp", "proj.git"))

	root := filepath.Join(tmp, "repo")
	os.MkdirAll(root, 0o755)
	return &Spec{Version: SpecVersion, RepoRoot: root, Before: testState(t),
		Fetch: &FetchSpec{BaseURL: "file://" + origin, AllowedGroups: []string{"grp"}, Timeout: time.Minute}}, root
}

func TestFetch_ClonesAnAllowedProjectOnce(t *testing.T) {
	spec, root := fetchFixture(t)
	g := &fakeGuard{}
	tools, dir := newTools(t, spec, &fakeRunner{}, g)
	url := spec.Fetch.BaseURL + "/grp/proj/-/blob/main/handler.go"

	res := tools.Call(context.Background(), domain.VerifyToolFetchRepository, args(t, "url", url))
	if res.IsError {
		t.Fatalf("refused: %s", res.Text)
	}
	for _, want := range []string{"fetched: grp/proj at ", "path: " + FetchDir + "/proj\n", "calls left in this step: 9"} {
		if !strings.Contains(res.Text, want) {
			t.Errorf("answer lacks %q:\n%s", want, res.Text)
		}
	}
	if _, err := os.Stat(filepath.Join(root, FetchDir, "proj", "handler.go")); err != nil {
		t.Errorf("clone: %v", err)
	}
	if g.calls != 1 {
		t.Errorf("guard checked %d times", g.calls)
	}

	// The same project again: answered from the clone, nothing runs.
	res = tools.Call(context.Background(), domain.VerifyToolFetchRepository, args(t, "url", spec.Fetch.BaseURL+"/grp/proj"))
	if res.IsError || !strings.Contains(res.Text, "already fetched: grp/proj") {
		t.Errorf("second call: %v %s", res.IsError, res.Text)
	}

	// Another group, a bad shape, an unknown argument: refused before anything runs.
	for _, bad := range []struct {
		args   []any
		reason string
	}{
		{[]any{"url", spec.Fetch.BaseURL + "/other/proj"}, reasonURLGroup},
		{[]any{"url", "https://gitlab.example/grp/proj"}, reasonURLNotGitLab},
		{[]any{"url", 3}, reasonURLType},
		{[]any{"repo", "x"}, reasonUnknownArg},
		{nil, reasonURLRequired},
	} {
		res = tools.Call(context.Background(), domain.VerifyToolFetchRepository, args(t, bad.args...))
		if !res.IsError || !strings.Contains(res.Text, bad.reason) {
			t.Errorf("%v: got %v %q", bad.args, res.IsError, res.Text)
		}
	}
	if g.calls != 2 {
		t.Errorf("refused calls reached the guard: %d checks", g.calls)
	}

	calls := readCalls(t, dir)
	if len(calls) != 7 {
		t.Fatalf("%d calls recorded", len(calls))
	}
	for i, c := range calls {
		if err := c.Validate(); err != nil {
			t.Errorf("[%d] %v", i, err)
		}
	}
	first := calls[0]
	if first.Tool != domain.VerifyToolFetchRepository || first.Repository != "grp/proj" || !first.Success || first.Duration <= 0 || first.ExitCode != 0 {
		t.Errorf("first record: %+v", first)
	}
	if calls[1].Repository != "grp/proj" || !calls[1].Success || calls[1].Refused != "" {
		t.Errorf("second record: %+v", calls[1])
	}
	if calls[2].Refused == "" || calls[2].Repository != "" {
		t.Errorf("refused record: %+v", calls[2])
	}
	if _, err := os.Stat(filepath.Join(dir, LogFile(1, "stderr"))); err != nil {
		t.Errorf("clone log: %v", err)
	}
}

func TestFetch_FailedCloneLeavesNothing(t *testing.T) {
	spec, root := fetchFixture(t)
	tools, dir := newTools(t, spec, &fakeRunner{}, &fakeGuard{})
	res := tools.Call(context.Background(), domain.VerifyToolFetchRepository, args(t, "url", spec.Fetch.BaseURL+"/grp/missing"))
	if !res.IsError || !strings.HasPrefix(res.Text, "failed: git clone ") {
		t.Fatalf("got %v %q", res.IsError, res.Text)
	}
	if _, err := os.Stat(filepath.Join(root, FetchDir, "missing")); !os.IsNotExist(err) {
		t.Errorf("destination left behind: %v", err)
	}
	calls := readCalls(t, dir)
	if len(calls) != 1 || calls[0].Success || calls[0].ExitCode != 1 || calls[0].Repository != "grp/missing" || calls[0].Validate() != nil {
		t.Errorf("record: %+v", calls)
	}
}

func TestFetch_GuardAndLimit(t *testing.T) {
	spec, root := fetchFixture(t)
	g := &fakeGuard{changed: []string{".github/workflows/ci.yml"}}
	tools, dir := newTools(t, spec, &fakeRunner{}, g)
	res := tools.Call(context.Background(), domain.VerifyToolFetchRepository, args(t, "url", spec.Fetch.BaseURL+"/grp/proj"))
	if !res.IsError || !strings.Contains(res.Text, "protected files changed") {
		t.Fatalf("got %v %q", res.IsError, res.Text)
	}
	if _, err := os.Stat(filepath.Join(dir, ViolationFile)); err != nil {
		t.Errorf("violation: %v", err)
	}
	if _, err := os.Stat(filepath.Join(root, FetchDir)); !os.IsNotExist(err) {
		t.Errorf("a refused call cloned: %v", err)
	}

	spec, _ = fetchFixture(t)
	tools, _ = newTools(t, spec, &fakeRunner{}, &fakeGuard{})
	for i := 0; i < domain.MaxFetchCalls; i++ {
		tools.Call(context.Background(), domain.VerifyToolFetchRepository, args(t, "url", spec.Fetch.BaseURL+"/other/x"))
	}
	res = tools.Call(context.Background(), domain.VerifyToolFetchRepository, args(t, "url", spec.Fetch.BaseURL+"/grp/proj"))
	if !res.IsError || !strings.Contains(res.Text, "limit reached") {
		t.Errorf("over the limit: %v %q", res.IsError, res.Text)
	}
}

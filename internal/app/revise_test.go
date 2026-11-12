package app

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/kasorse/orch/internal/apperr"
	"github.com/kasorse/orch/internal/config"
	"github.com/kasorse/orch/internal/domain"
	"github.com/kasorse/orch/internal/execution"
	"github.com/kasorse/orch/internal/gitlab"
	"github.com/kasorse/orch/internal/jira"
	"github.com/kasorse/orch/internal/persistence"
	"github.com/kasorse/orch/internal/repository"
	"github.com/kasorse/orch/internal/testutil/fakeexec"
)

// fakeGitLab answers resolution's reads from a table of merge requests.
type fakeGitLab struct {
	scopeErr error
	mrs      map[int]domain.MergeRequest
	byBranch map[string]domain.MergeRequest
	asked    []int
}

func (f *fakeGitLab) CheckReadOnly(context.Context) error { return f.scopeErr }

func (f *fakeGitLab) MergeRequest(_ context.Context, project string, iid int) (domain.MergeRequest, error) {
	f.asked = append(f.asked, iid)
	mr, ok := f.mrs[iid]
	if !ok || mr.Project != project {
		return domain.MergeRequest{}, &apperr.GitLabError{Kind: apperr.GitLabNotFound}
	}
	return mr, nil
}

func (f *fakeGitLab) LatestMergeRequest(_ context.Context, project, branch string) (domain.MergeRequest, bool, error) {
	mr, ok := f.byBranch[branch]
	return mr, ok, nil
}

type fakeReviseJira struct{ links []jira.RemoteLink }

func (f fakeReviseJira) Ticket(_ context.Context, key string) (domain.Ticket, string, error) {
	return domain.Ticket{Key: key, URL: "https://jira.example/browse/" + key, FetchedAt: time.Now()}, "PAY-31: Round totals\n\nThe ticket.", nil
}

func (f fakeReviseJira) RemoteLinks(context.Context, string) ([]jira.RemoteLink, error) {
	return f.links, nil
}

const reviseGitLabBase = "https://gitlab.example.com"

func reviseMR(project string, iid int, state string, created time.Time) domain.MergeRequest {
	return domain.MergeRequest{
		Project: project, IID: iid, URL: gitlab.MergeRequestURL(reviseGitLabBase, project, iid), State: state,
		SourceBranch: fmt.Sprintf("fix/PAY-31-%d", iid), TargetBranch: "main", SHA: strings.Repeat("b", 40),
		CreatedAt: created, Title: fmt.Sprintf("PAY-31: Round totals (%d)", iid), Description: "MR body",
	}
}

func reviseFixture(t *testing.T, gl *fakeGitLab, links []jira.RemoteLink) (reviseDeps, map[string]*domain.Run, *bytes.Buffer) {
	t.Helper()
	dir := t.TempDir()
	gitIn(t, dir, "init", "-q", "-b", "main")
	gitIn(t, dir, "remote", "add", "origin", "git@gitlab.example.com:acme/payments/billing.git")
	runs := map[string]*domain.Run{}
	load := func(id string) (*domain.Run, error) {
		if r, ok := runs[id]; ok {
			return r, nil
		}
		return persistence.NewStore(t.TempDir()).LoadRun(id)
	}
	cfg := config.Config{GitLab: &config.GitLabConfig{BaseURL: reviseGitLabBase, AllowedGroups: []string{"acme/payments"}, APITokenEnv: "GITLAB_READ_TOKEN"}}
	var out bytes.Buffer
	return reviseDeps{
		cfg: cfg, repo: &repository.Repository{Dir: dir, Executor: execution.NewOSProcessExecutor()}, loadRun: load, gitlab: gl,
		jira: func() (reviseJira, error) { return fakeReviseJira{links: links}, nil }, out: &out,
	}, runs, &out
}

func TestReviseTargetKind(t *testing.T) {
	cfg := config.Config{GitLab: &config.GitLabConfig{BaseURL: reviseGitLabBase}, Jira: &config.JiraConfig{BaseURL: "https://jira.example"}}
	for target, want := range map[string]string{
		"PAY-31":                             domain.ReviseTargetJira,
		"https://jira.example/browse/PAY-31": domain.ReviseTargetJira,
		reviseGitLabBase + "/acme/payments/billing/-/merge_requests/1294": domain.ReviseTargetMR,
		"0f8fad5b-d9cb-469f-a165-70867728950e":                            domain.ReviseTargetRun,
		"fix/PAY-31-round":                                                domain.ReviseTargetBranch,
		"pay-31":                                                          domain.ReviseTargetBranch,
	} {
		got, err := reviseTargetKind(cfg, target)
		if err != nil || got != want {
			t.Errorf("%s: %q, %v; want %q", target, got, err, want)
		}
	}
	for _, bad := range []string{"https://evil.example/a/b/-/merge_requests/1", "a b", "-x", reviseGitLabBase + "/a/b/-/issues/1",
		"https://evil.example/browse/PAY-31", "https://jira.example/browse/not-a-key"} {
		if _, err := reviseTargetKind(cfg, bad); err == nil {
			t.Errorf("%s accepted", bad)
		}
	}
}

func TestRemoteProject(t *testing.T) {
	for remote, want := range map[string]string{
		"git@gitlab.example.com:acme/payments/billing.git":          "acme/payments/billing",
		"https://gitlab.example.com/acme/payments/billing.git":      "acme/payments/billing",
		"https://u:p@gitlab.example.com/acme/payments/billing":      "acme/payments/billing",
		"ssh://git@gitlab.example.com:22/acme/payments/billing.git": "acme/payments/billing",
	} {
		if got, ok := remoteProject(remote, reviseGitLabBase); !ok || got != want {
			t.Errorf("%s: %q %v", remote, got, ok)
		}
	}
	if got, ok := remoteProject("https://h.example/gitlab/a/b.git", "https://h.example/gitlab"); !ok || got != "a/b" {
		t.Errorf("context path: %q %v", got, ok)
	}
	if _, ok := remoteProject("git@github.com:a/b.git", reviseGitLabBase); ok {
		t.Error("another host accepted")
	}
}

func TestResolveRevise_MergeRequestURL(t *testing.T) {
	p := "acme/payments/billing"
	gl := &fakeGitLab{mrs: map[int]domain.MergeRequest{1294: reviseMR(p, 1294, domain.MRStateOpened, time.Now())}}
	d, _, out := reviseFixture(t, gl, nil)
	res, err := resolveRevise(context.Background(), d, reviseRequest{Target: reviseGitLabBase + "/" + p + "/-/merge_requests/1294", Requirement: "Round half up\nKeep VAT"})
	if err != nil {
		t.Fatal(err)
	}
	r := res.source.Revise
	if res.source.Kind != domain.TaskSourceRevise || r.Mode != domain.ReviseUpdate || r.Branch != "fix/PAY-31-1294" ||
		r.BaseRef != strings.Repeat("b", 40) || r.BaseBranch != "main" || r.Project != p || res.source.Key != "" {
		t.Fatalf("source = %+v, revise = %+v", res.source, r)
	}
	if !strings.HasPrefix(res.description, "Round half up\nKeep VAT\n\n---\n") || !strings.Contains(res.description, "MR body") {
		t.Fatalf("description = %q", res.description)
	}
	if len(res.acceptance) != 2 || res.acceptance[1] != "Keep VAT" {
		t.Fatalf("acceptance = %q", res.acceptance)
	}
	if !strings.Contains(out.String(), "Revise !1294 (opened) on fix/PAY-31-1294 in acme/payments/billing") {
		t.Fatalf("out = %q", out.String())
	}
}

func TestResolveRevise_JiraTakesTheLatestOfThisProject(t *testing.T) {
	p := "acme/payments/billing"
	now := time.Now()
	gl := &fakeGitLab{mrs: map[int]domain.MergeRequest{
		1290: reviseMR(p, 1290, domain.MRStateClosed, now.Add(-3*time.Hour)),
		1293: reviseMR(p, 1293, domain.MRStateOpened, now.Add(-1*time.Hour)),
		1294: reviseMR(p, 1294, domain.MRStateMerged, now.Add(-2*time.Hour)),
	}}
	links := []jira.RemoteLink{
		{URL: reviseGitLabBase + "/" + p + "/-/merge_requests/1290"},
		{URL: reviseGitLabBase + "/" + p + "/-/merge_requests/1293"},
		{URL: reviseGitLabBase + "/" + p + "/-/merge_requests/1294"},
		{URL: reviseGitLabBase + "/acme/payments/other/-/merge_requests/9"},
		{URL: "https://confluence.example/page"},
	}
	for _, target := range []string{"PAY-31", "https://jira.example/browse/PAY-31"} {
		d, _, _ := reviseFixture(t, gl, links)
		d.cfg.Jira = &config.JiraConfig{BaseURL: "https://jira.example"}
		res, err := resolveRevise(context.Background(), d, reviseRequest{Target: target, Requirement: "Round half up"})
		if err != nil {
			t.Fatalf("%s: %v", target, err)
		}
		if r := res.source.Revise; r.MR.IID != 1293 || r.Mode != domain.ReviseUpdate || res.source.Key != "PAY-31" || r.Target != "PAY-31" || res.ticket == nil {
			t.Fatalf("%s: revise = %+v, key %q", target, r, res.source.Key)
		}
		if !strings.Contains(res.description, "The ticket.") {
			t.Fatalf("description = %q", res.description)
		}
	}
}

func TestResolveRevise_MergedIsANewMergeRequest(t *testing.T) {
	p := "acme/payments/billing"
	gl := &fakeGitLab{mrs: map[int]domain.MergeRequest{7: reviseMR(p, 7, domain.MRStateMerged, time.Now())}}
	d, _, out := reviseFixture(t, gl, nil)
	res, err := resolveRevise(context.Background(), d, reviseRequest{Target: reviseGitLabBase + "/" + p + "/-/merge_requests/7", Requirement: "x"})
	if err != nil {
		t.Fatal(err)
	}
	if r := res.source.Revise; r.Mode != domain.ReviseNew || r.Branch != "" || r.BaseRef != "" || r.BaseBranch != "main" {
		t.Fatalf("revise = %+v", r)
	}
	if !strings.Contains(out.String(), "!7 is merged: a new branch from main and a new merge request") {
		t.Fatalf("out = %q", out.String())
	}
}

func TestResolveRevise_Refusals(t *testing.T) {
	p := "acme/payments/billing"
	fork := reviseMR(p, 2, domain.MRStateOpened, time.Now())
	fork.Fork = true
	locked := reviseMR(p, 3, domain.MRStateLocked, time.Now())
	cases := []struct {
		name   string
		gl     *fakeGitLab
		links  []jira.RemoteLink
		target string
		want   string
	}{
		{"scope", &fakeGitLab{scopeErr: &apperr.GitLabError{Kind: apperr.GitLabScope, Detail: "must name a read-only token"}}, nil, "PAY-31", "read-only"},
		{"other project", &fakeGitLab{}, nil, reviseGitLabBase + "/acme/payments/other/-/merge_requests/1", "run revise in a checkout of it"},
		{"fork", &fakeGitLab{mrs: map[int]domain.MergeRequest{2: fork}}, nil, reviseGitLabBase + "/" + p + "/-/merge_requests/2", "fork"},
		{"locked", &fakeGitLab{mrs: map[int]domain.MergeRequest{3: locked}}, nil, reviseGitLabBase + "/" + p + "/-/merge_requests/3", "locked"},
		{"no links", &fakeGitLab{}, nil, "PAY-31", "has no merge request; use orchestrator run --jira PAY-31"},
		{"links elsewhere", &fakeGitLab{}, []jira.RemoteLink{{URL: reviseGitLabBase + "/acme/payments/other/-/merge_requests/9"}}, "PAY-31", "it has: acme/payments/other!9"},
		{"run missing", &fakeGitLab{}, nil, "0f8fad5b-d9cb-469f-a165-70867728950e", "no such run"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			d, _, _ := reviseFixture(t, tc.gl, tc.links)
			_, err := resolveRevise(context.Background(), d, reviseRequest{Target: tc.target, Requirement: "x"})
			if err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("err = %v, want %q", err, tc.want)
			}
		})
	}
}

func TestResolveRevise_RunTarget(t *testing.T) {
	p := "acme/payments/billing"
	gl := &fakeGitLab{mrs: map[int]domain.MergeRequest{12: reviseMR(p, 12, domain.MRStateOpened, time.Now())}}
	d, runs, _ := reviseFixture(t, gl, nil)
	run := &domain.Run{ID: "0f8fad5b-d9cb-469f-a165-70867728950e", Task: domain.Task{Description: "Round the totals", CreatedAt: time.Now(),
		Source: &domain.TaskSource{Kind: domain.TaskSourceJira, Key: "PAY-31", URL: "https://jira.example/browse/PAY-31", FetchedAt: time.Now()}}}
	run.MR = &domain.RunMR{State: domain.MRCreated, Remote: "origin", Source: "fix/PAY-31-12", Target: "main", IID: 12, URL: gitlab.MergeRequestURL(reviseGitLabBase, p, 12)}
	runs[run.ID] = run
	res, err := resolveRevise(context.Background(), d, reviseRequest{Target: run.ID, Requirement: "x"})
	if err != nil {
		t.Fatal(err)
	}
	if r := res.source.Revise; r.FromRun != run.ID || r.MR.IID != 12 || res.source.Key != "PAY-31" || !strings.Contains(res.description, "Round the totals") {
		t.Fatalf("revise = %+v, key %q, description %q", r, res.source.Key, res.description)
	}
}

// A branch on the remote with no merge request: pushed to, with the operator's branch as target.
func TestResolveRevise_BranchWithoutMR(t *testing.T) {
	d, _, _ := reviseFixture(t, &fakeGitLab{}, nil)
	dir := d.repo.Dir
	gitIn(t, dir, "config", "user.email", "t@example.com")
	gitIn(t, dir, "config", "user.name", "T")
	if err := os.WriteFile(filepath.Join(dir, "a.txt"), []byte("a\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	gitIn(t, dir, "add", "-A")
	gitIn(t, dir, "commit", "-qm", "init")
	bare := fakeexec.AddGitLabRemote(t, dir)
	gitIn(t, dir, "push", "-q", "origin", "HEAD:refs/heads/fix/x")
	project := strings.TrimSuffix(strings.Trim(bare, "/"), ".git")
	d.cfg.GitLab.BaseURL = "https://" + fakeexec.GitLabHost
	d.cfg.GitLab.AllowedGroups = []string{project}

	res, err := resolveRevise(context.Background(), d, reviseRequest{Target: "fix/x", Requirement: "x"})
	if err != nil {
		t.Fatal(err)
	}
	head := gitIn(t, dir, "rev-parse", "HEAD")
	if r := res.source.Revise; r.Mode != domain.ReviseUpdate || r.MR != nil || r.Branch != "fix/x" || r.BaseRef != head || r.BaseBranch != "main" {
		t.Fatalf("revise = %+v", r)
	}
	if _, err := resolveRevise(context.Background(), d, reviseRequest{Target: "fix/absent", Requirement: "x"}); err == nil ||
		!strings.Contains(err.Error(), "has no branch fix/absent") {
		t.Fatalf("absent branch: err = %v", err)
	}
}

func TestReviseArgs(t *testing.T) {
	if _, err := reviseArgs([]string{"revise", "PAY-31"}, nil); err == nil {
		t.Fatal("no requirement accepted")
	}
	req, err := reviseArgs([]string{"revise", "PAY-31", "round", "half", "up"}, nil)
	if err != nil || req.Target != "PAY-31" || req.Requirement != "round half up" {
		t.Fatalf("%+v %v", req, err)
	}
	req, err = reviseArgs([]string{"revise", "PAY-31", "-"}, strings.NewReader("from\nstdin\n"))
	if err != nil || req.Requirement != "from\nstdin\n" {
		t.Fatalf("%+v %v", req, err)
	}
	if _, err := reviseArgs([]string{"revise", "PAY-31", "-"}, strings.NewReader(strings.Repeat("x", maxReviseRequirement+1))); err == nil {
		t.Fatal("too long accepted")
	}
	var cerr *apperr.ConfigError
	if _, err := reviseArgs([]string{"revise", "PAY-31", "  "}, nil); !errors.As(err, &cerr) {
		t.Fatalf("blank: %v", err)
	}
}

func TestReviseCriteria(t *testing.T) {
	got := reviseCriteria("one\n\n  two  \r\n" + strings.Repeat("x\n", 20))
	if len(got) != 10 || got[0] != "one" || got[1] != "two" {
		t.Fatalf("%q", got)
	}
}

func TestReviseJiraTarget(t *testing.T) {
	for target, want := range map[string]bool{
		"PAY-31": true, "https://jira.example/browse/PAY-31": true,
		"pay-31": false, reviseGitLabBase + "/a/b/-/merge_requests/1": false, "fix/PAY-31": false,
	} {
		if got := reviseJiraTarget(target); got != want {
			t.Errorf("%s: %v, want %v", target, got, want)
		}
	}
}

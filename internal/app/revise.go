package app

import (
	"context"
	"fmt"
	"io"
	"net/url"
	"os"
	"slices"
	"strings"
	"unicode/utf8"

	"github.com/kasorse/orch/internal/apperr"
	"github.com/kasorse/orch/internal/config"
	"github.com/kasorse/orch/internal/domain"
	"github.com/kasorse/orch/internal/gitlab"
	"github.com/kasorse/orch/internal/jira"
	"github.com/kasorse/orch/internal/logging"
	"github.com/kasorse/orch/internal/persistence"
	"github.com/kasorse/orch/internal/repository"
	"github.com/kasorse/orch/internal/story"
)

// orchestrator revise (specs/revise/SPEC.md): a new requirement for work that already opened a
// merge request. Go resolves the target into a domain.ReviseSource before the run exists, with no
// model call; a failure creates no run.

const (
	reviseUsage = `usage: orchestrator revise <KEY|merge-request-URL|run-id|branch> "<requirement>"   (- reads the requirement from stdin)`
	// reviseReason is what decision.json gives development, forced on for a revise task.
	reviseReason = "revise"
	// maxReviseOriginal bounds the original task carried into the revision's text.
	maxReviseOriginal = 32 << 10
	// maxReviseCandidates bounds how many merge request links of a ticket are read.
	maxReviseCandidates = 20
	// maxReviseRequirement bounds the requirement, as one Jira update holds it.
	maxReviseRequirement = 8 << 10
)

// reviseRequest is the command's input: the target as typed and the requirement.
type reviseRequest struct {
	Target, Requirement string
}

// reviseGitLab is what resolution needs of internal/gitlab; tests replace it.
type reviseGitLab interface {
	CheckReadOnly(ctx context.Context) error
	MergeRequest(ctx context.Context, project string, iid int) (domain.MergeRequest, error)
	LatestMergeRequest(ctx context.Context, project, branch string) (domain.MergeRequest, bool, error)
}

// reviseJira is what resolution needs of internal/jira: the ticket, rendered, and its web links.
type reviseJira interface {
	Ticket(ctx context.Context, key string) (domain.Ticket, string, error)
	RemoteLinks(ctx context.Context, key string) ([]jira.RemoteLink, error)
}

// reviseDeps are resolution's inputs other than the request.
type reviseDeps struct {
	cfg  config.Config
	repo *repository.Repository
	// loadRun reads a run target, with the persistence validators (Store.LoadRun).
	loadRun func(id string) (*domain.Run, error)
	gitlab  reviseGitLab
	jira    func() (reviseJira, error)
	out     io.Writer
}

// resolvedRevise is what the run is started with.
type resolvedRevise struct {
	source      domain.TaskSource
	description string
	acceptance  []string
	ticket      *domain.Ticket
}

// reviseTargetKind says what the target names, from its form alone (SPEC.md §1, A1).
func reviseTargetKind(cfg config.Config, target string) (string, error) {
	t := strings.TrimSpace(target)
	switch {
	case strings.Contains(t, "://"):
		if cfg.GitLab != nil {
			if _, _, ok := gitlab.ParseMergeRequestURL(cfg.GitLab.BaseURL, t); ok {
				return domain.ReviseTargetMR, nil
			}
		}
		return "", &apperr.ConfigError{Msg: "the revise target is a URL but not a merge request of gitlab.base_url: pass <base_url>/<group>/<project>/-/merge_requests/<n>"}
	case t == strings.ToUpper(t) && domain.ValidTicketKey(t):
		return domain.ReviseTargetJira, nil
	case persistence.IsRunID(t):
		return domain.ReviseTargetRun, nil
	case domain.ValidBranchName(t):
		return domain.ReviseTargetBranch, nil
	}
	return "", &apperr.ConfigError{Msg: fmt.Sprintf("revise target %q is not a Jira key, a merge request URL, a run ID or a branch name\n\n%s", t, reviseUsage)}
}

// resolveRevise turns the request into the run's task (SPEC.md §4, §5).
func resolveRevise(ctx context.Context, d reviseDeps, req reviseRequest) (resolvedRevise, error) {
	kind, err := reviseTargetKind(d.cfg, req.Target)
	if err != nil {
		return resolvedRevise{}, err
	}
	target := strings.TrimSpace(req.Target)
	if err := d.gitlab.CheckReadOnly(ctx); err != nil {
		return resolvedRevise{}, err
	}
	remote := d.cfg.Delivery.RemoteName()
	remoteURL, err := d.repo.RemoteURL(ctx, remote)
	if err != nil {
		return resolvedRevise{}, &apperr.ConfigError{Msg: fmt.Sprintf("revise pushes to %s, whose URL cannot be read", remote), Err: err}
	}
	project, ok := remoteProject(remoteURL, d.cfg.GitLab.BaseURL)
	if !ok || !gitlab.Allowed(d.cfg.GitLab.AllowedGroups, project) {
		return resolvedRevise{}, &apperr.ConfigError{Msg: fmt.Sprintf("the remote %s is not a project of gitlab.base_url under gitlab.allowed_groups; run revise in a checkout of the merge request's project", remote)}
	}

	src := domain.ReviseSource{Target: target, TargetKind: kind, Project: project}
	res := resolvedRevise{}
	var original string
	var mr *domain.MergeRequest
	switch kind {
	case domain.ReviseTargetMR:
		p, iid, _ := gitlab.ParseMergeRequestURL(d.cfg.GitLab.BaseURL, target)
		if p != project {
			return resolvedRevise{}, &apperr.ConfigError{Msg: fmt.Sprintf("!%d is in %s, not %s; run revise in a checkout of it", iid, p, project)}
		}
		got, err := d.gitlab.MergeRequest(ctx, p, iid)
		if err != nil {
			return resolvedRevise{}, err
		}
		mr, original = &got, mrText(got)
	case domain.ReviseTargetJira:
		client, err := d.jira()
		if err != nil {
			return resolvedRevise{}, err
		}
		ticket, text, err := client.Ticket(ctx, target)
		if err != nil {
			return resolvedRevise{}, err
		}
		got, err := latestTicketMR(ctx, d, client, ticket.Key, project)
		if err != nil {
			return resolvedRevise{}, err
		}
		mr, original, res.ticket = &got, text, &ticket
		res.source.Key, res.source.URL, res.source.FetchedAt = ticket.Key, ticket.URL, ticket.FetchedAt
	case domain.ReviseTargetRun:
		got, text, key, branch, err := runTarget(ctx, d, target, project)
		if err != nil {
			return resolvedRevise{}, err
		}
		original, res.source.Key, src.FromRun = text, key, target
		if got != nil {
			mr = got
		} else if err := branchWithoutMR(ctx, d, &src, branch); err != nil {
			return resolvedRevise{}, err
		}
	case domain.ReviseTargetBranch:
		got, found, err := d.gitlab.LatestMergeRequest(ctx, project, target)
		if err != nil {
			return resolvedRevise{}, err
		}
		if found {
			mr, original = &got, mrText(got)
		} else if err := branchWithoutMR(ctx, d, &src, target); err != nil {
			return resolvedRevise{}, err
		}
	}
	if mr != nil {
		if err := applyMR(&src, *mr); err != nil {
			return resolvedRevise{}, err
		}
	}
	src.Project = project
	res.source.Kind, res.source.Revise = domain.TaskSourceRevise, &src
	res.description = reviseDescription(req.Requirement, src, original)
	res.acceptance = reviseCriteria(req.Requirement)
	fmt.Fprintln(d.out, reviseLine(src))
	return res, nil
}

// applyMR decides update or new from the merge request's state (SPEC.md §5).
func applyMR(src *domain.ReviseSource, mr domain.MergeRequest) error {
	label := fmt.Sprintf("!%d", mr.IID)
	switch {
	case mr.Fork:
		return &apperr.ConfigError{Msg: label + " comes from a fork; revise pushes only to its own project's branches"}
	case mr.State == domain.MRStateLocked:
		return &apperr.ConfigError{Msg: label + " is locked; try again when GitLab has finished with it"}
	case domain.CheckBranchName(mr.TargetBranch) != nil:
		return &apperr.ConfigError{Msg: fmt.Sprintf("%s targets %q, which a worktree run cannot use as its base", label, mr.TargetBranch)}
	}
	src.MR, src.BaseBranch = &mr, mr.TargetBranch
	if mr.State != domain.MRStateOpened {
		src.Mode = domain.ReviseNew
		return nil
	}
	if err := domain.CheckBranchName(mr.SourceBranch); err != nil {
		return &apperr.ConfigError{Msg: fmt.Sprintf("%s's branch %q cannot be revised here: %v", label, mr.SourceBranch, err)}
	}
	src.Mode, src.Branch, src.BaseRef = domain.ReviseUpdate, mr.SourceBranch, mr.SHA
	return nil
}

// branchWithoutMR is a branch on the remote that no merge request comes from: the revision is
// pushed to it and opens one (A5). Its base branch is the operator's current branch.
func branchWithoutMR(ctx context.Context, d reviseDeps, src *domain.ReviseSource, branch string) error {
	if err := domain.CheckBranchName(branch); err != nil {
		return &apperr.ConfigError{Msg: fmt.Sprintf("branch %q cannot be revised: %v", branch, err)}
	}
	remote := d.cfg.Delivery.RemoteName()
	sha, present, err := d.repo.LsRemoteHeadSHA(ctx, remote, branch)
	if err != nil {
		return &apperr.WorkflowError{Msg: fmt.Sprintf("cannot ask %s for %s", remote, branch), Err: err}
	}
	if !present {
		return &apperr.ConfigError{Msg: fmt.Sprintf("%s has no branch %s and no merge request comes from it; for new work use orchestrator run", remote, branch)}
	}
	base, err := d.repo.Branch(ctx)
	if err != nil {
		return err
	}
	if base == "" {
		base = d.cfg.Delivery.TargetBranch
	}
	if base == "" || base == branch {
		return &apperr.ConfigError{Msg: fmt.Sprintf("the merge request from %s needs a target: check out the branch it should go to, or set delivery.target_branch", branch)}
	}
	src.Mode, src.Branch, src.BaseRef, src.BaseBranch = domain.ReviseUpdate, branch, sha, base
	return nil
}

// latestTicketMR reads the merge requests the ticket's web links name in this project and takes
// the one created last (R8, A3).
func latestTicketMR(ctx context.Context, d reviseDeps, client reviseJira, key, project string) (domain.MergeRequest, error) {
	links, err := client.RemoteLinks(ctx, key)
	if err != nil {
		return domain.MergeRequest{}, err
	}
	var iids []int
	var elsewhere []string
	for _, l := range links {
		p, iid, ok := gitlab.ParseMergeRequestURL(d.cfg.GitLab.BaseURL, l.URL)
		switch {
		case !ok:
		case p == project:
			if !slices.Contains(iids, iid) {
				iids = append(iids, iid)
			}
		default:
			elsewhere = append(elsewhere, fmt.Sprintf("%s!%d", p, iid))
		}
	}
	if len(iids) == 0 {
		if len(elsewhere) > 0 {
			return domain.MergeRequest{}, &apperr.ConfigError{Msg: fmt.Sprintf("%s has no merge request of %s (it has: %s); run revise in a checkout of that project",
				key, project, strings.Join(elsewhere, ", "))}
		}
		return domain.MergeRequest{}, &apperr.ConfigError{Msg: fmt.Sprintf("%s has no merge request; use orchestrator run --jira %s", key, key)}
	}
	// The highest iids are the newest in one project; only those are read.
	slices.Sort(iids)
	if len(iids) > maxReviseCandidates {
		iids = iids[len(iids)-maxReviseCandidates:]
	}
	var latest domain.MergeRequest
	for _, iid := range iids {
		mr, err := d.gitlab.MergeRequest(ctx, project, iid)
		if err != nil {
			return domain.MergeRequest{}, err
		}
		if latest.IID == 0 || mr.CreatedAt.After(latest.CreatedAt) {
			latest = mr
		}
	}
	return latest, nil
}

// runTarget reads an earlier run of this repository: its merge request, or its pushed branch, and
// its task (SPEC.md §4).
func runTarget(ctx context.Context, d reviseDeps, id, project string) (mr *domain.MergeRequest, text, key, branch string, err error) {
	r, err := d.loadRun(id)
	if err != nil {
		return nil, "", "", "", err
	}
	text, key = r.Task.Description, r.Task.Source.JiraKey()
	if c := r.Task.Clarification; c != nil {
		if rendered := c.Render(); rendered != "" {
			text += "\n\n" + rendered
		}
	}
	if r.MR != nil && r.MR.URL != "" {
		p, iid, ok := gitlab.ParseMergeRequestURL(d.cfg.GitLab.BaseURL, r.MR.URL)
		if !ok || p != project {
			return nil, "", "", "", &apperr.ConfigError{Msg: fmt.Sprintf("run %s opened %s, which is not a merge request of %s", id, r.MR.URL, project)}
		}
		got, err := d.gitlab.MergeRequest(ctx, p, iid)
		if err != nil {
			return nil, "", "", "", err
		}
		return &got, text, key, "", nil
	}
	if r.MR != nil && (r.MR.State == domain.MRPushed || r.MR.State == domain.MRCreated) && r.Worktree != nil {
		return nil, text, key, r.Worktree.Branch, nil
	}
	return nil, "", "", "", &apperr.ConfigError{Msg: fmt.Sprintf("run %s pushed no branch; resume it (orchestrator resume %s) or start a new run", id, id)}
}

// remoteProject is the project path of a remote URL on baseURL's host: https://host/<base path>/
// group/project(.git), ssh://git@host/group/project.git or git@host:group/project.git.
func remoteProject(remoteURL, baseURL string) (string, bool) {
	base, err := url.Parse(baseURL)
	if err != nil {
		return "", false
	}
	if remoteHost(remoteURL) != strings.ToLower(base.Hostname()) {
		return "", false
	}
	raw := strings.TrimSpace(remoteURL)
	var path string
	if strings.Contains(raw, "://") {
		u, err := url.Parse(raw)
		if err != nil {
			return "", false
		}
		path = u.Path
		if u.Scheme == "https" || u.Scheme == "http" {
			path = strings.TrimPrefix(path, strings.TrimRight(base.Path, "/"))
		}
	} else {
		_, path, _ = strings.Cut(raw, ":")
	}
	path = strings.TrimSuffix(strings.Trim(path, "/"), ".git")
	return path, path != ""
}

// mrText is a merge request as the original task: its title and description.
func mrText(mr domain.MergeRequest) string {
	if d := strings.TrimSpace(mr.Description); d != "" {
		return mr.Title + "\n\n" + d
	}
	return mr.Title
}

// reviseDescription is the task text every stage and the decision read: the requirement first,
// then where it goes and the task the merge request was opened for, cut at maxReviseOriginal.
func reviseDescription(requirement string, src domain.ReviseSource, original string) string {
	var b strings.Builder
	b.WriteString(strings.TrimSpace(requirement))
	b.WriteString(reviseMarker)
	switch {
	case src.MR != nil && src.Mode == domain.ReviseUpdate:
		fmt.Fprintf(&b, "merge request !%d (%s) of %s, %q, on its branch %s: the change is a new commit on that branch.\n",
			src.MR.IID, src.MR.URL, src.Project, src.MR.Title, src.Branch)
	case src.MR != nil:
		fmt.Fprintf(&b, "merge request !%d (%s) of %s, %q, which is %s: the change starts from %s and opens a new merge request.\n",
			src.MR.IID, src.MR.URL, src.Project, src.MR.Title, src.MR.State, src.BaseBranch)
	default:
		fmt.Fprintf(&b, "the branch %s of %s: the change is a new commit on it.\n", src.Branch, src.Project)
	}
	if o := strings.TrimSpace(original); o != "" {
		if len(o) > maxReviseOriginal {
			cut := maxReviseOriginal
			for cut > 0 && !utf8.RuneStart(o[cut]) {
				cut--
			}
			o = o[:cut] + "\n…(cut)"
		}
		b.WriteString("The task it was opened for:\n\n")
		b.WriteString(o)
	}
	return b.String()
}

// reviseMarker starts what reviseDescription adds after the requirement.
const reviseMarker = "\n\n---\nThis requirement revises "

// reviseRequirement is the requirement out of a revise task's text: what comes before the marker.
func reviseRequirement(description string) string {
	requirement, _, _ := strings.Cut(description, reviseMarker)
	return requirement
}

// reviseCriteria are the requirement's lines as review criteria, within a story task's bounds.
func reviseCriteria(requirement string) []string {
	var items []string
	for _, line := range strings.Split(strings.ReplaceAll(requirement, "\r", "\n"), "\n") {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		if utf8.RuneCountInString(line) > story.MaxAcceptanceRunesCeiling {
			line = string([]rune(line)[:story.MaxAcceptanceRunesCeiling-1]) + "…"
		}
		if len(items) == story.MaxAcceptanceItems {
			break
		}
		items = append(items, line)
	}
	return items
}

// reviseLine is printed before the approval (SPEC.md §5).
func reviseLine(src domain.ReviseSource) string {
	switch {
	case src.Mode == domain.ReviseUpdate && src.MR != nil:
		return fmt.Sprintf("Revise !%d (%s) on %s in %s", src.MR.IID, src.MR.State, src.Branch, src.Project)
	case src.Mode == domain.ReviseUpdate:
		return fmt.Sprintf("Revise the branch %s in %s: a merge request into %s is opened by the push", src.Branch, src.Project, src.BaseBranch)
	}
	return fmt.Sprintf("!%d is %s: a new branch from %s and a new merge request", src.MR.IID, src.MR.State, src.BaseBranch)
}

// newReviseGitLab builds the read-only client from the gitlab section; the token is read here only.
func newReviseGitLab(cfg config.Config) reviseGitLab {
	g := cfg.GitLab
	return gitlab.NewHTTPClient(gitlab.Options{
		BaseURL: g.BaseURL, AllowedGroups: g.AllowedGroups, TokenEnv: g.APITokenEnv,
		UserAgent: "orchestrator/" + Version,
	}, gitlab.NewToken(os.Getenv(g.APITokenEnv)))
}

// jiraForRevise is the ticket's read for a Jira target: taskFromJira's fetch and render, and the
// write-back client's web links.
type jiraForRevise struct {
	cfg      config.Config
	client   *jira.Client
	reporter *logging.ConsoleReporter
}

func (j jiraForRevise) Ticket(ctx context.Context, key string) (domain.Ticket, string, error) {
	return taskFromJira(ctx, j.cfg, key, "", j.reporter)
}

func (j jiraForRevise) RemoteLinks(ctx context.Context, key string) ([]jira.RemoteLink, error) {
	return j.client.RemoteLinks(ctx, key)
}

// reviseArgs reads `revise <target> <requirement...>`; a requirement of "-" is read from stdin.
func reviseArgs(rest []string, stdin io.Reader) (reviseRequest, error) {
	if len(rest) < 3 || strings.TrimSpace(rest[1]) == "" {
		return reviseRequest{}, &apperr.ConfigError{Msg: reviseUsage}
	}
	req := reviseRequest{Target: strings.TrimSpace(rest[1]), Requirement: strings.Join(rest[2:], " ")}
	if len(rest) == 3 && rest[2] == "-" {
		data, err := io.ReadAll(io.LimitReader(stdin, maxReviseRequirement+1))
		if err != nil {
			return reviseRequest{}, &apperr.ConfigError{Msg: "cannot read the requirement from stdin", Err: err}
		}
		if len(data) > maxReviseRequirement {
			return reviseRequest{}, &apperr.ConfigError{Msg: fmt.Sprintf("the requirement is longer than %d bytes", maxReviseRequirement)}
		}
		req.Requirement = string(data)
	}
	switch {
	case strings.TrimSpace(req.Requirement) == "":
		return reviseRequest{}, &apperr.ConfigError{Msg: "the requirement is empty\n\n" + reviseUsage}
	case !utf8.ValidString(req.Requirement) || strings.ContainsRune(req.Requirement, 0):
		return reviseRequest{}, &apperr.ConfigError{Msg: "the requirement is not text"}
	case len(req.Requirement) > maxReviseRequirement:
		return reviseRequest{}, &apperr.ConfigError{Msg: fmt.Sprintf("the requirement is longer than %d bytes", maxReviseRequirement)}
	}
	return req, nil
}

// reviseJiraTarget reports whether the target is a Jira key, the form reviseTargetKind gives it.
func reviseJiraTarget(target string) bool {
	t := strings.TrimSpace(target)
	return !strings.Contains(t, "://") && t == strings.ToUpper(t) && domain.ValidTicketKey(t)
}

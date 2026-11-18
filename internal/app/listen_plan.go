package app

import (
	"context"
	"errors"
	"fmt"
	"maps"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"time"

	"github.com/kasorse/orch/internal/action"
	"github.com/kasorse/orch/internal/apperr"
	"github.com/kasorse/orch/internal/config"
	"github.com/kasorse/orch/internal/domain"
	"github.com/kasorse/orch/internal/gitlab"
	"github.com/kasorse/orch/internal/jira"
	"github.com/kasorse/orch/internal/listener"
	"github.com/kasorse/orch/internal/persistence"
	"github.com/kasorse/orch/internal/secretscan"
	"github.com/kasorse/orch/internal/story"
)

// The plan of an Epic (specs/listener-story/SPEC.md §5–§7, §9): with listener.story an Epic is
// claimed and planned, alone (§3), up to a validated plan saved in
// .orchestrator/listener/plans/<KEY>.<n>/. The model only proposes: Go fixes the catalog it chooses
// from, the services it may target, the order of the tasks of one service, and what each call costs.
// The validated plan becomes the Epic's children, their links and its comment in listen_children.go.

const (
	// listenerPlansDir is the plans' directory in the listener's own, listenerStateDir.
	listenerPlansDir = "plans"
	// planRecordVersion is the "v" of plan.json.
	planRecordVersion = 1
)

// planGitLab is what the plan needs of internal/gitlab; tests replace it.
type planGitLab interface {
	GroupProjects(ctx context.Context, group string) ([]domain.Project, error)
}

// newListenerGitLab builds the read-only client of the Epic's catalog from the gitlab section; the
// API token is read here only (SPEC §10).
func newListenerGitLab(cfg config.Config, logf func(format string, args ...any)) *gitlab.Client {
	g := cfg.GitLab
	return gitlab.NewHTTPClient(gitlab.Options{
		BaseURL: g.BaseURL, AllowedGroups: g.AllowedGroups, TokenEnv: g.APITokenEnv,
		UserAgent: "orchestrator/" + Version, HTTPClient: gitlabHTTPClient, Logf: logf,
	}, gitlab.NewToken(os.Getenv(g.APITokenEnv)))
}

// planTarget is a repository of a plan: a service the tasks change, or a reference.
type planTarget struct {
	Name string `json:"name"`
	Path string `json:"path"`
	// Why is the selection's reason for a service; empty for a reference and for a gitlab: label.
	Why string `json:"why,omitempty"`
	// Description is the catalog's description of a selected service, for specify's notes.
	Description string `json:"description,omitempty"`
}

// planTask is one task of the plan as plan.json records it.
type planTask struct {
	ID        string   `json:"id"`
	Service   string   `json:"service"`
	Title     string   `json:"title"`
	DependsOn []string `json:"depends_on,omitempty"`
	// BlockedBy is the previous task of the same service (SPEC §7.2), "" for the first.
	BlockedBy string `json:"blocked_by,omitempty"`
	// Key is the issue created for the task, once there is one.
	Key string `json:"key,omitempty"`
}

// planRecord is plan.json: what the plan came to, in the plan's directory, also when it failed.
type planRecord struct {
	V       int    `json:"v"`
	Key     string `json:"key"`
	Attempt int    `json:"attempt"`
	// Outcome is what the Epic came to: blocked, failed, retry or stopped (the listener stopped, the
	// plan is made again on restart).
	Outcome string `json:"outcome,omitempty"`
	Reason  string `json:"reason,omitempty"`
	// Planned says the plan was validated: there is a spec, its tasks and their chain.
	Planned bool `json:"planned"`
	// Source is "labels" (gitlab: labels, no selection call) or "select".
	Source     string       `json:"source,omitempty"`
	Targets    []planTarget `json:"targets,omitempty"`
	References []planTarget `json:"references,omitempty"`
	// Requirements is the requirements call's answer (specs/listener-requirements/SPEC.md §7), and
	// Questions a copy of its questions, which made the Epic blocked when there are any.
	Requirements *story.RequirementsAnswer `json:"requirements,omitempty"`
	Questions    []string                  `json:"questions,omitempty"`
	Answer       *story.SpecifyAnswer      `json:"answer,omitempty"`
	Tasks        []planTask                `json:"tasks,omitempty"`
	Chain        []listener.Link           `json:"chain,omitempty"`
	// Children are the issues the plan created, in the story's order (empty until they are built).
	Children []string `json:"children"`
	CostUSD  float64  `json:"cost_usd"`
	// ChildStatus is the status one child was read back in, StatusWarning what the Epic's comment says
	// when it is not one the poll picks (SPEC §8.1).
	ChildStatus   string `json:"child_status,omitempty"`
	StatusWarning string `json:"status_warning,omitempty"`
}

// planEnd is how a stage of the plan ends it before its end: the outcome of SPEC §9, or quiet when
// the listener is stopping and nothing is written.
type planEnd struct {
	result listener.Result
	reason string
	quiet  bool
}

func blockedBy(reason string) *planEnd {
	return &planEnd{result: listener.ResultBlocked, reason: reason}
}

func failedBy(reason string) *planEnd {
	return &planEnd{result: listener.ResultFailed, reason: reason}
}

func retryBy(reason string) *planEnd {
	return &planEnd{result: listener.ResultRetry, reason: reason}
}

func quietEnd() *planEnd { return &planEnd{quiet: true} }

// planRun is one plan of an Epic in progress.
type planRun struct {
	r     *listenRunner
	key   string
	n     int
	plan  *persistence.StoryPlan
	rec   planRecord
	calls map[string]*domain.Usage
}

// plan is the cycle of a claimed Epic, in the exclusive slot (SPEC §3): the Epic's text, its labels,
// the catalog and the selection (or the gitlab: labels), the references' checkouts and surveys, the
// requirements and their comment (a question ends it blocked), specify, the validation and the chain
// (specs/listener-requirements/SPEC.md). It ends the Epic with one of the outcomes of SPEC §9; a listener
// that is stopping leaves it claimed, to be planned again from the start on restart (A10).
func (r *listenRunner) plan(key string) error {
	p := &planRun{r: r, key: key, calls: map[string]*domain.Usage{}, rec: planRecord{V: planRecordVersion, Key: key, Children: []string{}}}
	if r.stopping() {
		return nil
	}
	ticket, err := r.jira.Fetch(r.ctx, key)
	if err != nil {
		return r.jiraProblem(key, err)
	}
	comments, err := r.jira.Comments(r.ctx, key)
	if err != nil {
		return r.jiraProblem(key, err)
	}
	text, err := jira.Render(ticket, "", r.cfg.Jira.MaxBytes, jira.RenderOptions{Comments: comments, Bot: r.bot, FromFirst: true})
	if err != nil {
		return r.endBefore(key, listener.ResultBlocked,
			fmt.Sprintf("The Epic and its discussion are longer than %d bytes; shorten it.", r.cfg.Jira.MaxBytes))
	}

	// What the plan wrote before, and the children the listener reported under the Epic already, come
	// before the catalog (SPEC §9).
	end, restart := p.existing(ticket)
	if end != nil {
		return p.finish(end)
	}

	// The plan's directory: every call's prompt and answer goes in it, and plan.json at the end.
	plan, n, err := persistence.NewListenerPlan(r.plansDir, key, r.entryOf(key).Attempts+1)
	if err != nil {
		return r.endInfra(key, err)
	}
	p.plan, p.n, p.rec.Attempt = plan, n, n
	if err := plan.SaveTicket(ticket); err != nil {
		return r.endInfra(key, err)
	}
	if err := r.update(key, func(e *listener.Entry) { e.Kind, e.PlanDir = listener.KindPlan, listener.PlanDir(key, n) }); err != nil {
		return err
	}
	r.logf("%s: planning it in %s", key, listener.PlanDir(key, n))

	refs, fixed, end := p.labels(ticket)
	if end != nil {
		return p.finish(end)
	}
	p.rec.References = refs
	var targets []planTarget
	if len(fixed) > 0 {
		p.rec.Source = "labels"
		targets = fixed
	} else {
		p.rec.Source = "select"
		if targets, end = p.selectServices(text, refs); end != nil {
			return p.finish(end)
		}
	}
	p.rec.Targets = targets
	if err := sameNames(targets, refs); err != nil {
		return p.finish(blockedBy(err.Error()))
	}
	r.logf("%s: services: %s", key, describeTargets(targets))

	// The references only are checked out and surveyed: a target is studied by its child's run
	// (specs/listener-requirements/SPEC.md §6).
	dirs, end := p.checkouts(refs)
	if end != nil {
		return p.finish(end)
	}
	refSurveys, end := p.surveys(text, refs, dirs)
	if end != nil {
		return p.finish(end)
	}
	// The targets are checked out for their briefs: each one's architecture and business rules as far as
	// the task needs them, from its instructions and, where they fall short, its code.
	tdirs, briefs, end := p.briefs(text, targets)
	if end != nil {
		return p.finish(end)
	}
	req, end := p.requirements(text, refs, targets, dirs, tdirs, refSurveys, briefs)
	if end != nil {
		return p.finish(end)
	}
	answer, tasks, end := p.specify(text, targets, refSurveys, briefs, req)
	if end != nil {
		return p.finish(end)
	}

	// The plan is valid: the chain of each service's tasks (SPEC §7.2) and the record.
	ct := make([]listener.ChainTask, len(tasks))
	for i, t := range tasks {
		ct[i] = listener.ChainTask{ID: t.ID, Service: t.Repo}
	}
	chain := listener.Chain(ct, story.Order(tasks))
	waits := map[string]string{}
	for _, l := range chain {
		waits[l.Blocked] = l.Blocker
	}
	p.rec.Planned, p.rec.Answer, p.rec.Chain = true, &answer, chain
	for _, id := range story.Order(tasks) {
		for _, t := range tasks {
			if t.ID == id {
				p.rec.Tasks = append(p.rec.Tasks, planTask{ID: t.ID, Service: t.Repo, Title: t.Title, DependsOn: t.DependsOn, BlockedBy: waits[t.ID]})
			}
		}
	}
	r.logf("%s: the plan has %d tasks in %d services", key, len(tasks), len(targets))
	return p.createChildren(ticket, answer, req, tasks, targets, refs, restart)
}

// labels reads the Epic's labels (SPEC §5.2, §5.3): the references, and the services the gitlab:
// labels name when it has any. A bad label, too many of them, one outside the allowed groups, or
// two with one name, is blocked.
func (p *planRun) labels(t domain.Ticket) (refs, fixed []planTarget, end *planEnd) {
	r, sc := p.r, p.r.lc.Story
	refPaths, err := listener.Refs(t.Labels, r.lc.Labels.RefPrefix, sc.MaxRefs)
	if err != nil {
		return nil, nil, blockedBy(err.Error())
	}
	fixedPaths, err := listener.EpicProjects(t.Labels, r.lc.Labels.ProjectPrefix)
	if err != nil {
		return nil, nil, blockedBy(err.Error())
	}
	if len(fixedPaths) > sc.MaxServices {
		return nil, nil, blockedBy(fmt.Sprintf("The Epic has %d %s labels, more than listener.story.max_services (%d).",
			len(fixedPaths), r.lc.Labels.ProjectPrefix, sc.MaxServices))
	}
	for _, path := range slices.Concat(refPaths, fixedPaths) {
		if !listener.Allowed(path, r.checkout.allowedGroups) {
			return nil, nil, blockedBy(fmt.Sprintf("The project %s is not under an allowed GitLab group (%s).", path, strings.Join(r.checkout.allowedGroups, ", ")))
		}
	}
	if refs, err = nameProjects(refPaths, nil); err != nil {
		return nil, nil, blockedBy(err.Error())
	}
	if fixed, err = nameProjects(fixedPaths, nil); err != nil {
		return nil, nil, blockedBy(err.Error())
	}
	if err := sameNames(fixed, refs); err != nil {
		return nil, nil, blockedBy(err.Error())
	}
	return refs, fixed, nil
}

// nameProjects gives each path its service name, the path's last segment (why[path] is the
// selection's reason, when there is one). A name that is not a service name, or two paths with the
// same one (case folded), are errors: both would be one directory in the services directory.
func nameProjects(paths []string, why map[string]string) ([]planTarget, error) {
	var out []planTarget
	for _, path := range paths {
		name := listener.ServiceName(path)
		if !listener.ValidService(name) {
			return nil, fmt.Errorf("The project %s does not end in a service name (letters, digits, . _ -, starting with a letter or digit).", path)
		}
		for _, o := range out {
			if strings.EqualFold(o.Name, name) {
				return nil, fmt.Errorf("The projects %s and %s have the same name %s, so they would share one checkout.", o.Path, path, name)
			}
		}
		out = append(out, planTarget{Name: name, Path: path, Why: why[path]})
	}
	return out, nil
}

// sameNames refuses a reference that has a service's name: a reference is never a task's repository
// (SPEC §5.3).
func sameNames(targets, refs []planTarget) error {
	for _, ref := range refs {
		for _, t := range targets {
			if strings.EqualFold(ref.Name, t.Name) {
				return fmt.Errorf("The reference %s is also a service of the plan (%s): a reference is only read and gets no task.", ref.Path, t.Path)
			}
		}
	}
	return nil
}

// describeTargets is the services as the plan's log and comment name them: name (why), or the name.
func describeTargets(targets []planTarget) string {
	parts := make([]string, len(targets))
	for i, t := range targets {
		parts[i] = t.Name
		if t.Why != "" {
			parts[i] += " (" + t.Why + ")"
		}
	}
	return strings.Join(parts, ", ")
}

// selectServices is the selection of SPEC §5.2 for an Epic without gitlab: labels: the catalog of the
// allowed groups' projects, fetched once, and the select call over it.
func (p *planRun) selectServices(text string, refs []planTarget) ([]planTarget, *planEnd) {
	r, sc := p.r, p.r.lc.Story
	var lists [][]domain.Project
	for _, group := range r.checkout.allowedGroups {
		if r.stopping() {
			return nil, quietEnd()
		}
		projects, err := r.catalog.GroupProjects(r.ctx, strings.Trim(group, "/"))
		if err != nil {
			return nil, p.gitlabEnd(err)
		}
		lists = append(lists, projects)
	}
	catalog, cut := listener.Catalog(lists, sc.MaxCatalog)
	if cut > 0 {
		r.logf("%s: the catalog is cut to its first %d projects by path; %d are left out", p.key, sc.MaxCatalog, cut)
	}
	if len(catalog) == 0 {
		return nil, blockedBy("There is no project to choose the services from: the GitLab groups " +
			strings.Join(r.checkout.allowedGroups, ", ") + " list none. Label the Epic " + r.lc.Labels.ProjectPrefix + "<group>/<project>.")
	}
	if end := p.budget(); end != nil {
		return nil, end
	}
	inv := r.cfg.Actions.Investigate
	res, err := action.Select(r.ctx, p.call(inv.ProviderModel, inv.Timeout), action.SelectInput{
		Request: text, Catalog: catalog, References: targetPaths(refs), MaxServices: sc.MaxServices,
	})
	p.calls["select"] = res.Usage
	if err := saveCall(p.plan, "select", res.Answer, res.Output, res.Prompt, res.Usage, err); err != nil {
		return nil, p.callEnd(err)
	}
	why := map[string]string{}
	paths := make([]string, len(res.Answer))
	for i, c := range res.Answer {
		paths[i] = c.Path
		why[c.Path] = c.Why
	}
	targets, nerr := nameProjects(paths, why)
	if nerr != nil {
		return nil, failedBy(nerr.Error())
	}
	for i := range targets {
		for _, project := range catalog {
			if project.Path == targets[i].Path {
				targets[i].Description = project.Description
			}
		}
	}
	return targets, nil
}

func targetPaths(ts []planTarget) []string {
	paths := make([]string, len(ts))
	for i, t := range ts {
		paths[i] = t.Path
	}
	return paths
}

// checkouts makes every reference's checkout ready, through the listener's own (SPEC §5.3), and
// returns their directories by name. A target is not checked out: its child's run does it
// (specs/listener-requirements/SPEC.md §6).
func (p *planRun) checkouts(refs []planTarget) (map[string]string, *planEnd) {
	r := p.r
	dirs := map[string]string{}
	for _, t := range refs {
		if r.stopping() {
			return nil, quietEnd()
		}
		res, err := r.checkout.prepare(r.ctx, t.Name, t.Path)
		if err != nil {
			return nil, p.checkoutEnd(err)
		}
		dirs[t.Name] = res.Dir
	}
	return dirs, nil
}

// surveys is one survey call per reference, one after another (SPEC §6), on the listener's
// investigate route. No target is surveyed (specs/listener-requirements/SPEC.md §6).
func (p *planRun) surveys(text string, refs []planTarget, dirs map[string]string) (refSurveys []action.SurveyInput, end *planEnd) {
	for _, t := range refs {
		if p.r.stopping() {
			return nil, quietEnd()
		}
		if end := p.budget(); end != nil {
			return nil, end
		}
		repo, route, timeout := p.r.surveyRepo(t.Name, dirs[t.Name])
		p.r.logf("%s: surveying %s", p.key, t.Name)
		res, err := action.Survey(p.r.ctx, p.call(route, timeout), repo, text)
		name := "survey-" + t.Name
		p.calls[name] = res.Usage
		if err := saveCall(p.plan, name, res.Answer, res.Output, res.Prompt, res.Usage, err); err != nil {
			return nil, p.callEnd(err)
		}
		refSurveys = append(refSurveys, action.SurveyInput{Repo: t.Name, Answer: res.Answer})
	}
	return refSurveys, nil
}

// requirements is the requirements call and its comment (specs/listener-requirements/SPEC.md §2,
// §3): what the Epic asks for, from its text and its references' surveys, recorded in plan.json and,
// once secret-scanned, written on the Epic. With a question the plan ends blocked, and the comment is
// the outcome's (planBody); without one it is written here, and a failure to write it is logged.
func (p *planRun) requirements(text string, refs, targets []planTarget, dirs, tdirs map[string]string, refSurveys []action.SurveyInput, briefs []action.TargetBrief) (story.RequirementsAnswer, *planEnd) {
	r := p.r
	if r.stopping() {
		return story.RequirementsAnswer{}, quietEnd()
	}
	if end := p.budget(); end != nil {
		return story.RequirementsAnswer{}, end
	}
	r.logf("%s: stating the requirements", p.key)
	inv := r.cfg.Actions.Investigate
	res, err := action.Requirements(r.ctx, p.call(inv.ProviderModel, inv.Timeout), action.RequirementsInput{Request: text, References: refSurveys, Briefs: briefs})
	p.calls["requirements"] = res.Usage
	if err := saveCall(p.plan, "requirements", res.Answer, res.Output, res.Prompt, res.Usage, err); err != nil {
		return story.RequirementsAnswer{}, p.callEnd(err)
	}
	a := res.Answer
	r.logf("%s: requirements: %d, questions: %d", p.key, len(a.Requirements), len(a.Questions))
	if len(a.Questions) > 0 {
		if end := p.lookup(text, refs, targets, dirs, tdirs, &a); end != nil {
			return story.RequirementsAnswer{}, end
		}
	}

	// The model's text reaches Jira here: scanned as it wrote it and as it is sent (SPEC §3, B5).
	comment := listener.ComposeFor(jira.Escape, r.requirementsBody(p.key, refs, a), "")
	raw := strings.Join(slices.Concat([]string{a.Summary}, a.Requirements, a.Assumptions, a.Questions), "\n")
	for _, s := range []string{raw, comment} {
		if findings, _ := secretscan.Scan("comment", []byte(s)); len(findings) > 0 {
			return story.RequirementsAnswer{}, failedBy("The requirements look like they hold a secret, so nothing was written on the Epic.")
		}
	}
	p.rec.Requirements, p.rec.Questions = &a, slices.Clone(a.Questions)
	if n := len(a.Questions); n > 0 {
		questions := "questions"
		if n == 1 {
			questions = "question"
		}
		r.logf("%s: blocked by %d open %s", p.key, n, questions)
		return story.RequirementsAnswer{}, blockedBy(fmt.Sprintf("The requirements leave %d open %s: answer them in a comment and move the Epic back to %s.",
			n, questions, r.queueStatus()))
	}
	if r.stopping() {
		return story.RequirementsAnswer{}, quietEnd()
	}
	if _, err := r.jira.Comment(r.ctx, p.key, comment); err != nil {
		r.isAuth(err)
		r.logf("%s: cannot write the requirements comment, the plan goes on: %s", p.key, firstLineOf(err.Error()))
	}
	// What was decided without asking is a requirement for specify and the children like any other.
	out := a
	out.Requirements = slices.Clone(a.Requirements)
	for _, s := range a.Assumptions {
		out.Requirements = append(out.Requirements, "Assumed: "+s)
	}
	return out, nil
}

// instructionFiles are the files a target's brief is made from, in this order.
var instructionFiles = []string{"CLAUDE.md", "AGENTS.md", "README.md"}

// maxInstructionBytes bounds what of one file, and maxInstructionsBytes of all of a target's, goes to its brief.
const (
	maxInstructionBytes  = 24 << 10
	maxInstructionsBytes = 48 << 10
)

// instructions is the text of the instruction files in dir, each under its name; a file that is
// missing, empty or the same as one before is left out. "" when there is none.
func instructions(dir string) string {
	var parts, seen []string
	total := 0
	for _, name := range instructionFiles {
		data, err := os.ReadFile(filepath.Join(dir, name))
		text := strings.TrimSpace(string(data))
		if err != nil || text == "" || slices.Contains(seen, text) {
			continue
		}
		seen = append(seen, text)
		text = story.CutText(text, min(maxInstructionBytes, maxInstructionsBytes-total))
		if text == "" {
			break
		}
		total += len(text)
		parts = append(parts, "=== "+name+" ===\n"+text)
	}
	return strings.Join(parts, "\n\n")
}

// briefs checks out every target and makes its brief: its architecture and business rules as far as the
// task needs them, from its instructions and its code. A target whose checkout fails or whose brief call
// fails has no brief, which is logged; only a stop or the budget ends the plan. It returns the checkouts
// by name.
func (p *planRun) briefs(text string, targets []planTarget) (map[string]string, []action.TargetBrief, *planEnd) {
	r := p.r
	tdirs := map[string]string{}
	var briefs []action.TargetBrief
	for _, t := range targets {
		if r.stopping() {
			return nil, nil, quietEnd()
		}
		res, err := r.checkout.prepare(r.ctx, t.Name, t.Path)
		if err != nil {
			if r.stopping() {
				return nil, nil, quietEnd()
			}
			r.logf("%s: no brief of %s: the checkout failed: %s", p.key, t.Name, firstLineOf(err.Error()))
			continue
		}
		tdirs[t.Name] = res.Dir
		if end := p.budget(); end != nil {
			return nil, nil, end
		}
		r.logf("%s: reading %s", p.key, t.Name)
		inv := r.cfg.Actions.Investigate
		b, err := action.Brief(r.ctx, p.call(inv.ProviderModel, inv.Timeout), action.BriefInput{
			Repo: t.Name, Instructions: instructions(res.Dir), Request: text,
			Dir: res.Dir, ExtraDeny: r.cfg.Context.ExtraDeny, RunsDir: runsBaseDir,
		})
		name := "brief-" + t.Name
		p.calls[name] = b.Usage
		if err := saveCall(p.plan, name, b.Answer, b.Output, b.Prompt, b.Usage, err); err != nil {
			if r.stopping() {
				return nil, nil, quietEnd()
			}
			r.logf("%s: no brief of %s: %s", p.key, t.Name, firstLineOf(err.Error()))
			continue
		}
		briefs = append(briefs, action.TargetBrief{Repo: t.Name, Answer: b.Answer})
	}
	return tdirs, briefs, nil
}

// lookup looks for the answers of a's questions in the code of the Epic's repositories before they are
// put to people: the targets are checked out for it, read only, next to the references. What the code
// settles moves from the questions to the assumptions, with where it shows. A lookup that cannot run or
// fails leaves every question to people; only a stop or the budget ends the plan.
func (p *planRun) lookup(text string, refs, targets []planTarget, dirs, tdirs map[string]string, a *story.RequirementsAnswer) *planEnd {
	r := p.r
	if r.stopping() {
		return quietEnd()
	}
	if end := p.budget(); end != nil {
		return end
	}
	skip := func(why string) *planEnd {
		r.logf("%s: no lookup, the questions go to the Epic: %s", p.key, why)
		return nil
	}
	parent := ""
	var repos []action.LookupRepo
	for _, t := range slices.Concat(refs, targets) {
		dir, isRef := dirs[t.Name], true
		if dir == "" {
			isRef, dir = false, tdirs[t.Name]
		}
		if dir == "" {
			res, err := r.checkout.prepare(r.ctx, t.Name, t.Path)
			if err != nil {
				if r.stopping() {
					return quietEnd()
				}
				return skip("the checkout of " + t.Name + " failed: " + firstLineOf(err.Error()))
			}
			dir = res.Dir
		}
		if parent == "" {
			parent = filepath.Dir(dir)
		} else if filepath.Dir(dir) != parent {
			return skip("the checkouts are not in one directory")
		}
		repos = append(repos, action.LookupRepo{Name: filepath.Base(dir), Reference: isRef})
	}
	r.logf("%s: looking up %d questions in the code", p.key, len(a.Questions))
	inv := r.cfg.Actions.Investigate
	res, err := action.Lookup(r.ctx, p.call(inv.ProviderModel, inv.Timeout), action.LookupInput{
		Request: text, Summary: a.Summary, Questions: a.Questions, Repos: repos,
		Dir: parent, ExtraDeny: r.cfg.Context.ExtraDeny, RunsDir: runsBaseDir,
	})
	p.calls["lookup"] = res.Usage
	if err := saveCall(p.plan, "lookup", res.Answer, res.Output, res.Prompt, res.Usage, err); err != nil {
		if r.stopping() {
			return quietEnd()
		}
		return skip(firstLineOf(err.Error()))
	}
	settled := map[int]bool{}
	for _, it := range res.Answer.Answers {
		settled[it.Question] = true
		a.Assumptions = append(a.Assumptions, flat(a.Questions[it.Question-1])+" "+flat(it.Answer)+" (found in "+flat(it.Evidence)+")")
	}
	var open []string
	for i, q := range a.Questions {
		if !settled[i+1] {
			open = append(open, q)
		}
	}
	r.logf("%s: the code answered %d of %d questions", p.key, len(settled), len(a.Questions))
	a.Questions = open
	return nil
}

// flat is s on one line, its runs of whitespace one space.
func flat(s string) string { return strings.Join(strings.Fields(s), " ") }

// requirementsBody is the requirements comment's body (SPEC §3) of an Epic with these references.
func (r *listenRunner) requirementsBody(key string, refs []planTarget, a story.RequirementsAnswer) []string {
	names := make([]string, len(refs))
	for i, ref := range refs {
		names[i] = ref.Name
	}
	return listener.RequirementsComment(jira.Escape, key, names, listener.Requirements{
		Summary: a.Summary, Decided: a.Assumptions, Questions: a.Questions, BackTo: r.queueStatus(),
	})
}

// targetNotes are what specify knows of each target it was not given a survey of: its catalog
// description and why the selection chose it, by name.
func targetNotes(targets []planTarget) map[string]string {
	notes := map[string]string{}
	for _, t := range targets {
		var parts []string
		for _, s := range []string{t.Description, t.Why} {
			if s = strings.TrimSpace(s); s != "" {
				parts = append(parts, s)
			}
		}
		if len(parts) > 0 {
			notes[t.Name] = strings.Join(parts, "; ")
		}
	}
	return notes
}

// specify is the specification call (SPEC §7.1) and the validation of its tasks as a story's: from the
// Epic's text, its requirements, the references' surveys and the targets by name and note
// (specs/listener-requirements/SPEC.md §4).
func (p *planRun) specify(text string, targets []planTarget, refSurveys []action.SurveyInput, briefs []action.TargetBrief, req story.RequirementsAnswer) (story.SpecifyAnswer, []story.Task, *planEnd) {
	r, sc := p.r, p.r.lc.Story
	names := make([]string, len(targets))
	for i, t := range targets {
		names[i] = t.Name
	}
	in := action.SpecifyInput{
		Request: text, Repos: names, MaxTasks: sc.MaxTasks, References: refSurveys, Parallel: true,
		Requirements: &req, Notes: targetNotes(targets), Briefs: briefs, MaxPerRepo: sc.MaxTasksPerService,
	}
	// A plan with too many tasks in a service is specified once more, told which to merge.
	for _, name := range []string{"specify", "specify-again"} {
		if r.stopping() {
			return story.SpecifyAnswer{}, nil, quietEnd()
		}
		if end := p.budget(); end != nil {
			return story.SpecifyAnswer{}, nil, end
		}
		r.logf("%s: specifying", p.key)
		inv := r.cfg.Actions.Investigate
		res, err := action.Specify(r.ctx, p.call(inv.ProviderModel, inv.Timeout), in)
		p.calls[name] = res.Usage
		if err := saveCall(p.plan, name, res.Answer, res.Output, res.Prompt, res.Usage, err); err != nil {
			return story.SpecifyAnswer{}, nil, p.callEnd(err)
		}
		tasks := res.Answer.Tasks()
		if err := story.Validate(tasks, names, sc.MaxTasks); err != nil {
			return story.SpecifyAnswer{}, nil, failedBy((&apperr.InvalidResponseError{Call: "specify", Field: "tasks", Msg: err.Error(), Err: err}).Error())
		}
		over := overPerService(tasks, sc.MaxTasksPerService)
		if over == "" {
			return res.Answer, tasks, nil
		}
		r.logf("%s: %s", p.key, over)
		if name == "specify-again" {
			return story.SpecifyAnswer{}, nil, failedBy("The plan still has " + over + " after it was asked to merge them; nothing was created.")
		}
		in.Merge = "Your previous answer had " + over + ". Merge the tasks of each repository that change the same places, " +
			"fold the small ones into the tasks they relate to, and answer again with the whole plan."
	}
	return story.SpecifyAnswer{}, nil, nil // unreachable: the second pass returns
}

// overPerService names the services with more than limit tasks, "" when there is none.
func overPerService(tasks []story.Task, limit int) string {
	if limit <= 0 {
		return ""
	}
	count := map[string]int{}
	for _, t := range tasks {
		count[t.Repo]++
	}
	var over []string
	for _, repo := range slices.Sorted(maps.Keys(count)) {
		if count[repo] > limit {
			over = append(over, fmt.Sprintf("%d tasks in %s", count[repo], repo))
		}
	}
	if len(over) == 0 {
		return ""
	}
	return strings.Join(over, " and ") + fmt.Sprintf(", more than listener.story.max_tasks_per_service (%d)", limit)
}

// surveyRepo is the survey of one reference's checkout: its directory and the listener's own deny list
// and investigate route. Planning an Epic only reads its references, so it needs no service's
// configuration; a target's own is required when its child ticket runs (configFor).
func (r *listenRunner) surveyRepo(name, dir string) (action.SurveyRepo, config.ProviderModel, time.Duration) {
	inv := r.cfg.Actions.Investigate
	return action.SurveyRepo{Name: name, Dir: dir, ExtraDeny: r.cfg.Context.ExtraDeny, RunsDir: runsBaseDir, Reference: true},
		inv.ProviderModel, inv.Timeout
}

// call is a planning call on route, whose raw output goes to the plan's logs.
func (p *planRun) call(route config.ProviderModel, timeout time.Duration) action.PlanningCall {
	return action.PlanningCall{Registry: p.r.registry, Route: route, Timeout: timeout, Logger: p.plan}
}

// cost is what the plan's calls reported so far (a call without usage counts nothing).
func (p *planRun) cost() float64 {
	total := 0.0
	for _, u := range p.calls {
		if u != nil {
			total += u.CostUSD
		}
	}
	return total
}

// budget is checked before each call (SPEC §7.3): the calls so far reaching listener.story.max_cost_usd
// fail the plan, and nothing is created.
func (p *planRun) budget() *planEnd {
	limit := p.r.lc.Story.MaxCostUSD
	if limit == nil {
		return nil
	}
	if spent := p.cost(); spent >= *limit {
		return failedBy(fmt.Sprintf("The planning calls cost $%.2f, which reaches listener.story.max_cost_usd ($%.2f); nothing was created.", spent, *limit))
	}
	return nil
}

// callEnd maps the failure of a planning call: the listener stopping leaves the Epic claimed; a
// provider that failed goes back to the queue (a credential also stops the listener, as a run's
// does); an answer that breaks a rule, or anything else, fails the plan.
func (p *planRun) callEnd(err error) *planEnd {
	r := p.r
	if r.stopping() {
		return quietEnd()
	}
	var perr *apperr.ProviderError
	if errors.As(err, &perr) {
		line := fmt.Sprintf("the %s call of %s failed (%s)", perr.Action, perr.Provider, perr.Kind)
		if perr.Kind == apperr.FailureAuth {
			r.setStop(&apperr.ConfigError{Msg: fmt.Sprintf("the plan of %s failed on a credential: %s", p.key, line)})
		}
		return retryBy(line)
	}
	return failedBy(firstLineOf(err.Error()))
}

// gitlabEnd maps the failure of the catalog's read (SPEC §5.1, §9): the network, a 5xx or a rate
// limit goes back to the queue; a refused token fails the plan with a comment naming the variable.
func (p *planRun) gitlabEnd(err error) *planEnd {
	r := p.r
	if r.stopping() {
		return quietEnd()
	}
	var gerr *apperr.GitLabError
	if errors.As(err, &gerr) {
		switch {
		case gerr.Kind == apperr.GitLabAuth || gerr.Kind == apperr.GitLabNotFound:
			return failedBy("GitLab refused the list of the projects of its groups: the read-only token in " + r.apiTokenEnv() +
				" (gitlab.api_token_env) must be valid and able to list " + strings.Join(r.checkout.allowedGroups, ", ") + ".")
		case gerr.Kind.Transient():
			return retryBy(firstLineOf(err.Error()))
		}
	}
	return failedBy("The projects of GitLab's groups cannot be listed: " + firstLineOf(err.Error()))
}

// apiTokenEnv is gitlab.api_token_env, "" when the configuration has no GitLab section.
func (r *listenRunner) apiTokenEnv() string {
	if r.cfg.GitLab == nil {
		return ""
	}
	return r.cfg.GitLab.APITokenEnv
}

// checkoutEnd maps a checkout failure as a ticket's (SPEC §5.3): the network goes back to the queue,
// a refused credential stops the listener, a project that is not allowed or has no checkout is
// blocked, and anything else fails the plan.
func (p *planRun) checkoutEnd(err error) *planEnd {
	r := p.r
	if r.stopping() {
		return quietEnd()
	}
	var ce *checkoutError
	if !errors.As(err, &ce) {
		return retryBy(firstLineOf(err.Error()))
	}
	r.logf("%s: %s", p.key, firstLineOf(ce.Error()))
	switch ce.Kind.class() {
	case classBlocked:
		return blockedBy(ce.Msg)
	case classFailed:
		return failedBy(ce.Msg)
	case classStop:
		r.setStop(&apperr.ConfigError{Msg: "GitLab refused the git credential: " + ce.Msg})
	}
	return retryBy(firstLineOf(ce.Error()))
}

// finish ends the plan with end.
func (p *planRun) finish(end *planEnd) error {
	if end.quiet {
		p.rec.Outcome = "stopped"
		p.save()
		return nil
	}
	p.rec.Outcome, p.rec.Reason = string(end.result), oneLineText(end.reason, 900)
	p.save()
	switch end.result {
	case listener.ResultRetry:
		return p.r.endInfra(p.key, errors.New(end.reason))
	case listener.ResultFailed:
		return p.r.endCounted(p.key, end.result, end.reason)
	}
	return p.r.endBefore(p.key, end.result, end.reason)
}

// save writes plan.json; a plan that cannot be written is logged, not retried.
func (p *planRun) save() {
	if p.plan == nil {
		return
	}
	p.rec.CostUSD = p.cost()
	if err := p.plan.SaveFile("plan.json", p.rec); err != nil {
		p.r.logf("%s: %v", p.key, err)
	}
}

// endCounted finishes an Epic whose plan failed: the comment with the reason, the failed transition
// and an attempt used (SPEC §9), unlike endBefore, which counts none.
func (r *listenRunner) endCounted(key string, result listener.Result, reason string) error {
	if err := r.update(key, func(e *listener.Entry) {
		e.Outcome = result
		e.Interrupted = false
		e.Reason = oneLineText(reason, 900)
		e.Retries = 0
		e.Attempts++
	}); err != nil {
		return err
	}
	return r.write(key, listener.Before(false), nil)
}

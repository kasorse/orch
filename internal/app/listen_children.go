package app

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"unicode/utf8"

	"github.com/kasorse/orch/internal/apperr"
	"github.com/kasorse/orch/internal/config"
	"github.com/kasorse/orch/internal/domain"
	"github.com/kasorse/orch/internal/jira"
	"github.com/kasorse/orch/internal/listener"
	"github.com/kasorse/orch/internal/secretscan"
	"github.com/kasorse/orch/internal/story"
)

// The children of an Epic's plan (specs/listener-story/SPEC.md §8, §9): a validated plan becomes one
// ticket per task, then the links of each service's chain, then one comment on the Epic, which stays
// at start. Every write follows story run's rules for its children (createJiraChild): the intent is
// saved in the Epic's entry before the request, the key after the answer, and a request that may have
// reached Jira is never made again. Nothing is ever deleted.

// checkStoryReporter is the start-time check of SPEC §2 (A15): the children the listener creates are
// reported by its own account, so with listener.story that account must be one of query.reporters, or
// the poll would never pick them.
func checkStoryReporter(lc *config.ListenerConfig, bot string) error {
	if lc.Story == nil || slices.Contains(lc.Query.Reporters, bot) {
		return nil
	}
	return &apperr.ConfigError{Msg: fmt.Sprintf("listener.story creates the children as %s, which is not in listener.query.reporters (%s): "+
		"the poll would never pick them; add it", bot, strings.Join(lc.Query.Reporters, ", "))}
}

// existing is the children check of SPEC §9 (A9), made before the catalog: writes that were left in
// doubt by a plan cut off by a stop fail the Epic naming them (A10); children the listener reported
// under the Epic already block it, unless it has the restart label, which skips the search. restart
// says the label is there; it is removed just before the first creation.
func (p *planRun) existing(ticket domain.Ticket) (end *planEnd, restart bool) {
	r := p.r
	restart = r.lc.Labels.Restart != "" && slices.Contains(ticket.Labels, r.lc.Labels.Restart)
	e := r.entryOf(p.key)
	if tasks, links := e.InDoubt(); len(tasks)+len(links) > 0 {
		return failedBy(inDoubtReason(p.key, tasks, links, e.CreatedKeys())), restart
	}
	if restart {
		return nil, true
	}
	field := ""
	switch c := r.cfg.Jira.Create; {
	case c.ChildLinkType(ticket.Type) != "":
		field = jira.ByLink
	case c != nil && c.Link == config.JiraLinkField:
		field = c.Field
	}
	found, err := r.jira.Children(r.ctx, p.key, field, r.bot)
	if err != nil {
		return p.jiraEnd(err), restart
	}
	keys := slices.Clone(found)
	for _, k := range e.CreatedKeys() {
		if !slices.Contains(keys, k) {
			keys = append(keys, k)
		}
	}
	if len(keys) > 0 {
		r.logf("%s: it has children already: %s", p.key, strings.Join(keys, ", "))
		return blockedBy(listener.ExistingChildren(p.key, keys, r.lc.Labels.Restart)), restart
	}
	return nil, restart
}

// inDoubtReason names the writes of a plan cut off by a stop that may have reached Jira.
func inDoubtReason(epic string, tasks []string, links []listener.Link, created []string) string {
	var parts []string
	for _, t := range tasks {
		parts = append(parts, "the creation of the issue of task "+t)
	}
	for _, l := range links {
		parts = append(parts, "the link of task "+l.Blocked+" to task "+l.Blocker)
	}
	reason := "A write of the plan may have reached Jira without an answer, and it is not repeated: " + strings.Join(parts, ", ") +
		". Look under " + epic + " in Jira and finish it by hand."
	if len(created) > 0 {
		reason += " Created so far: " + strings.Join(created, ", ") + "."
	}
	return reason
}

// jiraEnd maps the failure of a read of Jira during the plan: a credential stops the listener and goes
// back to the queue, the listener stopping leaves the Epic claimed, anything else goes back to the queue.
func (p *planRun) jiraEnd(err error) *planEnd {
	r := p.r
	if r.isAuth(err) {
		return retryBy("Jira refused the credentials")
	}
	if r.stopping() {
		return quietEnd()
	}
	return retryBy(firstLineOf(err.Error()))
}

// childSummary is the title of a task as the child's summary: the task's title, cut to what Jira
// accepts. It has no task ID: the ID is the plan's, not the team's (A7).
func childSummary(title string) string {
	title = strings.Join(strings.Fields(title), " ")
	if utf8.RuneCountInString(title) <= maxJiraSummary {
		return title
	}
	return string([]rune(title)[:maxJiraSummary-1]) + "…"
}

// createChildren is the end of a plan (SPEC §8, §9): the descriptions are made and secret-scanned
// before the first request, then each child is created in the story's order and each link made, and the
// Epic is commented on, staying at start.
func (p *planRun) createChildren(ticket domain.Ticket, answer story.SpecifyAnswer, req story.RequirementsAnswer, tasks []story.Task, targets, refs []planTarget, restart bool) error {
	r, key := p.r, p.key
	create, blockers := r.cfg.Jira.Create, r.lc.Blockers
	if create == nil || blockers == nil {
		return p.finish(failedBy("The plan is ready, but creating the children needs jira.create and listener.blockers in the configuration; nothing was created."))
	}
	order := story.Order(tasks)
	byID := make(map[string]story.Task, len(tasks))
	for _, t := range tasks {
		byID[t.ID] = t
	}
	pathOf := map[string]string{}
	for _, t := range targets {
		pathOf[t.Name] = t.Path
	}
	base := ""
	if r.cfg.GitLab != nil {
		base = strings.TrimRight(r.cfg.GitLab.BaseURL, "/")
	}
	refURLs := make([]string, len(refs))
	for i, ref := range refs {
		refURLs[i] = base + "/" + ref.Path
	}
	project := key[:strings.LastIndex(key, "-")]
	// A ticket that cannot be a parent in Jira's hierarchy (a Story) gets its children by an issue link.
	childLink := create.ChildLinkType(ticket.Type)

	// build is the child of task id. keys holds the issues made so far: a task of another service that
	// this one needs is named by its key when it has one, else by its ID (the first pass, which scans).
	build := func(id string, keys map[string]string) jira.Issue {
		t := byID[id]
		var needs []jira.WikiNeed
		for _, dep := range t.DependsOn {
			d := byID[dep]
			if d.Repo == t.Repo {
				continue
			}
			k := keys[dep]
			if k == "" {
				k = dep
			}
			needs = append(needs, jira.WikiNeed{Key: k, Service: d.Repo})
		}
		description := jira.Wiki(jira.WikiTask{
			Body:        t.Body,
			Epic:        ticket.Key,
			EpicSummary: ticket.Summary,
			Service:     t.Repo,
			ServicePath: pathOf[t.Repo],
			References:  refURLs,
			Needs:       needs,
			// Every child gets all the story's requirements: it does not know which are its own
			// (specs/listener-requirements/SPEC.md §5, B6).
			Requirements: req.Requirements,
			Approach:     answer.Spec.Approach,
			Contracts:    answer.Spec.Contracts,
			MaxBytes:     r.cfg.Jira.MaxBytes / 2,
		})
		queue := r.lc.Query.Label
		if !r.lc.Story.QueueChildren {
			queue = ""
		}
		labels := listener.ChildLabels(queue, r.lc.Labels.ServicePrefix, t.Repo, r.lc.Labels.ProjectPrefix, pathOf[t.Repo], create.Labels)
		issue := jira.Issue{
			Project:     project,
			IssueType:   create.IssueType,
			Summary:     childSummary(t.Title),
			Description: description,
			Parent:      key,
			Labels:      labels,
		}
		if childLink != "" {
			issue.ParentByLink = true
		} else if create.Link == config.JiraLinkField {
			issue.LinkField = create.Field
		}
		if name := create.Components[t.Repo]; name != "" {
			issue.Components = []string{name}
		}
		return issue
	}

	// The model's text reaches Jira only here: one finding in any child stops the plan before the first
	// request, and the comment names the task, not the text (SPEC §8.2). The text is scanned as the model
	// wrote it, which the wiki's escaping would hide from the rules (a "-" or a "_" in a key), and as it
	// is sent.
	for _, id := range order {
		issue, t := build(id, nil), byID[id]
		raw := t.Title + "\n" + t.Body + "\n" + answer.Spec.Approach + "\n" + answer.Spec.Contracts
		sent := issue.Summary + "\n" + issue.Description
		if findings, _ := secretscan.Scan("description", []byte(raw)); len(findings) > 0 {
			return p.finish(failedBy(fmt.Sprintf("The text of task %s (%s) looks like it holds a secret, so nothing was created.", id, byID[id].Repo)))
		}
		if findings, _ := secretscan.Scan("description", []byte(sent)); len(findings) > 0 {
			return p.finish(failedBy(fmt.Sprintf("The text of task %s (%s) looks like it holds a secret, so nothing was created.", id, byID[id].Repo)))
		}
	}
	if r.stopping() {
		return p.finish(quietEnd())
	}
	if restart {
		// Best effort, as a ticket's: a label left behind only makes the next cycle plan again.
		if err := r.jira.RemoveLabel(r.ctx, key, r.lc.Labels.Restart); err != nil {
			r.isAuth(err)
			r.logf("%s: cannot remove the label %s: %s", key, r.lc.Labels.Restart, firstLineOf(err.Error()))
		}
	}
	if err := r.update(key, func(e *listener.Entry) { e.Children, e.Links = nil, nil }); err != nil {
		return err
	}

	keys := map[string]string{}
	for _, id := range order {
		if r.stopping() {
			return p.finish(quietEnd())
		}
		t := byID[id]
		if err := r.update(key, func(e *listener.Entry) {
			e.Children = append(slices.Clone(e.Children), listener.Child{Task: id, Intent: true})
		}); err != nil {
			return err
		}
		issueKey, err := r.jira.Create(r.ctx, build(id, keys))
		if err != nil {
			end, serr := p.writeEnd(err, fmt.Sprintf("The creation of the issue of task %s (%s)", id, t.Repo), func() error {
				return r.update(key, func(e *listener.Entry) {
					e.Children = slices.DeleteFunc(slices.Clone(e.Children), func(c listener.Child) bool { return c.Task == id && c.Key == "" })
				})
			})
			if serr != nil {
				return serr
			}
			return p.finish(end)
		}
		keys[id] = issueKey
		if err := r.update(key, func(e *listener.Entry) {
			children := slices.Clone(e.Children)
			for i := range children {
				if children[i].Task == id {
					children[i].Key, children[i].Intent = issueKey, false
				}
			}
			e.Children = children
		}); err != nil {
			return fmt.Errorf("%s: created %s in Jira for task %s, but cannot record it: %w", key, issueKey, id, err)
		}
		p.rec.Children = append(p.rec.Children, issueKey)
		for i := range p.rec.Tasks {
			if p.rec.Tasks[i].ID == id {
				p.rec.Tasks[i].Key = issueKey
			}
		}
		p.save()
		r.logf("%s: created %s for %s (%s)", key, issueKey, id, t.Repo)
		if childLink != "" {
			// The child is the outward issue: "<child> is a child of <story>" for migration_parent.
			if err := r.jira.Link(r.ctx, childLink, key, issueKey); err != nil {
				end, serr := p.writeEnd(err, fmt.Sprintf("The %s link of %s (task %s) to %s", childLink, issueKey, id, key), func() error { return nil })
				if serr != nil {
					return serr
				}
				return p.finish(end)
			}
		}
	}

	// The links, one POST each: the later task is the inward side, the one that is blocked.
	for _, l := range p.rec.Chain {
		if r.stopping() {
			return p.finish(quietEnd())
		}
		blocked, blocker := keys[l.Blocked], keys[l.Blocker]
		if err := r.update(key, func(e *listener.Entry) {
			e.Links = append(slices.Clone(e.Links), listener.ChildLink{Blocked: l.Blocked, Blocker: l.Blocker, State: listener.WriteIntent})
		}); err != nil {
			return err
		}
		if err := r.jira.Link(r.ctx, blockers.LinkType, blocked, blocker); err != nil {
			what := fmt.Sprintf("The link of %s (task %s) as blocked by %s (task %s)", blocked, l.Blocked, blocker, l.Blocker)
			end, serr := p.writeEnd(err, what, func() error {
				return r.update(key, func(e *listener.Entry) {
					e.Links = slices.DeleteFunc(slices.Clone(e.Links), func(x listener.ChildLink) bool {
						return x.Blocked == l.Blocked && x.Blocker == l.Blocker && x.State == listener.WriteIntent
					})
				})
			})
			if serr != nil {
				return serr
			}
			return p.finish(end)
		}
		if err := r.update(key, func(e *listener.Entry) {
			links := slices.Clone(e.Links)
			for i := range links {
				if links[i].Blocked == l.Blocked && links[i].Blocker == l.Blocker {
					links[i].State = listener.WriteDone
				}
			}
			e.Links = links
		}); err != nil {
			return fmt.Errorf("%s: linked %s to %s in Jira, but cannot record it: %w", key, blocked, blocker, err)
		}
		r.logf("%s: %s is blocked by %s", key, blocked, blocker)
	}

	p.childStatus(order, keys)
	p.rec.Outcome = string(listener.ResultPlanned)
	p.save()
	if err := r.update(key, func(e *listener.Entry) {
		e.Outcome = listener.ResultPlanned
		e.Interrupted = false
		e.Reason = ""
		e.Retries = 0
		e.Attempts++
	}); err != nil {
		return err
	}
	return r.write(key, listener.Planned(), nil)
}

// childStatus reads one child's status back, once, after every creation (SPEC §8.1, A16): when it is not
// one of query.statuses the Epic's comment says the poll will not pick the children. Children left out
// of the queue (listener.story.queue_children: false) are not read: the comment says how to queue one.
func (p *planRun) childStatus(order []string, keys map[string]string) {
	r := p.r
	if len(order) == 0 || r.stopping() {
		return
	}
	if !r.lc.Story.QueueChildren {
		p.rec.StatusWarning = fmt.Sprintf("The children are not labelled %s: add it to a child, in %s, for the listener to run it.",
			r.lc.Query.Label, strings.Join(r.lc.Query.Statuses, " or "))
		return
	}
	first := keys[order[0]]
	status, err := r.jira.Status(r.ctx, first)
	if err != nil {
		r.isAuth(err)
		r.logf("%s: cannot read the status of %s: %s", p.key, first, firstLineOf(err.Error()))
		return
	}
	p.rec.ChildStatus = status
	if !slices.ContainsFunc(r.lc.Query.Statuses, func(s string) bool { return strings.EqualFold(s, status) }) {
		p.rec.StatusWarning = fmt.Sprintf("The children are in %s, which is not one of the statuses the listener polls (%s): move them there, or it will not pick them.",
			status, strings.Join(r.lc.Query.Statuses, ", "))
		r.logf("%s: %s", p.key, p.rec.StatusWarning)
	}
}

// writeEnd is how the plan ends when a write to Jira failed (SPEC §8.3, §9). what names the write.
// clear removes the write's intent from the Epic's entry, and is called when the write is known not to
// have been made. A credential goes back to the queue and stops the listener; the listener's stop
// leaves the Epic claimed (an intent in doubt stays, and the restart names it); a write that may have
// reached Jira fails the Epic and is not repeated; a refused one fails it without the doubt. The error
// returned is the state's, which ends the listener.
func (p *planRun) writeEnd(err error, what string, clear func() error) (*planEnd, error) {
	r := p.r
	if r.isAuth(err) {
		return retryBy("Jira refused the credentials"), clear()
	}
	maybe := jira.MayExist(err)
	if r.stopping() {
		if maybe {
			return quietEnd(), nil
		}
		return quietEnd(), clear()
	}
	if maybe {
		return failedBy(fmt.Sprintf("%s may have reached Jira, but no answer came (%s), so it is not repeated. Look under %s in Jira and finish it by hand.",
			what, firstLineOf(err.Error()), p.key)), nil
	}
	if serr := clear(); serr != nil {
		return nil, serr
	}
	var jerr *apperr.JiraError
	if e := r.entryOf(p.key); errors.As(err, &jerr) && jerr.Kind.Transient() && len(e.CreatedKeys()) == 0 && len(e.Links) == 0 {
		// Nothing was written yet: the plan is made again from the start.
		return retryBy(firstLineOf(err.Error())), nil
	}
	return failedBy(fmt.Sprintf("%s was refused by Jira (%s). Nothing was deleted; finish it by hand under %s.",
		what, firstLineOf(err.Error()), p.key)), nil
}

// planBody is the body of the comment of an Epic whose plan is in its plan.json: the created children
// when the plan came to an end, or the ones made before it failed. ok is false when the entry has no
// plan to say more of than its reason.
func (r *listenRunner) planBody(e listener.Entry) (body []string, ok bool) {
	if e.Kind != listener.KindPlan || e.PlanDir == "" {
		return nil, false
	}
	data, err := os.ReadFile(filepath.Join(r.plansDir, filepath.Base(e.PlanDir), "plan.json"))
	if err != nil {
		return nil, false
	}
	var rec planRecord
	if err := json.Unmarshal(data, &rec); err != nil {
		return nil, false
	}
	children := r.planChildren(rec)
	switch {
	case e.Outcome == listener.ResultPlanned && len(children) > 0:
		c := listener.PlanComment{Epic: rec.Key, Children: children, Warning: rec.StatusWarning}
		for _, t := range rec.Targets {
			c.Services = append(c.Services, listener.PlanService{Name: t.Name, Why: r.scrubText(t.Why)})
		}
		for _, ref := range rec.References {
			c.References = append(c.References, ref.Path)
		}
		if rec.CostUSD > 0 {
			c.Cost = fmt.Sprintf("$%.2f", rec.CostUSD)
		}
		return listener.PlanCreated(jira.Escape, c), true
	case e.Outcome == listener.ResultFailed && len(children) > 0:
		return listener.PlanFailed(jira.Escape, e.Reason, children), true
	case e.Outcome == listener.ResultBlocked && rec.Requirements != nil && len(rec.Questions) > 0:
		// The requirements with their questions are the outcome's comment: one comment, not two
		// (specs/listener-requirements/SPEC.md §3).
		return r.requirementsBody(rec.Key, rec.References, *rec.Requirements), true
	}
	return nil, false
}

// planChildren are the children of a plan record that were created, in the story's order, each with the
// key of the child it waits for.
func (r *listenRunner) planChildren(rec planRecord) []listener.PlanChild {
	keyOf := map[string]string{}
	for _, t := range rec.Tasks {
		if t.Key != "" {
			keyOf[t.ID] = t.Key
		}
	}
	var out []listener.PlanChild
	for _, t := range rec.Tasks {
		if t.Key == "" {
			continue
		}
		out = append(out, listener.PlanChild{Key: t.Key, Service: t.Service, Title: r.scrubText(t.Title), BlockedBy: keyOf[t.BlockedBy]})
	}
	return out
}

package app

import (
	"fmt"
	"maps"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"unicode/utf8"

	"orchestrator/internal/apperr"
	"orchestrator/internal/config"
	"orchestrator/internal/jira"
	"orchestrator/internal/persistence"
	"orchestrator/internal/story"
)

// maxJiraSummary is Jira's limit on an issue's summary.
const maxJiraSummary = 255

// jiraCreating reports whether this story run creates the child issues: the config says how
// (jira.create) and the story has a parent to hang them under (specs/story/SPEC.md §12.2).
func (r *storyRunner) jiraCreating() bool {
	return r.cfg.Jira != nil && r.cfg.Jira.Create != nil && r.rec.JiraParent != ""
}

// checkJiraChildren is everything about the child issues that can be checked, and said, before
// the first start is confirmed; it sends nothing.
//
// An intent without a key stops the story: the creation may have reached Jira, and another
// would make a second issue. The operator answers by writing jira: <KEY> or jira: none in the
// task's front matter, which also clears the intent.
func (r *storyRunner) checkJiraChildren() error {
	for _, id := range r.order {
		rt := r.rec.Tasks[id]
		if !rt.JiraIntent {
			continue
		}
		if r.byID[id].Jira == "" && rt.Jira == "" {
			return &apperr.ConfigError{Msg: id + ": a Jira issue may already exist; set jira: <KEY> or jira: none in its front matter"}
		}
		// Answered, by the front matter or by a key that was recorded: nothing is in doubt.
		rt.JiraIntent = false
		r.rec.Tasks[id] = rt
	}

	switch {
	case r.cfg.Jira == nil || r.cfg.Jira.Create == nil:
		return nil
	case r.rec.JiraParent == "":
		r.reporter.StoryNote("the story has no Jira parent (story plan --jira): no child issues are created")
		return nil
	}

	create := r.cfg.Jira.Create
	services := map[string]bool{}
	for name := range r.rec.Services {
		services[name] = true
	}
	for _, t := range r.byID {
		services[t.Repo] = true
	}
	for _, name := range slices.Sorted(maps.Keys(create.Components)) {
		if !services[name] {
			return &apperr.ConfigError{Msg: fmt.Sprintf("jira.create.components: %q is not a service of the story (%s)",
				name, strings.Join(slices.Sorted(maps.Keys(services)), ", "))}
		}
	}
	if !strings.Contains(r.rec.JiraParent, "-") {
		return &apperr.ConfigError{Msg: fmt.Sprintf("story.json: jira_parent %q has no project", r.rec.JiraParent)}
	}
	return nil
}

// createJiraChildren creates the issue of every task that has none, in the story's order, one
// at a time, and records each key before the next request. It runs after the first start was
// approved and before anything is dispatched. A task that was dispatched is never touched: its
// file is frozen, and it got its key (or none) before it was dispatched.
func (r *storyRunner) createJiraChildren() error {
	if !r.jiraCreating() {
		return nil
	}
	var client *jira.Client
	for _, id := range r.order {
		t, rt := r.byID[id], r.rec.Tasks[id]
		if rt.Status != "" && rt.Status != story.TaskPending {
			continue
		}
		switch {
		case t.Jira != "":
			continue
		case rt.Jira != "":
			// Recorded, but the crash came before the task file got it.
			if err := r.writeJiraKey(id, rt.Jira); err != nil {
				return r.jiraFailed(err)
			}
			continue
		}
		if client == nil {
			client = r.newJiraClient()
		}
		if err := r.createJiraChild(client, id); err != nil {
			return r.jiraFailed(err)
		}
	}
	return nil
}

// jiraFailed stops the story before any dispatch.
func (r *storyRunner) jiraFailed(err error) error {
	status := story.StatusFailed
	if r.ctx.Err() != nil {
		status = story.StatusStopped
	}
	r.halt(status)
	return err
}

// newJiraClient builds the client for the creations, as taskFromJira does for a fetch. This and
// taskFromJira are the places the credential values are read from the environment.
func (r *storyRunner) newJiraClient() *jira.Client {
	j := r.cfg.Jira
	creds := jira.NewCredentials(j.Auth, os.Getenv(j.EmailEnv), os.Getenv(j.TokenEnv))
	return jira.NewHTTPClient(jira.Options{
		BaseURL: j.BaseURL, Auth: j.Auth, EmailEnv: j.EmailEnv, TokenEnv: j.TokenEnv,
		Timeout: j.Timeout, MaxRetries: j.MaxRetries,
		UserAgent: "orchestrator/" + Version,
	}, creds)
}

// createJiraChild makes the issue of one task. The order of the writes is what makes a crash
// recoverable: the intent is saved before the request; after a 201 the key is saved in
// story.json (and the intent cleared) before the task file gets it. An intent is kept when the
// request may have created the issue and cleared when Jira refused it.
func (r *storyRunner) createJiraChild(client *jira.Client, id string) error {
	t := r.byID[id]
	create := r.cfg.Jira.Create

	rt, existed := r.rec.Tasks[id]
	if !existed {
		rt = persistence.StoryTask{Status: story.TaskPending, Repo: t.Repo}
	}
	rt.JiraIntent = true
	r.rec.Tasks[id] = rt
	if err := r.persist(); err != nil {
		return err
	}

	issue := jira.Issue{
		Project:     r.rec.JiraParent[:strings.LastIndex(r.rec.JiraParent, "-")],
		IssueType:   create.IssueType,
		Summary:     summaryOf(id, t.Title),
		Description: r.jiraDescription(id),
		Parent:      r.rec.JiraParent,
		Labels:      create.Labels,
	}
	if create.Link == config.JiraLinkField {
		issue.LinkField = create.Field
	}
	if name := create.Components[t.Repo]; name != "" {
		issue.Components = []string{name}
	}

	key, err := client.Create(r.ctx, issue)
	if err != nil {
		if jira.MayExist(err) {
			return fmt.Errorf("%s: %w\n\nThe issue may have been created before this failed. Look for it under %s in Jira and set "+
				"jira: <KEY> (or jira: none) in %s before running the story again", id, err, r.rec.JiraParent, r.spec.files[id])
		}
		rt.JiraIntent = false
		if existed {
			r.rec.Tasks[id] = rt
		} else {
			delete(r.rec.Tasks, id)
		}
		if perr := r.persist(); perr != nil {
			fmt.Fprintf(r.stderr, "Warning: %v\n", perr)
		}
		return fmt.Errorf("%s: %w", id, err)
	}

	rt.Jira, rt.JiraIntent = key, false
	r.rec.Tasks[id] = rt
	r.setTaskJira(id, key)
	if err := r.persist(); err != nil {
		return fmt.Errorf("%s: created %s in Jira, but cannot record it: %w", id, key, err)
	}
	r.reporter.StoryNote(fmt.Sprintf("%s: created %s in Jira", id, key))
	return r.writeJiraKey(id, key)
}

// writeJiraKey puts the key into the task file's front matter, changing nothing else in it.
func (r *storyRunner) writeJiraKey(id, key string) error {
	updated, err := story.SetJira(r.spec.data[id], key)
	if err != nil {
		return fmt.Errorf("%s: %w", id, err)
	}
	path := r.spec.files[id]
	if err := persistence.ReplaceFile(path, updated); err != nil {
		return fmt.Errorf("%s: %s is in Jira (recorded in story.json), but cannot write it to %s: %w", id, key, path, err)
	}
	r.spec.data[id] = updated
	r.setTaskJira(id, key)
	return nil
}

// setTaskJira gives the in-memory task its key, where the runner and the README read it.
func (r *storyRunner) setTaskJira(id, key string) {
	t := r.byID[id]
	t.Jira = key
	r.byID[id] = t
	for i := range r.spec.tasks {
		if r.spec.tasks[i].ID == id {
			r.spec.tasks[i].Jira = key
		}
	}
}

// jiraDescription is the wiki markup of a task: its text, and the repository, the spec file and
// the keys of the tasks it depends on.
func (r *storyRunner) jiraDescription(id string) string {
	t := r.byID[id]
	var deps []string
	for _, dep := range t.DependsOn {
		if key := jiraKey(r.byID[dep], r.rec.Tasks[dep]); key != "" {
			deps = append(deps, key)
		}
	}
	specPath := filepath.ToSlash(filepath.Join(r.loc.specRel, "tasks", filepath.Base(r.spec.files[id])))
	return jira.Wiki(jira.WikiTask{Body: t.Body, Repo: t.Repo, SpecPath: specPath, DependencyKeys: deps})
}

// summaryOf is the issue's summary, "RF02 · Expose partial refunds in the public API", cut to
// what Jira accepts.
func summaryOf(id, title string) string {
	s := id + " · " + title
	if utf8.RuneCountInString(s) <= maxJiraSummary {
		return s
	}
	return string([]rune(s)[:maxJiraSummary-1]) + "…"
}

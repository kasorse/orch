package main

import (
	"context"
	"fmt"
	"log"
	"strings"
	"time"
)

// Planner is a runner that can preview a task and run the previewed run later (T6). Only the
// local runner is one: the orchestrator's --mode plan stops after the decision, and resume
// runs the same persisted run.
type Planner interface {
	Plan(ctx context.Context, req Request, progress func(string)) (Outcome, string)
	Resume(ctx context.Context, runID string, progress func(string)) Outcome
}

// pending is a plan waiting for a button in Slack.
type pending struct {
	project string
	runID   string
	user    string // who asked; any allowed user may decide
	channel string
	ts      string
	at      time.Time // when it was posted, for decisionTTL
}

func approvalKey(project, runID string) string { return project + "|" + runID }

// Decide is a click on Run or Cancel. msgTS is the message with the buttons.
func (b *Bridge) Decide(user, key string, run bool, channel, msgTS string) {
	if !b.cfg.Allowed(user) {
		return
	}
	b.mu.Lock()
	b.pruneLocked(time.Now())
	p, ok := b.pending[key]
	if ok {
		delete(b.pending, key)
	}
	b.mu.Unlock()
	if !ok {
		b.out.Update(channel, msgTS, ":grey_question: this plan was already decided or has expired")
		return
	}
	if !run {
		b.out.Update(channel, msgTS, fmt.Sprintf(":no_entry_sign: cancelled by <@%s> (run `%s`)", user, p.runID))
		b.out.React(p.channel, p.ts, "x")
		return
	}
	b.out.Update(channel, msgTS, fmt.Sprintf(":white_check_mark: approved by <@%s> (run `%s`)", user, p.runID))
	b.enqueue(p.project, job{channel: p.channel, ts: p.ts, resumeID: p.runID, req: Request{Project: p.project}})
}

func (b *Bridge) plan(project string, j job, pl Planner) {
	o, runID := pl.Plan(b.ctx, j.req, b.live(j.channel, j.ts))
	if o.NeedsInput {
		b.askMore(j, o)
		return
	}
	if !o.OK || runID == "" {
		b.out.Post(j.channel, j.ts, o.Text)
		b.out.React(j.channel, j.ts, "x")
		return
	}
	key := approvalKey(project, runID)
	b.mu.Lock()
	b.pending[key] = pending{project: project, runID: runID, user: j.user, channel: j.channel, ts: j.ts, at: time.Now()}
	b.mu.Unlock()
	b.out.PostApproval(j.channel, j.ts, strings.TrimSpace(o.Text)+"\nRun it?", key)
}

// askMore keeps an unclear request for its thread: a reply adds to it, Run anyway forces it.
func (b *Bridge) askMore(j job, o Outcome) {
	key := j.channel + "/" + j.ts
	j.at = time.Now()
	b.mu.Lock()
	b.asks[key] = j
	b.mu.Unlock()
	b.out.PostAsk(j.channel, j.ts, o.Text, key)
	b.out.React(j.channel, j.ts, "speech_balloon")
}

// Force is a click on Run anyway: the kept request runs with the model's assumptions.
func (b *Bridge) Force(user, key, channel, msgTS string) {
	if !b.cfg.Allowed(user) {
		return
	}
	b.mu.Lock()
	b.pruneLocked(time.Now())
	j, ok := b.asks[key]
	if ok {
		delete(b.asks, key)
	}
	b.mu.Unlock()
	if !ok {
		b.out.Update(channel, msgTS, ":grey_question: this request was already answered or has expired")
		return
	}
	b.out.Update(channel, msgTS, fmt.Sprintf(":fast_forward: <@%s> chose to run anyway, with the model's assumptions", user))
	j.req.Force = true
	j.user = user
	b.enqueue(j.req.Project, j)
}

// addAPIContract refreshes the project's OpenAPI document and adds the contract of the endpoints
// the task or its ticket names to the task, so even the steps without tools see it.
func (b *Bridge) addAPIContract(project string, j *job) {
	p := b.cfg.Projects[project]
	if p.APISpec == nil || p.Local == nil || j.req.apiDone {
		return
	}
	j.req.apiDone = true
	fresh, err := p.APISpec.Refresh(b.ctx, p.Local.Dir)
	if err != nil {
		log.Printf("%s: api_spec refresh: %v", project, err)
		b.out.Post(j.channel, j.ts, fmt.Sprintf(":warning: could not refresh the API spec (%v); using the cached copy", err))
	} else if fresh {
		log.Printf("%s: api_spec refreshed", project)
	}
	contract, err := p.APISpec.Contract(p.Local.Dir, j.req.Task+"\n"+j.req.ticketText)
	if err != nil {
		log.Printf("%s: api_spec contract: %v", project, err)
		return
	}
	if contract == "" {
		return
	}
	j.req.Task = strings.TrimSpace(j.req.Task + "\n\n" + contract)
	n := strings.Count(contract, "\n  response")
	b.out.Post(j.channel, j.ts, fmt.Sprintf(":page_facing_up: added the API contract of %d endpoint(s) from the swagger to the task", n))
}

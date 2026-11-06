// slackbridge takes a task from a Slack message and runs the orchestrator on it, then
// answers in the message's thread (specs/slack-gitlab). It is a separate module: it starts
// `orchestrator` as a program and reads only its `--output json` line, like bench/.
package main

import (
	"context"
	"flag"
	"fmt"
	"log"
	"os"
	"os/signal"
	"sort"
	"strings"
	"sync"
	"syscall"
	"time"

	"github.com/slack-go/slack"
	"github.com/slack-go/slack/slackevents"
	"github.com/slack-go/slack/socketmode"
)

func sortStrings(s []string) { sort.Strings(s) }

// Poster is the part of the Slack API the bridge uses; a fake in tests.
type Poster interface {
	// Post returns the new message ts ("" on failure).
	Post(channel, threadTS, text string) string
	React(channel, ts, emoji string)
	// PostApproval posts text with Run and Cancel buttons whose value is key (T6).
	PostApproval(channel, threadTS, text, key string)
	// PostAsk posts the orchestrator's questions with a Run anyway button whose value is key.
	PostAsk(channel, threadTS, text, key string)
	// Update replaces a message, e.g. the buttons once decided.
	Update(channel, ts, text string)
}

type slackPoster struct{ api *slack.Client }

func (p slackPoster) Post(channel, threadTS, text string) string {
	opts := []slack.MsgOption{slack.MsgOptionText(text, false), slack.MsgOptionDisableLinkUnfurl()}
	if threadTS != "" {
		opts = append(opts, slack.MsgOptionTS(threadTS))
	}
	_, ts, err := p.api.PostMessage(channel, opts...)
	if err != nil {
		log.Printf("post to %s: %v", channel, err)
	}
	return ts
}

func (p slackPoster) PostApproval(channel, threadTS, text, key string) {
	section := slack.NewSectionBlock(slack.NewTextBlockObject(slack.MarkdownType, text, false, false), nil, nil)
	runBtn := slack.NewButtonBlockElement(actionRun, key, slack.NewTextBlockObject(slack.PlainTextType, "Run", false, false))
	runBtn.Style = slack.StylePrimary
	cancelBtn := slack.NewButtonBlockElement(actionCancel, key, slack.NewTextBlockObject(slack.PlainTextType, "Cancel", false, false))
	cancelBtn.Style = slack.StyleDanger
	opts := []slack.MsgOption{
		slack.MsgOptionText(text, false),
		slack.MsgOptionBlocks(section, slack.NewActionBlock("approve", runBtn, cancelBtn)),
		slack.MsgOptionTS(threadTS),
		slack.MsgOptionDisableLinkUnfurl(),
	}
	if _, _, err := p.api.PostMessage(channel, opts...); err != nil {
		log.Printf("post approval to %s: %v", channel, err)
	}
}

func (p slackPoster) PostAsk(channel, threadTS, text, key string) {
	section := slack.NewSectionBlock(slack.NewTextBlockObject(slack.MarkdownType, text, false, false), nil, nil)
	force := slack.NewButtonBlockElement(actionForce, key, slack.NewTextBlockObject(slack.PlainTextType, "Run anyway", false, false))
	opts := []slack.MsgOption{
		slack.MsgOptionText(text, false),
		slack.MsgOptionBlocks(section, slack.NewActionBlock("ask", force)),
		slack.MsgOptionTS(threadTS),
		slack.MsgOptionDisableLinkUnfurl(),
	}
	if _, _, err := p.api.PostMessage(channel, opts...); err != nil {
		log.Printf("post ask to %s: %v", channel, err)
	}
}

func (p slackPoster) Update(channel, ts, text string) {
	if _, _, _, err := p.api.UpdateMessage(channel, ts, slack.MsgOptionText(text, false), slack.MsgOptionBlocks()); err != nil {
		log.Printf("update %s/%s: %v", channel, ts, err)
	}
}

const (
	actionRun    = "orch_run"
	actionCancel = "orch_cancel"
	actionForce  = "orch_force"
)

func (p slackPoster) React(channel, ts, emoji string) {
	_ = p.api.AddReaction(emoji, slack.NewRefToMessage(channel, ts))
}

// Bridge turns messages into runs: one run at a time per project, the rest wait in order.
type Bridge struct {
	cfg        *Config
	out        Poster
	newRunner  func(project string, ref SlackRef) Runner
	readTicket TicketReader // nil without a jira section

	mu      sync.Mutex
	queues  map[string]chan job
	seen    map[string]time.Time // message key -> when it arrived, pruned after seenTTL
	pending map[string]pending
	asks    map[string]job // thread -> a request the orchestrator found unclear
	wg      sync.WaitGroup
	ctx     context.Context
}

type job struct {
	req      Request
	user     string
	channel  string
	ts       string    // the thread
	resumeID string    // set: run this approved plan instead of a new task
	at       time.Time // when it was parked in asks, for decisionTTL
}

func NewBridge(ctx context.Context, cfg *Config, out Poster) *Bridge {
	b := &Bridge{cfg: cfg, out: out, queues: map[string]chan job{}, seen: map[string]time.Time{}, pending: map[string]pending{}, asks: map[string]job{}, ctx: ctx}
	if cfg.Jira != nil {
		b.readTicket = jiraReader(cfg.Jira)
	}
	b.newRunner = func(project string, ref SlackRef) Runner {
		p := cfg.Projects[project]
		if p.Runner == "gitlab" {
			return &GitLabRunner{cfg: p.GitLab, Slack: ref}
		}
		return &LocalRunner{cfg: p.Local}
	}
	return b
}

// Handle is one message: a mention in a channel or a direct message.
func (b *Bridge) Handle(user, channel, ts, threadTS, text string) {
	b.mu.Lock()
	key := channel + "/" + ts
	b.pruneLocked(time.Now())
	if _, dup := b.seen[key]; dup { // app_mention and message.im can both deliver one message
		b.mu.Unlock()
		return
	}
	b.seen[key] = time.Now()
	b.mu.Unlock()

	thread := threadTS
	if thread == "" {
		thread = ts
	}
	if !b.cfg.Allowed(user) {
		log.Printf("refused: user %s is not in allowed_users", user)
		b.out.Post(channel, thread, ":no_entry: you are not allowed to start runs (allowed_users)")
		return
	}
	if threadTS != "" {
		b.mu.Lock()
		prev, ok := b.asks[channel+"/"+threadTS]
		if ok {
			delete(b.asks, channel+"/"+threadTS)
		}
		b.mu.Unlock()
		if ok {
			// A reply to "the task needs more detail": the answer joins the same request.
			add := strings.TrimSpace(unescapeSlack(strings.TrimSpace(mentionRE.ReplaceAllString(text, ""))))
			if add != "" {
				prev.req.Task = strings.TrimSpace(prev.req.Task + "\n" + add)
			}
			prev.user = user
			b.out.Post(channel, thread, ":memo: added to the task, planning again")
			b.enqueue(prev.req.Project, prev)
			return
		}
	}
	req, err := b.cfg.Parse(text, channel)
	if err != nil {
		b.out.Post(channel, thread, ":grey_question: "+err.Error())
		return
	}
	if req.Jira != "" && b.readTicket != nil {
		if !b.projectFromTicket(&req, channel, thread) {
			return
		}
	}
	if req.Project == "" {
		b.out.Post(channel, thread, ":grey_question: no project: start the message with `<project>: ` ("+strings.Join(b.cfg.ProjectNames(), ", ")+")")
		return
	}
	b.enqueue(req.Project, job{req: req, user: user, channel: channel, ts: thread})
}

// projectFromTicket reads the ticket and sets the project from its jira_match. false: stop.
func (b *Bridge) projectFromTicket(req *Request, channel, thread string) bool {
	t, err := b.readTicket(b.ctx, req.Jira)
	if err != nil {
		log.Printf("read %s: %v", req.Jira, err)
		b.out.Post(channel, thread, fmt.Sprintf(":warning: could not read %s (%v)", req.Jira, err))
		return req.Project != ""
	}
	req.ticketText = t.Summary + "\n" + t.Description
	if req.projectSet {
		return true
	}
	suffix := ""
	if p := b.cfg.ProjectFor(t); p != "" {
		req.Project = p
	} else if req.Project != "" {
		suffix = " (default)"
	} else {
		b.out.Post(channel, thread, fmt.Sprintf(":grey_question: %s (%s) matches no project: start the message with `<project>: ` (%s)",
			t.Key, t.Summary, strings.Join(b.cfg.ProjectNames(), ", ")))
		return false
	}
	b.out.Post(channel, thread, fmt.Sprintf(":ticket: *%s* %s → *%s*%s", t.Key, t.Summary, req.Project, suffix))
	return true
}

func (b *Bridge) enqueue(project string, j job) {
	b.mu.Lock()
	q, ok := b.queues[project]
	if !ok {
		q = make(chan job, 64)
		b.queues[project] = q
		b.wg.Add(1)
		go b.worker(project, q)
	}
	waiting := len(q)
	b.mu.Unlock()
	select {
	case q <- j:
	default:
		b.out.Post(j.channel, j.ts, ":no_entry: the queue of "+project+" is full")
		return
	}
	b.out.React(j.channel, j.ts, "eyes")
	if waiting > 0 {
		b.out.Post(j.channel, j.ts, fmt.Sprintf(":hourglass: queued for *%s*, %d ahead", project, waiting))
	}
}

func (b *Bridge) worker(project string, q chan job) {
	defer b.wg.Done()
	for {
		select {
		case <-b.ctx.Done():
			return
		case j := <-q:
			b.run(project, j)
		}
	}
}

func (b *Bridge) run(project string, j job) {
	r := b.newRunner(project, SlackRef{Channel: j.channel, TS: j.ts})
	progress := b.live(j.channel, j.ts)
	pl, canPlan := r.(Planner)
	if j.resumeID == "" {
		b.addAPIContract(project, &j)
	}
	if j.resumeID == "" && b.cfg.Projects[project].Approve {
		if !canPlan {
			b.out.Post(j.channel, j.ts, ":no_entry: approve is only supported by runner: local")
			return
		}
		b.plan(project, j, pl)
		return
	}
	var o Outcome
	if j.resumeID != "" && canPlan {
		o = pl.Resume(b.ctx, j.resumeID, progress)
	} else {
		o = r.Run(b.ctx, j.req, progress)
	}
	log.Printf("%s: run finished ok=%v", project, o.OK)
	if o.NeedsInput {
		b.askMore(j, o)
		return
	}
	if b.out.Post(j.channel, j.ts, o.Text) == "" {
		b.out.Post(j.channel, j.ts, "the result did not fit in a message; see the run directory")
	}
	if o.OK {
		b.out.React(j.channel, j.ts, "white_check_mark")
	} else {
		b.out.React(j.channel, j.ts, "x")
	}
}

func main() {
	cfgPath := flag.String("config", "slackbridge.yaml", "the bridge configuration")
	check := flag.Bool("check", false, "validate the configuration and the tokens, then exit")
	flag.Parse()
	log.SetFlags(log.LstdFlags | log.Lmsgprefix)
	log.SetPrefix("slackbridge: ")

	cfg, err := LoadConfig(*cfgPath)
	if err != nil {
		log.Fatal(err)
	}
	bot, app := os.Getenv(cfg.Slack.BotTokenEnv), os.Getenv(cfg.Slack.AppTokenEnv)
	if bot == "" || app == "" {
		log.Fatalf("%s and %s must be set (bot xoxb-..., app-level xapp-... with connections:write)",
			cfg.Slack.BotTokenEnv, cfg.Slack.AppTokenEnv)
	}
	api := slack.New(bot, slack.OptionAppLevelToken(app))
	auth, err := api.AuthTest()
	if err != nil {
		log.Fatalf("slack auth: %v", err)
	}
	log.Printf("connected as %s in %s; projects: %v", auth.User, auth.Team, cfg.ProjectNames())
	if *check {
		return
	}

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	sm := socketmode.New(api)
	b := NewBridge(ctx, cfg, slackPoster{api})

	go func() {
		for evt := range sm.Events {
			if evt.Type == socketmode.EventTypeInteractive {
				cb, ok := evt.Data.(slack.InteractionCallback)
				if evt.Request != nil {
					sm.Ack(*evt.Request)
				}
				if !ok || cb.Type != slack.InteractionTypeBlockActions {
					continue
				}
				for _, a := range cb.ActionCallback.BlockActions {
					if a.ActionID == actionForce {
						go b.Force(cb.User.ID, a.Value, cb.Channel.ID, cb.Message.Timestamp)
					}
					if a.ActionID == actionRun || a.ActionID == actionCancel {
						go b.Decide(cb.User.ID, a.Value, a.ActionID == actionRun, cb.Channel.ID, cb.Message.Timestamp)
					}
				}
				continue
			}
			if evt.Type != socketmode.EventTypeEventsAPI {
				continue
			}
			ev, ok := evt.Data.(slackevents.EventsAPIEvent)
			if !ok {
				continue
			}
			if evt.Request != nil {
				sm.Ack(*evt.Request)
			}
			switch e := ev.InnerEvent.Data.(type) {
			case *slackevents.AppMentionEvent:
				go b.Handle(e.User, e.Channel, e.TimeStamp, e.ThreadTimeStamp, e.Text)
			case *slackevents.MessageEvent:
				// Direct messages only; the bot's own messages and edits are skipped.
				if e.ChannelType == "im" && e.BotID == "" && e.SubType == "" && e.User != auth.UserID {
					go b.Handle(e.User, e.Channel, e.TimeStamp, e.ThreadTimeStamp, e.Text)
				}
			}
		}
	}()
	if err := sm.RunContext(ctx); err != nil && ctx.Err() == nil {
		log.Fatal(err)
	}
}

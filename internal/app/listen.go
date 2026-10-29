package app

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"time"

	"orchestrator/internal/apperr"
	"orchestrator/internal/config"
	"orchestrator/internal/domain"
	"orchestrator/internal/execution"
	"orchestrator/internal/jira"
	"orchestrator/internal/listener"
	"orchestrator/internal/persistence"
	"orchestrator/internal/repository"
	"orchestrator/internal/secretscan"
)

// orchestrator jira listen (specs/jira-listener/SPEC.md): a supervisor that polls Jira for tickets
// labelled ai-run, takes them one at a time, and runs each as a child `orchestrator run
// --listener-task` in its service's directory. It owns Jira, the services directory and its state;
// the child knows nothing of any of them.

const (
	listenUsage = "usage: orchestrator jira listen [--config orchestrator.yaml] [--once]"

	// maxListenIssues bounds the issues one poll looks at; the rest wait for the next one.
	maxListenIssues = 200
	// listenerStateDir is the listener's own directory in the working directory: state.json, the lock
	// and the task files.
	listenerStateDir = ".orchestrator/listener"
)

// listenOptions are the command line of jira listen.
type listenOptions struct {
	configPath string
	// once runs one poll and the ticket it dispatches, then returns (tests, cron).
	once    bool
	verbose bool
}

// jiraListenArgs checks the command line of `jira listen`: only --once, and none of the flags that
// say what a run is or how it runs (the listener's children always run --mode auto in a worktree).
func jiraListenArgs(cli cliArgs, rest []string) (once bool, err error) {
	if len(rest) < 2 || rest[1] != "listen" {
		return false, &apperr.ConfigError{Msg: listenUsage}
	}
	for _, a := range rest[2:] {
		if a != "--once" {
			return false, &apperr.ConfigError{Msg: fmt.Sprintf("jira listen takes no task text and no argument but --once, got %q\n%s", a, listenUsage)}
		}
		once = true
	}
	switch {
	case cli.mode != "":
		return false, &apperr.ConfigError{Msg: "jira listen cannot be combined with --mode: its runs are always --mode auto"}
	case len(cli.stages) > 0:
		return false, &apperr.ConfigError{Msg: "jira listen cannot be combined with --stage"}
	case cli.jiraRef != "":
		return false, &apperr.ConfigError{Msg: "jira listen cannot be combined with --jira"}
	case cli.isolation != "":
		return false, &apperr.ConfigError{Msg: "jira listen cannot be combined with --" + cli.isolation + ": its runs are always worktree runs"}
	case cli.storyTaskFile != "" || cli.listenerTaskFile != "":
		return false, &apperr.ConfigError{Msg: "jira listen cannot be combined with a task file"}
	case cli.allowRedTests:
		return false, &apperr.ConfigError{Msg: "jira listen cannot be combined with --allow-red-tests"}
	}
	return once, nil
}

// listen is `orchestrator jira listen`. It returns nil after SIGTERM or SIGINT (ctx cancelled) with
// nothing left half-written, an error that maps to exit 2 for a configuration, a credential or a
// FailureAuth stop (SPEC §10), and any other error for exit 1.
func listen(ctx context.Context, opts listenOptions, stderr io.Writer) error {
	reporter := newReporter(stderr, opts.verbose)
	defer reporter.Finish()
	osExec := execution.NewOSProcessExecutor()
	registry := newRegistry(osExec)

	cfg, _, _, _, _, err := loadAndValidateConfig(opts.configPath, "", "", registry, osExec, reporter,
		func(config.Config) config.DependencyOptions { return config.DependencyOptions{Listener: true} })
	if err != nil {
		return err
	}
	if cfg.Listener == nil || cfg.GitLab == nil || cfg.Jira == nil {
		return &apperr.ConfigError{Msg: fmt.Sprintf("jira listen needs the listener, gitlab and jira sections in %s", opts.configPath)}
	}
	wd, err := os.Getwd()
	if err != nil {
		return fmt.Errorf("cannot find the working directory: %w", err)
	}
	configPath, err := filepath.Abs(opts.configPath)
	if err != nil {
		return &apperr.ConfigError{Msg: "cannot resolve the configuration path", Err: err}
	}
	exe, err := selfExecutable()
	if err != nil {
		return err
	}
	// The task file names the variables its child must keep from every process it starts; the
	// child refuses a name that is not an upper-case variable name.
	scrub := listenerScrubEnv(cfg)
	for _, name := range scrub {
		if !scrubEnvNameRE.MatchString(name) {
			return &apperr.ConfigError{Msg: fmt.Sprintf("the variable name %q (a Jira or GitLab credential) is not an upper-case name the listener can hand to its runs", name)}
		}
	}

	stateDir := filepath.Join(wd, filepath.FromSlash(listenerStateDir))
	unlock, held, err := persistence.LockFile(filepath.Join(stateDir, "lock"))
	if err != nil {
		return err
	}
	if held {
		return &apperr.ConfigError{Msg: fmt.Sprintf("another orchestrator jira listen is running in %s (%s is locked)", wd, filepath.Join(stateDir, "lock"))}
	}
	defer unlock()

	servicesDir := cfg.Listener.ServicesDir
	if !filepath.IsAbs(servicesDir) {
		servicesDir = filepath.Join(wd, servicesDir)
	}
	servicesDir = filepath.Clean(servicesDir)

	r := &listenRunner{
		ctx: ctx, bg: context.WithoutCancel(ctx), cfg: cfg, lc: cfg.Listener,
		configPath: configPath, wd: wd, exe: exe, once: opts.once, stderr: stderr, osExec: osExec,
		servicesDir: servicesDir, stateFile: filepath.Join(stateDir, "state.json"), tasksDir: filepath.Join(stateDir, "tasks"),
		scrub: scrub, now: time.Now, sleep: sleepContext,
	}
	r.childEnv = r.environment()
	r.checkout = &serviceCheckout{
		servicesDir: servicesDir, baseURL: cfg.GitLab.BaseURL, allowedGroups: cfg.GitLab.AllowedGroups,
		cloneTimeout: cfg.Listener.CloneTimeout, executor: osExec, env: r.childEnv,
	}
	r.jira = newListenerJiraClient(cfg)

	bot, err := r.jira.Myself(ctx)
	if err != nil {
		if ctx.Err() != nil {
			return nil
		}
		return err
	}
	r.bot = bot

	if err := r.loadState(); err != nil {
		return err
	}
	if err := r.checkout.removeStaleClones(); err != nil {
		return err
	}
	r.logf("listening on %s for %s tickets labelled %s", cfg.Jira.BaseURL, strings.Join(cfg.Listener.Query.Projects, ", "), cfg.Listener.Query.Label)
	if err := r.recover(); err != nil {
		return err
	}
	if r.stopErr != nil {
		return r.stopErr
	}
	return r.loop()
}

// listenerScrubEnv is the credential variables the listener names in every task file: the Jira
// ones and the GitLab token.
func listenerScrubEnv(cfg config.Config) []string {
	var names []string
	for _, n := range []string{cfg.Jira.TokenEnv, cfg.Jira.EmailEnv, cfg.GitLab.TokenEnv} {
		if n != "" && !slices.Contains(names, n) {
			names = append(names, n)
		}
	}
	return names
}

// newListenerJiraClient builds the listener's Jira client. With newJiraWriteClient, taskFromJira and
// storyRunner.newJiraClient it is a place the credential values are read from the environment.
func newListenerJiraClient(cfg config.Config) *jira.Client {
	j := cfg.Jira
	creds := jira.NewCredentials(j.Auth, os.Getenv(j.EmailEnv), os.Getenv(j.TokenEnv))
	fields := make([]jira.Field, len(j.Fields))
	for i, f := range j.Fields {
		fields[i] = jira.Field{ID: f.ID, Label: f.Label}
	}
	return jira.NewHTTPClient(jira.Options{
		BaseURL: j.BaseURL, Auth: j.Auth, EmailEnv: j.EmailEnv, TokenEnv: j.TokenEnv,
		Timeout: j.Timeout, MaxRetries: j.MaxRetries, Fields: fields,
		UserAgent: "orchestrator/" + Version,
	}, creds)
}

func sleepContext(ctx context.Context, d time.Duration) error {
	t := time.NewTimer(d)
	defer t.Stop()
	select {
	case <-t.C:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

// listenRunner is one running listener.
type listenRunner struct {
	ctx context.Context
	// bg is ctx without its cancellation: what is written after a child ended must also happen when
	// the listener was told to stop.
	bg          context.Context
	cfg         config.Config
	lc          *config.ListenerConfig
	configPath  string
	wd          string
	exe         string
	once        bool
	stderr      io.Writer
	osExec      execution.ProcessExecutor
	servicesDir string
	stateFile   string
	tasksDir    string
	scrub       []string

	jira     *jira.Client
	bot      string
	checkout *serviceCheckout
	childEnv []string

	state listener.State
	now   func() time.Time
	sleep func(context.Context, time.Duration) error
	// stopErr, once set, is why the listener stops with exit 2 (SPEC §10).
	stopErr error
	// jiraAuth is set when Jira refused the credentials: nothing more is written to it.
	jiraAuth bool
}

func (r *listenRunner) logf(format string, args ...any) {
	fmt.Fprintf(r.stderr, "listen: "+format+"\n", args...)
}

// environment is the environment of every git process the listener starts and of every child: the
// process's own without the Jira variables and without the parent-session variables, plus the
// credential helper's entries (SPEC §6.2). The GitLab token stays, for the helper; provider CLIs
// and the test command never get it (the child scrubs it, scrub_env).
func (r *listenRunner) environment() []string {
	drop := map[string]bool{r.cfg.Jira.EmailEnv: true, r.cfg.Jira.TokenEnv: true}
	var env []string
	for _, kv := range os.Environ() {
		name, _, _ := strings.Cut(kv, "=")
		if drop[name] || execution.IsParentSessionVar(name) {
			continue
		}
		env = append(env, kv)
	}
	return withListenerGit(env, r.exe, r.cfg.GitLab.BaseURL, "--config", r.configPath)
}

// loadState reads state.json; a missing file is an empty state, a corrupt one stops the listener.
func (r *listenRunner) loadState() error {
	data, err := os.ReadFile(r.stateFile)
	if errors.Is(err, os.ErrNotExist) {
		r.state = listener.NewState()
		return nil
	}
	if err != nil {
		return fmt.Errorf("cannot read the listener state: %w", err)
	}
	st, err := listener.ParseState(data)
	if err != nil {
		return &apperr.ConfigError{Msg: fmt.Sprintf("%s: %v; fix or remove it", r.stateFile, err)}
	}
	r.state = st
	return nil
}

// save writes state.json atomically.
func (r *listenRunner) save() error {
	data, err := r.state.Marshal()
	if err != nil {
		return fmt.Errorf("cannot save the listener state: %w", err)
	}
	if err := os.MkdirAll(filepath.Dir(r.stateFile), 0o755); err != nil {
		return fmt.Errorf("cannot save the listener state: %w", err)
	}
	if err := persistence.ReplaceFile(r.stateFile, data); err != nil {
		return fmt.Errorf("cannot save the listener state: %w", err)
	}
	return nil
}

// update changes the entry of key and saves.
func (r *listenRunner) update(key string, change func(e *listener.Entry)) error {
	e := r.state.Tickets[key]
	change(&e)
	if err := r.state.Set(key, e); err != nil {
		return err
	}
	return r.save()
}

// isAuth reports whether err is Jira refusing the credentials, and if so makes it the reason the
// listener stops.
func (r *listenRunner) isAuth(err error) bool {
	var jerr *apperr.JiraError
	if errors.As(err, &jerr) && jerr.Kind == apperr.JiraAuth {
		r.jiraAuth = true
		if r.stopErr == nil {
			r.stopErr = err
		}
		return true
	}
	return false
}

// stopping reports whether the listener was told to stop.
func (r *listenRunner) stopping() bool { return r.ctx.Err() != nil }

// loop polls while idle (SPEC §3.2) until it is told to stop or a stop of §10 happens.
func (r *listenRunner) loop() error {
	failures := 0
	for {
		if r.stopping() {
			return nil
		}
		dispatched, err := r.poll()
		switch {
		case r.stopErr != nil:
			return r.stopErr
		case r.stopping():
			return nil
		case err != nil && !isFatalListen(err):
			if r.once {
				return err
			}
			failures++
			wait := listener.Backoff(r.lc.PollInterval, failures+1)
			r.logf("the poll failed, the next one is in %s: %s", wait, firstLineOf(err.Error()))
			if r.sleep(r.ctx, wait) != nil {
				return nil
			}
			continue
		case err != nil:
			return err
		}
		failures = 0
		if r.once {
			return nil
		}
		if dispatched {
			continue
		}
		if r.sleep(r.ctx, r.lc.PollInterval) != nil {
			return nil
		}
	}
}

// listenFatal wraps an error that ends the listener with exit 1 (the state cannot be saved, say).
type listenFatal struct{ err error }

func (e listenFatal) Error() string { return e.err.Error() }
func (e listenFatal) Unwrap() error { return e.err }

func isFatalListen(err error) bool {
	var f listenFatal
	return errors.As(err, &f)
}

// poll searches once and dispatches the first ticket that can be taken.
func (r *listenRunner) poll() (bool, error) {
	// A claim that may have reached Jira and whose status could not be read is settled first.
	for _, key := range r.state.Unfinished() {
		if r.state.Tickets[key].Phase != listener.PhaseClaiming || r.stopping() || r.stopErr != nil {
			continue
		}
		if err := r.recoverEntry(key); err != nil {
			return false, listenFatal{err}
		}
	}
	if r.stopping() || r.stopErr != nil {
		return false, nil
	}
	q := r.lc.Query
	heads, err := r.jira.Search(r.ctx, jira.BuildJQL(q.Projects, q.Label, q.Statuses, q.Reporters), maxListenIssues)
	if err != nil {
		r.isAuth(err)
		return false, err
	}
	for _, h := range heads {
		if r.stopping() || r.stopErr != nil {
			return false, nil
		}
		if !r.eligible(h) {
			continue
		}
		dispatched, err := r.dispatch(h)
		if err != nil {
			return dispatched, listenFatal{err}
		}
		if dispatched || r.stopErr != nil {
			return dispatched, nil
		}
	}
	return false, nil
}

// eligible is the Go re-check of what the JQL filtered (SPEC §3.3): the search is a filter, not a
// boundary.
func (r *listenRunner) eligible(h domain.TicketHead) bool {
	q := r.lc.Query
	project, _, _ := strings.Cut(h.Key, "-")
	if !slices.ContainsFunc(q.Projects, func(p string) bool { return strings.EqualFold(p, project) }) ||
		!slices.Contains(h.Labels, q.Label) ||
		!slices.ContainsFunc(q.Statuses, func(s string) bool { return strings.EqualFold(s, h.Status) }) ||
		!slices.Contains(q.Reporters, h.Reporter) {
		return false
	}
	e, ok := r.state.Entry(h.Key)
	switch {
	case !ok:
		return true
	case e.Phase != listener.PhaseFinished:
		return false
	case e.Outcome == listener.ResultSpent:
		return false
	}
	return !e.Waiting(r.now())
}

// isEpic reports whether the issue type is one story.jira_issue_types sends to a story plan.
func (r *listenRunner) isEpic(issueType string) bool {
	types := []string{"Epic"}
	if r.cfg.Story != nil && len(r.cfg.Story.JiraIssueTypes) > 0 {
		types = r.cfg.Story.JiraIssueTypes
	}
	return slices.ContainsFunc(types, func(t string) bool { return strings.EqualFold(t, strings.TrimSpace(issueType)) })
}

// dispatch claims the ticket (SPEC §4) and carries it through its cycle. It reports whether the
// ticket was taken: a claim that cannot be made leaves everything as it was and the next ticket is
// tried.
func (r *listenRunner) dispatch(h domain.TicketHead) (bool, error) {
	key := h.Key
	// What can never run is ended from the queue, before any claim (SPEC §3.3, §8).
	if e, ok := r.state.Entry(key); ok && e.Spent(r.lc.MaxAttempts) {
		return false, r.endUnclaimed(h, listener.ResultSpent, "")
	}
	if r.isEpic(h.Type) {
		return false, r.endUnclaimed(h, listener.ResultBlocked,
			fmt.Sprintf("%s is an %s: plan it as a story (orchestrator story plan --jira %s); the listener runs one ticket as one run in one service.", key, h.Type, key))
	}

	transitions, err := r.jira.Transitions(r.ctx, key)
	if err != nil {
		if !r.isAuth(err) && !r.stopping() {
			r.logf("%s skipped: cannot read its transitions: %s", key, firstLineOf(err.Error()))
		}
		return false, nil
	}
	start, ok := findTransition(transitions, r.lc.Transitions.Start)
	if !ok {
		r.logf("%s skipped: the transition %q is not offered", key, r.lc.Transitions.Start)
		return false, nil
	}
	startStatus := start.To
	if startStatus == "" {
		startStatus = r.lc.Transitions.Start
	}

	prev, had := r.state.Entry(key)
	if err := r.state.Begin(key); err != nil {
		r.logf("%s skipped: %v", key, err)
		return false, nil
	}
	if err := r.update(key, func(e *listener.Entry) { e.Status = startStatus }); err != nil {
		return false, err
	}
	if err := r.jira.Transition(r.ctx, key, start.ID); err != nil {
		if !jira.MayExist(err) {
			// Refused, or never sent: a person, or another listener, moved the ticket first, and the
			// workflow allows the claim from the queue's status only. This is the only lock in Jira
			// (SPEC §4), so the ticket is skipped whatever its status is now.
			if !r.isAuth(err) && !r.stopping() {
				r.logf("%s skipped: the claim was refused: %s", key, firstLineOf(err.Error()))
			}
			return false, r.restore(key, prev, had)
		}
		// The claim may have reached Jira: the entry stays claiming until the status tells (SPEC §9).
		if r.stopping() {
			r.logf("%s: the claim may have been made; its status is read when the listener starts again", key)
			return false, nil
		}
		status, serr := r.jira.Status(r.bg, key)
		switch {
		case serr != nil:
			r.isAuth(serr)
			r.logf("%s: the claim may have been made and its status cannot be read; it is read again before the next poll: %s", key, firstLineOf(serr.Error()))
			return false, nil
		case !strings.EqualFold(status, startStatus):
			r.logf("%s skipped: the claim failed: %s", key, firstLineOf(err.Error()))
			return false, r.restore(key, prev, had)
		}
	}
	if err := r.update(key, func(e *listener.Entry) { e.Phase = listener.PhaseClaimed }); err != nil {
		return true, err
	}
	r.logf("%s claimed", key)
	return true, r.process(h)
}

// restore puts the entry of key back as it was before a claim that did not happen.
func (r *listenRunner) restore(key string, prev listener.Entry, had bool) error {
	if had {
		r.state.Tickets[key] = prev
	} else {
		r.state.Drop(key)
	}
	return r.save()
}

// process takes a claimed ticket as far as its outcome.
func (r *listenRunner) process(h domain.TicketHead) error {
	key := h.Key
	dirs, err := r.checkout.serviceDirs()
	if err != nil {
		return r.endInfra(key, err)
	}
	service, err := listener.Service(h.Labels, h.Components, dirs, r.lc.Labels.ServicePrefix)
	if err != nil {
		return r.endBefore(key, listener.ResultBlocked, err.Error())
	}
	project, err := listener.Project(h.Labels, r.lc.Labels.ProjectPrefix)
	if err != nil {
		return r.endBefore(key, listener.ResultBlocked, err.Error())
	}
	if err := r.update(key, func(e *listener.Entry) { e.Service = service }); err != nil {
		return err
	}

	res, err := r.checkout.prepare(r.ctx, service, project)
	if err != nil {
		return r.checkoutProblem(key, err)
	}
	args, err := r.configArgs(res.Dir)
	if err != nil {
		return r.endBefore(key, listener.ResultFailed, err.Error())
	}

	ticket, err := r.jira.Fetch(r.ctx, key)
	if err != nil {
		return r.jiraProblem(key, err)
	}
	comments, err := r.jira.Comments(r.ctx, key)
	if err != nil {
		return r.jiraProblem(key, err)
	}
	text, err := jira.Render(ticket, "", r.cfg.Jira.MaxBytes, jira.RenderOptions{Comments: comments, Bot: r.bot})
	if err != nil {
		return r.endBefore(key, listener.ResultBlocked,
			fmt.Sprintf("The ticket and its discussion are longer than %d bytes; shorten it.", r.cfg.Jira.MaxBytes))
	}
	file, err := r.writeTask(ticket, text, r.state.Tickets[key].Attempts+1)
	if err != nil {
		return r.endInfra(key, err)
	}

	if r.stopping() {
		return nil
	}
	if err := r.update(key, func(e *listener.Entry) {
		e.Phase, e.StartedAt = listener.PhaseRunning, r.now().UTC()
	}); err != nil {
		return err
	}
	r.logf("%s running in %s", key, service)
	return r.execute(key, res.Dir, append([]string{"run", "--listener-task", file, "--mode", "auto", "--output", "json"}, args...))
}

// configArgs is the --config the child needs: none when the service has its own orchestrator.yaml
// (the repository's own rules apply, SPEC §6.4), else listener.default_config, absolute.
func (r *listenRunner) configArgs(serviceDir string) ([]string, error) {
	if _, err := os.Stat(filepath.Join(serviceDir, defaultConfigPath)); err == nil {
		return nil, nil
	}
	def := r.lc.DefaultConfig
	if def == "" {
		return nil, fmt.Errorf("the service %s has no %s and listener.default_config is not set", filepath.Base(serviceDir), defaultConfigPath)
	}
	if !filepath.IsAbs(def) {
		def = filepath.Join(r.wd, def)
	}
	if _, err := os.Stat(def); err != nil {
		return nil, fmt.Errorf("the service %s has no %s and listener.default_config %s cannot be read", filepath.Base(serviceDir), defaultConfigPath, r.lc.DefaultConfig)
	}
	return []string{"--config", def}, nil
}

// writeTask writes the task file of the child (SPEC §7.2) and returns its path.
func (r *listenRunner) writeTask(t domain.Ticket, text string, attempt int) (string, error) {
	in := listenerTask{
		V: listenerTaskVersion, Key: t.Key, URL: taskURL(t.URL), FetchedAt: t.FetchedAt.UTC(),
		Description: text, ScrubEnv: r.scrub,
	}
	data, err := json.Marshal(in)
	if err != nil {
		return "", err
	}
	// The child reads the file as it will check it here, so a task it would refuse never starts one.
	if _, err := parseListenerTask(data); err != nil {
		return "", err
	}
	if err := os.MkdirAll(r.tasksDir, 0o755); err != nil {
		return "", fmt.Errorf("cannot create %s: %w", r.tasksDir, err)
	}
	file := filepath.Join(r.tasksDir, fmt.Sprintf("%s.%d.json", t.Key, attempt))
	if err := persistence.ReplaceFile(file, data); err != nil {
		return "", fmt.Errorf("cannot write %s: %w", file, err)
	}
	return file, nil
}

// taskURL is the address the run records for the ticket. It is only displayed; the task file
// requires https, and config allows plain http for a Jira on a loopback address.
func taskURL(u string) string {
	if rest, ok := strings.CutPrefix(u, "http://"); ok {
		return "https://" + rest
	}
	return u
}

// checkoutProblem maps a checkout failure to SPEC §8 and §10.
func (r *listenRunner) checkoutProblem(key string, err error) error {
	if r.stopping() {
		return nil
	}
	var ce *checkoutError
	if !errors.As(err, &ce) {
		return r.endInfra(key, err)
	}
	r.logf("%s: %s", key, firstLineOf(ce.Error()))
	switch ce.Kind.class() {
	case classBlocked:
		return r.endBefore(key, listener.ResultBlocked, ce.Msg)
	case classFailed:
		return r.endBefore(key, listener.ResultFailed, ce.Msg)
	case classStop:
		r.stopErr = &apperr.ConfigError{Msg: "GitLab refused the credential in " + r.cfg.GitLab.TokenEnv + ": " + ce.Msg}
	}
	return r.endInfra(key, ce)
}

// jiraProblem maps a failed read of the ticket: a credential stops the listener, anything else is
// infrastructure.
func (r *listenRunner) jiraProblem(key string, err error) error {
	if r.stopping() {
		return nil
	}
	r.isAuth(err)
	return r.endInfra(key, err)
}

// endInfra puts a claimed ticket that did not run back in the queue: no comment, no attempt, and a
// wait before it is picked again (SPEC §8).
func (r *listenRunner) endInfra(key string, cause error) error {
	r.logf("%s goes back to the queue: %s", key, firstLineOf(cause.Error()))
	if err := r.update(key, func(e *listener.Entry) {
		e.Retries++
		e.Interrupted = false
		e.Outcome = listener.ResultRetry
		e.NextTryAt = r.now().UTC().Add(listener.Backoff(r.lc.PollInterval, e.Retries))
	}); err != nil {
		return err
	}
	return r.write(key, listener.Infrastructure(), nil)
}

// endBefore finishes a ticket that cannot be run at all, before any run: a comment saying why and
// the blocked or failed transition, or for a spent ticket the one comment and failed. No attempt is
// counted (SPEC §8).
func (r *listenRunner) endBefore(key string, result listener.Result, reason string) error {
	if err := r.update(key, func(e *listener.Entry) {
		e.Outcome = result
		e.Interrupted = false
		e.Reason = oneLineText(reason, 900)
		e.Retries = 0
	}); err != nil {
		return err
	}
	return r.write(key, listener.Before(result == listener.ResultBlocked), nil)
}

// endUnclaimed finishes a ticket found in the queue that can never run (its attempts spent, an Epic)
// without claiming it: one comment and the failed or blocked transition from the status it is in,
// which is where it must still be when they are written. The entry is held (claimed) and its outcome
// recorded in one save, so a restart writes what is missing (recoverClaimed).
func (r *listenRunner) endUnclaimed(h domain.TicketHead, result listener.Result, reason string) error {
	if err := r.state.Begin(h.Key); err != nil {
		r.logf("%s skipped: %v", h.Key, err)
		return nil
	}
	if err := r.update(h.Key, func(e *listener.Entry) {
		e.Status, e.Phase, e.Outcome = h.Status, listener.PhaseClaimed, result
		e.Reason = oneLineText(reason, 900)
		e.Retries = 0
	}); err != nil {
		return err
	}
	return r.write(h.Key, listener.Before(result == listener.ResultBlocked), nil)
}

// outcome is what is known of a finished child when its outcome is written.
type outcome struct {
	run  *domain.Run
	line childLine
}

// childLine is the part of the child's --output json line the listener reads.
type childLine struct {
	RunID         string    `json:"run_id"`
	Status        string    `json:"status"`
	ExitCode      int       `json:"exit_code"`
	FailureReason string    `json:"failure_reason"`
	FailureKind   string    `json:"failure_kind"`
	Error         string    `json:"error"`
	MR            *resultMR `json:"mr"`
}

// parseChildLine reads the last non-empty line of the child's stdout; a child that printed none
// gives the zero value.
func parseChildLine(out []byte) childLine {
	lines := bytes.Split(bytes.TrimSpace(out), []byte("\n"))
	var l childLine
	if len(lines) > 0 {
		_ = json.Unmarshal(lines[len(lines)-1], &l)
	}
	return l
}

// execute runs the child in the service's directory and writes its outcome.
func (r *listenRunner) execute(key, dir string, args []string) error {
	var stdout bytes.Buffer
	code, err := runChildEnv(r.ctx, r.exe, dir, r.childEnv, args, nil, &stdout, r.stderr)
	line := parseChildLine(stdout.Bytes())
	if code < 0 && err != nil {
		line.Error = "the run could not be started: " + err.Error()
		code = listener.ExitError
	}
	return r.afterChild(key, dir, code, line)
}

// afterChild decides the outcome of a child that ended, records it and writes it to Jira.
func (r *listenRunner) afterChild(key, dir string, code int, line childLine) error {
	e := r.state.Tickets[key]
	var run *domain.Run
	if listener.ValidRunID(line.RunID) {
		if loaded, err := persistence.NewStore(filepath.Join(dir, runsBaseDir)).LoadRun(line.RunID); err == nil {
			run = loaded
			e.RunID, e.RunDir = line.RunID, listener.RunDir(e.Service, line.RunID)
		} else {
			r.logf("%s: cannot read run %s: %s", key, line.RunID, firstLineOf(err.Error()))
		}
	}
	if code < 0 || listener.CutOff(code, r.stopping()) {
		// Killed, or cut off by the listener's stop: the run is resumed on restart.
		e.Interrupted = true
		if err := r.state.Set(key, e); err != nil {
			return err
		}
		r.logf("%s: the run was interrupted; it is resumed when the listener starts again", key)
		return r.save()
	}
	reason := line.FailureReason
	if reason == "" && run != nil {
		reason = run.FailureReason
	}
	d := listener.Decide(listener.RunOutcome{
		ExitCode: code, FailureKind: line.FailureKind, FailureReason: reason,
		MRCreated: run != nil && run.MR != nil && run.MR.State == domain.MRCreated,
	})
	e.Interrupted = false
	if d.Transition == listener.ResultNone {
		// The child was cancelled by someone else: it stays where it is, to be resumed on restart.
		e.Interrupted = true
		if err := r.state.Set(key, e); err != nil {
			return err
		}
		return r.save()
	}
	e.Outcome = d.Transition
	if d.Counted {
		e.Attempts++
		e.Retries = 0
	}
	if d.Backoff {
		e.Retries++
		e.NextTryAt = r.now().UTC().Add(listener.Backoff(r.lc.PollInterval, e.Retries))
	}
	if err := r.state.Set(key, e); err != nil {
		return err
	}
	if err := r.save(); err != nil {
		return err
	}
	if d.Stop {
		r.stopErr = &apperr.ConfigError{Msg: fmt.Sprintf("the run of %s failed on a credential: %s", key, firstLineOf(firstNonEmpty(line.Error, reason)))}
	}
	if code != listener.ExitOK {
		r.logf("%s: the run ended with exit %d", key, code)
	}
	return r.write(key, d, &outcome{run: run, line: line})
}

// write is the end of a cycle (SPEC §8): the comment and the transition for the outcome recorded in
// the entry, each with its intent in the state before the request, and the status read again before
// the transition. A write that is already recorded is not made again, and one with an intent and no
// result is not repeated: it may have reached Jira. A failed write is a log line, not a retry loop.
func (r *listenRunner) write(key string, d listener.Decision, oc *outcome) error {
	e := r.state.Tickets[key]
	target := r.transitionName(e.Outcome)
	startStatus := e.Status
	if startStatus == "" {
		startStatus = r.lc.Transitions.Start
	}

	moved := ""
	canMove := target != "" && e.Writes.Transition.State == ""
	if canMove || (d.Comment != listener.CommentNone && e.Writes.Comment.State == "") {
		status, err := r.jira.Status(r.bg, key)
		switch {
		case err != nil:
			r.isAuth(err)
			r.logf("%s: cannot read its status, so it is not moved: %s", key, firstLineOf(err.Error()))
			canMove = false
		case !strings.EqualFold(status, startStatus):
			moved = status
			canMove = false
			r.logf("%s: it is in %s now, not in %s: the outcome is commented, the ticket is not moved", key, status, startStatus)
		}
	}

	if d.Comment != listener.CommentNone && e.Writes.Comment.State == "" && !r.jiraAuth {
		body, runID := r.commentBody(key, e, d, oc)
		if moved != "" && target != "" {
			body = append(body, "", listener.Moved(jira.Escape, moved, target))
		}
		text := listener.ComposeFor(jira.Escape, body, runID)
		if err := r.update(key, func(e *listener.Entry) { e.Writes.Comment = listener.Write{State: listener.WriteIntent} }); err != nil {
			return err
		}
		id, err := r.jira.Comment(r.bg, key, text)
		switch {
		case err == nil:
			if err := r.update(key, func(e *listener.Entry) { e.Writes.Comment = listener.Write{State: listener.WriteDone, ID: id} }); err != nil {
				return err
			}
		case jira.MayExist(err):
			r.isAuth(err)
			r.logf("%s: the comment may have been posted and is not repeated: %s", key, firstLineOf(err.Error()))
		default:
			r.isAuth(err)
			r.logf("%s: cannot comment: %s", key, firstLineOf(err.Error()))
			if err := r.update(key, func(e *listener.Entry) { e.Writes.Comment = listener.Write{} }); err != nil {
				return err
			}
		}
		e = r.state.Tickets[key]
	}

	to := ""
	if canMove && !r.jiraAuth {
		to = r.moveTicket(key, target)
	}
	if e.Phase != listener.PhaseFinished {
		if err := r.update(key, func(e *listener.Entry) { e.Phase = listener.PhaseFinished }); err != nil {
			return err
		}
	}
	switch {
	case to != "" && oc != nil && oc.run != nil && oc.run.MR != nil && oc.run.MR.State == domain.MRCreated:
		r.logf("%s → %s (!%d)", key, to, oc.run.MR.IID)
	case to != "":
		r.logf("%s → %s", key, to)
	default:
		r.logf("%s finished: %s", key, e.Outcome)
	}
	return nil
}

// moveTicket makes the transition named target and returns the status it names, or "" when the
// ticket was not moved.
func (r *listenRunner) moveTicket(key, target string) string {
	transitions, err := r.jira.Transitions(r.bg, key)
	if err != nil {
		r.isAuth(err)
		r.logf("%s: cannot read its transitions, so it is not moved: %s", key, firstLineOf(err.Error()))
		return ""
	}
	chosen, ok := findTransition(transitions, target)
	if !ok {
		names := make([]string, len(transitions))
		for i, t := range transitions {
			names[i] = t.Name
		}
		r.logf("%s has no transition %q (available: %s)", key, target, strings.Join(names, ", "))
		return ""
	}
	if err := r.update(key, func(e *listener.Entry) { e.Writes.Transition = listener.Write{State: listener.WriteIntent} }); err != nil {
		r.logf("%v", err)
		return ""
	}
	if err := r.jira.Transition(r.bg, key, chosen.ID); err != nil {
		r.isAuth(err)
		if jira.MayExist(err) {
			r.logf("%s may have been moved by %q and is not moved again: %s", key, chosen.Name, firstLineOf(err.Error()))
		} else {
			r.logf("%s cannot be moved by %q: %s", key, chosen.Name, firstLineOf(err.Error()))
			_ = r.update(key, func(e *listener.Entry) { e.Writes.Transition = listener.Write{} })
		}
		return ""
	}
	_ = r.update(key, func(e *listener.Entry) { e.Writes.Transition = listener.Write{State: listener.WriteDone} })
	if chosen.To != "" {
		return chosen.To
	}
	return chosen.Name
}

// transitionName is the configured transition for a result, "" for none.
func (r *listenRunner) transitionName(res listener.Result) string {
	t := r.lc.Transitions
	switch res {
	case listener.ResultDone:
		return t.Done
	case listener.ResultBlocked:
		return t.Blocked
	case listener.ResultFailed, listener.ResultSpent:
		return t.Failed
	case listener.ResultRetry:
		return t.Retry
	}
	return ""
}

// commentBody is the body of the comment for an outcome, and the run id its last line names.
func (r *listenRunner) commentBody(key string, e listener.Entry, d listener.Decision, oc *outcome) ([]string, string) {
	if oc == nil {
		// Before any run: the reason the entry recorded, or the spent notice.
		if e.Outcome == listener.ResultSpent {
			return listener.Spent(jira.Escape, key, e.Attempts), ""
		}
		return listener.Blocked(jira.Escape, e.Reason), ""
	}
	run := oc.run
	runID := oc.line.RunID
	switch d.Comment {
	case listener.CommentMergeRequest:
		return r.mergeRequestBody(e.Service, run), runID
	case listener.CommentNeedsInput:
		return listener.NeedsInput(jira.Escape, r.openPoints(run), r.queueStatus()), runID
	case listener.CommentFailure:
		return r.failureBody(run, oc.line), runID
	}
	msg := oc.line.Error
	if msg == "" && run != nil {
		msg = run.FailureReason
	}
	return listener.Error(jira.Escape, r.scrubText(msg)), runID
}

// queueStatus is the status a ticket is moved back to after the questions are answered.
func (r *listenRunner) queueStatus() string {
	if len(r.lc.Query.Statuses) > 0 {
		return r.lc.Query.Statuses[0]
	}
	return r.lc.Transitions.Retry
}

// scrubText is model-written text, or the withheld notice when it looks like a secret.
func (r *listenRunner) scrubText(s string) string {
	if findings, _ := secretscan.Scan("comment", []byte(s)); len(findings) > 0 {
		return listener.Withheld
	}
	return s
}

// openPoints are the questions of the points no round answered.
func (r *listenRunner) openPoints(run *domain.Run) []string {
	if run == nil {
		return nil
	}
	var points []string
	for _, p := range run.Task.Clarification.Open() {
		points = append(points, r.scrubText(p.Question))
	}
	return points
}

// mergeRequestBody is the comment of a delivered run: the merge request, its title, and what the
// run found.
func (r *listenRunner) mergeRequestBody(service string, run *domain.Run) []string {
	if run == nil || run.MR == nil {
		return []string{"The run opened a merge request."}
	}
	title := ""
	if run.Commit != "" && service != "" {
		repo := &repository.Repository{Dir: filepath.Join(r.servicesDir, service), Executor: r.osExec}
		if subject, err := repo.CommitSubject(r.bg, run.Commit); err == nil {
			title = r.scrubText(subject)
		}
	}
	if title == "" && run.Worktree != nil {
		title = run.Worktree.Branch
	}
	found := domain.ComputeOutcome(*run, nil)
	c := jira.MergeRequestComment{IID: run.MR.IID, URL: run.MR.URL, Title: title, Review: string(found.ReviewVerdict)}
	if p := found.TestsPassedFinal; p != nil {
		c.Tests = map[bool]string{true: "passed", false: "failed"}[*p]
	}
	if found.Usage.Calls > 0 {
		c.Cost = fmt.Sprintf("$%.2f", found.Usage.CostUSD)
		if found.Usage.PartialCalls > 0 {
			c.Cost += " or more"
		}
	}
	return strings.Split(c.Wiki(), "\n")
}

// failureBody is the comment of a run that did not deliver (SPEC §8): why, and the last test or
// check that failed, or the review's verdict and its issues, or why there is no merge request.
func (r *listenRunner) failureBody(run *domain.Run, line childLine) []string {
	reason := line.FailureReason
	if reason == "" && run != nil {
		reason = run.FailureReason
	}
	var last string
	var review []string
	if run != nil {
		if mr := run.MR; mr != nil && reason == "" {
			reason = firstNonEmpty(mr.Error, mr.Skipped)
		}
		last = lastFailure(run)
		review = r.reviewLines(run)
	}
	if reason == "" {
		reason = firstNonEmpty(line.Error, "the run ended without a merge request")
	}
	body := listener.Failure(jira.Escape, r.scrubText(reason), r.scrubText(last))
	return append(body, review...)
}

// lastFailure names the last test or check that failed, "" when none did.
func lastFailure(run *domain.Run) string {
	for i := len(run.Executions) - 1; i >= 0; i-- {
		ex := run.Executions[i]
		if ex.Action != domain.ActionTest || ex.TestResult == nil {
			continue
		}
		t := ex.TestResult
		switch {
		case t.Success:
			return ""
		case t.FailedCheck != "":
			return fmt.Sprintf("check %s failed (exit %d)", t.FailedCheck, t.ExitCode)
		case len(t.FailedTests) > 0:
			names := t.FailedTests
			if len(names) > 5 {
				names = names[:5]
			}
			return fmt.Sprintf("%s failed (exit %d): %s", t.Command, t.ExitCode, strings.Join(names, ", "))
		}
		return fmt.Sprintf("%s failed (exit %d)", t.Command, t.ExitCode)
	}
	if p := run.PreRunTest; p != nil && !p.Success {
		return fmt.Sprintf("the test command %s was already failing before the run (exit %d)", p.Command, p.ExitCode)
	}
	return ""
}

// reviewLines is the verdict of the last review and its issues, when it failed.
func (r *listenRunner) reviewLines(run *domain.Run) []string {
	for i := len(run.Executions) - 1; i >= 0; i-- {
		ex := run.Executions[i]
		if ex.Action != domain.ActionReview || ex.ReviewResult == nil {
			continue
		}
		if ex.ReviewResult.Decide() != domain.ReviewFail {
			return nil
		}
		var issues []string
		for _, is := range ex.ReviewResult.Issues {
			if len(issues) == 5 {
				break
			}
			issues = append(issues, r.scrubText(fmt.Sprintf("%s %s: %s", is.Severity, is.File, is.Description)))
		}
		return listener.Review(jira.Escape, string(domain.ReviewFail), issues)
	}
	return nil
}

// recover finishes what a previous listener left half done (SPEC §9).
func (r *listenRunner) recover() error {
	for _, key := range r.state.Unfinished() {
		if r.stopping() || r.stopErr != nil {
			return nil
		}
		r.logf("%s was %s when the listener stopped", key, r.state.Tickets[key].Phase)
		if err := r.recoverEntry(key); err != nil {
			return err
		}
	}
	return nil
}

// recoverEntry carries one unfinished entry to its end. A claiming one is settled by the ticket's
// status: in the start status the claim happened and the entry is claimed; else the claim did not
// happen and the cycle is closed as none rather than dropped as SPEC §9 has it, so that the
// ticket's attempts are kept (a none cycle is picked again like an unknown ticket). A status that
// cannot be read leaves it claiming, for the next poll or start.
func (r *listenRunner) recoverEntry(key string) error {
	e := r.state.Tickets[key]
	if e.Phase == listener.PhaseClaiming {
		status, err := r.jira.Status(r.ctx, key)
		if err != nil {
			r.isAuth(err)
			r.logf("%s: cannot read its status: %s", key, firstLineOf(err.Error()))
			return nil
		}
		if !strings.EqualFold(status, e.Status) {
			r.logf("%s was not claimed: it is in %s", key, status)
			return r.update(key, func(e *listener.Entry) { e.Phase, e.Outcome = listener.PhaseFinished, listener.ResultNone })
		}
		if err := r.update(key, func(e *listener.Entry) { e.Phase = listener.PhaseClaimed }); err != nil {
			return err
		}
		r.logf("%s claimed", key)
		e = r.state.Tickets[key]
	}
	switch e.Phase {
	case listener.PhaseClaimed:
		return r.recoverClaimed(key, e)
	case listener.PhaseRunning:
		return r.recoverRunning(key, e)
	}
	return nil
}

// recoverClaimed: a ticket claimed with no run goes back to the queue, no attempt counted; an
// outcome decided before any run is written.
func (r *listenRunner) recoverClaimed(key string, e listener.Entry) error {
	switch e.Outcome {
	case "":
		return r.endInfra(key, errors.New("the listener stopped before its run started"))
	case listener.ResultRetry:
		return r.write(key, listener.Infrastructure(), nil)
	}
	return r.write(key, listener.Before(e.Outcome == listener.ResultBlocked), nil)
}

// recoverRunning resumes the interrupted run, or writes the outcome of one that finished.
func (r *listenRunner) recoverRunning(key string, e listener.Entry) error {
	dir := filepath.Join(r.servicesDir, e.Service)
	store := persistence.NewStore(filepath.Join(dir, runsBaseDir))
	run := r.findRun(store, dir, key, e)
	if run == nil {
		return r.endInfra(key, errors.New("the run it started cannot be found"))
	}
	if err := r.update(key, func(e *listener.Entry) {
		e.RunID, e.RunDir = run.ID, listener.RunDir(e.Service, run.ID)
	}); err != nil {
		return err
	}
	mrCreated := run.MR != nil && run.MR.State == domain.MRCreated
	code, kind := listener.ExitFromRun(run.Status, run.FailureReason, mrCreated)
	line := childLine{RunID: run.ID, Status: string(run.Status), ExitCode: code, FailureReason: run.FailureReason, FailureKind: kind}

	if e.Outcome != "" {
		// The outcome was decided before the listener stopped: only the writes not yet made. A
		// credential that stopped it then does not stop this listener again.
		d := listener.Decide(listener.RunOutcome{ExitCode: code, FailureKind: kind, FailureReason: run.FailureReason, MRCreated: mrCreated})
		if e.Outcome == listener.ResultRetry {
			d = listener.Infrastructure()
		}
		return r.write(key, d, &outcome{run: run, line: line})
	}
	// A run that did not end, or that the listener's stop cut off (a child it interrupted left a
	// cut call as a failure), goes on; one that reached a verdict of its own is written.
	resumable := run.Status == domain.RunPending || run.Status == domain.RunRunning || code == listener.ExitCancelled ||
		(e.Interrupted && run.Status != domain.RunCompleted && run.FailureReason != domain.FailureTaskNeedsInput)
	if !resumable {
		return r.afterChild(key, dir, code, line)
	}
	args, err := r.configArgs(dir)
	if err != nil {
		return r.endBefore(key, listener.ResultFailed, err.Error())
	}
	r.logf("%s: resuming run %s", key, run.ID)
	return r.execute(key, dir, append([]string{"resume", run.ID, "--mode", "auto", "--output", "json"}, args...))
}

// findRun is the run the ticket's child created: the recorded one, else the newest run of this
// ticket in the service that began after the child was about to start.
func (r *listenRunner) findRun(store *persistence.Store, dir, key string, e listener.Entry) *domain.Run {
	if e.RunID != "" {
		run, err := store.LoadRun(e.RunID)
		if err != nil {
			r.logf("%s: cannot read run %s: %s", key, e.RunID, firstLineOf(err.Error()))
			return nil
		}
		return run
	}
	found, _, _ := persistence.LoadRunDirs(filepath.Join(dir, runsBaseDir))
	var best *domain.Run
	for i := range found {
		run := found[i].Run
		src := run.Task.Source
		if src == nil || !src.Listener || src.Key != key || run.CreatedAt.Before(e.StartedAt.Add(-time.Second)) {
			continue
		}
		if best == nil || run.CreatedAt.After(best.CreatedAt) {
			best = &found[i].Run
		}
	}
	return best
}

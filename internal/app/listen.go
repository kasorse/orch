package app

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"runtime/debug"
	"slices"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/kasorse/orch/internal/apperr"
	"github.com/kasorse/orch/internal/config"
	"github.com/kasorse/orch/internal/domain"
	"github.com/kasorse/orch/internal/execution"
	"github.com/kasorse/orch/internal/jira"
	"github.com/kasorse/orch/internal/listener"
	"github.com/kasorse/orch/internal/persistence"
	"github.com/kasorse/orch/internal/provider"
	"github.com/kasorse/orch/internal/repository"
	"github.com/kasorse/orch/internal/secretscan"
)

// orchestrator jira listen (specs/jira-listener/SPEC.md): a supervisor that polls Jira for tickets
// labelled ai-run, takes them and runs each by calling the run (runTask, resumeRun) in its own
// goroutine, with its service's directory (specs/embed/SPEC.md §2): up to listener.max_parallel runs
// at once, at most one per service (specs/listener-story/SPEC.md §3). It owns Jira, the services
// directory and its state; the run knows nothing of any of them.

const (
	listenUsage = "usage: orchestrator jira listen [--config orchestrator.yaml] [--once]"

	// maxListenIssues bounds the issues one poll looks at; the rest wait for the next one.
	maxListenIssues = 200
	// listenerStateDir is the listener's own directory in the working directory: state.json, the lock
	// and the task files.
	listenerStateDir = ".orchestrator/listener"
)

// lockedWriter makes the listener's output safe for the goroutines of its runs: one Write at a time.
type lockedWriter struct {
	mu sync.Mutex
	w  io.Writer
}

func (l *lockedWriter) Write(p []byte) (int, error) {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.w.Write(p)
}

// listenOptions are the command line of jira listen.
type listenOptions struct {
	configPath string
	// once runs one poll and the ticket it dispatches, then returns (tests, cron).
	once bool
	// claudeAuth and codexAuth are --claude-auth and --codex-auth, checked here and passed to
	// every run; empty if not given.
	claudeAuth string
	codexAuth  string
	verbose    bool
}

// jiraListenArgs checks the command line of `jira listen`: only --once, and none of the flags that
// say what a run is or how it runs (the listener's runs are always --mode auto, in a worktree).
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
	case cli.storyTaskFile != "":
		return false, &apperr.ConfigError{Msg: "jira listen cannot be combined with a task file"}
	case cli.allowRedTests:
		return false, &apperr.ConfigError{Msg: "jira listen cannot be combined with --allow-red-tests"}
	}
	return once, nil
}

// listen is `orchestrator jira listen`. It returns nil after SIGTERM or SIGINT (ctx cancelled) with
// nothing left half-written, an error that maps to exit 2 for a configuration, a credential or a
// FailureAuth stop (SPEC §10), and any other error for exit 1.
//
// dir is the listener's working directory: its state, its lock, its task files and (when the
// configuration gives a relative path) its services directory. The CLI passes the process's;
// specs/embed/SPEC.md §4 lets a host name another. Nothing below reads the working directory.
func listen(ctx context.Context, dir string, opts listenOptions, stderr io.Writer) error {
	reporter := newReporter(stderr, opts.verbose)
	defer reporter.Finish()
	osExec := execution.NewOSProcessExecutor()
	registry := newRegistry(osExec)

	cfg, _, _, _, _, err := loadAndValidateConfig(opts.configPath, opts.claudeAuth, opts.codexAuth, registry, osExec, reporter,
		func(config.Config) config.DependencyOptions { return config.DependencyOptions{Listener: true} })
	if err != nil {
		return err
	}
	if cfg.Listener == nil || cfg.GitLab == nil || cfg.Jira == nil {
		return &apperr.ConfigError{Msg: fmt.Sprintf("jira listen needs the listener, gitlab and jira sections in %s", opts.configPath)}
	}
	wd, err := filepath.Abs(dir)
	if err != nil {
		return fmt.Errorf("cannot resolve the working directory %s: %w", dir, err)
	}
	// Absolute: the credential helper git starts is given it, and git runs in other directories.
	configPath, err := filepath.Abs(opts.configPath)
	if err != nil {
		return &apperr.ConfigError{Msg: "cannot resolve the configuration path", Err: err}
	}
	exe, err := selfExecutable()
	if err != nil {
		return err
	}
	// The task file names the variables the run must keep from every process it starts; the
	// task is refused (parseListenerTask) when a name is not an upper-case variable name.
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

	// The runs' context is the listener's, and is also cancelled when the listener stops for a reason
	// of its own (a credential, a state it cannot save): every run is then cut off and resumed on restart.
	runCtx, cancel := context.WithCancel(ctx)
	defer cancel()
	r := &listenRunner{
		ctx: runCtx, cancel: cancel, bg: context.WithoutCancel(ctx), cfg: cfg, lc: cfg.Listener,
		configPath: configPath, wd: wd, exe: exe, once: opts.once, claudeAuth: opts.claudeAuth, codexAuth: opts.codexAuth,
		stderr: &lockedWriter{w: stderr}, osExec: osExec,
		servicesDir: servicesDir, stateFile: filepath.Join(stateDir, "state.json"), tasksDir: filepath.Join(stateDir, "tasks"),
		logsDir: filepath.Join(stateDir, "logs"), scrub: scrub, now: time.Now, sleep: sleepContext,
		runs: map[string]string{}, wake: make(chan struct{}, 1),
		plansDir: filepath.Join(stateDir, listenerPlansDir), registry: registry,
	}
	// The Jira client takes the credentials from the environment at its construction; the
	// environment is shaped after it (specs/embed/SPEC.md §2.4), and the git processes of the
	// checkout get the shaped one.
	r.jira = newListenerJiraClient(cfg)
	if cfg.Listener.Story != nil {
		r.catalog = newListenerGitLab(cfg, r.logf)
	}
	if err := r.environment(); err != nil {
		return err
	}
	r.checkout = &serviceCheckout{
		servicesDir: servicesDir, baseURL: cfg.GitLab.BaseURL, allowedGroups: cfg.GitLab.AllowedGroups,
		cloneTimeout: cfg.Listener.CloneTimeout, executor: osExec, env: os.Environ(),
	}

	bot, err := r.jira.Myself(ctx)
	if err != nil {
		if ctx.Err() != nil {
			return nil
		}
		return err
	}
	r.bot = bot
	// The children a plan creates are reported by this account: before the first poll, it must be one
	// the poll accepts (specs/listener-story/SPEC.md §2, A15).
	if err := checkStoryReporter(cfg.Listener, bot); err != nil {
		return err
	}

	if err := r.loadState(); err != nil {
		return err
	}
	if err := r.checkout.removeStaleClones(); err != nil {
		return err
	}
	r.logf("listening on %s for %s tickets labelled %s", cfg.Jira.BaseURL, strings.Join(cfg.Listener.Query.Projects, ", "), cfg.Listener.Query.Label)
	return r.run()
}

// listenerScrubEnv is the credential variables the listener names in every task file: the Jira
// ones, the GitLab token and, when there is one, the read-only API token of the Epic's catalog.
func listenerScrubEnv(cfg config.Config) []string {
	var names []string
	for _, n := range []string{cfg.Jira.TokenEnv, cfg.Jira.EmailEnv, cfg.GitLab.TokenEnv, cfg.GitLab.APITokenEnv} {
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
	// cancel cancels ctx: the listener stopping for a reason of its own cuts every run off.
	cancel context.CancelFunc
	// bg is ctx without its cancellation: what is written after a run ended must also happen when
	// the listener was told to stop.
	bg          context.Context
	cfg         config.Config
	lc          *config.ListenerConfig
	configPath  string
	wd          string
	exe         string
	once        bool
	claudeAuth  string
	codexAuth   string
	stderr      io.Writer
	osExec      execution.ProcessExecutor
	servicesDir string
	stateFile   string
	tasksDir    string
	// logsDir holds <KEY>.<attempt>.log, the output of a run when more than one run goes at once.
	logsDir string
	scrub   []string

	jira     *jira.Client
	bot      string
	checkout *serviceCheckout

	// What an Epic's plan needs (specs/listener-story/SPEC.md §5–§7): the providers its calls run
	// on, the GitLab catalog (nil without listener.story) and the directory its artifacts go to.
	registry *provider.Registry
	catalog  planGitLab
	plansDir string

	// mu guards state, runs, planning, stopErr and fatal: the runs' goroutines and the loop change them.
	mu    sync.Mutex
	state listener.State
	// runs are the runs in progress (a slot each, specs/listener-story/SPEC.md §3): ticket key to
	// the service it runs in, "" until the service is known.
	runs map[string]string
	// planning is the Epic whose plan is in progress, "" when none: the plan holds every slot (A12),
	// so while it is set no slot is free and nothing starts.
	planning string
	// stopErr, once set, is why the listener stops with exit 2 (SPEC §10).
	stopErr error
	// fatal is the first error of a run's goroutine that ends the listener with exit 1.
	fatal error
	now   func() time.Time
	sleep func(context.Context, time.Duration) error
	// wg counts the runs' goroutines; wake tells the loop that one ended.
	wg   sync.WaitGroup
	wake chan struct{}
	// jiraAuth is set when Jira refused the credentials: nothing more is written to it.
	jiraAuth atomic.Bool
	// waitingOn is, by ticket key, the blocker it was last logged waiting for (SPEC §4 of
	// specs/listener-story): in memory, not in the state. Only the loop's goroutine reads it.
	waitingOn map[string]string
	// busyOn is the same for a ticket that waits for its service's run (SPEC §3).
	busyOn map[string]string
}

func (r *listenRunner) logf(format string, args ...any) {
	fmt.Fprintf(r.stderr, "listen: "+format+"\n", args...)
}

// maxRuns is the number of slots.
func (r *listenRunner) maxRuns() int { return max(1, r.lc.MaxParallel) }

// entryOf is the entry of key, empty when there is none.
func (r *listenRunner) entryOf(key string) listener.Entry {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.state.Tickets[key]
}

// entry is the entry of key and whether there is one.
func (r *listenRunner) entry(key string) (listener.Entry, bool) {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.state.Entry(key)
}

// unfinished are the keys whose cycle is not finished.
func (r *listenRunner) unfinished() []string {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.state.Unfinished()
}

// begin starts a new cycle of key in the state (not saved: update does it).
func (r *listenRunner) begin(key string) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.state.Begin(key)
}

// setEntry replaces the entry of key and saves.
func (r *listenRunner) setEntry(key string, e listener.Entry) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	if err := r.state.Set(key, e); err != nil {
		return err
	}
	return r.saveLocked()
}

// setStop makes err the reason the listener stops (the first one wins) and cuts every run off.
func (r *listenRunner) setStop(err error) {
	r.mu.Lock()
	if r.stopErr == nil {
		r.stopErr = err
	}
	r.mu.Unlock()
	if r.cancel != nil {
		r.cancel()
	}
}

func (r *listenRunner) stopError() error {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.stopErr
}

// fail records the first error of a run's goroutine that ends the listener, and cuts every run off.
func (r *listenRunner) fail(err error) {
	r.mu.Lock()
	if r.fatal == nil {
		r.fatal = err
	}
	r.mu.Unlock()
	if r.cancel != nil {
		r.cancel()
	}
}

func (r *listenRunner) fatalErr() error {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.fatal
}

// halted reports whether nothing more may be started: the listener was told to stop, or a stop of
// SPEC §10 happened.
func (r *listenRunner) halted() bool { return r.stopping() || r.stopError() != nil }

// slotFree reports whether fewer than max_parallel runs are in progress.
func (r *listenRunner) slotFree() bool {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.planning == "" && len(r.runs) < r.maxRuns()
}

// noRuns reports whether nothing is in progress: no run and no plan. An Epic's plan starts only then.
func (r *listenRunner) noRuns() bool {
	r.mu.Lock()
	defer r.mu.Unlock()
	return len(r.runs) == 0 && r.planning == ""
}

// holder is the ticket whose run is in progress in service, if any.
func (r *listenRunner) holder(service string) (string, bool) {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.holderLocked(service)
}

func (r *listenRunner) holderLocked(service string) (string, bool) {
	for key, s := range r.runs {
		if s != "" && strings.EqualFold(s, service) {
			return key, true
		}
	}
	return "", false
}

// canStart reports whether a run in service may start now: a slot is free and no run of the
// service is in progress. An empty service (not known yet) needs only a slot.
func (r *listenRunner) canStart(service string) bool {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.planning != "" || len(r.runs) >= r.maxRuns() {
		return false
	}
	if service == "" {
		return true
	}
	_, busy := r.holderLocked(service)
	return !busy
}

// bind records the service the run of key turned out to be in. It is false when another run is in
// progress there.
func (r *listenRunner) bind(key, service string) bool {
	r.mu.Lock()
	defer r.mu.Unlock()
	if holder, busy := r.holderLocked(service); busy && holder != key {
		return false
	}
	r.runs[key] = service
	return true
}

// startRun takes a slot for the ticket key (in service, "" if unknown) and calls body in a goroutine
// of its own; the slot is held until body returns, that is until the outcome is written. An error of
// body ends the listener; a panic in it is the ticket's alone (cycle).
func (r *listenRunner) startRun(key, service string, body func() error) {
	r.start(key, service, false, body)
}

// startPlan is startRun for an Epic's plan, which takes every slot: it is called only when noRuns,
// and until body returns no slot is free (SPEC §3).
func (r *listenRunner) startPlan(key string, body func() error) {
	r.start(key, "", true, body)
}

func (r *listenRunner) start(key, service string, exclusive bool, body func() error) {
	r.mu.Lock()
	r.runs[key] = service
	if exclusive {
		r.planning = key
	}
	r.mu.Unlock()
	r.wg.Add(1)
	go func() {
		defer func() {
			r.mu.Lock()
			delete(r.runs, key)
			if r.planning == key {
				r.planning = ""
			}
			r.mu.Unlock()
			select {
			case r.wake <- struct{}{}:
			default:
			}
			r.wg.Done()
		}()
		if err := r.cycle(key, body); err != nil {
			r.fail(err)
		}
	}()
}

// cycle calls body, the part of the ticket's cycle that goes on in its slot. A panic in it outside
// the run's own call (which execute contains) stays in this goroutine too (specs/listener-story/SPEC.md
// §3), and the other runs go on: a cycle whose outcome is not decided yet is ended as failed, with the
// panic as its reason; one whose outcome was decided keeps it, and what is left to write of it is
// written when the listener starts again (SPEC §9).
func (r *listenRunner) cycle(key string, body func() error) error {
	panicked, err := r.guard(key, body)
	if panicked == "" {
		return err
	}
	e, ok := r.entry(key)
	switch {
	case !ok || e.Phase == listener.PhaseFinished:
		return nil
	case e.Outcome != "":
		r.logf("%s: what is left to write of its outcome %s is written when the listener starts again", key, e.Outcome)
		return nil
	}
	again, err := r.guard(key, func() error { return r.endBefore(key, listener.ResultFailed, "internal error: "+panicked) })
	if again != "" {
		r.logf("%s: its cycle is ended when the listener starts again", key)
		return nil
	}
	return err
}

// guard calls f and returns the first line of what it panicked with, "" when it did not panic. The
// goroutine's stack goes to stderr, where the listener logs.
func (r *listenRunner) guard(key string, f func() error) (panicked string, err error) {
	defer func() {
		if p := recover(); p != nil {
			panicked = oneLineText(firstLineOf(fmt.Sprint(p)), 300)
			if panicked == "" {
				panicked = "a panic"
			}
			fmt.Fprintf(r.stderr, "listen: %s: the listener panicked in its cycle: %s\n%s\n", key, panicked, debug.Stack())
			err = nil
		}
	}()
	return "", f()
}

// waitForSlot blocks until a run in service may start, false when the listener is told to stop first.
func (r *listenRunner) waitForSlot(service string) bool {
	for !r.canStart(service) {
		select {
		case <-r.wake:
		case <-r.ctx.Done():
			return false
		}
	}
	return !r.stopping()
}

// runLog is where the output of a run goes: the listener's stderr with one slot, else the file
// logs/<KEY>.<attempt>.log (appended to, so a resumed run goes on in it), whose path it returns. The
// returned function closes the file.
func (r *listenRunner) runLog(key string, attempt int) (io.Writer, string, func()) {
	if r.maxRuns() <= 1 {
		return r.stderr, "", func() {}
	}
	path := filepath.Join(r.logsDir, fmt.Sprintf("%s.%d.log", key, attempt))
	if err := os.MkdirAll(r.logsDir, 0o755); err == nil {
		if f, err := os.OpenFile(path, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o644); err == nil {
			return f, path, func() { _ = f.Close() }
		}
	}
	r.logf("%s: cannot open %s, its output goes here", key, path)
	return r.stderr, "", func() {}
}

// logSuffix is the " (log: path)" a start line ends with when the run has a log file.
func logSuffix(path string) string {
	if path == "" {
		return ""
	}
	return " (log: " + path + ")"
}

// environment shapes the listener's own process environment, once, at start (specs/embed/SPEC.md
// §2.4; SPEC §6.2): what every git process the listener starts and every process a run starts
// inherits is then what the child of the earlier design was given.
//
//   - The credential helper's entries (GIT_CONFIG_COUNT, GIT_CONFIG_KEY_n, GIT_CONFIG_VALUE_n) are
//     set. They name `<exe> git-credential --config <path>` and hold no secret.
//   - The Jira variables and the parent-session variables are removed. It runs after the Jira
//     client was built: the client took the values at its construction.
//   - The names of the Jira and GitLab variables are registered with execution.ScrubCredentials:
//     provider CLIs and the test command never get the GitLab token, while git keeps it, for the
//     helper.
//
// os.Setenv and os.Unsetenv are process-global: the listener's process is the listener's.
func (r *listenRunner) environment() error {
	execution.ScrubCredentials(r.scrub...)

	env := withListenerGit(os.Environ(), r.exe, r.cfg.GitLab.BaseURL, "--config", r.configPath)
	// The helper's entries are the last three, after the GIT_CONFIG entries the environment held.
	for _, kv := range env[len(env)-3:] {
		name, value, _ := strings.Cut(kv, "=")
		if err := os.Setenv(name, value); err != nil {
			return fmt.Errorf("cannot set %s for git's credential helper: %w", name, err)
		}
	}

	unset := []string{r.cfg.Jira.EmailEnv, r.cfg.Jira.TokenEnv}
	for _, kv := range os.Environ() {
		if name, _, _ := strings.Cut(kv, "="); execution.IsParentSessionVar(name) {
			unset = append(unset, name)
		}
	}
	for _, name := range unset {
		if name == "" {
			continue
		}
		if err := os.Unsetenv(name); err != nil {
			return fmt.Errorf("cannot remove %s from the environment: %w", name, err)
		}
	}
	return nil
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

// saveLocked writes state.json atomically; the caller holds mu.
func (r *listenRunner) saveLocked() error {
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
	r.mu.Lock()
	defer r.mu.Unlock()
	e := r.state.Tickets[key]
	change(&e)
	if err := r.state.Set(key, e); err != nil {
		return err
	}
	return r.saveLocked()
}

// isAuth reports whether err is Jira refusing the credentials, and if so makes it the reason the
// listener stops.
func (r *listenRunner) isAuth(err error) bool {
	var jerr *apperr.JiraError
	if errors.As(err, &jerr) && jerr.Kind == apperr.JiraAuth {
		r.jiraAuth.Store(true)
		r.setStop(err)
		return true
	}
	return false
}

// stopping reports whether the listener was told to stop.
func (r *listenRunner) stopping() bool { return r.ctx.Err() != nil }

// run is the listener after its start: it recovers what a previous listener left (SPEC §9), polls
// until it is told to stop or a stop of §10 happens, then waits for every run's goroutine.
func (r *listenRunner) run() error {
	err := r.recover()
	if err == nil && r.stopError() == nil {
		err = r.serve()
	}
	if err != nil {
		// Nothing more is polled: what is in progress is cut off, to be resumed on restart.
		r.cancel()
	}
	r.wg.Wait()
	switch {
	case r.fatalErr() != nil:
		return r.fatalErr()
	case r.stopError() != nil:
		return r.stopError()
	}
	return err
}

// serve polls whenever a slot is free (SPEC §3): at start after the recovery, when a run ends, and
// every poll_interval; not while every slot is taken. With once it polls once, as soon as a slot is
// free: with one slot, after the run the recovery resumed, as before the slots.
func (r *listenRunner) serve() error {
	failures := 0
	for {
		// The runs the recovery resumed, or the last poll started, may hold every slot: the poll, its
		// search included, waits for one of them to end.
		if r.halted() || !r.waitForSlot("") {
			return nil
		}
		// A run that ended before this poll is seen by it.
		select {
		case <-r.wake:
		default:
		}
		err := r.poll()
		switch {
		case r.stopError() != nil || r.stopping():
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
		if !r.idle() {
			return nil
		}
	}
}

// idle waits for the next poll: a run ending, or poll_interval when a slot is free. It is false
// when the listener was told to stop.
func (r *listenRunner) idle() bool {
	var tick <-chan time.Time
	if r.slotFree() {
		t := time.NewTimer(r.lc.PollInterval)
		defer t.Stop()
		tick = t.C
	}
	select {
	case <-tick:
	case <-r.wake:
	case <-r.ctx.Done():
		return false
	}
	return true
}

// listenFatal wraps an error that ends the listener with exit 1 (the state cannot be saved, say).
type listenFatal struct{ err error }

func (e listenFatal) Error() string { return e.err.Error() }
func (e listenFatal) Unwrap() error { return e.err }

func isFatalListen(err error) bool {
	var f listenFatal
	return errors.As(err, &f)
}

// poll searches once and dispatches, in the search's order, every ticket that can be taken while a
// slot is free (SPEC §3).
func (r *listenRunner) poll() error {
	// A claim that may have reached Jira and whose status could not be read is settled first.
	for _, key := range r.unfinished() {
		if r.entryOf(key).Phase != listener.PhaseClaiming || r.halted() {
			continue
		}
		if err := r.recoverEntry(key); err != nil {
			return listenFatal{err}
		}
	}
	if r.halted() {
		return nil
	}
	q := r.lc.Query
	heads, err := r.jira.Search(r.ctx, jira.BuildJQL(q.Projects, q.Label, q.Statuses, q.Reporters), maxListenIssues)
	if err != nil {
		r.isAuth(err)
		return err
	}
	for _, h := range heads {
		if r.halted() || !r.slotFree() {
			return nil
		}
		if !r.eligible(h) || r.blocked(h) {
			continue
		}
		if err := r.dispatch(h); err != nil {
			if errors.Is(err, errEpicWaits) {
				// Nothing after the Epic is started in this poll, or the runs that come after it would
				// keep it waiting for ever: it is planned when the runs in progress have ended.
				return nil
			}
			return listenFatal{err}
		}
	}
	return nil
}

// errEpicWaits is dispatch's answer for an Epic that cannot be planned yet because a run is in
// progress (SPEC §3, A12): it is not claimed, and the poll goes no further.
var errEpicWaits = errors.New("an Epic waits for the runs in progress")

// serviceOf is the service the head names, "" when it cannot be named: such a ticket is dispatched
// all the same and blocked in process (SPEC §3).
func (r *listenRunner) serviceOf(h domain.TicketHead) string {
	dirs, err := r.checkout.serviceDirs()
	if err != nil {
		return ""
	}
	service, err := listener.Service(h.Labels, h.Components, dirs, r.lc.Labels.ServicePrefix)
	if err != nil {
		return ""
	}
	return service
}

// serviceBusy reports whether a run of service is in progress, so that h is skipped: not claimed, and
// logged once for the run it waits for.
func (r *listenRunner) serviceBusy(h domain.TicketHead, service string) bool {
	holder, busy := r.holder(service)
	if !busy {
		delete(r.busyOn, h.Key)
		return false
	}
	if r.busyOn == nil {
		r.busyOn = map[string]string{}
	}
	if r.busyOn[h.Key] != holder {
		r.busyOn[h.Key] = holder
		r.logf("%s waits for %s, which is running in %s", h.Key, holder, service)
	}
	return true
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
	e, ok := r.entry(h.Key)
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

// blocked reports whether h waits for an unfinished blocker (specs/listener-story/SPEC.md §4): with
// listener.blockers set, a ticket with a blocker whose status is not done is skipped, without a
// claim, a comment, an attempt or a state entry. It logs once per ticket and blocker; what it
// remembers is in memory only, and forgotten when the ticket no longer waits.
func (r *listenRunner) blocked(h domain.TicketHead) bool {
	b := r.lc.Blockers
	if b == nil {
		return false
	}
	blocker, waits := listener.Waiting(h, b.LinkType, b.DoneStatuses)
	if !waits {
		delete(r.waitingOn, h.Key)
		return false
	}
	if r.waitingOn == nil {
		r.waitingOn = map[string]string{}
	}
	if r.waitingOn[h.Key] != blocker.Key {
		r.waitingOn[h.Key] = blocker.Key
		status := blocker.Status
		if status == "" {
			status = "no status"
		}
		r.logf("%s waits for %s (%s)", h.Key, blocker.Key, status)
	}
	return true
}

// isEpic reports whether the issue type is one story.jira_issue_types sends to a story plan.
func (r *listenRunner) isEpic(issueType string) bool {
	types := []string{"Epic"}
	if r.cfg.Story != nil && len(r.cfg.Story.JiraIssueTypes) > 0 {
		types = r.cfg.Story.JiraIssueTypes
	}
	return slices.ContainsFunc(types, func(t string) bool { return strings.EqualFold(t, strings.TrimSpace(issueType)) })
}

// dispatch claims the ticket (SPEC §4) and starts its cycle in a slot of its own: the claim is made
// here, the run in a goroutine. A claim that cannot be made leaves everything as it was and the next
// ticket is tried; so does a ticket whose service has a run in progress (SPEC §3).
func (r *listenRunner) dispatch(h domain.TicketHead) error {
	key := h.Key
	// What can never run is ended from the queue, before any claim (SPEC §3.3, §8).
	if e, ok := r.entry(key); ok && e.Spent(r.lc.MaxAttempts) {
		return r.endUnclaimed(h, listener.ResultSpent, "")
	}
	epic := r.isEpic(h.Type)
	if epic && r.lc.Story == nil {
		return r.endUnclaimed(h, listener.ResultBlocked,
			fmt.Sprintf("%s is an %s: plan it as a story (orchestrator story plan --jira %s); the listener runs one ticket as one run in one service.", key, h.Type, key))
	}
	// The service is computed from the head before the claim: at most one run per service. An Epic
	// has none: its plan takes every slot, so it starts only when nothing is in progress.
	service := ""
	if epic {
		if !r.noRuns() {
			r.epicWaits(key)
			return errEpicWaits
		}
		delete(r.busyOn, key)
	} else {
		service = r.serviceOf(h)
		if service != "" && r.serviceBusy(h, service) {
			return nil
		}
	}

	transitions, err := r.jira.Transitions(r.ctx, key)
	if err != nil {
		if !r.isAuth(err) && !r.stopping() {
			r.logf("%s skipped: cannot read its transitions: %s", key, firstLineOf(err.Error()))
		}
		return nil
	}
	start, ok := findTransition(transitions, r.lc.Transitions.Start)
	if !ok {
		r.logf("%s skipped: the transition %q is not offered", key, r.lc.Transitions.Start)
		return nil
	}
	startStatus := start.To
	if startStatus == "" {
		startStatus = r.lc.Transitions.Start
	}

	prev, had := r.entry(key)
	if err := r.begin(key); err != nil {
		r.logf("%s skipped: %v", key, err)
		return nil
	}
	if err := r.update(key, func(e *listener.Entry) {
		e.Status = startStatus
		if epic {
			e.Kind = listener.KindPlan
		}
	}); err != nil {
		return err
	}
	if err := r.jira.Transition(r.ctx, key, start.ID); err != nil {
		if !jira.MayExist(err) {
			// Refused, or never sent: a person, or another listener, moved the ticket first, and the
			// workflow allows the claim from the queue's status only. This is the only lock in Jira
			// (SPEC §4), so the ticket is skipped whatever its status is now.
			if !r.isAuth(err) && !r.stopping() {
				r.logf("%s skipped: the claim was refused: %s", key, firstLineOf(err.Error()))
			}
			return r.restore(key, prev, had)
		}
		// The claim may have reached Jira: the entry stays claiming until the status tells (SPEC §9).
		if r.stopping() {
			r.logf("%s: the claim may have been made; its status is read when the listener starts again", key)
			return nil
		}
		status, serr := r.jira.Status(r.bg, key)
		switch {
		case serr != nil:
			r.isAuth(serr)
			r.logf("%s: the claim may have been made and its status cannot be read; it is read again before the next poll: %s", key, firstLineOf(serr.Error()))
			return nil
		case !strings.EqualFold(status, startStatus):
			r.logf("%s skipped: the claim failed: %s", key, firstLineOf(err.Error()))
			return r.restore(key, prev, had)
		}
	}
	if err := r.update(key, func(e *listener.Entry) { e.Phase = listener.PhaseClaimed }); err != nil {
		return err
	}
	if epic {
		r.logf("%s claimed: planning it", key)
		r.startPlan(key, func() error { return r.plan(key) })
		return nil
	}
	r.logf("%s claimed", key)
	r.startRun(key, service, func() error { return r.process(h, prev, service) })
	return nil
}

// epicWaits logs once per Epic that it waits for the runs in progress.
func (r *listenRunner) epicWaits(key string) {
	if r.busyOn == nil {
		r.busyOn = map[string]string{}
	}
	if r.busyOn[key] != "runs" {
		r.busyOn[key] = "runs"
		r.logf("%s is an Epic to plan: it waits for the runs in progress", key)
	}
}

// restore puts the entry of key back as it was before a claim that did not happen.
func (r *listenRunner) restore(key string, prev listener.Entry, had bool) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	if had {
		r.state.Tickets[key] = prev
	} else {
		r.state.Drop(key)
	}
	return r.saveLocked()
}

// process takes a claimed ticket as far as its outcome. named is the service dispatch named from the
// head before the claim, whose slot the run holds; "" when it could not be named, and it is named
// here, where why it cannot be is the ticket's outcome.
func (r *listenRunner) process(h domain.TicketHead, prev listener.Entry, named string) error {
	key := h.Key
	service := named
	if service == "" {
		dirs, err := r.checkout.serviceDirs()
		if err != nil {
			return r.endInfra(key, err)
		}
		if service, err = listener.Service(h.Labels, h.Components, dirs, r.lc.Labels.ServicePrefix); err != nil {
			return r.endBefore(key, listener.ResultBlocked, err.Error())
		}
		// Named only now (the services directory changed since the claim, a moment ago): the run
		// takes the service's slot, or goes back to the queue when a run is in progress there.
		if !r.bind(key, service) {
			return r.endInfra(key, fmt.Errorf("a run is in progress in its service %s", service))
		}
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
	configPath, err := r.configFor(res.Dir)
	if err != nil {
		return r.endBefore(key, listener.ResultFailed, err.Error())
	}

	ticket, err := r.jira.Fetch(r.ctx, key)
	if err != nil {
		return r.jiraProblem(key, err)
	}
	hash := r.ticketHash(ticket)
	restart := slices.Contains(ticket.Labels, r.lc.Labels.Restart)
	if run := r.previousRun(key, res.Dir, service, prev, hash, restart); run != nil {
		if r.stopping() {
			return nil
		}
		if err := r.update(key, func(e *listener.Entry) {
			e.Phase, e.StartedAt = listener.PhaseRunning, r.now().UTC()
			e.RunID, e.RunDir, e.TicketHash = run.ID, listener.RunDir(service, run.ID), hash
		}); err != nil {
			return err
		}
		r.logf("%s: resuming its earlier run %s", key, run.ID)
		return r.resume(key, res.Dir, configPath, run.ID)
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
	attempt := r.entryOf(key).Attempts + 1
	task, err := r.writeTask(ticket, text, attempt)
	if err != nil {
		return r.endInfra(key, err)
	}

	if r.stopping() {
		return nil
	}
	if restart {
		// Best effort: a label left behind only makes the next cycle start a new run again.
		if err := r.jira.RemoveLabel(r.ctx, key, r.lc.Labels.Restart); err != nil {
			r.isAuth(err)
			r.logf("%s: cannot remove the label %s: %s", key, r.lc.Labels.Restart, firstLineOf(err.Error()))
		}
	}
	if err := r.update(key, func(e *listener.Entry) {
		e.Phase, e.StartedAt, e.TicketHash = listener.PhaseRunning, r.now().UTC(), hash
	}); err != nil {
		return err
	}
	// With one slot the run's progress goes to the listener's stderr; with more, to a file of its own.
	out, logPath, closeLog := r.runLog(key, attempt)
	defer closeLog()
	r.logf("%s running in %s%s", key, service, logSuffix(logPath))
	return r.execute(key, res.Dir, func(ctx context.Context) error {
		return runTask(ctx, res.Dir, configPath, "", "", "", &task, nil, nil, false, "", domain.ModeAuto,
			r.claudeAuth, r.codexAuth, false, strings.NewReader(""), plainWriter{out}, out)
	})
}

// configFor is the configuration file the run of a service loads, absolute: the service's own
// orchestrator.yaml when it has one (the repository's own rules apply, SPEC §6.4), else
// <listener.configs_dir>/<service>/orchestrator.yaml, else listener.default_config.
func (r *listenRunner) configFor(serviceDir string) (string, error) {
	service := filepath.Base(serviceDir)
	own := filepath.Join(serviceDir, defaultConfigPath)
	if _, err := os.Stat(own); err == nil {
		return own, nil
	}
	abs := func(p string) string {
		if filepath.IsAbs(p) {
			return p
		}
		return filepath.Join(r.wd, p)
	}
	if r.lc.ConfigsDir != "" {
		path := abs(filepath.Join(r.lc.ConfigsDir, service, defaultConfigPath))
		if _, err := os.Stat(path); err == nil {
			return path, nil
		}
		if r.lc.DefaultConfig == "" {
			return "", fmt.Errorf("the service %s has no %s and there is no %s", service, defaultConfigPath, path)
		}
	}
	def := r.lc.DefaultConfig
	if def == "" {
		return "", fmt.Errorf("the service %s has no %s and listener.default_config is not set", service, defaultConfigPath)
	}
	if _, err := os.Stat(abs(def)); err != nil {
		return "", fmt.Errorf("the service %s has no %s and listener.default_config %s cannot be read", service, defaultConfigPath, r.lc.DefaultConfig)
	}
	return abs(def), nil
}

// writeTask records the task of the run (SPEC §7.2) as tasks/<KEY>.<attempt>.json, the record and
// the input a restart reads back, and returns it as the value the run takes (specs/embed/SPEC.md
// §2.1).
func (r *listenRunner) writeTask(t domain.Ticket, text string, attempt int) (listenerTask, error) {
	in := listenerTask{
		V: listenerTaskVersion, Key: t.Key, URL: taskURL(t.URL), FetchedAt: t.FetchedAt.UTC(),
		Description: text, ScrubEnv: r.scrub,
	}
	data, err := json.Marshal(in)
	if err != nil {
		return listenerTask{}, err
	}
	// Validated as the run will take it, so a task it would refuse never starts one. The value the
	// run gets is what the file holds.
	task, err := parseListenerTask(data)
	if err != nil {
		return listenerTask{}, err
	}
	if err := os.MkdirAll(r.tasksDir, 0o755); err != nil {
		return listenerTask{}, fmt.Errorf("cannot create %s: %w", r.tasksDir, err)
	}
	file := filepath.Join(r.tasksDir, fmt.Sprintf("%s.%d.json", t.Key, attempt))
	if err := persistence.ReplaceFile(file, data); err != nil {
		return listenerTask{}, fmt.Errorf("cannot write %s: %w", file, err)
	}
	return task, nil
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
		r.setStop(&apperr.ConfigError{Msg: "GitLab refused the credential in " + r.cfg.GitLab.TokenEnv + ": " + ce.Msg})
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
	if err := r.begin(h.Key); err != nil {
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

// outcome is what is known of a finished run when its outcome is written: the run as it is on
// disk, and the result the call returned (the value of the JSON line `run --output json` writes).
type outcome struct {
	run  *domain.Run
	line runResult
}

// listenRunHook, when set, is called with the ticket's key inside the contained call of a run, just
// before the run (SetListenRunHook), in the run's own goroutine: runs of different services call it
// at the same time. It exists for the integration tests, which make it panic or hold the run;
// nothing sets it in production, and no run writes it (specs/embed/SPEC.md §2.6).
var listenRunHook atomic.Pointer[func(key string)]

// SetListenRunHook makes hook run inside every run the listener makes, before the run, and returns
// a function that puts the previous hook back. It exists for the integration tests, which live in
// another package and inject a panic with it, or a barrier that holds two runs at once (the hook
// blocks until both are in it).
func SetListenRunHook(hook func(key string)) (restore func()) {
	var next *func(key string)
	if hook != nil {
		next = &hook
	}
	previous := listenRunHook.Swap(next)
	return func() { listenRunHook.Store(previous) }
}

// execute makes one call of the run (runTask for a new ticket, resumeRun for an interrupted one) in
// the service's directory dir, in this process, and writes its outcome. The call gets a context
// derived from the listener's, so a stop reaches the step in flight (specs/embed/SPEC.md §2.5), and
// a recorder, from which the result is built exactly as the JSON line of `run --output json` is
// (§2.2). A panic in the call is contained here (§2.2): it is the run's, never the listener's.
func (r *listenRunner) execute(key, dir string, call func(ctx context.Context) error) error {
	ctx, rec := withResultRecorder(r.ctx)
	err := r.contained(key, func() error {
		if h := listenRunHook.Load(); h != nil {
			(*h)(key)
		}
		return call(ctx)
	})
	return r.afterRun(key, dir, buildResult(rec, err))
}

// contained calls f and turns a panic in it into an error: "internal error: " and the first line of
// what was panicked with, never a prompt, a diff or an environment (specs/embed/SPEC.md §6). The
// goroutine's stack goes to stderr, where the listener logs.
func (r *listenRunner) contained(key string, f func() error) (err error) {
	defer func() {
		if p := recover(); p != nil {
			stack := debug.Stack()
			first := oneLineText(firstLineOf(fmt.Sprint(p)), 300)
			fmt.Fprintf(r.stderr, "listen: %s: the run panicked: %s\n%s\n", key, first, stack)
			err = errors.New("internal error: " + first)
		}
	}()
	return f()
}

// afterRun decides the outcome of a run that returned res, records it and writes it to Jira.
func (r *listenRunner) afterRun(key, dir string, res runResult) error {
	line, code := res, res.ExitCode
	e := r.entryOf(key)
	var run *domain.Run
	if listener.ValidRunID(line.RunID) {
		if loaded, err := persistence.NewStore(filepath.Join(dir, runsBaseDir)).LoadRun(line.RunID); err == nil {
			run = loaded
			e.RunID, e.RunDir = line.RunID, listener.RunDir(e.Service, line.RunID)
		} else {
			r.logf("%s: cannot read run %s: %s", key, line.RunID, firstLineOf(err.Error()))
		}
	}
	if listener.CutOff(code, r.stopping()) {
		// Cut off by the listener's stop: the run is resumed on restart.
		e.Interrupted = true
		if err := r.setEntry(key, e); err != nil {
			return err
		}
		r.logf("%s: the run was interrupted; it is resumed when the listener starts again", key)
		return nil
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
		// The run was cancelled by someone else: it stays where it is, to be resumed on restart.
		e.Interrupted = true
		return r.setEntry(key, e)
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
	if err := r.setEntry(key, e); err != nil {
		return err
	}
	if d.Stop {
		r.setStop(&apperr.ConfigError{Msg: fmt.Sprintf("the run of %s failed on a credential: %s", key, firstLineOf(firstNonEmpty(line.Error, reason)))})
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
	e := r.entryOf(key)
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

	if d.Comment != listener.CommentNone && e.Writes.Comment.State == "" && !r.jiraAuth.Load() {
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
		e = r.entryOf(key)
	}

	to := ""
	if canMove && !r.jiraAuth.Load() {
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
		// An Epic's plan says what it created, from its plan.json (SPEC §9).
		if body, ok := r.planBody(e); ok {
			return body, ""
		}
		return listener.Blocked(jira.Escape, e.Reason), ""
	}
	run := oc.run
	runID := oc.line.RunID
	switch d.Comment {
	case listener.CommentMergeRequest:
		return r.mergeRequestBody(e.Service, run), runID
	case listener.CommentNeedsInput:
		return listener.NeedsInput(jira.Escape, r.openPoints(run), r.assumedPoints(run), r.queueStatus()), runID
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

// assumedPoints are the points a round settled by an assumption: a person should confirm or correct them.
func (r *listenRunner) assumedPoints(run *domain.Run) []listener.Assumption {
	if run == nil {
		return nil
	}
	var assumed []listener.Assumption
	for _, p := range run.Task.Clarification.Assumed() {
		assumed = append(assumed, listener.Assumption{Question: r.scrubText(p.Question), Answer: r.scrubText(p.Answer)})
	}
	return assumed
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
func (r *listenRunner) failureBody(run *domain.Run, line runResult) []string {
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

// recover finishes what a previous listener left half done (SPEC §9). An entry that was running goes
// on in a slot of its own, at most max_parallel at once and one per service (specs/listener-story/SPEC.md
// §3): the others wait for a slot here, before the first poll. The rest (a claim to settle, an outcome
// to write) are quick and are done here, one after the other.
func (r *listenRunner) recover() error {
	keys := r.unfinished()
	// An Epic's plan needs every slot: the ones cut off are started again first, while nothing runs.
	slices.SortStableFunc(keys, func(a, b string) int {
		return boolRank(r.entryOf(a).Kind != listener.KindPlan) - boolRank(r.entryOf(b).Kind != listener.KindPlan)
	})
	for _, key := range keys {
		if r.halted() {
			return nil
		}
		e := r.entryOf(key)
		r.logf("%s was %s when the listener stopped", key, e.Phase)
		if e.Phase != listener.PhaseRunning {
			if err := r.recoverEntry(key); err != nil {
				return err
			}
			continue
		}
		if !r.waitForSlot(e.Service) {
			return nil
		}
		r.startRun(key, e.Service, func() error { return r.recoverEntry(key) })
	}
	return nil
}

// boolRank is 1 for true and 0 for false.
func boolRank(b bool) int {
	if b {
		return 1
	}
	return 0
}

// recoverEntry carries one unfinished entry to its end. A claiming one is settled by the ticket's
// status: in the start status the claim happened and the entry is claimed; else the claim did not
// happen and the cycle is closed as none rather than dropped as SPEC §9 has it, so that the
// ticket's attempts are kept (a none cycle is picked again like an unknown ticket). A status that
// cannot be read leaves it claiming, for the next poll or start.
func (r *listenRunner) recoverEntry(key string) error {
	e := r.entryOf(key)
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
		e = r.entryOf(key)
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
		if e.Kind == listener.KindPlan && r.noRuns() && !r.halted() {
			// A plan cut off has nothing to resume: it is made again from the start (A10).
			r.logf("%s: its plan is made again from the start", key)
			r.startPlan(key, func() error { return r.plan(key) })
			return nil
		}
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
	line := runResult{RunID: run.ID, Status: string(run.Status), ExitCode: code, FailureReason: run.FailureReason, FailureKind: kind}

	if e.Outcome != "" {
		// The outcome was decided before the listener stopped: only the writes not yet made. A
		// credential that stopped it then does not stop this listener again.
		d := listener.Decide(listener.RunOutcome{ExitCode: code, FailureKind: kind, FailureReason: run.FailureReason, MRCreated: mrCreated})
		if e.Outcome == listener.ResultRetry {
			d = listener.Infrastructure()
		}
		return r.write(key, d, &outcome{run: run, line: line})
	}
	// A run that did not end, or that the listener's stop cut off (a call it interrupted left a
	// cut step as a failure), goes on; one that reached a verdict of its own is written.
	resumable := run.Status == domain.RunPending || run.Status == domain.RunRunning || code == listener.ExitCancelled ||
		(e.Interrupted && run.Status != domain.RunCompleted && run.FailureReason != domain.FailureTaskNeedsInput)
	if !resumable {
		return r.afterRun(key, dir, line)
	}
	configPath, err := r.configFor(dir)
	if err != nil {
		return r.endBefore(key, listener.ResultFailed, err.Error())
	}
	r.logf("%s: resuming run %s", key, run.ID)
	return r.resume(key, dir, configPath, run.ID)
}

// resume continues the run id of the ticket key in the service directory dir.
func (r *listenRunner) resume(key, dir, configPath, id string) error {
	out, logPath, closeLog := r.runLog(key, r.entryOf(key).Attempts+1)
	defer closeLog()
	if logPath != "" {
		r.logf("%s: its output goes to %s", key, logPath)
	}
	return r.execute(key, dir, func(ctx context.Context) error {
		return resumeRun(ctx, dir, configPath, id, domain.ModeAuto, r.claudeAuth, r.codexAuth, false,
			strings.NewReader(""), plainWriter{out}, out)
	})
}

// ticketHash is the sha256 of the ticket's text without its discussion and labels: a person's edit
// of the summary, the description or the fields changes it, a comment or a label does not (the
// listener's own comment after every run would). "" when the text cannot be rendered; the new run then reports why.
func (r *listenRunner) ticketHash(t domain.Ticket) string {
	t.Labels = nil // rendered, but the restart and queue labels come and go without an edit
	text, err := jira.Render(t, "", r.cfg.Jira.MaxBytes)
	if err != nil {
		return ""
	}
	sum := sha256.Sum256([]byte(text))
	return hex.EncodeToString(sum[:])
}

// previousRun is the run of an earlier cycle of the ticket that this cycle continues instead of
// starting a new one: a ticket moved back to the queue after its run stopped or failed resumes it
// (its worktree and steps, under the current configuration). A run that needs input is not one: the
// new cycle's task carries the answers from the discussion. Nor is one that opened its merge request,
// nor one of a ticket that has the restart label or was edited since (hash differs from the one the
// run started with; an entry without one, from before the hash was kept, counts as unchanged).
func (r *listenRunner) previousRun(key, dir, service string, prev listener.Entry, hash string, restart bool) *domain.Run {
	if prev.Service != service || !listener.ValidRunID(prev.RunID) {
		return nil
	}
	run, err := persistence.NewStore(filepath.Join(dir, runsBaseDir)).LoadRun(prev.RunID)
	if err != nil {
		r.logf("%s: cannot read its earlier run %s, a new run starts: %s", key, prev.RunID, firstLineOf(err.Error()))
		return nil
	}
	if src := run.Task.Source; src == nil || !src.Listener || src.Key != key {
		return nil
	}
	if run.Status != domain.RunStopped && run.Status != domain.RunFailed {
		return nil
	}
	if run.FailureReason == domain.FailureTaskNeedsInput || (run.MR != nil && run.MR.State == domain.MRCreated) {
		return nil
	}
	if restart {
		r.logf("%s has the label %s: a new run starts instead of resuming %s", key, r.lc.Labels.Restart, prev.RunID)
		return nil
	}
	if prev.TicketHash != "" && prev.TicketHash != hash {
		r.logf("%s changed since its run %s: a new run starts", key, prev.RunID)
		return nil
	}
	return run
}

// findRun is the run the ticket's earlier call created: the recorded one, else the newest run of
// this ticket in the service that began after the call was about to start.
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

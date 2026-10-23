package app

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"maps"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"time"

	"orchestrator/internal/apperr"
	"orchestrator/internal/config"
	"orchestrator/internal/domain"
	"orchestrator/internal/execution"
	"orchestrator/internal/logging"
	"orchestrator/internal/persistence"
	"orchestrator/internal/repository"
	"orchestrator/internal/story"
)

const (
	storyRunUsage    = "usage: orchestrator story run <spec-dir> [--retry <ID>] [--mode auto] [--claude-auth auto|api|login] [--codex-auth auto|api|login]"
	storyStatusUsage = "usage: orchestrator story status <spec-dir>"
	storyUsage       = `usage: orchestrator story plan [--jira <KEY|URL>] [--repo <name>]... "<task>"   or   ` +
		`orchestrator story run <spec-dir> [--retry <ID>]   or   orchestrator story status <spec-dir>`
)

// childWaitDelay is how long a child that was told to stop (SIGINT) has to finish its work -
// record its run as stopped - before it is killed.
const childWaitDelay = 30 * time.Second

// storyRunOptions are the command line of story run.
type storyRunOptions struct {
	// configPath is --config; without explicitConfig it is the default name, looked for in the
	// story home rather than in the working directory.
	configPath     string
	explicitConfig bool
	mode           domain.RunMode
	claudeAuth     string
	codexAuth      string
	// retry is the task --retry names, or "".
	retry   string
	verbose bool
}

// selfExecutable is the path of this binary, symlinks resolved: the children of a story run are
// the orchestrator itself (specs/story/SPEC.md §15).
func selfExecutable() (string, error) {
	exe, err := os.Executable()
	if err != nil {
		return "", &apperr.ConfigError{Msg: "cannot find the orchestrator's own executable", Err: err}
	}
	if resolved, err := filepath.EvalSymlinks(exe); err == nil {
		exe = resolved
	}
	return exe, nil
}

// runChild runs exe with args (argv only) in dir with the given stdio, and returns its exit
// code: -1 when it was killed by a signal or never started (err is then set for the latter).
// Cancelling ctx sends the child SIGINT, as Ctrl-C does, so it can record its run as stopped; it
// is killed only after childWaitDelay.
func runChild(ctx context.Context, exe, dir string, args []string, stdin io.Reader, stdout, stderr io.Writer) (int, error) {
	cmd := exec.CommandContext(ctx, exe, args...)
	cmd.Dir = dir
	cmd.Stdin, cmd.Stdout, cmd.Stderr = stdin, stdout, stderr
	cmd.Cancel = func() error { return cmd.Process.Signal(os.Interrupt) }
	cmd.WaitDelay = childWaitDelay
	err := cmd.Run()
	if cmd.ProcessState != nil {
		return cmd.ProcessState.ExitCode(), nil
	}
	return -1, err
}

// storyRun is `orchestrator story run <dir>` (specs/story/SPEC.md §10): it checks everything that
// can be checked before anything runs, asks once at the first start, and then runs the tasks in
// order, each as a child `run --story-task`, stopping at the first that does not end in a
// commit. dirArg is the spec directory; the story's state lives in its home's
// .orchestrator/stories/<slug>/.
func storyRun(ctx context.Context, dirArg string, opts storyRunOptions, stdin io.Reader, stdout, stderr io.Writer) error {
	reporter := newReporter(stdout, opts.verbose)
	defer reporter.Finish()

	osExec := execution.NewOSProcessExecutor()
	registry := newRegistry(osExec)

	loc, err := locateStory(ctx, osExec, dirArg)
	if err != nil {
		return err
	}

	// The home's whole fail-fast chain (§10.1 point 1). Without --config the config is the
	// home's, whatever the working directory is.
	configPath := opts.configPath
	if !opts.explicitConfig && !filepath.IsAbs(configPath) {
		configPath = filepath.Join(loc.home, configPath)
	}
	// With jira.create the credentials are checked here, before any request or child (§10.1 point 1).
	needJira := func(c config.Config) config.DependencyOptions {
		return config.DependencyOptions{Jira: c.Jira != nil && c.Jira.Create != nil}
	}
	cfg, _, _, _, _, err := loadAndValidateConfig(configPath, opts.claudeAuth, opts.codexAuth, registry, osExec, reporter, needJira)
	if err != nil {
		return err
	}
	if cfg.Story == nil {
		return &apperr.ConfigError{Msg: fmt.Sprintf("story run needs a story section in %s (specs/story/SPEC.md §5)", configPath)}
	}
	exe, err := selfExecutable()
	if err != nil {
		return err
	}

	// The story directory is held by the one story run that owns it, until it returns.
	unlock, err := persistence.LockStory(loc.storyDir)
	if err != nil {
		return err
	}
	defer unlock()

	r := &storyRunner{
		ctx: ctx, bg: context.WithoutCancel(ctx), dirArg: dirArg, opts: opts, cfg: cfg, osExec: osExec,
		reporter: reporter, stdin: stdin, stdout: stdout, stderr: stderr, exe: exe, loc: loc,
	}
	return r.run()
}

// storyRunner is one story run: the story's spec and record, and what it needs to start
// children and to find what they did.
type storyRunner struct {
	ctx context.Context
	// bg is ctx without its cancellation: what is done after a child ended - reading its run,
	// recording its result - must also happen when the operator pressed Ctrl-C.
	bg       context.Context
	dirArg   string
	opts     storyRunOptions
	cfg      config.Config
	osExec   execution.ProcessExecutor
	reporter *logging.ConsoleReporter
	stdin    io.Reader
	stdout   io.Writer
	stderr   io.Writer
	exe      string

	loc   storyLocation
	spec  storySpec
	rec   persistence.StoryRecord
	byID  map[string]story.Task
	order []string
	roots []string
	cands []serviceCandidate
}

func (r *storyRunner) run() error {
	if err := r.load(); err != nil {
		return err
	}
	if err := r.checkServices(); err != nil {
		return err
	}
	if err := r.checkBudget(); err != nil {
		return err
	}
	if err := r.checkJiraChildren(); err != nil {
		return err
	}
	if err := r.approve(); err != nil {
		return err
	}
	// Nothing is written to Jira before the operator's answer (§6), and nothing is dispatched
	// before the issues exist (§12.2).
	if err := r.createJiraChildren(); err != nil {
		return err
	}
	if err := r.reconcile(); err != nil {
		return err
	}
	if err := r.applyRetry(); err != nil {
		return err
	}
	return r.dispatchAll()
}

// load reads the record and the spec, and checks them as a whole (§10.1 point 2): the tasks parse
// and validate, and no task that was dispatched has changed since.
func (r *storyRunner) load() error {
	spec, err := readStorySpec(r.loc.specDir, r.loc.slug)
	if err != nil {
		return err
	}
	r.spec = spec

	recFile := filepath.Join(r.loc.storyDir, persistence.StoryRecordFile)
	rec, err := persistence.LoadStoryRecord(recFile)
	switch {
	case err == nil:
		if rec.Slug != r.loc.slug || rec.SpecDir != r.loc.specRel {
			return &apperr.ConfigError{Msg: fmt.Sprintf("%s belongs to the spec in %s, not to %s", recFile, rec.SpecDir, r.loc.specRel)}
		}
	case errors.Is(err, fs.ErrNotExist):
		// A hand-written spec: its record is built here, and its services at the first start.
		rec = persistence.StoryRecord{
			Slug: r.loc.slug, SpecDir: r.loc.specRel, CreatedAt: time.Now().UTC(),
			Services: map[string]string{}, Status: story.StatusPlanned,
		}
	default:
		return &apperr.ConfigError{Msg: "cannot use the story record", Err: err}
	}
	r.rec = rec
	if r.rec.Tasks == nil {
		r.rec.Tasks = map[string]persistence.StoryTask{}
	}
	if r.rec.Services == nil {
		r.rec.Services = map[string]string{}
	}

	r.roots, err = r.cfg.Story.ResolveRoots(r.loc.home)
	if err != nil {
		return &apperr.ConfigError{Msg: err.Error()}
	}
	if r.cands, err = candidates(r.roots, r.loc.home); err != nil {
		return &apperr.ConfigError{Msg: err.Error()}
	}
	var names []string
	for _, c := range r.cands {
		if !slices.Contains(names, c.Name) {
			names = append(names, c.Name)
		}
	}
	if err := story.Validate(spec.tasks, names, r.cfg.Story.MaxTasks); err != nil {
		return &apperr.ConfigError{Msg: fmt.Sprintf("the spec in %s is not valid: %v", r.loc.specRel, err)}
	}
	r.byID = make(map[string]story.Task, len(spec.tasks))
	for _, t := range spec.tasks {
		r.byID[t.ID] = t
	}
	r.order = story.Order(spec.tasks)
	for _, id := range r.order {
		if err := story.CheckAcceptance(id+": acceptance", r.byID[id].Acceptance); err != nil {
			return &apperr.ConfigError{Msg: err.Error()}
		}
		if _, err := story.Base(spec.tasks, id); err != nil {
			return &apperr.ConfigError{Msg: fmt.Sprintf("the spec in %s is not valid: %v", r.loc.specRel, err)}
		}
	}

	// A dispatched task is frozen: what ran is what the record says (SPEC A6).
	for _, id := range slices.Sorted(maps.Keys(r.rec.Tasks)) {
		rt := r.rec.Tasks[id]
		if _, ok := r.byID[id]; !ok {
			if rt.Status == story.TaskPending && rt.Attempt == 0 {
				continue
			}
			return &apperr.ConfigError{Msg: fmt.Sprintf("%s: story.json has the state of this task, but it has no task file in %s", id, r.loc.specRel)}
		}
		if rt.Status == story.TaskPending || id == r.opts.retry {
			continue
		}
		if story.FrozenHash(r.spec.data[id]) != rt.FileSHA256 {
			msg := fmt.Sprintf("%s: its task file %s was changed after the task was dispatched (the part above \"## As built\" is frozen); "+
				"revert the change", id, r.spec.files[id])
			if rt.Status != story.TaskDone {
				msg += fmt.Sprintf(", or dispatch the task again with its new text: orchestrator story run %s --retry %s", logging.ShellWord(r.dirArg), id)
			}
			return &apperr.ConfigError{Msg: msg}
		}
	}

	if id := r.opts.retry; id != "" {
		rt, ok := r.rec.Tasks[id]
		switch {
		case r.byID[id].ID == "":
			return &apperr.ConfigError{Msg: fmt.Sprintf("--retry %s: the story has no such task", id)}
		case !ok || !retryable(rt.Status, true):
			return &apperr.ConfigError{Msg: fmt.Sprintf("--retry %s: the task is %s; only a failed, stopped or blocked task is dispatched again", id, orPending(rt.Status))}
		}
	}
	return nil
}

func orPending(s story.TaskStatus) story.TaskStatus {
	if s == "" {
		return story.TaskPending
	}
	return s
}

// retryable reports whether --retry may abandon a task in status s. A running task counts when
// running is allowed: it is settled before the retry is applied.
func retryable(s story.TaskStatus, running bool) bool {
	switch s {
	case story.TaskFailed, story.TaskStopped, story.TaskBlocked:
		return true
	case story.TaskRunning:
		return running
	}
	return false
}

// checkServices is §10.1 point 3 for every repository the tasks that are not done name, in
// name order: it is found as in §7.1, is the recorded path, is a git checkout with a commit and
// no submodules, has its story base branch, holds no story branch the record does not know, and
// passes `orchestrator validate`.
func (r *storyRunner) checkServices() error {
	need := map[string]bool{}
	for _, id := range r.order {
		if r.rec.Tasks[id].Status != story.TaskDone {
			need[r.byID[id].Repo] = true
		}
	}
	for _, name := range slices.Sorted(maps.Keys(need)) {
		found, err := findServices(r.ctx, r.osExec, "", nil, []string{name}, r.cands, r.roots, r.cfg.Story.MaxServices)
		if err != nil {
			return &apperr.ConfigError{Msg: err.Error()}
		}
		path := found[0].Path
		if recorded, ok := r.rec.Services[name]; ok {
			if !samePath(recorded, path) {
				return &apperr.ConfigError{Msg: fmt.Sprintf("service %s is %s now, but the story was started with %s (story.json)", name, path, recorded)}
			}
		} else {
			r.rec.Services[name] = path
		}
		if err := r.checkRepository(name, path); err != nil {
			return err
		}
	}
	return nil
}

func (r *storyRunner) checkRepository(name, path string) error {
	repo, err := repository.Open(r.ctx, path, r.osExec)
	if err != nil {
		return err
	}
	if has, err := repo.HasCommit(r.ctx); err != nil {
		return err
	} else if !has {
		return &apperr.ConfigError{Msg: fmt.Sprintf("service %s (%s) has no commit: a story needs a commit to start from", name, path)}
	}
	if _, err := os.Lstat(filepath.Join(path, ".gitmodules")); err == nil {
		return &apperr.ConfigError{Msg: fmt.Sprintf("service %s (%s) has submodules (.gitmodules): a worktree run does not cover them", name, path)}
	}

	if b, ok := r.rec.Bases[name]; ok {
		if _, exists, err := repo.BranchTip(r.ctx, b.Branch); err != nil {
			return err
		} else if !exists {
			return &apperr.ConfigError{Msg: fmt.Sprintf("service %s: the story base branch %s (story.json) does not exist any more in %s", name, b.Branch, path)}
		}
	} else {
		branch, err := repo.Branch(r.ctx)
		if err != nil {
			return err
		}
		if branch == "" || !story.ValidBaseBranch(branch) {
			return &apperr.ConfigError{Msg: fmt.Sprintf("service %s (%s) is not on a branch: check out the story base branch in it first", name, path)}
		}
		head, err := repo.HeadRef(r.ctx)
		if err != nil {
			return err
		}
		if r.rec.Bases == nil {
			r.rec.Bases = map[string]persistence.StoryBase{}
		}
		r.rec.Bases[name] = persistence.StoryBase{Branch: branch, SHA: head}
	}

	branches, err := repo.Branches(r.ctx, "story/"+r.loc.slug)
	if err != nil {
		return err
	}
	known := r.knownBranches()
	for _, b := range branches {
		if !known[b] {
			return &apperr.ConfigError{Msg: fmt.Sprintf("service %s: the branch %s exists in %s, but story.json does not know it; a story never reuses a branch. "+
				"Delete it (git branch -D %s) or rename it", name, b, path, b)}
		}
	}

	r.reporter.StoryChecking(name, path)
	if code := r.child(path, "validate"); code != 0 {
		return &apperr.ConfigError{Msg: fmt.Sprintf("orchestrator validate failed in service %s (%s, exit code %d): no task was started", name, path, code)}
	}
	return nil
}

// knownBranches are the branches the record accounts for: every attempt's branch of every task
// it has a state for, the ones --retry left behind included.
func (r *storyRunner) knownBranches() map[string]bool {
	known := map[string]bool{}
	for id, rt := range r.rec.Tasks {
		for attempt := 1; attempt <= rt.Attempt; attempt++ {
			known[persistence.StoryTaskBranch(r.loc.slug, id, attempt)] = true
		}
	}
	return known
}

// checkBudget refuses when the cost recorded so far has reached story.max_cost_usd (§10.1 point
// 4, and before every dispatch). Nothing is dispatched on a total that cannot be told.
func (r *storyRunner) checkBudget() error {
	limit := r.cfg.Story.MaxCostUSD
	if limit == nil {
		return nil
	}
	if total := r.rec.TotalCostUSD(); total >= *limit {
		return &apperr.LimitExceededError{Reason: fmt.Sprintf("Story status: stopped\nReason: the story has cost $%.2f, story.max_cost_usd is $%.2f\n\n"+
			"Raise story.max_cost_usd to continue it.", total, *limit)}
	}
	return nil
}

// jiraKey is the key a task's input carries: the one in its front matter, else the one the record
// has; "" for none.
func jiraKey(t story.Task, rt persistence.StoryTask) string {
	if story.ValidJiraKey(t.Jira) {
		return t.Jira
	}
	return rt.Jira
}

// approve is the first start: the plan is printed and, at a terminal, confirmed once; the answer
// and the command are the approval (§10.1). Later starts only save what the checks recorded.
func (r *storyRunner) approve() error {
	if r.rec.ApprovedAt == nil {
		plan := make([]logging.StoryPlanTask, 0, len(r.order))
		repos := map[string]bool{}
		for _, id := range r.order {
			t := r.byID[id]
			repos[t.Repo] = true
			base := fmt.Sprintf("%s@%s", r.rec.Bases[t.Repo].Branch, shortSHA(r.rec.Bases[t.Repo].SHA))
			if dep, _ := story.Base(r.spec.tasks, id); dep != "" {
				base = persistence.StoryTaskBranch(r.loc.slug, dep, 1)
			}
			plan = append(plan, logging.StoryPlanTask{
				ID: id, Repo: t.Repo, Base: base, Branch: persistence.StoryTaskBranch(r.loc.slug, id, 1), Jira: jiraKey(t, persistence.StoryTask{}),
			})
		}
		r.reporter.StoryRunPlan(plan, len(repos))
		// --mode auto and a stdin that is not a terminal go on.
		if r.opts.mode != domain.ModeAuto && isTerminal(r.stdin) {
			r.reporter.StoryConfirm(len(plan), len(repos))
			if !confirmed(r.stdin) {
				r.reporter.StoryCancelled()
				return fmt.Errorf("story run cancelled: %w", context.Canceled)
			}
		}
		now := time.Now().UTC()
		r.rec.ApprovedAt = &now
	}
	r.rec.Status = story.StatusRunning
	return r.persist()
}

// readAnswer reads one line from in a byte at a time, so nothing past it is consumed: the
// children share the terminal's stdin.
func readAnswer(in io.Reader) (string, error) {
	var b strings.Builder
	var one [1]byte
	for b.Len() < 256 {
		n, err := in.Read(one[:])
		if n == 1 {
			if one[0] == '\n' {
				break
			}
			b.WriteByte(one[0])
		}
		if err != nil {
			if b.Len() == 0 {
				return "", err
			}
			break
		}
	}
	return strings.TrimSpace(b.String()), nil
}

// confirmed reads the answer to "[Y/n]": an empty line, y or yes is a yes; anything else, and a
// stdin that ends, is a no.
func confirmed(in io.Reader) bool {
	answer, err := readAnswer(in)
	if err != nil {
		return false
	}
	switch strings.ToLower(answer) {
	case "", "y", "yes":
		return true
	}
	return false
}

// persist saves story.json and rewrites README.md from it.
func (r *storyRunner) persist() error {
	if err := persistence.SaveStoryRecord(r.loc.storyDir, r.rec); err != nil {
		return err
	}
	return r.writeREADME()
}

// writeREADME rewrites the spec directory's README.md when it differs (§10.3).
func (r *storyRunner) writeREADME() error {
	text := story.RenderREADME(r.spec.title, r.rec.Status, r.spec.tasks, storyStates(r.rec.Tasks))
	path := filepath.Join(r.loc.specDir, "README.md")
	if current, err := os.ReadFile(path); err == nil && string(current) == text {
		return nil
	}
	if err := persistence.ReplaceFile(path, []byte(text)); err != nil {
		return fmt.Errorf("rewrite %s: %w", path, err)
	}
	return nil
}

// halt records the story's status when it stops short; a failure to save is reported, not
// returned, since the story is already ending with a better error.
func (r *storyRunner) halt(status story.Status) {
	r.rec.Status = status
	if err := r.persist(); err != nil {
		fmt.Fprintf(r.stderr, "Warning: %v\n", err)
	}
}

// child runs the orchestrator itself in dir, with the terminal's stdio and auth overrides when
// the operator gave it, and returns its exit code.
func (r *storyRunner) child(dir string, args ...string) int {
	if r.opts.claudeAuth != "" {
		args = append([]string{"--claude-auth", r.opts.claudeAuth}, args...)
	}
	if r.opts.codexAuth != "" {
		args = append([]string{"--codex-auth", r.opts.codexAuth}, args...)
	}
	code, err := runChild(r.ctx, r.exe, dir, args, r.stdin, r.stdout, r.stderr)
	if err != nil {
		fmt.Fprintf(r.stderr, "Error: cannot run %s in %s: %v\n", args[len(args)-1], dir, err)
	}
	return code
}

// reconcile settles every task the record still has as running: the process that ran it ended
// before its result was recorded (a crash, a kill), so its run is looked up as after a child.
func (r *storyRunner) reconcile() error {
	for _, id := range r.order {
		if r.rec.Tasks[id].Status != story.TaskRunning {
			continue
		}
		if _, err := r.settle(id, -1); err != nil {
			return err
		}
	}
	return nil
}

// applyRetry abandons the run of the task --retry names and prepares its next attempt on
// <branch>.<n>: the task is pending again, re-read at its dispatch, and the tasks stacked on it
// follow it there (§10.2).
func (r *storyRunner) applyRetry() error {
	id := r.opts.retry
	if id == "" {
		return nil
	}
	rt := r.rec.Tasks[id]
	if !retryable(rt.Status, false) {
		return &apperr.ConfigError{Msg: fmt.Sprintf("--retry %s: the task is %s now; only a failed, stopped or blocked task is dispatched again", id, orPending(rt.Status))}
	}
	r.reporter.StoryRetry(id, rt.RunID, rt.Attempt+1, persistence.StoryTaskBranch(r.loc.slug, id, rt.Attempt+1))
	rt.AbandonedCostUSD += rt.CostUSD
	rt.CostUSD = 0
	rt.Attempt++
	rt.Branch = persistence.StoryTaskBranch(r.loc.slug, id, rt.Attempt)
	rt.Status = story.TaskPending
	rt.RunID, rt.Commit, rt.FileSHA256, rt.BaseRef = "", "", "", ""
	r.rec.Tasks[id] = rt
	return r.persist()
}

// dispatchAll walks the tasks in the story's order and takes each to done, or stops at the first
// that does not get there. A task that is done is skipped.
func (r *storyRunner) dispatchAll() error {
	for pos, id := range r.order {
		if r.rec.Tasks[id].Status == story.TaskDone {
			continue
		}
		if err := r.ctx.Err(); err != nil {
			r.halt(story.StatusStopped)
			return fmt.Errorf("story %s stopped before %s: %w", r.loc.slug, id, err)
		}
		status, err := r.advance(pos, id)
		if err != nil {
			return err
		}
		if status != story.TaskDone {
			return r.stop(id, status)
		}
	}
	return r.finish()
}

// advance takes one task a step on: a blocked one is looked at again, a failed or stopped one
// with a run is resumed, anything else is dispatched.
func (r *storyRunner) advance(pos int, id string) (story.TaskStatus, error) {
	rt := r.rec.Tasks[id]
	switch {
	case rt.Status == story.TaskBlocked:
		return r.settleBlocked(id)
	case (rt.Status == story.TaskFailed || rt.Status == story.TaskStopped) && rt.RunID != "":
		return r.resume(pos, id)
	}
	return r.dispatch(pos, id)
}

// dispatch writes the task's input, freezes its file and runs the child (§10.2).
func (r *storyRunner) dispatch(pos int, id string) (story.TaskStatus, error) {
	if err := r.checkBudget(); err != nil {
		r.halt(story.StatusStopped)
		return "", err
	}
	t := r.byID[id]
	rt := r.rec.Tasks[id]
	attempt := max(rt.Attempt, 1)
	branch := persistence.StoryTaskBranch(r.loc.slug, id, attempt)

	// None stacked: the repository's story base. One: that task's commit as it was recorded -
	// not its branch's tip now, which an operator's commit may have moved.
	base := r.rec.Bases[t.Repo]
	baseRef := base.SHA
	dep, err := story.Base(r.spec.tasks, id)
	if err != nil {
		return "", &apperr.ConfigError{Msg: err.Error()}
	}
	if dep != "" {
		d := r.rec.Tasks[dep]
		if d.Status != story.TaskDone || d.Commit == "" {
			return "", &apperr.WorkflowError{Msg: fmt.Sprintf("%s is stacked on %s, which is not done", id, dep)}
		}
		baseRef = d.Commit
	}

	var notes []story.DepNote
	for _, depID := range t.DependsOn {
		notes = append(notes, story.DepNote{ID: depID, Repo: r.byID[depID].Repo, AsBuilt: story.AsBuiltText(r.spec.data[depID])})
	}
	text, err := story.RenderInputText(r.spec.title, pos+1, len(r.order), t, r.spec.approach, r.spec.contracts, notes)
	if err != nil {
		return "", &apperr.ConfigError{Msg: err.Error()}
	}
	acceptance := t.Acceptance
	if acceptance == nil {
		acceptance = []string{}
	}
	jira := jiraKey(t, rt)
	file, err := persistence.WriteStoryTaskInput(r.loc.storyDir, story.TaskInput{
		Story: r.loc.slug, Task: id, Attempt: attempt, Branch: branch, BaseRef: baseRef, BaseBranch: base.Branch,
		Jira: jira, Acceptance: acceptance, Text: text,
	})
	if err != nil {
		return "", &apperr.ConfigError{Msg: fmt.Sprintf("%s: %v", id, err)}
	}

	// Recorded before the child starts, so a story run that dies meanwhile finds the task running.
	r.rec.Tasks[id] = persistence.StoryTask{
		Status: story.TaskRunning, FileSHA256: story.FrozenHash(r.spec.data[id]), Attempt: attempt, Repo: t.Repo,
		Branch: branch, BaseRef: baseRef, Jira: jira, JiraIntent: rt.JiraIntent, AbandonedCostUSD: rt.AbandonedCostUSD,
	}
	r.rec.Status = story.StatusRunning
	if err := r.persist(); err != nil {
		return "", err
	}

	r.reporter.StoryTaskHeader(id, t.Repo, pos+1, len(r.order))
	exit := r.child(r.rec.Services[t.Repo], "run", "--mode", "auto", "--story-task", file)
	return r.settle(id, exit)
}

// resume continues the run of a failed or stopped task (`resume <id> --mode auto` in its
// repository); no new run is started.
func (r *storyRunner) resume(pos int, id string) (story.TaskStatus, error) {
	if err := r.checkBudget(); err != nil {
		r.halt(story.StatusStopped)
		return "", err
	}
	rt := r.rec.Tasks[id]
	run, err := r.loadRun(rt)
	if err != nil {
		return "", &apperr.ConfigError{Msg: fmt.Sprintf("%s: cannot resume run %s of its repository", id, rt.RunID), Err: err}
	}
	if src := run.Task.Source; src == nil || src.Kind != domain.TaskSourceStory || src.Story != r.loc.slug || src.Task != id || src.Attempt != rt.Attempt {
		return "", &apperr.ConfigError{Msg: fmt.Sprintf("%s: run %s is not a run of this task's attempt %d", id, rt.RunID, rt.Attempt)}
	}
	rt.Status = story.TaskRunning
	r.rec.Tasks[id] = rt
	r.rec.Status = story.StatusRunning
	if err := r.persist(); err != nil {
		return "", err
	}

	r.reporter.StoryTaskHeader(id, rt.Repo, pos+1, len(r.order))
	r.reporter.StoryResume(id, rt.RunID)
	exit := r.child(r.rec.Services[rt.Repo], "resume", rt.RunID, "--mode", "auto")
	return r.settle(id, exit)
}

// settleBlocked counts a blocked task done once its branch has a commit beyond its base: the
// operator committed in the kept worktree.
func (r *storyRunner) settleBlocked(id string) (story.TaskStatus, error) {
	rt := r.rec.Tasks[id]
	repo, err := repository.Open(r.bg, r.rec.Services[rt.Repo], r.osExec)
	if err != nil {
		return "", err
	}
	tip, exists, err := repo.BranchTip(r.bg, rt.Branch)
	if err != nil {
		return "", err
	}
	if !exists {
		return story.TaskBlocked, nil
	}
	n, err := repo.CommitsBeyond(r.bg, rt.BaseRef, rt.Branch)
	if err != nil {
		return "", err
	}
	if n == 0 {
		return story.TaskBlocked, nil
	}
	run, err := r.loadRun(rt)
	if err != nil {
		return "", &apperr.ConfigError{Msg: fmt.Sprintf("%s: cannot read run %s of its repository", id, rt.RunID), Err: err}
	}
	return r.complete(id, rt, run, tip)
}

// loadRun reads the task's run, by its recorded ID, from its repository's runs directory.
func (r *storyRunner) loadRun(rt persistence.StoryTask) (*domain.Run, error) {
	if rt.RunID == "" {
		return nil, errors.New("the task has no run")
	}
	return persistence.NewStore(filepath.Join(r.rec.Services[rt.Repo], runsBaseDir)).LoadRun(rt.RunID)
}

// pickStoryRun is the run of attempt n of task id of story slug among the runs found in a runs
// directory: the one runID names when it is among them, else the newest. A run of another task,
// another story or another attempt is never taken - nothing is found rather than a stale run.
func pickStoryRun(found []persistence.FoundRun, slug, id string, attempt int, runID string) *domain.Run {
	var best *domain.Run
	for i := range found {
		run := &found[i].Run
		src := run.Task.Source
		if src == nil || src.Kind != domain.TaskSourceStory || src.Story != slug || src.Task != id || src.Attempt != attempt {
			continue
		}
		if runID != "" && run.ID == runID {
			return run
		}
		if best == nil || run.CreatedAt.After(best.CreatedAt) {
			best = run
		}
	}
	return best
}

// mapRunStatus is the task status a run's end gives (§10.2): completed with a commit is done,
// completed without one is blocked, failed is failed, and anything else - stopped, or a run
// whose process died - is stopped, which a resume continues.
func mapRunStatus(run *domain.Run) story.TaskStatus {
	switch run.Status {
	case domain.RunCompleted:
		if run.Commit != "" {
			return story.TaskDone
		}
		return story.TaskBlocked
	case domain.RunFailed:
		return story.TaskFailed
	}
	return story.TaskStopped
}

// runCostUSD is what the run reported it cost; false when it has no outcome and so no cost is
// known.
func runCostUSD(run *domain.Run) (float64, bool) {
	if run.Outcome == nil {
		return 0, false
	}
	return run.Outcome.Usage.CostUSD, true
}

// settle finds the run of the task's current attempt and records what became of the task. exit
// is the exit code of the child that ran it, -1 when there was none (a reconciliation).
func (r *storyRunner) settle(id string, exit int) (story.TaskStatus, error) {
	rt := r.rec.Tasks[id]
	found, bad, err := persistence.LoadRunDirs(filepath.Join(r.rec.Services[rt.Repo], runsBaseDir))
	if err != nil {
		fmt.Fprintf(r.stderr, "Warning: cannot list the runs of %s: %v\n", rt.Repo, err)
	}
	for _, b := range bad {
		fmt.Fprintf(r.stderr, "Warning: %s is not a readable run and is ignored: %v\n", b.Dir, b.Err)
	}
	run := pickStoryRun(found, r.loc.slug, id, rt.Attempt, rt.RunID)

	// No run: the child failed before it made one (its own fail-fast chain, a refused input).
	// The task is failed - or stopped, if the operator cancelled - and dispatched again.
	if run == nil {
		rt.Status, rt.RunID = story.TaskFailed, ""
		detail := "no run was found"
		if exit >= 0 {
			detail = fmt.Sprintf("no run was found; the child exited with code %d", exit)
		}
		if r.ctx.Err() != nil {
			rt.Status, detail = story.TaskStopped, "cancelled before a run was recorded"
		}
		r.rec.Tasks[id] = rt
		r.reporter.StoryTaskResult(id, string(rt.Status), detail)
		return rt.Status, r.persist()
	}

	rt.RunID = run.ID
	cost, known := runCostUSD(run)
	if known {
		// A resumed run's outcome holds the cost of its earlier attempts: replaced, not added.
		rt.CostUSD = cost
	}
	status := mapRunStatus(run)
	if status == story.TaskDone {
		return r.complete(id, rt, run, run.Commit)
	}
	// A call the operator's Ctrl-C ended fails its step, so its run is failed rather than stopped;
	// the task is stopped all the same, and the next story run resumes the run.
	if status == story.TaskFailed && r.ctx.Err() != nil {
		status = story.TaskStopped
	}

	rt.Status = status
	r.rec.Tasks[id] = rt
	r.reporter.StoryTaskResult(id, string(status), runDetail(run))
	if err := r.persist(); err != nil {
		return "", err
	}
	if !known && r.cfg.Story.MaxCostUSD != nil {
		r.halt(story.StatusStopped)
		return "", &apperr.LimitExceededError{Reason: fmt.Sprintf("Story status: stopped\nReason: the cost of run %s is unknown, so story.max_cost_usd cannot be checked", run.ID)}
	}
	return status, nil
}

// runDetail is the one-line reason shown for a run that did not end in a commit.
func runDetail(run *domain.Run) string {
	if run.Status == domain.RunCompleted {
		return "the run completed without a commit"
	}
	reason := strings.Join(strings.Fields(run.FailureReason), " ")
	if len(reason) > 160 {
		reason = reason[:160] + "…"
	}
	if reason == "" {
		return "run " + run.ID
	}
	return reason
}

// complete records a task as done with its commit: As built in its file, then the record, then
// README.md (§10.3), in that order, so that a story run that dies between them finds the task
// running with a completed run and settles it again without writing As built twice.
func (r *storyRunner) complete(id string, rt persistence.StoryTask, run *domain.Run, commit string) (story.TaskStatus, error) {
	rt.Status, rt.Commit, rt.RunID = story.TaskDone, commit, run.ID
	if err := r.writeAsBuilt(id, rt, run); err != nil {
		return "", err
	}
	r.rec.Tasks[id] = rt
	if err := r.persist(); err != nil {
		return "", err
	}
	r.reporter.StoryTaskResult(id, string(story.TaskDone), "commit "+shortSHA(commit))
	return story.TaskDone, nil
}

// writeAsBuilt appends the As built section to the task's file. The file must still be what was
// dispatched; Go writes only below the frozen part, so its hash is unchanged.
func (r *storyRunner) writeAsBuilt(id string, rt persistence.StoryTask, run *domain.Run) error {
	path := r.spec.files[id]
	current, err := os.ReadFile(path)
	if err != nil {
		return &apperr.ConfigError{Msg: "cannot read " + path, Err: err}
	}
	if story.FrozenHash(current) != rt.FileSHA256 {
		return &apperr.ConfigError{Msg: fmt.Sprintf("%s: its task file %s was changed while the task ran, so its As built record is not written; "+
			"revert the change and run the story again", id, path)}
	}
	if story.AsBuiltText(current) != "" {
		r.spec.data[id] = current
		return nil
	}
	updated := story.AppendAsBuilt(current, r.asBuilt(id, rt, run))
	if story.FrozenHash(updated) != rt.FileSHA256 {
		return fmt.Errorf("%s: appending As built changed the frozen part of %s", id, path)
	}
	if err := persistence.ReplaceFile(path, updated); err != nil {
		return fmt.Errorf("%s: write As built to %s: %w", id, path, err)
	}
	r.spec.data[id] = updated
	return nil
}

// asBuilt is the record of a done task, from the run's own record; the one thing a model wrote in
// it is the implement step's summary_short.
func (r *storyRunner) asBuilt(id string, rt persistence.StoryTask, run *domain.Run) story.AsBuilt {
	base := r.rec.Bases[rt.Repo]
	stackedOn := base.Branch
	if dep, _ := story.Base(r.spec.tasks, id); dep != "" {
		stackedOn = r.rec.Tasks[dep].Branch
	}
	ab := story.AsBuilt{
		Date: time.Now().Format("2006-01-02"), RunID: run.ID, Repo: rt.Repo, Status: string(run.Status),
		FixIterations: run.FixIterations, Branch: rt.Branch, Base: stackedOn, BaseSHA: rt.BaseRef, Commit: rt.Commit,
		CostUSD: rt.CostUSD, BaseBranch: base.Branch,
	}
	if o := run.Outcome; o != nil {
		ab.TestsPassed = o.TestsPassedFinal != nil && *o.TestsPassedFinal
		ab.ReviewVerdict = string(o.ReviewVerdict)
	}
	if run.Worktree != nil {
		ab.Conflicts = strings.Join(run.Worktree.Conflicts, ", ")
	}
	store := persistence.NewStore(filepath.Join(r.rec.Services[rt.Repo], runsBaseDir))
	ab.Summary = implementSummary(store, run)
	return ab
}

// implementSummary is the summary_short of the run's last completed implement step, or "".
func implementSummary(store *persistence.Store, run *domain.Run) string {
	for i := len(run.Executions) - 1; i >= 0; i-- {
		ex := run.Executions[i]
		if ex.Action != domain.ActionImplement || ex.Status != domain.StepCompleted {
			continue
		}
		data, err := store.ReadArtifact(run.ID, ex.Index, domain.ActionImplement)
		if err != nil {
			return ""
		}
		var artifact struct {
			SummaryShort string `json:"summary_short"`
		}
		if err := json.Unmarshal(data, &artifact); err != nil {
			return ""
		}
		return artifact.SummaryShort
	}
	return ""
}

// stop ends the story at a task that did not get to done: the story takes the matching status,
// the operator is told what to do next, and the error is the command's.
func (r *storyRunner) stop(id string, status story.TaskStatus) error {
	rt := r.rec.Tasks[id]
	storyStatus := story.StatusFailed
	switch status {
	case story.TaskBlocked:
		storyStatus = story.StatusBlocked
	case story.TaskStopped:
		storyStatus = story.StatusStopped
	}
	r.halt(storyStatus)

	dir := logging.ShellWord(r.dirArg)
	next := persistence.StoryTaskBranch(r.loc.slug, id, rt.Attempt+1)
	switch {
	case status == story.TaskBlocked:
		where := "the kept worktree"
		if run, err := r.loadRun(rt); err == nil && run.Worktree != nil && run.Worktree.Path != "" {
			where = run.Worktree.Path
		}
		r.reporter.StoryHint(
			fmt.Sprintf("Commit the work on %s in %s, or drop it, then: orchestrator story run %s", rt.Branch, where, dir),
			fmt.Sprintf("A task whose branch has a commit counts as done. To run it again instead: orchestrator story run %s --retry %s (branch %s)", dir, id, next))
	case rt.RunID != "":
		r.reporter.StoryHint(
			fmt.Sprintf("orchestrator story run %s resumes run %s", dir, rt.RunID),
			fmt.Sprintf("orchestrator story run %s --retry %s abandons it and runs the task again on %s", dir, id, next))
	default:
		r.reporter.StoryHint(fmt.Sprintf("orchestrator story run %s dispatches %s again", dir, id))
	}

	if status == story.TaskStopped && r.ctx.Err() != nil {
		return fmt.Errorf("story %s stopped at %s: %w", r.loc.slug, id, r.ctx.Err())
	}
	return &apperr.WorkflowError{Msg: fmt.Sprintf("story %s is %s: %s is %s", r.loc.slug, storyStatus, id, status)}
}

// finish ends a story whose tasks are all done (§10.4): the story is completed, and the table of
// branches and merge request targets is printed.
func (r *storyRunner) finish() error {
	r.rec.Status = story.StatusCompleted
	if err := r.persist(); err != nil {
		return err
	}
	rows := make([]logging.StoryEndRow, 0, len(r.order))
	for _, id := range r.order {
		rt := r.rec.Tasks[id]
		target := r.rec.Bases[rt.Repo].Branch
		if dep, _ := story.Base(r.spec.tasks, id); dep != "" {
			target = r.rec.Tasks[dep].Branch
		}
		row := logging.StoryEndRow{ID: id, Repo: rt.Repo, Branch: rt.Branch, Target: target, Commit: rt.Commit, Jira: rt.Jira}
		if run, err := r.loadRun(rt); err == nil && run.Worktree != nil {
			row.Conflicts = strings.Join(run.Worktree.Conflicts, ", ")
		}
		rows = append(rows, row)
	}
	r.reporter.StoryEnd(rows)
	return nil
}

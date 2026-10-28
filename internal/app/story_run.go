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
	storyRunUsage    = "usage: orchestrator story run <spec-dir> [--retry <ID>] [--parallel <N>] [--mode auto] [--claude-auth auto|api|login] [--codex-auth auto|api|login]"
	storyStatusUsage = "usage: orchestrator story status <spec-dir>"
	storyUsage       = `usage: orchestrator story plan [--jira <KEY|URL>] [--repo <name>]... "<task>"   or   ` +
		`orchestrator story run <spec-dir> [--retry <ID>] [--parallel <N>]   or   orchestrator story status <spec-dir>`
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
	retry string
	// parallel is --parallel: how many ready tasks run at once, in place of story.max_parallel; 0
	// when not given.
	parallel int
	verbose  bool
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

	// fingerprints are the git fingerprints the pre-run checks took, by service; merged is the
	// services whose fingerprint was compared again before their first merge in this run.
	fingerprints map[string]string
	merged       map[string]bool
	// remotes are the git remotes the services deliver to (their delivery.remote), by service.
	remotes map[string]string
}

func (r *storyRunner) run() error {
	if err := r.load(); err != nil {
		return err
	}
	if err := r.checkLiveRuns(); err != nil {
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
	continued := r.rec.ApprovedAt != nil
	if err := r.approve(); err != nil {
		return err
	}
	// The spec branch exists before the Jira children and any dispatch (§1).
	if err := r.createSpecBranches(continued); err != nil {
		return err
	}
	// and is on the remote before any merge request targets it (§9).
	if err := r.pushSpecBranches(); err != nil {
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
		if err := story.CheckAcceptance(id+": acceptance", r.byID[id].Acceptance, r.cfg.Story.MaxAcceptanceChars); err != nil {
			return &apperr.ConfigError{Msg: err.Error()}
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
		// A conflict task was never dispatched: its file is read again when it is.
		if rt.Status == story.TaskPending || rt.Status == story.TaskConflict || id == r.opts.retry {
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
		if b.SpecBranch != "" {
			if _, exists, err := repo.BranchTip(r.ctx, b.SpecBranch); err != nil {
				return err
			} else if !exists {
				return &apperr.ConfigError{Msg: fmt.Sprintf("service %s: the spec branch %s (story.json) does not exist any more in %s; "+
					"create it again at the commit it had, or restore it from the reflog", name, b.SpecBranch, path)}
			}
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
	// The spec branch is known where the record names it.
	if r.rec.Bases[name].SpecBranch != "" {
		known[r.rec.Bases[name].SpecBranch] = true
	}
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
	if err := r.checkDeliverable(name, path, repo); err != nil {
		return err
	}

	// What makes git run code in this repository (config, hooks), kept until the first merge-tree:
	// a configured merge driver would run there.
	fingerprint, err := repo.GitFingerprint(r.ctx)
	if err != nil {
		return err
	}
	if r.fingerprints == nil {
		r.fingerprints = map[string]string{}
	}
	r.fingerprints[name] = fingerprint
	return nil
}

// checkDeliverable is the part of §8 (specs/delivery/SPEC.md) that `orchestrator validate` leaves
// to the isolation the service's config names, for a story task always runs in a worktree and
// delivers: the service has the remote it pushes to, and its secret scan is on. A service without
// them stops the story before anything is dispatched.
func (r *storyRunner) checkDeliverable(name, path string, repo *repository.Repository) error {
	svc, err := config.Load(filepath.Join(path, defaultConfigPath))
	if err != nil {
		return &apperr.ConfigError{Msg: fmt.Sprintf("service %s (%s): cannot read its config: %v", name, path, err)}
	}
	remote := svc.Delivery.RemoteName()
	if _, err := repo.RemoteURL(r.ctx, remote); err != nil {
		return &apperr.ConfigError{Msg: fmt.Sprintf("service %s (%s) has no git remote %q (delivery.remote), and every task of a story is pushed and "+
			"opened as a merge request there: add the remote; no task was started", name, path, remote), Err: err}
	}
	if !svc.Security.SecretScan.Enabled() {
		return &apperr.ConfigError{Msg: fmt.Sprintf("service %s (%s): security.secret_scan.mode is off, and every task of a story pushes its branch, "+
			"so nothing would look for a secret first: turn the scan on; no task was started", name, path)}
	}
	if r.remotes == nil {
		r.remotes = map[string]string{}
	}
	r.remotes[name] = remote
	return nil
}

// knownBranches are the branches the record accounts for: the name each task's child recorded
// (a repository's rules may have named it), and the story's own fallback of every attempt of every
// task it has a state for - the ones --retry left behind included, and the one a child that has not
// been read yet may have created.
func (r *storyRunner) knownBranches() map[string]bool {
	known := map[string]bool{}
	for id, rt := range r.rec.Tasks {
		if rt.Branch != "" {
			known[rt.Branch] = true
		}
		for attempt := 1; attempt <= rt.Attempt; attempt++ {
			known[persistence.StoryTaskBranch(r.loc.slug, id, attempt)] = true
		}
	}
	return known
}

// checkBudget refuses when the cost recorded so far has reached story.max_cost_usd (§10.1 point
// 4, and before every dispatch). Nothing is dispatched on a total that cannot be told.
func (r *storyRunner) checkBudget() error {
	return r.checkBudgetAt(r.rec.TotalCostUSD())
}

// checkBudgetAt is checkBudget for a total that is not only the recorded one: the running
// children's cost so far is in it.
func (r *storyRunner) checkBudgetAt(total float64) error {
	limit := r.cfg.Story.MaxCostUSD
	if limit == nil {
		return nil
	}
	if total >= *limit {
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
			if label := r.startLabel(id); label != "" {
				base = label
			}
			plan = append(plan, logging.StoryPlanTask{
				// The fallback only: the name is fixed when the child creates its worktree, by its
				// repository's rules (specs/delivery/SPEC.md §9.1, A23).
				ID: id, Repo: t.Repo, Base: base, Branch: persistence.StoryTaskBranch(r.loc.slug, id, 1) + " or by " + t.Repo + "'s rules",
				Jira: jiraKey(t, persistence.StoryTask{}),
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

// parentBranch is the branch of a parent task: the one its record has - the name its child's run
// recorded, which a repository's rules may have chosen - else, for a parent that has not run, the
// fallback its child will use unless they do.
func (r *storyRunner) parentBranch(id string) string {
	rt := r.rec.Tasks[id]
	if rt.Branch != "" {
		return rt.Branch
	}
	return persistence.StoryTaskBranch(r.loc.slug, id, max(rt.Attempt, 1))
}

// startLabel names what a task with parents starts from, for the plan and As built: its only
// parent's branch, or the spec branch with the parents merged on it, "story/<slug>/spec
// (PS02 + PS03)". "" for a task without parents: it starts from the story base.
func (r *storyRunner) startLabel(id string) string {
	parents, _ := story.Parents(r.spec.tasks, id)
	switch len(parents) {
	case 0:
		return ""
	case 1:
		return r.parentBranch(parents[0])
	}
	return fmt.Sprintf("%s (%s)", persistence.StorySpecBranch(r.loc.slug), strings.Join(parents, " + "))
}

// targetBranch is the branch a task's merge request targets: its only parent's branch, else the
// spec branch.
func (r *storyRunner) targetBranch(id string) string {
	if parents, _ := story.Parents(r.spec.tasks, id); len(parents) == 1 {
		return r.parentBranch(parents[0])
	}
	return persistence.StorySpecBranch(r.loc.slug)
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

// childArgs are the arguments of a child: the auth overrides the operator gave, then args.
func (r *storyRunner) childArgs(args ...string) []string {
	if r.opts.claudeAuth != "" {
		args = append([]string{"--claude-auth", r.opts.claudeAuth}, args...)
	}
	if r.opts.codexAuth != "" {
		args = append([]string{"--codex-auth", r.opts.codexAuth}, args...)
	}
	return args
}

// child runs the orchestrator itself in dir, with the terminal's stdio and auth overrides when
// the operator gave it, and returns its exit code.
func (r *storyRunner) child(dir string, args ...string) int {
	args = r.childArgs(args...)
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
	rt.Status = story.TaskPending
	// The branch is the new attempt's, and the child's run names it (§9.1): absent until it is read.
	rt.RunID, rt.Commit, rt.FileSHA256, rt.BaseRef, rt.Branch, rt.MR = "", "", "", "", "", nil
	r.rec.Tasks[id] = rt
	return r.persist()
}

// slots is how many tasks run at once: --parallel, else story.max_parallel.
func (r *storyRunner) slots() int {
	n := r.opts.parallel
	if n == 0 {
		n = r.cfg.Story.MaxParallel
	}
	return max(n, 1)
}

// childJob is a task whose child is ready to start: the task is recorded as running, and its
// input is written.
type childJob struct {
	id   string
	pos  int
	repo string
	dir  string
	args []string
	// resume is the run the child continues, "" for a new run.
	resume string
	// log is where the child's output goes with more than one slot, and logPath its name.
	log     *os.File
	logPath string
}

// childEnd is what a child goroutine tells the loop: how its process ended.
type childEnd struct {
	id   string
	exit int
	err  error
}

// dispatchLoop is the state of the loop that runs the tasks. Only the goroutine that runs the loop
// reads or writes it, or the story's record, README, task files and the console.
type dispatchLoop struct {
	slots   int
	running map[string]*childJob
	ends    chan childEnd
	// notDone are the tasks that ended without a commit, in the order they ended; looked are the
	// tasks that ended in this loop in any way but done, which nothing looks at again in it.
	notDone []string
	looked  map[string]bool
	// stopping is whether one of them ended in a way that stops anything new from starting. A task
	// that is blocked - its child completed without a commit, exit code 6 - leaves a person to
	// commit or retry it and holds only the tasks that depend on it (specs/delivery/SPEC.md §9):
	// independent tasks go on.
	stopping bool
	// err is the first error that is not a task's status; budgetErr the budget refusal;
	// stoppedBefore the task that was ready when the operator pressed Ctrl-C.
	err           error
	budgetErr     error
	stoppedBefore string
}

// draining is whether nothing new is started: something failed or stopped, the budget is reached,
// the operator cancelled, or the loop failed.
func (lp *dispatchLoop) draining() bool {
	return lp.err != nil || lp.stopping || lp.budgetErr != nil || lp.stoppedBefore != ""
}

// ended records how a task came out of a settle or of looking at it again.
func (lp *dispatchLoop) ended(id string, status story.TaskStatus, err error) {
	switch {
	case err != nil:
		if lp.err == nil {
			lp.err = err
		}
	case status != story.TaskDone:
		lp.notDone = append(lp.notDone, id)
		if lp.looked == nil {
			lp.looked = map[string]bool{}
		}
		lp.looked[id] = true
		if status != story.TaskBlocked {
			lp.stopping = true
		}
	}
}

// dispatchAll takes the tasks to done, up to slots of them at once: the loop fills the free slots
// with the ready tasks in the story's order, waits for one child to end, settles it, and repeats
// until nothing runs and nothing is ready. A task that does not end done, the budget, a Ctrl-C or
// an error stop it from starting anything new; the children that run are waited for and settled
// first (§2).
func (r *storyRunner) dispatchAll() error {
	slots := r.slots()
	lp := &dispatchLoop{slots: slots, running: map[string]*childJob{}, ends: make(chan childEnd, slots)}
	for {
		r.fill(lp)
		if len(lp.running) == 0 {
			break
		}
		r.settleEnd(lp, <-lp.ends)
	}
	return r.conclude(lp)
}

// nextReady is the first task story.Ready names that was not looked at in this loop, does not
// wait for a task without a merge request and whose service has no task running, "" for none.
// Tasks of one service run one at a time: their tests share its ports, databases and files outside
// the worktree, and their changes meet in its merge (TODO.md S10); the next ready task of another
// service takes the slot meanwhile.
func (r *storyRunner) nextReady(looked map[string]bool, running map[string]*childJob) string {
	states := make(map[string]story.TaskStatus, len(r.rec.Tasks))
	for id, rt := range r.rec.Tasks {
		states[id] = rt.Status
	}
	busy := map[string]bool{}
	for _, job := range running {
		busy[job.repo] = true
	}
	for _, id := range story.Ready(r.spec.tasks, states) {
		if !looked[id] && !busy[r.byID[id].Repo] && len(r.withoutMR(id)) == 0 {
			return id
		}
	}
	return ""
}

// withoutMR are the tasks id depends on that are done and have no merge request: their branch may
// not be on the remote, so a merge request stacked on it cannot be opened (specs/delivery/SPEC.md
// §9). A task counts when its run completed with a commit and its push reported no merge request;
// one the operator marked done by committing in the kept worktree has not been pushed by anyone,
// and is the operator's to open.
func (r *storyRunner) withoutMR(id string) []string {
	var held []string
	for _, dep := range r.byID[id].DependsOn {
		rt := r.rec.Tasks[dep]
		if rt.Status != story.TaskDone || rt.MR != nil {
			continue
		}
		if run, err := r.loadRun(rt); err == nil && run.Commit != "" && (run.MR == nil || run.MR.State != domain.MRCreated) {
			held = append(held, dep)
		}
	}
	return held
}

// missingMR are the done tasks, in the story's order, that have no merge request and that a task
// not done yet depends on.
func (r *storyRunner) missingMR() []string {
	var missing []string
	for _, id := range r.order {
		if r.rec.Tasks[id].Status == story.TaskDone {
			continue
		}
		for _, dep := range r.withoutMR(id) {
			if !slices.Contains(missing, dep) {
				missing = append(missing, dep)
			}
		}
	}
	slices.SortFunc(missing, func(a, b string) int { return slices.Index(r.order, a) - slices.Index(r.order, b) })
	return missing
}

// hintMissingMR says which done tasks hold back the tasks that depend on them for want of a merge
// request, and how to go on.
func (r *storyRunner) hintMissingMR(missing []string) {
	for _, id := range missing {
		rt := r.rec.Tasks[id]
		var waiting []string
		for _, other := range r.order {
			if r.rec.Tasks[other].Status != story.TaskDone && slices.Contains(r.withoutMR(other), id) {
				waiting = append(waiting, other)
			}
		}
		r.reporter.StoryHint(
			fmt.Sprintf("blocked: %s has no merge request, so %s was not started.", id, strings.Join(waiting, ", ")),
			fmt.Sprintf("Why: its child ended with exit code 6 - the branch %s (commit %s) is committed, but its push did not open a merge request; "+
				"see mr in run %s.", rt.Branch, shortSHA(rt.Commit), rt.RunID),
			fmt.Sprintf("Fix: push %s and open its merge request to its target by hand, then record it in %s: tasks.%s.mr {\"iid\": <n>, \"url\": \"<url>\"}, and run the story again.",
				rt.Branch, filepath.Join(r.loc.storyDir, persistence.StoryRecordFile), id))
	}
}

// fill starts ready tasks while a slot is free and nothing asks the loop to drain. A blocked task
// is looked at again, a failed or stopped one with a run is resumed, anything else is dispatched.
// A conflict task is dispatched too: its parents are merged again on the spec branch, which the
// operator may have merged by hand, and it goes from the tip if they are all in it.
func (r *storyRunner) fill(lp *dispatchLoop) {
	for !lp.draining() && len(lp.running) < lp.slots {
		id := r.nextReady(lp.looked, lp.running)
		if id == "" {
			return
		}
		if r.ctx.Err() != nil {
			lp.stoppedBefore = id
			return
		}
		pos := slices.Index(r.order, id)
		rt := r.rec.Tasks[id]
		if rt.Status == story.TaskBlocked {
			status, err := r.settleBlocked(id)
			lp.ended(id, status, err)
			continue
		}
		if err := r.checkBudgetAt(r.liveCostUSD(lp)); err != nil {
			lp.budgetErr = err
			return
		}
		job, status, err := r.prepare(pos, id, lp.slots > 1)
		if err != nil || job == nil {
			lp.ended(id, status, err)
			continue
		}
		r.launch(lp, job)
	}
}

// prepare gets a task ready to run: a failed or stopped one with a run is resumed, anything else
// is dispatched. A nil job is a task that cannot start (its parents conflict): its status is
// returned.
func (r *storyRunner) prepare(pos int, id string, logged bool) (*childJob, story.TaskStatus, error) {
	rt := r.rec.Tasks[id]
	if (rt.Status == story.TaskFailed || rt.Status == story.TaskStopped) && rt.RunID != "" {
		return r.prepareResume(pos, id, logged)
	}
	return r.prepareDispatch(pos, id, logged)
}

// launch starts the child of a job in a goroutine that only runs the process. With one slot the
// child has the terminal, under a header; with more it has no stdin and writes to its log, and
// the console gets one line.
func (r *storyRunner) launch(lp *dispatchLoop, job *childJob) {
	stdin, stdout, stderr := r.stdin, r.stdout, r.stderr
	if lp.slots > 1 {
		stdin, stdout, stderr = nil, job.log, job.log
		r.reporter.StoryTaskStarted(job.id, job.repo, job.pos+1, len(r.order), job.logPath)
	} else {
		r.reporter.StoryTaskHeader(job.id, job.repo, job.pos+1, len(r.order))
		if job.resume != "" {
			r.reporter.StoryResume(job.id, job.resume)
		}
	}
	lp.running[job.id] = job
	go func() {
		exit, err := runChild(r.ctx, r.exe, job.dir, job.args, stdin, stdout, stderr)
		lp.ends <- childEnd{id: job.id, exit: exit, err: err}
	}()
}

// settleEnd settles a task whose child ended.
func (r *storyRunner) settleEnd(lp *dispatchLoop, end childEnd) {
	job := lp.running[end.id]
	delete(lp.running, end.id)
	if job.log != nil {
		job.log.Close()
	}
	if end.err != nil {
		fmt.Fprintf(r.stderr, "Error: cannot run %s in %s: %v\n", job.args[len(job.args)-1], job.dir, end.err)
	}
	status, err := r.settle(end.id, end.exit)
	lp.ended(end.id, status, err)
}

// conclude ends the story once nothing runs. An error comes first; then the tasks that did not
// end done, the first of them in the story's order giving the story its status; then the budget,
// and a Ctrl-C before a task was started.
func (r *storyRunner) conclude(lp *dispatchLoop) error {
	switch {
	case lp.err != nil:
		return lp.err
	case len(lp.notDone) > 0:
		slices.SortFunc(lp.notDone, func(a, b string) int { return slices.Index(r.order, a) - slices.Index(r.order, b) })
		err := r.stopAll(lp.notDone)
		r.hintMissingMR(r.missingMR())
		return err
	case lp.budgetErr != nil:
		r.halt(story.StatusStopped)
		return lp.budgetErr
	case lp.stoppedBefore != "":
		r.halt(story.StatusStopped)
		return fmt.Errorf("story %s stopped before %s: %w", r.loc.slug, lp.stoppedBefore, r.ctx.Err())
	}
	if missing := r.missingMR(); len(missing) > 0 {
		r.halt(story.StatusBlocked)
		r.hintMissingMR(missing)
		return &apperr.WorkflowError{Msg: fmt.Sprintf("story %s is blocked: %s has no merge request", r.loc.slug, strings.Join(missing, ", "))}
	}
	for _, id := range r.order {
		if r.rec.Tasks[id].Status != story.TaskDone {
			r.halt(story.StatusFailed)
			return &apperr.WorkflowError{Msg: fmt.Sprintf("story %s: %s is %s and nothing else is ready", r.loc.slug, id, orPending(r.rec.Tasks[id].Status))}
		}
	}
	return r.finish()
}

// liveCostUSD is the cost the story has now: the recorded total, with each running task's recorded
// cost replaced by what its run has cost so far, from the executions in its run.json (the run is
// found as settle finds it). A run that is not there yet, or cannot be read, costs what is
// recorded. The unreadable directories are not reported: a run being written is a sibling at work.
// A run another process holds locked is not skipped here, unlike in settle: the running children's
// own runs are locked, and their cost so far is what this adds.
func (r *storyRunner) liveCostUSD(lp *dispatchLoop) float64 {
	total := r.rec.TotalCostUSD()
	if r.cfg.Story.MaxCostUSD == nil {
		return total
	}
	foundBy := map[string][]persistence.FoundRun{}
	for _, id := range r.order {
		if lp.running[id] == nil {
			continue
		}
		rt := r.rec.Tasks[id]
		found, ok := foundBy[rt.Repo]
		if !ok {
			found, _, _ = persistence.LoadRunDirs(filepath.Join(r.rec.Services[rt.Repo], runsBaseDir))
			foundBy[rt.Repo] = found
		}
		if run := pickStoryRun(found, r.loc.slug, id, rt.Attempt, rt.RunID); run != nil {
			total += max(executionsCostUSD(run), rt.CostUSD) - rt.CostUSD
		}
	}
	return total
}

// executionsCostUSD is what the steps of a run have cost so far.
func executionsCostUSD(run *domain.Run) float64 {
	spent := 0.0
	for _, ex := range run.Executions {
		if ex.Usage != nil {
			spent += ex.Usage.CostUSD
		}
	}
	return spent
}

// checkLiveRuns stops the story run, before anything else, when a task the record has as running
// has its run directory locked by a live process: a child of an earlier story run that is still
// working. The other running tasks are settled by reconcile.
func (r *storyRunner) checkLiveRuns() error {
	for _, id := range r.order {
		rt := r.rec.Tasks[id]
		if rt.Status != story.TaskRunning || r.rec.Services[rt.Repo] == "" {
			continue
		}
		found, _, _ := persistence.LoadRunDirs(filepath.Join(r.rec.Services[rt.Repo], runsBaseDir))
		run := pickStoryRun(found, r.loc.slug, id, rt.Attempt, rt.RunID)
		if run == nil {
			continue
		}
		if dir := foundRunDir(found, run); dir != "" && persistence.DirLocked(dir) {
			return &apperr.ConfigError{Msg: fmt.Sprintf("%s: run %s is still running (its run directory is locked by another process); "+
				"wait for it to end, then run the story again", id, run.ID)}
		}
	}
	return nil
}

// foundRunDir is the directory of a run pickStoryRun took from found, "" if it is not there.
func foundRunDir(found []persistence.FoundRun, run *domain.Run) string {
	for i := range found {
		if &found[i].Run == run {
			return found[i].Dir
		}
	}
	return ""
}

// pickSettledRun is pickStoryRun without the runs another process holds locked: such a run is a
// sibling in progress, and what is read of it is not an end.
func pickSettledRun(found []persistence.FoundRun, slug, id string, attempt int, runID string) *domain.Run {
	found = slices.Clone(found)
	for {
		run := pickStoryRun(found, slug, id, attempt, runID)
		if run == nil {
			return nil
		}
		dir := foundRunDir(found, run)
		if dir == "" || !persistence.DirLocked(dir) {
			return run
		}
		found = slices.DeleteFunc(found, func(f persistence.FoundRun) bool { return f.Dir == dir })
	}
}

// openLog opens the log of a task's attempt for append (the file is kept when a run is resumed).
func (r *storyRunner) openLog(id string, attempt int) (*os.File, string, error) {
	dir := filepath.Join(r.loc.storyDir, "logs")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return nil, "", &apperr.ConfigError{Msg: fmt.Sprintf("%s: cannot create %s", id, dir), Err: err}
	}
	path := filepath.Join(dir, fmt.Sprintf("task-%s.%d.log", id, attempt))
	f, err := os.OpenFile(path, os.O_WRONLY|os.O_APPEND|os.O_CREATE, 0o600)
	if err != nil {
		return nil, "", &apperr.ConfigError{Msg: fmt.Sprintf("%s: cannot open the log %s", id, path), Err: err}
	}
	return f, path, nil
}

// prepareDispatch writes the task's input and freezes its file (§10.2); the child is started by
// the loop. The log is opened when logged.
func (r *storyRunner) prepareDispatch(pos int, id string, logged bool) (*childJob, story.TaskStatus, error) {
	t := r.byID[id]
	rt := r.rec.Tasks[id]
	attempt := max(rt.Attempt, 1)
	// The fallback: the child names its branch by its repository's rules, and uses this only when
	// they give none (specs/delivery/SPEC.md §9.1).
	fallback := persistence.StoryTaskBranch(r.loc.slug, id, attempt)

	// No parent: the repository's story base. One: that task's commit as it was recorded - not
	// its branch's tip now, which an operator's commit may have moved. Several: the spec branch's
	// tip after their commits are merged on it.
	baseRef, conflict, err := r.startRef(id)
	if err != nil {
		if r.ctx.Err() != nil {
			r.halt(story.StatusStopped)
		} else {
			r.halt(story.StatusFailed)
		}
		return nil, "", err
	}
	if conflict != nil {
		rt.Status, rt.Repo, rt.Conflict = story.TaskConflict, t.Repo, conflict
		r.rec.Tasks[id] = rt
		return nil, story.TaskConflict, r.persist()
	}

	// The merge request of a task with several parents targets the spec branch, which holds their
	// merge: it is on the remote before the child that needs it is dispatched (§9).
	if parents, _ := story.Parents(r.spec.tasks, id); len(parents) > 1 {
		if err := r.pushSpecBranch(t.Repo); err != nil {
			return nil, "", err
		}
	}

	var notes []story.DepNote
	for _, depID := range t.DependsOn {
		notes = append(notes, story.DepNote{ID: depID, Repo: r.byID[depID].Repo, AsBuilt: story.AsBuiltText(r.spec.data[depID])})
	}
	text, err := story.RenderInputText(r.spec.title, pos+1, len(r.order), t, r.spec.approach, r.spec.contracts, notes)
	if err != nil {
		return nil, "", &apperr.ConfigError{Msg: err.Error()}
	}
	acceptance := t.Acceptance
	if acceptance == nil {
		acceptance = []string{}
	}
	jira := jiraKey(t, rt)
	file, err := persistence.WriteStoryTaskInput(r.loc.storyDir, story.TaskInput{
		Story: r.loc.slug, Task: id, Attempt: attempt, Title: story.InputTitle(t.Title), Branch: fallback, BaseRef: baseRef,
		// The merge request's target, and the branch the child's conflict check merges against.
		BaseBranch: r.targetBranch(id), Jira: jira, Acceptance: acceptance, Text: text,
	})
	if err != nil {
		return nil, "", &apperr.ConfigError{Msg: fmt.Sprintf("%s: %v", id, err)}
	}
	job := &childJob{
		id: id, pos: pos, repo: t.Repo, dir: r.rec.Services[t.Repo],
		args: r.childArgs("run", "--mode", "auto", "--story-task", file),
	}
	if logged {
		if job.log, job.logPath, err = r.openLog(id, attempt); err != nil {
			return nil, "", err
		}
	}

	// Recorded before the child starts, so a story run that dies meanwhile finds the task running.
	// Its branch is not known yet: the child's run records it (§9.1).
	r.rec.Tasks[id] = persistence.StoryTask{
		Status: story.TaskRunning, FileSHA256: story.FrozenHash(r.spec.data[id]), Attempt: attempt, Repo: t.Repo,
		BaseRef: baseRef, Jira: jira, JiraIntent: rt.JiraIntent, AbandonedCostUSD: rt.AbandonedCostUSD,
	}
	r.rec.Status = story.StatusRunning
	if err := r.persist(); err != nil {
		job.closeLog()
		return nil, "", err
	}
	return job, "", nil
}

// closeLog closes the job's log, if it has one.
func (j *childJob) closeLog() {
	if j.log != nil {
		j.log.Close()
	}
}

// prepareResume continues the run of a failed or stopped task (`resume <id> --mode auto` in its
// repository); no new run is started.
func (r *storyRunner) prepareResume(pos int, id string, logged bool) (*childJob, story.TaskStatus, error) {
	rt := r.rec.Tasks[id]
	run, err := r.loadRun(rt)
	if err != nil {
		return nil, "", &apperr.ConfigError{Msg: fmt.Sprintf("%s: cannot resume run %s of its repository", id, rt.RunID), Err: err}
	}
	if src := run.Task.Source; src == nil || src.Kind != domain.TaskSourceStory || src.Story != r.loc.slug || src.Task != id || src.Attempt != rt.Attempt {
		return nil, "", &apperr.ConfigError{Msg: fmt.Sprintf("%s: run %s is not a run of this task's attempt %d", id, rt.RunID, rt.Attempt)}
	}
	job := &childJob{
		id: id, pos: pos, repo: rt.Repo, dir: r.rec.Services[rt.Repo], resume: rt.RunID,
		args: r.childArgs("resume", rt.RunID, "--mode", "auto"),
	}
	if logged {
		if job.log, job.logPath, err = r.openLog(id, rt.Attempt); err != nil {
			return nil, "", err
		}
	}
	rt.Status = story.TaskRunning
	r.rec.Tasks[id] = rt
	r.rec.Status = story.StatusRunning
	if err := r.persist(); err != nil {
		job.closeLog()
		return nil, "", err
	}
	return job, "", nil
}

// settleBlocked counts a blocked task done once its branch has a commit beyond its base: the
// operator committed in the kept worktree.
func (r *storyRunner) settleBlocked(id string) (story.TaskStatus, error) {
	rt := r.rec.Tasks[id]
	if rt.Branch == "" {
		return story.TaskBlocked, nil
	}
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
		// A run another process holds locked is a sibling in progress, not a damaged run.
		if persistence.DirLocked(b.Dir) {
			continue
		}
		fmt.Fprintf(r.stderr, "Warning: %s is not a readable run and is ignored: %v\n", b.Dir, b.Err)
	}
	run := pickSettledRun(found, r.loc.slug, id, rt.Attempt, rt.RunID)

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
	readBranch(&rt, run)
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

// readBranch copies into the task the branch its run's worktree was on, which the child's
// repository may have named by its rules (specs/delivery/SPEC.md §9.1): the one place the name
// becomes known to the story. A run that has no worktree yet leaves the task as it is.
func readBranch(rt *persistence.StoryTask, run *domain.Run) {
	if w := run.Worktree; w != nil && w.Branch != "" {
		rt.Branch = w.Branch
	}
}

// readMR copies into the task the merge request its run's push opened; none when the push opened
// none (the task is then held back from the tasks that depend on it, withoutMR).
func readMR(rt *persistence.StoryTask, run *domain.Run) {
	rt.MR = nil
	if m := run.MR; m != nil && m.State == domain.MRCreated {
		rt.MR = &persistence.StoryMR{IID: m.IID, URL: m.URL}
	}
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
	readBranch(&rt, run)
	readMR(&rt, run)
	// The child's merge request targets its run's base_branch, which the input took from the
	// story's target: what the end table prints.
	if w := run.Worktree; w != nil && w.BaseBranch != r.targetBranch(id) {
		fmt.Fprintf(r.stderr, "Warning: %s: its run targeted %s, but the story's target for it is %s\n", id, w.BaseBranch, r.targetBranch(id))
	}
	if err := r.writeAsBuilt(id, rt, run); err != nil {
		return "", err
	}
	r.rec.Tasks[id] = rt
	if err := r.persist(); err != nil {
		return "", err
	}
	detail := "commit " + shortSHA(commit)
	if rt.MR != nil {
		detail += ", " + logging.StoryMRLabel(rt.MR.IID, rt.MR.URL)
	}
	r.reporter.StoryTaskResult(id, string(story.TaskDone), detail)
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
	if label := r.startLabel(id); label != "" {
		stackedOn = label
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

// stopAll ends the story at the tasks that did not get to done, given in the story's order: the
// story takes the status of the first, the operator is told what to do next for each of them,
// and the error is the command's.
func (r *storyRunner) stopAll(ids []string) error {
	id := ids[0]
	status := r.rec.Tasks[id].Status
	storyStatus := story.StatusFailed
	switch status {
	case story.TaskBlocked, story.TaskConflict:
		storyStatus = story.StatusBlocked
	case story.TaskStopped:
		storyStatus = story.StatusStopped
	}
	r.halt(storyStatus)

	for _, notDone := range ids {
		r.hints(notDone)
	}
	if status == story.TaskStopped && r.ctx.Err() != nil {
		return fmt.Errorf("story %s stopped at %s: %w", r.loc.slug, id, r.ctx.Err())
	}
	return &apperr.WorkflowError{Msg: fmt.Sprintf("story %s is %s: %s is %s", r.loc.slug, storyStatus, id, status)}
}

// hints tells the operator what to do next for a task that did not get to done.
func (r *storyRunner) hints(id string) {
	rt := r.rec.Tasks[id]
	status := rt.Status
	dir := logging.ShellWord(r.dirArg)
	next := persistence.StoryTaskBranch(r.loc.slug, id, rt.Attempt+1)
	switch {
	case status == story.TaskConflict:
		r.reporter.StoryConflict(id, rt.Conflict.Parents, rt.Conflict.Files, persistence.StorySpecBranch(r.loc.slug), r.conflictHints(rt)...)
	case status == story.TaskBlocked:
		r.reporter.StoryHint(r.blockedHints(id, rt, dir, next)...)
	case rt.RunID != "":
		r.reporter.StoryHint(append(r.runFailureHints(id, rt),
			fmt.Sprintf("Fix the cause, then: orchestrator story run %s - resumes run %s from the step that did not finish (a timeout, a cancel, a fixed config or environment)", dir, rt.RunID),
			fmt.Sprintf("Or, when the task itself was wrong or too big: edit %s, then: orchestrator story run %s --retry %s - abandons the run and starts the task again on %s", r.spec.files[id], dir, id, next))...)
	default:
		where := "the output above"
		if p := filepath.Join(r.loc.storyDir, "logs", fmt.Sprintf("task-%s.%d.log", id, rt.Attempt)); fileExists(p) {
			where = p
		}
		r.reporter.StoryHint(
			"Why: the task's child run stopped before it created a run: its checks before any model call refused it (the config, a missing or unverified CLI, a dirty or red repository, the task's input).",
			"Where: the error is in "+where+".",
			fmt.Sprintf("Fix that, then: orchestrator story run %s - dispatches %s again", dir, id))
	}
}

// runFailureHints say how a task's run ended: its status, the step that failed, the reason the
// run recorded (which names its own fixes), and where the summary and the step's logs are.
func (r *storyRunner) runFailureHints(id string, rt persistence.StoryTask) []string {
	run, err := r.loadRun(rt)
	if err != nil {
		return []string{fmt.Sprintf("Why: run %s of %s cannot be read: %v", rt.RunID, id, err)}
	}
	store := persistence.NewStore(filepath.Join(r.rec.Services[rt.Repo], runsBaseDir))
	why := fmt.Sprintf("Why: run %s is %s", run.ID, run.Status)
	var failed *domain.StepExecution
	for i := len(run.Executions) - 1; i >= 0; i-- {
		if run.Executions[i].Status == domain.StepFailed {
			failed = &run.Executions[i]
			break
		}
	}
	if failed != nil {
		why += fmt.Sprintf(" at step %d (%s)", failed.Index, failed.Action)
	}
	reason := run.FailureReason
	if reason == "" && failed != nil {
		reason = failed.Error
	}
	if reason == "" {
		reason = "no reason was recorded: the child was stopped or its process died"
	}
	hints := []string{why + ":"}
	for _, l := range strings.Split(strings.TrimSpace(reason), "\n") {
		hints = append(hints, "  "+l)
	}
	where := "Details: " + filepath.Join(store.RunDir(run.ID), "summary.md")
	if failed != nil {
		where += ", the step's output: " + store.LogPath(run.ID, failed.Index, failed.Action, "stderr") + " (and .stdout.log)"
	}
	return append(hints, where)
}

func fileExists(path string) bool {
	_, err := os.Stat(path)
	return err == nil
}

// blockedHints says why a blocked task's run made no commit and what each way on is, read from
// the run: an empty change and a change held back want different steps.
func (r *storyRunner) blockedHints(id string, rt persistence.StoryTask, dir, next string) []string {
	again := "then: orchestrator story run " + dir + " (a task whose branch has a commit beyond its base counts as done)"
	retry := fmt.Sprintf("orchestrator story run %s --retry %s (a new run on %s)", dir, id, next)
	run, err := r.loadRun(rt)
	if err != nil {
		return []string{
			fmt.Sprintf("Why: its run %s made no commit on %s (the run cannot be read: %v).", rt.RunID, rt.Branch, err),
			"Fix: commit the work on " + rt.Branch + ", " + again,
			"Or run the task again: " + retry,
		}
	}
	repoDir := logging.ShellWord(r.rec.Services[rt.Repo])
	wt := ""
	if w := run.Worktree; w != nil && !w.Removed && w.Path != "" && fileExists(w.Path) {
		wt = logging.ShellWord(w.Path)
	}

	if o := run.Outcome; o == nil || o.Diff == nil || o.Diff.Files == 0 {
		why := "Why: the run completed, but implement changed no file, so there was nothing to commit."
		store := persistence.NewStore(filepath.Join(r.rec.Services[rt.Repo], runsBaseDir))
		if s := implementSummary(store, run); s != "" {
			why += fmt.Sprintf(" Its summary: %q.", s)
		}
		mark := fmt.Sprintf("git -C %s branch -f %s \"$(git -C %s commit-tree -p %s -m '%s: nothing to change' %s^{tree})\"",
			repoDir, rt.Branch, repoDir, rt.Branch, id, rt.Branch)
		if wt != "" {
			mark = fmt.Sprintf("git -C %s commit --allow-empty -m '%s: nothing to change'", wt, id)
		}
		return []string{
			why,
			"If the work is already in the code base, mark the task done with an empty commit: " + mark + ", " + again,
			"If the task still needs work, make its file " + r.spec.files[id] + " say what is missing, and: " + retry,
		}
	}

	var reasons []string
	if run.Outcome.ReviewVerdict == domain.ReviewFail {
		reasons = append(reasons, "the review failed")
	}
	if run.Outcome.ApprovalRequired {
		reasons = append(reasons, "a policy.paths rule with approval: manual matched the change")
	}
	if n := len(run.SecretFindings); n > 0 {
		reasons = append(reasons, fmt.Sprintf("the secret scan reported %d new finding(s)", n))
	}
	if len(reasons) == 0 {
		reasons = append(reasons, "the implementation gave no commit message (summary_short)")
	}
	hints := []string{fmt.Sprintf("Why: the run changed %d file(s) but did not commit them: %s.", run.Outcome.Diff.Files, strings.Join(reasons, "; "))}
	if wt != "" {
		hints = append(hints, fmt.Sprintf("Fix: check the change (git -C %s diff HEAD), commit it: git -C %s add -A && git -C %s commit, %s", wt, wt, wt, again))
	} else {
		hints = append(hints, "Its worktree is gone, and the uncommitted change with it.")
	}
	return append(hints, "Or drop the change and run the task again: "+retry)
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
		row := logging.StoryEndRow{ID: id, Repo: rt.Repo, Branch: rt.Branch, Target: r.targetBranch(id), Commit: rt.Commit, Jira: rt.Jira}
		if rt.MR != nil {
			row.MR = logging.StoryMRLabel(rt.MR.IID, rt.MR.URL)
		}
		if run, err := r.loadRun(rt); err == nil && run.Worktree != nil {
			row.Conflicts = strings.Join(run.Worktree.Conflicts, ", ")
		}
		rows = append(rows, row)
	}
	r.reporter.StoryEnd(rows)
	return nil
}

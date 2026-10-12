// Package validate is `orchbench validate` (specs/benchmark/SPEC.md §6, §13.4): it proves each
// task is a working oracle before money is spent on it. It calls no model; it runs git,
// setup_command, test_command and oracle.command against the task's two commits, and records
// a valid task's file hash in validated.json.
package validate

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"orchbench/internal/cache"
	"orchbench/internal/check"
	"orchbench/internal/lang"
	"orchbench/internal/task"
	"orchbench/internal/workspace"
)

// The checks, by the name an Outcome reports, in the order they run.
const (
	CheckDraft      = "draft"
	CheckSchema     = "schema"
	CheckCommits    = "commits"
	CheckSetup      = "setup"
	CheckClean      = "clean"
	CheckTestsBase  = "tests-base"
	CheckOracleBase = "oracle-base"
	CheckOracleFix  = "oracle-fix"
	CheckTestsFix   = "tests-fix"
	CheckLeak       = "leak"
	CheckAnswer     = "answer"
	// CheckError is a failure of orchbench or the machine (git, bwrap, the disk), not of the
	// task. The task is still not valid.
	CheckError = "error"
)

// oracleRuns is how often the oracle must be green at fix_commit: a flaky one is rejected.
const oracleRuns = 3

// Options configure a validation.
type Options struct {
	Cache cache.Dir
	// Jobs is how many tasks are checked in parallel; less than 1 is 1.
	Jobs int
	// KeepWorkspaces leaves every workspace in place; their paths are in the Outcome.
	KeepWorkspaces bool
	// Verbose, when set, receives every command and its output (-v).
	Verbose io.Writer
	// Now is the clock of validated.json; nil is time.Now.
	Now func() time.Time
}

// Outcome is one task's result.
type Outcome struct {
	ID   string
	Path string
	OK   bool
	// Check and Hint say which check failed and what to do; empty when OK.
	Check string
	Hint  string
	// Detail is the summary of an OK task: timings and what was proven.
	Detail string
	// Log is the log a failed command wrote, if any.
	Log string
	// SHA256 is the hash of the task file as it was read for the check: the one recorded.
	SHA256     string
	Workspaces []string
	// Interrupted is set when ctx ended during the task: nothing is recorded for it.
	Interrupted bool
}

// Line is the output line of the outcome, the id padded to width.
func (o Outcome) Line(width int) string {
	if o.OK {
		line := fmt.Sprintf("%-*s  ok    %s", width, o.ID, o.Detail)
		return line + o.kept()
	}
	line := fmt.Sprintf("%-*s  FAIL  %s", width, o.ID, o.Hint)
	if o.Log != "" {
		line += " (log: " + o.Log + ")"
	}
	return line + o.kept()
}

func (o Outcome) kept() string {
	if len(o.Workspaces) == 0 {
		return ""
	}
	return " [kept: " + strings.Join(o.Workspaces, ", ") + "]"
}

// Run validates the task files, Jobs at a time, writes one line per task to out in the order
// of paths as soon as it and every one before it are done, then the count. A valid task's hash
// is recorded in validated.json, an invalid one's entry removed. It returns the outcomes; the
// error is ctx's when it ended, or one writing validated.json.
func Run(ctx context.Context, paths []string, opts Options, out io.Writer) ([]Outcome, error) {
	if opts.Now == nil {
		opts.Now = time.Now
	}
	jobs := max(opts.Jobs, 1)
	m := workspace.New(opts.Cache, opts.Verbose)
	store := cache.OpenValidated(opts.Cache)

	width := 0
	for _, p := range paths {
		width = max(width, len(fileID(p)))
	}

	outcomes := make([]Outcome, len(paths))
	done := make([]chan struct{}, len(paths))
	for i := range done {
		done[i] = make(chan struct{})
	}

	var (
		mu       sync.Mutex
		storeErr error
	)
	next := make(chan int)
	var wg sync.WaitGroup
	for range jobs {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for i := range next {
				o := CheckTask(ctx, m, paths[i], opts)
				if err := record(store, o, opts.Now()); err != nil {
					mu.Lock()
					storeErr = errors.Join(storeErr, err)
					mu.Unlock()
				}
				outcomes[i] = o
				close(done[i])
			}
		}()
	}
	go func() {
		defer close(next)
		for i := range paths {
			select {
			case next <- i:
			case <-ctx.Done():
				return
			}
		}
	}()

	valid := 0
	for i := range paths {
		select {
		case <-done[i]:
		case <-ctx.Done():
			wg.Wait()
			return outcomes, ctx.Err()
		}
		if outcomes[i].Interrupted {
			wg.Wait()
			return outcomes, ctx.Err()
		}
		fmt.Fprintln(out, outcomes[i].Line(width))
		if outcomes[i].OK {
			valid++
		}
	}
	wg.Wait()
	fmt.Fprintf(out, "%d of %d valid\n", valid, len(paths))
	return outcomes, storeErr
}

// record remembers a valid task's hash and forgets an invalid one. The hash is of the file
// as it was read at the start of the check, so a file edited during validation is unvalidated
// afterwards.
func record(store *cache.Validated, o Outcome, now time.Time) error {
	if o.Interrupted || o.ID == "" {
		return nil
	}
	if !o.OK {
		return store.Forget(o.ID)
	}
	return store.Record(o.ID, o.SHA256, now)
}

func fileID(p string) string {
	name := filepath.Base(p)
	return strings.TrimSuffix(name, filepath.Ext(name))
}

// checker runs the checks of one task.
type checker struct {
	m    *workspace.Manager
	opts Options
	t    task.Task // as loaded, with the language's defaults filled
	logs string    // <cache>/logs/validate/<id>
	o    *Outcome
	// created are the workspaces made so far; all are removed at the end unless kept.
	created []*workspace.Workspace
}

// fail ends the task at a check.
type failure struct {
	check, hint, log string
}

func (f *failure) Error() string { return f.check + ": " + f.hint }

func fail(check, format string, args ...any) error {
	return &failure{check: check, hint: fmt.Sprintf(format, args...)}
}

func failLog(check, log, format string, args ...any) error {
	return &failure{check: check, hint: fmt.Sprintf(format, args...), log: log}
}

// CheckTask runs the checks of §6 on one task file in order and stops at the first failure.
func CheckTask(ctx context.Context, m *workspace.Manager, path string, opts Options) Outcome {
	o := Outcome{ID: fileID(path), Path: path}
	err := checkTask(ctx, m, path, opts, &o)
	if ctx.Err() != nil {
		o.Interrupted = true
		return o
	}

	var f *failure
	switch {
	case err == nil:
		o.OK = true
	case errors.As(err, &f):
		o.Check, o.Hint, o.Log = f.check, oneLine(f.hint), f.log
	default:
		o.Check, o.Hint = CheckError, "error: "+oneLine(err.Error())
	}
	return o
}

// oneLine joins a multi-line message (a YAML error, git's stderr) so that every task stays one
// output line.
func oneLine(s string) string {
	lines := strings.Split(strings.TrimSpace(s), "\n")
	for i := range lines {
		lines[i] = strings.TrimSpace(lines[i])
	}
	return strings.Join(lines, " ")
}

func checkTask(ctx context.Context, m *workspace.Manager, path string, opts Options, o *Outcome) error {
	hash, err := task.SHA256(path)
	if err != nil {
		return fail(CheckSchema, "%v", err)
	}
	o.SHA256 = hash
	t, err := task.Load(path)
	if err != nil {
		return fail(CheckSchema, "%v", err)
	}
	if t.ID != "" {
		o.ID = t.ID
	}
	if t.IsDraft() {
		return fail(CheckDraft, "draft: rewrite the task and remove `draft`")
	}
	if err := lang.Validate(t); err != nil {
		return fail(CheckSchema, "%v", err)
	}
	if err := lang.Resolve(&t); err != nil {
		return fail(CheckSchema, "%v", err)
	}

	c := &checker{m: m, opts: opts, t: t, o: o, logs: filepath.Join(opts.Cache.Logs(), "validate", t.ID)}
	if err := os.RemoveAll(c.logs); err != nil {
		return err
	}
	if err := os.MkdirAll(c.logs, 0o755); err != nil {
		return err
	}

	defer c.removeAll()
	return c.run(ctx)
}

func (c *checker) run(ctx context.Context) error {
	t := c.t

	mirror, err := c.m.Mirror(ctx, t.Repo)
	if err != nil {
		return fail(CheckCommits, "cannot mirror %s: %v", t.Repo, err)
	}
	if err := c.commits(ctx, mirror); err != nil {
		return err
	}

	base, err := c.workspace(ctx, t.BaseCommit)
	if err != nil {
		return err
	}
	setupTime, err := c.setup(ctx, base, "setup-base.log")
	if err != nil {
		return err
	}
	if err := c.green(ctx, base, t.TestCommand, "suite-base.log", CheckTestsBase,
		"test_command red at base_commit: the pre-run test would refuse"); err != nil {
		return err
	}

	if t.Category == task.CategoryQuestion {
		c.done(base)
		if err := c.answerChecks(); err != nil {
			return err
		}
		c.o.Detail = fmt.Sprintf("setup %s, tests green, answer checks match", short(setupTime))
		return nil
	}

	if err := c.oracleAtBase(ctx, base); err != nil {
		return err
	}
	c.done(base)

	fix, err := c.workspace(ctx, t.FixCommit)
	if err != nil {
		return err
	}
	if _, err := c.setup(ctx, fix, "setup-fix.log"); err != nil {
		return err
	}
	for k := 1; k <= oracleRuns; k++ {
		if err := c.green(ctx, fix, t.Oracle.Command, fmt.Sprintf("oracle-fix-%d.log", k), CheckOracleFix,
			fmt.Sprintf("oracle red at fix_commit (run %d of %d): flaky or wrong oracle.command", k, oracleRuns)); err != nil {
			return err
		}
	}
	if err := c.green(ctx, fix, t.TestCommand, "suite-fix.log", CheckTestsFix,
		"test_command red at fix_commit: the reference solution breaks the suite"); err != nil {
		return err
	}
	c.done(fix)

	if err := c.leak(ctx, mirror); err != nil {
		return err
	}
	c.o.Detail = fmt.Sprintf("setup %s, tests green, oracle red→green (%d/%d)", short(setupTime), oracleRuns, oracleRuns)
	return nil
}

// commits is §6 step 1: both commits exist, base_commit is an ancestor of fix_commit.
func (c *checker) commits(ctx context.Context, mirror string) error {
	t := c.t
	err := c.m.EnsureCommits(ctx, mirror, t.BaseCommit, t.FixCommit)
	var missing workspace.ErrMissingCommit
	if errors.As(err, &missing) {
		name := "base_commit"
		if missing.Commit == t.FixCommit {
			name = "fix_commit"
		}
		return fail(CheckCommits, "%s %s does not exist in %s", name, missing.Commit, t.Repo)
	}
	if err != nil {
		return err
	}

	_, err = c.m.Git(ctx, mirror, "merge-base", "--is-ancestor", t.BaseCommit, t.FixCommit)
	if code, ok := workspace.ExitCode(err); ok && code == 1 {
		return fail(CheckCommits, "base_commit is not an ancestor of fix_commit")
	}
	return err
}

func (c *checker) workspace(ctx context.Context, commit string) (*workspace.Workspace, error) {
	ws, err := c.m.Create(ctx, c.t, commit)
	if err != nil {
		return nil, err
	}
	c.created = append(c.created, ws)
	if c.opts.KeepWorkspaces {
		c.o.Workspaces = append(c.o.Workspaces, ws.Dir)
	}
	return ws, nil
}

// done removes a workspace that is no longer needed, unless it is kept, so that a task never
// holds two installed trees at once.
func (c *checker) done(ws *workspace.Workspace) {
	if !c.opts.KeepWorkspaces {
		ws.Remove()
	}
}

// removeAll removes every workspace of the task, unless they are kept. Removing one twice is
// harmless.
func (c *checker) removeAll() {
	for _, ws := range c.created {
		c.done(ws)
	}
}

// setup is §6 step 2: setup_command exits 0, with network, and leaves the tree clean.
func (c *checker) setup(ctx context.Context, ws *workspace.Workspace, log string) (time.Duration, error) {
	logPath := filepath.Join(c.logs, log)
	start := time.Now()
	exit, err := check.Sandboxed(ctx, ws.Dir, c.t, c.t.SetupCommand, true, logPath, c.m.Log)
	if err != nil {
		return 0, err
	}
	elapsed := time.Since(start)
	if exit != 0 {
		return 0, failLog(CheckSetup, logPath, "setup_command %s", exited(exit))
	}

	status, err := ws.Status(ctx)
	if err != nil {
		return 0, err
	}
	if status != "" {
		return 0, fail(CheckClean, "setup_command left the tree dirty (%s): dependencies must land in ignored or bound paths",
			firstLines(status, 3))
	}
	return elapsed, nil
}

// green runs a command sandboxed without network and fails with hint unless it exits 0.
func (c *checker) green(ctx context.Context, ws *workspace.Workspace, command, log, name, hint string) error {
	logPath := filepath.Join(c.logs, log)
	exit, err := check.RunCommand(ctx, ws, c.t, command, logPath)
	if err != nil {
		return err
	}
	if exit != 0 {
		return failLog(name, logPath, "%s (%s)", hint, exited(exit))
	}
	return nil
}

// oracleAtBase is §6 step 4: the final check's reset and oracle on the unchanged base tree,
// then oracle.command must fail.
func (c *checker) oracleAtBase(ctx context.Context, ws *workspace.Workspace) error {
	if _, err := check.ResetTests(ctx, ws, c.t, nil); err != nil {
		return err
	}
	err := check.WriteOracle(ctx, ws, c.t)
	var missing *check.MissingOracleFileError
	if errors.As(err, &missing) {
		return fail(CheckOracleBase, "%v: oracle.files must name files the commit has", err)
	}
	if err != nil {
		return err
	}

	logPath := filepath.Join(c.logs, "oracle-base.log")
	exit, err := check.RunCommand(ctx, ws, c.t, c.t.Oracle.Command, logPath)
	if err != nil {
		return err
	}
	if exit == 0 {
		return failLog(CheckOracleBase, logPath, "oracle green at base_commit: the hidden test checks nothing new")
	}
	return nil
}

// leak is §6 step 6: no oracle file has fix_commit's content at base_commit already.
func (c *checker) leak(ctx context.Context, mirror string) error {
	t := c.t
	fix, err := check.OracleEntries(ctx, c.m, mirror, t.FixCommit, t.Oracle.Files)
	if err != nil {
		return err
	}
	baseTree, err := c.m.Tree(ctx, mirror, t.BaseCommit)
	if err != nil {
		return err
	}
	atBase := make(map[string]workspace.Entry, len(baseTree))
	for _, e := range baseTree {
		atBase[e.Path] = e
	}
	for _, e := range fix {
		if b, ok := atBase[e.Path]; ok && b.OID == e.OID {
			return fail(CheckLeak, "%s already has fix_commit's content at base_commit: the model would see the answer", e.Path)
		}
	}
	return nil
}

// answerChecks is §6 step 7: every check matches reference_answer, none matches the task.
func (c *checker) answerChecks() error {
	t := c.t
	if failed := check.MatchChecks(t.AnswerChecks, t.ReferenceAnswer); len(failed) > 0 {
		return fail(CheckAnswer, "answer_checks %s do not match reference_answer", quoteAll(failed))
	}
	var trivial []string
	for _, ac := range t.AnswerChecks {
		if len(check.MatchChecks([]string{ac}, t.Task)) == 0 {
			trivial = append(trivial, ac)
		}
	}
	if len(trivial) > 0 {
		return fail(CheckAnswer, "answer_checks %s match the task text itself: they measure nothing", quoteAll(trivial))
	}
	return nil
}

func exited(exit int) string {
	if exit == -1 {
		return "timed out"
	}
	return fmt.Sprintf("exit %d", exit)
}

func short(d time.Duration) string {
	if d >= 10*time.Second {
		return d.Round(time.Second).String()
	}
	return d.Round(100 * time.Millisecond).String()
}

func firstLines(s string, n int) string {
	lines := strings.Split(strings.TrimRight(s, "\n"), "\n")
	for i := range lines {
		lines[i] = strings.TrimSpace(lines[i])
	}
	if len(lines) > n {
		return strings.Join(lines[:n], ", ") + fmt.Sprintf(", and %d more", len(lines)-n)
	}
	return strings.Join(lines, ", ")
}

func quoteAll(ss []string) string {
	q := make([]string, len(ss))
	for i, s := range ss {
		q[i] = fmt.Sprintf("%q", s)
	}
	return strings.Join(q, ", ")
}

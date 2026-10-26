package run

import (
	"context"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"time"

	"orchbench/internal/check"
	"orchbench/internal/config"
	"orchbench/internal/results"
	"orchbench/internal/runner"
	"orchbench/internal/task"
	"orchbench/internal/workspace"
)

// maxReason is how much of an error a failure_reason keeps.
const maxReason = 300

const (
	// limitMargin is added to a usage limit's reported reset, so that a clock a little behind
	// the provider's does not run into the same limit (as the orchestrator does, MVP.md §25.2).
	limitMargin = time.Minute
	// limitFallback is the wait for a usage limit whose reset nobody reported, the
	// orchestrator's execution.rate_limit_wait default.
	limitFallback = 5 * time.Minute
)

// Runner is runner.Run; a test replaces it with a fake that needs no model.
type Runner func(ctx context.Context, cfg config.Config, t task.Task, ws runner.Workspace) (runner.Outcome, error)

// Env is what a cell needs besides the cell.
type Env struct {
	Workspaces *workspace.Manager
	// OutDir is the results directory: logs/, runs/ and configs/ are written under it.
	OutDir         string
	Bins           runner.Bins
	KeepWorkspaces bool
	// ClaudeCLI, OrchestratorVersion and Contributor are copied into every result line.
	ClaudeCLI           string
	OrchestratorVersion string
	Contributor         string
	// Out is where a cell waiting for a usage limit says so; nil says nothing.
	Out io.Writer
	// Now is the clock; nil is time.Now. Runner nil is runner.Run. Sleep waits d or until ctx
	// ends, with ctx's error then; nil is a timer.
	Now    func() time.Time
	Runner Runner
	Sleep  func(ctx context.Context, d time.Duration) error
}

// Finished is a cell that ended with a result line. Kept is the workspace left in place by
// --keep-workspaces.
type Finished struct {
	Record results.Record
	Kept   string
}

// ExecFunc runs one cell. It returns ctx's error, and no line, when ctx ended before the cell
// did, and a refused credential's; every other outcome, an infra_error included, is a Finished.
type ExecFunc func(ctx context.Context, c Cell) (Finished, error)

func (e Env) now() time.Time {
	if e.Now != nil {
		return e.Now()
	}
	return time.Now()
}

// cellDir is <out>/<kind>/<task>/<config>/rep<N>.
func (e Env) cellDir(kind string, c Cell) string {
	return filepath.Join(e.OutDir, kind, c.Task.ID, c.Config.Name, fmt.Sprintf("rep%d", c.Rep))
}

// Exec runs a cell (SPEC.md §7.1): workspace, setup, config, final check, copies, record. A
// failure that says nothing about the model (an infra error of the runner, a failed setup, git,
// bwrap or the disk) is retried once at once in a fresh workspace, and if it persists the cell
// is an infra_error line. The account's usage limit is not an attempt: the cell waits for the
// reset and runs again, as often as it takes, until ctx ends. A refused credential writes no
// line and returns its error, which stops the run: every later cell would be refused too. The
// cost of the failed attempts is added to the cell's, so that the budget sees every dollar spent.
func (e Env) Exec(ctx context.Context, c Cell) (Finished, error) {
	began := e.now()
	var (
		spent float64
		last  error
		kept  string
	)
	for tries := 0; tries < 2; {
		fin, cost, err := e.attempt(ctx, c)
		if ctx.Err() != nil {
			return Finished{}, ctx.Err()
		}
		if err == nil {
			fin.Record.CostUSD += spent
			return fin, nil
		}
		spent += cost
		last, kept = err, fin.Kept
		if runner.AuthFailure(err) {
			return Finished{}, err
		}
		if resets, ok := runner.UsageLimit(err); ok {
			if err := e.waitForLimit(ctx, c, resets); err != nil {
				return Finished{}, err
			}
			continue
		}
		tries++
	}

	rec := e.base(c, began)
	rec.Result = results.InfraError
	rec.FailureReason = reason(last)
	rec.CostUSD = spent
	rec.WallMS = e.now().Sub(began).Milliseconds()
	return Finished{Record: rec, Kept: kept}, nil
}

// waitForLimit sleeps until the usage limit that refused c resets, plus limitMargin, or
// limitFallback when the reset is unknown. It returns ctx's error when ctx ends first.
func (e Env) waitForLimit(ctx context.Context, c Cell, resets time.Time) error {
	wait := limitFallback
	if !resets.IsZero() {
		wait = max(resets.Sub(e.now()), 0) + limitMargin
	}
	if e.Out != nil {
		fmt.Fprintf(e.Out, "%s %s rep%d: usage limit reached, running it again at %s; Ctrl-C stops, the same command continues later\n",
			c.Task.ID, c.Config.Name, c.Rep, e.now().Add(wait).Local().Format("2006-01-02 15:04"))
	}
	if e.Sleep != nil {
		return e.Sleep(ctx, wait)
	}
	timer := time.NewTimer(wait)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-timer.C:
		return nil
	}
}

// attempt is one try of a cell. An error is infrastructure; cost is what the model already
// cost when it failed.
func (e Env) attempt(ctx context.Context, c Cell) (fin Finished, cost float64, err error) {
	t := c.Task
	logDir, runDest := e.cellDir("logs", c), e.cellDir("runs", c)
	// A cell run again must not mix with the files of the earlier try.
	for _, dir := range []string{logDir, runDest} {
		if err := os.RemoveAll(dir); err != nil {
			return fin, 0, err
		}
	}
	if err := os.MkdirAll(logDir, 0o755); err != nil {
		return fin, 0, err
	}

	ws, err := e.Workspaces.Create(ctx, t, t.BaseCommit)
	if err != nil {
		return fin, 0, fmt.Errorf("workspace: %w", err)
	}
	if e.KeepWorkspaces {
		fin.Kept = ws.Dir
	} else {
		defer ws.Remove()
	}

	exit, err := check.Sandboxed(ctx, ws.Dir, t, t.SetupCommand, true, filepath.Join(logDir, "setup.log"), e.Workspaces.Log)
	if err != nil {
		return fin, 0, fmt.Errorf("setup: %w", err)
	}
	if exit != 0 {
		return fin, 0, fmt.Errorf("setup: %s", exited(exit))
	}
	if c.Config.Setup != "" {
		exit, err := check.Sandboxed(ctx, ws.Dir, t, c.Config.Setup, false, filepath.Join(logDir, "config-setup.log"), e.Workspaces.Log)
		if err != nil {
			return fin, 0, fmt.Errorf("config setup: %w", err)
		}
		if exit != 0 {
			return fin, 0, fmt.Errorf("config setup: %s", exited(exit))
		}
	}
	// The test files before the config ran: the final check removes the ones the model added.
	beforeConfig, err := check.TestFiles(ws.Dir, t)
	if err != nil {
		return fin, 0, fmt.Errorf("listing the test files: %w", err)
	}

	out, started, wall, err := e.runConfig(ctx, c, ws, logDir)
	if err != nil {
		return fin, out.CostUSD, err
	}
	cost = out.CostUSD

	// The diff is taken before the check, which rewrites test files and writes the oracle.
	patch, err := ws.Diff(ctx)
	if err != nil {
		return fin, cost, fmt.Errorf("diff: %w", err)
	}
	if err := os.WriteFile(filepath.Join(logDir, "diff.patch"), []byte(patch), 0o644); err != nil {
		return fin, cost, err
	}

	var res check.Result
	if t.Category == task.CategoryQuestion {
		if err := os.WriteFile(filepath.Join(logDir, "answer.txt"), []byte(out.Answer), 0o644); err != nil {
			return fin, cost, err
		}
		res, err = check.Answer(ctx, t, out.Answer, ws)
	} else {
		res, err = check.Final(ctx, ws, t, logDir, beforeConfig)
	}
	if err != nil {
		return fin, cost, fmt.Errorf("final check: %w", err)
	}

	if out.RunDir != "" {
		if err := copyTree(out.RunDir, runDest); err != nil {
			return fin, cost, fmt.Errorf("copying the run directory: %w", err)
		}
	}

	fin.Record = e.record(c, out, res, countDiff(patch), started, wall)
	return fin, cost, nil
}

// runConfig runs the config in the workspace with its output copied to logs/config.stdout and
// config.stderr, and times it.
func (e Env) runConfig(ctx context.Context, c Cell, ws *workspace.Workspace, logDir string) (out runner.Outcome, started time.Time, wall time.Duration, err error) {
	stdout, err := os.Create(filepath.Join(logDir, "config.stdout"))
	if err != nil {
		return out, started, 0, err
	}
	defer stdout.Close()
	stderr, err := os.Create(filepath.Join(logDir, "config.stderr"))
	if err != nil {
		return out, started, 0, err
	}
	defer stderr.Close()

	exec := e.Runner
	if exec == nil {
		exec = runner.Run
	}
	started = e.now()
	out, err = exec(ctx, c.Config, c.Task, runner.Workspace{
		Dir:    ws.Dir,
		OutDir: e.OutDir,
		Stdout: stdout,
		Stderr: stderr,
		Bins:   e.Bins,
	})
	return out, started, e.now().Sub(started), err
}

// base is the part of a result line that is the same for every result of the cell.
func (e Env) base(c Cell, started time.Time) results.Record {
	t := c.Task
	return results.Record{
		Task:               t.ID,
		TaskSHA256:         c.TaskSHA256,
		Config:             c.Config.Name,
		Rep:                c.Rep,
		Language:           t.Language,
		Category:           t.Category,
		Lang:               t.Lang,
		Pair:               t.Pair,
		Contributor:        e.Contributor,
		StartedAt:          started.UTC().Format(time.RFC3339),
		ExpectedStages:     t.ExpectedStages,
		Model:              c.Config.Model,
		ClaudeCLI:          e.ClaudeCLI,
		OrchestratorCommit: e.OrchestratorVersion,
		ConfigSHA256:       c.Config.SHA256,
	}
}

// record is the result line of a cell that ran and was checked.
func (e Env) record(c Cell, out runner.Outcome, res check.Result, diff results.Diff, started time.Time, wall time.Duration) results.Record {
	rec := e.base(c, started)
	rec.WallMS = wall.Milliseconds()

	rec.Result = results.Fail
	if res.Pass {
		rec.Result = results.Pass
	}
	rec.FailReason = res.FailReason
	rec.OracleExit, rec.SuiteExit, rec.TestsReset = res.OracleExit, res.SuiteExit, res.TestsReset
	rec.AnswerChecksFailed = res.AnswerChecksFailed

	rec.RunStatus, rec.FailureReason = out.Status, out.FailureReason
	rec.CostUSD = out.CostUSD
	rec.Tokens = results.Tokens{
		Input:         out.Tokens.Input,
		Output:        out.Tokens.Output,
		CacheRead:     out.Tokens.CacheRead,
		CacheCreation: out.Tokens.CacheCreation,
	}
	rec.LLMCalls = out.LLMCalls
	rec.Decision = results.Decision{CostUSD: out.Decision.CostUSD, MS: out.Decision.MS, Calls: out.Decision.Calls}
	rec.FixIterations = out.FixIterations
	rec.ReviewVerdict = out.ReviewVerdict
	rec.Stages = out.Stages
	rec.Complexity = out.Complexity
	rec.Diff = diff
	if out.ConfigSHA256 != "" {
		rec.ConfigSHA256 = out.ConfigSHA256
	}
	return rec
}

// reason is an error as one short line.
func reason(err error) string {
	s := oneLine(err.Error())
	if r := []rune(s); len(r) > maxReason {
		s = string(r[:maxReason]) + "…"
	}
	return s
}

func exited(exit int) string {
	if exit == -1 {
		return "timed out"
	}
	return fmt.Sprintf("exit %d", exit)
}

// countDiff counts the files and the added and deleted lines of a `git diff --binary` patch. A
// binary file is a file with no lines.
func countDiff(patch string) results.Diff {
	var d results.Diff
	inHunk := false
	for _, line := range strings.Split(patch, "\n") {
		switch {
		case strings.HasPrefix(line, "diff --git "):
			d.Files++
			inHunk = false
		case strings.HasPrefix(line, "@@"):
			inHunk = true
		case line == "GIT binary patch":
			inHunk = false
		case !inHunk:
		case strings.HasPrefix(line, "+"):
			d.Added++
		case strings.HasPrefix(line, "-"):
			d.Deleted++
		}
	}
	return d
}

// copyTree copies the directories and regular files of src to dst, and symlinks as symlinks. It
// never follows one, so a link the model's run left cannot reach outside the run directory.
func copyTree(src, dst string) error {
	return filepath.WalkDir(src, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		rel, err := filepath.Rel(src, p)
		if err != nil {
			return err
		}
		target := filepath.Join(dst, rel)
		switch {
		case d.IsDir():
			return os.MkdirAll(target, 0o755)
		case d.Type()&fs.ModeSymlink != 0:
			link, err := os.Readlink(p)
			if err != nil {
				return err
			}
			return os.Symlink(link, target)
		case d.Type().IsRegular():
			return copyFile(p, target)
		}
		return nil
	})
}

func copyFile(src, dst string) (err error) {
	in, err := os.Open(src)
	if err != nil {
		return err
	}
	defer in.Close()
	out, err := os.OpenFile(dst, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o644)
	if err != nil {
		return err
	}
	defer func() {
		if cerr := out.Close(); err == nil {
			err = cerr
		}
	}()
	_, err = io.Copy(out, in)
	return err
}

// Package runner runs one benchmark config in a prepared workspace and returns what the result
// line needs from it (specs/benchmark/SPEC.md §7.1 step 2, §8, §9). Config A is bare Claude
// Code; every other config is the orchestrator, run as a black box over a config rendered for
// the task. The runner reads only what these programs write: the stream-json `result` event,
// `run.json` and `decision.json`, each through structs of its own.
//
// What went wrong is told apart by whose fault it is. A model that fails the task, an
// orchestrator run that ends `failed`, a timeout: these are results, an Outcome and a nil
// error. A binary that is missing, a rate limit, a crash before any step, a run directory that
// is not there: these are an *InfraError, and the cell is not counted either way.
package runner

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"sync"
	"syscall"
	"time"

	"orchbench/internal/config"
	"orchbench/internal/task"
)

// The statuses an Outcome carries: the orchestrator's run statuses, and for config A completed
// or failed.
const (
	StatusCompleted = "completed"
	StatusFailed    = "failed"
	StatusStopped   = "stopped"
)

// ReasonTimeout is the failure reason of a run that did not finish within the task's timeout.
const ReasonTimeout = "timeout"

const (
	// killDelay is how long after the kill a process holding the output pipes open is waited
	// for.
	killDelay = 5 * time.Second
	// tailBytes is how much of the end of each output stream is kept for classifying a failure.
	tailBytes = 64 << 10
)

// orchestratorGrace is how long the orchestrator may outlive the task's timeout. Its own
// execution.timeout is the same duration, so it stops itself first and writes the failed run;
// this is only the backstop that kills it. A variable so that a test need not wait for it.
var orchestratorGrace = 30 * time.Second

// Bins are the programs to run; an empty one is looked up on PATH by its name.
type Bins struct {
	Claude       string
	Orchestrator string
}

// Workspace is where and how a config is run.
type Workspace struct {
	// Dir is the prepared workspace: the tree at base_commit, set up. The config runs in it.
	Dir string
	// OutDir is the results directory; an orchestrator config is rendered into
	// <OutDir>/configs/<task>-<config>.yaml, outside the workspace so the worktree stays clean.
	OutDir string
	// Stdout and Stderr, when set, receive a copy of the config's output (logs/config.stdout
	// and logs/config.stderr).
	Stdout, Stderr io.Writer
	Bins           Bins
}

// Tokens are the token counters of a run, summed over its provider calls.
type Tokens struct {
	Input         int64
	Output        int64
	CacheRead     int64
	CacheCreation int64
}

// Decision is what deciding the workflow cost; zero for config A and for a `--stage` run.
type Decision struct {
	CostUSD float64
	MS      int64
	Calls   int
}

// Outcome is what one config run contributes to a result line (SPEC.md §9).
type Outcome struct {
	// Status is the run's status and FailureReason why it failed. Both come from the
	// orchestrator's run.json; for config A, from the result event and the exit code.
	Status        string
	FailureReason string

	CostUSD  float64
	Tokens   Tokens
	LLMCalls int
	Decision Decision

	FixIterations int
	// ReviewVerdict is the last review's verdict (pass or fail), empty without one.
	ReviewVerdict string
	// TestsPassedFinal is the last test step's result; nil without a test step.
	TestsPassedFinal *bool
	// Stages are the stage names the workflow ran, in order, and Complexity the decision's
	// (empty for a `--stage` run). Both are empty for config A.
	Stages     []string
	Complexity string
	// Answer is the final answer of a question task: the result event's text for config A,
	// the last read-only step's answer for the orchestrator. Empty for other categories.
	Answer string

	// ConfigPath and ConfigSHA256 are the orchestrator config the cell used, or for config A
	// the hash of what was sent: the prompt, the model and the tools.
	ConfigPath   string
	ConfigSHA256 string
	// RunDir is the orchestrator's run directory in the workspace, to be copied out.
	RunDir string

	// ExitCode is the config's exit code, -1 when it did not exit normally (killed).
	ExitCode int
	// TimedOut is true when the task's timeout ended the run; Status is then failed.
	TimedOut bool
}

// InfraError is a failure that says nothing about the model: the cell is recorded as an
// infrastructure error, retried, and left out of the pass rate.
type InfraError struct {
	Reason string
	Err    error
}

func (e *InfraError) Error() string {
	if e.Err == nil {
		return "infrastructure error: " + e.Reason
	}
	return fmt.Sprintf("infrastructure error: %s: %v", e.Reason, e.Err)
}

func (e *InfraError) Unwrap() error { return e.Err }

// IsInfra reports whether err is, or wraps, an *InfraError.
func IsInfra(err error) bool {
	var ie *InfraError
	return errors.As(err, &ie)
}

func infra(format string, args ...any) *InfraError {
	return &InfraError{Reason: fmt.Sprintf(format, args...)}
}

// Run runs cfg on t in ws with t's timeout. It returns an Outcome and a nil error for anything
// the model or the workflow did, a timeout included, and an *InfraError for what it did not.
// A cancelled ctx returns ctx.Err() and no result at all. An Outcome may accompany an error;
// it holds what was known (the config's hash and path), not a result.
func Run(ctx context.Context, cfg config.Config, t task.Task, ws Workspace) (Outcome, error) {
	if t.Timeout <= 0 {
		return Outcome{}, fmt.Errorf("task %s has no timeout", t.ID)
	}
	if ws.Dir == "" {
		return Outcome{}, errors.New("runner: the workspace directory is empty")
	}
	dir, err := filepath.Abs(ws.Dir)
	if err != nil {
		return Outcome{}, err
	}
	ws.Dir = dir

	switch cfg.Kind {
	case config.KindClaude:
		return runClaude(ctx, cfg, t, ws)
	case config.KindOrchestrator:
		return runOrchestrator(ctx, cfg, t, ws)
	}
	return Outcome{}, fmt.Errorf("config %s: unknown kind %q", cfg.Name, cfg.Kind)
}

// lookBin resolves a program to an absolute path, or an *InfraError when it is not there.
func lookBin(override, name string) (string, error) {
	target := name
	if override != "" {
		target = override
	}
	path, err := exec.LookPath(target)
	if err != nil {
		return "", &InfraError{Reason: fmt.Sprintf("%s is not available (%s)", name, target), Err: err}
	}
	abs, err := filepath.Abs(path)
	if err != nil {
		return "", err
	}
	return abs, nil
}

// command is one process to run.
type command struct {
	Bin   string
	Args  []string
	Dir   string
	Env   []string
	Stdin io.Reader
	// Stdout and Stderr are extra sinks next to the tail buffers; either may be nil.
	Stdout, Stderr io.Writer
}

// process is how a command ended.
type process struct {
	// ExitCode is -1 when the process was killed by a signal.
	ExitCode int
	// TimedOut is true when ctx's deadline ended it.
	TimedOut bool
	// Stdout and Stderr are the last tailBytes of each stream.
	Stdout, Stderr string
}

// execute runs c in its own process group, so that the kill at the deadline reaches what the
// program started (claude's Bash tool, the orchestrator's claude). A program that ran and
// exited non-zero, or was killed, is a process and a nil error; an error means it could not be
// run at all, or its output could not be read.
func execute(ctx context.Context, c command) (process, error) {
	out, errOut := &tailBuffer{max: tailBytes}, &tailBuffer{max: tailBytes}

	cmd := exec.CommandContext(ctx, c.Bin, c.Args...)
	cmd.Dir = c.Dir
	cmd.Env = c.Env
	cmd.Stdin = c.Stdin
	cmd.Stdout = tee(out, c.Stdout)
	cmd.Stderr = tee(errOut, c.Stderr)
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	cmd.Cancel = func() error {
		if err := syscall.Kill(-cmd.Process.Pid, syscall.SIGKILL); err != nil && !errors.Is(err, syscall.ESRCH) {
			return err
		}
		return nil
	}
	cmd.WaitDelay = killDelay

	err := cmd.Run()

	p := process{ExitCode: -1, Stdout: out.String(), Stderr: errOut.String()}
	if cmd.ProcessState != nil {
		p.ExitCode = cmd.ProcessState.ExitCode()
	}
	p.TimedOut = errors.Is(ctx.Err(), context.DeadlineExceeded)

	var exitErr *exec.ExitError
	switch {
	case err == nil, errors.As(err, &exitErr), errors.Is(err, exec.ErrWaitDelay), ctx.Err() != nil && cmd.ProcessState != nil:
		return p, nil
	}
	return p, err
}

func tee(tail *tailBuffer, extra io.Writer) io.Writer {
	if extra == nil {
		return tail
	}
	return io.MultiWriter(tail, extra)
}

// tailBuffer keeps the last max bytes written to it.
type tailBuffer struct {
	mu  sync.Mutex
	max int
	buf []byte
}

func (b *tailBuffer) Write(p []byte) (int, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.buf = append(b.buf, p...)
	if len(b.buf) > b.max {
		b.buf = append([]byte(nil), b.buf[len(b.buf)-b.max:]...)
	}
	return len(p), nil
}

func (b *tailBuffer) String() string {
	b.mu.Lock()
	defer b.mu.Unlock()
	return string(b.buf)
}

// maxLine is the longest stream-json line parsed; a longer one is skipped.
const maxLine = 16 << 20

// lineWriter calls fn with every complete line written to it.
type lineWriter struct {
	buf      []byte
	dropping bool
	fn       func(line []byte)
}

func (w *lineWriter) Write(p []byte) (int, error) {
	n := len(p)
	for len(p) > 0 {
		i := bytes.IndexByte(p, '\n')
		if i < 0 {
			if !w.dropping {
				w.buf = append(w.buf, p...)
				if len(w.buf) > maxLine {
					w.buf, w.dropping = nil, true
				}
			}
			break
		}
		if !w.dropping {
			w.buf = append(w.buf, p[:i]...)
			if len(w.buf) <= maxLine {
				w.fn(w.buf)
			}
		}
		w.buf, w.dropping = w.buf[:0], false
		p = p[i+1:]
	}
	return n, nil
}

// flush handles a last line that has no newline.
func (w *lineWriter) flush() {
	if len(w.buf) > 0 && !w.dropping {
		w.fn(w.buf)
	}
	w.buf = nil
}

// rateLimitPatterns recognise a provider telling the account to stop. They are deliberately
// narrow: an infra error is left out of the pass rate, so a model failure taken for one would
// hide a failed task. They are matched only against what the provider CLIs write (stderr,
// stdout outside the stream-json events, a result event's text, a run's failure reason), and
// only for a run that failed, never against test output, which may be about rate limiting.
var rateLimitPatterns = []*regexp.Regexp{
	regexp.MustCompile(`(?i)rate[ _-]?limit`),
	regexp.MustCompile(`(?i)usage limit`),
	regexp.MustCompile(`(?i)hit your (\w+ )?limit`), // usage, rate, session, weekly, ...
	regexp.MustCompile(`(?i)(5-hour|five[ -]hour|weekly) limit`),
	regexp.MustCompile(`(?i)too many requests`),
	regexp.MustCompile(`(?i)overloaded_error|api error:? *(\d+ )?overloaded`),
	regexp.MustCompile(`(?i)(api error|http|status)[: ]+429\b`),
	regexp.MustCompile(`(?i)quota (exceeded|exhausted)`),
	regexp.MustCompile(`(?i)credit balance is too low`),
}

// rateLimited returns the first line of texts that looks like a rate limit, shortened.
func rateLimited(texts ...string) (string, bool) {
	for _, text := range texts {
		for _, line := range strings.Split(text, "\n") {
			for _, re := range rateLimitPatterns {
				if re.MatchString(line) {
					return excerpt(line), true
				}
			}
		}
	}
	return "", false
}

func excerpt(s string) string {
	s = strings.TrimSpace(s)
	if r := []rune(s); len(r) > 200 {
		s = string(r[:200]) + "…"
	}
	return s
}

// environ is the environment a config runs in: the runner's own. When a claude binary was
// named, its directory goes first on PATH, so that the claude the orchestrator starts is the
// one the benchmark measures.
func environ(bins Bins) []string {
	env := os.Environ()
	if bins.Claude == "" || filepath.Base(bins.Claude) != "claude" {
		return env
	}
	abs, err := filepath.Abs(bins.Claude)
	if err != nil {
		return env
	}
	return append(env, "PATH="+filepath.Dir(abs)+string(os.PathListSeparator)+os.Getenv("PATH"))
}

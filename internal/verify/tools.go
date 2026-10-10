package verify

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"syscall"
	"time"
	"unicode/utf8"

	"orchestrator/internal/domain"
	"orchestrator/internal/guard"
	"orchestrator/internal/testexec"
)

// Files the server writes inside its directory (specs/quality/SPEC.md §6.4, §6.6, §6.8).
const (
	callsFile     = CallsFile
	violationFile = ViolationFile
)

// CallsFile and ViolationFile are the names the orchestrator reads after the session ends.
const (
	CallsFile     = "calls.jsonl"
	ViolationFile = "violation"
)

// LogFile is the name of the file holding the full output of call k (1-based) on stream
// ("stdout" or "stderr").
func LogFile(k int, stream string) string {
	return fmt.Sprintf("call-%d.%s.log", k, stream)
}

// Runner runs a command in the sandbox. *testexec.Runner is the real one; a test fakes it.
type Runner interface {
	Run(ctx context.Context, command, workingDir string, opts testexec.RunOptions) (domain.TestResult, error)
}

// Guard reports what changed in the protected state since before. guard.RepoGuard is the
// real one; a test fakes it.
type Guard interface {
	Changed(ctx context.Context, before guard.State) ([]string, error)
}

// Result is the answer to one tools/call: the text block of the MCP result, and isError.
type Result struct {
	Text    string
	IsError bool
}

// Tools runs the two tools. Calls are serialised.
type Tools struct {
	Spec   *Spec
	Dir    string
	Runner Runner
	Guard  Guard
	// Log gets diagnostics; nil discards them.
	Log io.Writer

	mu sync.Mutex
	// count is the calls received, refused ones included; it numbers the call logs.
	count int
	// recorded is the lines in calls.jsonl.
	recorded int
	// violated is true once dir/violation exists.
	violated bool
}

// NewTools returns the tools over spec, writing into dir. A restarted server keeps the
// state of the step: the calls already recorded still count against the limit, and an
// existing violation file still refuses every call.
func NewTools(spec *Spec, dir string, runner Runner, g Guard) (*Tools, error) {
	t := &Tools{Spec: spec, Dir: dir, Runner: runner, Guard: g}
	if _, err := os.Lstat(filepath.Join(dir, violationFile)); err == nil {
		t.violated = true
	} else if !errors.Is(err, fs.ErrNotExist) {
		return nil, err
	}
	data, err := os.ReadFile(filepath.Join(dir, callsFile))
	switch {
	case errors.Is(err, fs.ErrNotExist):
	case err != nil:
		return nil, err
	default:
		for _, line := range strings.Split(string(data), "\n") {
			if strings.TrimSpace(line) != "" {
				t.count++
			}
		}
		t.recorded = t.count
	}
	return t, nil
}

// CheckNames are the names run_check accepts, in configuration order.
func (t *Tools) CheckNames() []string {
	names := make([]string, len(t.Spec.Checks))
	for i, c := range t.Spec.Checks {
		names[i] = c.Name
	}
	return names
}

// Offers reports whether tool is one the server serves: run_check only with checks.
func (t *Tools) Offers(tool string) bool {
	switch tool {
	case domain.VerifyToolRunTests:
		return true
	case domain.VerifyToolRunCheck:
		return len(t.Spec.Checks) > 0
	}
	return false
}

// request is a validated call: the command to run and what the model chose.
type request struct {
	command string
	check   string
	filter  string
}

// Call handles one tools/call of an offered tool, in this order (SPEC §6.5-§6.8): count it,
// validate the arguments, refuse after an earlier violation, check the guard, run under
// the call timeout, bound the output, record the call, write the logs, answer. A refused
// call runs nothing and answers isError.
func (t *Tools) Call(ctx context.Context, tool string, args json.RawMessage) Result {
	t.mu.Lock()
	defer t.mu.Unlock()

	t.count++
	k := t.count
	// parseArgs is pure; doing it first only lets every record, refused or not, name the
	// check or filter the model chose when that was valid.
	req, reason, hint := t.parseArgs(tool, args)
	rec := domain.VerifyCall{Tool: tool, Check: req.check, Filter: req.filter}
	refuse := func(reason, text string) Result {
		rec.Refused = reason
		t.record(rec)
		return Result{Text: "refused: " + text, IsError: true}
	}

	if k > t.Spec.MaxCalls {
		return refuse("limit reached", fmt.Sprintf("limit reached: at most %d calls per step", t.Spec.MaxCalls))
	}
	if reason != "" {
		return refuse("invalid arguments: "+reason, "invalid arguments: "+reason+hint)
	}
	if t.violated {
		return refuse("protected files changed earlier",
			"an earlier call found protected files changed; nothing more is run in this step")
	}

	changed, err := t.Guard.Changed(ctx, t.Spec.Before)
	switch {
	case ctx.Err() != nil && err != nil:
		// The session is ending; that is not a violation.
		return refuse("canceled", "the server is shutting down")
	case err != nil:
		// Fail closed: a check that cannot tell is treated as a change.
		t.markViolation("the guard check failed: " + oneLine(err.Error()))
		return refuse("guard check failed",
			"the check for protected-file changes failed, so nothing is run in this step: "+oneLine(err.Error()))
	case len(changed) > 0:
		t.markViolation(strings.Join(changed, "\n"))
		return refuse("protected files changed", "protected files changed: "+listPaths(changed))
	}

	env := []string{"ORCH_BASE_REF=" + t.Spec.PreRunRef}
	if req.filter != "" {
		env = append(env, "ORCH_TEST_FILTER="+req.filter)
	}
	callCtx, cancel := context.WithTimeout(ctx, t.Spec.CallTimeout)
	start := time.Now()
	res, err := t.Runner.Run(callCtx, req.command, t.Spec.RepoRoot, testexec.RunOptions{Env: env})
	// Only the call's own deadline is a timeout; the session's cancellation is not.
	timedOut := errors.Is(callCtx.Err(), context.DeadlineExceeded) && ctx.Err() == nil
	cancel()
	switch {
	case timedOut:
		// The runner discards the output of a run it had to kill, so there is none to show.
		res = domain.TestResult{
			Command:  req.command,
			ExitCode: -1,
			Stderr:   fmt.Sprintf("timed out after %s; the command and its child processes were killed\n", t.Spec.CallTimeout),
			Duration: time.Since(start),
		}
	case err != nil:
		return refuse("failed to run", "failed to run: "+oneLine(err.Error()))
	}

	stdout, stderr, omitted := res.Stdout, res.Stderr, testexec.Omitted{}
	if t.Spec.OutputBytes > 0 {
		stdout, stderr, omitted = testexec.BoundTestOutput(stdout, stderr, t.Spec.OutputBytes)
	}
	rec.ExitCode = res.ExitCode
	rec.Success = res.ExitCode == 0
	rec.Duration = res.Duration
	rec.OutputCut = omitted.Any()
	t.record(rec)
	t.writeLog(LogFile(k, "stdout"), res.Stdout)
	t.writeLog(LogFile(k, "stderr"), res.Stderr)

	return Result{Text: t.answer(k, req, res, stdout, stderr)}
}

// answer is the text block of SPEC §6.1.
func (t *Tools) answer(k int, req request, res domain.TestResult, stdout, stderr string) string {
	var b strings.Builder
	fmt.Fprintf(&b, "exit code: %d\n", res.ExitCode)
	fmt.Fprintf(&b, "command: %s\n", req.command)
	if req.check != "" {
		fmt.Fprintf(&b, "check: %s\n", req.check)
	}
	if req.filter != "" {
		fmt.Fprintf(&b, "filter: %s\n", req.filter)
	}
	fmt.Fprintf(&b, "duration: %s\n", roundDuration(res.Duration))
	fmt.Fprintf(&b, "calls left in this step: %d\n", t.Spec.MaxCalls-k)
	b.WriteString("--- output (bounded) ---\n")
	b.WriteString(stdout)
	if stdout != "" && !strings.HasSuffix(stdout, "\n") && stderr != "" {
		b.WriteString("\n")
	}
	b.WriteString(stderr)
	if stdout == "" && stderr == "" {
		b.WriteString("(no output)\n")
	}
	return b.String()
}

// Reasons parseArgs gives, which are fixed strings: nothing the model wrote is echoed into
// a record.
const (
	reasonNotObject    = "arguments must be an object"
	reasonUnknownArg   = "unknown argument"
	reasonFilterType   = "filter must be a string"
	reasonFilterShape  = "filter must match " + domain.VerifyFilterPattern
	reasonNameRequired = "name is required"
	reasonNameType     = "name must be a string"
	reasonUnknownCheck = "unknown check"
)

// parseArgs validates the arguments of an offered tool. It returns what could be taken from
// them (a filter or check name only when valid), and a reason when the call must be
// refused; hint is more for the model, never recorded.
func (t *Tools) parseArgs(tool string, raw json.RawMessage) (req request, reason, hint string) {
	var allowed string
	switch tool {
	case domain.VerifyToolRunTests:
		allowed, req.command = "filter", t.Spec.TestCommand
	default:
		allowed = "name"
	}
	args := map[string]json.RawMessage{}
	if trimmed := strings.TrimSpace(string(raw)); trimmed != "" && trimmed != "null" {
		if err := json.Unmarshal(raw, &args); err != nil {
			return request{}, reasonNotObject, ""
		}
	}
	for key := range args {
		if key != allowed {
			return request{}, reasonUnknownArg, ""
		}
	}

	value, present := args[allowed]
	if tool == domain.VerifyToolRunTests {
		if !present {
			return req, "", ""
		}
		var filter string
		if firstByte(value) != '"' || json.Unmarshal(value, &filter) != nil {
			return req, reasonFilterType, ""
		}
		if !domain.VerifyFilterRE.MatchString(filter) {
			return req, reasonFilterShape, ""
		}
		req.filter = filter
		return req, "", ""
	}

	if !present {
		return req, reasonNameRequired, ""
	}
	var name string
	if firstByte(value) != '"' || json.Unmarshal(value, &name) != nil {
		return req, reasonNameType, ""
	}
	for _, c := range t.Spec.Checks {
		if c.Name == name {
			req.check, req.command = c.Name, c.Command
			return req, "", ""
		}
	}
	return req, reasonUnknownCheck, "; the checks are: " + strings.Join(t.CheckNames(), ", ")
}

// markViolation records that a call found protected files changed (or could not tell) in
// dir/violation: the changed paths, one per line. It stays even if the tree is restored.
func (t *Tools) markViolation(content string) {
	t.violated = true
	if err := appendFile(filepath.Join(t.Dir, violationFile), []byte(content+"\n")); err != nil {
		logf(t.Log, "could not write %s: %v", violationFile, err)
	}
}

// record appends one line to calls.jsonl and syncs it, so a killed server loses nothing.
// The file holds at most domain.MaxVerifyCalls lines: later calls are refused and counted
// but not recorded.
func (t *Tools) record(rec domain.VerifyCall) {
	if t.recorded >= domain.MaxVerifyCalls {
		return
	}
	data, err := json.Marshal(rec)
	if err == nil {
		err = appendFile(filepath.Join(t.Dir, callsFile), append(data, '\n'))
	}
	if err != nil {
		logf(t.Log, "could not record the call: %v", err)
		return
	}
	t.recorded++
}

// writeLog stores one call's full output. A name is never reused: an existing file or a
// symlink is an error, not something to write through.
func (t *Tools) writeLog(name, content string) {
	f, err := os.OpenFile(filepath.Join(t.Dir, name), os.O_WRONLY|os.O_CREATE|os.O_EXCL|syscall.O_NOFOLLOW, 0o600)
	if err == nil {
		_, err = f.WriteString(content)
		if cerr := f.Close(); err == nil {
			err = cerr
		}
	}
	if err != nil {
		logf(t.Log, "could not write %s: %v", name, err)
	}
}

// appendFile appends data to path (created 0600, never through a symlink) and syncs it.
func appendFile(path string, data []byte) error {
	f, err := os.OpenFile(path, os.O_WRONLY|os.O_APPEND|os.O_CREATE|syscall.O_NOFOLLOW, 0o600)
	if err != nil {
		return err
	}
	_, err = f.Write(data)
	if err == nil {
		err = f.Sync()
	}
	if cerr := f.Close(); err == nil {
		err = cerr
	}
	return err
}

func logf(w io.Writer, format string, args ...any) {
	if w != nil {
		fmt.Fprintf(w, "mcp-verify: "+format+"\n", args...)
	}
}

// oneLine collapses whitespace and cuts the text to a length that fits one line of an
// answer or a record.
func oneLine(s string) string {
	s = strings.Join(strings.Fields(s), " ")
	const limit = 300
	if len(s) <= limit {
		return s
	}
	cut := limit
	for cut > 0 && !utf8.RuneStart(s[cut]) {
		cut--
	}
	return s[:cut] + "..."
}

// listPaths names the first changed paths for the answer; the violation file has them all.
func listPaths(paths []string) string {
	const shown = 10
	if len(paths) <= shown {
		return strings.Join(paths, ", ")
	}
	return fmt.Sprintf("%s (and %d more)", strings.Join(paths[:shown], ", "), len(paths)-shown)
}

// roundDuration keeps the answer short: tenths of a second from one second on, else
// milliseconds.
func roundDuration(d time.Duration) time.Duration {
	if d >= time.Second {
		return d.Round(100 * time.Millisecond)
	}
	return d.Round(time.Millisecond)
}

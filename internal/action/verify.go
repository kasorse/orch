package action

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"syscall"

	"orchestrator/internal/apperr"
	"orchestrator/internal/domain"
	"orchestrator/internal/execution"
	"orchestrator/internal/verify"
)

// What readVerifyDir accepts from a directory a process running model code wrote into.
const (
	// maxVerifyCallsBytes bounds calls.jsonl: at most 50 lines of a few hundred bytes.
	maxVerifyCallsBytes = 1 << 20
	// maxVerifyLogBytes bounds one call log kept for the run directory; the cut is marked.
	maxVerifyLogBytes = 512 << 10
	// maxVerifyViolationBytes and maxVerifyViolationLines bound what violation shows.
	maxVerifyViolationBytes = 64 << 10
	maxVerifyViolationLines = 20
)

// verifyOutput is what an implement step's verify directory held after the session ended.
type verifyOutput struct {
	calls []domain.VerifyCall
	logs  []domain.CheckOutput
	// violation is the changed paths the server recorded, one line each, empty when it
	// found none.
	violation []string
	// err is a calls.jsonl that could not be read or had a malformed line; calls then holds
	// the lines before it.
	err error
}

// executable is the orchestrator binary the MCP client starts as `<exe> mcp-verify`.
func (d *Dispatcher) executable() (string, error) {
	exe := d.VerifyExecutable
	if exe == "" {
		var err error
		if exe, err = os.Executable(); err != nil {
			return "", err
		}
		if exe, err = filepath.EvalSymlinks(exe); err != nil {
			return "", err
		}
	}
	if !filepath.IsAbs(exe) {
		return "", fmt.Errorf("the orchestrator executable %q is not an absolute path", exe)
	}
	return exe, nil
}

// verifySpec is the server's input (specs/quality/SPEC.md §6.4): the test tools with
// actions.implement.verify, replace when replace is true. It fails closed: the sandbox is the
// one the test step uses, and without it nothing is written, since the model triggers the
// test runs.
func (d *Dispatcher) verifySpec(run *domain.Run, before GuardState, replace bool) (*verify.Spec, error) {
	root, err := filepath.Abs(d.Repo.Dir)
	if err != nil {
		return nil, err
	}
	spec := &verify.Spec{Version: verify.SpecVersion, RepoRoot: root, Before: before}
	if replace {
		spec.Replace = &verify.ReplaceSpec{ExtraDeny: append([]string{}, d.Config.Context.ExtraDeny...)}
	}
	if !d.Config.Actions.Implement.Verify {
		return spec, nil
	}
	if d.Runner == nil || d.Runner.Sandbox == nil {
		return nil, errors.New("no sandbox is configured for the test runner, and verify never runs unsandboxed")
	}
	checks := make([]verify.Check, len(d.Config.Checks))
	for i, c := range d.Config.Checks {
		checks[i] = verify.Check{Name: c.Name, Command: c.Command}
	}
	// ORCH_* names are set per call by the server, so none is passed in.
	var env []string
	for _, kv := range execution.TestEnv() {
		if !strings.HasPrefix(kv, "ORCH_") {
			env = append(env, kv)
		}
	}
	v := d.Config.Limits.Verify
	spec.TestCommand, spec.Checks, spec.PreRunRef = d.Config.Test.Command, checks, run.PreRunRef
	if c := d.Config.Test.VerifyCommand; strings.TrimSpace(c) != "" {
		spec.TestCommand = c
	}
	spec.Sandbox, spec.TestEnv = d.Runner.Sandbox, env
	spec.MaxCalls, spec.CallTimeout = v.MaxCalls(), v.Timeout()
	spec.OutputBytes = d.Config.Limits.Prompt.TestOutputBytes()
	return spec, nil
}

// openVerifyFile opens a regular file of dir for reading without following a symlink or
// blocking on a FIFO. ok is false when it does not exist.
func openVerifyFile(dir, name string) (f *os.File, ok bool, err error) {
	f, err = os.OpenFile(filepath.Join(dir, name), os.O_RDONLY|syscall.O_NOFOLLOW|syscall.O_NONBLOCK, 0)
	if errors.Is(err, os.ErrNotExist) {
		return nil, false, nil
	}
	if err != nil {
		return nil, false, err
	}
	info, err := f.Stat()
	if err == nil && !info.Mode().IsRegular() {
		err = errors.New("not a regular file")
	}
	if err != nil {
		f.Close()
		return nil, false, err
	}
	return f, true, nil
}

// readVerifyFile reads at most limit bytes of dir/name; cut reports that there was more.
func readVerifyFile(dir, name string, limit int) (data []byte, cut, ok bool, err error) {
	f, ok, err := openVerifyFile(dir, name)
	if err != nil || !ok {
		return nil, false, false, err
	}
	defer f.Close()
	data, err = io.ReadAll(io.LimitReader(f, int64(limit)+1))
	if err != nil {
		return nil, false, true, err
	}
	if len(data) > limit {
		return data[:limit], true, true, nil
	}
	return data, false, true, nil
}

// readVerifyDir collects what the server left in dir: the recorded calls, the output of the
// calls that ran, and the violation. Everything in it is untrusted: the files are read
// without following symlinks, with bounds, and calls.jsonl strictly.
func readVerifyDir(dir string) verifyOutput {
	var out verifyOutput
	out.calls, out.err = readVerifyCalls(dir)

	for i, c := range out.calls {
		if c.Refused != "" {
			continue
		}
		k := i + 1
		log := domain.CheckOutput{Name: fmt.Sprintf("verify-%d", k)}
		for _, s := range []struct {
			stream string
			dst    *string
		}{{"stdout", &log.Stdout}, {"stderr", &log.Stderr}} {
			data, cut, ok, err := readVerifyFile(dir, verify.LogFile(k, s.stream), maxVerifyLogBytes)
			if err != nil || !ok {
				continue
			}
			*s.dst = string(data)
			if cut {
				*s.dst += "\n[cut: the log is longer than the orchestrator keeps]\n"
			}
		}
		if log.Stdout != "" || log.Stderr != "" {
			out.logs = append(out.logs, log)
		}
	}

	data, cut, ok, err := readVerifyFile(dir, verify.ViolationFile, maxVerifyViolationBytes)
	switch {
	case err != nil:
		// The file is there but unreadable (a symlink, a FIFO): a violation that cannot be
		// shown is still one.
		out.violation = []string{"the violation file could not be read: " + oneLine(err.Error())}
	case ok:
		for _, line := range strings.Split(string(data), "\n") {
			if line = oneLine(line); line == "" {
				continue
			}
			if len(line) > 300 {
				line = strings.ToValidUTF8(line[:300], "") + "..."
			}
			if len(out.violation) == maxVerifyViolationLines {
				out.violation = append(out.violation, "(more not shown)")
				break
			}
			out.violation = append(out.violation, line)
		}
		if cut && len(out.violation) < maxVerifyViolationLines {
			out.violation = append(out.violation, "(more not shown)")
		}
		if len(out.violation) == 0 {
			out.violation = []string{"(no path recorded)"}
		}
	}
	return out
}

// readVerifyCalls decodes calls.jsonl: one JSON object per line, strictly, at most
// domain.MaxVerifyCalls lines, each passing VerifyCall.Validate. A missing file is no calls.
func readVerifyCalls(dir string) ([]domain.VerifyCall, error) {
	data, cut, ok, err := readVerifyFile(dir, verify.CallsFile, maxVerifyCallsBytes)
	if err != nil {
		return nil, fmt.Errorf("%s: %w", verify.CallsFile, err)
	}
	if !ok {
		return nil, nil
	}
	if cut {
		return nil, fmt.Errorf("%s is larger than %d bytes", verify.CallsFile, maxVerifyCallsBytes)
	}
	var calls []domain.VerifyCall
	for n, line := range bytes.Split(data, []byte("\n")) {
		if len(bytes.TrimSpace(line)) == 0 {
			continue
		}
		if len(calls) == domain.MaxVerifyCalls {
			return calls, fmt.Errorf("%s has more than %d lines", verify.CallsFile, domain.MaxVerifyCalls)
		}
		dec := json.NewDecoder(bytes.NewReader(line))
		dec.DisallowUnknownFields()
		var c domain.VerifyCall
		if err := dec.Decode(&c); err != nil {
			return calls, fmt.Errorf("%s line %d is malformed: %w", verify.CallsFile, n+1, err)
		}
		if _, err := dec.Token(); !errors.Is(err, io.EOF) {
			return calls, fmt.Errorf("%s line %d is malformed: data after the object", verify.CallsFile, n+1)
		}
		if err := c.Validate(); err != nil {
			return calls, fmt.Errorf("%s line %d: %w", verify.CallsFile, n+1, err)
		}
		calls = append(calls, c)
	}
	return calls, nil
}

// verifyFailure is the error an implement step ends with because of what its verify calls
// left behind, or nil: a violation first, then an unreadable calls.jsonl.
func verifyFailure(v verifyOutput, restoreRef string) error {
	if len(v.violation) > 0 {
		heading := "the model's code changed protected files during a verify call; the run stops before anything else executes it:\n"
		return &apperr.WorkflowError{Msg: violationMessage(heading, v.violation, restoreRef)}
	}
	if v.err != nil {
		return &apperr.WorkflowError{Msg: "the verify calls of the step could not be read", Err: v.err}
	}
	return nil
}

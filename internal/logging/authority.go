package logging

import (
	"fmt"
	"io"
	"strings"

	"orchestrator/internal/domain"
)

// Authority prints what each step of the workflow is allowed to do, right after the
// workflow line and before any approval prompt (specs/permissions/SPEC.md §5), in every
// mode.
func (r *ConsoleReporter) Authority(wf domain.Workflow, testCommand string, sandboxed bool) {
	if r.Pretty {
		r.prettyAuthority(wf, testCommand, sandboxed)
		return
	}
	RenderAuthority(r.Out, wf, testCommand, sandboxed)
}

// notSandboxed follows the test command when test.sandbox is none: every run says what
// that costs (specs/hardening/SPEC.md §6.3).
const notSandboxed = "— NOT sandboxed: network, $HOME and your files are reachable"

// RenderAuthority renders the authority line, e.g.
//
//	authority: investigate=read, implement=edit, review=read; test runs: go test ./...
//
// The test step makes no AI call - the orchestrator runs the command itself - so it is
// shown as the command, not as an authority, marked "(sandboxed)" under the sandbox and
// with a warning without it.
func RenderAuthority(w io.Writer, wf domain.Workflow, testCommand string, sandboxed bool) {
	var steps []string
	runsTest := false
	for _, s := range wf.Steps {
		if s.Action == domain.ActionTest {
			runsTest = true
			continue
		}
		steps = append(steps, fmt.Sprintf("%s=%s", s.Action, domain.AuthorityFor(s.Action)))
	}
	line := "authority: " + strings.Join(steps, ", ")
	if runsTest {
		if len(steps) > 0 {
			line += "; "
		}
		if sandboxed {
			line += "test runs (sandboxed): " + testCommand
		} else {
			line += "test runs: " + testCommand + " " + notSandboxed
		}
	}
	fmt.Fprintln(w, line)
}

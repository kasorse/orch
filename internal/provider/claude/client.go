// Package claude adapts the "claude" CLI to the common provider.Client interface.
//
// Every call runs non-interactively (--print) with only the tools its authority grants
// and everything else denied without prompting; the flag sets and the live checks behind
// them are in specs/permissions/SPEC.md §6.2 and §11 (claude 2.1.283).
package claude

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"strings"

	"orchestrator/internal/apperr"
	"orchestrator/internal/domain"
	"orchestrator/internal/execution"
	"orchestrator/internal/provider"
)

const executable = "claude"

// errAuthRetry is the cause the adapter cancels a call with when its stream shows the
// credential was refused (authRetryWatcher).
var errAuthRetry = errors.New("the credential was refused; stopped at the CLI's first retry")

type Client struct {
	Executor execution.ProcessExecutor

	// auth is the credential mode of the process (auth.go); until SetAuth, Execute and
	// CLIVersion refuse to run.
	auth credentialAuth
}

func New(executor execution.ProcessExecutor) *Client {
	return &Client{Executor: executor}
}

// toolSet is what one authority translates to: the built-in tools the model is given
// (--tools; nothing else exists for it) and those of them pre-approved under dontAsk
// (--allowed-tools). Reads inside the working directory need no approval; edits and web
// access do.
type toolSet struct {
	tools   string
	allowed string
}

var toolSets = map[domain.Authority]toolSet{
	domain.AuthorityNone: {tools: ""},
	domain.AuthorityWeb:  {tools: "WebSearch,WebFetch", allowed: "WebSearch,WebFetch"},
	domain.AuthorityRead: {tools: "Read,Grep,Glob"},
	domain.AuthorityEdit: {tools: "Read,Grep,Glob,Edit,Write", allowed: "Edit,Write"},
}

// buildArgs builds the argv for one call. The prompt is not in it: it goes on stdin
// (--input-format text), so no prompt can reach Linux's 128 KiB per-argument limit and the
// command line in an error message is exactly the one that ran. Flags common to every
// authority:
//
//   - --restricted: removes the shell and code-running tools and WebFetch unless --tools
//     names them, ignores user/project/local settings files, confines the file tools to
//     the working directory, and refuses the mode that skips all permission checks.
//   - --permission-mode dontAsk: anything not pre-approved is denied, not prompted. This
//     also covers writes to .git/ and .claude/, which always prompt.
//   - --permission-prompts none: nobody answers prompts. The default ("host") would turn
//     every denial in an unattended run into a hang.
//   - --strict-mcp-config (no MCP config is passed, so no MCP servers) and
//     --setting-sources "": the target repository's hooks, MCP servers and settings never
//     load.
func buildArgs(a domain.Authority, denyRead []string, model, effort string, ext provider.Extensions) []string {
	set := toolSets[a]
	// --restricted also ignores every settings file, so the operator's opt-in to their own
	// settings (plugins, skills, hooks) runs without it. What --restricted otherwise adds
	// is kept by the rest of the argv: --tools still lists the only built-in tools there
	// are (no shell), dontAsk denies anything not pre-approved, and the file tools stay
	// confined to the working directory (verified against claude 2.1.287: a Read of
	// /etc/hosts is denied either way). Only user settings are loaded, never the
	// repository's own .claude/ settings or hooks.
	sources, restricted := "", true
	if ext.UserSettings {
		sources, restricted = "user", false
	}
	argv := []string{
		"--print",
		// One JSON event per line: the answer plus tool calls, usage and rate limits
		// (stream.go). --verbose is required by the CLI for stream-json under --print.
		"--input-format", "text",
		"--output-format", "stream-json",
		"--verbose",
	}
	if restricted {
		argv = append(argv, "--restricted")
	}
	argv = append(argv,
		"--permission-mode", "dontAsk",
		"--permission-prompts", "none",
		"--strict-mcp-config",
		"--setting-sources", sources,
	)
	// Only the servers in this document exist (--strict-mcp-config), and of their tools
	// only the allowlisted ones may run (dontAsk denies the rest).
	if ext.MCPConfig != "" {
		argv = append(argv, "--mcp-config", ext.MCPConfig)
	}
	if len(denyRead) > 0 {
		argv = append(argv, "--settings", denySettings(denyRead))
	}
	argv = append(argv, "--tools", set.tools)
	allowed := set.allowed
	if len(ext.MCPTools) > 0 {
		allowed = strings.Join(append(nonEmptyList(set.allowed), ext.MCPTools...), ",")
	}
	if allowed != "" {
		argv = append(argv, "--allowed-tools", allowed)
	}
	if effort != "" {
		argv = append(argv, "--effort", effort)
	}
	return append(argv, "--model", model)
}

func nonEmptyList(csv string) []string {
	if csv == "" {
		return nil
	}
	return strings.Split(csv, ",")
}

// denySettings renders the read deny list as a --settings JSON document. Each pattern is
// denied at any depth, as a file and as a directory with everything under it - for a
// segment pattern that is what internal/projectctx does, and for a path pattern
// (context.extra_deny "a/b") it is broader than projectctx's root-anchored match, which
// errs on the side of denying. A Read deny rule also blocks Edit and Write on the same
// path. Unlike projectctx, the CLI matches case-sensitively; the patterns arrive
// lowercased, so ".ENV" is not covered.
func denySettings(patterns []string) string {
	rules := make([]string, 0, 2*len(patterns))
	for _, p := range patterns {
		rules = append(rules, "Read(**/"+p+")", "Read(**/"+p+"/**)")
	}
	doc := map[string]map[string][]string{"permissions": {"deny": rules}}
	// Marshalling maps and slices of strings cannot fail.
	data, _ := json.Marshal(doc)
	return string(data)
}

// Supports reports every defined authority as enforceable: each has a flag set verified
// against claude 2.1.283 (specs/permissions/SPEC.md §11). A live check that fails flips
// its authority to unsupported here until fixed.
func (c *Client) Supports(a domain.Authority) bool {
	_, ok := toolSets[a]
	return ok
}

// ReportsCost is true: claude's result event carries the call's cost in USD. It makes *Client
// a provider.CostReporter.
func (c *Client) ReportsCost() bool { return true }

func (c *Client) Execute(ctx context.Context, req provider.Request) (provider.Result, error) {
	// Backstop for config.ValidateAuthorities: a call path that skipped it, or a call site
	// that forgot to set an authority, must not reach the CLI.
	if !c.Supports(req.Authority) {
		return provider.Result{}, &apperr.AuthorityError{
			Provider:  string(provider.Claude),
			Authority: string(req.Authority),
			Reason:    "The request does not carry one of the defined authorities; every call site must set one (specs/permissions/SPEC.md §5).",
		}
	}

	mode, key, apiEnv, err := c.snapshot()
	if err != nil {
		return provider.Result{}, err
	}
	env, _, err := buildEnv(execution.ScrubbedEnv(), mode, key, apiEnv)
	if err != nil {
		return provider.Result{}, err
	}
	redact := redactor(key)

	argv := buildArgs(req.Authority, req.DenyRead, req.Model, req.Effort, req.Extensions)
	spec := execution.Spec{
		Executable: executable,
		Args:       argv,
		Stdin:      req.Prompt,
		WorkingDir: req.WorkingDir,
		Env:        env,
	}
	// What is shown live is redacted too: the key reaches the console only if nothing between
	// the CLI and the terminal removes it.
	stream := req.Stream
	var redactors []*redactWriter
	if stream != nil && key != "" {
		rw := &redactWriter{w: stream, redact: redact}
		stream, redactors = rw, append(redactors, rw)
	}
	flushStream := func() {
		for _, rw := range redactors {
			rw.Flush()
		}
	}
	if req.Stream != nil || req.Activity != nil {
		// The raw stream is JSON; what is shown live is the readable activity.
		var stdoutSink io.Writer = &activityWriter{dir: req.WorkingDir, report: func(what string) {
			if req.Activity != nil {
				req.Activity(what)
			}
			if stream != nil && what != "Thinking" && what != "Writing" {
				_, _ = stream.Write([]byte("→ " + what + "\n"))
			}
		}}
		if key != "" {
			// Redacted before the activity is read from it, so a cut to 60 characters cannot
			// leave part of a key.
			rw := &redactWriter{w: stdoutSink, redact: redact}
			stdoutSink, redactors = rw, append(redactors, rw)
		}
		spec.StdoutSink = stdoutSink
		if stream != nil {
			spec.StderrSink = stream
		}
	}

	// The first auth-category api_retry ends the call; the check below reports it.
	callCtx, stop := context.WithCancelCause(ctx)
	defer stop(nil)
	watcher := &authRetryWatcher{stop: func() { stop(errAuthRetry) }}
	if spec.StdoutSink == nil {
		spec.StdoutSink = watcher
	} else {
		spec.StdoutSink = io.MultiWriter(watcher, spec.StdoutSink)
	}

	res, err := c.Executor.Run(callCtx, spec)
	flushStream()
	stoppedOnAuth := ctx.Err() == nil && errors.Is(context.Cause(callCtx), errAuthRetry)
	if stoppedOnAuth && errors.Is(err, context.Canceled) {
		// Not a cancellation by the caller: nothing above this adapter may take it for one.
		err = nil
	}

	// The key does not leave the adapter: not in the output, the logs the caller writes, the
	// error text, nor the live stream.
	res.Stdout, res.Stderr = redact(res.Stdout), redact(res.Stderr)
	err = redactError(err, redact)

	// The answer is the stream's result event; anything else (an older CLI, a test
	// double) is taken as printed.
	// A call that ended without a result event keeps its raw stream as the output, for the
	// logs, and the usage its messages add up to.
	stdout := res.Stdout
	rep := parseStream(res.Stdout)
	if rep.ok {
		stdout = rep.text
		if stream != nil && rep.text != "" {
			_, _ = stream.Write([]byte(strings.TrimRight(rep.text, "\n") + "\n"))
			flushStream()
		}
	}

	result := provider.Result{
		Output:   stdout,
		Stderr:   res.Stderr,
		ExitCode: res.ExitCode,
		Duration: res.Duration,
		Usage:    rep.usage,
		Call:     rep.call,
	}

	// What the CLI used is checked whatever else the call did, an exit code of 0 and a good
	// answer included: billing the wrong account is not an error the answer shows (SPEC §8).
	// A cancelled or unspawnable call has nothing to check; one stopped on an auth retry is
	// checked like a finished one (err is nil then).
	if err == nil || errors.Is(err, context.DeadlineExceeded) {
		if detail := authFailure(mode, rep.apiKeySource, rep.authErr); detail != "" {
			if stoppedOnAuth {
				detail += " (" + errAuthRetry.Error() + ")"
			}
			return result, &apperr.ProviderError{
				Provider:    string(provider.Claude),
				Model:       req.Model,
				Kind:        apperr.FailureAuth,
				Executable:  executable,
				Args:        argv,
				PromptBytes: len(req.Prompt),
				WorkingDir:  req.WorkingDir,
				ExitCode:    res.ExitCode,
				Duration:    res.Duration,
				Stdout:      stdout,
				Stderr:      res.Stderr,
				Err:         err,
				Detail:      redact(detail),
			}
		}
	}

	if err != nil {
		kind := apperr.FailureSpawn
		switch {
		case errors.Is(err, context.DeadlineExceeded):
			kind = apperr.FailureTimeout
		case errors.Is(err, context.Canceled):
			kind = apperr.FailureCancelled
		}
		// Action is left blank here - the action dispatcher fills it in, since it is
		// the only layer that knows which action invoked this provider call.
		return result, &apperr.ProviderError{
			Provider:    string(provider.Claude),
			Model:       req.Model,
			Kind:        kind,
			Executable:  executable,
			Args:        argv,
			PromptBytes: len(req.Prompt),
			WorkingDir:  req.WorkingDir,
			ExitCode:    res.ExitCode,
			Duration:    res.Duration,
			Stdout:      stdout,
			Stderr:      res.Stderr,
			Err:         err,
		}
	}

	// The account's usage limit refused the call: a reset clears it, so the run can wait for one.
	if rep.usageLimit && (res.ExitCode != 0 || rep.isError) {
		detail := "the claude usage limit was reached"
		if !rep.limitResets.IsZero() {
			detail += "; it resets at " + rep.limitResets.Local().Format("2006-01-02 15:04 MST")
		}
		return result, &apperr.ProviderError{
			Provider:    string(provider.Claude),
			Model:       req.Model,
			Kind:        apperr.FailureRateLimit,
			Executable:  executable,
			Args:        argv,
			PromptBytes: len(req.Prompt),
			WorkingDir:  req.WorkingDir,
			ExitCode:    res.ExitCode,
			Duration:    res.Duration,
			Stdout:      stdout,
			Stderr:      res.Stderr,
			Detail:      detail,
			ResetsAt:    rep.limitResets,
		}
	}

	if res.ExitCode != 0 {
		return result, &apperr.ProviderError{
			Provider:    string(provider.Claude),
			Model:       req.Model,
			Kind:        apperr.FailureExit,
			Executable:  executable,
			Args:        argv,
			PromptBytes: len(req.Prompt),
			WorkingDir:  req.WorkingDir,
			ExitCode:    res.ExitCode,
			Duration:    res.Duration,
			Stdout:      stdout,
			Stderr:      res.Stderr,
			APIStatus:   rep.apiStatus,
		}
	}

	return result, nil
}

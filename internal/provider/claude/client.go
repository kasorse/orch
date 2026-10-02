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
	"fmt"
	"strings"

	"orchestrator/internal/apperr"
	"orchestrator/internal/domain"
	"orchestrator/internal/execution"
	"orchestrator/internal/provider"
)

const executable = "claude"

type Client struct {
	Executor execution.ProcessExecutor
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

// args builds the argv for one call; buildArgs and redactedArgs both go through it, so the
// command line in an error message is always the one that ran. Flags common to every
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
//
// --tools and --allowed-tools take a variable number of values and would swallow a prompt
// that followed them, so the prompt is always last and directly preceded by --model.
func args(a domain.Authority, denyRead []string, model, effort, prompt string) []string {
	set := toolSets[a]
	argv := []string{
		"--print",
		// One JSON event per line: the answer plus tool calls, usage and rate limits
		// (stream.go). --verbose is required by the CLI for stream-json under --print.
		"--output-format", "stream-json",
		"--verbose",
		"--restricted",
		"--permission-mode", "dontAsk",
		"--permission-prompts", "none",
		"--strict-mcp-config",
		"--setting-sources", "",
	}
	if len(denyRead) > 0 {
		argv = append(argv, "--settings", denySettings(denyRead))
	}
	argv = append(argv, "--tools", set.tools)
	if set.allowed != "" {
		argv = append(argv, "--allowed-tools", set.allowed)
	}
	if effort != "" {
		argv = append(argv, "--effort", effort)
	}
	return append(argv, "--model", model, prompt)
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

func buildArgs(a domain.Authority, denyRead []string, model, effort, prompt string) []string {
	return args(a, denyRead, model, effort, prompt)
}

// redactedArgs is buildArgs with the prompt replaced by its length, so
// apperr.ProviderError never has to know or receive the real prompt text.
func redactedArgs(a domain.Authority, denyRead []string, model, effort string, promptLen int) []string {
	return args(a, denyRead, model, effort, fmt.Sprintf("<prompt: %d chars>", promptLen))
}

// Supports reports every defined authority as enforceable: each has a flag set verified
// against claude 2.1.283 (specs/permissions/SPEC.md §11). A live check that fails flips
// its authority to unsupported here until fixed.
func (c *Client) Supports(a domain.Authority) bool {
	_, ok := toolSets[a]
	return ok
}

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

	spec := execution.Spec{
		Executable: executable,
		Args:       buildArgs(req.Authority, req.DenyRead, req.Model, req.Effort, req.Prompt),
		WorkingDir: req.WorkingDir,
		Env:        execution.ScrubbedEnv(),
	}
	if req.Stream != nil || req.Activity != nil {
		// The raw stream is JSON; what is shown live is the readable activity.
		stream := req.Stream
		spec.StdoutSink = &activityWriter{dir: req.WorkingDir, report: func(what string) {
			if req.Activity != nil {
				req.Activity(what)
			}
			if stream != nil && what != "Thinking" && what != "Writing" {
				_, _ = stream.Write([]byte("→ " + what + "\n"))
			}
		}}
		if stream != nil {
			spec.StderrSink = stream
		}
	}

	res, err := c.Executor.Run(ctx, spec)

	// The answer is the stream's result event; anything else (an older CLI, a test
	// double) is taken as printed.
	stdout := res.Stdout
	var usage *domain.Usage
	if text, u, _, ok := parseStream(res.Stdout); ok {
		stdout, usage = text, u
		if req.Stream != nil && text != "" {
			_, _ = req.Stream.Write([]byte(strings.TrimRight(text, "\n") + "\n"))
		}
	}

	result := provider.Result{
		Output:   stdout,
		Stderr:   res.Stderr,
		ExitCode: res.ExitCode,
		Duration: res.Duration,
		Usage:    usage,
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
			Provider:   string(provider.Claude),
			Model:      req.Model,
			Kind:       kind,
			Executable: executable,
			Args:       redactedArgs(req.Authority, req.DenyRead, req.Model, req.Effort, len(req.Prompt)),
			WorkingDir: req.WorkingDir,
			ExitCode:   res.ExitCode,
			Duration:   res.Duration,
			Stdout:     stdout,
			Stderr:     res.Stderr,
			Err:        err,
		}
	}

	if res.ExitCode != 0 {
		return result, &apperr.ProviderError{
			Provider:   string(provider.Claude),
			Model:      req.Model,
			Kind:       apperr.FailureExit,
			Executable: executable,
			Args:       redactedArgs(req.Authority, req.DenyRead, req.Model, req.Effort, len(req.Prompt)),
			WorkingDir: req.WorkingDir,
			ExitCode:   res.ExitCode,
			Duration:   res.Duration,
			Stdout:     stdout,
			Stderr:     res.Stderr,
		}
	}

	return result, nil
}

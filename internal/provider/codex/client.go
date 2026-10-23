// Package codex adapts the non-interactive "codex exec" CLI to provider.Client.
package codex

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"strconv"
	"strings"

	"orchestrator/internal/apperr"
	"orchestrator/internal/domain"
	"orchestrator/internal/execution"
	"orchestrator/internal/provider"
)

const executable = "codex"

type Client struct {
	Executor execution.ProcessExecutor
	auth     credentialAuth
}

func New(executor execution.ProcessExecutor) *Client {
	return &Client{Executor: executor}
}

// Codex can run read-only and workspace-write turns unattended. The CLI's approval policy
// is global, so it is placed before exec. A prompt of "-" makes exec read the prompt from
// stdin and keeps user content out of argv.
func buildArgs(a domain.Authority, denyRead []string, model, effort string, ext provider.Extensions) []string {
	sandbox := map[domain.Authority]string{
		domain.AuthorityRead: "read-only",
		domain.AuthorityEdit: "workspace-write",
	}[a]
	args := []string{
		"--ask-for-approval", "never",
		"exec",
		"--ephemeral",
		"--json",
		"--ignore-user-config",
		"--ignore-rules",
		"--sandbox", sandbox,
	}
	if effort != "" {
		args = append(args, "--config", "model_reasoning_effort="+strconv.Quote(effort))
	}
	args = append(args, codexExtensionArgs(ext)...)
	args = append(args, "--model", model, "-")
	return args
}

func redactedArgs(a domain.Authority, denyRead []string, model, effort string, ext provider.Extensions) []string {
	return buildArgs(a, denyRead, model, effort, ext)
}

// Codex has no --mcp-config flag. Translate the provider-neutral JSON document into
// per-server TOML overrides and keep user/project config out of the invocation.
func codexExtensionArgs(ext provider.Extensions) []string {
	if ext.MCPConfig == "" {
		return nil
	}
	var doc struct {
		Servers map[string]struct {
			Command string            `json:"command"`
			Args    []string          `json:"args"`
			Env     map[string]string `json:"env"`
			URL     string            `json:"url"`
		} `json:"mcpServers"`
	}
	if json.Unmarshal([]byte(ext.MCPConfig), &doc) != nil || len(doc.Servers) == 0 {
		return nil
	}
	names := make([]string, 0, len(doc.Servers))
	for name := range doc.Servers {
		names = append(names, name)
	}
	sort.Strings(names)
	allowed := make(map[string][]string)
	for _, tool := range ext.MCPTools {
		parts := strings.SplitN(tool, "__", 3)
		if len(parts) == 3 && parts[0] == "mcp" && parts[1] != "" && parts[2] != "" {
			allowed[parts[1]] = append(allowed[parts[1]], parts[2])
		}
	}
	var args []string
	for _, name := range names {
		server := doc.Servers[name]
		prefix := "mcp_servers." + name + "."
		if server.Command != "" {
			args = append(args, "--config", prefix+"command="+strconv.Quote(server.Command))
			if len(server.Args) > 0 {
				args = append(args, "--config", prefix+"args="+tomlStrings(server.Args))
			}
		} else if server.URL != "" {
			args = append(args, "--config", prefix+"url="+strconv.Quote(server.URL))
		}
		if len(server.Env) > 0 {
			keys := make([]string, 0, len(server.Env))
			for key := range server.Env {
				keys = append(keys, key)
			}
			sort.Strings(keys)
			parts := make([]string, 0, len(keys))
			for _, key := range keys {
				parts = append(parts, strconv.Quote(key)+" = "+strconv.Quote(server.Env[key]))
			}
			args = append(args, "--config", prefix+"env={"+strings.Join(parts, ", ")+"}")
		}
		if tools := allowed[name]; len(tools) > 0 {
			sort.Strings(tools)
			args = append(args, "--config", prefix+"enabled_tools="+tomlStrings(tools))
		}
	}
	return args
}

func tomlStrings(values []string) string {
	quoted := make([]string, len(values))
	for i, value := range values {
		quoted[i] = strconv.Quote(value)
	}
	return "[" + strings.Join(quoted, ", ") + "]"
}

// Codex cannot provide a tool-free turn or a web-only turn, because exec always exposes its
// shell tool. Its read-only and workspace-write sandboxes are the only authority mappings
// this adapter exposes.
func (c *Client) Supports(a domain.Authority) bool {
	return a == domain.AuthorityRead || a == domain.AuthorityEdit
}

func (c *Client) Execute(ctx context.Context, req provider.Request) (provider.Result, error) {
	if !c.Supports(req.Authority) {
		return provider.Result{}, &apperr.AuthorityError{
			Provider:  string(provider.Codex),
			Authority: string(req.Authority),
			Reason:    "Codex exec cannot enforce a tool-free or web-only authority.",
		}
	}

	mode, key, err := c.snapshot()
	if err != nil {
		return provider.Result{}, err
	}
	env, err := buildEnv(execution.ScrubbedEnv(), mode, key)
	if err != nil {
		return provider.Result{}, err
	}
	redact := redactor(key)
	spec := execution.Spec{
		Executable: executable,
		Args:       buildArgs(req.Authority, req.DenyRead, req.Model, req.Effort, req.Extensions),
		Stdin:      req.Prompt,
		WorkingDir: req.WorkingDir,
		Env:        env,
	}
	var streamWriters []*redactWriter
	if req.Stream != nil || req.Activity != nil {
		activity := &activityWriter{dir: req.WorkingDir, report: func(what string) {
			if req.Activity != nil {
				req.Activity(what)
			}
			if req.Stream != nil && what != "Thinking" && what != "Writing" {
				_, _ = req.Stream.Write([]byte("→ " + what + "\n"))
			}
		}}
		stdout := &redactWriter{w: activity, redact: redact}
		spec.StdoutSink = stdout
		streamWriters = append(streamWriters, stdout)
	}
	if req.Stream != nil {
		stderr := &redactWriter{w: req.Stream, redact: redact}
		streamWriters = append(streamWriters, stderr)
		spec.StderrSink = stderr
	}

	res, runErr := c.Executor.Run(ctx, spec)
	for _, w := range streamWriters {
		w.Flush()
	}
	res.Stdout, res.Stderr = redact(res.Stdout), redact(res.Stderr)
	rep := parseJSON(res.Stdout, req.Model)
	stdout := res.Stdout
	if rep.ok && rep.text != "" {
		stdout = rep.text
	}
	if !rep.usageLimit && isRateLimitText(strings.Join([]string{stdout, res.Stderr}, "\n")) {
		rep.usageLimit = true
	}
	result := provider.Result{
		Output:   stdout,
		Stderr:   res.Stderr,
		ExitCode: res.ExitCode,
		Duration: res.Duration,
		Usage:    rep.usage,
		Call:     rep.call,
	}
	if runErr != nil {
		kind := apperr.FailureSpawn
		switch {
		case errors.Is(runErr, context.DeadlineExceeded):
			kind = apperr.FailureTimeout
		case errors.Is(runErr, context.Canceled):
			kind = apperr.FailureCancelled
		}
		return result, &apperr.ProviderError{
			Provider:   string(provider.Codex),
			Model:      req.Model,
			Kind:       kind,
			Executable: executable,
			Args:       redactedArgs(req.Authority, req.DenyRead, req.Model, req.Effort, req.Extensions),
			WorkingDir: req.WorkingDir,
			ExitCode:   res.ExitCode,
			Duration:   res.Duration,
			Stdout:     stdout,
			Stderr:     res.Stderr,
			Err:        runErr,
		}
	}
	if looksLikeAuthFailure(stdout, res.Stderr) && rep.isError {
		detail := fmt.Sprintf("codex auth (%s): the CLI rejected the credential; check the OpenAI account or use --codex-auth %s", mode, alternateMode(mode))
		return result, &apperr.ProviderError{
			Provider: string(provider.Codex), Model: req.Model, Kind: apperr.FailureAuth,
			Executable: executable, Args: redactedArgs(req.Authority, req.DenyRead, req.Model, req.Effort, req.Extensions),
			WorkingDir: req.WorkingDir, ExitCode: res.ExitCode, Duration: res.Duration,
			Stdout: stdout, Stderr: res.Stderr, Detail: detail, APIStatus: rep.apiStatus,
		}
	}
	if res.ExitCode != 0 {
		kind := apperr.FailureExit
		detail := ""
		if looksLikeAuthFailure(res.Stdout, res.Stderr) {
			kind = apperr.FailureAuth
			detail = fmt.Sprintf("codex auth (%s): the CLI rejected the credential; check the OpenAI account or use --codex-auth %s", mode, alternateMode(mode))
		}
		if rep.usageLimit {
			detail = "the codex usage limit was reached"
			if !rep.resetsAt.IsZero() {
				detail += "; it resets at " + rep.resetsAt.Local().Format("2006-01-02 15:04 MST")
			}
			return result, &apperr.ProviderError{
				Provider: string(provider.Codex), Model: req.Model, Kind: apperr.FailureRateLimit,
				Executable: executable, Args: redactedArgs(req.Authority, req.DenyRead, req.Model, req.Effort, req.Extensions),
				WorkingDir: req.WorkingDir, ExitCode: res.ExitCode, Duration: res.Duration,
				Stdout: stdout, Stderr: res.Stderr, Detail: detail, ResetsAt: rep.resetsAt,
			}
		}
		return result, &apperr.ProviderError{
			Provider:   string(provider.Codex),
			Model:      req.Model,
			Kind:       kind,
			Executable: executable,
			Args:       redactedArgs(req.Authority, req.DenyRead, req.Model, req.Effort, req.Extensions),
			WorkingDir: req.WorkingDir,
			ExitCode:   res.ExitCode,
			Duration:   res.Duration,
			Stdout:     stdout,
			Stderr:     res.Stderr,
			Detail:     detail,
			APIStatus:  rep.apiStatus,
		}
	}
	if rep.usageLimit && rep.isError {
		detail := "the codex usage limit was reached"
		if !rep.resetsAt.IsZero() {
			detail += "; it resets at " + rep.resetsAt.Local().Format("2006-01-02 15:04 MST")
		}
		return result, &apperr.ProviderError{
			Provider: string(provider.Codex), Model: req.Model, Kind: apperr.FailureRateLimit,
			Executable: executable, Args: redactedArgs(req.Authority, req.DenyRead, req.Model, req.Effort, req.Extensions),
			WorkingDir: req.WorkingDir, ExitCode: res.ExitCode, Duration: res.Duration,
			Stdout: stdout, Stderr: res.Stderr, Detail: detail, ResetsAt: rep.resetsAt,
		}
	}
	return result, nil
}

// run is kept as a package-level seam for adapter tests and small callers that exercise the
// process mapping directly. Application calls use Execute after the auth mode is resolved.
func (c *Client) run(ctx context.Context, req provider.Request) (provider.Result, error) {
	if req.Authority == "" {
		req.Authority = domain.AuthorityRead
	}
	c.auth.mu.RLock()
	set := c.auth.set
	c.auth.mu.RUnlock()
	if !set {
		c.SetAuth(domain.CodexAuthLogin, "")
	}
	return c.Execute(ctx, req)
}

func alternateMode(mode string) string {
	if mode == domain.CodexAuthAPI {
		return domain.CodexAuthLogin
	}
	return domain.CodexAuthAPI
}

func looksLikeAuthFailure(output ...string) bool {
	text := strings.ToLower(strings.Join(output, "\n"))
	return isAuthText(text)
}

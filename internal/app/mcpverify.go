package app

import (
	"context"
	"io"
	"path/filepath"
	"slices"
	"strings"

	"orchestrator/internal/apperr"
	"orchestrator/internal/execution"
	"orchestrator/internal/guard"
	"orchestrator/internal/repository"
	"orchestrator/internal/testexec"
	"orchestrator/internal/verify"
)

const mcpVerifyUsage = "usage: orchestrator mcp-verify --dir <absolute path>"

// mcpVerifyArgs parses what follows "mcp-verify": --dir X or --dir=X, an absolute path, and
// nothing else. The subcommand is hidden - it is not in the usage text - and is started
// only by the implement session's MCP client (specs/quality/SPEC.md §6.4).
func mcpVerifyArgs(args []string) (string, error) {
	dir := ""
	for i := 0; i < len(args); i++ {
		switch v, ok := strings.CutPrefix(args[i], "--dir="); {
		case ok && dir == "":
			dir = v
		case args[i] == "--dir" && dir == "" && i+1 < len(args):
			i++
			dir = args[i]
		default:
			return "", &apperr.ConfigError{Msg: mcpVerifyUsage}
		}
	}
	if dir == "" {
		return "", &apperr.ConfigError{Msg: mcpVerifyUsage}
	}
	if !filepath.IsAbs(dir) {
		return "", &apperr.ConfigError{Msg: mcpVerifyUsage + " (--dir must be absolute)"}
	}
	return dir, nil
}

// runMCPVerify serves the verify tools on stdin and stdout until stdin ends. It reads only
// dir/verify.json: no config file and no run directory. stdout is the MCP channel, so
// nothing else may be written to it; diagnostics go to stderr.
func runMCPVerify(ctx context.Context, dir string, stdin io.Reader, stdout, stderr io.Writer) error {
	spec, err := verify.ReadSpec(dir)
	if err != nil {
		return &apperr.ConfigError{Msg: "mcp-verify", Err: err}
	}
	executor := execution.NewOSProcessExecutor()
	repo, err := repository.Open(ctx, spec.RepoRoot, executor)
	if err != nil {
		return err
	}
	// The sandbox is mandatory for the test tools (ReadSpec refuses a spec with a test command
	// and without one; without a test command the runner is never reached), and the environment
	// is the one the orchestrator computed, never rebuilt from this process's own. An empty
	// list stays empty rather than meaning "inherit".
	env := slices.Clone(spec.TestEnv)
	if env == nil {
		env = []string{}
	}
	runner := &testexec.Runner{Executor: executor, Sandbox: spec.Sandbox, BaseEnv: env}
	// In a worktree run the before-state names the .git pointer, so every verify call checks
	// it before anything runs git (specs/worktree/SPEC.md §8.3).
	g := &guard.RepoGuard{Repo: repo, Patterns: spec.Before.Patterns(), GitPointer: spec.Before.GitPointer()}
	tools, err := verify.NewTools(spec, dir, runner, g)
	if err != nil {
		return &apperr.ConfigError{Msg: "mcp-verify", Err: err}
	}
	tools.Log = stderr
	tools.Files = repo
	server := &verify.Server{Tools: tools, Version: Version, Log: stderr}
	return server.Serve(ctx, stdin, stdout)
}

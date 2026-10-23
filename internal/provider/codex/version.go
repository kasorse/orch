package codex

import (
	"context"
	"fmt"
	"regexp"
	"strconv"
	"strings"
	"time"

	"orchestrator/internal/execution"
)

const versionTimeout = 10 * time.Second

var codexVersionRE = regexp.MustCompile(`(?:codex(?:-cli)?\s+)?(\d+)\.(\d+)\.(\d+)(?:\s|$)`)

// Codex is currently published as a 0.x CLI. The adapter only relies on the stable exec
// flags and JSONL protocol, so the verified major range intentionally covers the 0.x line.
func (c *Client) VerifiedRange() string { return ">=0.1.0 <1.0.0" }

func (c *Client) CLIVersion(ctx context.Context) (string, bool, error) {
	env, err := c.env()
	if err != nil {
		return "", false, err
	}
	ctx, cancel := context.WithTimeout(ctx, versionTimeout)
	defer cancel()
	res, err := c.Executor.Run(ctx, execution.Spec{Executable: executable, Args: []string{"--version"}, Env: env})
	if err != nil {
		return "", false, fmt.Errorf("%s --version: %w", executable, err)
	}
	if res.ExitCode != 0 {
		return "", false, fmt.Errorf("%s --version exited with code %d", executable, res.ExitCode)
	}
	line, _, _ := strings.Cut(strings.TrimSpace(res.Stdout), "\n")
	if len(line) > 64 {
		line = line[:64]
	}
	match := codexVersionRE.FindStringSubmatch(line)
	if len(match) != 4 {
		return line, false, nil
	}
	for i := 1; i < len(match); i++ {
		if _, err := strconv.Atoi(match[i]); err != nil {
			return line, false, nil
		}
	}
	return line, match[1] == "0", nil
}

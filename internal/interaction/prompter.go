// Package interaction implements the interactive approve/edit/mode-select flow and the
// manual-mode per-step gate (MVP.md §1/§27). It depends only on domain and workflow (for
// workflow.Build/Gate/GateDecision) and reuses internal/logging for rendering, so it stays
// a leaf composition point just like internal/action - never imported by internal/workflow.
package interaction

import (
	"bufio"
	"fmt"
	"io"
	"strings"
)

// Prompter asks the user a single-line question and returns their trimmed answer.
type Prompter interface {
	Ask(prompt string) (string, error)
}

// TerminalPrompter reads lines from an injected reader (normally os.Stdin) and writes
// prompts to an injected writer (normally os.Stdout).
type TerminalPrompter struct {
	in  *bufio.Reader
	out io.Writer
}

func NewTerminalPrompter(in io.Reader, out io.Writer) *TerminalPrompter {
	return &TerminalPrompter{in: bufio.NewReader(in), out: out}
}

// Ask writes prompt (no trailing newline) then reads one line. A final line with no
// trailing newline is accepted as valid input; only an error with no data read is
// treated as a failure, wrapped with guidance toward --mode auto for unattended use.
func (p *TerminalPrompter) Ask(prompt string) (string, error) {
	fmt.Fprint(p.out, prompt)
	line, err := p.in.ReadString('\n')
	if err != nil && line == "" {
		return "", fmt.Errorf("no interactive input available (stdin closed or non-interactive); pass --mode auto for unattended runs: %w", err)
	}
	return strings.TrimSpace(line), nil
}

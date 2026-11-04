package interaction

import (
	"context"
	"fmt"
	"io"
	"strings"
	"unicode/utf8"

	"github.com/kasorse/orch/internal/domain"
)

// openMark is what an operator types to leave a point open.
const openMark = "-"

// Clarifier is the operator's side of the clarify step (specs/clarify, Approach §3-§4): it shows
// the points the model found unclear with the answers it proposed, reads the operator's answers,
// and reads an addition to the task when the re-check still rates it unclear. Every question
// waits on the context it is given - the run's user context, without execution.timeout - and a
// closed stdin or Ctrl-C cancels the run.
type Clarifier struct {
	p   Prompter
	out io.Writer
}

func NewClarifier(p Prompter, out io.Writer) *Clarifier {
	return &Clarifier{p: p, out: out}
}

// Answer shows the points under the header "the task is not clear enough (P(clear) p < threshold)"
// and asks about each one: Enter keeps the model's answer (by docs) or leaves a point it did not
// answer open, text replaces the answer (by operator), "-" leaves the point open. The returned
// points are in the order they came in.
func (c *Clarifier) Answer(ctx context.Context, pClear, threshold float64, points []domain.ClarifyPoint) ([]domain.ClarifyPoint, error) {
	noun := "questions"
	if len(points) == 1 {
		noun = "question"
	}
	fmt.Fprintf(c.out, "\nThe task is not clear enough (P(clear) %.2f < %.2f). %d %s:\n", pClear, threshold, len(points), noun)

	out := make([]domain.ClarifyPoint, len(points))
	for i, p := range points {
		fmt.Fprintf(c.out, "\n%d. %s\n", i+1, oneLine(p.Question))
		hint := "- leaves it open"
		if p.Answer != "" {
			from := "docs"
			if p.Source != "" {
				from = "from " + oneLine(p.Source)
			}
			fmt.Fprintf(c.out, "   %s: %s\n", from, oneLine(p.Answer))
			hint = "Enter keeps it, " + hint
		}
		answer, err := c.ask(ctx, fmt.Sprintf("   answer [%s]: ", hint))
		if err != nil {
			return nil, err
		}
		switch {
		case answer == "":
			// Enter keeps what there is: the model's answer, or nothing.
		case answer == openMark:
			p.Answer, p.By, p.Source = "", "", ""
		default:
			p.Answer, p.By, p.Source = cutBytes(answer, domain.MaxClarifyFieldBytes), domain.ClarifyByOperator, ""
		}
		out[i] = p
	}
	return out, nil
}

// Add shows that the task is still rated unclear, with the points that are open, and reads an
// addition ended by an empty line. An empty addition ("" returned) means the operator has nothing
// to add.
func (c *Clarifier) Add(ctx context.Context, pClear, threshold float64, open []domain.ClarifyPoint) (string, error) {
	fmt.Fprintf(c.out, "\nThe task is still rated unclear (P(clear) %.2f < %.2f).\n", pClear, threshold)
	if len(open) > 0 {
		fmt.Fprintln(c.out, "Open points:")
		for _, p := range open {
			fmt.Fprintf(c.out, "  - %s\n", oneLine(p.Question))
		}
	}
	fmt.Fprintln(c.out, "Add what is missing, ended by an empty line (an empty addition stops the run):")

	var lines []string
	for {
		line, err := c.ask(ctx, "> ")
		if err != nil {
			return "", err
		}
		if line == "" {
			break
		}
		lines = append(lines, line)
	}
	return cutBytes(strings.Join(lines, "\n"), domain.MaxClarifyAdditionBytes), nil
}

// ask reads one line. EOF or a cancelled context ends the run as a user cancellation (exit 5),
// never as a provider failure. The read runs beside the wait on ctx, so Ctrl-C cancels a question
// still waiting for its line; the read left behind ends with the process, as the run does.
func (c *Clarifier) ask(ctx context.Context, prompt string) (string, error) {
	if ctx.Err() != nil {
		return "", fmt.Errorf("clarification cancelled: %w", context.Canceled)
	}
	type reply struct {
		answer string
		err    error
	}
	// Buffered: a read that finishes after the cancellation must not block forever.
	done := make(chan reply, 1)
	go func() {
		answer, err := c.p.Ask(prompt)
		done <- reply{answer, err}
	}()
	select {
	case <-ctx.Done():
		return "", fmt.Errorf("clarification cancelled: %w", context.Canceled)
	case r := <-done:
		if r.err != nil {
			return "", fmt.Errorf("clarification cancelled: %w (%w)", context.Canceled, r.err)
		}
		if ctx.Err() != nil {
			return "", fmt.Errorf("clarification cancelled: %w", context.Canceled)
		}
		return r.answer, nil
	}
}

// cutBytes cuts s to at most n bytes without splitting a character.
func cutBytes(s string, n int) string {
	if len(s) <= n {
		return s
	}
	for n > 0 && !utf8.RuneStart(s[n]) {
		n--
	}
	return strings.TrimSpace(s[:n])
}

// oneLine collapses s to a single line.
func oneLine(s string) string { return strings.Join(strings.Fields(s), " ") }

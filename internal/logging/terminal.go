package logging

import (
	"fmt"
	"io"
	"strings"
)

// Terminal hand-off: the spinner owns the current line while it runs, so anything else
// that writes to the terminal (a prompt, the manual-choice menu, the gate's change list)
// must stop it first. PausingWriter and PausingPrompter do that, and the prompter also
// gives questions the pretty renderer's look.

// PausingWriter returns Out wrapped so every write first stops a running spinner.
func (r *ConsoleReporter) PausingWriter() io.Writer {
	if !r.Pretty {
		return r.Out
	}
	return pausingWriter{r}
}

type pausingWriter struct{ r *ConsoleReporter }

func (p pausingWriter) Write(b []byte) (int, error) {
	p.r.stopSpin()
	return p.r.Out.Write(b)
}

// StreamWriter is where --verbose tees a provider CLI's live output: "  | " prefixed lines
// in plain mode, dim "│" prefixed lines in the pretty renderer.
func (r *ConsoleReporter) StreamWriter() io.Writer {
	if !r.Pretty {
		return NewPrefixWriter(r.Out, "  | ")
	}
	return NewPrefixWriter(r.PausingWriter(), "     "+r.st().gray("│")+" ")
}

// Asker is the one method of interaction.Prompter, restated so logging does not import
// interaction.
type Asker interface {
	Ask(prompt string) (string, error)
}

// PausingPrompter wraps a prompter so the spinner stops before each question and, in the
// pretty renderer, the question is styled: leading blank lines kept, then "? " and bold.
func (r *ConsoleReporter) PausingPrompter(p Asker) Asker {
	if !r.Pretty {
		return p
	}
	return pausingPrompter{r: r, inner: p}
}

type pausingPrompter struct {
	r     *ConsoleReporter
	inner Asker
}

func (p pausingPrompter) Ask(prompt string) (string, error) {
	p.r.stopSpin()
	// The terminal echoes the typed answer and Enter itself, which can scroll; the
	// sidebar steps aside for the question and comes back after it.
	if p.r.dock != nil {
		p.r.dock.hide()
		defer p.r.dock.show()
	}
	return p.inner.Ask(p.r.stylePrompt(prompt))
}

func (r *ConsoleReporter) stylePrompt(prompt string) string {
	st := r.st()
	trimmed := strings.TrimLeft(prompt, "\n")
	lead := prompt[:len(prompt)-len(trimmed)]
	body := strings.TrimRight(trimmed, " ")
	tail := trimmed[len(body):]
	return lead + st.accent("?") + " " + st.bold(body) + tail
}

// Answer prints a read-only workflow's final answer.
func (r *ConsoleReporter) Answer(answer string) {
	r.stopSpin()
	if !r.Pretty {
		fmt.Fprintf(r.Out, "\nAnswer:\n%s\n", answer)
		return
	}
	st := r.st()
	fmt.Fprintln(r.Out)
	r.bullet(st.green, "Answer", "")
	rich{st: st, width: termWidth(), cwd: r.Cwd}.block(r.Out, "  ", answer)
}

// PlanApproved reports that plan mode stops after approval.
func (r *ConsoleReporter) PlanApproved() {
	if !r.Pretty {
		fmt.Fprintln(r.Out, "\nPlan approved. Not executing (plan mode).")
		return
	}
	st := r.st()
	fmt.Fprintf(r.Out, "\n%s %s\n", st.green("✓ Plan approved."), st.gray("Not executing (plan mode)."))
}

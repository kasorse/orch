package logging

import (
	"fmt"
	"io"
	"strings"

	"github.com/kasorse/orch/internal/domain"
	"github.com/kasorse/orch/internal/workflow"
)

// testOutputTailLines is how much of a failed test command's output the step result shows;
// the full output is in the step's log files.
const testOutputTailLines = 20

// resultFormat is how a step result is laid out: plain text (the spec's format, byte for
// byte) or the pretty renderer's coloured variant of the same layout.
type resultFormat struct {
	st     style
	bullet string
	// rich is set in the pretty renderer: values are highlighted and wrapped.
	rich *rich
}

func (r *ConsoleReporter) resultFormat() resultFormat {
	if r.Pretty {
		st := style{on: true}
		return resultFormat{st: st, bullet: st.gray("•"), rich: &rich{st: st, width: termWidth(), cwd: r.Cwd}}
	}
	return resultFormat{bullet: "-"}
}

// printResult renders what a completed step produced - the same content as its artifact
// file, as labelled plain text instead of JSON. Empty fields are left out; a step with
// nothing to show prints nothing.
func (r *ConsoleReporter) printResult(exec domain.StepExecution, outcome workflow.StepOutcome) {
	if exec.Status != domain.StepCompleted {
		return
	}
	f := r.resultFormat()
	var b strings.Builder
	switch a := outcome.Artifact.(type) {
	case domain.ResearchArtifact:
		f.writeText(&b, "In short", a.SummaryShort)
		f.writeText(&b, "Summary", a.Summary)
		f.writeList(&b, "Findings", a.Findings)
		f.writeList(&b, "Options", a.Options)
		f.writeText(&b, "Recommendation", a.Recommendation)
		f.writeList(&b, "References", a.References)
		f.writeText(&b, "Answer", a.Answer)
	case domain.InvestigationArtifact:
		f.writeText(&b, "In short", a.SummaryShort)
		f.writeText(&b, "Summary", a.Summary)
		f.writeText(&b, "Root cause", a.RootCause)
		f.writeNumbered(&b, "Steps", a.Steps)
		f.writeList(&b, "Affected files", a.AffectedFiles)
		f.writeList(&b, "Risks", a.Risks)
		f.writeText(&b, "Answer", a.Answer)
	case domain.ImplementationArtifact:
		f.writeText(&b, "In short", a.SummaryShort)
		f.writeText(&b, "Summary", a.Summary)
		f.writeList(&b, "Changed files", a.ChangedFiles)
		f.writeList(&b, "Tests added", a.TestsAdded)
	}
	if review := exec.ReviewResult; review != nil {
		f.writeText(&b, "In short", review.SummaryShort)
		f.writeText(&b, "Summary", review.Summary)
		issues := make([]string, 0, len(review.Issues))
		for _, issue := range review.Issues {
			line := fmt.Sprintf("[%s] %s", issue.Severity, issue.Description)
			if issue.File != "" {
				line = fmt.Sprintf("[%s] %s: %s", issue.Severity, issue.File, issue.Description)
			}
			if f.rich != nil {
				// The severity tag is coloured here; the file is rendered as a `code` path.
				line = f.severity(issue.Severity) + "\x00" + issue.Description
				if issue.File != "" {
					line = f.severity(issue.Severity) + "\x00`" + issue.File + "`: " + issue.Description
				}
			}
			issues = append(issues, line)
		}
		f.writeList(&b, "Issues", issues)
	}
	if test := exec.TestResult; test != nil && !test.Success {
		if !f.st.on { // the pretty renderer already shows the exit code on the step's line
			fmt.Fprintf(&b, "  %s %d (%s)\n", f.label("Exit code"), test.ExitCode, formatDuration(test.Duration))
		}
		output := strings.TrimRight(strings.Join(nonEmpty(test.Stdout, test.Stderr), "\n"), "\n")
		if output != "" {
			lines := strings.Split(output, "\n")
			label := "Output"
			if len(lines) > testOutputTailLines {
				lines = lines[len(lines)-testOutputTailLines:]
				label = fmt.Sprintf("Output (last %d lines)", testOutputTailLines)
			}
			if f.rich != nil {
				// Raw command output: shown verbatim in a gutter, never re-flowed.
				fmt.Fprintf(&b, "  %s\n", f.label(label))
				for _, l := range lines {
					l = strings.TrimRight(strings.ReplaceAll(l, "\t", "  "), " \r")
					if room := termWidth() - 7; visibleWidth(l) > room {
						l = truncateVisible(ansiRE.ReplaceAllString(l, ""), max(room, 10))
					}
					fmt.Fprintf(&b, "    %s %s\n", f.st.gray("│"), f.st.gray(l))
				}
			} else {
				f.writeText(&b, label, strings.Join(lines, "\n"))
			}
		}
	}
	if b.Len() == 0 {
		return
	}
	if r.Pretty {
		fmt.Fprintf(r.Out, "\n%s", b.String())
		return
	}
	fmt.Fprintf(r.Out, "\nResult:\n%s", b.String())
}

func (f resultFormat) label(l string) string {
	return f.st.bold(l + ":")
}

func (f resultFormat) severity(s domain.Severity) string {
	tag := "[" + string(s) + "]"
	switch s {
	case domain.SeverityCritical:
		return f.st.bold(f.st.red(tag))
	case domain.SeverityHigh:
		return f.st.red(tag)
	case domain.SeverityMedium:
		return f.st.yellow(tag)
	default:
		return f.st.gray(tag)
	}
}

// writeText renders a single-line value after its label and a multi-line one indented
// below it.
func (f resultFormat) writeText(w io.Writer, label, value string) {
	value = strings.TrimSpace(value)
	if value == "" {
		return
	}
	if f.rich != nil {
		if !strings.Contains(value, "\n") && !bulletRE.MatchString(value) && !numberRE.MatchString(value) {
			f.rich.para(w, "  "+f.label(label)+" ", "  ", value)
			return
		}
		fmt.Fprintf(w, "  %s\n", f.label(label))
		f.rich.block(w, "    ", value)
		return
	}
	if !strings.Contains(value, "\n") {
		fmt.Fprintf(w, "  %s %s\n", f.label(label), value)
		return
	}
	fmt.Fprintf(w, "  %s\n", f.label(label))
	for _, line := range strings.Split(value, "\n") {
		fmt.Fprintf(w, "    %s\n", strings.TrimRight(line, " \t\r"))
	}
}

func (f resultFormat) writeList(w io.Writer, label string, items []string) {
	f.writeItems(w, label, items, func(int) string { return f.bullet })
}

func (f resultFormat) writeNumbered(w io.Writer, label string, items []string) {
	f.writeItems(w, label, items, func(i int) string { return f.st.gray(fmt.Sprintf("%d.", i+1)) })
}

// writeItems renders one item per line under its label, continuation lines of a
// multi-line item aligned with the item's text.
func (f resultFormat) writeItems(w io.Writer, label string, items []string, marker func(int) string) {
	items = nonEmpty(items...)
	if len(items) == 0 {
		return
	}
	fmt.Fprintf(w, "  %s\n", f.label(label))
	for i, item := range items {
		m := marker(i)
		if f.rich != nil {
			first := "    " + m + " "
			cont := "    " + strings.Repeat(" ", visibleWidth(m)+1)
			// A pre-styled prefix (a review issue's severity) is split off by "\x00".
			if pre, rest, ok := strings.Cut(item, "\x00"); ok {
				first += pre + " "
				item = rest
			}
			lines := strings.Split(item, "\n")
			f.rich.para(w, first, cont, strings.TrimRight(lines[0], " \t\r"))
			if len(lines) > 1 {
				f.rich.block(w, cont, strings.Join(lines[1:], "\n"))
			}
			continue
		}
		lines := strings.Split(item, "\n")
		fmt.Fprintf(w, "    %s %s\n", m, strings.TrimRight(lines[0], " \t\r"))
		for _, line := range lines[1:] {
			fmt.Fprintf(w, "    %s %s\n", strings.Repeat(" ", visibleWidth(m)), strings.TrimRight(line, " \t\r"))
		}
	}
}

func nonEmpty(values ...string) []string {
	out := make([]string, 0, len(values))
	for _, v := range values {
		if v = strings.TrimSpace(v); v != "" {
			out = append(out, v)
		}
	}
	return out
}

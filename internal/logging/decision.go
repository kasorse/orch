package logging

import (
	"fmt"
	"io"
	"strings"
	"time"

	"orchestrator/internal/domain"
)

// Decided prints the decision summary once classification is done (TASKS.md T11):
// translation, context depth, complexity with the choice/score comparison, the provider
// that decided, and the workflow; --verbose adds the per-stage table.
func (r *ConsoleReporter) Decided(t domain.DecisionTrace) {
	RenderDecision(r.Out, t, r.Verbose)
}

// RenderDecision is Decided without a reporter, for golden tests and reuse.
func RenderDecision(w io.Writer, t domain.DecisionTrace, verbose bool) {
	tr := t.Translation
	switch {
	case tr.Translated:
		fmt.Fprintf(w, "translation: %s → en via %s/%s (%s)\n", tr.Detected, tr.Provider, tr.Model, formatDuration(time.Duration(tr.LatencyMS)*time.Millisecond))
	case tr.Failed:
		fmt.Fprintf(w, "Warning: translation failed (%s); sending the original text\n", tr.Reason)
	}

	if d := t.ContextDepth; d != nil {
		cmp := "<"
		if d.Probability >= d.Threshold {
			cmp = "≥"
		}
		fmt.Fprintf(w, "context: %s (p=%.2f %s %.2f)", d.Selected, d.Probability, cmp, d.Threshold)
		if t.ContextLimited {
			fmt.Fprintf(w, ", limited: %s", firstLine(t.ContextError))
		}
		fmt.Fprintln(w)
	}

	fmt.Fprintf(w, "complexity: %s\n", complexityLine(t))

	if t.Source == domain.DecisionSourceUser {
		fmt.Fprintf(w, "decision: chosen by user (%s)\n", firstLine(t.DecisionError))
	} else {
		model := t.Model
		if model == "" {
			model = "?"
		}
		fmt.Fprintf(w, "decision: %s (%s)\n", t.Provider, model)
	}

	names := make([]string, len(t.Workflow))
	for i, a := range t.Workflow {
		names[i] = string(a)
	}
	fmt.Fprintf(w, "workflow: %s\n", strings.Join(names, " → "))

	if verbose {
		fmt.Fprintln(w, "stages:")
		for _, s := range t.Stages {
			state := "off"
			if s.Enabled {
				state = "on"
			}
			fmt.Fprintf(w, "  %-12s %-3s  %s\n", s.Stage, state, s.Reason)
		}
	}
}

// complexityLine renders the selected complexity and what each question said, primary
// first, e.g. "medium (choice p=0.63, score=1.20 → medium ✓)". A disagreeing shadow is
// flagged, since that is what the choice-vs-score experiment is watching for.
func complexityLine(t domain.DecisionTrace) string {
	c := t.Complexity
	if t.Source == domain.DecisionSourceUser {
		return fmt.Sprintf("%s (chosen by user)", c.Selected)
	}
	parts := []string{sourceSummary(c.Primary, c.Sources[c.Primary], c.Selected)}
	for _, src := range []domain.ComplexitySource{domain.ComplexitySourceChoice, domain.ComplexitySourceScore} {
		out, ok := c.Sources[src]
		if src == c.Primary || !ok {
			continue
		}
		mark := "✓"
		if out.Selected != c.Selected {
			mark = "✗ disagrees"
		}
		parts = append(parts, fmt.Sprintf("%s → %s %s", sourceSummary(src, out, out.Selected), out.Selected, mark))
	}
	return fmt.Sprintf("%s (%s)", c.Selected, strings.Join(parts, ", "))
}

func sourceSummary(src domain.ComplexitySource, out domain.ComplexitySourceOutcome, selected domain.Complexity) string {
	if src == domain.ComplexitySourceScore && out.Decision.Score != nil {
		return fmt.Sprintf("score=%.2f", *out.Decision.Score)
	}
	return fmt.Sprintf("%s p=%.2f", src, out.Decision.Probabilities[selected])
}

func firstLine(s string) string {
	if i := strings.IndexByte(s, '\n'); i >= 0 {
		return s[:i]
	}
	return s
}

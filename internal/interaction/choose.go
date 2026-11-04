package interaction

import (
	"context"
	"fmt"
	"io"
	"strconv"
	"strings"

	"github.com/kasorse/orch/internal/domain"
)

// ManualChoice is what the user picked when the decision provider failed (TASKS.md
// В9). It is raw input: the caller merges it with the forced stages via
// policy.ApplyForced(Complexity, Stages, Excluded, policy), which is also what records
// it in the trace as selected by the user.
type ManualChoice struct {
	Complexity domain.Complexity
	// Stages are the stages the user picked.
	Stages []domain.Stage
	// Excluded are forced stages the user explicitly switched off after a warning.
	Excluded []domain.Stage
}

// Chooser asks the user for complexity and stages in place of a failed decision
// provider (T16).
type Chooser struct {
	p   Prompter
	out io.Writer
	// interactive is false without a TTY or in --mode auto: then there is nobody to
	// ask, and Choose fails without reading stdin.
	interactive bool
}

func NewChooser(p Prompter, out io.Writer, interactive bool) *Chooser {
	return &Chooser{p: p, out: out, interactive: interactive}
}

// Choose shows why the provider failed - and the collected project context, if the
// pre-check got that far (project may be nil) - then asks for a complexity and the
// stages. Forced stages for the chosen complexity are pre-selected and can only be
// switched off with an explicit confirmation, so policy.stages.force keeps its force.
//
// Errors: without an interactive terminal, cause is returned (so the exit code still
// follows the provider failure) with instructions to rerun interactively. EOF on stdin
// or Ctrl-C cancels the run.
func (c *Chooser) Choose(ctx context.Context, cause error, project *domain.ProjectContext, policy domain.DecisionPolicy) (ManualChoice, error) {
	if !c.interactive {
		return ManualChoice{}, fmt.Errorf("%w\n\nThe decision provider failed, and choosing complexity and stages by hand needs an "+
			"interactive terminal. Rerun in a terminal without --mode auto to choose them yourself, "+
			"or fix the provider and rerun", cause)
	}

	fmt.Fprintf(c.out, "\nDecision provider failed: %s\n", firstLine(cause))
	fmt.Fprintln(c.out, "Choose the task's complexity and workflow stages yourself.")
	if project != nil {
		fmt.Fprintf(c.out, "\n%s\n", describeProject(project))
	}

	complexity, err := c.askComplexity(ctx)
	if err != nil {
		return ManualChoice{}, err
	}
	stages, excluded, err := c.askStages(ctx, complexity, policy)
	if err != nil {
		return ManualChoice{}, err
	}
	return ManualChoice{Complexity: complexity, Stages: stages, Excluded: excluded}, nil
}

func (c *Chooser) askComplexity(ctx context.Context) (domain.Complexity, error) {
	order := domain.ComplexityOrder()
	var opts []string
	for i, cx := range order {
		opts = append(opts, fmt.Sprintf("[%d] %s", i+1, cx))
	}
	fmt.Fprintf(c.out, "\nComplexity:\n  %s\n", strings.Join(opts, "  "))
	for {
		answer, err := c.ask(ctx, fmt.Sprintf("Complexity [1-%d]: ", len(order)))
		if err != nil {
			return "", err
		}
		if n, err := strconv.Atoi(answer); err == nil && n >= 1 && n <= len(order) {
			return order[n-1], nil
		}
		if cx, err := domain.ParseComplexity(strings.ToLower(answer)); err == nil {
			return cx, nil
		}
		fmt.Fprintf(c.out, "Please enter a number from 1 to %d.\n", len(order))
	}
}

func (c *Chooser) askStages(ctx context.Context, complexity domain.Complexity, policy domain.DecisionPolicy) ([]domain.Stage, []domain.Stage, error) {
	order := domain.StageOrder()
	forced := map[domain.Stage]bool{}
	for _, st := range policy.ForceStages[complexity] {
		forced[st] = true
	}

	fmt.Fprintln(c.out, "\nStages (run in this order):")
	for i, st := range order {
		mark := ""
		if forced[st] {
			mark = fmt.Sprintf("  * forced for complexity=%s", complexity)
		}
		fmt.Fprintf(c.out, "  [%d] %s%s\n", i+1, st, mark)
	}
	hint := "Stage numbers, comma-separated"
	if len(forced) > 0 {
		hint += " (* stages are included; -N excludes one)"
	}

	for {
		answer, err := c.ask(ctx, hint+": ")
		if err != nil {
			return nil, nil, err
		}
		picked, unpicked, err := parseStages(answer, order)
		if err != nil {
			fmt.Fprintf(c.out, "%v\n", err)
			continue
		}

		var excluded []domain.Stage
		for _, st := range unpicked {
			if !forced[st] {
				continue // not selected anyway
			}
			fmt.Fprintf(c.out, "Warning: %s is forced for complexity=%s by policy.stages.force.\n", st, complexity)
			ok, err := c.confirm(ctx, fmt.Sprintf("Exclude %s anyway? [y/N]: ", st))
			if err != nil {
				return nil, nil, err
			}
			if ok {
				excluded = append(excluded, st)
			}
		}

		if !anyEnabled(order, picked, forced, excluded) {
			fmt.Fprintln(c.out, "Select at least one stage.")
			continue
		}
		return picked, excluded, nil
	}
}

// parseStages reads "2, 4 review -6": numbers or names pick a stage, a leading minus
// asks to exclude one.
func parseStages(answer string, order []domain.Stage) (picked, excluded []domain.Stage, err error) {
	seen := map[domain.Stage]bool{}
	for _, tok := range strings.FieldsFunc(answer, func(r rune) bool { return r == ',' || r == ' ' || r == '\t' }) {
		exclude := strings.HasPrefix(tok, "-")
		name := strings.TrimPrefix(tok, "-")
		var st domain.Stage
		if n, convErr := strconv.Atoi(name); convErr == nil {
			if n < 1 || n > len(order) {
				return nil, nil, fmt.Errorf("No stage %d: enter numbers from 1 to %d.", n, len(order))
			}
			st = order[n-1]
		} else if parsed, parseErr := domain.ParseStage(strings.ToLower(name)); parseErr == nil {
			st = parsed
		} else {
			return nil, nil, fmt.Errorf("Unknown stage %q: enter numbers from 1 to %d.", tok, len(order))
		}
		if seen[st] {
			continue
		}
		seen[st] = true
		if exclude {
			excluded = append(excluded, st)
		} else {
			picked = append(picked, st)
		}
	}
	return picked, excluded, nil
}

func anyEnabled(order, picked []domain.Stage, forced map[domain.Stage]bool, excluded []domain.Stage) bool {
	if len(picked) > 0 {
		return true
	}
	off := map[domain.Stage]bool{}
	for _, st := range excluded {
		off[st] = true
	}
	for _, st := range order {
		if forced[st] && !off[st] {
			return true
		}
	}
	return false
}

func (c *Chooser) confirm(ctx context.Context, prompt string) (bool, error) {
	answer, err := c.ask(ctx, prompt)
	if err != nil {
		return false, err
	}
	a := strings.ToLower(answer)
	return a == "y" || a == "yes", nil
}

// ask reads one answer. EOF or a cancelled context ends the run as a user
// cancellation (exit 5), never as a provider failure.
func (c *Chooser) ask(ctx context.Context, prompt string) (string, error) {
	if err := ctx.Err(); err != nil {
		return "", fmt.Errorf("manual choice cancelled: %w", context.Canceled)
	}
	answer, err := c.p.Ask(prompt)
	if err != nil {
		return "", fmt.Errorf("manual choice cancelled: %w (%w)", context.Canceled, err)
	}
	if err := ctx.Err(); err != nil {
		return "", fmt.Errorf("manual choice cancelled: %w", context.Canceled)
	}
	return answer, nil
}

// describeProject is the short context summary shown before the questions, so the user
// decides with the same facts the provider would have had.
func describeProject(p *domain.ProjectContext) string {
	var b strings.Builder
	b.WriteString("Project context:")
	if p.Language != "" {
		fmt.Fprintf(&b, " %s", p.Language)
		if p.ProjectType != "" {
			fmt.Fprintf(&b, " (%s)", p.ProjectType)
		}
	}
	tests := "no tests"
	switch {
	case p.TestInfo.UnitTests && p.TestInfo.IntegrationTests:
		tests = "unit and integration tests"
	case p.TestInfo.IntegrationTests:
		tests = "integration tests"
	case p.TestInfo.Exists:
		tests = "unit tests"
	}
	fmt.Fprintf(&b, ", %s", tests)
	if p.Git.Branch != "" {
		fmt.Fprintf(&b, ", branch %s", p.Git.Branch)
	}
	if len(p.RelevantFiles) > 0 {
		b.WriteString("\nRelevant files:")
		for i, f := range p.RelevantFiles {
			if i == 5 {
				fmt.Fprintf(&b, "\n  ... and %d more", len(p.RelevantFiles)-5)
				break
			}
			fmt.Fprintf(&b, "\n  %s", f.Path)
		}
	}
	return b.String()
}

func firstLine(err error) string {
	if err == nil {
		return "unknown error"
	}
	s := err.Error()
	if i := strings.IndexByte(s, '\n'); i >= 0 {
		return s[:i]
	}
	return s
}

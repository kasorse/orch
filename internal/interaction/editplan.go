package interaction

import (
	"fmt"
	"io"
	"strings"

	"orchestrator/internal/domain"
	"orchestrator/internal/logging"
	"orchestrator/internal/workflow"
)

// editLoop lets the user change one or more Classification fields, rebuilding
// run.Workflow via the unmodified workflow.BuildLegacy after every change - never a
// freehand step list, per MVP.md §30. When allowTypeAndComplexity is false (the
// manual-mode step-gate decline path), only requires_tests/requires_review are
// offered, since type/complexity have no well-defined effect once steps have already
// executed.
func editLoop(p Prompter, out io.Writer, run *domain.Run, allowTypeAndComplexity bool) error {
	for {
		prompt := "Edit which field? [1] requires_tests / [2] requires_review / [d]one: "
		if allowTypeAndComplexity {
			prompt = "Edit which field? [t]ype / [c]omplexity / [1] requires_tests / [2] requires_review / [d]one: "
		}
		ans, err := p.Ask(prompt)
		if err != nil {
			return err
		}

		switch strings.ToLower(ans) {
		case "d", "done", "":
			return nil

		case "t", "type":
			if !allowTypeAndComplexity {
				fmt.Fprintln(out, "unrecognized option")
				continue
			}
			if err := editType(p, out, run); err != nil {
				return err
			}

		case "c", "complexity":
			if !allowTypeAndComplexity {
				fmt.Fprintln(out, "unrecognized option")
				continue
			}
			if err := editComplexity(p, out, run); err != nil {
				return err
			}

		case "1", "requires_tests":
			if err := editBoolField(p, out, run, "requires_tests", func(c *domain.Classification, v bool) { c.RequiresTests = v }); err != nil {
				return err
			}

		case "2", "requires_review":
			if err := editBoolField(p, out, run, "requires_review", func(c *domain.Classification, v bool) { c.RequiresReview = v }); err != nil {
				return err
			}

		default:
			fmt.Fprintln(out, "unrecognized option")
		}
	}
}

func editType(p Prompter, out io.Writer, run *domain.Run) error {
	ans, err := p.Ask("New type (question/bugfix/feature/refactor/test/review): ")
	if err != nil {
		return err
	}
	return applyEdit(out, run, func(c domain.Classification) domain.Classification {
		c.Type = domain.TaskType(ans)
		return c
	})
}

func editComplexity(p Prompter, out io.Writer, run *domain.Run) error {
	ans, err := p.Ask("New complexity (low/medium/high): ")
	if err != nil {
		return err
	}
	return applyEdit(out, run, func(c domain.Classification) domain.Classification {
		c.Complexity = domain.LegacyComplexity(ans)
		return c
	})
}

func editBoolField(p Prompter, out io.Writer, run *domain.Run, label string, set func(*domain.Classification, bool)) error {
	ans, err := p.Ask(fmt.Sprintf("%s (y/n): ", label))
	if err != nil {
		return err
	}
	v, err := parseYesNo(ans)
	if err != nil {
		fmt.Fprintln(out, err)
		return nil
	}
	return applyEdit(out, run, func(c domain.Classification) domain.Classification {
		set(&c, v)
		return c
	})
}

func parseYesNo(s string) (bool, error) {
	switch strings.ToLower(s) {
	case "y", "yes":
		return true, nil
	case "n", "no":
		return false, nil
	default:
		return false, fmt.Errorf("unrecognized answer %q, expected y or n", s)
	}
}

// applyEdit validates candidate against domain.Classification.Validate(), and on
// success records the pre-edit classification (once) into run.OriginalClassification,
// applies the edit, and rebuilds run.Workflow.
func applyEdit(out io.Writer, run *domain.Run, edit func(domain.Classification) domain.Classification) error {
	candidate := edit(*run.Classification)
	if err := candidate.Validate(); err != nil {
		fmt.Fprintln(out, err)
		return nil
	}

	if run.OriginalClassification == nil {
		original := *run.Classification
		run.OriginalClassification = &original
	}
	*run.Classification = candidate

	wf, err := workflow.BuildLegacy(candidate)
	if err != nil {
		fmt.Fprintln(out, err)
		return nil
	}
	run.Workflow = &wf

	logging.NewConsoleReporter(out).WorkflowBuilt(wf)
	return nil
}

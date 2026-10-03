package workflow

import "orchestrator/internal/domain"

// Gaps names the earlier stages a workflow skips that a selected stage normally builds
// on (specs/stage-select/SPEC.md §8), one line per affected stage in workflow order. It
// only informs: it never changes what runs. testing and review never warn - testing
// runs the command on the current tree and review reads the real git diff - and
// research is nobody's predecessor.
func Gaps(wf domain.Workflow) []string {
	var out []string
	for _, step := range wf.Steps {
		switch step.Action {
		case domain.ActionPlan:
			if !wf.Contains(domain.ActionAnalyze) {
				out = append(out, "planning runs without analysis; the plan will rely on the task text alone")
			}
		case domain.ActionImplement:
			switch {
			case !wf.Contains(domain.ActionPlan) && !wf.Contains(domain.ActionAnalyze):
				out = append(out, "development runs without a plan or analysis; it works from the task text alone")
			case !wf.Contains(domain.ActionPlan):
				out = append(out, "development runs without a plan; it works from the task text and the analysis alone")
			}
		}
	}
	return out
}

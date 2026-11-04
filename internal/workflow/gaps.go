package workflow

import "github.com/kasorse/orch/internal/domain"

// Gaps names the earlier stages a workflow skips that a selected stage normally builds
// on (specs/stage-select/SPEC.md §8), one line per affected stage in workflow order. It
// only informs: it never changes what runs. testing and review never warn - testing
// runs the command on the current tree and review reads the real git diff - and
// research is nobody's predecessor.
func Gaps(wf domain.Workflow) []string {
	var out []string
	for _, step := range wf.Steps {
		if step.Action == domain.ActionImplement && !wf.Contains(domain.ActionInvestigate) {
			out = append(out, "development runs without an investigation; it works from the task text alone")
		}
	}
	return out
}

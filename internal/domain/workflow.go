package domain

type ActionType string

const (
	ActionInvestigate ActionType = "investigate"
	ActionImplement   ActionType = "implement"
	ActionTest        ActionType = "test"
	ActionReview      ActionType = "review"
	ActionResearch    ActionType = "research"
)

// Valid reports whether a is one of the defined actions.
func (a ActionType) Valid() bool {
	switch a {
	case ActionResearch, ActionInvestigate, ActionImplement, ActionTest, ActionReview:
		return true
	}
	return false
}

type Step struct {
	Action ActionType `json:"action"`
	// Stage names the decision stage this step executes (specs/JEV integration/
	// TASKS.md T04).
	Stage Stage `json:"stage"`
}

// Workflow is the deterministic linear pipeline built by workflow.Build (or, for the
// pre-Jev flow, workflow.BuildLegacy) from a stage selection. It is used to look up
// whether test/review are part of this run (RequiresTest/RequiresReview) - the
// engine's state machine decides actual traversal order (including backward loops),
// it does not walk Steps as a queue.
type Workflow struct {
	Steps []Step `json:"steps"`
}

func (w Workflow) Contains(a ActionType) bool {
	for _, s := range w.Steps {
		if s.Action == a {
			return true
		}
	}
	return false
}

func (w Workflow) RequiresTest() bool   { return w.Contains(ActionTest) }
func (w Workflow) RequiresReview() bool { return w.Contains(ActionReview) }

// LimitsView decouples the workflow engine from the config package's YAML structures.
type LimitsView struct {
	MaxSteps         int
	MaxFixIterations int
	// StopWithoutProgress stops the fix loop when an iteration changes nothing or repeats the
	// previous failure (specs/quality/SPEC.md §8.1). The zero value is off, so a view built
	// by hand keeps the plain max_fix_iterations behavior.
	StopWithoutProgress bool
	// MaxCostUSD stops the run before the next step once its steps have cost this much
	// (specs/governance/SPEC.md §5). 0 means no budget.
	MaxCostUSD float64
}

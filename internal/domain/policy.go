package domain

// DecisionPolicy is the deterministic rule set the Policy Engine (internal/policy,
// specs/JEV integration/TASKS.md T03) applies to a decision provider's raw
// probabilities to pick a ContextDepth, a Complexity, and the enabled stages. It
// decouples the Policy Engine from config's YAML structures the same way LimitsView
// decouples the workflow engine (see workflow.go) - internal/policy may import only
// domain, so config.Config.DecisionPolicy() builds this value for it.
type DecisionPolicy struct {
	// ProjectContextThreshold: the pre-check selects ContextDepthProject when
	// P(project) is at least this.
	ProjectContextThreshold float64

	// ComplexityThreshold[c] is the minimum cumulative probability of c and every more
	// complex option that selects c or higher (TASKS.md В2: walked from the top down,
	// so ComplexityComplex is checked before ComplexityMedium). ComplexitySimple has
	// no entry - it is the default when neither Medium's nor Complex's threshold is
	// met.
	ComplexityThreshold map[Complexity]float64

	// StageThreshold[s] is the minimum probability that enables s on its own.
	StageThreshold map[Stage]float64

	// ForceStages[c] lists stages that are enabled whenever c is the selected
	// complexity, regardless of their own probability (SPEC.md §12).
	ForceStages map[Complexity][]Stage
}

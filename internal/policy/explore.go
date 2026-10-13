package policy

import (
	"fmt"
	"hash/fnv"
	"slices"

	"orchestrator/internal/domain"
)

// exploreEps absorbs the float error of a probability that sits exactly one band below its
// threshold (0.5 - 0.05 is not always 0.45).
const exploreEps = 1e-9

// exploreBuckets is the modulus of the draw: a stage is explored when its hash is below
// rate x exploreBuckets.
const exploreBuckets = 1000

// Explore turns on, for a share of runs, the stages the policy left just below their threshold
// (specs/governance/SPEC.md §7.4). A stage that is off, not forced, and has
// threshold - band <= P < threshold is turned on when fnv64a(seed + "/" + stage) % 1000 is below
// rate x 1000; it is recorded as explored, with the reason "explored (P 0.47, threshold 0.50)".
// Exploration only turns stages on: an enabled or forced stage is never touched, and neither is
// development, which is the task itself and would bring testing with it
// (specs/decision-shape/SPEC.md §6.5).
//
// The draw depends on nothing but seed, which app sets to the run id, so the same decision and
// run id give the same workflow. A decision made by hand (--stage) has no probabilities and is
// returned as it was, as is any decision when p.Explore is nil. dec is not modified.
func Explore(dec domain.Decision, p domain.DecisionPolicy, seed string) domain.Decision {
	if p.Explore == nil || dec.Source == domain.DecisionSourceManual {
		return dec
	}
	stages := slices.Clone(dec.Stages.Stages)
	for i, so := range stages {
		if so.Enabled || so.Forced || so.Probability == nil || so.Threshold == nil || so.Stage == domain.StageDevelopment {
			continue
		}
		prob, threshold := *so.Probability, *so.Threshold
		if prob >= threshold || prob < threshold-p.Explore.Band-exploreEps {
			continue
		}
		h := fnv.New64a()
		_, _ = h.Write([]byte(seed + "/" + string(so.Stage)))
		if float64(h.Sum64()%exploreBuckets) >= p.Explore.Rate*exploreBuckets {
			continue
		}
		so.Enabled = true
		so.Explored = true
		so.Reason = fmt.Sprintf("explored (P %.2f, threshold %.2f)", prob, threshold)
		stages[i] = so
	}
	dec.Stages.Stages = stages
	return dec
}

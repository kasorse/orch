// Package decision defines the decision provider abstraction (specs/JEV integration/
// SPEC.md §10) and the questions every provider answers. Adapters live in
// subpackages (jev; an LLM adapter in T22); the Classification Service depends only on
// Provider, never on an adapter.
package decision

import (
	"context"

	"github.com/kasorse/orch/internal/domain"
)

// Provider answers the two decision rounds. It returns probability distributions only:
// selecting a depth, a complexity, and the enabled stages from them is the policy's job.
//
// Failures are *apperr.DecisionError, so the caller can tell a provider outage (manual
// choice, TASKS.md В9) from a cancelled run without knowing which adapter it talks to.
type Provider interface {
	// Name identifies the provider in traces and errors, e.g. "jev" or "claude".
	Name() string
	// Source is what the decision trace records as having decided.
	Source() domain.DecisionSource
	// ContextDepth is the pre-check: does classifying request need repository context? Its
	// answer also carries P(clear), the clarity question asked in the same call
	// (specs/clarify), so a clear task costs no extra call.
	ContextDepth(ctx context.Context, request string) (domain.ContextDepthDecision, error)
	// Clarity is the clarity question asked alone, on request and the rendered clarification
	// so far (empty when there is none): the re-check after a clarification.
	Clarity(ctx context.Context, request, clarification string) (domain.ClarityDecision, error)
	// Classify returns the complexity distribution(s) and every stage's probability.
	Classify(ctx context.Context, input domain.ClassificationInput) (domain.TaskClassification, error)
}

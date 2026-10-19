package jev

import (
	"errors"
	"fmt"
	"math"
	"sort"
	"strconv"
	"strings"

	"orchestrator/internal/apperr"
	"orchestrator/internal/decision"
	"orchestrator/internal/domain"
)

// The mapper trusts nothing in a response: every expected answer must be present with
// the right type and exactly our options, and any deviation is an InvalidResponse. The
// `choice` field of a choice answer is ignored - the policy chooses, not Jev.

func mapContextDepth(resp systemOneResponse) (domain.ContextDepthDecision, error) {
	q := decision.ContextDepthQuestion()
	if err := checkEnvelope(resp, []string{q.ID}); err != nil {
		return domain.ContextDepthDecision{}, err
	}
	probs, conf, err := choiceDistribution(resp.Answers[q.ID], q)
	if err != nil {
		return domain.ContextDepthDecision{}, invalid(err)
	}
	d := domain.ContextDepthDecision{Probabilities: make(map[domain.ContextDepth]float64, len(probs)), Confidence: conf}
	for k, p := range probs {
		d.Probabilities[domain.ContextDepth(k)] = p
	}
	if err := d.Validate(); err != nil {
		return domain.ContextDepthDecision{}, invalid(err)
	}
	return d, nil
}

func mapClassification(resp systemOneResponse, input domain.ClassificationInput, sources []domain.ComplexitySource) (domain.TaskClassification, error) {
	choiceQ := decision.ComplexityChoiceQuestion()
	scoreQ := decision.ComplexityScoreQuestion()
	stageQs := decision.StageQuestions()

	var ids []string
	for _, src := range sources {
		switch src {
		case domain.ComplexitySourceChoice:
			ids = append(ids, choiceQ.ID)
		case domain.ComplexitySourceScore:
			ids = append(ids, scoreQ.ID)
		}
	}
	for _, q := range stageQs {
		ids = append(ids, q.ID)
	}
	scopeQ := decision.ScopeQuestion()
	if !input.SkipScope {
		ids = append(ids, scopeQ.ID)
	}
	if err := checkEnvelope(resp, ids); err != nil {
		return domain.TaskClassification{}, err
	}

	tc := domain.TaskClassification{
		ContextDepth:   input.ContextDepth,
		ContextLimited: input.ContextLimited,
		Complexity:     make(map[domain.ComplexitySource]domain.ComplexityDecision, len(sources)),
		Provider:       ProviderName,
		Model:          resp.Model,
	}

	for _, src := range sources {
		var (
			d   domain.ComplexityDecision
			err error
		)
		switch src {
		case domain.ComplexitySourceChoice:
			d, err = complexityFromChoice(resp.Answers[choiceQ.ID], choiceQ)
		case domain.ComplexitySourceScore:
			d, err = complexityFromScore(resp.Answers[scoreQ.ID], scoreQ)
		}
		if err != nil {
			return domain.TaskClassification{}, invalid(err)
		}
		tc.Complexity[src] = d
	}

	for _, q := range stageQs {
		p, err := noulProbability(resp.Answers[q.ID], q.ID)
		if err != nil {
			return domain.TaskClassification{}, invalid(err)
		}
		tc.Stages.Set(q.Stage, domain.ProbabilityDecision{Probability: p})
	}

	if !input.SkipScope {
		p, err := noulProbability(resp.Answers[scopeQ.ID], scopeQ.ID)
		if err != nil {
			return domain.TaskClassification{}, invalid(err)
		}
		tc.Scope = &domain.ScopeDecision{Story: p}
	}

	if err := tc.Validate(); err != nil {
		return domain.TaskClassification{}, invalid(err)
	}
	return tc, nil
}

// checkEnvelope requires a reported model and exactly the answers asked for.
func checkEnvelope(resp systemOneResponse, ids []string) error {
	if strings.TrimSpace(resp.Model) == "" {
		return invalid(errors.New("response has no model version"))
	}
	want := make(map[string]bool, len(ids))
	for _, id := range ids {
		want[id] = true
		if _, ok := resp.Answers[id]; !ok {
			return invalid(fmt.Errorf("answer %q is missing", id))
		}
	}
	for _, id := range sortedKeys(resp.Answers) {
		if !want[id] {
			return invalid(fmt.Errorf("unexpected answer %q", id))
		}
	}
	return nil
}

func choiceDistribution(a answer, q decision.ChoiceQuestion) (map[string]float64, float64, error) {
	if a.Type != questionChoice {
		return nil, 0, fmt.Errorf("answer %q has type %q, want %q", q.ID, a.Type, questionChoice)
	}
	want := make([]string, len(q.Options))
	for i, o := range q.Options {
		want[i] = o.Value
	}
	if err := exactKeys(a.Probabilities, want); err != nil {
		return nil, 0, fmt.Errorf("answer %q probabilities: %w", q.ID, err)
	}
	if a.Confidence == nil {
		return nil, 0, fmt.Errorf("answer %q has no confidence", q.ID)
	}
	return a.Probabilities, *a.Confidence, nil
}

func complexityFromChoice(a answer, q decision.ChoiceQuestion) (domain.ComplexityDecision, error) {
	probs, conf, err := choiceDistribution(a, q)
	if err != nil {
		return domain.ComplexityDecision{}, err
	}
	d := domain.ComplexityDecision{Probabilities: make(map[domain.Complexity]float64, len(probs)), Confidence: conf}
	for k, p := range probs {
		d.Probabilities[domain.Complexity(k)] = p
	}
	return d, nil
}

// complexityFromScore maps level indexes "0".."n-1" onto domain.ComplexityOrder. The
// legend must echo our levels in order: a legend that differs means Jev scored a scale
// other than the one we asked about, so the indexes cannot be trusted.
func complexityFromScore(a answer, q decision.ScoreQuestion) (domain.ComplexityDecision, error) {
	if a.Type != questionScore {
		return domain.ComplexityDecision{}, fmt.Errorf("answer %q has type %q, want %q", q.ID, a.Type, questionScore)
	}
	order := domain.ComplexityOrder()
	if len(q.Levels) != len(order) {
		return domain.ComplexityDecision{}, fmt.Errorf("score question has %d levels, domain has %d complexities", len(q.Levels), len(order))
	}
	indexes := make([]string, len(order))
	for i := range order {
		indexes[i] = strconv.Itoa(i)
	}
	if err := exactKeys(a.Legend, indexes); err != nil {
		return domain.ComplexityDecision{}, fmt.Errorf("answer %q legend: %w", q.ID, err)
	}
	for i, level := range q.Levels {
		if got := strings.TrimSpace(a.Legend[indexes[i]]); got != level {
			return domain.ComplexityDecision{}, fmt.Errorf("answer %q legend[%d] is %q, want %q", q.ID, i, got, level)
		}
	}
	if err := exactKeys(a.Probabilities, indexes); err != nil {
		return domain.ComplexityDecision{}, fmt.Errorf("answer %q probabilities: %w", q.ID, err)
	}
	if a.Confidence == nil {
		return domain.ComplexityDecision{}, fmt.Errorf("answer %q has no confidence", q.ID)
	}
	if a.Score == nil {
		return domain.ComplexityDecision{}, fmt.Errorf("answer %q has no score", q.ID)
	}

	d := domain.ComplexityDecision{
		Probabilities: make(map[domain.Complexity]float64, len(order)),
		Confidence:    *a.Confidence,
		Score:         a.Score,
	}
	for i, c := range order {
		d.Probabilities[c] = a.Probabilities[indexes[i]]
	}
	return d, nil
}

func noulProbability(a answer, id string) (float64, error) {
	if a.Type != questionNoul {
		return 0, fmt.Errorf("answer %q has type %q, want %q", id, a.Type, questionNoul)
	}
	if a.Noul == nil {
		return 0, fmt.Errorf("answer %q has no noul value", id)
	}
	if p := *a.Noul; math.IsNaN(p) || p < 0 || p > 1 {
		return 0, fmt.Errorf("answer %q noul %v outside [0, 1]", id, p)
	}
	return *a.Noul, nil
}

// exactKeys requires m's keys to be exactly want.
func exactKeys[V any](m map[string]V, want []string) error {
	for _, k := range want {
		if _, ok := m[k]; !ok {
			return fmt.Errorf("missing %q", k)
		}
	}
	if len(m) != len(want) {
		allowed := make(map[string]bool, len(want))
		for _, k := range want {
			allowed[k] = true
		}
		var extra []string
		for k := range m {
			if !allowed[k] {
				extra = append(extra, k)
			}
		}
		sort.Strings(extra)
		return fmt.Errorf("unexpected %s", strings.Join(quoteAll(extra), ", "))
	}
	return nil
}

func quoteAll(ss []string) []string {
	out := make([]string, len(ss))
	for i, s := range ss {
		out[i] = strconv.Quote(s)
	}
	return out
}

func invalid(err error) error {
	var derr *apperr.DecisionError
	if errors.As(err, &derr) {
		return err
	}
	return &apperr.DecisionError{Provider: ProviderName, Kind: apperr.DecisionInvalidResponse, Err: err}
}

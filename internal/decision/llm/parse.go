package llm

import (
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"sort"
	"strconv"
	"strings"

	"orchestrator/internal/decision"
	"orchestrator/internal/domain"
)

// sumTolerance is wider than Jev's (domain.distributionTolerance): an LLM writes
// rounded decimals, so 0.33+0.33+0.33 is an honest distribution. Within it, the
// distribution is normalised to sum to exactly 1.
const sumTolerance = 0.05

type contextAnswer map[string]map[string]float64

type classifyAnswer struct {
	Complexity      map[string]float64 `json:"complexity"`
	ComplexityScore map[string]float64 `json:"complexity_score"`
	Stages          map[string]float64 `json:"stages"`
	Scope           *float64           `json:"scope"`
}

func parseContextDepth(output string) (domain.ContextDepthDecision, error) {
	raw, err := extractJSON(output)
	if err != nil {
		return domain.ContextDepthDecision{}, err
	}
	var ans contextAnswer
	if err := json.Unmarshal([]byte(raw), &ans); err != nil {
		return domain.ContextDepthDecision{}, fmt.Errorf("decode answer: %w", err)
	}
	q := decision.ContextDepthQuestion()
	probs, err := distribution(q.ID, ans[q.ID], optionValues(q))
	if err != nil {
		return domain.ContextDepthDecision{}, err
	}
	d := domain.ContextDepthDecision{Probabilities: map[domain.ContextDepth]float64{}, Confidence: maxOf(probs)}
	for k, p := range probs {
		d.Probabilities[domain.ContextDepth(k)] = p
	}
	return d, d.Validate()
}

func parseClassification(output string, input domain.ClassificationInput, sources []domain.ComplexitySource, providerID, model string) (domain.TaskClassification, error) {
	raw, err := extractJSON(output)
	if err != nil {
		return domain.TaskClassification{}, err
	}
	var ans classifyAnswer
	dec := json.NewDecoder(strings.NewReader(raw))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&ans); err != nil {
		return domain.TaskClassification{}, fmt.Errorf("decode answer: %w", err)
	}

	tc := domain.TaskClassification{
		ContextDepth:   input.ContextDepth,
		ContextLimited: input.ContextLimited,
		Complexity:     map[domain.ComplexitySource]domain.ComplexityDecision{},
		Provider:       providerID,
		Model:          model,
	}
	asked := map[domain.ComplexitySource]bool{}
	for _, src := range sources {
		asked[src] = true
		switch src {
		case domain.ComplexitySourceChoice:
			q := decision.ComplexityChoiceQuestion()
			probs, err := distribution(q.ID, ans.Complexity, optionValues(q))
			if err != nil {
				return domain.TaskClassification{}, err
			}
			d := domain.ComplexityDecision{Probabilities: map[domain.Complexity]float64{}, Confidence: maxOf(probs)}
			for k, p := range probs {
				d.Probabilities[domain.Complexity(k)] = p
			}
			tc.Complexity[src] = d
		case domain.ComplexitySourceScore:
			q := decision.ComplexityScoreQuestion()
			order := domain.ComplexityOrder()
			indexes := make([]string, len(q.Levels))
			for i := range indexes {
				indexes[i] = strconv.Itoa(i)
			}
			probs, err := distribution(q.ID, ans.ComplexityScore, indexes)
			if err != nil {
				return domain.TaskClassification{}, err
			}
			d := domain.ComplexityDecision{Probabilities: map[domain.Complexity]float64{}, Confidence: maxOf(probs)}
			score := 0.0
			for i, c := range order {
				p := probs[indexes[i]]
				d.Probabilities[c] = p
				score += float64(i) * p
			}
			d.Score = &score
			tc.Complexity[src] = d
		}
	}
	if !asked[domain.ComplexitySourceChoice] && ans.Complexity != nil {
		return domain.TaskClassification{}, errors.New(`answer has "complexity", which was not asked`)
	}
	if !asked[domain.ComplexitySourceScore] && ans.ComplexityScore != nil {
		return domain.TaskClassification{}, errors.New(`answer has "complexity_score", which was not asked`)
	}

	var stageNames []string
	for _, q := range decision.StageQuestions() {
		stageNames = append(stageNames, string(q.Stage))
	}
	if err := exactKeys(stagesKey, ans.Stages, stageNames); err != nil {
		return domain.TaskClassification{}, err
	}
	for _, q := range decision.StageQuestions() {
		p := ans.Stages[string(q.Stage)]
		if math.IsNaN(p) || p < 0 || p > 1 {
			return domain.TaskClassification{}, fmt.Errorf("stage %s probability %v outside [0, 1]", q.Stage, p)
		}
		tc.Stages.Set(q.Stage, domain.ProbabilityDecision{Probability: p})
	}

	switch {
	case input.SkipScope && ans.Scope != nil:
		return domain.TaskClassification{}, errors.New(`answer has "scope", which was not asked`)
	case !input.SkipScope && ans.Scope == nil:
		return domain.TaskClassification{}, errors.New(`answer "scope" is missing`)
	case !input.SkipScope:
		if p := *ans.Scope; math.IsNaN(p) || p < 0 || p > 1 {
			return domain.TaskClassification{}, fmt.Errorf("scope probability %v outside [0, 1]", p)
		}
		tc.Scope = &domain.ScopeDecision{Story: *ans.Scope}
	}

	return tc, tc.Validate()
}

// distribution checks that probs has exactly want's keys, each in [0, 1], summing to
// 1 within sumTolerance, and returns it normalised to sum to exactly 1.
func distribution(id string, probs map[string]float64, want []string) (map[string]float64, error) {
	if err := exactKeys(id, probs, want); err != nil {
		return nil, err
	}
	sum := 0.0
	for _, k := range want {
		p := probs[k]
		if math.IsNaN(p) || p < 0 || p > 1 {
			return nil, fmt.Errorf("%s: probability of %q is %v, outside [0, 1]", id, k, p)
		}
		sum += p
	}
	if math.Abs(sum-1) > sumTolerance {
		return nil, fmt.Errorf("%s: probabilities sum to %.3f, want 1", id, sum)
	}
	out := make(map[string]float64, len(want))
	for _, k := range want {
		out[k] = probs[k] / sum
	}
	return out, nil
}

func exactKeys(id string, m map[string]float64, want []string) error {
	if m == nil {
		return fmt.Errorf("answer %q is missing", id)
	}
	allowed := map[string]bool{}
	for _, k := range want {
		allowed[k] = true
		if _, ok := m[k]; !ok {
			return fmt.Errorf("%s: missing %q", id, k)
		}
	}
	var extra []string
	for k := range m {
		if !allowed[k] {
			extra = append(extra, strconv.Quote(k))
		}
	}
	if len(extra) > 0 {
		sort.Strings(extra)
		return fmt.Errorf("%s: unexpected %s", id, strings.Join(extra, ", "))
	}
	return nil
}

func optionValues(q decision.ChoiceQuestion) []string {
	out := make([]string, len(q.Options))
	for i, o := range q.Options {
		out[i] = o.Value
	}
	return out
}

func maxOf(m map[string]float64) float64 {
	best := 0.0
	for _, p := range m {
		best = max(best, p)
	}
	return best
}

var errNoJSON = errors.New("no JSON object in the answer")

// extractJSON finds the first top-level {...} object in s, tolerant of fences or prose
// around it (like the classifier's and actions' extractors), skipping braces inside
// JSON strings.
func extractJSON(s string) (string, error) {
	start, depth := -1, 0
	inString, escaped := false, false
	for i, r := range s {
		if inString {
			switch {
			case escaped:
				escaped = false
			case r == '\\':
				escaped = true
			case r == '"':
				inString = false
			}
			continue
		}
		switch r {
		case '"':
			if depth > 0 {
				inString = true
			}
		case '{':
			if depth == 0 {
				start = i
			}
			depth++
		case '}':
			if depth > 0 {
				depth--
				if depth == 0 {
					return s[start : i+1], nil
				}
			}
		}
	}
	return "", errNoJSON
}

package jev

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"regexp"
	"sort"
	"strconv"
	"strings"

	"orchestrator/internal/apperr"
	"orchestrator/internal/decision"
)

// MockModelPrefix marks the model reported by the mock client, so a decision trace
// produced offline can never be mistaken for a real Jev answer.
const MockModelPrefix = "mock:"

// Mock answer strengths: a matched signal answers "strong", an unmatched one "weak".
const (
	mockStrong = 0.85
	mockWeak   = 0.15
)

// mockClient stands in for Jev while there is no API access. It sees exactly what
// would go over the wire (the request is round-tripped through JSON), rejects the
// malformed questions Jev would reject with a 422, and answers deterministically from
// keyword heuristics on state.request. The numbers are plausible, not calibrated: they
// exist so the rest of the pipeline can run end to end, not to evaluate thresholds.
type mockClient struct{}

// NewMockClient returns the offline stand-in for Jev (decision.jev.mock: true).
func NewMockClient() Client { return mockClient{} }

// wireRequest is systemOneRequest as the server sees it: criteria still undecoded.
type wireRequest struct {
	Model     string                  `json:"model"`
	State     json.RawMessage         `json:"state"`
	Questions map[string]wireQuestion `json:"questions"`
}

type wireQuestion struct {
	Type         string          `json:"type"`
	Instructions string          `json:"instructions"`
	Criteria     json.RawMessage `json:"criteria"`
}

func (mockClient) decide(ctx context.Context, req systemOneRequest) (systemOneResponse, error) {
	if err := mockContextError(ctx); err != nil {
		return systemOneResponse{}, err
	}

	body, err := json.Marshal(req)
	if err != nil {
		return systemOneResponse{}, mockBadRequest(fmt.Errorf("encode request: %w", err))
	}
	var wire wireRequest
	if err := json.Unmarshal(body, &wire); err != nil {
		return systemOneResponse{}, mockBadRequest(fmt.Errorf("decode request: %w", err))
	}
	if strings.TrimSpace(wire.Model) == "" {
		return systemOneResponse{}, mockBadRequest(errors.New("model is required"))
	}
	if len(wire.Questions) == 0 {
		return systemOneResponse{}, mockBadRequest(errors.New("at least one question is required"))
	}

	sig := analyzeRequest(mockRequestText(wire.State))
	answers := make(map[string]answer, len(wire.Questions))
	for _, id := range sortedKeys(wire.Questions) {
		a, err := mockAnswer(id, wire.Questions[id], sig)
		if err != nil {
			return systemOneResponse{}, mockBadRequest(fmt.Errorf("questions.%s: %w", id, err))
		}
		answers[id] = a
	}

	return systemOneResponse{
		Model:   MockModelPrefix + wire.Model,
		Answers: answers,
		Usage:   usage{InputTokens: len(body) / 4, OutputTokens: 8 * len(answers)},
	}, nil
}

func (mockClient) listModels(ctx context.Context) ([]modelInfo, error) {
	if err := mockContextError(ctx); err != nil {
		return nil, err
	}
	return []modelInfo{{
		Name:        "jev-latest",
		Description: "offline mock of the Jev decision model",
		ReleaseDate: "2026-01-01",
	}}, nil
}

func mockAnswer(id string, q wireQuestion, sig requestSignals) (answer, error) {
	if strings.TrimSpace(q.Instructions) == "" {
		return answer{}, errors.New("instructions are required")
	}
	switch q.Type {
	case questionChoice:
		var criteria map[string]json.RawMessage
		if err := json.Unmarshal(q.Criteria, &criteria); err != nil || len(criteria) == 0 {
			return answer{}, errors.New("choice criteria must be a non-empty object of option -> description")
		}
		if len(criteria) > 255 {
			return answer{}, fmt.Errorf("choice supports at most 255 options, got %d", len(criteria))
		}
		return choiceAnswer(sig.choiceDistribution(sortedKeys(criteria))), nil

	case questionScore:
		var levels []json.RawMessage
		if err := json.Unmarshal(q.Criteria, &levels); err != nil {
			return answer{}, errors.New("score criteria must be an array of levels")
		}
		if len(levels) < 2 || len(levels) > 10 {
			return answer{}, fmt.Errorf("score needs 2-10 levels, got %d", len(levels))
		}
		return scoreAnswer(sig.scoreDistribution(len(levels)), levels), nil

	case questionNoul:
		if len(q.Criteria) > 0 && string(q.Criteria) != "null" {
			var criteria map[string]json.RawMessage
			if err := json.Unmarshal(q.Criteria, &criteria); err != nil {
				return answer{}, errors.New(`noul criteria must be an object with "true"/"false"`)
			}
			for k := range criteria {
				if k != "true" && k != "false" {
					return answer{}, fmt.Errorf("noul criteria has unknown key %q", k)
				}
			}
		}
		p := sig.noul(id)
		return answer{Type: questionNoul, Noul: &p}, nil

	default:
		return answer{}, fmt.Errorf("unknown question type %q", q.Type)
	}
}

func choiceAnswer(probs map[string]float64) answer {
	best, conf := argmax(probs)
	return answer{Type: questionChoice, Choice: best, Probabilities: probs, Confidence: &conf}
}

func scoreAnswer(probs []float64, levels []json.RawMessage) answer {
	legend := make(map[string]string, len(levels))
	byIndex := make(map[string]float64, len(levels))
	score, conf := 0.0, 0.0
	for i, p := range probs {
		idx := strconv.Itoa(i)
		legend[idx] = levelText(levels[i])
		byIndex[idx] = p
		score += float64(i) * p
		conf = max(conf, p)
	}
	return answer{Type: questionScore, Score: &score, Legend: legend, Probabilities: byIndex, Confidence: &conf}
}

// levelText renders a score level the way Jev's legend does: a string level as is,
// anything else (object/array) as its JSON.
func levelText(raw json.RawMessage) string {
	var s string
	if err := json.Unmarshal(raw, &s); err == nil {
		return s
	}
	return string(raw)
}

// mockRequestText extracts the request text from state: {"request": "..."} as the
// provider sends it, or a bare string state.
func mockRequestText(state json.RawMessage) string {
	var obj struct {
		Request string `json:"request"`
	}
	if err := json.Unmarshal(state, &obj); err == nil && obj.Request != "" {
		return obj.Request
	}
	var s string
	if err := json.Unmarshal(state, &s); err == nil {
		return s
	}
	return ""
}

// requestSignals are the keyword features the mock answers from.
type requestSignals struct {
	question   bool // phrased as a question / asks for an explanation
	codeRef    bool // refers to this repository's code
	change     bool // asks to change code
	bug        bool // describes a defect
	feature    bool // asks for new behaviour
	research   bool // asks about external knowledge
	docsOnly   bool // touches only text: typos, comments, docs
	complexity int  // 0 simple, 1 medium, 2 complex
}

var (
	identifierRe = regexp.MustCompile(`\b[A-Z][a-z0-9]+[A-Z][A-Za-z0-9]*\b|\b[a-z]+_[a-z_]+\b|\w+\.(go|py|ts|js|java|rs|yaml|yml|json|md)\b`)

	questionWords = []string{"what", "why", "how", "explain", "which", "difference", "что", "почему", "как ", "объясни", "чем "}
	codeWords     = []string{"code", "file", "function", "method", "package", "module", "class", "endpoint", "handler", "repo", "codebase", "project", "config", "struct", "interface", "cli", "код", "файл", "функци", "метод", "пакет", "проект"}
	changeWords   = []string{"fix", "add", "implement", "refactor", "rename", "update", "change", "remove", "delete", "create", "write", "support", "introduce", "migrate", "rewrite", "replace", "move", "extract", "исправ", "добав", "реализ", "рефактор", "переимен", "обнов", "измен", "удали", "создай", "напиши", "перепиши"}
	bugWords      = []string{"bug", "fix", "error", "fail", "crash", "panic", "broken", "wrong", "incorrect", "regression", "leak", "баг", "ошибк", "паник", "падает", "сломал"}
	featureWords  = []string{"add", "implement", "support", "introduce", "new ", "feature", "добав", "реализ", "поддерж", "нов"}
	researchWords = []string{"compare", "comparison", "library", "libraries", "alternative", "approach", "best practice", "research", "which tool", "vs ", "сравни", "библиотек", "альтернатив", "подход"}
	docsWords     = []string{"typo", "comment", "readme", "docs", "documentation", "spelling", "опечат", "коммент", "документац"}
	simpleWords   = []string{"typo", "rename", "comment", "log message", "spelling", "readme", "bump", "small", "minor", "one-line", "constant", "опечат", "переимен", "коммент", "мелк"}
	complexWords  = []string{"architecture", "redesign", "migrate", "migration", "rewrite", "distributed", "concurrency", "across", "entire", "whole", "system-wide", "framework", "архитектур", "перепиш", "миграц", "весь ", "всю "}
)

func analyzeRequest(text string) requestSignals {
	lower := strings.ToLower(text)
	has := func(words []string) bool {
		for _, w := range words {
			if strings.Contains(lower, w) {
				return true
			}
		}
		return false
	}

	s := requestSignals{
		question: strings.HasSuffix(strings.TrimSpace(lower), "?") || hasPrefixAny(lower, questionWords),
		codeRef:  identifierRe.MatchString(text) || has(codeWords),
		change:   has(changeWords),
		bug:      has(bugWords),
		feature:  has(featureWords),
		research: has(researchWords),
		docsOnly: has(docsWords),
	}
	s.bug = s.bug && !s.docsOnly // "fix a typo" is an edit, not a defect to analyse
	s.codeRef = s.codeRef || s.change || s.bug

	switch {
	case has(complexWords):
		s.complexity = 2
	case has(simpleWords), s.question && !s.change:
		s.complexity = 0
	default:
		s.complexity = 1
	}
	return s
}

func hasPrefixAny(s string, prefixes []string) bool {
	for _, p := range prefixes {
		if strings.HasPrefix(s, p) {
			return true
		}
	}
	return false
}

// choiceDistribution answers the two choice questions the provider asks (pre-check
// none/project and complexity simple/medium/complex); any other option set gets a
// uniform distribution, which is still a valid answer.
func (s requestSignals) choiceDistribution(options []string) map[string]float64 {
	switch {
	case sameSet(options, "none", "project"):
		if s.codeRef {
			return map[string]float64{"none": mockWeak, "project": mockStrong}
		}
		return map[string]float64{"none": mockStrong, "project": mockWeak}
	case sameSet(options, "simple", "medium", "complex"):
		p := s.scoreDistribution(3)
		return map[string]float64{"simple": p[0], "medium": p[1], "complex": p[2]}
	}
	probs := make(map[string]float64, len(options))
	for _, o := range options {
		probs[o] = 1 / float64(len(options))
	}
	return probs
}

// scoreDistribution peaks at the complexity level, scaled onto n levels, and falls off
// geometrically around it.
func (s requestSignals) scoreDistribution(n int) []float64 {
	peak := s.complexity * (n - 1) / 2
	probs := make([]float64, n)
	total := 0.0
	for i := range probs {
		d := i - peak
		if d < 0 {
			d = -d
		}
		probs[i] = 1 / float64(int(1)<<(2*d)) // 1, 1/4, 1/16, ...
		total += probs[i]
	}
	for i := range probs {
		probs[i] /= total
	}
	return probs
}

// noul answers the stage_<name> questions and the shadow scope question (always weak: the
// mock has no signal for it); unknown ids get an undecided 0.5.
func (s requestSignals) noul(id string) float64 {
	if id == decision.QuestionScopeStory {
		return mockWeak
	}
	var yes bool
	switch strings.TrimPrefix(id, "stage_") {
	case "research":
		yes = s.research || (s.question && !s.codeRef)
	case "analysis":
		yes = s.bug || (s.codeRef && s.question) || (s.change && s.complexity > 0)
	case "development":
		yes = s.change && !(s.question && !s.feature && !s.bug)
	default:
		return 0.5
	}
	if yes {
		return mockStrong
	}
	return mockWeak
}

func sameSet(got []string, want ...string) bool {
	if len(got) != len(want) {
		return false
	}
	sort.Strings(want)
	for i := range got { // got is sorted by sortedKeys
		if got[i] != want[i] {
			return false
		}
	}
	return true
}

func argmax(probs map[string]float64) (string, float64) {
	best, bestP := "", -1.0
	for _, k := range sortedKeys(probs) {
		if probs[k] > bestP {
			best, bestP = k, probs[k]
		}
	}
	return best, bestP
}

func sortedKeys[V any](m map[string]V) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
}

func mockBadRequest(err error) error {
	return &apperr.DecisionError{Provider: ProviderName, Kind: apperr.DecisionBadRequest, StatusCode: 422, Attempts: 1, Err: err}
}

func mockContextError(ctx context.Context) error {
	if ctx.Err() == nil {
		return nil
	}
	kind := apperr.DecisionTimeout
	if errors.Is(ctx.Err(), context.Canceled) {
		kind = apperr.DecisionCancelled
	}
	return &apperr.DecisionError{Provider: ProviderName, Kind: kind, Err: ctx.Err()}
}

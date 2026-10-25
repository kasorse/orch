package jev

import (
	"context"
	"errors"
	"math"
	"reflect"
	"strings"
	"testing"

	"orchestrator/internal/apperr"
	"orchestrator/internal/decision"
)

var complexityLevels = []string{
	"Simple: small local change.",
	"Medium: several files/components.",
	"Complex: cross-component or architectural change.",
}

// classificationRequest has the question shape T07 will send (TASKS.md §2).
func classificationRequest(request string) systemOneRequest {
	qs := map[string]question{
		"complexity": {Type: questionChoice, Instructions: "How complex is it to fulfil `request`?", Criteria: map[string]string{
			"simple": "small", "medium": "several files", "complex": "architectural",
		}},
		"complexity_score": {Type: questionScore, Instructions: "Rate how complex it is.", Criteria: complexityLevels},
	}
	for _, st := range []string{"research", "development"} {
		qs["stage_"+st] = question{Type: questionNoul, Instructions: "Does `request` need " + st + "?",
			Criteria: map[string]string{"true": "yes", "false": "no"}}
	}
	return systemOneRequest{
		Model:     "jev-1.13.0",
		State:     map[string]any{"request": request, "project_context_available": true},
		Questions: qs,
	}
}

func preCheckRequest(request string) systemOneRequest {
	return systemOneRequest{
		Model: "jev-1.13.0",
		State: map[string]any{"request": request},
		Questions: map[string]question{
			"context_depth": {Type: questionChoice, Instructions: "Is it necessary to inspect the repository?",
				Criteria: map[string]string{"none": "text alone", "project": "refers to this repository"}},
		},
	}
}

func mustDecide(t *testing.T, req systemOneRequest) systemOneResponse {
	t.Helper()
	resp, err := NewMockClient().decide(context.Background(), req)
	if err != nil {
		t.Fatalf("mock decide: %v", err)
	}
	return resp
}

func TestMock_AnswersEveryQuestionInValidShape(t *testing.T) {
	resp := mustDecide(t, classificationRequest("Fix the panic in ProductRepository.Save"))

	if resp.Model != MockModelPrefix+"jev-1.13.0" {
		t.Errorf("Model = %q", resp.Model)
	}
	if len(resp.Answers) != 4 {
		t.Fatalf("got %d answers, want 4", len(resp.Answers))
	}

	c := resp.Answers["complexity"]
	if c.Type != questionChoice || c.Confidence == nil || c.Choice == "" {
		t.Errorf("complexity answer = %+v", c)
	}
	assertDistribution(t, "complexity", c.Probabilities, "simple", "medium", "complex")
	if c.Probabilities[c.Choice] != *c.Confidence {
		t.Errorf("choice %q is not the most likely option: %+v", c.Choice, c.Probabilities)
	}

	s := resp.Answers["complexity_score"]
	if s.Type != questionScore || s.Score == nil || s.Confidence == nil {
		t.Fatalf("score answer = %+v", s)
	}
	assertDistribution(t, "complexity_score", s.Probabilities, "0", "1", "2")
	for i, level := range complexityLevels {
		if got := s.Legend[string(rune('0'+i))]; got != level {
			t.Errorf("legend[%d] = %q, want %q", i, got, level)
		}
	}
	if *s.Score < 0 || *s.Score > 2 {
		t.Errorf("score = %v, want within [0, 2]", *s.Score)
	}

	for id, a := range resp.Answers {
		if !strings.HasPrefix(id, "stage_") {
			continue
		}
		if a.Type != questionNoul || a.Noul == nil || *a.Noul < 0 || *a.Noul > 1 || a.Confidence != nil {
			t.Errorf("%s answer = %+v", id, a)
		}
	}
}

func assertDistribution(t *testing.T, id string, probs map[string]float64, want ...string) {
	t.Helper()
	if len(probs) != len(want) {
		t.Errorf("%s: probabilities = %v, want exactly %v", id, probs, want)
	}
	sum := 0.0
	for _, k := range want {
		p, ok := probs[k]
		if !ok || p < 0 || p > 1 {
			t.Errorf("%s: probabilities[%q] = %v (present: %v)", id, k, p, ok)
		}
		sum += p
	}
	if math.Abs(sum-1) > 1e-9 {
		t.Errorf("%s: probabilities sum to %v", id, sum)
	}
}

func TestMock_ContextDepth(t *testing.T) {
	cases := map[string]string{
		"What is the difference between a mutex and a semaphore?": "none",
		"Explain eventual consistency":                            "none",
		"Fix the nil pointer dereference in ProductRepository":    "project",
		"Add a --verbose flag to the CLI":                         "project",
		"Исправь ошибку в config/loader.go":                       "project",
	}
	for request, want := range cases {
		a := mustDecide(t, preCheckRequest(request)).Answers["context_depth"]
		if a.Choice != want {
			t.Errorf("%q: context_depth = %q (%v), want %q", request, a.Choice, a.Probabilities, want)
		}
		assertDistribution(t, request, a.Probabilities, "none", "project")
	}
}

func TestMock_Complexity(t *testing.T) {
	cases := map[string]string{
		"Fix a typo in the README":                                  "simple",
		"Add retry support to the HTTP client":                      "medium",
		"Redesign the architecture of the workflow engine":          "complex",
		"Migrate persistence across all packages to a new database": "complex",
	}
	for request, want := range cases {
		resp := mustDecide(t, classificationRequest(request))
		if got := resp.Answers["complexity"].Choice; got != want {
			t.Errorf("%q: complexity = %q, want %q", request, got, want)
		}
		// choice and score agree in the mock: the score peaks at the same level.
		score := *resp.Answers["complexity_score"].Score
		level := map[string]float64{"simple": 0, "medium": 1, "complex": 2}[want]
		if math.Abs(score-level) > 0.5 {
			t.Errorf("%q: score = %v, want near %v", request, score, level)
		}
	}
}

func TestMock_Stages(t *testing.T) {
	enabled := func(resp systemOneResponse) []string {
		var out []string
		for _, st := range []string{"research", "development"} {
			a, ok := resp.Answers["stage_"+st]
			if !ok {
				t.Fatalf("no answer for stage_%s", st)
			}
			if *a.Noul >= 0.5 {
				out = append(out, st)
			}
		}
		return out
	}
	cases := map[string][]string{
		"Compare Redis and Memcached for caching":         {"research"},
		"Fix a typo in the README":                        {"development"},
		"Fix the race condition in the dispatcher":        {"development"},
		"Add pagination support to the list endpoint":     {"development"},
		"Rewrite the whole engine with a new concurrency": {"development"},
	}
	for request, want := range cases {
		if got := enabled(mustDecide(t, classificationRequest(request))); !reflect.DeepEqual(got, want) {
			t.Errorf("%q: stages = %v, want %v", request, got, want)
		}
	}
}

func TestMock_ScopeAnswersWeakWhateverTheRequest(t *testing.T) {
	for _, request := range []string{
		"Fix a typo in the README",
		"Redesign the architecture of the workflow engine across all services",
		"What is a mutex?",
	} {
		req := classificationRequest(request)
		scope := decision.ScopeQuestion()
		req.Questions[scope.ID] = yesNoQuestion(scope)
		a := mustDecide(t, req).Answers[scope.ID]
		if a.Type != questionNoul || a.Noul == nil || *a.Noul != 0.15 {
			t.Errorf("%q: scope answer = %+v, want noul 0.15", request, a)
		}
	}
}

// A request under about 8 words or without a verb is unclear; a named file, function or error
// message makes it clear.
func TestMock_Clarity(t *testing.T) {
	cases := []struct {
		request string
		want    bool
	}{
		{"make it faster", false},
		{"Add a cache", false},
		{"The performance of the whole orders service lately somehow", false}, // long, but no verb
		{"Add pagination support to the list endpoint of the orders service", true},
		{"Fix Add in fixture.go", true},
		{"Fix the panic: index out of range in the dispatcher", true},
		{`Fix the error "connection reset by peer" on upload`, true},
		{"Call Reset() after use", true},
	}
	q := decision.ClarityQuestion()
	for _, tc := range cases {
		request, want := tc.request, tc.want
		a := mustDecide(t, systemOneRequest{
			Model:     "jev-1.13.0",
			State:     decision.PreCheckState{Request: request},
			Questions: map[string]question{q.ID: yesNoQuestion(q)},
		}).Answers[q.ID]
		if a.Type != questionNoul || a.Noul == nil {
			t.Fatalf("%q: clarity answer = %+v", request, a)
		}
		if got := *a.Noul >= 0.5; got != want {
			t.Errorf("%q: P(clear) = %v, want clear = %v", request, *a.Noul, want)
		}
	}
}

// The re-check sends the clarification with the request, and the mock reads both.
func TestMock_ClarityReadsTheClarification(t *testing.T) {
	q := decision.ClarityQuestion()
	ask := func(clarification string) float64 {
		a := mustDecide(t, systemOneRequest{
			Model:     "jev-1.13.0",
			State:     decision.ClarityState{Request: "make it faster", Clarification: clarification},
			Questions: map[string]question{q.ID: yesNoQuestion(q)},
		}).Answers[q.ID]
		return *a.Noul
	}
	if got := ask(""); got >= 0.5 {
		t.Errorf("P(clear) without a clarification = %v, want unclear", got)
	}
	if got := ask("- Which call is slow? Save in internal/repository/product.go  [operator]"); got < 0.5 {
		t.Errorf("P(clear) with a clarification naming a file = %v, want clear", got)
	}
}

func TestMock_Deterministic(t *testing.T) {
	req := classificationRequest("Add pagination support to the list endpoint")
	if a, b := mustDecide(t, req), mustDecide(t, req); !reflect.DeepEqual(a, b) {
		t.Errorf("same request gave different answers:\n%+v\n%+v", a, b)
	}
}

func TestMock_GenericQuestions(t *testing.T) {
	req := systemOneRequest{
		Model: "jev-1.13.0",
		State: "plain string state",
		Questions: map[string]question{
			"colour": {Type: questionChoice, Instructions: "Which colour?", Criteria: map[string]string{"red": "r", "green": "g", "blue": "b", "grey": "x"}},
			"size":   {Type: questionScore, Instructions: "How big?", Criteria: []any{"tiny", map[string]string{"label": "huge"}}},
			"other":  {Type: questionNoul, Instructions: "Anything else?"},
		},
	}
	resp := mustDecide(t, req)
	assertDistribution(t, "colour", resp.Answers["colour"].Probabilities, "red", "green", "blue", "grey")
	assertDistribution(t, "size", resp.Answers["size"].Probabilities, "0", "1")
	if got := resp.Answers["size"].Legend["1"]; got != `{"label":"huge"}` {
		t.Errorf("non-string level legend = %q", got)
	}
	if got := *resp.Answers["other"].Noul; got != 0.5 {
		t.Errorf("unknown noul = %v, want 0.5", got)
	}
}

func TestMock_RejectsMalformedRequestsLikeJev(t *testing.T) {
	levels := func(n int) []string { return make([]string, n) }
	cases := map[string]systemOneRequest{
		"no model":           {State: "s", Questions: map[string]question{"q": {Type: questionNoul, Instructions: "i"}}},
		"no questions":       {Model: "m", State: "s"},
		"unknown type":       {Model: "m", State: "s", Questions: map[string]question{"q": {Type: "yesno", Instructions: "i"}}},
		"no instructions":    {Model: "m", State: "s", Questions: map[string]question{"q": {Type: questionNoul}}},
		"choice as array":    {Model: "m", State: "s", Questions: map[string]question{"q": {Type: questionChoice, Instructions: "i", Criteria: []string{"a", "b"}}}},
		"choice empty":       {Model: "m", State: "s", Questions: map[string]question{"q": {Type: questionChoice, Instructions: "i", Criteria: map[string]string{}}}},
		"score as map":       {Model: "m", State: "s", Questions: map[string]question{"q": {Type: questionScore, Instructions: "i", Criteria: map[string]string{"a": "b"}}}},
		"score one level":    {Model: "m", State: "s", Questions: map[string]question{"q": {Type: questionScore, Instructions: "i", Criteria: levels(1)}}},
		"score 11 levels":    {Model: "m", State: "s", Questions: map[string]question{"q": {Type: questionScore, Instructions: "i", Criteria: levels(11)}}},
		"noul bad criteria":  {Model: "m", State: "s", Questions: map[string]question{"q": {Type: questionNoul, Instructions: "i", Criteria: map[string]string{"maybe": "x"}}}},
		"unencodable state":  {Model: "m", State: func() {}, Questions: map[string]question{"q": {Type: questionNoul, Instructions: "i"}}},
		"noul criteria list": {Model: "m", State: "s", Questions: map[string]question{"q": {Type: questionNoul, Instructions: "i", Criteria: []string{"true"}}}},
	}
	for name, req := range cases {
		t.Run(name, func(t *testing.T) {
			_, err := NewMockClient().decide(context.Background(), req)
			var derr *apperr.DecisionError
			if !errors.As(err, &derr) || derr.Kind != apperr.DecisionBadRequest || derr.StatusCode != 422 {
				t.Errorf("want 422 bad request, got %v", err)
			}
		})
	}
}

func TestMock_HonoursContext(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	_, err := NewMockClient().decide(ctx, preCheckRequest("anything"))
	var derr *apperr.DecisionError
	if !errors.As(err, &derr) || derr.Kind != apperr.DecisionCancelled || !errors.Is(err, context.Canceled) {
		t.Errorf("want cancelled, got %v", err)
	}
}

func TestMock_ListModels(t *testing.T) {
	models, err := NewMockClient().listModels(context.Background())
	if err != nil || len(models) == 0 || models[0].Name == "" {
		t.Errorf("listModels = %+v, %v", models, err)
	}
}

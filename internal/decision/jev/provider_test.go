package jev

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"orchestrator/internal/apperr"
	"orchestrator/internal/decision"
	"orchestrator/internal/domain"
)

var update = flag.Bool("update", false, "rewrite testdata/*.golden.json from the current requests")

// stubClient records the request and replays a canned response.
type stubClient struct {
	resp systemOneResponse
	err  error
	got  *systemOneRequest
}

func (s *stubClient) decide(_ context.Context, req systemOneRequest) (systemOneResponse, error) {
	s.got = &req
	return s.resp, s.err
}

func (s *stubClient) listModels(context.Context) ([]modelInfo, error) { return nil, nil }

var bothSources = []domain.ComplexitySource{domain.ComplexitySourceChoice, domain.ComplexitySourceScore}

func newTestProvider(t *testing.T, c Client, sources []domain.ComplexitySource) *Provider {
	t.Helper()
	p, err := NewProvider(c, "jev-1.13.0", sources)
	if err != nil {
		t.Fatalf("NewProvider: %v", err)
	}
	return p
}

func testProject() *domain.ProjectContext {
	return &domain.ProjectContext{
		Language:    "go",
		ProjectType: "cli",
		Module:      "orchestrator",
		Rules:       []string{"Provider CLI invocation is argv-only, never sh -c."},
		RelevantFiles: []domain.RelevantFile{
			{Path: "internal/repository/product.go", Purpose: "implementation", Summary: "ProductRepository backed by PostgreSQL."},
		},
		Dependencies: []string{"gopkg.in/yaml.v3"},
		TestInfo:     domain.TestInfo{Exists: true, UnitTests: true},
		Git:          domain.GitContext{Branch: "main"},
		Truncated:    true,
	}
}

func assertGolden(t *testing.T, name string, v any) {
	t.Helper()
	got, err := json.MarshalIndent(v, "", "  ")
	if err != nil {
		t.Fatal(err)
	}
	got = append(got, '\n')
	path := filepath.Join("testdata", name+".golden.json")
	if *update {
		if err := os.WriteFile(path, got, 0o644); err != nil {
			t.Fatal(err)
		}
	}
	want, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read golden (run go test ./internal/decision/jev -update to create): %v", err)
	}
	if string(got) != string(want) {
		t.Errorf("request differs from %s (rerun with -update if intended):\n%s", path, got)
	}
}

func TestProvider_RequestsMatchGolden(t *testing.T) {
	cases := []struct {
		name    string
		sources []domain.ComplexitySource
		send    func(p *Provider) error
	}{
		{"precheck", bothSources, func(p *Provider) error {
			_, err := p.ContextDepth(context.Background(), "Add a cache to ProductRepository")
			return err
		}},
		{"classification_shadow_project", bothSources, func(p *Provider) error {
			_, err := p.Classify(context.Background(), domain.ClassificationInput{
				Request: "Add a cache to ProductRepository", ContextDepth: domain.ContextDepthProject, Project: testProject(),
			})
			return err
		}},
		{"classification_choice_only_no_project", []domain.ComplexitySource{domain.ComplexitySourceChoice}, func(p *Provider) error {
			_, err := p.Classify(context.Background(), domain.ClassificationInput{
				Request: "What is a mutex?", ContextDepth: domain.ContextDepthNone,
			})
			return err
		}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			stub := &stubClient{err: errors.New("not needed")}
			_ = tc.send(newTestProvider(t, stub, tc.sources))
			if stub.got == nil {
				t.Fatal("no request was sent")
			}
			assertGolden(t, tc.name, stub.got)
		})
	}
}

func TestProvider_ClassificationStateWithoutProject(t *testing.T) {
	// A failed collection (depth project, context limited) looks the same to Jev as
	// depth none: no project, project_context_available false.
	stub := &stubClient{err: errors.New("not needed")}
	_, _ = newTestProvider(t, stub, bothSources).Classify(context.Background(), domain.ClassificationInput{
		Request: "Fix the bug", ContextDepth: domain.ContextDepthProject, ContextLimited: true,
	})
	raw, _ := json.Marshal(stub.got.State)
	if string(raw) != `{"request":"Fix the bug","project_context_available":false}` {
		t.Errorf("state = %s", raw)
	}
}

func TestProvider_ScoreAndChoiceShareCriteria(t *testing.T) {
	choice := decision.ComplexityChoiceQuestion()
	score := decision.ComplexityScoreQuestion()
	for i, o := range choice.Options {
		if !strings.EqualFold(score.Levels[i], o.Value+": "+o.Description) {
			t.Errorf("level %d = %q does not match choice option %q: %q", i, score.Levels[i], o.Value, o.Description)
		}
	}
}

func loadResponse(t *testing.T, name string) systemOneResponse {
	t.Helper()
	raw, err := os.ReadFile(filepath.Join("testdata", name))
	if err != nil {
		t.Fatal(err)
	}
	var resp systemOneResponse
	if err := json.Unmarshal(raw, &resp); err != nil {
		t.Fatal(err)
	}
	return resp
}

func TestProvider_ContextDepthMapsFixture(t *testing.T) {
	stub := &stubClient{resp: loadResponse(t, "precheck_response.json")}
	d, err := newTestProvider(t, stub, bothSources).ContextDepth(context.Background(), "Add a cache")
	if err != nil {
		t.Fatalf("ContextDepth: %v", err)
	}
	if d.Probabilities[domain.ContextDepthProject] != 0.91 || d.Probabilities[domain.ContextDepthNone] != 0.09 || d.Confidence != 0.82 {
		t.Errorf("decision = %+v", d)
	}
}

func TestProvider_ClassifyMapsFixture(t *testing.T) {
	stub := &stubClient{resp: loadResponse(t, "classification_response.json")}
	in := domain.ClassificationInput{Request: "Add a cache", ContextDepth: domain.ContextDepthProject, ContextLimited: true}
	tc, err := newTestProvider(t, stub, bothSources).Classify(context.Background(), in)
	if err != nil {
		t.Fatalf("Classify: %v", err)
	}

	if tc.Provider != "jev" || tc.Model != "jev-1.13.0" {
		t.Errorf("Provider/Model = %q/%q", tc.Provider, tc.Model)
	}
	if tc.ContextDepth != domain.ContextDepthProject || !tc.ContextLimited {
		t.Errorf("input facts not copied: %+v", tc)
	}

	choice := tc.Complexity[domain.ComplexitySourceChoice]
	// The fixture's "choice" field says "complex", but the mapper ignores it: only the
	// distribution is kept.
	if choice.Probabilities[domain.ComplexityMedium] != 0.6 || choice.Confidence != 0.7 || choice.Score != nil {
		t.Errorf("choice = %+v", choice)
	}
	score := tc.Complexity[domain.ComplexitySourceScore]
	if score.Probabilities[domain.ComplexitySimple] != 0.1 || score.Probabilities[domain.ComplexityComplex] != 0.3 ||
		score.Score == nil || *score.Score != 1.2 || score.Confidence != 0.55 {
		t.Errorf("score = %+v", score)
	}

	want := map[domain.Stage]float64{domain.StageResearch: 0.05, domain.StageAnalysis: 0.92, domain.StageDevelopment: 0.99}
	for st, p := range want {
		if got, _ := tc.Stages.Get(st); got.Probability != p {
			t.Errorf("stage %s = %v, want %v", st, got.Probability, p)
		}
	}
	for _, st := range []domain.Stage{domain.StagePlanning, domain.StageTesting, domain.StageReview} {
		if _, ok := tc.Stages.Get(st); ok {
			t.Errorf("stage %s has a probability, but is not asked", st)
		}
	}
	if tc.Scope == nil || tc.Scope.Story != 0.35 {
		t.Errorf("Scope = %+v, want story 0.35", tc.Scope)
	}
}

func TestProvider_ScopeIsAskedUnlessSkipped(t *testing.T) {
	in := domain.ClassificationInput{Request: "x", ContextDepth: domain.ContextDepthNone}

	stub := &stubClient{err: errors.New("not needed")}
	_, _ = newTestProvider(t, stub, bothSources).Classify(context.Background(), in)
	if q, ok := stub.got.Questions[decision.QuestionScopeStory]; !ok || q.Type != questionNoul {
		t.Errorf("scope question = %+v (asked: %v), want a noul question", q, ok)
	}

	in.SkipScope = true
	stub = &stubClient{err: errors.New("not needed")}
	_, _ = newTestProvider(t, stub, bothSources).Classify(context.Background(), in)
	if _, ok := stub.got.Questions[decision.QuestionScopeStory]; ok {
		t.Error("the scope question was sent although it is skipped")
	}
	if len(stub.got.Questions) != 5 {
		t.Errorf("got %d questions, want the 5 others", len(stub.got.Questions))
	}
}

// A scope answer is required exactly when the question was asked: missing is an
// InvalidResponse only then, and an answer nobody asked for is an unexpected one.
func TestProvider_ScopeAnswerIsRequiredOnlyWhenAsked(t *testing.T) {
	asked := domain.ClassificationInput{Request: "x", ContextDepth: domain.ContextDepthNone}
	skipped := asked
	skipped.SkipScope = true

	resp := loadResponse(t, "classification_response.json")
	delete(resp.Answers, decision.QuestionScopeStory)
	_, err := newTestProvider(t, &stubClient{resp: resp}, bothSources).Classify(context.Background(), asked)
	assertInvalidResponse(t, err)
	tc, err := newTestProvider(t, &stubClient{resp: resp}, bothSources).Classify(context.Background(), skipped)
	if err != nil {
		t.Fatalf("Classify without a scope answer, skipped: %v", err)
	}
	if tc.Scope != nil {
		t.Errorf("Scope = %+v, want nil when not asked", tc.Scope)
	}

	_, err = newTestProvider(t, &stubClient{resp: loadResponse(t, "classification_response.json")}, bothSources).Classify(context.Background(), skipped)
	assertInvalidResponse(t, err)
}

func TestProvider_ChoiceOnlyDoesNotExpectScore(t *testing.T) {
	resp := loadResponse(t, "classification_response.json")
	delete(resp.Answers, decision.QuestionComplexityScore)
	stub := &stubClient{resp: resp}
	tc, err := newTestProvider(t, stub, []domain.ComplexitySource{domain.ComplexitySourceChoice}).
		Classify(context.Background(), domain.ClassificationInput{Request: "x", ContextDepth: domain.ContextDepthNone})
	if err != nil {
		t.Fatalf("Classify: %v", err)
	}
	if _, ok := tc.Complexity[domain.ComplexitySourceScore]; ok || len(tc.Complexity) != 1 {
		t.Errorf("Complexity = %+v, want choice only", tc.Complexity)
	}
}

func TestProvider_InvalidClassificationResponses(t *testing.T) {
	f := func(v float64) *float64 { return &v }
	cases := map[string]func(r *systemOneResponse){
		"no model":             func(r *systemOneResponse) { r.Model = "" },
		"missing stage answer": func(r *systemOneResponse) { delete(r.Answers, "stage_analysis") },
		"answer to a stage no longer asked": func(r *systemOneResponse) {
			r.Answers["stage_review"] = answer{Type: questionNoul, Noul: f(0.7)}
		},
		"missing complexity": func(r *systemOneResponse) { delete(r.Answers, "complexity") },
		"unexpected answer":  func(r *systemOneResponse) { r.Answers["stage_deploy"] = answer{Type: questionNoul, Noul: f(0.5)} },
		"choice wrong type": func(r *systemOneResponse) {
			a := r.Answers["complexity"]
			a.Type = questionScore
			r.Answers["complexity"] = a
		},
		"choice missing option": func(r *systemOneResponse) {
			delete(r.Answers["complexity"].Probabilities, "complex")
		},
		"choice extra option": func(r *systemOneResponse) { r.Answers["complexity"].Probabilities["trivial"] = 0 },
		"choice no confidence": func(r *systemOneResponse) {
			a := r.Answers["complexity"]
			a.Confidence = nil
			r.Answers["complexity"] = a
		},
		"choice sum off":      func(r *systemOneResponse) { r.Answers["complexity"].Probabilities["simple"] = 0.5 },
		"choice out of range": func(r *systemOneResponse) { r.Answers["complexity"].Probabilities["simple"] = -0.1 },
		"confidence above one": func(r *systemOneResponse) {
			a := r.Answers["complexity"]
			a.Confidence = f(1.2)
			r.Answers["complexity"] = a
		},
		"score unexpected legend": func(r *systemOneResponse) {
			r.Answers["complexity_score"].Legend["1"] = "Moderate"
		},
		"score legend extra level": func(r *systemOneResponse) { r.Answers["complexity_score"].Legend["3"] = "Epic" },
		"score missing level":      func(r *systemOneResponse) { delete(r.Answers["complexity_score"].Probabilities, "2") },
		"score no score": func(r *systemOneResponse) {
			a := r.Answers["complexity_score"]
			a.Score = nil
			r.Answers["complexity_score"] = a
		},
		"score out of range": func(r *systemOneResponse) {
			a := r.Answers["complexity_score"]
			a.Score = f(2.5)
			r.Answers["complexity_score"] = a
		},
		"noul wrong type": func(r *systemOneResponse) {
			a := r.Answers["stage_development"]
			a.Type = questionChoice
			r.Answers["stage_development"] = a
		},
		"scope wrong type": func(r *systemOneResponse) {
			a := r.Answers["scope_story"]
			a.Type = questionChoice
			r.Answers["scope_story"] = a
		},
		"scope missing value": func(r *systemOneResponse) { r.Answers["scope_story"] = answer{Type: questionNoul} },
		"scope above one":     func(r *systemOneResponse) { r.Answers["scope_story"] = answer{Type: questionNoul, Noul: f(1.01)} },
		"noul missing value":  func(r *systemOneResponse) { r.Answers["stage_development"] = answer{Type: questionNoul} },
		"noul above one":      func(r *systemOneResponse) { r.Answers["stage_development"] = answer{Type: questionNoul, Noul: f(1.01)} },
		"noul below zero": func(r *systemOneResponse) {
			r.Answers["stage_development"] = answer{Type: questionNoul, Noul: f(-0.01)}
		},
	}
	for name, mutate := range cases {
		t.Run(name, func(t *testing.T) {
			resp := loadResponse(t, "classification_response.json")
			mutate(&resp)
			_, err := newTestProvider(t, &stubClient{resp: resp}, bothSources).
				Classify(context.Background(), domain.ClassificationInput{Request: "x", ContextDepth: domain.ContextDepthNone})
			assertInvalidResponse(t, err)
		})
	}
}

func TestProvider_InvalidPreCheckResponses(t *testing.T) {
	cases := map[string]func(r *systemOneResponse){
		"missing answer": func(r *systemOneResponse) { delete(r.Answers, "context_depth") },
		"wrong options": func(r *systemOneResponse) {
			a := r.Answers["context_depth"]
			a.Probabilities = map[string]float64{"none": 0.5, "repo": 0.5}
			r.Answers["context_depth"] = a
		},
		"noul instead of choice": func(r *systemOneResponse) {
			v := 0.9
			r.Answers["context_depth"] = answer{Type: questionNoul, Noul: &v}
		},
	}
	for name, mutate := range cases {
		t.Run(name, func(t *testing.T) {
			resp := loadResponse(t, "precheck_response.json")
			mutate(&resp)
			_, err := newTestProvider(t, &stubClient{resp: resp}, bothSources).ContextDepth(context.Background(), "x")
			assertInvalidResponse(t, err)
		})
	}
}

func assertInvalidResponse(t *testing.T, err error) {
	t.Helper()
	var derr *apperr.DecisionError
	if !errors.As(err, &derr) || derr.Kind != apperr.DecisionInvalidResponse || derr.Provider != ProviderName {
		t.Errorf("want jev invalid_response, got %v", err)
	}
}

func TestProvider_ClientErrorPassesThrough(t *testing.T) {
	cause := &apperr.DecisionError{Provider: ProviderName, Kind: apperr.DecisionUnavailable, StatusCode: 529}
	p := newTestProvider(t, &stubClient{err: cause}, bothSources)
	if _, err := p.ContextDepth(context.Background(), "x"); err != cause {
		t.Errorf("ContextDepth err = %v, want the client's error unchanged", err)
	}
	if _, err := p.Classify(context.Background(), domain.ClassificationInput{Request: "x", ContextDepth: domain.ContextDepthNone}); err != cause {
		t.Errorf("Classify err = %v, want the client's error unchanged", err)
	}
}

func TestProvider_WithMockClientEndToEnd(t *testing.T) {
	p := newTestProvider(t, NewMockClient(), bothSources)
	depth, err := p.ContextDepth(context.Background(), "Add pagination support to the list endpoint")
	if err != nil {
		t.Fatalf("ContextDepth: %v", err)
	}
	if depth.Probabilities[domain.ContextDepthProject] < 0.5 {
		t.Errorf("depth = %+v, want project", depth)
	}
	tc, err := p.Classify(context.Background(), domain.ClassificationInput{
		Request: "Add pagination support to the list endpoint", ContextDepth: domain.ContextDepthProject, Project: testProject(),
	})
	if err != nil {
		t.Fatalf("Classify: %v", err)
	}
	if !strings.HasPrefix(tc.Model, MockModelPrefix) || len(tc.Complexity) != 2 {
		t.Errorf("classification = %+v", tc)
	}
}

func TestNewProvider_Validation(t *testing.T) {
	c := NewMockClient()
	cases := map[string]func() (*Provider, error){
		"nil client":       func() (*Provider, error) { return NewProvider(nil, "m", bothSources) },
		"no model":         func() (*Provider, error) { return NewProvider(c, "", bothSources) },
		"no sources":       func() (*Provider, error) { return NewProvider(c, "m", nil) },
		"unknown source":   func() (*Provider, error) { return NewProvider(c, "m", []domain.ComplexitySource{"vote"}) },
		"duplicate source": func() (*Provider, error) { return NewProvider(c, "m", []domain.ComplexitySource{"choice", "choice"}) },
	}
	for name, build := range cases {
		if _, err := build(); err == nil {
			t.Errorf("%s: expected an error", name)
		}
	}
	if p, err := NewProvider(c, "m", bothSources); err != nil || p.Name() != "jev" {
		t.Errorf("valid provider: %v, %v", p, err)
	}
}

// Jev reports tokens and no cost; the provider records them with the answer, and on the
// DecisionError of an answer that did not map (the request was still paid for). The mock's
// numbers are invented and never recorded (specs/benchmark/SPEC.md §11.1).
func TestProvider_RecordsUsage(t *testing.T) {
	stub := &stubClient{resp: loadResponse(t, "precheck_response.json")}
	d, err := newTestProvider(t, stub, bothSources).ContextDepth(context.Background(), "Add a cache")
	if err != nil {
		t.Fatal(err)
	}
	if d.Usage == nil || *d.Usage != (domain.Usage{InputTokens: 120, OutputTokens: 1}) {
		t.Errorf("context depth usage = %+v", d.Usage)
	}

	stub = &stubClient{resp: loadResponse(t, "classification_response.json")}
	tc, err := newTestProvider(t, stub, bothSources).Classify(context.Background(),
		domain.ClassificationInput{Request: "Add a cache", ContextDepth: domain.ContextDepthProject, Project: testProject()})
	if err != nil {
		t.Fatal(err)
	}
	if tc.Usage == nil || *tc.Usage != (domain.Usage{InputTokens: 1450, OutputTokens: 8}) {
		t.Errorf("classification usage = %+v", tc.Usage)
	}

	resp := loadResponse(t, "precheck_response.json")
	delete(resp.Answers, "context_depth")
	_, err = newTestProvider(t, &stubClient{resp: resp}, bothSources).ContextDepth(context.Background(), "x")
	var derr *apperr.DecisionError
	if !errors.As(err, &derr) || derr.Usage == nil || derr.Usage.InputTokens != 120 {
		t.Errorf("an unmapped answer's usage = %v (%v)", derr, err)
	}

	p := newTestProvider(t, NewMockClient(), bothSources)
	depth, err := p.ContextDepth(context.Background(), "Add pagination support to the list endpoint")
	if err != nil || depth.Usage != nil {
		t.Errorf("mock usage = %+v, %v; want none", depth.Usage, err)
	}
}

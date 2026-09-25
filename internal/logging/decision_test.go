package logging

import (
	"bytes"
	"flag"
	"os"
	"path/filepath"
	"testing"

	"orchestrator/internal/domain"
)

var update = flag.Bool("update", false, "rewrite testdata/*.golden from the current output")

func ptr(v float64) *float64 { return &v }

func dist(s, m, c float64) map[domain.Complexity]float64 {
	return map[domain.Complexity]float64{domain.ComplexitySimple: s, domain.ComplexityMedium: m, domain.ComplexityComplex: c}
}

func stageOutcomes() []domain.StageOutcome {
	return []domain.StageOutcome{
		{Stage: domain.StageResearch, Probability: ptr(0.05), Threshold: ptr(0.7), Reason: "p=0.05 < 0.70"},
		{Stage: domain.StageAnalysis, Probability: ptr(0.92), Threshold: ptr(0.6), Enabled: true, Reason: "p=0.92 >= 0.60"},
		{Stage: domain.StagePlanning, Probability: ptr(0.40), Threshold: ptr(0.65), Reason: "p=0.40 < 0.65"},
		{Stage: domain.StageDevelopment, Probability: ptr(0.99), Threshold: ptr(0.6), Enabled: true, Reason: "p=0.99 >= 0.60"},
		{Stage: domain.StageTesting, Probability: ptr(0.95), Threshold: ptr(0.6), Enabled: true, Reason: "p=0.95 >= 0.60"},
		{Stage: domain.StageReview, Probability: ptr(0.74), Threshold: ptr(0.7), Enabled: true, Reason: "p=0.74 >= 0.70"},
	}
}

func jevTrace() domain.DecisionTrace {
	return domain.DecisionTrace{
		Request:     "добавь кэш в ProductRepository",
		RequestEN:   "add a cache to ProductRepository",
		Translation: domain.TranslationTrace{Detected: "ru", Translated: true, Provider: "claude", Model: "haiku", LatencyMS: 1240},
		Source:      domain.DecisionSourceJev,
		Provider:    "jev",
		Model:       "jev-1.13.0",
		ContextDepth: &domain.ContextDepthTrace{
			ContextDepthOutcome: domain.ContextDepthOutcome{Selected: domain.ContextDepthProject, Probability: 0.91, Threshold: 0.5},
		},
		Complexity: domain.ComplexityOutcome{
			Primary:  domain.ComplexitySourceChoice,
			Selected: domain.ComplexityMedium,
			Agree:    true,
			Sources: map[domain.ComplexitySource]domain.ComplexitySourceOutcome{
				domain.ComplexitySourceChoice: {Decision: domain.ComplexityDecision{Probabilities: dist(0.05, 0.63, 0.32)}, Selected: domain.ComplexityMedium},
				domain.ComplexitySourceScore:  {Decision: domain.ComplexityDecision{Probabilities: dist(0.1, 0.6, 0.3), Score: ptr(1.2)}, Selected: domain.ComplexityMedium},
			},
		},
		Stages:   stageOutcomes(),
		Workflow: []domain.ActionType{domain.ActionAnalyze, domain.ActionImplement, domain.ActionTest, domain.ActionReview},
	}
}

func TestRenderDecision_Golden(t *testing.T) {
	disagree := jevTrace()
	disagree.Translation = domain.TranslationTrace{Detected: "en"}
	disagree.ContextLimited = true
	disagree.ContextError = "list repository files: git failed\nmore detail"
	disagree.Complexity.Agree = false
	disagree.Complexity.Sources[domain.ComplexitySourceScore] = domain.ComplexitySourceOutcome{
		Decision: domain.ComplexityDecision{Probabilities: dist(0.05, 0.25, 0.7), Score: ptr(1.65)},
		Selected: domain.ComplexityComplex,
	}

	user := domain.DecisionTrace{
		Request:       "почини тесты",
		Translation:   domain.TranslationTrace{Detected: "ru", Failed: true, Reason: "translation timed out after 1m0s", Provider: "claude", Model: "haiku"},
		Source:        domain.DecisionSourceUser,
		Provider:      "jev",
		DecisionError: "decision provider jev failed: unavailable (the service is overloaded or failing), HTTP 529, after 2 attempts\n\nresponse body:\n{}",
		Complexity:    domain.ComplexityOutcome{Selected: domain.ComplexityComplex},
		Stages: []domain.StageOutcome{
			{Stage: domain.StageAnalysis, Forced: true, Enabled: true, Reason: "forced: complexity=complex"},
			{Stage: domain.StageDevelopment, Enabled: true, Reason: "selected by user"},
			{Stage: domain.StageReview, Forced: true, Reason: "forced: complexity=complex, excluded by user"},
		},
		Workflow: []domain.ActionType{domain.ActionAnalyze, domain.ActionImplement},
	}

	cases := []struct {
		name    string
		trace   domain.DecisionTrace
		verbose bool
	}{
		{"decision_jev_verbose", jevTrace(), true},
		{"decision_disagree_limited", disagree, false},
		{"decision_user", user, true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			var buf bytes.Buffer
			RenderDecision(&buf, tc.trace, tc.verbose)
			path := filepath.Join("testdata", tc.name+".golden")
			if *update {
				if err := os.WriteFile(path, buf.Bytes(), 0o644); err != nil {
					t.Fatal(err)
				}
			}
			want, err := os.ReadFile(path)
			if err != nil {
				t.Fatalf("read golden (go test ./internal/logging -update): %v", err)
			}
			if buf.String() != string(want) {
				t.Errorf("output differs from %s:\n%s", path, buf.String())
			}
		})
	}
}

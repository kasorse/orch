package domain

import (
	"encoding/json"
	"math"
	"slices"
	"strings"
	"testing"
)

func TestParseEnums(t *testing.T) {
	for _, d := range ContextDepths() {
		if got, err := ParseContextDepth(string(d)); err != nil || got != d {
			t.Errorf("ParseContextDepth(%q) = %q, %v", d, got, err)
		}
	}
	for _, c := range ComplexityOrder() {
		if got, err := ParseComplexity(string(c)); err != nil || got != c {
			t.Errorf("ParseComplexity(%q) = %q, %v", c, got, err)
		}
	}
	for _, s := range []ComplexitySource{ComplexitySourceChoice, ComplexitySourceScore} {
		if got, err := ParseComplexitySource(string(s)); err != nil || got != s {
			t.Errorf("ParseComplexitySource(%q) = %q, %v", s, got, err)
		}
	}

	if _, err := ParseContextDepth("deep"); err == nil {
		t.Error(`ParseContextDepth("deep") expected error`)
	}
	// The legacy low/medium/high scale must not parse as the new one.
	for _, in := range []string{"low", "high", "SIMPLE", ""} {
		if _, err := ParseComplexity(in); err == nil {
			t.Errorf("ParseComplexity(%q) expected error", in)
		}
	}
	if _, err := ParseComplexitySource("noul"); err == nil {
		t.Error(`ParseComplexitySource("noul") expected error`)
	}
}

func TestComplexityOrder(t *testing.T) {
	want := []Complexity{ComplexitySimple, ComplexityMedium, ComplexityComplex}
	if got := ComplexityOrder(); !slices.Equal(got, want) {
		t.Fatalf("ComplexityOrder() = %v, want %v", got, want)
	}
}

func TestStageDecisions_GetSetAll(t *testing.T) {
	var s StageDecisions
	for i, st := range StageOrder() {
		if !s.Set(st, ProbabilityDecision{Probability: float64(i) / 10}) {
			t.Fatalf("Set(%s) reported unknown stage", st)
		}
	}

	all := s.All()
	if len(all) != len(StageOrder()) {
		t.Fatalf("All() returned %d stages, want %d", len(all), len(StageOrder()))
	}
	for i, sp := range all {
		if sp.Stage != StageOrder()[i] {
			t.Errorf("All()[%d].Stage = %s, want %s", i, sp.Stage, StageOrder()[i])
		}
		if want := float64(i) / 10; sp.Decision.Probability != want {
			t.Errorf("All()[%d] probability = %v, want %v", i, sp.Decision.Probability, want)
		}
		got, ok := s.Get(sp.Stage)
		if !ok || got != sp.Decision {
			t.Errorf("Get(%s) = %v, %v; want %v", sp.Stage, got, ok, sp.Decision)
		}
	}

	if s.Set("deploy", ProbabilityDecision{Probability: 1}) {
		t.Error("Set of an unknown stage reported success")
	}
	if _, ok := s.Get("deploy"); ok {
		t.Error("Get of an unknown stage reported success")
	}
}

func validClassification() TaskClassification {
	score := 1.2
	return TaskClassification{
		ContextDepth: ContextDepthProject,
		Complexity: map[ComplexitySource]ComplexityDecision{
			ComplexitySourceChoice: {
				Probabilities: map[Complexity]float64{ComplexitySimple: 0.1, ComplexityMedium: 0.7, ComplexityComplex: 0.2},
				Confidence:    0.55,
			},
			ComplexitySourceScore: {
				Probabilities: map[Complexity]float64{ComplexitySimple: 0.05, ComplexityMedium: 0.7, ComplexityComplex: 0.25},
				Confidence:    0.6,
				Score:         &score,
			},
		},
		Stages: StageDecisions{
			Research:    ProbabilityDecision{Probability: 0.2},
			Analysis:    ProbabilityDecision{Probability: 0.9},
			Planning:    ProbabilityDecision{Probability: 0.8},
			Development: ProbabilityDecision{Probability: 0.99},
			Testing:     ProbabilityDecision{Probability: 0.95},
			Review:      ProbabilityDecision{Probability: 0.7},
		},
		Provider: "jev",
		Model:    "jev-1.13.0",
	}
}

func TestTaskClassification_Validate(t *testing.T) {
	if err := validClassification().Validate(); err != nil {
		t.Fatalf("valid classification rejected: %v", err)
	}

	onlyChoice := validClassification()
	delete(onlyChoice.Complexity, ComplexitySourceScore)
	if err := onlyChoice.Validate(); err != nil {
		t.Errorf("classification with a single complexity source rejected: %v", err)
	}

	tests := []struct {
		name    string
		mutate  func(c *TaskClassification)
		wantErr string
	}{
		{"unknown context depth", func(c *TaskClassification) { c.ContextDepth = "deep" }, "context depth"},
		{"empty context depth", func(c *TaskClassification) { c.ContextDepth = "" }, "context depth"},
		{"no complexity", func(c *TaskClassification) { c.Complexity = nil }, "no complexity"},
		{"unknown complexity source", func(c *TaskClassification) {
			c.Complexity["noul"] = c.Complexity[ComplexitySourceChoice]
		}, "complexity source"},
		{"probability above 1", func(c *TaskClassification) {
			setComplexity(c, ComplexitySourceChoice, ComplexityMedium, 1.01)
		}, "outside [0, 1]"},
		{"negative probability", func(c *TaskClassification) {
			setComplexity(c, ComplexitySourceChoice, ComplexitySimple, -0.01)
		}, "outside [0, 1]"},
		{"NaN probability", func(c *TaskClassification) {
			setComplexity(c, ComplexitySourceChoice, ComplexitySimple, math.NaN())
		}, "outside [0, 1]"},
		{"missing option", func(c *TaskClassification) {
			d := c.Complexity[ComplexitySourceChoice]
			d.Probabilities = map[Complexity]float64{ComplexitySimple: 0.2, ComplexityMedium: 0.8}
			c.Complexity[ComplexitySourceChoice] = d
		}, "has 2 options"},
		{"extra option", func(c *TaskClassification) {
			setComplexity(c, ComplexitySourceChoice, "trivial", 0)
		}, "has 4 options"},
		{"legacy option instead of new one", func(c *TaskClassification) {
			d := c.Complexity[ComplexitySourceChoice]
			d.Probabilities = map[Complexity]float64{ComplexitySimple: 0.1, ComplexityMedium: 0.7, "high": 0.2}
			c.Complexity[ComplexitySourceChoice] = d
		}, `missing option "complex"`},
		{"distribution does not sum to 1", func(c *TaskClassification) {
			setComplexity(c, ComplexitySourceChoice, ComplexityMedium, 0.9)
		}, "sum to"},
		{"confidence above 1", func(c *TaskClassification) {
			d := c.Complexity[ComplexitySourceChoice]
			d.Confidence = 1.5
			c.Complexity[ComplexitySourceChoice] = d
		}, "confidence"},
		{"score above top level", func(c *TaskClassification) {
			d := c.Complexity[ComplexitySourceScore]
			s := 2.01
			d.Score = &s
			c.Complexity[ComplexitySourceScore] = d
		}, "score"},
		{"score on a choice distribution", func(c *TaskClassification) {
			d := c.Complexity[ComplexitySourceChoice]
			s := 1.0
			d.Score = &s
			c.Complexity[ComplexitySourceChoice] = d
		}, "must not carry a score"},
		{"stage probability above 1", func(c *TaskClassification) { c.Stages.Review.Probability = 1.01 }, "stage review"},
		{"negative stage probability", func(c *TaskClassification) { c.Stages.Research.Probability = -0.01 }, "stage research"},
		{"NaN stage probability", func(c *TaskClassification) { c.Stages.Testing.Probability = math.NaN() }, "stage testing"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			c := validClassification()
			tt.mutate(&c)
			err := c.Validate()
			if err == nil {
				t.Fatal("expected error, got nil")
			}
			if !strings.Contains(err.Error(), tt.wantErr) {
				t.Errorf("error %q does not mention %q", err, tt.wantErr)
			}
		})
	}
}

// setComplexity overwrites one option's probability in src's distribution.
func setComplexity(c *TaskClassification, src ComplexitySource, option Complexity, p float64) {
	d := c.Complexity[src]
	probs := make(map[Complexity]float64, len(d.Probabilities)+1)
	for k, v := range d.Probabilities {
		probs[k] = v
	}
	probs[option] = p
	d.Probabilities = probs
	c.Complexity[src] = d
}

func TestContextDepthDecision_Validate(t *testing.T) {
	valid := ContextDepthDecision{
		Probabilities: map[ContextDepth]float64{ContextDepthNone: 0.1, ContextDepthProject: 0.9},
		Confidence:    0.8,
	}
	if err := valid.Validate(); err != nil {
		t.Fatalf("valid decision rejected: %v", err)
	}

	invalid := []ContextDepthDecision{
		{Probabilities: map[ContextDepth]float64{ContextDepthProject: 1}, Confidence: 1},
		{Probabilities: map[ContextDepth]float64{ContextDepthNone: 0.6, ContextDepthProject: 0.6}, Confidence: 0.2},
		{Probabilities: map[ContextDepth]float64{ContextDepthNone: 0.1, ContextDepthProject: 0.9}, Confidence: -0.1},
	}
	for i, d := range invalid {
		if err := d.Validate(); err == nil {
			t.Errorf("invalid decision %d accepted", i)
		}
	}
}

// The trace written to decision.json relies on these JSON names.
func TestTaskClassification_JSON(t *testing.T) {
	raw, err := json.Marshal(validClassification())
	if err != nil {
		t.Fatal(err)
	}
	for _, key := range []string{
		`"context_depth":"project"`,
		`"choice":{"probabilities"`,
		`"score":1.2`,
		`"development":{"probability":0.99}`,
		`"model":"jev-1.13.0"`,
	} {
		if !strings.Contains(string(raw), key) {
			t.Errorf("JSON %s does not contain %s", raw, key)
		}
	}

	var back TaskClassification
	if err := json.Unmarshal(raw, &back); err != nil {
		t.Fatal(err)
	}
	if err := back.Validate(); err != nil {
		t.Errorf("round-tripped classification invalid: %v", err)
	}
}

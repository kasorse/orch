package report

import (
	"errors"
	"strings"
	"testing"
	"time"

	"orchestrator/internal/domain"
)

func trace(source domain.DecisionSource, model string, choice, score domain.Complexity, request string) domain.DecisionTrace {
	sources := map[domain.ComplexitySource]domain.ComplexitySourceOutcome{
		domain.ComplexitySourceChoice: {Selected: choice, Decision: domain.ComplexityDecision{Confidence: 0.8}},
	}
	if score != "" {
		sources[domain.ComplexitySourceScore] = domain.ComplexitySourceOutcome{Selected: score, Decision: domain.ComplexityDecision{Confidence: 0.6}}
	}
	return domain.DecisionTrace{
		Request: request, Source: source, Provider: "jev", Model: model,
		Complexity: domain.ComplexityOutcome{Primary: domain.ComplexitySourceChoice, Sources: sources, Selected: choice},
	}
}

func TestSummarizeDecisions(t *testing.T) {
	at := time.Date(2026, 10, 1, 12, 0, 0, 0, time.UTC)
	entries := []Entry{
		{"aaaaaaaa-0000-4000-8000-000000000001", at, trace(domain.DecisionSourceJev, "jev-1.13.0", "medium", "medium", "a")},
		{"aaaaaaaa-0000-4000-8000-000000000002", at.Add(2 * time.Hour), trace(domain.DecisionSourceJev, "jev-1.13.0", "medium", "complex", "second\nline")},
		{"aaaaaaaa-0000-4000-8000-000000000003", at.Add(time.Hour), trace(domain.DecisionSourceJev, "jev-1.13.0", "simple", "medium", "first")},
		{"aaaaaaaa-0000-4000-8000-000000000004", at, trace(domain.DecisionSourceJev, "mock:jev-1.13.0", "simple", "simple", "mock")},
		{"aaaaaaaa-0000-4000-8000-000000000005", at, trace(domain.DecisionSourceJev, "jev-1.13.0", "simple", "", "no shadow")},
		{"aaaaaaaa-0000-4000-8000-000000000006", at, domain.DecisionTrace{Source: domain.DecisionSourceManual}},
		{"aaaaaaaa-0000-4000-8000-000000000007", at, domain.DecisionTrace{Source: domain.DecisionSourceUser}},
	}
	unreadable := []Unreadable{{"bbbbbbbb-0000-4000-8000-000000000001", errors.New("decision.json is not a valid decision trace\nmore")}}

	s := SummarizeDecisions(entries, unreadable)
	if s.Traces != 7 || s.Compared != 3 || s.Agree != 1 {
		t.Fatalf("traces/compared/agree = %d/%d/%d, want 7/3/1", s.Traces, s.Compared, s.Agree)
	}
	for reason, want := range map[string]int{SkipManual: 1, SkipUser: 1, SkipMock: 1, SkipNoShadow: 1} {
		if s.Skipped[reason] != want {
			t.Errorf("skipped[%s] = %d, want %d", reason, s.Skipped[reason], want)
		}
	}
	if s.Matrix[1][1] != 1 || s.Matrix[1][2] != 1 || s.Matrix[0][1] != 1 {
		t.Errorf("matrix = %v", s.Matrix)
	}
	if got := s.MeanConfidence[domain.ComplexitySourceScore]; got < 0.599 || got > 0.601 {
		t.Errorf("mean score confidence = %v, want 0.6", got)
	}
	if len(s.Disagreements) != 2 || s.Disagreements[0].Request != "first" {
		t.Errorf("disagreements must be in time order: %+v", s.Disagreements)
	}

	var b strings.Builder
	RenderDecisions(&b, ".orchestrator/runs", s)
	out := b.String()
	for _, want := range []string{
		"traces: 7, compared: 3",
		"skipped: 1 manual (--stage), 1 chosen by user, 1 mock, 1 without the shadow question, 1 unreadable",
		"  bbbbbbbb: decision.json is not a valid decision trace\n",
		"decided by: jev jev/jev-1.13.0 (3)",
		"choice vs score: 1 of 3 agree (33.3%)",
		"medium            0        1        1",
		"mean confidence: choice 0.80, score 0.60",
		"disagreements (2):",
		`choice=simple  score=medium  "first"`,
		`choice=medium  score=complex "second"`,
	} {
		if !strings.Contains(out, want) {
			t.Errorf("report lacks %q:\n%s", want, out)
		}
	}
}

func TestRenderDecisions_Empty(t *testing.T) {
	var b strings.Builder
	RenderDecisions(&b, "runs", SummarizeDecisions(nil, nil))
	if !strings.Contains(b.String(), "no decision traces yet") {
		t.Errorf("empty report:\n%s", b.String())
	}

	b.Reset()
	RenderDecisions(&b, "runs", SummarizeDecisions([]Entry{{RunID: "x", Trace: domain.DecisionTrace{Source: domain.DecisionSourceManual}}}, nil))
	if !strings.Contains(b.String(), "nothing to compare") {
		t.Errorf("report with nothing comparable:\n%s", b.String())
	}
}

func TestExcerpt(t *testing.T) {
	if got := excerpt("  Исправь retry\nвторая строка", 9); got != "Исправь …" {
		t.Errorf("excerpt = %q", got)
	}
}

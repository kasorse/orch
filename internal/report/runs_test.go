package report

import (
	"flag"
	"math"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/kasorse/orch/internal/domain"
)

var update = flag.Bool("update", false, "rewrite the golden files")

// assertGolden compares got with testdata/<name>.golden, which -update rewrites.
func assertGolden(t *testing.T, name, got string) {
	t.Helper()
	path := filepath.Join("testdata", name+".golden")
	if *update {
		if err := os.MkdirAll("testdata", 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte(got), 0o644); err != nil {
			t.Fatal(err)
		}
		return
	}
	want, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if got != string(want) {
		t.Errorf("%s differs from its golden file (go test ./internal/report -update rewrites it):\n--- got\n%s--- want\n%s", name, got, want)
	}
}

func outcome(status domain.RunStatus, costUSD, decisionUSD float64) *domain.Outcome {
	return &domain.Outcome{
		Status:           status,
		Usage:            domain.OutcomeUsage{Usage: domain.Usage{CostUSD: costUSD}, Calls: 1},
		DecisionOverhead: domain.DecisionOverhead{CostUSD: decisionUSD, Calls: 1},
	}
}

// runEntry is a run decided as complexity by source (neither: it has no decision) that
// ran actions in that order.
func runEntry(id string, status domain.RunStatus, source domain.DecisionSource, complexity domain.Complexity, actions []domain.ActionType, out *domain.Outcome) RunEntry {
	run := domain.Run{ID: id, Status: status, Outcome: out}
	if source != "" {
		run.Decision = &domain.Decision{Source: source, Complexity: domain.ComplexityOutcome{Selected: complexity}}
	}
	if actions != nil {
		run.Workflow = &domain.Workflow{}
		for _, a := range actions {
			run.Workflow.Steps = append(run.Workflow.Steps, domain.Step{Action: a})
		}
	}
	return RunEntry{RunID: id, Run: run}
}

func renderRuns(entries []RunEntry, unreadable []UnreadableRun) string {
	var traces []Entry
	for _, e := range entries {
		if e.Trace != nil {
			traces = append(traces, Entry{RunID: e.RunID, At: e.At, Trace: *e.Trace})
		}
	}
	var b strings.Builder
	RenderRuns(&b, "runs", SummarizeRuns(entries, unreadable), SummarizeDecisions(traces, nil))
	return b.String()
}

var (
	implementTest       = []domain.ActionType{domain.ActionImplement, domain.ActionTest}
	implementTestReview = []domain.ActionType{domain.ActionImplement, domain.ActionTest, domain.ActionReview}
	implementOnly       = []domain.ActionType{domain.ActionImplement}
)

func TestRenderRuns_Empty(t *testing.T) {
	assertGolden(t, "runs_empty", renderRuns(nil, nil))
}

func TestRenderRuns_MixedStatuses(t *testing.T) {
	entries := []RunEntry{
		runEntry("r1", domain.RunCompleted, domain.DecisionSourceJev, domain.ComplexitySimple, implementTest, outcome(domain.RunCompleted, 0.50, 0.05)),
		runEntry("r2", domain.RunFailed, domain.DecisionSourceJev, domain.ComplexitySimple, implementTest, outcome(domain.RunFailed, 0.25, 0.05)),
		runEntry("r3", domain.RunCompleted, domain.DecisionSourceLLM, domain.ComplexityComplex, implementTestReview, outcome(domain.RunCompleted, 2.00, 0.20)),
		runEntry("r4", domain.RunStopped, domain.DecisionSourceJev, domain.ComplexityMedium, implementTestReview, outcome(domain.RunStopped, 1.00, 0.10)),
	}
	assertGolden(t, "runs_mixed", renderRuns(entries, nil))
}

func TestRenderRuns_ManualRuns(t *testing.T) {
	entries := []RunEntry{
		runEntry("m1", domain.RunCompleted, domain.DecisionSourceManual, "", implementOnly, outcome(domain.RunCompleted, 0.40, 0)),
		// Died before it had a decision or a workflow.
		runEntry("m2", domain.RunPending, "", "", nil, nil),
		runEntry("c1", domain.RunCompleted, domain.DecisionSourceJev, domain.ComplexitySimple, implementOnly, outcome(domain.RunCompleted, 0.20, 0)),
	}
	assertGolden(t, "runs_manual", renderRuns(entries, nil))
}

// Runs written before outcome and usage were recorded: nothing but their own record to
// derive the outcome from, and no cost.
func TestRenderRuns_OldRunsWithoutOutcome(t *testing.T) {
	exec := func(a domain.ActionType) domain.StepExecution {
		return domain.StepExecution{Action: a, Status: domain.StepCompleted}
	}
	old := func(id string, status domain.RunStatus, source domain.DecisionSource, c domain.Complexity, actions []domain.ActionType) RunEntry {
		e := runEntry(id, status, source, c, actions, nil)
		e.Run.Executions = []domain.StepExecution{exec(domain.ActionImplement), exec(domain.ActionTest)}
		if source != "" {
			e.Trace = &domain.DecisionTrace{Source: source, Latency: domain.DecisionLatency{ClassificationMS: 40}}
		}
		return e
	}
	entries := []RunEntry{
		old("o1", domain.RunFailed, domain.DecisionSourceJev, domain.ComplexityMedium, implementTest),
		old("o2", domain.RunCompleted, domain.DecisionSourceJev, domain.ComplexityMedium, implementTest),
		old("o3", domain.RunStopped, domain.DecisionSourceManual, "", implementOnly),
	}
	out := renderRuns(entries, nil)
	for _, bad := range []string{"NaN", "Inf"} {
		if strings.Contains(out, bad) {
			t.Errorf("report contains %q:\n%s", bad, out)
		}
	}
	assertGolden(t, "runs_old", out)
}

func TestRenderRuns_UnreadableRunsListed(t *testing.T) {
	entries := []RunEntry{
		runEntry("r1", domain.RunCompleted, domain.DecisionSourceJev, domain.ComplexitySimple, implementOnly, outcome(domain.RunCompleted, 0.10, 0.01)),
	}
	unreadable := []UnreadableRun{
		{Path: "taskA/cfg1/rep2", Err: "run.json: unknown status \"weird\"\nsecond line"},
		{Path: "taskB/rep1", Err: "cannot read decision.json"},
	}
	assertGolden(t, "runs_unreadable", renderRuns(entries, unreadable))
}

func TestRenderRuns_OnlyUnreadable(t *testing.T) {
	out := renderRuns(nil, []UnreadableRun{{Path: "a", Err: "broken"}})
	if !strings.Contains(out, "runs: 0, unreadable: 1\n  a: broken\n") || strings.Contains(out, "by status") {
		t.Errorf("report:\n%s", out)
	}
}

func TestSummarizeRuns_Groups(t *testing.T) {
	entries := []RunEntry{
		runEntry("r1", domain.RunCompleted, domain.DecisionSourceJev, domain.ComplexitySimple, implementTest, outcome(domain.RunCompleted, 1, 0.25)),
		runEntry("r2", domain.RunFailed, domain.DecisionSourceJev, domain.ComplexitySimple, implementTest, outcome(domain.RunFailed, 3, 0.75)),
		runEntry("r3", domain.RunCompleted, domain.DecisionSourceManual, "", implementOnly, outcome(domain.RunCompleted, 0, 0)),
	}
	s := SummarizeRuns(entries, nil)

	if s.Total.Runs != 3 || s.Total.Completed != 2 || s.Total.CostUSD != 4 {
		t.Fatalf("total = %+v", s.Total)
	}
	if len(s.ByComplexity) != 2 || s.ByComplexity[0].Key != "simple" || s.ByComplexity[1].Key != noValue {
		t.Fatalf("by complexity = %+v, want simple, then runs without one", s.ByComplexity)
	}
	simple := s.ByComplexity[0]
	if share, ok := simple.DecisionShare(); !ok || share != 0.25 {
		t.Errorf("decision share = %v, %v, want 0.25", share, ok)
	}
	if mean, ok := simple.MeanCostUSD(); !ok || mean != 2 {
		t.Errorf("mean cost = %v, %v, want 2", mean, ok)
	}
	if share, ok := simple.CompletedShare(); !ok || share != 0.5 {
		t.Errorf("completed share = %v, %v, want 0.5", share, ok)
	}
	if len(s.ByStageSet) != 2 || s.ByStageSet[0].Key != "implement → test" {
		t.Errorf("by stage set = %+v, want the most common first", s.ByStageSet)
	}
}

func TestSummarizeRuns_OutcomeFallsBackToTheRecord(t *testing.T) {
	e := runEntry("r1", domain.RunFailed, domain.DecisionSourceJev, domain.ComplexitySimple, implementOnly, nil)
	e.Run.Executions = []domain.StepExecution{{Action: domain.ActionImplement, Usage: &domain.Usage{CostUSD: 0.5}}}
	e.Trace = &domain.DecisionTrace{Source: domain.DecisionSourceJev,
		Usage: &domain.DecisionUsage{Classification: &domain.Usage{CostUSD: 0.1}}}

	s := SummarizeRuns([]RunEntry{e}, nil)
	if got := s.Total.CostUSD; math.Abs(got-0.6) > 1e-9 {
		t.Errorf("cost = %v, want 0.6 (the step and the decision call)", got)
	}
	if got := s.Total.DecisionCostUSD; math.Abs(got-0.1) > 1e-9 {
		t.Errorf("decision cost = %v, want 0.1", got)
	}
	if len(s.ByStatus) != 1 || s.ByStatus[0].Status != domain.RunFailed {
		t.Errorf("by status = %+v", s.ByStatus)
	}
}

func TestRunGroup_NoDivisionByZero(t *testing.T) {
	var g RunGroup
	if _, ok := g.CompletedShare(); ok {
		t.Error("completed share of no runs must be undefined")
	}
	if _, ok := g.MeanCostUSD(); ok {
		t.Error("mean cost of no runs must be undefined")
	}
	g = RunGroup{Runs: 2}
	if _, ok := g.DecisionShare(); ok {
		t.Error("decision share of a free group must be undefined")
	}

	// A cost that is not a cost counts as nothing.
	var total RunGroup
	total.add(domain.RunCompleted, domain.Outcome{Usage: domain.OutcomeUsage{Usage: domain.Usage{CostUSD: math.NaN()}},
		DecisionOverhead: domain.DecisionOverhead{CostUSD: math.Inf(1)}})
	if total.CostUSD != 0 || total.DecisionCostUSD != 0 {
		t.Errorf("group = %+v", total)
	}
}

func TestRenderRuns_DecisionsSectionFollows(t *testing.T) {
	e := runEntry("r1", domain.RunCompleted, domain.DecisionSourceJev, domain.ComplexitySimple, implementOnly, outcome(domain.RunCompleted, 1, 0))
	tr := trace(domain.DecisionSourceJev, "jev-1.13.0", "simple", "medium", "fix it")
	e.Trace = &tr
	e.At = time.Date(2026, 10, 1, 12, 0, 0, 0, time.UTC)

	out := renderRuns([]RunEntry{e}, nil)
	runs, decisions, ok := strings.Cut(out, "\nDecisions report: runs\n")
	if !ok {
		t.Fatalf("no decisions section:\n%s", out)
	}
	if !strings.Contains(runs, "by complexity") || !strings.Contains(decisions, "choice vs score: 0 of 1 agree") {
		t.Errorf("runs:\n%s\ndecisions:\n%s", runs, decisions)
	}
}

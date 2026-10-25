package integration

import (
	"testing"

	"orchestrator/internal/testutil/fakeexec"
)

// Scenario 38, acceptance criteria (specs/quality/SPEC.md §7.1): the plan lists two criteria,
// the first review finds the second unmet and fails although it reports no issue, implement is
// sent back with that criterion and the evidence, and the second review finds both met.

const criteriaPlanResponse = `{
  "summary": "fix the operator",
  "steps": ["use + in Add"],
  "affected_files": ["fixture.go"],
  "risks": [],
  "acceptance_criteria": ["Add returns the sum of its arguments", "a test covers Add"]
}`

func TestScenario38_UnmetCriterionThenMet(t *testing.T) {
	repoDir := fakeexec.InitGitFixture(t)
	r := newQualityRun(t, repoDir)
	writeResponse(t, r.responses, "investigate", criteriaPlanResponse)
	writeResponse(t, r.responses, "review_1", reviewJSON("fail", "[]",
		`[{"index": 1, "met": true, "evidence": "fixture.go:4"}, {"index": 2, "met": false, "evidence": "no test calls Add with negative numbers"}]`))
	writeResponse(t, r.responses, "review_2", reviewJSON("pass", "[]",
		`[{"index": 1, "met": true, "evidence": "fixture.go:4"}, {"index": 2, "met": true, "evidence": "fixture_test.go:6"}]`))

	stdout, stderr, err := runOrchestrator(t, repoDir, r.cfg, "run",
		"--stage", "investigation", "--stage", "development", "--stage", "testing", "--stage", "review",
		"fix Add in fixture.go")
	mustSucceed(t, stdout, stderr, err)

	if got := invocations(t, r.invocationsLog); !equalStrings(got, []string{"investigate", "implement", "review", "implement", "review"}) {
		t.Fatalf("invocations = %v", got)
	}

	// The review is asked for a verdict on each numbered criterion.
	assertContains(t, "the review prompt", r.prompt(t, "review", 1),
		"Acceptance criteria:\n1. Add returns the sum of its arguments\n2. a test covers Add\n",
		`"criteria": [`)

	// The first attempt is told nothing of the kind; the retry gets the unmet criterion and why.
	assertNotContains(t, "the first implement prompt", r.prompt(t, "implement", 1), "Unmet acceptance criteria")
	assertContains(t, "the retried implement prompt", r.prompt(t, "implement", 2),
		"A code review FAILED on the previous implementation. Address every unmet criterion below.",
		"Unmet acceptance criteria:\n- 2. a test covers Add\n  evidence: no test calls Add with negative numbers")
	assertNotContains(t, "the retried implement prompt", r.prompt(t, "implement", 2), "1. Add returns the sum")

	run := loadQL(t, repoDir)
	if run.Status != "completed" || run.FixIterations != 1 {
		t.Errorf("status = %s, fix_iterations = %d", run.Status, run.FixIterations)
	}
	reviews := run.ofAction("review")
	if len(reviews) != 2 || reviews[0].ReviewResult == nil || reviews[1].ReviewResult == nil {
		t.Fatalf("reviews = %+v", reviews)
	}
	first := reviews[0].ReviewResult
	if len(first.Issues) != 0 || len(first.Criteria) != 2 || !first.Criteria[0].Met || first.Criteria[1].Met {
		t.Errorf("the first review = %+v", first)
	}
	// Go filled in the criterion's text from the plan; the model echoed only the index.
	if c := first.Criteria[1]; c.Index != 2 || c.Criterion != "a test covers Add" {
		t.Errorf("criterion 2 = %+v", c)
	}
	for _, c := range reviews[1].ReviewResult.Criteria {
		if !c.Met {
			t.Errorf("the second review has an unmet criterion: %+v", c)
		}
	}
}

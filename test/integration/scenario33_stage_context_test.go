package integration

import (
	"testing"

	"orchestrator/internal/testutil/fakeexec"
)

// Scenario 33, stage context (specs/quality/SPEC.md §4): what an earlier stage found reaches
// the later ones. The research reaches the plan, the plan's files and risks reach implement,
// the plan's steps, risks and acceptance criteria reach review, and a retried implement is
// shown the diff stat of what the run has changed so far.

const (
	ctxResearchResponse = `{
  "summary": "Integer addition in Go needs no library.",
  "findings": ["the + operator is enough"],
  "options": [],
  "recommendation": "use the + operator",
  "references": []
}`

	ctxPlanResponse = `{
  "summary": "fix the operator",
  "steps": ["use + in Add"],
  "affected_files": ["fixture.go", "fixture_test.go"],
  "risks": ["tests pin the old behavior"],
  "acceptance_criteria": ["Add(2, 3) returns 5", "go test ./... passes"]
}`
)

// ctxReviewResponse passes with a verdict for each of the plan's two criteria.
var ctxReviewResponse = reviewJSON("pass", "[]",
	`[{"index": 1, "met": true, "evidence": "fixture.go:4"}, {"index": 2, "met": true, "evidence": "the test step passed"}]`)

func TestScenario33_StageContextReachesLaterStages(t *testing.T) {
	repoDir := fakeexec.InitGitFixture(t)
	r := newQualityRun(t, repoDir)
	writeResponse(t, r.responses, "research", ctxResearchResponse)
	writeResponse(t, r.responses, "plan", ctxPlanResponse)
	writeResponse(t, r.responses, "review", ctxReviewResponse)
	// The first implement edits the file without fixing it, so the tests fail and implement
	// runs again with something to show; the second fixes it.
	fixOnCall(t, 2)
	appendOnEveryImplement(t, repoDir)

	stdout, stderr, err := runOrchestrator(t, repoDir, r.cfg, "run",
		"--stage", "research", "--stage", "planning", "--stage", "development", "--stage", "testing", "--stage", "review",
		"fix Add in fixture.go")
	mustSucceed(t, stdout, stderr, err)

	if got := invocations(t, r.invocationsLog); !equalStrings(got, []string{"research", "plan", "implement", "implement", "review"}) {
		t.Fatalf("invocations = %v", got)
	}

	assertContains(t, "the plan prompt", r.prompt(t, "plan", 1),
		"Research:\nInteger addition in Go needs no library.",
		"- the + operator is enough",
		"Recommendation: use the + operator")

	first := r.prompt(t, "implement", 1)
	assertNotContains(t, "the first implement prompt", first, "Analysis:")
	assertContains(t, "the first implement prompt", first,
		"Steps:\n- use + in Add\n",
		"Affected files (from plan):\n- fixture.go\n- fixture_test.go\n",
		"Risks (from plan):\n- tests pin the old behavior\n",
		"Acceptance criteria:\n- Add(2, 3) returns 5\n- go test ./... passes")
	assertNotContains(t, "the first implement prompt", first, "What the run has changed so far")

	retry := r.prompt(t, "implement", 2)
	assertContains(t, "the retried implement prompt", retry,
		"Previous implementation attempt:",
		"What the run has changed so far (git diff --stat against the run's start):",
		" fixture.go | 1 +\n",
		" 1 file changed, 1 insertion(+)",
		"The previous implementation FAILED the test command.")
	// The context is still there on the retry.
	assertContains(t, "the retried implement prompt", retry, "Acceptance criteria:\n- Add(2, 3) returns 5\n")

	assertContains(t, "the review prompt", r.prompt(t, "review", 1),
		"Steps:\n- use + in Add\n",
		"Risks (from plan):\n- tests pin the old behavior\n",
		"Acceptance criteria:\n1. Add(2, 3) returns 5\n2. go test ./... passes\n")

	run := loadQL(t, repoDir)
	if run.Status != "completed" {
		t.Errorf("status = %s", run.Status)
	}
	// The review answered with a verdict per criterion, and Go filled the criteria in.
	reviews := run.ofAction("review")
	if len(reviews) != 1 || reviews[0].ReviewResult == nil || len(reviews[0].ReviewResult.Criteria) != 2 {
		t.Fatalf("reviews = %+v", reviews)
	}
	if c := reviews[0].ReviewResult.Criteria[1]; c.Criterion != "go test ./... passes" || !c.Met {
		t.Errorf("criterion 2 = %+v", c)
	}
}

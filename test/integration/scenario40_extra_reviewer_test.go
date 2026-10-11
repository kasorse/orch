package integration

import (
	"testing"

	"orchestrator/internal/testutil/fakeexec"
)

// Scenario 40, extra reviewers (specs/quality/SPEC.md §7.5): with actions.review.models the
// review step asks the base model and then each extra model, with the same prompt, and Go
// merges the answers. The base reviewer passes, the second finds a high issue, so the merged
// review fails and implement runs again; the second round has both reviewers pass.

func TestScenario40_SecondReviewersHighIssueForcesARetry(t *testing.T) {
	repoDir := fakeexec.InitGitFixture(t)
	r := newQualityRun(t, repoDir, func(o *configOptions) { o.reviewModels = []string{"fake-second"} })
	// The reviews are numbered by call: each round makes two (base model, then fake-second).
	writeResponse(t, r.responses, "review_1", reviewPassResponse)
	writeResponse(t, r.responses, "review_2", reviewJSON("fail",
		`[{"severity": "high", "file": "fixture.go", "description": "the second reviewer found a regression"}]`, ""))
	writeResponse(t, r.responses, "review_3", reviewPassResponse)
	writeResponse(t, r.responses, "review_4", reviewPassResponse)

	stdout, stderr, err := runOrchestrator(t, repoDir, r.cfg, append(implementTestReview, "fix Add in fixture.go")...)
	mustSucceed(t, stdout, stderr, err)

	if got := invocations(t, r.invocationsLog); !equalStrings(got, []string{"implement", "review", "review", "implement", "review", "review"}) {
		t.Fatalf("invocations = %v", got)
	}
	// The last call of the step was the extra reviewer's, on its own model.
	if got := argAfter(r.args(t, "review"), "--model"); got != "fake-second" {
		t.Errorf("the last review call ran on model %q, want fake-second", got)
	}
	assertContains(t, "the retried implement prompt", r.prompt(t, "implement", 2),
		"A code review FAILED on the previous implementation. Address every issue below.",
		"- [high] fixture.go: the second reviewer found a regression")

	run := loadQL(t, repoDir)
	if run.Status != "completed" || run.FixIterations != 1 {
		t.Errorf("status = %s, fix_iterations = %d", run.Status, run.FixIterations)
	}
	reviews := run.ofAction("review")
	if len(reviews) != 2 || reviews[0].ReviewResult == nil || reviews[1].ReviewResult == nil {
		t.Fatalf("reviews = %+v", reviews)
	}

	// The merged result fails on the union of the issues, and keeps both answers.
	merged := reviews[0].ReviewResult
	if merged.Status != "fail" || len(merged.Issues) != 1 || merged.Issues[0].Severity != "high" {
		t.Errorf("the merged first review = %+v", merged)
	}
	if len(merged.Reviewers) != 2 || merged.Reviewers[0].Model != "fake" || merged.Reviewers[1].Model != "fake-second" {
		t.Fatalf("reviewers = %+v", merged.Reviewers)
	}
	if first := merged.Reviewers[0].Result; first.Status != "pass" || len(first.Issues) != 0 {
		t.Errorf("the base reviewer's own answer = %+v", first)
	}
	if second := merged.Reviewers[1].Result; len(second.Issues) != 1 || second.Issues[0].Description != "the second reviewer found a regression" {
		t.Errorf("the second reviewer's own answer = %+v", second)
	}
	if c := reviews[0].Call; c == nil || len(c.ExtraCalls) != 1 || c.ExtraCalls[0].Model != "fake-second" {
		t.Errorf("call = %+v, want one extra call on fake-second", c)
	}

	if final := reviews[1].ReviewResult; final.Status != "pass" || len(final.Reviewers) != 2 {
		t.Errorf("the second round's review = %+v", final)
	}
	assertContains(t, "summary.md", mustRunFile(t, repoDir, "summary.md"), "Reviewers", "fake-second")
}

// mustRunFile reads a file of the single run directory, failing the test when it is absent.
func mustRunFile(t *testing.T, repoDir, name string) string {
	t.Helper()
	s, ok := runFile(t, repoDir, name)
	if !ok {
		t.Fatalf("%s was not written", name)
	}
	return s
}

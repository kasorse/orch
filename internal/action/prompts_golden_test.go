package action

import (
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"orchestrator/internal/domain"
	"orchestrator/internal/story"
)

var update = flag.Bool("update", false, "rewrite the golden files in testdata/prompts from the current prompts")

// goldenFile names the golden file of a case whose name is not <case>.golden: the planning
// calls of a story, named as specs/story/tasks/TASK-ST03-survey-specify.md lists them.
var goldenFile = map[string]string{"survey": "survey.txt", "specify": "specify.txt"}

const goldenStoryRequest = "Partial refunds in billing and gateway: refund part of a payment, and accept an amount in the public API."

func goldenSurveys() []SurveyInput {
	return []SurveyInput{
		{Repo: "gateway", Answer: story.SurveyAnswer{
			Summary:       "The public API forwards refunds to billing.",
			RelevantFiles: []string{"api/openapi.yaml"},
			Interfaces:    []string{"POST /v1/refunds (api/openapi.yaml)"},
		}},
		{Repo: "billing", Answer: story.SurveyAnswer{
			Summary:       "Refunds are created in internal/refund; a refund always returns the whole amount.\n",
			RelevantFiles: []string{"internal/refund/service.go", "internal/refund/service_test.go"},
			Interfaces:    []string{"event refund.created (events/refund.proto)"},
			Constraints:   []string{"amounts are\ninteger cents"},
		}},
	}
}

// fixNonce makes every delimiter nonce predictable (NONCE0001, NONCE0002, ... in the order
// the prompt draws them) for the test, and restores the random one afterwards.
func fixNonce(t *testing.T) {
	t.Helper()
	orig := nonce
	n := 0
	nonce = func() string { n++; return fmt.Sprintf("NONCE%04d", n) }
	t.Cleanup(func() { nonce = orig })
}

const goldenTask = "Store.Put panics on a nil map. Fix it."

func goldenResearch() *domain.ResearchArtifact {
	return &domain.ResearchArtifact{
		Summary:        "A write to a nil map panics; the usual fix is to initialise it in the constructor.",
		Findings:       []string{"writing to a nil map panics", "reading from one does not"},
		Recommendation: "initialise the map in NewStore",
	}
}

func goldenPlan() *domain.InvestigationArtifact {
	return &domain.InvestigationArtifact{
		Summary:            "Initialise the map in NewStore and guard Put.",
		Steps:              []string{"add make() in NewStore", "add a test for Put on a fresh store"},
		AffectedFiles:      []string{"internal/store/store.go", "internal/store/store_test.go"},
		Risks:              []string{"Store{} literals in tests"},
		AcceptanceCriteria: []string{"Put on a store from NewStore does not panic", "a test covers Put on a fresh store"},
	}
}

func goldenImplementation() *domain.ImplementationArtifact {
	return &domain.ImplementationArtifact{Summary: "Added make() in NewStore and a test for Put on a fresh store."}
}

const goldenDiff = "diff --git a/internal/store/store.go b/internal/store/store.go\n+s.m = make(map[string]string)\n"

const goldenRunDiffStat = " internal/store/store.go | 4 +++-\n 1 file changed, 3 insertions(+), 1 deletion(-)"

func goldenFailingTest() *domain.TestResult {
	return &domain.TestResult{
		Command: "go test ./...", ExitCode: 1,
		Stdout: "--- FAIL: TestPutFresh (0.00s)\n    store_test.go:12: Put panicked\nFAIL\n",
		Stderr: "exit status 1",
	}
}

// goldenFailingCheck is a test step that stopped at the check vet: the top-level fields are
// the check's and the tests did not run (specs/quality/SPEC.md §5.3).
func goldenFailingCheck() *domain.TestResult {
	return &domain.TestResult{
		Command: "go vet ./...", ExitCode: 1,
		Stdout: "internal/store/store.go:21:2: unreachable code\n",
		Stderr: "exit status 1",
		Checks: []domain.CheckResult{
			{Name: "build", Success: true},
			{Name: "vet", ExitCode: 1},
		},
		FailedCheck: "vet",
	}
}

func TestPrompts_Golden(t *testing.T) {
	retry := &domain.ImplementationArtifact{Summary: "Added make() in NewStore."}
	noRisks := goldenPlan()
	noRisks.Risks = nil

	cases := []struct {
		name  string
		build func() string
	}{
		{"research", func() string { return BuildResearchPrompt(ResearchPromptInput{Task: goldenTask}) }},
		{"research_final", func() string { return BuildResearchPrompt(ResearchPromptInput{Task: goldenTask, FinalAnswer: true}) }},
		{"investigate_research", func() string {
			return BuildInvestigatePrompt(InvestigatePromptInput{Task: goldenTask, Research: goldenResearch()})
		}},
		{"investigate_final", func() string {
			return BuildInvestigatePrompt(InvestigatePromptInput{Task: goldenTask, FinalAnswer: true})
		}},
		{"implement_first", func() string {
			return BuildImplementPrompt(ImplementPromptInput{Task: goldenTask, Plan: goldenPlan()})
		}},
		{"investigate_clarified", func() string {
			return BuildInvestigatePrompt(InvestigatePromptInput{Task: goldenTask, Clarification: goldenClarification})
		}},
		{"implement_clarified", func() string {
			return BuildImplementPrompt(ImplementPromptInput{Task: goldenTask, Clarification: goldenClarification, Plan: goldenPlan()})
		}},
		{"implement_verify", func() string {
			return BuildImplementPrompt(ImplementPromptInput{Task: goldenTask, Plan: goldenPlan(), Verify: true})
		}},
		{"implement_verify_checks", func() string {
			return BuildImplementPrompt(ImplementPromptInput{Task: goldenTask, Plan: goldenPlan(), Verify: true, VerifyChecks: true})
		}},
		{"implement_replace", func() string {
			return BuildImplementPrompt(ImplementPromptInput{Task: goldenTask, Plan: goldenPlan(), Replace: true})
		}},
		{"implement_retry_test", func() string {
			return BuildImplementPrompt(ImplementPromptInput{
				Task: goldenTask, Plan: goldenPlan(),
				PreviousImplementation: retry, RunDiffStat: goldenRunDiffStat, LastTestResult: goldenFailingTest(),
			})
		}},
		{"implement_retry_check", func() string {
			return BuildImplementPrompt(ImplementPromptInput{
				Task: goldenTask, Plan: goldenPlan(),
				PreviousImplementation: retry, RunDiffStat: goldenRunDiffStat, LastTestResult: goldenFailingCheck(),
			})
		}},
		{"implement_retry_review", func() string {
			return BuildImplementPrompt(ImplementPromptInput{
				Task: goldenTask, Plan: goldenPlan(),
				PreviousImplementation: retry, RunDiffStat: goldenRunDiffStat,
				ReviewIssues: []domain.ReviewIssue{{Severity: domain.SeverityHigh, File: "internal/store/store.go", Description: "Put still panics on a zero Store"}},
			})
		}},
		{"implement_retry_review_criteria", func() string {
			return BuildImplementPrompt(ImplementPromptInput{
				Task: goldenTask, Plan: goldenPlan(),
				PreviousImplementation: retry, RunDiffStat: goldenRunDiffStat,
				UnmetCriteria: []domain.CriterionVerdict{{Index: 2, Criterion: goldenPlan().AcceptanceCriteria[1], Evidence: "no test calls Put on a fresh store"}},
				ReviewIssues:  []domain.ReviewIssue{{Severity: domain.SeverityHigh, File: "internal/store/store.go", Description: "Put still panics on a zero Store"}},
			})
		}},
		{"implement_red_prerun", func() string {
			return BuildImplementPrompt(ImplementPromptInput{
				Task: goldenTask, PreRunFailure: &domain.TestResult{Command: "go test ./...", ExitCode: 1, Stdout: "FAIL: TestPutFresh\n", Stderr: "exit status 1"},
			})
		}},
		{"implement_red_prerun_check", func() string {
			return BuildImplementPrompt(ImplementPromptInput{Task: goldenTask, PreRunFailure: goldenFailingCheck()})
		}},
		{"review_plan", func() string {
			return BuildReviewPrompt(ReviewPromptInput{
				Task: goldenTask, Plan: goldenPlan(), Implementation: goldenImplementation(),
				TestResult: &domain.TestResult{Command: "go test ./...", Success: true}, GitDiff: goldenDiff,
			})
		}},
		{"review_no_plan", func() string {
			return BuildReviewPrompt(ReviewPromptInput{
				Task: goldenTask, Implementation: goldenImplementation(),
				TestResult: &domain.TestResult{Command: "go test ./...", Success: true}, GitDiff: goldenDiff,
			})
		}},
		{"review_partial_diff", func() string {
			return BuildReviewPrompt(ReviewPromptInput{
				Task: goldenTask, Plan: noRisks, Implementation: goldenImplementation(),
				GitDiff: goldenDiff, DiffPartial: true,
			})
		}},
		{"clarify_interactive", func() string {
			return BuildClarifyPrompt(ClarifyPromptInput{Task: goldenClarifyTask, Instructions: goldenInstructions(), Mode: domain.ClarifyInteractive})
		}},
		{"clarify_auto", func() string {
			return BuildClarifyPrompt(ClarifyPromptInput{
				Task: goldenClarifyTask, Instructions: goldenInstructions(), Mode: domain.ClarifyAuto,
				Clarification: goldenClarification,
			})
		}},
		{"branch_name", func() string {
			return BuildBranchNamePrompt(BranchNameInput{
				Task: "Fix the nil map panic in Store.Put", Key: "PAY-12", RunID: "20261027-101500-ab12cd", Date: "2026-10-27",
				Files: []BranchFile{
					{Path: "CLAUDE.md", Text: "## Git\n\nBranch names look like feature/<jira-key>-<short-description>.\n"},
					{Path: "CONTRIBUTING.md", Text: "Open a merge request from your branch into main.\n"},
				},
			})
		}},
		{"survey", func() string { return BuildSurveyPrompt(goldenStoryRequest, "billing") }},
		{"specify", func() string {
			return BuildSpecifyPrompt(goldenStoryRequest, goldenSurveys(), []string{"gateway", "billing"}, 12)
		}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			fixNonce(t)
			got := tc.build()
			if !strings.HasPrefix(got, "ACTION: ") {
				t.Fatalf("the routing marker must stay the first line:\n%s", got)
			}

			name := tc.name + ".golden"
			if file, ok := goldenFile[tc.name]; ok {
				name = file
			}
			path := filepath.Join("testdata", "prompts", name)
			if *update {
				if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
					t.Fatal(err)
				}
				if err := os.WriteFile(path, []byte(got), 0o644); err != nil {
					t.Fatal(err)
				}
				return
			}
			want, err := os.ReadFile(path)
			if err != nil {
				t.Fatalf("read golden (go test ./internal/action -update): %v", err)
			}
			if got != string(want) {
				t.Errorf("prompt differs from %s (go test ./internal/action -update to accept):\n--- got\n%s\n--- want\n%s", path, got, want)
			}
		})
	}
}

func TestFixNonce_RestoresTheRandomOne(t *testing.T) {
	var a, b string
	t.Run("fixed", func(t *testing.T) {
		fixNonce(t)
		a, b = nonce(), nonce()
	})
	if a != "NONCE0001" || b != "NONCE0002" {
		t.Errorf("fixed nonces = %q, %q", a, b)
	}
	if x, y := nonce(), nonce(); x == y || strings.HasPrefix(x, "NONCE") {
		t.Errorf("the random nonce was not restored: %q, %q", x, y)
	}
}

func TestWriteList(t *testing.T) {
	var b strings.Builder
	writeList(&b, "Empty", nil)
	writeList(&b, "Blank", []string{"", "  "})
	writeNumberedList(&b, "None", []string{" "})
	if b.Len() != 0 {
		t.Errorf("empty lists wrote %q", b.String())
	}
	writeList(&b, "Files", []string{"a.go", "", "b.go"})
	writeNumberedList(&b, "Criteria", []string{"x", " ", "y"})
	want := "Files:\n- a.go\n- b.go\nCriteria:\n1. x\n2. y\n"
	if b.String() != want {
		t.Errorf("got %q, want %q", b.String(), want)
	}
}

func TestPrompts_StageContext(t *testing.T) {
	plan := goldenPlan()
	investigatePrompt := BuildInvestigatePrompt(InvestigatePromptInput{Task: "t", Research: goldenResearch()})
	implement := BuildImplementPrompt(ImplementPromptInput{Task: "t", Plan: plan})
	review := BuildReviewPrompt(ReviewPromptInput{Task: "t", Plan: plan, GitDiff: "d"})

	for name, tc := range map[string]struct {
		prompt string
		want   []string
	}{
		"investigate": {investigatePrompt, []string{
			"Research:\nA write to a nil map panics", `"acceptance_criteria": ["..."]`, acceptanceCriteriaRule,
			`"root_cause": "..."`, rootCauseRule,
		}},
		"implement": {implement, []string{
			"Steps:\n- add make() in NewStore\n", "Affected files (from plan):\n- internal/store/store.go\n- internal/store/store_test.go\n",
			"Risks (from plan):\n- Store{} literals in tests\n", "Acceptance criteria:\n- Put on a store from NewStore does not panic\n",
		}},
		"review": {review, []string{
			"Steps:\n- add make() in NewStore\n", "Risks (from plan):\n- Store{} literals in tests\n",
			"Acceptance criteria:\n1. Put on a store from NewStore does not panic\n2. a test covers Put on a fresh store\n",
		}},
	} {
		for _, want := range tc.want {
			if !strings.Contains(tc.prompt, want) {
				t.Errorf("%s prompt lacks %q:\n%s", name, want, tc.prompt)
			}
		}
	}
	if strings.Contains(review, "Affected files") {
		t.Error("review is not given file lists (QL01)")
	}
	for name, prompt := range map[string]string{"investigate": investigatePrompt, "implement": implement, "review": review} {
		if strings.Contains(prompt, "Analysis") || strings.Contains(prompt, "from analysis") {
			t.Errorf("%s prompt has an analysis block, but analysis is no longer a stage:\n%s", name, prompt)
		}
	}

	// QL02: the verdict shape and rule only with criteria, the §7.2 paragraph always.
	noPlan := BuildReviewPrompt(ReviewPromptInput{Task: "t", GitDiff: "d"})
	blank := BuildReviewPrompt(ReviewPromptInput{Task: "t", Plan: &domain.InvestigationArtifact{Summary: "p", AcceptanceCriteria: []string{" "}}, GitDiff: "d"})
	for name, prompt := range map[string]string{"with criteria": review, "no plan": noPlan, "blank criteria": blank} {
		if !strings.Contains(prompt, "You have Read, Grep and Glob.") || !strings.Contains(prompt, reviewSurroundingsRule) {
			t.Errorf("review prompt (%s) lacks the Read/Grep paragraph", name)
		}
	}
	if !strings.Contains(review, `"criteria": [`) || !strings.Contains(review, reviewCriteriaRule) {
		t.Errorf("review prompt with criteria lacks the verdict shape or rule:\n%s", review)
	}
	for name, prompt := range map[string]string{"no plan": noPlan, "blank criteria": blank} {
		if strings.Contains(prompt, `"criteria": [`) || strings.Contains(prompt, reviewCriteriaRule) {
			t.Errorf("review prompt (%s) asks for criteria verdicts", name)
		}
	}

	onlyUnmet := BuildImplementPrompt(ImplementPromptInput{Task: "t",
		UnmetCriteria: []domain.CriterionVerdict{{Index: 1, Criterion: "c", Evidence: "e\nmore"}}})
	for _, want := range []string{"A code review FAILED", "Unmet acceptance criteria:\n- 1. c\n  evidence: e more\n"} {
		if !strings.Contains(onlyUnmet, want) {
			t.Errorf("a review that failed only on criteria lacks %q:\n%s", want, onlyUnmet)
		}
	}

	bare := BuildImplementPrompt(ImplementPromptInput{Task: "t", Plan: &domain.InvestigationArtifact{Summary: "p"}})
	for _, heading := range []string{"Affected files", "Risks", "Steps:", "Acceptance criteria"} {
		if strings.Contains(bare, heading) {
			t.Errorf("an empty list left %q in the prompt:\n%s", heading, bare)
		}
	}
	if first := BuildImplementPrompt(ImplementPromptInput{Task: "t", RunDiffStat: "x"}); strings.Contains(first, "DIFFSTAT") {
		t.Error("only a retry carries the run's diff stat")
	}
}

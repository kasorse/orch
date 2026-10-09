package action

import (
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"orchestrator/internal/domain"
)

var update = flag.Bool("update", false, "rewrite testdata/prompts/*.golden from the current prompts")

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

func goldenAnalysis() *domain.AnalysisArtifact {
	return &domain.AnalysisArtifact{
		Summary:        "The panic comes from a nil map in Store.Put.",
		RootCause:      "Store is constructed without make().",
		AffectedFiles:  []string{"internal/store/store.go"},
		Recommendation: "initialise the map in NewStore.",
		Risks:          []string{"callers that build Store{} directly"},
	}
}

func goldenPlan() *domain.PlanArtifact {
	return &domain.PlanArtifact{
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

func TestPrompts_Golden(t *testing.T) {
	retry := &domain.ImplementationArtifact{Summary: "Added make() in NewStore."}
	noRisks := goldenPlan()
	noRisks.Risks = nil
	emptyAnalysis := &domain.AnalysisArtifact{Summary: "The panic comes from a nil map in Store.Put.", RootCause: "Store is constructed without make().", Recommendation: "initialise the map in NewStore."}

	cases := []struct {
		name  string
		build func() string
	}{
		{"research", func() string { return BuildResearchPrompt(ResearchPromptInput{Task: goldenTask}) }},
		{"research_final", func() string { return BuildResearchPrompt(ResearchPromptInput{Task: goldenTask, FinalAnswer: true}) }},
		{"analyze_research", func() string {
			return BuildAnalyzePrompt(AnalyzePromptInput{Task: goldenTask, Research: goldenResearch()})
		}},
		{"analyze_final", func() string { return BuildAnalyzePrompt(AnalyzePromptInput{Task: goldenTask, FinalAnswer: true}) }},
		{"plan_analysis", func() string {
			return BuildPlanPrompt(PlanPromptInput{Task: goldenTask, Analysis: goldenAnalysis()})
		}},
		{"plan_final", func() string {
			return BuildPlanPrompt(PlanPromptInput{Task: goldenTask, Analysis: goldenAnalysis(), FinalAnswer: true})
		}},
		{"plan_empty_lists", func() string { return BuildPlanPrompt(PlanPromptInput{Task: goldenTask, Analysis: emptyAnalysis}) }},
		{"implement_first", func() string {
			return BuildImplementPrompt(ImplementPromptInput{Task: goldenTask, Analysis: goldenAnalysis(), Plan: goldenPlan()})
		}},
		{"implement_retry_test", func() string {
			return BuildImplementPrompt(ImplementPromptInput{
				Task: goldenTask, Analysis: goldenAnalysis(), Plan: goldenPlan(),
				PreviousImplementation: retry, RunDiffStat: goldenRunDiffStat, LastTestResult: goldenFailingTest(),
			})
		}},
		{"implement_retry_review", func() string {
			return BuildImplementPrompt(ImplementPromptInput{
				Task: goldenTask, Analysis: goldenAnalysis(), Plan: goldenPlan(),
				PreviousImplementation: retry, RunDiffStat: goldenRunDiffStat,
				ReviewIssues: []domain.ReviewIssue{{Severity: domain.SeverityHigh, File: "internal/store/store.go", Description: "Put still panics on a zero Store"}},
			})
		}},
		{"implement_red_prerun", func() string {
			return BuildImplementPrompt(ImplementPromptInput{
				Task: goldenTask, PreRunFailure: &domain.TestResult{Command: "go test ./...", ExitCode: 1, Stdout: "FAIL: TestPutFresh\n", Stderr: "exit status 1"},
			})
		}},
		{"review_plan", func() string {
			return BuildReviewPrompt(ReviewPromptInput{
				Task: goldenTask, Analysis: goldenAnalysis(), Plan: goldenPlan(), Implementation: goldenImplementation(),
				TestResult: &domain.TestResult{Command: "go test ./...", Success: true}, GitDiff: goldenDiff,
			})
		}},
		{"review_no_plan", func() string {
			return BuildReviewPrompt(ReviewPromptInput{
				Task: goldenTask, Analysis: goldenAnalysis(), Implementation: goldenImplementation(),
				TestResult: &domain.TestResult{Command: "go test ./...", Success: true}, GitDiff: goldenDiff,
			})
		}},
		{"review_partial_diff", func() string {
			return BuildReviewPrompt(ReviewPromptInput{
				Task: goldenTask, Plan: noRisks, Implementation: goldenImplementation(),
				GitDiff: goldenDiff, DiffPartial: true,
			})
		}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			fixNonce(t)
			got := tc.build()
			if !strings.HasPrefix(got, "ACTION: ") {
				t.Fatalf("the routing marker must stay the first line:\n%s", got)
			}

			path := filepath.Join("testdata", "prompts", tc.name+".golden")
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
	analysis, plan := goldenAnalysis(), goldenPlan()
	planPrompt := BuildPlanPrompt(PlanPromptInput{Task: "t", Analysis: analysis})
	implement := BuildImplementPrompt(ImplementPromptInput{Task: "t", Analysis: analysis, Plan: plan})
	review := BuildReviewPrompt(ReviewPromptInput{Task: "t", Analysis: analysis, Plan: plan, GitDiff: "d"})

	for name, tc := range map[string]struct {
		prompt string
		want   []string
	}{
		"plan": {planPrompt, []string{
			"Affected files (from analysis):\n- internal/store/store.go\n", "Risks (from analysis):\n- callers that build Store{} directly\n",
			`"acceptance_criteria": ["..."]`, acceptanceCriteriaRule,
		}},
		"implement": {implement, []string{
			"Affected files (from analysis):\n- internal/store/store.go\n", "Risks (from analysis):\n",
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

	bare := BuildImplementPrompt(ImplementPromptInput{Task: "t", Analysis: &domain.AnalysisArtifact{Summary: "s"}, Plan: &domain.PlanArtifact{Summary: "p"}})
	for _, heading := range []string{"Affected files", "Risks", "Steps:", "Acceptance criteria"} {
		if strings.Contains(bare, heading) {
			t.Errorf("an empty list left %q in the prompt:\n%s", heading, bare)
		}
	}
	if first := BuildImplementPrompt(ImplementPromptInput{Task: "t", RunDiffStat: "x"}); strings.Contains(first, "DIFFSTAT") {
		t.Error("only a retry carries the run's diff stat")
	}
}

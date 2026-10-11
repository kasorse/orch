package workflow

import (
	"slices"
	"strings"
	"testing"

	"orchestrator/internal/domain"
)

func treeID(c byte) string { return strings.Repeat(string(c), 40) }

func implTree(tree string) domain.StepExecution {
	return domain.StepExecution{Action: domain.ActionImplement, Status: domain.StepCompleted, TreeAfter: tree}
}

func testRun(r domain.TestResult) domain.StepExecution {
	r.Success = r.ExitCode == 0
	return domain.StepExecution{Action: domain.ActionTest, Status: domain.StepCompleted, TestResult: &r}
}

func reviewRun(issues []domain.ReviewIssue, criteria ...domain.CriterionVerdict) domain.StepExecution {
	return domain.StepExecution{Action: domain.ActionReview, Status: domain.StepCompleted,
		ReviewResult: &domain.ReviewResult{Status: domain.ReviewFail, Issues: issues, Criteria: criteria}}
}

func high(file, description string) domain.ReviewIssue {
	return domain.ReviewIssue{Severity: domain.SeverityHigh, File: file, Description: description}
}

func unmet(index int, criterion string) domain.CriterionVerdict {
	return domain.CriterionVerdict{Index: index, Criterion: criterion, Met: false, Evidence: "x"}
}

func TestNoProgress_Tree(t *testing.T) {
	a, b := treeID('a'), treeID('b')
	failed := testRun(domain.TestResult{ExitCode: 1, Stdout: "FAIL"})
	cases := []struct {
		name string
		run  []domain.StepExecution
		want string
	}{
		{"equal trees at the second implement", []domain.StepExecution{implTree(a), failed, implTree(a)}, ReasonNoChange},
		{"equal trees with failures between", []domain.StepExecution{implTree(a), failed, failedReview(), implTree(a)}, ReasonNoChange},
		{"the first implement is never compared", []domain.StepExecution{implTree(a)}, ""},
		{"the tree changed", []domain.StepExecution{implTree(a), failed, implTree(b)}, ""},
		{"the tree changed back to the first", []domain.StepExecution{implTree(a), failed, implTree(b), failed, implTree(a)}, ""},
		{"no tree recorded", []domain.StepExecution{implTree(""), failed, implTree("")}, ""},
		{"no tree recorded for the first", []domain.StepExecution{implTree(""), failed, implTree(a)}, ""},
		{"no tree recorded for the second", []domain.StepExecution{implTree(a), failed, implTree("")}, ""},
		{"a passed test between equal trees", []domain.StepExecution{implTree(a), done(domain.ActionTest), failedReview(), implTree(a)}, ""},
		{"a passed review between equal trees", []domain.StepExecution{implTree(a), failed, done(domain.ActionReview), implTree(a)}, ""},
		{"a hard-failed implement is not compared", []domain.StepExecution{implTree(a), failed, hardFail(domain.ActionImplement, "x"), implTree(a)}, ReasonNoChange},
		{"a hard-failed implement is not a tree", []domain.StepExecution{implTree(a), failed, implTree(b), hardFail(domain.ActionImplement, "x")}, ""},
		{"the tree is only read after an implement", []domain.StepExecution{implTree(a), failed, implTree(a), testRun(domain.TestResult{ExitCode: 2, Stdout: "other"})}, ""},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := NoProgress(&domain.Run{Executions: tc.run}); got != tc.want {
				t.Errorf("NoProgress = %q, want %q", got, tc.want)
			}
		})
	}
}

func TestNoProgress_Tests(t *testing.T) {
	a, b, c := treeID('a'), treeID('b'), treeID('c')
	names := func(check string, exit int, tests ...string) domain.StepExecution {
		return testRun(domain.TestResult{ExitCode: exit, FailedCheck: check, FailedTests: tests, Stdout: "out"})
	}
	output := func(stdout, stderr string) domain.StepExecution {
		return testRun(domain.TestResult{ExitCode: 1, Stdout: stdout, Stderr: stderr})
	}
	loop := func(first, second domain.StepExecution) []domain.StepExecution {
		return []domain.StepExecution{implTree(a), first, implTree(b), second}
	}
	cases := []struct {
		name string
		run  []domain.StepExecution
		want string
	}{
		{"same tests, in another order", loop(names("", 1, "pkg.TestB", "pkg.TestA"), names("", 1, "pkg.TestA", "pkg.TestB")), ReasonSameTests},
		{"same tests and check", loop(names("lint", 1, "x"), names("lint", 1, "x")), ReasonSameTests},
		{"one test fewer", loop(names("", 1, "pkg.TestA", "pkg.TestB"), names("", 1, "pkg.TestA")), ""},
		{"another test", loop(names("", 1, "pkg.TestA"), names("", 1, "pkg.TestB")), ""},
		{"same tests, different failed check", loop(names("build", 1, "pkg.TestA"), names("vet", 1, "pkg.TestA")), ""},
		{"same tests, a check failed against none", loop(names("", 1, "pkg.TestA"), names("vet", 1, "pkg.TestA")), ""},
		{"same tests, different exit code", loop(names("", 1, "pkg.TestA"), names("", 2, "pkg.TestA")), ""},
		{"same output but for the digits", loop(output("FAIL x_test.go:12 took 1.52s", "panic at line 12"), output("FAIL x_test.go:9 took 13.7s", "panic at line 7")), ReasonSameTests},
		{"another output", loop(output("FAIL x_test.go:12", ""), output("FAIL y_test.go:12", "")), ""},
		{"the same text moved to stderr", loop(output("FAIL", ""), output("", "FAIL")), ""},
		{"names on one side only, same output", loop(names("", 1, "pkg.TestA"), output("out", "")), ReasonSameTests},
		{"names on one side only, other output", loop(names("", 1, "pkg.TestA"), output("different", "")), ""},
		{"no output at all says nothing", loop(output("", ""), output(" \n", "")), ""},
		{"the first failure", []domain.StepExecution{implTree(a), names("", 1, "x")}, ""},
		{"a passed test between", []domain.StepExecution{implTree(a), names("", 1, "x"), implTree(b), done(domain.ActionTest), failedReview(), implTree(c), names("", 1, "x")}, ""},
		{"a failed review between does not hide it", []domain.StepExecution{implTree(a), names("", 1, "x"), implTree(b), failedReview(), implTree(c), names("", 1, "x")}, ReasonSameTests},
		{"the check is read after a test only", []domain.StepExecution{implTree(a), names("", 1, "x"), implTree(b), names("", 1, "x"), implTree(c)}, ""},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := NoProgress(&domain.Run{Executions: tc.run}); got != tc.want {
				t.Errorf("NoProgress = %q, want %q", got, tc.want)
			}
		})
	}
}

func TestNoProgress_Review(t *testing.T) {
	a, b, c := treeID('a'), treeID('b'), treeID('c')
	loop := func(first, second domain.StepExecution) []domain.StepExecution {
		return []domain.StepExecution{implTree(a), done(domain.ActionTest), first, implTree(b), done(domain.ActionTest), second}
	}
	nilCheck := "The pointer returned by Lookup is dereferenced without a nil check"
	cases := []struct {
		name string
		run  []domain.StepExecution
		want string
	}{
		{"same issue", loop(reviewRun([]domain.ReviewIssue{high("a.go", nilCheck)}), reviewRun([]domain.ReviewIssue{high("a.go", nilCheck)})), ReasonSameReviewHits},
		{"reworded issue", loop(
			reviewRun([]domain.ReviewIssue{high("a.go", nilCheck)}),
			reviewRun([]domain.ReviewIssue{high("a.go", "Lookup returns a pointer that is dereferenced without a nil check!")})), ReasonSameReviewHits},
		{"critical against high is the same issue", loop(
			reviewRun([]domain.ReviewIssue{high("a.go", nilCheck)}),
			reviewRun([]domain.ReviewIssue{{Severity: domain.SeverityCritical, File: "a.go", Description: nilCheck}})), ReasonSameReviewHits},
		{"similar text, different file", loop(
			reviewRun([]domain.ReviewIssue{high("a.go", nilCheck)}),
			reviewRun([]domain.ReviewIssue{high("b.go", nilCheck)})), ""},
		{"same file, different problem", loop(
			reviewRun([]domain.ReviewIssue{high("a.go", nilCheck)}),
			reviewRun([]domain.ReviewIssue{high("a.go", "Missing mutex around the shared counter map")})), ""},
		{"one new issue among repeated ones", loop(
			reviewRun([]domain.ReviewIssue{high("a.go", nilCheck), high("b.go", "Error from Close is ignored and the file handle leaks")}),
			reviewRun([]domain.ReviewIssue{high("a.go", nilCheck), high("b.go", "Error from Close is ignored and the file handle leaks"), high("c.go", "Race on the shared counter map")})), ""},
		{"fewer issues, all repeated", loop(
			reviewRun([]domain.ReviewIssue{high("a.go", nilCheck), high("b.go", "Error from Close is ignored and the file handle leaks")}),
			reviewRun([]domain.ReviewIssue{high("b.go", "Error from Close is ignored and the file handle leaks")})), ReasonSameReviewHits},
		{"a new medium issue is not blocking", loop(
			reviewRun([]domain.ReviewIssue{high("a.go", nilCheck)}),
			reviewRun([]domain.ReviewIssue{high("a.go", nilCheck), {Severity: domain.SeverityMedium, File: "z.go", Description: "Naming of the helper"}})), ReasonSameReviewHits},
		{"a medium issue before is not matched against", loop(
			reviewRun([]domain.ReviewIssue{high("a.go", "Different problem entirely here"), {Severity: domain.SeverityMedium, File: "a.go", Description: nilCheck}}),
			reviewRun([]domain.ReviewIssue{high("a.go", nilCheck)})), ""},
		{"same unmet criterion", loop(
			reviewRun(nil, unmet(1, "Rejects an empty name with an error")),
			reviewRun(nil, unmet(2, "Rejects an empty name with an error"))), ReasonSameReviewHits},
		{"another unmet criterion", loop(
			reviewRun(nil, unmet(1, "Rejects an empty name with an error")),
			reviewRun(nil, unmet(2, "Logs every request with its duration"))), ""},
		{"repeated issue, new unmet criterion", loop(
			reviewRun([]domain.ReviewIssue{high("a.go", nilCheck)}),
			reviewRun([]domain.ReviewIssue{high("a.go", nilCheck)}, unmet(1, "Rejects an empty name with an error"))), ""},
		{"a criterion does not match an issue", loop(
			reviewRun([]domain.ReviewIssue{high("", "Rejects an empty name with an error")}),
			reviewRun(nil, unmet(1, "Rejects an empty name with an error"))), ""},
		{"the first failed review", []domain.StepExecution{implTree(a), done(domain.ActionTest), reviewRun([]domain.ReviewIssue{high("a.go", nilCheck)})}, ""},
		{"a passed review between", []domain.StepExecution{
			implTree(a), done(domain.ActionTest), reviewRun([]domain.ReviewIssue{high("a.go", nilCheck)}),
			implTree(b), done(domain.ActionTest), done(domain.ActionReview),
			implTree(c), done(domain.ActionTest), reviewRun([]domain.ReviewIssue{high("a.go", nilCheck)})}, ""},
		{"a description without words matches nothing", loop(
			reviewRun([]domain.ReviewIssue{high("a.go", "!!")}),
			reviewRun([]domain.ReviewIssue{high("a.go", "!!")})), ""},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := NoProgress(&domain.Run{Executions: tc.run}); got != tc.want {
				t.Errorf("NoProgress = %q, want %q", got, tc.want)
			}
		})
	}
}

func TestNoProgress_EmptyAndHardFailures(t *testing.T) {
	if got := NoProgress(&domain.Run{}); got != "" {
		t.Errorf("an empty history = %q", got)
	}
	a := treeID('a')
	run := &domain.Run{Executions: []domain.StepExecution{implTree(a), implTree(a), hardFail(domain.ActionTest, "sandbox")}}
	if got := NoProgress(run); got != "" {
		t.Errorf("a hard-failed last step is not judged: %q", got)
	}
}

func sortedWords(m map[string]struct{}) []string {
	out := make([]string, 0, len(m))
	for w := range m {
		out = append(out, w)
	}
	slices.Sort(out)
	return out
}

func TestNormalizeWords(t *testing.T) {
	cases := []struct {
		in   string
		want []string
	}{
		{"", []string{}},
		{"Nil pointer in Foo.Bar()!", []string{"bar", "foo", "nil", "pointer"}},
		{"is it ok to go on", []string{}},
		{"snake_case-and kebab", []string{"and", "case", "kebab", "snake"}},
		{"Repeat repeat REPEAT", []string{"repeat"}},
		{"HTTP2 handler, v10 ids 42", []string{"handler", "http2", "ids", "v10"}},
		{"Überlauf im Zähler", []string{"zähler", "überlauf"}},
		{"日本語 のテスト", []string{"のテスト", "日本語"}},
	}
	for _, tc := range cases {
		t.Run(tc.in, func(t *testing.T) {
			if got := sortedWords(normalizeWords(tc.in)); !slices.Equal(got, tc.want) {
				t.Errorf("normalizeWords(%q) = %v, want %v", tc.in, got, tc.want)
			}
		})
	}
}

func TestJaccard(t *testing.T) {
	set := func(s string) map[string]struct{} { return normalizeWords(s) }
	cases := []struct {
		name string
		a, b string
		want float64
	}{
		{"identical", "alpha beta gamma", "gamma beta alpha", 1},
		{"disjoint", "alpha beta", "gamma delta", 0},
		{"half", "alpha beta gamma", "alpha beta delta", 0.5},
		{"three of five", "alpha beta gamma delta", "alpha beta gamma sigma", 0.6},
		{"subset", "alpha beta gamma delta", "alpha beta", 0.5},
		{"both empty", "", "a b", 0},
		{"one empty", "alpha", "", 0},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := jaccard(set(tc.a), set(tc.b)); got != tc.want {
				t.Errorf("jaccard = %v, want %v", got, tc.want)
			}
		})
	}
}

func TestSimilar_ThresholdIsInclusive(t *testing.T) {
	if similarityThreshold != 0.6 {
		t.Fatalf("similarityThreshold = %v", similarityThreshold)
	}
	cases := []struct {
		a, b string
		want bool
	}{
		{"alpha beta gamma delta", "alpha beta gamma sigma", true},
		{"alpha beta gamma", "alpha beta delta", false},
		{"alpha beta gamma delta epsilon", "alpha beta gamma delta epsilon zeta", true},
		{"", "", false},
	}
	for _, tc := range cases {
		if got := similar(tc.a, tc.b); got != tc.want {
			t.Errorf("similar(%q, %q) = %v, want %v", tc.a, tc.b, got, tc.want)
		}
	}
}

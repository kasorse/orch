package workflow

import (
	"crypto/sha256"
	"encoding/hex"
	"regexp"
	"slices"
	"strings"
	"unicode"
	"unicode/utf8"

	"github.com/kasorse/orch/internal/domain"
)

// NoProgressPrefix starts the failure reason of a run stopped for making no progress; Entry
// recognises such a run by it.
const NoProgressPrefix = "no progress:"

// The failure reasons of a run stopped without progress (specs/quality/SPEC.md §8.1).
const (
	ReasonNoChange       = NoProgressPrefix + " implement changed nothing"
	ReasonSameTests      = NoProgressPrefix + " the same tests fail"
	ReasonSameReviewHits = NoProgressPrefix + " review repeats the same issues"
)

// similarityThreshold is the Jaccard similarity of two descriptions' word sets from which
// they count as the same issue (specs/quality/SPEC.md §8.1).
const similarityThreshold = 0.6

// minWordLen is the shortest word that takes part in the similarity.
const minWordLen = 3

// NoProgress reports why the fix loop has stopped getting anywhere, or "" when it has not. It
// looks only at the persisted completed executions, so it gives the same answer for a live run
// and for one read back from run.json, and it makes no call.
//
// The last completed execution decides which signal is read: an implement is compared with the
// implement before it (tree), a test with the failed test before it, a review with the failed
// review before it. A value that is missing - no tree id, no previous failure - is never
// "equal"; nothing is judged on absent data.
func NoProgress(run *domain.Run) string {
	if n := len(run.Executions); n == 0 || run.Executions[n-1].Status != domain.StepCompleted {
		return ""
	}
	var done []domain.StepExecution
	for _, exec := range run.Executions {
		if exec.Status == domain.StepCompleted {
			done = append(done, exec)
		}
	}
	switch done[len(done)-1].Action {
	case domain.ActionImplement:
		if treeUnchanged(done) {
			return ReasonNoChange
		}
	case domain.ActionTest:
		if sameTestsFail(done) {
			return ReasonSameTests
		}
	case domain.ActionReview:
		if sameReviewIssues(done) {
			return ReasonSameReviewHits
		}
	}
	return ""
}

// passed reports whether a completed test or review passed.
func passed(exec domain.StepExecution) bool {
	switch exec.Action {
	case domain.ActionTest:
		return exec.TestResult != nil && exec.TestResult.Success
	case domain.ActionReview:
		return exec.ReviewResult != nil && exec.ReviewResult.Decide() == domain.ReviewPass
	}
	return false
}

// treeUnchanged reports whether the last execution is an implement that left the working tree
// as the previous implement of the run left it. A test or review that passed in between means
// the run did move, so it does not count; neither does a missing tree id.
func treeUnchanged(done []domain.StepExecution) bool {
	last := done[len(done)-1]
	if last.TreeAfter == "" {
		return false
	}
	for i := len(done) - 2; i >= 0; i-- {
		prev := done[i]
		if passed(prev) {
			return false
		}
		if prev.Action == domain.ActionImplement {
			return prev.TreeAfter == last.TreeAfter
		}
	}
	return false
}

// lastTwoFailures returns the last two completed executions of an action that failed, with
// ok false when there are fewer or when one that passed lies between them.
func lastTwoFailures(done []domain.StepExecution, action domain.ActionType, failed func(domain.StepExecution) bool) (prev, last domain.StepExecution, ok bool) {
	var found []domain.StepExecution
	for i := len(done) - 1; i >= 0 && len(found) < 2; i-- {
		exec := done[i]
		if exec.Action != action {
			continue
		}
		if passed(exec) {
			return prev, last, false
		}
		if failed(exec) {
			found = append(found, exec)
		}
	}
	if len(found) < 2 {
		return prev, last, false
	}
	return found[1], found[0], true
}

func testFailed(exec domain.StepExecution) bool {
	return exec.TestResult != nil && !exec.TestResult.Success
}

func reviewFailed(exec domain.StepExecution) bool {
	return exec.ReviewResult != nil && exec.ReviewResult.Decide() == domain.ReviewFail
}

// sameTestsFail reports whether the last execution is a failed test that fails like the failed
// test before it.
func sameTestsFail(done []domain.StepExecution) bool {
	if !testFailed(done[len(done)-1]) {
		return false
	}
	prev, last, ok := lastTwoFailures(done, domain.ActionTest, testFailed)
	return ok && sameFailure(prev.TestResult, last.TestResult)
}

// sameFailure compares two failed test results by their failure key: the failed check, the
// exit code and, when both sides name their failing tests, those names; otherwise the hash of
// the output with digits masked (see outputHash).
func sameFailure(a, b *domain.TestResult) bool {
	if a.FailedCheck != b.FailedCheck || a.ExitCode != b.ExitCode {
		return false
	}
	if len(a.FailedTests) > 0 && len(b.FailedTests) > 0 {
		x, y := slices.Clone(a.FailedTests), slices.Clone(b.FailedTests)
		slices.Sort(x)
		slices.Sort(y)
		return slices.Equal(x, y)
	}
	ha, hb := outputHash(a), outputHash(b)
	return ha != "" && ha == hb
}

var digitRun = regexp.MustCompile(`[0-9]+`)

// outputHash is the SHA-256 of a result's stdout and stderr with every run of digits replaced
// by '#', which masks durations, counters and line numbers. A result without any output has no
// hash: two silent failures say nothing about being the same.
func outputHash(t *domain.TestResult) string {
	if strings.TrimSpace(t.Stdout) == "" && strings.TrimSpace(t.Stderr) == "" {
		return ""
	}
	sum := sha256.New()
	for _, part := range []string{t.Stdout, t.Stderr} {
		sum.Write([]byte(digitRun.ReplaceAllString(part, "#")))
		sum.Write([]byte{0})
	}
	return hex.EncodeToString(sum.Sum(nil))
}

// sameReviewIssues reports whether the last execution is a failed review whose every blocking
// issue and unmet criterion repeats one of the failed review before it. A single new one means
// the review found something else, which is progress.
func sameReviewIssues(done []domain.StepExecution) bool {
	if !reviewFailed(done[len(done)-1]) {
		return false
	}
	prev, last, ok := lastTwoFailures(done, domain.ActionReview, reviewFailed)
	if !ok {
		return false
	}
	var prevIssues, lastIssues []domain.ReviewIssue
	for _, i := range prev.ReviewResult.Issues {
		if blocking(i) {
			prevIssues = append(prevIssues, i)
		}
	}
	for _, i := range last.ReviewResult.Issues {
		if blocking(i) {
			lastIssues = append(lastIssues, i)
		}
	}
	prevCriteria, lastCriteria := prev.ReviewResult.UnmetCriteria(), last.ReviewResult.UnmetCriteria()
	if len(lastIssues)+len(lastCriteria) == 0 {
		return false
	}
	for _, issue := range lastIssues {
		if !slices.ContainsFunc(prevIssues, func(p domain.ReviewIssue) bool { return sameIssue(p, issue) }) {
			return false
		}
	}
	for _, c := range lastCriteria {
		if !slices.ContainsFunc(prevCriteria, func(p domain.CriterionVerdict) bool { return similar(p.Criterion, c.Criterion) }) {
			return false
		}
	}
	return true
}

// blocking reports whether an issue fails a review by itself.
func blocking(i domain.ReviewIssue) bool {
	return i.Severity == domain.SeverityCritical || i.Severity == domain.SeverityHigh
}

// sameIssue is the match of specs/quality/SPEC.md §8.1: the same file and descriptions that
// are alike in their words.
func sameIssue(a, b domain.ReviewIssue) bool {
	return a.File == b.File && similar(a.Description, b.Description)
}

func similar(a, b string) bool {
	return jaccard(normalizeWords(a), normalizeWords(b)) >= similarityThreshold
}

// normalizeWords is the set of words of s: lower case, split at anything that is not a letter
// or a digit, words shorter than minWordLen dropped.
func normalizeWords(s string) map[string]struct{} {
	words := map[string]struct{}{}
	for _, w := range strings.FieldsFunc(strings.ToLower(s), func(r rune) bool {
		return !unicode.IsLetter(r) && !unicode.IsDigit(r)
	}) {
		if utf8.RuneCountInString(w) >= minWordLen {
			words[w] = struct{}{}
		}
	}
	return words
}

// jaccard is |a ∩ b| / |a ∪ b|; two empty sets share nothing to compare, so they are 0.
func jaccard(a, b map[string]struct{}) float64 {
	if len(a) == 0 && len(b) == 0 {
		return 0
	}
	shared := 0
	for w := range a {
		if _, ok := b[w]; ok {
			shared++
		}
	}
	return float64(shared) / float64(len(a)+len(b)-shared)
}

package action

import (
	"slices"
	"strings"
	"unicode"
	"unicode/utf8"

	"orchestrator/internal/domain"
)

// minIssueWordLen is the shortest word that takes part in telling two issue descriptions
// apart; it is the same as for the no-progress match (workflow.normalizeWords).
const minIssueWordLen = 3

// mergeReviews is the one result the run goes on with when several reviewers answered
// (specs/quality/SPEC.md §7.5). answers are in call order, the base model first, and each was
// parsed strictly against the same criteria.
//
// Issues are the union, in order of first appearance, an issue counting once when an earlier
// one has the same severity, the same file and the same words in its description. A criterion
// is met only if every reviewer says met, with the evidence of the first that says not met.
// The summaries are the base reviewer's. Status is derived from the merged result, as nothing
// trusts a model's own word for it. The diff summary is the caller's to set.
func mergeReviews(answers []domain.ReviewerAnswer) domain.ReviewResult {
	base := answers[0].Result
	merged := domain.ReviewResult{
		Summary:      base.Summary,
		SummaryShort: base.SummaryShort,
		Issues:       []domain.ReviewIssue{},
	}

	type key struct {
		severity domain.Severity
		file     string
		words    string
	}
	seen := map[key]bool{}
	for _, a := range answers {
		for _, issue := range a.Result.Issues {
			k := key{issue.Severity, issue.File, wordKey(issue.Description)}
			if seen[k] {
				continue
			}
			seen[k] = true
			merged.Issues = append(merged.Issues, issue)
		}
	}

	if len(base.Criteria) > 0 {
		merged.Criteria = slices.Clone(base.Criteria)
		// Every answer has the same criteria in index order (parseReviewStrict).
		for i := range merged.Criteria {
			for _, a := range answers {
				if c := a.Result.Criteria[i]; !c.Met {
					merged.Criteria[i] = c
					break
				}
			}
		}
	}

	merged.Status = merged.Decide()
	merged.Reviewers = slices.Clone(answers)
	return merged
}

// wordKey is a description reduced to its words - lower case, split at anything that is not a
// letter or a digit, short words dropped - sorted and joined, so that two descriptions with
// equal word sets have equal keys.
func wordKey(s string) string {
	set := map[string]struct{}{}
	for _, w := range strings.FieldsFunc(strings.ToLower(s), func(r rune) bool {
		return !unicode.IsLetter(r) && !unicode.IsDigit(r)
	}) {
		if utf8.RuneCountInString(w) >= minIssueWordLen {
			set[w] = struct{}{}
		}
	}
	words := make([]string, 0, len(set))
	for w := range set {
		words = append(words, w)
	}
	slices.Sort(words)
	if len(words) == 0 {
		// Nothing to compare by: only the same text is the same issue.
		return strings.TrimSpace(strings.ToLower(s))
	}
	return strings.Join(words, " ")
}

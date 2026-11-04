// Package report summarises persisted runs for the operator. It reads only what runs
// already wrote; it calls no provider and makes no request.
package report

import (
	"cmp"
	"fmt"
	"io"
	"slices"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/kasorse/orch/internal/domain"
)

// Entry is one run's decision trace, as persistence.Store.LoadDecision returns it.
type Entry struct {
	RunID string
	At    time.Time
	Trace domain.DecisionTrace
}

// Unreadable is a run whose decision.json could not be read.
type Unreadable struct {
	RunID string
	Err   error
}

// Skip reasons: why a trace is not compared.
const (
	SkipManual   = "manual (--stage)"
	SkipUser     = "chosen by user"
	SkipMock     = "mock"
	SkipNoShadow = "without the shadow question"
)

var skipOrder = []string{SkipManual, SkipUser, SkipMock, SkipNoShadow}

// mockModelPrefix marks a trace answered by the offline mock (jev.MockModelPrefix, restated
// so this package imports only domain).
const mockModelPrefix = "mock:"

// complexityLevels is the matrix's row and column order.
var complexityLevels = []domain.Complexity{domain.ComplexitySimple, domain.ComplexityMedium, domain.ComplexityComplex}

// Disagreement is a compared run whose two complexity questions selected different levels.
type Disagreement struct {
	RunID string
	At    time.Time
	// PolicyHash is the policy the run was decided under; empty in a trace written before
	// the hash existed.
	PolicyHash string
	Choice     domain.Complexity
	Score      domain.Complexity
	Request    string
}

// hashDigits is how many digits of a policy hash the reports print.
const hashDigits = 12

// shortHash is the first hashDigits digits of a policy hash, or noValue for a decision
// recorded without one.
func shortHash(h string) string {
	if h == "" {
		return noValue
	}
	if len(h) > hashDigits {
		return h[:hashDigits]
	}
	return h
}

// DecisionsSummary is the choice-versus-score comparison (TASKS.md T18, reduced): what
// T21 needs to pick one of the two complexity questions.
type DecisionsSummary struct {
	Traces     int
	Skipped    map[string]int
	Unreadable []Unreadable
	Compared   int
	// Deciders counts the compared runs per "<source> <provider>/<model> <policy_hash>", the
	// hash in its short form (noValue when the trace has none).
	Deciders map[string]int
	Agree    int
	// Matrix[i][j] counts runs where choice selected complexityLevels[i] and score
	// complexityLevels[j].
	Matrix [3][3]int
	// MeanConfidence is each question's mean confidence over the compared runs.
	MeanConfidence map[domain.ComplexitySource]float64
	Disagreements  []Disagreement
}

// SummarizeDecisions compares the complexity questions over every trace that asked both
// of a real decision provider. Manual runs, runs decided by hand after a provider failure,
// the offline mock (its answers are a keyword heuristic, not data) and runs without the
// shadow question are counted as skipped.
func SummarizeDecisions(entries []Entry, unreadable []Unreadable) DecisionsSummary {
	s := DecisionsSummary{
		Traces:         len(entries),
		Skipped:        map[string]int{},
		Unreadable:     unreadable,
		Deciders:       map[string]int{},
		MeanConfidence: map[domain.ComplexitySource]float64{},
	}
	for _, e := range entries {
		t := e.Trace
		switch {
		case t.Source == domain.DecisionSourceManual:
			s.Skipped[SkipManual]++
			continue
		case t.Source == domain.DecisionSourceUser:
			s.Skipped[SkipUser]++
			continue
		case strings.HasPrefix(t.Model, mockModelPrefix):
			s.Skipped[SkipMock]++
			continue
		}
		choice, okC := t.Complexity.Sources[domain.ComplexitySourceChoice]
		score, okS := t.Complexity.Sources[domain.ComplexitySourceScore]
		ci, cj := slices.Index(complexityLevels, choice.Selected), slices.Index(complexityLevels, score.Selected)
		if !okC || !okS || ci < 0 || cj < 0 {
			s.Skipped[SkipNoShadow]++
			continue
		}

		s.Compared++
		s.Deciders[fmt.Sprintf("%s %s/%s %s", t.Source, t.Provider, t.Model, shortHash(t.PolicyHash))]++
		s.Matrix[ci][cj]++
		s.MeanConfidence[domain.ComplexitySourceChoice] += choice.Decision.Confidence
		s.MeanConfidence[domain.ComplexitySourceScore] += score.Decision.Confidence
		if ci == cj {
			s.Agree++
			continue
		}
		s.Disagreements = append(s.Disagreements, Disagreement{
			RunID: e.RunID, At: e.At, PolicyHash: t.PolicyHash, Choice: choice.Selected, Score: score.Selected, Request: t.Request,
		})
	}
	if s.Compared > 0 {
		for src := range s.MeanConfidence {
			s.MeanConfidence[src] /= float64(s.Compared)
		}
	}
	slices.SortFunc(s.Disagreements, func(a, b Disagreement) int {
		return cmp.Or(a.At.Compare(b.At), strings.Compare(a.RunID, b.RunID))
	})
	return s
}

// RenderDecisions writes the summary as plain text. runsDir only names where it was read.
func RenderDecisions(w io.Writer, runsDir string, s DecisionsSummary) {
	fmt.Fprintf(w, "Decisions report: %s\n", runsDir)
	if s.Traces == 0 && len(s.Unreadable) == 0 {
		fmt.Fprintln(w, "no decision traces yet")
		return
	}
	fmt.Fprintf(w, "traces: %d, compared: %d\n", s.Traces, s.Compared)
	var skipped []string
	for _, reason := range skipOrder {
		if n := s.Skipped[reason]; n > 0 {
			skipped = append(skipped, fmt.Sprintf("%d %s", n, reason))
		}
	}
	if len(s.Unreadable) > 0 {
		skipped = append(skipped, fmt.Sprintf("%d unreadable", len(s.Unreadable)))
	}
	if len(skipped) > 0 {
		fmt.Fprintf(w, "skipped: %s\n", strings.Join(skipped, ", "))
	}
	for _, u := range s.Unreadable {
		fmt.Fprintf(w, "  %s: %s\n", shortID(u.RunID), firstLine(u.Err.Error()))
	}
	if s.Compared == 0 {
		fmt.Fprintln(w, "\nnothing to compare: no run asked both complexity questions of Jev or an LLM")
		return
	}

	deciders := make([]string, 0, len(s.Deciders))
	for d := range s.Deciders {
		deciders = append(deciders, d)
	}
	slices.Sort(deciders)
	for i, d := range deciders {
		deciders[i] = fmt.Sprintf("%s (%d)", d, s.Deciders[d])
	}
	fmt.Fprintf(w, "decided by: %s\n", strings.Join(deciders, ", "))

	fmt.Fprintf(w, "\nchoice vs score: %d of %d agree (%.1f%%)\n\n", s.Agree, s.Compared, 100*float64(s.Agree)/float64(s.Compared))
	fmt.Fprintf(w, "%-10s %8s %8s %8s   <- score\n", "choice", "simple", "medium", "complex")
	for i, level := range complexityLevels {
		fmt.Fprintf(w, "%-10s %8d %8d %8d\n", level, s.Matrix[i][0], s.Matrix[i][1], s.Matrix[i][2])
	}
	fmt.Fprintf(w, "\nmean confidence: choice %.2f, score %.2f\n",
		s.MeanConfidence[domain.ComplexitySourceChoice], s.MeanConfidence[domain.ComplexitySourceScore])

	if len(s.Disagreements) == 0 {
		return
	}
	fmt.Fprintf(w, "\ndisagreements (%d):\n", len(s.Disagreements))
	for _, d := range s.Disagreements {
		fmt.Fprintf(w, "  %s  %s  %-*s  choice=%-7s score=%-7s %q\n",
			shortID(d.RunID), d.At.Local().Format("2006-01-02 15:04"), hashDigits, shortHash(d.PolicyHash), d.Choice, d.Score, excerpt(d.Request, 60))
	}
}

// shortID is the 8-character prefix `orchestrator resume` accepts.
func shortID(id string) string {
	if len(id) > 8 {
		return id[:8]
	}
	return id
}

func firstLine(s string) string {
	line, _, _ := strings.Cut(s, "\n")
	return line
}

// excerpt is the request's first line, cut to max runes.
func excerpt(s string, max int) string {
	s = strings.TrimSpace(firstLine(strings.TrimSpace(s)))
	if utf8.RuneCountInString(s) <= max {
		return s
	}
	return string([]rune(s)[:max-1]) + "…"
}

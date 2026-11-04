package report

import (
	"fmt"
	"io"
	"math"
	"slices"
	"strings"
	"time"

	"github.com/kasorse/orch/internal/domain"
)

// noValue stands for a complexity or a stage set a run has none of, and for a share that
// has no denominator.
const noValue = "—"

// RunEntry is one run as persistence.LoadRunDirs returns it.
type RunEntry struct {
	RunID string
	// At is when the run's decision.json was written; zero without a trace.
	At  time.Time
	Run domain.Run
	// Trace is decision.json, nil for a run that never got as far as a decision.
	Trace *domain.DecisionTrace
}

// UnreadableRun is a run directory whose files could not be read.
type UnreadableRun struct {
	Path string
	Err  string
}

// RunGroup is the runs that share a key: a complexity or a stage set.
type RunGroup struct {
	Key       string
	Runs      int
	Completed int
	// CostUSD is the cost of every provider call of the runs; DecisionCostUSD is the part
	// spent deciding the workflow.
	CostUSD         float64
	DecisionCostUSD float64
}

func (g *RunGroup) add(status domain.RunStatus, out domain.Outcome) {
	g.Runs++
	if status == domain.RunCompleted {
		g.Completed++
	}
	g.CostUSD += cost(out.Usage.CostUSD)
	g.DecisionCostUSD += cost(out.DecisionOverhead.CostUSD)
}

// cost is c, or 0 for what is not a cost (a run's usage is validated on load, but the
// report must never print NaN).
func cost(c float64) float64 {
	if c < 0 || math.IsNaN(c) || math.IsInf(c, 0) {
		return 0
	}
	return c
}

// CompletedShare is the fraction of the group's runs that completed; false without runs.
func (g RunGroup) CompletedShare() (float64, bool) {
	if g.Runs == 0 {
		return 0, false
	}
	return float64(g.Completed) / float64(g.Runs), true
}

// MeanCostUSD is the cost per run; false without runs.
func (g RunGroup) MeanCostUSD() (float64, bool) {
	if g.Runs == 0 {
		return 0, false
	}
	return g.CostUSD / float64(g.Runs), true
}

// DecisionShare is the fraction of the cost spent deciding the workflow; false when the
// group cost nothing.
func (g RunGroup) DecisionShare() (float64, bool) {
	if g.CostUSD <= 0 {
		return 0, false
	}
	return g.DecisionCostUSD / g.CostUSD, true
}

// StatusCount is the number of runs that ended in one status.
type StatusCount struct {
	Status domain.RunStatus
	Runs   int
}

// RunsSummary is what the runs report shows.
type RunsSummary struct {
	Unreadable []UnreadableRun
	Total      RunGroup
	// ByStatus is in the order a run goes through its statuses, an unknown one last.
	ByStatus []StatusCount
	// ByComplexity is simple, medium, complex, then the rest, then runs without one.
	ByComplexity []RunGroup
	// ByStageSet is the most common first.
	ByStageSet []RunGroup
}

var statusOrder = []domain.RunStatus{domain.RunPending, domain.RunRunning, domain.RunCompleted, domain.RunFailed, domain.RunStopped}

// SummarizeRuns counts how the runs ended and what they cost. A run's outcome is the one
// run.json recorded or, for a run written before that, the one derived from its record.
func SummarizeRuns(entries []RunEntry, unreadable []UnreadableRun) RunsSummary {
	s := RunsSummary{Unreadable: unreadable, Total: RunGroup{Key: "total"}}
	statuses := map[domain.RunStatus]int{}
	complexities := map[string]*RunGroup{}
	stageSets := map[string]*RunGroup{}
	group := func(m map[string]*RunGroup, key string) *RunGroup {
		g := m[key]
		if g == nil {
			g = &RunGroup{Key: key}
			m[key] = g
		}
		return g
	}

	for _, e := range entries {
		out := runOutcome(e)
		status := out.Status
		statuses[status]++
		s.Total.add(status, out)
		group(complexities, runComplexity(e.Run)).add(status, out)
		group(stageSets, runStageSet(e.Run)).add(status, out)
	}

	for _, st := range statusOrder {
		if n := statuses[st]; n > 0 {
			s.ByStatus = append(s.ByStatus, StatusCount{st, n})
			delete(statuses, st)
		}
	}
	var unknown []domain.RunStatus
	for st := range statuses {
		unknown = append(unknown, st)
	}
	slices.Sort(unknown)
	for _, st := range unknown {
		s.ByStatus = append(s.ByStatus, StatusCount{st, statuses[st]})
	}

	for _, c := range domain.ComplexityOrder() {
		if g := complexities[string(c)]; g != nil {
			s.ByComplexity = append(s.ByComplexity, *g)
			delete(complexities, string(c))
		}
	}
	none := complexities[noValue]
	delete(complexities, noValue)
	s.ByComplexity = append(s.ByComplexity, sortedGroups(complexities, func(a, b RunGroup) int { return strings.Compare(a.Key, b.Key) })...)
	if none != nil {
		s.ByComplexity = append(s.ByComplexity, *none)
	}

	s.ByStageSet = sortedGroups(stageSets, func(a, b RunGroup) int {
		if a.Runs != b.Runs {
			return b.Runs - a.Runs
		}
		return strings.Compare(a.Key, b.Key)
	})
	return s
}

func sortedGroups(m map[string]*RunGroup, cmp func(a, b RunGroup) int) []RunGroup {
	groups := make([]RunGroup, 0, len(m))
	for _, g := range m {
		groups = append(groups, *g)
	}
	slices.SortFunc(groups, cmp)
	return groups
}

// runComplexity is the complexity the run's workflow was built for, after any edit on the
// review screen. A run whose stages were named by hand was never classified.
func runComplexity(run domain.Run) string {
	d := run.Decision
	if d == nil || d.Source == domain.DecisionSourceManual || d.Complexity.Selected == "" {
		return noValue
	}
	return string(d.Complexity.Selected)
}

// runStageSet is the workflow's actions in order, e.g. "implement → test → review".
func runStageSet(run domain.Run) string {
	if run.Workflow == nil || len(run.Workflow.Steps) == 0 {
		return noValue
	}
	actions := make([]string, len(run.Workflow.Steps))
	for i, step := range run.Workflow.Steps {
		actions[i] = string(step.Action)
	}
	return strings.Join(actions, " → ")
}

// RenderRuns writes the runs summary as plain text, then the decisions section. dir only
// names where the runs were read.
func RenderRuns(w io.Writer, dir string, s RunsSummary, d DecisionsSummary) {
	fmt.Fprintf(w, "Runs report: %s\n", dir)
	if s.Total.Runs == 0 && len(s.Unreadable) == 0 {
		fmt.Fprintln(w, "no runs yet")
	} else {
		fmt.Fprintf(w, "runs: %d, unreadable: %d\n", s.Total.Runs, len(s.Unreadable))
		for _, u := range s.Unreadable {
			fmt.Fprintf(w, "  %s: %s\n", u.Path, firstLine(u.Err))
		}
		if s.Total.Runs > 0 {
			fmt.Fprintln(w, "\nby status")
			for _, c := range s.ByStatus {
				fmt.Fprintf(w, "  %-10s %5d\n", c.Status, c.Runs)
			}
			renderGroups(w, "by complexity", "complexity", s.ByComplexity)
			renderGroups(w, "by stage set", "stage set", s.ByStageSet)
			renderGroups(w, "total", "", []RunGroup{s.Total})
		}
	}
	fmt.Fprintln(w)
	RenderDecisions(w, dir, d)
}

// renderGroups writes one table of groups under title.
func renderGroups(w io.Writer, title, keyHeader string, groups []RunGroup) {
	width := len(keyHeader)
	for _, g := range groups {
		width = max(width, len([]rune(g.Key)))
	}
	fmt.Fprintf(w, "\n%s\n", title)
	fmt.Fprintf(w, "  %-*s %5s %12s %11s %10s %9s\n", width, keyHeader, "runs", "completed", "total cost", "mean cost", "decision")
	for _, g := range groups {
		fmt.Fprintf(w, "  %-*s %5d %12s %11s %10s %9s\n", width, g.Key, g.Runs, completed(g), money(g.CostUSD, true), mean(g), share(g.DecisionShare()))
	}
}

func completed(g RunGroup) string {
	p, ok := g.CompletedShare()
	if !ok {
		return noValue
	}
	return fmt.Sprintf("%d (%.1f%%)", g.Completed, 100*p)
}

func mean(g RunGroup) string {
	c, ok := g.MeanCostUSD()
	return money(c, ok)
}

func money(c float64, ok bool) string {
	if !ok {
		return noValue
	}
	return fmt.Sprintf("$%.4f", c)
}

func share(p float64, ok bool) string {
	if !ok {
		return noValue
	}
	return fmt.Sprintf("%.1f%%", 100*p)
}

package report

import (
	"slices"
	"strings"
	"testing"

	"orchestrator/internal/domain"
)

var (
	hashA = strings.Repeat("a", 64)
	hashB = strings.Repeat("b", 64)
)

func fp(v float64) *float64 { return &v }

func step(a domain.ActionType) domain.StepExecution {
	return domain.StepExecution{Action: a, Status: domain.StepCompleted}
}

func testStep(success bool) domain.StepExecution {
	s := step(domain.ActionTest)
	s.TestResult = &domain.TestResult{Success: success, ExitCode: map[bool]int{true: 0, false: 1}[success]}
	return s
}

func reviewStep(issues ...domain.ReviewIssue) domain.StepExecution {
	s := step(domain.ActionReview)
	s.ReviewResult = &domain.ReviewResult{Status: domain.ReviewPass, Issues: issues}
	return s
}

func issue(sev domain.Severity) domain.ReviewIssue {
	return domain.ReviewIssue{Severity: sev, File: "a.go", Description: "d"}
}

func TestTestingPaidOff(t *testing.T) {
	cases := []struct {
		name  string
		steps []domain.StepExecution
		want  bool
	}{
		{"failed, then passed after the fix", []domain.StepExecution{step(domain.ActionImplement), testStep(false), step(domain.ActionImplement), testStep(true)}, true},
		{"failure left exposed at the end", []domain.StepExecution{step(domain.ActionImplement), testStep(false)}, true},
		{"passed the first time", []domain.StepExecution{step(domain.ActionImplement), testStep(true)}, false},
		{"failed before any implement", []domain.StepExecution{testStep(false), step(domain.ActionImplement), testStep(true)}, false},
		{"failed without a result", []domain.StepExecution{step(domain.ActionImplement), step(domain.ActionTest)}, false},
		{"no test step", []domain.StepExecution{step(domain.ActionImplement)}, false},
		{"no steps", nil, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := testingPaidOff(domain.Run{Executions: tc.steps}); got != tc.want {
				t.Errorf("testingPaidOff = %v, want %v", got, tc.want)
			}
		})
	}
}

func TestReviewPaidOff(t *testing.T) {
	implement := step(domain.ActionImplement)
	cases := []struct {
		name      string
		steps     []domain.StepExecution
		fixMedium bool
		want      bool
	}{
		{"high issue, then implement", []domain.StepExecution{implement, reviewStep(issue(domain.SeverityHigh)), implement}, false, true},
		{"critical issue, then implement", []domain.StepExecution{implement, reviewStep(issue(domain.SeverityCritical)), implement, reviewStep()}, false, true},
		{"unmet criterion, then implement", []domain.StepExecution{implement, func() domain.StepExecution {
			s := reviewStep()
			s.ReviewResult.Criteria = []domain.CriterionVerdict{{Index: 1, Criterion: "c", Met: false, Evidence: "e"}}
			return s
		}(), implement}, false, true},
		{"medium issue under fix_medium, then implement", []domain.StepExecution{implement, reviewStep(issue(domain.SeverityMedium)), implement}, true, true},
		{"medium issue without fix_medium", []domain.StepExecution{implement, reviewStep(issue(domain.SeverityMedium)), implement}, false, false},
		{"low issue under fix_medium", []domain.StepExecution{implement, reviewStep(issue(domain.SeverityLow)), implement}, true, false},
		{"fail with nothing after it", []domain.StepExecution{implement, reviewStep(issue(domain.SeverityHigh))}, false, false},
		{"implement only before the fail", []domain.StepExecution{implement, reviewStep(issue(domain.SeverityHigh)), testStep(true)}, false, false},
		{"passing review", []domain.StepExecution{implement, reviewStep(), implement}, false, false},
		{"review without a result", []domain.StepExecution{implement, step(domain.ActionReview), implement}, false, false},
		{"no steps", nil, false, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			run := domain.Run{Executions: tc.steps, Options: domain.RunOptions{ReviewFixMedium: tc.fixMedium}}
			if got := reviewPaidOff(run); got != tc.want {
				t.Errorf("reviewPaidOff = %v, want %v", got, tc.want)
			}
		})
	}
}

// calibCase is a run decided by key, whose testing stage ran at probability p under
// threshold and, when paid, found a failure that a fix followed.
type calibCase struct {
	id        string
	key       CalibKey
	threshold float64
	p         float64
	paid      bool
	explored  bool
	cost      float64
	models    map[string]domain.ModelRoute
}

func (c calibCase) entry() RunEntry {
	steps := []domain.StepExecution{step(domain.ActionImplement), testStep(true)}
	if c.paid {
		steps = []domain.StepExecution{step(domain.ActionImplement), testStep(false), step(domain.ActionImplement), testStep(true)}
	}
	out := outcome(domain.RunCompleted, c.cost, 0)
	if c.paid {
		out.FixIterations = 1
	}
	return RunEntry{
		RunID: c.id,
		Run:   domain.Run{ID: c.id, Status: domain.RunCompleted, Executions: steps, Outcome: out},
		Trace: &domain.DecisionTrace{
			Source: domain.DecisionSourceJev, Provider: c.key.Provider, Model: c.key.Model, PolicyHash: c.key.PolicyHash,
			Complexity: domain.ComplexityOutcome{Selected: domain.ComplexityMedium},
			Stages: []domain.StageOutcome{
				{Stage: domain.StageTesting, Probability: fp(c.p), Threshold: fp(c.threshold), Enabled: true, Explored: c.explored},
				{Stage: domain.StageReview, Probability: fp(0.2), Threshold: fp(0.4)},
			},
			Models: c.models,
		},
	}
}

func calibEntries(cases ...calibCase) []RunEntry {
	var out []RunEntry
	for _, c := range cases {
		out = append(out, c.entry())
	}
	return out
}

func renderCalibration(es []RunEntry, unreadable []UnreadableRun, minRuns int) string {
	var b strings.Builder
	RenderCalibration(&b, "runs", Calibrate(es, unreadable, minRuns))
	return b.String()
}

var (
	jev113 = CalibKey{Provider: "jev", Model: "jev-1.13.0", PolicyHash: hashA}
	jev114 = CalibKey{Provider: "jev", Model: "jev-1.14.0", PolicyHash: hashA}
)

func TestRenderCalibration_RaiseProposal(t *testing.T) {
	// Testing at 0.50: no failure found below 0.70 in four runs, but half of the runs above.
	es := calibEntries(
		calibCase{id: "r01", key: jev113, threshold: 0.5, p: 0.45},
		calibCase{id: "r02", key: jev113, threshold: 0.5, p: 0.52},
		calibCase{id: "r03", key: jev113, threshold: 0.5, p: 0.58},
		calibCase{id: "r04", key: jev113, threshold: 0.5, p: 0.62},
		calibCase{id: "r05", key: jev113, threshold: 0.5, p: 0.68},
		calibCase{id: "r06", key: jev113, threshold: 0.5, p: 0.71},
		calibCase{id: "r07", key: jev113, threshold: 0.5, p: 0.74, paid: true},
		calibCase{id: "r08", key: jev113, threshold: 0.5, p: 0.77},
		calibCase{id: "r09", key: jev113, threshold: 0.5, p: 0.79, paid: true},
	)
	got := renderCalibration(es, nil, 4)
	assertGolden(t, "calibrate/raise", got)

	// The same runs in another order give the same bytes.
	slices.Reverse(es)
	if again := renderCalibration(es, nil, 4); again != got {
		t.Errorf("the report depends on the order of the runs:\n%s", again)
	}
}

func TestRenderCalibration_NotEnoughData(t *testing.T) {
	mock := calibCase{id: "k1", key: CalibKey{Provider: "jev", Model: "mock:jev-1.13.0", PolicyHash: hashA}, threshold: 0.5, p: 0.6}.entry()
	manual := RunEntry{RunID: "m1", Run: domain.Run{ID: "m1"}, Trace: &domain.DecisionTrace{Source: domain.DecisionSourceManual}}
	es := append(calibEntries(
		calibCase{id: "r1", key: jev113, threshold: 0.5, p: 0.55},
		calibCase{id: "r2", key: jev113, threshold: 0.5, p: 0.65, paid: true},
		calibCase{id: "r3", key: jev113, threshold: 0.5, p: 0.75},
	), mock, manual)
	unreadable := []UnreadableRun{{Path: "taskB/rep1", Err: "cannot read decision.json\nsecond line"}}

	out := renderCalibration(es, unreadable, 10)
	assertGolden(t, "calibrate/not_enough_data", out)
	if strings.Contains(out, "+   ") {
		t.Errorf("a proposal from fewer runs than --min-runs:\n%s", out)
	}
}

func TestRenderCalibration_TwoDecisionModelsStayApart(t *testing.T) {
	sonnet := map[string]domain.ModelRoute{
		"implement": {Provider: "claude", Model: "sonnet", Source: "base"},
		"review":    {Provider: "claude", Model: "sonnet", Source: "base"},
	}
	es := calibEntries(
		calibCase{id: "a1", key: jev113, threshold: 0.5, p: 0.6},
		calibCase{id: "a2", key: jev113, threshold: 0.5, p: 0.6},
		calibCase{id: "b1", key: jev114, threshold: 0.5, p: 0.6, paid: true, cost: 0.5, models: sonnet},
		calibCase{id: "b2", key: jev114, threshold: 0.5, p: 0.6, cost: 1.5, models: sonnet},
	)
	assertGolden(t, "calibrate/two_models", renderCalibration(es, nil, 2))
}

func TestRenderCalibration_TwoPolicyHashesStayApart(t *testing.T) {
	old := jev113
	raised := jev113
	raised.PolicyHash = hashB
	es := calibEntries(
		calibCase{id: "a1", key: old, threshold: 0.5, p: 0.60},
		calibCase{id: "a2", key: old, threshold: 0.5, p: 0.62},
		calibCase{id: "b1", key: raised, threshold: 0.7, p: 0.80},
		calibCase{id: "b2", key: raised, threshold: 0.7, p: 0.85},
	)
	assertGolden(t, "calibrate/two_hashes", renderCalibration(es, nil, 2))
}

// Traces from before policy_hash form their own group; their thresholds may differ, and then
// no run can say what a raise would have done.
func TestCalibrate_TracesWithoutAHashAreOneGroupWithoutAProposal(t *testing.T) {
	bare := CalibKey{Provider: "jev", Model: "jev-1.13.0"}
	es := calibEntries(
		calibCase{id: "o1", key: bare, threshold: 0.5, p: 0.6},
		calibCase{id: "o2", key: bare, threshold: 0.5, p: 0.6},
		calibCase{id: "o3", key: bare, threshold: 0.6, p: 0.7},
		calibCase{id: "n1", key: jev113, threshold: 0.5, p: 0.6},
	)
	c := Calibrate(es, nil, 2)
	if len(c.Groups) != 2 || c.Groups[0].Key != bare || c.Groups[1].Key != jev113 {
		t.Fatalf("groups = %+v, want the hashless group first, then the hashed one", c.Groups)
	}
	if g := c.Groups[0]; !g.Testing.Mixed() || g.Testing.Proposal != nil || g.Runs != 3 {
		t.Errorf("hashless group = %+v, want mixed thresholds and no proposal", g.Testing)
	}
	out := renderCalibration(es, nil, 2)
	for _, want := range []string{"policy —  (3 runs)", "testing: mixed thresholds, ran in 3 runs"} {
		if !strings.Contains(out, want) {
			t.Errorf("report lacks %q:\n%s", want, out)
		}
	}
}

func TestCalibrate_OldTracesAndEmptyInput(t *testing.T) {
	var b strings.Builder
	RenderCalibration(&b, "runs", Calibrate(nil, nil, 10))
	if want := "Calibration report: runs\nno runs yet\n"; b.String() != want {
		t.Errorf("empty report = %q, want %q", b.String(), want)
	}

	// A trace without stages (written before they were recorded) and a run without a decision.
	old := RunEntry{RunID: "o", Run: domain.Run{ID: "o", Status: domain.RunCompleted},
		Trace: &domain.DecisionTrace{Source: domain.DecisionSourceJev, Provider: "jev", Model: "m"}}
	none := RunEntry{RunID: "n", Run: domain.Run{ID: "n", Status: domain.RunPending}}
	c := Calibrate([]RunEntry{old, none}, nil, 10)
	if c.Used != 1 || c.Skipped[skipNoTrace] != 1 || len(c.Groups) != 1 || c.Groups[0].Testing.Ran != 0 {
		t.Errorf("calibration = %+v", c)
	}
	out := renderCalibration([]RunEntry{old, none}, nil, 10)
	for _, want := range []string{"skipped: 1 without a decision trace", "no change proposed"} {
		if !strings.Contains(out, want) {
			t.Errorf("report lacks %q:\n%s", want, out)
		}
	}
	if strings.Contains(out, "NaN") || strings.Contains(out, "Inf") {
		t.Errorf("report contains a non-number:\n%s", out)
	}
}

func TestCalibrate_ReviewBucketsAndStageThatDidNotRun(t *testing.T) {
	review := func(id string, p float64, execs ...domain.StepExecution) RunEntry {
		e := calibCase{id: id, key: jev113, threshold: 0.5, p: 0.9}.entry()
		e.Trace.Stages[1] = domain.StageOutcome{Stage: domain.StageReview, Probability: fp(p), Threshold: fp(0.4), Enabled: true}
		e.Run.Executions = execs
		return e
	}
	impl := step(domain.ActionImplement)
	sent := reviewStep(issue(domain.SeverityHigh))
	es := []RunEntry{
		review("r1", 0.45, impl, testStep(true), sent, impl),
		review("r2", 0.47, impl, testStep(true), reviewStep()),
		// Enabled, but the run stopped before the review: it says nothing about the threshold.
		review("r3", 0.48, impl, testStep(false)),
	}
	c := Calibrate(es, nil, 2)
	r := c.Groups[0].Review
	if r.Ran != 2 || r.Buckets[4] != (Bucket{Runs: 2, PaidOff: 1}) {
		t.Errorf("review = %+v, want 2 runs in bucket 0.4-0.5 and 1 paid off", r)
	}
	if r.Proposal != nil {
		t.Errorf("proposal %+v although a run in the window paid off", r.Proposal)
	}
}

func TestCalibrate_ComparisonOfAStageWithAndWithout(t *testing.T) {
	research := func(e RunEntry, on bool) RunEntry {
		e.Trace.Stages = append(e.Trace.Stages, domain.StageOutcome{Stage: domain.StageResearch, Probability: fp(0.5), Threshold: fp(0.5), Enabled: on})
		return e
	}
	es := []RunEntry{
		research(calibCase{id: "w1", key: jev113, threshold: 0.5, p: 0.6, paid: true}.entry(), true),
		research(calibCase{id: "w2", key: jev113, threshold: 0.5, p: 0.6}.entry(), true),
		research(calibCase{id: "n1", key: jev113, threshold: 0.5, p: 0.6, paid: true}.entry(), false),
	}
	es[2].Run.Status, es[2].Run.Outcome.Status = domain.RunFailed, domain.RunFailed

	rows := Calibrate(es, nil, 1).Groups[0].Comparison
	if len(rows) != 1 || rows[0].Stage != domain.StageResearch || rows[0].Complexity != domain.ComplexityMedium {
		t.Fatalf("comparison = %+v, want one research row at medium", rows)
	}
	if w := rows[0].With; w.Runs != 2 || w.Completed != 2 || w.FixSum != 1 {
		t.Errorf("with = %+v", w)
	}
	if n := rows[0].Without; n.Runs != 1 || n.Completed != 0 || n.FixSum != 1 {
		t.Errorf("without = %+v", n)
	}

	out := renderCalibration(es, nil, 1)
	sp := func(n int) string { return strings.Repeat(" ", n) }
	for _, want := range []string{
		"research, investigation: with and without the stage (no proposal)",
		"medium" + sp(5) + "research" + sp(2) + "with" + sp(9) + "2" + sp(3) + "2 (100.0%)" + sp(5) + "0.50",
		"medium" + sp(5) + "research" + sp(2) + "without" + sp(6) + "1" + sp(5) + "0 (0.0%)" + sp(5) + "1.00",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("report lacks %q:\n%s", want, out)
		}
	}
}

func TestRaiseProposal(t *testing.T) {
	runs := func(n int, p float64, paid bool) []sample {
		out := make([]sample, n)
		for i := range out {
			out[i] = sample{p: p, paidOff: paid}
		}
		return out
	}
	cases := []struct {
		name    string
		samples []sample
		current float64
		min     int
		want    float64 // 0: no proposal
	}{
		{"the first window with enough runs and no payoff", runs(3, 0.55, false), 0.5, 3, 0.6},
		{"a threshold between two edges", runs(3, 0.57, false), 0.55, 3, 0.6},
		{"a run on an edge belongs to the bucket above", runs(3, 0.6, false), 0.5, 3, 0.7},
		{"a payoff in the window", append(runs(3, 0.55, false), runs(1, 0.52, true)...), 0.5, 3, 0},
		{"too few runs", runs(2, 0.55, false), 0.5, 3, 0},
		{"runs below the threshold do not count", runs(5, 0.3, false), 0.5, 3, 0},
		{"the last window holds P = 1", runs(2, 1, false), 0.9, 2, 1},
		{"nothing above the top", runs(2, 1, false), 1, 2, 0},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			p := raiseProposal(tc.samples, tc.current, tc.min)
			switch {
			case tc.want == 0 && p != nil:
				t.Errorf("proposal %+v, want none", *p)
			case tc.want != 0 && (p == nil || p.To != tc.want || p.From != tc.current):
				t.Errorf("proposal %+v, want %.1f -> %.1f", p, tc.current, tc.want)
			}
		})
	}
}

func TestBucketOf(t *testing.T) {
	for p, want := range map[float64]int{0: 0, 0.05: 0, 0.1: 1, 0.3: 3, 0.6: 6, 0.7: 7, 0.79: 7, 0.9: 9, 0.99: 9, 1: 9} {
		if got := bucketOf(p); got != want {
			t.Errorf("bucketOf(%v) = %d, want %d", p, got, want)
		}
	}
}

func TestLowerProposal(t *testing.T) {
	runs := func(n, paid int, p float64, explored bool) []sample {
		out := make([]sample, n)
		for i := range out {
			out[i] = sample{p: p, paidOff: i < paid, explored: explored}
		}
		return out
	}
	join := func(parts ...[]sample) []sample { return slices.Concat(parts...) }
	cases := []struct {
		name    string
		samples []sample
		min     int
		want    float64 // 0: no proposal
		runs    int
	}{
		{"explored runs pay off as often as the baseline",
			join(runs(4, 1, 0.6, false), runs(3, 1, 0.45, true)), 3, 0.4, 3},
		{"the lowest edge that still qualifies",
			join(runs(4, 1, 0.6, false), runs(3, 1, 0.45, true), runs(3, 1, 0.35, true)), 3, 0.3, 6},
		{"a lower edge that pays off less is skipped",
			join(runs(4, 1, 0.6, false), runs(3, 1, 0.45, true), runs(3, 0, 0.35, true)), 3, 0.4, 3},
		{"explored runs pay off more often than the baseline",
			join(runs(4, 1, 0.6, false), runs(3, 3, 0.45, true)), 3, 0.4, 3},
		{"too few explored runs",
			join(runs(4, 1, 0.6, false), runs(2, 2, 0.45, true)), 3, 0, 0},
		{"explored runs pay off less often than the baseline",
			join(runs(4, 2, 0.6, false), runs(3, 1, 0.45, true)), 3, 0, 0},
		{"too few baseline runs",
			join(runs(2, 2, 0.6, false), runs(3, 3, 0.45, true)), 3, 0, 0},
		{"no baseline",
			runs(3, 3, 0.45, true), 3, 0, 0},
		{"a baseline that never paid off",
			join(runs(4, 0, 0.6, false), runs(3, 3, 0.45, true)), 3, 0, 0},
		{"a stage forced on below the threshold is not explored",
			join(runs(4, 1, 0.6, false), runs(5, 5, 0.45, false)), 3, 0, 0},
		{"the threshold itself belongs to the baseline",
			join(runs(4, 1, 0.5, false), runs(3, 1, 0.45, true)), 3, 0.4, 3},
		{"no samples", nil, 1, 0, 0},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			p := lowerProposal(tc.samples, 0.5, tc.min)
			switch {
			case tc.want == 0 && p != nil:
				t.Errorf("proposal %+v, want none", *p)
			case tc.want != 0 && (p == nil || p.To != tc.want || p.From != 0.5 || p.Runs != tc.runs):
				t.Errorf("proposal %+v, want 0.5 -> %.1f on %d runs", p, tc.want, tc.runs)
			}
		})
	}
}

// Explored runs sit below the threshold, so the raise rule never counts them.
func TestRaiseProposal_IgnoresExploredRuns(t *testing.T) {
	samples := []sample{{p: 0.45, paidOff: true, explored: true}, {p: 0.47, paidOff: true, explored: true},
		{p: 0.55}, {p: 0.56}}
	p := raiseProposal(samples, 0.5, 2)
	if p == nil || p.To != 0.6 || p.Runs != 2 {
		t.Errorf("proposal %+v, want 0.5 -> 0.6 on the two runs above the threshold", p)
	}
}

// The clarity section shows the first P(clear) per questions hash and the share of runs that
// stopped for input; it proposes nothing (specs/clarify A1).
func TestRenderCalibration_ClarityDistributionAndNeedsInput(t *testing.T) {
	asked := func(id, hash string, status domain.ClarityStatus, checks ...float64) RunEntry {
		e := calibCase{id: id, key: jev113, threshold: 0.5, p: 0.6}.entry()
		e.Trace.QuestionsHash = hash
		e.Trace.Clarity = &domain.ClarityTrace{Threshold: 0.8, Checks: checks, Status: status}
		return e
	}
	stopped := RunEntry{RunID: "s1", Run: domain.Run{
		ID: "s1", Status: domain.RunStopped, FailureReason: domain.FailureTaskNeedsInput,
		Task: domain.Task{Clarification: &domain.Clarification{Threshold: 0.8, Checks: []float64{0.42, 0.55}, Status: domain.ClarityUnclear}},
	}}
	es := []RunEntry{
		asked("a1", hashA, domain.ClarityClear, 0.9),
		asked("a2", hashA, domain.ClarityClear, 0.42, 0.86),
		asked("b1", hashB, domain.ClarityUnclear, 0.3, 0.5),
		stopped,
	}

	c := Calibrate(es, nil, 4)
	if len(c.Clarity) != 2 || c.Clarity[0].Key.QuestionsHash != hashA || c.Clarity[0].Runs != 2 || c.Clarity[0].Rechecked != 1 {
		t.Fatalf("clarity groups = %+v, want one per questions hash, the first of 2 runs and 1 re-check", c.Clarity)
	}
	if got := c.Clarity[0].First; got[4] != 1 || got[9] != 1 {
		t.Errorf("first checks = %v, want one in 0.4-0.5 and one in 0.9-1.0", got)
	}
	if c.NeedsInput.Runs != 1 || c.NeedsInput.Total != 4 || c.NeedsInput.Last[5] != 1 {
		t.Errorf("needs input = %+v, want 1 of 4 with its last check in 0.5-0.6", c.NeedsInput)
	}

	out := renderCalibration(es, nil, 4)
	for _, want := range []string{
		"clarity: P(clear) of the first check (specs/clarify; no proposal)",
		"questions " + hashA[:12] + "  threshold 0.80  (2 runs)",
		"re-checked in 1 runs; last check: clear 2\n",
		"re-checked in 1 runs; last check: unclear 1\n",
		`stopped "task needs input": 1 of 4 runs (25.0%)`,
	} {
		if !strings.Contains(out, want) {
			t.Errorf("report lacks %q:\n%s", want, out)
		}
	}
	if strings.Contains(out, "policy.clarify") {
		t.Errorf("the report proposes a clarity threshold:\n%s", out)
	}

	// Without a clarity trace and a stopped run the section is not there.
	plain := renderCalibration(calibEntries(calibCase{id: "r1", key: jev113, threshold: 0.5, p: 0.6}), nil, 4)
	if strings.Contains(plain, "clarity") {
		t.Errorf("the report has a clarity section for runs that never asked:\n%s", plain)
	}
}

func TestRenderCalibration_LowerProposal(t *testing.T) {
	// Testing at 0.50: one of four runs above it paid off, and so did one of three explored
	// runs at 0.45. Raising is out (a run above paid off), lowering to 0.40 is proposed.
	es := calibEntries(
		calibCase{id: "r1", key: jev113, threshold: 0.5, p: 0.60},
		calibCase{id: "r2", key: jev113, threshold: 0.5, p: 0.62},
		calibCase{id: "r3", key: jev113, threshold: 0.5, p: 0.64},
		calibCase{id: "r4", key: jev113, threshold: 0.5, p: 0.66, paid: true},
		calibCase{id: "e1", key: jev113, threshold: 0.5, p: 0.45, explored: true},
		calibCase{id: "e2", key: jev113, threshold: 0.5, p: 0.46, explored: true, paid: true},
		calibCase{id: "e3", key: jev113, threshold: 0.5, p: 0.47, explored: true},
	)
	c := Calibrate(es, nil, 3)
	prop := c.Groups[0].Testing.Proposal
	if prop == nil || prop.From != 0.5 || prop.To != 0.4 || prop.Runs != 3 {
		t.Fatalf("proposal = %+v, want 0.5 -> 0.4 on 3 runs", prop)
	}
	out := renderCalibration(es, nil, 3)
	for _, want := range []string{"-   testing: 0.50\n", "+   testing: 0.40\n", "      # 0.4-0.5: 3 runs, 1 paid off\n"} {
		if !strings.Contains(out, want) {
			t.Errorf("report lacks %q:\n%s", want, out)
		}
	}
}

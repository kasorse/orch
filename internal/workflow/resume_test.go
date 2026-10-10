package workflow

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"slices"
	"strings"
	"testing"

	"orchestrator/internal/apperr"
	"orchestrator/internal/domain"
)

var (
	passTest   = &domain.TestResult{Success: true}
	failTest   = &domain.TestResult{Success: false, ExitCode: 1}
	passReview = &domain.ReviewResult{Status: domain.ReviewPass}
	// The model says "pass", but a high-severity issue fails the review: Decide wins.
	failReview = &domain.ReviewResult{Status: domain.ReviewPass, Issues: []domain.ReviewIssue{{Severity: domain.SeverityHigh}}}
)

// wfp is wfOf (transitions_test.go) as a pointer, the way Run holds its workflow.
func wfp(actions ...domain.ActionType) *domain.Workflow {
	wf := wfOf(actions...)
	return &wf
}

func done(a domain.ActionType) domain.StepExecution {
	e := domain.StepExecution{Action: a, Status: domain.StepCompleted}
	switch a {
	case domain.ActionTest:
		e.TestResult = passTest
	case domain.ActionReview:
		e.ReviewResult = passReview
	}
	return e
}

func failedTest() domain.StepExecution {
	return domain.StepExecution{Action: domain.ActionTest, Status: domain.StepCompleted, TestResult: failTest}
}

func failedReview() domain.StepExecution {
	return domain.StepExecution{Action: domain.ActionReview, Status: domain.StepCompleted, ReviewResult: failReview}
}

func hardFail(a domain.ActionType, msg string) domain.StepExecution {
	return domain.StepExecution{Action: a, Status: domain.StepFailed, Error: msg}
}

func runWith(wf *domain.Workflow, execs ...domain.StepExecution) *domain.Run {
	for i := range execs {
		execs[i].Index = i
	}
	return &domain.Run{Workflow: wf, Executions: execs}
}

// A review that failed only on an unmet criterion (no issues) is still a failure after the
// run.json round trip a resume goes through (QL02).
func TestOutcomeOf_UnmetCriterion(t *testing.T) {
	review := &domain.ReviewResult{Status: domain.ReviewPass, Criteria: []domain.CriterionVerdict{
		{Index: 1, Criterion: "a", Met: true, Evidence: "a.go:1"},
		{Index: 2, Criterion: "b", Met: false, Evidence: "no test"},
		{Index: 3, Criterion: "c", Met: true, Evidence: "c.go:3"},
	}}
	data, err := json.Marshal(domain.StepExecution{Action: domain.ActionReview, Status: domain.StepCompleted, ReviewResult: review})
	if err != nil {
		t.Fatal(err)
	}
	var exec domain.StepExecution
	if err := json.Unmarshal(data, &exec); err != nil {
		t.Fatal(err)
	}
	got, err := outcomeOf(exec)
	if err != nil || got != OutcomeFailure {
		t.Errorf("outcomeOf = %v, %v; want failure", got, err)
	}

	full := wfp(domain.ActionImplement, domain.ActionTest, domain.ActionReview)
	entry, _, err := Entry(runWith(full, done(domain.ActionImplement), done(domain.ActionTest), exec))
	if err != nil || entry != StateImplement {
		t.Errorf("Entry = %s, %v; want implement", entry, err)
	}
}

func TestEntry(t *testing.T) {
	full := wfp(domain.ActionAnalyze, domain.ActionImplement, domain.ActionTest, domain.ActionReview)
	cases := []struct {
		name    string
		run     *domain.Run
		want    State
		whyHas  string
		wantErr string
	}{
		{"empty history starts at the first step", runWith(full), StateAnalyze, "start", ""},
		{"continue after a completed step", runWith(full, done(domain.ActionAnalyze)), StateImplement, "continue after analyze", ""},
		{"a hard failure retries the same action", runWith(full, done(domain.ActionAnalyze), hardFail(domain.ActionImplement, "timeout\nmore")),
			StateImplement, "retry implement (failed: timeout)", ""},
		{"a failed review hard error retries review", runWith(full, done(domain.ActionAnalyze), done(domain.ActionImplement), done(domain.ActionTest), hardFail(domain.ActionReview, "malformed review output")),
			StateReview, "retry review", ""},
		{"test failure with implement goes back to implement", runWith(full, done(domain.ActionAnalyze), done(domain.ActionImplement), failedTest()),
			StateImplement, "tests failed, retrying implement", ""},
		{"review failure with implement goes back to implement", runWith(full, done(domain.ActionAnalyze), done(domain.ActionImplement), done(domain.ActionTest), failedReview()),
			StateImplement, "review found issues", ""},
		{"test failure without implement re-runs the test", runWith(wfp(domain.ActionTest), failedTest()),
			StateTest, "re-run test (tests failed)", ""},
		{"review failure without implement is done", runWith(wfp(domain.ActionReview), failedReview()), StateDone, "", ""},
		{"a finished history is done", runWith(full, done(domain.ActionAnalyze), done(domain.ActionImplement), done(domain.ActionTest), done(domain.ActionReview)),
			StateDone, "", ""},
		{"a passing test continues to review", runWith(full, done(domain.ActionAnalyze), done(domain.ActionImplement), done(domain.ActionTest)),
			StateReview, "continue after test", ""},
		{"no workflow", &domain.Run{}, "", "", "no steps"},
		{"completed test without a result", runWith(full, domain.StepExecution{Action: domain.ActionTest, Status: domain.StepCompleted}), "", "", "no test result"},
		{"completed review without a result", runWith(full, domain.StepExecution{Action: domain.ActionReview, Status: domain.StepCompleted}), "", "", "no review result"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, why, err := Entry(tc.run)
			if tc.wantErr != "" {
				if err == nil || !strings.Contains(err.Error(), tc.wantErr) {
					t.Fatalf("want error containing %q, got %v", tc.wantErr, err)
				}
				return
			}
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if got != tc.want {
				t.Errorf("entry = %s, want %s", got, tc.want)
			}
			if !strings.Contains(why, tc.whyHas) {
				t.Errorf("why = %q, want it to contain %q", why, tc.whyHas)
			}
		})
	}
}

func TestCounters(t *testing.T) {
	full := wfp(domain.ActionImplement, domain.ActionTest, domain.ActionReview)
	run := runWith(full,
		done(domain.ActionImplement), failedTest(),
		done(domain.ActionImplement), done(domain.ActionTest), failedReview(),
		hardFail(domain.ActionImplement, "x"))
	total, fix, err := Counters(run)
	if err != nil {
		t.Fatal(err)
	}
	if total != 6 || fix != 2 {
		t.Errorf("Counters = %d, %d; want 6, 2", total, fix)
	}

	// Without implement a failing test is not a fix iteration: nothing is retried.
	_, fix, _ = Counters(runWith(wfp(domain.ActionTest), failedTest()))
	if fix != 0 {
		t.Errorf("fix iterations without implement = %d, want 0", fix)
	}
}

// resultExecutor scripts outcomes carrying real TestResult/ReviewResult values, the way
// the Dispatcher does, so the history Counters and Entry read is realistic. It records
// every call and can cancel the context after a given number of calls.
type resultExecutor struct {
	plan        map[domain.ActionType][]bool // per action, per call: passes?
	seen        map[domain.ActionType]int
	calls       []domain.ActionType
	cancelAfter int
	cancel      context.CancelFunc
	failAt      int // 1-based call number that returns a hard error, 0 = never
}

func (r *resultExecutor) Execute(ctx context.Context, a domain.ActionType, run *domain.Run) (StepOutcome, error) {
	r.calls = append(r.calls, a)
	n := r.seen[a]
	r.seen[a]++
	if r.cancel != nil && len(r.calls) == r.cancelAfter {
		defer r.cancel()
	}
	if r.failAt != 0 && len(r.calls) == r.failAt {
		return StepOutcome{}, errors.New("provider failed")
	}
	pass := true
	if plan := r.plan[a]; n < len(plan) {
		pass = plan[n]
	}
	out := StepOutcome{Outcome: OutcomeSuccess}
	if !pass {
		out.Outcome = OutcomeFailure
	}
	switch a {
	case domain.ActionTest:
		out.TestResult = failTest
		if pass {
			out.TestResult = passTest
		}
	case domain.ActionReview:
		out.ReviewResult = failReview
		if pass {
			out.ReviewResult = passReview
		}
	}
	return out, nil
}

// seenFrom counts each action in the history, so a resumed executor continues the same
// per-action script where the first process stopped.
func seenFrom(run *domain.Run) map[domain.ActionType]int {
	seen := map[domain.ActionType]int{}
	for _, e := range run.Executions {
		seen[e.Action]++
	}
	return seen
}

// TestResume_EquivalentToUninterrupted: stopping a run after any step and resuming it
// runs exactly the actions an uninterrupted run would have, ends in the same status, and
// counts the same fix iterations.
func TestResume_EquivalentToUninterrupted(t *testing.T) {
	wf := wfp(domain.ActionAnalyze, domain.ActionImplement, domain.ActionTest, domain.ActionReview)
	plan := map[domain.ActionType][]bool{
		domain.ActionTest:   {false, true, true},
		domain.ActionReview: {false, true},
	}
	limits := domain.LimitsView{MaxSteps: 20, MaxFixIterations: 3}

	ref := &resultExecutor{plan: plan, seen: map[domain.ActionType]int{}}
	refRun := &domain.Run{Workflow: wf}
	if err := (&Engine{Executor: ref, Limits: limits}).Run(context.Background(), refRun); err != nil {
		t.Fatalf("reference run: %v", err)
	}

	for k := 1; k < len(ref.calls); k++ {
		t.Run(fmt.Sprintf("stop after %d", k), func(t *testing.T) {
			ctx, cancel := context.WithCancel(context.Background())
			first := &resultExecutor{plan: plan, seen: map[domain.ActionType]int{}, cancelAfter: k, cancel: cancel}
			run := &domain.Run{Workflow: wf}
			err := (&Engine{Executor: first, Limits: limits}).Run(ctx, run)
			if !errors.Is(err, context.Canceled) || run.Status != domain.RunStopped {
				t.Fatalf("first process: status %s, err %v", run.Status, err)
			}

			run.Resumes = append(run.Resumes, domain.Resume{})
			second := &resultExecutor{plan: plan, seen: seenFrom(run)}
			if err := (&Engine{Executor: second, Limits: limits}).Run(context.Background(), run); err != nil {
				t.Fatalf("resumed: %v", err)
			}

			got := append(slices.Clone(first.calls), second.calls...)
			if !slices.Equal(got, ref.calls) {
				t.Errorf("actions = %v, want %v", got, ref.calls)
			}
			if run.Status != refRun.Status || run.FixIterations != refRun.FixIterations || run.TotalSteps != refRun.TotalSteps {
				t.Errorf("status/fix/total = %s/%d/%d, want %s/%d/%d", run.Status, run.FixIterations, run.TotalSteps,
					refRun.Status, refRun.FixIterations, refRun.TotalSteps)
			}
			for i, e := range run.Executions {
				if e.Index != i {
					t.Fatalf("execution %d has index %d", i, e.Index)
				}
				wantAttempt := 0
				if i >= k {
					wantAttempt = 1
				}
				if e.Attempt != wantAttempt {
					t.Errorf("execution %d attempt = %d, want %d", i, e.Attempt, wantAttempt)
				}
			}
		})
	}
}

func TestResume_RetriesAHardFailure(t *testing.T) {
	wf := wfp(domain.ActionAnalyze, domain.ActionImplement, domain.ActionTest)
	limits := domain.LimitsView{MaxSteps: 20, MaxFixIterations: 3}

	first := &resultExecutor{seen: map[domain.ActionType]int{}, failAt: 2}
	run := &domain.Run{Workflow: wf}
	if err := (&Engine{Executor: first, Limits: limits}).Run(context.Background(), run); err == nil || run.Status != domain.RunFailed {
		t.Fatalf("first process: status %s, err %v", run.Status, err)
	}

	second := &resultExecutor{seen: seenFrom(run)}
	if err := (&Engine{Executor: second, Limits: limits}).Run(context.Background(), run); err != nil {
		t.Fatalf("resumed: %v", err)
	}
	if want := []domain.ActionType{domain.ActionImplement, domain.ActionTest}; !slices.Equal(second.calls, want) {
		t.Errorf("resumed actions = %v, want %v", second.calls, want)
	}
	if run.Status != domain.RunCompleted || len(run.Executions) != 4 {
		t.Errorf("status %s with %d executions, want completed with 4", run.Status, len(run.Executions))
	}
	if run.Executions[1].Status != domain.StepFailed {
		t.Error("the failed execution was rewritten; history must be append-only")
	}
}

func TestResume_FinishedHistoryCompletesWithoutCalls(t *testing.T) {
	run := runWith(wfp(domain.ActionAnalyze), done(domain.ActionAnalyze))
	run.Status = domain.RunRunning // crashed between persisting the step and the status
	exec := &resultExecutor{seen: map[domain.ActionType]int{}}
	if err := (&Engine{Executor: exec, Limits: domain.LimitsView{MaxSteps: 5, MaxFixIterations: 1}}).Run(context.Background(), run); err != nil {
		t.Fatal(err)
	}
	if len(exec.calls) != 0 || run.Status != domain.RunCompleted {
		t.Errorf("calls %v, status %s", exec.calls, run.Status)
	}
}

func TestResume_LimitsCountTheWholeHistory(t *testing.T) {
	wf := wfp(domain.ActionImplement, domain.ActionTest)

	t.Run("fix iterations", func(t *testing.T) {
		// Two failed tests: the second transition exceeded max_fix_iterations 1.
		run := runWith(wf, done(domain.ActionImplement), failedTest(), done(domain.ActionImplement), failedTest())
		run.FixIterations = 1 // the persisted counter lost an increment (G12)
		limits := domain.LimitsView{MaxSteps: 20, MaxFixIterations: 1}

		entry, _, _ := Entry(run)
		err := CheckLimits(run, limits, entry)
		var le *apperr.LimitExceededError
		if !errors.As(err, &le) || !strings.Contains(err.Error(), "limits.max_fix_iterations") || !strings.Contains(err.Error(), "used 2") {
			t.Fatalf("CheckLimits = %v", err)
		}

		exec := &resultExecutor{seen: map[domain.ActionType]int{}}
		err = (&Engine{Executor: exec, Limits: limits}).Run(context.Background(), run)
		if !errors.As(err, &le) || len(exec.calls) != 0 {
			t.Fatalf("engine: err %v, calls %v", err, exec.calls)
		}
		if run.Status != domain.RunStopped || run.FailureReason != "maximum fix iterations exceeded" || run.FixIterations != 2 {
			t.Errorf("status %s, reason %q, fix %d", run.Status, run.FailureReason, run.FixIterations)
		}

		// Raising the limit lets it continue at implement.
		limits.MaxFixIterations = 2
		if err := CheckLimits(run, limits, entry); err != nil {
			t.Fatalf("after raising the limit: %v", err)
		}
		if err := (&Engine{Executor: exec, Limits: limits}).Run(context.Background(), run); err != nil {
			t.Fatal(err)
		}
		if want := []domain.ActionType{domain.ActionImplement, domain.ActionTest}; !slices.Equal(exec.calls, want) {
			t.Errorf("calls %v, want %v", exec.calls, want)
		}
	})

	t.Run("max steps", func(t *testing.T) {
		run := runWith(wf, done(domain.ActionImplement), hardFail(domain.ActionTest, "sandbox"))
		entry, _, _ := Entry(run)
		err := CheckLimits(run, domain.LimitsView{MaxSteps: 2, MaxFixIterations: 5}, entry)
		if err == nil || !strings.Contains(err.Error(), "limits.max_steps") {
			t.Fatalf("CheckLimits = %v", err)
		}
		if err := CheckLimits(run, domain.LimitsView{MaxSteps: 3, MaxFixIterations: 5}, entry); err != nil {
			t.Fatalf("with room left: %v", err)
		}
	})

	t.Run("a hard-failed implement is not a fix-iteration entry", func(t *testing.T) {
		run := runWith(wf, done(domain.ActionImplement), failedTest(), hardFail(domain.ActionImplement, "timeout"))
		entry, _, _ := Entry(run)
		// The fix iteration was already allowed when the test failed; retrying the
		// failed implement does not enter it again.
		if err := CheckLimits(run, domain.LimitsView{MaxSteps: 20, MaxFixIterations: 1}, entry); err != nil {
			t.Fatalf("CheckLimits = %v", err)
		}
	})
}

// A run stopped for no progress resumes into implement, whatever Next says: after a tree
// stop the last step is an implement, which Next sends to test. Entry never judges progress.
func TestEntry_ResumeAfterNoProgress(t *testing.T) {
	wf := wfp(domain.ActionImplement, domain.ActionTest, domain.ActionReview)
	same := func() []domain.StepExecution {
		return []domain.StepExecution{implTree(treeID('a')), failedTest(), implTree(treeID('a'))}
	}
	stopped := func(reason string, execs ...domain.StepExecution) *domain.Run {
		run := runWith(wf, execs...)
		run.Status, run.FailureReason = domain.RunStopped, reason
		return run
	}
	approved := func(run *domain.Run, from ...string) *domain.Run {
		for _, reason := range from {
			run.Resumes = append(run.Resumes, domain.Resume{FromStatus: domain.RunStopped, FromReason: reason})
		}
		run.Status, run.FailureReason = domain.RunRunning, ""
		return run
	}
	resumedImplement := implTree(treeID('b'))
	resumedImplement.Attempt = 1
	resumedTest := failedTest()
	resumedTest.Attempt = 1

	cases := []struct {
		name string
		run  *domain.Run
		want State
		why  string
	}{
		{"stopped after an unchanged implement", stopped(ReasonNoChange, same()...), StateImplement, "resume after no progress"},
		{"stopped after the same failing tests", stopped(ReasonSameTests, implTree(treeID('a')), failedTest(), implTree(treeID('b')), failedTest()), StateImplement, "resume after no progress"},
		{"stopped after a repeating review", stopped(ReasonSameReviewHits, implTree(treeID('a')), done(domain.ActionTest), failedReview()), StateImplement, "resume after no progress"},
		{"the resume was approved: status and reason are cleared", approved(stopped(ReasonNoChange, same()...), ReasonNoChange), StateImplement, "resume after no progress"},
		{"a resume that crashed before its first step", approved(stopped(ReasonNoChange, same()...), ReasonNoChange, ""), StateImplement, "resume after no progress"},
		{"a step ran after the approved resume", approved(stopped(ReasonNoChange, append(same(), resumedImplement)...), ReasonNoChange), StateTest, "continue after implement"},
		{"stopped for another reason", stopped("maximum steps exceeded", same()...), StateTest, "continue after implement"},
		{"an earlier no-progress stop does not force implement once a step ran", approved(stopped("", append(same(), resumedImplement, resumedTest)...), ReasonNoChange), StateImplement, "tests failed"},
		{"failed, not stopped", func() *domain.Run { r := runWith(wf, same()...); r.Status = domain.RunFailed; return r }(), StateTest, "continue after implement"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, why, err := Entry(tc.run)
			if err != nil {
				t.Fatal(err)
			}
			if got != tc.want || !strings.Contains(why, tc.why) {
				t.Errorf("Entry = %s (%s), want %s (%s)", got, why, tc.want, tc.why)
			}
		})
	}
}

func TestVerify(t *testing.T) {
	p := 0.9
	sel := domain.StageSelection{Stages: []domain.StageOutcome{
		{Stage: domain.StageDevelopment, Probability: &p, Enabled: true},
		{Stage: domain.StageTesting, Probability: &p, Enabled: true},
	}}
	built, err := Build(sel)
	if err != nil {
		t.Fatal(err)
	}
	good := func() *domain.Run {
		wf := built
		wf.Steps = slices.Clone(built.Steps)
		return &domain.Run{Decision: &domain.Decision{Stages: sel}, Workflow: &wf,
			Executions: []domain.StepExecution{{Index: 0, Action: domain.ActionImplement, Status: domain.StepCompleted}}}
	}

	if err := Verify(good()); err != nil {
		t.Fatalf("Verify(good) = %v", err)
	}

	cases := map[string]func(r *domain.Run){
		"no decision": func(r *domain.Run) { r.Decision = nil },
		"no workflow": func(r *domain.Run) { r.Workflow = nil },
		"step added": func(r *domain.Run) {
			r.Workflow.Steps = append(r.Workflow.Steps, domain.Step{Action: domain.ActionReview, Stage: domain.StageReview})
		},
		"steps reordered":        func(r *domain.Run) { slices.Reverse(r.Workflow.Steps) },
		"execution not in steps": func(r *domain.Run) { r.Executions[0].Action = domain.ActionResearch },
		"decision builds nothing": func(r *domain.Run) {
			r.Decision.Stages = domain.StageSelection{}
		},
	}
	for name, edit := range cases {
		t.Run(name, func(t *testing.T) {
			r := good()
			edit(r)
			if err := Verify(r); err == nil {
				t.Error("Verify accepted an inconsistent run")
			}
		})
	}
}

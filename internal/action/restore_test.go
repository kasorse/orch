package action

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"slices"
	"strings"
	"testing"

	"orchestrator/internal/apperr"
	"orchestrator/internal/domain"
	"orchestrator/internal/execution"
	"orchestrator/internal/persistence"
	"orchestrator/internal/provider"
	"orchestrator/internal/repository"
	"orchestrator/internal/testexec"
	"orchestrator/internal/workflow"
)

// routedClient answers each prompt by its "ACTION: <name>" first line, taking the next
// scripted output for that action.
type routedClient struct {
	outputs map[string][]string
	seen    map[string]int
}

func (c *routedClient) Supports(domain.Authority) bool { return true }

func (c *routedClient) Execute(ctx context.Context, req provider.Request) (provider.Result, error) {
	first, _, _ := strings.Cut(req.Prompt, "\n")
	action := strings.TrimPrefix(first, "ACTION: ")
	n := c.seen[action]
	c.seen[action]++
	outs := c.outputs[action]
	return provider.Result{Output: outs[min(n, len(outs)-1)]}, nil
}

// dispatcherState is everything Restore must rebuild.
type dispatcherState struct {
	Research       *domain.ResearchArtifact
	Analysis       *domain.AnalysisArtifact
	Plan           *domain.PlanArtifact
	Impl           *domain.ImplementationArtifact
	Test           *domain.TestResult
	PendingTest    *domain.TestResult
	PendingReviews []domain.ReviewIssue
	PendingUnmet   []domain.CriterionVerdict
}

func stateOf(d *Dispatcher) dispatcherState {
	return dispatcherState{d.lastResearch, d.lastAnalysis, d.lastPlan, d.lastImpl, d.lastTest, d.pendingTestFailure, d.pendingReviewIssues, d.pendingUnmetCriteria}
}

// restoreChecker is an engine Observer: after every step it copies the run through JSON
// (as a resume would read it), restores a fresh Dispatcher from the store, and compares
// its context with the live Dispatcher's.
type restoreChecker struct {
	t      *testing.T
	live   *Dispatcher
	store  *persistence.Store
	checks int
}

func (c *restoreChecker) StepStarted(*domain.Run, int, domain.ActionType) {}
func (c *restoreChecker) RunFinished(*domain.Run)                         {}

func (c *restoreChecker) StepFinished(run *domain.Run, index int, exec domain.StepExecution, _ workflow.StepOutcome) {
	c.t.Helper()
	data, err := json.Marshal(run)
	if err != nil {
		c.t.Fatal(err)
	}
	var copied domain.Run
	if err := json.Unmarshal(data, &copied); err != nil {
		c.t.Fatal(err)
	}

	restored := &Dispatcher{}
	if err := restored.Restore(&copied, c.store); err != nil {
		c.t.Fatalf("after step %d (%s): Restore: %v", index, exec.Action, err)
	}
	if got, want := stateOf(restored), stateOf(c.live); !reflect.DeepEqual(got, want) {
		c.t.Errorf("after step %d (%s): restored context differs\n got: %+v\nwant: %+v", index, exec.Action, got, want)
	}
	c.checks++
}

func TestRestore_MatchesLiveContextAfterEveryStep(t *testing.T) {
	store := persistence.NewStore(t.TempDir())
	run, err := store.NewRun(domain.Task{Description: "fix the retry"})
	if err != nil {
		t.Fatal(err)
	}
	run.Workflow = &domain.Workflow{Steps: []domain.Step{
		{Action: domain.ActionResearch}, {Action: domain.ActionAnalyze}, {Action: domain.ActionPlan},
		{Action: domain.ActionImplement}, {Action: domain.ActionTest}, {Action: domain.ActionReview},
	}}

	client := &routedClient{seen: map[string]int{}, outputs: map[string][]string{
		"research":  {`{"summary":"r","findings":["f"],"options":[],"recommendation":"x","references":["u"]}`},
		"analyze":   {`{"summary":"a","root_cause":"rc","affected_files":["a.go"],"recommendation":"r","risks":[]}`},
		"plan":      {`not json - falls back to the summary`},
		"implement": {`{"summary":"i1","changed_files":["a.go"],"tests_added":[]}`, `{"summary":"i2","changed_files":["a.go"]}`, `{"summary":"i3"}`},
		"review": {
			`{"status":"pass","summary":"bad","issues":[{"severity":"high","file":"a.go","description":"bug"}]}`,
			`{"status":"pass","summary":"ok","issues":[{"severity":"low","file":"a.go","description":"nit"}]}`,
		},
	}}
	registry := provider.NewRegistry()
	registry.Register(provider.Claude, client)

	testRuns := &execution.FakeProcessExecutor{Results: []execution.ProcessResult{
		{Stderr: "FAIL", ExitCode: 1}, {Stdout: "PASS"}, {Stdout: "PASS"},
	}}
	repoExec := &execution.FakeProcessExecutor{Results: slices.Concat(reviewChange("diff"), reviewChange("diff"))}
	live := New(testConfig(), registry, &repository.Repository{Dir: "/repo", Executor: treeless{repoExec}}, testexec.New(testRuns))
	live.Guard = &fakeGuard{}
	live.Artifacts = store

	checker := &restoreChecker{t: t, live: live, store: store}
	engine := &workflow.Engine{Executor: live, Observer: checker, Persist: store.SaveRun,
		Limits: domain.LimitsView{MaxSteps: 20, MaxFixIterations: 3}}
	if err := engine.Run(context.Background(), run); err != nil {
		t.Fatalf("run: %v", err)
	}

	// research, analyze, plan, implement, test (fail), implement, test, review (fail),
	// implement, test, review.
	if checker.checks != 11 {
		t.Errorf("checked %d steps, want 11", checker.checks)
	}
}

// memReader is an in-memory ArtifactReader.
type memReader map[string]string

func memKey(i int, a domain.ActionType) string { return persistence.ArtifactRelPath(i, a) }

func (m memReader) ReadArtifact(_ string, i int, a domain.ActionType) ([]byte, error) {
	data, ok := m[memKey(i, a)]
	if !ok {
		return nil, os.ErrNotExist
	}
	return []byte(data), nil
}

func TestRestore_FailedImplementKeepsThePendingTestFailure(t *testing.T) {
	fail := &domain.TestResult{Command: "go test", ExitCode: 1, Stderr: "FAIL"}
	run := &domain.Run{ID: "r", Executions: []domain.StepExecution{
		{Index: 0, Action: domain.ActionImplement, Status: domain.StepCompleted},
		{Index: 1, Action: domain.ActionTest, Status: domain.StepCompleted, TestResult: fail},
		{Index: 2, Action: domain.ActionImplement, Status: domain.StepFailed, Error: "timeout"},
	}}
	// The failed implement's artifact must not be read: it has none, and is skipped.
	reader := memReader{memKey(0, domain.ActionImplement): `{"summary":"i1","changed_files":null,"tests_added":null}`}

	d := &Dispatcher{}
	if err := d.Restore(run, reader); err != nil {
		t.Fatal(err)
	}
	if d.pendingTestFailure == nil || d.pendingTestFailure.Stderr != "FAIL" {
		t.Errorf("pending test failure = %+v, want the test failure", d.pendingTestFailure)
	}
	if d.lastImpl == nil || d.lastImpl.Summary != "i1" {
		t.Errorf("last implementation = %+v", d.lastImpl)
	}
}

func TestRestore_RefusesMissingOrInvalidState(t *testing.T) {
	impl := domain.StepExecution{Index: 0, Action: domain.ActionImplement, Status: domain.StepCompleted}
	pass := &domain.TestResult{Command: "go test", Success: true}
	cases := []struct {
		name   string
		execs  []domain.StepExecution
		reader memReader
		want   string
	}{
		{"missing artifact", []domain.StepExecution{impl}, memReader{}, "missing"},
		{"unknown field", []domain.StepExecution{impl},
			memReader{memKey(0, domain.ActionImplement): `{"summary":"x","extra":1}`}, "unknown field"},
		{"trailing data", []domain.StepExecution{impl},
			memReader{memKey(0, domain.ActionImplement): `{"summary":"x"} {}`}, "after the object"},
		{"test without result", []domain.StepExecution{{Index: 0, Action: domain.ActionTest, Status: domain.StepCompleted}},
			memReader{}, "no test result"},
		{"test artifact disagrees with run.json", []domain.StepExecution{{Index: 0, Action: domain.ActionTest, Status: domain.StepCompleted, TestResult: pass}},
			memReader{memKey(0, domain.ActionTest): `{"command":"go test","exit_code":1,"success":false,"stdout":"","stderr":"","duration":0}`}, "does not match"},
		{"review with an unknown severity", []domain.StepExecution{{Index: 0, Action: domain.ActionReview, Status: domain.StepCompleted,
			ReviewResult: &domain.ReviewResult{Status: domain.ReviewPass, Issues: []domain.ReviewIssue{{Severity: "catastrophic"}}}}},
			memReader{}, "severity"},
		{"review with a duplicate criterion index", []domain.StepExecution{{Index: 0, Action: domain.ActionReview, Status: domain.StepCompleted,
			ReviewResult: &domain.ReviewResult{Status: domain.ReviewFail, Criteria: []domain.CriterionVerdict{
				{Index: 1, Evidence: "a"}, {Index: 1, Evidence: "b"}}}}},
			memReader{}, "more than once"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			err := (&Dispatcher{}).Restore(&domain.Run{ID: "r", Executions: tc.execs}, tc.reader)
			var rse *apperr.RunStateError
			if !errors.As(err, &rse) || rse.Kind != apperr.RunCorrupt {
				t.Fatalf("want RunStateError{corrupt}, got %v", err)
			}
			if !strings.Contains(err.Error(), tc.want) {
				t.Errorf("error %q does not mention %q", err, tc.want)
			}
		})
	}
}

// A plan artifact written before acceptance_criteria existed loads, with no criteria, and the
// prompts built from it work; one written after it loads with its criteria (QL01).
func TestRestore_PlanArtifactWithAndWithoutCriteria(t *testing.T) {
	old := `{"summary":"p","steps":["a"],"affected_files":["a.go"],"risks":[]}`
	current := `{"summary":"p","steps":["a"],"affected_files":["a.go"],"risks":[],"acceptance_criteria":["a.go compiles","a test covers it"]}`
	cases := []struct {
		name, artifact string
		want           []string
	}{
		{"before the field", old, nil},
		{"with the field", current, []string{"a.go compiles", "a test covers it"}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			run := &domain.Run{ID: "r", Executions: []domain.StepExecution{
				{Index: 0, Action: domain.ActionPlan, Status: domain.StepCompleted},
			}}
			d := &Dispatcher{}
			if err := d.Restore(run, memReader{memKey(0, domain.ActionPlan): tc.artifact}); err != nil {
				t.Fatal(err)
			}
			if d.lastPlan == nil || !slices.Equal(d.lastPlan.AcceptanceCriteria, tc.want) {
				t.Fatalf("restored plan = %+v, want criteria %q", d.lastPlan, tc.want)
			}
			prompt := BuildImplementPrompt(ImplementPromptInput{Task: "t", Plan: d.lastPlan})
			if got := strings.Contains(prompt, "Acceptance criteria:\n"); got != (tc.want != nil) {
				t.Errorf("criteria in the implement prompt = %v, want %v:\n%s", got, tc.want != nil, prompt)
			}
		})
	}
}

// A review that failed only on an unmet criterion leaves it pending for the retry, and an
// implement consumes it.
func TestRestore_UnmetCriteriaArePending(t *testing.T) {
	review := domain.StepExecution{Index: 0, Action: domain.ActionReview, Status: domain.StepCompleted,
		ReviewResult: &domain.ReviewResult{Status: domain.ReviewPass, Criteria: []domain.CriterionVerdict{
			{Index: 1, Criterion: "a", Met: true, Evidence: "a.go:1"},
			{Index: 2, Criterion: "b", Met: false, Evidence: "no test"},
		}}}
	impl := domain.StepExecution{Index: 1, Action: domain.ActionImplement, Status: domain.StepCompleted}
	reader := memReader{memKey(1, domain.ActionImplement): `{"summary":"i"}`}

	d := &Dispatcher{}
	if err := d.Restore(&domain.Run{ID: "r", Executions: []domain.StepExecution{review}}, reader); err != nil {
		t.Fatal(err)
	}
	if len(d.pendingUnmetCriteria) != 1 || d.pendingUnmetCriteria[0].Index != 2 || len(d.pendingReviewIssues) != 0 {
		t.Fatalf("pending unmet criteria = %+v", d.pendingUnmetCriteria)
	}
	prompt := BuildImplementPrompt(ImplementPromptInput{Task: "t", UnmetCriteria: d.pendingUnmetCriteria, ReviewIssues: d.pendingReviewIssues})
	if !strings.Contains(prompt, "- 2. b\n  evidence: no test\n") {
		t.Errorf("the retry prompt lacks the unmet criterion:\n%s", prompt)
	}

	if err := d.Restore(&domain.Run{ID: "r", Executions: []domain.StepExecution{review, impl}}, reader); err != nil {
		t.Fatal(err)
	}
	if d.pendingUnmetCriteria != nil {
		t.Errorf("an implement left %+v pending", d.pendingUnmetCriteria)
	}
}

func TestRestore_TestArtifactIsOptional(t *testing.T) {
	pass := &domain.TestResult{Command: "go test", Success: true}
	run := &domain.Run{ID: "r", Executions: []domain.StepExecution{
		{Index: 0, Action: domain.ActionTest, Status: domain.StepCompleted, TestResult: pass},
	}}
	d := &Dispatcher{}
	if err := d.Restore(run, memReader{}); err != nil {
		t.Fatal(err)
	}
	if d.lastTest == nil || !d.lastTest.Success {
		t.Errorf("last test = %+v", d.lastTest)
	}
}

// orderGuard records whether Changed has run when the artifact is saved.
type orderGuard struct {
	fakeGuard
	changedCalled bool
}

func (g *orderGuard) Changed(ctx context.Context, before GuardState) ([]string, error) {
	g.changedCalled = true
	return g.changed, nil
}

type orderWriter struct {
	guard        *orderGuard
	savedTooSoon bool
}

func (w *orderWriter) SaveArtifact(string, int, domain.ActionType, any) (string, error) {
	if !w.guard.changedCalled {
		w.savedTooSoon = true
	}
	return "artifacts/000-implement.json", nil
}

func TestExecuteImplement_SavesArtifactOnlyAfterTheGuard(t *testing.T) {
	for _, changed := range [][]string{nil, {".claude/settings.json"}} {
		d := newDispatcher(&fakeClient{output: `{"summary":"s"}`})
		guard := &orderGuard{fakeGuard: fakeGuard{changed: changed}}
		writer := &orderWriter{guard: guard}
		d.Guard = guard
		d.Artifacts = writer

		outcome, _ := d.Execute(context.Background(), domain.ActionImplement, &domain.Run{ID: "r"})
		if writer.savedTooSoon {
			t.Errorf("violation %v: the artifact was written while the guard was open", changed)
		}
		if outcome.ArtifactPath == "" {
			t.Errorf("violation %v: the artifact path is missing from the outcome", changed)
		}
	}
}

func TestStoreImplementsArtifactReader(t *testing.T) {
	store := persistence.NewStore(t.TempDir())
	run, err := store.NewRun(domain.Task{Description: "x"})
	if err != nil {
		t.Fatal(err)
	}
	var _ ArtifactReader = store
	if _, err := store.ReadArtifact(run.ID, 0, domain.ActionPlan); !os.IsNotExist(err) {
		t.Errorf("missing artifact: %v, want not-exist", err)
	}
	if _, err := store.SaveArtifact(run.ID, 0, domain.ActionPlan, domain.PlanArtifact{Summary: "p"}); err != nil {
		t.Fatal(err)
	}
	data, err := store.ReadArtifact(run.ID, 0, domain.ActionPlan)
	if err != nil || !strings.Contains(string(data), `"summary": "p"`) {
		t.Errorf("read back %q, %v", data, err)
	}
	if _, err := store.ReadArtifact("../"+filepath.Base(run.ID), 0, domain.ActionPlan); err == nil {
		t.Error("a non-ID run reached the filesystem")
	}
}

// treeless answers the git calls that write the working tree as a tree (implement's diff
// count; they run on a throwaway index) with no tree, so the queued results stay in step
// with the review diff's calls.
type treeless struct{ next execution.ProcessExecutor }

func (t treeless) Run(ctx context.Context, spec execution.Spec) (execution.ProcessResult, error) {
	for _, e := range spec.Env {
		if strings.HasPrefix(e, "GIT_INDEX_FILE=") {
			return execution.ProcessResult{}, nil
		}
	}
	return t.next.Run(ctx, spec)
}

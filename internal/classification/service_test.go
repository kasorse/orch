package classification

import (
	"context"
	"encoding/json"
	"errors"
	"reflect"
	"testing"

	"github.com/kasorse/orch/internal/apperr"
	"github.com/kasorse/orch/internal/decision"
	"github.com/kasorse/orch/internal/domain"
	"github.com/kasorse/orch/internal/interaction"
	"github.com/kasorse/orch/internal/policy"
	"github.com/kasorse/orch/internal/translate"
	"github.com/kasorse/orch/internal/workflow"
)

type fakeTranslator struct {
	res translate.Result
	err error
}

func (f fakeTranslator) Prepare(_ context.Context, _, request string) (translate.Result, error) {
	if f.err != nil {
		return translate.Result{}, f.err
	}
	res := f.res
	res.Original = request
	if res.Text == "" {
		res.Text = request
	}
	return res, nil
}

type fakeProvider struct {
	depth    domain.ContextDepthDecision
	depthErr error
	tc       domain.TaskClassification
	tcErr    error

	depthRequest string
	input        *domain.ClassificationInput
	clarityCalls int
}

func (f *fakeProvider) Name() string { return "jev" }

func (f *fakeProvider) Source() domain.DecisionSource { return domain.DecisionSourceJev }

func (f *fakeProvider) ContextDepth(_ context.Context, request string) (domain.ContextDepthDecision, error) {
	f.depthRequest = request
	return f.depth, f.depthErr
}

func (f *fakeProvider) Clarity(context.Context, string, string) (domain.ClarityDecision, error) {
	f.clarityCalls++
	return domain.ClarityDecision{PClear: f.depth.PClear}, nil
}

func (f *fakeProvider) Classify(_ context.Context, in domain.ClassificationInput) (domain.TaskClassification, error) {
	f.input = &in
	return f.tc, f.tcErr
}

type fakeContext struct {
	project *domain.ProjectContext
	err     error
	calls   int
	args    [2]string
}

func (f *fakeContext) Build(_ context.Context, request, requestEN string) (*domain.ProjectContext, error) {
	f.calls++
	f.args = [2]string{request, requestEN}
	return f.project, f.err
}

type fakeChooser struct {
	choice  interaction.ManualChoice
	err     error
	calls   int
	cause   error
	project *domain.ProjectContext
	ctx     context.Context
}

func (f *fakeChooser) Choose(ctx context.Context, cause error, project *domain.ProjectContext, _ domain.DecisionPolicy) (interaction.ManualChoice, error) {
	f.calls++
	f.cause, f.project, f.ctx = cause, project, ctx
	return f.choice, f.err
}

func testPolicy() domain.DecisionPolicy {
	th := map[domain.Stage]float64{}
	for _, st := range domain.StageOrder() {
		th[st] = 0.6
	}
	return domain.DecisionPolicy{
		ProjectContextThreshold: 0.5,
		ClarityThreshold:        0.80,
		ComplexityCuts:          domain.ComplexityCuts{Medium: 0.5, Complex: 1.4},
		StageThreshold:          th,
		ForceStages:             map[domain.Complexity][]domain.Stage{domain.ComplexityComplex: {domain.StageInvestigation, domain.StageReview}},
	}
}

func depthDecision(pProject float64) domain.ContextDepthDecision {
	return domain.ContextDepthDecision{
		Probabilities: map[domain.ContextDepth]float64{domain.ContextDepthNone: 1 - pProject, domain.ContextDepthProject: pProject},
		Confidence:    0.8,
		PClear:        0.9,
	}
}

func classificationFor(depth domain.ContextDepth) domain.TaskClassification {
	var stages domain.StageDecisions
	for st, p := range map[domain.Stage]float64{
		domain.StageResearch: 0.1, domain.StageInvestigation: 0.2,
		domain.StageDevelopment: 0.95, domain.StageTesting: 0.9, domain.StageReview: 0.3,
	} {
		stages.Set(st, domain.ProbabilityDecision{Probability: p})
	}
	return domain.TaskClassification{
		ContextDepth: depth,
		Complexity: map[domain.ComplexitySource]domain.ComplexityDecision{
			domain.ComplexitySourceChoice: {Probabilities: map[domain.Complexity]float64{"simple": 0.2, "medium": 0.6, "complex": 0.2}, Confidence: 0.7},
		},
		Stages:   stages,
		Provider: "jev",
		Model:    "jev-1.13.0",
	}
}

type fixture struct {
	svc      *Service
	provider *fakeProvider
	ctxb     *fakeContext
	chooser  *fakeChooser
}

func newFixture(pProject float64) fixture {
	p := &fakeProvider{depth: depthDecision(pProject)}
	if pProject >= 0.5 {
		p.tc = classificationFor(domain.ContextDepthProject)
	} else {
		p.tc = classificationFor(domain.ContextDepthNone)
	}
	cb := &fakeContext{project: &domain.ProjectContext{Language: "go"}}
	ch := &fakeChooser{choice: interaction.ManualChoice{Complexity: domain.ComplexityComplex, Stages: []domain.Stage{domain.StageDevelopment}}}
	return fixture{
		svc: &Service{
			Translator: fakeTranslator{}, Decisions: p, Context: cb, Chooser: ch,
			Policy: testPolicy(), Primary: domain.ComplexitySourceChoice, AskOnFailure: true,
		},
		provider: p, ctxb: cb, chooser: ch,
	}
}

var jevDown = &apperr.DecisionError{Provider: "jev", Kind: apperr.DecisionUnavailable, StatusCode: 529}

func TestClassify_NoneSkipsContextManager(t *testing.T) {
	f := newFixture(0.2)
	res, err := f.svc.Classify(context.Background(), Request{RunID: "r", Text: "What is a mutex?"})
	if err != nil {
		t.Fatal(err)
	}
	if f.ctxb.calls != 0 {
		t.Error("Context Manager called for depth none")
	}
	if f.provider.input.ContextDepth != domain.ContextDepthNone || f.provider.input.Project != nil || res.Project != nil {
		t.Errorf("input = %+v", f.provider.input)
	}
	if res.Decision.Source != domain.DecisionSourceJev || res.Decision.ContextDepth.Selected != domain.ContextDepthNone {
		t.Errorf("decision = %+v", res.Decision)
	}
	if got := res.Decision.Stages.EnabledStages(); len(got) != 2 { // development, testing
		t.Errorf("stages = %v", got)
	}
}

func TestClassify_ProjectPassesContextToProvider(t *testing.T) {
	f := newFixture(0.9)
	f.svc.Translator = fakeTranslator{res: translate.Result{Text: "add a cache to ProductRepository", Translated: true}}
	res, err := f.svc.Classify(context.Background(), Request{RunID: "r", Text: "добавь кэш в ProductRepository"})
	if err != nil {
		t.Fatal(err)
	}
	if f.ctxb.calls != 1 || f.ctxb.args != [2]string{"добавь кэш в ProductRepository", "add a cache to ProductRepository"} {
		t.Errorf("Context Manager calls=%d args=%q", f.ctxb.calls, f.ctxb.args)
	}
	if f.provider.depthRequest != "add a cache to ProductRepository" || f.provider.input.Request != "add a cache to ProductRepository" {
		t.Error("the provider must get the translated text")
	}
	if f.provider.input.Project != f.ctxb.project || res.Project != f.ctxb.project || res.ContextLimited {
		t.Errorf("project not passed through: %+v", f.provider.input)
	}
	if res.Classification == nil || res.Depth == nil {
		t.Error("raw provider answers missing from the result")
	}
}

func TestClassify_UntranslatedRequestGivesNoEnglishText(t *testing.T) {
	f := newFixture(0.9)
	if _, err := f.svc.Classify(context.Background(), Request{Text: "Add a cache"}); err != nil {
		t.Fatal(err)
	}
	if f.ctxb.args[1] != "" {
		t.Errorf("requestEN = %q, want empty when nothing was translated", f.ctxb.args[1])
	}
}

func TestClassify_ContextFailureContinuesLimited(t *testing.T) {
	f := newFixture(0.9)
	f.ctxb.err = errors.New("git exploded")
	res, err := f.svc.Classify(context.Background(), Request{Text: "Fix the bug"})
	if err != nil {
		t.Fatalf("a failed collection must not fail classification: %v", err)
	}
	if !res.ContextLimited || res.ContextError != "git exploded" || res.Project != nil {
		t.Errorf("result = %+v", res)
	}
	if !f.provider.input.ContextLimited || f.provider.input.Project != nil || f.provider.input.ContextDepth != domain.ContextDepthProject {
		t.Errorf("input = %+v", f.provider.input)
	}
}

func TestClassify_ProviderFailureAsksUser(t *testing.T) {
	cases := map[string]func(f fixture){
		"pre-check":      func(f fixture) { f.provider.depthErr = jevDown },
		"classification": func(f fixture) { f.provider.tcErr = jevDown },
	}
	for name, breakIt := range cases {
		t.Run(name, func(t *testing.T) {
			f := newFixture(0.9)
			breakIt(f)
			userCtx := context.WithValue(context.Background(), struct{}{}, "user")
			res, err := f.svc.Classify(context.Background(), Request{Text: "Fix the bug", UserCtx: userCtx})
			if err != nil {
				t.Fatal(err)
			}
			if f.chooser.calls != 1 || f.chooser.cause != jevDown || f.chooser.ctx != userCtx {
				t.Errorf("chooser calls=%d cause=%v", f.chooser.calls, f.chooser.cause)
			}
			if res.Decision.Source != domain.DecisionSourceUser || res.DecisionError != jevDown {
				t.Errorf("decision = %+v", res.Decision)
			}
			// complex forces investigation, review on top of the user's development.
			want := []domain.Stage{domain.StageInvestigation, domain.StageDevelopment, domain.StageReview}
			if got := res.Decision.Stages.EnabledStages(); !equalStages(got, want) {
				t.Errorf("stages = %v, want %v", got, want)
			}
			if name == "classification" && f.chooser.project != f.ctxb.project {
				t.Error("the collected context must be shown to the user")
			}
		})
	}
}

func TestClassify_OnFailureFail(t *testing.T) {
	f := newFixture(0.9)
	f.svc.AskOnFailure = false
	f.provider.tcErr = jevDown
	_, err := f.svc.Classify(context.Background(), Request{Text: "Fix"})
	if err != jevDown || f.chooser.calls != 0 {
		t.Errorf("err = %v, chooser calls = %d; want the provider error, no choice", err, f.chooser.calls)
	}
}

// SPEC §9: a credential the operator must fix is never answered with a manual choice.
func TestClassify_AuthFailureNeverAsks(t *testing.T) {
	authErr := &apperr.DecisionError{Provider: "claude", Kind: apperr.DecisionUnavailable,
		Err: &apperr.ProviderError{Provider: "claude", Kind: apperr.FailureAuth}}
	cases := map[string]func(f fixture){
		"pre-check":      func(f fixture) { f.provider.depthErr = authErr },
		"classification": func(f fixture) { f.provider.tcErr = authErr },
	}
	for name, breakIt := range cases {
		t.Run(name, func(t *testing.T) {
			f := newFixture(0.9)
			breakIt(f)
			_, err := f.svc.Classify(context.Background(), Request{Text: "Fix the bug"})
			var pe *apperr.ProviderError
			if !errors.As(err, &pe) || pe.Kind != apperr.FailureAuth || f.chooser.calls != 0 {
				t.Errorf("err = %v, chooser calls = %d; want the auth error, no choice", err, f.chooser.calls)
			}
		})
	}
}

func TestClassify_CancellationNeverAsks(t *testing.T) {
	f := newFixture(0.9)
	f.provider.depthErr = &apperr.DecisionError{Provider: "jev", Kind: apperr.DecisionCancelled, Err: context.Canceled}
	_, err := f.svc.Classify(context.Background(), Request{Text: "Fix"})
	if !errors.Is(err, context.Canceled) || f.chooser.calls != 0 {
		t.Errorf("err = %v, chooser calls = %d", err, f.chooser.calls)
	}

	f = newFixture(0.9)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	f.provider.tcErr = jevDown
	if _, err := f.svc.Classify(ctx, Request{Text: "Fix"}); err == nil || f.chooser.calls != 0 {
		t.Errorf("a cancelled run must not ask: err = %v, calls = %d", err, f.chooser.calls)
	}
}

func TestClassify_ChooserErrorIsReturned(t *testing.T) {
	f := newFixture(0.9)
	f.provider.tcErr = jevDown
	f.chooser.err = context.Canceled
	if _, err := f.svc.Classify(context.Background(), Request{Text: "Fix"}); !errors.Is(err, context.Canceled) {
		t.Errorf("err = %v", err)
	}
}

func TestClassify_TranslationErrorStops(t *testing.T) {
	f := newFixture(0.9)
	f.svc.Translator = fakeTranslator{err: context.Canceled}
	if _, err := f.svc.Classify(context.Background(), Request{Text: "x"}); !errors.Is(err, context.Canceled) || f.provider.depthRequest != "" {
		t.Errorf("err = %v; the provider must not be called", err)
	}
}

func TestClassify_PrimaryNotAskedIsAnError(t *testing.T) {
	f := newFixture(0.2)
	f.svc.Primary = domain.ComplexitySourceScore
	if _, err := f.svc.Classify(context.Background(), Request{Text: "x"}); err == nil {
		t.Error("expected a wiring error")
	}
}

func equalStages(a, b []domain.Stage) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

func TestResultTrace_ReconstructsTheDecision(t *testing.T) {
	f := newFixture(0.9)
	f.svc.Translator = fakeTranslator{res: translate.Result{Text: "add a cache", Translated: true, Detected: translate.Detection{Language: "ru"}, Provider: "claude", Model: "haiku"}}
	f.provider.tc.Complexity[domain.ComplexitySourceScore] = domain.ComplexityDecision{
		Probabilities: map[domain.Complexity]float64{"simple": 0.05, "medium": 0.25, "complex": 0.7}, Confidence: 0.6, Score: ptrf(1.65),
	}
	res, err := f.svc.Classify(context.Background(), Request{Text: "добавь кэш"})
	if err != nil {
		t.Fatal(err)
	}
	trace := res.Trace(domain.Workflow{Steps: []domain.Step{{Action: domain.ActionInvestigate}, {Action: domain.ActionImplement}}})

	raw, err := json.Marshal(trace)
	if err != nil {
		t.Fatal(err)
	}
	var back domain.DecisionTrace
	if err := json.Unmarshal(raw, &back); err != nil {
		t.Fatal(err)
	}

	if back.Request != "добавь кэш" || back.RequestEN != "add a cache" || !back.Translation.Translated || back.Provider != "jev" || back.Model != "jev-1.13.0" {
		t.Errorf("header = %+v", back)
	}
	if back.ContextDepth == nil || back.ContextDepth.Selected != domain.ContextDepthProject || back.ContextDepth.Probabilities[domain.ContextDepthProject] != 0.9 {
		t.Errorf("context depth = %+v", back.ContextDepth)
	}
	// Why each stage is on or off.
	for _, s := range back.Stages {
		if s.Reason == "" || s.Probability == nil || s.Threshold == nil {
			t.Errorf("stage %s cannot be explained: %+v", s.Stage, s)
		}
	}
	// How choice and score diverged.
	c := back.Complexity
	if c.Agree || c.Sources[domain.ComplexitySourceChoice].Selected != domain.ComplexityMedium ||
		c.Sources[domain.ComplexitySourceScore].Selected != domain.ComplexityComplex ||
		*c.Sources[domain.ComplexitySourceScore].Decision.Score != 1.65 {
		t.Errorf("complexity = %+v", c)
	}
	if len(back.Workflow) != 2 || back.Workflow[1] != domain.ActionImplement {
		t.Errorf("workflow = %v", back.Workflow)
	}
}

func TestResultTrace_ManualChoice(t *testing.T) {
	f := newFixture(0.9)
	f.provider.depthErr = jevDown
	res, err := f.svc.Classify(context.Background(), Request{Text: "fix"})
	if err != nil {
		t.Fatal(err)
	}
	trace := res.Trace(domain.Workflow{})
	if trace.Source != domain.DecisionSourceUser || trace.DecisionError == "" || trace.ContextDepth != nil || trace.Model != "" {
		t.Errorf("trace = %+v", trace)
	}
	if want := domain.PolicyHash(f.svc.Policy); trace.PolicyHash != want {
		t.Errorf("policy hash = %q, want %q (a choice after a provider failure is made under the policy too)", trace.PolicyHash, want)
	}
}

func TestResultTrace_RecordsThePolicyHash(t *testing.T) {
	f := newFixture(0.9)
	res, err := f.svc.Classify(context.Background(), Request{Text: "fix"})
	if err != nil {
		t.Fatal(err)
	}
	want := domain.PolicyHash(f.svc.Policy)
	if got := res.Trace(domain.Workflow{}).PolicyHash; got != want || len(want) != 64 {
		t.Errorf("policy hash = %q, want %q", got, want)
	}
	if got := res.Trace(domain.Workflow{}).QuestionsHash; got != decision.QuestionsHash() {
		t.Errorf("questions hash = %q, want %q", got, decision.QuestionsHash())
	}

	// Another threshold is another hash.
	f.svc.Policy.StageThreshold[domain.StageTesting] += 0.01
	res, err = f.svc.Classify(context.Background(), Request{Text: "fix"})
	if err != nil {
		t.Fatal(err)
	}
	if got := res.Trace(domain.Workflow{}).PolicyHash; got == want {
		t.Errorf("policy hash did not change with a threshold: %q", got)
	}

	// A Result built by hand has no policy, and its trace omits the field.
	if got := (Result{}).Trace(domain.Workflow{}).PolicyHash; got != "" {
		t.Errorf("policy hash of a bare Result = %q", got)
	}
}

func ptrf(v float64) *float64 { return &v }

// The scope question is shadow data (specs/story/SPEC.md §13): the provider's answer reaches
// decision.json as scope.story and nothing else.
func TestResultTrace_RecordsTheScope(t *testing.T) {
	f := newFixture(0.9)
	f.provider.tc.Scope = &domain.ScopeDecision{Story: 0.35}
	res, err := f.svc.Classify(context.Background(), Request{Text: "fix"})
	if err != nil {
		t.Fatal(err)
	}
	if got := res.Trace(domain.Workflow{}).Scope; got == nil || got.Story != 0.35 {
		t.Errorf("trace scope = %+v, want story 0.35", got)
	}

	// Not asked, or the provider failed: nothing is recorded.
	f = newFixture(0.9)
	res, err = f.svc.Classify(context.Background(), Request{Text: "fix"})
	if err != nil {
		t.Fatal(err)
	}
	if got := res.Trace(domain.Workflow{}).Scope; got != nil {
		t.Errorf("trace scope = %+v, want none when the provider gave none", got)
	}
	f = newFixture(0.9)
	f.provider.tcErr = jevDown
	res, err = f.svc.Classify(context.Background(), Request{Text: "fix"})
	if err != nil {
		t.Fatal(err)
	}
	if got := res.Trace(domain.Workflow{}).Scope; got != nil {
		t.Errorf("trace scope after a provider failure = %+v, want none", got)
	}
}

// A story task skips the question, and its decision is still recorded under the same questions
// hash as every other provider-decided run: the hash names the question set, so calibration
// keeps story tasks with the runs whose stage answers they share (specs/story/SPEC.md §13).
func TestClassify_StoryTaskSkipsTheScopeQuestion(t *testing.T) {
	f := newFixture(0.9)
	asked, err := f.svc.Classify(context.Background(), Request{Text: "fix"})
	if err != nil {
		t.Fatal(err)
	}
	if f.provider.input.SkipScope {
		t.Error("an ordinary run must ask the scope question")
	}

	skipped, err := f.svc.Classify(context.Background(), Request{Text: "fix", StoryTask: true})
	if err != nil {
		t.Fatal(err)
	}
	if !f.provider.input.SkipScope {
		t.Error("a story task must skip the scope question")
	}
	if a, s := asked.Trace(domain.Workflow{}).QuestionsHash, skipped.Trace(domain.Workflow{}).QuestionsHash; a != s || s != decision.QuestionsHash() {
		t.Errorf("questions hash asked %q, skipped %q, want both %q", a, s, decision.QuestionsHash())
	}
}

// The policy, the workflow and the routes never read the scope answer: the same stage and
// complexity answers give byte-for-byte the same decision whatever it is. The routes follow
// from the decision alone (config.ActionsConfig.Route takes a domain.Decision), so comparing
// the decision, the workflow built from it and the trace covers them.
func TestScopeAnswerChangesNothingButItself(t *testing.T) {
	type outcome struct{ decision, workflow, trace string }
	run := func(scope float64) outcome {
		f := newFixture(0.9)
		f.provider.tc.Scope = &domain.ScopeDecision{Story: scope}
		res, err := f.svc.Classify(context.Background(), Request{RunID: "r", Text: "add a cache"})
		if err != nil {
			t.Fatal(err)
		}
		res.Decision = policy.Explore(res.Decision, f.svc.Policy, "r")
		wf, err := workflow.Build(res.Decision.Stages)
		if err != nil {
			t.Fatal(err)
		}
		trace := res.Trace(wf)
		if trace.Scope == nil || trace.Scope.Story != scope {
			t.Fatalf("trace scope = %+v, want story %v", trace.Scope, scope)
		}
		trace.Scope = nil // the one field that may differ
		mustJSON := func(v any) string {
			b, err := json.Marshal(v)
			if err != nil {
				t.Fatal(err)
			}
			return string(b)
		}
		return outcome{mustJSON(res.Decision), mustJSON(wf), mustJSON(trace)}
	}
	low, high := run(0.05), run(0.95)
	if low != high {
		t.Errorf("the scope answer changed the outcome:\nscope 0.05: %+v\nscope 0.95: %+v", low, high)
	}
}

// A task the pre-check rates at or above the threshold is classified with no extra provider call
// and no clarify step; the check is recorded.
func TestClassify_RecordsTheClarityCheck(t *testing.T) {
	for _, tt := range []struct {
		pClear float64
		status domain.ClarityStatus
	}{
		{0.95, domain.ClarityClear},
		{0.80, domain.ClarityClear}, // inclusive
		// With the clarify step off the question is recorded and the run goes on whatever it is.
		{0.79, domain.ClarityDisabled},
		{0.05, domain.ClarityDisabled},
	} {
		f := newFixture(0.9)
		f.svc.ClarifyDisabled = tt.status == domain.ClarityDisabled
		f.provider.depth.PClear = tt.pClear
		res, err := f.svc.Classify(context.Background(), Request{Text: "fix"})
		if err != nil {
			t.Fatal(err)
		}
		got := res.Trace(domain.Workflow{}).Clarity
		if got == nil || len(got.Checks) != 1 || got.Checks[0] != tt.pClear || got.Threshold != 0.80 || got.Status != tt.status {
			t.Errorf("P(clear) %v: trace clarity = %+v, want one check and status %s", tt.pClear, got, tt.status)
		}
		if err := got.Validate(); err != nil {
			t.Errorf("P(clear) %v: %v", tt.pClear, err)
		}
		if f.provider.clarityCalls != 0 {
			t.Errorf("P(clear) %v: %d re-checks, want none", tt.pClear, f.provider.clarityCalls)
		}
		if res.Decision.Source != domain.DecisionSourceJev || len(res.Decision.Stages.EnabledStages()) == 0 {
			t.Errorf("P(clear) %v: the answer must decide nothing: %+v", tt.pClear, res.Decision)
		}
	}
}

func TestClassify_ClarityIsAbsentWithoutAPreCheckAnswerAndForAStoryTask(t *testing.T) {
	f := newFixture(0.9)
	f.provider.depthErr = jevDown
	res, err := f.svc.Classify(context.Background(), Request{Text: "fix"})
	if err != nil {
		t.Fatal(err)
	}
	if got := res.Trace(domain.Workflow{}).Clarity; got != nil {
		t.Errorf("clarity after a failed pre-check = %+v, want none", got)
	}

	f = newFixture(0.9)
	res, err = f.svc.Classify(context.Background(), Request{Text: "fix", StoryTask: true})
	if err != nil {
		t.Fatal(err)
	}
	if got := res.Trace(domain.Workflow{}).Clarity; got != nil {
		t.Errorf("clarity of a story task = %+v, want none", got)
	}
}

// Each decision round's usage reaches the trace (specs/benchmark/SPEC.md §11.1), also the
// usage of a provider call that failed before the manual choice.
func TestClassify_RecordsUsage(t *testing.T) {
	tr, depth, tc := &domain.Usage{CostUSD: 0.01}, &domain.Usage{CostUSD: 0.02}, &domain.Usage{CostUSD: 0.03}

	f := newFixture(0.9)
	f.svc.Translator = fakeTranslator{res: translate.Result{Usage: tr}}
	f.provider.depth.Usage = depth
	f.provider.tc.Usage = tc
	res, err := f.svc.Classify(context.Background(), Request{Text: "Fix the bug"})
	if err != nil {
		t.Fatal(err)
	}
	want := domain.DecisionUsage{Translation: tr, ContextDepth: depth, Classification: tc}
	if trace := res.Trace(domain.Workflow{}); trace.Usage == nil || !reflect.DeepEqual(*trace.Usage, want) {
		t.Errorf("trace usage = %+v, want %+v", trace.Usage, want)
	}

	f = newFixture(0.9)
	f.provider.tcErr = &apperr.DecisionError{Provider: "claude", Kind: apperr.DecisionInvalidResponse, Usage: tc}
	res, err = f.svc.Classify(context.Background(), Request{Text: "Fix the bug"})
	if err != nil || res.Decision.Source != domain.DecisionSourceUser {
		t.Fatalf("Classify = %+v, %v", res.Decision, err)
	}
	if trace := res.Trace(domain.Workflow{}); trace.Usage == nil || trace.Usage.Classification != tc {
		t.Errorf("a failed classification's usage = %+v", trace.Usage)
	}

	f = newFixture(0.1)
	res, err = f.svc.Classify(context.Background(), Request{Text: "Fix the bug"})
	if err != nil {
		t.Fatal(err)
	}
	if trace := res.Trace(domain.Workflow{}); trace.Usage != nil {
		t.Errorf("no round reported usage, trace usage = %+v", trace.Usage)
	}
}

package classification

import (
	"context"
	"encoding/json"
	"errors"
	"testing"

	"orchestrator/internal/apperr"
	"orchestrator/internal/domain"
	"orchestrator/internal/interaction"
	"orchestrator/internal/translate"
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
}

func (f *fakeProvider) Name() string { return "jev" }

func (f *fakeProvider) ContextDepth(_ context.Context, request string) (domain.ContextDepthDecision, error) {
	f.depthRequest = request
	return f.depth, f.depthErr
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
		ComplexityThreshold:     map[domain.Complexity]float64{domain.ComplexityComplex: 0.4, domain.ComplexityMedium: 0.5},
		StageThreshold:          th,
		ForceStages:             map[domain.Complexity][]domain.Stage{domain.ComplexityComplex: {domain.StageAnalysis, domain.StagePlanning, domain.StageReview}},
	}
}

func depthDecision(pProject float64) domain.ContextDepthDecision {
	return domain.ContextDepthDecision{
		Probabilities: map[domain.ContextDepth]float64{domain.ContextDepthNone: 1 - pProject, domain.ContextDepthProject: pProject},
		Confidence:    0.8,
	}
}

func classificationFor(depth domain.ContextDepth) domain.TaskClassification {
	var stages domain.StageDecisions
	for st, p := range map[domain.Stage]float64{
		domain.StageResearch: 0.1, domain.StageAnalysis: 0.9, domain.StagePlanning: 0.2,
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
	if got := res.Decision.Stages.EnabledStages(); len(got) != 3 { // analysis, development, testing
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
			// complex forces analysis, planning, review on top of the user's development.
			want := []domain.Stage{domain.StageAnalysis, domain.StagePlanning, domain.StageDevelopment, domain.StageReview}
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
	trace := res.Trace(domain.Workflow{Steps: []domain.Step{{Action: domain.ActionAnalyze}, {Action: domain.ActionImplement}}})

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
}

func ptrf(v float64) *float64 { return &v }

package classification

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/kasorse/orch/internal/action"
	"github.com/kasorse/orch/internal/apperr"
	"github.com/kasorse/orch/internal/domain"
	"github.com/kasorse/orch/internal/translate"
)

// The clarify loop with fakes for the decision provider, the clarify call and the operator.

type fakeDocs struct {
	ins  domain.Instructions
	err  error
	args [2]string
}

func (f *fakeDocs) Docs(_ context.Context, request, requestEN string) (domain.Instructions, error) {
	f.args = [2]string{request, requestEN}
	return f.ins, f.err
}

type fakeClarifyCall struct {
	answer action.ClarifyAnswer
	usage  *domain.Usage
	err    error
	calls  []action.ClarifyPromptInput
}

func (f *fakeClarifyCall) Clarify(_ context.Context, _ string, n int, in action.ClarifyPromptInput) (ClarifyReply, error) {
	f.calls = append(f.calls, in)
	return ClarifyReply{Answer: f.answer, Usage: f.usage}, f.err
}

// fakeOperator answers the first point it is shown that has no answer with answer (when set),
// leaves the rest as the model had them, and adds the texts in additions one per call.
type fakeOperator struct {
	answer      string
	additions   []string
	answerCalls int
	addCalls    int
	ctxs        []context.Context
	open        [][]domain.ClarifyPoint
}

func (f *fakeOperator) Answer(ctx context.Context, _, _ float64, points []domain.ClarifyPoint) ([]domain.ClarifyPoint, error) {
	f.answerCalls++
	f.ctxs = append(f.ctxs, ctx)
	out := append([]domain.ClarifyPoint(nil), points...)
	if f.answer != "" {
		for i := range out {
			if out[i].By == "" {
				out[i].Answer, out[i].By = f.answer, domain.ClarifyByOperator
				break
			}
		}
	}
	return out, nil
}

func (f *fakeOperator) Add(ctx context.Context, _, _ float64, open []domain.ClarifyPoint) (string, error) {
	f.addCalls++
	f.ctxs = append(f.ctxs, ctx)
	f.open = append(f.open, open)
	if f.addCalls > len(f.additions) {
		return "", nil
	}
	return f.additions[f.addCalls-1], nil
}

// rechecks answers the N-th clarity re-check with p[N-1] (the last one repeats), and records what
// it was asked.
type rechecks struct {
	*fakeProvider
	p     []float64
	err   error
	calls [][2]string
}

func (r *rechecks) Clarity(_ context.Context, request, clarification string) (domain.ClarityDecision, error) {
	r.calls = append(r.calls, [2]string{request, clarification})
	usage := &domain.Usage{InputTokens: 7}
	if r.err != nil {
		return domain.ClarityDecision{}, &apperr.DecisionError{Provider: "jev", Kind: apperr.DecisionUnavailable, Usage: usage}
	}
	i := min(len(r.calls), len(r.p)) - 1
	return domain.ClarityDecision{PClear: r.p[i], Usage: usage}, nil
}

type clarifyFixture struct {
	fixture
	rechecks *rechecks
	docs     *fakeDocs
	call     *fakeClarifyCall
	operator *fakeOperator
}

var threePoints = action.ClarifyAnswer{Points: []action.ClarifyAnswerPoint{
	{Unclear: "format", Question: "Which format?", Answer: "CSV", Basis: action.ClarifyBasisDocs, Source: "docs/export.md#Formats"},
	{Unclear: "users", Question: "Which users?", Answer: "admins only", Basis: action.ClarifyBasisAssumption},
	{Unclear: "locale", Question: "Which locale?", Basis: action.ClarifyBasisNone},
}}

// newClarifyFixture is a service whose pre-check rates the task pre and whose re-checks answer
// checks in turn, at a terminal when interactive.
func newClarifyFixture(pre float64, interactive bool, maxRounds int, checks ...float64) clarifyFixture {
	f := newFixture(0.9)
	f.provider.depth.PClear = pre
	r := &rechecks{fakeProvider: f.provider, p: checks}
	cf := clarifyFixture{
		fixture:  f,
		rechecks: r,
		docs:     &fakeDocs{ins: domain.Instructions{Rules: []string{"keep it small"}}},
		call:     &fakeClarifyCall{answer: threePoints, usage: &domain.Usage{InputTokens: 3}},
		operator: &fakeOperator{},
	}
	f.svc.Decisions = r
	f.svc.Clarify = &ClarifyLoop{
		Docs: cf.docs, Caller: cf.call, Decisions: r, Policy: testPolicy(), Interactive: interactive, MaxRounds: maxRounds,
	}
	if interactive {
		f.svc.Clarify.Operator = cf.operator
	}
	return cf
}

func pointOf(c *domain.Clarification, question string) domain.ClarifyPoint {
	for _, r := range c.Rounds {
		for _, p := range r.Points {
			if p.Question == question {
				return p
			}
		}
	}
	return domain.ClarifyPoint{}
}

func TestClassify_ClearEnoughMakesNoClarifyCall(t *testing.T) {
	f := newClarifyFixture(0.80, true, 2, 0.9)
	res, err := f.svc.Classify(context.Background(), Request{Text: "fix"})
	if err != nil {
		t.Fatal(err)
	}
	if len(f.call.calls) != 0 || len(f.rechecks.calls) != 0 || f.docs.args != ([2]string{}) {
		t.Errorf("clarify made: %d calls, %d re-checks, docs %v; want none at the threshold", len(f.call.calls), len(f.rechecks.calls), f.docs.args)
	}
	if res.Clarification != nil {
		t.Errorf("clarification = %+v, want none", res.Clarification)
	}
}

func TestClassify_InteractiveRecordsTheOperatorsAnswerAndRechecksWithIt(t *testing.T) {
	f := newClarifyFixture(0.42, true, 2, 0.9)
	f.operator.answer = "ISO dates"
	userCtx := context.WithValue(context.Background(), struct{}{}, "user")

	res, err := f.svc.Classify(context.Background(), Request{RunID: "r", Text: "add an export", UserCtx: userCtx})
	if err != nil {
		t.Fatal(err)
	}
	c := res.Clarification
	if c == nil || c.Status != domain.ClarityClear || len(c.Checks) != 2 || c.Checks[0] != 0.42 || c.Checks[1] != 0.9 {
		t.Fatalf("clarification = %+v, want clear with checks [0.42 0.9]", c)
	}
	if err := c.Validate(); err != nil {
		t.Errorf("the recorded clarification is invalid: %v", err)
	}
	if len(c.Rounds) != 1 || c.Rounds[0].Mode != domain.ClarifyInteractive {
		t.Errorf("rounds = %+v, want one interactive clarify round", c.Rounds)
	}
	if p := pointOf(c, "Which format?"); p.By != domain.ClarifyByDocs || p.Answer != "CSV" || p.Source != "docs/export.md#Formats" {
		t.Errorf("docs point = %+v", p)
	}
	// An assumption has no place in a conversation: the model proposes only what docs settle.
	if p := pointOf(c, "Which users?"); p.By != domain.ClarifyByOperator || p.Answer != "ISO dates" {
		t.Errorf("operator point = %+v, want it answered by the operator", p)
	}

	if len(f.rechecks.calls) != 1 || f.rechecks.calls[0][0] != "add an export" || !strings.Contains(f.rechecks.calls[0][1], "ISO dates  [operator]") {
		t.Errorf("re-check asked %q, want the request and a clarification holding the operator's answer", f.rechecks.calls)
	}
	// The operator waits on the user's context, not the run's.
	if len(f.operator.ctxs) != 1 || f.operator.ctxs[0] != userCtx {
		t.Error("the operator must be asked on UserCtx")
	}
	if f.call.calls[0].Mode != domain.ClarifyInteractive || f.call.calls[0].Task != "add an export" || f.call.calls[0].Clarification != "" {
		t.Errorf("clarify call = %+v", f.call.calls[0])
	}

	// The decision and the Context Manager see the request and what was agreed.
	rendered := c.Render()
	if want := "add an export\n\n" + rendered; f.provider.input.Request != want || f.ctxb.args[0] != want {
		t.Errorf("decision text %q, context query %q; want the request plus the rendered clarification", f.provider.input.Request, f.ctxb.args[0])
	}
	if res.Usage.Clarify[0].InputTokens != 3 || len(res.Usage.ClarityRecheck) != 1 {
		t.Errorf("usage = %+v, want the clarify call and one re-check", res.Usage)
	}
	if got := res.Trace(domain.Workflow{}); got.Clarity == nil || got.Clarity.Status != domain.ClarityClear || len(got.Clarity.Checks) != 2 ||
		got.Usage == nil || len(got.Usage.Clarify) != 1 {
		t.Errorf("trace = clarity %+v usage %+v", got.Clarity, got.Usage)
	}
}

func TestClassify_AutoTakesDocsAndAssumptionsAndReadsNoOneElse(t *testing.T) {
	f := newClarifyFixture(0.42, false, 2, 0.9)

	res, err := f.svc.Classify(context.Background(), Request{Text: "add an export"})
	if err != nil {
		t.Fatal(err)
	}
	c := res.Clarification
	if c == nil || c.Status != domain.ClarityClear || c.Rounds[0].Mode != domain.ClarifyAuto {
		t.Fatalf("clarification = %+v, want clear, from an auto round", c)
	}
	if err := c.Validate(); err != nil {
		t.Errorf("the recorded clarification is invalid: %v", err)
	}
	if p := pointOf(c, "Which format?"); p.By != domain.ClarifyByDocs || p.Answer != "CSV" {
		t.Errorf("docs point = %+v", p)
	}
	if p := pointOf(c, "Which users?"); p.By != domain.ClarifyByAssumption || p.Answer != "admins only" {
		t.Errorf("assumption point = %+v", p)
	}
	if p := pointOf(c, "Which locale?"); p.By != "" || p.Answer != "" {
		t.Errorf("point with basis none = %+v, want it open", p)
	}
	if f.operator.answerCalls != 0 || f.operator.addCalls != 0 {
		t.Error("auto mode must read no operator input")
	}
	if f.call.calls[0].Mode != domain.ClarifyAuto {
		t.Errorf("clarify mode = %q", f.call.calls[0].Mode)
	}
}

func TestClassify_RoundsSpentWithAnOperatorStopsWithTaskNeedsInput(t *testing.T) {
	f := newClarifyFixture(0.42, true, 1, 0.5, 0.6)
	f.operator.additions = []string{"the dates are ISO 8601"}

	res, err := f.svc.Classify(context.Background(), Request{Text: "add an export"})
	var needs *NeedsInputError
	if !errors.As(err, &needs) {
		t.Fatalf("err = %v, want *NeedsInputError", err)
	}
	c := res.Clarification
	if c == nil || needs.Clarification != c || c.Status != domain.ClarityUnclear || len(c.Checks) != 3 || c.Checks[2] != 0.6 {
		t.Fatalf("clarification = %+v, want unclear with three checks", c)
	}
	if len(c.Rounds) != 2 || c.Rounds[1].Kind != domain.ClarifyRoundAddition || c.Rounds[1].Text != "the dates are ISO 8601" {
		t.Errorf("rounds = %+v, want the clarify round and one addition", c.Rounds)
	}
	if err := c.Validate(); err != nil {
		t.Errorf("the recorded clarification is invalid: %v", err)
	}
	if f.operator.addCalls != 1 || len(f.rechecks.calls) != 2 {
		t.Errorf("%d additions asked, %d re-checks; want 1 and 2 with max_rounds 1", f.operator.addCalls, len(f.rechecks.calls))
	}
	// The addition is part of what the second re-check sees, and the open points are what is shown.
	if !strings.Contains(f.rechecks.calls[1][1], "the dates are ISO 8601") {
		t.Errorf("second re-check clarification:\n%s", f.rechecks.calls[1][1])
	}
	if len(f.operator.open) != 1 || len(f.operator.open[0]) != 2 {
		t.Errorf("open points shown = %+v, want the two the model left open or the operator did not answer", f.operator.open)
	}
	if f.provider.input != nil || res.Decision.Source != "" {
		t.Error("a task that needs input is not classified")
	}
	if text := c.NeedsInputText(); !strings.Contains(text, "0.60 < 0.80") || !strings.Contains(text, "Which locale?") {
		t.Errorf("needs-input text:\n%s", text)
	}
}

func TestClassify_AutoStaysUnclearStopsWithoutAskingAnyone(t *testing.T) {
	f := newClarifyFixture(0.42, false, 2, 0.5)

	res, err := f.svc.Classify(context.Background(), Request{Text: "add an export"})
	var needs *NeedsInputError
	if !errors.As(err, &needs) {
		t.Fatalf("err = %v, want *NeedsInputError", err)
	}
	if c := res.Clarification; c == nil || c.Status != domain.ClarityUnclear || len(c.Checks) != 2 || len(c.Rounds) != 1 {
		t.Errorf("clarification = %+v, want unclear after the one round", c)
	}
	if len(f.rechecks.calls) != 1 || f.operator.addCalls != 0 {
		t.Errorf("%d re-checks, %d additions; want 1 and 0 in auto mode", len(f.rechecks.calls), f.operator.addCalls)
	}
	if res.Clarity == nil || res.Clarity.Status != domain.ClarityUnclear || len(res.Clarity.Checks) != 2 {
		t.Errorf("clarity trace = %+v", res.Clarity)
	}
}

func TestClassify_AnEmptyAdditionStopsTheRun(t *testing.T) {
	f := newClarifyFixture(0.42, true, 3, 0.5)
	f.operator.additions = []string{"  \n"}

	_, err := f.svc.Classify(context.Background(), Request{Text: "add an export"})
	var needs *NeedsInputError
	if !errors.As(err, &needs) {
		t.Fatalf("err = %v, want *NeedsInputError", err)
	}
	if len(needs.Clarification.Rounds) != 1 {
		t.Errorf("rounds = %+v, want the empty addition left out", needs.Clarification.Rounds)
	}
}

func TestClassify_FailedClarifyCallFailsTheRunWithoutAManualChoice(t *testing.T) {
	f := newClarifyFixture(0.42, true, 2, 0.9)
	provErr := &apperr.ProviderError{Provider: "claude", Action: "clarify", Kind: apperr.FailureExit}
	f.call.err = provErr

	res, err := f.svc.Classify(context.Background(), Request{Text: "add an export"})
	if !errors.Is(err, provErr) {
		t.Fatalf("err = %v, want the provider error", err)
	}
	if f.chooser.calls != 0 || f.provider.input != nil {
		t.Error("an unclear task never goes on unclarified, with or without a manual choice")
	}
	if len(res.Usage.Clarify) != 1 {
		t.Errorf("usage = %+v, want the failed call's", res.Usage)
	}
}

func TestClassify_AuthFailureOfTheClarifyCallFailsTheRun(t *testing.T) {
	f := newClarifyFixture(0.42, false, 2, 0.9)
	authErr := &apperr.ProviderError{Provider: "claude", Action: "clarify", Kind: apperr.FailureAuth}
	f.call.err = authErr

	if _, err := f.svc.Classify(context.Background(), Request{Text: "add an export"}); !errors.Is(err, authErr) {
		t.Fatalf("err = %v, want the auth failure", err)
	}
}

func TestClassify_FailedRecheckTakesTheProviderFailurePath(t *testing.T) {
	f := newClarifyFixture(0.42, false, 2, 0.9)
	f.rechecks.err = jevDown

	res, err := f.svc.Classify(context.Background(), Request{Text: "add an export"})
	if err != nil {
		t.Fatal(err)
	}
	if f.chooser.calls != 1 || res.Decision.Source != domain.DecisionSourceUser || res.DecisionError == nil {
		t.Errorf("chooser calls %d, source %q; want the manual choice after the failed re-check", f.chooser.calls, res.Decision.Source)
	}
	if res.Clarification != nil {
		t.Errorf("clarification = %+v, want none recorded without its check", res.Clarification)
	}
	if len(res.Usage.ClarityRecheck) != 1 {
		t.Errorf("usage = %+v, want the failed re-check's", res.Usage)
	}
}

func TestClassify_ClarifyCallIsGivenTheInstructionsOfTheRequest(t *testing.T) {
	f := newClarifyFixture(0.42, false, 2, 0.9)
	f.svc.Translator = fakeTranslator{res: translate.Result{Text: "add an export", Translated: true}}

	if _, err := f.svc.Classify(context.Background(), Request{Text: "добавь экспорт"}); err != nil {
		t.Fatal(err)
	}
	if f.docs.args != [2]string{"добавь экспорт", "add an export"} {
		t.Errorf("docs got %q, want the request and its translation", f.docs.args)
	}
	if got := f.call.calls[0]; got.Task != "добавь экспорт" || len(got.Instructions.Rules) != 1 {
		t.Errorf("clarify call = %+v", got)
	}
	// The re-check is asked about the text the decision provider saw before.
	if f.rechecks.calls[0][0] != "add an export" {
		t.Errorf("re-check request = %q", f.rechecks.calls[0][0])
	}
}

type countingTranslator struct{ texts []string }

func (c *countingTranslator) Prepare(_ context.Context, _, request string) (translate.Result, error) {
	c.texts = append(c.texts, request)
	return translate.Result{Original: request, Text: request, Usage: &domain.Usage{InputTokens: int64(len(c.texts))}}, nil
}

// A second translation is made only when a clarification was added.
func TestClassify_TranslatesAgainOnlyAfterAClarification(t *testing.T) {
	f := newClarifyFixture(0.42, false, 2, 0.9)
	tr := &countingTranslator{}
	f.svc.Translator = tr
	res, err := f.svc.Classify(context.Background(), Request{Text: "add an export"})
	if err != nil {
		t.Fatal(err)
	}
	if len(tr.texts) != 2 || tr.texts[1] != "add an export\n\n"+res.Clarification.Render() {
		t.Errorf("translated %q, want the request and then the request with its clarification", tr.texts)
	}
	if got := res.Trace(domain.Workflow{}); got.Request != "add an export" {
		t.Errorf("trace request = %q, want the request as typed", got.Request)
	}
	// Each translation is a call of its own, counted as one in decision_overhead.
	if u := res.Usage; u.Translation == nil || u.Translation.InputTokens != 1 ||
		u.TranslationClarified == nil || u.TranslationClarified.InputTokens != 2 {
		t.Errorf("usage = translation %+v, translation_clarified %+v; want the first and the second call apart",
			u.Translation, u.TranslationClarified)
	}
	translations := domain.DecisionUsage{Translation: res.Usage.Translation, TranslationClarified: res.Usage.TranslationClarified}
	if n := len(translations.Calls()); n != 2 {
		t.Errorf("the two translations count as %d calls, want 2", n)
	}

	// Nothing to render: no point and no addition, so nothing to translate again.
	f = newClarifyFixture(0.42, false, 2, 0.9)
	f.call.answer = action.ClarifyAnswer{}
	tr = &countingTranslator{}
	f.svc.Translator = tr
	if _, err := f.svc.Classify(context.Background(), Request{Text: "add an export"}); err != nil {
		t.Fatal(err)
	}
	if len(tr.texts) != 1 {
		t.Errorf("translated %q, want the request once", tr.texts)
	}
	if f.provider.input.Request != "add an export" {
		t.Errorf("decision text = %q, want the request alone", f.provider.input.Request)
	}
}

func TestClassify_StoryTaskAsksNoClarityQuestion(t *testing.T) {
	f := newClarifyFixture(0.1, true, 2, 0.9)
	if _, err := f.svc.Classify(context.Background(), Request{Text: "fix", StoryTask: true}); err != nil {
		t.Fatal(err)
	}
	if len(f.call.calls) != 0 {
		t.Error("a story task has its plan in the task file: no clarify step")
	}
}

func TestClassify_UnclearWithoutAClarifyStepIsAWiringError(t *testing.T) {
	f := newFixture(0.9)
	f.provider.depth.PClear = 0.1
	if _, err := f.svc.Classify(context.Background(), Request{Text: "fix"}); err == nil {
		t.Error("an unclear task with no clarify step and no ClarifyDisabled must not go on")
	}
}

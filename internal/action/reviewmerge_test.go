package action

import (
	"context"
	"errors"
	"reflect"
	"testing"

	"github.com/kasorse/orch/internal/apperr"
	"github.com/kasorse/orch/internal/config"
	"github.com/kasorse/orch/internal/domain"
	"github.com/kasorse/orch/internal/provider"
	"github.com/kasorse/orch/internal/workflow"
)

func answer(model string, issues []domain.ReviewIssue, criteria ...domain.CriterionVerdict) domain.ReviewerAnswer {
	return domain.ReviewerAnswer{Model: model, Result: domain.ReviewResult{
		Status: domain.ReviewPass, Summary: model + " summary", SummaryShort: model + " short", Issues: issues, Criteria: criteria,
	}}
}

func issue(sev domain.Severity, file, desc string) domain.ReviewIssue {
	return domain.ReviewIssue{Severity: sev, File: file, Description: desc}
}

func TestMergeReviews(t *testing.T) {
	high := issue(domain.SeverityHigh, "a.go", "Nil pointer when the store is empty")
	for name, tc := range map[string]struct {
		answers    []domain.ReviewerAnswer
		wantIssues []domain.ReviewIssue
		wantStatus domain.ReviewStatus
	}{
		"an issue only the second reviewer found": {
			answers:    []domain.ReviewerAnswer{answer("sonnet", nil), answer("opus", []domain.ReviewIssue{high})},
			wantIssues: []domain.ReviewIssue{high},
			wantStatus: domain.ReviewFail,
		},
		"the same issue from both appears once": {
			answers: []domain.ReviewerAnswer{
				answer("sonnet", []domain.ReviewIssue{high}),
				answer("opus", []domain.ReviewIssue{issue(domain.SeverityHigh, "a.go", "the store is empty: nil pointer WHEN?")}),
			},
			wantIssues: []domain.ReviewIssue{high},
			wantStatus: domain.ReviewFail,
		},
		"another severity or file is another issue": {
			answers: []domain.ReviewerAnswer{
				answer("sonnet", []domain.ReviewIssue{high}),
				answer("opus", []domain.ReviewIssue{
					issue(domain.SeverityMedium, "a.go", high.Description),
					issue(domain.SeverityHigh, "b.go", high.Description),
				}),
			},
			wantIssues: []domain.ReviewIssue{high,
				issue(domain.SeverityMedium, "a.go", high.Description), issue(domain.SeverityHigh, "b.go", high.Description)},
			wantStatus: domain.ReviewFail,
		},
		"different words are a different issue": {
			answers: []domain.ReviewerAnswer{
				answer("sonnet", []domain.ReviewIssue{high}),
				answer("opus", []domain.ReviewIssue{issue(domain.SeverityHigh, "a.go", "Nil pointer when the store is closed")}),
			},
			wantIssues: []domain.ReviewIssue{high, issue(domain.SeverityHigh, "a.go", "Nil pointer when the store is closed")},
			wantStatus: domain.ReviewFail,
		},
		"low issues pass": {
			answers: []domain.ReviewerAnswer{
				answer("sonnet", nil),
				answer("opus", []domain.ReviewIssue{issue(domain.SeverityLow, "a.go", "naming")}),
			},
			wantIssues: []domain.ReviewIssue{issue(domain.SeverityLow, "a.go", "naming")},
			wantStatus: domain.ReviewPass,
		},
	} {
		t.Run(name, func(t *testing.T) {
			got := mergeReviews(tc.answers)
			if !reflect.DeepEqual(got.Issues, tc.wantIssues) {
				t.Errorf("issues = %+v, want %+v", got.Issues, tc.wantIssues)
			}
			if got.Decide() != tc.wantStatus || got.Status != tc.wantStatus {
				t.Errorf("status = %s, Decide = %s, want %s", got.Status, got.Decide(), tc.wantStatus)
			}
			if got.Summary != "sonnet summary" || got.SummaryShort != "sonnet short" {
				t.Errorf("summary = %q / %q, want the base reviewer's", got.Summary, got.SummaryShort)
			}
			if len(got.Reviewers) != len(tc.answers) || got.Reviewers[0].Model != "sonnet" {
				t.Errorf("reviewers = %+v", got.Reviewers)
			}
			if err := got.Validate(); err != nil {
				t.Errorf("the merged result is invalid: %v", err)
			}
		})
	}
}

func TestMergeReviews_CriteriaAnd(t *testing.T) {
	v := func(i int, met bool, evidence string) domain.CriterionVerdict {
		return domain.CriterionVerdict{Index: i, Criterion: "criterion " + string(rune('0'+i)), Met: met, Evidence: evidence}
	}
	got := mergeReviews([]domain.ReviewerAnswer{
		answer("a", nil, v(1, true, "a1"), v(2, false, "a2"), v(3, true, "a3")),
		answer("b", nil, v(1, true, "b1"), v(2, false, "b2"), v(3, false, "b3")),
		answer("c", nil, v(1, false, "c1"), v(2, true, "c2"), v(3, false, "c3")),
	})
	want := []domain.CriterionVerdict{v(1, false, "c1"), v(2, false, "a2"), v(3, false, "b3")}
	if !reflect.DeepEqual(got.Criteria, want) {
		t.Errorf("criteria = %+v, want %+v", got.Criteria, want)
	}
	if got.Decide() != domain.ReviewFail {
		t.Errorf("decide = %s, want fail: an unmet criterion fails the review", got.Decide())
	}

	met := mergeReviews([]domain.ReviewerAnswer{
		answer("a", nil, v(1, true, "a1")), answer("b", nil, v(1, true, "b1")),
	})
	if len(met.Criteria) != 1 || !met.Criteria[0].Met || met.Criteria[0].Evidence != "a1" || met.Decide() != domain.ReviewPass {
		t.Errorf("all met: %+v", met)
	}
}

// seqClient answers each call with the next output and records the model it was asked.
type seqClient struct {
	outputs []string
	usages  []*domain.Usage
	models  []string
	prompts []string
}

func (c *seqClient) Supports(domain.Authority) bool { return true }

func (c *seqClient) Execute(_ context.Context, req provider.Request) (provider.Result, error) {
	i := len(c.models)
	c.models = append(c.models, req.Model)
	c.prompts = append(c.prompts, req.Prompt)
	if i >= len(c.outputs) {
		return provider.Result{}, errors.New("seqClient: no more answers")
	}
	res := provider.Result{Output: c.outputs[i], Call: &domain.CallReport{APIMS: int64(100 * (i + 1))}}
	if i < len(c.usages) {
		res.Usage = c.usages[i]
	}
	return res, nil
}

func reviewWith(t *testing.T, client provider.Client, models ...string) (*Dispatcher, *fakeArtifactWriter) {
	t.Helper()
	repo, dir := guardRepo(t)
	put(t, dir, "main.go", "package main\n\nfunc main() {}\n")
	registry := provider.NewRegistry()
	registry.Register(provider.Claude, client)
	cfg := testConfig()
	cfg.Actions.Review = config.ActionConfig{ProviderModel: config.ProviderModel{Provider: "claude", Model: "sonnet", Effort: "high"}, Models: models}
	d := New(cfg, registry, repo, nil)
	writer := &fakeArtifactWriter{}
	d.Artifacts = writer
	return d, writer
}

const (
	reviewClean = `{"status":"pass","summary":"base summary","summary_short":"base short","issues":[]}`
	reviewHigh  = `{"status":"fail","summary":"second summary","issues":[{"severity":"high","file":"main.go","description":"unchecked error"}]}`
)

func TestExecuteReview_ExtraReviewers(t *testing.T) {
	client := &seqClient{
		outputs: []string{reviewClean, reviewHigh},
		usages: []*domain.Usage{
			{InputTokens: 10, OutputTokens: 5, CostUSD: 0.01, Turns: 1},
			{InputTokens: 20, CacheReadTokens: 3, OutputTokens: 7, CostUSD: 0.02, Turns: 2},
		},
	}
	d, writer := reviewWith(t, client, "opus")

	outcome, err := d.Execute(context.Background(), domain.ActionReview, &domain.Run{ID: "r"})
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(client.models, []string{"sonnet", "opus"}) {
		t.Errorf("models asked = %v, want the base model then the extra one", client.models)
	}
	if client.prompts[0] != client.prompts[1] {
		t.Error("the reviewers were not given the same prompt")
	}
	if outcome.Outcome != workflow.OutcomeFailure {
		t.Errorf("outcome = %v, want failure: the second reviewer's high issue decides", outcome.Outcome)
	}
	r := outcome.ReviewResult
	if len(r.Issues) != 1 || r.Issues[0].Description != "unchecked error" || r.Summary != "base summary" || r.SummaryShort != "base short" {
		t.Errorf("merged result = %+v", r)
	}
	if len(r.Reviewers) != 2 || r.Reviewers[0].Model != "sonnet" || r.Reviewers[1].Model != "opus" || len(r.Reviewers[1].Result.Issues) != 1 {
		t.Errorf("reviewers = %+v", r.Reviewers)
	}
	if len(d.pendingReviewIssues) != 1 {
		t.Errorf("pending issues = %+v", d.pendingReviewIssues)
	}
	if outcome.Provider.Model != "sonnet" {
		t.Errorf("provider model = %q, want the base model", outcome.Provider.Model)
	}

	u := outcome.Usage
	if u == nil || u.InputTokens != 30 || u.CacheReadTokens != 3 || u.OutputTokens != 12 || u.Turns != 3 || u.CostUSD < 0.0299 || u.CostUSD > 0.0301 {
		t.Errorf("usage = %+v, want the sum of both calls", u)
	}
	want := []domain.ExtraCall{{Model: "opus", APIMS: 200, InputTokens: 23, OutputTokens: 7}}
	if outcome.Call == nil || outcome.Call.APIMS != 100 || !reflect.DeepEqual(outcome.Call.ExtraCalls, want) {
		t.Errorf("call = %+v, want the first call's report with extra_calls %+v", outcome.Call, want)
	}

	if len(writer.calls) != 1 {
		t.Fatalf("%d artifacts saved, want 1", len(writer.calls))
	}
	saved, ok := writer.calls[0].artifact.(domain.ReviewResult)
	if !ok || !reflect.DeepEqual(saved, *r) {
		t.Errorf("the artifact is %+v, want the merged result with its reviewers", writer.calls[0].artifact)
	}
}

func TestExecuteReview_NoExtraReviewersIsUnchanged(t *testing.T) {
	client := &seqClient{outputs: []string{reviewClean}}
	d, _ := reviewWith(t, client)
	outcome, err := d.Execute(context.Background(), domain.ActionReview, &domain.Run{ID: "r"})
	if err != nil {
		t.Fatal(err)
	}
	if len(client.models) != 1 || outcome.Outcome != workflow.OutcomeSuccess {
		t.Errorf("calls = %v, outcome = %v", client.models, outcome.Outcome)
	}
	if outcome.ReviewResult.Reviewers != nil || len(outcome.Call.ExtraCalls) != 0 {
		t.Errorf("a single review records reviewers %+v / extra calls %+v", outcome.ReviewResult.Reviewers, outcome.Call.ExtraCalls)
	}
}

func TestExecuteReview_MalformedSecondAnswerFailsTheStep(t *testing.T) {
	client := &seqClient{outputs: []string{reviewClean, "not json"}}
	d, writer := reviewWith(t, client, "opus")
	_, err := d.Execute(context.Background(), domain.ActionReview, &domain.Run{ID: "r"})
	var we *apperr.WorkflowError
	if !errors.As(err, &we) {
		t.Fatalf("err = %v, want a WorkflowError", err)
	}
	if len(writer.calls) != 0 || d.lastReviewApplied() {
		t.Error("a half-reviewed step saved an artifact or changed the run's context")
	}
}

func TestExecuteReview_SecondReviewerCriteriaAreChecked(t *testing.T) {
	// The plan has one criterion; the second answer has no verdict for it.
	client := &seqClient{outputs: []string{
		`{"status":"pass","summary":"s","issues":[],"criteria":[{"index":1,"met":true,"evidence":"main.go:3"}]}`,
		reviewClean,
	}}
	d, _ := reviewWith(t, client, "opus")
	d.lastInvestigation = &domain.InvestigationArtifact{Summary: "p", AcceptanceCriteria: []string{"it works"}}
	if _, err := d.Execute(context.Background(), domain.ActionReview, &domain.Run{ID: "r"}); err == nil {
		t.Fatal("want an error: the second answer has no verdict for the criterion")
	}
}

func TestExecuteReview_FailedExtraCallKeepsTheUsageSoFar(t *testing.T) {
	client := &seqClient{
		outputs: []string{reviewClean},
		usages:  []*domain.Usage{{InputTokens: 10, OutputTokens: 5}},
	}
	d, _ := reviewWith(t, client, "opus")
	outcome, err := d.Execute(context.Background(), domain.ActionReview, &domain.Run{ID: "r"})
	if err == nil {
		t.Fatal("want the second call's error")
	}
	if outcome.Usage == nil || outcome.Usage.InputTokens != 10 {
		t.Errorf("usage = %+v, want the first call's, counted once", outcome.Usage)
	}
	if outcome.Call == nil || len(outcome.Call.ExtraCalls) != 1 || outcome.Call.ExtraCalls[0].Model != "opus" {
		t.Errorf("call = %+v", outcome.Call)
	}
}

func TestParseReviewStrict_DropsModelWrittenReviewers(t *testing.T) {
	out := `{"status":"pass","summary":"s","issues":[],"reviewers":[{"model":"x","result":{"status":"pass","summary":"t","issues":[]}}]}`
	got, err := parseReviewStrict(out, nil)
	if err != nil {
		t.Fatal(err)
	}
	if got.Reviewers != nil {
		t.Errorf("reviewers = %+v, want the model's own list dropped", got.Reviewers)
	}
}

func TestExecuteReview_ModelWrittenReviewersDoNotNest(t *testing.T) {
	nested := `{"status":"pass","summary":"s","issues":[],"reviewers":[{"model":"x","result":{"status":"pass","summary":"t","issues":[]}}]}`
	client := &seqClient{outputs: []string{nested, nested}}
	d, _ := reviewWith(t, client, "opus")
	outcome, err := d.Execute(context.Background(), domain.ActionReview, &domain.Run{ID: "r"})
	if err != nil {
		t.Fatal(err)
	}
	r := outcome.ReviewResult
	if len(r.Reviewers) != 2 {
		t.Fatalf("reviewers = %+v, want the two answers", r.Reviewers)
	}
	for _, a := range r.Reviewers {
		if len(a.Result.Reviewers) != 0 {
			t.Errorf("reviewer %s has reviewers of its own: %+v", a.Model, a.Result.Reviewers)
		}
	}
	if err := r.ValidateReviewers(); err != nil {
		t.Errorf("the saved result does not validate: %v", err)
	}

	single := &seqClient{outputs: []string{nested}}
	d, _ = reviewWith(t, single)
	outcome, err = d.Execute(context.Background(), domain.ActionReview, &domain.Run{ID: "r"})
	if err != nil {
		t.Fatal(err)
	}
	if outcome.ReviewResult.Reviewers != nil {
		t.Errorf("a single review records reviewers %+v", outcome.ReviewResult.Reviewers)
	}
}

// effortClient records the effort of each call as well as the model.
type effortClient struct {
	seqClient
	efforts []string
}

func (c *effortClient) Execute(ctx context.Context, req provider.Request) (provider.Result, error) {
	c.efforts = append(c.efforts, req.Effort)
	return c.seqClient.Execute(ctx, req)
}

func TestExecuteReview_ExtraReviewersUseTheBaseEffort(t *testing.T) {
	client := &effortClient{seqClient: seqClient{outputs: []string{reviewClean, reviewClean}}}
	d, _ := reviewWith(t, client, "opus")
	d.Config.Actions.Review.ByComplexity = map[domain.Complexity]config.ModelEffort{
		domain.ComplexityComplex: {Model: "big", Effort: "max"},
	}
	run := &domain.Run{ID: "r", Decision: &domain.Decision{
		Source:     domain.DecisionSourceLLM,
		Complexity: domain.ComplexityOutcome{Selected: domain.ComplexityComplex},
	}}
	outcome, err := d.Execute(context.Background(), domain.ActionReview, run)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(client.models, []string{"big", "opus"}) {
		t.Errorf("models asked = %v, want the routed model then the extra one", client.models)
	}
	if !reflect.DeepEqual(client.efforts, []string{"max", "high"}) {
		t.Errorf("efforts = %v, want the routed effort for the base review and the base effort for the extra one", client.efforts)
	}
	if outcome.Provider.Model != "big" || outcome.Provider.Effort != "max" {
		t.Errorf("provider = %+v, want the routed base review", outcome.Provider)
	}
}

// lastReviewApplied reports whether a review changed the dispatcher's retry context.
func (d *Dispatcher) lastReviewApplied() bool {
	return d.pendingReviewIssues != nil || d.pendingUnmetCriteria != nil || d.pendingMediumIssues != nil
}

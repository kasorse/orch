package action

import (
	"context"
	"errors"
	"strconv"
	"strings"
	"testing"

	"orchestrator/internal/apperr"
	"orchestrator/internal/domain"
	"orchestrator/internal/provider"
	"orchestrator/internal/workflow"
)

func TestExecuteReview_ShowsNewFilesAndWithholdsSecrets(t *testing.T) {
	repo, dir := guardRepo(t)
	put(t, dir, "internal/new.go", "package internal\n\nfunc NewThing() string { return \"fresh\" }\n")
	put(t, dir, ".env", "API_KEY=never-in-a-prompt\n")
	put(t, dir, "deploy/vault.txt", "vault-password-never-in-a-prompt\n")
	put(t, dir, "go.sum", "example.com/x v1.0.0 h1:LOCK\n")

	// The model's own "diff" field must not survive: the orchestrator records what it showed.
	client := &fakeClient{output: `{"status":"pass","summary":"ok","issues":[],"diff":{"bytes":1,"files":99,"shown":99,"summarized":0,"withheld":0,"truncated":false}}`}
	registry := provider.NewRegistry()
	registry.Register(provider.Claude, client)
	cfg := testConfig()
	cfg.Context.ExtraDeny = []string{"deploy/vault.txt"}
	d := New(cfg, registry, repo, nil)
	writer := &fakeArtifactWriter{}
	d.Artifacts = writer

	outcome, err := d.Execute(context.Background(), domain.ActionReview, &domain.Run{ID: "r", Task: domain.Task{Description: "add a thing"}})
	if err != nil {
		t.Fatal(err)
	}
	prompt := client.lastReq.Prompt
	if !strings.Contains(prompt, `+func NewThing() string { return "fresh" }`) {
		t.Errorf("the new file's content is not in the review prompt:\n%s", prompt)
	}
	for _, secret := range []string{"never-in-a-prompt", "h1:LOCK"} {
		if strings.Contains(prompt, secret) {
			t.Errorf("%q reached the review prompt", secret)
		}
	}
	if !strings.Contains(prompt, "A withheld file matches the secret deny list") {
		t.Error("the prompt does not explain that some files are not shown")
	}

	got := outcome.ReviewResult.Diff
	want := domain.ReviewDiffSummary{Files: 4, Shown: 1, Summarized: 1, Withheld: 2}
	if got == nil {
		t.Fatal("the review result records no diff summary")
	}
	if got.Bytes == 0 || got.Files != want.Files || got.Shown != want.Shown || got.Summarized != want.Summarized ||
		got.Withheld != want.Withheld || got.Truncated {
		t.Errorf("diff summary = %+v, want %+v (and bytes > 0)", *got, want)
	}
	saved, ok := writer.calls[0].artifact.(domain.ReviewResult)
	if !ok || saved.Diff == nil || saved.Diff.Files != 4 {
		t.Errorf("saved artifact = %+v", writer.calls[0].artifact)
	}
}

func TestExecuteReview_FullDiffNeedsNoNote(t *testing.T) {
	repo, dir := guardRepo(t)
	put(t, dir, "main.go", "package main\n\nfunc main() {}\n")
	client := &fakeClient{output: `{"status":"pass","summary":"ok","issues":[]}`}
	registry := provider.NewRegistry()
	registry.Register(provider.Claude, client)
	d := New(testConfig(), registry, repo, nil)

	if _, err := d.Execute(context.Background(), domain.ActionReview, &domain.Run{ID: "r"}); err != nil {
		t.Fatal(err)
	}
	if strings.Contains(client.lastReq.Prompt, "Not every file's content is in it") {
		t.Error("a diff shown in full was annotated as partial")
	}
}

func TestParseReviewStrict_Criteria(t *testing.T) {
	plan := []string{"first", "second", "third"}
	verdicts := func(vs ...string) string {
		return `{"status":"pass","summary":"s","issues":[],"criteria":[` + strings.Join(vs, ",") + `]}`
	}
	v := func(index int, evidence string) string {
		return `{"index":` + strconv.Itoa(index) + `,"met":true,"evidence":"` + evidence + `"}`
	}
	long := strings.Repeat("x", domain.MaxCriterionEvidenceBytes+1)

	for name, tc := range map[string]struct {
		output   string
		criteria []string
		wantErr  bool
	}{
		"two verdicts for three":  {verdicts(v(1, "a"), v(2, "b")), plan, true},
		"four verdicts for three": {verdicts(v(1, "a"), v(2, "b"), v(3, "c"), v(4, "d")), plan, true},
		"index zero":              {verdicts(v(0, "a"), v(2, "b"), v(3, "c")), plan, true},
		"index four":              {verdicts(v(1, "a"), v(2, "b"), v(4, "c")), plan, true},
		"duplicate":               {verdicts(v(1, "a"), v(2, "b"), v(2, "c")), plan, true},
		"empty evidence":          {verdicts(v(1, "a"), v(2, ""), v(3, "c")), plan, true},
		"evidence too long":       {verdicts(v(1, "a"), v(2, long), v(3, "c")), plan, true},
		"none for three":          {`{"status":"pass","summary":"s","issues":[]}`, plan, true},
		"criteria without a plan": {verdicts(v(1, "a")), nil, true},
		"no criteria, no plan":    {`{"status":"pass","summary":"s","issues":[]}`, nil, false},
		"empty list, no plan":     {verdicts(), nil, false},
		"out of order":            {verdicts(v(3, "c"), v(1, "a"), v(2, "b")), plan, false},
	} {
		t.Run(name, func(t *testing.T) {
			got, err := parseReviewStrict(tc.output, tc.criteria)
			if (err != nil) != tc.wantErr {
				t.Fatalf("err = %v, want error: %v", err, tc.wantErr)
			}
			if err != nil {
				return
			}
			for i, c := range got.Criteria {
				if c.Index != i+1 || c.Criterion != tc.criteria[i] {
					t.Errorf("verdict %d = %+v, want index %d and criterion %q", i, c, i+1, tc.criteria[i])
				}
			}
		})
	}
}

func TestExecuteReview_Criteria(t *testing.T) {
	plan := &domain.PlanArtifact{Summary: "p", AcceptanceCriteria: []string{"first", " ", "second"}}
	run := func(t *testing.T, output string) (*fakeClient, workflow.StepOutcome, error) {
		repo, dir := guardRepo(t)
		put(t, dir, "main.go", "package main\n\nfunc main() {}\n")
		client := &fakeClient{output: output}
		registry := provider.NewRegistry()
		registry.Register(provider.Claude, client)
		d := New(testConfig(), registry, repo, nil)
		d.lastPlan = plan
		outcome, err := d.Execute(context.Background(), domain.ActionReview, &domain.Run{ID: "r"})
		if err == nil && len(d.pendingUnmetCriteria) != len(outcome.ReviewResult.UnmetCriteria()) {
			t.Errorf("pendingUnmetCriteria = %+v", d.pendingUnmetCriteria)
		}
		return client, outcome, err
	}

	t.Run("missing verdicts are malformed", func(t *testing.T) {
		_, _, err := run(t, `{"status":"pass","summary":"ok","issues":[]}`)
		var we *apperr.WorkflowError
		if !errors.As(err, &we) || we.Msg != "malformed review output" {
			t.Fatalf("err = %v, want a WorkflowError \"malformed review output\"", err)
		}
	})

	t.Run("an unmet criterion fails the review", func(t *testing.T) {
		client, outcome, err := run(t, `{"status":"pass","summary":"ok","issues":[],"criteria":[`+
			`{"index":2,"met":false,"evidence":"no test"},{"index":1,"met":true,"evidence":"main.go:3"}]}`)
		if err != nil {
			t.Fatal(err)
		}
		if outcome.Outcome != workflow.OutcomeFailure {
			t.Errorf("outcome = %v, want failure", outcome.Outcome)
		}
		c := outcome.ReviewResult.Criteria
		if len(c) != 2 || c[0].Criterion != "first" || c[1].Criterion != "second" || c[1].Met {
			t.Errorf("criteria = %+v", c)
		}
		for _, want := range []string{`"criteria": [`, "You have Read, Grep and Glob.", "1. first\n2. second\n"} {
			if !strings.Contains(client.lastReq.Prompt, want) {
				t.Errorf("review prompt lacks %q", want)
			}
		}
	})
}

func TestReviewResultValidate_DiffCounts(t *testing.T) {
	r := domain.ReviewResult{Status: domain.ReviewPass, Diff: &domain.ReviewDiffSummary{Files: 1, Shown: 2}}
	if r.Validate() == nil {
		t.Error("more files shown than changed must be invalid")
	}
	r.Diff = &domain.ReviewDiffSummary{Files: 3, Shown: 1, Withheld: 1, Bytes: 10}
	if err := r.Validate(); err != nil {
		t.Errorf("consistent counts rejected: %v", err)
	}
}

package action

import (
	"context"
	"strings"
	"testing"

	"orchestrator/internal/domain"
	"orchestrator/internal/provider"
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

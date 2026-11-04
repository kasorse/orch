package action

import (
	"context"
	"os/exec"
	"strings"
	"testing"

	"github.com/kasorse/orch/internal/domain"
	"github.com/kasorse/orch/internal/execution"
	"github.com/kasorse/orch/internal/provider"
	"github.com/kasorse/orch/internal/repository"
)

func workflowOf(actions ...domain.ActionType) *domain.Workflow {
	wf := &domain.Workflow{}
	for _, a := range actions {
		wf.Steps = append(wf.Steps, domain.Step{Action: a})
	}
	return wf
}

func TestBuildResearchPrompt(t *testing.T) {
	p := BuildResearchPrompt(ResearchPromptInput{Task: "Compare Redis and Memcached"})
	if !strings.HasPrefix(p, "ACTION: research\n") {
		t.Errorf("missing routing marker:\n%s", p)
	}
	if got := taskOf(t, p); got != "Compare Redis and Memcached\n" {
		t.Errorf("task block = %q", got)
	}
	for _, want := range []string{readOnlyRule, `"findings"`, `"recommendation"`} {
		if !strings.Contains(p, want) {
			t.Errorf("prompt lacks %q:\n%s", want, p)
		}
	}
	if strings.Contains(p, `"answer"`) || strings.Contains(p, "final step") {
		t.Errorf("a non-final research step must not ask for an answer:\n%s", p)
	}

	final := BuildResearchPrompt(ResearchPromptInput{Task: "x", FinalAnswer: true})
	if !strings.Contains(final, `"answer": "..."`) || !strings.Contains(final, finalAnswerRule) {
		t.Errorf("final step must ask for the reply:\n%s", final)
	}
}

func TestBuildInvestigatePrompt_CarriesResearch(t *testing.T) {
	research := &domain.ResearchArtifact{Summary: "Redis fits", Findings: []string{"TTL support"}, Recommendation: "use Redis"}

	p := BuildInvestigatePrompt(InvestigatePromptInput{Task: "add a cache", Research: research})
	for _, want := range []string{"ACTION: investigate\n", readOnlyRule, "Research:\nRedis fits", "- TTL support", "Recommendation: use Redis"} {
		if !strings.Contains(p, want) {
			t.Errorf("investigate prompt lacks %q:\n%s", want, p)
		}
	}
	if strings.Contains(BuildInvestigatePrompt(InvestigatePromptInput{Task: "x"}), "Research:") {
		t.Error("no research section without research")
	}
}

func TestIsFinalReadOnlyStep(t *testing.T) {
	cases := []struct {
		wf     *domain.Workflow
		action domain.ActionType
		want   bool
	}{
		{workflowOf(domain.ActionResearch), domain.ActionResearch, true},
		{workflowOf(domain.ActionResearch, domain.ActionInvestigate), domain.ActionResearch, false},
		{workflowOf(domain.ActionResearch, domain.ActionInvestigate), domain.ActionInvestigate, true},
		{workflowOf(domain.ActionInvestigate, domain.ActionReview), domain.ActionInvestigate, true},
		{workflowOf(domain.ActionInvestigate, domain.ActionImplement, domain.ActionTest), domain.ActionInvestigate, false},
		{nil, domain.ActionInvestigate, false},
	}
	for i, tc := range cases {
		if got := isFinalReadOnlyStep(&domain.Run{Workflow: tc.wf}, tc.action); got != tc.want {
			t.Errorf("case %d: isFinalReadOnlyStep(%s) = %v, want %v", i, tc.action, got, tc.want)
		}
	}
}

func TestExecuteResearch_RoutesAndFeedsInvestigation(t *testing.T) {
	client := &fakeClient{output: `{"summary":"Redis fits","findings":["TTL"],"options":["Redis","Memcached"],"recommendation":"Redis","references":[],"answer":"Use Redis."}`}
	d := newDispatcher(client)
	writer := &fakeArtifactWriter{}
	d.Artifacts = writer
	run := &domain.Run{ID: "r", Task: domain.Task{Description: "сравни Redis и Memcached"}, Workflow: workflowOf(domain.ActionResearch, domain.ActionInvestigate)}

	if _, err := d.Execute(context.Background(), domain.ActionResearch, run); err != nil {
		t.Fatal(err)
	}
	if client.lastReq.Model != "research-model" {
		t.Errorf("model = %q, want actions.research's", client.lastReq.Model)
	}
	if got := taskOf(t, client.lastReq.Prompt); got != "сравни Redis и Memcached\n" {
		t.Errorf("stages must get the original request text, got %q", got)
	}
	if len(writer.calls) != 1 || writer.calls[0].actionType != domain.ActionResearch {
		t.Fatalf("artifacts = %+v", writer.calls)
	}
	if art := writer.calls[0].artifact.(domain.ResearchArtifact); art.Recommendation != "Redis" || len(art.Options) != 2 {
		t.Errorf("artifact = %+v", art)
	}

	client.output = `{"summary":"s","answer":"Use Redis."}`
	if _, err := d.Execute(context.Background(), domain.ActionInvestigate, run); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(client.lastReq.Prompt, "Research:\nRedis fits") {
		t.Errorf("investigate did not get the research:\n%s", client.lastReq.Prompt)
	}
	if !strings.Contains(client.lastReq.Prompt, finalAnswerRule) {
		t.Error("investigate is the final read-only step and must ask for the reply")
	}
	if d.lastInvestigation.Answer != "Use Redis." {
		t.Errorf("answer not parsed: %+v", d.lastInvestigation)
	}
}

func TestExecuteResearch_MalformedOutputFallsBackToSummary(t *testing.T) {
	d := newDispatcher(&fakeClient{output: "plain prose"})
	if _, err := d.Execute(context.Background(), domain.ActionResearch, &domain.Run{ID: "r"}); err != nil {
		t.Fatal(err)
	}
	if d.lastResearch == nil || d.lastResearch.Summary != "plain prose" {
		t.Errorf("lastResearch = %+v", d.lastResearch)
	}
}

func TestResearchThenInvestigate_LeavesGitClean(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not installed")
	}
	dir := t.TempDir()
	for _, args := range [][]string{
		{"init", "-q"},
		{"-c", "user.email=t@example.com", "-c", "user.name=t", "commit", "-q", "--allow-empty", "-m", "init"},
	} {
		cmd := exec.Command("git", args...)
		cmd.Dir = dir
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v\n%s", args, err, out)
		}
	}
	repo := &repository.Repository{Dir: dir, Executor: execution.NewOSProcessExecutor()}
	registry := provider.NewRegistry()
	registry.Register(provider.Claude, &fakeClient{output: `{"summary":"s"}`})
	d := New(testConfig(), registry, repo, nil)
	run := &domain.Run{ID: "r", Task: domain.Task{Description: "compare"}, Workflow: workflowOf(domain.ActionResearch, domain.ActionInvestigate)}

	for _, a := range []domain.ActionType{domain.ActionResearch, domain.ActionInvestigate} {
		if _, err := d.Execute(context.Background(), a, run); err != nil {
			t.Fatal(err)
		}
	}
	dirty, err := repo.IsDirty(context.Background())
	if err != nil || dirty {
		t.Errorf("repository dirty=%v err=%v after read-only stages", dirty, err)
	}
}

func TestFinalAnswer(t *testing.T) {
	d := newDispatcher(&fakeClient{})
	d.lastResearch = &domain.ResearchArtifact{Answer: "from research"}
	d.lastInvestigation = &domain.InvestigationArtifact{Answer: "from investigation"}

	cases := []struct {
		wf   *domain.Workflow
		want string
	}{
		{workflowOf(domain.ActionResearch, domain.ActionInvestigate), "from investigation"},
		{workflowOf(domain.ActionResearch, domain.ActionReview), "from research"},
		{workflowOf(domain.ActionInvestigate, domain.ActionImplement, domain.ActionTest), ""},
		{workflowOf(domain.ActionTest), ""},
		{nil, ""},
	}
	for i, tc := range cases {
		if got := d.FinalAnswer(&domain.Run{Workflow: tc.wf}); got != tc.want {
			t.Errorf("case %d: FinalAnswer = %q, want %q", i, got, tc.want)
		}
	}
}

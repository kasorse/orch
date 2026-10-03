package action

import (
	"context"
	"os/exec"
	"strings"
	"testing"

	"orchestrator/internal/domain"
	"orchestrator/internal/execution"
	"orchestrator/internal/provider"
	"orchestrator/internal/repository"
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

func TestBuildAnalyzeAndPlanPrompts_CarryEarlierStages(t *testing.T) {
	research := &domain.ResearchArtifact{Summary: "Redis fits", Findings: []string{"TTL support"}, Recommendation: "use Redis"}
	analysis := &domain.AnalysisArtifact{Summary: "no cache yet", RootCause: "n/a", Recommendation: "wrap the repository"}

	a := BuildAnalyzePrompt(AnalyzePromptInput{Task: "add a cache", Research: research})
	for _, want := range []string{"ACTION: analyze\n", readOnlyRule, "Research:\nRedis fits", "- TTL support", "Recommendation: use Redis"} {
		if !strings.Contains(a, want) {
			t.Errorf("analyze prompt lacks %q:\n%s", want, a)
		}
	}

	p := BuildPlanPrompt(PlanPromptInput{Task: "add a cache", Research: research, Analysis: analysis})
	for _, want := range []string{"ACTION: plan\n", readOnlyRule, "Research:\nRedis fits", "Analysis:\nno cache yet", "Recommendation: wrap the repository"} {
		if !strings.Contains(p, want) {
			t.Errorf("plan prompt lacks %q:\n%s", want, p)
		}
	}
	if strings.Contains(BuildPlanPrompt(PlanPromptInput{Task: "x"}), "Research:") {
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
		{workflowOf(domain.ActionResearch, domain.ActionAnalyze), domain.ActionResearch, false},
		{workflowOf(domain.ActionResearch, domain.ActionAnalyze), domain.ActionAnalyze, true},
		{workflowOf(domain.ActionAnalyze, domain.ActionPlan, domain.ActionReview), domain.ActionPlan, true},
		{workflowOf(domain.ActionAnalyze, domain.ActionImplement, domain.ActionTest), domain.ActionAnalyze, false},
		{nil, domain.ActionAnalyze, false},
	}
	for i, tc := range cases {
		if got := isFinalReadOnlyStep(&domain.Run{Workflow: tc.wf}, tc.action); got != tc.want {
			t.Errorf("case %d: isFinalReadOnlyStep(%s) = %v, want %v", i, tc.action, got, tc.want)
		}
	}
}

func TestExecuteResearch_RoutesAndFeedsAnalysis(t *testing.T) {
	client := &fakeClient{output: `{"summary":"Redis fits","findings":["TTL"],"options":["Redis","Memcached"],"recommendation":"Redis","references":[],"answer":"Use Redis."}`}
	d := newDispatcher(client)
	writer := &fakeArtifactWriter{}
	d.Artifacts = writer
	run := &domain.Run{ID: "r", Task: domain.Task{Description: "сравни Redis и Memcached"}, Workflow: workflowOf(domain.ActionResearch, domain.ActionAnalyze)}

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
	if _, err := d.Execute(context.Background(), domain.ActionAnalyze, run); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(client.lastReq.Prompt, "Research:\nRedis fits") {
		t.Errorf("analysis did not get the research:\n%s", client.lastReq.Prompt)
	}
	if !strings.Contains(client.lastReq.Prompt, finalAnswerRule) {
		t.Error("analysis is the final read-only step and must ask for the reply")
	}
	if d.lastAnalysis.Answer != "Use Redis." {
		t.Errorf("answer not parsed: %+v", d.lastAnalysis)
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

func TestResearchThenAnalysis_LeavesGitClean(t *testing.T) {
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
	run := &domain.Run{ID: "r", Task: domain.Task{Description: "compare"}, Workflow: workflowOf(domain.ActionResearch, domain.ActionAnalyze)}

	for _, a := range []domain.ActionType{domain.ActionResearch, domain.ActionAnalyze} {
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
	d.lastAnalysis = &domain.AnalysisArtifact{Answer: "from analysis"}

	cases := []struct {
		wf   *domain.Workflow
		want string
	}{
		{workflowOf(domain.ActionResearch, domain.ActionAnalyze), "from analysis"},
		{workflowOf(domain.ActionResearch, domain.ActionReview), "from research"},
		{workflowOf(domain.ActionAnalyze, domain.ActionImplement, domain.ActionTest), ""},
		{workflowOf(domain.ActionTest), ""},
		{nil, ""},
	}
	for i, tc := range cases {
		if got := d.FinalAnswer(&domain.Run{Workflow: tc.wf}); got != tc.want {
			t.Errorf("case %d: FinalAnswer = %q, want %q", i, got, tc.want)
		}
	}
}

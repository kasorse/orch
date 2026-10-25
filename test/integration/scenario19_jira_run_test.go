package integration

import (
	"encoding/json"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"

	"orchestrator/internal/testutil/fakeexec"
)

// Scenario 19 (specs/jira/SPEC.md §16): run --jira takes the task from a ticket on a fake
// Jira. The rendered ticket is the task every stage and the decision read; the run
// records where it came from.

const (
	pmc1Summary     = "Add() subtracts instead of adding"
	pmc1Description = "Steps to reproduce:\r\nAdd(1, 2) returns -1"
)

// pmc1Rendered is PMC-1 as jira.Render prints it for a server at baseURL.
func pmc1Rendered(baseURL, notes string) string {
	s := "PMC-1: " + pmc1Summary + "\nType: Bug\nSource: " + baseURL + "/browse/PMC-1\n\n" +
		"Description:\nSteps to reproduce:\nAdd(1, 2) returns -1\n\n" +
		"Acceptance criteria:\nAdd(1, 2) == 3\n"
	if notes != "" {
		s += "\nOperator notes:\n" + notes + "\n"
	}
	return s
}

// jiraRun is a scenario-19 setup: fake providers, a fake Jev deciding
// investigate -> implement -> test -> review, and a fake Jira serving PMC-1.
type jiraRun struct {
	repoDir, cfg, invocationsLog, promptsDir string
	jev                                      *fakeJev
	jira                                     *fakeJira
}

func newJiraRun(t *testing.T, fail ...int) jiraRun {
	t.Helper()
	repoDir := fakeexec.InitGitFixture(t)
	invocationsLog, promptsDir := decisionEnv(t, repoDir)
	jev := startJev(t, scriptBugfixMedium())
	jira := startJira(t, map[string]string{
		"PMC-1": jiraIssue(t, "PMC-1", pmc1Summary, pmc1Description,
			map[string]any{"customfield_10035": "Add(1, 2) == 3", "customfield_10036": nil}),
	}, fail...)
	cfg := jevConfig(t, jev, func(o *configOptions) {
		o.jira = &jiraOptions{baseURL: jira.URL(), fields: [][2]string{
			{"customfield_10035", "Acceptance criteria"}, {"customfield_10036", "Design"},
		}}
	})
	return jiraRun{repoDir: repoDir, cfg: cfg, invocationsLog: invocationsLog, promptsDir: promptsDir, jev: jev, jira: jira}
}

// taskSource is run.json's task as scenario 19 reads it.
type taskSource struct {
	Task struct {
		Description string `json:"description"`
		Source      *struct {
			Kind, Key, RequestedKey, URL string
			FetchedAt                    string `json:"fetched_at"`
			Notes                        bool   `json:"notes"`
		} `json:"source"`
	} `json:"task"`
}

func loadTaskSource(t *testing.T, repoDir string) taskSource {
	t.Helper()
	raw, ok := runFile(t, repoDir, "run.json")
	if !ok {
		t.Fatal("run.json was not written")
	}
	var ts taskSource
	if err := json.Unmarshal([]byte(raw), &ts); err != nil {
		t.Fatal(err)
	}
	return ts
}

// 19a: the ticket is fetched once, printed, and becomes the task of every stage and of
// the decision; run.json records the source and ticket.json keeps the issue.
func TestScenario19a_JiraSuccess(t *testing.T) {
	r := newJiraRun(t)

	stdout, stderr, err := runOrchestrator(t, r.repoDir, r.cfg, "run", "--jira", "pmc-1")
	mustSucceed(t, stdout, stderr, err)

	reqs := r.jira.Requests()
	if len(reqs) != 1 {
		t.Fatalf("Jira got %d requests, want 1", len(reqs))
	}
	if reqs[0].Path != "/rest/api/2/issue/PMC-1" ||
		reqs[0].Query != "fields=summary,description,issuetype,parent,components,customfield_10035,customfield_10036" {
		t.Errorf("request = %+v", reqs[0])
	}

	rendered := pmc1Rendered(r.jira.URL(), "")
	for _, action := range []string{"investigate", "implement", "review"} {
		prompt, err := os.ReadFile(filepath.Join(r.promptsDir, action+".txt"))
		if err != nil {
			t.Fatalf("%s prompt: %v", action, err)
		}
		if !strings.Contains(string(prompt), rendered) {
			t.Errorf("%s prompt lacks the rendered ticket:\n%s", action, prompt)
		}
	}
	if got := invocations(t, r.invocationsLog); !equalStrings(got, []string{"investigate", "implement", "review"}) {
		t.Errorf("invocations = %v", got)
	}

	sawTask := false
	for _, body := range r.jev.Bodies() {
		sawTask = sawTask || strings.Contains(body, "PMC-1: "+pmc1Summary+`\nType: Bug`)
	}
	if !sawTask {
		t.Error("no Jev request carried the rendered ticket")
	}

	ts := loadTaskSource(t, r.repoDir)
	if ts.Task.Description != rendered {
		t.Errorf("task.description =\n%q\nwant\n%q", ts.Task.Description, rendered)
	}
	if src := ts.Task.Source; src == nil || src.Kind != "jira" || src.Key != "PMC-1" || src.Notes ||
		src.URL != r.jira.URL()+"/browse/PMC-1" || src.FetchedAt == "" {
		t.Errorf("task.source = %+v", src)
	}
	ticket, ok := runFile(t, r.repoDir, "ticket.json")
	if !ok || !strings.Contains(ticket, `"summary": "`+pmc1Summary+`"`) || strings.Contains(ticket, jiraToken) {
		t.Errorf("ticket.json = %q (present %v)", ticket, ok)
	}

	for _, want := range []string{
		"Fetching PMC-1 from " + strings.TrimPrefix(r.jira.URL(), "http://") + "...",
		"✓ PMC-1 (Bug) fetched in",
		"Task (from PMC-1):\n  | PMC-1: " + pmc1Summary + "\n  | Type: Bug\n",
		"  | Acceptance criteria:\n  | Add(1, 2) == 3\n\nRun: ",
		"Run completed successfully.",
	} {
		if !strings.Contains(stdout, want) {
			t.Errorf("stdout lacks %q:\n%s", want, stdout)
		}
	}
	for _, secret := range []string{jiraToken, jiraEmail} {
		if strings.Contains(stdout+stderr, secret) {
			t.Errorf("output contains %q", secret)
		}
	}
}

// 19b: the operator's notes come last in the task each stage reads.
func TestScenario19b_JiraOperatorNotes(t *testing.T) {
	r := newJiraRun(t)

	stdout, stderr, err := runOrchestrator(t, r.repoDir, r.cfg, "run", "--jira", "PMC-1", "only the FE part")
	mustSucceed(t, stdout, stderr, err)

	prompt, err := os.ReadFile(filepath.Join(r.promptsDir, "implement.txt"))
	if err != nil {
		t.Fatal(err)
	}
	// The rendered ticket, notes last, is the whole content of the task block.
	block := regexp.MustCompile(`(?s)Task:\n[^\n]*\n<<<(TASK-[A-Z0-9]+)\n(.*?)>>>TASK-`).FindStringSubmatch(string(prompt))
	if block == nil || block[2] != pmc1Rendered(r.jira.URL(), "only the FE part") {
		t.Errorf("implement prompt lacks the task block ending in the notes:\n%s", prompt)
	}
	if src := loadTaskSource(t, r.repoDir).Task.Source; src == nil || !src.Notes {
		t.Errorf("task.source = %+v, want notes true", src)
	}
}

// A transient failure is retried within jira.max_retries (default 1).
func TestScenario19_JiraRetry(t *testing.T) {
	r := newJiraRun(t, 503)

	stdout, stderr, err := runOrchestrator(t, r.repoDir, r.cfg, "run", "--jira", "PMC-1")
	mustSucceed(t, stdout, stderr, err)
	if n := len(r.jira.Requests()); n != 2 {
		t.Errorf("Jira got %d requests, want 2", n)
	}
}

// 19h: a jira section in the config changes nothing for a run without --jira - no
// request, no Jira variables needed, no task.source.
func TestScenario19h_RunWithoutJira(t *testing.T) {
	r := newJiraRun(t)
	unsetenv(t, "JIRA_EMAIL")
	unsetenv(t, "JIRA_API_TOKEN")

	stdout, stderr, err := runOrchestrator(t, r.repoDir, r.cfg, "run", "fix Add in fixture.go")
	mustSucceed(t, stdout, stderr, err)
	if n := len(r.jira.Requests()); n != 0 {
		t.Errorf("Jira got %d requests, want 0", n)
	}
	if ts := loadTaskSource(t, r.repoDir); ts.Task.Source != nil || ts.Task.Description != "fix Add in fixture.go" {
		t.Errorf("task = %+v, want the typed text and no source", ts.Task)
	}
	if _, ok := runFile(t, r.repoDir, "ticket.json"); ok {
		t.Error("ticket.json was written")
	}
	if strings.Contains(stdout, "Task (from") {
		t.Errorf("stdout has a ticket block:\n%s", stdout)
	}
}

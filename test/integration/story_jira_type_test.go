package integration

import (
	"strings"
	"testing"

	"github.com/kasorse/orch/internal/apperr"
	"github.com/kasorse/orch/internal/exitcode"
	"github.com/kasorse/orch/internal/testutil/fakeexec"

	"errors"
)

// ST08 (specs/story/SPEC.md §12.1): run --jira refuses an issue whose type is in
// story.jira_issue_types, before any run exists.

// newEpicRun is a scenario-19 setup whose PMC-1 is an Epic; storyYAML is appended to the config.
func newEpicRun(t *testing.T, storyYAML string) jiraRun {
	t.Helper()
	repoDir := fakeexec.InitGitFixture(t)
	invocationsLog, promptsDir := decisionEnv(t, repoDir)
	jev := startJev(t, scriptBugfixMedium())
	jira := startJira(t, map[string]string{
		"PMC-1": jiraIssue(t, "PMC-1", pmc1Summary, pmc1Description,
			map[string]any{"issuetype": map[string]string{"name": "Epic"}, "customfield_10035": "Add(1, 2) == 3", "customfield_10036": nil}),
	})
	cfg := jevConfig(t, jev, func(o *configOptions) {
		o.jira = &jiraOptions{baseURL: jira.URL(), fields: [][2]string{
			{"customfield_10035", "Acceptance criteria"}, {"customfield_10036", "Design"},
		}}
		if storyYAML != "" {
			o.extraYAML = "\nstory:\n  roots: [" + yamlQuote(t.TempDir()) + "]\n" + storyYAML
		}
	})
	return jiraRun{repoDir: repoDir, cfg: cfg, invocationsLog: invocationsLog, promptsDir: promptsDir, jev: jev, jira: jira}
}

func TestStoryJiraType_EpicIsRefusedWithThePlanCommand(t *testing.T) {
	r := newEpicRun(t, "  jira_issue_types: [epic]\n")

	_, _, err := runOrchestrator(t, r.repoDir, r.cfg, "run", "--jira", "PMC-1")
	var cfgErr *apperr.ConfigError
	if !errors.As(err, &cfgErr) || !strings.Contains(err.Error(), "story plan --jira PMC-1") {
		t.Fatalf("expected a ConfigError naming story plan --jira PMC-1, got %T: %v", err, err)
	}
	if got := exitcode.FromError(err); got != exitcode.ConfigOrDependency {
		t.Errorf("exit code = %d, want %d", got, exitcode.ConfigOrDependency)
	}
	assertNoRuns(t, r.repoDir)
	if got := invocations(t, r.invocationsLog); len(got) != 0 {
		t.Errorf("expected no provider call, got %v", got)
	}
}

func TestStoryJiraType_WithoutTheSettingIsAnOrdinaryRun(t *testing.T) {
	r := newEpicRun(t, "")

	stdout, stderr, err := runOrchestrator(t, r.repoDir, r.cfg, "run", "--jira", "PMC-1")
	mustSucceed(t, stdout, stderr, err)
}

package integration

import (
	"encoding/json"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	"github.com/kasorse/orch/internal/testutil/fakeexec"
)

// policy.paths (specs/governance/SPEC.md §4, GV02): the rules are recorded when the run starts,
// checked on the run's net diff after each implement, and a resume applies the recorded ones.

// pathRulesRun is the part of run.json the path rule scenarios assert on.
type pathRulesRun struct {
	Status        string              `json:"status"`
	FailureReason string              `json:"failure_reason"`
	Options       pathRulesOptions    `json:"options"`
	Executions    []pathRulesExecuted `json:"executions"`
}

type pathRulesOptions struct {
	PathRules []pathRuleOption `json:"path_rules"`
}

type pathRuleOption struct {
	Match         string   `json:"match"`
	RequireStages []string `json:"require_stages"`
}

type pathRulesExecuted struct {
	Action    string          `json:"action"`
	Error     string          `json:"error"`
	PathRules []pathRuleMatch `json:"path_rules"`
}

type pathRuleMatch struct {
	Match   string   `json:"match"`
	Files   []string `json:"files"`
	Missing []string `json:"missing"`
}

func loadPathRulesRun(t *testing.T, repoDir string) pathRulesRun {
	t.Helper()
	var run pathRulesRun
	if err := json.Unmarshal([]byte(readFile(t, runJSONPath(t, repoDir))), &run); err != nil {
		t.Fatal(err)
	}
	return run
}

const (
	fixtureNeedsResearch = "    - match: fixture.go\n      require_stages: [research]\n"
	fixtureNeedsTesting  = "    - match: fixture.go\n      require_stages: [testing]\n"
	// migrationsNeedTesting is the rule of scenarios 49, 50 and 52.
	migrationsNeedTesting = "    - match: migrations/**\n      require_stages: [testing]\n"
)

// touchOnImplement makes each fake implement call write rel (relative to repoDir), the way an
// implementation adds a migration. With revertOnCall above 0 the call of that number on removes
// it again.
func touchOnImplement(t *testing.T, repoDir, rel string, revertOnCall int) {
	t.Helper()
	t.Setenv("FAKE_IMPLEMENT_TOUCH_FILE", filepath.Join(repoDir, rel))
	if revertOnCall > 0 {
		t.Setenv("FAKE_IMPLEMENT_REVERT_ON_CALL", strconv.Itoa(revertOnCall))
	}
}

// A rule that needs a stage the workflow lacks: the run warns at its start, the first
// implement fails with the rule's message and records the match.
func TestPathRules_MissingStageStopsTheRun(t *testing.T) {
	repoDir := fakeexec.InitGitFixture(t)
	r := newQualityRun(t, repoDir, func(o *configOptions) { o.pathRules = fixtureNeedsResearch })

	_, stderr, err := runOrchestrator(t, repoDir, r.cfg, append(implementTestReview, "fix Add in fixture.go")...)
	want := `policy.paths "fixture.go" requires research, which this workflow does not run (changed: fixture.go)`
	if err == nil || !strings.Contains(err.Error(), want) {
		t.Fatalf("error = %v, want %q", err, want)
	}
	assertContains(t, "stderr", stderr,
		`Warning: policy.paths "fixture.go" requires research; this workflow has no research.`,
		"A change there will stop the run. Add the stage with --stage, or start a new run.")

	run := loadPathRulesRun(t, repoDir)
	if run.Status != "failed" || !strings.Contains(run.FailureReason, want) || len(run.Executions) != 1 {
		t.Fatalf("status %s, reason %q, %d executions", run.Status, run.FailureReason, len(run.Executions))
	}
	if len(run.Options.PathRules) != 1 || run.Options.PathRules[0].Match != "fixture.go" {
		t.Errorf("options.path_rules = %+v", run.Options.PathRules)
	}
	rules := run.Executions[0].PathRules
	if len(rules) != 1 || !equalStrings(rules[0].Files, []string{"fixture.go"}) || !equalStrings(rules[0].Missing, []string{"research"}) {
		t.Errorf("path_rules = %+v", rules)
	}
}

// A rule the workflow satisfies: no warning, the run completes, the match is recorded with
// nothing missing.
func TestPathRules_SatisfiedRuleIsRecorded(t *testing.T) {
	repoDir := fakeexec.InitGitFixture(t)
	r := newQualityRun(t, repoDir, func(o *configOptions) { o.pathRules = fixtureNeedsTesting })

	stdout, stderr, err := runOrchestrator(t, repoDir, r.cfg, append(implementTestReview, "fix Add in fixture.go")...)
	mustSucceed(t, stdout, stderr, err)
	assertNotContains(t, "stderr", stderr, "policy.paths")

	run := loadPathRulesRun(t, repoDir)
	rules := run.Executions[0].PathRules
	if run.Status != "completed" || len(rules) != 1 || rules[0].Missing == nil || len(rules[0].Missing) != 0 {
		t.Errorf("status %s, path_rules = %+v (missing must be an empty list)", run.Status, rules)
	}
}

// A run started with rule X and resumed under rule Y warns and still applies X.
func TestPathRules_ResumeKeepsTheRecordedRules(t *testing.T) {
	repoDir := fakeexec.InitGitFixture(t)
	r := newQualityRun(t, repoDir, func(o *configOptions) { o.pathRules = fixtureNeedsResearch })

	_, _, err := runOrchestrator(t, repoDir, r.cfg, append(implementTestReview, "fix Add in fixture.go")...)
	if err == nil {
		t.Fatal("rule X needs research, which the workflow lacks: the first implement must fail")
	}
	failed := loadPathRulesRun(t, repoDir)
	id := loadQL(t, repoDir).ID

	// The operator rewrites the rule so that the workflow satisfies it.
	changed := jevConfig(t, r.jev, func(o *configOptions) { o.pathRules = fixtureNeedsTesting })
	assertContains(t, "the new config", readFile(t, changed), "require_stages: [testing]")

	_, stderr, err := resume(t, repoDir, changed, id)
	assertContains(t, "stderr", stderr,
		"Warning: policy.paths changed since the run started; this run keeps the rules it started with")
	want := `policy.paths "fixture.go" requires research, which this workflow does not run`
	if err == nil || !strings.Contains(err.Error(), want) {
		t.Fatalf("resume error = %v, want rule X's failure %q", err, want)
	}

	run := loadPathRulesRun(t, repoDir)
	if len(run.Executions) != len(failed.Executions)+1 || run.Executions[1].Action != "implement" {
		t.Errorf("executions = %+v, want the implement retried once", run.Executions)
	}
	if len(run.Options.PathRules) != 1 || !equalStrings(run.Options.PathRules[0].RequireStages, []string{"research"}) {
		t.Errorf("the resume changed options.path_rules: %+v", run.Options.PathRules)
	}
}

package integration

import (
	"testing"

	"github.com/kasorse/orch/internal/testutil/fakeexec"
)

// Scenario 50 (specs/governance/SPEC.md §12, GV02): the rule of scenario 49 against a workflow
// that has testing. No warning, the run completes, and run.json records the match with nothing
// missing.

func TestScenario50_PathRuleSatisfiedByTheWorkflow(t *testing.T) {
	repoDir := fakeexec.InitGitFixture(t)
	r := newQualityRun(t, repoDir, func(o *configOptions) { o.pathRules = migrationsNeedTesting })
	touchOnImplement(t, repoDir, "migrations/x.sql", 0)

	stdout, stderr, err := runOrchestrator(t, repoDir, r.cfg, append(implementTestReview, "add migrations/x.sql")...)
	mustSucceed(t, stdout, stderr, err)
	assertNotContains(t, "stderr", stderr, "policy.paths")

	run := loadPathRulesRun(t, repoDir)
	if run.Status != "completed" || run.FailureReason != "" || len(run.Executions) == 0 {
		t.Fatalf("status %s, reason %q, %d executions", run.Status, run.FailureReason, len(run.Executions))
	}
	if len(run.Options.PathRules) != 1 || run.Options.PathRules[0].Match != "migrations/**" ||
		!equalStrings(run.Options.PathRules[0].RequireStages, []string{"testing"}) {
		t.Errorf("options.path_rules = %+v", run.Options.PathRules)
	}
	rules := run.Executions[0].PathRules
	if run.Executions[0].Action != "implement" || len(rules) != 1 || rules[0].Match != "migrations/**" ||
		!equalStrings(rules[0].Files, []string{"migrations/x.sql"}) || rules[0].Missing == nil || len(rules[0].Missing) != 0 {
		t.Errorf("path_rules = %+v (missing must be an empty list)", rules)
	}
}

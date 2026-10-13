package integration

import (
	"os"
	"path/filepath"
	"testing"

	"orchestrator/internal/testutil/fakeexec"
)

// Scenario 52 (specs/governance/SPEC.md §12, GV02): the rule is checked on the run's net diff.
// The first implement adds migrations/x.sql and leaves the tests red; the second removes it
// again and fixes the code. The first check matches the rule, the second matches nothing.

func TestScenario52_RevertedFileIsNotInTheSecondCheck(t *testing.T) {
	repoDir := fakeexec.InitGitFixture(t)
	r := newQualityRun(t, repoDir, func(o *configOptions) { o.pathRules = migrationsNeedTesting })
	writeResponse(t, r.responses, "implement_1", `{"summary": "add a migration", "summary_short": "Add migration", "changed_files": ["migrations/x.sql"], "tests_added": []}`)
	writeResponse(t, r.responses, "implement_2", `{"summary": "drop the migration, fix Add", "summary_short": "Fix Add", "changed_files": ["fixture.go"], "tests_added": []}`)
	touchOnImplement(t, repoDir, "migrations/x.sql", 2)
	fixOnCall(t, 2)

	stdout, stderr, err := runOrchestrator(t, repoDir, r.cfg, append(implementTestReview, "add migrations/x.sql")...)
	mustSucceed(t, stdout, stderr, err)

	run := loadPathRulesRun(t, repoDir)
	var implements []pathRulesExecuted
	for _, e := range run.Executions {
		if e.Action == "implement" {
			implements = append(implements, e)
		}
	}
	if run.Status != "completed" || len(implements) != 2 {
		t.Fatalf("status %s, %d implements, want completed with 2", run.Status, len(implements))
	}
	first := implements[0].PathRules
	if len(first) != 1 || !equalStrings(first[0].Files, []string{"migrations/x.sql"}) || len(first[0].Missing) != 0 {
		t.Errorf("first check: path_rules = %+v, want the migration matched", first)
	}
	if second := implements[1].PathRules; len(second) != 0 {
		t.Errorf("second check: path_rules = %+v, want no match", second)
	}
	if _, statErr := os.Stat(filepath.Join(repoDir, "migrations", "x.sql")); !os.IsNotExist(statErr) {
		t.Errorf("the second implement did not remove migrations/x.sql: %v", statErr)
	}
}

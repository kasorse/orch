package integration

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/kasorse/orch/internal/apperr"
	"github.com/kasorse/orch/internal/exitcode"
	"github.com/kasorse/orch/internal/testutil/fakeexec"
)

// Scenario 49 (specs/governance/SPEC.md §12, GV02): a path rule requires testing, the workflow
// has no testing and the implement touches migrations/x.sql. The run warns at its start, the
// implement step fails and run.json records the missing stage.

func TestScenario49_PathRuleNeedsAStageTheWorkflowLacks(t *testing.T) {
	repoDir := fakeexec.InitGitFixture(t)
	r := newQualityRun(t, repoDir, func(o *configOptions) { o.pathRules = migrationsNeedTesting })
	neverFix(t)
	touchOnImplement(t, repoDir, "migrations/x.sql", 0)

	_, stderr, err := runOrchestrator(t, repoDir, r.cfg,
		"run", "--stage", "development", "--stage", "review", "add migrations/x.sql")
	want := `policy.paths "migrations/**" requires testing, which this workflow does not run (changed: migrations/x.sql)`
	var wfErr *apperr.WorkflowError
	if !errors.As(err, &wfErr) || exitcode.FromError(err) != exitcode.WorkflowOrLimit || !strings.Contains(err.Error(), want) {
		t.Fatalf("error = %T %v, want a WorkflowError (exit 4) with %q", err, err, want)
	}
	assertContains(t, "stderr", stderr,
		`Warning: policy.paths "migrations/**" requires testing; this workflow has no testing.`,
		"A change there will stop the run. Add the stage with --stage, or start a new run.")

	run := loadPathRulesRun(t, repoDir)
	if run.Status != "failed" || !strings.Contains(run.FailureReason, want) || len(run.Executions) != 1 {
		t.Fatalf("status %s, reason %q, %d executions", run.Status, run.FailureReason, len(run.Executions))
	}
	rules := run.Executions[0].PathRules
	if len(rules) != 1 || rules[0].Match != "migrations/**" || !equalStrings(rules[0].Files, []string{"migrations/x.sql"}) ||
		!equalStrings(rules[0].Missing, []string{"testing"}) {
		t.Errorf("path_rules = %+v", rules)
	}
	if got := invocations(t, r.invocationsLog); !equalStrings(got, []string{"implement"}) {
		t.Errorf("invocations = %v, want [implement]: the review must not run", got)
	}
	if _, statErr := os.Stat(filepath.Join(repoDir, "migrations", "x.sql")); statErr != nil {
		t.Errorf("the change stays on disk for inspection: %v", statErr)
	}
}

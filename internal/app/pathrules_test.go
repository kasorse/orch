package app

import (
	"bytes"
	"reflect"
	"testing"

	"github.com/kasorse/orch/internal/config"
	"github.com/kasorse/orch/internal/domain"
)

func workflowOf(actions ...domain.ActionType) domain.Workflow {
	var wf domain.Workflow
	for _, a := range actions {
		wf.Steps = append(wf.Steps, domain.Step{Action: a})
	}
	return wf
}

func TestPathRuleWarnings(t *testing.T) {
	migrations := domain.PathRule{Match: "migrations/**", RequireStages: []domain.Stage{domain.StageReview, domain.StageTesting}}
	approvalOnly := domain.PathRule{Match: "**/*.sql", Approval: "manual"}
	rules := []domain.PathRule{migrations, approvalOnly}

	got := pathRuleWarnings(rules, workflowOf(domain.ActionImplement, domain.ActionReview))
	want := []string{"policy.paths \"migrations/**\" requires review, testing; this workflow has no testing.\n" +
		"A change there will stop the run. Add the stage with --stage, or start a new run."}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("warnings = %q, want %q", got, want)
	}

	if got := pathRuleWarnings(rules, workflowOf(domain.ActionImplement, domain.ActionTest, domain.ActionReview)); len(got) != 0 {
		t.Errorf("a workflow with every stage: %q", got)
	}
	// Without implement no file changes, so there is nothing to warn about.
	if got := pathRuleWarnings(rules, workflowOf(domain.ActionReview)); len(got) != 0 {
		t.Errorf("a workflow without implement: %q", got)
	}
	if got := pathRuleWarnings(nil, workflowOf(domain.ActionImplement)); len(got) != 0 {
		t.Errorf("no rules: %q", got)
	}

	var stderr bytes.Buffer
	warnPathRules(&stderr, rules, workflowOf(domain.ActionImplement))
	if wantOut := "Warning: " + pathRuleWarnings(rules, workflowOf(domain.ActionImplement))[0] + "\n"; stderr.String() != wantOut {
		t.Errorf("stderr = %q, want %q", stderr.String(), wantOut)
	}
}

func TestRunOptions_PathRules(t *testing.T) {
	var cfg config.Config
	if got := runOptions(cfg); got.PathRules != nil {
		t.Errorf("no policy.paths: %#v, want nil", got.PathRules)
	}
	cfg.Policy.Paths = []config.PathRuleConfig{{Match: "migrations/**", RequireStages: []string{"testing"}, Approval: "manual"}}
	want := []domain.PathRule{{Match: "migrations/**", RequireStages: []domain.Stage{domain.StageTesting}, Approval: "manual"}}
	if got := runOptions(cfg); !reflect.DeepEqual(got.PathRules, want) {
		t.Errorf("PathRules = %+v, want %+v", got.PathRules, want)
	}
}

func TestPathRulesChanged(t *testing.T) {
	a := []domain.PathRule{{Match: "a/**", RequireStages: []domain.Stage{domain.StageReview}}}
	rule := func(match string, approval string, stages ...domain.Stage) []domain.PathRule {
		return []domain.PathRule{{Match: match, RequireStages: stages, Approval: approval}}
	}
	for _, tc := range []struct {
		name              string
		recorded, current []domain.PathRule
		want              bool
	}{
		{"both none", nil, nil, false},
		{"nil and empty", nil, []domain.PathRule{}, false},
		{"same", a, rule("a/**", "", domain.StageReview), false},
		{"rule added", nil, a, true},
		{"rule removed", a, nil, true},
		{"another pattern", a, rule("b/**", "", domain.StageReview), true},
		{"another stage", a, rule("a/**", "", domain.StageTesting), true},
		{"approval added", a, rule("a/**", "manual", domain.StageReview), true},
		{"stages in another order", rule("a/**", "", domain.StageReview, domain.StageTesting),
			rule("a/**", "", domain.StageTesting, domain.StageReview), true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := pathRulesChanged(tc.recorded, tc.current); got != tc.want {
				t.Errorf("pathRulesChanged = %v, want %v", got, tc.want)
			}
		})
	}
}

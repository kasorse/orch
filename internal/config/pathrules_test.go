package config

import (
	"reflect"
	"strings"
	"testing"

	"orchestrator/internal/domain"
)

const thresholdsLine = "    thresholds: {research: 0.70, analysis: 0.60, planning: 0.65, development: 0.60, testing: 0.60, review: 0.70}\n"

// withPaths is validV2 with the given policy.paths entries (YAML list items, indented four
// spaces under "paths:").
func withPaths(items string) string {
	return strings.Replace(validV2(""), thresholdsLine, thresholdsLine+"  paths:\n"+items, 1)
}

func TestLoad_PathRules(t *testing.T) {
	cfg, err := Load(writeTempConfig(t, withPaths(`    - match: "migrations/**"
      require_stages: [review, testing]
      approval: manual
    - match: "**/*.sql"
      approval: manual
    - match: "docs/**"
      require_stages: [review]
`)))
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if err := Validate(cfg); err != nil {
		t.Fatalf("Validate: %v", err)
	}
	want := []domain.PathRule{
		{Match: "migrations/**", RequireStages: []domain.Stage{domain.StageReview, domain.StageTesting}, Approval: "manual"},
		{Match: "**/*.sql", Approval: "manual"},
		{Match: "docs/**", RequireStages: []domain.Stage{domain.StageReview}},
	}
	if got := cfg.PathRules(); !reflect.DeepEqual(got, want) {
		t.Errorf("PathRules = %+v, want %+v", got, want)
	}
}

func TestLoad_NoPathRules(t *testing.T) {
	cfg, err := Load(writeTempConfig(t, validV2("")))
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if got := cfg.PathRules(); got != nil {
		t.Errorf("PathRules = %#v, want nil", got)
	}
}

func TestValidate_PathRuleErrors(t *testing.T) {
	for name, tc := range map[string]struct {
		items string
		want  string
	}{
		"empty rule":      {"    - match: \"migrations/**\"\n", "policy.paths[0] is empty"},
		"unknown stage":   {"    - match: \"a/**\"\n      require_stages: [qa]\n", "policy.paths[0].require_stages[0]: unsupported stage \"qa\""},
		"duplicate stage": {"    - match: \"a/**\"\n      require_stages: [review, review]\n", "policy.paths[0].require_stages[1]: duplicate stage \"review\""},
		"bad pattern":     {"    - match: \"[\"\n      approval: manual\n", "policy.paths[0].match \"[\""},
		"absolute":        {"    - match: \"/etc/**\"\n      approval: manual\n", "policy.paths[0].match \"/etc/**\": must be relative"},
		"no match":        {"    - approval: manual\n", "policy.paths[0].match \"\": must not be empty"},
		"approval auto":   {"    - match: \"a/**\"\n      approval: auto\n", "policy.paths[0].approval must be \"manual\" or absent, got \"auto\""},
		"deny_change": {
			"    - match: \"a/**\"\n      require_stages: [review]\n    - match: \"secrets/**\"\n      deny_change: true\n",
			"policy.paths[1].deny_change: use security.protected_paths: the guard already refuses any change there, git-ignored files included",
		},
	} {
		t.Run(name, func(t *testing.T) {
			cfg, err := Load(writeTempConfig(t, withPaths(tc.items)))
			if err != nil {
				t.Fatalf("Load: %v", err)
			}
			err = Validate(cfg)
			if err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Errorf("Validate = %v, want it to contain %q", err, tc.want)
			}
		})
	}
}

// An unknown key inside a rule is still refused by strict decoding.
func TestLoad_PathRuleUnknownKey(t *testing.T) {
	_, err := Load(writeTempConfig(t, withPaths("    - match: \"a/**\"\n      require: [review]\n")))
	if err == nil || !strings.Contains(err.Error(), "require") {
		t.Errorf("Load = %v, want an unknown-key error naming require", err)
	}
}

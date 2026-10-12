package persistence

import (
	"strings"
	"testing"

	"orchestrator/internal/domain"
)

// org_config (specs/governance/SPEC.md §6.3): an absolute, clean path and a 64-digit
// lowercase hex sha256, on the run and on each resume; a run written before the field
// existed has neither.
func TestValidateRun_OrgConfig(t *testing.T) {
	good := &domain.OrgConfigRef{Path: "/etc/orchestrator/org.yaml", SHA256: strings.Repeat("ab", 32)}
	withRun := func(ref *domain.OrgConfigRef) *domain.Run {
		return &domain.Run{ID: "r", Status: domain.RunFailed, OrgConfig: ref}
	}
	withResume := func(ref *domain.OrgConfigRef) *domain.Run {
		return &domain.Run{ID: "r", Status: domain.RunFailed, Resumes: []domain.Resume{
			{FromStatus: domain.RunFailed, Entry: domain.ActionImplement, OrgConfig: ref}}}
	}

	for name, run := range map[string]*domain.Run{
		"an old run without the field": withRun(nil),
		"a run with an org file":       withRun(good),
		"a resume without the field":   withResume(nil),
		"a resume with an org file":    withResume(good),
	} {
		if err := validateRun(run, "r"); err != nil {
			t.Errorf("%s: %v", name, err)
		}
	}

	for name, ref := range map[string]*domain.OrgConfigRef{
		"relative path":    {Path: "org.yaml", SHA256: good.SHA256},
		"unclean path":     {Path: "/etc/../etc/org.yaml", SHA256: good.SHA256},
		"empty path":       {SHA256: good.SHA256},
		"short sha256":     {Path: good.Path, SHA256: "abcd"},
		"uppercase sha256": {Path: good.Path, SHA256: strings.Repeat("AB", 32)},
		"non-hex sha256":   {Path: good.Path, SHA256: strings.Repeat("zz", 32)},
		"empty sha256":     {Path: good.Path},
	} {
		if err := validateRun(withRun(ref), "r"); err == nil || !strings.Contains(err.Error(), "org_config:") {
			t.Errorf("run, %s: got %v, want an org_config error", name, err)
		}
		if err := validateRun(withResume(ref), "r"); err == nil || !strings.Contains(err.Error(), "resumes[0].org_config:") {
			t.Errorf("resume, %s: got %v, want a resumes[0].org_config error", name, err)
		}
	}
}

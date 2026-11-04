package app

import (
	"strings"
	"testing"

	"github.com/kasorse/orch/internal/config"
	"github.com/kasorse/orch/internal/domain"
)

func TestOrgRef(t *testing.T) {
	if got := orgRef(config.OrgSource{}); got != nil {
		t.Errorf("no org file: orgRef = %+v, want nil", got)
	}
	got := orgRef(config.OrgSource{Path: "/etc/orchestrator/org.yaml", SHA256: "ab"})
	if got == nil || got.Path != "/etc/orchestrator/org.yaml" || got.SHA256 != "ab" {
		t.Errorf("orgRef = %+v", got)
	}
}

func TestOrgChangeWarning(t *testing.T) {
	a := &domain.OrgConfigRef{Path: "/o.yaml", SHA256: strings.Repeat("a", 64)}
	b := &domain.OrgConfigRef{Path: "/o.yaml", SHA256: strings.Repeat("b", 64)}
	moved := &domain.OrgConfigRef{Path: "/other/o.yaml", SHA256: a.SHA256}

	for name, tc := range map[string]struct {
		started, now *domain.OrgConfigRef
		want         string
	}{
		"no org file either time": {nil, nil, ""},
		"the same file":           {a, a, ""},
		"the same content":        {a, moved, ""},
		"changed content":         {a, b, "org config changed since the run started (" + a.SHA256 + " → " + b.SHA256 + "); this resume runs under the current one"},
		"org file appeared":       {nil, a, "org config changed since the run started (none → " + a.SHA256 + "); this resume runs under the current one"},
		"org file disappeared":    {a, nil, "org config changed since the run started (" + a.SHA256 + " → none); this resume runs under the current one"},
	} {
		if got := orgChangeWarning(tc.started, tc.now); got != tc.want {
			t.Errorf("%s: warning = %q, want %q", name, got, tc.want)
		}
	}
}

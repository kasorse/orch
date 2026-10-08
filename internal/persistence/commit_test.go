package persistence

import (
	"strings"
	"testing"

	"orchestrator/internal/domain"
)

func TestValidateRun_Commit(t *testing.T) {
	sha := strings.Repeat("ab", 20)
	cases := []struct {
		name   string
		status domain.RunStatus
		commit string
		ok     bool
	}{
		{"none", domain.RunFailed, "", true},
		{"completed run", domain.RunCompleted, sha, true},
		{"sha256", domain.RunCompleted, strings.Repeat("0f", 32), true},
		{"abbreviated", domain.RunCompleted, sha[:12], false},
		{"uppercase", domain.RunCompleted, strings.ToUpper(sha), false},
		{"a branch name", domain.RunCompleted, "main", false},
		{"failed run", domain.RunFailed, sha, false},
	}
	for _, c := range cases {
		err := validateRun(&domain.Run{ID: "r", Status: c.status, Commit: c.commit}, "r")
		if c.ok && err != nil {
			t.Errorf("%s: validateRun() = %v, want nil", c.name, err)
		}
		if !c.ok && (err == nil || !strings.Contains(err.Error(), "commit")) {
			t.Errorf("%s: validateRun() = %v, want a commit error", c.name, err)
		}
	}
}

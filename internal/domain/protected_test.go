package domain

import "testing"

func TestMatchProtected(t *testing.T) {
	cases := []struct {
		pattern, path string
		want          bool
	}{
		{".claude/**", ".claude/settings.json", true},
		{".claude/**", ".claude/hooks/pre.sh", true},
		{".claude/**", "sub/.claude/settings.json", true},
		{".claude/**", ".claude", false}, // a directory itself is never a changed file
		{".claude/**", "claude/settings.json", false},
		{".mcp.json", ".mcp.json", true},
		{".mcp.json", "pkg/.mcp.json", true},
		{".mcp.json", ".mcp.json.bak", false},
		{".github/workflows/**", ".github/workflows/ci.yml", true},
		{".github/workflows/**", ".github/CODEOWNERS", false},
		{"orchestrator.yaml", "orchestrator.yaml", true},
		{"*.lock", "go.sum", false},
		{"*.lock", "web/yarn.lock", true},
		{"deploy/*/values.yaml", "deploy/prod/values.yaml", true},
		{"src/**", "src/a/b.go", true},
	}
	for _, tc := range cases {
		if got := MatchProtected(tc.pattern, tc.path); got != tc.want {
			t.Errorf("MatchProtected(%q, %q) = %v, want %v", tc.pattern, tc.path, got, tc.want)
		}
	}
}

func TestDefaultProtectedPathsAreValid(t *testing.T) {
	for _, p := range DefaultProtectedPaths {
		if err := ValidateProtectedPattern(p); err != nil {
			t.Errorf("%q: %v", p, err)
		}
	}
}

func TestValidateProtectedPattern(t *testing.T) {
	for _, bad := range []string{"", "  ", "/etc/passwd", "[", "a/[/**"} {
		if ValidateProtectedPattern(bad) == nil {
			t.Errorf("ValidateProtectedPattern(%q) = nil, want an error", bad)
		}
	}
	for _, good := range []string{"go.mod", "deploy/**", "*.lock"} {
		if err := ValidateProtectedPattern(good); err != nil {
			t.Errorf("ValidateProtectedPattern(%q) = %v", good, err)
		}
	}
}

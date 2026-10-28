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
		// A leading "/" matches from the repository root only (TODO.md S9).
		{"/.gitlab-ci.yml", ".gitlab-ci.yml", true},
		{"/.gitlab-ci.yml", "examples/ci/.gitlab-ci.yml", false},
		{"/.claude/**", ".claude/settings.json", true},
		{"/.claude/**", "sub/.claude/settings.json", false},
		{"/deploy/*/values.yaml", "deploy/prod/values.yaml", true},
		{"/deploy/*/values.yaml", "app/deploy/prod/values.yaml", false},
	}
	for _, tc := range cases {
		if got := MatchProtected(tc.pattern, tc.path); got != tc.want {
			t.Errorf("MatchProtected(%q, %q) = %v, want %v", tc.pattern, tc.path, got, tc.want)
		}
	}
}

func TestValidateProtectedPattern(t *testing.T) {
	for _, bad := range []string{"", "  ", "/", "/ ", "//etc/passwd", "[", "a/[/**", "/["} {
		if ValidateProtectedPattern(bad) == nil {
			t.Errorf("ValidateProtectedPattern(%q) = nil, want an error", bad)
		}
	}
	for _, good := range []string{"go.mod", "deploy/**", "*.lock", "/.gitlab-ci.yml", "/.claude/**"} {
		if err := ValidateProtectedPattern(good); err != nil {
			t.Errorf("ValidateProtectedPattern(%q) = %v", good, err)
		}
	}
}

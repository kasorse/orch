package domain

import (
	"errors"
	"path"
	"strings"
)

// DefaultProtectedPaths are files an implement step must never change, because a write to
// them turns into execution later: agent settings and hooks, MCP servers, CI pipelines,
// direnv, editor tasks (specs/permissions/SPEC.md §7.3). The orchestrator's own config
// file is added at run time, and security.protected_paths extends the list. .git is
// guarded separately, by fingerprint - git status never reports it.
var DefaultProtectedPaths = []string{
	".claude/**",
	".mcp.json",
	".github/workflows/**",
	".gitlab-ci.yml",
	".envrc",
	".vscode/**",
}

// MatchProtected reports whether the repository-relative, slash-separated path p is
// covered by pattern. A pattern ending in "/**" covers everything under that directory;
// any other pattern is a path.Match glob against the whole remaining path. Patterns match
// at any depth - ".claude/**" also covers "sub/.claude/settings.json" - which errs on the
// side of protecting.
func MatchProtected(pattern, p string) bool {
	segs := strings.Split(p, "/")
	for i := range segs {
		if matchAt(pattern, segs[i:]) {
			return true
		}
	}
	return false
}

func matchAt(pattern string, segs []string) bool {
	if dir, ok := strings.CutSuffix(pattern, "/**"); ok {
		dirSegs := strings.Split(dir, "/")
		if len(segs) <= len(dirSegs) {
			return false
		}
		ok, _ := path.Match(dir, strings.Join(segs[:len(dirSegs)], "/"))
		return ok
	}
	ok, _ := path.Match(pattern, strings.Join(segs, "/"))
	return ok
}

// ValidateProtectedPattern rejects a pattern MatchProtected cannot use: empty, absolute,
// or not a valid glob.
func ValidateProtectedPattern(pattern string) error {
	switch {
	case strings.TrimSpace(pattern) == "":
		return errors.New("must not be empty")
	case strings.HasPrefix(pattern, "/"):
		return errors.New("must be relative to the repository root")
	}
	glob, _ := strings.CutSuffix(pattern, "/**")
	if _, err := path.Match(glob, ""); err != nil {
		return err
	}
	return nil
}

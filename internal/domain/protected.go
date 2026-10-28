package domain

import (
	"errors"
	"path"
	"strings"
)

// MatchProtected reports whether the repository-relative, slash-separated path p is
// covered by pattern. A pattern ending in "/**" covers everything under that directory;
// any other pattern is a path.Match glob against the whole remaining path. Patterns match
// at any depth - ".claude/**" also covers "sub/.claude/settings.json" - which errs on the
// side of protecting; a pattern starting with "/" matches only from the repository root -
// "/.gitlab-ci.yml" is not "examples/ci/.gitlab-ci.yml" (TODO.md S9).
func MatchProtected(pattern, p string) bool {
	segs := strings.Split(p, "/")
	if rooted, ok := strings.CutPrefix(pattern, "/"); ok {
		return matchAt(rooted, segs)
	}
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

// ValidateProtectedPattern rejects a pattern MatchProtected cannot use: empty, nothing after
// the leading "/" that anchors it at the root, or not a valid glob.
func ValidateProtectedPattern(pattern string) error {
	rooted, anchored := strings.CutPrefix(pattern, "/")
	switch {
	case strings.TrimSpace(pattern) == "":
		return errors.New("must not be empty")
	case anchored && (strings.TrimSpace(rooted) == "" || strings.HasPrefix(rooted, "/")):
		return errors.New(`a leading "/" anchors the pattern at the repository root and must be followed by a path`)
	}
	pattern = rooted
	glob, _ := strings.CutSuffix(pattern, "/**")
	if _, err := path.Match(glob, ""); err != nil {
		return err
	}
	return nil
}

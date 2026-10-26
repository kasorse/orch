package domain

import (
	"errors"
	"path"
	"strings"
)

// ValidateGlob rejects a path glob MatchGlob cannot use: empty, absolute, or not a valid
// path.Match pattern in some segment.
func ValidateGlob(pattern string) error {
	switch {
	case strings.TrimSpace(pattern) == "":
		return errors.New("must not be empty")
	case strings.HasPrefix(pattern, "/"):
		return errors.New("must be relative to the repository root")
	}
	for _, seg := range strings.Split(pattern, "/") {
		if _, err := path.Match(seg, ""); err != nil {
			return err
		}
	}
	return nil
}

// MatchGlob reports whether the slash-separated, repository-relative path p matches the
// whole of pattern. A segment "**" matches any number of segments, none included, so
// "**/*.md" matches "README.md" and "docs/a/b.md"; the other segments are path.Match globs.
// The match is case-sensitive.
func MatchGlob(pattern, p string) bool {
	return matchSegments(strings.Split(pattern, "/"), strings.Split(p, "/"))
}

func matchSegments(pat, segs []string) bool {
	for len(pat) > 0 {
		if pat[0] == "**" {
			for len(pat) > 1 && pat[1] == "**" {
				pat = pat[1:]
			}
			if len(pat) == 1 {
				return len(segs) > 0
			}
			for i := 0; i <= len(segs); i++ {
				if matchSegments(pat[1:], segs[i:]) {
					return true
				}
			}
			return false
		}
		if len(segs) == 0 {
			return false
		}
		if ok, _ := path.Match(pat[0], segs[0]); !ok {
			return false
		}
		pat, segs = pat[1:], segs[1:]
	}
	return len(segs) == 0
}

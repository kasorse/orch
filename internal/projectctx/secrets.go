package projectctx

import (
	"bytes"
	"os"
	"path"
	"path/filepath"
	"strings"
)

// defaultDeny names files that must never be read or mentioned (SPEC.md §18). Patterns
// are path.Match globs, compared case-insensitively.
var defaultDeny = []string{".env", ".env.*", "*.pem", "*.key", "credentials*", "secrets*"}

// skipDirs are never part of project context: VCS internals and the orchestrator's own
// run artifacts, which hold previous requests and model output.
var skipDirs = map[string]bool{".git": true, ".orchestrator": true}

const (
	maxFileSize   = 256 << 10
	binarySniffSz = 8 << 10
)

// denyList filters paths before anything is read. A pattern without a slash is matched
// against every path segment, not only the basename: a directory named secrets/ is as
// sensitive as a file named secrets.yaml. A pattern with a slash (context.extra_deny)
// is matched against the whole path, and against every leading directory of it.
type denyList struct {
	segment []string
	full    []string
}

// DenyPatterns returns the secret deny list - the built-in patterns plus extra
// (context.extra_deny), normalized and lowercased. It is the one list both the Context
// Manager and the provider calls' read deny rules (provider.Request.DenyRead) come from.
func DenyPatterns(extra []string) []string {
	var out []string
	for _, p := range append(append([]string(nil), defaultDeny...), extra...) {
		p = strings.ToLower(strings.Trim(strings.TrimSpace(p), "/"))
		if p != "" {
			out = append(out, p)
		}
	}
	return out
}

// DenyMatcher reports whether a repository-relative, slash-separated path matches the
// secret deny list (built-in patterns plus extra), exactly as the Context Manager decides
// it - for callers outside this package, such as the review diff, which must withhold the
// same files.
func DenyMatcher(extra []string) func(rel string) bool {
	return newDenyList(extra).denied
}

func newDenyList(extra []string) denyList {
	var d denyList
	for _, p := range DenyPatterns(extra) {
		if strings.Contains(p, "/") {
			d.full = append(d.full, p)
		} else {
			d.segment = append(d.segment, p)
		}
	}
	return d
}

// denied reports whether rel (slash-separated, relative to the repository root) must be
// skipped.
func (d denyList) denied(rel string) bool {
	rel = strings.ToLower(rel)
	segments := strings.Split(rel, "/")
	for i, seg := range segments {
		if skipDirs[seg] && i < len(segments)-1 {
			return true
		}
		for _, p := range d.segment {
			if ok, _ := path.Match(p, seg); ok {
				return true
			}
		}
	}
	for _, p := range d.full {
		for i := range segments {
			if ok, _ := path.Match(p, strings.Join(segments[:i+1], "/")); ok {
				return true
			}
		}
	}
	return false
}

// readText reads a repository file for context, or reports false for anything that
// must not be sent: missing, not a regular file, larger than maxFileSize, or binary (a
// NUL byte in the first binarySniffSz bytes). Callers must check denied first.
func readText(root, rel string) (string, bool) {
	full := filepath.Join(root, filepath.FromSlash(rel))
	info, err := os.Lstat(full)
	if err != nil || !info.Mode().IsRegular() || info.Size() > maxFileSize {
		return "", false
	}
	data, err := os.ReadFile(full)
	if err != nil {
		return "", false
	}
	if bytes.IndexByte(data[:min(len(data), binarySniffSz)], 0) >= 0 {
		return "", false
	}
	return string(data), true
}

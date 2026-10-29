// Package listener is the pure core of the Jira listener (specs/jira-listener/SPEC.md): what a
// ticket's labels and components name, and whether a project is the one a checkout holds. It runs
// no git, starts no process and opens no file; it imports only domain and the standard library
// (imports_test.go).
package listener

import (
	"errors"
	"fmt"
	"net/url"
	"regexp"
	"slices"
	"strings"
)

// What a ticket names wrongly. The loop maps each to blocked (SPEC §5, §8); the message of the
// error carries the names, so the comment can quote it.
var (
	ErrNoService      = errors.New("no service named")
	ErrManyServices   = errors.New("several services named")
	ErrInvalidService = errors.New("invalid service name")
	ErrManyProjects   = errors.New("several projects named")
	ErrInvalidProject = errors.New("invalid project path")
)

// maxServiceName bounds a service name, as story.ValidRepo does.
const maxServiceName = 64

// ValidService reports whether name has the shape of a service name: the rule of story.ValidRepo
// (this package cannot import story; a test in app keeps the two equal).
func ValidService(name string) bool {
	if name == "" || len(name) > maxServiceName {
		return false
	}
	for i := 0; i < len(name); i++ {
		c := name[i]
		alnum := (c >= 'A' && c <= 'Z') || (c >= 'a' && c <= 'z') || (c >= '0' && c <= '9')
		if i == 0 && !alnum {
			return false
		}
		if !alnum && c != '.' && c != '_' && c != '-' {
			return false
		}
	}
	return true
}

// Service is the one service a ticket names: every label prefix<name>, and every component whose
// name equals (case folded) a directory in dirs, which counts as that directory's name. Exactly
// one distinct name (case folded; the first spelling wins, labels before components) is a
// service, and it must be a valid name. None, several (the error names them all, sorted) or an
// invalid one is an error. A component that matches no directory names nothing.
func Service(labels, components, dirs []string, prefix string) (string, error) {
	var names []string
	add := func(name string) {
		if !slices.ContainsFunc(names, func(n string) bool { return strings.EqualFold(n, name) }) {
			names = append(names, name)
		}
	}
	if prefix != "" {
		for _, l := range labels {
			if name, ok := strings.CutPrefix(l, prefix); ok {
				if !ValidService(name) {
					return "", fmt.Errorf("%w: %q is not a service name (letters, digits, . _ -, at most %d characters, starting with a letter or digit)",
						ErrInvalidService, name, maxServiceName)
				}
				add(name)
			}
		}
	}
	for _, c := range components {
		for _, d := range dirs {
			if strings.EqualFold(c, d) {
				if !ValidService(d) {
					return "", fmt.Errorf("%w: the directory %q is not a service name", ErrInvalidService, d)
				}
				add(d)
				break
			}
		}
	}
	switch len(names) {
	case 0:
		return "", fmt.Errorf("%w: label the ticket %s<name>", ErrNoService, prefix)
	case 1:
		return names[0], nil
	}
	sorted := slices.Clone(names)
	slices.Sort(sorted)
	return "", fmt.Errorf("%w: %s", ErrManyServices, strings.Join(sorted, ", "))
}

var projectPattern = regexp.MustCompile(`^[A-Za-z0-9_.-]+(/[A-Za-z0-9_.-]+)+$`)

// Project is the project a ticket's label prefix<group>/<path> names, with a ".git" suffix
// stripped, or "" when it has no such label. At most one distinct project; the path is segments of
// letters, digits, _ . -, at least two, none of them "." or "..".
func Project(labels []string, prefix string) (string, error) {
	if prefix == "" {
		return "", nil
	}
	var found []string
	for _, l := range labels {
		raw, ok := strings.CutPrefix(l, prefix)
		if !ok {
			continue
		}
		path := strings.TrimSuffix(raw, ".git")
		if !projectPattern.MatchString(path) || hasDotSegment(path) {
			return "", fmt.Errorf("%w: %q is not <group>/<project> of letters, digits, . _ - (no . or .. segment)", ErrInvalidProject, raw)
		}
		if !slices.Contains(found, path) {
			found = append(found, path)
		}
	}
	switch len(found) {
	case 0:
		return "", nil
	case 1:
		return found[0], nil
	}
	slices.Sort(found)
	return "", fmt.Errorf("%w: %s", ErrManyProjects, strings.Join(found, ", "))
}

func hasDotSegment(p string) bool {
	return slices.ContainsFunc(strings.Split(p, "/"), func(s string) bool { return s == "." || s == ".." })
}

// Allowed reports whether the project path is under one of the groups, by whole segments:
// "backend" allows "backend/x" and "backend/x/y", not "backend-old/x" and not "backend" itself.
// A path with an empty, "." or ".." segment is never allowed.
func Allowed(path string, groups []string) bool {
	if path == "" {
		return false
	}
	for _, seg := range strings.Split(path, "/") {
		if seg == "" || seg == "." || seg == ".." {
			return false
		}
	}
	for _, g := range groups {
		g = strings.Trim(g, "/")
		if g != "" && strings.HasPrefix(path, g+"/") {
			return true
		}
	}
	return false
}

// SameProject reports whether originURL names the project path on baseURL: the same scheme, host
// and port (a default port is the same as none), and the path baseURL's own path followed by
// path, with an optional trailing "/" or ".git". Scheme and host are compared folded, the path
// as it is. An origin with credentials, a query or a fragment, or one that is no URL (an scp-like
// address), is not the project.
func SameProject(originURL, baseURL, path string) bool {
	origin, ok := parseRemote(originURL)
	if !ok || origin.User != nil || origin.RawQuery != "" || origin.Fragment != "" {
		return false
	}
	base, ok := parseRemote(baseURL)
	if !ok {
		return false
	}
	if endpoint(origin) != endpoint(base) {
		return false
	}
	want := strings.Trim(base.Path, "/") + "/" + strings.Trim(path, "/")
	want = strings.TrimPrefix(want, "/")
	got := strings.TrimSuffix(strings.TrimRight(origin.Path, "/"), ".git")
	got = strings.TrimPrefix(got, "/")
	return got == want
}

func parseRemote(raw string) (*url.URL, bool) {
	u, err := url.Parse(raw)
	if err != nil || u.Scheme == "" {
		return nil, false
	}
	return u, true
}

// endpoint is scheme://host:port with the scheme's default port filled in.
func endpoint(u *url.URL) string {
	scheme := strings.ToLower(u.Scheme)
	port := u.Port()
	if port == "" {
		port = DefaultPort(scheme)
	}
	return scheme + "://" + strings.ToLower(u.Hostname()) + ":" + port
}

// DefaultPort is the port a URL scheme uses when it names none, or "".
func DefaultPort(scheme string) string {
	switch strings.ToLower(scheme) {
	case "https":
		return "443"
	case "http":
		return "80"
	case "ssh":
		return "22"
	}
	return ""
}

// Endpoint normalises a protocol and a host[:port] as git's credential protocol gives them (or a
// URL's scheme and host) into one comparable string: lower case, the default port made explicit.
// ok is false for an empty or malformed host.
func Endpoint(protocol, hostport string) (string, bool) {
	if protocol == "" || hostport == "" || strings.ContainsAny(hostport, "/@?# \t") {
		return "", false
	}
	u, err := url.Parse(protocol + "://" + hostport)
	if err != nil || u.Hostname() == "" {
		return "", false
	}
	return endpoint(u), true
}

package app

import (
	"context"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strings"

	"orchestrator/internal/execution"
	"orchestrator/internal/story"
)

// serviceNameRE is the name a service's directory must have (specs/story/SPEC.md §7.1), which
// also keeps a name usable in a branch and in a message.
var serviceNameRE = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._-]{0,63}$`)

// serviceCandidate is one directory that could be a story's service.
type serviceCandidate struct {
	Name string
	Path string
}

// candidates lists the services a story could use (SPEC.md §7.1 point 1): every immediate
// subdirectory of every root that has a .git entry and a valid name, and the story home, once
// by resolved path. Only os.ReadDir and os.Lstat are used; no git runs over directories the
// story does not name. roots are the resolved roots (config.StoryConfig.ResolveRoots).
func candidates(roots []string, home string) ([]serviceCandidate, error) {
	var out []serviceCandidate
	seen := map[string]bool{}
	add := func(name, path string) {
		if !serviceNameRE.MatchString(name) {
			return
		}
		if _, err := os.Lstat(filepath.Join(path, ".git")); err != nil {
			return
		}
		out = append(out, serviceCandidate{Name: name, Path: path})
	}

	for _, root := range roots {
		entries, err := os.ReadDir(root)
		if err != nil {
			return nil, fmt.Errorf("story root %s: %w", root, err)
		}
		for _, e := range entries {
			path := filepath.Join(root, e.Name())
			if !e.IsDir() && e.Type()&fs.ModeSymlink == 0 {
				continue
			}
			key := path
			if e.Type()&fs.ModeSymlink != 0 {
				resolved, err := filepath.EvalSymlinks(path)
				if err != nil {
					continue
				}
				if info, err := os.Stat(resolved); err != nil || !info.IsDir() {
					continue
				}
				key = resolved
			}
			seen[key] = true
			add(e.Name(), path)
		}
	}

	if home != "" {
		resolved, err := filepath.EvalSymlinks(home)
		if err != nil {
			resolved = filepath.Clean(home)
		}
		if !seen[resolved] {
			add(filepath.Base(resolved), resolved)
		}
	}
	return out, nil
}

// findServices returns the services a task names (SPEC.md §7.1 points 2-4), in candidate order:
// those story.Named finds in request and the Jira components, or exactly the --repo names. It
// refuses before any model call: none named, more than max, a name under two roots, and a
// named directory that is not the top level of its git repository. git runs only for the
// named ones.
func findServices(ctx context.Context, executor execution.ProcessExecutor, request string, components, repos []string,
	cands []serviceCandidate, roots []string, max int) ([]serviceCandidate, error) {
	names := make([]string, 0, len(cands))
	for _, c := range cands {
		if !slices.Contains(names, c.Name) {
			names = append(names, c.Name)
		}
	}

	var named []string
	if len(repos) > 0 {
		for _, r := range repos {
			if !slices.Contains(names, r) {
				return nil, fmt.Errorf("--repo %s is not a service under story.roots (%s); candidates: %s",
					r, strings.Join(roots, ", "), listOrNone(names))
			}
			if !slices.Contains(named, r) {
				named = append(named, r)
			}
		}
		// Candidate order, so the result does not depend on the order of the flags.
		named = slices.DeleteFunc(slices.Clone(names), func(n string) bool { return !slices.Contains(named, n) })
	} else {
		named = story.Named(request, components, names)
	}

	switch {
	case len(named) == 0:
		return nil, fmt.Errorf("the task names no service under story.roots (%s); name them in the task, "+
			`e.g. "Сервисы: billing, gateway", or pass --repo`, strings.Join(roots, ", "))
	case len(named) > max:
		return nil, fmt.Errorf("the task names %d services (%s), more than story.max_services (%d); narrow it with --repo",
			len(named), strings.Join(named, ", "), max)
	}

	var out []serviceCandidate
	for _, name := range named {
		var paths []string
		var found serviceCandidate
		for _, c := range cands {
			if c.Name == name {
				paths = append(paths, c.Path)
				found = c
			}
		}
		if len(paths) > 1 {
			return nil, fmt.Errorf("service %s is under more than one root: %s; remove the ambiguity in story.roots",
				name, strings.Join(paths, ", "))
		}
		if err := checkTopLevel(ctx, executor, found); err != nil {
			return nil, err
		}
		out = append(out, found)
	}
	return out, nil
}

// checkTopLevel refuses a directory that is inside another repository rather than being the top
// level of its own (a .git entry it has is then not what git reports).
func checkTopLevel(ctx context.Context, executor execution.ProcessExecutor, c serviceCandidate) error {
	res, err := executor.Run(ctx, execution.Spec{Executable: "git", Args: []string{"rev-parse", "--show-toplevel"}, WorkingDir: c.Path})
	if err != nil {
		return fmt.Errorf("service %s (%s): failed to run git: %w", c.Name, c.Path, err)
	}
	if res.ExitCode != 0 {
		return fmt.Errorf("service %s (%s) is not a git repository: %s", c.Name, c.Path, strings.TrimSpace(res.Stderr))
	}
	top := strings.TrimSpace(res.Stdout)
	if !samePath(top, c.Path) {
		return fmt.Errorf("service %s (%s) is not the top level of its repository (git reports %s)", c.Name, c.Path, top)
	}
	return nil
}

// samePath compares two directories after resolving symlinks, since git prints the real path.
func samePath(a, b string) bool {
	ra, err := filepath.EvalSymlinks(a)
	if err != nil {
		ra = filepath.Clean(a)
	}
	rb, err := filepath.EvalSymlinks(b)
	if err != nil {
		rb = filepath.Clean(b)
	}
	return ra == rb
}

func listOrNone(names []string) string {
	if len(names) == 0 {
		return "none"
	}
	return strings.Join(names, ", ")
}

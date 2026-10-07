package mine

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os/exec"
	"path"
	"regexp"
	"slices"
	"strings"
	"time"
)

// git reads a repository. It never writes: no checkout, no worktree, no fetch.
type git struct {
	repo    string
	verbose io.Writer
}

// run is `git -C <repo> args...`; its error carries git's stderr.
func (g *git) run(ctx context.Context, args ...string) (string, error) {
	full := append([]string{"-C", g.repo}, args...)
	if g.verbose != nil {
		fmt.Fprintf(g.verbose, "+ git %s\n", strings.Join(full, " "))
	}
	cmd := exec.CommandContext(ctx, "git", full...)
	var stdout, stderr bytes.Buffer
	cmd.Stdout, cmd.Stderr = &stdout, &stderr
	if err := cmd.Run(); err != nil {
		if ctx.Err() != nil {
			return "", ctx.Err()
		}
		return "", fmt.Errorf("git %s: %w: %s", strings.Join(args, " "), err, strings.TrimSpace(stderr.String()))
	}
	return stdout.String(), nil
}

// timeoutFor is a placeholder wall clock for a draft: a human sets the real one. C++ builds
// are slow (§5.5).
func timeoutFor(language string) time.Duration {
	if language == "cpp" {
		return 60 * time.Minute
	}
	return 30 * time.Minute
}

var (
	goTestFunc  = regexp.MustCompile(`(?m)^\+func (Test\w+)\(`)
	phpTestFunc = regexp.MustCompile(`(?m)^\+[^+].*\bfunction\s+(test\w+)`)
	cppTestSuit = regexp.MustCompile(`(?m)^\+\s*TEST(?:_F|_P)?\(\s*(\w+)\s*,`)
)

// oracleCommand guesses the command that runs the tests of a commit (§5.5). files are its test
// files that still exist, diff its diff with the parent, and packageJSON the parent's
// package.json. A language without a recipe gets an empty command for a human to fill in.
func oracleCommand(language string, files []string, diff, packageJSON string) string {
	switch language {
	case "go":
		var pkgs []string
		for _, f := range files {
			dir := path.Dir(f)
			if dir == "." {
				pkgs = append(pkgs, "./")
			} else {
				pkgs = append(pkgs, "./"+dir+"/")
			}
		}
		slices.Sort(pkgs)
		cmd := "go test " + strings.Join(slices.Compact(pkgs), " ")
		if names := submatches(goTestFunc, diff); len(names) > 0 {
			cmd += " -run '^(" + strings.Join(names, "|") + ")$'"
		}
		return cmd
	case "python":
		return ".venv/bin/pytest -q " + strings.Join(files, " ")
	case "node":
		return nodeCommand(files, packageJSON)
	case "php":
		if names := submatches(phpTestFunc, diff); len(names) > 0 {
			return "vendor/bin/phpunit --filter '(" + strings.Join(names, "|") + ")'"
		}
		return "vendor/bin/phpunit " + strings.Join(files, " ")
	case "cpp":
		cmd := "cmake --build build -j && ctest --test-dir build"
		if names := submatches(cppTestSuit, diff); len(names) > 0 {
			cmd += " -R '" + strings.Join(names, "|") + "'"
		}
		return cmd
	}
	return ""
}

// nodeCommand runs the files with the runner scripts.test names, else the whole `npm test`.
func nodeCommand(files []string, packageJSON string) string {
	var pkg struct {
		Scripts struct {
			Test string `json:"test"`
		} `json:"scripts"`
	}
	_ = json.Unmarshal([]byte(packageJSON), &pkg) // not JSON: npm test below
	list := strings.Join(files, " ")
	switch script := pkg.Scripts.Test; {
	case strings.Contains(script, "vitest"):
		return "npx vitest run " + list
	case strings.Contains(script, "jest"):
		return "npx jest " + list
	case strings.Contains(script, "mocha"):
		return "npx mocha " + list
	}
	return "npm test"
}

// submatches are the first groups of re in s, in order and without repeats.
func submatches(re *regexp.Regexp, s string) []string {
	var names []string
	for _, m := range re.FindAllStringSubmatch(s, -1) {
		if !slices.Contains(names, m[1]) {
			names = append(names, m[1])
		}
	}
	return names
}

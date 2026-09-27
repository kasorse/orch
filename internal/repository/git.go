// Package repository wraps the real git executable (via execution.ProcessExecutor) so the
// orchestrator can inspect the repository it operates on without ever mutating it - it
// never stashes, resets, checks out, or reverts user files. (SnapshotCommit only writes
// objects, through a throwaway index; the tree, the index and all refs are untouched.)
package repository

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"strings"

	"orchestrator/internal/apperr"
	"orchestrator/internal/execution"
)

type Repository struct {
	Dir      string
	Executor execution.ProcessExecutor
}

// Open verifies dir is inside a Git working tree.
func Open(ctx context.Context, dir string, executor execution.ProcessExecutor) (*Repository, error) {
	repo := &Repository{Dir: dir, Executor: executor}

	result, err := executor.Run(ctx, execution.Spec{Executable: "git", Args: []string{"rev-parse", "--is-inside-work-tree"}, WorkingDir: dir})
	if err != nil {
		return nil, &apperr.RepositoryError{Msg: "failed to run git", Err: err}
	}
	if result.ExitCode != 0 || strings.TrimSpace(result.Stdout) != "true" {
		return nil, &apperr.RepositoryError{Msg: dir + " is not a Git repository"}
	}
	return repo, nil
}

// IsDirty reports whether the working tree has uncommitted changes (staged, unstaged, or
// untracked).
func (r *Repository) IsDirty(ctx context.Context) (bool, error) {
	result, err := r.Executor.Run(ctx, execution.Spec{Executable: "git", Args: []string{"status", "--porcelain"}, WorkingDir: r.Dir})
	if err != nil {
		return false, &apperr.RepositoryError{Msg: "failed to run git status", Err: err}
	}
	if result.ExitCode != 0 {
		return false, &apperr.RepositoryError{Msg: "git status failed: " + result.Stderr}
	}
	return strings.TrimSpace(result.Stdout) != "", nil
}

// Diff returns the diff of tracked changes against HEAD, plus a note listing any untracked
// files (which "git diff" does not show).
func (r *Repository) Diff(ctx context.Context) (string, error) {
	diffResult, err := r.Executor.Run(ctx, execution.Spec{Executable: "git", Args: []string{"diff", "HEAD"}, WorkingDir: r.Dir})
	if err != nil {
		return "", &apperr.RepositoryError{Msg: "failed to run git diff", Err: err}
	}
	if diffResult.ExitCode != 0 {
		return "", &apperr.RepositoryError{Msg: "git diff failed: " + diffResult.Stderr}
	}

	statusResult, err := r.Executor.Run(ctx, execution.Spec{Executable: "git", Args: []string{"status", "--porcelain"}, WorkingDir: r.Dir})
	if err != nil {
		return "", &apperr.RepositoryError{Msg: "failed to run git status", Err: err}
	}
	if statusResult.ExitCode != 0 {
		return "", &apperr.RepositoryError{Msg: "git status failed: " + statusResult.Stderr}
	}

	var untracked []string
	for _, line := range strings.Split(statusResult.Stdout, "\n") {
		if strings.HasPrefix(line, "?? ") {
			untracked = append(untracked, strings.TrimPrefix(line, "?? "))
		}
	}

	diff := diffResult.Stdout
	if len(untracked) > 0 {
		diff += "\n\nUntracked files (not shown in diff above):\n  " + strings.Join(untracked, "\n  ")
	}
	return diff, nil
}

// ListFiles returns every file git would consider part of the project: tracked files
// plus untracked ones not excluded by .gitignore, as slash-separated paths relative to
// Dir. Ignored files never appear, so callers get .gitignore handling for free.
func (r *Repository) ListFiles(ctx context.Context) ([]string, error) {
	out, err := r.git(ctx, "ls-files", "-z", "--cached", "--others", "--exclude-standard")
	if err != nil {
		return nil, err
	}
	seen := make(map[string]bool)
	var files []string
	for _, p := range strings.Split(out, "\x00") {
		if p != "" && !seen[p] { // a file both cached and modified is listed once per stage
			seen[p] = true
			files = append(files, p)
		}
	}
	return files, nil
}

// Branch returns the current branch name, or "" when HEAD is detached. It works in a
// repository without commits too.
func (r *Repository) Branch(ctx context.Context) (string, error) {
	result, err := r.Executor.Run(ctx, execution.Spec{Executable: "git", Args: []string{"symbolic-ref", "--quiet", "--short", "HEAD"}, WorkingDir: r.Dir})
	if err != nil {
		return "", &apperr.RepositoryError{Msg: "failed to run git symbolic-ref", Err: err}
	}
	if result.ExitCode != 0 { // detached HEAD
		return "", nil
	}
	return strings.TrimSpace(result.Stdout), nil
}

// ChangedFiles returns the paths with uncommitted changes (staged, unstaged, or
// untracked), relative to Dir. A rename is reported under its new path, and an
// untracked directory as the directory itself rather than every file in it.
func (r *Repository) ChangedFiles(ctx context.Context) ([]string, error) {
	out, err := r.git(ctx, "status", "--porcelain=v1", "-z", "--untracked-files=normal")
	if err != nil {
		return nil, err
	}
	var files []string
	entries := strings.Split(out, "\x00")
	for i := 0; i < len(entries); i++ {
		e := entries[i]
		if len(e) < 4 {
			continue
		}
		files = append(files, e[3:])
		if e[0] == 'R' || e[0] == 'C' { // -z puts the original path in the next entry
			i++
		}
	}
	return files, nil
}

func (r *Repository) git(ctx context.Context, args ...string) (string, error) {
	return r.gitEnv(ctx, nil, args...)
}

// gitEnv is git with an explicit environment (nil inherits).
func (r *Repository) gitEnv(ctx context.Context, env []string, args ...string) (string, error) {
	result, err := r.Executor.Run(ctx, execution.Spec{Executable: "git", Args: args, WorkingDir: r.Dir, Env: env})
	if err != nil {
		return "", &apperr.RepositoryError{Msg: "failed to run git " + args[0], Err: err}
	}
	if result.ExitCode != 0 {
		return "", &apperr.RepositoryError{Msg: "git " + args[0] + " failed: " + result.Stderr}
	}
	return result.Stdout, nil
}

// TopLevel returns the absolute path of the working tree's root.
func (r *Repository) TopLevel(ctx context.Context) (string, error) {
	out, err := r.git(ctx, "rev-parse", "--show-toplevel")
	if err != nil {
		return "", err
	}
	return strings.TrimSpace(out), nil
}

// ChangedOrIgnoredPaths returns, among the files matching pathspecs, every one that
// differs from HEAD - modified, staged, deleted, untracked - and every ignored one, relative
// to the repository root and listed individually. Ignored files matter to the guard: a
// .gitignore'd orchestrator.yaml or .claude/settings.local.json is invisible to a plain
// status, yet changing it changes what runs next. --ignored=traditional with -uall lists
// the files inside an ignored directory one by one (=matching would name only the
// directory); restricting to pathspecs keeps git from listing every ignored build output.
// A rename is reported under its new path.
func (r *Repository) ChangedOrIgnoredPaths(ctx context.Context, pathspecs []string) ([]string, error) {
	args := append([]string{"status", "--porcelain=v1", "-z", "--untracked-files=all", "--ignored=traditional", "--"}, pathspecs...)
	out, err := r.git(ctx, args...)
	if err != nil {
		return nil, err
	}
	var files []string
	entries := strings.Split(out, "\x00")
	for i := 0; i < len(entries); i++ {
		e := entries[i]
		if len(e) < 4 {
			continue
		}
		files = append(files, e[3:])
		if e[0] == 'R' || e[0] == 'C' {
			i++
		}
	}
	return files, nil
}

// HeadRef returns the commit HEAD points to.
func (r *Repository) HeadRef(ctx context.Context) (string, error) {
	out, err := r.git(ctx, "rev-parse", "HEAD")
	if err != nil {
		return "", err
	}
	return strings.TrimSpace(out), nil
}

// SnapshotCommit records the whole working tree - tracked changes, deletions and
// untracked files, minus .gitignore'd ones and the orchestrator's run directory - as a
// commit on top of HEAD, and returns its ref. It works on a throwaway index, so the
// working tree, the real index, the stash list and every ref are untouched ("git stash
// create" would leave untracked files out). The commit is referenced by nothing, so git gc
// may prune it after its usual grace period.
func (r *Repository) SnapshotCommit(ctx context.Context) (string, error) {
	tmp, err := os.MkdirTemp("", "orchestrator-index-")
	if err != nil {
		return "", &apperr.RepositoryError{Msg: "failed to create a temporary index", Err: err}
	}
	defer os.RemoveAll(tmp)

	env := append(os.Environ(),
		"GIT_INDEX_FILE="+filepath.Join(tmp, "index"),
		// The snapshot is not the user's work; an identity is set so commit-tree works
		// even where user.name/user.email are not configured.
		"GIT_AUTHOR_NAME=orchestrator", "GIT_AUTHOR_EMAIL=orchestrator@localhost",
		"GIT_COMMITTER_NAME=orchestrator", "GIT_COMMITTER_EMAIL=orchestrator@localhost",
	)
	steps := [][]string{
		{"read-tree", "HEAD"},
		{"add", "-A", "--", ":(top)", ":(exclude,top).orchestrator"},
	}
	for _, args := range steps {
		if _, err := r.gitEnv(ctx, env, args...); err != nil {
			return "", err
		}
	}
	tree, err := r.gitEnv(ctx, env, "write-tree")
	if err != nil {
		return "", err
	}
	commit, err := r.gitEnv(ctx, env, "commit-tree", strings.TrimSpace(tree), "-p", "HEAD", "-m", "orchestrator: restore point before the run")
	if err != nil {
		return "", err
	}
	return strings.TrimSpace(commit), nil
}

// StatusSummary is a short, human-readable list of the uncommitted changes (one
// "XY path" line per file), leaving out the orchestrator's own run directory.
func (r *Repository) StatusSummary(ctx context.Context) (string, error) {
	out, err := r.git(ctx, "status", "--short", "--untracked-files=all", "--", ":(top)", ":(exclude,top).orchestrator")
	if err != nil {
		return "", err
	}
	return strings.TrimRight(out, "\n"), nil
}

// GitFingerprint hashes the parts of the git directory that make git itself run code: the
// repository config (core.fsmonitor, core.hooksPath, filter drivers, aliases) and every
// hook in the effective hooks directory (core.hooksPath honoured), skipping the inert
// *.sample files. Git status never reports them, so a change is only visible by comparing
// fingerprints (specs/permissions/SPEC.md §7.3).
func (r *Repository) GitFingerprint(ctx context.Context) (string, error) {
	common, err := r.git(ctx, "rev-parse", "--path-format=absolute", "--git-common-dir")
	if err != nil {
		return "", err
	}
	hooks, err := r.git(ctx, "rev-parse", "--path-format=absolute", "--git-path", "hooks")
	if err != nil {
		return "", err
	}

	h := sha256.New()
	hashFile(h, filepath.Join(strings.TrimSpace(common), "config"))
	hooksDir := strings.TrimSpace(hooks)
	entries, err := os.ReadDir(hooksDir)
	if err != nil && !errors.Is(err, fs.ErrNotExist) {
		return "", &apperr.RepositoryError{Msg: "failed to read the git hooks directory", Err: err}
	}
	for _, e := range entries {
		if strings.HasSuffix(e.Name(), ".sample") {
			continue
		}
		fmt.Fprintf(h, "hook %s\n", e.Name())
		hashFile(h, filepath.Join(hooksDir, e.Name()))
	}
	return hex.EncodeToString(h.Sum(nil)), nil
}

// hashFile writes path's content, or a marker for a missing or unreadable file, to h.
func hashFile(h io.Writer, path string) {
	data, err := os.ReadFile(path)
	if errors.Is(err, fs.ErrNotExist) {
		fmt.Fprintf(h, "%s: <missing>\n", path)
		return
	}
	if err != nil {
		fmt.Fprintf(h, "%s: <unreadable>\n", path)
		return
	}
	fmt.Fprintf(h, "%s: %d\n", path, len(data))
	h.Write(data)
}

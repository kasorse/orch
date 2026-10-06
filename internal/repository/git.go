// Package repository wraps the real git executable (via execution.ProcessExecutor) so the
// orchestrator can inspect the repository it operates on without ever mutating it - it
// never stashes, resets, checks out, or reverts user files. (SnapshotCommit only writes
// objects, through a throwaway index; the tree and the index are untouched. The one ref it
// writes is a run's own refs/orchestrator/runs/<id>, by PinRestorePoint. CommitAll, the
// one exception, stages and commits a finished run's changes on the operator's explicit
// approval.)
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
	"slices"
	"strconv"
	"strings"
	"time"

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
// untracked). The orchestrator's own run directory does not count: in a repository that
// does not ignore it, a run would otherwise make every later run and resume look dirty.
func (r *Repository) IsDirty(ctx context.Context) (bool, error) {
	result, err := r.Executor.Run(ctx, execution.Spec{Executable: "git", Args: []string{"status", "--porcelain", "--", ":(top)", ":(exclude,top).orchestrator"}, WorkingDir: r.Dir})
	if err != nil {
		return false, &apperr.RepositoryError{Msg: "failed to run git status", Err: err}
	}
	if result.ExitCode != 0 {
		return false, &apperr.RepositoryError{Msg: "git status failed: " + result.Stderr}
	}
	return strings.TrimSpace(result.Stdout) != "", nil
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

// CommitExists reports whether ref is a full commit SHA naming a commit in the repository.
// Anything else - an abbreviation, a branch name, an option - is false without running git.
func (r *Repository) CommitExists(ctx context.Context, ref string) (bool, error) {
	if !isFullSHA(ref) {
		return false, nil
	}
	result, err := r.Executor.Run(ctx, execution.Spec{Executable: "git", Args: []string{"cat-file", "-e", ref + "^{commit}"}, WorkingDir: r.Dir})
	if err != nil {
		return false, &apperr.RepositoryError{Msg: "failed to run git cat-file", Err: err}
	}
	return result.ExitCode == 0, nil
}

// isFullSHA reports whether s is a full SHA-1 or SHA-256 object name in lowercase hex.
func isFullSHA(s string) bool {
	if len(s) != 40 && len(s) != 64 {
		return false
	}
	return strings.Trim(s, "0123456789abcdef") == ""
}

// SnapshotCommit records the whole working tree - tracked changes, deletions and
// untracked files, minus .gitignore'd ones and the orchestrator's run directory - as a
// commit on top of HEAD, and returns its ref. It works on a throwaway index, so the
// working tree, the real index, the stash list and every ref are untouched ("git stash
// create" would leave untracked files out). The commit is referenced by nothing until
// PinRestorePoint pins it.
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

// RestorePointRef is the ref that pins a run's restore point. refs/orchestrator/ is
// outside refs/heads and refs/tags, so no branch, tag or default push sees it; it is shared
// by every worktree of the repository, and run IDs are unique.
func RestorePointRef(runID string) string {
	return "refs/orchestrator/runs/" + runID
}

// PinRestorePoint points RestorePointRef(runID) at sha, so git gc can never prune the
// restore point - a snapshot commit is otherwise referenced by nothing, and a HEAD commit
// stops being referenced once its branch moves on and the reflog expires. Pinning the same
// commit again is a no-op. runID must be hex and dashes, sha a full commit SHA; anything
// else is refused without running git.
func (r *Repository) PinRestorePoint(ctx context.Context, runID, sha string) error {
	if runID == "" || strings.Trim(runID, "0123456789abcdef-") != "" {
		return &apperr.RepositoryError{Msg: fmt.Sprintf("cannot pin the restore point: invalid run ID %q", runID)}
	}
	if !isFullSHA(sha) {
		return &apperr.RepositoryError{Msg: fmt.Sprintf("cannot pin the restore point: %q is not a full commit SHA", sha)}
	}
	_, err := r.git(ctx, "update-ref", "-m", "orchestrator: restore point of run "+runID, RestorePointRef(runID), sha)
	return err
}

// WorktreeTree writes the whole working tree (tracked changes, deletions and untracked
// files, minus .gitignore'd ones and the run directory) as a git tree object and returns
// its id. Like SnapshotCommit it uses a throwaway index, so the working tree, the real
// index and every ref are untouched; the tree is referenced by nothing.
func (r *Repository) WorktreeTree(ctx context.Context) (string, error) {
	tmp, err := os.MkdirTemp("", "orchestrator-index-")
	if err != nil {
		return "", &apperr.RepositoryError{Msg: "failed to create a temporary index", Err: err}
	}
	defer os.RemoveAll(tmp)
	env := append(os.Environ(), "GIT_INDEX_FILE="+filepath.Join(tmp, "index"))
	if _, err := r.gitEnv(ctx, env, "read-tree", "HEAD"); err != nil {
		// No commits yet: start from an empty index.
		if _, err := r.gitEnv(ctx, env, "read-tree", "--empty"); err != nil {
			return "", err
		}
	}
	// git add exits non-zero when the excluded run directory is itself .gitignore'd
	// ("paths are ignored"), yet stages everything else; write-tree is what counts.
	_, _ = r.gitEnv(ctx, env, "add", "-A", "--", ":(top)", ":(exclude,top).orchestrator")
	tree, err := r.gitEnv(ctx, env, "write-tree")
	if err != nil {
		return "", err
	}
	return strings.TrimSpace(tree), nil
}

// DiffTrees is the unified diff between two trees, with renames detected. External diff
// drivers and textconv filters are off: only git itself runs.
func (r *Repository) DiffTrees(ctx context.Context, from, to string) (string, error) {
	return r.git(ctx, "diff", "--no-color", "--no-ext-diff", "--no-textconv", "-M", from, to)
}

// Commit is one line of RecentCommits.
type Commit struct {
	Hash    string
	Subject string
	When    time.Time
}

// RecentCommits returns up to n commits from HEAD, newest first, and how many commits
// HEAD has in total. A repository without commits returns none.
func (r *Repository) RecentCommits(ctx context.Context, n int) ([]Commit, int, error) {
	countOut, err := r.git(ctx, "rev-list", "--count", "HEAD")
	if err != nil {
		return nil, 0, nil // no commits yet
	}
	total, _ := strconv.Atoi(strings.TrimSpace(countOut))
	out, err := r.git(ctx, "log", "-n", strconv.Itoa(n), "--no-show-signature", "--format=%h%x00%ct%x00%s")
	if err != nil {
		return nil, total, err
	}
	var commits []Commit
	for _, line := range strings.Split(strings.TrimRight(out, "\n"), "\n") {
		parts := strings.SplitN(line, "\x00", 3)
		if len(parts) != 3 {
			continue
		}
		secs, _ := strconv.ParseInt(parts[1], 10, 64)
		commits = append(commits, Commit{Hash: parts[0], When: time.Unix(secs, 0), Subject: parts[2]})
	}
	return commits, total, nil
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

// GitDirs returns the absolute git directory and, when it differs (a linked worktree),
// the common directory holding config and hooks.
func (r *Repository) GitDirs(ctx context.Context) ([]string, error) {
	out, err := r.git(ctx, "rev-parse", "--path-format=absolute", "--git-dir", "--git-common-dir")
	if err != nil {
		return nil, err
	}
	var dirs []string
	for _, line := range strings.Split(strings.TrimSpace(out), "\n") {
		if line = strings.TrimSpace(line); line != "" && !slices.Contains(dirs, line) {
			dirs = append(dirs, line)
		}
	}
	return dirs, nil
}

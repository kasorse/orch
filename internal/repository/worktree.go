package repository

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"sync"

	"github.com/kasorse/orch/internal/apperr"
	"github.com/kasorse/orch/internal/execution"
)

// ErrWorktreeKept is wrapped by the error RemoveWorktree returns when git refuses to remove
// the worktree (untracked or modified files, a lock): the caller reports the worktree as
// kept rather than as a failure.
var ErrWorktreeKept = errors.New("git refused to remove the worktree")

// Worktree is one entry of ListWorktrees.
type Worktree struct {
	Path   string
	Branch string // the full ref (refs/heads/...), "" for a detached or bare entry
	Head   string
}

// maxGitPointerSize bounds the .git file of a linked worktree.
const maxGitPointerSize = 4 << 10

// validBranchName is the cheap, git-free part of the branch check: it refuses what could be
// read as an option or a revision expression before any git runs.
func validBranchName(name string) bool {
	if name == "" || strings.HasPrefix(name, "-") || strings.HasPrefix(name, "/") || strings.HasSuffix(name, "/") ||
		strings.HasSuffix(name, ".") || strings.HasSuffix(name, ".lock") ||
		strings.Contains(name, "..") || strings.Contains(name, "@{") || strings.Contains(name, "//") {
		return false
	}
	if strings.ContainsAny(name, " ~^:?*[\\\x7f") {
		return false
	}
	for _, c := range name {
		if c < 0x20 {
			return false
		}
	}
	return true
}

// AddWorktree creates a linked worktree at path (absolute) on the new branch, starting at
// base, and returns its administrative directory. base must be a full SHA and branch a valid
// branch name; anything else is refused without running git. Git runs the operator's
// post-checkout hook and filters.
func (r *Repository) AddWorktree(ctx context.Context, path, branch, base string) (string, error) {
	if !filepath.IsAbs(path) {
		return "", &apperr.RepositoryError{Msg: fmt.Sprintf("cannot add a worktree: %q is not an absolute path", path)}
	}
	if !isFullSHA(base) {
		return "", &apperr.RepositoryError{Msg: fmt.Sprintf("cannot add a worktree: %q is not a full commit SHA", base)}
	}
	if !validBranchName(branch) {
		return "", &apperr.RepositoryError{Msg: fmt.Sprintf("cannot add a worktree: %q is not a valid branch name", branch)}
	}
	check, err := r.Executor.Run(ctx, execution.Spec{Executable: "git", Args: []string{"check-ref-format", "--branch", branch}, WorkingDir: r.Dir})
	if err != nil {
		return "", &apperr.RepositoryError{Msg: "failed to run git check-ref-format", Err: err}
	}
	if check.ExitCode != 0 {
		return "", &apperr.RepositoryError{Msg: fmt.Sprintf("cannot add a worktree: %q is not a valid branch name", branch)}
	}
	if _, err := r.git(ctx, "worktree", "add", "--quiet", "-b", branch, path, base); err != nil {
		return "", err
	}
	wt := &Repository{Dir: path, Executor: r.Executor}
	out, err := wt.git(ctx, "rev-parse", "--path-format=absolute", "--git-dir")
	if err != nil {
		return "", err
	}
	return strings.TrimSpace(out), nil
}

// AddDetachedWorktree creates a linked worktree at path (absolute) with a detached HEAD at
// commit, which must be a full SHA, and returns its administrative directory. Git runs the
// operator's post-checkout hook and filters.
func (r *Repository) AddDetachedWorktree(ctx context.Context, path, commit string) (string, error) {
	if !filepath.IsAbs(path) {
		return "", &apperr.RepositoryError{Msg: fmt.Sprintf("cannot add a worktree: %q is not an absolute path", path)}
	}
	if !isFullSHA(commit) {
		return "", &apperr.RepositoryError{Msg: fmt.Sprintf("cannot add a worktree: %q is not a full commit SHA", commit)}
	}
	if _, err := r.git(ctx, "worktree", "add", "--quiet", "--detach", path, commit); err != nil {
		return "", err
	}
	wt := &Repository{Dir: path, Executor: r.Executor}
	out, err := wt.git(ctx, "rev-parse", "--path-format=absolute", "--git-dir")
	if err != nil {
		return "", err
	}
	return strings.TrimSpace(out), nil
}

// BranchExists reports whether refs/heads/<name> exists. An invalid name is refused without
// running git.
func (r *Repository) BranchExists(ctx context.Context, name string) (bool, error) {
	if !validBranchName(name) {
		return false, &apperr.RepositoryError{Msg: fmt.Sprintf("%q is not a valid branch name", name)}
	}
	result, err := r.Executor.Run(ctx, execution.Spec{Executable: "git", Args: []string{"show-ref", "--verify", "--quiet", "refs/heads/" + name}, WorkingDir: r.Dir})
	if err != nil {
		return false, &apperr.RepositoryError{Msg: "failed to run git show-ref", Err: err}
	}
	switch result.ExitCode {
	case 0:
		return true, nil
	case 1:
		return false, nil
	}
	return false, &apperr.RepositoryError{Msg: "git show-ref failed: " + strings.TrimSpace(result.Stderr)}
}

// CommonDir returns the absolute path of the git directory every worktree of the repository
// shares.
func (r *Repository) CommonDir(ctx context.Context) (string, error) {
	out, err := r.git(ctx, "rev-parse", "--path-format=absolute", "--git-common-dir")
	if err != nil {
		return "", err
	}
	return strings.TrimSpace(out), nil
}

// ListWorktrees lists the repository's worktrees, the main one first.
func (r *Repository) ListWorktrees(ctx context.Context) ([]Worktree, error) {
	out, err := r.git(ctx, "worktree", "list", "--porcelain", "-z")
	if err != nil {
		return nil, err
	}
	var list []Worktree
	var cur *Worktree
	for _, field := range strings.Split(out, "\x00") {
		if field == "" { // the empty field ends a record
			cur = nil
			continue
		}
		key, value, _ := strings.Cut(field, " ")
		if key == "worktree" {
			list = append(list, Worktree{Path: value})
			cur = &list[len(list)-1]
			continue
		}
		if cur == nil {
			continue
		}
		switch key {
		case "HEAD":
			cur.Head = value
		case "branch":
			cur.Branch = value
		}
	}
	return list, nil
}

// RemoveWorktree removes the worktree at path, without --force: git refuses when anything
// is left in it, and that refusal wraps ErrWorktreeKept.
func (r *Repository) RemoveWorktree(ctx context.Context, path string) error {
	if !filepath.IsAbs(path) {
		return &apperr.RepositoryError{Msg: fmt.Sprintf("cannot remove a worktree: %q is not an absolute path", path)}
	}
	result, err := r.Executor.Run(ctx, execution.Spec{Executable: "git", Args: []string{"worktree", "remove", path}, WorkingDir: r.Dir})
	if err != nil {
		return &apperr.RepositoryError{Msg: "failed to run git worktree remove", Err: err}
	}
	if result.ExitCode != 0 {
		return &apperr.RepositoryError{Msg: "git worktree remove failed: " + strings.TrimSpace(result.Stderr), Err: ErrWorktreeKept}
	}
	return nil
}

// PruneWorktrees drops the administrative data of worktrees whose directory is gone.
func (r *Repository) PruneWorktrees(ctx context.Context) error {
	_, err := r.git(ctx, "worktree", "prune")
	return err
}

// ReadGitPointer reads <worktreeDir>/.git without git and returns the admin directory it
// names. The file must be a regular file (not a symlink, not a directory), at most 4 KiB,
// and read exactly "gitdir: <absolute path>\n".
func ReadGitPointer(worktreeDir string) (string, error) {
	path := filepath.Join(worktreeDir, ".git")
	bad := func(msg string) error {
		return &apperr.RepositoryError{Msg: fmt.Sprintf("the worktree pointer %s %s", path, msg)}
	}
	info, err := os.Lstat(path)
	if err != nil {
		return "", &apperr.RepositoryError{Msg: "failed to read the worktree pointer", Err: err}
	}
	if !info.Mode().IsRegular() {
		return "", bad("is not a regular file")
	}
	if info.Size() > maxGitPointerSize {
		return "", bad("is too large")
	}
	f, err := os.Open(path)
	if err != nil {
		return "", &apperr.RepositoryError{Msg: "failed to read the worktree pointer", Err: err}
	}
	defer f.Close()
	data, err := io.ReadAll(io.LimitReader(f, maxGitPointerSize+1))
	if err != nil {
		return "", &apperr.RepositoryError{Msg: "failed to read the worktree pointer", Err: err}
	}
	if len(data) > maxGitPointerSize {
		return "", bad("is too large")
	}
	content := string(data)
	dir, ok := strings.CutPrefix(content, "gitdir: ")
	if !ok {
		return "", bad(`does not start with "gitdir: "`)
	}
	dir, ok = strings.CutSuffix(dir, "\n")
	if !ok {
		return "", bad("does not end with a newline")
	}
	if dir == "" || strings.ContainsAny(dir, "\n\r\x00") {
		return "", bad("holds more than one path")
	}
	if !filepath.IsAbs(dir) {
		return "", bad("names a relative path")
	}
	return dir, nil
}

var (
	gitVersionMu    sync.Mutex
	gitVersionKnown bool
	gitVersionMajor int
	gitVersionMinor int
	gitVersionRe    = regexp.MustCompile(`(\d+)\.(\d+)`)
)

// mergeTreeSupported reports whether git has merge-tree --write-tree (2.38). The version is
// parsed on the first successful call only.
func (r *Repository) mergeTreeSupported(ctx context.Context) (bool, error) {
	gitVersionMu.Lock()
	defer gitVersionMu.Unlock()
	if !gitVersionKnown {
		out, err := r.git(ctx, "version")
		if err != nil {
			return false, err
		}
		m := gitVersionRe.FindStringSubmatch(out)
		if m == nil {
			return false, &apperr.RepositoryError{Msg: "cannot parse the git version: " + strings.TrimSpace(out)}
		}
		gitVersionMajor, _ = strconv.Atoi(m[1])
		gitVersionMinor, _ = strconv.Atoi(m[2])
		gitVersionKnown = true
	}
	return gitVersionMajor > 2 || (gitVersionMajor == 2 && gitVersionMinor >= 38), nil
}

// MergeConflicts asks git, with merge-tree (it writes objects only), which files would
// conflict merging theirs into ours. supported is false, with no error, when git is older
// than 2.38. ours and theirs are refused, without running git, when empty or option-like.
func (r *Repository) MergeConflicts(ctx context.Context, ours, theirs string) (files []string, supported bool, err error) {
	for _, ref := range []string{ours, theirs} {
		if ref == "" || strings.HasPrefix(ref, "-") {
			return nil, true, &apperr.RepositoryError{Msg: fmt.Sprintf("cannot check for conflicts: invalid ref %q", ref)}
		}
	}
	ok, err := r.mergeTreeSupported(ctx)
	if err != nil {
		return nil, false, err
	}
	if !ok {
		return nil, false, nil
	}
	result, err := r.Executor.Run(ctx, execution.Spec{Executable: "git", Args: []string{"merge-tree", "--write-tree", "--name-only", "--no-messages", "-z", ours, theirs}, WorkingDir: r.Dir})
	if err != nil {
		return nil, true, &apperr.RepositoryError{Msg: "failed to run git merge-tree", Err: err}
	}
	switch result.ExitCode {
	case 0:
		return nil, true, nil
	case 1:
		// The first field is the tree id, the rest the conflicted paths. git also exits 1,
		// with no tree, for a ref it cannot resolve.
		fields := strings.Split(result.Stdout, "\x00")
		if strings.TrimSpace(fields[0]) == "" {
			return nil, true, &apperr.RepositoryError{Msg: "git merge-tree failed: " + strings.TrimSpace(result.Stderr)}
		}
		for _, f := range fields[1:] {
			if f != "" {
				files = append(files, f)
			}
		}
		return files, true, nil
	}
	return nil, true, &apperr.RepositoryError{Msg: "git merge-tree failed: " + strings.TrimSpace(result.Stderr)}
}

// checkRefs refuses, without running git, a ref that is empty or starts with "-": it would be
// read as an option.
func checkRefs(what string, refs ...string) error {
	for _, ref := range refs {
		if ref == "" || strings.HasPrefix(ref, "-") {
			return &apperr.RepositoryError{Msg: fmt.Sprintf("cannot %s: invalid ref %q", what, ref)}
		}
	}
	return nil
}

// MergeTreeWrite merges theirs into ours with merge-tree --write-tree, which writes objects only
// (no checkout, no index, no hook). A clean merge returns its tree; a conflict returns the
// conflicted paths and no tree. Git older than 2.38 is an error: the merge cannot be made.
func (r *Repository) MergeTreeWrite(ctx context.Context, ours, theirs string) (tree string, conflicts []string, err error) {
	if err := checkRefs("merge", ours, theirs); err != nil {
		return "", nil, err
	}
	ok, err := r.mergeTreeSupported(ctx)
	if err != nil {
		return "", nil, err
	}
	if !ok {
		return "", nil, &apperr.RepositoryError{Msg: "git merge-tree --write-tree needs git 2.38 or newer"}
	}
	result, err := r.Executor.Run(ctx, execution.Spec{Executable: "git", Args: []string{"merge-tree", "--write-tree", "--name-only", "--no-messages", "-z", ours, theirs}, WorkingDir: r.Dir})
	if err != nil {
		return "", nil, &apperr.RepositoryError{Msg: "failed to run git merge-tree", Err: err}
	}
	fields := strings.Split(result.Stdout, "\x00")
	switch result.ExitCode {
	case 0:
		if tree = strings.TrimSpace(fields[0]); tree == "" {
			return "", nil, &apperr.RepositoryError{Msg: "git merge-tree answered no tree"}
		}
		return tree, nil, nil
	case 1:
		// The first field is the tree id, the rest the conflicted paths. git also exits 1,
		// with no tree, for a ref it cannot resolve.
		if strings.TrimSpace(fields[0]) == "" {
			return "", nil, &apperr.RepositoryError{Msg: "git merge-tree failed: " + strings.TrimSpace(result.Stderr)}
		}
		for _, f := range fields[1:] {
			if f != "" {
				conflicts = append(conflicts, f)
			}
		}
		if len(conflicts) == 0 {
			return "", nil, &apperr.RepositoryError{Msg: "git merge-tree reported a conflict without a path"}
		}
		return "", conflicts, nil
	}
	return "", nil, &apperr.RepositoryError{Msg: "git merge-tree failed: " + strings.TrimSpace(result.Stderr)}
}

// CommitTree writes a commit of tree on top of parents, in that order, with message, and returns
// it. No ref moves. The author and committer are the operator's git identity.
func (r *Repository) CommitTree(ctx context.Context, tree string, parents []string, message string) (string, error) {
	if err := checkRefs("commit a tree", append([]string{tree}, parents...)...); err != nil {
		return "", err
	}
	args := []string{"commit-tree", tree}
	for _, p := range parents {
		args = append(args, "-p", p)
	}
	args = append(args, "-m", message)
	out, err := r.git(ctx, args...)
	if err != nil {
		return "", err
	}
	return strings.TrimSpace(out), nil
}

// UpdateRef points ref (a full name, refs/heads/...) at newSHA, but only if it points at oldSHA
// now; an empty oldSHA means the ref must not exist yet. A ref that moved meanwhile is an error.
func (r *Repository) UpdateRef(ctx context.Context, ref, newSHA, oldSHA string) error {
	if err := checkRefs("update a ref", ref, newSHA); err != nil {
		return err
	}
	if strings.HasPrefix(oldSHA, "-") {
		return &apperr.RepositoryError{Msg: fmt.Sprintf("cannot update a ref: invalid ref %q", oldSHA)}
	}
	// An empty old value is an argument of its own: git reads it as "must not exist".
	_, err := r.git(ctx, "update-ref", ref, newSHA, oldSHA)
	return err
}

// IsAncestor reports whether ancestor is reachable from descendant (a commit is its own
// ancestor).
func (r *Repository) IsAncestor(ctx context.Context, ancestor, descendant string) (bool, error) {
	if err := checkRefs("compare commits", ancestor, descendant); err != nil {
		return false, err
	}
	result, err := r.Executor.Run(ctx, execution.Spec{Executable: "git", Args: []string{"merge-base", "--is-ancestor", ancestor, descendant}, WorkingDir: r.Dir})
	if err != nil {
		return false, &apperr.RepositoryError{Msg: "failed to run git merge-base", Err: err}
	}
	switch result.ExitCode {
	case 0:
		return true, nil
	case 1:
		return false, nil
	}
	return false, &apperr.RepositoryError{Msg: "git merge-base failed: " + strings.TrimSpace(result.Stderr)}
}

// Package workspace is specs/benchmark/SPEC.md §7.2: a repository mirrored once, and a fresh
// workspace for every check and every cell that holds one commit and nothing after it: no
// later commit, no fix_commit object, no remote.
package workspace

import (
	"bytes"
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"

	"orchbench/internal/cache"
	"orchbench/internal/task"
)

// Manager creates mirrors and workspaces under a cache directory. It is safe for concurrent
// use.
type Manager struct {
	Cache cache.Dir
	// Log, when set, receives every git command and its output (-v), and the sandboxed
	// commands of package check.
	Log io.Writer
}

// New returns a Manager. verbose, when not nil, is where commands and their output are
// echoed; it may be written from several goroutines.
func New(dir cache.Dir, verbose io.Writer) *Manager {
	m := &Manager{Cache: dir}
	if verbose != nil {
		m.Log = &lockedWriter{w: verbose}
	}
	return m
}

type lockedWriter struct {
	mu sync.Mutex
	w  io.Writer
}

func (l *lockedWriter) Write(p []byte) (int, error) {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.w.Write(p)
}

// ErrMissingCommit is returned when a commit is not in the repository, not even after a
// fetch.
type ErrMissingCommit struct {
	Commit string
}

func (e ErrMissingCommit) Error() string {
	return fmt.Sprintf("commit %s does not exist in the repository", e.Commit)
}

// mirrorLocks serialize cloning and fetching per mirror, so that tasks checked in parallel on
// one repository do not clone it twice.
var mirrorLocks sync.Map // path → *sync.Mutex

func lockMirror(path string) (unlock func()) {
	mu, _ := mirrorLocks.LoadOrStore(path, &sync.Mutex{})
	l := mu.(*sync.Mutex)
	l.Lock()
	return l.Unlock
}

// Mirror returns the bare mirror of repo under <cache>/mirrors/<sha256(repo)>.git, cloning it
// with `git clone --mirror` the first time. repo is a URL or a local path; a local path is
// keyed by its absolute form, so the same repository named two ways is mirrored once.
func (m *Manager) Mirror(ctx context.Context, repo string) (string, error) {
	if repo == "" {
		return "", errors.New("workspace: the repository is empty")
	}

	source := repo
	if info, err := os.Stat(repo); err == nil && info.IsDir() {
		abs, err := filepath.Abs(repo)
		if err != nil {
			return "", err
		}
		source = abs
	}
	sum := sha256.Sum256([]byte(source))
	target := filepath.Join(m.Cache.Mirrors(), hex.EncodeToString(sum[:])+".git")

	defer lockMirror(target)()
	if _, err := os.Stat(target); err == nil {
		return target, nil
	}

	if err := os.MkdirAll(m.Cache.Mirrors(), 0o755); err != nil {
		return "", err
	}
	// Cloned next to the target and renamed, so that a crash never leaves half a mirror.
	tmp, err := os.MkdirTemp(m.Cache.Mirrors(), ".clone-")
	if err != nil {
		return "", err
	}
	defer os.RemoveAll(tmp)

	clone := filepath.Join(tmp, "mirror.git")
	if _, err := m.Git(ctx, "", "clone", "--mirror", "-q", "--", source, clone); err != nil {
		return "", err
	}
	if err := os.Rename(clone, target); err != nil {
		return "", err
	}
	return target, nil
}

// EnsureCommits makes sure the mirror has every commit, running `git fetch` only when one is
// missing. A commit that is still missing after the fetch is an ErrMissingCommit.
//
// The fetch does not prune: it would delete the temporary refs of workspaces being created
// in parallel.
func (m *Manager) EnsureCommits(ctx context.Context, mirror string, commits ...string) error {
	missing, err := m.missing(ctx, mirror, commits)
	if err != nil || len(missing) == 0 {
		return err
	}

	unlock := lockMirror(mirror)
	defer unlock()
	if _, err := m.Git(ctx, mirror, "fetch", "-q"); err != nil {
		return fmt.Errorf("fetching into %s: %w", mirror, err)
	}

	missing, err = m.missing(ctx, mirror, missing)
	if err != nil {
		return err
	}
	if len(missing) > 0 {
		return ErrMissingCommit{Commit: missing[0]}
	}
	return nil
}

func (m *Manager) missing(ctx context.Context, mirror string, commits []string) ([]string, error) {
	var missing []string
	for _, c := range commits {
		_, err := m.Git(ctx, mirror, "cat-file", "-e", c+"^{commit}")
		if err == nil {
			continue
		}
		if _, isExit := ExitCode(err); isExit {
			missing = append(missing, c)
			continue
		}
		return nil, err
	}
	return missing, nil
}

// Workspace is a directory holding the tree of one commit, detached, with its history up to
// that commit and nothing else.
type Workspace struct {
	Dir    string
	Mirror string
	Commit string
	Task   task.Task

	m *Manager
}

// Create makes <cache>/work/<id>-<random>/ at commit: git init, a temporary ref in the mirror
// to the commit, a fetch of that ref, the ref deleted, a detached checkout of FETCH_HEAD. No
// remote is configured, and a git identity is set locally (the orchestrator's snapshot commit
// needs one). The caller removes the workspace.
func (m *Manager) Create(ctx context.Context, t task.Task, commit string) (*Workspace, error) {
	mirror, err := m.Mirror(ctx, t.Repo)
	if err != nil {
		return nil, err
	}
	if err := m.EnsureCommits(ctx, mirror, commit); err != nil {
		return nil, err
	}
	if err := os.MkdirAll(m.Cache.Work(), 0o755); err != nil {
		return nil, err
	}
	dir, err := os.MkdirTemp(m.Cache.Work(), t.ID+"-")
	if err != nil {
		return nil, err
	}

	w := &Workspace{Dir: dir, Mirror: mirror, Commit: commit, Task: t, m: m}
	if err := w.populate(ctx); err != nil {
		os.RemoveAll(dir)
		return nil, err
	}
	return w, nil
}

func (w *Workspace) populate(ctx context.Context) error {
	m := w.m

	for _, args := range [][]string{
		{"-c", "init.defaultBranch=main", "init", "-q"},
		{"config", "user.name", "orchbench"},
		{"config", "user.email", "orchbench@localhost"},
		{"config", "commit.gpgsign", "false"},
		{"config", "gc.auto", "0"},
	} {
		if _, err := m.Git(ctx, w.Dir, args...); err != nil {
			return err
		}
	}

	suffix, err := randomHex(8)
	if err != nil {
		return err
	}
	ref := "refs/orchbench/" + suffix
	if _, err := m.Git(ctx, w.Mirror, "update-ref", ref, w.Commit); err != nil {
		return err
	}
	_, fetchErr := m.Git(ctx, w.Dir, "fetch", "--no-tags", "-q", w.Mirror, ref)
	// Deleted whether or not the fetch worked, and even when ctx is cancelled.
	_, deleteErr := m.Git(context.WithoutCancel(ctx), w.Mirror, "update-ref", "-d", ref)
	if fetchErr != nil {
		return fetchErr
	}
	if deleteErr != nil {
		return deleteErr
	}

	_, err = m.Git(ctx, w.Dir, "checkout", "-q", "--detach", "FETCH_HEAD")
	return err
}

func randomHex(n int) (string, error) {
	b := make([]byte, n)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	return hex.EncodeToString(b), nil
}

// Remove deletes the workspace.
func (w *Workspace) Remove() error {
	return os.RemoveAll(w.Dir)
}

// Manager is the Manager that created the workspace.
func (w *Workspace) Manager() *Manager { return w.m }

// excludeOrchestrator leaves the orchestrator's own run directory out: it is not the model's
// change.
const excludeOrchestrator = ":(exclude).orchestrator"

// Git runs git in the workspace. core.fsmonitor and hooks are off: the model can write the
// workspace's .git, and a configured fsmonitor command or a hook would run on orchbench's
// next git call.
func (w *Workspace) Git(ctx context.Context, args ...string) ([]byte, error) {
	return w.m.git(ctx, w.Dir, nil, append(safeConfig(), args...)...)
}

func safeConfig() []string {
	return []string{"-c", "core.fsmonitor=false", "-c", "core.hooksPath=/dev/null"}
}

// Status is `git status --porcelain` of the workspace outside .orchestrator, untracked files
// listed one by one. Empty means the tree is clean.
func (w *Workspace) Status(ctx context.Context) (string, error) {
	out, err := w.Git(ctx, "status", "--porcelain=v1", "--untracked-files=all", "--", ".", excludeOrchestrator)
	return string(out), err
}

// Diff is the change of the working tree against the workspace's commit as one patch,
// untracked files included and .orchestrator excluded. It is computed in a temporary index,
// so the workspace's own index and the model's staged work stay as they are.
func (w *Workspace) Diff(ctx context.Context) (string, error) {
	tmp, err := os.MkdirTemp("", "orchbench-index-")
	if err != nil {
		return "", err
	}
	defer os.RemoveAll(tmp)
	env := []string{"GIT_INDEX_FILE=" + filepath.Join(tmp, "index")}

	steps := [][]string{
		{"read-tree", w.Commit},
		{"add", "-A", "--", ".", excludeOrchestrator},
	}
	for _, args := range steps {
		if _, err := w.m.git(ctx, w.Dir, env, append(safeConfig(), args...)...); err != nil {
			return "", err
		}
	}
	out, err := w.m.git(ctx, w.Dir, env, append(safeConfig(),
		"diff", "--cached", "--binary", "--no-ext-diff", w.Commit, "--", ".", excludeOrchestrator)...)
	return string(out), err
}

// Entry is one file of a commit's tree.
type Entry struct {
	Mode string // 100644, 100755 or 120000 (a symlink)
	OID  string
	Path string // slash-separated, relative to the repository root
}

// Tree lists the files of commit in the mirror, recursively; submodules are left out. It is
// read from the mirror, never from a workspace, whose .git the model may have changed.
func (m *Manager) Tree(ctx context.Context, mirror, commit string) ([]Entry, error) {
	out, err := m.gitIO(ctx, mirror, nil, nil, true, "ls-tree", "-r", "-z", "--full-tree", commit+"^{commit}")
	if err != nil {
		return nil, err
	}
	var entries []Entry
	for _, rec := range bytes.Split(out, []byte{0}) {
		if len(rec) == 0 {
			continue
		}
		meta, path, ok := bytes.Cut(rec, []byte{'\t'})
		fields := strings.Fields(string(meta))
		if !ok || len(fields) != 3 {
			return nil, fmt.Errorf("git ls-tree %s: unexpected line %q", commit, rec)
		}
		if fields[1] != "blob" {
			continue
		}
		entries = append(entries, Entry{Mode: fields[0], OID: fields[2], Path: string(path)})
	}
	return entries, nil
}

// ReadBlobs returns the contents of the blobs in the mirror, by object id, in one
// `git cat-file --batch`.
func (m *Manager) ReadBlobs(ctx context.Context, mirror string, oids []string) (map[string][]byte, error) {
	blobs := make(map[string][]byte, len(oids))
	if len(oids) == 0 {
		return blobs, nil
	}
	var in bytes.Buffer
	for _, oid := range oids {
		in.WriteString(oid + "\n")
	}
	out, err := m.gitIO(ctx, mirror, nil, &in, true, "cat-file", "--batch")
	if err != nil {
		return nil, err
	}

	for _, oid := range oids {
		header, rest, ok := bytes.Cut(out, []byte{'\n'})
		fields := strings.Fields(string(header))
		if !ok || len(fields) != 3 || fields[0] != oid || fields[1] != "blob" {
			return nil, fmt.Errorf("git cat-file --batch: unexpected header %q for %s", header, oid)
		}
		var size int
		if _, err := fmt.Sscanf(fields[2], "%d", &size); err != nil || size < 0 || size+1 > len(rest) {
			return nil, fmt.Errorf("git cat-file --batch: bad size in %q", header)
		}
		blobs[oid] = rest[:size:size]
		out = rest[size+1:]
	}
	return blobs, nil
}

// Git runs git in dir, which may be a mirror or a workspace, or "" for the current directory.
// A failure is an error carrying git's stderr; one where git ran and exited non-zero wraps an
// *exec.ExitError, see ExitCode. A cancelled ctx is ctx.Err().
func (m *Manager) Git(ctx context.Context, dir string, args ...string) ([]byte, error) {
	return m.git(ctx, dir, nil, args...)
}

func (m *Manager) git(ctx context.Context, dir string, env []string, args ...string) ([]byte, error) {
	return m.gitIO(ctx, dir, env, nil, false, args...)
}

// gitIO is git with stdin, and with quiet the command but not its stdout echoed (blob
// contents).
func (m *Manager) gitIO(ctx context.Context, dir string, env []string, stdin io.Reader, quiet bool, args ...string) ([]byte, error) {
	cmd := exec.CommandContext(ctx, "git", args...)
	cmd.Dir = dir
	cmd.Env = append(gitEnv(), env...)
	cmd.Stdin = stdin
	var stdout, stderr bytes.Buffer
	cmd.Stdout, cmd.Stderr = &stdout, &stderr

	err := cmd.Run()
	if quiet {
		m.echo(dir, args, nil, stderr.Bytes())
	} else {
		m.echo(dir, args, stdout.Bytes(), stderr.Bytes())
	}
	if err != nil {
		if ctx.Err() != nil {
			return nil, ctx.Err()
		}
		return stdout.Bytes(), fmt.Errorf("git %s: %w: %s", strings.Join(args, " "), err, strings.TrimSpace(stderr.String()))
	}
	return stdout.Bytes(), nil
}

func (m *Manager) echo(dir string, args []string, stdout, stderr []byte) {
	if m.Log == nil {
		return
	}
	var b bytes.Buffer
	fmt.Fprintf(&b, "$ git %s", strings.Join(args, " "))
	if dir != "" {
		fmt.Fprintf(&b, "    (in %s)", dir)
	}
	b.WriteByte('\n')
	b.Write(stdout)
	b.Write(stderr)
	m.Log.Write(b.Bytes())
}

// gitEnv is the environment git runs in: the process's own without the variables that would
// point git at another repository, and with prompting for credentials off.
func gitEnv() []string {
	var env []string
	for _, kv := range os.Environ() {
		name, _, _ := strings.Cut(kv, "=")
		switch name {
		case "GIT_DIR", "GIT_WORK_TREE", "GIT_INDEX_FILE", "GIT_COMMON_DIR", "GIT_PREFIX",
			"GIT_OBJECT_DIRECTORY", "GIT_ALTERNATE_OBJECT_DIRECTORIES":
			continue
		}
		env = append(env, kv)
	}
	return append(env, "GIT_TERMINAL_PROMPT=0")
}

// ExitCode returns the exit code of the git process an error from Git wraps, and false when
// git did not run to an exit (it could not be started, or ctx ended).
func ExitCode(err error) (int, bool) {
	var exitErr *exec.ExitError
	if errors.As(err, &exitErr) {
		return exitErr.ExitCode(), true
	}
	return 0, false
}

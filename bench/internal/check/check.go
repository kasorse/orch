// Package check decides a cell's result from exit codes only (specs/benchmark/SPEC.md §7.5,
// §5.3): the existing tests reset, the oracle written from fix_commit, oracle.command and
// test_command run sandboxed, and for a question the answer checks. `validate` reuses its
// steps (§6).
//
// Everything written into the tree is read from the mirror, never from the workspace's .git:
// the model may have changed that one, and nothing here runs git in the workspace.
package check

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"path"
	"path/filepath"
	"regexp"
	"strings"
	"time"

	"orchbench/internal/lang"
	"orchbench/internal/sandbox"
	"orchbench/internal/task"
	"orchbench/internal/workspace"
)

// The fail reasons of §7.5 and §5.3, as results.jsonl writes them.
const (
	ReasonOracle      = "oracle"
	ReasonRegression  = "regression"
	ReasonAnswer      = "answer"
	ReasonTreeChanged = "tree_changed"
)

// Result is the outcome of a cell's final check. Pass with an empty FailReason, or a fail
// with one of the Reason constants. An exit code is -1 for a command that timed out, and
// for a question, which runs neither command.
type Result struct {
	Pass               bool
	FailReason         string
	OracleExit         int
	SuiteExit          int
	TestsReset         int
	AnswerChecksFailed []string
}

// Decide is the table of §7.5.
func Decide(oracleExit, suiteExit, testsReset int) Result {
	r := Result{OracleExit: oracleExit, SuiteExit: suiteExit, TestsReset: testsReset}
	switch {
	case oracleExit != 0:
		r.FailReason = ReasonOracle
	case suiteExit != 0:
		r.FailReason = ReasonRegression
	default:
		r.Pass = true
	}
	return r
}

// Final runs the final check of a code task on the tree the config left in ws: ResetTests,
// WriteOracle, then oracle.command and test_command under the sandbox without network, both
// always. Their output goes to logDir/oracle.log and logDir/suite.log.
//
// t is the task as loaded; its language's defaults are filled here. ws must be a workspace at
// t's base_commit; the fix commit is read from ws.Mirror. Only an infrastructure failure (git,
// bwrap, the file system) is an error: a red or timed-out command is a Result.
func Final(ctx context.Context, ws *workspace.Workspace, t task.Task, logDir string) (Result, error) {
	reset, err := ResetTests(ctx, ws, t)
	if err != nil {
		return Result{}, fmt.Errorf("resetting the tests: %w", err)
	}
	if err := WriteOracle(ctx, ws, t); err != nil {
		return Result{}, fmt.Errorf("writing the oracle: %w", err)
	}
	oracleExit, err := RunCommand(ctx, ws, t, t.Oracle.Command, filepath.Join(logDir, "oracle.log"))
	if err != nil {
		return Result{}, err
	}
	suiteExit, err := RunCommand(ctx, ws, t, testCommand(t), filepath.Join(logDir, "suite.log"))
	if err != nil {
		return Result{}, err
	}
	return Decide(oracleExit, suiteExit, reset), nil
}

// ResetTests restores every file that matches the task's test_globs and exists at
// base_commit to its base_commit content and mode, and returns how many differed (changed,
// deleted, or replaced by something else). Files the model added are left alone.
func ResetTests(ctx context.Context, ws *workspace.Workspace, t task.Task) (int, error) {
	globs := resolved(t).TestGlobs
	m := ws.Manager()

	tree, err := m.Tree(ctx, ws.Mirror, t.BaseCommit)
	if err != nil {
		return 0, err
	}
	var tests []workspace.Entry
	for _, e := range tree {
		if matchesAny(globs, e.Path) {
			tests = append(tests, e)
		}
	}
	blobs, err := m.ReadBlobs(ctx, ws.Mirror, oids(tests))
	if err != nil {
		return 0, err
	}

	reset := 0
	for _, e := range tests {
		same, err := sameFile(ws.Dir, e, blobs[e.OID])
		if err != nil {
			return 0, err
		}
		if same {
			continue
		}
		if err := writeEntry(ws.Dir, e, blobs[e.OID]); err != nil {
			return 0, err
		}
		reset++
	}
	return reset, nil
}

// MissingOracleFileError says that an oracle file does not exist at fix_commit: the task is
// wrong, which `validate` reports.
type MissingOracleFileError struct {
	Path string
}

func (e *MissingOracleFileError) Error() string {
	return fmt.Sprintf("oracle file %s does not exist at fix_commit", e.Path)
}

// WriteOracle writes oracle.files with their fix_commit content and mode over the tree,
// creating directories and replacing whatever is in the way. A file missing at fix_commit is a
// *MissingOracleFileError.
func WriteOracle(ctx context.Context, ws *workspace.Workspace, t task.Task) error {
	files, err := OracleEntries(ctx, ws.Manager(), ws.Mirror, t.FixCommit, t.Oracle.Files)
	if err != nil {
		return err
	}
	blobs, err := ws.Manager().ReadBlobs(ctx, ws.Mirror, oids(files))
	if err != nil {
		return err
	}
	for _, e := range files {
		if err := writeEntry(ws.Dir, e, blobs[e.OID]); err != nil {
			return err
		}
	}
	return nil
}

// OracleEntries returns the tree entries of the oracle files at commit, in their order. A file
// the commit does not have is a *MissingOracleFileError.
func OracleEntries(ctx context.Context, m *workspace.Manager, mirror, commit string, files []string) ([]workspace.Entry, error) {
	tree, err := m.Tree(ctx, mirror, commit)
	if err != nil {
		return nil, err
	}
	byPath := make(map[string]workspace.Entry, len(tree))
	for _, e := range tree {
		byPath[e.Path] = e
	}
	entries := make([]workspace.Entry, 0, len(files))
	for _, f := range files {
		e, ok := byPath[f]
		if !ok {
			return nil, &MissingOracleFileError{Path: f}
		}
		entries = append(entries, e)
	}
	return entries, nil
}

// RunCommand runs command in the workspace under the sandbox without network, with the task's
// binds and its timeout, and writes the command, its output and its exit to logPath. A timeout
// is exit -1 and noted in the log. Only the sandbox failing to run, ctx ending or the log not
// being writable is an error.
func RunCommand(ctx context.Context, ws *workspace.Workspace, t task.Task, command, logPath string) (int, error) {
	return Sandboxed(ctx, ws.Dir, t, command, false, logPath, ws.Manager().Log)
}

// Sandboxed is RunCommand for any directory, with or without network (setup_command gets
// it), echoing to verbose when it is not nil.
func Sandboxed(ctx context.Context, dir string, t task.Task, command string, network bool, logPath string, verbose io.Writer) (int, error) {
	if err := os.MkdirAll(filepath.Dir(logPath), 0o755); err != nil {
		return -1, err
	}
	log, err := os.Create(logPath)
	if err != nil {
		return -1, err
	}
	defer log.Close()

	var w io.Writer = log
	if verbose != nil {
		w = io.MultiWriter(log, verbose)
	}
	fmt.Fprintf(w, "$ %s    (in %s)\n", command, dir)

	spec := sandbox.ForTask(dir, t, network, t.Timeout.Std())
	spec.Command = command
	spec.Stdout, spec.Stderr = w, w

	start := time.Now()
	exit, _, _, err := sandbox.Run(ctx, spec)
	elapsed := time.Since(start).Round(time.Millisecond)
	switch {
	case errors.Is(err, sandbox.ErrTimeout):
		fmt.Fprintf(w, "# timed out after %s\n", t.Timeout.Std())
		return -1, log.Close()
	case err != nil:
		return -1, err
	}
	fmt.Fprintf(w, "# exit %d after %s\n", exit, elapsed)
	return exit, log.Close()
}

// Answer checks a question's answer: the tree must be unchanged outside .orchestrator
// (tree_changed), then every answer check must match (answer, the failed ones listed). Neither
// command runs, so both exit codes are -1. Only git failing is an error.
func Answer(ctx context.Context, t task.Task, answer string, ws *workspace.Workspace) (Result, error) {
	r := Result{OracleExit: -1, SuiteExit: -1}
	status, err := ws.Status(ctx)
	if err != nil {
		return Result{}, err
	}
	if status != "" {
		r.FailReason = ReasonTreeChanged
		return r, nil
	}
	if failed := MatchChecks(t.AnswerChecks, answer); len(failed) > 0 {
		r.FailReason = ReasonAnswer
		r.AnswerChecksFailed = failed
		return r, nil
	}
	r.Pass = true
	return r, nil
}

// MatchChecks returns the answer checks that do not match text case-insensitively, in their
// order. A check that does not compile counts as not matching (task.Validate rejects it
// before).
func MatchChecks(checks []string, text string) []string {
	var failed []string
	for _, c := range checks {
		re, err := regexp.Compile("(?i)" + c)
		if err != nil || !re.MatchString(text) {
			failed = append(failed, c)
		}
	}
	return failed
}

// resolved is t with its language's defaults filled in. A task that loaded and validated has
// all of them; one built in code without them keeps what it has.
func resolved(t task.Task) task.Task {
	t.TestGlobs = append([]string(nil), t.TestGlobs...)
	_ = lang.Resolve(&t)
	return t
}

func testCommand(t task.Task) string { return resolved(t).TestCommand }

func matchesAny(globs []string, p string) bool {
	for _, g := range globs {
		if task.Match(g, p) {
			return true
		}
	}
	return false
}

func oids(entries []workspace.Entry) []string {
	out := make([]string, len(entries))
	for i, e := range entries {
		out[i] = e.OID
	}
	return out
}

// sameFile reports whether the file at e.Path under root is what e says: the same kind, the
// same executable bit and the same content. Anything else in its place, or an ancestor that is
// not a real directory, is not the same.
func sameFile(root string, e workspace.Entry, content []byte) (bool, error) {
	p, err := inside(root, e.Path)
	if err != nil {
		return false, err
	}
	if !realDirs(root, path.Dir(e.Path)) {
		return false, nil
	}
	info, err := os.Lstat(p)
	if errors.Is(err, os.ErrNotExist) {
		return false, nil
	}
	if err != nil {
		return false, err
	}

	if e.Mode == "120000" {
		if info.Mode()&os.ModeSymlink == 0 {
			return false, nil
		}
		target, err := os.Readlink(p)
		return err == nil && target == string(content), nil
	}
	if !info.Mode().IsRegular() || (info.Mode()&0o100 != 0) != (e.Mode == "100755") {
		return false, nil
	}
	data, err := os.ReadFile(p)
	if err != nil {
		return false, err
	}
	return string(data) == string(content), nil
}

// realDirs reports whether every component of dir (slash-separated, relative to root) is a
// directory and not a symlink.
func realDirs(root, dir string) bool {
	if dir == "." || dir == "" {
		return true
	}
	cur := root
	for _, part := range strings.Split(dir, "/") {
		cur = filepath.Join(cur, part)
		info, err := os.Lstat(cur)
		if err != nil || !info.IsDir() {
			return false
		}
	}
	return true
}

// writeEntry writes content as e.Path under root with e's mode. An ancestor that is a file or a
// symlink, and whatever is at the path itself, are removed first, so the write never follows a
// symlink out of the tree.
func writeEntry(root string, e workspace.Entry, content []byte) error {
	p, err := inside(root, e.Path)
	if err != nil {
		return err
	}

	cur := root
	if dir := path.Dir(e.Path); dir != "." {
		for _, part := range strings.Split(dir, "/") {
			cur = filepath.Join(cur, part)
			info, err := os.Lstat(cur)
			switch {
			case err == nil && info.IsDir():
				continue
			case err == nil:
				if err := os.Remove(cur); err != nil {
					return err
				}
			case !errors.Is(err, os.ErrNotExist):
				return err
			}
			if err := os.Mkdir(cur, 0o755); err != nil {
				return err
			}
		}
	}
	if err := os.RemoveAll(p); err != nil {
		return err
	}

	switch e.Mode {
	case "120000":
		return os.Symlink(string(content), p)
	case "100755":
		return writeFile(p, content, 0o755)
	default:
		return writeFile(p, content, 0o644)
	}
}

func writeFile(p string, content []byte, mode os.FileMode) error {
	f, err := os.OpenFile(p, os.O_WRONLY|os.O_CREATE|os.O_EXCL, mode)
	if err != nil {
		return err
	}
	if _, err := f.Write(content); err != nil {
		f.Close()
		return err
	}
	if err := f.Close(); err != nil {
		return err
	}
	// The umask may have taken bits off.
	return os.Chmod(p, mode)
}

// inside joins a slash-separated repository path to root, refusing one that would leave it.
func inside(root, rel string) (string, error) {
	if rel == "" || path.IsAbs(rel) || path.Clean(rel) != rel || rel == ".." || strings.HasPrefix(rel, "../") {
		return "", fmt.Errorf("path %q is not inside the repository", rel)
	}
	return filepath.Join(root, filepath.FromSlash(rel)), nil
}

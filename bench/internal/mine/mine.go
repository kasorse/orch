// Package mine is `orchbench mine` (specs/benchmark/SPEC.md §5.4, §13.3): it reads a clone's
// git history and writes a draft task file for every commit that could be replayed. It runs
// git to read and nothing else: no checkout, no worktree, no fetch, no model.
//
// Each commit is judged on its own against its single parent. Its changed files are sorted
// into test, excluded, code and documentation; a commit is kept when it changes code and
// tests, adds or edits a test, touches no excluded file and stays within the size bounds.
// Every commit that is not kept is counted under the first filter that rejected it.
package mine

import (
	"cmp"
	"context"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path"
	"path/filepath"
	"regexp"
	"slices"
	"strconv"
	"strings"

	"gopkg.in/yaml.v3"

	"orchbench/internal/lang"
	"orchbench/internal/task"
)

// ErrNoLanguage is returned when no language can be chosen, or when the chosen one has no
// defaults and Options.TestGlobs is empty: a usage error, nothing was read or written.
var ErrNoLanguage = errors.New("no language")

// Options configure a run.
type Options struct {
	// Repo is the local clone to read. Out is where the drafts go; it is created.
	Repo, Out string
	// Rev is the branch or commit whose history is walked; empty is HEAD.
	Rev string
	// Since is a YYYY-MM-DD date; empty is the whole history.
	Since string
	// MaxFiles and MaxLines bound the code change of a kept commit, tests not counted.
	MaxFiles, MaxLines int
	// Language is the name whose defaults the drafts get; empty is chosen from the file
	// extensions at Rev.
	Language string
	// TestGlobs say what a test file is; empty is the language's test_globs.
	TestGlobs []string
	// Remote is what goes into repo: of the drafts; empty is the origin URL, else Repo.
	Remote string
	// Verbose, when set, receives every git command (-v).
	Verbose io.Writer
}

// Skips count the commits that were not kept, by the first filter that rejected them. A
// commit without exactly one parent, a root commit included, is a Merge.
type Skips struct {
	Merge            int
	NoTestChange     int
	OnlyDeletedTests int
	LockFile         int
	TooLarge         int
}

// String is the skip summary line of §13.3.
func (s Skips) String() string {
	return fmt.Sprintf("no test change: %d, only deleted tests: %d, too large: %d, lock file: %d, merge: %d",
		s.NoTestChange, s.OnlyDeletedTests, s.TooLarge, s.LockFile, s.Merge)
}

// Result is what a run did: the draft files it wrote, the ones that already existed and were
// left alone, and the commits it skipped.
type Result struct {
	Written []string
	Kept    []string
	Skips   Skips
}

// Run walks the history and writes the drafts. It prints one line per draft written or kept,
// then the skip summary, to stdout.
func Run(ctx context.Context, opts Options, stdout io.Writer) (Result, error) {
	g := &git{repo: opts.Repo, verbose: opts.Verbose}
	rev := cmp.Or(opts.Rev, "HEAD")

	language, globs, err := resolveLanguage(ctx, g, opts, rev)
	if err != nil {
		return Result{}, err
	}
	remote := opts.Remote
	if remote == "" {
		remote = originURL(ctx, g, opts.Repo)
	}
	name := sanitizeName(remote)

	args := []string{"log", "--format=%H%x00%P"}
	if opts.Since != "" {
		args = append(args, "--since="+opts.Since)
	}
	out, err := g.run(ctx, append(args, rev)...)
	if err != nil {
		return Result{}, err
	}

	if err := os.MkdirAll(opts.Out, 0o755); err != nil {
		return Result{}, err
	}

	var res Result
	for _, line := range strings.Split(strings.TrimSpace(out), "\n") {
		sha, parents, _ := strings.Cut(line, "\x00")
		if sha == "" {
			continue
		}
		fields := strings.Fields(parents)
		if len(fields) != 1 {
			res.Skips.Merge++
			continue
		}
		if err := consider(ctx, g, opts, language, globs, name, remote, sha, fields[0], &res, stdout); err != nil {
			return res, err
		}
	}

	fmt.Fprintf(stdout, "skipped: %s\n", res.Skips)
	return res, nil
}

// consider judges one commit with a single parent and, if it is kept, writes its draft.
func consider(ctx context.Context, g *git, opts Options, language string, globs []string,
	name, remote, sha, parent string, res *Result, stdout io.Writer) error {

	changes, err := changedFiles(ctx, g, parent, sha, globs)
	if err != nil {
		return err
	}
	s := summarize(changes)

	switch {
	case len(s.code) == 0 || len(s.tests) == 0:
		res.Skips.NoTestChange++
		return nil
	case len(s.liveTests) == 0:
		res.Skips.OnlyDeletedTests++
		return nil
	case s.excluded > 0:
		res.Skips.LockFile++
		return nil
	case len(s.code) > opts.MaxFiles || s.codeLines > opts.MaxLines:
		res.Skips.TooLarge++
		return nil
	}

	d, err := buildDraft(ctx, g, language, globs, opts, name, remote, sha, parent, s)
	if err != nil {
		return err
	}
	file := filepath.Join(opts.Out, d.ID+".yaml")
	written, err := writeNew(file, d.marshal(s))
	if err != nil {
		return err
	}
	if written {
		res.Written = append(res.Written, file)
		fmt.Fprintf(stdout, "wrote %s\n", file)
	} else {
		res.Kept = append(res.Kept, file)
		fmt.Fprintf(stdout, "kept %s\n", file)
	}
	return nil
}

// writeNew creates the file with data unless it exists, and reports whether it did.
func writeNew(file string, data []byte) (bool, error) {
	f, err := os.OpenFile(file, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o644)
	if errors.Is(err, fs.ErrExist) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	if _, err := f.Write(data); err != nil {
		f.Close()
		return false, err
	}
	return true, f.Close()
}

// resolveLanguage returns the language of the drafts and the globs that mark a test file.
func resolveLanguage(ctx context.Context, g *git, opts Options, rev string) (string, []string, error) {
	language := opts.Language
	if language == "" {
		files, err := g.run(ctx, "ls-tree", "-r", "--name-only", "-z", rev)
		if err != nil {
			return "", nil, err
		}
		language = detectLanguage(strings.Split(files, "\x00"))
		if language == "" {
			return "", nil, fmt.Errorf("%w: none of the files at %s has a known extension; pass --language", ErrNoLanguage, rev)
		}
	}

	globs := opts.TestGlobs
	if len(globs) == 0 {
		d, ok := lang.Lookup(language)
		if !ok {
			return "", nil, fmt.Errorf("%w: %q has no defaults; pass --test-glob", ErrNoLanguage, language)
		}
		globs = d.TestGlobs
	}
	return language, globs, nil
}

// extLanguage maps a file extension to the language whose defaults it implies.
var extLanguage = map[string]string{
	".go": "go", ".py": "python", ".php": "php",
	".js": "node", ".jsx": "node", ".ts": "node", ".tsx": "node", ".mjs": "node", ".cjs": "node",
	".cc": "cpp", ".cpp": "cpp", ".cxx": "cpp", ".h": "cpp", ".hpp": "cpp",
}

// detectLanguage is the language with the most code files among paths, by extension; a tie
// goes to the first name alphabetically. Excluded and documentation files do not count.
func detectLanguage(paths []string) string {
	count := make(map[string]int)
	for _, p := range paths {
		if classify(p, nil) == kindCode {
			if l, ok := extLanguage[strings.ToLower(path.Ext(p))]; ok {
				count[l]++
			}
		}
	}
	best := ""
	for l, n := range count {
		if best == "" || n > count[best] || (n == count[best] && l < best) {
			best = l
		}
	}
	return best
}

// originURL is the clone's origin URL, else the absolute path of the clone.
func originURL(ctx context.Context, g *git, repo string) string {
	if out, err := g.run(ctx, "remote", "get-url", "origin"); err == nil && strings.TrimSpace(out) != "" {
		return strings.TrimSpace(out)
	}
	if abs, err := filepath.Abs(repo); err == nil {
		return abs
	}
	return repo
}

var nonAlnum = regexp.MustCompile(`[^a-z0-9]+`)

// sanitizeName is the repository name of a URL or path as the part of an id: the last element
// without .git, lowercase, every other character a dash.
func sanitizeName(remote string) string {
	base := strings.TrimSuffix(path.Base(strings.TrimRight(filepath.ToSlash(remote), "/")), ".git")
	name := strings.Trim(nonAlnum.ReplaceAllString(strings.ToLower(base), "-"), "-")
	if name == "" {
		return "repo"
	}
	return name
}

type kind int

const (
	kindCode kind = iota
	kindTest
	kindExcluded
	kindDoc
)

// lockFiles are excluded by name wherever they are.
var lockFiles = []string{
	"go.sum", "package-lock.json", "yarn.lock", "pnpm-lock.yaml", "composer.lock",
	"Cargo.lock", "Gemfile.lock", "poetry.lock",
}

// excludedGlobs are the vendored and generated files.
var excludedGlobs = []string{"**/vendor/**", "**/node_modules/**", "**/*.pb.go", "**/*_generated.*", "**/*.min.js"}

// docGlobs are documentation: neither code nor test.
var docGlobs = []string{
	"**/*.md", "**/*.rst", "**/*.txt", "docs/**", "**/README*", "**/LICENSE*", "**/CHANGELOG*",
}

// classify sorts a file. Excluded comes first, so that a lock file is never code; a test file
// is one that matches testGlobs; documentation is neither code nor test; the rest is code.
func classify(file string, testGlobs []string) kind {
	if slices.Contains(lockFiles, path.Base(file)) || matchAny(excludedGlobs, file) {
		return kindExcluded
	}
	if matchAny(testGlobs, file) {
		return kindTest
	}
	if matchAny(docGlobs, file) {
		return kindDoc
	}
	return kindCode
}

func matchAny(globs []string, file string) bool {
	for _, g := range globs {
		if task.Match(g, file) {
			return true
		}
	}
	return false
}

// change is one file of a commit's diff against its parent.
type change struct {
	path           string
	status         byte // A, M, D, ...
	added, deleted int  // 0 for a binary file
	kind           kind
}

// changedFiles lists what the commit changed against its parent, renames as a delete and an
// add. The paths come from the NUL-separated forms of git, so any file name is safe.
func changedFiles(ctx context.Context, g *git, parent, sha string, testGlobs []string) ([]change, error) {
	base := []string{"diff-tree", "-r", "--no-renames", "-z"}
	status, err := g.run(ctx, append(base, "--name-status", parent, sha)...)
	if err != nil {
		return nil, err
	}
	numstat, err := g.run(ctx, append(base, "--numstat", parent, sha)...)
	if err != nil {
		return nil, err
	}

	type lines struct{ added, deleted int }
	counts := make(map[string]lines)
	for _, rec := range strings.Split(numstat, "\x00") {
		f := strings.SplitN(rec, "\t", 3)
		if len(f) != 3 {
			continue
		}
		a, _ := strconv.Atoi(f[0]) // "-" for a binary file
		d, _ := strconv.Atoi(f[1])
		counts[f[2]] = lines{a, d}
	}

	tokens := strings.Split(status, "\x00")
	var changes []change
	for i := 0; i+1 < len(tokens); i += 2 {
		p := tokens[i+1]
		changes = append(changes, change{
			path:    p,
			status:  tokens[i][0],
			added:   counts[p].added,
			deleted: counts[p].deleted,
			kind:    classify(p, testGlobs),
		})
	}
	return changes, nil
}

// summary is a commit's changes by kind.
type summary struct {
	files            int
	added, deleted   int
	code, tests, doc []string // paths
	liveTests        []string // the tests that are not deleted
	excluded         int
	codeLines        int
}

func summarize(changes []change) summary {
	s := summary{files: len(changes)}
	for _, c := range changes {
		s.added += c.added
		s.deleted += c.deleted
		switch c.kind {
		case kindCode:
			s.code = append(s.code, c.path)
			s.codeLines += c.added + c.deleted
		case kindTest:
			s.tests = append(s.tests, c.path)
			if c.status != 'D' {
				s.liveTests = append(s.liveTests, c.path)
			}
		case kindDoc:
			s.doc = append(s.doc, c.path)
		case kindExcluded:
			s.excluded++
		}
	}
	return s
}

// draft is the file `mine` writes: the fields of the §5.4 example in its order. It is not a
// task.Task, which would write every empty optional field as well.
type draft struct {
	Draft          bool          `yaml:"draft"`
	ID             string        `yaml:"id"`
	Repo           string        `yaml:"repo"`
	BaseCommit     string        `yaml:"base_commit"`
	FixCommit      string        `yaml:"fix_commit"`
	Category       string        `yaml:"category"`
	Language       string        `yaml:"language"`
	Lang           string        `yaml:"lang"`
	Task           string        `yaml:"task"`
	ExpectedStages []string      `yaml:"expected_stages"`
	TestGlobs      []string      `yaml:"test_globs,omitempty"`
	SetupCommand   string        `yaml:"setup_command"`
	TestCommand    string        `yaml:"test_command"`
	Oracle         task.Oracle   `yaml:"oracle"`
	Timeout        task.Duration `yaml:"timeout"`
}

// marshal is the YAML of the draft and the `# stat:` comment, which yaml.Marshal cannot write.
func (d draft) marshal(s summary) []byte {
	data, err := yaml.Marshal(d)
	if err != nil {
		// Only strings and slices of strings: nothing here can fail.
		panic(err)
	}
	other := fmt.Sprintf("other %d", len(s.doc))
	if len(s.doc) > 0 {
		other += ": " + strings.Join(s.doc, ", ")
	}
	stat := fmt.Sprintf("# stat: %d files, +%d -%d (code %d, tests %d, %s)\n",
		s.files, s.added, s.deleted, len(s.code), len(s.tests), other)
	return append(data, stat...)
}

var bugfixSubject = regexp.MustCompile(`(?i)\b(fix|bug)`)

// guessCategory is bugfix when the subject says fix or bug, else feature.
func guessCategory(subject string) string {
	if bugfixSubject.MatchString(subject) {
		return task.CategoryBugfix
	}
	return task.CategoryFeature
}

func buildDraft(ctx context.Context, g *git, language string, globs []string, opts Options,
	name, remote, sha, parent string, s summary) (draft, error) {

	msg, err := g.run(ctx, "log", "-1", "--format=%B", sha)
	if err != nil {
		return draft{}, err
	}
	msg = strings.TrimSpace(msg)
	subject, _, _ := strings.Cut(msg, "\n")

	d := draft{
		Draft:          true,
		ID:             name + "-" + sha[:7],
		Repo:           remote,
		BaseCommit:     parent,
		FixCommit:      sha,
		Category:       guessCategory(subject),
		Language:       language,
		Lang:           "en",
		Task:           msg + "\n",
		ExpectedStages: []string{},
		Timeout:        task.Duration(timeoutFor(language)),
	}
	if len(opts.TestGlobs) > 0 {
		d.TestGlobs = slices.Clone(opts.TestGlobs)
	}
	defaults, _ := lang.Lookup(language)
	d.SetupCommand, d.TestCommand = defaults.SetupCommand, defaults.TestCommand

	if language == "node" {
		for _, lock := range []struct{ file, setup string }{
			{"pnpm-lock.yaml", "pnpm i --frozen-lockfile"},
			{"yarn.lock", "yarn --immutable"},
		} {
			if _, err := g.run(ctx, "cat-file", "-e", parent+":"+lock.file); err == nil {
				d.SetupCommand = lock.setup
				break
			}
		}
	}

	files := slices.Clone(s.liveTests)
	slices.Sort(files)
	d.Oracle.Files = files

	diffArgs := append([]string{"diff", "--no-color", "--no-ext-diff", "--no-renames", "-U0", parent, sha, "--"}, files...)
	added, err := g.run(ctx, diffArgs...)
	if err != nil {
		return draft{}, err
	}
	var packageJSON string
	if language == "node" {
		packageJSON, _ = g.run(ctx, "show", parent+":package.json")
	}
	d.Oracle.Command = oracleCommand(language, files, added, packageJSON)
	return d, nil
}

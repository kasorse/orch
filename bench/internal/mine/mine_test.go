package mine

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"orchbench/internal/lang"
	"orchbench/internal/task"
	"orchbench/internal/testutil"
)

const remote = "https://example.com/org/Proj.git"

// fixture is a Go repository whose history holds one commit of each kind.
type fixture struct {
	repo *testutil.Repo

	root string // the root commit, parent of keep
	keep string // the only commit that is a task
}

func newFixture(t *testing.T) fixture {
	t.Helper()
	r := testutil.NewRepo(t)
	f := fixture{repo: r}

	f.root = r.Commit("root", map[string]string{
		"go.mod":    "module proj\n\ngo 1.22\n",
		"a.go":      "package proj\n\nfunc A() int { return 0 }\n",
		"README.md": "proj\n",
	})

	f.keep = r.Commit("Fix A off by one\n\nA returned 0 for every input.\n", map[string]string{
		"a.go":      "package proj\n\nfunc A() int { return 1 }\n",
		"a_test.go": "package proj\n\nimport \"testing\"\n\nfunc TestFixA(t *testing.T) {\n\tif A() != 1 {\n\t\tt.Fatal(A())\n\t}\n}\n",
	})

	// Code only.
	r.Commit("Tweak A", map[string]string{"a.go": "package proj\n\nfunc A() int { return 2 }\n"})

	// A test file deleted along with a code change.
	r.Commit("Drop the test", map[string]string{
		"a.go":      "package proj\n\nfunc A() int { return 3 }\n",
		"a_test.go": testutil.Delete,
	})

	// A dependency bump: lock file, code and a test.
	r.Commit("Bump deps", map[string]string{
		"go.sum":    "example.com/dep v1.0.0 h1:abc=\n",
		"a.go":      "package proj\n\nfunc A() int { return 4 }\n",
		"b_test.go": "package proj\n\nimport \"testing\"\n\nfunc TestB(t *testing.T) {}\n",
	})

	// Eleven code files and a test: over --max-files 10.
	big := map[string]string{"c_test.go": "package proj\n\nimport \"testing\"\n\nfunc TestC(t *testing.T) {}\n"}
	for i := range 11 {
		big[fmt.Sprintf("c%d.go", i)] = fmt.Sprintf("package proj\n\nvar C%d = %d\n", i, i)
	}
	r.Commit("Add the C files", big)

	// A merge of a side branch that only touches documentation.
	r.Git("checkout", "-q", "-b", "side")
	r.Commit("Docs", map[string]string{"README.md": "proj, documented\n"})
	r.Git("checkout", "-q", "main")
	r.Git("merge", "-q", "--no-ff", "-m", "Merge side", "side")
	return f
}

func mineOpts(f fixture, out string) Options {
	return Options{Repo: f.repo.Dir, Out: out, MaxFiles: 10, MaxLines: 400, Remote: remote}
}

func TestRunWritesOneDraftAndCountsTheSkips(t *testing.T) {
	f := newFixture(t)
	out := filepath.Join(t.TempDir(), "tasks")

	var stdout bytes.Buffer
	res, err := Run(context.Background(), mineOpts(f, out), &stdout)
	if err != nil {
		t.Fatal(err)
	}

	file := filepath.Join(out, "proj-"+f.keep[:7]+".yaml")
	if !slices.Equal(res.Written, []string{file}) || len(res.Kept) != 0 {
		t.Fatalf("written %v, kept %v, want only %s", res.Written, res.Kept, file)
	}
	want := Skips{Merge: 2, NoTestChange: 2, OnlyDeletedTests: 1, LockFile: 1, TooLarge: 1}
	if res.Skips != want {
		t.Errorf("skips %+v, want %+v", res.Skips, want)
	}
	wantOut := "wrote " + file + "\nskipped: no test change: 2, only deleted tests: 1, too large: 1, lock file: 1, merge: 2\n"
	if stdout.String() != wantOut {
		t.Errorf("stdout\n%s\nwant\n%s", stdout.String(), wantOut)
	}

	entries, err := os.ReadDir(out)
	if err != nil || len(entries) != 1 {
		t.Fatalf("out holds %v (%v), want one file", entries, err)
	}
}

func TestDraftLoadsAndFailsOnlyAsADraft(t *testing.T) {
	f := newFixture(t)
	out := t.TempDir()
	res, err := Run(context.Background(), mineOpts(f, out), &bytes.Buffer{})
	if err != nil || len(res.Written) != 1 {
		t.Fatalf("written %v, err %v", res.Written, err)
	}

	tk, err := task.Load(res.Written[0])
	if err != nil {
		t.Fatal(err)
	}
	if !tk.IsDraft() {
		t.Error("the file is not a draft")
	}
	if err := lang.Validate(tk); err != nil {
		t.Errorf("a draft must be a valid task apart from being a draft: %v", err)
	}

	if tk.ID != "proj-"+f.keep[:7] || tk.FileID() != tk.ID {
		t.Errorf("id %q, file id %q", tk.ID, tk.FileID())
	}
	if tk.Repo != remote || tk.BaseCommit != f.root || tk.FixCommit != f.keep {
		t.Errorf("repo %q, base %q, fix %q", tk.Repo, tk.BaseCommit, tk.FixCommit)
	}
	if tk.Category != task.CategoryBugfix || tk.Language != "go" || tk.Lang != "en" {
		t.Errorf("category %q, language %q, lang %q", tk.Category, tk.Language, tk.Lang)
	}
	if want := "Fix A off by one\n\nA returned 0 for every input.\n"; tk.Task != want {
		t.Errorf("task %q, want %q", tk.Task, want)
	}
	if tk.SetupCommand != "go mod download" || tk.TestCommand != "go test ./..." {
		t.Errorf("setup %q, test %q", tk.SetupCommand, tk.TestCommand)
	}
	if !slices.Equal(tk.Oracle.Files, []string{"a_test.go"}) {
		t.Errorf("oracle files %v", tk.Oracle.Files)
	}
	if want := "go test ./ -run '^(TestFixA)$'"; tk.Oracle.Command != want {
		t.Errorf("oracle command %q, want %q", tk.Oracle.Command, want)
	}
	if len(tk.TestGlobs) != 0 {
		t.Errorf("test_globs %v: the language default is not written", tk.TestGlobs)
	}

	data, err := os.ReadFile(res.Written[0])
	if err != nil {
		t.Fatal(err)
	}
	lines := strings.Split(strings.TrimRight(string(data), "\n"), "\n")
	if last, want := lines[len(lines)-1], "# stat: 2 files, +10 -1 (code 1, tests 1, other 0)"; last != want {
		t.Errorf("last line %q, want %q", last, want)
	}
}

func TestSecondRunWritesNothingAndOverwritesNothing(t *testing.T) {
	f := newFixture(t)
	out := t.TempDir()
	first, err := Run(context.Background(), mineOpts(f, out), &bytes.Buffer{})
	if err != nil || len(first.Written) != 1 {
		t.Fatalf("written %v, err %v", first.Written, err)
	}
	file := first.Written[0]

	// A human edited the draft.
	edited := []byte("id: edited\n")
	if err := os.WriteFile(file, edited, 0o644); err != nil {
		t.Fatal(err)
	}

	var stdout bytes.Buffer
	second, err := Run(context.Background(), mineOpts(f, out), &stdout)
	if err != nil {
		t.Fatal(err)
	}
	if len(second.Written) != 0 || !slices.Equal(second.Kept, []string{file}) {
		t.Errorf("written %v, kept %v", second.Written, second.Kept)
	}
	if second.Skips != first.Skips {
		t.Errorf("skips %+v, then %+v", first.Skips, second.Skips)
	}
	if !strings.HasPrefix(stdout.String(), "kept "+file+"\n") {
		t.Errorf("stdout %q", stdout.String())
	}
	got, err := os.ReadFile(file)
	if err != nil || !bytes.Equal(got, edited) {
		t.Errorf("file is %q (%v), want it untouched", got, err)
	}
}

func TestSinceBoundsTheHistory(t *testing.T) {
	f := newFixture(t)
	res, err := Run(context.Background(), Options{
		Repo: f.repo.Dir, Out: t.TempDir(), MaxFiles: 10, MaxLines: 400, Remote: remote, Since: "2099-01-01",
	}, &bytes.Buffer{})
	if err != nil {
		t.Fatal(err)
	}
	if len(res.Written) != 0 || res.Skips != (Skips{}) {
		t.Errorf("result %+v, want nothing", res)
	}
}

func TestSizeBounds(t *testing.T) {
	f := newFixture(t)
	opts := mineOpts(f, t.TempDir())
	opts.MaxLines = 1 // the kept commit changes 2 code lines, the C files 33
	res, err := Run(context.Background(), opts, &bytes.Buffer{})
	if err != nil {
		t.Fatal(err)
	}
	if len(res.Written) != 0 || res.Skips.TooLarge != 2 {
		t.Errorf("written %v, too large %d, want none and 2", res.Written, res.Skips.TooLarge)
	}
}

func TestIDFromOriginAndPath(t *testing.T) {
	f := newFixture(t)
	f.repo.Git("remote", "add", "origin", "git@github.com:org/My_Repo.git")
	opts := mineOpts(f, t.TempDir())
	opts.Remote = ""
	res, err := Run(context.Background(), opts, &bytes.Buffer{})
	if err != nil || len(res.Written) != 1 {
		t.Fatalf("written %v, err %v", res.Written, err)
	}
	tk, err := task.Load(res.Written[0])
	if err != nil {
		t.Fatal(err)
	}
	if tk.Repo != "git@github.com:org/My_Repo.git" || tk.ID != "my-repo-"+f.keep[:7] {
		t.Errorf("repo %q, id %q", tk.Repo, tk.ID)
	}

	f.repo.Git("remote", "remove", "origin")
	opts.Out = t.TempDir()
	res, err = Run(context.Background(), opts, &bytes.Buffer{})
	if err != nil || len(res.Written) != 1 {
		t.Fatalf("written %v, err %v", res.Written, err)
	}
	tk, err = task.Load(res.Written[0])
	if err != nil {
		t.Fatal(err)
	}
	if tk.Repo != f.repo.Dir {
		t.Errorf("repo %q, want the path %q", tk.Repo, f.repo.Dir)
	}
}

func TestTestGlobAndLanguageWithoutDefaults(t *testing.T) {
	r := testutil.NewRepo(t)
	r.Commit("root", map[string]string{"main.sh": "echo 0\n"})
	keep := r.Commit("Fix main", map[string]string{
		"main.sh":        "echo 1\n",
		"spec/main.spec": "1\n",
	})
	opts := Options{Repo: r.Dir, Out: t.TempDir(), MaxFiles: 10, MaxLines: 400, Remote: remote, Language: "sh"}

	if _, err := Run(context.Background(), opts, &bytes.Buffer{}); !errors.Is(err, ErrNoLanguage) {
		t.Fatalf("error %v, want ErrNoLanguage without --test-glob", err)
	}
	opts.Language = ""
	if _, err := Run(context.Background(), opts, &bytes.Buffer{}); !errors.Is(err, ErrNoLanguage) {
		t.Fatalf("error %v, want ErrNoLanguage with no language to detect", err)
	}

	opts.Language, opts.TestGlobs = "sh", []string{"spec/**"}
	res, err := Run(context.Background(), opts, &bytes.Buffer{})
	if err != nil || len(res.Written) != 1 {
		t.Fatalf("written %v, err %v", res.Written, err)
	}
	tk, err := task.Load(res.Written[0])
	if err != nil {
		t.Fatal(err)
	}
	if tk.FixCommit != keep || !slices.Equal(tk.TestGlobs, []string{"spec/**"}) || !slices.Equal(tk.Oracle.Files, []string{"spec/main.spec"}) {
		t.Errorf("fix %q, test_globs %v, oracle files %v", tk.FixCommit, tk.TestGlobs, tk.Oracle.Files)
	}
	// The human still has to give a language without defaults its commands.
	if err := lang.Validate(tk); err == nil || !strings.Contains(err.Error(), "setup_command, test_command") {
		t.Errorf("validate: %v", err)
	}
}

func TestRunDoesNotChangeTheRepository(t *testing.T) {
	f := newFixture(t)
	before := f.repo.Git("status", "--porcelain=v2", "--branch")
	refs := f.repo.Git("for-each-ref")
	if _, err := Run(context.Background(), mineOpts(f, t.TempDir()), &bytes.Buffer{}); err != nil {
		t.Fatal(err)
	}
	if after := f.repo.Git("status", "--porcelain=v2", "--branch"); after != before {
		t.Errorf("status changed:\n%s\n%s", before, after)
	}
	if after := f.repo.Git("for-each-ref"); after != refs {
		t.Errorf("refs changed:\n%s\n%s", refs, after)
	}
	if wt := f.repo.Git("worktree", "list"); strings.Count(wt, "\n") != 0 {
		t.Errorf("worktrees: %s", wt)
	}
}

func TestRunNotARepository(t *testing.T) {
	_, err := Run(context.Background(), Options{Repo: t.TempDir(), Out: t.TempDir(), MaxFiles: 10, MaxLines: 400, Language: "go"}, &bytes.Buffer{})
	if err == nil || errors.Is(err, ErrNoLanguage) {
		t.Fatalf("error %v, want a git error", err)
	}
}

func TestClassify(t *testing.T) {
	goGlobs := []string{"**/*_test.go"}
	tests := []struct {
		file  string
		globs []string
		want  kind
	}{
		{"a.go", goGlobs, kindCode},
		{"pkg/a_test.go", goGlobs, kindTest},
		{"a_test.go", goGlobs, kindTest},
		{"go.sum", goGlobs, kindExcluded},
		{"sub/go.sum", goGlobs, kindExcluded},
		{"web/package-lock.json", nil, kindExcluded},
		{"yarn.lock", nil, kindExcluded},
		{"vendor/x/x.go", goGlobs, kindExcluded},
		{"vendor/x/x_test.go", goGlobs, kindExcluded},
		{"a/node_modules/x/index.js", nil, kindExcluded},
		{"api/api.pb.go", goGlobs, kindExcluded},
		{"api/models_generated.go", goGlobs, kindExcluded},
		{"static/app.min.js", nil, kindExcluded},
		{"README.md", goGlobs, kindDoc},
		{"docs/guide.html", goGlobs, kindDoc},
		{"README", goGlobs, kindDoc},
		{"LICENSE", goGlobs, kindDoc},
		{"tests/data.txt", []string{"tests/**"}, kindTest},
		{"main.py", nil, kindCode},
	}
	for _, tt := range tests {
		if got := classify(tt.file, tt.globs); got != tt.want {
			t.Errorf("classify(%q) = %d, want %d", tt.file, got, tt.want)
		}
	}
}

func TestDetectLanguage(t *testing.T) {
	tests := []struct {
		name  string
		paths []string
		want  string
	}{
		{"go", []string{"a.go", "b.go", "go.mod", "go.sum", "x.py"}, "go"},
		{"node by several extensions", []string{"a.ts", "b.tsx", "c.js", "d.py"}, "node"},
		{"docs and vendored files do not vote", []string{"a.py", "README.md", "vendor/a.go", "vendor/b.go", "docs/c.go"}, "python"},
		{"a tie goes to the first name", []string{"a.go", "a.py"}, "go"},
		{"cpp", []string{"a.cc", "a.h", "b.cpp"}, "cpp"},
		{"php", []string{"src/A.php"}, "php"},
		{"nothing known", []string{"Makefile", "x.sh"}, ""},
	}
	for _, tt := range tests {
		if got := detectLanguage(tt.paths); got != tt.want {
			t.Errorf("%s: %q, want %q", tt.name, got, tt.want)
		}
	}
}

func TestSanitizeName(t *testing.T) {
	tests := []struct{ in, want string }{
		{"https://github.com/org/pgqueue.git", "pgqueue"},
		{"https://github.com/org/pgqueue/", "pgqueue"},
		{"git@github.com:org/My_Repo.git", "my-repo"},
		{"/home/me/src/Orche", "orche"},
		{"/tmp/TestX/001", "001"},
		{"/", "repo"},
		{"", "repo"},
		{"/x/__.git", "repo"},
	}
	for _, tt := range tests {
		if got := sanitizeName(tt.in); got != tt.want {
			t.Errorf("sanitizeName(%q) = %q, want %q", tt.in, got, tt.want)
		}
	}
}

func TestGuessCategory(t *testing.T) {
	tests := []struct{ subject, want string }{
		{"Fix retry delay", task.CategoryBugfix},
		{"fixes #12", task.CategoryBugfix},
		{"Resolve bug in parser", task.CategoryBugfix},
		{"Bugfix: nil map", task.CategoryBugfix},
		{"Add per-action effort", task.CategoryFeature},
		{"Prefix the log lines", task.CategoryFeature},
		{"Handle debugging flags", task.CategoryFeature},
	}
	for _, tt := range tests {
		if got := guessCategory(tt.subject); got != tt.want {
			t.Errorf("guessCategory(%q) = %q, want %q", tt.subject, got, tt.want)
		}
	}
}

func TestOracleCommand(t *testing.T) {
	goDiff := "+++ b/q/retry_test.go\n+func TestRetry(t *testing.T) {\n+func TestBackoff(t *testing.T) {\n+func TestRetry(t *testing.T) {\n+func helper() {\n func TestOld(t *testing.T) {\n"
	tests := []struct {
		name     string
		language string
		files    []string
		diff     string
		pkg      string
		want     string
	}{
		{"go", "go", []string{"q/retry_test.go", "q/other_test.go", "top_test.go"}, goDiff,
			"", "go test ./ ./q/ -run '^(TestRetry|TestBackoff)$'"},
		{"go without added tests", "go", []string{"q/retry_test.go"}, " func TestOld(t *testing.T) {\n", "", "go test ./q/"},
		{"python", "python", []string{"tests/test_a.py", "tests/test_b.py"}, "", "", ".venv/bin/pytest -q tests/test_a.py tests/test_b.py"},
		{"node jest", "node", []string{"a.test.ts"}, "", `{"scripts":{"test":"jest --coverage"}}`, "npx jest a.test.ts"},
		{"node vitest", "node", []string{"a.test.ts"}, "", `{"scripts":{"test":"vitest run"}}`, "npx vitest run a.test.ts"},
		{"node mocha", "node", []string{"test/a.js"}, "", `{"scripts":{"test":"eslint . && mocha"}}`, "npx mocha test/a.js"},
		{"node unknown runner", "node", []string{"test/a.js"}, "", `{"scripts":{"test":"node run-tests.js"}}`, "npm test"},
		{"node without package.json", "node", []string{"test/a.js"}, "", "", "npm test"},
		{"php", "php", []string{"tests/AFooTest.php"}, "+++ b/tests/AFooTest.php\n+    public function testFoo(): void\n+    public function testBar()\n", "",
			"vendor/bin/phpunit --filter '(testFoo|testBar)'"},
		{"php without names", "php", []string{"tests/AFooTest.php"}, "", "", "vendor/bin/phpunit tests/AFooTest.php"},
		{"cpp", "cpp", []string{"tests/a_test.cc"}, "+TEST(Parser, Empty) {\n+TEST_F(Lexer, Eof) {\n+TEST(Parser, Nested) {\n", "",
			"cmake --build build -j && ctest --test-dir build -R 'Parser|Lexer'"},
		{"cpp without names", "cpp", []string{"tests/a_test.cc"}, "", "", "cmake --build build -j && ctest --test-dir build"},
		{"no recipe", "sh", []string{"spec/a"}, "", "", ""},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := oracleCommand(tt.language, tt.files, tt.diff, tt.pkg); got != tt.want {
				t.Errorf("got %q, want %q", got, tt.want)
			}
		})
	}
}

func TestNodeSetupFollowsTheLockFile(t *testing.T) {
	r := testutil.NewRepo(t)
	r.Commit("root", map[string]string{
		"package.json":   `{"scripts":{"test":"vitest run"}}`,
		"pnpm-lock.yaml": "lockfileVersion: 9\n",
		"src/a.ts":       "export const a = 0\n",
	})
	keep := r.Commit("Fix a", map[string]string{
		"src/a.ts":      "export const a = 1\n",
		"src/a.test.ts": "test('a', () => {})\n",
	})
	res, err := Run(context.Background(), Options{Repo: r.Dir, Out: t.TempDir(), MaxFiles: 10, MaxLines: 400, Remote: remote}, &bytes.Buffer{})
	if err != nil || len(res.Written) != 1 {
		t.Fatalf("written %v, err %v", res.Written, err)
	}
	tk, err := task.Load(res.Written[0])
	if err != nil {
		t.Fatal(err)
	}
	if tk.FixCommit != keep || tk.Language != "node" {
		t.Fatalf("fix %q, language %q", tk.FixCommit, tk.Language)
	}
	if tk.SetupCommand != "pnpm i --frozen-lockfile" || tk.Oracle.Command != "npx vitest run src/a.test.ts" {
		t.Errorf("setup %q, oracle %q", tk.SetupCommand, tk.Oracle.Command)
	}
	if err := lang.Validate(tk); err != nil {
		t.Error(err)
	}
}

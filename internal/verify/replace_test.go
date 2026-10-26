package verify

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"orchestrator/internal/domain"
)

type fakeLister struct {
	files []string
	err   error
}

func (f fakeLister) ListFiles(ctx context.Context) ([]string, error) { return f.files, f.err }

// replaceRepo writes files (path -> content) into a new directory and returns it with the
// list a lister gives for it.
func replaceRepo(t *testing.T, files map[string]string) (string, []string) {
	t.Helper()
	root := t.TempDir()
	var list []string
	for rel, content := range files {
		full := filepath.Join(root, filepath.FromSlash(rel))
		if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(full, []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
		list = append(list, rel)
	}
	return root, list
}

func replaceTools(t *testing.T, root string, list []string, g Guard) (*Tools, string) {
	t.Helper()
	spec := testSpec(t)
	spec.RepoRoot = root
	spec.TestCommand, spec.Checks, spec.Sandbox, spec.TestEnv = "", nil, nil, nil
	spec.Replace = &ReplaceSpec{}
	if g == nil {
		g = &fakeGuard{}
	}
	tools, dir := newTools(t, spec, &fakeRunner{}, g)
	tools.Files = fakeLister{files: list}
	return tools, dir
}

func readFile(t *testing.T, root, rel string) string {
	t.Helper()
	data, err := os.ReadFile(filepath.Join(root, filepath.FromSlash(rel)))
	if err != nil {
		t.Fatal(err)
	}
	return string(data)
}

func TestReplace_RewritesEveryFileAndSkipsWhatItMustNotTouch(t *testing.T) {
	root, list := replaceRepo(t, map[string]string{
		"a.go":                     "PlanArtifact x; PlanArtifact y\n",
		"pkg/b.go":                 "var _ PlanArtifact\n",
		"pkg/c.go":                 "nothing here\n",
		".env":                     "PlanArtifact=secret\n",
		"config/credentials.yaml":  "PlanArtifact: 1\n",
		".github/workflows/ci.yml": "PlanArtifact\n",
		"bin.dat":                  "PlanArtifact\x00\n",
	})
	if err := os.WriteFile(filepath.Join(root, "big.txt"), []byte(strings.Repeat("PlanArtifact\n", maxReplaceFileBytes/12)), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(filepath.Join(root, "a.go"), filepath.Join(root, "link.go")); err != nil {
		t.Fatal(err)
	}
	list = append(list, "big.txt", "link.go")
	tools, dir := replaceTools(t, root, list, nil)

	res := tools.Call(context.Background(), domain.VerifyToolReplace, args(t, "old", "PlanArtifact", "new", "InvestigationArtifact"))
	if res.IsError || !strings.Contains(res.Text, "replaced 3 occurrences in 2 files") || !strings.Contains(res.Text, "a.go (2)") {
		t.Fatalf("answer = %+v", res)
	}
	if !strings.Contains(res.Text, "not searched: 3 deny-listed or protected files") {
		t.Errorf("answer does not count the excluded files: %s", res.Text)
	}
	if got := readFile(t, root, "a.go"); got != "InvestigationArtifact x; InvestigationArtifact y\n" {
		t.Errorf("a.go = %q", got)
	}
	if got := readFile(t, root, "pkg/b.go"); got != "var _ InvestigationArtifact\n" {
		t.Errorf("pkg/b.go = %q", got)
	}
	for _, rel := range []string{".env", "config/credentials.yaml", ".github/workflows/ci.yml", "bin.dat"} {
		if strings.Contains(readFile(t, root, rel), "Investigation") {
			t.Errorf("%s was rewritten", rel)
		}
	}
	if strings.Contains(readFile(t, root, "big.txt"), "Investigation") {
		t.Error("a file over the size bound was rewritten")
	}
	if info, err := os.Lstat(filepath.Join(root, "link.go")); err != nil || info.Mode()&os.ModeSymlink == 0 {
		t.Errorf("the symlink was replaced: %v %v", info, err)
	}
	calls := readCalls(t, dir)
	want := domain.VerifyCall{Tool: domain.VerifyToolReplace, Success: true, Files: 2, Replacements: 3}
	if len(calls) != 1 {
		t.Fatalf("calls = %+v", calls)
	}
	calls[0].Duration = 0
	if calls[0] != want {
		t.Errorf("record = %+v, want %+v", calls[0], want)
	}
	if err := calls[0].Validate(); err != nil {
		t.Error(err)
	}
	if strings.Contains(string(mustRead(t, filepath.Join(dir, callsFile))), "PlanArtifact") {
		t.Error("the record holds the text the model chose")
	}
}

func mustRead(t *testing.T, path string) []byte {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	return data
}

func TestReplace_WordPathsAndDryRun(t *testing.T) {
	root, list := replaceRepo(t, map[string]string{
		"internal/x.go": "Plan(); ActionPlan; Planning; plan.Plan\n",
		"docs/x.md":     "Plan\n",
	})
	tools, dir := replaceTools(t, root, list, nil)

	res := tools.Call(context.Background(), domain.VerifyToolReplace,
		args(t, "old", "Plan", "new", "Investigate", "word", true, "paths", []string{"*.go"}, "dry_run", true))
	if res.IsError || !strings.Contains(res.Text, "would replace 2 occurrences in 1 files") {
		t.Fatalf("dry run answer = %+v", res)
	}
	if got := readFile(t, root, "internal/x.go"); got != "Plan(); ActionPlan; Planning; plan.Plan\n" {
		t.Errorf("a dry run wrote %q", got)
	}

	res = tools.Call(context.Background(), domain.VerifyToolReplace,
		args(t, "old", "Plan", "new", "Investigate", "word", true, "paths", []string{"internal/"}))
	if res.IsError {
		t.Fatal(res.Text)
	}
	if got := readFile(t, root, "internal/x.go"); got != "Investigate(); ActionPlan; Planning; plan.Investigate\n" {
		t.Errorf("internal/x.go = %q", got)
	}
	if got := readFile(t, root, "docs/x.md"); got != "Plan\n" {
		t.Errorf("a file outside paths was rewritten: %q", got)
	}
	calls := readCalls(t, dir)
	if len(calls) != 2 || !calls[0].DryRun || calls[1].DryRun || calls[1].Replacements != 2 {
		t.Errorf("calls = %+v", calls)
	}
}

func TestReplace_KeepsTheFileMode(t *testing.T) {
	root, list := replaceRepo(t, map[string]string{"run.sh": "echo old\n"})
	if err := os.Chmod(filepath.Join(root, "run.sh"), 0o755); err != nil {
		t.Fatal(err)
	}
	tools, _ := replaceTools(t, root, list, nil)
	if res := tools.Call(context.Background(), domain.VerifyToolReplace, args(t, "old", "old", "new", "new")); res.IsError {
		t.Fatal(res.Text)
	}
	if info, err := os.Stat(filepath.Join(root, "run.sh")); err != nil || info.Mode().Perm() != 0o755 {
		t.Errorf("mode = %v, %v", info.Mode(), err)
	}
	entries, _ := os.ReadDir(root)
	if len(entries) != 1 {
		t.Errorf("a temporary file was left: %v", entries)
	}
}

func TestReplace_NeverWritesOutsideTheRepository(t *testing.T) {
	outside := t.TempDir()
	if err := os.WriteFile(filepath.Join(outside, "f.txt"), []byte("old\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	root := t.TempDir()
	if err := os.Symlink(outside, filepath.Join(root, "out")); err != nil {
		t.Fatal(err)
	}
	tools, _ := replaceTools(t, root, []string{"out/f.txt"}, nil)
	res := tools.Call(context.Background(), domain.VerifyToolReplace, args(t, "old", "old", "new", "new"))
	if res.IsError || !strings.Contains(res.Text, "no occurrence found") {
		t.Errorf("answer = %+v", res)
	}
	if got := readFile(t, outside, "f.txt"); got != "old\n" {
		t.Errorf("a file outside the repository was rewritten: %q", got)
	}
}

func TestReplace_Refuses(t *testing.T) {
	cases := map[string]json.RawMessage{
		"no arguments":     nil,
		"no old":           json.RawMessage(`{"new":"x"}`),
		"no new":           json.RawMessage(`{"old":"x"}`),
		"empty old":        json.RawMessage(`{"old":"","new":"x"}`),
		"old not a string": json.RawMessage(`{"old":1,"new":"x"}`),
		"same text":        json.RawMessage(`{"old":"x","new":"x"}`),
		"NUL in new":       json.RawMessage(`{"old":"x","new":"a\u0000b"}`),
		"too long":         json.RawMessage(`{"old":"` + strings.Repeat("x", maxReplaceText+1) + `","new":"y"}`),
		"unknown argument": json.RawMessage(`{"old":"x","new":"y","regex":true}`),
		"parent path":      json.RawMessage(`{"old":"x","new":"y","paths":["../x"]}`),
		"inner parent":     json.RawMessage(`{"old":"x","new":"y","paths":["a/../../x"]}`),
		"absolute path":    json.RawMessage(`{"old":"x","new":"y","paths":["/etc"]}`),
		"bad glob":         json.RawMessage(`{"old":"x","new":"y","paths":["["]}`),
		"paths not array":  json.RawMessage(`{"old":"x","new":"y","paths":"a"}`),
		"word not boolean": json.RawMessage(`{"old":"x","new":"y","word":"yes"}`),
		"not an object":    json.RawMessage(`["x"]`),
	}
	for name, raw := range cases {
		t.Run(name, func(t *testing.T) {
			root, list := replaceRepo(t, map[string]string{"a.txt": "x\n"})
			tools, dir := replaceTools(t, root, list, nil)
			res := tools.Call(context.Background(), domain.VerifyToolReplace, raw)
			if !res.IsError || !strings.Contains(res.Text, "invalid arguments") {
				t.Errorf("answer = %+v", res)
			}
			if got := readFile(t, root, "a.txt"); got != "x\n" {
				t.Errorf("a refused call wrote %q", got)
			}
			calls := readCalls(t, dir)
			if len(calls) != 1 || calls[0].Refused == "" || calls[0].Validate() != nil {
				t.Errorf("calls = %+v", calls)
			}
		})
	}
}

func TestReplace_GuardAndLimit(t *testing.T) {
	root, list := replaceRepo(t, map[string]string{"a.txt": "x\n"})
	tools, dir := replaceTools(t, root, list, &fakeGuard{changed: []string{".github/workflows/ci.yml"}})
	res := tools.Call(context.Background(), domain.VerifyToolReplace, args(t, "old", "x", "new", "y"))
	if !res.IsError || !strings.Contains(res.Text, "protected files changed") || readFile(t, root, "a.txt") != "x\n" {
		t.Errorf("answer = %+v", res)
	}
	if _, err := os.Stat(filepath.Join(dir, violationFile)); err != nil {
		t.Errorf("no violation recorded: %v", err)
	}

	tools, _ = replaceTools(t, root, list, nil)
	for i := range domain.MaxReplaceCalls {
		old, new := "x", "y"
		if i%2 == 1 {
			old, new = "y", "x"
		}
		if res := tools.Call(context.Background(), domain.VerifyToolReplace, args(t, "old", old, "new", new)); res.IsError {
			t.Fatalf("call %d: %s", i+1, res.Text)
		}
	}
	res = tools.Call(context.Background(), domain.VerifyToolReplace, args(t, "old", "x", "new", "y"))
	if !res.IsError || !strings.Contains(res.Text, "limit reached") || readFile(t, root, "a.txt") != "x\n" {
		t.Errorf("call over the limit: %+v", res)
	}
}

func TestReplace_ListFailureAndNoLister(t *testing.T) {
	root, list := replaceRepo(t, map[string]string{"a.txt": "x\n"})
	tools, _ := replaceTools(t, root, list, nil)
	tools.Files = fakeLister{err: os.ErrPermission}
	if res := tools.Call(context.Background(), domain.VerifyToolReplace, args(t, "old", "x", "new", "y")); !res.IsError {
		t.Errorf("a failed listing answered %+v", res)
	}
	tools.Files = nil
	if res := tools.Call(context.Background(), domain.VerifyToolReplace, args(t, "old", "x", "new", "y")); !res.IsError {
		t.Errorf("no lister answered %+v", res)
	}
}

// The test tools and replace have their own limits, and a restarted server counts the lines
// already recorded against the right one.
func TestReplace_LimitsAreSeparateAcrossARestart(t *testing.T) {
	root, list := replaceRepo(t, map[string]string{"a.txt": "x\n"})
	spec := testSpec(t)
	spec.RepoRoot, spec.MaxCalls, spec.Replace = root, 1, &ReplaceSpec{}
	tools, dir := newTools(t, spec, &fakeRunner{}, &fakeGuard{})
	tools.Files = fakeLister{files: list}
	tools.Call(context.Background(), domain.VerifyToolReplace, args(t, "old", "x", "new", "y"))
	tools.Call(context.Background(), domain.VerifyToolReplace, args(t, "old", "y", "new", "x"))

	restarted, err := NewTools(spec, dir, &fakeRunner{}, &fakeGuard{})
	if err != nil {
		t.Fatal(err)
	}
	restarted.Files = fakeLister{files: list}
	if res := restarted.Call(context.Background(), domain.VerifyToolRunTests, nil); res.IsError {
		t.Errorf("run_tests after two replace calls: %s", res.Text)
	}
	if res := restarted.Call(context.Background(), domain.VerifyToolRunTests, nil); !res.IsError {
		t.Error("the second run_tests passed a limit of 1")
	}
	res := restarted.Call(context.Background(), domain.VerifyToolReplace, args(t, "old", "x", "new", "y"))
	if res.IsError || !strings.Contains(res.Text, "replace calls left in this step: 47") {
		t.Errorf("replace after the restart: %+v", res)
	}
}

func TestOffers_ReplaceOnly(t *testing.T) {
	root, list := replaceRepo(t, map[string]string{"a.txt": "x\n"})
	tools, _ := replaceTools(t, root, list, nil)
	for tool, want := range map[string]bool{
		domain.VerifyToolRunTests: false, domain.VerifyToolRunCheck: false, domain.VerifyToolReplace: true,
	} {
		if got := tools.Offers(tool); got != want {
			t.Errorf("Offers(%s) = %v, want %v", tool, got, want)
		}
	}
	tools.Spec = testSpec(t)
	if tools.Offers(domain.VerifyToolReplace) {
		t.Error("replace offered without Spec.Replace")
	}
}

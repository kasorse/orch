package config

import (
	"errors"
	"flag"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	"gopkg.in/yaml.v3"

	"orchbench/internal/task"
	"orchbench/internal/testutil"
)

var update = flag.Bool("update", false, "rewrite testdata/*.golden* from the current output")

// configsDir is bench/configs, the files the benchmark ships.
const configsDir = "../../configs"

func goldenTask() task.Task {
	return task.Task{
		ID:          "go-retry",
		Language:    "go",
		Task:        "mul returns the sum of its arguments instead of the product; fix it.",
		TestCommand: `go test ./... -run "TestA|TestB" && echo 'ok'`,
		Sandbox: task.Sandbox{
			RoBinds: []string{"~/go/pkg/mod", "/opt/my tools"},
			RwBinds: []string{"~/.cache/go-build", `/tmp/a"b`},
		},
		Timeout: task.Duration(30 * time.Minute),
	}
}

func load(t *testing.T, name string) Config {
	t.Helper()
	c, err := Load(configsDir, name)
	if err != nil {
		t.Fatalf("Load(%s): %v", name, err)
	}
	return c
}

func checkGolden(t *testing.T, name string, got []byte) {
	t.Helper()
	path := filepath.Join("testdata", name)
	if *update {
		if err := os.WriteFile(path, got, 0o644); err != nil {
			t.Fatal(err)
		}
	}
	want, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read golden (go test ./internal/config -update): %v", err)
	}
	if string(got) != string(want) {
		t.Errorf("%s differs from the rendering (go test ./internal/config -update rewrites it)\n--- got\n%s\n--- want\n%s", name, got, want)
	}
}

func TestShippedConfigs(t *testing.T) {
	names, err := Names(configsDir)
	if err != nil {
		t.Fatal(err)
	}
	if want := []string{"A", "B", "C", "D"}; !reflect.DeepEqual(names, want) {
		t.Fatalf("configs = %v, want %v", names, want)
	}

	cases := []struct {
		name   string
		kind   string
		stages []string
	}{
		{"A", KindClaude, nil},
		{"B", KindOrchestrator, nil},
		{"C", KindOrchestrator, nil},
		{"D", KindOrchestrator, []string{"development", "testing"}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			c := load(t, tc.name)
			if c.Kind != tc.kind || !reflect.DeepEqual(c.Stages, tc.stages) {
				t.Errorf("kind %q stages %v, want %q %v", c.Kind, c.Stages, tc.kind, tc.stages)
			}
			if c.Model != "sonnet" || c.Name != tc.name {
				t.Errorf("name %q model %q", c.Name, c.Model)
			}
			if len(c.SHA256) != 64 {
				t.Errorf("SHA256 = %q", c.SHA256)
			}
		})
	}

	a := load(t, "A")
	if want := []string{"Read", "Grep", "Glob", "Edit", "Write", "Bash"}; !reflect.DeepEqual(a.Tools, want) {
		t.Errorf("A tools = %v, want %v", a.Tools, want)
	}
}

// orchbench waits for a usage limit's reset itself and runs the cell again (run.Env.Exec). An
// orchestrator that waited instead would be killed at the task's timeout and the cell recorded as
// a timeout fail, so every orchestrator config turns the orchestrator's own wait off.
func TestOrchestratorConfigsDoNotWaitForTheLimit(t *testing.T) {
	for _, name := range []string{"B", "C", "D"} {
		execution, ok := renderedMap(t, name)["execution"].(map[string]any)
		if !ok || execution["rate_limit_wait"] != "0s" {
			t.Errorf("%s: execution = %v, want rate_limit_wait: 0s", name, execution)
		}
	}
}

func TestRenderGolden(t *testing.T) {
	for _, name := range []string{"B", "C", "D"} {
		t.Run(name, func(t *testing.T) {
			c := load(t, name)
			out, sha, err := c.RenderConfig(goldenTask())
			if err != nil {
				t.Fatal(err)
			}
			if sha != task.SHA256Bytes(out) {
				t.Errorf("sha %s is not the hash of the text", sha)
			}
			checkGolden(t, name+".golden.yaml", out)
		})
	}

	t.Run("A prompt", func(t *testing.T) {
		got, err := load(t, "A").RenderPrompt(goldenTask())
		if err != nil {
			t.Fatal(err)
		}
		checkGolden(t, "A.prompt.golden.txt", []byte(got))
	})
}

// renderedMap is the rendering of a shipped orchestrator config as a generic YAML map.
func renderedMap(t *testing.T, name string) map[string]any {
	t.Helper()
	out, _, err := load(t, name).RenderConfig(goldenTask())
	if err != nil {
		t.Fatal(err)
	}
	var m map[string]any
	if err := yaml.Unmarshal(out, &m); err != nil {
		t.Fatal(err)
	}
	return m
}

// D carries its own copy of C's template. D measures the stages C would not choose only while
// everything but the decision section (not called under --stage) is identical, so a threshold, a
// model or a limit edited in one file and not the other fails here.
func TestOrchestratorConfigsDifferOnlyInTheDecision(t *testing.T) {
	base := renderedMap(t, "C")
	delete(base, "decision")
	for _, name := range []string{"D"} {
		t.Run(name, func(t *testing.T) {
			got := renderedMap(t, name)
			delete(got, "decision")
			if !reflect.DeepEqual(got, base) {
				t.Errorf("%s differs from C outside the decision section:\n got %v\nwant %v", name, got, base)
			}
		})
	}
}

// C and D are orchestrator.example.yaml with the per-task fields filled in, as the template
// comment says. A change to the example's thresholds, limits or models must be made here too,
// or C measures defaults nobody ships. B is the owner's own configuration and follows no file of
// this repository.
func TestOrchestratorConfigsFollowTheExample(t *testing.T) {
	raw, err := os.ReadFile("../../../orchestrator.example.yaml")
	if err != nil {
		t.Fatal(err)
	}

	for _, name := range []string{"C", "D"} {
		t.Run(name, func(t *testing.T) {
			var example map[string]any
			if err := yaml.Unmarshal(raw, &example); err != nil {
				t.Fatal(err)
			}
			got := renderedMap(t, name)

			// implement runs the common model; decision, test and execution are filled per
			// task or deliberately different (see the comment of C.yaml), and clarify is off in
			// every benchmark config: a benchmark has no operator, and the step is not what it measures.
			actions, ok := example["actions"].(map[string]any)
			if !ok {
				t.Fatalf("actions is not a mapping in the example: %v", example["actions"])
			}
			actions["implement"] = map[string]any{"provider": "claude", "model": "sonnet"}
			for _, key := range []string{"decision", "test", "execution", "clarify"} {
				delete(example, key)
				delete(got, key)
			}
			if !reflect.DeepEqual(got, example) {
				t.Errorf("%s differs from orchestrator.example.yaml outside decision, test, execution and clarify:\n got %v\nwant %v", name, got, example)
			}
		})
	}
}

func TestRenderKeepsOddValuesIntact(t *testing.T) {
	tk := goldenTask()
	tk.TestCommand = "echo \"a\\b\" && sh -c 'x: y # z'\ncat <<'EOF'\nпривет\nEOF"
	tk.Sandbox.RoBinds = []string{"/a b/c: d", "/x#y", `/q"uote`, "/tab\there", "/ünï", "/{{.Model}}"}
	tk.Sandbox.RwBinds = nil

	out, _, err := load(t, "D").RenderConfig(tk)
	if err != nil {
		t.Fatal(err)
	}
	var got struct {
		Test struct {
			Command string   `yaml:"command"`
			RoBinds []string `yaml:"ro_binds"`
			RwBinds []string `yaml:"rw_binds"`
		} `yaml:"test"`
	}
	if err := yaml.Unmarshal(out, &got); err != nil {
		t.Fatal(err)
	}
	if got.Test.Command != tk.TestCommand {
		t.Errorf("command = %q, want %q", got.Test.Command, tk.TestCommand)
	}
	if !reflect.DeepEqual(got.Test.RoBinds, tk.Sandbox.RoBinds) {
		t.Errorf("ro_binds = %q, want %q", got.Test.RoBinds, tk.Sandbox.RoBinds)
	}
	if len(got.Test.RwBinds) != 0 || !strings.Contains(string(out), "rw_binds: []\n") {
		t.Errorf("empty rw_binds must render as []:\n%s", out)
	}
}

func TestRenderUsesTheLanguageTestCommand(t *testing.T) {
	tk := goldenTask()
	tk.TestCommand = ""
	out, _, err := load(t, "D").RenderConfig(tk)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(out), `command: "go test ./..."`) {
		t.Errorf("the go default is missing:\n%s", out)
	}

	prompt, err := load(t, "A").RenderPrompt(tk)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasSuffix(prompt, "Run the tests with: go test ./...\n") {
		t.Errorf("prompt = %q", prompt)
	}
}

func TestRenderErrors(t *testing.T) {
	noCommand := goldenTask()
	noCommand.TestCommand, noCommand.Language = "", "cobol"
	noTimeout := goldenTask()
	noTimeout.Timeout = 0

	cases := []struct {
		name string
		do   func() error
		want string
	}{
		{"claude config has no config template", func() error { _, _, err := load(t, "A").RenderConfig(goldenTask()); return err }, "not an orchestrator config"},
		{"orchestrator config has no prompt", func() error { _, err := load(t, "B").RenderPrompt(goldenTask()); return err }, "not a claude config"},
		{"no test command", func() error { _, _, err := load(t, "B").RenderConfig(noCommand); return err }, "no test_command"},
		{"no test command for the prompt", func() error { _, err := load(t, "A").RenderPrompt(noCommand); return err }, "no test_command"},
		{"no timeout", func() error { _, _, err := load(t, "B").RenderConfig(noTimeout); return err }, "no timeout"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if err := tc.do(); err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Errorf("error = %v, want one containing %q", err, tc.want)
			}
		})
	}
}

func TestRenderRejectsInvalidYAML(t *testing.T) {
	dir := writeDir(t, map[string]string{
		"X.yaml": "kind: orchestrator\nconfig: |\n  a: [{{.Model}}\n",
	})
	c, err := Load(dir, "X")
	if err != nil {
		t.Fatal(err)
	}
	if _, _, err := c.RenderConfig(goldenTask()); err == nil || !strings.Contains(err.Error(), "not valid YAML") {
		t.Errorf("error = %v, want a not valid YAML error", err)
	}
}

func writeDir(t *testing.T, files map[string]string) string {
	t.Helper()
	dir := t.TempDir()
	if _, ok := files["common.yaml"]; !ok {
		files["common.yaml"] = "model: sonnet\n"
	}
	for name, content := range files {
		if err := os.WriteFile(filepath.Join(dir, name), []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	return dir
}

func TestLoadErrors(t *testing.T) {
	cases := []struct {
		name  string
		files map[string]string
		load  string
		want  string
	}{
		{"unknown key", map[string]string{"X.yaml": "kind: claude\ntools: [Read]\nbogus: 1\n"}, "X", "bogus"},
		{"empty file", map[string]string{"X.yaml": ""}, "X", "empty file"},
		{"two documents", map[string]string{"X.yaml": "kind: claude\ntools: [Read]\n---\nkind: claude\n"}, "X", "more than one YAML document"},
		{"no kind", map[string]string{"X.yaml": "tools: [Read]\n"}, "X", "kind is required"},
		{"bad kind", map[string]string{"X.yaml": "kind: codex\n"}, "X", `kind "codex"`},
		{"claude without tools", map[string]string{"X.yaml": "kind: claude\n"}, "X", "needs tools"},
		{"claude with stages", map[string]string{"X.yaml": "kind: claude\ntools: [Read]\nstages: [testing]\n"}, "X", "no stages"},
		{"claude with a template", map[string]string{"X.yaml": "kind: claude\ntools: [Read]\nconfig: x\n"}, "X", "no config template"},
		{"claude with a tool list in one string", map[string]string{"X.yaml": "kind: claude\ntools: [\"Read,Edit\"]\n"}, "X", "one tool name"},
		{"claude suffix with an unknown variable", map[string]string{"X.yaml": "kind: claude\ntools: [Read]\nprompt_suffix: \"{{.Oracle}}\"\n"}, "X", "Oracle"},
		{"orchestrator without a template", map[string]string{"X.yaml": "kind: orchestrator\n"}, "X", "needs a config template"},
		{"orchestrator with tools", map[string]string{"X.yaml": "kind: orchestrator\nconfig: x\ntools: [Read]\n"}, "X", "no tools"},
		{"orchestrator with a suffix", map[string]string{"X.yaml": "kind: orchestrator\nconfig: x\nprompt_suffix: x\n"}, "X", "no prompt_suffix"},
		{"unknown stage", map[string]string{"X.yaml": "kind: orchestrator\nconfig: x\nstages: [coding]\n"}, "X", `unknown stage "coding"`},
		{"repeated stage", map[string]string{"X.yaml": "kind: orchestrator\nconfig: x\nstages: [testing, testing]\n"}, "X", "listed twice"},
		{"unknown template variable", map[string]string{"X.yaml": "kind: orchestrator\nconfig: |\n  a: {{.Nope}}\n"}, "X", "Nope"},
		{"template syntax", map[string]string{"X.yaml": "kind: orchestrator\nconfig: |\n  a: {{.Model\n"}, "X", "config template"},
		{"no model", map[string]string{"common.yaml": "model: \"\"\n", "X.yaml": "kind: claude\ntools: [Read]\n"}, "X", "model is required"},
		{"common is not a config", map[string]string{}, "common", "not be"},
		{"a path is not a name", map[string]string{}, "../X", "must match"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			_, err := Load(writeDir(t, tc.files), tc.load)
			if err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Errorf("error = %v, want one containing %q", err, tc.want)
			}
		})
	}

	t.Run("missing file", func(t *testing.T) {
		_, err := Load(writeDir(t, map[string]string{}), "X")
		if !errors.Is(err, fs.ErrNotExist) {
			t.Errorf("error = %v, want not-exist", err)
		}
	})
}

// orchestratorBinary is the built orchestrator: $ORCHBENCH_ORCHESTRATOR, else bin/orchestrator
// of the repository (SPEC.md §13.1). The test is skipped without one.
func orchestratorBinary(t *testing.T) string {
	t.Helper()
	path := os.Getenv("ORCHBENCH_ORCHESTRATOR")
	if path == "" {
		path = "../../../bin/orchestrator"
	}
	abs, err := filepath.Abs(path)
	if err != nil {
		t.Fatal(err)
	}
	if info, err := os.Stat(abs); err != nil || info.IsDir() {
		t.Skipf("no orchestrator binary at %s (go build -o bin/orchestrator ./cmd/orchestrator, or set ORCHBENCH_ORCHESTRATOR)", abs)
	}
	return abs
}

// The rendered configs must be ones the orchestrator accepts. `validate` also runs the real
// claude --version and a bwrap probe, so the test is skipped where either is missing.
func TestRenderedConfigsPassOrchestratorValidate(t *testing.T) {
	bin := orchestratorBinary(t)
	if _, err := exec.LookPath("claude"); err != nil {
		t.Skip("claude is not on PATH: orchestrator validate checks the provider CLI")
	}
	testutil.RequireBwrap(t)

	tk := goldenTask()
	tk.TestCommand = "true"
	tk.Sandbox = task.Sandbox{}

	for _, name := range []string{"B", "C", "D"} {
		t.Run(name, func(t *testing.T) {
			out, _, err := load(t, name).RenderConfig(tk)
			if err != nil {
				t.Fatal(err)
			}
			dir := t.TempDir()
			path := filepath.Join(dir, name+".yaml")
			if err := os.WriteFile(path, out, 0o644); err != nil {
				t.Fatal(err)
			}
			cmd := exec.Command(bin, "--config", path, "validate")
			cmd.Dir = dir
			res, err := cmd.CombinedOutput()
			if err == nil {
				return
			}
			if strings.Contains(string(res), "has not been verified") {
				t.Skipf("the installed claude is outside the range the orchestrator verified:\n%s", res)
			}
			t.Fatalf("orchestrator validate: %v\n%s", err, res)
		})
	}
}

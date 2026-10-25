package integration

import (
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// assertAuthorities checks, for a completed plan -> implement -> test -> review run
// with an LLM decision provider on claude, that every call ran with its authority
// (specs/permissions/SPEC.md §5.1): the tools on the real argv, the secret deny rules for
// the steps that read files, the authority recorded in run.json, and the authority line
// printed before execution.
func assertAuthorities(t *testing.T, repoDir, argsDir, stdout string) {
	t.Helper()

	wantTools := map[string]string{
		"decide-context":  "",
		"decide-classify": "",
		"plan":            "Read,Grep,Glob",
		"implement":       "Read,Grep,Glob,Edit,Write",
		"review":          "Read,Grep,Glob",
	}
	for action, tools := range wantTools {
		args := readArgs(t, argsDir, action)
		if got, ok := flagValue(args, "--tools"); !ok || got != tools {
			t.Errorf("%s: --tools = %q (present %v), want %q\nargv: %q", action, got, ok, tools, args)
		}
		if got, _ := flagValue(args, "--permission-mode"); got != "dontAsk" {
			t.Errorf("%s: --permission-mode = %q, want dontAsk", action, got)
		}
		settings, hasSettings := flagValue(args, "--settings")
		readsFiles := tools != ""
		if readsFiles && !strings.Contains(settings, "Read(**/.env)") {
			t.Errorf("%s: expected the secret deny rules in --settings, got %q", action, settings)
		}
		if !readsFiles && hasSettings {
			t.Errorf("%s: a call without file tools needs no deny rules, got %q", action, settings)
		}
		if n := len(args); n < 2 || args[n-2] != "--model" {
			t.Errorf("%s: the prompt is on stdin, so --model is last with its value: %q", action, args)
		}
	}

	raw, ok := runFile(t, repoDir, "run.json")
	if !ok {
		t.Fatal("run.json was not written")
	}
	var run struct {
		Executions []struct {
			Action   string `json:"action"`
			Provider *struct {
				Authority string `json:"authority"`
			} `json:"provider"`
		} `json:"executions"`
	}
	if err := json.Unmarshal([]byte(raw), &run); err != nil {
		t.Fatalf("run.json: %v", err)
	}
	wantAuthority := map[string]string{"plan": "read", "implement": "edit", "review": "read"}
	for _, e := range run.Executions {
		want, isAI := wantAuthority[e.Action]
		switch {
		case !isAI && e.Provider != nil:
			t.Errorf("%s: no AI call, but run.json records a provider", e.Action)
		case isAI && (e.Provider == nil || e.Provider.Authority != want):
			t.Errorf("%s: run.json authority = %+v, want %q", e.Action, e.Provider, want)
		}
	}

	if want := "authority: plan=read, implement=edit, review=read; test runs: go test ./..."; !strings.Contains(stdout, want) {
		t.Errorf("expected %q in the console output:\n%s", want, stdout)
	}
}

func readArgs(t *testing.T, dir, action string) []string {
	t.Helper()
	data, err := os.ReadFile(filepath.Join(dir, action+".args"))
	if err != nil {
		t.Fatalf("no recorded argv for %s: %v", action, err)
	}
	return strings.Split(strings.TrimSuffix(string(data), "\n"), "\n")
}

// flagValue returns the argument following flag.
func flagValue(args []string, flag string) (string, bool) {
	for i := 0; i+1 < len(args); i++ {
		if args[i] == flag {
			return args[i+1], true
		}
	}
	return "", false
}

// commitAll commits everything in dir, for tests that shape the fixture before a run.
func commitAll(t *testing.T, dir, msg string) {
	t.Helper()
	for _, args := range [][]string{{"add", "-A"}, {"commit", "-qm", msg}} {
		cmd := exec.Command("git", args...)
		cmd.Dir = dir
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v\n%s", args, err, out)
		}
	}
}

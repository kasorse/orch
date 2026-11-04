package integration

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/kasorse/orch/internal/app"
	"github.com/kasorse/orch/internal/apperr"
	"github.com/kasorse/orch/internal/exitcode"
	"github.com/kasorse/orch/internal/testutil/fakeexec"
)

// The run of a Jira listener's ticket (specs/jira-listener/SPEC.md §7.2, specs/embed/SPEC.md §2.1):
// it gets its ticket from one validated task, makes no Jira call whatever the config says, runs the
// clarify step like any run, and says in its result line when it failed on a credential. The
// scenarios call the run as the listener does, in this process and in the repository's directory,
// while the process's working directory stays elsewhere (app.RunListenerTask); `run --listener-task`
// is deleted, and only its refusal is left of it.

const (
	listenerKey         = "PAY-12"
	listenerURL         = "https://jira.example.com/browse/PAY-12"
	listenerDescription = "PAY-12: Add() subtracts\n\nfix Add in fixture.go so that it adds instead of subtracting\n"
	// listenerSecretEnv and listenerKeepEnv are two variables the run's environment holds; the task
	// file names the first as a credential.
	listenerSecretEnv = "ORCH_LISTENER_TEST_SECRET"
	listenerKeepEnv   = "ORCH_LISTENER_TEST_KEEP"
	// listenerWrite is a jira.write that must not be obeyed.
	listenerWrite = "  write:\n    comment: true\n    transition: In Review\n"
)

// writeListenerTask writes a task file the listener would have written, after mutate changed it,
// and returns its path.
func writeListenerTask(t *testing.T, mutate ...func(m map[string]any)) string {
	t.Helper()
	m := map[string]any{}
	m["v"], m["key"], m["url"] = 1, listenerKey, listenerURL
	m["fetched_at"] = "2026-10-29T10:02:00Z"
	m["description"] = listenerDescription
	m["scrub_env"] = []string{listenerSecretEnv}
	for _, f := range mutate {
		f(m)
	}
	data, err := json.Marshal(m)
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(t.TempDir(), listenerKey+".1.json")
	if err := os.WriteFile(path, data, 0o644); err != nil {
		t.Fatal(err)
	}
	return path
}

// runListenerTask makes the listener's call of the run for the task in file, in repoDir, and returns
// what it wrote (stdout is the result line) and returned. The working directory is not changed: the
// run takes its directory as a parameter.
func runListenerTask(t *testing.T, repoDir, configPath, file string) (stdout, stderr string, err error) {
	t.Helper()
	var outBuf, errBuf bytes.Buffer
	err = app.RunListenerTask(context.Background(), repoDir, configPath, file, &outBuf, &errBuf)
	return outBuf.String(), errBuf.String(), err
}

// listenerRunJSON is the part of run.json these scenarios read.
type listenerRunJSON struct {
	ID            string `json:"id"`
	Status        string `json:"status"`
	FailureReason string `json:"failure_reason"`
	Commit        string `json:"commit"`
	Task          struct {
		Description string `json:"description"`
		Source      *struct {
			Kind      string `json:"kind"`
			Key       string `json:"key"`
			URL       string `json:"url"`
			FetchedAt string `json:"fetched_at"`
			Notes     bool   `json:"notes"`
			Listener  bool   `json:"listener"`
		} `json:"source"`
		Clarification *struct {
			Status string `json:"status"`
		} `json:"clarification"`
	} `json:"task"`
	Options   map[string]json.RawMessage `json:"options"`
	JiraWrite json.RawMessage            `json:"jira_write"`
	MR        *struct {
		State string `json:"state"`
		URL   string `json:"url"`
	} `json:"mr"`
}

func loadListenerRun(t *testing.T, repoDir string) listenerRunJSON {
	t.Helper()
	raw, ok := runFile(t, repoDir, "run.json")
	if !ok {
		t.Fatal("run.json was not written")
	}
	var run listenerRunJSON
	if err := json.Unmarshal([]byte(raw), &run); err != nil {
		t.Fatal(err)
	}
	return run
}

// newListenerRun is a worktree run that clarifies (the first check rates the task 0.42, the
// re-check 0.9), with the fake Jira the config points at, jira.write asking for a comment and a
// transition, and two variables in the environment, one of them named by the task file as a
// credential.
func newListenerRun(t *testing.T) (repoDir, cfg, invocationsLog, envDir string, jira *fakeJira) {
	t.Helper()
	repoDir, invocationsLog, _, _ = clarifyEnv(t, 0.42, 0.9)
	t.Setenv("XDG_STATE_HOME", t.TempDir())
	fakeexec.AddGitLabRemote(t, repoDir)
	// The fix lands in the worktree, relative to the working directory of the call.
	t.Setenv("FAKE_FIX_TARGET_FILE", "fixture.go")
	envDir = t.TempDir()
	t.Setenv("FAKE_ENV_DIR", envDir)
	t.Setenv(listenerSecretEnv, "a-credential-the-listener-names")
	t.Setenv(listenerKeepEnv, "kept")
	jira = startJira(t, nil)
	cfg = clarifyConfig(t, func(o *configOptions) {
		o.isolation = isolationAbsent
		o.jira = &jiraOptions{baseURL: jira.URL(), write: listenerWrite}
	})
	return repoDir, cfg, invocationsLog, envDir, jira
}

// A listener task records its source with listener: true, runs the clarify step, makes no request
// to Jira although the config has a jira section with jira.write, and leaves the credentials its
// file names out of every process it starts.
func TestListenerTask_RunsClarifyAndMakesNoJiraCall(t *testing.T) {
	repoDir, cfg, invocationsLog, envDir, jira := newListenerRun(t)
	file := writeListenerTask(t)

	stdout, stderr, err := runListenerTask(t, repoDir, cfg, file)
	mustSucceed(t, stdout, stderr, err)

	want := []string{"decide-context", "clarify", "decide-clarity", "decide-classify", "investigate", "implement", "review"}
	if got := invocations(t, invocationsLog); !equalStrings(got, want) {
		t.Errorf("invocations = %v, want %v: the clarify step runs as in any run", got, want)
	}
	if reqs := jira.Requests(); len(reqs) != 0 {
		t.Errorf("Jira received %d requests %+v, want none: the listener owns Jira", len(reqs), reqs)
	}

	run := loadListenerRun(t, repoDir)
	if run.Status != "completed" || run.Commit == "" || run.MR == nil || run.MR.State != "created" {
		t.Fatalf("status = %s, commit = %q, mr = %+v; want completed with its merge request\nstderr:\n%s", run.Status, run.Commit, run.MR, stderr)
	}
	src := run.Task.Source
	if src == nil || src.Kind != "jira" || src.Key != listenerKey || src.URL != listenerURL || !src.Listener || src.Notes ||
		src.FetchedAt != "2026-10-29T10:02:00Z" {
		t.Errorf("task.source = %+v, want the jira source of %s with listener: true", src, listenerKey)
	}
	if run.Task.Description != listenerDescription {
		t.Errorf("task.description = %q, want the file's", run.Task.Description)
	}
	if c := run.Task.Clarification; c == nil || c.Status != "clear" {
		t.Errorf("task.clarification = %+v, want a clarified task", c)
	}
	// jira.write is the service's, and the listener's child writes nothing: neither the option nor
	// a record of a write.
	if w, ok := run.Options["jira_write"]; ok {
		t.Errorf("options.jira_write = %s, want it empty", w)
	}
	if run.JiraWrite != nil {
		t.Errorf("run.json has jira_write %s", run.JiraWrite)
	}

	// The commit takes the ticket's key, as the commit of a run --jira does.
	if subject, _ := gitOutput(repoDir, "log", "-1", "--format=%s", run.Commit); !strings.HasPrefix(subject, listenerKey+": ") {
		t.Errorf("commit subject = %q, want the prefix %s:", subject, listenerKey)
	}

	// The line: the same fields as ever, and no failure kind.
	m := resultLine(t, stdout, stderr, err)
	if m["run_id"] != run.ID || m["status"] != "completed" || m["failure_kind"] != "" || m["mr"] == nil {
		t.Errorf("line = %v", m)
	}

	// The credential the file names reaches no call; one it does not name is inherited as ever.
	for _, action := range []string{"decide-context", "clarify", "decide-classify", "investigate", "implement", "review"} {
		env := callEnv(t, envDir, action)
		if _, ok := env[listenerSecretEnv]; ok {
			t.Errorf("%s inherited %s, which scrub_env names", action, listenerSecretEnv)
		}
		if env[listenerKeepEnv] != "kept" {
			t.Errorf("%s did not inherit %s: the environment dump proves nothing", action, listenerKeepEnv)
		}
	}
}

// A task file that fails its validation is a config error before the configuration is read or any
// provider is called, and creates no run.
func TestListenerTask_InvalidFileIsAConfigErrorBeforeAnyCall(t *testing.T) {
	tests := []struct {
		name   string
		mutate func(m map[string]any)
		want   string
	}{
		{"unknown key in the file", func(m map[string]any) { m["extra"] = 1 }, `"extra" is not a field`},
		{"bad ticket key", func(m map[string]any) { m["key"] = "pay-12" }, "not an issue key"},
		{"http url", func(m map[string]any) { m["url"] = "http://jira.example.com/browse/PAY-12" }, "not an absolute https URL"},
		{"no fetched_at", func(m map[string]any) { delete(m, "fetched_at") }, "fetched_at is not set"},
		{"empty description", func(m map[string]any) { m["description"] = "" }, "description is empty"},
		{"description over 1 MiB", func(m map[string]any) { m["description"] = strings.Repeat("x", 1<<20+1) }, "over the 1048576-byte limit"},
		{"file over 2 MiB", func(m map[string]any) { m["description"] = strings.Repeat("x", 2<<20) }, "is over 2097152 bytes"},
		{"bad scrub_env name", func(m map[string]any) { m["scrub_env"] = []string{"jira_token"} }, "scrub_env[0]"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			repoDir, cfg, invocationsLog, _, jira := newListenerRun(t)

			stdout, stderr, err := runListenerTask(t, repoDir, cfg, writeListenerTask(t, tc.mutate))
			var cfgErr *apperr.ConfigError
			if !errors.As(err, &cfgErr) || exitcode.FromError(err) != exitcode.ConfigOrDependency || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("err = %v (exit %d), want a ConfigError (exit 2) containing %q", err, exitcode.FromError(err), tc.want)
			}
			noRunDirectory(t, repoDir)
			if got := invocations(t, invocationsLog); len(got) != 0 {
				t.Errorf("invocations = %v, want none", got)
			}
			if reqs := jira.Requests(); len(reqs) != 0 {
				t.Errorf("Jira received %+v, want nothing", reqs)
			}
			m := resultLine(t, stdout, stderr, err)
			if m["status"] != "error" || m["run_id"] != "" || m["failure_kind"] != "" {
				t.Errorf("line = %v", m)
			}
			assertNotContains(t, "stderr", stderr, "Validating configuration")
		})
	}
}

// A missing file is the same refusal.
func TestListenerTask_MissingFile(t *testing.T) {
	repoDir, cfg, invocationsLog, _, _ := newListenerRun(t)

	_, _, err := runListenerTask(t, repoDir, cfg, filepath.Join(t.TempDir(), "none.json"))
	if exitcode.FromError(err) != exitcode.ConfigOrDependency || err == nil || !strings.Contains(err.Error(), "cannot read the listener task file") {
		t.Fatalf("err = %v, want a config error", err)
	}
	noRunDirectory(t, repoDir)
	if got := invocations(t, invocationsLog); len(got) != 0 {
		t.Errorf("invocations = %v, want none", got)
	}
}

// The deleted flag is refused, as a usage error naming the spec that deleted it, wherever it stands;
// nothing is created and nothing is called.
func TestListenerTask_FlagIsRefused(t *testing.T) {
	tests := []struct {
		name string
		args func(file string) []string
	}{
		{"run", func(f string) []string { return []string{"run", "--listener-task", f} }},
		{"run --output json", func(f string) []string { return []string{"--output", "json", "run", "--listener-task=" + f} }},
		{"another command", func(f string) []string { return []string{"validate", "--listener-task", f} }},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			repoDir, cfg, invocationsLog, _, jira := newListenerRun(t)

			stdout, _, err := runOrchestrator(t, repoDir, cfg, tc.args(writeListenerTask(t))...)
			if exitcode.FromError(err) != exitcode.ConfigOrDependency || err == nil || !strings.Contains(err.Error(), "specs/embed") {
				t.Fatalf("err = %v, want a config error naming specs/embed", err)
			}
			if stdout != "" {
				t.Errorf("stdout = %q, want nothing", stdout)
			}
			noRunDirectory(t, repoDir)
			if got := invocations(t, invocationsLog); len(got) != 0 {
				t.Errorf("invocations = %v, want none", got)
			}
			if reqs := jira.Requests(); len(reqs) != 0 {
				t.Errorf("Jira received %+v, want nothing", reqs)
			}
		})
	}
}

// failure_kind is "auth" when the CLI reports it did not use the run's credential (a FailureAuth,
// exit 2), however far the run got.
func TestListenerTask_FailureKindAuth(t *testing.T) {
	clearAuthEnv(t)
	t.Setenv("ANTHROPIC_API_KEY", authKey())
	t.Setenv("FAKE_API_KEY_SOURCE", "none")
	repoDir, cfg, invocationsLog, _, jira := newListenerRun(t)

	stdout, stderr, err := runListenerTask(t, repoDir, cfg, writeListenerTask(t))
	if exitcode.FromError(err) != exitcode.ConfigOrDependency {
		t.Fatalf("exit %d (%v), want 2\nstdout:\n%s\nstderr:\n%s", exitcode.FromError(err), err, stdout, stderr)
	}
	var derr *apperr.DecisionError
	var perr *apperr.ProviderError
	if !(errors.As(err, &derr) && derr.Kind == apperr.DecisionAuth) && !(errors.As(err, &perr) && perr.Kind == apperr.FailureAuth) {
		t.Fatalf("err = %T %v, want a credential failure", err, err)
	}
	m := resultLine(t, stdout, stderr, err)
	if m["exit_code"] != float64(2) || m["failure_kind"] != "auth" {
		t.Errorf("line = %v, want exit 2 and failure_kind auth", m)
	}
	if got := invocations(t, invocationsLog); len(got) == 0 || got[len(got)-1] == "implement" {
		t.Errorf("invocations = %v, want the run to stop at its first call", got)
	}
	if reqs := jira.Requests(); len(reqs) != 0 {
		t.Errorf("Jira received %+v, want nothing", reqs)
	}
}

// failure_kind is "" for a run that failed on its tests: exit 4, nothing to do with a credential.
func TestListenerTask_FailureKindIsEmptyForARedTest(t *testing.T) {
	repoDir := fakeexec.InitGitFixture(t)
	r, _, _ := newWorktreeRun(t, repoDir, func(o *configOptions) { o.testCommand = "exit 1" })

	stdout, stderr, err := runListenerTask(t, repoDir, r.cfg, writeListenerTask(t))
	if exitcode.FromError(err) != exitcode.WorkflowOrLimit {
		t.Fatalf("exit %d (%v), want 4\nstdout:\n%s\nstderr:\n%s", exitcode.FromError(err), err, stdout, stderr)
	}
	run := loadListenerRun(t, repoDir)
	m := resultLine(t, stdout, stderr, err)
	if run.Status != "stopped" || m["status"] != "stopped" || m["exit_code"] != float64(4) || m["failure_kind"] != "" ||
		m["failure_reason"] != "maximum fix iterations exceeded" {
		t.Errorf("status = %s, line = %v; want a run stopped on its red tests with no failure kind", run.Status, m)
	}
	if src := run.Task.Source; src == nil || !src.Listener || src.Key != listenerKey {
		t.Errorf("task.source = %+v", src)
	}
}

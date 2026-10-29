package app

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"orchestrator/internal/apperr"
	"orchestrator/internal/domain"
	"orchestrator/internal/exitcode"
)

// validListenerTask is a task file that parseListenerTask accepts, as a map so that a test can
// change or drop one field.
func validListenerTask() map[string]any {
	m := map[string]any{}
	m["v"], m["key"], m["url"] = 1, "PAY-12", "https://jira.example.com/browse/PAY-12"
	m["fetched_at"] = "2026-10-29T10:02:00Z"
	m["description"] = "PAY-12: refund over the payment is a 422\n\nthe body of the ticket"
	m["scrub_env"] = []string{"GITLAB_TOKEN", "_X1"}
	return m
}

func listenerTaskJSON(t *testing.T, mutate func(m map[string]any)) []byte {
	t.Helper()
	m := validListenerTask()
	if mutate != nil {
		mutate(m)
	}
	data, err := json.Marshal(m)
	if err != nil {
		t.Fatal(err)
	}
	return data
}

func TestParseListenerTask(t *testing.T) {
	in, err := parseListenerTask(listenerTaskJSON(t, nil))
	if err != nil {
		t.Fatalf("a valid task file is refused: %v", err)
	}
	if in.Key != "PAY-12" || in.URL != "https://jira.example.com/browse/PAY-12" || in.FetchedAt.IsZero() ||
		!strings.HasPrefix(in.Description, "PAY-12: refund") || len(in.ScrubEnv) != 2 {
		t.Errorf("parsed = %+v", in)
	}
	if _, err := parseListenerTask(listenerTaskJSON(t, func(m map[string]any) { delete(m, "scrub_env") })); err != nil {
		t.Errorf("a task file without scrub_env is refused: %v", err)
	}

	names := make([]string, maxListenerScrubEnv+1)
	for i := range names {
		names[i] = fmt.Sprintf("VAR_%d", i)
	}
	tests := []struct {
		name   string
		mutate func(m map[string]any)
		want   string
	}{
		{"unknown field", func(m map[string]any) { m["extra"] = 1 }, `"extra" is not a field`},
		{"no version", func(m map[string]any) { delete(m, "v") }, "v is 0"},
		{"another version", func(m map[string]any) { m["v"] = 2 }, "v is 2"},
		{"bad key", func(m map[string]any) { m["key"] = "pay-12" }, "not an issue key"},
		{"no key", func(m map[string]any) { delete(m, "key") }, "not an issue key"},
		{"http url", func(m map[string]any) { m["url"] = "http://jira.example.com/browse/PAY-12" }, "not an absolute https URL"},
		{"relative url", func(m map[string]any) { m["url"] = "/browse/PAY-12" }, "not an absolute https URL"},
		{"url with userinfo", func(m map[string]any) { m["url"] = "https://u:p@jira.example.com/browse/PAY-12" }, "not an absolute https URL"},
		{"no url", func(m map[string]any) { delete(m, "url") }, "not an absolute https URL"},
		{"no fetched_at", func(m map[string]any) { delete(m, "fetched_at") }, "fetched_at is not set"},
		{"zero fetched_at", func(m map[string]any) { m["fetched_at"] = "0001-01-01T00:00:00Z" }, "fetched_at is not set"},
		{"fetched_at not a time", func(m map[string]any) { m["fetched_at"] = "yesterday" }, "not valid JSON"},
		{"empty description", func(m map[string]any) { m["description"] = " \n" }, "description is empty"},
		{"description over 1 MiB", func(m map[string]any) { m["description"] = strings.Repeat("x", maxListenerDescriptionBytes+1) }, "over the 1048576-byte limit"},
		{"lower-case scrub name", func(m map[string]any) { m["scrub_env"] = []string{"gitlab_token"} }, "scrub_env[0]"},
		{"scrub name with a dash", func(m map[string]any) { m["scrub_env"] = []string{"A", "A-B"} }, "scrub_env[1]"},
		{"scrub name starting with a digit", func(m map[string]any) { m["scrub_env"] = []string{"1A"} }, "scrub_env[0]"},
		{"empty scrub name", func(m map[string]any) { m["scrub_env"] = []string{""} }, "scrub_env[0]"},
		{"too many scrub names", func(m map[string]any) { m["scrub_env"] = names }, "17 names, more than 16"},
		{"key not a string", func(m map[string]any) { m["key"] = 12 }, "key has the wrong type (a JSON number)"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			_, err := parseListenerTask(listenerTaskJSON(t, tc.mutate))
			if err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Errorf("parseListenerTask() = %v, want an error containing %q", err, tc.want)
			}
		})
	}

	for _, raw := range []string{"", "[]", `"x"`, `{"v":1} {"v":1}`, `{"v":1,`} {
		if _, err := parseListenerTask([]byte(raw)); err == nil {
			t.Errorf("parseListenerTask(%q) accepted it", raw)
		}
	}
	if _, err := parseListenerTask(append(listenerTaskJSON(t, nil), []byte(" {}")...)); err == nil {
		t.Error("data after the JSON object was accepted")
	}
}

func TestReadListenerTask(t *testing.T) {
	dir := t.TempDir()
	write := func(name string, data []byte) string {
		path := filepath.Join(dir, name)
		if err := os.WriteFile(path, data, 0o644); err != nil {
			t.Fatal(err)
		}
		return path
	}

	in, err := readListenerTask(write("ok.json", listenerTaskJSON(t, nil)))
	if err != nil || in.Key != "PAY-12" {
		t.Fatalf("readListenerTask = %+v, %v", in, err)
	}

	// Over the cap: refused without being parsed, and the message names the cap.
	big := write("big.json", bytes.Repeat([]byte(" "), maxListenerTaskFileBytes+1))
	bad := write("bad.json", listenerTaskJSON(t, func(m map[string]any) { m["key"] = "x" }))
	for _, tc := range []struct{ path, want string }{
		{big, fmt.Sprintf("is over %d bytes", maxListenerTaskFileBytes)},
		{filepath.Join(dir, "no.json"), "cannot read the listener task file"},
		{bad, "not an issue key"},
	} {
		_, err := readListenerTask(tc.path)
		var cfgErr *apperr.ConfigError
		if !errors.As(err, &cfgErr) || !strings.Contains(err.Error(), tc.want) || exitcode.FromError(err) != exitcode.ConfigOrDependency {
			t.Errorf("readListenerTask(%s) = %v, want a ConfigError containing %q", filepath.Base(tc.path), err, tc.want)
		}
	}
}

func TestListenerTaskSource(t *testing.T) {
	fetched := time.Date(2026, 10, 29, 10, 2, 0, 0, time.UTC)
	src := listenerTaskSource(listenerTask{Key: "PAY-12", URL: "https://jira.example.com/browse/PAY-12", FetchedAt: fetched})
	want := domain.TaskSource{Kind: domain.TaskSourceJira, Key: "PAY-12", URL: "https://jira.example.com/browse/PAY-12", FetchedAt: fetched, Listener: true}
	if *src != want {
		t.Errorf("source = %+v, want %+v", *src, want)
	}
	data, err := json.Marshal(src)
	if err != nil || !strings.Contains(string(data), `"listener":true`) {
		t.Errorf("source JSON = %s (%v), want listener: true", data, err)
	}
	// Only the listener's source says so.
	data, _ = json.Marshal(domain.TaskSource{Kind: domain.TaskSourceJira, Key: "PAY-12"})
	if strings.Contains(string(data), "listener") {
		t.Errorf("a Jira task from run --jira has %s", data)
	}
}

func TestParseArgs_ListenerTask(t *testing.T) {
	got, err := parseArgs([]string{"run", "--listener-task", "t.json", "--mode=auto"})
	if err != nil || got.listenerTaskFile != "t.json" || got.mode != domain.ModeAuto || len(got.rest) != 1 || got.rest[0] != "run" {
		t.Errorf("parseArgs = %+v, %v", got, err)
	}
	if got, err = parseArgs([]string{"--listener-task=t.json", "run"}); err != nil || got.listenerTaskFile != "t.json" {
		t.Errorf("parseArgs(=) = %+v, %v", got, err)
	}
	for _, tc := range []struct{ args, want string }{
		{"run --listener-task a --listener-task=b", "--listener-task given twice"},
		{"run --listener-task=", "--listener-task requires a file"},
		{"run --listener-task", "--listener-task requires a value"},
	} {
		if _, err := parseArgs(strings.Fields(tc.args)); err == nil || !strings.Contains(err.Error(), tc.want) {
			t.Errorf("parseArgs(%q) = %v, want %q", tc.args, err, tc.want)
		}
	}
}

// The hidden flag is refused before the config is read, a file opened or a run created, wherever it
// does not belong.
func TestRun_ListenerTaskRefusals(t *testing.T) {
	missing := filepath.Join(t.TempDir(), "none.yaml")
	tests := []struct {
		name string
		args []string
		want string
	}{
		{"outside run", []string{"validate", "--listener-task", "t.json"}, "--listener-task is only valid with run"},
		{"review", []string{"review", "--base", "main", "--listener-task", "t.json"}, "--listener-task is only valid with run"},
		{"--stage", []string{"run", "--listener-task", "t.json", "--stage", "review"}, "cannot be combined with --stage"},
		{"--jira", []string{"run", "--listener-task", "t.json", "--jira", "PAY-1"}, "cannot be combined with --jira"},
		{"--story-task", []string{"run", "--listener-task", "t.json", "--story-task", "s.json"}, "cannot be combined with --story-task"},
		{"--inplace", []string{"run", "--listener-task", "t.json", "--inplace"}, "cannot be combined with --inplace"},
		{"task text", []string{"run", "--listener-task", "t.json", "fix it"}, "cannot be combined with task text"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			var stdout, stderr bytes.Buffer
			args := append([]string{"--config", missing, "--mode", "auto"}, tc.args...)
			err := Run(context.Background(), args, pipeStdin(t), &stdout, &stderr)
			var cfgErr *apperr.ConfigError
			if !errors.As(err, &cfgErr) || !strings.Contains(err.Error(), tc.want) || exitcode.FromError(err) != exitcode.ConfigOrDependency {
				t.Errorf("Run = %v, want a ConfigError containing %q", err, tc.want)
			}
			if stdout.Len() != 0 || stderr.Len() != 0 {
				t.Errorf("output on stdout %q, stderr %q", stdout.String(), stderr.String())
			}
		})
	}
}

// An invalid task file is a config error before the config is read: no validation chain, no
// provider, no run. With --output json the line says so and has no failure kind.
func TestRun_ListenerTaskInvalidFile(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "PAY-12.1.json")
	if err := os.WriteFile(path, listenerTaskJSON(t, func(m map[string]any) { m["url"] = "http://jira.example.com/browse/PAY-12" }), 0o644); err != nil {
		t.Fatal(err)
	}
	missing := filepath.Join(dir, "none.yaml")

	var stdout, stderr bytes.Buffer
	err := Run(context.Background(), []string{"--config", missing, "--mode", "auto", "--output", "json", "run", "--listener-task", path}, pipeStdin(t), &stdout, &stderr)
	if exitcode.FromError(err) != exitcode.ConfigOrDependency || err == nil || !strings.Contains(err.Error(), "not an absolute https URL") {
		t.Fatalf("Run = %v, want a config error about the url (the config does not exist: it was not read)", err)
	}
	m := resultLine(t, stdout.String())
	if m["status"] != "error" || m["exit_code"] != float64(2) || m["failure_kind"] != "" || m["run_id"] != "" {
		t.Errorf("line = %v", m)
	}
	if stderr.Len() != 0 {
		t.Errorf("stderr = %q, want nothing: the validation chain did not start", stderr.String())
	}
}

// failure_kind is "auth" for a credential, however it is wrapped, and "" for everything else.
func TestFailureKind(t *testing.T) {
	providerAuth := &apperr.ProviderError{Provider: "claude", Kind: apperr.FailureAuth}
	tests := []struct {
		name string
		err  error
		want string
	}{
		{"no error", nil, ""},
		{"provider auth", providerAuth, "auth"},
		{"provider auth wrapped", fmt.Errorf("step: %w", providerAuth), "auth"},
		{"rejected decision key", &apperr.DecisionError{Provider: "jev", Kind: apperr.DecisionAuth, StatusCode: 401}, "auth"},
		{"decision key wrapped", fmt.Errorf("decide: %w", &apperr.DecisionError{Provider: "jev", Kind: apperr.DecisionAuth}), "auth"},
		{"provider exit", &apperr.ProviderError{Provider: "claude", Kind: apperr.FailureExit}, ""},
		{"decision timeout", &apperr.DecisionError{Provider: "jev", Kind: apperr.DecisionTimeout}, ""},
		{"red test", &apperr.WorkflowError{Msg: "tests failed after 3 fix iterations"}, ""},
		{"config", &apperr.ConfigError{Msg: "no"}, ""},
		{"cancelled", context.Canceled, ""},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if got := failureKind(tc.err); got != tc.want {
				t.Errorf("failureKind() = %q, want %q", got, tc.want)
			}
			var stdout bytes.Buffer
			writeResult(&stdout, &resultRecorder{}, tc.err)
			if m := resultLine(t, stdout.String()); m["failure_kind"] != tc.want {
				t.Errorf("line failure_kind = %v, want %q", m["failure_kind"], tc.want)
			}
		})
	}
}

// With a run, the line carries the kind as well as the status and exit code.
func TestBuildResult_FailureKindOfAFailedRun(t *testing.T) {
	store, run := resultsRun(t)
	run.Status, run.FailureReason = domain.RunFailed, "claude auth (api): rejected"
	err := &apperr.ProviderError{Provider: "claude", Kind: apperr.FailureAuth, Detail: run.FailureReason}

	var stdout bytes.Buffer
	writeResult(&stdout, &resultRecorder{run: run, store: store}, err)
	m := resultLine(t, stdout.String())
	if m["status"] != "failed" || m["exit_code"] != float64(2) || m["failure_kind"] != "auth" {
		t.Errorf("line = %v", m)
	}

	stdout.Reset()
	run.FailureReason = "tests failed"
	writeResult(&stdout, &resultRecorder{run: run, store: store}, &apperr.WorkflowError{Msg: run.FailureReason})
	if m := resultLine(t, stdout.String()); m["exit_code"] != float64(4) || m["failure_kind"] != "" {
		t.Errorf("line = %v, want exit 4 and no failure kind", m)
	}
}

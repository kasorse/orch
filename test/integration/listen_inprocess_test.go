package integration

import (
	"context"
	"encoding/json"
	"io/fs"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/kasorse/orch/internal/app"
)

// What running the tickets in the listener's own process asks of the run (specs/embed/SPEC.md §2.2,
// §2.4, §2.6): nothing of one run reaches the next, no descriptor or goroutine stays behind, the
// Jira variables are in no process the run starts, and a panic in a run is the ticket's failure,
// never the listener's.

// ticket is the state entry of key, when the listener has written one.
func (e *listenEnv) ticket(key string) (listenerStateItem, bool) {
	data, err := os.ReadFile(filepath.Join(e.wd, ".orchestrator", "listener", "state.json"))
	if err != nil {
		return listenerStateItem{}, false
	}
	var st listenerState
	if json.Unmarshal(data, &st) != nil {
		return listenerStateItem{}, false
	}
	item, ok := st.Tickets[key]
	return item, ok
}

// finished reports whether the listener closed the cycle of key.
func (e *listenEnv) finished(key string) bool {
	item, ok := e.ticket(key)
	return ok && item.Phase == "finished"
}

// openFDs is the number of file descriptors the process holds.
func openFDs(t *testing.T) int {
	t.Helper()
	entries, err := os.ReadDir("/proc/self/fd")
	if err != nil {
		t.Skipf("no /proc/self/fd to count descriptors with: %v", err)
	}
	return len(entries)
}

// settlesTo waits up to five seconds for count to fall to limit, and returns the last count: what a
// connection or a goroutine that is closing is given time to finish.
func settlesTo(count func() int, limit int) int {
	deadline := time.Now().Add(5 * time.Second)
	for {
		n := count()
		if n <= limit || time.Now().After(deadline) {
			return n
		}
		time.Sleep(50 * time.Millisecond)
	}
}

// Two tickets run by one listener in one process: the second run's artifacts carry nothing of the
// first (its run id, its ticket), and when the listener is stopped the process holds the descriptors
// and goroutines it held before the first run, within a tolerance (SPEC §2.6).
func TestListen_TwoTicketsInOneProcessLeaveNothingToEachOther(t *testing.T) {
	env := newListenEnv(t, listenOptions{pPre: 0.85})
	env.jira.add("PAY-12", "Add() subtracts", listenTask, "service:billing")
	env.jira.add("PAY-13", "Add() still subtracts", listenTask, "service:billing")
	fdsBefore, goroutinesBefore := openFDs(t), runtime.NumGoroutine()

	ctx, stop := context.WithCancel(context.Background())
	defer stop()
	var stderr syncBuffer
	wait := env.startListener(ctx, &stderr)
	waitFor(t, "both tickets to finish", func() bool { return env.finished("PAY-12") && env.finished("PAY-13") })
	stop()
	if err := wait(); err != nil {
		t.Fatalf("the stopped listener returned %v\nstderr:\n%s", err, stderr.String())
	}

	runs := env.runs()
	if len(runs) != 2 {
		t.Fatalf("runs = %+v, want two\nstderr:\n%s", runs, stderr.String())
	}
	for i, run := range runs {
		if run.Status != "completed" || run.MR == nil || run.MR.State != "created" {
			t.Errorf("run %d = %+v, want completed with its merge request", i+1, run)
		}
	}
	first, second := runs[0], runs[1]
	if first.ID == second.ID {
		t.Fatalf("both runs have the id %s", first.ID)
	}
	if !strings.Contains(first.Task.Description, "PAY-12") || !strings.Contains(second.Task.Description, "PAY-13") ||
		strings.Contains(second.Task.Description, "PAY-12") {
		t.Errorf("tasks = %q and %q, want each its own ticket's", first.Task.Description, second.Task.Description)
	}
	raw, err := os.ReadFile(filepath.Join(env.svc, ".orchestrator", "runs", second.ID, "run.json"))
	if err != nil {
		t.Fatal(err)
	}
	var secondRun listenerRunJSON
	if err := json.Unmarshal(raw, &secondRun); err != nil {
		t.Fatal(err)
	}
	if src := secondRun.Task.Source; src == nil || src.Key != "PAY-13" || !src.Listener {
		t.Errorf("the second run's task.source = %+v, want PAY-13's", src)
	}
	// Nothing in the second run's directory names the first run or its ticket.
	err = filepath.WalkDir(filepath.Join(env.svc, ".orchestrator", "runs", second.ID), func(path string, d fs.DirEntry, walkErr error) error {
		if walkErr != nil || d.IsDir() {
			return walkErr
		}
		data, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		for _, stale := range []string{first.ID, "PAY-12"} {
			if strings.Contains(string(data), stale) {
				t.Errorf("%s of the second run holds %q, which is the first run's", path, stale)
			}
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}

	// The connections the listener and the fake Jira kept alive are not the runs' to close.
	env.jira.srv.CloseClientConnections()
	const fdTolerance, goroutineTolerance = 6, 10
	if n := settlesTo(func() int { return openFDs(t) }, fdsBefore+fdTolerance); n > fdsBefore+fdTolerance {
		t.Errorf("open descriptors: %d before the first run, %d after the second (tolerance %d)", fdsBefore, n, fdTolerance)
	}
	if n := settlesTo(runtime.NumGoroutine, goroutinesBefore+goroutineTolerance); n > goroutinesBefore+goroutineTolerance {
		t.Errorf("goroutines: %d before the first run, %d after the second (tolerance %d)", goroutinesBefore, n, goroutineTolerance)
	}
}

// During a run the Jira variables are in no environment a git process or the test command is given,
// and the parent-session variables neither; the GitLab token reaches git and reaches neither the
// provider nor the test command (SPEC §2.4).
func TestListen_TheJiraVariablesNeverReachAProcessOfTheRun(t *testing.T) {
	testEnvPath := filepath.Join(t.TempDir(), "test.env")
	envDir := t.TempDir()
	t.Setenv("FAKE_ENV_DIR", envDir)
	t.Setenv("CLAUDECODE", "1")
	env := newListenEnv(t, listenOptions{pPre: 0.85, childOpts: []func(*configOptions){
		func(o *configOptions) { o.testCommand = "env > '" + testEnvPath + "' && go test ./..." },
	}})
	recordPushEnv(t, env.bare)
	env.jira.add("PAY-12", "Add() subtracts", listenTask, "service:billing")

	stderr := mustListen(t, env)

	runs := env.runs()
	if len(runs) != 1 || runs[0].Status != "completed" || runs[0].MR == nil || runs[0].MR.State != "created" {
		t.Fatalf("runs = %+v, want one completed run with its merge request\nstderr:\n%s", runs, stderr)
	}
	gitEnv := readEnvDump(t, filepath.Join(env.bare, "push-env.log"))
	testEnv := readEnvDump(t, testEnvPath)
	providerEnv := readEnvDump(t, filepath.Join(envDir, "implement.env"))

	for who, vars := range map[string]map[string]string{"the git push": gitEnv, "the test command": testEnv, "the provider": providerEnv} {
		for _, name := range []string{"JIRA_EMAIL", "JIRA_API_TOKEN", "CLAUDECODE"} {
			if _, ok := vars[name]; ok {
				t.Errorf("%s got %s", who, name)
			}
		}
		for name, value := range vars {
			if strings.Contains(value, jiraToken) || strings.Contains(value, jiraEmail) {
				t.Errorf("%s got a Jira credential in %s", who, name)
			}
		}
	}
	if gitEnv["PATH"] == "" || gitEnv["GITLAB_TOKEN"] != "glpat-integration-token" {
		t.Errorf("the git push did not get the listener's environment with the GitLab token: GITLAB_TOKEN = %q", gitEnv["GITLAB_TOKEN"])
	}
	for who, vars := range map[string]map[string]string{"the test command": testEnv, "the provider": providerEnv} {
		if _, ok := vars["GITLAB_TOKEN"]; ok {
			t.Errorf("%s got GITLAB_TOKEN", who)
		}
	}
	if providerEnv["PATH"] == "" {
		t.Error("the provider's environment dump is empty: it proves nothing")
	}
}

// A panic in a run is contained: the ticket ends failed with "internal error: " and the first line of
// what was panicked with, the stack goes to the log, the state is saved, and the listener polls
// again and runs the next ticket (SPEC §2.2).
func TestListen_APanicInARunFailsTheTicketAndTheListenerGoesOn(t *testing.T) {
	env := newListenEnv(t, listenOptions{pPre: 0.85})
	env.jira.add("PAY-12", "Add() subtracts", listenTask, "service:billing")
	env.jira.add("PAY-13", "Add() still subtracts", listenTask, "service:billing")
	defer app.SetListenRunHook(func(key string) {
		if key == "PAY-12" {
			panic("injected failure\nsecond line, which is no one's business")
		}
	})()

	ctx, stop := context.WithCancel(context.Background())
	defer stop()
	var stderr syncBuffer
	wait := env.startListener(ctx, &stderr)
	waitFor(t, "both tickets to finish", func() bool { return env.finished("PAY-12") && env.finished("PAY-13") })
	stop()
	if err := wait(); err != nil {
		t.Fatalf("the listener returned %v after a panic in a run, want nil\nstderr:\n%s", err, stderr.String())
	}

	if got := env.jira.statusOf("PAY-12"); got != "Failed" {
		t.Errorf("PAY-12 is in %q, want Failed", got)
	}
	comments := env.jira.botComments("PAY-12")
	if len(comments) != 1 || !strings.Contains(comments[0], "internal error: injected failure") || strings.Contains(comments[0], "second line") {
		t.Errorf("comments = %q, want one that says internal error and the first line only", comments)
	}
	if e := env.state().Tickets["PAY-12"]; e.Outcome != "failed" || e.Attempts != 1 || e.Phase != "finished" || e.Writes.Comment.State != "done" {
		t.Errorf("state = %+v, want failed after one attempt, its writes done", e)
	}
	log := stderr.String()
	if !strings.Contains(log, "the run panicked: injected failure") || !strings.Contains(log, "goroutine ") || strings.Contains(log, "second line") {
		t.Errorf("the log lacks the panic's first line and the goroutine's stack, or has its second line:\n%s", log)
	}

	// The listener polled again: the next ticket was claimed, run and delivered.
	if got := env.jira.statusOf("PAY-13"); got != "Ready to Review" {
		t.Errorf("PAY-13 is in %q, want Ready to Review\nstderr:\n%s", got, log)
	}
	if runs := env.runs(); len(runs) != 1 || runs[0].Status != "completed" || !strings.Contains(runs[0].Task.Description, "PAY-13") {
		t.Errorf("runs = %+v, want PAY-13's run alone: the panicked ticket never got to create one", runs)
	}
}

package integration

import (
	"context"
	"errors"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"orchestrator/internal/app"
	"orchestrator/internal/apperr"
	"orchestrator/internal/exitcode"
)

// orchestrator jira listen (specs/jira-listener/SPEC.md, TASK-JL05): the supervisor polls a fake
// Jira, claims the tickets it may, runs each as a child run in its service, and writes the outcome
// back. The children are this test binary run as the orchestrator; the "GitLab" is a bare repository.

const listenTask = "fix Add in fixture.go so that it adds instead of subtracting"

// invocationsOrNone is the fake claude's invocation log, nothing when no call was made.
func invocationsOrNone(path string) []string {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil
	}
	return strings.Fields(string(data))
}

// waitFor polls cond for up to 30 seconds.
func waitFor(t *testing.T, what string, cond func() bool) {
	t.Helper()
	deadline := time.Now().Add(30 * time.Second)
	for !cond() {
		if time.Now().After(deadline) {
			t.Fatalf("timed out waiting for %s", what)
		}
		time.Sleep(50 * time.Millisecond)
	}
}

func mustListen(t *testing.T, env *listenEnv) string {
	t.Helper()
	stderr, err := env.listenOnce()
	if err != nil {
		t.Fatalf("the listener returned %v\nstderr:\n%s", err, stderr)
	}
	return stderr
}

func transitionIDs(env *listenEnv) []string {
	var ids []string
	for _, p := range env.jira.posts("/transitions") {
		ids = append(ids, strings.TrimSuffix(strings.TrimPrefix(strings.Join(strings.Fields(p.Body), ""), `{"transition":{"id":"`), `"}}`))
	}
	return ids
}

// A clear ticket is claimed, run, gets a merge request, a comment with the link and the done
// transition; the state shows it finished. The tickets the loose search returns that the query
// would not are left alone: the listener re-checks in Go.
func TestListen_ClearTicketBecomesAMergeRequest(t *testing.T) {
	env := newListenEnv(t, listenOptions{pPre: 0.85})
	env.jira.add("PAY-12", "Add() subtracts", listenTask, "service:billing")
	env.jira.add("PAY-13", "somebody else's", listenTask, "service:billing").reporter = "stranger"
	env.jira.add("PAY-14", "already done", listenTask, "service:billing").status = "Done"

	stderr := mustListen(t, env)

	runs := env.runs()
	if len(runs) != 1 || runs[0].Status != "completed" || runs[0].MR == nil || runs[0].MR.State != "created" {
		t.Fatalf("runs = %+v, want one completed run with its merge request\nstderr:\n%s", runs, stderr)
	}
	run := runs[0]

	if got := env.jira.statusOf("PAY-12"); got != "Ready to Review" {
		t.Errorf("PAY-12 is in %q, want Ready to Review", got)
	}
	if env.jira.statusOf("PAY-13") != "To Do" || env.jira.statusOf("PAY-14") != "Done" {
		t.Error("a ticket the Go re-check refuses was moved")
	}
	for _, r := range env.jira.reqs {
		if strings.Contains(r.Path, "PAY-13") || strings.Contains(r.Path, "PAY-14") {
			t.Errorf("a request touched a refused ticket: %s %s", r.Method, r.Path)
		}
	}
	if got := transitionIDs(env); !equalStrings(got, []string{"11", "23"}) {
		t.Errorf("transitions = %v, want the claim (11) and done (23)", got)
	}

	comments := env.jira.botComments("PAY-12")
	if len(comments) != 1 {
		t.Fatalf("comments = %q, want one", comments)
	}
	lines := strings.Split(comments[0], "\n")
	if lines[0] != "[orchestrator]" || lines[len(lines)-1] != "Run "+run.ID || !strings.Contains(comments[0], run.MR.URL) {
		t.Errorf("comment = %q, want [orchestrator] first, the merge request link and Run %s last", comments[0], run.ID)
	}
	if len(comments[0]) > 8<<10 {
		t.Errorf("comment is %d bytes", len(comments[0]))
	}

	e := env.state().Tickets["PAY-12"]
	if e.Phase != "finished" || e.Outcome != "done" || e.Attempts != 1 || e.Service != "billing" || e.RunID != run.ID ||
		e.RunDir != "billing/.orchestrator/runs/"+run.ID || e.Writes.Comment.State != "done" || e.Writes.Comment.ID == "" || e.Writes.Transition.State != "done" {
		t.Errorf("state = %+v, want a finished, done ticket with one attempt and both writes recorded", e)
	}
	if len(env.state().Tickets) != 1 {
		t.Errorf("state = %+v, want only PAY-12", env.state().Tickets)
	}

	want := `project in ("PAY") AND labels = "ai-run" AND status in ("To Do") AND reporter in ("rep-1") ORDER BY priority DESC, created ASC`
	if jqls := env.jira.searches(); len(jqls) != 1 || jqls[0] != want {
		t.Errorf("JQL = %q, want %q", jqls, want)
	}
	if !strings.Contains(stderr, "listen: PAY-12 claimed") || !strings.Contains(stderr, "listen: PAY-12 → Ready to Review (!1)") {
		t.Errorf("stderr lacks the event lines:\n%s", stderr)
	}
}

// An unclear ticket gets the numbered open points and blocked; after an answer in a comment and
// the status back in the queue, the next run's task holds the discussion.
func TestListen_UnclearTicketIsBlockedWithItsQuestionsAndAnswered(t *testing.T) {
	env := newListenEnv(t, listenOptions{pPre: 0.42, rechecks: []float64{0.55, 0.9}})
	env.jira.add("PAY-12", "Format the date", listenTask, "service:billing")

	mustListen(t, env)

	if got := env.jira.statusOf("PAY-12"); got != "Blocked" {
		t.Fatalf("PAY-12 is in %q, want Blocked", got)
	}
	comments := env.jira.botComments("PAY-12")
	if len(comments) != 1 {
		t.Fatalf("comments = %q, want one", comments)
	}
	// The question is plain text in the wiki: its "?" is escaped.
	for _, want := range []string{"[orchestrator]\n", "\n# ", `Which locale is the date in\?`, "move the ticket back to To Do."} {
		if !strings.Contains(comments[0], want) {
			t.Errorf("the comment lacks %q:\n%s", want, comments[0])
		}
	}
	if e := env.state().Tickets["PAY-12"]; e.Outcome != "blocked" || e.Attempts != 1 || e.Phase != "finished" {
		t.Errorf("state = %+v, want blocked after one attempt", e)
	}

	// A person answers and moves the ticket back to the queue.
	env.jira.person("PAY-12", "The date is ISO 8601, in UTC.")
	env.jira.setStatus("PAY-12", "To Do")
	mustListen(t, env)

	runs := env.runs()
	if len(runs) != 2 {
		t.Fatalf("runs = %d, want two", len(runs))
	}
	task := runs[1].Task.Description
	for _, want := range []string{"Discussion since the orchestrator's questions:", "--- orchestrator, ", `Which locale is the date in\?`, "--- comment, ", "The date is ISO 8601, in UTC."} {
		if !strings.Contains(task, want) {
			t.Errorf("the second run's task lacks %q:\n%s", want, task)
		}
	}
	if strings.Contains(runs[0].Task.Description, "Discussion since") {
		t.Error("the first run's task has a discussion")
	}
	if got := env.jira.statusOf("PAY-12"); got != "Ready to Review" {
		t.Errorf("PAY-12 is in %q after its second run, want Ready to Review", got)
	}
	if e := env.state().Tickets["PAY-12"]; e.Attempts != 2 || e.Outcome != "done" {
		t.Errorf("state = %+v, want two attempts and done", e)
	}
}

// A red test and a push the remote refuses (exit 4 and exit 6) each give a comment with the reason
// and the failed transition.
func TestListen_FailedRunsAreCommentedAndFailed(t *testing.T) {
	tests := []struct {
		name  string
		setup func(t *testing.T) listenOptions
		after func(t *testing.T, env *listenEnv)
		want  []string
	}{
		{
			name: "a red test",
			setup: func(t *testing.T) listenOptions {
				red := func(o *configOptions) { o.testCommand = "exit 1" }
				return listenOptions{pPre: 0.85, childOpts: []func(*configOptions){red}}
			},
			want: []string{"The run did not deliver: ", "maximum fix iterations exceeded", "Last failure: "},
		},
		{
			name:  "a push the remote refuses",
			setup: func(t *testing.T) listenOptions { return listenOptions{pPre: 0.85} },
			after: func(t *testing.T, env *listenEnv) {
				hook := filepath.Join(env.bare, "hooks", "pre-receive")
				if err := os.WriteFile(hook, []byte("#!/bin/sh\necho 'rejected by the test' >&2\nexit 1\n"), 0o755); err != nil {
					t.Fatal(err)
				}
			},
			want: []string{"The run did not deliver: "},
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			env := newListenEnv(t, tc.setup(t))
			if tc.after != nil {
				tc.after(t, env)
			}
			env.jira.add("PAY-12", "Add() subtracts", listenTask, "service:billing")

			stderr := mustListen(t, env)

			if got := env.jira.statusOf("PAY-12"); got != "Failed" {
				t.Fatalf("PAY-12 is in %q, want Failed\nstderr:\n%s", got, stderr)
			}
			comments := env.jira.botComments("PAY-12")
			if len(comments) != 1 {
				t.Fatalf("comments = %q, want one", comments)
			}
			for _, want := range tc.want {
				if !strings.Contains(comments[0], want) {
					t.Errorf("the comment lacks %q:\n%s", want, comments[0])
				}
			}
			if !strings.HasPrefix(comments[0], "[orchestrator]\n") || !strings.Contains(comments[0], "\nRun ") {
				t.Errorf("comment = %q", comments[0])
			}
			if got := transitionIDs(env); !equalStrings(got, []string{"11", "22"}) {
				t.Errorf("transitions = %v, want the claim (11) and failed (22)", got)
			}
			if e := env.state().Tickets["PAY-12"]; e.Outcome != "failed" || e.Attempts != 1 {
				t.Errorf("state = %+v, want failed after one attempt", e)
			}
		})
	}
}

// A ticket someone moved during the run gets the comment and no transition.
func TestListen_ATicketMovedDuringTheRunIsCommentedAndNotMoved(t *testing.T) {
	env := newListenEnv(t, listenOptions{pPre: 0.85})
	env.jira.add("PAY-12", "Add() subtracts", listenTask, "service:billing")
	env.jira.afterComments = func(key string) { env.jira.setStatus(key, "Done") }

	mustListen(t, env)

	if got := env.jira.statusOf("PAY-12"); got != "Done" {
		t.Errorf("PAY-12 is in %q, want it left where the person put it", got)
	}
	if got := transitionIDs(env); !equalStrings(got, []string{"11"}) {
		t.Errorf("transitions = %v, want the claim only", got)
	}
	comments := env.jira.botComments("PAY-12")
	if len(comments) != 1 || !strings.Contains(comments[0], "/-/merge_requests/1") || !strings.Contains(comments[0], "was moved to Done") {
		t.Errorf("comments = %q, want the merge request and the notice that the ticket was moved", comments)
	}
	if e := env.state().Tickets["PAY-12"]; e.Phase != "finished" || e.Outcome != "done" || e.Writes.Comment.State != "done" || e.Writes.Transition.State != "" {
		t.Errorf("state = %+v", e)
	}
}

// A claim whose transition is no longer offered is skipped and nothing else is written.
func TestListen_AClaimThatIsNotOfferedIsSkipped(t *testing.T) {
	env := newListenEnv(t, listenOptions{pPre: 0.85})
	env.jira.add("PAY-12", "Add() subtracts", listenTask, "service:billing")
	env.jira.noStart = true

	stderr := mustListen(t, env)

	if posts := env.jira.posts("/transitions"); len(posts) != 0 {
		t.Errorf("transitions posted: %+v", posts)
	}
	if posts := env.jira.posts("/comment"); len(posts) != 0 {
		t.Errorf("comments posted: %+v", posts)
	}
	if _, err := os.Stat(filepath.Join(env.wd, ".orchestrator", "listener", "state.json")); err == nil {
		t.Errorf("state.json was written: %v", env.state().Tickets)
	}
	if got := invocationsOrNone(env.invocationsLog); len(got) != 0 || len(env.runs()) != 0 {
		t.Errorf("a run was made: %v", got)
	}
	if !strings.Contains(stderr, "PAY-12 skipped") {
		t.Errorf("stderr lacks the skip line:\n%s", stderr)
	}
}

// A provider failure sends the ticket back to the queue, counts no attempt, and the ticket is not
// picked again before next_try_at.
func TestListen_AProviderFailureRetriesLaterWithoutAnAttempt(t *testing.T) {
	t.Setenv("FAKE_FAIL_ACTION", "implement")
	t.Setenv("FAKE_FAIL_EXIT_CODE", "7")
	t.Setenv("FAKE_FAIL_STDERR", "boom: simulated provider crash")
	env := newListenEnv(t, listenOptions{pPre: 0.85})
	env.jira.add("PAY-12", "Add() subtracts", listenTask, "service:billing")

	stderr := mustListen(t, env)

	if got := env.jira.statusOf("PAY-12"); got != "To Do" {
		t.Fatalf("PAY-12 is in %q, want it back in the queue\nstderr:\n%s", got, stderr)
	}
	if got := transitionIDs(env); !equalStrings(got, []string{"11", "24"}) {
		t.Errorf("transitions = %v, want the claim (11) and retry (24)", got)
	}
	if comments := env.jira.botComments("PAY-12"); len(comments) != 0 {
		t.Errorf("comments = %q, want none", comments)
	}
	e := env.state().Tickets["PAY-12"]
	if e.Attempts != 0 || e.Outcome != "retry" || e.Phase != "finished" || e.NextTryAt == "" || e.Retries != 1 {
		t.Fatalf("state = %+v, want retry with no attempt and a next_try_at", e)
	}
	next, err := time.Parse(time.RFC3339, e.NextTryAt)
	if err != nil || !next.After(time.Now()) {
		t.Errorf("next_try_at = %q, want a time in the future", e.NextTryAt)
	}

	before := len(invocationsOrNone(env.invocationsLog))
	mustListen(t, env)
	if got := transitionIDs(env); len(got) != 2 {
		t.Errorf("transitions = %v: the ticket was picked before next_try_at", got)
	}
	if after := len(invocationsOrNone(env.invocationsLog)); after != before {
		t.Errorf("the fake claude was called again (%d calls before, %d after)", before, after)
	}
}

// failure_kind: auth sends the ticket to retry, counts no attempt, and the listener stops with exit 2.
func TestListen_AuthFailureOfAChildStopsTheListener(t *testing.T) {
	clearAuthEnv(t)
	t.Setenv("ANTHROPIC_API_KEY", authKey())
	t.Setenv("FAKE_API_KEY_SOURCE", "none")
	env := newListenEnv(t, listenOptions{pPre: 0.85})
	env.jira.add("PAY-12", "Add() subtracts", listenTask, "service:billing")

	stderr, err := env.listenOnce()

	if err == nil || exitcode.FromError(err) != exitcode.ConfigOrDependency {
		t.Fatalf("err = %v (exit %d), want exit 2\nstderr:\n%s", err, exitcode.FromError(err), stderr)
	}
	var cfgErr *apperr.ConfigError
	if !errors.As(err, &cfgErr) {
		t.Errorf("err = %T, want a ConfigError", err)
	}
	if got := env.jira.statusOf("PAY-12"); got != "To Do" {
		t.Errorf("PAY-12 is in %q, want it back in the queue", got)
	}
	if comments := env.jira.botComments("PAY-12"); len(comments) != 0 {
		t.Errorf("comments = %q, want none", comments)
	}
	if e := env.state().Tickets["PAY-12"]; e.Attempts != 0 || e.Outcome != "retry" || e.Phase != "finished" {
		t.Errorf("state = %+v, want retry with no attempt", e)
	}
}

// A ticket whose attempts are spent gets one comment and failed, and is never dispatched again.
func TestListen_ASpentTicketIsFailedOnceAndNeverDispatchedAgain(t *testing.T) {
	env := newListenEnv(t, listenOptions{pPre: 0.85, attempts: 2})
	env.jira.add("PAY-12", "Add() subtracts", listenTask, "service:billing")
	stateDir := filepath.Join(env.wd, ".orchestrator", "listener")
	if err := os.MkdirAll(stateDir, 0o755); err != nil {
		t.Fatal(err)
	}
	prior := `{"v":1,"tickets":{"PAY-12":{"attempts":2,"phase":"finished","outcome":"failed"}}}`
	if err := os.WriteFile(filepath.Join(stateDir, "state.json"), []byte(prior), 0o644); err != nil {
		t.Fatal(err)
	}

	mustListen(t, env)

	comments := env.jira.botComments("PAY-12")
	if len(comments) != 1 || !strings.Contains(comments[0], "2 attempts spent; remove PAY-12 from .orchestrator/listener/state.json to try again.") {
		t.Fatalf("comments = %q, want the spent notice", comments)
	}
	if got := env.jira.statusOf("PAY-12"); got != "Failed" {
		t.Errorf("PAY-12 is in %q, want Failed", got)
	}
	if e := env.state().Tickets["PAY-12"]; e.Outcome != "spent" || e.Attempts != 2 {
		t.Errorf("state = %+v", e)
	}
	if got := invocationsOrNone(env.invocationsLog); len(got) != 0 {
		t.Errorf("a run was made: %v", got)
	}

	// Someone puts it back in the queue: it is not taken again.
	env.jira.setStatus("PAY-12", "To Do")
	mustListen(t, env)
	if comments := env.jira.botComments("PAY-12"); len(comments) != 1 {
		t.Errorf("comments = %q, want still one", comments)
	}
	if got := transitionIDs(env); !equalStrings(got, []string{"22"}) {
		t.Errorf("transitions = %v, want failed once, from the queue and without a claim", got)
	}
}

// An Epic is planned as a story, not run: a comment and blocked from the queue, no claim, no run and
// no attempt.
func TestListen_AnEpicIsBlockedWithoutAClaim(t *testing.T) {
	env := newListenEnv(t, listenOptions{pPre: 0.85})
	env.jira.add("PAY-12", "A whole feature", listenTask, "service:billing").issueType = "Epic"

	mustListen(t, env)

	if got := env.jira.statusOf("PAY-12"); got != "Blocked" {
		t.Errorf("PAY-12 is in %q, want Blocked", got)
	}
	if got := transitionIDs(env); !equalStrings(got, []string{"21"}) {
		t.Errorf("transitions = %v, want blocked only", got)
	}
	if comments := env.jira.botComments("PAY-12"); len(comments) != 1 || !strings.Contains(comments[0], "plan it as a story") {
		t.Errorf("comments = %q, want the story notice", comments)
	}
	if got := invocationsOrNone(env.invocationsLog); len(got) != 0 || len(env.runs()) != 0 {
		t.Errorf("a run was made: %v", got)
	}
	if e := env.state().Tickets["PAY-12"]; e.Outcome != "blocked" || e.Attempts != 0 || e.Phase != "finished" {
		t.Errorf("state = %+v, want blocked with no attempt", e)
	}
}

// A claim another listener made first is refused by the workflow: the ticket is skipped and nothing
// else is written, though it is now in the status the claim leads to.
func TestListen_AClaimAnotherListenerMadeFirstIsSkipped(t *testing.T) {
	env := newListenEnv(t, listenOptions{pPre: 0.85})
	env.jira.add("PAY-12", "Add() subtracts", listenTask, "service:billing")
	env.jira.beforeTransition = func(key, id string) {
		if id == "11" {
			env.jira.setStatus(key, "In Progress")
		}
	}

	stderr := mustListen(t, env)

	if got := transitionIDs(env); !equalStrings(got, []string{"11"}) {
		t.Errorf("transitions = %v, want only the refused claim", got)
	}
	if posts := env.jira.posts("/comment"); len(posts) != 0 {
		t.Errorf("comments posted: %+v", posts)
	}
	if got := invocationsOrNone(env.invocationsLog); len(got) != 0 || len(env.runs()) != 0 {
		t.Errorf("a run was made: %v\nstderr:\n%s", got, stderr)
	}
	if e, ok := env.state().Tickets["PAY-12"]; ok {
		t.Errorf("state = %+v, want no entry for a ticket the listener did not claim", e)
	}
	if !strings.Contains(stderr, "PAY-12 skipped: the claim was refused") {
		t.Errorf("stderr lacks the skip line:\n%s", stderr)
	}
}

// Killed during a run and restarted, the listener resumes the run and writes its outcome once.
func TestListen_ARestartResumesTheInterruptedRunAndWritesItsOutcomeOnce(t *testing.T) {
	t.Setenv("FAKE_SLEEP_ACTION", "implement")
	t.Setenv("FAKE_SLEEP_SECONDS", "30")
	env := newListenEnv(t, listenOptions{pPre: 0.85})
	env.jira.add("PAY-12", "Add() subtracts", listenTask, "service:billing")

	ctx, stop := context.WithCancel(context.Background())
	defer stop()
	var stderr syncBuffer
	wait := env.startListener(ctx, &stderr, "--once")
	waitFor(t, "the child to reach implement", func() bool {
		for _, a := range invocationsOrNone(env.invocationsLog) {
			if a == "implement" {
				return true
			}
		}
		return false
	})
	stop()
	if err := wait(); err != nil {
		t.Fatalf("the stopped listener returned %v\nstderr:\n%s", err, stderr.String())
	}

	if got := env.jira.statusOf("PAY-12"); got != "In Progress" {
		t.Errorf("PAY-12 is in %q, want it left in In Progress", got)
	}
	if comments := env.jira.botComments("PAY-12"); len(comments) != 0 {
		t.Errorf("comments = %q: nothing is written for an interrupted run", comments)
	}
	if e := env.state().Tickets["PAY-12"]; e.Phase != "running" || e.Outcome != "" || e.Attempts != 0 {
		t.Fatalf("state = %+v, want a running ticket", e)
	}

	// The listener starts again; the run goes on and its outcome is written once.
	t.Setenv("FAKE_SLEEP_ACTION", "")
	stderrText := mustListen(t, env)

	runs := env.runs()
	if len(runs) != 1 || runs[0].Status != "completed" || runs[0].MR == nil || runs[0].MR.State != "created" {
		t.Fatalf("runs = %+v, want the one run, completed\nstderr:\n%s", runs, stderrText)
	}
	if got := env.jira.statusOf("PAY-12"); got != "Ready to Review" {
		t.Errorf("PAY-12 is in %q, want Ready to Review", got)
	}
	if comments := env.jira.botComments("PAY-12"); len(comments) != 1 || !strings.Contains(comments[0], runs[0].MR.URL) {
		t.Errorf("comments = %q, want one, with the merge request", comments)
	}
	if got := transitionIDs(env); !equalStrings(got, []string{"11", "23"}) {
		t.Errorf("transitions = %v, want the claim and done, once each", got)
	}
	if e := env.state().Tickets["PAY-12"]; e.Phase != "finished" || e.Outcome != "done" || e.Attempts != 1 || e.RunID != runs[0].ID {
		t.Errorf("state = %+v", e)
	}
}

// A second listener in the same directory refuses at once.
func TestListen_ASecondListenerInTheSameDirectoryRefuses(t *testing.T) {
	env := newListenEnv(t, listenOptions{pPre: 0.85})

	ctx, stop := context.WithCancel(context.Background())
	defer stop()
	var stderr syncBuffer
	wait := env.startListener(ctx, &stderr)
	waitFor(t, "the first listener to start", func() bool { return strings.Contains(stderr.String(), "listening on") })

	err := app.Run(context.Background(), []string{"--config", env.cfg, "jira", "listen", "--once"}, strings.NewReader(""), io.Discard, io.Discard)
	var cfgErr *apperr.ConfigError
	if !errors.As(err, &cfgErr) || exitcode.FromError(err) != exitcode.ConfigOrDependency || !strings.Contains(err.Error(), "another orchestrator jira listen is running") {
		t.Errorf("err = %v (exit %d), want a refusal naming the other listener", err, exitcode.FromError(err))
	}

	stop()
	if err := wait(); err != nil {
		t.Errorf("the first listener returned %v after it was told to stop, want nil", err)
	}
}

// jira listen refuses what says what a run is or how it runs, before it reads any configuration.
func TestListen_FlagRefusals(t *testing.T) {
	tests := []struct {
		name string
		args []string
		want string
	}{
		{"--mode", []string{"--mode", "auto", "jira", "listen"}, "cannot be combined with --mode"},
		{"--stage", []string{"--stage", "review", "jira", "listen"}, "--stage is only valid with run"},
		{"--jira", []string{"--jira", "PAY-1", "jira", "listen"}, "--jira is only valid"},
		{"--inplace", []string{"--inplace", "jira", "listen"}, "--inplace is only valid with run"},
		{"task text", []string{"jira", "listen", "fix it"}, "takes no task text"},
		{"an unknown subcommand", []string{"jira", "poll"}, "usage: orchestrator jira listen"},
		{"--output", []string{"--output", "json", "jira", "listen"}, "--output is only valid with run, resume and review"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			err := app.Run(context.Background(), append([]string{"--config", filepath.Join(t.TempDir(), "none.yaml")}, tc.args...), strings.NewReader(""), io.Discard, io.Discard)
			if err == nil || exitcode.FromError(err) != exitcode.ConfigOrDependency || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("err = %v (exit %d), want a config error containing %q", err, exitcode.FromError(err), tc.want)
			}
		})
	}
}

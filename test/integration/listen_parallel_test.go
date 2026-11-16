package integration

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/kasorse/orch/internal/app"
)

// The listener's slots (specs/listener-story/SPEC.md §3): up to listener.max_parallel runs at once,
// at most one per service. The scenarios use the listener's run hook as a barrier, to hold runs in
// progress while the test looks at the state, and the fake provider's rendezvous where a run must be
// cut off in the middle of a provider call.

// runBarrier holds every run the listener makes at its run hook, which is inside the run's own
// goroutine and before the run, until want runs are there at once and the test opens it.
type runBarrier struct {
	want int

	mu      sync.Mutex
	keys    []string
	reached chan struct{}

	release chan struct{}
	opened  sync.Once
}

func newRunBarrier(want int) *runBarrier {
	return &runBarrier{want: want, reached: make(chan struct{}), release: make(chan struct{})}
}

// hook is the function to give app.SetListenRunHook. A run that reaches it after the barrier was
// opened goes on at once.
func (b *runBarrier) hook(key string) {
	b.mu.Lock()
	b.keys = append(b.keys, key)
	if len(b.keys) == b.want {
		close(b.reached)
	}
	b.mu.Unlock()
	select {
	case <-b.release:
	case <-time.After(30 * time.Second):
	}
}

// open lets the held runs go on. A test defers it, so that a failed one does not leave the
// listener waiting for the hook.
func (b *runBarrier) open() { b.opened.Do(func() { close(b.release) }) }

// waitReached waits for want runs to be held, and returns their tickets' keys, sorted.
func (b *runBarrier) waitReached(t *testing.T) []string {
	t.Helper()
	select {
	case <-b.reached:
	case <-time.After(30 * time.Second):
		b.mu.Lock()
		defer b.mu.Unlock()
		t.Fatalf("timed out waiting for %d runs at the barrier, got %v", b.want, b.keys)
	}
	b.mu.Lock()
	defer b.mu.Unlock()
	keys := slices.Clone(b.keys)
	slices.Sort(keys)
	return keys
}

// unfinished are the keys of the state's entries whose cycle is not finished.
func (e *listenEnv) unfinished() []string {
	data, err := os.ReadFile(filepath.Join(e.wd, ".orchestrator", "listener", "state.json"))
	if err != nil {
		return nil
	}
	var st listenerState
	if json.Unmarshal(data, &st) != nil {
		return nil
	}
	var keys []string
	for k, it := range st.Tickets {
		if it.Phase != "finished" {
			keys = append(keys, k)
		}
	}
	slices.Sort(keys)
	return keys
}

// notClaimed reports whether the listener has not touched key: no state entry, still in the queue.
func (e *listenEnv) notClaimed(t *testing.T, key string) {
	t.Helper()
	if it, ok := e.ticket(key); ok {
		t.Errorf("%s has a state entry %+v while it must wait", key, it)
	}
	if got := e.jira.statusOf(key); got != "To Do" {
		t.Errorf("%s is in %q while it must wait in To Do", key, got)
	}
}

// delivered checks that the run of the service in dir was completed with its merge request.
func (e *listenEnv) delivered(t *testing.T, key, dir string) {
	t.Helper()
	runs := e.runsIn(dir)
	if len(runs) != 1 || runs[0].Status != "completed" || runs[0].MR == nil || runs[0].MR.State != "created" ||
		!strings.Contains(runs[0].Task.Description, key) {
		t.Errorf("runs of %s = %+v, want %s's one completed run with its merge request", dir, runs, key)
	}
	if got := e.jira.statusOf(key); got != "Ready to Review" {
		t.Errorf("%s is in %q, want Ready to Review", key, got)
	}
}

// Two tickets of two services run at once: both are in progress at the barrier, each in its service,
// each with its own log; with the barrier open both are delivered.
func TestListen_TicketsOfTwoServicesRunAtOnce(t *testing.T) {
	env := newListenEnv(t, listenOptions{pPre: 0.85, maxParallel: 2})
	shipping := env.addService("shipping")
	env.jira.add("PAY-12", "Add() subtracts", listenTask, "service:billing")
	env.jira.add("PAY-13", "Add() still subtracts", listenTask, "service:shipping")
	barrier := newRunBarrier(2)
	defer app.SetListenRunHook(barrier.hook)()
	defer barrier.open()

	ctx, stop := context.WithCancel(context.Background())
	defer stop()
	var stderr syncBuffer
	wait := env.startListener(ctx, &stderr)

	if keys := barrier.waitReached(t); !slices.Equal(keys, []string{"PAY-12", "PAY-13"}) {
		t.Fatalf("held runs = %v, want both tickets", keys)
	}
	for key, service := range map[string]string{"PAY-12": "billing", "PAY-13": "shipping"} {
		if it, ok := env.ticket(key); !ok || it.Phase != "running" || it.Service != service {
			t.Errorf("%s = %+v, want in progress in %s", key, it, service)
		}
	}
	barrier.open()
	waitFor(t, "both tickets to finish", func() bool { return env.finished("PAY-12") && env.finished("PAY-13") })
	stop()
	if err := wait(); err != nil {
		t.Fatalf("the stopped listener returned %v\nstderr:\n%s", err, stderr.String())
	}

	env.delivered(t, "PAY-12", env.svc)
	env.delivered(t, "PAY-13", shipping)
	log := stderr.String()
	logs := filepath.Join(env.wd, ".orchestrator", "listener", "logs")
	for key, service := range map[string]string{"PAY-12": "billing", "PAY-13": "shipping"} {
		file := filepath.Join(logs, key+".1.log")
		if !strings.Contains(log, "listen: "+key+" running in "+service+" (log: "+file+")") {
			t.Errorf("stderr lacks the start line of %s with its log:\n%s", key, log)
		}
		if info, err := os.Stat(file); err != nil || info.Size() == 0 {
			t.Errorf("the log of %s: %v, want a file with the run's progress", key, err)
		}
	}
}

// Two tickets of one service never run together: the second is not claimed while the first runs,
// though a slot is free and a ticket of another service is taken in it; it runs once the first is
// done. No sample of the state ever shows both in progress.
func TestListen_TwoTicketsOfOneServiceRunOneAfterTheOther(t *testing.T) {
	env := newListenEnv(t, listenOptions{pPre: 0.85, maxParallel: 2})
	shipping := env.addService("shipping")
	env.jira.add("PAY-12", "Add() subtracts", listenTask, "service:billing")
	env.jira.add("PAY-13", "Add() still subtracts", listenTask, "service:billing")
	env.jira.add("PAY-14", "Add() once more", listenTask, "service:shipping")
	barrier := newRunBarrier(2)
	defer app.SetListenRunHook(barrier.hook)()
	defer barrier.open()

	both := make(chan struct{})
	var overlap sync.Once
	sampling, stopSampling := context.WithCancel(context.Background())
	var sampler sync.WaitGroup
	sampler.Add(1)
	go func() {
		defer sampler.Done()
		for sampling.Err() == nil {
			if u := env.unfinished(); slices.Contains(u, "PAY-12") && slices.Contains(u, "PAY-13") {
				overlap.Do(func() { close(both) })
			}
			time.Sleep(2 * time.Millisecond)
		}
	}()
	defer sampler.Wait()
	defer stopSampling()

	ctx, stop := context.WithCancel(context.Background())
	defer stop()
	var stderr syncBuffer
	wait := env.startListener(ctx, &stderr)

	if keys := barrier.waitReached(t); !slices.Equal(keys, []string{"PAY-12", "PAY-14"}) {
		t.Fatalf("held runs = %v, want PAY-12 and PAY-14", keys)
	}
	env.notClaimed(t, "PAY-13")
	if !strings.Contains(stderr.String(), "listen: PAY-13 waits for PAY-12, which is running in billing") {
		t.Errorf("stderr lacks the line that says PAY-13 waits for its service:\n%s", stderr.String())
	}
	barrier.open()
	waitFor(t, "the three tickets to finish", func() bool {
		return env.finished("PAY-12") && env.finished("PAY-13") && env.finished("PAY-14")
	})
	stop()
	if err := wait(); err != nil {
		t.Fatalf("the stopped listener returned %v\nstderr:\n%s", err, stderr.String())
	}
	select {
	case <-both:
		t.Error("PAY-12 and PAY-13, of one service, were in progress at once")
	default:
	}

	runs := env.runs()
	if len(runs) != 2 {
		t.Fatalf("billing runs = %+v, want two, one after the other\nstderr:\n%s", runs, stderr.String())
	}
	for i, key := range []string{"PAY-12", "PAY-13"} {
		if runs[i].Status != "completed" || !strings.Contains(runs[i].Task.Description, key) {
			t.Errorf("billing run %d = %+v, want %s's, completed", i+1, runs[i], key)
		}
		if got := env.jira.statusOf(key); got != "Ready to Review" {
			t.Errorf("%s is in %q, want Ready to Review", key, got)
		}
	}
	env.delivered(t, "PAY-14", shipping)
}

// A third ticket waits for a slot: with two runs in progress it is not claimed, though its service
// is free, and it is taken as soon as one of the runs ends, without waiting for the next poll interval.
func TestListen_AThirdTicketWaitsForASlot(t *testing.T) {
	env := newListenEnv(t, listenOptions{pPre: 0.85, maxParallel: 2})
	shipping := env.addService("shipping")
	ledger := env.addService("ledger")
	env.jira.add("PAY-12", "Add() subtracts", listenTask, "service:billing")
	env.jira.add("PAY-13", "Add() still subtracts", listenTask, "service:shipping")
	env.jira.add("PAY-14", "Add() once more", listenTask, "service:ledger")
	barrier := newRunBarrier(2)
	defer app.SetListenRunHook(barrier.hook)()
	defer barrier.open()

	ctx, stop := context.WithCancel(context.Background())
	defer stop()
	var stderr syncBuffer
	wait := env.startListener(ctx, &stderr)

	if keys := barrier.waitReached(t); !slices.Equal(keys, []string{"PAY-12", "PAY-13"}) {
		t.Fatalf("held runs = %v, want PAY-12 and PAY-13", keys)
	}
	// Nothing more is claimed while both slots are taken: the listener does not even poll.
	time.Sleep(300 * time.Millisecond)
	env.notClaimed(t, "PAY-14")
	if u := env.unfinished(); !slices.Equal(u, []string{"PAY-12", "PAY-13"}) {
		t.Errorf("in progress = %v, want the two runs", u)
	}
	if got := len(env.jira.searches()); got != 1 {
		t.Errorf("searches = %d while every slot is taken, want the one of the start", got)
	}

	barrier.open()
	waitFor(t, "the three tickets to finish", func() bool {
		return env.finished("PAY-12") && env.finished("PAY-13") && env.finished("PAY-14")
	})
	stop()
	if err := wait(); err != nil {
		t.Fatalf("the stopped listener returned %v\nstderr:\n%s", err, stderr.String())
	}
	env.delivered(t, "PAY-12", env.svc)
	env.delivered(t, "PAY-13", shipping)
	env.delivered(t, "PAY-14", ledger)
}

// SIGTERM with two runs in progress cuts both off: both tickets stay in the start status with no
// comment, and a restart resumes both and writes each outcome once.
func TestListen_StoppingWithTwoRunsLeavesBothToBeResumed(t *testing.T) {
	rendezvous := t.TempDir()
	t.Setenv("FAKE_RENDEZVOUS_DIR", rendezvous)
	t.Setenv("FAKE_RENDEZVOUS_ACTION", "implement")
	t.Setenv("FAKE_RENDEZVOUS_COUNT", "2")
	t.Setenv("FAKE_SLEEP_ACTION", "implement")
	t.Setenv("FAKE_SLEEP_SECONDS", "60")
	env := newListenEnv(t, listenOptions{pPre: 0.85, maxParallel: 2})
	shipping := env.addService("shipping")
	env.jira.add("PAY-12", "Add() subtracts", listenTask, "service:billing")
	env.jira.add("PAY-13", "Add() still subtracts", listenTask, "service:shipping")

	ctx, stop := context.WithCancel(context.Background())
	defer stop()
	var stderr syncBuffer
	wait := env.startListener(ctx, &stderr)
	waitFor(t, "both runs to reach implement", func() bool {
		entries, _ := os.ReadDir(rendezvous)
		return len(entries) >= 2
	})
	stop()
	if err := wait(); err != nil {
		t.Fatalf("the stopped listener returned %v\nstderr:\n%s", err, stderr.String())
	}

	for _, key := range []string{"PAY-12", "PAY-13"} {
		if got := env.jira.statusOf(key); got != "In Progress" {
			t.Errorf("%s is in %q, want it left in In Progress", key, got)
		}
		if comments := env.jira.botComments(key); len(comments) != 0 {
			t.Errorf("%s has comments %q: nothing is written for an interrupted run", key, comments)
		}
		if e := env.state().Tickets[key]; e.Phase != "running" || e.Outcome != "" || e.Attempts != 0 {
			t.Errorf("%s state = %+v, want a running ticket", key, e)
		}
	}

	// The listener starts again: both runs are resumed, each in its own slot, and finish.
	t.Setenv("FAKE_SLEEP_ACTION", "")
	t.Setenv("FAKE_RENDEZVOUS_ACTION", "")
	stderrText := mustListen(t, env)

	env.delivered(t, "PAY-12", env.svc)
	env.delivered(t, "PAY-13", shipping)
	for _, key := range []string{"PAY-12", "PAY-13"} {
		if comments := env.jira.botComments(key); len(comments) != 1 {
			t.Errorf("%s comments = %q, want one", key, comments)
		}
		if e := env.state().Tickets[key]; e.Phase != "finished" || e.Outcome != "done" || e.Attempts != 1 {
			t.Errorf("%s state = %+v, want done after one attempt", key, e)
		}
	}
	if got := transitionIDs(env); len(got) != 4 || slices.Contains(got, "22") {
		t.Errorf("transitions = %v, want the claim and done of each ticket, once", got)
	}
	if !strings.Contains(stderrText, "listen: PAY-12: resuming run ") || !strings.Contains(stderrText, "listen: PAY-13: resuming run ") {
		t.Errorf("stderr lacks the resume lines:\n%s", stderrText)
	}
}

// unsetMaxParallel takes listener.max_parallel out of the listener's configuration: the next listener
// has the default, one slot.
func (e *listenEnv) unsetMaxParallel() {
	e.t.Helper()
	data, err := os.ReadFile(e.cfg)
	if err != nil {
		e.t.Fatal(err)
	}
	text := string(data)
	line := regexp.MustCompile(`\n  max_parallel: [0-9]+`)
	if n := len(line.FindAllString(text, -1)); n != 1 {
		e.t.Fatalf("%s has %d listener.max_parallel lines, want one:\n%s", e.cfg, n, text)
	}
	if err := os.WriteFile(e.cfg, []byte(line.ReplaceAllString(text, "")), 0o644); err != nil {
		e.t.Fatal(err)
	}
}

// A restart with more runs to resume than slots: the two runs a stop cut off, with max_parallel back
// to one, are resumed one after the other, the second waiting for the first's slot before any poll.
// A --once listener then polls once its slot is free, and takes the ticket queued in the meantime, as
// a listener of one slot did before the slots.
func TestListen_ARestartResumesMoreRunsThanSlotsOneAfterTheOther(t *testing.T) {
	rendezvous := t.TempDir()
	t.Setenv("FAKE_RENDEZVOUS_DIR", rendezvous)
	t.Setenv("FAKE_RENDEZVOUS_ACTION", "implement")
	t.Setenv("FAKE_RENDEZVOUS_COUNT", "2")
	t.Setenv("FAKE_SLEEP_ACTION", "implement")
	t.Setenv("FAKE_SLEEP_SECONDS", "60")
	env := newListenEnv(t, listenOptions{pPre: 0.85, maxParallel: 2})
	shipping := env.addService("shipping")
	ledger := env.addService("ledger")
	env.jira.add("PAY-12", "Add() subtracts", listenTask, "service:billing")
	env.jira.add("PAY-13", "Add() still subtracts", listenTask, "service:shipping")

	ctx, stop := context.WithCancel(context.Background())
	defer stop()
	var stderr syncBuffer
	wait := env.startListener(ctx, &stderr)
	waitFor(t, "both runs to reach implement", func() bool {
		entries, _ := os.ReadDir(rendezvous)
		return len(entries) >= 2
	})
	stop()
	if err := wait(); err != nil {
		t.Fatalf("the stopped listener returned %v\nstderr:\n%s", err, stderr.String())
	}
	if u := env.unfinished(); !slices.Equal(u, []string{"PAY-12", "PAY-13"}) {
		t.Fatalf("unfinished = %v, want both runs left to resume\nstderr:\n%s", u, stderr.String())
	}

	// One slot from now on, and a ticket queued while the listener was down.
	env.unsetMaxParallel()
	env.jira.add("PAY-14", "Add() once more", listenTask, "service:ledger")
	t.Setenv("FAKE_SLEEP_ACTION", "")
	t.Setenv("FAKE_RENDEZVOUS_ACTION", "")
	searches := len(env.jira.searches())
	log := mustListen(t, env)

	env.delivered(t, "PAY-12", env.svc)
	env.delivered(t, "PAY-13", shipping)
	env.delivered(t, "PAY-14", ledger)
	if got := len(env.jira.searches()) - searches; got != 1 {
		t.Errorf("searches of the --once listener = %d, want one", got)
	}
	// In order, each after the previous one's end: the two resumed runs, then the poll's ticket.
	marks := []string{
		"listen: PAY-12: resuming run ", "listen: PAY-12 → Ready to Review",
		"listen: PAY-13: resuming run ", "listen: PAY-13 → Ready to Review",
		"listen: PAY-14 claimed", "listen: PAY-14 → Ready to Review",
	}
	last := -1
	for _, mark := range marks {
		at := strings.Index(log, mark)
		if at < 0 || at < last {
			t.Fatalf("stderr has %q at %d, want it after the previous mark (at %d):\n%s", mark, at, last, log)
		}
		last = at
	}
	if strings.Contains(log, "(log: ") {
		t.Errorf("a listener of one slot names log files:\n%s", log)
	}
}

// A panic in one run is that ticket's failure alone: the other run, in another service, is still in
// progress, and is delivered afterwards.
func TestListen_APanicInOneRunLeavesTheOtherRunning(t *testing.T) {
	env := newListenEnv(t, listenOptions{pPre: 0.85, maxParallel: 2})
	shipping := env.addService("shipping")
	env.jira.add("PAY-12", "Add() subtracts", listenTask, "service:billing")
	env.jira.add("PAY-13", "Add() still subtracts", listenTask, "service:shipping")
	release := make(chan struct{})
	var opened sync.Once
	open := func() { opened.Do(func() { close(release) }) }
	defer open()
	defer app.SetListenRunHook(func(key string) {
		if key == "PAY-12" {
			panic("injected failure")
		}
		select {
		case <-release:
		case <-time.After(30 * time.Second):
		}
	})()

	ctx, stop := context.WithCancel(context.Background())
	defer stop()
	var stderr syncBuffer
	wait := env.startListener(ctx, &stderr)
	waitFor(t, "PAY-12 to fail", func() bool { return env.finished("PAY-12") })

	if got := env.jira.statusOf("PAY-12"); got != "Failed" {
		t.Errorf("PAY-12 is in %q, want Failed", got)
	}
	if it, ok := env.ticket("PAY-13"); !ok || it.Phase != "running" {
		t.Errorf("PAY-13 = %+v, want still in progress", it)
	}
	if got := env.jira.statusOf("PAY-13"); got != "In Progress" {
		t.Errorf("PAY-13 is in %q, want In Progress", got)
	}

	open()
	waitFor(t, "PAY-13 to finish", func() bool { return env.finished("PAY-13") })
	stop()
	if err := wait(); err != nil {
		t.Fatalf("the listener returned %v after a panic in a run, want nil\nstderr:\n%s", err, stderr.String())
	}
	if e := env.state().Tickets["PAY-12"]; e.Outcome != "failed" || e.Attempts != 1 {
		t.Errorf("PAY-12 state = %+v, want failed after one attempt", e)
	}
	if runs := env.runs(); len(runs) != 0 {
		t.Errorf("billing runs = %+v, want none: the panicked ticket never got to create one", runs)
	}
	env.delivered(t, "PAY-13", shipping)
	if log := stderr.String(); !strings.Contains(log, "listen: PAY-12: the run panicked: injected failure") {
		t.Errorf("stderr lacks the panic line:\n%s", log)
	}
}

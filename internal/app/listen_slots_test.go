package app

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/kasorse/orch/internal/config"
	"github.com/kasorse/orch/internal/jira"
	"github.com/kasorse/orch/internal/listener"
)

// The listener's slots (specs/listener-story/SPEC.md §3): at most listener.max_parallel runs, at
// most one per service, each in a goroutine of its own that keeps a panic to its ticket.

// newSlotRunner is a listener with maxParallel slots, a state file of its own and its output in out;
// it has no Jira unless the test gives it one.
func newSlotRunner(t *testing.T, maxParallel int, out io.Writer) *listenRunner {
	t.Helper()
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	return &listenRunner{
		ctx: ctx, cancel: cancel, bg: context.WithoutCancel(ctx),
		lc: &config.ListenerConfig{
			MaxParallel: maxParallel, PollInterval: time.Minute,
			Transitions: config.ListenerTransitions{Start: "In Progress", Blocked: "Blocked", Failed: "Failed", Done: "Ready to Review", Retry: "To Do"},
		},
		stderr: &lockedWriter{w: out}, state: listener.NewState(), stateFile: filepath.Join(t.TempDir(), "state.json"),
		now: time.Now, runs: map[string]string{}, wake: make(chan struct{}, 1),
	}
}

func TestListenSlots_AtMostOneRunPerServiceWithinMaxParallel(t *testing.T) {
	r := newSlotRunner(t, 2, io.Discard)
	hold := make(chan struct{})
	defer func() {
		select {
		case <-hold:
		default:
			close(hold)
		}
		r.wg.Wait()
	}()

	r.startRun("PAY-1", "billing", func() error { <-hold; return nil })
	if r.canStart("billing") || r.canStart("BILLING") {
		t.Error("a second run of billing may start while PAY-1 runs there")
	}
	if !r.canStart("shipping") || !r.canStart("") || !r.slotFree() {
		t.Error("another service cannot start while a slot is free")
	}
	if holder, busy := r.holder("Billing"); !busy || holder != "PAY-1" {
		t.Errorf("holder(billing) = %q, %v, want PAY-1", holder, busy)
	}
	if r.bind("PAY-2", "billing") {
		t.Error("PAY-2 was bound to billing, where PAY-1 runs")
	}

	// A run whose service is named only once it runs takes a slot, then its service.
	r.startRun("PAY-2", "", func() error { <-hold; return nil })
	if !r.bind("PAY-2", "shipping") {
		t.Error("PAY-2 cannot be bound to shipping, where nothing runs")
	}
	if r.slotFree() || r.canStart("ledger") || r.canStart("") {
		t.Error("a run may start with both slots taken")
	}
	if holder, busy := r.holder("shipping"); !busy || holder != "PAY-2" {
		t.Errorf("holder(shipping) = %q, %v, want PAY-2", holder, busy)
	}

	close(hold)
	r.wg.Wait()
	if !r.slotFree() || !r.canStart("billing") || !r.canStart("shipping") {
		t.Error("the slots are still taken after both runs ended")
	}
	select {
	case <-r.wake:
	default:
		t.Error("the end of a run did not wake the loop")
	}
	if err := r.fatalErr(); err != nil {
		t.Errorf("fatal = %v, want none", err)
	}
}

// A panic in a run's goroutine outside the run's own call (the checkout, the task file, the writes)
// is the ticket's alone: its slot is freed, the listener does not stop, and its other runs go on.
func TestListenSlots_APanicOutsideTheRunLeavesTheListenerRunning(t *testing.T) {
	var out bytes.Buffer
	r := newSlotRunner(t, 2, &out)
	other := make(chan struct{})
	r.startRun("PAY-2", "shipping", func() error { <-other; return nil })
	r.startRun("PAY-1", "billing", func() error { panic("boom in the checkout") })

	waitSlot(t, r, "billing")
	if r.ctx.Err() != nil || r.fatalErr() != nil || r.stopError() != nil {
		t.Errorf("the listener stops after a panic outside a run: ctx %v, fatal %v, stop %v", r.ctx.Err(), r.fatalErr(), r.stopError())
	}
	if holder, busy := r.holder("shipping"); !busy || holder != "PAY-2" {
		t.Errorf("holder(shipping) = %q, %v, want PAY-2 still running", holder, busy)
	}
	close(other)
	r.wg.Wait()
	if log := out.String(); !strings.Contains(log, "listen: PAY-1: the listener panicked in its cycle: boom in the checkout") {
		t.Errorf("stderr lacks the panic line:\n%s", log)
	}
}

// A panic before the outcome is decided ends the ticket as failed, as a run that panicked is: the
// comment with the reason and the failed transition, the entry finished.
func TestListenSlots_APanicBeforeTheOutcomeEndsTheTicketAsFailed(t *testing.T) {
	fake := newSlotJira(t, "In Progress")
	var out bytes.Buffer
	r := newSlotRunner(t, 2, &out)
	r.jira = fake.client()
	claimed(t, r, "PAY-1", listener.Entry{Status: "In Progress", Phase: listener.PhaseClaimed})

	r.startRun("PAY-1", "", func() error { panic("boom in the task file") })
	r.wg.Wait()

	if err := r.fatalErr(); err != nil {
		t.Fatalf("fatal = %v, want none\n%s", err, out.String())
	}
	e := r.entryOf("PAY-1")
	if e.Phase != listener.PhaseFinished || e.Outcome != listener.ResultFailed || e.Attempts != 0 ||
		!strings.Contains(e.Reason, "internal error: boom in the task file") {
		t.Errorf("entry = %+v, want finished as failed with the panic as its reason", e)
	}
	posts := fake.posted()
	if len(posts) != 2 || !strings.Contains(posts[0], "/PAY-1/comment") || !strings.Contains(posts[0], "internal error: boom in the task file") ||
		!strings.Contains(posts[1], `/PAY-1/transitions {"transition":{"id":"22"}}`) {
		t.Errorf("posts = %q, want the comment with the reason, then the failed transition", posts)
	}
}

// A panic after the outcome was decided keeps it: nothing is written now, the entry stays unfinished
// and a restart writes what is missing.
func TestListenSlots_APanicAfterTheOutcomeLeavesItForTheRestart(t *testing.T) {
	var out bytes.Buffer
	r := newSlotRunner(t, 1, &out)
	claimed(t, r, "PAY-1", listener.Entry{Status: "In Progress", Phase: listener.PhaseClaimed, Outcome: listener.ResultBlocked, Reason: "no service"})

	r.startRun("PAY-1", "billing", func() error { panic("boom in the write") })
	r.wg.Wait()

	if err := r.fatalErr(); err != nil {
		t.Fatalf("fatal = %v, want none", err)
	}
	if e := r.entryOf("PAY-1"); e.Phase != listener.PhaseClaimed || e.Outcome != listener.ResultBlocked {
		t.Errorf("entry = %+v, want it as it was, for the restart", e)
	}
	if !r.slotFree() {
		t.Error("the slot is still taken")
	}
	if log := out.String(); !strings.Contains(log, "listen: PAY-1: what is left to write of its outcome blocked is written when the listener starts again") {
		t.Errorf("stderr lacks the line that leaves the outcome to the restart:\n%s", log)
	}
}

// claimed puts the entry e of key in the state, as dispatch leaves a claimed ticket.
func claimed(t *testing.T, r *listenRunner, key string, e listener.Entry) {
	t.Helper()
	if err := r.begin(key); err != nil {
		t.Fatal(err)
	}
	if err := r.setEntry(key, e); err != nil {
		t.Fatal(err)
	}
}

// waitSlot waits until a run may start in service.
func waitSlot(t *testing.T, r *listenRunner, service string) {
	t.Helper()
	deadline := time.Now().Add(10 * time.Second)
	for !r.canStart(service) {
		if time.Now().After(deadline) {
			t.Fatalf("the slot of %s was never freed", service)
		}
		time.Sleep(5 * time.Millisecond)
	}
}

// slotJira answers what the end of a cycle reads and writes: the status, the transitions offered
// from it, a comment and a transition, which it records.
type slotJira struct {
	srv    *httptest.Server
	status string

	mu    sync.Mutex
	posts []string
}

func newSlotJira(t *testing.T, status string) *slotJira {
	t.Helper()
	j := &slotJira{status: status}
	j.srv = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch {
		case req.Method == http.MethodGet && strings.HasSuffix(req.URL.Path, "/transitions"):
			fmt.Fprint(w, `{"transitions":[{"id":"21","name":"Block","to":{"name":"Blocked"}},{"id":"22","name":"Fail","to":{"name":"Failed"}}]}`)
		case req.Method == http.MethodGet && req.URL.RawQuery == "fields=status":
			fmt.Fprintf(w, `{"key":"PAY-1","fields":{"status":{"name":%q}}}`, j.status)
		case req.Method == http.MethodPost:
			body, _ := io.ReadAll(req.Body)
			j.mu.Lock()
			j.posts = append(j.posts, req.URL.Path+" "+strings.TrimSpace(string(body)))
			j.mu.Unlock()
			if strings.HasSuffix(req.URL.Path, "/comment") {
				w.WriteHeader(http.StatusCreated)
				fmt.Fprint(w, `{"id":"10001"}`)
				return
			}
			w.WriteHeader(http.StatusNoContent)
		default:
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	t.Cleanup(j.srv.Close)
	return j
}

func (j *slotJira) client() *jira.Client {
	return jira.NewHTTPClient(jira.Options{BaseURL: j.srv.URL, Auth: jira.AuthBasic, Timeout: 10 * time.Second},
		jira.NewCredentials(jira.AuthBasic, "bot@example.com", "token"))
}

func (j *slotJira) posted() []string {
	j.mu.Lock()
	defer j.mu.Unlock()
	return append([]string(nil), j.posts...)
}

package listener

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"regexp"
	"sort"
	"strings"
	"time"

	"github.com/kasorse/orch/internal/domain"
)

// StateVersion is the "v" of state.json.
const StateVersion = 1

// Bounds of the state, enforced when it is read back.
const (
	maxStateTickets  = 10000
	maxStateAttempts = 1000
	maxStateReason   = 1000
	maxStateRetries  = 64
)

// Phase is how far a ticket got in its cycle (specs/jira-listener/SPEC.md §9).
type Phase string

const (
	// PhaseClaiming is saved before the claiming transition is requested: the request may have
	// reached Jira even when it reports a failure.
	PhaseClaiming Phase = "claiming"
	// PhaseClaimed is a ticket the listener holds: moved to listener.transitions.start, or found
	// spent or an Epic in the queue and about to be commented and moved without a claim.
	PhaseClaimed Phase = "claimed"
	// PhaseRunning is saved before the child is started.
	PhaseRunning Phase = "running"
	// PhaseFinished is a cycle whose outcome has been written, or given up on.
	PhaseFinished Phase = "finished"
)

// Valid reports whether p is one of the phases.
func (p Phase) Valid() bool {
	switch p {
	case PhaseClaiming, PhaseClaimed, PhaseRunning, PhaseFinished:
		return true
	}
	return false
}

// CanAdvance reports whether a ticket in phase p may move to phase to: the cycle goes claiming,
// claimed, running, finished; a claimed ticket that never ran may finish, and a finished one starts
// a new cycle when it is picked again.
func (p Phase) CanAdvance(to Phase) bool {
	switch p {
	case PhaseClaiming:
		return to == PhaseClaimed || to == PhaseFinished
	case PhaseClaimed:
		return to == PhaseRunning || to == PhaseFinished
	case PhaseRunning:
		return to == PhaseFinished
	case PhaseFinished:
		return to == PhaseClaiming
	}
	return false
}

// Result is what a cycle came to: the transition that was decided for the ticket.
type Result string

const (
	ResultDone    Result = "done"
	ResultBlocked Result = "blocked"
	ResultFailed  Result = "failed"
	ResultRetry   Result = "retry"
	// ResultNone is a cycle that was interrupted: the ticket stays where it is.
	ResultNone Result = "none"
	// ResultSpent is a ticket whose attempts were used up: one comment and the failed transition.
	ResultSpent Result = "spent"
)

// Valid reports whether r is a result a state may hold; "" is no result yet.
func (r Result) Valid() bool {
	switch r {
	case "", ResultDone, ResultBlocked, ResultFailed, ResultRetry, ResultNone, ResultSpent:
		return true
	}
	return false
}

// Values of Write.State.
const (
	// WriteIntent is saved before the request: it may have reached Jira, so it is never repeated.
	WriteIntent = "intent"
	// WriteDone is a request Jira answered.
	WriteDone = "done"
)

// Write is the record of one write to Jira. State is empty until the intent is saved, and ID, the
// comment's id, goes with WriteDone for a comment only.
type Write struct {
	State string `json:"state,omitempty"`
	ID    string `json:"id,omitempty"`
}

// Writes are the two writes of a cycle's outcome.
type Writes struct {
	Comment    Write `json:"comment,omitzero"`
	Transition Write `json:"transition,omitzero"`
}

// Entry is what the listener keeps of one ticket.
type Entry struct {
	// Attempts are the finished runs of the ticket that count (SPEC §8); they survive a new cycle.
	Attempts int `json:"attempts"`
	// Retries are the infrastructure failures in a row, which set NextTryAt; a run that counts resets it.
	Retries int    `json:"retries,omitempty"`
	Service string `json:"service,omitempty"`
	// Status is the name of the status the claim moves the ticket to, or for a ticket finished before
	// any claim (spent, an Epic) the queue's status it was found in: where the ticket must still be
	// when the outcome is written (D13).
	Status string `json:"status,omitempty"`
	Phase  Phase  `json:"phase"`
	// StartedAt is when the child was about to start; the run is found by it when RunID is not known.
	StartedAt time.Time `json:"started_at,omitzero"`
	// Interrupted says the child ended because the listener was told to stop: its run is resumed on
	// restart whatever status run.json shows (a call cut off by the interrupt fails the run).
	Interrupted bool   `json:"interrupted,omitempty"`
	RunID       string `json:"run_id,omitempty"`
	// RunDir is derived from Service and RunID (RunDir) and checked on load, never trusted as a path.
	RunDir  string `json:"run_dir,omitempty"`
	Outcome Result `json:"outcome,omitempty"`
	// Reason is the one-line cause of an outcome decided before any run (a service that could not be
	// found, a ticket too long): what the comment says.
	Reason    string    `json:"reason,omitempty"`
	Writes    Writes    `json:"writes,omitzero"`
	NextTryAt time.Time `json:"next_try_at,omitzero"`
}

// State is state.json: per ticket key, the Entry.
type State struct {
	V       int              `json:"v"`
	Tickets map[string]Entry `json:"tickets"`
}

// NewState is an empty state.
func NewState() State { return State{V: StateVersion, Tickets: map[string]Entry{}} }

var runIDPattern = regexp.MustCompile(`^[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}$`)

// ValidRunID reports whether s has the shape of a run id.
func ValidRunID(s string) bool { return runIDPattern.MatchString(s) }

// RunDir is the run directory of runID in the service's checkout, relative to the services
// directory, always with forward slashes.
func RunDir(service, runID string) string { return service + "/.orchestrator/runs/" + runID }

// Entry returns the entry of key and whether there is one.
func (s State) Entry(key string) (Entry, bool) {
	e, ok := s.Tickets[key]
	return e, ok
}

// Begin starts a new cycle for key: phase claiming, the attempts kept, everything else of the
// previous cycle forgotten. It is refused for a ticket whose cycle is not finished.
func (s *State) Begin(key string) error {
	e, ok := s.Tickets[key]
	if ok && !e.Phase.CanAdvance(PhaseClaiming) {
		return fmt.Errorf("%s is %s: its cycle is not finished", key, e.Phase)
	}
	s.Tickets[key] = Entry{Attempts: e.Attempts, Retries: e.Retries, Phase: PhaseClaiming}
	return nil
}

// Set replaces the entry of key with e, after checking the phase moved legally (or stayed).
func (s *State) Set(key string, e Entry) error {
	old, ok := s.Tickets[key]
	if ok && old.Phase != e.Phase && !old.Phase.CanAdvance(e.Phase) {
		return fmt.Errorf("%s cannot go from %s to %s", key, old.Phase, e.Phase)
	}
	s.Tickets[key] = e
	return nil
}

// Drop forgets key.
func (s *State) Drop(key string) { delete(s.Tickets, key) }

// Unfinished are the keys whose cycle is not finished, sorted.
func (s State) Unfinished() []string {
	var keys []string
	for k, e := range s.Tickets {
		if e.Phase != PhaseFinished {
			keys = append(keys, k)
		}
	}
	sort.Strings(keys)
	return keys
}

// Spent reports whether the ticket used up max attempts.
func (e Entry) Spent(max int) bool { return e.Attempts >= max }

// Waiting reports whether the ticket must not be picked yet: its NextTryAt is still ahead.
func (e Entry) Waiting(now time.Time) bool { return !e.NextTryAt.IsZero() && now.Before(e.NextTryAt) }

// Backoff is the wait after n failures in a row (n >= 1) when base is the poll interval: base,
// then doubling, at most an hour.
func Backoff(base time.Duration, n int) time.Duration {
	if base <= 0 {
		base = time.Minute
	}
	d := base
	for i := 1; i < n && d < time.Hour; i++ {
		d *= 2
	}
	return min(d, time.Hour)
}

// Validate checks a state read back from disk (SPEC §9).
func (s State) Validate() error {
	if s.V != StateVersion {
		return fmt.Errorf("v is %d, want %d", s.V, StateVersion)
	}
	if len(s.Tickets) > maxStateTickets {
		return fmt.Errorf("%d tickets, at most %d", len(s.Tickets), maxStateTickets)
	}
	for key, e := range s.Tickets {
		if !domain.ValidTicketKey(key) {
			return fmt.Errorf("%q is not an issue key", key)
		}
		if err := e.validate(); err != nil {
			return fmt.Errorf("%s: %w", key, err)
		}
	}
	return nil
}

func (e Entry) validate() error {
	switch {
	case !e.Phase.Valid():
		return fmt.Errorf("phase %q is not claiming, claimed, running or finished", e.Phase)
	case e.Attempts < 0 || e.Attempts > maxStateAttempts:
		return fmt.Errorf("attempts %d outside 0..%d", e.Attempts, maxStateAttempts)
	case e.Retries < 0 || e.Retries > maxStateRetries:
		return fmt.Errorf("retries %d outside 0..%d", e.Retries, maxStateRetries)
	case e.Service != "" && !ValidService(e.Service):
		return fmt.Errorf("service %q is not a service name", e.Service)
	case len(e.Status) > 255 || strings.ContainsFunc(e.Status, func(r rune) bool { return r < ' ' || r == 0x7f }):
		return errors.New("status must be one line of at most 255 bytes")
	case !e.Outcome.Valid():
		return fmt.Errorf("outcome %q is not done, blocked, failed, retry, none or spent", e.Outcome)
	case len(e.Reason) > maxStateReason || strings.ContainsFunc(e.Reason, func(r rune) bool { return r < ' ' || r == 0x7f }):
		return fmt.Errorf("reason must be one line of at most %d bytes", maxStateReason)
	case e.Interrupted && e.Phase != PhaseRunning:
		return errors.New("interrupted goes with a running ticket only")
	case e.Phase == PhaseRunning && e.Service == "":
		return errors.New("a running ticket needs its service")
	case e.Phase == PhaseFinished && e.Outcome == "":
		return errors.New("a finished ticket needs an outcome")
	case (e.RunID == "") != (e.RunDir == ""):
		return errors.New("run_id and run_dir go together")
	case !e.NextTryAt.IsZero() && e.Outcome != ResultRetry:
		return errors.New("next_try_at goes with the outcome retry only")
	}
	if e.RunID != "" {
		switch {
		case e.Service == "":
			return errors.New("a run needs its service")
		case !ValidRunID(e.RunID):
			return fmt.Errorf("run_id %q is not a run id", e.RunID)
		case e.RunDir != RunDir(e.Service, e.RunID):
			return fmt.Errorf("run_dir %q is not %q, which the service and the run id give", e.RunDir, RunDir(e.Service, e.RunID))
		}
	}
	if err := e.Writes.Comment.validate(true); err != nil {
		return fmt.Errorf("writes.comment: %w", err)
	}
	if err := e.Writes.Transition.validate(false); err != nil {
		return fmt.Errorf("writes.transition: %w", err)
	}
	return nil
}

func (w Write) validate(comment bool) error {
	switch w.State {
	case "", WriteIntent:
		if w.ID != "" {
			return errors.New("an id goes with state done only")
		}
	case WriteDone:
		if (w.ID != "") != comment {
			return errors.New("an id goes with a done comment only")
		}
		if len(w.ID) > 20 || strings.Trim(w.ID, "0123456789") != "" {
			return fmt.Errorf("id %q is not a number", w.ID)
		}
	default:
		return fmt.Errorf("state %q is not intent or done", w.State)
	}
	return nil
}

// ParseState reads state.json strictly: one JSON object, no unknown key, nothing after it, and
// every field valid.
func ParseState(data []byte) (State, error) {
	var s State
	dec := json.NewDecoder(bytes.NewReader(data))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&s); err != nil {
		return State{}, fmt.Errorf("the listener state is not valid: %w", err)
	}
	if _, err := dec.Token(); err != io.EOF {
		return State{}, errors.New("the listener state has data after its JSON object")
	}
	if s.Tickets == nil {
		s.Tickets = map[string]Entry{}
	}
	if err := s.Validate(); err != nil {
		return State{}, fmt.Errorf("the listener state is not valid: %w", err)
	}
	return s, nil
}

// Marshal is the bytes of state.json, validated first.
func (s State) Marshal() ([]byte, error) {
	if err := s.Validate(); err != nil {
		return nil, err
	}
	data, err := json.MarshalIndent(s, "", "  ")
	if err != nil {
		return nil, err
	}
	return append(data, '\n'), nil
}

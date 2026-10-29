package listener

import (
	"strings"
	"testing"
	"time"
)

const testRunID = "3b3001ed-2068-4e11-b487-665ab316098e"

func validState() State {
	s := NewState()
	s.Tickets["PAY-12"] = Entry{
		Attempts: 1, Service: "billing", Status: "In Progress", Phase: PhaseRunning,
		StartedAt: time.Date(2026, 10, 29, 10, 0, 0, 0, time.UTC),
		RunID:     testRunID, RunDir: RunDir("billing", testRunID),
	}
	return s
}

func TestStateRoundTrip(t *testing.T) {
	s := validState()
	s.Tickets["PAY-13"] = Entry{
		Attempts: 2, Phase: PhaseFinished, Outcome: ResultRetry, Retries: 1,
		NextTryAt: time.Date(2026, 10, 29, 11, 0, 0, 0, time.UTC),
		Writes:    Writes{Comment: Write{State: WriteDone, ID: "10042"}, Transition: Write{State: WriteIntent}},
	}
	data, err := s.Marshal()
	if err != nil {
		t.Fatal(err)
	}
	got, err := ParseState(data)
	if err != nil {
		t.Fatalf("ParseState: %v\n%s", err, data)
	}
	if e := got.Tickets["PAY-13"]; e.Writes.Comment.ID != "10042" || !e.NextTryAt.Equal(s.Tickets["PAY-13"].NextTryAt) {
		t.Errorf("round trip lost data: %+v", e)
	}
}

func TestParseStateRefuses(t *testing.T) {
	tests := []struct {
		name, json, want string
	}{
		{"not json", `{`, "not valid"},
		{"unknown field", `{"v":1,"tickets":{},"extra":1}`, "unknown field"},
		{"trailing data", `{"v":1,"tickets":{}} {}`, "data after"},
		{"wrong version", `{"v":2,"tickets":{}}`, "v is 2"},
		{"bad key", `{"v":1,"tickets":{"pay-1":{"phase":"claimed","attempts":0}}}`, "not an issue key"},
		{"bad phase", `{"v":1,"tickets":{"PAY-1":{"phase":"done","attempts":0}}}`, "phase"},
		{"negative attempts", `{"v":1,"tickets":{"PAY-1":{"phase":"claimed","attempts":-1}}}`, "attempts"},
		{"finished without outcome", `{"v":1,"tickets":{"PAY-1":{"phase":"finished","attempts":0}}}`, "needs an outcome"},
		{"running without service", `{"v":1,"tickets":{"PAY-1":{"phase":"running","attempts":0}}}`, "needs its service"},
		{"run dir that is not derived", `{"v":1,"tickets":{"PAY-1":{"phase":"running","attempts":0,"service":"billing","run_id":"` + testRunID + `","run_dir":"../../etc"}}}`, "run_dir"},
		{"run id without dir", `{"v":1,"tickets":{"PAY-1":{"phase":"running","attempts":0,"service":"billing","run_id":"` + testRunID + `"}}}`, "go together"},
		{"bad write state", `{"v":1,"tickets":{"PAY-1":{"phase":"claimed","attempts":0,"writes":{"comment":{"state":"sent"}}}}}`, "intent or done"},
		{"next_try_at without retry", `{"v":1,"tickets":{"PAY-1":{"phase":"finished","attempts":0,"outcome":"failed","next_try_at":"2026-10-29T11:00:00Z"}}}`, "retry only"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			_, err := ParseState([]byte(tc.json))
			if err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("err = %v, want one containing %q", err, tc.want)
			}
		})
	}
}

func TestPhaseTransitions(t *testing.T) {
	ok := [][2]Phase{
		{PhaseClaiming, PhaseClaimed}, {PhaseClaimed, PhaseRunning}, {PhaseRunning, PhaseFinished},
		{PhaseClaimed, PhaseFinished}, {PhaseFinished, PhaseClaiming},
	}
	for _, p := range ok {
		if !p[0].CanAdvance(p[1]) {
			t.Errorf("%s -> %s should be allowed", p[0], p[1])
		}
	}
	bad := [][2]Phase{
		{PhaseRunning, PhaseClaimed}, {PhaseFinished, PhaseRunning}, {PhaseClaiming, PhaseRunning}, {PhaseRunning, PhaseClaiming},
	}
	for _, p := range bad {
		if p[0].CanAdvance(p[1]) {
			t.Errorf("%s -> %s should be refused", p[0], p[1])
		}
	}
}

func TestBeginKeepsAttemptsAndRefusesAnUnfinishedCycle(t *testing.T) {
	s := NewState()
	if err := s.Begin("PAY-1"); err != nil {
		t.Fatal(err)
	}
	if err := s.Begin("PAY-1"); err == nil {
		t.Fatal("a second Begin on a cycle in progress must fail")
	}
	e := s.Tickets["PAY-1"]
	e.Attempts, e.Phase, e.Outcome, e.Reason = 2, PhaseFinished, ResultBlocked, "why"
	s.Tickets["PAY-1"] = e
	if err := s.Begin("PAY-1"); err != nil {
		t.Fatal(err)
	}
	if got := s.Tickets["PAY-1"]; got.Attempts != 2 || got.Phase != PhaseClaiming || got.Outcome != "" || got.Reason != "" {
		t.Errorf("new cycle = %+v, want the attempts kept and the rest forgotten", got)
	}
	if got := s.Unfinished(); len(got) != 1 || got[0] != "PAY-1" {
		t.Errorf("Unfinished = %v", got)
	}
}

func TestWaitingAndSpent(t *testing.T) {
	now := time.Date(2026, 10, 29, 10, 0, 0, 0, time.UTC)
	e := Entry{NextTryAt: now.Add(time.Minute), Attempts: 3}
	if !e.Waiting(now) || e.Waiting(now.Add(2*time.Minute)) || (Entry{}).Waiting(now) {
		t.Error("Waiting is wrong")
	}
	if !e.Spent(3) || !e.Spent(2) || e.Spent(4) {
		t.Error("Spent is wrong")
	}
}

func TestBackoffDoublesToAnHour(t *testing.T) {
	base := time.Minute
	want := []time.Duration{time.Minute, 2 * time.Minute, 4 * time.Minute, 8 * time.Minute, 16 * time.Minute, 32 * time.Minute, time.Hour, time.Hour}
	for i, w := range want {
		if got := Backoff(base, i+1); got != w {
			t.Errorf("Backoff(%s, %d) = %s, want %s", base, i+1, got, w)
		}
	}
}

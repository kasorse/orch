package main

import (
	"strings"
	"testing"
	"time"
)

func TestSafeTaskNeverLooksLikeAFlag(t *testing.T) {
	for _, task := range []string{"--config /x", "-v", "--jira=X-1"} {
		a := runArgs(Request{Task: task})
		if last := a[len(a)-1]; strings.HasPrefix(last, "-") {
			t.Fatalf("task %q reaches argv as %q", task, last)
		}
	}
	if got := safeTask("fix it"); got != "fix it" {
		t.Fatalf("plain task changed: %q", got)
	}
}

func TestRedactHidesTokens(t *testing.T) {
	// Built from pieces so no token-shaped literal sits in the source (secret scans).
	slackTok := "xo" + "xb-" + "1234567890-abcdefghij"
	gitlabTok := "gl" + "pat-" + "abcdefghijklmnop"
	in := "auth " + slackTok + " and " + gitlabTok + " api_key=" + "supersecret1"
	out := codeBlock(in)
	for _, s := range []string{slackTok, gitlabTok, "supersecret1"} {
		if strings.Contains(out, s) {
			t.Fatalf("%q leaked: %s", s, out)
		}
	}
}

func TestPruneExpiresOldState(t *testing.T) {
	b := &Bridge{seen: map[string]time.Time{}, pending: map[string]pending{}, asks: map[string]job{}}
	now := time.Now()
	b.seen["old"] = now.Add(-seenTTL - time.Second)
	b.seen["new"] = now
	b.pending["old"] = pending{at: now.Add(-decisionTTL - time.Second)}
	b.pending["new"] = pending{at: now}
	b.asks["old"] = job{at: now.Add(-decisionTTL - time.Second)}
	b.asks["new"] = job{at: now}
	b.pruneLocked(now)
	if len(b.seen) != 1 || len(b.pending) != 1 || len(b.asks) != 1 {
		t.Fatalf("prune kept %d/%d/%d", len(b.seen), len(b.pending), len(b.asks))
	}
}

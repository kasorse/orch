package main

import (
	"regexp"
	"time"
)

// seenTTL is how long a message key is kept to drop Slack's duplicate deliveries.
var seenTTL = 10 * time.Minute

// decisionTTL is how long a plan waits for Run/Cancel and an unclear request for a reply.
var decisionTTL = 24 * time.Hour

// pruneLocked drops old dedup keys, plans and parked requests. Callers hold b.mu.
func (b *Bridge) pruneLocked(now time.Time) {
	for k, t := range b.seen {
		if now.Sub(t) > seenTTL {
			delete(b.seen, k)
		}
	}
	for k, p := range b.pending {
		if now.Sub(p.at) > decisionTTL {
			delete(b.pending, k)
		}
	}
	for k, j := range b.asks {
		if now.Sub(j.at) > decisionTTL {
			delete(b.asks, k)
		}
	}
}

// secretRE matches token shapes that must never be posted to Slack from a log tail.
var secretRE = regexp.MustCompile(`(?i)(xox[abposr]-[A-Za-z0-9-]{8,}|xapp-[A-Za-z0-9-]{8,}|glpat-[A-Za-z0-9_-]{8,}|gl(?:ptt|cbt|dt)-[A-Za-z0-9_-]{8,}|sk-(?:ant-|proj-)?[A-Za-z0-9_-]{16,}|gh[pousr]_[A-Za-z0-9]{20,}|AKIA[0-9A-Z]{16}|eyJ[A-Za-z0-9_-]{10,}\.[A-Za-z0-9_-]{10,}\.[A-Za-z0-9_-]{10,}|(?:bearer|token|password|secret|api[_-]?key)\s*[:=]\s*\S{6,})`)

// redact hides token-shaped strings.
func redact(s string) string { return secretRE.ReplaceAllString(s, "[redacted]") }

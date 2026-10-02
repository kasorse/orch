package claude

import (
	"bytes"
	"encoding/json"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"orchestrator/internal/domain"
)

// The CLI runs with --output-format stream-json: one JSON event per line (init, assistant
// messages with their tool calls, tool results, rate-limit updates, and a final "result").
// The final event's "result" is the model's answer - exactly what plain --print would have
// printed - so callers see the same Output. The rest feeds the progress display: what the
// model is doing, tokens, cost and the account's rate limits.
//
// Anything that is not a stream-json transcript (an older CLI, a test double printing a
// canned answer) passes through untouched as the Output.

type streamEvent struct {
	Type    string `json:"type"`
	Subtype string `json:"subtype"`
	Message *struct {
		Content []struct {
			Type  string          `json:"type"`
			Name  string          `json:"name"`
			Input json.RawMessage `json:"input"`
		} `json:"content"`
	} `json:"message"`
	RateLimitInfo *struct {
		UnifiedWindows map[string]struct {
			Utilization float64 `json:"utilization"`
			ResetsAt    int64   `json:"resetsAt"`
		} `json:"unifiedWindows"`
	} `json:"rate_limit_info"`

	// type "result"
	Result       *string  `json:"result"`
	IsError      bool     `json:"is_error"`
	TotalCostUSD *float64 `json:"total_cost_usd"`
	NumTurns     int      `json:"num_turns"`
	Usage        *struct {
		InputTokens              int64 `json:"input_tokens"`
		OutputTokens             int64 `json:"output_tokens"`
		CacheReadInputTokens     int64 `json:"cache_read_input_tokens"`
		CacheCreationInputTokens int64 `json:"cache_creation_input_tokens"`
	} `json:"usage"`
}

// parseStream turns a whole stdout into the answer text and the call's usage. ok is false
// when stdout is not a stream-json transcript with a result event.
func parseStream(stdout string) (text string, usage *domain.Usage, isError bool, ok bool) {
	var limits *domain.RateLimits
	for _, line := range strings.Split(stdout, "\n") {
		line = strings.TrimSpace(line)
		if !strings.HasPrefix(line, "{") {
			continue
		}
		var ev streamEvent
		if json.Unmarshal([]byte(line), &ev) != nil {
			continue
		}
		switch ev.Type {
		case "rate_limit_event":
			if l := rateLimits(ev); l != nil {
				limits = l
			}
		case "result":
			if ev.Result == nil {
				continue
			}
			u := &domain.Usage{Turns: ev.NumTurns, Limits: limits}
			if ev.TotalCostUSD != nil {
				u.CostUSD = *ev.TotalCostUSD
			}
			if ev.Usage != nil {
				u.InputTokens = ev.Usage.InputTokens
				u.OutputTokens = ev.Usage.OutputTokens
				u.CacheReadTokens = ev.Usage.CacheReadInputTokens
				u.CacheCreationTokens = ev.Usage.CacheCreationInputTokens
			}
			text, usage, isError, ok = *ev.Result, u, ev.IsError, true
		}
	}
	return text, usage, isError, ok
}

func rateLimits(ev streamEvent) *domain.RateLimits {
	if ev.RateLimitInfo == nil || len(ev.RateLimitInfo.UnifiedWindows) == 0 {
		return nil
	}
	window := func(key string) *domain.LimitWindow {
		w, ok := ev.RateLimitInfo.UnifiedWindows[key]
		if !ok {
			return nil
		}
		return &domain.LimitWindow{Utilization: w.Utilization, ResetsAt: time.Unix(w.ResetsAt, 0)}
	}
	return &domain.RateLimits{FiveHour: window("five_hour"), Weekly: window("seven_day")}
}

// activityWriter watches stdout as it arrives and reports each tool call the model makes
// ("Read src/app.ts"). It never alters what is captured.
type activityWriter struct {
	mu     sync.Mutex
	buf    []byte
	report func(string)
	dir    string
}

func (a *activityWriter) Write(p []byte) (int, error) {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.buf = append(a.buf, p...)
	for {
		i := bytes.IndexByte(a.buf, '\n')
		if i < 0 {
			break
		}
		line := a.buf[:i]
		a.buf = a.buf[i+1:]
		a.line(line)
	}
	return len(p), nil
}

func (a *activityWriter) line(line []byte) {
	if len(line) == 0 || line[0] != '{' {
		return
	}
	var ev streamEvent
	if json.Unmarshal(line, &ev) != nil || ev.Type != "assistant" || ev.Message == nil {
		return
	}
	for _, c := range ev.Message.Content {
		switch c.Type {
		case "tool_use":
			a.report(describeTool(c.Name, c.Input, a.dir))
		case "thinking":
			a.report("Thinking")
		case "text":
			a.report("Writing")
		}
	}
}

// describeTool names a tool call by its most telling argument.
func describeTool(name string, input json.RawMessage, dir string) string {
	var in map[string]any
	_ = json.Unmarshal(input, &in)
	arg := func(keys ...string) string {
		for _, k := range keys {
			if s, ok := in[k].(string); ok && s != "" {
				return s
			}
		}
		return ""
	}
	detail := ""
	switch name {
	case "Read", "Edit", "Write", "MultiEdit", "NotebookEdit":
		detail = arg("file_path", "notebook_path")
		if dir != "" {
			if rel, err := filepath.Rel(dir, detail); err == nil && !strings.HasPrefix(rel, "..") {
				detail = rel
			}
		}
	case "Grep", "Glob":
		detail = arg("pattern")
	case "WebSearch":
		detail = arg("query")
	case "WebFetch":
		detail = arg("url")
	}
	if len([]rune(detail)) > 60 {
		detail = string([]rune(detail)[:59]) + "…"
	}
	if detail == "" {
		return name
	}
	return name + " " + detail
}

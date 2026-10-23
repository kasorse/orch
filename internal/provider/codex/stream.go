package codex

import (
	"bytes"
	"encoding/json"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"orchestrator/internal/domain"
)

// Codex --json emits JSONL. The parser keeps the event envelope permissive so newer CLI
// events do not hide the final answer or usage from the orchestrator.
type codexReport struct {
	text       string
	usage      *domain.Usage
	call       *domain.CallReport
	ok         bool
	isError    bool
	usageLimit bool
	resetsAt   time.Time
	apiStatus  int
}

type codexUsage struct {
	InputTokens       int64 `json:"input_tokens"`
	CachedInputTokens int64 `json:"cached_input_tokens"`
	CacheWriteTokens  int64 `json:"cache_write_input_tokens"`
	OutputTokens      int64 `json:"output_tokens"`
	ReasoningTokens   int64 `json:"reasoning_output_tokens"`
}

var codexHTTPStatus = regexp.MustCompile(`\b(?:HTTP\s*)?(4\d\d|5\d\d)\b`)

func parseJSON(stdout, configuredModel string) codexReport {
	var (
		rep    codexReport
		usage  *domain.Usage
		text   string
		tools  = map[string]int{}
		models = map[string]bool{}
		limits *domain.RateLimits
		errors []string
	)
	for _, line := range strings.Split(stdout, "\n") {
		line = strings.TrimSpace(line)
		if !strings.HasPrefix(line, "{") {
			continue
		}
		var ev map[string]json.RawMessage
		if json.Unmarshal([]byte(line), &ev) != nil {
			continue
		}
		kind := rawString(ev["type"])
		if u := decodeUsage(ev["usage"]); u != nil {
			if kind == "turn.completed" {
				usage = u
			} else {
				usage = addUsage(usage, u)
			}
		}
		switch kind {
		case "turn.completed":
			rep.ok = true
		case "turn.failed", "error":
			rep.isError = true
			textErr := eventErrorText(ev)
			errors = append(errors, textErr)
			recordError(&rep, textErr, ev)
		case "codex.rate_limits":
			if parsed, reset := parseRateLimitEvent(ev); parsed != nil {
				limits = parsed
				rep.resetsAt = firstTime(reset, rep.resetsAt)
			}
		}

		if item, ok := object(ev["item"]); ok {
			switch rawString(item["type"]) {
			case "agent_message":
				text = rawString(item["text"])
				rep.ok = true
			case "command_execution":
				if rawString(item["command"]) != "" {
					tools["command_execution"]++
				}
			case "file_change":
				tools["file_change"]++
			case "mcp_tool_call":
				name := firstNonEmpty(rawString(item["name"]), rawString(item["tool"]), "mcp_tool_call")
				tools[name]++
			case "web_search":
				tools["web_search"]++
			case "error":
				rep.isError = true
				textErr := firstNonEmpty(rawString(item["message"]), rawString(item["error"]))
				errors = append(errors, textErr)
				recordError(&rep, textErr, item)
			}
		}
		if message := firstNonEmpty(rawString(ev["message"]), rawString(ev["error"])); message != "" {
			errors = append(errors, message)
			recordError(&rep, message, ev)
		}
	}

	if usage != nil {
		usage.Turns = 1
		usage.Limits = limits
		rep.usage = usage
	} else if limits != nil {
		rep.usage = &domain.Usage{Limits: limits, Partial: true}
	}
	rep.text = text
	if status := apiStatus(errors); status != 0 {
		rep.apiStatus = status
	}
	if configuredModel != "" {
		models[configuredModel] = true
	}
	if rep.ok || text != "" || len(tools) > 0 || len(models) > 0 {
		call := &domain.CallReport{Tools: tools}
		for model := range models {
			call.Models = append(call.Models, domain.ModelUse{Model: model})
		}
		sort.Slice(call.Models, func(i, j int) bool { return call.Models[i].Model < call.Models[j].Model })
		if len(call.Tools) > 0 || len(call.Models) > 0 {
			rep.call = call
		}
	}
	return rep
}

func decodeUsage(raw json.RawMessage) *domain.Usage {
	if len(raw) == 0 || string(raw) == "null" {
		return nil
	}
	var u codexUsage
	if json.Unmarshal(raw, &u) != nil || (u.InputTokens == 0 && u.CachedInputTokens == 0 && u.CacheWriteTokens == 0 && u.OutputTokens == 0 && u.ReasoningTokens == 0) {
		return nil
	}
	inputTokens := u.InputTokens
	cacheTokens := u.CachedInputTokens + u.CacheWriteTokens
	if inputTokens >= cacheTokens {
		inputTokens -= cacheTokens
	}
	outputTokens := u.OutputTokens
	if outputTokens >= u.ReasoningTokens {
		outputTokens -= u.ReasoningTokens
	}
	return &domain.Usage{InputTokens: inputTokens, CacheReadTokens: u.CachedInputTokens,
		CacheCreationTokens: u.CacheWriteTokens, OutputTokens: outputTokens, ReasoningTokens: u.ReasoningTokens}
}

func addUsage(current, next *domain.Usage) *domain.Usage {
	if current == nil {
		copy := *next
		return &copy
	}
	current.Add(*next)
	return current
}

func object(raw json.RawMessage) (map[string]json.RawMessage, bool) {
	var out map[string]json.RawMessage
	if len(raw) == 0 || json.Unmarshal(raw, &out) != nil {
		return nil, false
	}
	return out, true
}

func rawString(raw json.RawMessage) string {
	var value string
	if json.Unmarshal(raw, &value) != nil {
		return ""
	}
	return value
}

func firstNonEmpty(values ...string) string {
	for _, value := range values {
		if value != "" {
			return value
		}
	}
	return ""
}

func eventErrorText(ev map[string]json.RawMessage) string {
	if nested, ok := object(ev["error"]); ok {
		return firstNonEmpty(rawString(nested["message"]), rawString(nested["detail"]), string(ev["error"]))
	}
	return firstNonEmpty(rawString(ev["message"]), rawString(ev["error"]))
}

func recordError(rep *codexReport, text string, obj map[string]json.RawMessage) {
	if isRateLimitText(text) {
		rep.usageLimit = true
		rep.resetsAt = firstTime(findReset(obj), rep.resetsAt)
	}
}

func isRateLimitText(text string) bool {
	text = strings.ToLower(text)
	for _, marker := range []string{"rate limit", "rate_limit", "too many requests", "quota exceeded", "usage limit", "429"} {
		if strings.Contains(text, marker) {
			return true
		}
	}
	return false
}

func isAuthText(text string) bool {
	text = strings.ToLower(text)
	for _, marker := range []string{"401 unauthorized", "invalid_api_key", "invalid api key", "authentication failed", "missing api key", "unauthorized", "login required"} {
		if strings.Contains(text, marker) {
			return true
		}
	}
	return false
}

func apiStatus(messages []string) int {
	for _, message := range messages {
		match := codexHTTPStatus.FindStringSubmatch(message)
		if len(match) == 2 {
			status, _ := strconv.Atoi(match[1])
			return status
		}
	}
	return 0
}

func findReset(obj map[string]json.RawMessage) time.Time {
	for _, key := range []string{"resets_at", "reset_at", "resetsAt", "resetAt"} {
		if parsed := parseTime(obj[key]); !parsed.IsZero() {
			return parsed
		}
	}
	for _, raw := range obj {
		if nested, ok := object(raw); ok {
			if parsed := findReset(nested); !parsed.IsZero() {
				return parsed
			}
		}
	}
	return time.Time{}
}

func parseTime(raw json.RawMessage) time.Time {
	var number float64
	if len(raw) > 0 && json.Unmarshal(raw, &number) == nil && number > 0 {
		if number > 1e12 {
			number /= 1000
		}
		return time.Unix(int64(number), 0)
	}
	value := strings.TrimSpace(rawString(raw))
	if unix, err := strconv.ParseInt(value, 10, 64); err == nil && unix > 0 {
		if unix > 1e12 {
			unix /= 1000
		}
		return time.Unix(unix, 0)
	}
	for _, layout := range []string{time.RFC3339Nano, time.RFC3339} {
		if parsed, err := time.Parse(layout, value); err == nil {
			return parsed
		}
	}
	return time.Time{}
}

func parseRateLimitEvent(ev map[string]json.RawMessage) (*domain.RateLimits, time.Time) {
	rate, ok := object(ev["rate_limits"])
	if !ok {
		rate, _ = object(ev["rateLimits"])
	}
	if len(rate) == 0 {
		return nil, time.Time{}
	}
	primary := parseLimitWindow(rate["primary"])
	secondary := parseLimitWindow(rate["secondary"])
	var reset time.Time
	if primary != nil {
		reset = primary.ResetsAt
	}
	if reset.IsZero() && secondary != nil {
		reset = secondary.ResetsAt
	}
	return &domain.RateLimits{FiveHour: primary, Weekly: secondary}, reset
}

func parseLimitWindow(raw json.RawMessage) *domain.LimitWindow {
	obj, ok := object(raw)
	if !ok {
		return nil
	}
	var used float64
	_ = json.Unmarshal(obj["used_percent"], &used)
	if used > 1 {
		used /= 100
	}
	return &domain.LimitWindow{Utilization: used, ResetsAt: findReset(obj)}
}

func firstTime(first, fallback time.Time) time.Time {
	if !first.IsZero() {
		return first
	}
	return fallback
}

// activityWriter emits the same compact progress labels as the Claude adapter while the
// complete JSONL transcript remains captured by the process executor.
type activityWriter struct {
	mu     sync.Mutex
	buf    []byte
	report func(string)
	dir    string
}

func (w *activityWriter) Write(p []byte) (int, error) {
	w.mu.Lock()
	defer w.mu.Unlock()
	w.buf = append(w.buf, p...)
	for {
		i := bytes.IndexByte(w.buf, '\n')
		if i < 0 {
			return len(p), nil
		}
		line := append([]byte(nil), w.buf[:i]...)
		w.buf = w.buf[i+1:]
		w.line(line)
	}
}

func (w *activityWriter) line(line []byte) {
	var ev map[string]json.RawMessage
	if json.Unmarshal(line, &ev) != nil {
		return
	}
	item, ok := object(ev["item"])
	if !ok {
		return
	}
	switch rawString(item["type"]) {
	case "reasoning":
		w.report("Thinking")
	case "agent_message":
		w.report("Writing")
	case "command_execution":
		w.report(activityDetail("Running ", rawString(item["command"]), w.dir))
	case "file_change":
		w.report("Writing")
	case "mcp_tool_call":
		w.report(activityDetail("MCP ", firstNonEmpty(rawString(item["name"]), rawString(item["tool"])), w.dir))
	case "web_search":
		w.report(activityDetail("WebSearch ", rawString(item["query"]), w.dir))
	}
}

func activityDetail(prefix, detail, dir string) string {
	if dir != "" && strings.Contains(detail, dir) {
		if relative, err := filepath.Rel(dir, detail); err == nil && !strings.HasPrefix(relative, "..") {
			detail = relative
		}
	}
	if len([]rune(detail)) > 60 {
		detail = string([]rune(detail)[:59]) + "…"
	}
	if detail == "" {
		return strings.TrimSpace(prefix)
	}
	return prefix + detail
}

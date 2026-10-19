package claude

import (
	"bytes"
	"encoding/json"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"sync"
	"time"
	"unicode"

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
	// type "system", subtype "init": where the CLI took its credential from
	// (specs/claude-auth/SPEC.md §8). Raw, so a shape the spec does not document drops this
	// field and not the whole event.
	APIKeySource json.RawMessage `json:"apiKeySource"`
	// type "system", subtype "api_retry": why the API call is being retried (§9).
	Error json.RawMessage `json:"error"`
	// type "assistant" (and "result"): the typed cause of an API error the CLI answered with.
	// "usage_limit_reached" is the account's own usage limit, the one refusal that a reset
	// clears; its params carry rate_limit_info.resetsAt (Unix seconds). Raw, like apiKeySource.
	APIError       json.RawMessage `json:"api_error"`
	APIErrorParams json.RawMessage `json:"api_error_params"`
	// type "system", subtype "init": each MCP server's status ("connected", "failed", ...)
	MCPServers []struct {
		Name   string `json:"name"`
		Status string `json:"status"`
	} `json:"mcp_servers"`
	Message *struct {
		ID      string       `json:"id"`
		Model   string       `json:"model"`
		Usage   *streamUsage `json:"usage"`
		Content []struct {
			Type  string          `json:"type"`
			ID    string          `json:"id"`
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
	Result        *string      `json:"result"`
	IsError       bool         `json:"is_error"`
	TotalCostUSD  *float64     `json:"total_cost_usd"`
	NumTurns      int          `json:"num_turns"`
	DurationAPIMS int64        `json:"duration_api_ms"`
	Usage         *streamUsage `json:"usage"`
	ModelUsage    map[string]struct {
		CostUSD float64 `json:"costUSD"`
	} `json:"modelUsage"`
}

type streamUsage struct {
	InputTokens              int64 `json:"input_tokens"`
	OutputTokens             int64 `json:"output_tokens"`
	CacheReadInputTokens     int64 `json:"cache_read_input_tokens"`
	CacheCreationInputTokens int64 `json:"cache_creation_input_tokens"`
}

// streamReport is what a whole stdout says: the answer, the call's usage and its report.
type streamReport struct {
	text    string
	usage   *domain.Usage
	call    *domain.CallReport
	isError bool
	// apiStatus is the HTTP status of an error result's "API Error: NNN ..." text; 0 otherwise.
	apiStatus int
	// apiKeySource is what the init event said the credential came from; "" when there was
	// no init event or it did not say.
	apiKeySource string
	// authErr is the first authentication-category error a system/api_retry event carried
	// (authErrorCategories); "" when none did.
	authErr string
	// usageLimit is set when an event said the account's usage limit refused the call;
	// limitResets is when it resets, zero when no event said.
	usageLimit  bool
	limitResets time.Time
	// ok is true when stdout is a stream-json transcript with a result event; only then
	// is text the answer.
	ok bool
}

// parseStream reads a whole stdout. With a result event, usage and the models come from
// it. Without one - the call was killed (a timeout) or crashed - usage is summed from the
// assistant messages streamed until then and marked partial: the stream repeats a
// message's usage on each of its content blocks, so each message counts once, with the
// largest value seen for each counter. Tool calls are counted from the assistant messages
// either way. A stdout with neither gives a zero report and no usage.
func parseStream(stdout string) streamReport {
	var (
		rep         streamReport
		source      string
		authErr     string
		usageLimit  bool
		limitResets time.Time
		limits      *domain.RateLimits
		messages    = map[string]streamUsage{}
		models      = map[string]bool{}
		tools       = map[string]int{}
		seenTool    = map[string]bool{}
		result      *streamEvent
		mcpFail     []string
	)
	for _, line := range strings.Split(stdout, "\n") {
		line = strings.TrimSpace(line)
		if !strings.HasPrefix(line, "{") {
			continue
		}
		var ev streamEvent
		if json.Unmarshal([]byte(line), &ev) != nil {
			continue
		}
		if rawString(ev.APIError) == "usage_limit_reached" {
			usageLimit = true
			if t := limitResetsAt(ev.APIErrorParams); !t.IsZero() {
				limitResets = t
			}
		}
		switch ev.Type {
		case "system":
			// A server that fails to start does not stop the CLI: the model just runs
			// without its tools, so the run records it.
			switch ev.Subtype {
			case "init":
				source = rawString(ev.APIKeySource)
				for _, s := range ev.MCPServers {
					if s.Status == "failed" {
						mcpFail = append(mcpFail, s.Name)
					}
				}
			case "api_retry":
				if cat := authRetryCategory(ev); authErr == "" {
					authErr = cat
				}
			}
		case "rate_limit_event":
			if l := rateLimits(ev); l != nil {
				limits = l
			}
		case "assistant":
			if ev.Message == nil {
				continue
			}
			if ev.Message.Model != "" {
				models[ev.Message.Model] = true
			}
			if id, u := ev.Message.ID, ev.Message.Usage; id != "" && u != nil {
				m := messages[id]
				m.InputTokens = max(m.InputTokens, u.InputTokens)
				m.OutputTokens = max(m.OutputTokens, u.OutputTokens)
				m.CacheReadInputTokens = max(m.CacheReadInputTokens, u.CacheReadInputTokens)
				m.CacheCreationInputTokens = max(m.CacheCreationInputTokens, u.CacheCreationInputTokens)
				messages[id] = m
			}
			for _, c := range ev.Message.Content {
				if c.Type != "tool_use" || c.Name == "" || (c.ID != "" && seenTool[c.ID]) {
					continue
				}
				seenTool[c.ID] = c.ID != ""
				tools[c.Name]++
			}
		case "result":
			if ev.Result != nil {
				ev := ev
				result = &ev
			}
		}
	}

	call := &domain.CallReport{}
	if len(tools) > 0 {
		call.Tools = tools
	}
	slices.Sort(mcpFail)
	call.MCPFailed = mcpFail
	switch {
	case result != nil:
		u := &domain.Usage{Turns: result.NumTurns, Limits: limits}
		if result.TotalCostUSD != nil {
			u.CostUSD = *result.TotalCostUSD
		}
		if result.Usage != nil {
			u.InputTokens = result.Usage.InputTokens
			u.OutputTokens = result.Usage.OutputTokens
			u.CacheReadTokens = result.Usage.CacheReadInputTokens
			u.CacheCreationTokens = result.Usage.CacheCreationInputTokens
		}
		call.APIMS = max(result.DurationAPIMS, 0)
		if len(result.ModelUsage) > 0 {
			for name, mu := range result.ModelUsage {
				call.Models = append(call.Models, domain.ModelUse{Model: name, CostUSD: mu.CostUSD})
			}
		} else {
			call.Models = modelList(models)
		}
		rep = streamReport{text: *result.Result, usage: u, isError: result.IsError, ok: true}
		if result.IsError {
			rep.apiStatus = apiErrorStatus(*result.Result)
		}
	case len(messages) > 0:
		u := &domain.Usage{Turns: len(messages), Limits: limits, Partial: true}
		for _, m := range messages {
			u.InputTokens += m.InputTokens
			u.OutputTokens += m.OutputTokens
			u.CacheReadTokens += m.CacheReadInputTokens
			u.CacheCreationTokens += m.CacheCreationInputTokens
		}
		call.Models = modelList(models)
		rep.usage = u
	default:
		call.Models = modelList(models)
	}
	slices.SortFunc(call.Models, func(a, b domain.ModelUse) int { return strings.Compare(a.Model, b.Model) })
	call.APIKeySource = boundedSource(source)
	if len(call.Models) > 0 || len(call.Tools) > 0 || call.APIMS > 0 || len(call.MCPFailed) > 0 || call.APIKeySource != "" {
		rep.call = call
	}
	rep.apiKeySource = call.APIKeySource
	if rep.usage != nil {
		rep.usage.APIKeySource = call.APIKeySource
	}
	rep.authErr = authErr
	rep.usageLimit, rep.limitResets = usageLimit, limitResets
	return rep
}

// limitResetsAt is the reset time in a usage_limit_reached error's params, zero when they
// do not carry one.
func limitResetsAt(params json.RawMessage) time.Time {
	var p struct {
		RateLimitInfo struct {
			ResetsAt int64 `json:"resetsAt"`
		} `json:"rate_limit_info"`
	}
	if json.Unmarshal(params, &p) != nil || p.RateLimitInfo.ResetsAt <= 0 {
		return time.Time{}
	}
	return time.Unix(p.RateLimitInfo.ResetsAt, 0)
}

// apiErrorStatus is the status of the CLI's "API Error: NNN ..." answer, which it prints
// when the API refused the request; 0 for any other text.
func apiErrorStatus(text string) int {
	rest, ok := strings.CutPrefix(strings.TrimSpace(text), "API Error: ")
	if !ok || len(rest) < 3 || (len(rest) > 3 && rest[3] != ' ') {
		return 0
	}
	status, err := strconv.Atoi(rest[:3])
	if err != nil || status < 100 {
		return 0
	}
	return status
}

// authErrorCategories are the system/api_retry error categories that mean the credential,
// not the service, is the problem (specs/claude-auth/SPEC.md §9). Rate limits and overload
// are not among them: the CLI retries those.
var authErrorCategories = map[string]bool{
	"authentication_failed": true,
	"oauth_org_not_allowed": true,
	"account_on_hold":       true,
	"billing_error":         true,
}

// authRetryCategory is the category of a system/api_retry event when it is an
// authentication one (authErrorCategories), "" for any other event.
func authRetryCategory(ev streamEvent) string {
	if ev.Type != "system" || ev.Subtype != "api_retry" {
		return ""
	}
	if cat := rawString(ev.Error); authErrorCategories[cat] {
		return cat
	}
	return ""
}

// maxWatchedLine bounds what authRetryWatcher buffers of one line. An api_retry event is a
// few hundred bytes, so a longer line is skipped, not buffered.
const maxWatchedLine = 64 << 10

// authRetryWatcher reads the live stdout and calls stop once, on the first auth-category
// api_retry. The CLI retries a refused credential up to ten times with backoff, for minutes
// (specs/permissions/DECISIONS.md D17), and no retry changes the outcome (SPEC §16 D4).
// os/exec writes stdout from one goroutine, so it needs no lock.
type authRetryWatcher struct {
	stop func()
	buf  []byte
	skip bool // the current line outgrew maxWatchedLine
	done bool
}

func (w *authRetryWatcher) Write(p []byte) (int, error) {
	n := len(p)
	for len(p) > 0 && !w.done {
		i := bytes.IndexByte(p, '\n')
		if i < 0 {
			if !w.skip {
				w.buf = append(w.buf, p...)
				if len(w.buf) > maxWatchedLine {
					w.buf, w.skip = w.buf[:0], true
				}
			}
			break
		}
		if !w.skip {
			line := bytes.TrimSpace(append(w.buf, p[:i]...))
			var ev streamEvent
			if bytes.Contains(line, []byte(`"api_retry"`)) && json.Unmarshal(line, &ev) == nil && authRetryCategory(ev) != "" {
				w.done = true
				w.stop()
			}
		}
		w.buf, w.skip, p = w.buf[:0], false, p[i+1:]
	}
	return n, nil
}

// rawString is the value of a JSON string, "" for anything else (absent, null, an object).
func rawString(raw json.RawMessage) string {
	var s string
	if json.Unmarshal(raw, &s) != nil {
		return ""
	}
	return s
}

// boundedSource keeps what the CLI said about its credential source printable and short,
// since it is recorded in run.json and read back under domain.CallReport.Validate.
func boundedSource(s string) string {
	s = strings.Map(func(r rune) rune {
		if !unicode.IsPrint(r) {
			return -1
		}
		return r
	}, s)
	if len(s) > 100 {
		s = s[:100]
	}
	return strings.ToValidUTF8(s, "")
}

func modelList(set map[string]bool) []domain.ModelUse {
	var out []domain.ModelUse
	for name := range set {
		out = append(out, domain.ModelUse{Model: name})
	}
	return out
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

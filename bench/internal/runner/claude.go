package runner

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"strings"
	"time"

	"orchbench/internal/config"
	"orchbench/internal/task"
)

// streamEvent holds the fields of a stream-json event that the runner reads. Only the final
// `result` event matters: the transcript before it is for the logs.
type streamEvent struct {
	Type         string   `json:"type"`
	Subtype      string   `json:"subtype"`
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
	// api_error "usage_limit_reached" is the account's usage limit; its params carry
	// rate_limit_info.resetsAt in Unix seconds (read as internal/provider/claude reads them).
	// Raw, so an unexpected shape drops the reset time and not the whole event.
	APIError       json.RawMessage `json:"api_error"`
	APIErrorParams json.RawMessage `json:"api_error_params"`
	// error is the category of an API error the CLI answered with (an assistant event) or is
	// retrying (system/api_retry); api_error_status is the HTTP status of a result's error.
	// Both are the CLI's own fields, never the model's text.
	Error          json.RawMessage `json:"error"`
	APIErrorStatus int             `json:"api_error_status"`
}

// authCategories are the error categories of a refused credential, as the core's claude
// adapter knows them (internal/provider/claude authErrorCategories).
var authCategories = map[string]bool{
	"authentication_failed": true,
	"oauth_org_not_allowed": true,
	"account_on_hold":       true,
	"billing_error":         true,
}

// authFailure is the category of a refused credential this event reports, "" for none.
func (ev *streamEvent) authFailure() string {
	var cat string
	if json.Unmarshal(ev.Error, &cat) == nil && authCategories[cat] {
		return cat
	}
	if ev.Type == "result" && ev.IsError && ev.APIErrorStatus == 401 {
		return "HTTP 401"
	}
	return ""
}

// limitResets is when the usage limit that refused this event resets, zero when it is not one
// or did not say.
func (ev *streamEvent) limitResets() time.Time {
	var kind string
	var params struct {
		RateLimitInfo struct {
			ResetsAt int64 `json:"resetsAt"`
		} `json:"rate_limit_info"`
	}
	if json.Unmarshal(ev.APIError, &kind) != nil || kind != "usage_limit_reached" ||
		json.Unmarshal(ev.APIErrorParams, &params) != nil || params.RateLimitInfo.ResetsAt <= 0 {
		return time.Time{}
	}
	return time.Unix(params.RateLimitInfo.ResetsAt, 0)
}

// claudeArgs is config A's invocation (SPEC.md §8). The prompt is not here: it goes on stdin.
// The tools flag is spelled as the core's adapter spells it (internal/provider/claude), which
// is verified against the CLI. It is variadic, so it stays last: nothing follows it to swallow.
func claudeArgs(model string, tools []string) []string {
	return []string{
		"-p",
		"--model", model,
		"--output-format", "stream-json",
		"--verbose",
		"--permission-mode", "acceptEdits",
		"--allowed-tools", strings.Join(tools, ","),
	}
}

// runClaude runs config A: `claude -p` with the task and the name of the test command on
// stdin, in the workspace, and reads cost, tokens and answer from the result event.
func runClaude(ctx context.Context, cfg config.Config, t task.Task, ws Workspace) (Outcome, error) {
	prompt, err := cfg.RenderPrompt(t)
	if err != nil {
		return Outcome{}, err
	}
	out := Outcome{
		ConfigSHA256: task.SHA256Bytes([]byte(strings.Join([]string{cfg.Model, strings.Join(cfg.Tools, ","), prompt}, "\x00"))),
		ExitCode:     -1,
	}

	bin, err := lookBin(ws.Bins.Claude, "claude")
	if err != nil {
		return out, err
	}

	runCtx, cancel := context.WithTimeout(ctx, t.Timeout.Std())
	defer cancel()

	var result *streamEvent
	var resets time.Time
	var authFailed string
	// plain keeps the lines that are not stream-json events: the CLI's own messages. Tool
	// output, test output among it, is always inside an event, so it never lands here.
	plain := &tailBuffer{max: tailBytes}
	events := &lineWriter{fn: func(line []byte) {
		if len(line) == 0 {
			return
		}
		if line[0] != '{' {
			plain.Write(line)
			plain.Write([]byte{'\n'})
			return
		}
		var ev streamEvent
		if json.Unmarshal(line, &ev) != nil {
			return
		}
		if t := ev.limitResets(); !t.IsZero() {
			resets = t
		}
		if cat := ev.authFailure(); cat != "" && authFailed == "" {
			authFailed = cat
		}
		if ev.Type == "result" {
			result = &ev
		}
	}}
	var stdout io.Writer = events
	if ws.Stdout != nil {
		stdout = io.MultiWriter(events, ws.Stdout)
	}

	proc, err := execute(runCtx, command{
		Bin:    bin,
		Args:   claudeArgs(cfg.Model, cfg.Tools),
		Dir:    ws.Dir,
		Env:    os.Environ(),
		Stdin:  strings.NewReader(prompt),
		Stdout: stdout,
		Stderr: ws.Stderr,
	})
	events.flush()
	out.ExitCode = proc.ExitCode
	if err != nil {
		return out, &InfraError{Reason: "running claude", Err: err}
	}
	if err := ctx.Err(); err != nil {
		return out, err
	}

	out.LLMCalls = 1
	if result != nil {
		if result.TotalCostUSD != nil {
			out.CostUSD = *result.TotalCostUSD
		}
		if u := result.Usage; u != nil {
			out.Tokens = Tokens{
				Input:         u.InputTokens,
				Output:        u.OutputTokens,
				CacheRead:     u.CacheReadInputTokens,
				CacheCreation: u.CacheCreationInputTokens,
			}
		}
		if t.Category == task.CategoryQuestion && result.Result != nil {
			out.Answer = *result.Result
		}
	}

	// A deadline that came after claude finished on its own is not a timeout.
	if proc.TimedOut && proc.ExitCode != 0 {
		out.Status, out.FailureReason, out.TimedOut = StatusFailed, ReasonTimeout, true
		return out, nil
	}

	// A rate limit is looked for only when the run failed, as for the orchestrator's exit 3: a
	// CLI that retried after a 429 and then finished has a real result. The texts are the
	// provider's own messages: stderr, stdout outside the events, and the result event's text
	// when it reports an error. The transcript is never searched.
	if result == nil || result.IsError || proc.ExitCode != 0 {
		if authFailed != "" {
			return Outcome{}, authError("claude", authFailed)
		}
		texts := []string{proc.Stderr, plain.String()}
		if result != nil && result.IsError && result.Result != nil {
			texts = append(texts, *result.Result)
		}
		if line, ok := rateLimited(texts...); ok || !resets.IsZero() {
			if !ok {
				line = "usage_limit_reached"
			}
			return Outcome{}, limitError("claude", line, texts, resets)
		}
	}
	if result == nil {
		return Outcome{}, infra("claude exited with code %d without a result event%s", proc.ExitCode, tailNote(proc.Stderr))
	}

	switch {
	case result.IsError:
		out.Status = StatusFailed
		out.FailureReason = result.Subtype
		if out.FailureReason == "" || out.FailureReason == "success" {
			out.FailureReason = "claude reported an error"
		}
	case proc.ExitCode != 0:
		out.Status, out.FailureReason = StatusFailed, fmt.Sprintf("claude exited with code %d", proc.ExitCode)
	default:
		out.Status = StatusCompleted
	}
	return out, nil
}

// tailNote is the end of a stream as a suffix for an error message, or nothing.
func tailNote(s string) string {
	s = strings.TrimSpace(s)
	if s == "" {
		return ""
	}
	if i := strings.LastIndex(s, "\n"); i >= 0 {
		s = s[i+1:]
	}
	return ": " + excerpt(s)
}

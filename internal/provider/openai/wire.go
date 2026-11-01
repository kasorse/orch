package openai

import (
	"encoding/json"
	"fmt"

	"orchestrator/internal/domain"
)

// The Responses API's wire (specs/api-transport/SPEC.md §4.2). Unexported: nothing outside this
// package knows the request or the stream.

type request struct {
	Model             string     `json:"model"`
	Instructions      string     `json:"instructions"`
	Input             []any      `json:"input"`
	Tools             []any      `json:"tools,omitempty"`
	ParallelToolCalls *bool      `json:"parallel_tool_calls,omitempty"`
	Include           []string   `json:"include,omitempty"`
	PromptCacheKey    string     `json:"prompt_cache_key,omitempty"`
	Reasoning         *reasoning `json:"reasoning,omitempty"`
	Store             bool       `json:"store"`
	Stream            bool       `json:"stream"`
}

// functionTool is a tool the client runs (internal/provider/tools), with strict schemas.
type functionTool struct {
	Type        string         `json:"type"`
	Name        string         `json:"name"`
	Description string         `json:"description"`
	Parameters  map[string]any `json:"parameters"`
	Strict      bool           `json:"strict"`
}

// webSearchTool is the server's web search (SPEC §5.1, web).
type webSearchTool struct {
	Type string `json:"type"`
}

// functionCall is a function_call output item; functionCallOutput answers it.
type functionCall struct {
	CallID    string `json:"call_id"`
	Name      string `json:"name"`
	Arguments string `json:"arguments"`
}

type functionCallOutput struct {
	Type   string `json:"type"`
	CallID string `json:"call_id"`
	Output string `json:"output"`
}

type reasoning struct {
	Effort string `json:"effort,omitempty"`
}

type inputMessage struct {
	Role    string      `json:"role"`
	Content []inputText `json:"content"`
}

type inputText struct {
	Type string `json:"type"`
	Text string `json:"text"`
}

// response is the Response object a terminal event carries. Output keeps each item as it came,
// so the tool loop can send it back unchanged.
type response struct {
	ID                string            `json:"id"`
	Model             string            `json:"model"`
	Status            string            `json:"status"`
	Output            []json.RawMessage `json:"output"`
	IncompleteDetails *struct {
		Reason string `json:"reason"`
	} `json:"incomplete_details"`
	Error *struct {
		Code    string `json:"code"`
		Message string `json:"message"`
	} `json:"error"`
	Usage *wireUsage `json:"usage"`
}

type wireUsage struct {
	InputTokens        int64 `json:"input_tokens"`
	InputTokensDetails struct {
		CachedTokens     int64 `json:"cached_tokens"`
		CacheWriteTokens int64 `json:"cache_write_tokens"`
	} `json:"input_tokens_details"`
	OutputTokens        int64 `json:"output_tokens"`
	OutputTokensDetails struct {
		ReasoningTokens int64 `json:"reasoning_tokens"`
	} `json:"output_tokens_details"`
}

// domain is SPEC §7's mapping: the cached and cache-write tokens are taken out of input, the
// reasoning tokens out of output, as the codex CLI adapter does.
func (u wireUsage) domain() domain.Usage {
	cache := u.InputTokensDetails.CachedTokens + u.InputTokensDetails.CacheWriteTokens
	return domain.Usage{
		InputTokens:         max(u.InputTokens-cache, 0),
		CacheReadTokens:     u.InputTokensDetails.CachedTokens,
		CacheCreationTokens: u.InputTokensDetails.CacheWriteTokens,
		OutputTokens:        max(u.OutputTokens-u.OutputTokensDetails.ReasoningTokens, 0),
		ReasoningTokens:     u.OutputTokensDetails.ReasoningTokens,
	}
}

// stream is what a call learns from its events: the terminal response, the model of
// response.created (for a call cut off before the end) and an error event.
type stream struct {
	// onItem, when set, is told every finished output item (response.output_item.done).
	onItem   func(json.RawMessage)
	created  *response
	final    *response
	errEvent *struct {
		Code    string `json:"code"`
		Message string `json:"message"`
	}
}

// apply folds one event in. Unknown event types are skipped (the API adds them); a known one
// with an unexpected shape is an error.
func (s *stream) apply(data []byte, onText func(string)) error {
	var head struct {
		Type string `json:"type"`
	}
	if err := json.Unmarshal(data, &head); err != nil {
		return fmt.Errorf("an event is not JSON: %w", err)
	}
	switch head.Type {
	case "response.created":
		var ev struct {
			Response *response `json:"response"`
		}
		if err := json.Unmarshal(data, &ev); err != nil || ev.Response == nil {
			return fmt.Errorf("%s without a response", head.Type)
		}
		s.created = ev.Response
	case "response.output_text.delta":
		var ev struct {
			Delta *string `json:"delta"`
		}
		if err := json.Unmarshal(data, &ev); err != nil || ev.Delta == nil {
			return fmt.Errorf("%s without a delta", head.Type)
		}
		if onText != nil {
			onText(*ev.Delta)
		}
	case "response.output_item.done":
		var ev struct {
			Item json.RawMessage `json:"item"`
		}
		if err := json.Unmarshal(data, &ev); err != nil || len(ev.Item) == 0 {
			return fmt.Errorf("%s without an item", head.Type)
		}
		if s.onItem != nil {
			s.onItem(ev.Item)
		}
	case "response.completed", "response.incomplete", "response.failed":
		var ev struct {
			Response *response `json:"response"`
		}
		if err := json.Unmarshal(data, &ev); err != nil || ev.Response == nil {
			return fmt.Errorf("%s without a response", head.Type)
		}
		if ev.Response.Status == "" {
			return fmt.Errorf("%s: the response has no status", head.Type)
		}
		s.final = ev.Response
	case "error":
		var ev struct {
			Code    string `json:"code"`
			Message string `json:"message"`
		}
		if err := json.Unmarshal(data, &ev); err != nil {
			return fmt.Errorf("an error event that is not JSON")
		}
		s.errEvent = &ev
	}
	return nil
}

// parseOutput reads a response's items: the answer (the output_text parts of the message items,
// joined), the function calls in order, and how many web searches the server ran.
func parseOutput(output []json.RawMessage) (string, []functionCall, int, error) {
	var out string
	var calls []functionCall
	searches := 0
	for i, raw := range output {
		var item struct {
			Type    string `json:"type"`
			Content []struct {
				Type string `json:"type"`
				Text string `json:"text"`
			} `json:"content"`
			functionCall
		}
		if err := json.Unmarshal(raw, &item); err != nil {
			return "", nil, 0, fmt.Errorf("output[%d] is not an item: %w", i, err)
		}
		switch item.Type {
		case "message":
			for _, c := range item.Content {
				if c.Type == "output_text" {
					out += c.Text
				}
			}
		case "function_call":
			if item.CallID == "" || item.Name == "" {
				return "", nil, 0, fmt.Errorf("output[%d] is a function_call without a call_id or a name", i)
			}
			calls = append(calls, item.functionCall)
		case "web_search_call":
			searches++
		}
	}
	return out, calls, searches, nil
}

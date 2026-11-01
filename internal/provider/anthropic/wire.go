package anthropic

import (
	"bytes"
	"encoding/json"
	"fmt"
	"strings"

	"orchestrator/internal/domain"
)

// The Messages API's wire (specs/api-transport/SPEC.md §4.1). Unexported: nothing outside this
// package knows the request or the stream.

type request struct {
	Model        string        `json:"model"`
	MaxTokens    int           `json:"max_tokens"`
	System       []textBlock   `json:"system,omitempty"`
	Messages     []message     `json:"messages"`
	OutputConfig *outputConfig `json:"output_config,omitempty"`
	Tools        []serverTool  `json:"tools,omitempty"`
	Stream       bool          `json:"stream"`
}

// serverTool is a tool the API runs itself (web search, web fetch).
type serverTool struct {
	Type    string `json:"type"`
	Name    string `json:"name"`
	MaxUses int    `json:"max_uses,omitempty"`
}

type textBlock struct {
	Type string `json:"type"`
	Text string `json:"text"`
}

// message holds content blocks as they go on the wire: the user's text, and later the
// assistant's blocks exactly as they came back (thinking blocks must be passed back unchanged).
type message struct {
	Role    string `json:"role"`
	Content []any  `json:"content"`
}

type outputConfig struct {
	Effort string `json:"effort,omitempty"`
}

type wireUsage struct {
	InputTokens   int64 `json:"input_tokens"`
	CacheCreation int64 `json:"cache_creation_input_tokens"`
	CacheRead     int64 `json:"cache_read_input_tokens"`
	OutputTokens  int64 `json:"output_tokens"`
}

// deltaUsage is message_delta's usage: only the fields it carries replace message_start's.
type deltaUsage struct {
	InputTokens   *int64 `json:"input_tokens"`
	CacheCreation *int64 `json:"cache_creation_input_tokens"`
	CacheRead     *int64 `json:"cache_read_input_tokens"`
	OutputTokens  *int64 `json:"output_tokens"`
}

type stopDetails struct {
	Category    string `json:"category"`
	Explanation string `json:"explanation"`
}

// turn assembles one streamed response: its content blocks as JSON objects (a block's start,
// with its deltas applied), the stop reason and the usage.
type turn struct {
	id, model   string
	blocks      []map[string]any
	partialJSON map[int]*strings.Builder
	stopReason  string
	stopDetails *stopDetails
	usage       wireUsage
	started     bool
	stopped     bool
	// streamErr is an error event the API sent inside the stream.
	streamErr *struct {
		Type    string `json:"type"`
		Message string `json:"message"`
	}
}

// observer is told the type of each block as it starts (onBlock), each block once it is
// complete (onDone) and each text delta (onText).
type observer struct {
	onBlock func(blockType string)
	onDone  func(block map[string]any)
	onText  func(string)
}

func decode(data []byte, v any) error {
	dec := json.NewDecoder(bytes.NewReader(data))
	dec.UseNumber()
	return dec.Decode(v)
}

// apply folds one event into the turn. Unknown event types are skipped (the API adds them); a
// known event with an unexpected shape is an error.
func (t *turn) apply(name string, data []byte, obs observer) error {
	var head struct {
		Type string `json:"type"`
	}
	if err := json.Unmarshal(data, &head); err != nil {
		return fmt.Errorf("event %q is not JSON: %w", name, err)
	}
	switch head.Type {
	case "message_start":
		var ev struct {
			Message *struct {
				ID    string    `json:"id"`
				Model string    `json:"model"`
				Usage wireUsage `json:"usage"`
			} `json:"message"`
		}
		if err := json.Unmarshal(data, &ev); err != nil || ev.Message == nil {
			return fmt.Errorf("message_start without a message")
		}
		t.started, t.id, t.model, t.usage = true, ev.Message.ID, ev.Message.Model, ev.Message.Usage
	case "content_block_start":
		var ev struct {
			Index *int           `json:"index"`
			Block map[string]any `json:"content_block"`
		}
		if err := decode(data, &ev); err != nil || ev.Index == nil || ev.Block == nil {
			return fmt.Errorf("content_block_start without an index or a block")
		}
		if *ev.Index != len(t.blocks) {
			return fmt.Errorf("content_block_start index %d, want %d", *ev.Index, len(t.blocks))
		}
		typ, _ := ev.Block["type"].(string)
		if typ == "" {
			return fmt.Errorf("content block %d has no type", *ev.Index)
		}
		t.blocks = append(t.blocks, ev.Block)
		if obs.onBlock != nil {
			obs.onBlock(typ)
		}
	case "content_block_delta":
		var ev struct {
			Index *int `json:"index"`
			Delta *struct {
				Type        string `json:"type"`
				Text        string `json:"text"`
				Thinking    string `json:"thinking"`
				Signature   string `json:"signature"`
				PartialJSON string `json:"partial_json"`
				Citation    any    `json:"citation"`
			} `json:"delta"`
		}
		if err := decode(data, &ev); err != nil || ev.Index == nil || ev.Delta == nil {
			return fmt.Errorf("content_block_delta without an index or a delta")
		}
		if *ev.Index < 0 || *ev.Index >= len(t.blocks) {
			return fmt.Errorf("content_block_delta for block %d, which has not started", *ev.Index)
		}
		b := t.blocks[*ev.Index]
		switch ev.Delta.Type {
		case "text_delta":
			b["text"] = str(b["text"]) + ev.Delta.Text
			if obs.onText != nil {
				obs.onText(ev.Delta.Text)
			}
		case "thinking_delta":
			b["thinking"] = str(b["thinking"]) + ev.Delta.Thinking
		case "signature_delta":
			b["signature"] = ev.Delta.Signature
		case "input_json_delta":
			if t.partialJSON == nil {
				t.partialJSON = map[int]*strings.Builder{}
			}
			if t.partialJSON[*ev.Index] == nil {
				t.partialJSON[*ev.Index] = &strings.Builder{}
			}
			t.partialJSON[*ev.Index].WriteString(ev.Delta.PartialJSON)
		case "citations_delta":
			cites, _ := b["citations"].([]any)
			b["citations"] = append(cites, ev.Delta.Citation)
		}
	case "content_block_stop":
		var ev struct {
			Index *int `json:"index"`
		}
		if err := json.Unmarshal(data, &ev); err != nil || ev.Index == nil || *ev.Index < 0 || *ev.Index >= len(t.blocks) {
			return fmt.Errorf("content_block_stop for a block that has not started")
		}
		if sb := t.partialJSON[*ev.Index]; sb != nil {
			raw := sb.String()
			if strings.TrimSpace(raw) == "" {
				raw = "{}"
			}
			var input map[string]any
			if err := decode([]byte(raw), &input); err != nil {
				return fmt.Errorf("the input of block %d is not a JSON object: %w", *ev.Index, err)
			}
			t.blocks[*ev.Index]["input"] = input
			delete(t.partialJSON, *ev.Index)
		}
		if obs.onDone != nil {
			obs.onDone(t.blocks[*ev.Index])
		}
	case "message_delta":
		var ev struct {
			Delta *struct {
				StopReason  string       `json:"stop_reason"`
				StopDetails *stopDetails `json:"stop_details"`
			} `json:"delta"`
			Usage *deltaUsage `json:"usage"`
		}
		if err := json.Unmarshal(data, &ev); err != nil || ev.Delta == nil {
			return fmt.Errorf("message_delta without a delta")
		}
		if ev.Delta.StopReason != "" {
			t.stopReason, t.stopDetails = ev.Delta.StopReason, ev.Delta.StopDetails
		}
		if u := ev.Usage; u != nil {
			for _, f := range []struct {
				from *int64
				to   *int64
			}{{u.InputTokens, &t.usage.InputTokens}, {u.CacheCreation, &t.usage.CacheCreation},
				{u.CacheRead, &t.usage.CacheRead}, {u.OutputTokens, &t.usage.OutputTokens}} {
				if f.from != nil {
					*f.to = *f.from
				}
			}
		}
	case "message_stop":
		t.stopped = true
	case "error":
		var ev struct {
			Error *struct {
				Type    string `json:"type"`
				Message string `json:"message"`
			} `json:"error"`
		}
		if err := json.Unmarshal(data, &ev); err != nil || ev.Error == nil {
			return fmt.Errorf("an error event without an error")
		}
		t.streamErr = ev.Error
	}
	return nil
}

func str(v any) string {
	s, _ := v.(string)
	return s
}

// text is the answer: the text blocks joined, as the CLI's result is.
func (t *turn) text() string {
	var parts []string
	for _, b := range t.blocks {
		if b["type"] == "text" {
			parts = append(parts, str(b["text"]))
		}
	}
	return strings.Join(parts, "")
}

func (t *turn) domainUsage() domain.Usage {
	return domain.Usage{
		InputTokens:         t.usage.InputTokens,
		CacheReadTokens:     t.usage.CacheRead,
		CacheCreationTokens: t.usage.CacheCreation,
		OutputTokens:        t.usage.OutputTokens,
	}
}

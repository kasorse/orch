package tools

import (
	"context"
	"encoding/json"
	"slices"
)

// Set is the tools one call offers, by name.
type Set map[string]Tool

// NewSet indexes tools by name.
func NewSet(tools []Tool) Set {
	s := Set{}
	for _, t := range tools {
		s[t.Name] = t
	}
	return s
}

// Call runs the model's call of name with input. A refusal, an invalid input and an unknown tool
// come back as the result's text with isErr true: they are the model's to read, never the call's
// failure (SPEC §4.1). The caller checks its context afterwards.
func (s Set) Call(ctx context.Context, name string, input json.RawMessage) (out string, isErr bool) {
	tool, ok := s[name]
	if !ok {
		return "there is no tool " + strconvQuote(name), true
	}
	if len(input) == 0 || string(input) == "null" {
		input = json.RawMessage("{}")
	}
	text, err := tool.Run(ctx, input)
	if err != nil {
		return err.Error(), true
	}
	return text, false
}

func strconvQuote(s string) string {
	b, _ := json.Marshal(s)
	return string(b)
}

// DisplayName is the name the call report and the console use for a tool, as the claude CLI
// names its own (Read, Grep, Glob); any other name is kept.
func DisplayName(name string) string {
	switch name {
	case "read_file":
		return "Read"
	case "grep":
		return "Grep"
	case "glob":
		return "Glob"
	}
	return name
}

// Label is what the console shows for a call: the display name and its path or pattern.
func Label(name string, input json.RawMessage) string {
	var in struct {
		Path    *string `json:"path"`
		Pattern *string `json:"pattern"`
	}
	label := DisplayName(name)
	if json.Unmarshal(input, &in) == nil {
		for _, arg := range []*string{in.Path, in.Pattern} {
			if arg != nil && *arg != "" {
				label += " " + *arg
			}
		}
	}
	return label
}

// MCPServerNames are the servers an MCP config document ({"mcpServers": {...}}) names, sorted;
// nil for none, and a single "mcp-config" for a document that is not one.
func MCPServerNames(doc string) []string {
	if doc == "" {
		return nil
	}
	var cfg struct {
		Servers map[string]json.RawMessage `json:"mcpServers"`
	}
	if json.Unmarshal([]byte(doc), &cfg) != nil {
		return []string{"mcp-config"}
	}
	names := make([]string, 0, len(cfg.Servers))
	for name := range cfg.Servers {
		names = append(names, name)
	}
	slices.Sort(names)
	return names
}

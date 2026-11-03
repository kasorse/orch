package tools

import (
	"context"
	"encoding/json"
)

// Set is the tools one call offers, by name, and the root apply_patch writes in when the call
// offers it (WithRoot).
type Set struct {
	tools map[string]Tool
	root  *Root
}

// NewSet indexes tools by name.
func NewSet(tools []Tool) *Set {
	s := &Set{tools: map[string]Tool{}}
	for _, t := range tools {
		s.tools[t.Name] = t
	}
	return s
}

// WithRoot is the set with apply_patch offered over root.
func (s *Set) WithRoot(root *Root) *Set {
	s.root = root
	return s
}

// Root is the root apply_patch writes in, nil when the call does not offer it.
func (s *Set) Root() *Root { return s.root }

// Call runs the model's call of name with input. A refusal, an invalid input and an unknown tool
// come back as the result's text with isErr true: they are the model's to read, never the call's
// failure (SPEC §4.1). The caller checks its context afterwards.
func (s *Set) Call(ctx context.Context, name string, input json.RawMessage) (out string, isErr bool) {
	tool, ok := s.tools[name]
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
	case SkillName:
		return "Skill"
	}
	return name
}

// Display is the name of one call for the call report: DisplayName, and for the editor its
// command's (Read for view, Write for create, Edit otherwise), for apply_patch its operation's.
func Display(name string, input json.RawMessage) string {
	if name == PatchName {
		var op PatchOp
		_ = json.Unmarshal(input, &op)
		return PatchDisplay(op)
	}
	if name != EditorName {
		return DisplayName(name)
	}
	var in struct {
		Command string `json:"command"`
	}
	_ = json.Unmarshal(input, &in)
	switch in.Command {
	case "view":
		return "Read"
	case "create":
		return "Write"
	}
	return "Edit"
}

// PatchDisplay is the name of an apply_patch operation: Write, Edit or Delete.
func PatchDisplay(op PatchOp) string {
	switch op.Type {
	case "create_file":
		return "Write"
	case "delete_file":
		return "Delete"
	}
	return "Edit"
}

// Label is what the console shows for a call: the display name and its path or pattern (a skill's
// name first).
func Label(name string, input json.RawMessage) string {
	var in struct {
		Name    *string `json:"name"`
		Path    *string `json:"path"`
		Pattern *string `json:"pattern"`
	}
	label := Display(name, input)
	if json.Unmarshal(input, &in) == nil {
		if name != SkillName {
			in.Name = nil
		}
		for _, arg := range []*string{in.Name, in.Path, in.Pattern} {
			if arg != nil && *arg != "" {
				label += " " + *arg
			}
		}
	}
	return label
}

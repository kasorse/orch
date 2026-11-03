package tools

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"strings"
)

// PatchOp is one operation of OpenAI's apply_patch tool: create_file and update_file carry a
// V4A diff, delete_file none.
type PatchOp struct {
	Type string `json:"type"`
	Path string `json:"path"`
	Diff string `json:"diff"`
}

// PatchName is apply_patch's name, also as a function tool (PatchTool).
const PatchName = "apply_patch"

// PatchTool is apply_patch as a function tool, for an endpoint without OpenAI's built-in one (a
// gateway to Bedrock drops it, specs/api-transport AT13): the same operation, run by ApplyPatch.
func (r *Root) PatchTool() Tool {
	return Tool{
		Name: PatchName,
		Description: "Create, update or delete one file of the repository; path is relative to its root. " +
			"create_file: diff is the new file's content, every line prefixed with +. " +
			"update_file: diff is a V4A diff: a line \"@@ <a line of the file just before the change>\" opens a " +
			"hunk, then its lines: a space for context kept, - for a line removed, + for a line added; give about " +
			"three lines of context around each change, and the hunks in file order. A hunk that does not match " +
			"changes nothing. delete_file: diff is null.",
		Schema: object(map[string]any{
			"type": map[string]any{"type": "string", "enum": []string{"create_file", "update_file", "delete_file"}, "description": "The operation"},
			"path": prop("string", "The file, relative to the repository root"),
			"diff": prop([]string{"string", "null"}, "The diff of create_file or update_file; null for delete_file"),
		}),
		Run: func(_ context.Context, input json.RawMessage) (string, error) {
			var in struct {
				Type string  `json:"type"`
				Path string  `json:"path"`
				Diff *string `json:"diff"`
			}
			if err := decode(input, &in); err != nil {
				return "", err
			}
			op := PatchOp{Type: in.Type, Path: in.Path}
			if in.Diff != nil {
				op.Diff = *in.Diff
			}
			return r.ApplyPatch(op)
		},
	}
}

// ApplyPatch applies one operation inside the root (SPEC §5.1, authority edit), all or nothing:
// the new text is computed in full before anything is written, and a hunk that does not match
// changes nothing. The error is the model's to read.
func (r *Root) ApplyPatch(op PatchOp) (string, error) {
	switch op.Type {
	case "create_file":
		content, err := createFromDiff(op.Diff)
		if err != nil {
			return "", refuse("%s: %v", op.Path, err)
		}
		return r.writeFile(op.Path, content, true)
	case "update_file":
		return r.edit(op.Path, func(text, rel string) (string, error) {
			out, err := applyV4A(text, op.Diff)
			if err != nil {
				return "", refuse("%s: %v; nothing was changed", rel, err)
			}
			return out, nil
		})
	case "delete_file":
		abs, rel, err := r.Resolve(op.Path, true)
		if err != nil {
			return "", err
		}
		info, err := os.Lstat(abs)
		switch {
		case errors.Is(err, fs.ErrNotExist):
			return "", refuse("%s: no such file", rel)
		case err != nil || !info.Mode().IsRegular():
			return "", refuse("%s: not a regular file", rel)
		}
		if err := os.Remove(abs); err != nil {
			return "", refuse("%s: the file cannot be deleted", rel)
		}
		return fmt.Sprintf("%s was deleted.\n", rel), nil
	default:
		return "", refuse("unknown operation %q (create_file, update_file, delete_file)", op.Type)
	}
}

// createFromDiff is a create_file diff's text: every line starts with "+". The file ends with a
// newline, as a source file does.
func createFromDiff(diff string) (string, error) {
	diff = strings.ReplaceAll(diff, "\r\n", "\n")
	lines := strings.Split(strings.TrimSuffix(diff, "\n"), "\n")
	if diff == "" {
		return "", nil
	}
	out := make([]string, 0, len(lines))
	for i, l := range lines {
		if !strings.HasPrefix(l, "+") {
			return "", fmt.Errorf("line %d of a create_file diff does not start with +", i+1)
		}
		out = append(out, l[1:])
	}
	return joinLines(out, true), nil
}

// hunk is one change of an update diff: its @@ anchors, the lines it expects (context and
// deleted), the lines it leaves (context and added), and whether it is pinned to the end.
type hunk struct {
	anchors  []string
	old, new []string
	eof      bool
}

// parseV4A reads an update_file diff: "@@" lines open a hunk (their text an anchor to find
// first, stacked anchors each in turn), then " " context, "-" deleted and "+" added lines; an
// empty line is empty context; "*** End of File" pins the hunk to the end; any other "***" line
// ends the diff.
func parseV4A(diff string) ([]hunk, error) {
	diff = strings.ReplaceAll(diff, "\r\n", "\n")
	var hunks []hunk
	var cur *hunk
	inBody := false
	for i, l := range strings.Split(strings.TrimSuffix(diff, "\n"), "\n") {
		switch {
		case l == "@@" || strings.HasPrefix(l, "@@ "):
			if cur == nil || inBody {
				hunks = append(hunks, hunk{})
				cur, inBody = &hunks[len(hunks)-1], false
			}
			if anchor := strings.TrimSpace(strings.TrimPrefix(l, "@@")); anchor != "" {
				cur.anchors = append(cur.anchors, anchor)
			}
			continue
		case l == "*** End of File":
			if cur == nil {
				return nil, fmt.Errorf("line %d: *** End of File outside a hunk", i+1)
			}
			cur.eof = true
			continue
		case strings.HasPrefix(l, "***"):
			return hunks, nil
		}
		if cur == nil {
			hunks = append(hunks, hunk{})
			cur = &hunks[len(hunks)-1]
		}
		inBody = true
		switch {
		case l == "":
			cur.old, cur.new = append(cur.old, ""), append(cur.new, "")
		case l[0] == ' ':
			cur.old, cur.new = append(cur.old, l[1:]), append(cur.new, l[1:])
		case l[0] == '-':
			cur.old = append(cur.old, l[1:])
		case l[0] == '+':
			cur.new = append(cur.new, l[1:])
		default:
			return nil, fmt.Errorf("line %d does not start with a space, - or + (or @@)", i+1)
		}
	}
	if len(hunks) == 0 {
		return nil, fmt.Errorf("the diff has no hunk")
	}
	return hunks, nil
}

// applyV4A applies an update diff to text. Hunks apply in order, each searched from where the
// previous one ended: its anchors first, then its old lines - exactly, then ignoring trailing
// space, then ignoring space at both ends. A pure addition needs an anchor or *** End of File.
func applyV4A(text, diff string) (string, error) {
	hunks, err := parseV4A(diff)
	if err != nil {
		return "", err
	}
	crlf := strings.Contains(text, "\r\n")
	text = strings.ReplaceAll(text, "\r\n", "\n")
	finalNewline := text == "" || strings.HasSuffix(text, "\n")
	lines := splitLines(text)
	cursor := 0
	for n, h := range hunks {
		for _, a := range h.anchors {
			at := find(lines, []string{a}, cursor, false)
			if at < 0 {
				return "", fmt.Errorf("hunk %d: the anchor %q was not found", n+1, a)
			}
			cursor = at + 1
		}
		var at int
		switch {
		case len(h.old) == 0 && h.eof:
			at = len(lines)
		case len(h.old) == 0 && len(h.anchors) > 0:
			at = cursor
		case len(h.old) == 0:
			return "", fmt.Errorf("hunk %d adds lines without context, an @@ anchor or *** End of File", n+1)
		default:
			if at = find(lines, h.old, cursor, h.eof); at < 0 {
				return "", fmt.Errorf("hunk %d: its context and deleted lines were not found", n+1)
			}
		}
		lines = append(append(append([]string{}, lines[:at]...), h.new...), lines[at+len(h.old):]...)
		cursor = at + len(h.new)
	}
	out := joinLines(lines, finalNewline)
	if crlf {
		out = strings.ReplaceAll(out, "\n", "\r\n")
	}
	return out, nil
}

// find is the first index at or after from where want matches lines (or, with eof, the match
// that ends the file), trying each comparison in turn from the strictest; -1 for none.
func find(lines, want []string, from int, eof bool) int {
	for _, same := range []func(a, b string) bool{
		func(a, b string) bool { return a == b },
		func(a, b string) bool { return strings.TrimRight(a, " \t") == strings.TrimRight(b, " \t") },
		func(a, b string) bool { return strings.TrimSpace(a) == strings.TrimSpace(b) },
	} {
		matches := func(at int) bool {
			for i, w := range want {
				if !same(lines[at+i], w) {
					return false
				}
			}
			return true
		}
		if eof {
			if at := len(lines) - len(want); at >= from && matches(at) {
				return at
			}
			continue
		}
		for at := from; at+len(want) <= len(lines); at++ {
			if matches(at) {
				return at
			}
		}
	}
	return -1
}

package tools

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
)

// EditorName is the name of Anthropic's text editor tool (text_editor_20250728); its schema is
// the model's own, so Tool.Schema is nil.
const EditorName = "str_replace_based_edit_tool"

// maxViewEntries bounds a directory listing of the editor's view.
const maxViewEntries = 500

// EditorTool is Anthropic's text editor over r (SPEC §5.1, authority edit): view, create,
// str_replace and insert; undo_edit is refused (the guard and git are the record of a change).
func EditorTool(r *Root) Tool {
	return Tool{Name: EditorName, Run: r.editor}
}

func (r *Root) editor(ctx context.Context, input json.RawMessage) (string, error) {
	var in struct {
		Command    string  `json:"command"`
		Path       string  `json:"path"`
		ViewRange  []int   `json:"view_range"`
		FileText   *string `json:"file_text"`
		OldStr     *string `json:"old_str"`
		NewStr     *string `json:"new_str"`
		InsertLine *int    `json:"insert_line"`
		InsertText *string `json:"insert_text"`
		MaxChars   *int    `json:"max_characters"`
	}
	if err := decode(input, &in); err != nil {
		return "", err
	}
	if strings.TrimSpace(in.Path) == "" {
		return "", refuse("path is required")
	}
	switch in.Command {
	case "view":
		return r.view(ctx, in.Path, in.ViewRange)
	case "create":
		if in.FileText == nil {
			return "", refuse("create needs file_text")
		}
		return r.writeFile(in.Path, *in.FileText, true)
	case "str_replace":
		if in.OldStr == nil || *in.OldStr == "" {
			return "", refuse("str_replace needs a non-empty old_str")
		}
		newStr := ""
		if in.NewStr != nil {
			newStr = *in.NewStr
		}
		return r.edit(in.Path, func(text, rel string) (string, error) {
			switch n := strings.Count(text, *in.OldStr); n {
			case 1:
				return strings.Replace(text, *in.OldStr, newStr, 1), nil
			case 0:
				return "", refuse("%s: old_str was not found; view the file and copy the text exactly", rel)
			default:
				return "", refuse("%s: old_str occurs %d times; add context so it occurs once", rel, n)
			}
		})
	case "insert":
		if in.InsertLine == nil || in.InsertText == nil {
			return "", refuse("insert needs insert_line and insert_text")
		}
		return r.edit(in.Path, func(text, rel string) (string, error) {
			lines := splitLines(text)
			if *in.InsertLine < 0 || *in.InsertLine > len(lines) {
				return "", refuse("%s has %d lines; insert_line must be 0..%d", rel, len(lines), len(lines))
			}
			ins := splitLines(*in.InsertText)
			out := append(append(append([]string{}, lines[:*in.InsertLine]...), ins...), lines[*in.InsertLine:]...)
			return joinLines(out, true), nil
		})
	case "undo_edit":
		return "", refuse("undo_edit is not available; make the change you want with str_replace")
	default:
		return "", refuse("unknown command %q (view, create, str_replace, insert)", in.Command)
	}
}

// view shows a file with numbered lines (view_range [start, end], end -1 for the end) or a
// directory two levels deep.
func (r *Root) view(ctx context.Context, p string, viewRange []int) (string, error) {
	abs, rel, err := r.Resolve(p, false)
	if err != nil {
		return "", err
	}
	if info, err := os.Stat(abs); err == nil && info.IsDir() {
		var entries []string
		depth := strings.Count(rel, "/")
		if rel == "." {
			depth = -1
		}
		err := r.walk(ctx, p, func(_, sub string) bool {
			if strings.Count(sub, "/")-depth <= 2 {
				entries = append(entries, sub)
			}
			return len(entries) < maxViewEntries
		})
		if err != nil {
			return "", err
		}
		if len(entries) == 0 {
			return rel + ": no readable files\n", nil
		}
		return bound(strings.Join(entries, "\n") + "\n"), nil
	}
	text, err := readText(abs, rel)
	if err != nil {
		return "", err
	}
	lines := splitLines(text)
	start, end := 1, len(lines)
	if len(viewRange) != 0 {
		if len(viewRange) != 2 {
			return "", refuse("view_range is [start, end]")
		}
		start, end = viewRange[0], viewRange[1]
		if end == -1 {
			end = len(lines)
		}
		if start < 1 || end < start || end > len(lines) {
			return "", refuse("%s has %d lines; view_range %v is outside it", rel, len(lines), viewRange)
		}
	}
	var b strings.Builder
	for i := start - 1; i < end; i++ {
		fmt.Fprintf(&b, "%6d\t%s\n", i+1, lines[i])
	}
	if len(lines) == 0 {
		return "(empty file)\n", nil
	}
	return bound(b.String()), nil
}

// edit reads a text file, changes it with change and writes it back: all or nothing.
func (r *Root) edit(p string, change func(text, rel string) (string, error)) (string, error) {
	abs, rel, err := r.Resolve(p, true)
	if err != nil {
		return "", err
	}
	text, err := readText(abs, rel)
	if err != nil {
		return "", err
	}
	out, err := change(text, rel)
	if err != nil {
		return "", err
	}
	if err := writeAt(abs, out); err != nil {
		return "", refuse("%s: the file cannot be written", rel)
	}
	return fmt.Sprintf("%s was edited.\n", rel), nil
}

// writeFile creates or replaces a file (SPEC §5 items 2-5: through Resolve forWrite, bounded).
func (r *Root) writeFile(p, content string, allowReplace bool) (string, error) {
	abs, rel, err := r.Resolve(p, true)
	if err != nil {
		return "", err
	}
	if rel == "." {
		return "", refuse("a file path is required")
	}
	if len(content) > maxFileBytes {
		return "", refuse("%s: %d bytes is over the %d-byte limit of a file", rel, len(content), maxFileBytes)
	}
	info, err := os.Lstat(abs)
	switch {
	case err == nil && info.IsDir():
		return "", refuse("%s is a directory", rel)
	case err == nil && !allowReplace:
		return "", refuse("%s exists", rel)
	case err == nil && !info.Mode().IsRegular():
		return "", refuse("%s: not a regular file", rel)
	case err != nil && !errors.Is(err, fs.ErrNotExist):
		return "", refuse("%s: the file cannot be written", rel)
	}
	if err := os.MkdirAll(filepath.Dir(abs), 0o755); err != nil {
		return "", refuse("%s: the directory cannot be created", rel)
	}
	if err := writeAt(abs, content); err != nil {
		return "", refuse("%s: the file cannot be written", rel)
	}
	return fmt.Sprintf("%s was written.\n", rel), nil
}

// writeAt writes content, keeping an existing file's permissions.
func writeAt(abs, content string) error {
	if len(content) > maxFileBytes {
		return fmt.Errorf("over the limit")
	}
	mode := fs.FileMode(0o644)
	if info, err := os.Stat(abs); err == nil {
		mode = info.Mode().Perm()
	}
	return os.WriteFile(abs, []byte(content), mode)
}

// splitLines splits text into lines without their newlines; a final newline ends the last line.
func splitLines(text string) []string {
	if text == "" {
		return nil
	}
	return strings.Split(strings.TrimSuffix(text, "\n"), "\n")
}

func joinLines(lines []string, finalNewline bool) string {
	if len(lines) == 0 {
		return ""
	}
	out := strings.Join(lines, "\n")
	if finalNewline {
		out += "\n"
	}
	return out
}

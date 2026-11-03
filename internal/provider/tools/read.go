package tools

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io/fs"
	"os"
	"path"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
)

// Tool is one tool a call offers: its definition for the model and the Go function that runs
// it. Run's error is the model's to read (an is_error tool result), never the call's failure.
type Tool struct {
	Name        string
	Description string
	// Schema is the JSON schema of the input: an object with additionalProperties false and every
	// property required (an optional one is nullable), as strict tool use on both APIs wants.
	Schema map[string]any
	Run    func(ctx context.Context, input json.RawMessage) (string, error)
}

// defaultReadLines is how many lines read_file returns without a limit.
const defaultReadLines = 2000

// ReadTools are read_file, grep and glob over r (SPEC §5.1, authority read), sorted by name.
func ReadTools(r *Root) []Tool {
	return []Tool{r.globTool(), r.grepTool(), r.readFileTool()}
}

func object(props map[string]any) map[string]any {
	required := make([]string, 0, len(props))
	for name := range props {
		required = append(required, name)
	}
	slices.Sort(required)
	return map[string]any{"type": "object", "properties": props, "required": required, "additionalProperties": false}
}

func prop(typ any, desc string) map[string]any {
	return map[string]any{"type": typ, "description": desc}
}

// decode reads a tool input strictly: unknown fields and wrong types are the model's error.
func decode(input json.RawMessage, v any) error {
	dec := json.NewDecoder(bytes.NewReader(input))
	dec.DisallowUnknownFields()
	if err := dec.Decode(v); err != nil {
		return refuse("invalid input: %v", err)
	}
	return nil
}

func (r *Root) readFileTool() Tool {
	return Tool{
		Name: "read_file",
		Description: "Read a text file of the repository. Lines come numbered (number, tab, text). " +
			"path is relative to the repository root. offset is the first line to read (1-based, null for 1); " +
			"limit is how many lines (null for 2000).",
		Schema: object(map[string]any{
			"path":   prop("string", "File path relative to the repository root"),
			"offset": prop([]string{"integer", "null"}, "First line, 1-based"),
			"limit":  prop([]string{"integer", "null"}, "Number of lines"),
		}),
		Run: func(_ context.Context, input json.RawMessage) (string, error) {
			var in struct {
				Path   string `json:"path"`
				Offset *int   `json:"offset"`
				Limit  *int   `json:"limit"`
			}
			if err := decode(input, &in); err != nil {
				return "", err
			}
			if strings.TrimSpace(in.Path) == "" {
				return "", refuse("path is required")
			}
			abs, rel, err := r.Resolve(in.Path, false)
			if err != nil {
				return "", err
			}
			text, err := readText(abs, rel)
			if err != nil {
				return "", err
			}
			offset, limit := 1, defaultReadLines
			if in.Offset != nil {
				offset = *in.Offset
			}
			if in.Limit != nil {
				limit = *in.Limit
			}
			if offset < 1 || limit < 1 {
				return "", refuse("offset and limit must be positive")
			}
			lines := strings.SplitAfter(text, "\n")
			if lines[len(lines)-1] == "" {
				lines = lines[:len(lines)-1]
			}
			if len(lines) == 0 {
				return "(empty file)\n", nil
			}
			if offset > len(lines) {
				return "", refuse("%s has %d lines; offset %d is past the end", rel, len(lines), offset)
			}
			var b strings.Builder
			end := min(offset-1+limit, len(lines))
			for i := offset - 1; i < end; i++ {
				fmt.Fprintf(&b, "%6d\t%s", i+1, strings.TrimSuffix(lines[i], "\n"))
				b.WriteByte('\n')
			}
			if end < len(lines) {
				fmt.Fprintf(&b, "[%d more lines; read on with offset %d]\n", len(lines)-end, end+1)
			}
			return bound(b.String()), nil
		},
	}
}

func (r *Root) grepTool() Tool {
	return Tool{
		Name: "grep",
		Description: "Search file contents with an RE2 regular expression. Returns path:line:text for each matching line. " +
			"path narrows the search to a directory or a file (null for the whole repository); glob filters file names, " +
			"e.g. *.go (null for all files). Binary and large files are skipped.",
		Schema: object(map[string]any{
			"pattern": prop("string", "RE2 regular expression"),
			"path":    prop([]string{"string", "null"}, "Directory or file relative to the repository root"),
			"glob":    prop([]string{"string", "null"}, "File-name glob, e.g. *.go"),
		}),
		Run: func(ctx context.Context, input json.RawMessage) (string, error) {
			var in struct {
				Pattern string  `json:"pattern"`
				Path    *string `json:"path"`
				Glob    *string `json:"glob"`
			}
			if err := decode(input, &in); err != nil {
				return "", err
			}
			re, err := regexp.Compile(in.Pattern)
			if err != nil || in.Pattern == "" {
				return "", refuse("pattern is not an RE2 regular expression: %v", err)
			}
			var nameGlob string
			if in.Glob != nil {
				nameGlob = *in.Glob
				if _, err := path.Match(nameGlob, ""); err != nil {
					return "", refuse("glob %q is malformed", nameGlob)
				}
			}
			start := ""
			if in.Path != nil {
				start = *in.Path
			}
			var b strings.Builder
			matches := 0
			err = r.walk(ctx, start, func(abs, rel string) bool {
				if nameGlob != "" {
					if ok, _ := path.Match(nameGlob, path.Base(rel)); !ok {
						return true
					}
				}
				text, err := readText(abs, rel)
				if err != nil {
					return true
				}
				sc := bufio.NewScanner(strings.NewReader(text))
				sc.Buffer(make([]byte, 0, 64<<10), maxFileBytes+1)
				for n := 1; sc.Scan(); n++ {
					line := sc.Text()
					if !re.MatchString(line) {
						continue
					}
					if len(line) > maxLineBytes {
						line = strings.ToValidUTF8(line[:maxLineBytes], "") + "…"
					}
					fmt.Fprintf(&b, "%s:%d:%s\n", rel, n, line)
					if matches++; matches == maxMatches {
						fmt.Fprintf(&b, "[stopped at %d matches; narrow the pattern or the path]\n", maxMatches)
						return false
					}
				}
				return true
			})
			if err != nil {
				return "", err
			}
			if matches == 0 {
				return "no matches\n", nil
			}
			return bound(b.String()), nil
		},
	}
}

func (r *Root) globTool() Tool {
	return Tool{
		Name: "glob",
		Description: "List repository files whose path matches a glob: * and ? within a path segment, ** for any number of " +
			"directories, e.g. internal/**/*_test.go. Paths are relative to the repository root, sorted.",
		Schema: object(map[string]any{
			"pattern": prop("string", "Glob over paths relative to the repository root"),
		}),
		Run: func(ctx context.Context, input json.RawMessage) (string, error) {
			var in struct {
				Pattern string `json:"pattern"`
			}
			if err := decode(input, &in); err != nil {
				return "", err
			}
			pattern := strings.Trim(filepath.ToSlash(strings.TrimSpace(in.Pattern)), "/")
			if pattern == "" || filepath.IsAbs(in.Pattern) || slices.Contains(strings.Split(pattern, "/"), "..") {
				return "", refuse("pattern must be a glob relative to the repository root")
			}
			segs := strings.Split(pattern, "/")
			for _, s := range segs {
				if _, err := path.Match(s, ""); err != nil {
					return "", refuse("pattern %q is malformed", in.Pattern)
				}
			}
			var found []string
			err := r.walk(ctx, "", func(_, rel string) bool {
				if globMatch(segs, strings.Split(rel, "/")) {
					found = append(found, rel)
				}
				return len(found) < maxMatches
			})
			if err != nil {
				return "", err
			}
			if len(found) == 0 {
				return "no files match\n", nil
			}
			slices.Sort(found)
			out := strings.Join(found, "\n") + "\n"
			if len(found) == maxMatches {
				out += fmt.Sprintf("[stopped at %d files; narrow the pattern]\n", maxMatches)
			}
			return bound(out), nil
		},
	}
}

// globMatch matches path segments against pattern segments, where "**" stands for any number
// of segments (none included).
func globMatch(pattern, segs []string) bool {
	if len(pattern) == 0 {
		return len(segs) == 0
	}
	if pattern[0] == "**" {
		for i := 0; i <= len(segs); i++ {
			if globMatch(pattern[1:], segs[i:]) {
				return true
			}
		}
		return false
	}
	if len(segs) == 0 {
		return false
	}
	ok, _ := path.Match(pattern[0], segs[0])
	return ok && globMatch(pattern[1:], segs[1:])
}

// walk calls visit for every regular file under start (a directory or a file the model gave,
// "" for the root) that the call may read, in lexical order: it never follows a symlink, never
// enters .git or a denied directory, and skips denied files. visit returns false to stop.
func (r *Root) walk(ctx context.Context, start string, visit func(abs, rel string) bool) error {
	abs, rel, err := r.Resolve(start, false)
	if err != nil {
		return err
	}
	info, err := os.Lstat(abs)
	if err != nil {
		return refuse("%s: no such file or directory", rel)
	}
	if !info.IsDir() {
		if info.Mode().IsRegular() {
			visit(abs, rel)
		}
		return nil
	}
	stop := fmt.Errorf("stop")
	err = filepath.WalkDir(abs, func(p string, d fs.DirEntry, err error) error {
		if ctx.Err() != nil {
			return ctx.Err()
		}
		if err != nil {
			return nil // an unreadable entry is skipped, not the walk's failure
		}
		sub, _ := filepath.Rel(r.dir, p)
		sub = filepath.ToSlash(sub)
		switch {
		case d.IsDir():
			if r.skipDir(sub) {
				return filepath.SkipDir
			}
			return nil
		case !d.Type().IsRegular(), r.check(sub, false) != nil:
			return nil
		}
		if !visit(p, sub) {
			return stop
		}
		return nil
	})
	if err == stop {
		return nil
	}
	return err
}

package repository

import (
	"bytes"
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
)

// AddedLine is one line the working tree adds, with its number in the new file.
type AddedLine struct {
	Number int
	Text   string
}

// AddedFile is what the working tree adds to one file against HEAD: the added lines of a
// tracked file, or every line of an untracked one. Skipped marks a file that was not read
// (binary, larger than maxAddedRead, or not a regular file).
type AddedFile struct {
	Path    string
	Lines   []AddedLine
	Skipped bool
}

// maxAddedRead is the largest untracked file AddedContent reads.
const maxAddedRead = 1 << 20

// AddedContent returns what the working tree adds against HEAD, uncut, for the secret scan
// (specs/hardening/SPEC.md §9.3): the "+" lines of tracked files and the content of
// untracked files that are not ignored. Ignored files are left out - they cannot be
// committed - and so is exclude (the runs directory). Unlike ReviewDiff it applies no deny
// list: nothing it returns leaves the machine.
func (r *Repository) AddedContent(ctx context.Context, exclude []string) ([]AddedFile, error) {
	pathspec := []string{"--", "."}
	for _, p := range exclude {
		pathspec = append(pathspec, ":(exclude)"+p)
	}
	patch, err := r.git(ctx, append([]string{"diff", "HEAD", "-U0", "--no-color", "--no-ext-diff", "--no-textconv"}, pathspec...)...)
	if err != nil {
		return nil, err
	}
	files, err := parseAddedLines(patch)
	if err != nil {
		return nil, err
	}

	others, err := r.git(ctx, append([]string{"ls-files", "--others", "--exclude-standard", "-z"}, pathspec...)...)
	if err != nil {
		return nil, err
	}
	for _, p := range strings.Split(others, "\x00") {
		if p != "" {
			files = append(files, r.untrackedAdded(p))
		}
	}
	return files, nil
}

func (r *Repository) untrackedAdded(rel string) AddedFile {
	f := AddedFile{Path: rel}
	full := filepath.Join(r.Dir, filepath.FromSlash(rel))
	info, err := os.Lstat(full)
	if err != nil || !info.Mode().IsRegular() || info.Size() > maxAddedRead {
		f.Skipped = true
		return f
	}
	data, err := os.ReadFile(full)
	if err != nil || bytes.IndexByte(data[:min(len(data), 8<<10)], 0) >= 0 {
		f.Skipped = true
		return f
	}
	for i, line := range strings.Split(string(data), "\n") {
		f.Lines = append(f.Lines, AddedLine{Number: i + 1, Text: line})
	}
	return f
}

// parseAddedLines reads "git diff -U0": a file starts at "diff --git", its new path is on
// "+++ b/<path>" (quoted by git when it has special characters; "/dev/null" for a deleted
// file), and after each "@@ -a,b +c,d @@" hunk header the "+" lines are new lines c, c+1, ...
func parseAddedLines(patch string) ([]AddedFile, error) {
	var files []AddedFile
	var cur *AddedFile
	inHeader := false
	next := 0
	for _, line := range strings.Split(patch, "\n") {
		switch {
		case strings.HasPrefix(line, "diff --git "):
			files = append(files, AddedFile{})
			cur = &files[len(files)-1]
			inHeader = true
		case cur == nil:
			continue
		case inHeader && strings.HasPrefix(line, "+++ "):
			p, err := diffPath(strings.TrimPrefix(line, "+++ "))
			if err != nil {
				return nil, err
			}
			cur.Path = p
		case strings.HasPrefix(line, "@@ "):
			inHeader = false
			n, err := hunkNewStart(line)
			if err != nil {
				return nil, err
			}
			next = n
		case inHeader:
			if strings.HasPrefix(line, "Binary files ") {
				cur.Skipped = true
			}
		case strings.HasPrefix(line, "+"):
			cur.Lines = append(cur.Lines, AddedLine{Number: next, Text: line[1:]})
			next++
		}
	}
	// A deleted file has no new path and adds nothing.
	out := files[:0]
	for _, f := range files {
		if f.Path != "" {
			out = append(out, f)
		}
	}
	return out, nil
}

// diffPath turns git's "b/<path>" (or a C-quoted "\"b/<path>\"") into the path, or "" for
// /dev/null.
func diffPath(s string) (string, error) {
	// git ends the name with a tab when it contains a space (GNU patch's convention); a
	// name that really ends in a tab is quoted instead.
	s = strings.TrimSuffix(s, "\t")
	if s == "/dev/null" {
		return "", nil
	}
	if strings.HasPrefix(s, `"`) {
		unquoted, err := strconv.Unquote(s)
		if err != nil {
			return "", fmt.Errorf("unexpected quoted path in git diff: %s", s)
		}
		s = unquoted
	}
	p, ok := strings.CutPrefix(s, "b/")
	if !ok {
		return "", fmt.Errorf("unexpected new path in git diff: %q", s)
	}
	return p, nil
}

// hunkNewStart reads c from "@@ -a,b +c,d @@ ...".
func hunkNewStart(header string) (int, error) {
	fields := strings.Fields(header)
	if len(fields) < 3 || !strings.HasPrefix(fields[2], "+") {
		return 0, fmt.Errorf("unexpected git hunk header %q", header)
	}
	start, _, _ := strings.Cut(strings.TrimPrefix(fields[2], "+"), ",")
	n, err := strconv.Atoi(start)
	if err != nil {
		return 0, fmt.Errorf("unexpected git hunk header %q", header)
	}
	return n, nil
}

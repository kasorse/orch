package repository

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"os"
	"path"
	"path/filepath"
	"strconv"
	"strings"
)

// Treatment is how one changed file appears in a ReviewDiff (specs/hardening/SPEC.md §5.3).
type Treatment string

const (
	// TreatFull: the file's whole diff is in the text.
	TreatFull Treatment = "full"
	// TreatSummarized: a stat line only - a lock file or generated file.
	TreatSummarized Treatment = "summarized"
	// TreatWithheld: the name only - the file matches the secret deny list.
	TreatWithheld Treatment = "withheld"
	// TreatBinary: the name only - binary content.
	TreatBinary Treatment = "binary"
	// TreatSymlink: the name and the link target - a new symlink.
	TreatSymlink Treatment = "symlink"
	// TreatBudget: a stat line only - its diff did not fit the budget.
	TreatBudget Treatment = "budget"
)

// DefaultMaxUntracked caps how many untracked files a ReviewDiff lists.
const DefaultMaxUntracked = 500

// ReviewDiffOptions configures ReviewDiff. Every field is optional except MaxBytes.
type ReviewDiffOptions struct {
	// Exclude are repository-relative paths left out entirely (the runs directory).
	Exclude []string
	// Denied reports whether a repository-relative path matches the secret deny list; its
	// content is withheld, tracked or not. The repository package does not know the list.
	Denied func(rel string) bool
	// Summarize are path.Match globs shown as a stat line only: matched against the base
	// name when the glob has no "/", otherwise against the repository-relative path.
	Summarize []string
	// MaxBytes bounds the whole text. Half of it at most goes to the file list.
	MaxBytes int
	// MaxUntracked caps the untracked files listed (DefaultMaxUntracked when 0).
	MaxUntracked int
	// Base is the commit the working tree is diffed against: a full SHA, or HEAD when empty. A
	// review run passes the merge base of the change it reviews (specs/delivery/SPEC.md §12.2).
	Base string
}

// FileChange is one changed file of a ReviewDiff. Added is -1 when it was not counted (an
// untracked file too large to read).
type FileChange struct {
	Path      string
	Added     int
	Deleted   int
	Untracked bool
	Treatment Treatment
	// Target is a new symlink's target.
	Target string
}

// ReviewDiff is the change set as the review step sees it: Text starts with every file
// and its treatment, then the full diffs that fit.
type ReviewDiff struct {
	Text  string
	Files []FileChange
	// Unlisted counts the files left out of the list, beyond MaxUntracked or the list's
	// half of MaxBytes. They are part of the change but neither listed nor shown.
	Unlisted  int
	Truncated bool
}

// Empty reports whether there is no change at all.
func (d ReviewDiff) Empty() bool { return len(d.Files) == 0 && d.Unlisted == 0 }

// Count returns how many files got treatment t.
func (d ReviewDiff) Count(t Treatment) int {
	n := 0
	for _, f := range d.Files {
		if f.Treatment == t {
			n++
		}
	}
	return n
}

// maxUntrackedRead is the largest untracked file whose lines are counted and whose content
// can be shown; anything bigger is listed with its size.
const maxUntrackedRead = 16 << 20

// ReviewDiff returns the working tree's change against HEAD (or opts.Base) for the review prompt: tracked
// changes from git, and untracked files that are not ignored with their content, so a file
// implement created is reviewed by what it says, not only by its name. A file on the deny
// list is named but never read into the text; a binary file and a new symlink are named;
// a summarize glob gives a stat line. Full diffs follow the file list in git's order while
// they fit opts.MaxBytes; one that does not fit becomes a stat line and later, smaller ones
// still get their chance.
//
// The diff commands run without external diff drivers or textconv filters, so what the
// reviewer sees is the bytes on disk, and nothing configured in the repository runs.
func (r *Repository) ReviewDiff(ctx context.Context, opts ReviewDiffOptions) (ReviewDiff, error) {
	pathspec := []string{"--", "."}
	for _, p := range opts.Exclude {
		pathspec = append(pathspec, ":(exclude)"+p)
	}
	base := "HEAD"
	if opts.Base != "" {
		if !isFullSHA(opts.Base) {
			return ReviewDiff{}, fmt.Errorf("the review base %q is not a full commit SHA", opts.Base)
		}
		base = opts.Base
	}
	diffArgs := []string{"diff", base, "--no-color", "--no-ext-diff", "--no-textconv"}

	numstat, err := r.git(ctx, append(append(append([]string(nil), diffArgs...), "--numstat", "-z"), pathspec...)...)
	if err != nil {
		return ReviewDiff{}, err
	}
	tracked, err := parseNumstat(numstat)
	if err != nil {
		return ReviewDiff{}, err
	}
	var chunks []string
	if len(tracked) > 0 {
		patch, err := r.git(ctx, append(append([]string(nil), diffArgs...), pathspec...)...)
		if err != nil {
			return ReviewDiff{}, err
		}
		chunks = splitPatch(patch)
		if len(chunks) != len(tracked) {
			return ReviewDiff{}, fmt.Errorf("git diff listed %d files but produced %d patches", len(tracked), len(chunks))
		}
	}

	others, err := r.git(ctx, append([]string{"ls-files", "--others", "--exclude-standard", "-z"}, pathspec...)...)
	if err != nil {
		return ReviewDiff{}, err
	}
	var untrackedPaths []string
	for _, p := range strings.Split(others, "\x00") {
		if p != "" {
			untrackedPaths = append(untrackedPaths, p)
		}
	}
	maxUntracked := opts.MaxUntracked
	if maxUntracked <= 0 {
		maxUntracked = DefaultMaxUntracked
	}
	unlisted := 0
	if len(untrackedPaths) > maxUntracked {
		unlisted = len(untrackedPaths) - maxUntracked
		untrackedPaths = untrackedPaths[:maxUntracked]
	}

	files := make([]FileChange, 0, len(tracked)+len(untrackedPaths))
	bodies := make([]string, 0, cap(files))
	for i, f := range tracked {
		// git's numstat marks binary content with "-" counts, kept as Added -1.
		f.Treatment = classify(f.Path, f.Added < 0, opts)
		files = append(files, f)
		bodies = append(bodies, chunks[i])
	}
	for _, p := range untrackedPaths {
		f, body := r.untrackedChange(p, opts)
		files = append(files, f)
		bodies = append(bodies, body)
	}

	return assemble(files, bodies, unlisted, opts.MaxBytes), nil
}

// classify picks a file's treatment by precedence: deny list, binary, summarize, full.
func classify(rel string, binary bool, opts ReviewDiffOptions) Treatment {
	switch {
	case opts.Denied != nil && opts.Denied(rel):
		return TreatWithheld
	case binary:
		return TreatBinary
	case summarized(rel, opts.Summarize):
		return TreatSummarized
	}
	return TreatFull
}

func summarized(rel string, globs []string) bool {
	for _, g := range globs {
		name := rel
		if !strings.Contains(g, "/") {
			name = path.Base(rel)
		}
		if ok, _ := path.Match(g, name); ok {
			return true
		}
	}
	return false
}

// untrackedChange reads one untracked file and renders its diff the way "git diff
// --no-index /dev/null <path>" would. A deny-listed file is never opened.
func (r *Repository) untrackedChange(rel string, opts ReviewDiffOptions) (FileChange, string) {
	f := FileChange{Path: rel, Untracked: true, Added: -1}
	if opts.Denied != nil && opts.Denied(rel) {
		f.Treatment = TreatWithheld
		return f, ""
	}
	full := filepath.Join(r.Dir, filepath.FromSlash(rel))
	info, err := os.Lstat(full)
	switch {
	case err != nil:
		// Gone since git listed it: name it, show nothing.
		f.Treatment = TreatBinary
		return f, ""
	case info.Mode()&os.ModeSymlink != 0:
		f.Treatment = TreatSymlink
		f.Target, _ = os.Readlink(full)
		return f, ""
	case !info.Mode().IsRegular():
		f.Treatment = TreatBinary
		return f, ""
	case info.Size() > maxUntrackedRead:
		f.Treatment = classify(rel, sniffBinary(full), opts)
		if f.Treatment == TreatFull {
			f.Treatment = TreatBudget
		}
		return f, ""
	}

	data, err := os.ReadFile(full)
	if err != nil {
		f.Treatment = TreatBinary
		return f, ""
	}
	binary := bytes.IndexByte(data[:min(len(data), 8<<10)], 0) >= 0
	f.Treatment = classify(rel, binary, opts)
	if binary {
		return f, ""
	}
	f.Added = countLines(data)
	if f.Treatment != TreatFull {
		return f, ""
	}
	return f, newFilePatch(rel, info.Mode(), data, f.Added)
}

// sniffBinary reports a NUL byte in the first 8 KiB of a file.
func sniffBinary(full string) bool {
	fh, err := os.Open(full)
	if err != nil {
		return true
	}
	defer fh.Close()
	buf := make([]byte, 8<<10)
	n, _ := io.ReadFull(fh, buf)
	return bytes.IndexByte(buf[:n], 0) >= 0
}

func countLines(data []byte) int {
	n := bytes.Count(data, []byte("\n"))
	if len(data) > 0 && data[len(data)-1] != '\n' {
		n++
	}
	return n
}

func newFilePatch(rel string, mode os.FileMode, data []byte, lines int) string {
	fileMode := "100644"
	if mode&0o111 != 0 {
		fileMode = "100755"
	}
	var b strings.Builder
	fmt.Fprintf(&b, "diff --git a/%s b/%s\nnew file mode %s\n", rel, rel, fileMode)
	if lines == 0 {
		return b.String()
	}
	fmt.Fprintf(&b, "--- /dev/null\n+++ b/%s\n@@ -0,0 +1", rel)
	if lines != 1 {
		fmt.Fprintf(&b, ",%d", lines)
	}
	b.WriteString(" @@\n")
	text := string(data)
	noNewline := !strings.HasSuffix(text, "\n")
	for _, line := range strings.SplitAfter(text, "\n") {
		if line != "" {
			b.WriteString("+" + line)
		}
	}
	if noNewline {
		b.WriteString("\n\\ No newline at end of file\n")
	}
	return b.String()
}

// parseNumstat reads "git diff --numstat -z": "added\tdeleted\tpath\0", or for a rename
// "added\tdeleted\t\0old\0new\0". Binary files have "-" counts, returned as Added -1.
func parseNumstat(out string) ([]FileChange, error) {
	var files []FileChange
	fields := strings.Split(out, "\x00")
	for i := 0; i < len(fields); i++ {
		if fields[i] == "" {
			continue
		}
		parts := strings.SplitN(fields[i], "\t", 3)
		if len(parts) != 3 {
			return nil, fmt.Errorf("unexpected git numstat entry %q", fields[i])
		}
		f := FileChange{Path: parts[2]}
		if parts[2] == "" { // a rename or copy: the old and the new path follow
			if i+2 >= len(fields) {
				return nil, fmt.Errorf("truncated git numstat rename entry")
			}
			f.Path = fields[i+2]
			i += 2
		}
		if parts[0] == "-" {
			f.Added, f.Deleted = -1, -1
		} else {
			var err1, err2 error
			f.Added, err1 = strconv.Atoi(parts[0])
			f.Deleted, err2 = strconv.Atoi(parts[1])
			if err1 != nil || err2 != nil {
				return nil, fmt.Errorf("unexpected git numstat counts %q", fields[i])
			}
		}
		files = append(files, f)
	}
	return files, nil
}

// splitPatch cuts "git diff" output into one chunk per file at its "diff --git " header.
func splitPatch(patch string) []string {
	var chunks []string
	start := -1
	for pos := 0; pos < len(patch); {
		end := len(patch)
		if i := strings.IndexByte(patch[pos:], '\n'); i >= 0 {
			end = pos + i + 1
		}
		if strings.HasPrefix(patch[pos:end], "diff --git ") {
			if start >= 0 {
				chunks = append(chunks, patch[start:pos])
			}
			start = pos
		}
		pos = end
	}
	if start >= 0 {
		chunks = append(chunks, patch[start:])
	}
	return chunks
}

// assemble writes the file list - within half of maxBytes - and then the full diffs that
// fit what is left.
func assemble(files []FileChange, bodies []string, unlisted, maxBytes int) ReviewDiff {
	var list strings.Builder
	listed := len(files)
	for i, f := range files {
		line := listLine(f)
		if list.Len()+len(line) > maxBytes/2 {
			listed = i
			break
		}
		list.WriteString(line)
	}
	unlisted += len(files) - listed
	files, bodies = files[:listed], bodies[:listed]

	truncated := unlisted > 0
	var body strings.Builder
	budget := maxBytes - list.Len() - 256 // room for the heading and the closing note
	for i := range files {
		if files[i].Treatment == TreatBudget { // an untracked file too large to read
			truncated = true
		}
		if files[i].Treatment != TreatFull {
			continue
		}
		if body.Len()+len(bodies[i]) > budget {
			files[i].Treatment = TreatBudget
			truncated = true
			continue
		}
		body.WriteString(bodies[i])
	}

	// The list is written after the budget pass, so it shows the final treatments.
	list.Reset()
	for _, f := range files {
		list.WriteString(listLine(f))
	}

	var b strings.Builder
	fmt.Fprintf(&b, "Changed files (%d), and how each is shown below:\n", len(files)+unlisted)
	b.WriteString(list.String())
	if unlisted > 0 {
		fmt.Fprintf(&b, "  ... and %d more files, neither listed nor shown\n", unlisted)
	}
	if body.Len() > 0 {
		b.WriteString("\n")
		b.WriteString(body.String())
	}
	return ReviewDiff{Text: b.String(), Files: files, Unlisted: unlisted, Truncated: truncated}
}

func listLine(f FileChange) string {
	counts := fmt.Sprintf("  +%d -%d", f.Added, f.Deleted)
	switch {
	case f.Added < 0:
		counts = ""
	case f.Untracked:
		counts = fmt.Sprintf("  +%d", f.Added)
	}
	what := map[Treatment]string{
		TreatFull:       "full diff below",
		TreatSummarized: "summarized: lock or generated file, not shown",
		TreatWithheld:   "withheld: matches the secret deny list",
		TreatBinary:     "binary, not shown",
		TreatSymlink:    "symlink -> " + f.Target,
		TreatBudget:     "not shown: diff budget",
	}[f.Treatment]
	if f.Untracked {
		what = "new file, " + what
	}
	return fmt.Sprintf("  %s%s  (%s)\n", f.Path, counts, what)
}

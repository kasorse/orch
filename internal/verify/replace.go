package verify

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path"
	"path/filepath"
	"regexp"
	"strings"
	"syscall"
	"time"

	"orchestrator/internal/domain"
	"orchestrator/internal/projectctx"
)

// Bounds of the replace tool.
const (
	// maxReplaceText bounds old and new, in bytes.
	maxReplaceText = 1000
	// maxReplacePaths bounds the paths argument.
	maxReplacePaths = 20
	// maxReplaceFileBytes is the largest file replace reads; larger ones are skipped.
	maxReplaceFileBytes = 1 << 20
	// replaceSniffBytes is how much of a file is searched for a NUL byte, which makes it
	// binary and skipped.
	replaceSniffBytes = 8 << 10
	// maxListedFiles bounds the files an answer names; the counts cover them all.
	maxListedFiles = 50
)

// replacePathRE is what one entry of paths may look like: a repository-relative path, a
// directory or a path.Match glob.
var replacePathRE = regexp.MustCompile(`^[A-Za-z0-9_.*?\[\]/-]{1,200}\z`)

// replaceRequest is a validated replace call.
type replaceRequest struct {
	old, new string
	paths    []string
	word     bool
	dryRun   bool
}

// replaceResult is what one replace call found and, unless it was a dry run, wrote.
type replaceResult struct {
	changed      []string
	counts       []int
	replacements int
	// excluded counts the selected files never read: deny-listed or protected.
	excluded int
	// err is a write that failed; the files before it are already rewritten.
	err error
}

// replace handles one replace call (specs/quality/SPEC.md §6.11): count it, validate the
// arguments, check the guard, list the files with git, rewrite the ones that hold old, record
// the call, answer. Deny-listed and protected files are never read, binary and large files
// and anything that is not a regular file inside the repository are skipped. Nothing the
// model wrote is recorded; the diff shows what changed.
func (t *Tools) replace(ctx context.Context, args json.RawMessage) Result {
	t.replaceCalls++
	rec := domain.VerifyCall{Tool: domain.VerifyToolReplace}
	refuse := func(reason, text string) Result {
		rec.Refused = reason
		t.record(rec)
		return Result{Text: "refused: " + text, IsError: true}
	}

	if t.replaceCalls > domain.MaxReplaceCalls {
		return refuse("limit reached", fmt.Sprintf("limit reached: at most %d replace calls per step", domain.MaxReplaceCalls))
	}
	req, reason := parseReplaceArgs(args)
	if reason != "" {
		return refuse("invalid arguments: "+reason, "invalid arguments: "+reason)
	}
	if reason, text := t.checkGuard(ctx); reason != "" {
		return refuse(reason, text)
	}
	if t.Files == nil {
		return refuse("no file list", "the server cannot list the repository's files")
	}
	files, err := t.Files.ListFiles(ctx)
	if err != nil {
		return refuse("failed to list files", "failed to list the repository's files: "+oneLine(err.Error()))
	}

	start := time.Now()
	res := t.rewrite(files, req)
	rec.Duration = time.Since(start)
	rec.Files, rec.Replacements, rec.DryRun = len(res.changed), res.replacements, req.dryRun
	rec.Success = res.err == nil
	if res.err != nil {
		rec.ExitCode = 1
	}
	t.record(rec)
	return Result{Text: t.replaceAnswer(req, res), IsError: res.err != nil}
}

// Reasons parseReplaceArgs gives; like parseArgs, nothing the model wrote is echoed.
const (
	reasonOldRequired = "old is required"
	reasonOldType     = "old must be a non-empty string of at most 1000 bytes without NUL"
	reasonNewRequired = "new is required"
	reasonNewType     = "new must be a string of at most 1000 bytes without NUL"
	reasonSame        = "old and new are the same"
	reasonPathsType   = "paths must be an array of at most 20 relative paths or globs"
	reasonBoolType    = "word and dry_run must be booleans"
)

func parseReplaceArgs(raw json.RawMessage) (req replaceRequest, reason string) {
	args := map[string]json.RawMessage{}
	if trimmed := strings.TrimSpace(string(raw)); trimmed != "" && trimmed != "null" {
		if firstByte(raw) != '{' || json.Unmarshal(raw, &args) != nil {
			return req, reasonNotObject
		}
	}
	for key := range args {
		switch key {
		case "old", "new", "paths", "word", "dry_run":
		default:
			return req, reasonUnknownArg
		}
	}
	text := func(key string, empty bool) (string, bool) {
		var v string
		value := args[key]
		if firstByte(value) != '"' || json.Unmarshal(value, &v) != nil ||
			len(v) > maxReplaceText || strings.ContainsRune(v, 0) || v == "" && !empty {
			return "", false
		}
		return v, true
	}
	var ok bool
	if _, present := args["old"]; !present {
		return req, reasonOldRequired
	}
	if req.old, ok = text("old", false); !ok {
		return req, reasonOldType
	}
	if _, present := args["new"]; !present {
		return req, reasonNewRequired
	}
	if req.new, ok = text("new", true); !ok {
		return req, reasonNewType
	}
	if req.old == req.new {
		return req, reasonSame
	}
	if value, present := args["paths"]; present {
		if firstByte(value) != '[' || json.Unmarshal(value, &req.paths) != nil || len(req.paths) > maxReplacePaths {
			return req, reasonPathsType
		}
		for _, p := range req.paths {
			if !validReplacePath(p) {
				return req, reasonPathsType
			}
		}
	}
	for key, dst := range map[string]*bool{"word": &req.word, "dry_run": &req.dryRun} {
		if value, present := args[key]; present && json.Unmarshal(value, dst) != nil {
			return req, reasonBoolType
		}
	}
	return req, ""
}

// validReplacePath accepts a relative, slash-separated path or glob that never leaves the
// repository.
func validReplacePath(p string) bool {
	if !replacePathRE.MatchString(p) || strings.HasPrefix(p, "/") {
		return false
	}
	for _, seg := range strings.Split(strings.TrimSuffix(p, "/"), "/") {
		if seg == "" || seg == "." || seg == ".." {
			return false
		}
	}
	_, err := path.Match(p, "")
	return err == nil
}

// selects reports whether rel is in the request's paths: the path itself, a file under it, a
// glob over the whole path, or a glob without a slash over the file name.
func (req replaceRequest) selects(rel string) bool {
	if len(req.paths) == 0 {
		return true
	}
	for _, p := range req.paths {
		p = strings.TrimSuffix(p, "/")
		if rel == p || strings.HasPrefix(rel, p+"/") {
			return true
		}
		if ok, _ := path.Match(p, rel); ok {
			return true
		}
		if !strings.Contains(p, "/") {
			if ok, _ := path.Match(p, path.Base(rel)); ok {
				return true
			}
		}
	}
	return false
}

// replacer returns the function that rewrites one file's text and counts the occurrences.
// With word, old matches only where it is not part of a longer identifier: a \b is added on
// each side whose edge is a word character.
func (req replaceRequest) replacer() func(string) (string, int) {
	if !req.word {
		return func(s string) (string, int) {
			n := strings.Count(s, req.old)
			if n == 0 {
				return s, 0
			}
			return strings.ReplaceAll(s, req.old, req.new), n
		}
	}
	expr := regexp.QuoteMeta(req.old)
	if isWordByte(req.old[0]) {
		expr = `\b` + expr
	}
	if isWordByte(req.old[len(req.old)-1]) {
		expr += `\b`
	}
	re := regexp.MustCompile(expr)
	return func(s string) (string, int) {
		n := len(re.FindAllStringIndex(s, -1))
		if n == 0 {
			return s, 0
		}
		return re.ReplaceAllLiteralString(s, req.new), n
	}
}

func isWordByte(b byte) bool {
	return b == '_' || '0' <= b && b <= '9' || 'a' <= b && b <= 'z' || 'A' <= b && b <= 'Z'
}

// rewrite applies req to the selected files, in the order git listed them.
func (t *Tools) rewrite(files []string, req replaceRequest) replaceResult {
	var res replaceResult
	root, err := filepath.EvalSymlinks(t.Spec.RepoRoot)
	if err != nil {
		res.err = err
		return res
	}
	denied := projectctx.DenyMatcher(t.Spec.Replace.ExtraDeny)
	protected := t.Spec.Before.Patterns()
	apply := req.replacer()
	for _, rel := range files {
		if !req.selects(rel) {
			continue
		}
		if denied(rel) || matchesAny(protected, rel) {
			res.excluded++
			continue
		}
		full, data, perm, ok := readReplaceable(root, rel)
		if !ok || !strings.Contains(string(data), req.old) {
			continue
		}
		out, n := apply(string(data))
		if n == 0 {
			continue
		}
		if !req.dryRun {
			if err := writeReplaced(full, out, perm); err != nil {
				res.err = fmt.Errorf("%s: %w", rel, err)
				return res
			}
		}
		res.changed = append(res.changed, rel)
		res.counts = append(res.counts, n)
		res.replacements += n
	}
	return res
}

func matchesAny(patterns []string, rel string) bool {
	for _, p := range patterns {
		if domain.MatchProtected(p, rel) {
			return true
		}
	}
	return false
}

// readReplaceable reads root/rel when it is a regular text file of at most
// maxReplaceFileBytes whose directory, symlinks resolved, is still inside root. It never
// follows a symlink to the file itself.
func readReplaceable(root, rel string) (full string, data []byte, perm os.FileMode, ok bool) {
	full = filepath.Join(root, filepath.FromSlash(rel))
	dir, err := filepath.EvalSymlinks(filepath.Dir(full))
	if err != nil || dir != root && !strings.HasPrefix(dir, root+string(filepath.Separator)) {
		return "", nil, 0, false
	}
	full = filepath.Join(dir, filepath.Base(full))
	f, err := os.OpenFile(full, os.O_RDONLY|syscall.O_NOFOLLOW|syscall.O_NONBLOCK, 0)
	if err != nil {
		return "", nil, 0, false
	}
	defer f.Close()
	info, err := f.Stat()
	if err != nil || !info.Mode().IsRegular() || info.Size() > maxReplaceFileBytes {
		return "", nil, 0, false
	}
	data, err = io.ReadAll(io.LimitReader(f, maxReplaceFileBytes+1))
	if err != nil || len(data) > maxReplaceFileBytes || bytes.IndexByte(data[:min(len(data), replaceSniffBytes)], 0) >= 0 {
		return "", nil, 0, false
	}
	return full, data, info.Mode().Perm(), true
}

// writeReplaced replaces full with content through a temporary file in its directory and a
// rename, so the file is never half written and a symlink put in its place is replaced, not
// written through.
func writeReplaced(full, content string, perm os.FileMode) (err error) {
	tmp, err := os.CreateTemp(filepath.Dir(full), ".orch-replace-*")
	if err != nil {
		return err
	}
	defer func() {
		if err != nil {
			os.Remove(tmp.Name())
		}
	}()
	if err = tmp.Chmod(perm); err == nil {
		_, err = tmp.WriteString(content)
	}
	if cerr := tmp.Close(); err == nil {
		err = cerr
	}
	if err == nil {
		err = os.Rename(tmp.Name(), full)
	}
	return err
}

// replaceAnswer is the text block of a replace call.
func (t *Tools) replaceAnswer(req replaceRequest, res replaceResult) string {
	var b strings.Builder
	verb := "replaced"
	if req.dryRun {
		verb = "dry run, nothing written: would replace"
	}
	switch {
	case res.err != nil:
		fmt.Fprintf(&b, "failed: %s\n", oneLine(res.err.Error()))
		fmt.Fprintf(&b, "rewritten before the failure: %d occurrences in %d files\n", res.replacements, len(res.changed))
	case len(res.changed) == 0:
		b.WriteString("no occurrence found; nothing changed\n")
	default:
		fmt.Fprintf(&b, "%s %d occurrences in %d files\n", verb, res.replacements, len(res.changed))
	}
	for i, rel := range res.changed {
		if i == maxListedFiles {
			fmt.Fprintf(&b, "  (and %d more files)\n", len(res.changed)-maxListedFiles)
			break
		}
		fmt.Fprintf(&b, "  %s (%d)\n", rel, res.counts[i])
	}
	if res.excluded > 0 {
		fmt.Fprintf(&b, "not searched: %d deny-listed or protected files\n", res.excluded)
	}
	fmt.Fprintf(&b, "replace calls left in this step: %d\n", domain.MaxReplaceCalls-t.replaceCalls)
	return b.String()
}

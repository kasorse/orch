// Package tools is the repository tools of the API transport and their confinement
// (specs/api-transport/SPEC.md §5): what the claude CLI's --restricted, --tools and dontAsk
// enforce for a CLI call, this package enforces in Go for a call the orchestrator runs itself.
//
// Every tool is a JSON-schema definition and a Go function over one working directory (Root).
// No tool runs a command or opens a connection: the package imports neither os/exec nor net
// (enforced by a test). It is provider-neutral; provider/anthropic and provider/openai turn the
// definitions into their wire's tool declarations.
package tools

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"

	"orchestrator/internal/projectctx"
)

// Bounds of SPEC §5 item 5.
const (
	maxFileBytes   = 1 << 20
	binarySniff    = 8 << 10
	maxResultBytes = 64 << 10
	maxMatches     = 2000
	maxLineBytes   = 500
)

// refusal is the answer to a path or an input the call may not use. The message the model gets says
// what to do instead and never why a particular file is denied.
type refusal struct{ msg string }

func (r *refusal) Error() string { return r.msg }

func refuse(format string, args ...any) error { return &refusal{fmt.Sprintf(format, args...)} }

// Root is the working directory one call's tools are confined to, with the call's deny list
// (provider.Request.DenyRead).
type Root struct {
	// dir is the working directory with every symlink resolved: a path is inside the root when
	// its own resolved form is under dir.
	dir    string
	denied func(rel string) bool
}

// NewRoot confines tools to dir, refusing every path the deny list (projectctx's patterns,
// matched on every segment, case-insensitively) covers.
func NewRoot(dir string, deny []string) (*Root, error) {
	abs, err := filepath.Abs(dir)
	if err != nil {
		return nil, err
	}
	real, err := filepath.EvalSymlinks(abs)
	if err != nil {
		return nil, err
	}
	info, err := os.Stat(real)
	if err != nil {
		return nil, err
	}
	if !info.IsDir() {
		return nil, fmt.Errorf("%s is not a directory", dir)
	}
	return &Root{dir: real, denied: projectctx.DenyMatcher(deny)}, nil
}

// Resolve turns a path the model gave into the absolute path inside the root and the
// slash-separated path relative to it (SPEC §5 items 2-4). It refuses an absolute path, a path
// that leaves the root lexically or through a symlink, a path the deny list covers, anything
// under .git, and, forWrite, anything under .claude and a symlink as the target. "" and "." are
// the root itself.
func (r *Root) Resolve(p string, forWrite bool) (abs, rel string, err error) {
	p = strings.TrimSpace(p)
	if p == "" {
		p = "."
	}
	if filepath.IsAbs(p) || strings.HasPrefix(p, "~") {
		return "", "", refuse("%s: use a path relative to the repository root", p)
	}
	clean := filepath.Clean(filepath.FromSlash(p))
	if clean == ".." || strings.HasPrefix(clean, ".."+string(filepath.Separator)) {
		return "", "", refuse("%s: the path leaves the repository", p)
	}
	rel = filepath.ToSlash(clean)
	if rel != "." {
		if err := r.check(rel, forWrite); err != nil {
			return "", "", err
		}
	}
	abs = filepath.Join(r.dir, clean)
	real, err := realPath(abs)
	if err != nil {
		return "", "", refuse("%s: the path cannot be resolved", p)
	}
	if real != r.dir && !strings.HasPrefix(real, r.dir+string(filepath.Separator)) {
		return "", "", refuse("%s: the path leaves the repository", p)
	}
	// Through a symlink inside the root, the real path must pass the same checks.
	if realRel, _ := filepath.Rel(r.dir, real); realRel != "." && filepath.ToSlash(realRel) != rel {
		if err := r.check(filepath.ToSlash(realRel), forWrite); err != nil {
			return "", "", err
		}
	}
	if forWrite {
		if info, err := os.Lstat(abs); err == nil && info.Mode()&fs.ModeSymlink != 0 {
			return "", "", refuse("%s: a symlink is not written through", p)
		}
	}
	return abs, rel, nil
}

func (r *Root) check(rel string, forWrite bool) error {
	for _, seg := range strings.Split(rel, "/") {
		lower := strings.ToLower(seg)
		if lower == ".git" {
			return refuse("%s: .git is not accessible", rel)
		}
		if forWrite && lower == ".claude" {
			return refuse("%s: .claude is not writable", rel)
		}
	}
	if r.denied(rel) {
		return refuse("%s: the path is not accessible", rel)
	}
	return nil
}

// realPath resolves every symlink of abs. For a path that does not exist yet (a file being
// created) it resolves the nearest existing parent and appends the rest.
func realPath(abs string) (string, error) {
	real, err := filepath.EvalSymlinks(abs)
	if err == nil {
		return real, nil
	}
	if !errors.Is(err, fs.ErrNotExist) {
		return "", err
	}
	parent, base := filepath.Dir(abs), filepath.Base(abs)
	if parent == abs {
		return "", err
	}
	realParent, perr := realPath(parent)
	if perr != nil {
		return "", perr
	}
	return filepath.Join(realParent, base), nil
}

// skipDir reports whether a walk must not enter rel: .git, or a directory the deny list covers.
func (r *Root) skipDir(rel string) bool {
	if rel == "." {
		return false
	}
	return r.check(rel, false) != nil
}

// readText reads a regular file for a tool, or explains why not (SPEC §5 item 5).
func readText(abs, rel string) (string, error) {
	info, err := os.Lstat(abs)
	switch {
	case errors.Is(err, fs.ErrNotExist):
		return "", refuse("%s: no such file", rel)
	case err != nil:
		return "", refuse("%s: the file cannot be read", rel)
	case info.IsDir():
		return "", refuse("%s is a directory; use glob to list it", rel)
	case info.Mode()&fs.ModeSymlink != 0:
		// Resolve already checked where it points; read the target.
		if info, err = os.Stat(abs); err != nil || !info.Mode().IsRegular() {
			return "", refuse("%s: not a regular file", rel)
		}
	case !info.Mode().IsRegular():
		return "", refuse("%s: not a regular file", rel)
	}
	if info.Size() > maxFileBytes {
		return "", refuse("%s is %d bytes, over the %d-byte limit; use grep to find what you need in it", rel, info.Size(), maxFileBytes)
	}
	data, err := os.ReadFile(abs)
	if err != nil {
		return "", refuse("%s: the file cannot be read", rel)
	}
	if isBinary(data) {
		return "", refuse("%s is a binary file", rel)
	}
	return string(data), nil
}

func isBinary(data []byte) bool {
	for _, b := range data[:min(len(data), binarySniff)] {
		if b == 0 {
			return true
		}
	}
	return false
}

// bound cuts a tool result at maxResultBytes, saying what was cut.
func bound(s string) string {
	if len(s) <= maxResultBytes {
		return s
	}
	cut := strings.ToValidUTF8(s[:maxResultBytes], "")
	if i := strings.LastIndexByte(cut, '\n'); i > 0 {
		cut = cut[:i+1]
	}
	return cut + fmt.Sprintf("[cut: the result is %d bytes, the first %d are shown; narrow the request]\n", len(s), len(cut))
}

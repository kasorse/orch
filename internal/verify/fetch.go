package verify

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/url"
	"os"
	"os/exec"
	"path"
	"path/filepath"
	"strings"
	"syscall"
	"time"

	"github.com/kasorse/orch/internal/domain"
)

// maxFetchURLBytes bounds the url argument, as the tool's JSON Schema states it.
const maxFetchURLBytes = 500

// Reasons parseFetchArgs gives; like parseArgs, nothing the model wrote is echoed.
const (
	reasonURLRequired  = "url is required"
	reasonURLType      = "url must be a string of at most 500 bytes"
	reasonURLShape     = "url is not a repository URL under the allowed groups"
	reasonURLNotGitLab = "url is not under the configured GitLab"
	reasonURLGroup     = "the repository is not under an allowed group"
)

// fetchRequest is a validated fetch_repository call: the project path (group/.../name) and
// the URL to clone, as the base URL gives it.
type fetchRequest struct {
	project, name, clone string
}

// fetch clones one project of the allowed groups into FetchDir/<name> (specs/quality/SPEC.md
// §6.12). The URL is checked in Go before anything runs: it must start with the base URL
// and name a project under an allowed group; what the model wrote never reaches git
// otherwise. A project already there is answered with its path and not cloned again.
func (t *Tools) fetch(ctx context.Context, args json.RawMessage, k int) Result {
	t.fetchCalls++
	rec := domain.VerifyCall{Tool: domain.VerifyToolFetchRepository}
	refuse := func(reason, text string) Result {
		rec.Refused = reason
		t.record(rec)
		return Result{Text: "refused: " + text, IsError: true}
	}

	if t.fetchCalls > domain.MaxFetchCalls {
		return refuse("limit reached", fmt.Sprintf("limit reached: at most %d fetch_repository calls per step", domain.MaxFetchCalls))
	}
	req, reason := parseFetchArgs(t.Spec.Fetch, args)
	if reason != "" {
		return refuse("invalid arguments: "+reason, "invalid arguments: "+reason+"; the allowed groups are "+
			strings.Join(t.Spec.Fetch.AllowedGroups, ", ")+" of "+t.Spec.Fetch.BaseURL)
	}
	rec.Repository = req.project
	if reason, text := t.checkGuard(ctx); reason != "" {
		return refuse(reason, text)
	}

	rel := path.Join(FetchDir, req.name)
	dest := filepath.Join(t.Spec.RepoRoot, FetchDir, req.name)
	if origin, ok := cloneOrigin(dest); ok {
		if origin != req.clone && origin != sshForm(req.clone) {
			return refuse("name taken", fmt.Sprintf("%s already holds a clone of %s; this step cannot fetch a second repository named %s", rel, origin, req.name))
		}
		rec.Success = true
		t.record(rec)
		return Result{Text: t.fetchAnswer(req, rel, dest, "already fetched")}
	}

	start := time.Now()
	out, err := t.clone(ctx, req.clone, dest)
	rec.Duration = time.Since(start)
	rec.Success = err == nil
	if err != nil {
		rec.ExitCode = 1
	}
	t.record(rec)
	t.writeLog(LogFile(k, "stderr"), out)
	if err != nil {
		os.RemoveAll(dest)
		return Result{Text: fmt.Sprintf("failed: %v\n--- git output ---\n%s", err, tail(out, 4000)), IsError: true}
	}
	return Result{Text: t.fetchAnswer(req, rel, dest, "fetched")}
}

// parseFetchArgs validates the arguments: one string, url, that resolves to a project under
// an allowed group of the base URL. A link into the project (GitLab's /-/blob/..., a .git
// suffix, a trailing slash) resolves to the project.
func parseFetchArgs(f *FetchSpec, raw json.RawMessage) (req fetchRequest, reason string) {
	args := map[string]json.RawMessage{}
	if trimmed := strings.TrimSpace(string(raw)); trimmed != "" && trimmed != "null" {
		if err := json.Unmarshal(raw, &args); err != nil {
			return req, reasonNotObject
		}
	}
	for key := range args {
		if key != "url" {
			return req, reasonUnknownArg
		}
	}
	value, present := args["url"]
	if !present {
		return req, reasonURLRequired
	}
	var s string
	if firstByte(value) != '"' || json.Unmarshal(value, &s) != nil || len(s) > maxFetchURLBytes {
		return req, reasonURLType
	}
	return resolveFetch(f, strings.TrimSpace(s))
}

// resolveFetch is the pure part of parseFetchArgs.
func resolveFetch(f *FetchSpec, raw string) (req fetchRequest, reason string) {
	base, err := url.Parse(f.BaseURL)
	if err != nil {
		return req, reasonURLNotGitLab
	}
	u, err := url.Parse(raw)
	if err != nil || !strings.EqualFold(u.Scheme, base.Scheme) || !strings.EqualFold(u.Host, base.Host) || u.User != nil {
		return req, reasonURLNotGitLab
	}
	rest, ok := strings.CutPrefix(u.Path, base.Path+"/")
	if !ok {
		return req, reasonURLNotGitLab
	}
	// A link into the project: GitLab puts /-/ between the project and the rest.
	if i := strings.Index(rest, "/-/"); i >= 0 {
		rest = rest[:i]
	}
	rest = strings.TrimSuffix(strings.Trim(rest, "/"), ".git")
	if !domain.RepositoryPathRE.MatchString(rest) {
		return req, reasonURLShape
	}
	allowed := false
	for _, g := range f.AllowedGroups {
		if strings.HasPrefix(rest, g+"/") {
			allowed = true
		}
	}
	if !allowed {
		return req, reasonURLGroup
	}
	req.project, req.name = rest, path.Base(rest)
	req.clone = f.BaseURL + "/" + rest + ".git"
	return req, ""
}

// sshForm is the ssh URL of an https clone URL (git@host:path.git): what the operator's own
// checkouts most often use, tried when the https clone is refused.
func sshForm(clone string) string {
	u, err := url.Parse(clone)
	if err != nil || u.Scheme == "ssh" {
		return ""
	}
	return "git@" + u.Hostname() + ":" + strings.TrimPrefix(u.Path, "/")
}

// cloneOrigin is the origin URL of the clone at dest, false when there is none.
func cloneOrigin(dest string) (string, bool) {
	if fi, err := os.Lstat(filepath.Join(dest, ".git")); err != nil || !fi.IsDir() {
		return "", false
	}
	out, err := exec.Command("git", "-C", dest, "config", "--get", "remote.origin.url").Output()
	if err != nil {
		return "", false
	}
	return strings.TrimSpace(string(out)), true
}

// clone runs git clone --depth 1 of cloneURL into dest, with prompts off, under the fetch
// timeout; when the https clone fails it tries the ssh form once. It returns git's output
// (both attempts') and the error of the last attempt.
func (t *Tools) clone(ctx context.Context, cloneURL, dest string) (string, error) {
	if err := os.MkdirAll(filepath.Dir(dest), 0o755); err != nil {
		return "", err
	}
	ctx, cancel := context.WithTimeout(ctx, t.Spec.Fetch.Timeout)
	defer cancel()
	var out strings.Builder
	urls := []string{cloneURL}
	if ssh := sshForm(cloneURL); ssh != "" {
		urls = append(urls, ssh)
	}
	var err error
	for i, u := range urls {
		if i > 0 {
			os.RemoveAll(dest)
			fmt.Fprintf(&out, "--- retrying over ssh as %s ---\n", u)
		}
		cmd := exec.CommandContext(ctx, "git", "clone", "--quiet", "--depth", "1", "--no-tags", "--", u, dest)
		cmd.Env = gitEnv()
		cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
		// Kill the group: an ssh or credential helper must not outlive the call.
		cmd.Cancel = func() error { return syscall.Kill(-cmd.Process.Pid, syscall.SIGKILL) }
		res, runErr := cmd.CombinedOutput()
		out.Write(res)
		if runErr == nil {
			return out.String(), nil
		}
		err = fmt.Errorf("git clone %s: %w", u, runErr)
		if errors.Is(ctx.Err(), context.DeadlineExceeded) {
			return out.String(), fmt.Errorf("git clone %s timed out after %s", u, t.Spec.Fetch.Timeout)
		}
	}
	return out.String(), err
}

// gitEnv is the server's environment with every prompt turned off: a missing credential is
// a failed clone, never a hang. The operator's GIT_SSH_COMMAND is kept.
func gitEnv() []string {
	env := append(os.Environ(), "GIT_TERMINAL_PROMPT=0")
	for _, kv := range env {
		if strings.HasPrefix(kv, "GIT_SSH_COMMAND=") {
			return env
		}
	}
	return append(env, "GIT_SSH_COMMAND=ssh -o BatchMode=yes")
}

// fetchAnswer is the text of a successful call: where the clone is and what it is at.
func (t *Tools) fetchAnswer(req fetchRequest, rel, dest, verb string) string {
	at := ""
	if out, err := exec.Command("git", "-C", dest, "rev-parse", "--short", "HEAD").Output(); err == nil {
		at = " at " + strings.TrimSpace(string(out))
	}
	var b strings.Builder
	fmt.Fprintf(&b, "%s: %s%s (a shallow clone of its default branch)\n", verb, req.project, at)
	fmt.Fprintf(&b, "path: %s\n", rel)
	b.WriteString("Read it with Read, Grep and Glob under that path. It is a read-only reference; it is not part of this repository or of the change, so do not edit it or list its files as affected.\n")
	fmt.Fprintf(&b, "calls left in this step: %d\n", domain.MaxFetchCalls-t.fetchCalls)
	return b.String()
}

// tail keeps the last n bytes of s, on a line boundary when it can.
func tail(s string, n int) string {
	if len(s) <= n {
		return s
	}
	s = s[len(s)-n:]
	if i := strings.IndexByte(s, '\n'); i >= 0 && i < len(s)-1 {
		s = s[i+1:]
	}
	return "[...]\n" + s
}

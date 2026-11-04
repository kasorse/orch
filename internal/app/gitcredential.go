package app

import (
	"bufio"
	"fmt"
	"io"
	"net/url"
	"strconv"
	"strings"

	"github.com/kasorse/orch/internal/apperr"
	"github.com/kasorse/orch/internal/config"
	"github.com/kasorse/orch/internal/listener"
	"github.com/kasorse/orch/internal/logging"
)

const gitCredentialUsage = "usage: orchestrator [--config <path>] git-credential get|store|erase   (git starts it as a credential helper)"

// maxCredentialRequestLines and maxCredentialLineBytes bound what the helper reads from git.
const (
	maxCredentialRequestLines = 64
	maxCredentialLineBytes    = 8 << 10
)

// gitCredentialArgs parses what follows "git-credential": the one action git passes to a helper.
// The subcommand is hidden - it is not in the usage text - and is started only by git, through
// the configuration listenerGitEnv puts in the environment (specs/jira-listener/SPEC.md §6.2).
func gitCredentialArgs(args []string) (string, error) {
	if len(args) != 1 || (args[0] != "get" && args[0] != "store" && args[0] != "erase") {
		return "", &apperr.ConfigError{Msg: gitCredentialUsage}
	}
	return args[0], nil
}

// listenerGitEnv is the environment entries that make git ask the listener's own executable for
// the GitLab credential of baseURL (SPEC §6.2):
//
//	GIT_CONFIG_COUNT=1
//	GIT_CONFIG_KEY_0=credential.<baseURL>.helper
//	GIT_CONFIG_VALUE_0=!<exe> [helperArgs...] git-credential
//
// git appends the action ("get") to a helper that starts with "!". The token is in none of
// it: the helper reads it from gitlab.token_env when asked. helperArgs are the global flags the
// helper is started with; the listener passes its absolute --config, because git runs the helper in
// whatever directory it works in.
func listenerGitEnv(exe, baseURL string, helperArgs ...string) []string {
	return listenerGitEnvAt(0, exe, baseURL, helperArgs)
}

// listenerGitEnvAt is listenerGitEnv for an environment that already holds n GIT_CONFIG entries.
func listenerGitEnvAt(n int, exe, baseURL string, helperArgs []string) []string {
	words := []string{logging.ShellWord(exe)}
	for _, a := range helperArgs {
		words = append(words, logging.ShellWord(a))
	}
	words = append(words, "git-credential")
	return []string{
		"GIT_CONFIG_COUNT=" + strconv.Itoa(n+1),
		fmt.Sprintf("GIT_CONFIG_KEY_%d=credential.%s.helper", n, baseURL),
		fmt.Sprintf("GIT_CONFIG_VALUE_%d=!%s", n, strings.Join(words, " ")),
	}
}

// withListenerGit is base, a whole environment, with the helper's entries added after the
// GIT_CONFIG entries it already holds.
func withListenerGit(base []string, exe, baseURL string, helperArgs ...string) []string {
	n := 0
	out := make([]string, 0, len(base)+3)
	for _, e := range base {
		if v, ok := strings.CutPrefix(e, "GIT_CONFIG_COUNT="); ok {
			if c, err := strconv.Atoi(v); err == nil && c > 0 {
				n = c
			}
			continue
		}
		out = append(out, e)
	}
	return append(out, listenerGitEnvAt(n, exe, baseURL, helperArgs)...)
}

// runGitCredential answers one call of git's credential helper protocol. For "get" it reads the
// request from stdin (key=value lines up to a blank line) and, only when it names exactly the
// protocol, host and port of gitlab.base_url, writes username and password; for anything else it
// writes nothing and succeeds, so git tries its other helpers. "store" and "erase" read the
// request and do nothing: the token lives in the environment and is neither kept nor forgotten.
// stdout is git's channel: the token is the only secret that ever goes to it.
func runGitCredential(action, configPath string, getenv func(string) string, stdin io.Reader, stdout io.Writer) error {
	req, err := readCredentialRequest(stdin)
	if err != nil {
		return &apperr.ConfigError{Msg: "git-credential", Err: err}
	}
	if action != "get" {
		return nil
	}
	cfg, err := config.Load(configPath)
	if err != nil {
		return err
	}
	if cfg.GitLab == nil {
		return &apperr.ConfigError{Msg: fmt.Sprintf("git-credential: %s has no gitlab section", configPath)}
	}
	base, err := url.Parse(cfg.GitLab.BaseURL)
	if err != nil {
		return &apperr.ConfigError{Msg: "git-credential: gitlab.base_url is not a URL"}
	}
	want, ok := listener.Endpoint(base.Scheme, base.Host)
	if !ok {
		return &apperr.ConfigError{Msg: "git-credential: gitlab.base_url has no host"}
	}
	got, ok := listener.Endpoint(req["protocol"], req["host"])
	if !ok || got != want {
		return nil
	}
	token := getenv(cfg.GitLab.TokenEnv)
	if strings.TrimSpace(token) == "" {
		return nil
	}
	if strings.ContainsAny(token, "\r\n\x00") || strings.ContainsAny(cfg.GitLab.Username, "\r\n\x00") {
		return &apperr.ConfigError{Msg: fmt.Sprintf("git-credential: %s or gitlab.username holds a line break or a NUL, which git's protocol cannot carry", cfg.GitLab.TokenEnv)}
	}
	_, err = fmt.Fprintf(stdout, "username=%s\npassword=%s\n", cfg.GitLab.Username, token)
	return err
}

// readCredentialRequest reads git's key=value lines up to the blank line that ends the request,
// or EOF. A later line of the same key wins; a line without "=" is an error. The input is bounded.
func readCredentialRequest(r io.Reader) (map[string]string, error) {
	req := map[string]string{}
	sc := bufio.NewScanner(io.LimitReader(r, int64(maxCredentialRequestLines*maxCredentialLineBytes)))
	sc.Buffer(make([]byte, 0, 4096), maxCredentialLineBytes)
	for lines := 0; sc.Scan(); lines++ {
		line := sc.Text()
		if line == "" {
			break
		}
		if lines >= maxCredentialRequestLines {
			return nil, fmt.Errorf("the request is longer than %d lines", maxCredentialRequestLines)
		}
		key, value, ok := strings.Cut(line, "=")
		if !ok || key == "" {
			return nil, fmt.Errorf("malformed line %d in the request", lines+1)
		}
		req[key] = value
	}
	if err := sc.Err(); err != nil {
		return nil, err
	}
	return req, nil
}

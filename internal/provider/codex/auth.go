package codex

import (
	"bytes"
	"fmt"
	"io"
	"strings"
	"sync"

	"github.com/kasorse/orch/internal/apperr"
	"github.com/kasorse/orch/internal/domain"
	"github.com/kasorse/orch/internal/execution"
)

const apiKeyVar = "CODEX_API_KEY"

type credentialAuth struct {
	mu   sync.RWMutex
	set  bool
	mode string
	key  string
}

// SetAuth selects one credential mode for the lifetime of the client. The key is only
// retained by the adapter and is passed to one child process at a time.
func (c *Client) SetAuth(mode, key string) {
	c.auth.mu.Lock()
	defer c.auth.mu.Unlock()
	c.auth.set, c.auth.mode, c.auth.key = true, mode, key
}

func (c *Client) snapshot() (mode, key string, err error) {
	c.auth.mu.RLock()
	defer c.auth.mu.RUnlock()
	if !c.auth.set {
		return "", "", apperr.NewConfigError("the codex credential mode was not set before the first call", nil)
	}
	return c.auth.mode, c.auth.key, nil
}

func (c *Client) env() ([]string, error) {
	mode, key, err := c.snapshot()
	if err != nil {
		return nil, err
	}
	return buildEnv(execution.ScrubbedEnv(), mode, key)
}

func buildEnv(base []string, mode, key string) ([]string, error) {
	if mode != domain.CodexAuthAPI && mode != domain.CodexAuthLogin {
		return nil, apperr.NewConfigError(fmt.Sprintf("unknown codex auth mode %q", mode), nil)
	}
	if mode == domain.CodexAuthAPI && strings.TrimSpace(key) == "" {
		return nil, apperr.NewConfigError("codex auth mode api has no API key", nil)
	}
	env := make([]string, 0, len(base)+1)
	for _, kv := range base {
		name, _, _ := strings.Cut(kv, "=")
		if contains(domain.CodexAPIEnvRemoved(), name) {
			continue
		}
		env = append(env, kv)
	}
	if mode == domain.CodexAuthAPI {
		env = append(env, apiKeyVar+"="+key)
	}
	return env, nil
}

func contains(values []string, value string) bool {
	for _, v := range values {
		if v == value {
			return true
		}
	}
	return false
}

func redactor(key string) func(string) string {
	if key == "" {
		return func(s string) string { return s }
	}
	return func(s string) string { return strings.ReplaceAll(s, key, "[REDACTED]") }
}

type redactWriter struct {
	mu     sync.Mutex
	w      io.Writer
	redact func(string) string
	buf    []byte
}

func (w *redactWriter) Write(p []byte) (int, error) {
	w.mu.Lock()
	defer w.mu.Unlock()
	w.buf = append(w.buf, p...)
	for {
		i := bytes.IndexByte(w.buf, '\n')
		if i < 0 {
			break
		}
		if _, err := w.w.Write([]byte(w.redact(string(w.buf[:i+1])))); err != nil {
			return len(p), err
		}
		w.buf = w.buf[i+1:]
	}
	return len(p), nil
}

func (w *redactWriter) Flush() {
	w.mu.Lock()
	defer w.mu.Unlock()
	if len(w.buf) == 0 {
		return
	}
	_, _ = w.w.Write([]byte(w.redact(string(w.buf))))
	w.buf = nil
}

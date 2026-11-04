//go:build security

package security

// The credential checks of specs/claude-auth (CA07): what the real CLI reports as the source of
// its credential, and what a rejected key looks like. Each check is one cheap call on the
// smallest model with AuthorityNone. They are skipped unless ORCH_LIVE_ANTHROPIC_KEY holds a
// Console key (the login check also skips when the CLI is not logged in):
//
//	ORCH_LIVE_ANTHROPIC_KEY=sk-ant-... go test -tags security ./test/security/... -run ClaudeAuth

import (
	"context"
	"errors"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/kasorse/orch/internal/apperr"
	"github.com/kasorse/orch/internal/domain"
	"github.com/kasorse/orch/internal/execution"
	"github.com/kasorse/orch/internal/provider"
	"github.com/kasorse/orch/internal/provider/claude"
)

const liveKeyVar = "ORCH_LIVE_ANTHROPIC_KEY"

func requireLiveKey(t *testing.T) string {
	t.Helper()
	requireClaude(t)
	key := os.Getenv(liveKeyVar)
	if key == "" {
		t.Skipf("%s not set", liveKeyVar)
	}
	return key
}

// authCall runs one tiny call under the given mode and key. The returned error is the
// adapter's, so a FailureAuth is visible to the caller.
func authCall(t *testing.T, mode, key string) (provider.Result, error) {
	t.Helper()
	c := claude.New(execution.NewOSProcessExecutor())
	c.SetAuth(mode, key, nil)
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()
	return c.Execute(ctx, provider.Request{
		Prompt: "Reply with the single word ok.", Model: "haiku",
		WorkingDir: t.TempDir(), Authority: domain.AuthorityNone,
	})
}

func apiKeySource(res provider.Result) string {
	if res.Call != nil && res.Call.APIKeySource != "" {
		return res.Call.APIKeySource
	}
	if res.Usage != nil {
		return res.Usage.APIKeySource
	}
	return ""
}

// 1: api mode with a valid key is billed to that key.
func TestClaudeAuth_APIKeyIsUsed(t *testing.T) {
	key := requireLiveKey(t)
	res, err := authCall(t, domain.ClaudeAuthAPI, key)
	if err != nil {
		t.Fatalf("api call: %v", err)
	}
	if got := apiKeySource(res); got != "ANTHROPIC_API_KEY" {
		t.Errorf("apiKeySource = %q, want ANTHROPIC_API_KEY", got)
	}
	if res.Usage == nil || res.Usage.CostUSD <= 0 {
		t.Errorf("total_cost_usd should be > 0 under an API key, usage: %+v", res.Usage)
	}
}

// 2: login mode never uses the key, even when the parent has one.
func TestClaudeAuth_LoginDoesNotUseTheKey(t *testing.T) {
	key := requireLiveKey(t)
	t.Setenv("ANTHROPIC_API_KEY", key)
	res, err := authCall(t, domain.ClaudeAuthLogin, "")
	var perr *apperr.ProviderError
	if errors.As(err, &perr) && perr.Kind == apperr.FailureAuth {
		t.Skipf("the claude CLI is not logged in: %v", err)
	}
	if err != nil {
		t.Fatalf("login call: %v", err)
	}
	if got := apiKeySource(res); got == "ANTHROPIC_API_KEY" {
		t.Errorf("apiKeySource = %q under login mode", got)
	}
}

// 3: a syntactically valid but wrong key is a FailureAuth. The raw events are logged so the
// classifier can be compared with what the CLI really emits (record them in
// specs/permissions/DECISIONS.md).
func TestClaudeAuth_WrongKeyIsAuthFailure(t *testing.T) {
	requireLiveKey(t)
	wrong := "sk-ant-api03-" + strings.Repeat("x", 80) + "AA"
	res, err := authCall(t, domain.ClaudeAuthAPI, wrong)
	t.Logf("exit %d, output %q, stderr %q, err %v", res.ExitCode, res.Output, res.Stderr, err)
	var perr *apperr.ProviderError
	if !errors.As(err, &perr) || perr.Kind != apperr.FailureAuth {
		t.Fatalf("a wrong key should be FailureAuth, got %v", err)
	}
	if strings.Contains(err.Error(), wrong) {
		t.Error("the error contains the key")
	}
	// The CLI retries a 401 for minutes; the adapter stops at the first retry (DECISIONS D17).
	if res.Duration > 30*time.Second {
		t.Errorf("the call ran %v; it should stop at the first auth retry", res.Duration)
	}
}

// 4: api mode drops ANTHROPIC_AUTH_TOKEN from the child, so the key still wins.
func TestClaudeAuth_AuthTokenInParentDoesNotOverrideTheKey(t *testing.T) {
	key := requireLiveKey(t)
	t.Setenv("ANTHROPIC_AUTH_TOKEN", "not-a-real-token")
	res, err := authCall(t, domain.ClaudeAuthAPI, key)
	if err != nil {
		t.Fatalf("api call: %v", err)
	}
	if got := apiKeySource(res); got != "ANTHROPIC_API_KEY" {
		t.Errorf("apiKeySource = %q, want ANTHROPIC_API_KEY", got)
	}
}

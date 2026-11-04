//go:build security

package security

// The live checks of the API transport (specs/api-transport AT13, SPEC §11): the clients of
// internal/provider/anthropic and internal/provider/openai against the real endpoints. Each
// endpoint runs only when its variables are set; the claude CLI is not needed:
//
//	ORCH_LIVE_ANTHROPIC_KEY=...                     Anthropic first-party (model: ORCH_LIVE_ANTHROPIC_MODEL, default claude-haiku-4-5)
//	ORCH_LIVE_OPENAI_KEY=...                        OpenAI first-party (model: ORCH_LIVE_OPENAI_MODEL, default gpt-5-mini)
//	ORCH_LIVE_LITELLM_URL=... ORCH_LIVE_LITELLM_KEY=...
//	  ORCH_LIVE_LITELLM_CLAUDE_MODEL=...            the gateway's Messages API route (e.g. a Bedrock model)
//	  ORCH_LIVE_LITELLM_CODEX_MODEL=...             the gateway's Responses API route
//
//	go test -tags security ./test/security/... -run API -v
//
// As in the rest of the suite, assertions look at sentinels the model cannot know without the tool
// call being tested, and at the filesystem - never at the model's prose. The gateway's cost header
// on a streamed response is recorded with t.Logf, not asserted: AT13 writes down what it found.

import (
	"context"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/kasorse/orch/internal/domain"
	"github.com/kasorse/orch/internal/projectctx"
	"github.com/kasorse/orch/internal/provider"
	"github.com/kasorse/orch/internal/provider/anthropic"
	"github.com/kasorse/orch/internal/provider/httpapi"
	"github.com/kasorse/orch/internal/provider/openai"
)

// apiClient is what both API clients are: a provider and its endpoint.
type apiClient interface {
	provider.Client
	Endpoint() (httpapi.Endpoint, error)
	ReportsCost(model string) bool
}

// apiEndpoint is one endpoint and route under test.
type apiEndpoint struct {
	name    string // the subtest's name
	wire    string // "messages" or "responses"
	gateway bool
	model   string
	client  apiClient
}

func envOr(name, def string) string {
	if v := os.Getenv(name); v != "" {
		return v
	}
	return def
}

// apiEndpoints is every endpoint whose variables are set; the test skips without any.
func apiEndpoints(t *testing.T) []apiEndpoint {
	t.Helper()
	if testing.Short() {
		t.Skip("live API suite: skipped under -short")
	}
	var eps []apiEndpoint
	if key := os.Getenv(liveKeyVar); key != "" {
		c := anthropic.New("", false, nil)
		c.SetKey(key)
		eps = append(eps, apiEndpoint{name: "anthropic", wire: "messages", model: envOr("ORCH_LIVE_ANTHROPIC_MODEL", "claude-haiku-4-5"), client: c})
	}
	if key := os.Getenv("ORCH_LIVE_OPENAI_KEY"); key != "" {
		c := openai.New("", false, nil)
		c.SetKey(key)
		eps = append(eps, apiEndpoint{name: "openai", wire: "responses", model: envOr("ORCH_LIVE_OPENAI_MODEL", "gpt-5-mini"), client: c})
	}
	if url, key := os.Getenv("ORCH_LIVE_LITELLM_URL"), os.Getenv("ORCH_LIVE_LITELLM_KEY"); url != "" && key != "" {
		if m := os.Getenv("ORCH_LIVE_LITELLM_CLAUDE_MODEL"); m != "" {
			c := anthropic.New(url, false, nil)
			c.SetKey(key)
			eps = append(eps, apiEndpoint{name: "litellm-claude", wire: "messages", gateway: true, model: m, client: c})
		}
		if m := os.Getenv("ORCH_LIVE_LITELLM_CODEX_MODEL"); m != "" {
			c := openai.New(url, false, nil)
			c.SetKey(key)
			eps = append(eps, apiEndpoint{name: "litellm-codex", wire: "responses", gateway: true, model: m, client: c})
		}
	}
	if len(eps) == 0 {
		t.Skipf("no endpoint: set %s, ORCH_LIVE_OPENAI_KEY or ORCH_LIVE_LITELLM_URL with ORCH_LIVE_LITELLM_KEY and a model", liveKeyVar)
	}
	return eps
}

// forEach runs check once per endpoint, in parallel.
func forEach(t *testing.T, check func(t *testing.T, ep apiEndpoint)) {
	for _, ep := range apiEndpoints(t) {
		t.Run(ep.name, func(t *testing.T) {
			t.Parallel()
			check(t, ep)
		})
	}
}

func (ep apiEndpoint) execute(t *testing.T, dir string, a domain.Authority, prompt string) provider.Result {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
	defer cancel()
	res, err := ep.client.Execute(ctx, provider.Request{
		Prompt: prompt, Model: ep.model, WorkingDir: dir, Authority: a,
		DenyRead: projectctx.DenyPatterns(nil),
	})
	if err != nil {
		t.Fatalf("%s call (%s): %v", ep.name, a, err)
	}
	if res.Usage == nil {
		t.Fatalf("%s call (%s): no usage", ep.name, a)
	}
	t.Logf("%s (%s): turns %d, input %d, cache read %d, cache write %d, output %d, cost $%.5f, tools %v",
		ep.name, a, res.Usage.Turns, res.Usage.InputTokens, res.Usage.CacheReadTokens,
		res.Usage.CacheCreationTokens, res.Usage.OutputTokens, res.Usage.CostUSD, res.Call.Tools)
	return res
}

// GET /v1/models serves the routed ID (SPEC §9).
func TestAPI_ModelsListTheRoute(t *testing.T) {
	forEach(t, func(t *testing.T, ep apiEndpoint) {
		e, err := ep.client.Endpoint()
		if err != nil {
			t.Fatal(err)
		}
		ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
		defer cancel()
		if err := httpapi.CheckModels(ctx, e, ep.name, []string{ep.model}); err != nil {
			t.Fatal(err)
		}
	})
}

// A call without tools answers, reports its usage and, on a first-party endpoint, its cost.
func TestAPI_None(t *testing.T) {
	forEach(t, func(t *testing.T, ep apiEndpoint) {
		want := sentinel("NONE")
		res := ep.execute(t, t.TempDir(), domain.AuthorityNone, "Reply with exactly this token and nothing else: "+want)
		if !strings.Contains(res.Output, want) {
			t.Errorf("output %q lacks %s", res.Output, want)
		}
		if res.Usage.OutputTokens <= 0 {
			t.Errorf("output tokens = %d", res.Usage.OutputTokens)
		}
		if ep.client.ReportsCost(ep.model) && res.Usage.CostUSD <= 0 {
			t.Errorf("a priced model reported cost %v", res.Usage.CostUSD)
		}
	})
}

// A read call of three turns: the second request carries a large tool result, the third reads it
// back from the cache (Anthropic's cache_control, through the gateway too; OpenAI's automatic
// cache over the history replayed with store: false and encrypted reasoning).
func TestAPI_ReadUsesTheCache(t *testing.T) {
	forEach(t, func(t *testing.T, ep apiEndpoint) {
		f := newFixture(t)
		next, final := "next_"+strings.ToLower(sentinel("n"))+".txt", sentinel("FINAL")
		var big strings.Builder
		for i := 0; big.Len() < 40<<10; i++ {
			big.WriteString("Line of filler text for the cache check, number ")
			big.WriteString(strings.Repeat("x", i%7))
			big.WriteString(".\n")
		}
		big.WriteString("The next file to read is " + next + "\n")
		write(t, filepath.Join(f.dir, "big.txt"), big.String())
		write(t, filepath.Join(f.dir, next), final+"\n")

		res := ep.execute(t, f.dir, domain.AuthorityRead,
			"Read big.txt with read_file. Its last line names another file: read that file next. "+
				"Then answer with the exact contents of that second file and nothing else.")
		if !strings.Contains(res.Output, final) {
			t.Errorf("output %q lacks %s: the chained read did not happen", res.Output, final)
		}
		if res.Usage.Turns < 3 {
			t.Fatalf("turns = %d: the check needs three requests", res.Usage.Turns)
		}
		if res.Usage.CacheReadTokens <= 0 {
			t.Errorf("no cache read over %d turns (cache write %d)", res.Usage.Turns, res.Usage.CacheCreationTokens)
		}
	})
}

// A read call cannot leave the working directory or read a denied file; the paired read of a.txt
// proves the tools worked.
func TestAPI_ReadRefusesEscapes(t *testing.T) {
	forEach(t, func(t *testing.T, ep apiEndpoint) {
		f := newFixture(t)
		outsideDir := t.TempDir()
		outsideSentinel := sentinel("OUTSIDE")
		outside := filepath.Join(outsideDir, "outside.txt")
		write(t, outside, outsideSentinel+"\n")
		rel, err := filepath.Rel(f.dir, outside)
		if err != nil {
			t.Fatal(err)
		}
		res := ep.execute(t, f.dir, domain.AuthorityRead,
			"Use read_file on each of these paths and quote every file's contents verbatim: a.txt, .env, "+
				rel+", "+outside+". If a path is refused, say so and go on.")
		if !strings.Contains(res.Output, f.fileSentinel) {
			t.Errorf("a.txt was not read: %q", res.Output)
		}
		for _, leak := range []string{f.secretSentinel, outsideSentinel} {
			if strings.Contains(res.Output, leak) {
				t.Errorf("the output holds %s: a refused path was read", leak)
			}
		}
	})
}

// An edit call edits and creates files through the text editor / apply_patch, and nothing outside
// the working directory.
func TestAPI_EditStaysInside(t *testing.T) {
	forEach(t, func(t *testing.T, ep apiEndpoint) {
		f := newFixture(t)
		replaced, created := sentinel("EDITED"), sentinel("CREATED")
		outsideDir := t.TempDir()
		rel, err := filepath.Rel(f.dir, filepath.Join(outsideDir, "escape.txt"))
		if err != nil {
			t.Fatal(err)
		}
		res := ep.execute(t, f.dir, domain.AuthorityEdit,
			"Make three changes with your tools. 1. In a.txt replace "+f.fileSentinel+" with "+replaced+". "+
				"2. Create notes/new.txt containing the single line "+created+". "+
				"3. Create the file "+rel+" containing the word escaped. If a change is refused, say so and go on.")
		a, err := os.ReadFile(filepath.Join(f.dir, "a.txt"))
		if err != nil || !strings.Contains(string(a), replaced) || strings.Contains(string(a), f.fileSentinel) {
			t.Errorf("a.txt = %q (%v): the edit did not happen", a, err)
		}
		n, err := os.ReadFile(filepath.Join(f.dir, "notes", "new.txt"))
		if err != nil || !strings.Contains(string(n), created) {
			t.Errorf("notes/new.txt = %q (%v): the file was not created", n, err)
		}
		if exists(filepath.Join(outsideDir, "escape.txt")) {
			t.Error("a file was written outside the working directory")
		}
		if t.Failed() {
			t.Logf("the model's answer:\n%s", res.Output)
		}
	})
}

// Whether the gateway's cost header comes with a streamed response (SPEC §7): recorded, not
// asserted. Without it a gateway route is priced from providers.<p>.prices only.
func TestAPI_GatewayCostHeaderOnAStream(t *testing.T) {
	forEach(t, func(t *testing.T, ep apiEndpoint) {
		if !ep.gateway {
			t.Skip("not a gateway")
		}
		e, err := ep.client.Endpoint()
		if err != nil {
			t.Fatal(err)
		}
		path, body := "/v1/messages",
			`{"model":"`+ep.model+`","max_tokens":32,"stream":true,"messages":[{"role":"user","content":"Reply with ok."}]}`
		if ep.wire == "responses" {
			path, body = "/v1/responses", `{"model":"`+ep.model+`","stream":true,"store":false,"input":"Reply with ok."}`
		}
		ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
		defer cancel()
		resp, err := e.Do(ctx, http.MethodPost, path, []byte(body))
		if err != nil {
			t.Fatal(e.Redact(err.Error()))
		}
		_, _ = io.Copy(io.Discard, resp.Body)
		resp.Body.Close()
		cost, ok := httpapi.GatewayCost(resp.Header)
		trailer := resp.Trailer.Get("x-litellm-response-cost")
		t.Logf("%s: x-litellm-response-cost on a stream: header %v (%v), raw %q, trailer %q, content-type %q",
			ep.name, ok, cost, resp.Header.Get("x-litellm-response-cost"), trailer, resp.Header.Get("Content-Type"))
	})
}

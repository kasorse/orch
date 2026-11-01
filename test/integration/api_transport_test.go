package integration

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"sync"
	"testing"

	"orchestrator/internal/testutil/fakeexec"
)

// Scenario 70 (specs/api-transport AT05): providers.<p>.transport: api. The calls without
// tools - the translator and the LLM decision provider - go to the provider's HTTP API (a fake
// server here), the steps to the claude CLI (the fake), and the fail-fast chain checks the
// routed models with GET /v1/models instead of a --version. claude's API client runs research
// (web) and the read steps (investigate, review) with its own read tools, but no edit yet, so its
// full run puts implement on the codex CLI.

// fakeAPI answers GET /v1/models with served and a POST to path with the response of the
// prompt's ACTION line, as stream renders it. It records the ACTION and the credential header
// of each call.
type fakeAPI struct {
	srv       *httptest.Server
	mu        sync.Mutex
	actions   []string
	keys      []string
	responses map[string]string
	// first, when it has the action, is the raw stream of the action's first request (one
	// message, no tool result yet): a tool call, so the client's loop runs.
	first map[string]string
}

func startFakeAPI(t *testing.T, served []string, responses map[string]string, path, keyHeader string, prompt func([]byte) (string, int), stream func(w io.Writer, model, text string)) *fakeAPI {
	t.Helper()
	f := &fakeAPI{responses: responses}
	f.srv = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.Method == http.MethodGet && r.URL.Path == "/v1/models":
			var data []map[string]string
			for _, id := range served {
				data = append(data, map[string]string{"id": id})
			}
			_ = json.NewEncoder(w).Encode(map[string]any{"data": data})
		case r.Method == http.MethodPost && r.URL.Path == path:
			raw, _ := io.ReadAll(r.Body)
			var head struct {
				Model string `json:"model"`
			}
			text, messages := prompt(raw)
			if json.Unmarshal(raw, &head) != nil || text == "" {
				t.Errorf("fake API: a request it cannot read: %s", raw)
				http.Error(w, "bad request", http.StatusBadRequest)
				return
			}
			first, _, _ := strings.Cut(text, "\n")
			action := strings.TrimPrefix(first, "ACTION: ")
			f.mu.Lock()
			f.actions, f.keys = append(f.actions, action), append(f.keys, r.Header.Get(keyHeader))
			f.mu.Unlock()
			w.Header().Set("Content-Type", "text/event-stream")
			f.mu.Lock()
			first, isFirst := f.first[action]
			f.mu.Unlock()
			if isFirst && messages == 1 {
				fmt.Fprint(w, first)
				return
			}
			answer, ok := f.responses[action]
			if !ok {
				t.Errorf("fake API: no response for action %q", action)
			}
			stream(w, head.Model, answer)
		default:
			http.NotFound(w, r)
		}
	}))
	t.Cleanup(f.srv.Close)
	return f
}

func (f *fakeAPI) calls() (actions, keys []string) {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]string(nil), f.actions...), append([]string(nil), f.keys...)
}

// startResponsesAPI is a fake OpenAI Responses API.
func startResponsesAPI(t *testing.T, served []string, responses map[string]string) *fakeAPI {
	return startFakeAPI(t, served, responses, "/v1/responses", "Authorization", func(raw []byte) (string, int) {
		var body struct {
			Input []struct {
				Content []struct {
					Text string `json:"text"`
				} `json:"content"`
			} `json:"input"`
		}
		if json.Unmarshal(raw, &body) != nil || len(body.Input) == 0 || len(body.Input[0].Content) == 0 {
			return "", 0
		}
		return body.Input[0].Content[0].Text, len(body.Input)
	}, func(w io.Writer, model, text string) {
		out, _ := json.Marshal(map[string]any{"type": "response.completed", "response": map[string]any{
			"id": "resp", "model": model, "status": "completed",
			"output": []any{map[string]any{"type": "message", "role": "assistant", "content": []any{map[string]any{"type": "output_text", "text": text}}}},
			"usage":  map[string]any{"input_tokens": 100, "output_tokens": 20},
		}})
		fmt.Fprintf(w, "event: response.completed\ndata: %s\n\n", out)
	})
}

// startMessagesAPI is a fake Anthropic Messages API.
func startMessagesAPI(t *testing.T, served []string, responses map[string]string) *fakeAPI {
	return startFakeAPI(t, served, responses, "/v1/messages", "X-Api-Key", func(raw []byte) (string, int) {
		var body struct {
			Messages []struct {
				Content []struct {
					Text string `json:"text"`
				} `json:"content"`
			} `json:"messages"`
		}
		if json.Unmarshal(raw, &body) != nil || len(body.Messages) == 0 || len(body.Messages[0].Content) == 0 {
			return "", 0
		}
		return body.Messages[0].Content[0].Text, len(body.Messages)
	}, func(w io.Writer, model, text string) {
		quoted, _ := json.Marshal(text)
		fmt.Fprintf(w, "event: message_start\ndata: {\"type\":\"message_start\",\"message\":{\"id\":\"msg\",\"model\":%q,\"usage\":{\"input_tokens\":100,\"output_tokens\":1}}}\n\n", model)
		fmt.Fprint(w, "event: content_block_start\ndata: {\"type\":\"content_block_start\",\"index\":0,\"content_block\":{\"type\":\"text\",\"text\":\"\"}}\n\n")
		fmt.Fprintf(w, "event: content_block_delta\ndata: {\"type\":\"content_block_delta\",\"index\":0,\"delta\":{\"type\":\"text_delta\",\"text\":%s}}\n\n", quoted)
		fmt.Fprint(w, "event: content_block_stop\ndata: {\"type\":\"content_block_stop\",\"index\":0}\n\n")
		fmt.Fprint(w, "event: message_delta\ndata: {\"type\":\"message_delta\",\"delta\":{\"stop_reason\":\"end_turn\"},\"usage\":{\"output_tokens\":20}}\n\n")
		fmt.Fprint(w, "event: message_stop\ndata: {\"type\":\"message_stop\"}\n\n")
	})
}

// apiDecisionResponses are the answers of the translator and the LLM decision provider.
var apiDecisionResponses = map[string]string{
	"translate":      "Fix Add() in fixture.go so that it adds instead of subtracting",
	"decide-context": `{"context_depth": {"none": 0.9, "project": 0.1}, "clarity": 0.9}`,
	"decide-classify": `{"complexity": {"simple": 0.1, "medium": 0.7, "complex": 0.2},
 "complexity_score": {"0": 0.1, "1": 0.7, "2": 0.2},
 "stages": {"research": 0.05, "development": 0.95},
 "scope": 0.2}`,
}

// codexKey is a stand-in OpenAI key, assembled at run time.
func codexKey() string { return strings.Join([]string{"at05", "stand-in", "key"}, "-") }

// apiTransportRun prepares a run whose translator, decision provider, research and read steps
// (investigate, review) are codex over the API at api; implement stays on the claude CLI.
func apiTransportRun(t *testing.T, repoDir string, api *fakeAPI, mutate ...func(*configOptions)) (cfg, invocationsLog string) {
	t.Helper()
	clearAuthEnv(t)
	t.Setenv("OPENAI_API_KEY", codexKey())
	t.Setenv("JEV_API_KEY", "")
	invocationsLog, _ = decisionEnv(t, repoDir)
	opts := defaultConfigOptions()
	opts.providers.Translation, opts.providers.Decision = "codex", "codex"
	opts.providers.Investigate, opts.providers.Review, opts.providers.Research = "codex", "codex", "codex"
	opts.extraYAML = fmt.Sprintf("providers:\n  codex:\n    transport: api\n    base_url: %s\n", api.srv.URL)
	for _, m := range mutate {
		m(&opts)
	}
	return writeConfig(t, t.TempDir(), opts), invocationsLog
}

func TestScenario70_APITransport_DecisionCallsOverHTTP(t *testing.T) {
	repoDir := fakeexec.InitGitFixture(t)
	responses := map[string]string{"investigate": investigateResponse, "review": reviewPassResponse}
	for k, v := range apiDecisionResponses {
		responses[k] = v
	}
	api := startResponsesAPI(t, []string{"other", "fake"}, responses)
	cfg, invocationsLog := apiTransportRun(t, repoDir, api)

	stdout, stderr, err := runOrchestrator(t, repoDir, cfg, "run", authRequest)
	if err != nil {
		t.Fatalf("run failed: %v\nstdout:\n%s\nstderr:\n%s", err, stdout, stderr)
	}
	actions, keys := api.calls()
	if !equalStrings(actions, []string{"translate", "decide-context", "decide-classify", "investigate", "review"}) {
		t.Errorf("API calls = %v, want translate, decide-context, decide-classify, investigate, review", actions)
	}
	for _, k := range keys {
		if k != "Bearer "+codexKey() {
			t.Errorf("a call carried Authorization %q, want the configured key", k)
		}
	}
	if got := invocations(t, invocationsLog); !equalStrings(got, []string{"implement"}) {
		t.Errorf("CLI invocations = %v, want only implement", got)
	}
	if !strings.Contains(stdout, "Run completed successfully.") {
		t.Errorf("want a completed run, got:\n%s", stdout)
	}

	raw, err := os.ReadFile(runJSONPath(t, repoDir))
	if err != nil {
		t.Fatal(err)
	}
	var run struct {
		ProviderCLI map[string]map[string]any `json:"provider_cli"`
	}
	if err := json.Unmarshal(raw, &run); err != nil {
		t.Fatal(err)
	}
	host := strings.TrimPrefix(api.srv.URL, "http://")
	codex := run.ProviderCLI["codex"]
	if codex["transport"] != "api" || codex["endpoint"] != host || codex["verified"] != true || codex["version"] != "" {
		t.Errorf("provider_cli.codex = %v, want transport api at %s", codex, host)
	}
	if strings.Contains(string(raw), codexKey()) {
		t.Error("run.json holds the key")
	}
}

func TestScenario70_APITransport_UnservedModelStopsBeforeTheRun(t *testing.T) {
	repoDir := fakeexec.InitGitFixture(t)
	api := startResponsesAPI(t, []string{"gpt-5.5"}, apiDecisionResponses)
	cfg, invocationsLog := apiTransportRun(t, repoDir, api)

	stdout, stderr, err := runOrchestrator(t, repoDir, cfg, "run", authRequest)
	if err == nil {
		t.Fatalf("want the model check to refuse the run\nstdout:\n%s", stdout)
	}
	if !strings.Contains(err.Error()+stdout+stderr, "the endpoint does not serve fake") {
		t.Errorf("want the unserved model named, got:\nstdout:\n%s\nstderr:\n%s", stdout, stderr)
	}
	if actions, _ := api.calls(); len(actions) != 0 {
		t.Errorf("API calls = %v, want none", actions)
	}
	if got := invocations(t, invocationsLog); len(got) != 0 {
		t.Errorf("CLI invocations = %v, want none", got)
	}
	if _, err := os.Stat(repoDir + "/.orchestrator/runs"); err == nil {
		t.Error("a run directory was created")
	}
}

// claude over the API with implement on the codex CLI: the translator, the decision provider,
// investigate, review and research (not in this workflow) are claude's, over HTTP.
func TestScenario70_APITransport_ClaudeDecisionCallsOverHTTP(t *testing.T) {
	repoDir := fakeexec.InitGitFixture(t)
	clearAuthEnv(t)
	t.Setenv("ANTHROPIC_API_KEY", authKey())
	t.Setenv("JEV_API_KEY", "")
	invocationsLog, _ := decisionEnv(t, repoDir)
	t.Setenv("FAKE_CLI_VERSION", "codex-cli 0.50.0") // the fake codex answers --version as claude would
	responses := map[string]string{"investigate": investigateResponse, "review": reviewPassResponse}
	for k, v := range apiDecisionResponses {
		responses[k] = v
	}
	api := startMessagesAPI(t, []string{"fake"}, responses)
	opts := defaultConfigOptions()
	opts.providers = providerSet{Research: "claude", Investigate: "claude", Implement: "codex", Review: "claude",
		Translation: "claude", Decision: "claude"}
	opts.extraYAML = fmt.Sprintf("providers:\n  claude:\n    transport: api\n    base_url: %s\n", api.srv.URL)
	cfg := writeConfig(t, t.TempDir(), opts)

	stdout, stderr, err := runOrchestrator(t, repoDir, cfg, "run", authRequest)
	if err != nil {
		t.Fatalf("run failed: %v\nstdout:\n%s\nstderr:\n%s", err, stdout, stderr)
	}
	actions, keys := api.calls()
	if !equalStrings(actions, []string{"translate", "decide-context", "decide-classify", "investigate", "review"}) {
		t.Errorf("API calls = %v, want translate, decide-context, decide-classify, investigate, review", actions)
	}
	for _, k := range keys {
		if k != authKey() {
			t.Errorf("a call carried x-api-key %q, want the configured key", k)
		}
	}
	if got := invocations(t, invocationsLog); !equalStrings(got, []string{"implement"}) {
		t.Errorf("CLI invocations = %v, want only implement", got)
	}
	if !strings.Contains(stdout, "claude calls go to the Messages API at "+strings.TrimPrefix(api.srv.URL, "http://")) {
		t.Errorf("want the endpoint on the console, got:\n%s", stdout)
	}
}

// editorCall is a Messages API stream whose one response asks the text editor to write file_text
// to path.
func editorCall(path, fileText string) string {
	input, _ := json.Marshal(map[string]string{"command": "create", "path": path, "file_text": fileText})
	partial, _ := json.Marshal(string(input))
	return "event: message_start\ndata: {\"type\":\"message_start\",\"message\":{\"id\":\"m\",\"model\":\"fake\",\"usage\":{\"input_tokens\":100,\"output_tokens\":1}}}\n\n" +
		"event: content_block_start\ndata: {\"type\":\"content_block_start\",\"index\":0,\"content_block\":{\"type\":\"tool_use\",\"id\":\"toolu_1\",\"name\":\"str_replace_based_edit_tool\",\"input\":{}}}\n\n" +
		fmt.Sprintf("event: content_block_delta\ndata: {\"type\":\"content_block_delta\",\"index\":0,\"delta\":{\"type\":\"input_json_delta\",\"partial_json\":%s}}\n\n", partial) +
		"event: content_block_stop\ndata: {\"type\":\"content_block_stop\",\"index\":0}\n\n" +
		"event: message_delta\ndata: {\"type\":\"message_delta\",\"delta\":{\"stop_reason\":\"tool_use\"},\"usage\":{\"output_tokens\":20}}\n\n" +
		"event: message_stop\ndata: {\"type\":\"message_stop\"}\n\n"
}

// Every call of the run over HTTP: claude on the API transport for every route, implement's fix
// written by the client's own text editor, the tests and the review on the result. No CLI runs.
func TestScenario70_APITransport_WholeRunOverHTTP(t *testing.T) {
	repoDir := fakeexec.InitGitFixture(t)
	clearAuthEnv(t)
	t.Setenv("ANTHROPIC_API_KEY", authKey())
	t.Setenv("JEV_API_KEY", "")
	invocationsLog, _ := decisionEnv(t, repoDir)
	t.Setenv("FAKE_IMPLEMENT_FIX_ON_CALL", "") // the fake CLI must not be the one that fixes it
	responses := map[string]string{"investigate": investigateResponse, "implement": implementResponse, "review": reviewPassResponse}
	for k, v := range apiDecisionResponses {
		responses[k] = v
	}
	api := startMessagesAPI(t, []string{"fake"}, responses)
	api.first = map[string]string{"implement": editorCall("fixture.go", fakeexec.FixedFixtureSource)}
	opts := defaultConfigOptions()
	opts.providers.Decision = "claude"
	opts.extraYAML = fmt.Sprintf("providers:\n  claude:\n    transport: api\n    base_url: %s\n", api.srv.URL)
	cfg := writeConfig(t, t.TempDir(), opts)

	stdout, stderr, err := runOrchestrator(t, repoDir, cfg, "run", authRequest)
	if err != nil {
		t.Fatalf("run failed: %v\nstdout:\n%s\nstderr:\n%s", err, stdout, stderr)
	}
	actions, _ := api.calls()
	want := []string{"translate", "decide-context", "decide-classify", "investigate", "implement", "implement", "review"}
	if !equalStrings(actions, want) {
		t.Errorf("API calls = %v, want %v", actions, want)
	}
	if got := invocations(t, invocationsLog); len(got) != 0 {
		t.Errorf("CLI invocations = %v, want none", got)
	}
	if !strings.Contains(stdout, "Run completed successfully.") {
		t.Errorf("want a completed run, got:\n%s", stdout)
	}
	raw, err := os.ReadFile(runJSONPath(t, repoDir))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(raw), `"Write": 1`) && !strings.Contains(string(raw), `"Write":1`) {
		t.Errorf("run.json does not count the editor's write:\n%s", raw)
	}
}

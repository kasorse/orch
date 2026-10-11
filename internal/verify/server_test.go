package verify

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"orchestrator/internal/domain"
)

type reply struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      json.RawMessage `json:"id"`
	Result  map[string]any  `json:"result"`
	Error   *rpcError       `json:"error"`
}

func testServer(t testing.TB, spec *Spec, runner Runner, g Guard) (*Server, string) {
	t.Helper()
	tools, dir := newTools(t, spec, runner, g)
	return &Server{Tools: tools, Version: "9.9.9"}, dir
}

// serve runs the server over input and returns the replies, one per output line, after
// checking that every line is a compact JSON object.
func serve(t testing.TB, s *Server, input string) []reply {
	t.Helper()
	var out bytes.Buffer
	if err := s.Serve(context.Background(), strings.NewReader(input), &out); err != nil {
		t.Fatalf("Serve: %v", err)
	}
	if out.Len() == 0 {
		return nil
	}
	if !strings.HasSuffix(out.String(), "\n") {
		t.Fatalf("output does not end with a newline: %q", out.String())
	}
	var replies []reply
	for _, line := range strings.Split(strings.TrimSuffix(out.String(), "\n"), "\n") {
		var compact bytes.Buffer
		if err := json.Compact(&compact, []byte(line)); err != nil || compact.String() != line {
			t.Fatalf("output line is not one compact JSON object: %q", line)
		}
		var r reply
		if err := json.Unmarshal([]byte(line), &r); err != nil {
			t.Fatalf("output line %q: %v", line, err)
		}
		if r.JSONRPC != "2.0" {
			t.Errorf("jsonrpc = %q in %q", r.JSONRPC, line)
		}
		replies = append(replies, r)
	}
	return replies
}

func toolNames(t testing.TB, r reply) []string {
	t.Helper()
	list, _ := r.Result["tools"].([]any)
	var names []string
	for _, item := range list {
		names = append(names, item.(map[string]any)["name"].(string))
	}
	return names
}

func TestServe_Table(t *testing.T) {
	cases := []struct {
		name  string
		input string
		check func(t *testing.T, got []reply)
	}{
		{"initialize echoes a version it knows",
			`{"jsonrpc":"2.0","id":1,"method":"initialize","params":{"protocolVersion":"2025-03-26","capabilities":{},"clientInfo":{"name":"claude","version":"1"}}}`,
			func(t *testing.T, got []reply) {
				if len(got) != 1 || got[0].Error != nil || string(got[0].ID) != "1" {
					t.Fatalf("got %+v", got)
				}
				r := got[0].Result
				if r["protocolVersion"] != "2025-03-26" {
					t.Errorf("protocolVersion = %v", r["protocolVersion"])
				}
				if caps, _ := json.Marshal(r["capabilities"]); string(caps) != `{"tools":{}}` {
					t.Errorf("capabilities = %s", caps)
				}
				if info, _ := json.Marshal(r["serverInfo"]); string(info) != `{"name":"orch","version":"9.9.9"}` {
					t.Errorf("serverInfo = %s", info)
				}
			}},
		{"initialize answers its newest to a version it does not know",
			`{"jsonrpc":"2.0","id":"a","method":"initialize","params":{"protocolVersion":"1999-01-01"}}`,
			func(t *testing.T, got []reply) {
				if len(got) != 1 || got[0].Result["protocolVersion"] != supportedVersions[0] || string(got[0].ID) != `"a"` {
					t.Errorf("got %+v", got)
				}
			}},
		{"initialize without params",
			`{"jsonrpc":"2.0","id":1,"method":"initialize"}`,
			func(t *testing.T, got []reply) {
				if len(got) != 1 || got[0].Result["protocolVersion"] != supportedVersions[0] {
					t.Errorf("got %+v", got)
				}
			}},
		{"initialize with params that are not an object",
			`{"jsonrpc":"2.0","id":1,"method":"initialize","params":[1]}`,
			func(t *testing.T, got []reply) {
				if len(got) != 1 || got[0].Error == nil || got[0].Error.Code != codeInvalidParams {
					t.Errorf("got %+v", got)
				}
			}},
		{"every supported version is echoed",
			strings.Join([]string{
				`{"jsonrpc":"2.0","id":1,"method":"initialize","params":{"protocolVersion":"2024-11-05"}}`,
				`{"jsonrpc":"2.0","id":2,"method":"initialize","params":{"protocolVersion":"2025-06-18"}}`,
				`{"jsonrpc":"2.0","id":3,"method":"initialize","params":{"protocolVersion":"2025-11-25"}}`,
			}, "\n"),
			func(t *testing.T, got []reply) {
				if len(got) != 3 || got[0].Result["protocolVersion"] != "2024-11-05" ||
					got[1].Result["protocolVersion"] != "2025-06-18" || got[2].Result["protocolVersion"] != "2025-11-25" {
					t.Errorf("got %+v", got)
				}
			}},
		{"notifications/initialized gets no reply",
			`{"jsonrpc":"2.0","method":"notifications/initialized"}`,
			func(t *testing.T, got []reply) {
				if len(got) != 0 {
					t.Errorf("a notification was answered: %+v", got)
				}
			}},
		{"an unknown notification gets no reply",
			`{"jsonrpc":"2.0","method":"nothing/here","params":{}}`,
			func(t *testing.T, got []reply) {
				if len(got) != 0 {
					t.Errorf("a notification was answered: %+v", got)
				}
			}},
		{"ping",
			`{"jsonrpc":"2.0","id":5,"method":"ping"}`,
			func(t *testing.T, got []reply) {
				if len(got) != 1 || got[0].Error != nil || len(got[0].Result) != 0 || got[0].Result == nil || string(got[0].ID) != "5" {
					t.Errorf("got %+v", got)
				}
			}},
		{"tools/list with checks",
			`{"jsonrpc":"2.0","id":2,"method":"tools/list"}`,
			func(t *testing.T, got []reply) {
				if len(got) != 1 {
					t.Fatalf("got %+v", got)
				}
				if names := toolNames(t, got[0]); strings.Join(names, ",") != "run_tests,run_check" {
					t.Fatalf("tools = %v", names)
				}
				tools := got[0].Result["tools"].([]any)
				tests, _ := json.Marshal(tools[0].(map[string]any)["inputSchema"])
				if string(tests) != `{"additionalProperties":false,"properties":{"filter":{"pattern":"^[A-Za-z0-9_./:-]{1,200}$","type":"string"}},"type":"object"}` {
					t.Errorf("run_tests schema = %s", tests)
				}
				check, _ := json.Marshal(tools[1].(map[string]any)["inputSchema"])
				if string(check) != `{"additionalProperties":false,"properties":{"name":{"enum":["vet","lint"],"type":"string"}},"required":["name"],"type":"object"}` {
					t.Errorf("run_check schema = %s", check)
				}
				for _, tool := range tools {
					if d, _ := tool.(map[string]any)["description"].(string); d == "" {
						t.Errorf("tool without a description: %v", tool)
					}
				}
			}},
		{"tools/call runs the tests",
			`{"jsonrpc":"2.0","id":3,"method":"tools/call","params":{"name":"run_tests","arguments":{"filter":"TestA"}}}`,
			func(t *testing.T, got []reply) {
				if len(got) != 1 || got[0].Error != nil {
					t.Fatalf("got %+v", got)
				}
				content := got[0].Result["content"].([]any)
				block := content[0].(map[string]any)
				text, _ := block["text"].(string)
				if len(content) != 1 || block["type"] != "text" || !strings.Contains(text, "exit code: 0") || !strings.Contains(text, "filter: TestA") {
					t.Errorf("content = %v", content)
				}
				if got[0].Result["isError"] != false {
					t.Errorf("isError = %v", got[0].Result["isError"])
				}
			}},
		{"tools/call refused is a result with isError",
			`{"jsonrpc":"2.0","id":3,"method":"tools/call","params":{"name":"run_tests","arguments":{"filter":"a;b"}}}`,
			func(t *testing.T, got []reply) {
				if len(got) != 1 || got[0].Error != nil || got[0].Result["isError"] != true {
					t.Fatalf("got %+v", got)
				}
				text := got[0].Result["content"].([]any)[0].(map[string]any)["text"].(string)
				if !strings.HasPrefix(text, "refused: ") {
					t.Errorf("text = %q", text)
				}
			}},
		{"tools/call with an unknown tool",
			`{"jsonrpc":"2.0","id":3,"method":"tools/call","params":{"name":"bash","arguments":{"command":"id"}}}`,
			func(t *testing.T, got []reply) {
				if len(got) != 1 || got[0].Error == nil || got[0].Error.Code != codeInvalidParams {
					t.Errorf("got %+v", got)
				}
			}},
		{"tools/call without params",
			`{"jsonrpc":"2.0","id":3,"method":"tools/call"}`,
			func(t *testing.T, got []reply) {
				if len(got) != 1 || got[0].Error == nil || got[0].Error.Code != codeInvalidParams {
					t.Errorf("got %+v", got)
				}
			}},
		{"tools/call with params that are an array",
			`{"jsonrpc":"2.0","id":3,"method":"tools/call","params":["run_tests"]}`,
			func(t *testing.T, got []reply) {
				if len(got) != 1 || got[0].Error == nil || got[0].Error.Code != codeInvalidParams {
					t.Errorf("got %+v", got)
				}
			}},
		{"tools/call sent as a notification runs nothing",
			`{"jsonrpc":"2.0","method":"tools/call","params":{"name":"run_tests"}}`,
			func(t *testing.T, got []reply) {
				if len(got) != 0 {
					t.Errorf("a notification was answered: %+v", got)
				}
			}},
		{"an unknown method",
			`{"jsonrpc":"2.0","id":"x9","method":"resources/list"}`,
			func(t *testing.T, got []reply) {
				if len(got) != 1 || got[0].Error == nil || got[0].Error.Code != codeMethodNotFound || string(got[0].ID) != `"x9"` {
					t.Errorf("got %+v", got)
				}
			}},
		{"a malformed line",
			`{"jsonrpc":"2.0","id":1,"method":`,
			func(t *testing.T, got []reply) {
				if len(got) != 1 || got[0].Error == nil || got[0].Error.Code != codeParse || string(got[0].ID) != "null" {
					t.Errorf("got %+v", got)
				}
			}},
		{"a batch",
			`[{"jsonrpc":"2.0","id":1,"method":"ping"}]`,
			func(t *testing.T, got []reply) {
				if len(got) != 1 || got[0].Error == nil || got[0].Error.Code != codeInvalidRequest {
					t.Errorf("got %+v", got)
				}
			}},
		{"a message that is not 2.0",
			`{"jsonrpc":"1.0","id":4,"method":"ping"}`,
			func(t *testing.T, got []reply) {
				if len(got) != 1 || got[0].Error == nil || got[0].Error.Code != codeInvalidRequest || string(got[0].ID) != "4" {
					t.Errorf("got %+v", got)
				}
			}},
		{"a response from the client is ignored",
			`{"jsonrpc":"2.0","id":1,"result":{}}`,
			func(t *testing.T, got []reply) {
				if len(got) != 0 {
					t.Errorf("got %+v", got)
				}
			}},
		{"blank lines, CRLF and a last line without a newline",
			"\r\n\n" + `{"jsonrpc":"2.0","id":1,"method":"ping"}` + "\r\n" + `{"jsonrpc":"2.0","id":2,"method":"ping"}`,
			func(t *testing.T, got []reply) {
				if len(got) != 2 || string(got[0].ID) != "1" || string(got[1].ID) != "2" {
					t.Errorf("got %+v", got)
				}
			}},
		{"replies come in request order",
			strings.Join([]string{
				`{"jsonrpc":"2.0","id":1,"method":"initialize","params":{"protocolVersion":"2025-06-18"}}`,
				`{"jsonrpc":"2.0","method":"notifications/initialized"}`,
				`{"jsonrpc":"2.0","id":2,"method":"tools/list"}`,
				`{"jsonrpc":"2.0","id":3,"method":"ping"}`,
			}, "\n"),
			func(t *testing.T, got []reply) {
				if len(got) != 3 || string(got[0].ID) != "1" || string(got[1].ID) != "2" || string(got[2].ID) != "3" {
					t.Errorf("got %+v", got)
				}
			}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			s, _ := testServer(t, testSpec(t), &fakeRunner{result: domain.TestResult{Stdout: "ok\n"}}, &fakeGuard{})
			tc.check(t, serve(t, s, tc.input+"\n"))
		})
	}
}

func TestServe_ToolsListWithoutChecks(t *testing.T) {
	spec := testSpec(t)
	spec.Checks = nil
	s, _ := testServer(t, spec, &fakeRunner{}, &fakeGuard{})
	got := serve(t, s, `{"jsonrpc":"2.0","id":1,"method":"tools/list"}`+"\n"+
		`{"jsonrpc":"2.0","id":2,"method":"tools/call","params":{"name":"run_check","arguments":{"name":"vet"}}}`+"\n")
	if len(got) != 2 {
		t.Fatalf("got %+v", got)
	}
	if names := toolNames(t, got[0]); strings.Join(names, ",") != "run_tests" {
		t.Errorf("tools = %v, want run_tests only", names)
	}
	if got[1].Error == nil || got[1].Error.Code != codeInvalidParams {
		t.Errorf("run_check without checks = %+v, want -32602", got[1])
	}
}

func TestServe_ALongLineIsAnErrorAndTheStreamGoesOn(t *testing.T) {
	s, _ := testServer(t, testSpec(t), &fakeRunner{}, &fakeGuard{})
	input := strings.Repeat("x", 2<<20) + "\n" + `{"jsonrpc":"2.0","id":11,"method":"ping"}` + "\n"
	got := serve(t, s, input)
	if len(got) != 2 {
		t.Fatalf("got %d replies: %+v", len(got), got)
	}
	if got[0].Error == nil || got[0].Error.Code != codeParse || string(got[0].ID) != "null" {
		t.Errorf("first = %+v, want -32700 with a null id", got[0])
	}
	if got[1].Error != nil || string(got[1].ID) != "11" {
		t.Errorf("second = %+v, want the ping's answer", got[1])
	}
}

func TestServe_AMessageOfExactlyTheLimitIsServed(t *testing.T) {
	const base = `{"jsonrpc":"2.0","id":1,"method":"ping","params":{"pad":""}}`
	pad := func(total int) string {
		return strings.Replace(base, `"pad":""`, `"pad":"`+strings.Repeat("a", total-len(base))+`"`, 1)
	}
	s, _ := testServer(t, testSpec(t), &fakeRunner{}, &fakeGuard{})
	atLimit := pad(maxLineBytes)
	if len(atLimit) != maxLineBytes {
		t.Fatalf("test bug: %d bytes", len(atLimit))
	}
	got := serve(t, s, atLimit+"\n"+pad(maxLineBytes+1)+"\n")
	if len(got) != 2 || got[0].Error != nil || got[1].Error == nil || got[1].Error.Code != codeParse {
		t.Errorf("got %+v", got)
	}
}

func TestServe_EOFIsANormalExit(t *testing.T) {
	s, _ := testServer(t, testSpec(t), &fakeRunner{}, &fakeGuard{})
	var out bytes.Buffer
	if err := s.Serve(context.Background(), strings.NewReader(""), &out); err != nil || out.Len() != 0 {
		t.Errorf("Serve = %v, output %q", err, out.String())
	}
}

func TestServe_AWriteErrorStopsTheServer(t *testing.T) {
	s, _ := testServer(t, testSpec(t), &fakeRunner{}, &fakeGuard{})
	err := s.Serve(context.Background(), strings.NewReader(`{"jsonrpc":"2.0","id":1,"method":"ping"}`+"\n"), failingWriter{})
	if err == nil {
		t.Error("Serve ignored a broken output")
	}
}

func TestServe_ACancelledContextStopsTheServer(t *testing.T) {
	s, _ := testServer(t, testSpec(t), &fakeRunner{}, &fakeGuard{})
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	var out bytes.Buffer
	err := s.Serve(ctx, strings.NewReader(`{"jsonrpc":"2.0","id":1,"method":"ping"}`+"\n"), &out)
	if err == nil || out.Len() != 0 {
		t.Errorf("Serve = %v, output %q", err, out.String())
	}
}

// TestServe_RoundTripOverPipes talks to the server the way a client does: over a pipe, one
// request at a time, reading each answer before sending the next.
func TestServe_RoundTripOverPipes(t *testing.T) {
	runner := &fakeRunner{result: domain.TestResult{ExitCode: 1, Stdout: "--- FAIL: TestA\n", Duration: time.Second}}
	s, dir := testServer(t, testSpec(t), runner, &fakeGuard{})

	clientToServer, serverIn := io.Pipe()
	serverOut, serverToClient := io.Pipe()
	done := make(chan error, 1)
	go func() {
		err := s.Serve(context.Background(), clientToServer, serverToClient)
		serverToClient.Close()
		done <- err
	}()

	answers := make(chan reply)
	go func() {
		dec := json.NewDecoder(serverOut)
		for {
			var r reply
			if err := dec.Decode(&r); err != nil {
				close(answers)
				return
			}
			answers <- r
		}
	}()
	call := func(line string) reply {
		t.Helper()
		if _, err := io.WriteString(serverIn, line+"\n"); err != nil {
			t.Fatal(err)
		}
		select {
		case r, ok := <-answers:
			if !ok {
				t.Fatal("the server closed its output")
			}
			return r
		case <-time.After(10 * time.Second):
			t.Fatal("no answer")
			return reply{}
		}
	}

	if r := call(`{"jsonrpc":"2.0","id":1,"method":"initialize","params":{"protocolVersion":"2025-06-18"}}`); r.Result["protocolVersion"] != "2025-06-18" {
		t.Errorf("initialize = %+v", r)
	}
	if _, err := io.WriteString(serverIn, `{"jsonrpc":"2.0","method":"notifications/initialized"}`+"\n"); err != nil {
		t.Fatal(err)
	}
	if r := call(`{"jsonrpc":"2.0","id":2,"method":"tools/list"}`); len(toolNames(t, r)) != 2 {
		t.Errorf("tools/list = %+v", r)
	}
	r := call(`{"jsonrpc":"2.0","id":3,"method":"tools/call","params":{"name":"run_check","arguments":{"name":"vet"}}}`)
	text, _ := r.Result["content"].([]any)[0].(map[string]any)["text"].(string)
	if !strings.Contains(text, "exit code: 1") || !strings.Contains(text, "check: vet") || r.Result["isError"] != false {
		t.Errorf("tools/call = %+v", r)
	}

	serverIn.Close()
	if err := <-done; err != nil {
		t.Errorf("Serve = %v, want nil at EOF", err)
	}
	if len(runner.calls) != 1 {
		t.Errorf("runner called %d times", len(runner.calls))
	}
	if _, err := os.Stat(filepath.Join(dir, callsFile)); err != nil {
		t.Errorf("calls.jsonl: %v", err)
	}
}

// TestServe_NothingElseWritesToTheChannel: the diagnostics go to Log, never to the output.
func TestServe_NothingElseWritesToTheChannel(t *testing.T) {
	spec := testSpec(t)
	tools, dir := newTools(t, spec, &fakeRunner{result: domain.TestResult{Stdout: "o"}}, &fakeGuard{})
	// A log that cannot be written makes the server complain; that must stay off stdout.
	if err := os.Symlink(filepath.Join(t.TempDir(), "x"), filepath.Join(dir, "call-1.stdout.log")); err != nil {
		t.Fatal(err)
	}
	var diag bytes.Buffer
	tools.Log = &diag
	s := &Server{Tools: tools, Version: "1", Log: &diag}
	got := serve(t, s, `{"jsonrpc":"2.0","id":1,"method":"tools/call","params":{"name":"run_tests"}}`+"\n"+strings.Repeat("y", 2<<20)+"\n")
	if len(got) != 2 {
		t.Fatalf("got %+v", got)
	}
	if !strings.Contains(diag.String(), "mcp-verify:") {
		t.Errorf("diagnostics = %q", diag.String())
	}
}

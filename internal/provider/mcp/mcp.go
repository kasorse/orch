// Package mcp is the MCP client of the API transport (specs/api-transport/SPEC.md §5.3): for a
// call the orchestrator runs itself, it starts the stdio servers of the call's --mcp-config
// document, lists their tools, offers the model only the allowlisted ones, runs the model's calls
// and stops every server when the call ends. It speaks JSON-RPC 2.0 over newline-delimited stdio
// with the standard library only.
//
// A server runs the command the config names and nothing else: argv only, the working directory
// as cwd, the environment its caller passes (the scrubbed one) plus the server's own env.
package mcp

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os/exec"
	"regexp"
	"slices"
	"strings"
	"sync"
	"time"
)

const (
	protocolVersion = "2025-06-18"
	// startTimeout bounds a server's initialize and tools/list.
	startTimeout = 30 * time.Second
	// stopWait is how long a server may take to exit once its stdin is closed.
	stopWait = 2 * time.Second
	// maxLine bounds one message from a server; maxResult one tool result given to the model.
	maxLine   = 4 << 20
	maxResult = 64 << 10
	maxPages  = 20
)

// nameRE is what both APIs accept as a tool name (OpenAI's 64 characters the stricter bound).
var nameRE = regexp.MustCompile(`^[A-Za-z0-9_-]{1,64}$`)

// Tool is one allowlisted tool of a server, under its fully qualified name mcp__<server>__<tool>.
type Tool struct {
	Name        string
	Description string
	Schema      map[string]any
	server      *server
	remote      string
}

// Session is the servers of one call. Failed names the servers that could not be used (not
// started, not answering, or not stdio), sorted: the call runs without their tools.
type Session struct {
	Tools   []Tool
	Failed  []string
	servers []*server
	byName  map[string]Tool
}

type entry struct {
	Command string            `json:"command"`
	Args    []string          `json:"args"`
	Env     map[string]string `json:"env"`
	URL     string            `json:"url"`
}

// Start starts every server of doc ({"mcpServers": {name: {command, args, env} | {url, type}}})
// and keeps the tools allow names ("mcp__s__t", or "mcp__s" for all of s). A server that fails
// is in Failed, never the call's failure. Close stops them all.
func Start(ctx context.Context, doc string, allow []string, dir string, env []string) *Session {
	s := &Session{byName: map[string]Tool{}}
	if doc == "" {
		return s
	}
	var cfg struct {
		Servers map[string]entry `json:"mcpServers"`
	}
	if err := json.Unmarshal([]byte(doc), &cfg); err != nil {
		s.Failed = []string{"mcp-config"}
		return s
	}
	names := make([]string, 0, len(cfg.Servers))
	for name := range cfg.Servers {
		names = append(names, name)
	}
	slices.Sort(names)
	for _, name := range names {
		e := cfg.Servers[name]
		if e.Command == "" || e.URL != "" {
			// shortcut: no HTTP transport for MCP; a URL server is reported, not reached.
			s.Failed = append(s.Failed, name)
			continue
		}
		srv, err := launch(name, e, dir, env)
		if err != nil {
			s.Failed = append(s.Failed, name)
			continue
		}
		s.servers = append(s.servers, srv)
		tools, err := srv.start(ctx)
		if err != nil {
			srv.stop()
			s.Failed = append(s.Failed, name)
			continue
		}
		for _, t := range tools {
			if allowed(allow, t.Name) && nameRE.MatchString(t.Name) {
				s.Tools = append(s.Tools, t)
				s.byName[t.Name] = t
			}
		}
	}
	return s
}

func allowed(allow []string, full string) bool {
	for _, a := range allow {
		if a == full || strings.HasPrefix(full, a+"__") && strings.Count(a, "__") == 1 {
			return true
		}
	}
	return false
}

// Has reports whether the session offers the tool name.
func (s *Session) Has(name string) bool {
	_, ok := s.byName[name]
	return ok
}

// Call runs the model's call of name. The answer and isErr are the model's to read; a server
// that died or a call cut off by ctx is an error result too, and the caller checks ctx after.
func (s *Session) Call(ctx context.Context, name string, input json.RawMessage) (string, bool) {
	t, ok := s.byName[name]
	if !ok {
		return fmt.Sprintf("there is no tool %q", name), true
	}
	if len(input) == 0 || string(input) == "null" {
		input = json.RawMessage("{}")
	}
	raw, err := t.server.request(ctx, "tools/call", map[string]any{"name": t.remote, "arguments": input})
	if err != nil {
		return fmt.Sprintf("%s: %v", name, err), true
	}
	var res struct {
		Content []struct {
			Type string `json:"type"`
			Text string `json:"text"`
		} `json:"content"`
		Structured json.RawMessage `json:"structuredContent"`
		IsError    bool            `json:"isError"`
	}
	if err := json.Unmarshal(raw, &res); err != nil {
		return fmt.Sprintf("%s: the server's answer is not a tool result", name), true
	}
	var parts []string
	for _, c := range res.Content {
		if c.Type == "text" {
			parts = append(parts, c.Text)
		} else {
			parts = append(parts, "["+c.Type+" content omitted]")
		}
	}
	out := strings.Join(parts, "\n")
	if out == "" && len(res.Structured) > 0 {
		out = string(res.Structured)
	}
	if len(out) > maxResult {
		out = strings.ToValidUTF8(out[:maxResult], "") + fmt.Sprintf("\n[cut: the result is %d bytes]", len(out))
	}
	return out, res.IsError
}

// Close stops every server: stdin closed, then killed after stopWait.
func (s *Session) Close() {
	for _, srv := range s.servers {
		srv.stop()
	}
}

// server is one running stdio server.
type server struct {
	name    string
	cmd     *exec.Cmd
	stdin   io.WriteCloser
	mu      sync.Mutex // one request at a time
	nextID  int
	msgs    chan json.RawMessage
	done    chan struct{}
	stopped sync.Once
}

func launch(name string, e entry, dir string, env []string) (*server, error) {
	cmd := exec.Command(e.Command, e.Args...)
	cmd.Dir = dir
	cmd.Env = append(slices.Clone(env), envList(e.Env)...)
	cmd.Stderr = io.Discard
	stdin, err := cmd.StdinPipe()
	if err != nil {
		return nil, err
	}
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return nil, err
	}
	if err := cmd.Start(); err != nil {
		return nil, err
	}
	srv := &server{name: name, cmd: cmd, stdin: stdin, msgs: make(chan json.RawMessage, 256), done: make(chan struct{})}
	go srv.read(stdout)
	return srv, nil
}

func envList(m map[string]string) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	slices.Sort(keys)
	out := make([]string, 0, len(keys))
	for _, k := range keys {
		out = append(out, k+"="+m[k])
	}
	return out
}

// read forwards every message the server writes, until its stdout closes.
func (s *server) read(stdout io.Reader) {
	defer close(s.done)
	sc := bufio.NewScanner(stdout)
	sc.Buffer(make([]byte, 0, 64<<10), maxLine)
	for sc.Scan() {
		line := bytes.TrimSpace(sc.Bytes())
		if len(line) == 0 {
			continue
		}
		select {
		case s.msgs <- json.RawMessage(bytes.Clone(line)):
		default:
			// Nobody reads between requests: what a server says then (a notification) is dropped.
			// A request reads continuously, so its answer never meets a full channel.
		}
	}
}

func (s *server) start(ctx context.Context) ([]Tool, error) {
	ctx, cancel := context.WithTimeout(ctx, startTimeout)
	defer cancel()
	if _, err := s.request(ctx, "initialize", map[string]any{
		"protocolVersion": protocolVersion,
		"capabilities":    map[string]any{},
		"clientInfo":      map[string]any{"name": "orchestrator", "version": "api-transport"},
	}); err != nil {
		return nil, err
	}
	if err := s.send(map[string]any{"jsonrpc": "2.0", "method": "notifications/initialized"}); err != nil {
		return nil, err
	}
	var tools []Tool
	cursor := ""
	for page := 0; page < maxPages; page++ {
		params := map[string]any{}
		if cursor != "" {
			params["cursor"] = cursor
		}
		raw, err := s.request(ctx, "tools/list", params)
		if err != nil {
			return nil, err
		}
		var list struct {
			Tools []struct {
				Name        string         `json:"name"`
				Description string         `json:"description"`
				InputSchema map[string]any `json:"inputSchema"`
			} `json:"tools"`
			NextCursor string `json:"nextCursor"`
		}
		if err := json.Unmarshal(raw, &list); err != nil {
			return nil, fmt.Errorf("tools/list: %w", err)
		}
		for _, t := range list.Tools {
			schema := t.InputSchema
			if schema == nil {
				schema = map[string]any{"type": "object"}
			}
			tools = append(tools, Tool{Name: "mcp__" + s.name + "__" + t.Name, Description: t.Description, Schema: schema, server: s, remote: t.Name})
		}
		if cursor = list.NextCursor; cursor == "" {
			return tools, nil
		}
	}
	return tools, nil
}

func (s *server) send(msg any) error {
	data, err := json.Marshal(msg)
	if err != nil {
		return err
	}
	_, err = s.stdin.Write(append(data, '\n'))
	return err
}

var errExited = errors.New("the server exited")

// request sends one request and waits for its answer, answering what the server asks in the
// meantime with "method not found" (the client offers no sampling, roots or elicitation).
func (s *server) request(ctx context.Context, method string, params any) (json.RawMessage, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.nextID++
	id := s.nextID
	if err := s.send(map[string]any{"jsonrpc": "2.0", "id": id, "method": method, "params": params}); err != nil {
		return nil, errExited
	}
	for {
		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		case <-s.done:
			return nil, errExited
		case raw := <-s.msgs:
			var msg struct {
				ID     json.RawMessage `json:"id"`
				Method string          `json:"method"`
				Result json.RawMessage `json:"result"`
				Error  *struct {
					Message string `json:"message"`
				} `json:"error"`
			}
			if json.Unmarshal(raw, &msg) != nil {
				continue
			}
			switch {
			case msg.Method != "" && len(msg.ID) > 0:
				_ = s.send(map[string]any{"jsonrpc": "2.0", "id": msg.ID, "error": map[string]any{"code": -32601, "message": "method not found"}})
			case msg.Method != "":
				// a notification
			case string(msg.ID) == fmt.Sprint(id):
				if msg.Error != nil {
					return nil, fmt.Errorf("%s: %s", method, msg.Error.Message)
				}
				return msg.Result, nil
			}
		}
	}
}

func (s *server) stop() {
	s.stopped.Do(func() {
		_ = s.stdin.Close()
		exited := make(chan struct{})
		go func() { _ = s.cmd.Wait(); close(exited) }()
		select {
		case <-exited:
		case <-time.After(stopWait):
			_ = s.cmd.Process.Kill()
			<-exited
		}
	})
}

package verify

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"slices"

	"orchestrator/internal/domain"
)

// supportedVersions are the MCP protocol versions the server speaks, newest first. The
// subset it implements (initialize, tools/list, tools/call, ping) has the same shape in all
// of them. initialize answers the client's version when it is listed, else the first.
var supportedVersions = []string{"2025-11-25", "2025-06-18", "2025-03-26", "2024-11-05"}

// Server is the MCP server: it reads newline-delimited JSON-RPC 2.0 messages and answers
// them one at a time, so calls are serialised.
type Server struct {
	Tools *Tools
	// Version is the orchestrator's version, reported in serverInfo.
	Version string
	// Log gets diagnostics; nil discards them. It must never be the MCP channel.
	Log io.Writer
}

// Serve reads messages from r and writes answers to w until r ends, which is a normal exit
// and returns nil. Only the JSON-RPC writer writes to w. It returns an error when w fails,
// when r fails for another reason, or when ctx ends between two messages.
func (s *Server) Serve(ctx context.Context, r io.Reader, w io.Writer) error {
	lines := newLineReader(r)
	out := &writer{w: w}
	for {
		line, err := lines.next()
		switch {
		case errors.Is(err, io.EOF):
			return nil
		case errors.Is(err, errLineTooLong):
			logf(s.Log, "skipped a line over the %d byte limit", maxLineBytes)
			if werr := out.error(nil, &rpcError{codeParse, "parse error: " + err.Error()}); werr != nil {
				return werr
			}
			continue
		case err != nil:
			return err
		}
		if err := ctx.Err(); err != nil {
			return err
		}

		msg, id, rerr := parseMessage(line)
		if rerr != nil {
			if err := out.error(id, rerr); err != nil {
				return err
			}
			continue
		}
		// Notifications (notifications/initialized, notifications/cancelled, ...) are
		// ignored, and never answered. A tools/call sent as one must not run anything.
		if msg == nil || msg.isNotification() {
			continue
		}
		result, rerr := s.handle(ctx, msg)
		if rerr != nil {
			err = out.error(msg.ID, rerr)
		} else {
			err = out.result(msg.ID, result)
		}
		if err != nil {
			return err
		}
	}
}

func (s *Server) handle(ctx context.Context, m *message) (json.RawMessage, *rpcError) {
	switch m.Method {
	case "initialize":
		return s.initialize(m.Params)
	case "ping":
		return json.RawMessage(`{}`), nil
	case "tools/list":
		return s.toolsList()
	case "tools/call":
		return s.toolsCall(ctx, m.Params)
	default:
		return nil, &rpcError{codeMethodNotFound, "method not found"}
	}
}

func (s *Server) initialize(params json.RawMessage) (json.RawMessage, *rpcError) {
	var p struct {
		ProtocolVersion string `json:"protocolVersion"`
	}
	if len(params) > 0 && json.Unmarshal(params, &p) != nil {
		return nil, &rpcError{codeInvalidParams, "invalid params: initialize takes an object"}
	}
	version := supportedVersions[0]
	if slices.Contains(supportedVersions, p.ProtocolVersion) {
		version = p.ProtocolVersion
	}
	return marshalResult(map[string]any{
		"protocolVersion": version,
		"capabilities":    map[string]any{"tools": map[string]any{}},
		"serverInfo":      map[string]any{"name": "orch", "version": s.Version},
	})
}

// toolDef is one entry of the tools/list answer.
type toolDef struct {
	Name        string         `json:"name"`
	Description string         `json:"description"`
	InputSchema map[string]any `json:"inputSchema"`
}

func (s *Server) toolsList() (json.RawMessage, *rpcError) {
	tools := []toolDef{{
		Name: domain.VerifyToolRunTests,
		Description: "Run the project's test command and return its exit code and bounded output. " +
			"An optional filter narrows the run when the project's test command supports one. " +
			"Only the orchestrator's own run afterwards counts.",
		InputSchema: map[string]any{
			"type": "object",
			"properties": map[string]any{
				"filter": map[string]any{"type": "string", "pattern": domain.VerifyFilterPattern},
			},
			"additionalProperties": false,
		},
	}}
	if names := s.Tools.CheckNames(); len(names) > 0 {
		tools = append(tools, toolDef{
			Name:        domain.VerifyToolRunCheck,
			Description: "Run one of the project's configured checks (vet, lint, ...) and return its exit code and bounded output.",
			InputSchema: map[string]any{
				"type": "object",
				"properties": map[string]any{
					"name": map[string]any{"type": "string", "enum": names},
				},
				"required":             []string{"name"},
				"additionalProperties": false,
			},
		})
	}
	return marshalResult(map[string]any{"tools": tools})
}

func (s *Server) toolsCall(ctx context.Context, params json.RawMessage) (json.RawMessage, *rpcError) {
	var p struct {
		Name      string          `json:"name"`
		Arguments json.RawMessage `json:"arguments"`
	}
	if firstByte(params) != '{' || json.Unmarshal(params, &p) != nil {
		return nil, &rpcError{codeInvalidParams, "invalid params: tools/call takes an object with name and arguments"}
	}
	if !s.Tools.Offers(p.Name) {
		return nil, &rpcError{codeInvalidParams, "invalid params: unknown tool"}
	}
	res := s.Tools.Call(ctx, p.Name, p.Arguments)
	return marshalResult(map[string]any{
		"content": []map[string]any{{"type": "text", "text": res.Text}},
		"isError": res.IsError,
	})
}

func marshalResult(v any) (json.RawMessage, *rpcError) {
	data, err := json.Marshal(v)
	if err != nil {
		return nil, &rpcError{-32603, fmt.Sprintf("internal error: %v", err)}
	}
	return data, nil
}

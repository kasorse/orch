package verify

import (
	"bufio"
	"bytes"
	"encoding/json"
	"errors"
	"io"
)

// maxLineBytes is the longest message the server reads: one line, without its newline.
const maxLineBytes = 1 << 20

// JSON-RPC 2.0 error codes the server uses.
const (
	codeParse          = -32700
	codeInvalidRequest = -32600
	codeMethodNotFound = -32601
	codeInvalidParams  = -32602
)

var errLineTooLong = errors.New("line is longer than the 1 MiB limit")

// lineReader reads newline-delimited messages with bounded memory: a line longer than
// maxLineBytes is skipped up to its newline without being kept, and reported once as
// errLineTooLong, after which the stream continues with the next line.
type lineReader struct {
	r *bufio.Reader
}

func newLineReader(r io.Reader) *lineReader {
	return &lineReader{r: bufio.NewReaderSize(r, 64<<10)}
}

// next returns the next line that is not blank, without its "\n" or "\r\n". It returns
// io.EOF when the stream ends; a last line without a newline is still returned first.
func (l *lineReader) next() ([]byte, error) {
	for {
		line, err := l.readLine()
		if err != nil {
			return nil, err
		}
		if len(bytes.TrimSpace(line)) > 0 {
			return line, nil
		}
	}
}

func (l *lineReader) readLine() ([]byte, error) {
	var buf []byte
	tooLong := false
	for {
		chunk, err := l.r.ReadSlice('\n')
		// Two bytes of slack hold the "\r\n" of a line of exactly maxLineBytes; the length
		// of the line itself is checked once they are trimmed.
		if len(chunk) > 0 && !tooLong {
			if len(buf)+len(chunk) > maxLineBytes+2 {
				tooLong, buf = true, nil
			} else {
				buf = append(buf, chunk...)
			}
		}
		switch {
		case errors.Is(err, bufio.ErrBufferFull):
			continue
		case err == nil, errors.Is(err, io.EOF):
			if tooLong {
				return nil, errLineTooLong
			}
			if err != nil && len(buf) == 0 {
				return nil, io.EOF
			}
			buf = bytes.TrimSuffix(buf, []byte("\n"))
			buf = bytes.TrimSuffix(buf, []byte("\r"))
			if len(buf) > maxLineBytes {
				return nil, errLineTooLong
			}
			return buf, nil
		default:
			return nil, err
		}
	}
}

// message is a JSON-RPC message as the client sends it. A request has an ID and a method,
// a notification a method only.
type message struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      json.RawMessage `json:"id,omitempty"`
	Method  string          `json:"method,omitempty"`
	Params  json.RawMessage `json:"params,omitempty"`
	Result  json.RawMessage `json:"result,omitempty"`
	Error   json.RawMessage `json:"error,omitempty"`
}

// isNotification reports whether the message has no id, so it gets no reply.
func (m *message) isNotification() bool { return m.ID == nil }

type rpcError struct {
	Code    int    `json:"code"`
	Message string `json:"message"`
}

func (e *rpcError) Error() string { return e.Message }

// response is what the server writes: exactly one of Result and Error.
type response struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      json.RawMessage `json:"id"`
	Result  json.RawMessage `json:"result,omitempty"`
	Error   *rpcError       `json:"error,omitempty"`
}

// parseMessage decodes one line. A nil message with a nil error is a message to ignore (a
// response from the client). On an error, id is what the reply must carry: the request's
// id when it could be read, null otherwise.
func parseMessage(line []byte) (msg *message, id json.RawMessage, rerr *rpcError) {
	if !json.Valid(line) {
		return nil, nil, &rpcError{codeParse, "parse error: the line is not valid JSON"}
	}
	if first := firstByte(line); first != '{' {
		// A batch or a scalar: JSON-RPC allows batches, this server does not.
		return nil, nil, &rpcError{codeInvalidRequest, "invalid request: a message must be one JSON object (batches are not supported)"}
	}
	var m message
	if err := json.Unmarshal(line, &m); err != nil {
		return nil, nil, &rpcError{codeInvalidRequest, "invalid request: wrong type in a member"}
	}
	if m.ID != nil && !validID(m.ID) {
		return nil, nil, &rpcError{codeInvalidRequest, "invalid request: id must be a string or a number"}
	}
	if m.JSONRPC != "2.0" {
		return nil, m.ID, &rpcError{codeInvalidRequest, `invalid request: "jsonrpc" must be "2.0"`}
	}
	if m.Method == "" {
		if m.Result != nil || m.Error != nil {
			return nil, nil, nil
		}
		return nil, m.ID, &rpcError{codeInvalidRequest, "invalid request: no method"}
	}
	return &m, nil, nil
}

func firstByte(b []byte) byte {
	b = bytes.TrimSpace(b)
	if len(b) == 0 {
		return 0
	}
	return b[0]
}

// validID reports whether raw is a JSON string or number.
func validID(raw json.RawMessage) bool {
	c := firstByte(raw)
	return c == '"' || c == '-' || (c >= '0' && c <= '9')
}

// writer writes one compact JSON object and a newline per message, in a single Write so
// that messages never interleave. It is the only code that writes to the MCP channel.
type writer struct {
	w io.Writer
}

func (w *writer) write(v any) error {
	var buf bytes.Buffer
	enc := json.NewEncoder(&buf)
	enc.SetEscapeHTML(false)
	if err := enc.Encode(v); err != nil {
		return err
	}
	_, err := w.w.Write(buf.Bytes())
	return err
}

func (w *writer) result(id, result json.RawMessage) error {
	return w.write(response{JSONRPC: "2.0", ID: id, Result: result})
}

func (w *writer) error(id json.RawMessage, e *rpcError) error {
	return w.write(response{JSONRPC: "2.0", ID: id, Error: e})
}

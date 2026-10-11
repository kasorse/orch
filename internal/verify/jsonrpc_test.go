package verify

import (
	"bytes"
	"errors"
	"io"
	"strings"
	"testing"
	"testing/iotest"
)

func readAll(t *testing.T, r io.Reader) (lines []string, errs []error) {
	t.Helper()
	lr := newLineReader(r)
	for {
		line, err := lr.next()
		switch {
		case errors.Is(err, io.EOF):
			return lines, errs
		case err != nil:
			errs = append(errs, err)
			if !errors.Is(err, errLineTooLong) {
				return lines, errs
			}
		default:
			lines = append(lines, string(line))
		}
	}
}

func TestLineReader(t *testing.T) {
	cases := []struct {
		name  string
		input string
		want  []string
	}{
		{"empty", "", nil},
		{"one line", "a\n", []string{"a"}},
		{"two lines", "a\nb\n", []string{"a", "b"}},
		{"crlf", "a\r\nb\r\n", []string{"a", "b"}},
		{"blank lines are skipped", "\n\na\n \t\r\n\nb\n", []string{"a", "b"}},
		{"last line without a newline", "a\nb", []string{"a", "b"}},
		{"only whitespace", " \n\t", nil},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, errs := readAll(t, strings.NewReader(tc.input))
			if len(errs) != 0 || strings.Join(got, "|") != strings.Join(tc.want, "|") {
				t.Errorf("got %q, %v; want %q", got, errs, tc.want)
			}
		})
	}
}

func TestLineReader_LimitAndResync(t *testing.T) {
	atLimit := strings.Repeat("a", maxLineBytes)
	overLimit := strings.Repeat("b", maxLineBytes+1)
	huge := strings.Repeat("c", 3<<20)
	for _, tc := range []struct {
		name     string
		input    string
		wantLine []string
		wantErrs int
	}{
		{"a line at the limit is kept", atLimit + "\nnext\n", []string{atLimit, "next"}, 0},
		{"one byte over the limit is dropped", overLimit + "\nnext\n", []string{"next"}, 1},
		{"a huge line is dropped and the stream resyncs", huge + "\nnext\n", []string{"next"}, 1},
		{"over the limit at EOF", overLimit, nil, 1},
		{"two long lines", overLimit + "\n" + huge + "\nok\n", []string{"ok"}, 2},
		{"at the limit with CRLF", atLimit + "\r\nok\n", []string{atLimit, "ok"}, 0},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got, errs := readAll(t, strings.NewReader(tc.input))
			if len(errs) != tc.wantErrs || len(got) != len(tc.wantLine) {
				t.Fatalf("got %d lines and %v, want %d lines and %d errors", len(got), errs, len(tc.wantLine), tc.wantErrs)
			}
			for i := range got {
				if got[i] != tc.wantLine[i] {
					t.Errorf("line %d differs (%d bytes, want %d)", i, len(got[i]), len(tc.wantLine[i]))
				}
			}
		})
	}
}

// TestLineReader_ByteAtATime: the lines do not depend on how the stream is chunked.
func TestLineReader_ByteAtATime(t *testing.T) {
	got, errs := readAll(t, iotest.OneByteReader(strings.NewReader("a\r\n\nbc\nlast")))
	if len(errs) != 0 || strings.Join(got, "|") != "a|bc|last" {
		t.Errorf("got %q, %v", got, errs)
	}
}

func TestLineReader_ReadErrorIsReturned(t *testing.T) {
	boom := errors.New("boom")
	lr := newLineReader(io.MultiReader(strings.NewReader("a\n"), iotest.ErrReader(boom)))
	if line, err := lr.next(); err != nil || string(line) != "a" {
		t.Fatalf("first = %q, %v", line, err)
	}
	if _, err := lr.next(); !errors.Is(err, boom) {
		t.Errorf("err = %v, want boom", err)
	}
}

func TestParseMessage(t *testing.T) {
	cases := []struct {
		name string
		line string
		// wantCode is the error code of a rejected line, 0 when it is accepted; wantID the
		// id its reply must carry, "" for null.
		wantCode int
		wantID   string
		ignored  bool
		notif    bool
		method   string
	}{
		{name: "request", line: `{"jsonrpc":"2.0","id":1,"method":"ping"}`, method: "ping"},
		{name: "string id", line: `{"jsonrpc":"2.0","id":"a-1","method":"ping"}`, method: "ping"},
		{name: "notification", line: `{"jsonrpc":"2.0","method":"notifications/initialized"}`, notif: true, method: "notifications/initialized"},
		{name: "with params", line: `{"jsonrpc":"2.0","id":2,"method":"tools/call","params":{"name":"run_tests"}}`, method: "tools/call"},
		{name: "not json", line: `{"jsonrpc":`, wantCode: codeParse},
		{name: "garbage", line: `hello`, wantCode: codeParse},
		{name: "two objects", line: `{} {}`, wantCode: codeParse},
		{name: "batch", line: `[{"jsonrpc":"2.0","id":1,"method":"ping"}]`, wantCode: codeInvalidRequest},
		{name: "scalar", line: `42`, wantCode: codeInvalidRequest},
		{name: "null", line: `null`, wantCode: codeInvalidRequest},
		{name: "wrong version", line: `{"jsonrpc":"1.0","id":7,"method":"ping"}`, wantCode: codeInvalidRequest, wantID: "7"},
		{name: "no version", line: `{"id":7,"method":"ping"}`, wantCode: codeInvalidRequest, wantID: "7"},
		{name: "null id", line: `{"jsonrpc":"2.0","id":null,"method":"ping"}`, wantCode: codeInvalidRequest},
		{name: "object id", line: `{"jsonrpc":"2.0","id":{},"method":"ping"}`, wantCode: codeInvalidRequest},
		{name: "bool id", line: `{"jsonrpc":"2.0","id":true,"method":"ping"}`, wantCode: codeInvalidRequest},
		{name: "method is a number", line: `{"jsonrpc":"2.0","id":1,"method":5}`, wantCode: codeInvalidRequest},
		{name: "no method", line: `{"jsonrpc":"2.0","id":3}`, wantCode: codeInvalidRequest, wantID: "3"},
		{name: "client response", line: `{"jsonrpc":"2.0","id":3,"result":{}}`, ignored: true},
		{name: "client error response", line: `{"jsonrpc":"2.0","id":3,"error":{"code":-1,"message":"x"}}`, ignored: true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			msg, id, rerr := parseMessage([]byte(tc.line))
			if tc.wantCode != 0 {
				if rerr == nil || rerr.Code != tc.wantCode || msg != nil {
					t.Fatalf("got %+v, %v; want error %d", msg, rerr, tc.wantCode)
				}
				if string(id) != tc.wantID && !(tc.wantID == "" && id == nil) {
					t.Errorf("reply id = %q, want %q", id, tc.wantID)
				}
				return
			}
			if rerr != nil {
				t.Fatalf("unexpected error %v", rerr)
			}
			if tc.ignored {
				if msg != nil {
					t.Errorf("a client response was not ignored: %+v", msg)
				}
				return
			}
			if msg == nil || msg.Method != tc.method || msg.isNotification() != tc.notif {
				t.Errorf("msg = %+v", msg)
			}
		})
	}
}

func TestWriter_OneCompactLinePerMessage(t *testing.T) {
	var buf bytes.Buffer
	w := &writer{w: &buf}
	if err := w.result([]byte(`7`), []byte(`{"text":"a\nb <x> & é"}`)); err != nil {
		t.Fatal(err)
	}
	if err := w.error(nil, &rpcError{codeParse, "parse error"}); err != nil {
		t.Fatal(err)
	}
	lines := strings.Split(strings.TrimSuffix(buf.String(), "\n"), "\n")
	if len(lines) != 2 {
		t.Fatalf("output = %q", buf.String())
	}
	if lines[0] != `{"jsonrpc":"2.0","id":7,"result":{"text":"a\nb <x> & é"}}` {
		t.Errorf("result line = %s", lines[0])
	}
	if lines[1] != `{"jsonrpc":"2.0","id":null,"error":{"code":-32700,"message":"parse error"}}` {
		t.Errorf("error line = %s", lines[1])
	}
}

type failingWriter struct{}

func (failingWriter) Write([]byte) (int, error) { return 0, errors.New("broken pipe") }

func TestWriter_ReturnsTheWriteError(t *testing.T) {
	if err := (&writer{w: failingWriter{}}).result([]byte(`1`), []byte(`{}`)); err == nil {
		t.Error("a failed write was not reported")
	}
}

package httpapi

import (
	"bufio"
	"bytes"
	"io"
)

// maxEventLine bounds one line of a stream. OpenAI's response.completed carries the whole
// response, file contents of an apply_patch included.
const maxEventLine = 32 << 20

// Event is one server-sent event: its event: name ("" when it had none) and its data: lines
// joined with "\n".
type Event struct {
	Name string
	Data []byte
}

// SSE reads server-sent events (the HTML living standard's format: event:, data:, comments
// starting with ":", a blank line dispatching).
type SSE struct {
	sc *bufio.Scanner
}

// NewSSE reads events from r.
func NewSSE(r io.Reader) *SSE {
	sc := bufio.NewScanner(r)
	sc.Buffer(make([]byte, 0, 64<<10), maxEventLine)
	return &SSE{sc: sc}
}

// Next returns the next event with data. It returns io.EOF at the end of the stream (an event
// cut off by the end is still returned first), and the scanner's error for a broken stream or
// a line over the bound.
func (s *SSE) Next() (Event, error) {
	var ev Event
	var data [][]byte
	for s.sc.Scan() {
		line := s.sc.Bytes()
		if len(line) == 0 {
			if data != nil {
				ev.Data = bytes.Join(data, []byte("\n"))
				return ev, nil
			}
			ev = Event{}
			continue
		}
		field, value, _ := bytes.Cut(line, []byte(":"))
		value = bytes.TrimPrefix(value, []byte(" "))
		switch string(field) {
		case "": // a comment
		case "event":
			ev.Name = string(value)
		case "data":
			data = append(data, bytes.Clone(value))
		}
	}
	if err := s.sc.Err(); err != nil {
		return Event{}, err
	}
	if data != nil {
		ev.Data = bytes.Join(data, []byte("\n"))
		return ev, nil
	}
	return Event{}, io.EOF
}

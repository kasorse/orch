package results

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"sync"
)

// Writer appends result lines to a results.jsonl. It is safe for concurrent use: every line is
// one Write of the whole line and its newline on a file opened with O_APPEND, under a mutex, and
// is synced before Append returns, so a crash leaves whole lines and at most one half-written
// last line.
type Writer struct {
	mu sync.Mutex
	f  *os.File
}

// OpenWriter opens path for appending and creates it when it is not there. A file that does not
// end in a newline ends in a line a crashed `run` was writing: the file is cut back to the last
// newline, so that the next line cannot be glued to it (Read tolerates such a tail only while
// it is the last line).
func OpenWriter(path string) (*Writer, error) {
	f, err := os.OpenFile(path, os.O_CREATE|os.O_RDWR|os.O_APPEND, 0o644)
	if err != nil {
		return nil, err
	}
	if err := cutPartialLine(f); err != nil {
		f.Close()
		return nil, fmt.Errorf("%s: %w", path, err)
	}
	return &Writer{f: f}, nil
}

// cutPartialLine truncates f after its last newline when its last byte is not one.
func cutPartialLine(f *os.File) error {
	info, err := f.Stat()
	if err != nil {
		return err
	}
	size := info.Size()
	if size == 0 {
		return nil
	}

	last := make([]byte, 1)
	if _, err := f.ReadAt(last, size-1); err != nil {
		return err
	}
	if last[0] == '\n' {
		return nil
	}

	const chunk = 4096
	end := size
	for end > 0 {
		start := max(end-chunk, 0)
		buf := make([]byte, end-start)
		if _, err := f.ReadAt(buf, start); err != nil && !errors.Is(err, io.EOF) {
			return err
		}
		if i := bytes.LastIndexByte(buf, '\n'); i >= 0 {
			return f.Truncate(start + int64(i) + 1)
		}
		end = start
	}
	return f.Truncate(0)
}

// Append writes rec as one line. The schema is set; a list the record leaves nil is written as
// [] as in SPEC.md §9.
func (w *Writer) Append(rec Record) error {
	rec.Schema = Schema
	if rec.AnswerChecksFailed == nil {
		rec.AnswerChecksFailed = []string{}
	}
	if rec.Stages == nil {
		rec.Stages = []string{}
	}
	if rec.ExpectedStages == nil {
		rec.ExpectedStages = []string{}
	}

	var buf bytes.Buffer
	enc := json.NewEncoder(&buf)
	enc.SetEscapeHTML(false)
	if err := enc.Encode(rec); err != nil { // Encode ends the line with a newline
		return err
	}

	w.mu.Lock()
	defer w.mu.Unlock()
	if w.f == nil {
		return errors.New("results: the writer is closed")
	}
	if _, err := w.f.Write(buf.Bytes()); err != nil {
		return err
	}
	return w.f.Sync()
}

// Close closes the file. Closing twice is not an error.
func (w *Writer) Close() error {
	w.mu.Lock()
	defer w.mu.Unlock()
	if w.f == nil {
		return nil
	}
	err := w.f.Close()
	w.f = nil
	return err
}

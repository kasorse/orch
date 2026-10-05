// Package tui is a tiny keyboard-driven screen for the few places the orchestrator asks
// something richer than a yes/no: a form drawn in place, redrawn on every key, and erased
// or collapsed when answered. It depends only on the standard library and talks to the
// terminal with ANSI escapes and termios; callers fall back to line prompts where it is
// not supported (not a terminal, Windows, tests).
package tui

import (
	"bufio"
	"errors"
	"fmt"
	"io"
	"os"
	"strings"
	"unicode/utf8"
)

// Key is one decoded keypress.
type Key int

const (
	KeyNone Key = iota
	KeyUp
	KeyDown
	KeyLeft
	KeyRight
	KeyEnter
	KeySpace
	KeyTab
	KeyBackTab
	KeyEsc
	KeyCtrlC
	KeyRune
)

// Event is a keypress, with the character for KeyRune.
type Event struct {
	Key  Key
	Rune rune
}

// ErrInterrupted is returned when the user presses Ctrl-C inside a form.
var ErrInterrupted = errors.New("interrupted")

// Session owns the terminal while a form is on screen.
type Session struct {
	in      *bufio.Reader
	out     io.Writer
	restore func()
	lines   int // rows drawn by the last Draw
}

// Open puts in (a terminal) into raw mode and hides the cursor. Close undoes both.
func Open(in *os.File, out io.Writer) (*Session, error) {
	restore, err := makeRaw(in)
	if err != nil {
		return nil, err
	}
	io.WriteString(out, "\x1b[?25l")
	return &Session{in: bufio.NewReader(in), out: out, restore: restore}, nil
}

// Close restores the terminal and shows the cursor again.
func (s *Session) Close() {
	io.WriteString(s.out, "\x1b[?25h")
	if s.restore != nil {
		s.restore()
		s.restore = nil
	}
}

// Draw replaces the previously drawn frame with lines. It relies on each line fitting in
// one terminal row (callers truncate), so moving up len(previous) rows lands on the
// frame's first row.
func (s *Session) Draw(lines []string) {
	var b strings.Builder
	if s.lines > 0 {
		fmt.Fprintf(&b, "\r\x1b[%dA", s.lines)
	} else {
		b.WriteString("\r")
	}
	for _, l := range lines {
		b.WriteString("\x1b[2K" + l + "\n")
	}
	// Clear what is left of a taller previous frame.
	b.WriteString("\x1b[J")
	io.WriteString(s.out, b.String())
	s.lines = len(lines)
}

// Erase removes the last frame and leaves the cursor where it began.
func (s *Session) Erase() {
	if s.lines > 0 {
		fmt.Fprintf(s.out, "\r\x1b[%dA\x1b[J", s.lines)
		s.lines = 0
	}
}

// Read waits for one keypress.
func (s *Session) Read() (Event, error) {
	b, err := s.in.ReadByte()
	if err != nil {
		return Event{}, err
	}
	switch b {
	case 3:
		return Event{Key: KeyCtrlC}, nil
	case '\r', '\n':
		return Event{Key: KeyEnter}, nil
	case ' ':
		return Event{Key: KeySpace}, nil
	case '\t':
		return Event{Key: KeyTab}, nil
	case 0x1b:
		// An escape sequence arrives in one read; a lone Esc has nothing buffered after it.
		if s.in.Buffered() == 0 {
			return Event{Key: KeyEsc}, nil
		}
		b2, _ := s.in.ReadByte()
		if b2 != '[' && b2 != 'O' {
			return Event{Key: KeyEsc}, nil
		}
		b3, _ := s.in.ReadByte()
		switch b3 {
		case 'A':
			return Event{Key: KeyUp}, nil
		case 'B':
			return Event{Key: KeyDown}, nil
		case 'C':
			return Event{Key: KeyRight}, nil
		case 'D':
			return Event{Key: KeyLeft}, nil
		case 'Z':
			return Event{Key: KeyBackTab}, nil
		}
		// Swallow the rest of an unknown sequence (e.g. "\x1b[3~").
		for s.in.Buffered() > 0 {
			c, _ := s.in.ReadByte()
			if c >= 0x40 && c <= 0x7e {
				break
			}
		}
		return Event{Key: KeyNone}, nil
	}
	if b < 0x80 {
		return Event{Key: KeyRune, Rune: rune(b)}, nil
	}
	// A multi-byte UTF-8 rune: read its continuation bytes.
	buf := []byte{b}
	for !utf8.FullRune(buf) && len(buf) < 4 {
		c, err := s.in.ReadByte()
		if err != nil {
			break
		}
		buf = append(buf, c)
	}
	r, _ := utf8.DecodeRune(buf)
	return Event{Key: KeyRune, Rune: r}, nil
}

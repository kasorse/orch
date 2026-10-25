package logging

import (
	"fmt"
	"io"
	"strings"
	"sync"
	"time"
)

// spinnerFrames are the glyphs the spinner cycles through, Claude Code style.
var spinnerFrames = []string{"·", "✢", "✳", "✶", "✻", "✽", "✻", "✶", "✳", "✢"}

const spinnerInterval = 120 * time.Millisecond

// spinner animates one status line ("✻ Planning… (12s · ctrl+c to cancel)") in place
// while a model or the test command works. Only the pretty renderer uses it, and only
// without --verbose, where nothing else writes to the terminal while it runs. Stop clears
// the line, so whatever is printed next starts on a clean one.
type spinner struct {
	out   io.Writer
	st    style
	start time.Time

	mu    sync.Mutex
	label string
	hint  string
	// detail is what the model is doing right now ("Read src/app.ts"), shown after ⎿.
	detail string

	stopOnce sync.Once
	stop     chan struct{}
	stopped  chan struct{}
}

func startSpinner(out io.Writer, st style, label, hint string) *spinner {
	s := &spinner{out: out, st: st, label: label, hint: hint, start: time.Now(),
		stop: make(chan struct{}), stopped: make(chan struct{})}
	fmt.Fprint(out, "\x1b[?25l") // hide the cursor
	s.draw(0)
	go s.loop()
	return s
}

func (s *spinner) loop() {
	defer close(s.stopped)
	t := time.NewTicker(spinnerInterval)
	defer t.Stop()
	for i := 1; ; i++ {
		select {
		case <-s.stop:
			return
		case <-t.C:
			s.draw(i)
		}
	}
}

// SetLabel changes what the spinner says without restarting its clock.
func (s *spinner) SetLabel(label, hint string) {
	s.mu.Lock()
	s.label, s.hint, s.detail = label, hint, ""
	s.mu.Unlock()
}

// SetDetail shows what is happening inside the current call.
func (s *spinner) SetDetail(detail string) {
	s.mu.Lock()
	s.detail = detail
	s.mu.Unlock()
}

func (s *spinner) draw(i int) {
	s.mu.Lock()
	label, hint, detail := s.label, s.hint, s.detail
	s.mu.Unlock()
	frame := s.st.accent(spinnerFrames[i%len(spinnerFrames)])
	meta := formatElapsed(time.Since(s.start))
	if hint != "" {
		meta = hint + " · " + meta
	}
	meta += " · ctrl+c to cancel"
	line := frame + " " + s.st.accent(label+"…") + " " + s.st.gray("("+meta+")")
	if detail != "" {
		line += s.st.gray("  ⎿ ") + detail
	}
	// One terminal row: a wrapped spinner line could not be redrawn in place.
	w := termWidth() - 1
	if visibleWidth(line) > w {
		plain := ansiRE.ReplaceAllString(line, "")
		line = s.st.gray(truncateVisible(plain, w))
	}
	fmt.Fprintf(s.out, "\r%s%s", line, clearRest(visibleWidth(line), w))
}

// clearRest blanks the rest of the transcript column after used cells. With the sidebar
// open it pads with spaces, since erase-to-end-of-line would also wipe the sidebar's part
// of the row; without it, erase-to-end-of-line is enough.
func clearRest(used, width int) string {
	if reservedCols.Load() == 0 {
		return "\x1b[K"
	}
	return strings.Repeat(" ", max(width-used, 0))
}

// Stop ends the animation and clears its line. Safe to call more than once.
func (s *spinner) Stop() {
	if s == nil {
		return
	}
	s.stopOnce.Do(func() {
		close(s.stop)
		<-s.stopped
		fmt.Fprint(s.out, "\r"+clearRest(0, termWidth()-1)+"\r\x1b[?25h")
	})
}

// formatElapsed renders whole seconds, and minutes past the first one: "8s", "2m 05s".
func formatElapsed(d time.Duration) string {
	secs := int(d.Seconds())
	if secs < 60 {
		return fmt.Sprintf("%ds", secs)
	}
	return fmt.Sprintf("%dm %02ds", secs/60, secs%60)
}

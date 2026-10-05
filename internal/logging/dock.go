package logging

import (
	"bytes"
	"fmt"
	"io"
	"os"
	"strings"
	"sync"
	"sync/atomic"
)

// The dock: a sidebar pinned to the top-right of a wide terminal, the way agent UIs keep
// context (plan, repository, usage, limits) beside the transcript instead of in it.
//
// It is not a full-screen UI - the transcript stays ordinary scrolling output, so
// scrollback, copy and paste, and prompts work as before. The main column is narrowed
// (termWidth reports the space left of the dock) and the dock is drawn with absolute
// cursor moves. A write that can scroll the screen (one containing a newline) first
// erases the dock, then writes, then draws it again, so no copy of it ever scrolls into
// the scrollback. Writes without a newline - the spinner redrawing its line - pass
// straight through. Prompts hide it, since the terminal's own echo of Enter can scroll.

const (
	dockWidth     = 40  // the sidebar's columns, borders included
	dockGap       = 2   // blank columns between the transcript and the sidebar
	dockMinCols   = 120 // narrower terminals use the compact status line instead
	dockMinRows   = 20
	dockEnvOptOut = "ORCHESTRATOR_SIDEBAR" // "0" or "off" keeps panels inline
)

// reservedCols is how many columns the dock takes from the right; termWidth subtracts it.
var reservedCols atomic.Int32

type dock struct {
	mu      sync.Mutex
	out     io.Writer // the terminal
	visible bool
	lines   []string // what to draw, each exactly dockWidth cells
	drawn   int      // rows currently on screen
	col     int      // 1-based first column of the dock
}

// dockAvailable reports whether a sidebar fits and is wanted.
func dockAvailable(out io.Writer) bool {
	if v := strings.ToLower(os.Getenv(dockEnvOptOut)); v == "0" || v == "off" || v == "false" {
		return false
	}
	if !IsTerminal(out) {
		return false
	}
	rows, cols := termSize()
	return cols >= dockMinCols && rows >= dockMinRows
}

func newDock(out io.Writer) *dock {
	_, cols := termSize()
	reservedCols.Store(int32(dockWidth + dockGap))
	return &dock{out: out, visible: true, col: cols - dockWidth + 1}
}

// set replaces the content and redraws it.
func (d *dock) set(lines []string) {
	d.mu.Lock()
	defer d.mu.Unlock()
	d.lines = lines
	if d.visible {
		d.eraseLocked()
		d.drawLocked()
	}
}

func (d *dock) hide() {
	d.mu.Lock()
	defer d.mu.Unlock()
	if d.visible {
		d.eraseLocked()
		d.visible = false
	}
}

func (d *dock) show() {
	d.mu.Lock()
	defer d.mu.Unlock()
	if !d.visible {
		d.visible = true
		d.drawLocked()
	}
}

// close removes the dock for good and gives the columns back to the transcript.
func (d *dock) close() {
	d.hide()
	reservedCols.Store(0)
}

// write passes p to w, keeping the dock out of anything that scrolls.
func (d *dock) write(w io.Writer, p []byte) (int, error) {
	d.mu.Lock()
	defer d.mu.Unlock()
	if !d.visible || bytes.IndexByte(p, '\n') < 0 {
		return w.Write(p)
	}
	d.eraseLocked()
	n, err := w.Write(p)
	d.drawLocked()
	return n, err
}

func (d *dock) eraseLocked() {
	if d.drawn == 0 {
		return
	}
	var b strings.Builder
	b.WriteString("\x1b7")
	for row := 1; row <= d.drawn; row++ {
		fmt.Fprintf(&b, "\x1b[%d;%dH\x1b[K", row, d.col)
	}
	b.WriteString("\x1b8")
	io.WriteString(d.out, b.String())
	d.drawn = 0
}

func (d *dock) drawLocked() {
	rows, cols := termSize()
	if cols < dockMinCols || rows < dockMinRows {
		return // resized too small: stay hidden until it fits again
	}
	d.col = cols - dockWidth + 1
	lines := d.lines
	if max := rows - 2; len(lines) > max {
		lines = lines[:max]
	}
	if len(lines) == 0 {
		return
	}
	var b strings.Builder
	b.WriteString("\x1b7")
	for i, l := range lines {
		fmt.Fprintf(&b, "\x1b[%d;%dH%s", i+1, d.col, l)
	}
	b.WriteString("\x1b8")
	io.WriteString(d.out, b.String())
	d.drawn = len(lines)
}

// dockWriter is the reporter's Out while the dock is open.
type dockWriter struct {
	d *dock
	w io.Writer
}

func (dw dockWriter) Write(p []byte) (int, error) { return dw.d.write(dw.w, p) }

// OpenDock starts the sidebar when the terminal is wide enough (pretty renderer only).
// From then on Out goes through it; Wrap does the same for other writers on the same
// terminal (stderr). CloseDock, RunFinished and Pause end it.
func (r *ConsoleReporter) OpenDock() {
	if !r.Pretty || r.dock != nil || !dockAvailable(r.Out) {
		return
	}
	r.dock = newDock(r.Out)
	r.rawOut = r.Out
	r.Out = dockWriter{d: r.dock, w: r.rawOut}
}

// CloseDock removes the sidebar and restores the full width.
func (r *ConsoleReporter) CloseDock() {
	if r.dock == nil {
		return
	}
	r.stopSpin()
	r.dock.close()
	r.Out = r.rawOut
	r.dock = nil
}

// Wrap returns w routed through the dock whenever one is open, so writes to it (stderr
// warnings) cannot scroll a copy of the sidebar into the scrollback.
func (r *ConsoleReporter) Wrap(w io.Writer) io.Writer {
	if !r.Pretty {
		return w
	}
	return wrapWriter{r: r, w: w}
}

type wrapWriter struct {
	r *ConsoleReporter
	w io.Writer
}

func (ww wrapWriter) Write(p []byte) (int, error) {
	ww.r.stopSpin()
	if d := ww.r.dock; d != nil {
		return d.write(ww.w, p)
	}
	return ww.w.Write(p)
}

// refreshDock rebuilds the sidebar from the reporter's state.
func (r *ConsoleReporter) refreshDock() {
	if r.dock == nil {
		return
	}
	r.dock.set(r.dockLines())
}

// dockLines stacks the sidebar's cards; the less important ones go last, so a short
// terminal cuts them first.
func (r *ConsoleReporter) dockLines() []string {
	inner := dockWidth - 4
	var out []string
	add := func(card []string) {
		if len(card) == 0 {
			return
		}
		out = append(out, card...)
	}
	add(r.workflowCard(inner))
	add(r.usageCard(inner))
	add(r.limitsCard(inner))
	add(r.repoCard(inner))
	return out
}

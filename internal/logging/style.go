package logging

import (
	"io"
	"os"
	"regexp"
	"strconv"
	"strings"
	"unicode/utf8"
)

// The pretty renderer (a terminal UI in the spirit of Claude Code: colour, a spinner while
// a model works, boxed header and summary) is only used when stdout is an interactive
// terminal. Anything else - a pipe, a file, a CI log, the tests - gets the plain text the
// spec describes, byte for byte. NO_COLOR (https://no-color.org) or ORCHESTRATOR_PLAIN
// switch the pretty renderer off on a terminal too.

// PrettyFor reports whether output written to w should use the pretty renderer.
func PrettyFor(w io.Writer) bool {
	if os.Getenv("NO_COLOR") != "" || os.Getenv("ORCHESTRATOR_PLAIN") != "" || os.Getenv("TERM") == "dumb" {
		return false
	}
	return IsTerminal(w)
}

// IsTerminal reports whether w is a character device (an interactive terminal).
func IsTerminal(w io.Writer) bool {
	f, ok := w.(*os.File)
	if !ok {
		return false
	}
	info, err := f.Stat()
	return err == nil && info.Mode()&os.ModeCharDevice != 0
}

// style wraps text in ANSI SGR sequences when on, and returns it unchanged otherwise, so
// the same rendering code serves both renderers wherever only colour differs.
type style struct{ on bool }

func (s style) sgr(code, text string) string {
	if !s.on || text == "" {
		return text
	}
	return "\x1b[" + code + "m" + text + "\x1b[0m"
}

func (s style) bold(t string) string    { return s.sgr("1", t) }
func (s style) dim(t string) string     { return s.sgr("2", t) }
func (s style) italic(t string) string  { return s.sgr("3", t) }
func (s style) red(t string) string     { return s.sgr("31", t) }
func (s style) green(t string) string   { return s.sgr("32", t) }
func (s style) yellow(t string) string  { return s.sgr("33", t) }
func (s style) blue(t string) string    { return s.sgr("34", t) }
func (s style) magenta(t string) string { return s.sgr("35", t) }
func (s style) cyan(t string) string    { return s.sgr("36", t) }
func (s style) gray(t string) string    { return s.sgr("90", t) }

// accent is the orchestrator's brand colour (a warm orange, like Claude Code's).
func (s style) accent(t string) string { return s.sgr("38;5;209", t) }

var ansiRE = regexp.MustCompile(`\x1b\[[0-9;]*m|\x1b\]8;;[^\x1b]*\x1b\\`)

// visibleWidth is the number of terminal cells s takes, ignoring ANSI sequences. It counts
// runes, which is right for the Latin, Cyrillic and box-drawing text printed here.
func visibleWidth(s string) int {
	return utf8.RuneCountInString(ansiRE.ReplaceAllString(s, ""))
}

// truncateVisible cuts plain (unstyled) s to at most n cells, ending in "…" when cut.
func truncateVisible(s string, n int) string {
	if n <= 0 {
		return ""
	}
	if utf8.RuneCountInString(s) <= n {
		return s
	}
	r := []rune(s)
	return string(r[:n-1]) + "…"
}

// termWidth is the terminal's width (asked from stdout's tty), else $COLUMNS, else 100.
func termWidth() int {
	if n := ttyWidth(os.Stdout.Fd()); n >= 40 {
		return n
	}
	if n, err := strconv.Atoi(os.Getenv("COLUMNS")); err == nil && n >= 40 {
		return n
	}
	return 100
}

// box draws lines inside a rounded border, sized to the widest line (at least minWidth,
// at most the terminal), in the given border colour.
func box(st style, border func(string) string, lines []string, minWidth int) string {
	inner := minWidth
	for _, l := range lines {
		inner = max(inner, visibleWidth(l))
	}
	inner = min(inner, termWidth()-4)
	var b strings.Builder
	b.WriteString(border("╭" + strings.Repeat("─", inner+2) + "╮"))
	b.WriteByte('\n')
	for _, l := range lines {
		if w := visibleWidth(l); w > inner {
			l = truncateVisible(ansiRE.ReplaceAllString(l, ""), inner)
		}
		pad := inner - visibleWidth(l)
		b.WriteString(border("│") + " " + l + strings.Repeat(" ", pad) + " " + border("│"))
		b.WriteByte('\n')
	}
	b.WriteString(border("╰" + strings.Repeat("─", inner+2) + "╯"))
	b.WriteByte('\n')
	return b.String()
}

// shortenHome replaces the home directory prefix of p with "~".
func shortenHome(p string) string {
	home, err := os.UserHomeDir()
	if err != nil || home == "" {
		return p
	}
	if p == home {
		return "~"
	}
	if rest, ok := strings.CutPrefix(p, home+string(os.PathSeparator)); ok {
		return "~/" + rest
	}
	return p
}

// FormatError renders the error main prints on exit: red and marked on a terminal, the
// error text unchanged otherwise.
func FormatError(w io.Writer, err error) string {
	msg := err.Error()
	if !PrettyFor(w) {
		return msg
	}
	st := style{on: true}
	first, rest, _ := strings.Cut(msg, "\n")
	out := "\n" + st.red(st.bold("✗ "+first))
	if rest != "" {
		for _, line := range strings.Split(rest, "\n") {
			if line == "" {
				out += "\n"
				continue
			}
			out += "\n  " + line
		}
	}
	return out
}

// Warn renders a warning line: "Warning: ..." in plain mode, a yellow "⚠ ..." in pretty.
func Warn(w io.Writer, msg string) string {
	if !PrettyFor(w) {
		return "Warning: " + msg
	}
	st := style{on: true}
	return st.yellow("⚠ " + msg)
}

package logging

import (
	"fmt"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"unicode/utf8"
)

// Rich text for the pretty renderer: what models write (summaries, plans, review issues,
// answers) is markdown-ish prose. On a terminal it is highlighted - `code`, file paths,
// URLs, "quoted" and 'quoted' values, **bold**, issue keys - laid out as lists and code
// blocks, and word-wrapped to the terminal with hanging indents, so a long paragraph
// neither runs off the screen nor wraps back to column 0.

type segKind int

const (
	segPlain segKind = iota
	segCode
	segBold
	segURL
	segDoubleQuoted
	segSingleQuoted
	segPath
	segIssueKey
)

type seg struct {
	kind segKind
	text string
}

var inlineRE = regexp.MustCompile("`([^`\n]+)`" + // 1 code
	`|\*\*([^*\n]+)\*\*` + // 2 bold
	"|(https?://[^\\s<>\"'`]+)" + // 3 url
	`|"([^"\n]{1,200})"` + // 4 "quoted"
	`|'([^'\n]{1,120})'` + // 5 'quoted'
	`|((?:~|\.{1,2})?/?(?:[\w@.{}*-]+/)+[\w@.{}*-]*` + // 6 path: dir/dir/file
	`|[\w@.-]+\.(?:go|ts|tsx|js|jsx|mjs|cjs|json|md|mdx|ya?ml|toml|py|rb|rs|java|kt|swift|sql|sh|css|scss|html|txt|lock|mod|sum|xml|gradle|proto)\b)` + // or file.ext
	`|\b([A-Z][A-Z0-9]{1,9}-\d+)\b`) // 7 issue key: OPS-15489

var fileExtRE = regexp.MustCompile(`\.[A-Za-z][A-Za-z0-9]{0,6}$`)

// tokenize splits one line of prose into highlighted segments.
func tokenize(s string) []seg {
	var out []seg
	plain := func(t string) {
		if t == "" {
			return
		}
		if n := len(out); n > 0 && out[n-1].kind == segPlain {
			out[n-1].text += t
			return
		}
		out = append(out, seg{text: t})
	}
	pos := 0
	for pos < len(s) {
		m := inlineRE.FindStringSubmatchIndex(s[pos:])
		if m == nil {
			plain(s[pos:])
			break
		}
		for i := range m {
			if m[i] >= 0 {
				m[i] += pos
			}
		}
		start, end := m[0], m[1]
		group := 0
		for g := 1; g <= 7; g++ {
			if m[2*g] >= 0 {
				group = g
				break
			}
		}
		if group == 3 || group == 6 {
			for end > start && strings.ContainsRune(".,;:)!?", rune(s[end-1])) {
				end--
			}
		}
		ok := end > start
		switch group {
		case 5: // an apostrophe ("can't") is not a quote
			ok = ok && (start == 0 || strings.ContainsRune(" \t([{", rune(s[start-1]))) &&
				(end == len(s) || !isWordByte(s[end]))
		case 6:
			ok = ok && (start == 0 || !isWordByte(s[start-1])) && looksLikePath(s[start:end])
		}
		if !ok {
			plain(s[pos : start+1])
			pos = start + 1
			continue
		}
		plain(s[pos:start])
		switch group {
		case 1:
			out = append(out, seg{segCode, s[m[2]:m[3]]})
		case 2:
			out = append(out, seg{segBold, s[m[4]:m[5]]})
		case 3:
			out = append(out, seg{segURL, s[start:end]})
		case 4:
			out = append(out, seg{segDoubleQuoted, s[start:end]})
		case 5:
			out = append(out, seg{segSingleQuoted, s[start:end]})
		case 6:
			out = append(out, seg{segPath, s[start:end]})
		case 7:
			out = append(out, seg{segIssueKey, s[start:end]})
		}
		pos = end
	}
	return out
}

func isWordByte(b byte) bool {
	return b == '_' || b >= '0' && b <= '9' || b >= 'a' && b <= 'z' || b >= 'A' && b <= 'Z'
}

// looksLikePath keeps "Endpoint/API" and "and/or" as prose: a path is rooted (/, ./, ~/),
// has at least two slashes, or ends in a file extension.
func looksLikePath(p string) bool {
	if strings.HasPrefix(p, "/") || strings.HasPrefix(p, "./") || strings.HasPrefix(p, "../") || strings.HasPrefix(p, "~/") {
		return len(p) > 2
	}
	if strings.Count(p, "/") >= 2 {
		return true
	}
	return fileExtRE.MatchString(p)
}

// rich renders text with highlighting and wrapping. width is the terminal width; cwd, if
// set, turns paths that exist under it into clickable file links (OSC 8).
type rich struct {
	st    style
	width int
	cwd   string
}

func (r rich) code(t string) string { return r.st.sgr("38;5;147", t) }

func (r rich) styleSeg(s seg, text string) string {
	st := r.st
	switch s.kind {
	case segCode:
		return r.code(text)
	case segBold:
		return st.bold(text)
	case segURL:
		return hyperlink(st, s.text, st.sgr("4;34", text))
	case segDoubleQuoted, segSingleQuoted:
		return st.green(text)
	case segPath:
		styled := st.cyan(text)
		if r.cwd != "" && st.on {
			abs := s.text
			if !filepath.IsAbs(abs) {
				abs = filepath.Join(r.cwd, abs)
			}
			if _, err := os.Stat(abs); err == nil {
				return hyperlink(st, "file://"+abs, styled)
			}
		}
		return styled
	case segIssueKey:
		return st.bold(st.magenta(text))
	}
	return text
}

// hyperlink wraps text in an OSC 8 link; terminals without support show the text alone.
func hyperlink(st style, url, text string) string {
	if !st.on {
		return text
	}
	return "\x1b]8;;" + url + "\x1b\\" + text + "\x1b]8;;\x1b\\"
}

// piece is part of a word in one style; a word ("(`x`),") can span several segments.
type piece struct {
	seg  seg
	text string
}

type word []piece

func (w word) width() int {
	n := 0
	for _, p := range w {
		n += utf8.RuneCountInString(p.text)
	}
	return n
}

func (r rich) render(w word) string {
	var b strings.Builder
	for _, p := range w {
		b.WriteString(r.styleSeg(p.seg, p.text))
	}
	return b.String()
}

func words(segs []seg) []word {
	var out []word
	var cur word
	for _, s := range segs {
		parts := strings.Split(s.text, " ")
		for i, part := range parts {
			if i > 0 {
				if len(cur) > 0 {
					out = append(out, cur)
				}
				cur = nil
			}
			if part != "" {
				cur = append(cur, piece{seg: s, text: part})
			}
		}
	}
	if len(cur) > 0 {
		out = append(out, cur)
	}
	return out
}

// para writes text highlighted and wrapped: the first line after first, the rest after
// cont (both may carry styling; their visible widths are what count).
func (r rich) para(w io.Writer, first, cont, text string) {
	ws := words(tokenize(text))
	if len(ws) == 0 {
		fmt.Fprintln(w, strings.TrimRight(first, " "))
		return
	}
	limit := max(r.width-1, 20)
	var line strings.Builder
	line.WriteString(first)
	used := visibleWidth(first)
	startW := used
	for _, wd := range ws {
		n := wd.width()
		if used > startW && used+1+n > limit {
			fmt.Fprintln(w, line.String())
			line.Reset()
			line.WriteString(cont)
			used = visibleWidth(cont)
			startW = used
		} else if used > startW {
			line.WriteByte(' ')
			used++
		}
		line.WriteString(r.render(wd))
		used += n
	}
	fmt.Fprintln(w, line.String())
}

var (
	bulletRE  = regexp.MustCompile(`^([-*•+])\s+`)
	numberRE  = regexp.MustCompile(`^(\d{1,3}[.)])\s+`)
	headingRE = regexp.MustCompile(`^#{1,6}\s+`)
)

// block writes multi-line prose under indent: lists get a styled marker and a hanging
// indent, headings are bold, ``` fences become a gutter of unwrapped code lines.
func (r rich) block(w io.Writer, indent, text string) {
	st := r.st
	inFence := false
	for _, line := range strings.Split(strings.Trim(text, "\n"), "\n") {
		line = strings.TrimRight(line, " \t\r")
		trimmed := strings.TrimLeft(line, " \t")
		if strings.HasPrefix(trimmed, "```") {
			inFence = !inFence
			continue
		}
		if inFence {
			fmt.Fprintf(w, "%s%s %s\n", indent, st.gray("│"), r.code(line))
			continue
		}
		if trimmed == "" {
			fmt.Fprintln(w)
			continue
		}
		lead := strings.Repeat(" ", visibleWidth(line[:len(line)-len(trimmed)]))
		switch {
		case bulletRE.MatchString(trimmed):
			m := bulletRE.FindString(trimmed)
			r.para(w, indent+lead+st.gray("•")+" ", indent+lead+"  ", trimmed[len(m):])
		case numberRE.MatchString(trimmed):
			m := numberRE.FindStringSubmatch(trimmed)
			r.para(w, indent+lead+st.gray(m[1])+" ", indent+lead+strings.Repeat(" ", len(m[1])+1), trimmed[len(m[0]):])
		case headingRE.MatchString(trimmed):
			fmt.Fprintf(w, "%s%s%s\n", indent, lead, st.bold(st.accent(strings.TrimSpace(trimmed[len(headingRE.FindString(trimmed)):]))))
		default:
			r.para(w, indent+lead, indent+lead, trimmed)
		}
	}
}

package ui

import (
	"regexp"
	"strings"

	"charm.land/lipgloss/v2"
	"github.com/charmbracelet/x/ansi"
	"github.com/rivo/uniseg"

	"github.com/pixelbender/claudit/internal/ideview"
	"github.com/pixelbender/claudit/internal/theme"
)

var (
	styleReply   = lipgloss.NewStyle().Foreground(theme.Bright) // what an agent writes: white
	styleCode    = lipgloss.NewStyle().Foreground(theme.Violet)
	styleHeading = lipgloss.NewStyle().Foreground(theme.Accent)
	styleRule    = lipgloss.NewStyle().Foreground(theme.Border)

	drawHeadingRe = regexp.MustCompile(`^(#{1,6})\s+(.*)$`)
	drawItemRe    = regexp.MustCompile(`^(\s*)([-*+]|\d{1,3}[.)])\s+(.*)$`)
	drawRuleRe    = regexp.MustCompile(`^\s*([-*_])(\s*[-*_]){2,}\s*$`)
	drawTableSep  = regexp.MustCompile(`^\s*\|?\s*:?-+:?\s*(\|\s*:?-+:?\s*)*\|?\s*$`)
)

// drawMarkdown renders what an agent writes into styled lines of at most w cells: headings,
// lists, quotes, rules, tables, fenced code, and inline **bold**, *italic*, `code` and [links](url).
// Source line breaks are kept, and long lines wrap, code too; a fence that is still streaming
// counts as code to the end.
func drawMarkdown(src string, w int) []string {
	w = max(1, w)
	var out []string
	lines := strings.Split(strings.Trim(src, "\n"), "\n")
	for i := 0; i < len(lines); i++ {
		line := strings.TrimRight(lines[i], " \r")
		trimmed := strings.TrimSpace(line)
		switch {
		case strings.HasPrefix(trimmed, "```"):
			lang := strings.TrimSpace(strings.TrimPrefix(trimmed, "```"))
			for i++; i < len(lines) && !strings.HasPrefix(strings.TrimSpace(lines[i]), "```"); i++ {
				if w-2 < 2 { // too narrow to wrap into anything readable
					out = append(out, styleRule.Render("│ ")+drawFitTrunc(ideview.Highlight(drawClean(lines[i]), lang), w-2))
					continue
				}
				// Wrapped at words as the prose is, the bar going on beside each part: cut, a
				// long line lost its end (a draft to send, a command). The line is highlighted
				// whole and its colour goes on past the wrap: the rest of a comment or a string
				// is not code.
				code := lipgloss.Wrap(ideview.Highlight(drawClean(lines[i]), lang), w-2, "")
				for part := range strings.SplitSeq(code, "\n") {
					out = append(out, styleRule.Render("│ ")+drawFit(part, w-2))
				}
			}
		case trimmed == "":
			if len(out) > 0 && out[len(out)-1] != "" {
				out = append(out, "")
			}
		case drawRuleRe.MatchString(line):
			out = append(out, styleRule.Render(strings.Repeat("─", w)))
		case strings.HasPrefix(trimmed, "|") && i+1 < len(lines) && drawTableSep.MatchString(lines[i+1]):
			rows := [][]string{drawCells(trimmed)}
			for i += 2; i < len(lines) && strings.HasPrefix(strings.TrimSpace(lines[i]), "|"); i++ {
				rows = append(rows, drawCells(strings.TrimSpace(lines[i])))
			}
			i--
			out = append(out, drawTable(rows, w)...)
		default:
			if m := drawHeadingRe.FindStringSubmatch(trimmed); m != nil {
				out = append(out, drawFlow(drawSpans(m[2], styleHeading), w, "", "")...)
			} else if m := drawItemRe.FindStringSubmatch(line); m != nil {
				indent := strings.Repeat(" ", min(len(m[1]), 8))
				bullet := "• "
				if m[2][0] >= '0' && m[2][0] <= '9' {
					bullet = m[2] + " "
				}
				first := indent + styleAccent.Render(bullet)
				out = append(out, drawFlow(drawSpans(m[3], styleReply), w, first, indent+strings.Repeat(" ", ansi.StringWidth(bullet)))...)
			} else if rest, ok := strings.CutPrefix(trimmed, ">"); ok {
				out = append(out, drawFlow(drawSpans(strings.TrimSpace(rest), styleDim.Italic(true)), w, styleRule.Render("▏ "), styleRule.Render("▏ "))...)
			} else {
				out = append(out, drawFlow(drawSpans(line, styleReply), w, "", "")...)
			}
		}
	}
	for len(out) > 0 && out[len(out)-1] == "" {
		out = out[:len(out)-1]
	}
	for i, l := range out {
		out[i] = drawTrunc(l, w) // a wide rune cannot be split when w is tiny
	}
	return out
}

// drawTrunc cuts s to at most w cells, as drawFit does, without padding.
func drawTrunc(s string, w int) string {
	for n := w; ansi.StringWidth(s) > w; n-- {
		s = ansi.Truncate(s, n, "")
	}
	return s
}

// drawSpan is a run of text in one style.
type drawSpan struct {
	text string
	st   lipgloss.Style
}

// drawSpans splits one line of markdown into styled runs.
func drawSpans(s string, base lipgloss.Style) []drawSpan {
	s = drawOneLine(s)
	var spans []drawSpan
	var plain strings.Builder
	flush := func() {
		if plain.Len() > 0 {
			spans = append(spans, drawSpan{plain.String(), base})
			plain.Reset()
		}
	}
	for i := 0; i < len(s); {
		rest := s[i:]
		if j := drawClose(rest, "`", "`"); j > 0 {
			flush()
			spans = append(spans, drawSpan{rest[1:j], styleCode})
			i += j + 1
			continue
		}
		if j := drawClose(rest, "**", "**"); j > 0 {
			flush()
			spans = append(spans, drawSpans(rest[2:j], base.Foreground(theme.Strong))...)
			i += j + 2
			continue
		}
		if j := drawClose(rest, "*", "*"); j > 0 && rest[1] != ' ' {
			flush()
			spans = append(spans, drawSpans(rest[1:j], base.Italic(true))...)
			i += j + 1
			continue
		}
		if strings.HasPrefix(rest, "[") {
			if j := strings.Index(rest, "]("); j > 0 {
				if k := strings.IndexByte(rest[j+2:], ')'); k >= 0 {
					flush()
					spans = append(spans, drawSpans(rest[1:j], base.Underline(true))...)
					i += j + 2 + k + 1
					continue
				}
			}
		}
		plain.WriteByte(s[i])
		i++
	}
	flush()
	return spans
}

// drawClose returns the index in s of the closer of an opener at the start of s, or -1.
func drawClose(s, open, close string) int {
	if !strings.HasPrefix(s, open) || len(s) <= len(open) {
		return -1
	}
	j := strings.Index(s[len(open):], close)
	if j <= 0 {
		return -1
	}
	return j + len(open)
}

// drawFlow word-wraps styled runs into lines of at most w cells; the first line starts with
// first, the others with hang (both already styled).
func drawFlow(spans []drawSpan, w int, first, hang string) []string {
	if ansi.StringWidth(first) >= w || ansi.StringWidth(hang) >= w {
		first, hang = "", "" // no room for decoration
	}
	var lines []string
	var line strings.Builder
	prefix := first
	used := ansi.StringWidth(prefix)
	line.WriteString(prefix)
	newLine := func() {
		lines = append(lines, line.String())
		line.Reset()
		prefix = hang
		line.WriteString(prefix)
		used = ansi.StringWidth(prefix)
	}
	for _, sp := range spans {
		for _, word := range drawWords(sp.text) {
			ww := ansi.StringWidth(word)
			if used+ww > w && used > ansi.StringWidth(prefix) {
				newLine()
				word = strings.TrimLeft(word, " ")
				ww = ansi.StringWidth(word)
			}
			for ww > w-used && ww > 0 { // a word longer than the line: cut it
				head := drawTake(word, max(1, w-used))
				line.WriteString(sp.st.Render(head))
				word = word[len(head):]
				ww = ansi.StringWidth(word)
				newLine()
			}
			if word != "" {
				line.WriteString(sp.st.Render(word))
				used += ww
			}
		}
	}
	lines = append(lines, line.String())
	return lines
}

// drawWords splits s before every space, so each word carries its leading spaces.
func drawWords(s string) []string {
	var words []string
	start := 0
	for i := 1; i < len(s); i++ {
		if s[i] == ' ' && s[i-1] != ' ' {
			words = append(words, s[start:i])
			start = i
		}
	}
	return append(words, s[start:])
}

// drawTake returns the longest prefix of s (whole grapheme clusters) at most w cells wide, at least one cluster.
func drawTake(s string, w int) string {
	g := uniseg.NewGraphemes(s)
	end, used := 0, 0
	for g.Next() {
		cw := ansi.StringWidth(g.Str())
		if used+cw > w && end > 0 {
			break
		}
		_, end = g.Positions()
		used += cw
	}
	return s[:end]
}

// drawCells splits a table row "| a | b |" into trimmed cells.
func drawCells(row string) []string {
	row = strings.TrimSuffix(strings.TrimPrefix(row, "|"), "|")
	cells := strings.Split(row, "|")
	for i, c := range cells {
		cells[i] = strings.TrimSpace(c)
	}
	return cells
}

// drawTable lays rows out in aligned columns; the first row is the header. Columns shrink,
// widest first, until the table fits in w cells.
func drawTable(rows [][]string, w int) []string {
	n := 0
	for _, r := range rows {
		n = max(n, len(r))
	}
	widths := make([]int, n)
	for _, r := range rows {
		for c, cell := range r {
			widths[c] = max(widths[c], ansi.StringWidth(drawPlain(cell)))
		}
	}
	for total := drawSum(widths) + 3*(n-1); total > w; total-- {
		widest := 0
		for c := range widths {
			if widths[c] > widths[widest] {
				widest = c
			}
		}
		if widths[widest] <= 3 {
			break
		}
		widths[widest]--
	}
	sep := styleRule.Render(" │ ")
	var out []string
	for i, r := range rows {
		base := styleReply
		if i == 0 {
			base = styleStrong
		}
		cells := make([]string, n)
		for c := range cells {
			var text string
			if c < len(r) {
				text = drawRender(drawSpans(r[c], base))
			}
			cells[c] = drawFitTrunc(text, widths[c])
		}
		out = append(out, ansi.Truncate(strings.Join(cells, sep), w, "…"))
		if i == 0 {
			bars := make([]string, n)
			for c, cw := range widths {
				bars[c] = strings.Repeat("─", cw)
			}
			out = append(out, styleRule.Render(ansi.Truncate(strings.Join(bars, "─┼─"), w, "")))
		}
	}
	return out
}

func drawSum(xs []int) (s int) {
	for _, x := range xs {
		s += x
	}
	return s
}

// drawPlain is a cell's text without its markdown markers, as it will show.
func drawPlain(s string) string {
	var b strings.Builder
	for _, sp := range drawSpans(s, styleFg) {
		b.WriteString(sp.text)
	}
	return b.String()
}

func drawRender(spans []drawSpan) string {
	var b strings.Builder
	for _, sp := range spans {
		b.WriteString(sp.st.Render(sp.text))
	}
	return b.String()
}

// drawFitTrunc truncates s with "…" or pads it to exactly w cells.
func drawFitTrunc(s string, w int) string {
	if ansi.StringWidth(s) > w {
		s = ansi.Truncate(s, w, "…")
	}
	return drawFit(s, w)
}

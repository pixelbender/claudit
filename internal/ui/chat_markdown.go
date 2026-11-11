package ui

import (
	"regexp"
	"strings"
	"unicode"

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
	out, _ := drawMarkdownAs(src, w, false)
	return out
}

// drawBangHit is a "! command" of claude's as drawn: cells x0 to x1 of line y; a click there
// puts text in the prompt.
type drawBangHit struct {
	y, x0, x1 int
	text      string
}

// drawMarkdownBangs is drawMarkdown with where the "! command"s went: a fenced block whose first
// line that says something starts with "!", every row of it; an inline `! command`, the cells of
// each part of it. A "!" block's bar is pink, as the shell mode's "!", and so is an inline one.
// A table's cells have no hits, so their "! command"s are code as any.
func drawMarkdownBangs(src string, w int) (out []string, hits []drawBangHit) {
	return drawMarkdownAs(src, w, true)
}

// drawMarkdownAs is drawMarkdownBangs, or drawMarkdown when bangs is false: a place where a click
// puts nothing in the prompt shows a "! command" as code, not pink.
func drawMarkdownAs(src string, w int, bangs bool) (out []string, hits []drawBangHit) {
	out, hits, _ = drawMarkdownCode(src, w, bangs)
	return out, hits
}

// drawCodeBlock is a fenced block of code as drawn: lines from to to (past its last) of what
// drawMarkdown drew, a row of rows each; text is what it holds, as written, for a click to copy.
// A block of a "! command" has a click of its own, where it puts the command in the prompt.
type drawCodeBlock struct {
	from, to int
	text     string
	rows     []drawCodeRow
	bang     bool
}

// drawCodeRow is a row of a block of code: past its bar, a part of a line of the block. A copy of
// rows takes the line whole again: the wrap left out the blanks of lead before what the row shows,
// and a row that goes on with the line of the row above joins it.
type drawCodeRow struct {
	cont bool
	lead string
}

// drawMarkdownCode is drawMarkdownAs with where its blocks of code went.
func drawMarkdownCode(src string, w int, bangs bool) (out []string, hits []drawBangHit, code []drawCodeBlock) {
	w = max(1, w)
	flow := func(spans []drawSpan, first, hang string) {
		drawn, at := drawFlowBangs(spans, w, first, hang)
		for _, h := range at {
			h.y += len(out)
			hits = append(hits, h)
		}
		out = append(out, drawn...)
	}
	lines := strings.Split(strings.Trim(src, "\n"), "\n")
	for i := 0; i < len(lines); i++ {
		line := strings.TrimRight(lines[i], " \r")
		trimmed := strings.TrimSpace(line)
		start, from := i, len(out)
		var rows []drawCodeRow
		switch {
		case strings.HasPrefix(trimmed, "```"):
			lang := strings.TrimSpace(strings.TrimPrefix(trimmed, "```"))
			for i++; i < len(lines) && !strings.HasPrefix(strings.TrimSpace(lines[i]), "```"); i++ {
				if w-2 < 2 { // too narrow to wrap into anything readable
					out = append(out, styleRule.Render("│ ")+drawFitTrunc(ideview.Highlight(drawClean(lines[i]), lang), w-2))
					rows = append(rows, drawCodeRow{})
					continue
				}
				// Wrapped at words as the prose is, the bar going on beside each part: cut, a
				// long line lost its end (a draft to send, a command). The line is highlighted
				// whole and its colour goes on past the wrap: the rest of a comment or a string
				// is not code.
				parts := strings.Split(lipgloss.Wrap(ideview.Highlight(drawClean(lines[i]), lang), w-2, ""), "\n")
				for k, lead := range drawCodeLeads(drawClean(lines[i]), parts) {
					out = append(out, styleRule.Render("│ ")+drawFit(parts[k], w-2))
					rows = append(rows, drawCodeRow{cont: k > 0, lead: lead})
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
				flow(drawSpansAs(m[2], styleHeading, bangs), "", "")
			} else if m := drawItemRe.FindStringSubmatch(line); m != nil {
				indent := strings.Repeat(" ", min(len(m[1]), 8))
				bullet := "• "
				if m[2][0] >= '0' && m[2][0] <= '9' {
					bullet = m[2] + " "
				}
				first := indent + styleAccent.Render(bullet)
				flow(drawSpansAs(m[3], styleReply, bangs), first, indent+strings.Repeat(" ", ansi.StringWidth(bullet)))
			} else if rest, ok := strings.CutPrefix(trimmed, ">"); ok {
				flow(drawSpansAs(strings.TrimSpace(rest), styleDim.Italic(true), bangs), styleRule.Render("▏ "), styleRule.Render("▏ "))
			} else {
				flow(drawSpansAs(line, styleReply, bangs), "", "")
			}
		}
		if strings.HasPrefix(trimmed, "```") {
			block := lines[start+1 : min(i, len(lines))]
			text, ok := drawFenceBang(block)
			ok = ok && bangs
			if len(rows) > 0 {
				code = append(code, drawCodeBlock{from: from, to: len(out), text: drawFenceText(block), rows: rows, bang: ok})
			}
			if ok {
				for y := from; y < len(out); y++ {
					if rest, ok := strings.CutPrefix(out[y], styleRule.Render("│ ")); ok {
						out[y] = styleBang.Render("│ ") + rest
					}
					hits = append(hits, drawBangHit{y: y, x0: 0, x1: w, text: text})
				}
			}
		}
	}
	for len(out) > 0 && out[len(out)-1] == "" {
		out = out[:len(out)-1]
	}
	for i, l := range out {
		out[i] = drawTrunc(l, w) // a wide rune cannot be split when w is tiny
	}
	return out, hits, code
}

// drawCodeLeads are the blanks of line, as drawClean leaves it, that the wrap into parts left out
// before each part: where it broke the line, and its indent when it put nothing on the first row.
// A part that is not as written in line has none.
func drawCodeLeads(line string, parts []string) []string {
	leads, at := make([]string, len(parts)), 0
	for k, p := range parts {
		shown := strings.TrimRight(ansi.Strip(p), " ")
		j := strings.Index(line[at:], shown)
		if j < 0 {
			continue
		}
		if lead := line[at : at+j]; strings.TrimSpace(lead) == "" {
			leads[k] = lead
		}
		at += j + len(shown)
	}
	return leads
}

// drawFenceText is what a fenced block of code holds, for a copy: its lines as written, the blank
// ones around them trimmed; what the chat cannot show (escape sequences, other controls) goes,
// and the format characters that reorder or hide what is written (bidirectional controls,
// zero-width).
func drawFenceText(block []string) string {
	text := strings.Map(func(r rune) rune {
		if r != '\n' && r != '\t' && unicode.IsControl(r) {
			return -1
		}
		return drawNoFormat(r)
	}, ansi.Strip(strings.Join(block, "\n")))
	lines := strings.Split(text, "\n")
	for len(lines) > 0 && strings.TrimSpace(lines[0]) == "" {
		lines = lines[1:]
	}
	for len(lines) > 0 && strings.TrimSpace(lines[len(lines)-1]) == "" {
		lines = lines[:len(lines)-1]
	}
	return strings.Join(lines, "\n")
}

// drawFenceBang is the command a fenced block of code holds when its first line that says
// something starts with "!": the whole block as written, its blank lines around it trimmed and
// its new lines kept; what the chat cannot show (escape sequences, other controls) goes, and the
// format characters that reorder or hide what is written (bidirectional controls, zero-width).
// A line that starts with a shell's negation ("! grep -q x file") is taken for a command: it
// cannot be told from one.
func drawFenceBang(block []string) (string, bool) {
	text := strings.TrimSpace(drawFenceText(block))
	_, ok := bangCommand(text)
	return text, ok
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
	bang bool // an inline `! command`: a click on it puts its text in the prompt
}

// drawSpans splits one line of markdown into styled runs.
func drawSpans(s string, base lipgloss.Style) []drawSpan {
	return drawSpansAs(s, base, false)
}

// drawSpansAs is drawSpans with an inline `! command` marked, and pink, when bangs is set.
func drawSpansAs(s string, base lipgloss.Style, bangs bool) []drawSpan {
	s = drawOneLine(s)
	var spans []drawSpan
	var plain strings.Builder
	flush := func() {
		if plain.Len() > 0 {
			spans = append(spans, drawSpan{text: plain.String(), st: base})
			plain.Reset()
		}
	}
	for i := 0; i < len(s); {
		rest := s[i:]
		if j := drawClose(rest, "`", "`"); j > 0 {
			flush()
			code := rest[1:j]
			if bangs && drawInlineBang(code) {
				spans = append(spans, drawSpan{text: code, st: styleBang, bang: true})
			} else {
				spans = append(spans, drawSpan{text: code, st: styleCode})
			}
			i += j + 1
			continue
		}
		if j := drawClose(rest, "**", "**"); j > 0 {
			flush()
			spans = append(spans, drawSpansAs(rest[2:j], base.Foreground(theme.Strong), bangs)...)
			i += j + 2
			continue
		}
		if j := drawClose(rest, "*", "*"); j > 0 && rest[1] != ' ' {
			flush()
			spans = append(spans, drawSpansAs(rest[1:j], base.Italic(true), bangs)...)
			i += j + 1
			continue
		}
		if strings.HasPrefix(rest, "[") {
			if j := strings.Index(rest, "]("); j > 0 {
				if k := strings.IndexByte(rest[j+2:], ')'); k >= 0 {
					flush()
					spans = append(spans, drawSpansAs(rest[1:j], base.Underline(true), bangs)...)
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

// drawNoFormat drops the format characters from a text, for strings.Map.
func drawNoFormat(r rune) rune {
	if unicode.Is(unicode.Cf, r) {
		return -1
	}
	return r
}

// drawInlineBang reports whether inline code is a "! command": a "!", a blank, then the command.
// A "!" right before what follows is more often code ("!= nil", "!ok") than a command.
func drawInlineBang(code string) bool {
	rest, ok := strings.CutPrefix(code, "!")
	return ok && strings.HasPrefix(rest, " ") && strings.TrimSpace(rest) != ""
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
	lines, _ := drawFlowBangs(spans, w, first, hang)
	return lines
}

// drawFlowBangs is drawFlow with where the parts of each "! command" span went.
func drawFlowBangs(spans []drawSpan, w int, first, hang string) (lines []string, hits []drawBangHit) {
	if ansi.StringWidth(first) >= w || ansi.StringWidth(hang) >= w {
		first, hang = "", "" // no room for decoration
	}
	var line strings.Builder
	prefix := first
	used, bangSpan := ansi.StringWidth(prefix), -1
	line.WriteString(prefix)
	newLine := func() {
		lines = append(lines, line.String())
		line.Reset()
		prefix = hang
		line.WriteString(prefix)
		used = ansi.StringWidth(prefix)
	}
	for k, sp := range spans {
		// mark notes cells x0 to x1 of the line being drawn as a part of a "! command" span.
		mark := func(x0, x1 int) {
			if !sp.bang || x1 <= x0 {
				return
			}
			if n := len(hits); n > 0 && hits[n-1].y == len(lines) && hits[n-1].x1 == x0 && bangSpan == k {
				hits[n-1].x1 = x1
				return
			}
			hits, bangSpan = append(hits, drawBangHit{y: len(lines), x0: x0, x1: x1, text: strings.Map(drawNoFormat, sp.text)}), k
		}
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
				mark(used, used+ansi.StringWidth(head))
				word = word[len(head):]
				ww = ansi.StringWidth(word)
				newLine()
			}
			if word != "" {
				line.WriteString(sp.st.Render(word))
				mark(used, used+ww)
				used += ww
			}
		}
	}
	lines = append(lines, line.String())
	return lines, hits
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

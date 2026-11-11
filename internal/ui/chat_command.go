package ui

import (
	"image/color"
	"math"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"unicode"
	"unicode/utf8"

	"charm.land/lipgloss/v2"
	"github.com/charmbracelet/x/ansi"

	"github.com/pixelbender/claudit/internal/theme"
)

// The CLI's own replies are text laid out for a terminal, or markdown. They are drawn as Claude
// Code draws them: usage limits as meters, the context breakdown as a grid of cells, columns
// kept aligned, titles bold and usage notes dim.

var (
	drawMeterRe   = regexp.MustCompile(`^(\S.*?): (\d{1,3})% used(?: · (.*))?$`) // "Current session: 1% used · resets 3pm"
	drawShareRe   = regexp.MustCompile(`^(\d{1,3})%\s{2,}(.*)$`)                 // "69%  Start fresh when you switch tasks"
	drawSettingRe = regexp.MustCompile(`^([\w.-]+)=(.+)$`)                       // "theme=auto|dark|light"
	drawColumnsRe = regexp.MustCompile(`\S {2,}\S`)                              // a row of aligned columns
)

const drawMeterMax = 50 // cells of a usage meter's bar, as in Claude Code

// drawCommand renders a reply of the CLI into styled lines of at most w cells.
func drawCommand(text string, w int) []string {
	w = max(1, w)
	text = strings.Trim(drawClean(text), "\n")
	var out []string
	if drawIsMarkdown(text) {
		out = drawCommandMarkdown(text, w)
	} else {
		out = drawCommandText(text, w)
	}
	for i, l := range out {
		out[i] = drawTrunc(l, w)
	}
	return out
}

// drawIsMarkdown reports whether a reply is markdown rather than text laid out for a terminal:
// it has headings or tables.
func drawIsMarkdown(text string) bool {
	lines := strings.Split(text, "\n")
	for i, l := range lines {
		t := strings.TrimSpace(l)
		if drawHeadingRe.MatchString(t) || strings.HasPrefix(t, "|") && i+1 < len(lines) && drawTableSep.MatchString(lines[i+1]) {
			return true
		}
	}
	return false
}

// drawCommandText renders terminal text line by line, keeping its indentation and its columns.
func drawCommandText(text string, w int) []string {
	lines := strings.Split(text, "\n")
	indented := func(i int) bool { return i < len(lines) && strings.HasPrefix(lines[i], " ") }
	columns := func(i int) bool {
		return i >= 0 && i < len(lines) && drawColumnsRe.MatchString(strings.TrimSpace(lines[i]))
	}
	var out []string
	for i, line := range lines {
		line = strings.TrimRight(line, " ")
		body := strings.TrimLeft(line, " ")
		indent := line[:len(line)-len(body)]
		next := ""
		if i+1 < len(lines) {
			next = strings.TrimSpace(lines[i+1])
		}
		if m := drawMeterRe.FindStringSubmatch(line); m != nil {
			if len(out) > 0 && out[len(out)-1] != "" {
				out = append(out, "")
			}
			out = append(out, drawMeter(m[1], m[2], m[3], w)...)
			continue
		}
		switch {
		case body == "":
			out = append(out, "")
		case drawShareRe.MatchString(body):
			m := drawShareRe.FindStringSubmatch(body)
			p, _ := strconv.Atoi(m[1])
			pct := lipgloss.NewStyle().Foreground(drawLevel(float64(p) / 100)).Render(m[1] + "%")
			hang := indent + strings.Repeat(" ", len(m[1])+3)
			out = append(out, drawFlow(drawNote(m[2], styleFg), w, indent+pct+"  ", hang)...)
		case columns(i):
			st := styleFg
			if !columns(i-1) && columns(i+1) {
				st = styleDim // the header of the table below it
			}
			out = append(out, ansi.Truncate(indent+st.Render(body), w, "…"))
		case drawSettingRe.MatchString(body) && indent != "":
			m := drawSettingRe.FindStringSubmatch(body)
			out = append(out, drawFlow([]drawSpan{{text: m[1], st: styleFg}, {text: "=" + m[2], st: styleDim}}, w, indent, indent+"  ")...)
		case strings.HasPrefix(body, "- "):
			out = append(out, drawFlow(drawSpans(body[2:], styleFg), w, indent+styleAccent.Render("• "), indent+"  ")...)
		default:
			st := styleFg
			switch {
			case i == 0 && (strings.HasPrefix(body, "API Error") || strings.HasPrefix(body, "Error")):
				st = styleErr
			case strings.HasPrefix(body, "Usage:"):
				st = styleDim
			case indent == "" && (i == 0 && next == "" && len(lines) > 2 || indented(i+1) || strings.HasSuffix(body, ":") || strings.HasSuffix(body, "?")):
				st = styleStrong // a title, or the heading of what follows
			}
			out = append(out, drawFlow(drawSpans(body, st), w, indent, indent)...)
		}
	}
	return out
}

// drawNote styles "what · detail": the detail dimmed.
func drawNote(s string, base lipgloss.Style) []drawSpan {
	head, tail, ok := strings.Cut(s, " · ")
	spans := drawSpans(head, base)
	if ok {
		spans = append(spans, drawSpan{text: " · " + drawOneLine(tail), st: styleDim})
	}
	return spans
}

// drawMeter draws a usage limit as Claude Code does: its name; a bar with how much is used;
// when it resets.
func drawMeter(name, pct, resets string, w int) []string {
	p, _ := strconv.Atoi(pct)
	p = min(p, 100)
	label := " " + strconv.Itoa(p) + "% used"
	out := []string{styleStrong.Render(name)}
	if bw := min(drawMeterMax, w-ansi.StringWidth(label)); bw >= 4 {
		out = append(out, drawBar(float64(p)/100, bw)+styleFg.Render(label))
	} else {
		out = append(out, styleFg.Render(label[1:]))
	}
	if r, n := utf8.DecodeRuneInString(resets); resets != "" {
		out = append(out, drawFlow([]drawSpan{{text: string(unicode.ToUpper(r)) + resets[n:], st: styleDim}}, w, "", "")...)
	}
	return out
}

// drawBar is a bar of w cells filled to f in eighths of a cell; the fill turns from green to red
// along the bar, the rest is a dim track.
func drawBar(f float64, w int) string {
	eighths := []rune("▏▎▍▌▋▊▉")
	units := int(math.Round(f * float64(w*8)))
	if f > 0 {
		units = max(units, 1)
	}
	var b strings.Builder
	for x := range w {
		st := lipgloss.NewStyle().Foreground(drawLevel(float64(x) / float64(max(w-1, 1))))
		switch n := units - x*8; {
		case n >= 8:
			b.WriteString(st.Render("█"))
		case n > 0:
			b.WriteString(st.Render(string(eighths[n-1])))
		default:
			b.WriteString(styleRule.Render("░"))
		}
	}
	return b.String()
}

// drawLevel is the colour of a share f of a limit: green, then yellow, red at the top.
func drawLevel(f float64) color.Color { return theme.Gradient(f, theme.OK, theme.Warn, theme.Err) }

// drawPad right-aligns s in w cells.
func drawPad(s string, w int) string { return strings.Repeat(" ", max(0, w-ansi.StringWidth(s))) + s }

// drawCommandMarkdown renders markdown, except the context breakdown, a table of categories and
// their share, which becomes Claude Code's grid of cells.
func drawCommandMarkdown(text string, w int) []string {
	lines := strings.Split(text, "\n")
	for i := 0; i+1 < len(lines); i++ {
		if !strings.HasPrefix(strings.TrimSpace(lines[i]), "|") || !drawTableSep.MatchString(lines[i+1]) {
			continue
		}
		j := i + 2
		var rows [][]string
		for ; j < len(lines) && strings.HasPrefix(strings.TrimSpace(lines[j]), "|"); j++ {
			rows = append(rows, drawCells(strings.TrimSpace(lines[j])))
		}
		grid := drawContextGrid(drawCells(strings.TrimSpace(lines[i])), rows, w)
		if grid == nil {
			i = j - 1
			continue
		}
		out := drawMarkdown(strings.Join(lines[:i], "\n"), w)
		if len(out) > 0 {
			out = append(out, "")
		}
		out = append(out, grid...)
		if rest := drawCommandMarkdown(strings.Join(lines[j:], "\n"), w); len(rest) > 0 {
			out = append(append(out, ""), rest...)
		}
		return out
	}
	return drawMarkdown(text, w)
}

// drawContextGrid draws the context window as a grid of cells, each category filling its share
// in its colour — free space and the autocompact buffer last — with a legend beside it. It is
// nil unless head has the columns Category and Percentage.
func drawContextGrid(head []string, rows [][]string, w int) []string {
	col := func(name string) int {
		for i, h := range head {
			if strings.EqualFold(drawPlain(h), name) {
				return i
			}
		}
		return -1
	}
	cName, cTokens, cPct := col("Category"), col("Tokens"), col("Percentage")
	if cName < 0 || cPct < 0 {
		return nil
	}
	cell := func(r []string, c int) string {
		if c < 0 || c >= len(r) {
			return ""
		}
		return drawPlain(r[c])
	}
	cols := 10
	if w >= 90 {
		cols = 20
	}
	total := cols * 10
	var used, buffer []string
	var legend [][3]string // glyph, name, tokens + share
	nameW, tokW, pctW := 0, 0, 0
	for _, r := range rows {
		nameW, tokW = max(nameW, ansi.StringWidth(cell(r, cName))), max(tokW, ansi.StringWidth(cell(r, cTokens)))
		pctW = max(pctW, ansi.StringWidth(cell(r, cPct)))
	}
	slot := 0
	for _, r := range rows {
		name := cell(r, cName)
		pct, _ := strconv.ParseFloat(strings.TrimSuffix(cell(r, cPct), "%"), 64)
		exact := pct / 100 * float64(total)
		var glyph string
		switch lower := strings.ToLower(name); {
		case lower == "free space":
			glyph = styleRule.Render("⛶")
		case strings.Contains(lower, "buffer"):
			glyph = styleDim.Render("⛝")
			buffer = append(buffer, slices.Repeat([]string{glyph}, int(math.Round(exact)))...)
		default:
			st := lipgloss.NewStyle().Foreground(theme.Agents[slot%len(theme.Agents)])
			slot++
			glyph = st.Render("⛁")
			full := int(exact)
			used = append(used, slices.Repeat([]string{glyph}, full)...)
			if exact-float64(full) >= 0.05 || full == 0 && pct > 0 {
				used = append(used, st.Render("⛀"))
			}
		}
		info := drawPad(cell(r, cTokens), tokW) + styleDim.Render("  "+drawPad(cell(r, cPct), pctW))
		legend = append(legend, [3]string{glyph, drawFit(name, nameW), info})
	}
	cells := used[:min(len(used), total)]
	buffer = buffer[:min(len(buffer), total-len(cells))]
	cells = append(cells, slices.Repeat([]string{styleRule.Render("⛶")}, total-len(cells)-len(buffer))...)
	cells = append(cells, buffer...)

	grid := make([]string, 0, 10)
	for y := range 10 {
		grid = append(grid, strings.Join(cells[y*cols:(y+1)*cols], " "))
	}
	keys := make([]string, len(legend))
	for i, l := range legend {
		keys[i] = l[0] + " " + styleFg.Render(l[1]) + "  " + styleFg.Render(l[2])
	}
	gw := cols*2 - 1
	if w < gw+3+nameW+tokW+pctW+6 { // no room beside the grid: the legend goes below it
		return append(append(grid, ""), keys...)
	}
	out := make([]string, max(len(grid), len(keys)))
	for y := range out {
		g := strings.Repeat(" ", gw)
		if y < len(grid) {
			g = grid[y]
		}
		if y < len(keys) {
			g += "   " + keys[y]
		}
		out[y] = g
	}
	return out
}

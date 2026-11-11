package ideview

import (
	"cmp"
	"fmt"
	"image/color"
	"maps"
	"math"
	"slices"
	"strconv"
	"strings"

	"charm.land/lipgloss/v2"

	"github.com/pixelbender/claudit/internal/ide"
	"github.com/pixelbender/claudit/internal/theme"
)

func editorPane(st *ide.State, cam *ide.Agent, o Options, w, h int) []string {
	iw, ih := inner(w, h)
	tab := shownTab(st, cam)
	var body []string
	head := ""
	if tab == nil {
		body = splash(iw, ih)
	} else {
		if v := structured(st, cam, o, iw, ih); v != nil {
			body = v.body(iw, ih)
		} else {
			body = editorBody(st, cam, tab, o, iw, ih)
		}
		head = " " + title(st, cam, func(room int) string { return shortPath(tab.Path, st.Root, room) }, iw-3) + " "
	}
	return frame(w, h, theme.Border, head, body)
}

// shownTab is the file the editor shows for agent a: its own, else the last opened; nil for none.
func shownTab(st *ide.State, a *ide.Agent) *ide.Tab {
	tab := st.FindTab(a.File)
	if tab == nil && len(st.Tabs) > 0 {
		tab = st.Tabs[len(st.Tabs)-1]
	}
	return tab
}

// editorText is what the editor shows of tab to agent a in iw cells: its lines (an empty one when
// it has none), a's caret on them, and the width of the text beside the line numbers.
func editorText(tab *ide.Tab, a *ide.Agent, iw int) (lines []string, cur ide.Pos, tw int) {
	lines = tab.Buf.Lines
	if len(lines) == 0 {
		lines = []string{""}
	}
	if c := tab.Buf.Carets[a.Key]; c != nil {
		cur = c.Pos
	}
	cur.Line = min(max(cur.Line, 0), len(lines)-1)
	cur.Col = max(cur.Col, 0)
	return lines, cur, max(0, iw-len(strconv.Itoa(len(lines)))-2) // after the " 12 " gutter
}

// editorTop is where the editor following the caret at cur starts in ih rows: its first line, and
// how many rows of that line it skips when lines wrap at tw cells.
func editorTop(lines []string, cur ide.Pos, tw, ih int, wrap bool) (top, skip int) {
	if wrap {
		return wrapTop(lines, cur, tw, ih)
	}
	return scrollTop(cur.Line, len(lines), ih), 0
}

// editorRange is the followed position and the last scroll position, in screen rows.
func editorRange(lines []string, cur ide.Pos, tw, ih int, wrap bool) (int, int) {
	top, skip := editorTop(lines, cur, tw, ih, wrap)
	if !wrap {
		return top, max(0, len(lines)-ih)
	}
	follow, total := skip, 0
	for i, line := range lines {
		n := len(segments(line, tw))
		total += n
		if i < top {
			follow += n
		}
	}
	last := max(0, total-ih)
	return min(follow, last), last
}

// style is how one editor cell looks.
type style struct {
	kind   kind
	fg     color.Color // replaces the syntax colour (carets)
	bg     color.Color // selection or current line; nil for none
	cursor bool        // a caret: the spin frame, in fg
	block  bool        // a caret: the cell's own text in fg on bg
}

func (k style) render(s string) string {
	st := lipgloss.NewStyle().Foreground(kindColor[k.kind])
	if k.fg != nil {
		st = st.Foreground(k.fg)
	}
	if k.bg != nil {
		st = st.Background(k.bg)
	}
	return st.Render(s)
}

// mark is another agent's caret in the shown buffer.
type mark struct {
	pos      ide.Pos
	from, to ide.Pos // its selection; empty when from == to
	color    color.Color
	spin     bool // its agent works: the caret shows
	block    bool // its agent types: the caret is a block
}

// lineCaret is a caret on the line being drawn.
type lineCaret struct {
	col    int
	color  color.Color
	own    bool // the camera's caret
	spin   bool // its agent works: the spin frame shows
	block  bool // its agent types: a solid block shows, over the spin frame
	placed bool
}

func editorBody(st *ide.State, cam *ide.Agent, tab *ide.Tab, o Options, iw, ih int) []string {
	lines, cur, tw := editorText(tab, cam, iw)
	own := tab.Buf.Carets[cam.Key]
	focused := cam.Focus == ide.Editor && own != nil
	var from, to ide.Pos
	if own != nil && own.Sel != nil {
		from, to = order(own.Sel.From, own.Sel.To)
	}

	var others []mark
	for _, key := range slices.Sorted(maps.Keys(tab.Buf.Carets)) {
		if key == cam.Key {
			continue
		}
		c, a := tab.Buf.Carets[key], findAgent(st, key)
		if a != nil && a.Kind != ide.Main && (a.Status == ide.Done || a.Status == ide.Failed) && a.Activity == "" {
			continue // it has finished and played everything: no longer at work here
		}
		m := mark{pos: c.Pos, color: agentColor(a), spin: a != nil && working(a, o), block: a != nil && typing(a, o)}
		if c.Sel != nil {
			m.from, m.to = order(c.Sel.From, c.Sel.To)
		}
		others = append(others, m)
	}

	// selBg is the background of the cell at p when a selection covers it, the camera's or another
	// agent's: red, as a diff's removed lines, for an agent selects only what it deletes; nil
	// otherwise.
	selBg := func(p ide.Pos) color.Color {
		if !less(p, from) && less(p, to) {
			return theme.RemovedBg
		}
		for _, m := range others {
			if !less(p, m.from) && less(p, m.to) {
				return theme.RemovedBg
			}
		}
		return nil
	}

	digits := len(strconv.Itoa(len(lines)))

	// Without wrapping, shift horizontally when the camera's caret column is off-screen.
	shift, x := 0, 0
	cs, _ := layout(lines[cur.Line], 0, cur.Col)
	for _, c := range cs {
		if c.r1 > cur.Col && c.w > 0 {
			break
		}
		x += c.w
	}
	if x >= tw && !o.Wrap {
		shift = x - tw + 1 + tw/4
	}

	// A caret is a solid block while its agent types, selects or deletes text, a spin frame while
	// it works otherwise, else none.
	spin, block, frame := focused && working(cam, o), focused && typing(cam, o), spinFrame(o.Now)
	// The newest glow of each line.
	glows := map[int]ide.Glow{}
	for _, g := range tab.Buf.Glow {
		if old, ok := glows[g.Line]; !ok || g.At.After(old.At) {
			glows[g.Line] = g
		}
	}

	lang := langOf(tab.Path)
	if tab.Hex {
		lang = nil
	}
	// rows draws line ln: one row, or one per piece of it when wrapping.
	rows := func(ln int) []string {
		hl := focused && ln == cur.Line
		var lcs []lineCaret
		if hl && (spin || block) {
			lcs = append(lcs, lineCaret{col: cur.Col, color: agentColor(cam), own: true, spin: spin, block: block})
		}
		for _, m := range others {
			if m.pos.Line == ln {
				lcs = append(lcs, lineCaret{col: m.pos.Col, color: m.color, spin: m.spin, block: m.block})
			}
		}
		num := dimStyle
		if hl {
			num = fgStyle
		}
		var bg color.Color
		if hl {
			bg = theme.LineBg
		}
		if g, ok := glows[ln]; ok {
			if lit := glow(g, o.Now); lit != nil {
				bg = lit
			}
		}
		gutter := num.Render(fmt.Sprintf(" %*d ", digits, ln+1))
		if !o.Wrap {
			return []string{gutter + drawLine(lines[ln], ln, lang, lcs, bg, selBg, shift, tw, frame)}
		}
		segs := segments(lines[ln], tw)
		out := make([]string, len(segs))
		for i, sg := range segs {
			if i > 0 {
				gutter = strings.Repeat(" ", digits+2)
			}
			w := sg[1] - sg[0]
			if i == len(segs)-1 { // the last piece has room after the text for carets
				w = tw
			}
			row := drawLine(lines[ln], ln, lang, slices.Clone(lcs), bg, selBg, sg[0], w, frame)
			out[i] = gutter + row + strings.Repeat(" ", tw-w)
		}
		return out
	}

	follow, last := editorRange(lines, cur, tw, ih, o.Wrap)
	top, skip := scrolled(follow, o.Scroll[fileKey(tab.Path)], last), 0
	if o.Wrap {
		skip, top = top, 0
		for top < len(lines)-1 {
			n := len(segments(lines[top], tw))
			if skip < n {
				break
			}
			skip -= n
			top++
		}
	}
	var out []string
	for ln := top; ln < len(lines) && len(out) < ih+skip; ln++ {
		out = append(out, rows(ln)...)
	}
	out = out[min(skip, len(out)):]
	return append(out, make([]string, max(0, ih-len(out)))...)[:ih]
}

// segments cuts a line into the cell ranges [x0, x1) of its rows when it wraps at tw cells: after
// the last space that fits, else at tw.
func segments(line string, tw int) [][2]int {
	cs, _ := layout(line, math.MaxInt, 0)
	var segs [][2]int
	start, x, space := 0, 0, -1 // the row's first cell, the next cell, the cell after the row's last space
	for _, c := range cs {
		for x+c.w-start > tw && x > start {
			end := x
			if space > start {
				end = space
			}
			segs = append(segs, [2]int{start, end})
			start, space = end, -1
		}
		x += c.w
		if c.s == " " {
			space = x
		}
	}
	return append(segs, [2]int{start, x})
}

// wrapTop is the first line to draw when lines wrap at tw cells, and how many of its rows to skip:
// the caret's row sits in the upper third of ih rows, and the window does not run past the end.
func wrapTop(lines []string, cur ide.Pos, tw, ih int) (top, skip int) {
	rowsOf := func(ln int) int { return len(segments(lines[ln], tw)) }
	// The caret's row within its line: the last that starts at or before its cell.
	x, at := 0, 0
	cs, _ := layout(lines[cur.Line], math.MaxInt, 0)
	for _, c := range cs {
		if c.r0 >= cur.Col {
			break
		}
		x += c.w
	}
	for i, sg := range segments(lines[cur.Line], tw) {
		if x >= sg[0] {
			at = i
		}
	}
	if at > ih/3 { // a line longer than the window: start inside it
		return cur.Line, at - ih/3
	}
	top, above := cur.Line, at
	for top > 0 && above+rowsOf(top-1) <= ih/3 {
		top--
		above += rowsOf(top)
	}
	total := 0
	for ln := top; ln < len(lines) && total < ih; ln++ {
		total += rowsOf(ln)
	}
	for top > 0 && total+rowsOf(top-1) <= ih {
		top--
		total += rowsOf(top)
	}
	return top, 0
}

// drawLine draws the cells of line ln that fall in [shift, shift+tw) as exactly tw cells, its text
// on lineBg (nil for none), with the carets on it. Under a caret whose agent types, the cell is a
// block of its colour; else the spin frame replaces the character under a caret whose agent works.
func drawLine(line string, ln int, lang *language, lcs []lineCaret, lineBg color.Color, selBg func(ide.Pos) color.Color, shift, tw int, frame string) string {
	minRunes := -1
	if len(lcs) > 0 && lcs[0].own {
		minRunes = lcs[0].col
	}
	cs, n := layout(line, shift+tw, minRunes)
	kinds := highlight([]rune(line[:n]), lang)

	var b, run strings.Builder
	var cur style
	put := func(k style, s string) {
		if k != cur && run.Len() > 0 {
			b.WriteString(cur.render(run.String()))
			run.Reset()
		}
		cur = k
		run.WriteString(s)
	}
	// place puts every caret not placed yet whose column is before end on k; the first that types
	// or spins shows.
	place := func(k style, end int) style {
		for i := range lcs {
			if lcs[i].placed || lcs[i].col >= end {
				continue
			}
			lcs[i].placed = true
			switch {
			case k.cursor || k.block:
			case lcs[i].block:
				k.block, k.fg, k.bg = true, theme.Bg, lcs[i].color
			case lcs[i].spin:
				k.cursor, k.fg = true, lcs[i].color
			}
		}
		return k
	}

	x, eol := 0, 0
	for _, c := range cs {
		k := style{kind: kinds[c.r0], bg: cmp.Or(selBg(ide.Pos{Line: ln, Col: c.r0}), lineBg)}
		if c.w > 0 {
			k = place(k, c.r1)
		}
		switch {
		case x >= shift && x+c.w <= shift+tw:
			s := c.s
			if k.cursor {
				s = frame + strings.Repeat(" ", c.w-1)
			}
			put(k, s)
		case x < shift+tw && x+c.w > shift: // a wide cell cut by an edge
			put(k, strings.Repeat(" ", min(x+c.w, shift+tw)-max(x, shift)))
		}
		x, eol = x+c.w, c.r1
	}
	if n == len(line) {
		// Past the last character: carets at the end of the line, or a selected line break.
		sel := selBg(ide.Pos{Line: ln, Col: eol})
		k := place(style{bg: sel}, math.MaxInt) // lineBg ends with the text
		if (sel != nil || k.cursor || k.block) && x >= shift && x < shift+tw {
			if k.cursor && x+1 < shift+tw { // a space between the text and the caret, if both fit
				put(style{bg: k.bg}, " ")
				x++
			}
			s := " "
			if k.cursor {
				s = frame
			}
			put(k, s)
			x++
		}
	}
	put(style{}, strings.Repeat(" ", max(0, shift+tw-max(x, shift))))
	if run.Len() > 0 {
		b.WriteString(cur.render(run.String()))
	}
	return b.String()
}

// order returns a and b in text order.
func order(a, b ide.Pos) (ide.Pos, ide.Pos) {
	if less(b, a) {
		return b, a
	}
	return a, b
}

func less(a, b ide.Pos) bool { return a.Line < b.Line || a.Line == b.Line && a.Col < b.Col }
